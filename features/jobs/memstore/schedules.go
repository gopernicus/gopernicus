package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// Compile-time seam: the Schedules store fills the exact schedule.Repository port.
var _ schedule.Repository = (*Schedules)(nil)

// Schedules is the in-memory schedule.Repository. Every operation is serialized
// on a single mutex; ClaimDue is the pure value compare-and-set the fire engine
// relies on to fire each (schedule, slot) pair exactly once.
type Schedules struct {
	mu   sync.Mutex
	byID map[string]schedule.Schedule
}

// NewSchedules builds an empty in-memory schedule store.
func NewSchedules() *Schedules {
	return &Schedules{byID: map[string]schedule.Schedule{}}
}

// Ensure upserts by Name: it creates the schedule (enabled, next_run_at = next)
// or updates the existing one's kind, spec, and payload, advancing next_run_at
// to next only when the spec changed.
func (s *Schedules) Ensure(_ context.Context, in schedule.Ensure, next time.Time) (schedule.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if existing, ok := s.findByName(in.Name); ok {
		specChanged := existing.Spec != in.Spec
		existing.Kind = in.Kind
		existing.Spec = in.Spec
		existing.Payload = in.Payload
		if specChanged {
			existing.NextRunAt = next
		}
		existing.UpdatedAt = now
		s.byID[existing.ID] = existing
		return existing, nil
	}

	sch := schedule.Schedule{
		ID:        newID("sched"),
		Name:      in.Name,
		Kind:      in.Kind,
		Spec:      in.Spec,
		Payload:   in.Payload,
		Enabled:   true,
		NextRunAt: next,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.byID[sch.ID] = sch
	return sch, nil
}

// ListDue returns up to limit enabled schedules whose next_run_at <= now,
// ordered by (next_run_at, id) so the batch is deterministic.
func (s *Schedules) ListDue(_ context.Context, now time.Time, limit int) ([]schedule.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var due []schedule.Schedule
	for _, sch := range s.byID {
		if sch.Enabled && !sch.NextRunAt.After(now) {
			due = append(due, sch)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].NextRunAt.Equal(due[j].NextRunAt) {
			return due[i].ID < due[j].ID
		}
		return due[i].NextRunAt.Before(due[j].NextRunAt)
	})
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

// ClaimDue is the pure value compare-and-set on next_run_at: it advances
// next_run_at to newNextRunAt (and last_run_at to now) only when the row's
// current next_run_at still equals prevNextRunAt and the schedule is enabled,
// reporting true when this caller won the (schedule, slot) pair. A stale
// prevNextRunAt, a disabled schedule, or a missing id loses (false, nil).
func (s *Schedules) ClaimDue(_ context.Context, id string, prevNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sch, ok := s.byID[id]
	if !ok || !sch.Enabled || !sch.NextRunAt.Equal(prevNextRunAt) {
		return false, nil
	}
	claimed := now
	sch.NextRunAt = newNextRunAt
	sch.LastRunAt = &claimed
	sch.UpdatedAt = now
	s.byID[id] = sch
	return true, nil
}

// SetLastJob records the id of the job fired for the most recent slot. A missing
// id yields sdk.ErrNotFound.
func (s *Schedules) SetLastJob(_ context.Context, id, jobID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sch, ok := s.byID[id]
	if !ok {
		return sdk.ErrNotFound
	}
	sch.LastJobID = jobID
	sch.UpdatedAt = now
	s.byID[id] = sch
	return nil
}

// Get returns the schedule with the given id, or sdk.ErrNotFound.
func (s *Schedules) Get(_ context.Context, id string) (schedule.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sch, ok := s.byID[id]
	if !ok {
		return schedule.Schedule{}, sdk.ErrNotFound
	}
	return sch, nil
}

// List returns a cursor-paginated page of schedules, ordered by (created_at, id)
// descending.
func (s *Schedules) List(_ context.Context, req crud.ListRequest) (crud.Page[schedule.Schedule], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := make([]schedule.Schedule, 0, len(s.byID))
	for _, sch := range s.byID {
		all = append(all, sch)
	}
	return page(all, req, schedule.OrderFields, func(sch schedule.Schedule) (time.Time, string) { return sch.CreatedAt, sch.ID })
}

// SetEnabled toggles a schedule's enabled flag. A missing id yields
// sdk.ErrNotFound.
func (s *Schedules) SetEnabled(_ context.Context, id string, enabled bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sch, ok := s.byID[id]
	if !ok {
		return sdk.ErrNotFound
	}
	sch.Enabled = enabled
	sch.UpdatedAt = now
	s.byID[id] = sch
	return nil
}

// Delete removes a schedule; a missing id yields sdk.ErrNotFound.
func (s *Schedules) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byID[id]; !ok {
		return sdk.ErrNotFound
	}
	delete(s.byID, id)
	return nil
}

// findByName returns the schedule with the given unique Name, if present. The
// caller holds the mutex.
func (s *Schedules) findByName(name string) (schedule.Schedule, bool) {
	for _, sch := range s.byID {
		if sch.Name == name {
			return sch, true
		}
	}
	return schedule.Schedule{}, false
}
