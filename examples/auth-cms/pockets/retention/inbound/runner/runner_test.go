package runner

import (
	"bytes"
	"context"
	"errors"
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

type fakePasser struct {
	mu    sync.Mutex
	calls int
}

func (p *fakePasser) Pass(context.Context) (maintenance.Report, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return maintenance.Report{Steps: []maintenance.Step{{Name: maintenance.StepSessions, Count: 3}, {Name: maintenance.StepInvitations, Count: 2}}}, nil
}

func (p *fakePasser) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestTickRunsEveryStepPastFailuresAndPanics(t *testing.T) {
	log, buf := newLog()
	var order []string
	r, err := New(time.Hour, log,
		Step{Name: "boom", Run: func(context.Context) (int, error) { order = append(order, "boom"); panic("boom") }},
		Step{Name: "fails", Run: func(context.Context) (int, error) {
			order = append(order, "fails")
			return 0, errors.New("permission denied for table sessions")
		}},
		MaintenanceStep(&fakePasser{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	if strings.Join(order, ",") != "boom,fails" {
		t.Errorf("order = %v", order)
	}
	out := buf.String()
	for _, want := range []string{"step=boom count=0 error_kind=panic", "step=fails count=0 error_kind=other", `msg="retention step" step=maintenance count=5`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "permission denied") {
		t.Errorf("a step line carries error text:\n%s", out)
	}
}

func TestRunTicksAtStartAndEveryInterval(t *testing.T) {
	log, _ := newLog()
	p := &fakePasser{}
	r, err := New(10*time.Millisecond, log, MaintenanceStep(p))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for p.count() < 3 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("Run ticked %d times in 5s", p.count())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

type fakeExpirer struct {
	mu     sync.Mutex
	limits []int
}

func (e *fakeExpirer) ExpireOperations(_ context.Context, limit int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.limits = append(e.limits, limit)
	return 1, nil
}

func TestOperationsStepAsksForTheBatch(t *testing.T) {
	log, buf := newLog()
	e := &fakeExpirer{}
	r, err := New(time.Hour, log, OperationsStep(e, 7))
	if err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background())
	r.Tick(context.Background())
	if len(e.limits) != 2 || e.limits[0] != 7 || e.limits[1] != 7 {
		t.Fatalf("limits = %v, want [7 7]", e.limits)
	}
	if !strings.Contains(buf.String(), `msg="retention step" step=operations count=1`) {
		t.Fatalf("no operations step line:\n%s", buf.String())
	}
}

func TestNewRefusesInvalidInput(t *testing.T) {
	log, _ := newLog()
	for name, build := range map[string]func() (*Runner, error){
		"zero interval": func() (*Runner, error) { return New(0, log) },
		"nil logger":    func() (*Runner, error) { return New(time.Second, nil) },
		"unnamed step": func() (*Runner, error) {
			return New(time.Second, log, Step{Run: func(context.Context) (int, error) { return 0, nil }})
		},
		"nil call": func() (*Runner, error) { return New(time.Second, log, Step{Name: "x"}) },
	} {
		if _, err := build(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}
