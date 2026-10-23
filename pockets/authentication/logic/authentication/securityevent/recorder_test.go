package securityevent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// recordingRepo captures every Create with the context it ran under. createErr
// fails every write; hang blocks until the write context ends.
type recordingRepo struct {
	mu        sync.Mutex
	events    []SecurityEvent
	ctxs      []context.Context
	createErr error
	hang      bool
}

func (r *recordingRepo) Create(ctx context.Context, evt SecurityEvent) (SecurityEvent, error) {
	r.mu.Lock()
	r.ctxs = append(r.ctxs, ctx)
	hang, createErr := r.hang, r.createErr
	r.mu.Unlock()
	if hang {
		<-ctx.Done()
		return SecurityEvent{}, ctx.Err()
	}
	if createErr != nil {
		return SecurityEvent{}, createErr
	}
	if err := ctx.Err(); err != nil {
		return SecurityEvent{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
	return evt, nil
}

func (r *recordingRepo) List(context.Context, ListFilter, list.Request) (list.Page[SecurityEvent], error) {
	return list.Page[SecurityEvent]{}, nil
}

func (r *recordingRepo) recorded() []SecurityEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SecurityEvent(nil), r.events...)
}

// fakeClock is a settable clock for the once-then-per-minute log rule.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func warnLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})), &buf
}

func TestRecorderStampsEvent(t *testing.T) {
	repo := &recordingRepo{}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.FixedZone("x", 3600))
	rec := NewRecorder(repo, sdk.NewIDGenerator(func() (string, error) { return "evt-1", nil }), func() time.Time { return at }, nil,
		WithClientInfoReader(func(context.Context) (string, string) { return "203.0.113.7", "agent/1" }))

	ctx := sdk.WithTraceID(sdk.WithRequestID(context.Background(), "req-1"), "trace-1")
	rec.Record(ctx, Input{UserID: "u1", Actor: Principal{Type: "user", ID: "admin"}, Type: TypeLogin, Status: StatusSuccess, Details: map[string]any{"k": "v"}})

	got := repo.recorded()
	if len(got) != 1 {
		t.Fatalf("recorded %d events, want 1", len(got))
	}
	evt := got[0]
	want := SecurityEvent{ID: "evt-1", UserID: "u1", Actor: Principal{Type: "user", ID: "admin"}, EventType: TypeLogin, EventStatus: StatusSuccess,
		IPAddress: "203.0.113.7", UserAgent: "agent/1", RequestID: "req-1", TraceID: "trace-1", CreatedAt: at.UTC()}
	if !equalEvent(evt, want) || evt.CreatedAt.Location() != time.UTC {
		t.Errorf("event = %+v, want %+v", evt, want)
	}
	if evt.Details["k"] != "v" {
		t.Errorf("Details = %v", evt.Details)
	}
}

func equalEvent(a, b SecurityEvent) bool {
	return a.ID == b.ID && a.UserID == b.UserID && a.Actor == b.Actor && a.EventType == b.EventType &&
		a.EventStatus == b.EventStatus && a.IPAddress == b.IPAddress && a.UserAgent == b.UserAgent &&
		a.RequestID == b.RequestID && a.TraceID == b.TraceID && a.CreatedAt.Equal(b.CreatedAt)
}

func TestRecorderCorrelationIDsBlankedNeverTruncated(t *testing.T) {
	kept := strings.Repeat("r", MaxCorrelationIDLen)
	for _, tc := range []struct {
		name string
		id   string
		want string
	}{
		{"128 bytes kept", kept, kept},
		{"129 bytes blanked", kept + "r", ""},
		{"200-byte header blanked", strings.Repeat("h", 200), ""},
		{"control byte blanked", "req\n1", ""},
		{"DEL blanked", "req\x7f", ""},
		{"non-ASCII blanked", "req-é", ""},
		{"printable edges kept", " ~", " ~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &recordingRepo{}
			rec := NewRecorder(repo, sdk.IDGenerator{}, nil, nil)
			ctx := sdk.WithTraceID(sdk.WithRequestID(context.Background(), tc.id), tc.id)
			rec.Record(ctx, Input{Type: TypeLogin, Status: StatusFailure})
			got := repo.recorded()
			if len(got) != 1 {
				t.Fatalf("recorded %d events, want 1 — an invalid id must never refuse the write", len(got))
			}
			if got[0].RequestID != tc.want || got[0].TraceID != tc.want {
				t.Errorf("RequestID/TraceID = %q/%q, want %q", got[0].RequestID, got[0].TraceID, tc.want)
			}
		})
	}
}

func TestRecorderNoOps(t *testing.T) {
	var nilRecorder *Recorder
	nilRecorder.Record(context.Background(), Input{Type: TypeLogin})
	if nilRecorder.WriteFailures() != 0 || nilRecorder.Repository() != nil {
		t.Fatal("nil Recorder is not inert")
	}
	rec := NewRecorder(nil, sdk.IDGenerator{}, nil, nil)
	rec.Record(context.Background(), Input{Type: TypeLogin})
	if rec.WriteFailures() != 0 {
		t.Fatal("a Recorder without a repository counted a failure")
	}
}

func TestRecorderWritesUnderCancelledRequest(t *testing.T) {
	repo := &recordingRepo{}
	rec := NewRecorder(repo, sdk.IDGenerator{}, nil, nil)
	ctx, cancel := context.WithCancel(sdk.WithRequestID(context.Background(), "req-gone"))
	cancel()
	rec.Record(ctx, Input{Type: TypeLogin, Status: StatusFailure})
	got := repo.recorded()
	if len(got) != 1 || got[0].RequestID != "req-gone" {
		t.Fatalf("cancelled request lost its row: %+v", got)
	}
	if rec.WriteFailures() != 0 {
		t.Fatalf("WriteFailures = %d, want 0", rec.WriteFailures())
	}
}

func TestRecorderWriteDeadlineIsTwoSeconds(t *testing.T) {
	repo := &recordingRepo{}
	rec := NewRecorder(repo, sdk.IDGenerator{}, nil, nil)
	start := time.Now()
	rec.Record(context.Background(), Input{Type: TypeLogin, Status: StatusSuccess})
	deadline, ok := repo.ctxs[0].Deadline()
	if !ok {
		t.Fatal("the write context carries no deadline")
	}
	if d := deadline.Sub(start); d < 2*time.Second-100*time.Millisecond || d > 2*time.Second+100*time.Millisecond {
		t.Fatalf("write deadline = %v after Record, want 2s", d)
	}
}

func TestRecorderTimesOutHungStore(t *testing.T) {
	repo := &recordingRepo{hang: true}
	var observed []WriteFailure
	rec := NewRecorder(repo, sdk.IDGenerator{}, nil, slog.New(slog.DiscardHandler),
		WithWriteObserver(func(_ context.Context, f WriteFailure) { observed = append(observed, f) }))
	rec.timeout = 20 * time.Millisecond

	done := make(chan struct{})
	go func() {
		rec.Record(context.Background(), Input{Type: TypeLogin, Status: StatusFailure})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record did not return on a hung store")
	}
	if rec.WriteFailures() != 1 || len(observed) != 1 || observed[0].EventType != TypeLogin {
		t.Fatalf("hung write not counted: failures=%d observed=%+v", rec.WriteFailures(), observed)
	}
}

func TestRecorderWriteFailureLogsOnceThenPerMinute(t *testing.T) {
	repo := &recordingRepo{createErr: errors.New("boom: audit db unavailable")}
	clock := &fakeClock{now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	logger, buf := warnLogger()
	var mu sync.Mutex
	var observed []WriteFailure
	rec := NewRecorder(repo, sdk.IDGenerator{}, clock.Now, logger,
		WithWriteObserver(func(_ context.Context, f WriteFailure) {
			mu.Lock()
			defer mu.Unlock()
			observed = append(observed, f)
		}))
	fail := func() {
		rec.Record(context.Background(), Input{Type: TypeLogin, Status: StatusFailure, Details: map[string]any{"email": "who@example.com"}})
	}
	lines := func() []string {
		out := strings.TrimSpace(buf.String())
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}

	fail()
	got := lines()
	if len(got) != 1 {
		t.Fatalf("first failure logged %d lines, want 1: %q", len(got), got)
	}
	for _, want := range []string{"level=WARN", "msg=audit_write_failed", "event_type=login", "status=failure", "error_kind=unknown"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("first line lacks %q: %s", want, got[0])
		}
	}
	for _, forbidden := range []string{"who@example.com", "boom", "unavailable"} {
		if strings.Contains(buf.String(), forbidden) {
			t.Errorf("log leaked %q: %s", forbidden, buf.String())
		}
	}

	for i := 0; i < 5; i++ {
		clock.Advance(10 * time.Second)
		fail()
	}
	if got := lines(); len(got) != 1 {
		t.Fatalf("failures within the minute logged %d lines, want still 1: %q", len(got), got)
	}

	clock.Advance(15 * time.Second)
	fail()
	got = lines()
	if len(got) != 2 || !strings.Contains(got[1], "msg=audit_write_failed count=6") {
		t.Fatalf("minute summary = %q, want a second line with count=6", got)
	}

	clock.Advance(30 * time.Second)
	fail()
	if got := lines(); len(got) != 2 {
		t.Fatalf("a failure inside the next minute logged: %q", got)
	}
	clock.Advance(31 * time.Second)
	fail()
	got = lines()
	if len(got) != 3 || !strings.Contains(got[2], "count=2") {
		t.Fatalf("second summary = %q, want count=2", got)
	}

	clock.Advance(2 * time.Minute)
	fail()
	got = lines()
	if len(got) != 4 || !strings.Contains(got[3], "event_type=login") {
		t.Fatalf("a failure after a quiet minute = %q, want a new detailed line", got)
	}

	if rec.WriteFailures() != 10 {
		t.Errorf("WriteFailures = %d, want 10", rec.WriteFailures())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 10 {
		t.Fatalf("observer called %d times, want 10", len(observed))
	}
	if observed[0] != (WriteFailure{EventType: TypeLogin, EventStatus: StatusFailure, ErrorKind: "unknown"}) {
		t.Errorf("observed = %+v", observed[0])
	}
}

func TestRecorderObserverContextOutlivesRequest(t *testing.T) {
	repo := &recordingRepo{createErr: sdk.ErrConflict}
	var observedErr error
	var observedID string
	rec := NewRecorder(repo, sdk.IDGenerator{}, nil, slog.New(slog.DiscardHandler),
		WithWriteObserver(func(ctx context.Context, f WriteFailure) {
			observedErr = ctx.Err()
			observedID, _ = sdk.RequestIDFromContext(ctx)
			if f.ErrorKind != "conflict" {
				t.Errorf("ErrorKind = %q, want conflict", f.ErrorKind)
			}
		}))
	ctx, cancel := context.WithCancel(sdk.WithRequestID(context.Background(), "req-9"))
	cancel()
	rec.Record(ctx, Input{Type: TypeLogin, Status: StatusSuccess})
	if observedErr != nil || observedID != "req-9" {
		t.Fatalf("observer ctx err=%v request id=%q, want live ctx carrying req-9", observedErr, observedID)
	}
}

func TestErrorKindLabels(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "none"},
		{sdk.ErrAlreadyExists, "already_exists"},
		{sdk.ErrInvalidInput, "invalid_input"},
		{sdk.ErrInvalidReference, "invalid_reference"},
		{sdk.ErrNotFound, "not_found"},
		{sdk.ErrConflict, "conflict"},
		{errors.New("driver: connection refused"), "unknown"},
	} {
		if got := ErrorKind(tc.err); got != tc.want {
			t.Errorf("ErrorKind(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
