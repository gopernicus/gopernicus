package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newLog() (*slog.Logger, *lockedBuffer) {
	buf := &lockedBuffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

// fakeMaintainer blocks in Run until ctx ends and serves a fixed last report.
type fakeMaintainer struct {
	mu      sync.Mutex
	started bool
	stopped bool
	report  *maintenance.Report
}

func (m *fakeMaintainer) Run(ctx context.Context) error {
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	<-ctx.Done()
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	return nil
}

func (m *fakeMaintainer) LastReport() (maintenance.Report, bool) {
	if m.report == nil {
		return maintenance.Report{}, false
	}
	return *m.report, true
}

func (m *fakeMaintainer) state() (started, stopped bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started, m.stopped
}

// fakeExpirer returns the queued batch sizes in order, then zero, or err.
type fakeExpirer struct {
	mu      sync.Mutex
	batches []int
	limits  []int
	err     error
	panics  bool
}

func (e *fakeExpirer) ExpireOperations(_ context.Context, limit int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.limits = append(e.limits, limit)
	if e.panics {
		panic("boom")
	}
	if e.err != nil {
		return 0, e.err
	}
	if len(e.batches) == 0 {
		return 0, nil
	}
	n := e.batches[0]
	e.batches = e.batches[1:]
	return n, nil
}

func (e *fakeExpirer) calls() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]int(nil), e.limits...)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRunDrivesMaintenanceAndDrainsOperations proves Run starts the
// maintenance loop, drains the expired operations at start in OperationBatch
// calls until a short batch, logs one step line with the total, and returns
// only after the maintenance loop has stopped.
func TestRunDrivesMaintenanceAndDrainsOperations(t *testing.T) {
	log, buf := newLog()
	m := &fakeMaintainer{}
	e := &fakeExpirer{batches: []int{OperationBatch, OperationBatch, 7}}
	r, err := New(m, e, time.Hour, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	want := fmt.Sprintf(`msg="retention step" step=operations count=%d`, 2*OperationBatch+7)
	waitFor(t, "the operations step line", func() bool { return strings.Contains(buf.String(), want) })
	waitFor(t, "the maintenance loop to start", func() bool { started, _ := m.state(); return started })
	for _, limit := range e.calls() {
		if limit != OperationBatch {
			t.Fatalf("a call asked for %d, want %d", limit, OperationBatch)
		}
	}
	if n := len(e.calls()); n != 3 {
		t.Fatalf("the drain made %d calls, want 3 (it stops at the short batch)", n)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if _, stopped := m.state(); !stopped {
		t.Fatal("Run returned before the maintenance loop stopped")
	}
}

// TestOperationsFailureLogsKindAndKeepsTicking proves a failing or panicking
// expiry is logged by its kind, never its text, and the next tick retries.
func TestOperationsFailureLogsKindAndKeepsTicking(t *testing.T) {
	for name, tc := range map[string]struct {
		expirer *fakeExpirer
		kind    string
	}{
		"forbidden": {&fakeExpirer{err: fmt.Errorf("permission denied for table iam_operations: %w", sdk.ErrForbidden)}, KindForbidden},
		"other":     {&fakeExpirer{err: errors.New("permission denied for table iam_operations")}, KindOther},
		"panic":     {&fakeExpirer{panics: true}, KindOther},
	} {
		t.Run(name, func(t *testing.T) {
			log, buf := newLog()
			r, err := New(&fakeMaintainer{}, tc.expirer, 5*time.Millisecond, log)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { r.Run(ctx); close(done) }()
			waitFor(t, "three expiry calls", func() bool { return len(tc.expirer.calls()) >= 3 })
			cancel()
			<-done
			out := buf.String()
			if !strings.Contains(out, "step=operations count=0 error_kind="+tc.kind) {
				t.Errorf("no failed-step line with error_kind=%s:\n%s", tc.kind, out)
			}
			if strings.Contains(out, "iam_operations") {
				t.Errorf("a step line carries error text:\n%s", out)
			}
		})
	}
}

// TestHealthReportsTheLastPassWithoutText pins the /healthz snapshot: null
// before the first pass, then the pass's finish time, its failed-step count
// and the first failed step's kind — budget_exhausted when nothing failed but
// the budget stopped the pass — and never an error's text.
func TestHealthReportsTheLastPassWithoutText(t *testing.T) {
	finished := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	step := func(name maintenance.StepName, err error) maintenance.Step {
		return maintenance.Step{Name: name, Err: err}
	}
	secret := "permission denied for function security_events_purge_before"
	cases := []struct {
		name   string
		report *maintenance.Report
		want   Health
	}{
		{name: "before the first pass", want: Health{ErrorKind: KindNone}},
		{name: "clean", report: &maintenance.Report{FinishedAt: finished, Steps: []maintenance.Step{step(maintenance.StepSessions, nil)}},
			want: Health{LastPass: finished, ErrorKind: KindNone}},
		{name: "budget", report: &maintenance.Report{FinishedAt: finished, BudgetExhausted: true},
			want: Health{LastPass: finished, ErrorKind: KindBudgetExhausted}},
		{name: "failed steps", report: &maintenance.Report{FinishedAt: finished, BudgetExhausted: true, Steps: []maintenance.Step{
			step(maintenance.StepSessions, nil),
			step(maintenance.StepSecurityEvents, fmt.Errorf("%s: %w", secret, sdk.ErrForbidden)),
			step(maintenance.StepOAuth2, errors.New("connection reset")),
		}}, want: Health{LastPass: finished, StepsFailed: 2, ErrorKind: KindForbidden}},
		{name: "archive", report: &maintenance.Report{FinishedAt: finished, Steps: []maintenance.Step{
			step(maintenance.StepSecurityEvents, fmt.Errorf("%w: %s", maintenance.ErrArchiveFailed, secret)),
		}}, want: Health{LastPass: finished, StepsFailed: 1, ErrorKind: KindArchiveFailed}},
		{name: "below floor", report: &maintenance.Report{FinishedAt: finished, Steps: []maintenance.Step{
			step(maintenance.StepSecurityEvents, maintenance.ErrBelowRetentionFloor),
		}}, want: Health{LastPass: finished, StepsFailed: 1, ErrorKind: KindBelowFloor}},
	}
	log, _ := newLog()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(&fakeMaintainer{report: tc.report}, &fakeExpirer{}, time.Hour, log)
			if err != nil {
				t.Fatal(err)
			}
			got := r.Health()
			if got != tc.want {
				t.Fatalf("Health() = %+v, want %+v", got, tc.want)
			}
			raw, err := json.Marshal(got.Detail())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "permission") || strings.Contains(string(raw), "connection") {
				t.Fatalf("detail carries error text: %s", raw)
			}
			wantLast := `"maintenance_last_pass":null`
			if tc.report != nil {
				wantLast = `"maintenance_last_pass":"2026-10-23T12:00:00Z"`
			}
			if !strings.Contains(string(raw), wantLast) {
				t.Fatalf("detail %s lacks %s", raw, wantLast)
			}
		})
	}
}

func TestNewRefusesInvalidInput(t *testing.T) {
	log, _ := newLog()
	for name, build := range map[string]func() (*Runner, error){
		"nil maintenance":   func() (*Runner, error) { return New(nil, &fakeExpirer{}, time.Hour, log) },
		"nil expirer":       func() (*Runner, error) { return New(&fakeMaintainer{}, nil, time.Hour, log) },
		"zero interval":     func() (*Runner, error) { return New(&fakeMaintainer{}, &fakeExpirer{}, 0, log) },
		"nil logger":        func() (*Runner, error) { return New(&fakeMaintainer{}, &fakeExpirer{}, time.Hour, nil) },
		"negative interval": func() (*Runner, error) { return New(&fakeMaintainer{}, &fakeExpirer{}, -time.Second, log) },
	} {
		if _, err := build(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}
