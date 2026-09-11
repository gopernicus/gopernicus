package schedules_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	schedulesapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type claimCall struct {
	id         string
	prev, next time.Time
	now        time.Time
	kind       string
}

// fakeSchedules is an in-test schedulesapi.Repository. ListDue/ClaimDue/SetLastJob/
// schedulesapi.Ensure carry behavior; the rest satisfy the port.
type fakeSchedules struct {
	pending    []schedulesapi.Occurrence
	due        []schedulesapi.Schedule
	listDueErr error
	listKinds  [][]string // the kinds each ListDue call was handed
	claimFn    func(c claimCall) (bool, error)
	claims     []claimCall
	setLast    []struct{ id, jobID string }
	ensured    []struct {
		in   schedulesapi.Ensure
		next time.Time
	}
}

func (f *fakeSchedules) Ensure(ctx context.Context, in schedulesapi.Ensure, next time.Time) (schedulesapi.Schedule, error) {
	f.ensured = append(f.ensured, struct {
		in   schedulesapi.Ensure
		next time.Time
	}{in, next})
	return schedulesapi.Schedule{ID: "sched-" + in.Name, Name: in.Name, Kind: in.Kind, Spec: in.Spec, NextRunAt: next, Enabled: true}, nil
}
func (f *fakeSchedules) ListDue(ctx context.Context, now time.Time, limit int, kinds []string) ([]schedulesapi.Schedule, error) {
	f.listKinds = append(f.listKinds, kinds)
	return f.due, f.listDueErr
}
func (f *fakeSchedules) ClaimDue(ctx context.Context, expected schedulesapi.Schedule, next, now time.Time) (bool, error) {
	c := claimCall{id: expected.ID, prev: expected.NextRunAt, next: next, now: now, kind: expected.Kind}
	f.claims = append(f.claims, c)
	if f.claimFn != nil {
		won, err := f.claimFn(c)
		if !won || err != nil {
			return won, err
		}
	}
	f.pending = append(f.pending, schedulesapi.Occurrence{JobID: schedulesapi.OccurrenceID(expected.ID, expected.NextRunAt), ScheduleID: expected.ID, Kind: expected.Kind, TenantID: expected.TenantID, Payload: expected.Payload, Slot: expected.NextRunAt, ClaimedAt: now})
	return true, nil
}
func (f *fakeSchedules) ListPending(context.Context, int, []string) ([]schedulesapi.Occurrence, error) {
	return f.pending, nil
}
func (f *fakeSchedules) AckOccurrence(ctx context.Context, jobID string, now time.Time) error {
	f.setLast = append(f.setLast, struct{ id, jobID string }{"", jobID})
	return nil
}
func (f *fakeSchedules) Get(ctx context.Context, id string) (schedulesapi.Schedule, error) {
	return schedulesapi.Schedule{}, sdk.ErrNotFound
}
func (f *fakeSchedules) List(ctx context.Context, _ list.Request) (list.Page[schedulesapi.Schedule], error) {
	return list.Page[schedulesapi.Schedule]{}, nil
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
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq))

	err := svc.WorkFunc(nil)(context.Background())
	if !errors.Is(err, workers.ErrNoWork) {
		t.Fatalf("err = %v, want ErrNoWork", err)
	}
}

func TestWorkFunc_CopiesKindsIndependently(t *testing.T) {
	repo := &fakeSchedules{}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(&fakeEnqueuer{}))
	kinds := []string{"a"}
	first := svc.WorkFunc(kinds)
	kinds[0] = "b"
	second := svc.WorkFunc(kinds)
	kinds[0] = "changed"
	for _, work := range []workers.WorkFunc{first, second, first} {
		if err := work(context.Background()); !errors.Is(err, workers.ErrNoWork) {
			t.Fatalf("work: %v, want ErrNoWork", err)
		}
	}
	if want := [][]string{{"a"}, {"b"}, {"a"}}; !reflect.DeepEqual(repo.listKinds, want) {
		t.Fatalf("ListDue kinds = %v, want %v", repo.listKinds, want)
	}
}

// TestWorkFunc_KindsReachListDueAndClaimDue proves the #37 wiring: the tick
// lists only the runtime kinds, and each fire re-asserts the listed schedule's kind in
// the CAS so a re-kinded row is left to its new owner.
func TestWorkFunc_KindsReachListDueAndClaimDue(t *testing.T) {
	slot := time.Unix(1_000_000, 0).UTC()
	now := slot.Add(time.Second)
	repo := &fakeSchedules{
		due: []schedulesapi.Schedule{{ID: "s1", Name: "n", Kind: "a", Spec: schedulesapi.Spec{Every: time.Hour}, NextRunAt: slot, Enabled: true}},
	}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(&fakeEnqueuer{}), schedulesapi.WithClock(fixedClock(now)))

	if err := svc.WorkFunc([]string{"a", "z"})(context.Background()); err != nil {
		t.Fatalf("workfunc: %v", err)
	}
	if len(repo.listKinds) != 1 || len(repo.listKinds[0]) != 2 || repo.listKinds[0][0] != "a" || repo.listKinds[0][1] != "z" {
		t.Fatalf("ListDue kinds = %v, want [[a z]]", repo.listKinds)
	}
	if len(repo.claims) != 1 || repo.claims[0].kind != "a" {
		t.Fatalf("ClaimDue calls = %+v, want one with expectedKind a", repo.claims)
	}
}

func TestFire_CASWin_EnqueuesDeterministicIDAndSetsLastJob(t *testing.T) {
	slot := time.Unix(1_000_000, 0).UTC()
	now := slot.Add(time.Second) // on-time-ish
	repo := &fakeSchedules{
		due: []schedulesapi.Schedule{{
			ID: "s1", Name: "nightly", Kind: "demo.run", TenantID: "tenant-a",
			Spec: schedulesapi.Spec{Every: time.Hour}, NextRunAt: slot, Enabled: true,
		}},
	}
	enq := &fakeEnqueuer{}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithClock(fixedClock(now)))

	if err := svc.WorkFunc(nil)(context.Background()); err != nil {
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

	wantID := schedulesapi.OccurrenceID("s1", slot)
	if len(enq.calls) != 1 || enq.calls[0].ID != wantID {
		t.Fatalf("enqueue calls = %+v, want one with ID %q", enq.calls, wantID)
	}
	// The schedule's tenant is copied onto the fired job (vocabulary carry-through)
	// so tenant-scoped ops queries see fired work.
	if enq.calls[0].TenantID != "tenant-a" {
		t.Fatalf("fired job TenantID = %q, want the schedule's %q", enq.calls[0].TenantID, "tenant-a")
	}
	if len(repo.setLast) != 1 || repo.setLast[0].jobID != wantID {
		t.Fatalf("setLast = %+v, want one with jobID %q", repo.setLast, wantID)
	}
}

func TestFire_CASLose_DoesNotEnqueue(t *testing.T) {
	slot := time.Unix(2_000_000, 0).UTC()
	repo := &fakeSchedules{
		due: []schedulesapi.Schedule{{
			ID: "s2", Kind: "demo.run",
			Spec: schedulesapi.Spec{Every: time.Minute}, NextRunAt: slot, Enabled: true,
		}},
		claimFn: func(c claimCall) (bool, error) { return false, nil }, // lost the slot
	}
	enq := &fakeEnqueuer{}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithClock(fixedClock(slot.Add(time.Second))))

	if err := svc.WorkFunc(nil)(context.Background()); err != nil {
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
		due: []schedulesapi.Schedule{{
			ID: "s3", Kind: "demo.run",
			Spec: schedulesapi.Spec{Every: time.Hour}, NextRunAt: slot, Enabled: true,
		}},
	}
	enq := &fakeEnqueuer{}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithClock(fixedClock(now)))

	if err := svc.WorkFunc(nil)(context.Background()); err != nil {
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
	wantID := schedulesapi.OccurrenceID("s4", slot)
	repo := &fakeSchedules{
		due: []schedulesapi.Schedule{{
			ID: "s4", Kind: "demo.run",
			Spec: schedulesapi.Spec{Every: time.Hour}, NextRunAt: slot, Enabled: true,
		}},
	}
	enq := &fakeEnqueuer{existsFor: map[string]bool{wantID: true}}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(enq), schedulesapi.WithClock(fixedClock(slot.Add(time.Second))))

	if err := svc.WorkFunc(nil)(context.Background()); err != nil {
		t.Fatalf("workfunc: %v", err)
	}
	// The duplicate is swallowed: SetLastJob still runs.
	if len(repo.setLast) != 1 || repo.setLast[0].jobID != wantID {
		t.Fatalf("setLast = %+v, want one with %q despite ErrAlreadyExists", repo.setLast, wantID)
	}
}

func TestEnsureSchedule_CronNil_Loud(t *testing.T) {
	repo := &fakeSchedules{}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(&fakeEnqueuer{})) // no Cron

	_, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{
		Name: "c", Kind: "demo.run", Spec: schedulesapi.Spec{Cron: "* * * * *"},
	})
	if !errors.Is(err, schedulesapi.ErrCronRequired) {
		t.Fatalf("err = %v, want schedulesapi.ErrCronRequired", err)
	}
	if len(repo.ensured) != 0 {
		t.Fatal("must not upsert when cron parser is missing")
	}
}

func TestEnsureSchedule_EveryPath_ParserFree(t *testing.T) {
	now := time.Unix(6_000_000, 0).UTC()
	repo := &fakeSchedules{}
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(&fakeEnqueuer{}), schedulesapi.WithClock(fixedClock(now))) // no Cron

	_, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{
		Name: "iv", Kind: "demo.run", Spec: schedulesapi.Spec{Every: 15 * time.Second},
	})
	if err != nil {
		t.Fatalf("Every path must need no parser: %v", err)
	}
	if len(repo.ensured) != 1 || !repo.ensured[0].next.Equal(now.Add(15*time.Second)) {
		t.Fatalf("ensured = %+v, want next = now+15s", repo.ensured)
	}
}

func TestEnsureSchedule_InvalidSpec(t *testing.T) {
	svc := newScheduleService(t, &fakeSchedules{}, schedulesapi.WithEnqueuer(&fakeEnqueuer{}))

	cases := map[string]schedulesapi.Spec{
		"neither": {},
		"both":    {Cron: "* * * * *", Every: time.Minute},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{Name: "x", Kind: "k", Spec: spec})
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
	svc := newScheduleService(t, repo, schedulesapi.WithEnqueuer(&fakeEnqueuer{}), schedulesapi.WithCronParser(testCronParser{next: cronNext}), schedulesapi.WithClock(fixedClock(now)))

	if _, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{
		Name: "cr", Kind: "demo.run", Spec: schedulesapi.Spec{Cron: "*/5 * * * *"},
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(repo.ensured) != 1 || !repo.ensured[0].next.Equal(fireAt) {
		t.Fatalf("ensured = %+v, want next = %v", repo.ensured, fireAt)
	}
}

func (f *fakeSchedules) RecordAttempt(context.Context, string) (bool, error) { return true, nil }

func newScheduleService(t *testing.T, repo schedulesapi.Repository, opts ...schedulesapi.Option) *schedulesapi.Service {
	t.Helper()
	svc, err := schedulesapi.NewService(repo, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

type testCronParser struct {
	next func(string, time.Time) (time.Time, error)
}

func (p testCronParser) Parse(expr string) (schedulesapi.CronSchedule, error) {
	return testCronSchedule{expr: expr, next: p.next}, nil
}

type testCronSchedule struct {
	expr string
	next func(string, time.Time) (time.Time, error)
}

func (s testCronSchedule) Next(after time.Time) time.Time {
	next, err := s.next(s.expr, after)
	if err != nil {
		panic(err)
	}
	return next
}

func TestPublicServiceConstructionAndManagementOnlyUse(t *testing.T) {
	if _, err := schedulesapi.NewService(nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing repository: %v", err)
	}
	repo := &fakeSchedules{}
	service := newScheduleService(t, repo)
	if _, err := service.EnsureSchedule(context.Background(), schedulesapi.Ensure{Name: "managed", Kind: "work", Spec: schedulesapi.Spec{Every: time.Hour}}); err != nil {
		t.Fatal(err)
	}
	if err := service.WorkFunc([]string{"work"})(context.Background()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing enqueuer: %v", err)
	}
	if len(repo.listKinds) != 0 {
		t.Fatal("management-only worker queried or admitted occurrences before refusing missing enqueuer")
	}
}

func TestConstructionOptionsCanSelectManagementOnly(t *testing.T) {
	fixed := time.Unix(1700000000, 0).UTC()
	repo := &fakeSchedules{}
	svc := newScheduleService(t, repo,
		schedulesapi.WithEnqueuer(&fakeEnqueuer{}), schedulesapi.WithEnqueuer(nil),
		schedulesapi.WithCronParser(testCronParser{}), schedulesapi.WithCronParser(nil),
		schedulesapi.WithClock(fixedClock(fixed)),
	)
	if _, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{Name: "fixed", Kind: "work", Spec: schedulesapi.Spec{Every: time.Hour}}); err != nil {
		t.Fatal(err)
	}
	if !repo.ensured[0].next.Equal(fixed.Add(time.Hour)) {
		t.Fatalf("next = %v", repo.ensured[0].next)
	}
	if _, err := svc.EnsureSchedule(context.Background(), schedulesapi.Ensure{Name: "cron", Kind: "work", Spec: schedulesapi.Spec{Cron: "* * * * *"}}); !errors.Is(err, schedulesapi.ErrCronRequired) {
		t.Fatalf("disabled cron: %v", err)
	}
	if err := svc.WorkFunc([]string{"work"})(context.Background()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("management-only work: %v", err)
	}
	if _, err := schedulesapi.NewService(repo, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
}
