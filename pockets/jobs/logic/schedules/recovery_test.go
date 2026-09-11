package schedules_test

import (
	"context"
	"errors"
	"testing"
	"time"

	schedulesapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type recoveringEnqueuer struct {
	queue     *memory.Queue
	fail      bool
	uncertain bool
}

func (e *recoveringEnqueuer) EnqueueJob(ctx context.Context, in job.Enqueue) (job.Job, error) {
	if e.fail {
		return job.Job{}, errors.New("queue unavailable")
	}
	j, err := e.queue.Enqueue(ctx, in)
	if err == nil && e.uncertain {
		return job.Job{}, errors.New("response lost after commit")
	}
	return j, err
}
func TestPendingOccurrenceRecoversAfterRestart(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_enqueue", true: "after_enqueue"}[uncertain], func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			repo := memory.NewSchedules()
			q := memory.NewQueue()
			enq := &recoveringEnqueuer{queue: q, fail: !uncertain, uncertain: uncertain}
			sch, err := repo.Ensure(ctx, schedulesapi.Ensure{Name: "recovery", Kind: "a", Spec: schedulesapi.Spec{Every: time.Hour}}, now)
			if err != nil {
				t.Fatal(err)
			}
			svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithClock(fixedClock(now)))
			if err := svc.WorkFunc([]string{"a"})(ctx); err == nil {
				t.Fatal("failure hidden")
			}
			pending, err := repo.ListPending(ctx, 0, nil)
			if err != nil || len(pending) != 1 {
				t.Fatalf("lost occurrence: %v %v", pending, err)
			}
			enq.fail = false
			enq.uncertain = false
			restarted := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithClock(fixedClock(now)))
			if err := restarted.WorkFunc([]string{"a"})(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := q.Get(ctx, pending[0].JobID)
			if err != nil || got.Kind != "a" {
				t.Fatalf("job missing: %+v %v", got, err)
			}
			if remain, _ := repo.ListPending(ctx, 0, nil); len(remain) != 0 {
				t.Fatal("pending not acknowledged")
			}
			current, _ := repo.Get(ctx, sch.ID)
			if current.LastJobID != got.ID() {
				t.Fatal("wrong metadata")
			}
			if err := restarted.WorkFunc([]string{"a"})(ctx); !errors.Is(err, workers.ErrNoWork) {
				t.Fatal("unexpected extra dispatch", err)
			}
		})
	}
}
func TestScheduleValidation(t *testing.T) {
	now := time.Now().UTC()
	for _, every := range []time.Duration{-1, 100 * time.Millisecond, 1500 * time.Millisecond} {
		svc := newScheduleService(t, memory.NewSchedules(), schedulesapi.WithClock(fixedClock(now)))
		_, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{Name: "a", Kind: "a", Spec: schedulesapi.Spec{Every: every}})
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Every %v: %v", every, err)
		}
	}
	for _, next := range []time.Time{{}, now, now.Add(-time.Second)} {
		svc := newScheduleService(t, memory.NewSchedules(), schedulesapi.WithCronParser(testCronParser{next: func(string, time.Time) (time.Time, error) { return next, nil }}), schedulesapi.WithClock(fixedClock(now)))
		_, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{Name: "a", Kind: "a", Spec: schedulesapi.Spec{Cron: "* * * * *"}})
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("next %v: %v", next, err)
		}
	}
}

// ackFailureModels the crash boundary after enqueue but before acknowledgement.
type ackFailureSchedules struct {
	schedulesapi.Repository
	failAck     bool
	failAttempt bool
}

func (s *ackFailureSchedules) AckOccurrence(ctx context.Context, id string, now time.Time) error {
	if s.failAck {
		return errors.New("ack unavailable")
	}
	return s.Repository.AckOccurrence(ctx, id, now)
}
func (s *ackFailureSchedules) RecordAttempt(ctx context.Context, id string) (bool, error) {
	if s.failAttempt {
		return false, errors.New("attempt unavailable")
	}
	return s.Repository.RecordAttempt(ctx, id)
}
func TestDispatchFailureBoundaries(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	repo := &ackFailureSchedules{Repository: memory.NewSchedules(), failAttempt: true}
	_, err := repo.Ensure(ctx, schedulesapi.Ensure{Name: "a", Kind: "a", Spec: schedulesapi.Spec{Every: time.Hour}}, now)
	if err != nil {
		t.Fatal(err)
	}
	enq := &fakeEnqueuer{}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithClock(fixedClock(now)))
	if err := svc.WorkFunc(nil)(ctx); err == nil || len(enq.calls) != 0 {
		t.Fatal("enqueue after failed attempt admission", err)
	}
	repo.failAttempt = false
	repo.failAck = true
	if err := svc.WorkFunc(nil)(ctx); err == nil || len(enq.calls) != 1 {
		t.Fatal("ack failure not surfaced", err)
	}
	repo.failAck = false
	enq.existsFor = map[string]bool{enq.calls[0].ID: true}
	if err := svc.WorkFunc(nil)(ctx); err != nil {
		t.Fatal(err)
	}
	if len(enq.calls) != 2 || enq.calls[0].ID != enq.calls[1].ID {
		t.Fatal("ack retry changed identity")
	}
	pending, _ := repo.ListPending(ctx, 0, nil)
	if len(pending) != 0 {
		t.Fatal("ack retry left pending")
	}
}

type selectiveEnqueuer struct{ calls map[string]int }

func (e *selectiveEnqueuer) EnqueueJob(_ context.Context, in job.Enqueue) (job.Job, error) {
	e.calls[in.Kind]++
	if in.Kind == "broken" {
		return job.Job{}, errors.New("destination unavailable")
	}
	return job.Job{JobID: in.ID}, nil
}
func TestFailedOccurrenceDoesNotStarveNextBatch(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	repo := memory.NewSchedules()
	for i, kind := range []string{"broken", "healthy"} {
		if _, err := repo.Ensure(ctx, schedulesapi.Ensure{Name: kind, Kind: kind, Spec: schedulesapi.Spec{Every: time.Hour}}, now.Add(time.Duration(i-1)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	enq := &selectiveEnqueuer{calls: map[string]int{}}
	for range 3 {
		// A new engine each time also exercises persisted attempt rotation.
		svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithBatchSize(1), schedulesapi.WithClock(fixedClock(now)))
		_ = svc.WorkFunc(nil)(ctx)
	}
	if enq.calls["healthy"] != 1 {
		t.Fatalf("healthy work starved: %v", enq.calls)
	}
	pending, _ := repo.ListPending(ctx, 0, nil)
	if len(pending) != 1 || pending[0].Kind != "broken" {
		t.Fatalf("wrong pending work: %v", pending)
	}
}
