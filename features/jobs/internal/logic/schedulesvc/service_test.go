package schedulesvc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

type claimCall struct {
	id         string
	prev, next time.Time
	now        time.Time
}

// fakeSchedules is an in-test schedule.Repository. ListDue/ClaimDue/SetLastJob/
// Ensure carry behavior; the rest satisfy the port.
type fakeSchedules struct {
	due        []schedule.Schedule
	listDueErr error
	claimFn    func(c claimCall) (bool, error)
	claims     []claimCall
	setLast    []struct{ id, jobID string }
	ensured    []struct {
		in   schedule.Ensure
		next time.Time
	}
}

func (f *fakeSchedules) Ensure(ctx context.Context, in schedule.Ensure, next time.Time) (schedule.Schedule, error) {
	f.ensured = append(f.ensured, struct {
		in   schedule.Ensure
		next time.Time
	}{in, next})
	return schedule.Schedule{ID: "sched-" + in.Name, Name: in.Name, Kind: in.Kind, Spec: in.Spec, NextRunAt: next, Enabled: true}, nil
}
func (f *fakeSchedules) ListDue(ctx context.Context, now time.Time, limit int) ([]schedule.Schedule, error) {
	return f.due, f.listDueErr
}
func (f *fakeSchedules) ClaimDue(ctx context.Context, id string, prev, next, now time.Time) (bool, error) {
	c := claimCall{id: id, prev: prev, next: next, now: now}
	f.claims = append(f.claims, c)
	if f.claimFn != nil {
		return f.claimFn(c)
	}
	return true, nil
}
func (f *fakeSchedules) SetLastJob(ctx context.Context, id, jobID string, now time.Time) error {
	f.setLast = append(f.setLast, struct{ id, jobID string }{id, jobID})
	return nil
}
func (f *fakeSchedules) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	return schedule.Schedule{}, sdk.ErrNotFound
}
func (f *fakeSchedules) List(ctx context.Context, _ crud.ListRequest) (crud.Page[schedule.Schedule], error) {
	return crud.Page[schedule.Schedule]{}, nil
}
func (f *fakeSchedules) SetEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	return nil
}
func (f *fakeSchedules) Delete(ctx context.Context, id string) error { return nil }

type fakeEnqueuer struct {
	calls     []job.Enqueue
	existsFor map[string]bool
	err       error
}

func (f *fakeEnqueuer) EnqueueJob(ctx context.Context, in job.Enqueue) (job.Job, error) {
	f.calls = append(f.calls, in)
	if f.existsFor[in.ID] {
		return job.Job{}, fmt.Errorf("%s: %w", in.ID, sdk.ErrAlreadyExists)
	}
	if f.err != nil {
		return job.Job{}, f.err
	}
	return job.Job{JobID: in.ID, Kind: in.Kind}, nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestWorkFunc_NoDue_ReturnsErrNoWork(t *testing.T) {
	repo := &fakeSchedules{}
	enq := &fakeEnqueuer{}
	svc := NewService(Deps{Schedules: repo, Enqueuer: enq})

	err := svc.WorkFunc()(context.Background())
	if !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("err = %v, want ErrNoWork", err)
	}
}

func TestFire_CASWin_EnqueuesDeterministicIDAndSetsLastJob(t *testing.T) {
	slot := time.Unix(1_000_000, 0).UTC()
	now := slot.Add(time.Second) // on-time-ish
	repo := &fakeSchedules{
		due: []schedule.Schedule{{
			ID: "s1", Name: "nightly", Kind: "demo.run",
			Spec: schedule.Spec{Every: time.Hour}, NextRunAt: slot, Enabled: true,
		}},
	}
	enq := &fakeEnqueuer{}
	svc := NewService(Deps{Schedules: repo, Enqueuer: enq, Clock: fixedClock(now)})

	if err := svc.WorkFunc()(context.Background()); err != nil {
		t.Fatalf("workfunc: %v", err)
	}

	if len(repo.claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(repo.claims))
	}
	got := repo.claims[0]
	if !got.prev.Equal(slot) {
		t.Fatalf("CAS prev = %v, want the due slot %v", got.prev, slot)
	}
	// next advances from NOW, not from the slot (missed-window semantics).
	if !got.next.Equal(now.Add(time.Hour)) {
		t.Fatalf("CAS next = %v, want now+1h %v", got.next, now.Add(time.Hour))
	}

	wantID := fmt.Sprintf("sched_s1_%d", slot.Unix())
	if len(enq.calls) != 1 || enq.calls[0].ID != wantID {
		t.Fatalf("enqueue calls = %+v, want one with ID %q", enq.calls, wantID)
	}
	if len(repo.setLast) != 1 || repo.setLast[0].jobID != wantID {
		t.Fatalf("setLast = %+v, want one with jobID %q", repo.setLast, wantID)
	}
}

func TestFire_CASLose_DoesNotEnqueue(t *testing.T) {
	slot := time.Unix(2_000_000, 0).UTC()
	repo := &fakeSchedules{
		due: []schedule.Schedule{{
			ID: "s2", Kind: "demo.run",
			Spec: schedule.Spec{Every: time.Minute}, NextRunAt: slot, Enabled: true,
		}},
		claimFn: func(c claimCall) (bool, error) { return false, nil }, // lost the slot
	}
	enq := &fakeEnqueuer{}
	svc := NewService(Deps{Schedules: repo, Enqueuer: enq, Clock: fixedClock(slot.Add(time.Second))})

	if err := svc.WorkFunc()(context.Background()); err != nil {
		t.Fatalf("workfunc: %v", err)
	}
	if len(enq.calls) != 0 {
		t.Fatalf("lost CAS must not enqueue, got %+v", enq.calls)
	}
	if len(repo.setLast) != 0 {
		t.Fatalf("lost CAS must not set last job, got %+v", repo.setLast)
	}
}

func TestFire_MissedWindowFiresOnce(t *testing.T) {
	// Hourly schedule whose slot is 3 hours stale (a long outage). It must fire
	// exactly once, and the next slot advances from now.
	now := time.Unix(3_000_000, 0).UTC()
	slot := now.Add(-3 * time.Hour)
	repo := &fakeSchedules{
		due: []schedule.Schedule{{
			ID: "s3", Kind: "demo.run",
			Spec: schedule.Spec{Every: time.Hour}, NextRunAt: slot, Enabled: true,
		}},
	}
	enq := &fakeEnqueuer{}
	svc := NewService(Deps{Schedules: repo, Enqueuer: enq, Clock: fixedClock(now)})

	if err := svc.WorkFunc()(context.Background()); err != nil {
		t.Fatalf("workfunc: %v", err)
	}
	if len(enq.calls) != 1 {
		t.Fatalf("missed window must fire exactly once, got %d", len(enq.calls))
	}
	if !repo.claims[0].next.Equal(now.Add(time.Hour)) {
		t.Fatalf("next = %v, want now+1h", repo.claims[0].next)
	}
}

func TestFire_SwallowsErrAlreadyExists(t *testing.T) {
	slot := time.Unix(4_000_000, 0).UTC()
	wantID := fmt.Sprintf("sched_s4_%d", slot.Unix())
	repo := &fakeSchedules{
		due: []schedule.Schedule{{
			ID: "s4", Kind: "demo.run",
			Spec: schedule.Spec{Every: time.Hour}, NextRunAt: slot, Enabled: true,
		}},
	}
	enq := &fakeEnqueuer{existsFor: map[string]bool{wantID: true}}
	svc := NewService(Deps{Schedules: repo, Enqueuer: enq, Clock: fixedClock(slot.Add(time.Second))})

	if err := svc.WorkFunc()(context.Background()); err != nil {
		t.Fatalf("workfunc: %v", err)
	}
	// The duplicate is swallowed: SetLastJob still runs.
	if len(repo.setLast) != 1 || repo.setLast[0].jobID != wantID {
		t.Fatalf("setLast = %+v, want one with %q despite ErrAlreadyExists", repo.setLast, wantID)
	}
}

func TestEnsureSchedule_CronNil_Loud(t *testing.T) {
	repo := &fakeSchedules{}
	svc := NewService(Deps{Schedules: repo, Enqueuer: &fakeEnqueuer{}}) // no CronNext

	_, err := svc.EnsureSchedule(context.Background(), schedule.Ensure{
		Name: "c", Kind: "demo.run", Spec: schedule.Spec{Cron: "* * * * *"},
	})
	if !errors.Is(err, ErrCronRequired) {
		t.Fatalf("err = %v, want ErrCronRequired", err)
	}
	if len(repo.ensured) != 0 {
		t.Fatal("must not upsert when cron parser is missing")
	}
}

func TestEnsureSchedule_EveryPath_ParserFree(t *testing.T) {
	now := time.Unix(6_000_000, 0).UTC()
	repo := &fakeSchedules{}
	svc := NewService(Deps{Schedules: repo, Enqueuer: &fakeEnqueuer{}, Clock: fixedClock(now)}) // no CronNext

	_, err := svc.EnsureSchedule(context.Background(), schedule.Ensure{
		Name: "iv", Kind: "demo.run", Spec: schedule.Spec{Every: 15 * time.Second},
	})
	if err != nil {
		t.Fatalf("Every path must need no parser: %v", err)
	}
	if len(repo.ensured) != 1 || !repo.ensured[0].next.Equal(now.Add(15*time.Second)) {
		t.Fatalf("ensured = %+v, want next = now+15s", repo.ensured)
	}
}

func TestEnsureSchedule_InvalidSpec(t *testing.T) {
	svc := NewService(Deps{Schedules: &fakeSchedules{}, Enqueuer: &fakeEnqueuer{}})

	cases := map[string]schedule.Spec{
		"neither": {},
		"both":    {Cron: "* * * * *", Every: time.Minute},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.EnsureSchedule(context.Background(), schedule.Ensure{Name: "x", Kind: "k", Spec: spec})
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestEnsureSchedule_CronPath(t *testing.T) {
	now := time.Unix(7_000_000, 0).UTC()
	fireAt := now.Add(42 * time.Second)
	repo := &fakeSchedules{}
	cronNext := func(expr string, after time.Time) (time.Time, error) {
		if expr != "*/5 * * * *" {
			t.Fatalf("unexpected expr %q", expr)
		}
		return fireAt, nil
	}
	svc := NewService(Deps{Schedules: repo, Enqueuer: &fakeEnqueuer{}, CronNext: cronNext, Clock: fixedClock(now)})

	if _, err := svc.EnsureSchedule(context.Background(), schedule.Ensure{
		Name: "cr", Kind: "demo.run", Spec: schedule.Spec{Cron: "*/5 * * * *"},
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(repo.ensured) != 1 || !repo.ensured[0].next.Equal(fireAt) {
		t.Fatalf("ensured = %+v, want next = %v", repo.ensured, fireAt)
	}
}
