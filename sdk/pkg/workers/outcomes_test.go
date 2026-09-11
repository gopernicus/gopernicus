package workers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

type identityJob struct{ id string }

func (j *identityJob) ID() string { return j.id }

type outcomeStore struct {
	job       *identityJob
	completed string
	failed    int
	ceiling   int
	err       error
}

func (s *outcomeStore) Claim(context.Context, string, time.Time) (*identityJob, error) {
	return s.job, nil
}
func (s *outcomeStore) Complete(_ context.Context, id string, _ time.Time) error {
	s.completed = id
	return s.err
}
func (s *outcomeStore) Fail(_ context.Context, _ string, _ time.Time, _ string, max int) error {
	s.failed++
	s.ceiling = max
	return s.err
}

type deferringStore struct {
	*outcomeStore
	until time.Time
}

func (s *deferringStore) Defer(_ context.Context, _ string, until time.Time, _ string, _ time.Time) error {
	s.until = until
	return s.err
}

func TestRunnerClaimIdentityAndPersistenceFailure(t *testing.T) {
	outage := errors.New("database unavailable")
	var logs bytes.Buffer
	store := &outcomeStore{job: &identityJob{id: "claimed"}, err: outage}
	runner := NewRunner(store, func(_ context.Context, j *identityJob) error { j.id = "changed"; return nil }, WithRunnerLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := runner.WorkFunc()(context.Background()); !errors.Is(err, outage) {
		t.Fatalf("lost write error: %v", err)
	}
	if store.completed != "claimed" {
		t.Fatalf("completed %q", store.completed)
	}
	if strings.Contains(logs.String(), "job completed") {
		t.Fatal("false completion log")
	}
}

func TestJobMiddlewareOrderAndDeferral(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Minute)
	store := &deferringStore{outcomeStore: &outcomeStore{job: &identityJob{id: "job"}}}
	var order []string
	wrap := func(name string) JobMiddleware[*identityJob] {
		return func(next ProcessFunc[*identityJob]) ProcessFunc[*identityJob] {
			return func(ctx context.Context, j *identityJob) error {
				order = append(order, name+" before")
				err := next(ctx, j)
				order = append(order, name+" after")
				return err
			}
		}
	}
	gate := func(next ProcessFunc[*identityJob]) ProcessFunc[*identityJob] {
		return func(context.Context, *identityJob) error { return DeferUntil(until, "tenant paused") }
	}
	calls := 0
	processor := ChainJobMiddleware(func(context.Context, *identityJob) error { calls++; return nil }, wrap("outer"), wrap("inner"), gate)
	runner := NewRunner(store, processor, WithRunnerLogger(silentLogger()), WithClock(func() time.Time { return now }))
	if err := runner.WorkFunc()(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || store.failed != 0 || store.completed != "" || !store.until.Equal(until) {
		t.Fatalf("closed gate processed or failed work: %+v", store)
	}
	if want := []string{"outer before", "inner before", "inner after", "outer after"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v", order)
	}
}

func TestRunnerInvalidUnsupportedAndPermanentGate(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		outcome error
		want    error
		failed  int
	}{
		{"unsupported", DeferUntil(now.Add(time.Minute), "paused"), ErrDeferralUnsupported, 0},
		{"invalid", DeferUntil(now, "paused"), ErrInvalidDeferral, 0},
		{"permanent", errors.Join(DeferUntil(now.Add(time.Minute), "paused"), Reject("denied")), nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &outcomeStore{job: &identityJob{id: "job"}}
			runner := NewRunner(store, func(context.Context, *identityJob) error { return tc.outcome }, WithRunnerLogger(silentLogger()), WithClock(func() time.Time { return now }), WithMaxAttempts(20))
			if err := runner.WorkFunc()(context.Background()); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if store.failed != tc.failed || store.completed != "" {
				t.Fatalf("unexpected transition: %+v", store)
			}
			if store.failed > 0 && store.ceiling != 1 {
				t.Fatalf("permanent rejection ceiling=%d", store.ceiling)
			}
		})
	}
}

type fencedOutageStore struct {
	*fencedFakeStore
	err error
}

func (s *fencedOutageStore) Complete(context.Context, string, string, time.Time) error { return s.err }
func (s *fencedOutageStore) Fail(context.Context, string, string, string, time.Time) error {
	return s.err
}
func (s *fencedOutageStore) Reschedule(context.Context, string, string, time.Time, string, time.Time) error {
	return s.err
}

func TestFencedRunnerReturnsPersistenceFailures(t *testing.T) {
	for _, stage := range []string{"complete", "fail", "reschedule"} {
		for _, writeErr := range []error{errors.New("storage unavailable"), sdk.ErrConflict} {
			t.Run(stage+writeErr.Error(), func(t *testing.T) {
				store := &fencedOutageStore{fencedFakeStore: newFencedFakeStore(), err: writeErr}
				store.enqueue("job", time.Time{})
				var logs bytes.Buffer
				opts := []FencedRunnerOption{WithFencedLogger(slog.New(slog.NewTextHandler(&logs, nil)))}
				if stage == "reschedule" {
					opts = append(opts, WithFencedRetryDecider(func(error, int) (time.Duration, bool) { return time.Minute, true }))
				}
				r := NewFencedRunner(store, func(context.Context, fakeJob) error {
					if stage == "complete" {
						return nil
					}
					return errors.New("processing failed")
				}, opts...)
				hooks := 0
				r.SetDeadLetterHook(func(context.Context, fakeJob, string) error { hooks++; return nil })
				err := r.WorkFunc()(context.Background())
				if errors.Is(writeErr, sdk.ErrConflict) {
					if err != nil {
						t.Fatal(err)
					}
				} else if !errors.Is(err, writeErr) {
					t.Fatalf("lost failure: %v", err)
				}
				if hooks != 0 || strings.Contains(logs.String(), "fenced job completed") {
					t.Fatal("failed transition reported successful")
				}
			})
		}
	}
}

func TestJobMiddlewarePanicBecomesProcessingFailure(t *testing.T) {
	store := &outcomeStore{job: &identityJob{id: "job"}}
	gate := func(ProcessFunc[*identityJob]) ProcessFunc[*identityJob] {
		return func(context.Context, *identityJob) error { panic("gate panic") }
	}
	r := NewRunner(store, ChainJobMiddleware(func(context.Context, *identityJob) error { t.Fatal("processor invoked"); return nil }, gate), WithRunnerLogger(silentLogger()))
	if err := r.WorkFunc()(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.failed != 1 || store.completed != "" {
		t.Fatal("gate panic lost claimed work")
	}
}

func TestFencedTimeoutCannotExceedLease(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("invalid timeout accepted")
		}
	}()
	NewFencedRunner(newFencedFakeStore(), func(context.Context, fakeJob) error { return nil }, WithFencedLogger(silentLogger()), WithLeaseDuration(time.Second), WithFencedProcessTimeout(time.Second))
}

type fencedCompletionContextStore struct {
	*fencedFakeStore
	beforeComplete func(context.Context)
}

func (s *fencedCompletionContextStore) Complete(ctx context.Context, id, leaseID string, now time.Time) error {
	s.beforeComplete(ctx)
	return s.fencedFakeStore.Complete(ctx, id, leaseID, now)
}

func TestFencedRunnerCancelsProcessingContextBeforeCompletion(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var processCtx context.Context
	completed := false
	store := &fencedCompletionContextStore{
		fencedFakeStore: newFencedFakeStore(),
		beforeComplete: func(ctx context.Context) {
			completed = true
			if processCtx == nil || processCtx.Err() != context.Canceled {
				t.Fatal("processing timeout context must be canceled before Complete")
			}
			select {
			case <-processCtx.Done():
			default:
				t.Fatal("processing context is still live during Complete")
			}
			if ctx.Err() != nil || parent.Err() != nil {
				t.Fatal("Complete must retain a live parent context")
			}
			if _, hasDeadline := ctx.Deadline(); hasDeadline {
				t.Fatal("processing deadline leaked into Complete")
			}
		},
	}
	store.enqueue("job", time.Time{})
	runner := NewFencedRunner(store, func(ctx context.Context, _ fakeJob) error {
		processCtx = ctx
		if _, hasDeadline := ctx.Deadline(); !hasDeadline || ctx.Err() != nil {
			t.Fatal("processor must receive a live timeout context")
		}
		return nil
	}, WithFencedLogger(silentLogger()), WithLeaseDuration(2*time.Minute), WithFencedProcessTimeout(time.Minute))
	if err := runner.WorkFunc()(parent); err != nil {
		t.Fatal(err)
	}
	if !completed || store.snapshot("job").status != "completed" {
		t.Fatal("job was not completed")
	}
}
