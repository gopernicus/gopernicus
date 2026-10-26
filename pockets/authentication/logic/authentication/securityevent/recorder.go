package securityevent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"maps"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
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

	// rowsSuppressedMessage is the stable message of every over-budget log line.
	rowsSuppressedMessage = "audit_rows_suppressed"

	// failureRowKeyPrefix scopes the failure-row budget's limiter keys; the
	// client IP's digest follows it.
	failureRowKeyPrefix = "securityevent:failure-row:ip:"

	// unattributedMessage is the stable message of the one missing-attribution
	// WARN.
	unattributedMessage = "security event recorded without client attribution; mount ClientInfoMiddleware"
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
//
// Budgeted marks a failure row a caller can drive without an authenticated
// session — every oauth2_grant_failed row and the OAuth callback's
// unknown_provider row. With WithFailureRowBudget wired, Record writes it only
// while the client IP's budget admits it.
type Input struct {
	UserID   string
	Actor    Principal
	Type     string
	Status   string
	Details  map[string]any
	Budgeted bool
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
	repo        SecurityEventRepository
	ids         sdk.IDGenerator
	now         func() time.Time
	log         *slog.Logger
	clientInfo  func(context.Context) (ip, ua string)
	observer    WriteObserver
	timeout     time.Duration
	budget      ratelimiter.Allower
	budgetLimit ratelimiter.Limit

	warnUnattributed bool
	unattributedOnce sync.Once

	failures       atomic.Uint64
	rowsSuppressed atomic.Uint64

	mu             sync.Mutex
	failureLog     logThrottle
	suppressionLog logThrottle
}

// logThrottle is the once-then-per-minute rule for one log message: the first
// occurrence after a quiet minute logs a detailed line, later ones inside the
// minute are only counted, and the first one a minute on logs the count.
type logThrottle struct {
	last    time.Time
	pending uint64
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

// WithFailureRowBudget budgets Budgeted rows per client IP: Record asks limiter
// to admit one row under a key derived from the attributed IP (a blank IP
// shares one key) at limit. Over budget the row is not written, SuppressedRows
// counts it and an "audit_rows_suppressed" WARN logs at most once a minute. A
// limiter error writes the row: a limiter fault costs a row, never loses one.
// A nil limiter leaves every row unbudgeted.
func WithFailureRowBudget(limiter ratelimiter.Allower, limit ratelimiter.Limit) RecorderOption {
	return func(r *Recorder) { r.budget, r.budgetLimit = limiter, limit }
}

// WithMissingAttributionWarning makes Record log one WARN per Recorder — once
// per process for the pocket's one recorder — the first time it records a row
// whose context carries a request id but whose client-info reader returned no
// IP: a request on a host route ClientInfoMiddleware did not stamp. A stamped
// request always carries at least the peer address, and a row without a
// request id (background work) never warns. The pocket root enables it in
// production.
func WithMissingAttributionWarning() RecorderOption {
	return func(r *Recorder) { r.warnUnattributed = true }
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
// ids from ctx (sdk.RequestIDFromContext, sdk.TraceIDFromContext), and passes
// the ids, the IP, the User-Agent and every top-level string value of Details
// through BoundValue: a value its rule refuses is recorded blank, never
// refused. The caller's Details map is never written. A Budgeted row over the
// WithFailureRowBudget budget is not written. The write runs detached from
// ctx's cancellation — a client that disconnects cannot cancel its own
// failed-login row — and is bounded to two seconds.
//
// A failed write never reaches the caller. Each one increments WriteFailures and
// is reported to the WriteObserver. The first failure logs one WARN
// "audit_write_failed" with event_type, status and error_kind; while failures
// continue, at most one line per minute follows, "audit_write_failed" with count,
// the failures since the previous line. A failure after a quiet minute starts
// over with the detailed line.
func (r *Recorder) Record(ctx context.Context, in Input) {
	_ = r.RecordReporting(ctx, in)
}

// RecordReporting is Record for a caller whose row is evidence it must act on:
// the write runs, and a failure is counted, logged and observed exactly as
// Record does, then returned. A row Record would not write (no repository, a
// refused budget) returns nil.
func (r *Recorder) RecordReporting(ctx context.Context, in Input) error {
	evt, ok := r.Prepare(ctx, in)
	if !ok {
		return nil
	}
	detached := context.WithoutCancel(ctx)
	writeCtx, cancel := context.WithTimeout(detached, r.timeout)
	defer cancel()
	_, err := r.repo.Create(writeCtx, evt)
	if err != nil {
		r.failed(detached, WriteFailure{EventType: in.Type, EventStatus: in.Status, ErrorKind: ErrorKind(err)})
	}
	return err
}

// Prepare builds the row Record would write for in — the id, the time, the
// correlation ids, the client attribution and every BoundValue rule applied —
// without writing it. ok is false when Record would write nothing: a nil
// Recorder, one without a repository, or a Budgeted row the failure-row budget
// refuses (the refusal is counted as Record counts it). A store that must
// append the row inside its own transaction — the user erasure — receives the
// prepared row and writes it there, so the evidence commits with the change.
func (r *Recorder) Prepare(ctx context.Context, in Input) (SecurityEvent, bool) {
	if r == nil || r.repo == nil {
		return SecurityEvent{}, false
	}
	var ip, ua string
	var stamped bool
	if r.clientInfo != nil {
		ip, ua = r.clientInfo(ctx)
		stamped = ip != ""
		ip, ua = BoundValue(FieldIPAddress, ip), BoundValue(FieldUserAgent, ua)
	}
	if in.Budgeted && !r.withinBudget(context.WithoutCancel(ctx), ip, in.Type) {
		return SecurityEvent{}, false
	}
	evt := New(r.ids, in.Type, in.Status, r.now())
	evt.UserID = in.UserID
	evt.Actor = in.Actor
	evt.Details = boundDetails(in.Details)
	evt.IPAddress, evt.UserAgent = ip, ua
	if id, ok := sdk.RequestIDFromContext(ctx); ok {
		evt.RequestID = BoundValue(FieldRequestID, id)
		if !stamped && r.warnUnattributed {
			r.unattributedOnce.Do(func() { r.log.Warn(unattributedMessage, "event_type", in.Type) })
		}
	}
	if id, ok := sdk.TraceIDFromContext(ctx); ok {
		evt.TraceID = BoundValue(FieldTraceID, id)
	}
	return evt, true
}

// WriteFailures returns how many writes have failed since construction; zero
// for a nil Recorder.
func (r *Recorder) WriteFailures() uint64 {
	if r == nil {
		return 0
	}
	return r.failures.Load()
}

// SuppressedRows returns how many Budgeted rows the failure-row budget has
// refused since construction; zero for a nil Recorder.
func (r *Recorder) SuppressedRows() uint64 {
	if r == nil {
		return 0
	}
	return r.rowsSuppressed.Load()
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
	r.mu.Lock()
	detailed, count := r.failureLog.next(now)
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

// withinBudget reports whether the failure-row budget admits one more row for
// ip, counting and logging a refusal under the once-then-per-minute rule. The
// limiter call is detached from the request and bounded like a write.
func (r *Recorder) withinBudget(ctx context.Context, ip, eventType string) bool {
	if r.budget == nil {
		return true
	}
	digest := sha256.Sum256([]byte(ip))
	limitCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	result, err := r.budget.Allow(limitCtx, failureRowKeyPrefix+hex.EncodeToString(digest[:]), r.budgetLimit)
	if err != nil || result.Allowed {
		return true
	}
	r.rowsSuppressed.Add(1)
	now := r.now()
	r.mu.Lock()
	detailed, count := r.suppressionLog.next(now)
	r.mu.Unlock()
	switch {
	case detailed:
		r.log.Warn(rowsSuppressedMessage, "event_type", eventType, "count", 1)
	case count > 0:
		r.log.Warn(rowsSuppressedMessage, "count", count)
	}
	return false
}

// next counts one occurrence at now and reports the line due: detailed for the
// first after a quiet minute, a count for the first a minute after the last
// line, neither inside the minute.
func (t *logThrottle) next(now time.Time) (detailed bool, count uint64) {
	switch quiet := t.last.IsZero() || now.Sub(t.last) >= summaryInterval; {
	case quiet && t.pending == 0:
		t.last = now
		return true, 0
	case quiet:
		count, t.last, t.pending = t.pending+1, now, 0
		return false, count
	default:
		t.pending++
		return false, 0
	}
}

// boundDetails applies FieldDetail to every top-level string value of
// details, a named string type included, cloning the map before the first
// change so the caller's map is never written.
func boundDetails(details map[string]any) map[string]any {
	out, cloned := details, false
	for key, value := range details {
		text, ok := value.(string)
		if !ok {
			v := reflect.ValueOf(value)
			if v.Kind() != reflect.String {
				continue
			}
			text = v.String()
		}
		bounded := BoundValue(FieldDetail, text)
		if bounded == text {
			continue
		}
		if !cloned {
			out, cloned = maps.Clone(details), true
		}
		out[key] = bounded
	}
	return out
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
