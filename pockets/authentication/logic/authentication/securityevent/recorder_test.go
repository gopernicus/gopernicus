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
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
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

// purpose is a named string type, as a Details producer may carry.
type purpose string

func TestRecorderBoundsAttributedValues(t *testing.T) {
	repo := &recordingRepo{}
	rec := NewRecorder(repo, sdk.IDGenerator{}, nil, nil,
		WithClientInfoReader(func(context.Context) (string, string) { return "203.0.113.7:443", "agent/\xff" }))
	details := map[string]any{
		"client_id": "mcp\x00",
		"oversized": strings.Repeat("a", 4096),
		"named":     purpose("step\x00up"),
		"provider":  "google",
		"count":     3,
		"flag":      true,
	}
	rec.Record(sdk.WithRequestID(context.Background(), "req-é"), Input{Type: TypeOAuth2GrantFailed, Status: StatusFailure, Details: details})
	got := repo.recorded()
	if len(got) != 1 {
		t.Fatalf("recorded %d rows, want 1: a refused value blanks, never loses the row", len(got))
	}
	evt := got[0]
	if evt.IPAddress != "" || evt.UserAgent != "" || evt.RequestID != "" {
		t.Errorf("ip/ua/request id = %q/%q/%q, want all blank", evt.IPAddress, evt.UserAgent, evt.RequestID)
	}
	for key, want := range map[string]any{"client_id": "", "oversized": "", "named": "", "provider": "google", "count": 3, "flag": true} {
		if evt.Details[key] != want {
			t.Errorf("Details[%q] = %#v, want %#v", key, evt.Details[key], want)
		}
	}
	if details["client_id"] != "mcp\x00" || details["named"] != purpose("step\x00up") {
		t.Error("Record wrote the caller's Details map")
	}

	clean := map[string]any{"provider": "google"}
	rec = NewRecorder(repo, sdk.IDGenerator{}, nil, nil,
		WithClientInfoReader(func(context.Context) (string, string) { return "2001:db8::1", strings.Repeat("u", MaxUserAgentLen+1) }))
	rec.Record(context.Background(), Input{Type: TypeLogin, Status: StatusSuccess, Details: clean})
	evt = repo.recorded()[1]
	if evt.IPAddress != "2001:db8::1" || evt.UserAgent != strings.Repeat("u", MaxUserAgentLen) || evt.Details["provider"] != "google" {
		t.Errorf("clean row = ip %q, ua %d bytes, details %v", evt.IPAddress, len(evt.UserAgent), evt.Details)
	}
}

// budgetAllower admits the first allow calls, then refuses; err fails every
// call. It records each key and limit it was asked about.
type budgetAllower struct {
	mu     sync.Mutex
	allow  int
	err    error
	keys   []string
	limits []ratelimiter.Limit
}

func (b *budgetAllower) Allow(_ context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keys = append(b.keys, key)
	b.limits = append(b.limits, limit)
	if b.err != nil {
		return ratelimiter.Result{}, b.err
	}
	if len(b.keys) <= b.allow {
		return ratelimiter.Result{Allowed: true}, nil
	}
	return ratelimiter.Result{Allowed: false}, nil
}

func (b *budgetAllower) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.keys...)
}

func TestRecorderFailureRowBudget(t *testing.T) {
	limit := ratelimiter.PerMinute(60)
	ipReader := func(ip string) RecorderOption {
		return WithClientInfoReader(func(context.Context) (string, string) { return ip, "agent/1" })
	}
	budgeted := Input{Type: TypeOAuth2GrantFailed, Status: StatusFailure, Details: map[string]any{"reason": ReasonInvalidClient}, Budgeted: true}

	t.Run("over budget writes no row, counts it and warns once a minute", func(t *testing.T) {
		repo := &recordingRepo{}
		clock := &fakeClock{now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
		logger, buf := warnLogger()
		allower := &budgetAllower{allow: 2}
		rec := NewRecorder(repo, sdk.IDGenerator{}, clock.Now, logger, ipReader("203.0.113.7"), WithFailureRowBudget(allower, limit))
		lines := func() []string {
			out := strings.TrimSpace(buf.String())
			if out == "" {
				return nil
			}
			return strings.Split(out, "\n")
		}
		for i := 0; i < 5; i++ {
			rec.Record(context.Background(), budgeted)
		}
		if n := len(repo.recorded()); n != 2 || rec.SuppressedRows() != 3 {
			t.Fatalf("rows = %d, suppressed = %d; want 2 and 3", n, rec.SuppressedRows())
		}
		if rec.WriteFailures() != 0 {
			t.Fatalf("a suppressed row counted as a write failure")
		}
		got := lines()
		if len(got) != 1 || !strings.Contains(got[0], "msg=audit_rows_suppressed") || !strings.Contains(got[0], "event_type=oauth2_grant_failed") || !strings.Contains(got[0], "count=1") {
			t.Fatalf("first suppression logged %q, want one audit_rows_suppressed line", got)
		}
		if strings.Contains(buf.String(), "203.0.113.7") {
			t.Fatalf("the WARN carries the client IP: %s", buf.String())
		}
		clock.Advance(time.Minute)
		rec.Record(context.Background(), budgeted)
		got = lines()
		if len(got) != 2 || !strings.Contains(got[1], "msg=audit_rows_suppressed count=3") {
			t.Fatalf("minute summary = %q, want count=3", got)
		}
		keys := allower.calls()
		if len(keys) != 6 || keys[0] != keys[5] || !strings.HasPrefix(keys[0], failureRowKeyPrefix) || strings.Contains(keys[0], "203.0.113.7") {
			t.Fatalf("limiter keys = %q, want one digest key per IP that never carries the address", keys)
		}
		if allower.limits[0] != limit {
			t.Fatalf("limit = %+v, want %+v", allower.limits[0], limit)
		}
	})
	t.Run("a key per IP, one shared key without an IP", func(t *testing.T) {
		allower := &budgetAllower{allow: 100}
		for _, ip := range []string{"203.0.113.7", "203.0.113.8", "", "not-an-address"} {
			NewRecorder(&recordingRepo{}, sdk.IDGenerator{}, nil, nil, ipReader(ip), WithFailureRowBudget(allower, limit)).Record(context.Background(), budgeted)
		}
		keys := allower.calls()
		if keys[0] == keys[1] || keys[2] != keys[3] || keys[0] == keys[2] {
			t.Fatalf("keys = %q, want distinct per IP and one shared for a blank or unparsable IP", keys)
		}
	})
	t.Run("a limiter error writes the row", func(t *testing.T) {
		repo := &recordingRepo{}
		allower := &budgetAllower{err: errors.New("limiter unavailable")}
		rec := NewRecorder(repo, sdk.IDGenerator{}, nil, slog.New(slog.DiscardHandler), ipReader("203.0.113.7"), WithFailureRowBudget(allower, limit))
		rec.Record(context.Background(), budgeted)
		if len(repo.recorded()) != 1 || rec.SuppressedRows() != 0 {
			t.Fatalf("rows = %d, suppressed = %d; a limiter fault must cost a row, never lose one", len(repo.recorded()), rec.SuppressedRows())
		}
	})
	t.Run("an unflagged row never asks the limiter", func(t *testing.T) {
		repo := &recordingRepo{}
		allower := &budgetAllower{}
		rec := NewRecorder(repo, sdk.IDGenerator{}, nil, nil, ipReader("203.0.113.7"), WithFailureRowBudget(allower, limit))
		unflagged := budgeted
		unflagged.Budgeted = false
		rec.Record(context.Background(), unflagged)
		if len(allower.calls()) != 0 || len(repo.recorded()) != 1 {
			t.Fatalf("limiter calls = %d, rows = %d; want 0 and 1", len(allower.calls()), len(repo.recorded()))
		}
	})
	t.Run("no budget wired writes every row", func(t *testing.T) {
		repo := &recordingRepo{}
		rec := NewRecorder(repo, sdk.IDGenerator{}, nil, nil, ipReader("203.0.113.7"), WithFailureRowBudget(nil, limit))
		for i := 0; i < 3; i++ {
			rec.Record(context.Background(), budgeted)
		}
		if len(repo.recorded()) != 3 || rec.SuppressedRows() != 0 {
			t.Fatalf("rows = %d, suppressed = %d", len(repo.recorded()), rec.SuppressedRows())
		}
	})
	t.Run("the memory limiter admits the limit, then suppresses", func(t *testing.T) {
		repo := &recordingRepo{}
		rec := NewRecorder(repo, sdk.IDGenerator{}, nil, slog.New(slog.DiscardHandler), ipReader("203.0.113.7"), WithFailureRowBudget(ratelimiter.NewMemory(), limit))
		for i := 0; i < limit.Requests+10; i++ {
			rec.Record(context.Background(), budgeted)
		}
		if len(repo.recorded()) != limit.Requests || rec.SuppressedRows() != 10 {
			t.Fatalf("rows = %d, suppressed = %d; want %d and 10", len(repo.recorded()), rec.SuppressedRows(), limit.Requests)
		}
	})
	t.Run("nil recorder", func(t *testing.T) {
		var rec *Recorder
		if rec.SuppressedRows() != 0 {
			t.Fatal("nil Recorder reports suppressed rows")
		}
	})
}

// TestRecorderMissingAttributionWarning pins the production signal for a host
// route serving without ClientInfoMiddleware: a request-id row with no client
// IP warns once per Recorder, and never without the option or for a
// background row.
func TestRecorderMissingAttributionWarning(t *testing.T) {
	reader := func(ip string) RecorderOption {
		return WithClientInfoReader(func(context.Context) (string, string) { return ip, "" })
	}
	request := sdk.WithRequestID(context.Background(), "req-1")
	row := Input{Type: TypeLogin, Status: StatusFailure}
	for _, tc := range []struct {
		name string
		opts []RecorderOption
		ctx  context.Context
		want int
	}{
		{"an unattributed request row warns once", []RecorderOption{reader(""), WithMissingAttributionWarning()}, request, 1},
		{"no reader warns once", []RecorderOption{WithMissingAttributionWarning()}, request, 1},
		{"an attributed request row never warns", []RecorderOption{reader("203.0.113.7"), WithMissingAttributionWarning()}, request, 0},
		{"a stamped but unparsable IP never warns", []RecorderOption{reader("pipe"), WithMissingAttributionWarning()}, request, 0},
		{"a background row never warns", []RecorderOption{reader(""), WithMissingAttributionWarning()}, context.Background(), 0},
		{"without the option never warns", []RecorderOption{reader("")}, request, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &recordingRepo{}
			logger, buf := warnLogger()
			rec := NewRecorder(repo, sdk.IDGenerator{}, nil, logger, tc.opts...)
			for range 3 {
				rec.Record(tc.ctx, row)
			}
			if n := len(repo.recorded()); n != 3 {
				t.Fatalf("recorded %d rows, want 3: the warning never refuses a row", n)
			}
			got := strings.Count(buf.String(), "level=WARN")
			if got != tc.want || (tc.want == 1 && !strings.Contains(buf.String(), "mount ClientInfoMiddleware")) {
				t.Fatalf("logged %d WARNs, want %d naming ClientInfoMiddleware:\n%s", got, tc.want, buf.String())
			}
		})
	}
}
