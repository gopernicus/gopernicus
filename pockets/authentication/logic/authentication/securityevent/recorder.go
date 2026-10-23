package securityevent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// writeTimeout bounds one audit write. The write is detached from the
	// request's cancellation, so this is what stops a hung store from holding
	// the flow.
	writeTimeout = 2 * time.Second

	// summaryInterval is the least time between two audit_write_failed lines.
	summaryInterval = time.Minute

	// writeFailedMessage is the stable message of every write-failure log line.
	writeFailedMessage = "audit_write_failed"
)

// RecorderOption configures NewRecorder. A nil option is ignored.
type RecorderOption func(*Recorder)

// WriteObserver is told about every failed audit write, synchronously and
// with coarse fields only. Its context carries the request's values but never
// its cancellation. It must return promptly and tolerate concurrent calls.
type WriteObserver func(context.Context, WriteFailure)

// Input is the content a sensitive operation hands Recorder.Record. The
// recorder fills the ID, the timestamp, the correlation ids and the client
// attribution (IP/UA), so the caller never touches those and each has exactly
// one source.
//
// Content hygiene: Details carries identifiers and key PREFIXES only. Never put
// a raw API key, JWT, session token, password, or OAuth token in Details,
// Actor or UserID.
type Input struct {
	UserID  string
	Actor   Principal
	Type    string
	Status  string
	Details map[string]any
}

// WriteFailure describes one failed audit write: the event's type and status
// and ErrorKind's label for the error — never the event body or the error text.
type WriteFailure struct {
	EventType   string
	EventStatus string
	ErrorKind   string
}

// Recorder appends security events to a repository. It is the single
// recording site of the authentication pocket: the service, the external
// authenticator, the OAuth2 server and the invitation service hold the same
// Recorder when the pocket root builds them, so WriteFailures counts every
// lost row. A bridge or host that records into the rail accepts the narrow
// interface{ Record(context.Context, Input) } and is handed that Recorder
// rather than building another.
//
// Recording never fails the caller's flow: a write that fails is lost, logged,
// counted and reported to the WriteObserver.
type Recorder struct {
	repo       SecurityEventRepository
	ids        sdk.IDGenerator
	now        func() time.Time
	log        *slog.Logger
	clientInfo func(context.Context) (ip, ua string)
	observer   WriteObserver
	timeout    time.Duration

	failures atomic.Uint64

	mu         sync.Mutex
	lastLine   time.Time
	suppressed uint64
}

// WithClientInfoReader sets how Record attributes a row to a client: read
// returns the request's IP address and User-Agent. Without it IPAddress and
// UserAgent are recorded empty.
func WithClientInfoReader(read func(context.Context) (ip, ua string)) RecorderOption {
	return func(r *Recorder) { r.clientInfo = read }
}

// WithWriteObserver sets the observer told about every failed write.
func WithWriteObserver(observer WriteObserver) RecorderOption {
	return func(r *Recorder) { r.observer = observer }
}

// NewRecorder returns a Recorder appending to repo, minting event ids from ids
// and timestamps from now. A nil now selects time.Now and a nil log
// slog.Default(). A nil repo yields a Recorder whose Record does nothing — the
// host keeps no audit trail (ratified AV9).
func NewRecorder(repo SecurityEventRepository, ids sdk.IDGenerator, now func() time.Time, log *slog.Logger, opts ...RecorderOption) *Recorder {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	r := &Recorder{repo: repo, ids: ids, now: now, log: log, timeout: writeTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// Record appends one event synchronously. A nil Recorder, or one without a
// repository, does nothing.
//
// It stamps the id, the time, the client attribution and the request and trace
// ids from ctx (sdk.RequestIDFromContext, sdk.TraceIDFromContext); an id longer
// than MaxCorrelationIDLen bytes or carrying a byte outside printable ASCII is
// recorded blank, never truncated. The write runs detached from ctx's
// cancellation — a client that disconnects cannot cancel its own failed-login
// row — and is bounded to two seconds.
//
// A failed write never reaches the caller. Each one increments WriteFailures and
// is reported to the WriteObserver. The first failure logs one WARN
// "audit_write_failed" with event_type, status and error_kind; while failures
// continue, at most one line per minute follows, "audit_write_failed" with count,
// the failures since the previous line. A failure after a quiet minute starts
// over with the detailed line.
func (r *Recorder) Record(ctx context.Context, in Input) {
	if r == nil || r.repo == nil {
		return
	}
	evt := New(r.ids, in.Type, in.Status, r.now())
	evt.UserID = in.UserID
	evt.Actor = in.Actor
	evt.Details = in.Details
	if id, ok := sdk.RequestIDFromContext(ctx); ok {
		evt.RequestID = correlationID(id)
	}
	if id, ok := sdk.TraceIDFromContext(ctx); ok {
		evt.TraceID = correlationID(id)
	}
	if r.clientInfo != nil {
		evt.IPAddress, evt.UserAgent = r.clientInfo(ctx)
	}
	detached := context.WithoutCancel(ctx)
	writeCtx, cancel := context.WithTimeout(detached, r.timeout)
	defer cancel()
	if _, err := r.repo.Create(writeCtx, evt); err != nil {
		r.failed(detached, WriteFailure{EventType: in.Type, EventStatus: in.Status, ErrorKind: ErrorKind(err)})
	}
}

// WriteFailures returns how many writes have failed since construction; zero
// for a nil Recorder.
func (r *Recorder) WriteFailures() uint64 {
	if r == nil {
		return 0
	}
	return r.failures.Load()
}

// Repository returns the repository r appends to; nil for a nil Recorder.
func (r *Recorder) Repository() SecurityEventRepository {
	if r == nil {
		return nil
	}
	return r.repo
}

// failed counts one failed write, logs it under the once-then-per-minute rule
// and reports it to the observer.
func (r *Recorder) failed(ctx context.Context, failure WriteFailure) {
	r.failures.Add(1)
	now := r.now()
	var detailed bool
	var count uint64
	r.mu.Lock()
	switch quiet := r.lastLine.IsZero() || now.Sub(r.lastLine) >= summaryInterval; {
	case quiet && r.suppressed == 0:
		detailed, r.lastLine = true, now
	case quiet:
		count, r.lastLine, r.suppressed = r.suppressed+1, now, 0
	default:
		r.suppressed++
	}
	r.mu.Unlock()
	switch {
	case detailed:
		r.log.Warn(writeFailedMessage,
			"event_type", failure.EventType,
			"status", failure.EventStatus,
			"error_kind", failure.ErrorKind,
		)
	case count > 0:
		r.log.Warn(writeFailedMessage, "count", count)
	}
	if r.observer != nil {
		r.observer(ctx, failure)
	}
}

// correlationID returns id when it is at most MaxCorrelationIDLen bytes of
// printable ASCII, and "" otherwise.
func correlationID(id string) string {
	if len(id) > MaxCorrelationIDLen {
		return ""
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] > 0x7e {
			return ""
		}
	}
	return id
}

// ErrorKind reduces err to a coarse, secret-free sentinel label for a log line
// (design §5.1 WI3 — the log carries event_type, status, and error kind only,
// never the event body). Unrecognized errors report "unknown" rather than their
// message, so no store detail leaks into logs.
func ErrorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, sdk.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, sdk.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, sdk.ErrInvalidReference):
		return "invalid_reference"
	case errors.Is(err, sdk.ErrNotFound):
		return "not_found"
	case errors.Is(err, sdk.ErrConflict):
		return "conflict"
	default:
		return "unknown"
	}
}
