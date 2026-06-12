package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/workers"
)

// fakeSchedule is an engine-local Schedule — the engine never sees the
// project's row type, only the accessor contract.
type fakeSchedule struct {
	id, name, eventType, cronExpr string

	payload   json.RawMessage
	nextRunAt time.Time
}

func (f fakeSchedule) GetScheduleID() string       { return f.id }
func (f fakeSchedule) GetName() string             { return f.name }
func (f fakeSchedule) GetEventType() string        { return f.eventType }
func (f fakeSchedule) GetCronExpr() string         { return f.cronExpr }
func (f fakeSchedule) GetPayload() json.RawMessage { return f.payload }
func (f fakeSchedule) GetNextRunAt() time.Time     { return f.nextRunAt }

type fakeStore struct {
	due      []fakeSchedule
	claims   map[string]bool // scheduleID → claim outcome
	claimed  []string
	lastJobs map[string]string
}

func (f *fakeStore) ListDue(_ context.Context, _ int) ([]fakeSchedule, error) {
	return f.due, nil
}
func (f *fakeStore) ClaimDue(_ context.Context, id string, _, _, _ time.Time) (bool, error) {
	f.claimed = append(f.claimed, id)
	if ok, set := f.claims[id]; set {
		return ok, nil
	}
	return true, nil
}
func (f *fakeStore) SetLastJob(_ context.Context, id, jobID string, _ time.Time) error {
	if f.lastJobs == nil {
		f.lastJobs = map[string]string{}
	}
	f.lastJobs[id] = jobID
	return nil
}

type fakeQueue struct {
	created []Job
	err     error
}

func (f *fakeQueue) enqueue(_ context.Context, job Job) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, job)
	return nil
}

func sched(id, name, cronExpr string, next time.Time) fakeSchedule {
	return fakeSchedule{
		id: id, name: name, eventType: "digest.send",
		cronExpr: cronExpr, nextRunAt: next,
		payload: json.RawMessage(`{"k":"v"}`),
	}
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestWorkFuncNoWork(t *testing.T) {
	s := New(&fakeStore{}, (&fakeQueue{}).enqueue, quiet())
	if err := s.WorkFunc()(context.Background()); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("err = %v, want ErrNoWork", err)
	}
}

func TestFireEnqueuesWithDeterministicID(t *testing.T) {
	slot := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	store := &fakeStore{due: []fakeSchedule{sched("s1", "digest", "0 9 * * *", slot)}}
	queue := &fakeQueue{}
	s := New(store, queue.enqueue, quiet())

	if err := s.WorkFunc()(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.created) != 1 {
		t.Fatalf("created = %d jobs", len(queue.created))
	}
	job := queue.created[0]
	if job.JobID != JobID("s1", slot) {
		t.Errorf("job id = %q, want deterministic slot id", job.JobID)
	}
	if job.EventType != "digest.send" || string(job.Payload) != `{"k":"v"}` {
		t.Errorf("job = %+v", job)
	}
	if store.lastJobs["s1"] != job.JobID {
		t.Errorf("audit pointer = %q", store.lastJobs["s1"])
	}
}

func TestLostClaimDoesNotEnqueue(t *testing.T) {
	slot := time.Now().UTC().Add(-time.Minute)
	store := &fakeStore{
		due:    []fakeSchedule{sched("s1", "digest", "@hourly", slot)},
		claims: map[string]bool{"s1": false},
	}
	queue := &fakeQueue{}
	s := New(store, queue.enqueue, quiet())

	// All claims lost → nothing fired → the tick reports no work.
	if err := s.WorkFunc()(context.Background()); !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("err = %v, want ErrNoWork", err)
	}
	if len(queue.created) != 0 {
		t.Fatalf("lost claim must not enqueue, created %d", len(queue.created))
	}
}

func TestAlreadyExistsRefireIsSwallowed(t *testing.T) {
	slot := time.Now().UTC().Add(-time.Minute)
	store := &fakeStore{due: []fakeSchedule{sched("s1", "digest", "@hourly", slot)}}
	queue := &fakeQueue{err: ErrJobExists}
	s := New(store, queue.enqueue, quiet())

	if err := s.WorkFunc()(context.Background()); err != nil {
		t.Fatalf("idempotent refire must not error the tick: %v", err)
	}
	if store.lastJobs["s1"] == "" {
		t.Error("audit pointer should still update on refire")
	}
}

func TestPoisonCronSkippedNotWedged(t *testing.T) {
	slot := time.Now().UTC().Add(-time.Minute)
	store := &fakeStore{due: []fakeSchedule{
		sched("bad", "poison", "not-a-cron", slot),
		sched("good", "digest", "@hourly", slot),
	}}
	queue := &fakeQueue{}
	s := New(store, queue.enqueue, quiet())

	if err := s.WorkFunc()(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.created) != 1 || queue.created[0].JobID != JobID("good", slot) {
		t.Fatalf("good schedule must fire past the poisoned one: %+v", queue.created)
	}
}

// Fire-once catch-up: next_run_at advances from NOW, not the missed slot.
func TestCatchUpAdvancesFromNow(t *testing.T) {
	missed := time.Now().UTC().Add(-3 * time.Hour)
	store := &fakeStore{due: []fakeSchedule{sched("s1", "hourly", "@hourly", missed)}}
	queue := &fakeQueue{}
	s := New(store, queue.enqueue, quiet())

	if err := s.WorkFunc()(context.Background()); err != nil {
		t.Fatal(err)
	}
	// One job for a 3h outage on an hourly schedule.
	if len(queue.created) != 1 {
		t.Fatalf("catch-up fired %d jobs, want exactly 1", len(queue.created))
	}
}
