package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// Compile-time seam: the Schedules store fills the exact schedule.Repository port.
var _ schedule.Repository = (*Schedules)(nil)

// Schedules is the in-memory schedule.Repository. Every operation is serialized
// on a single mutex; a successful ClaimDue persists a pending occurrence
// before a separate queue enqueue is attempted.
type Schedules struct {
	mu      sync.Mutex
	byID    map[string]schedule.Schedule
	pending map[string]schedule.Occurrence
}

// NewSchedules builds an empty in-memory schedule store.
func NewSchedules() *Schedules {
	return &Schedules{byID: map[string]schedule.Schedule{}, pending: map[string]schedule.Occurrence{}}
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
		existing.TenantID = in.TenantID
		existing.Spec = in.Spec
		existing.Payload = append([]byte(nil), in.Payload...)
		if specChanged {
			existing.NextRunAt = next
		}
		if now.After(existing.UpdatedAt) {
			existing.UpdatedAt = now
		}
		s.byID[existing.ID] = existing
		return cloneSchedule(existing), nil
	}

	sch := schedule.Schedule{
		ID:        newID("sched"),
		Name:      in.Name,
		Kind:      in.Kind,
		TenantID:  in.TenantID,
		Spec:      in.Spec,
		Payload:   append([]byte(nil), in.Payload...),
		Enabled:   true,
		NextRunAt: next,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.byID[sch.ID] = sch
	return cloneSchedule(sch), nil
}

// ListDue returns up to limit enabled schedules whose next_run_at <= now,
// ordered by (next_run_at, id) so the batch is deterministic. A non-empty kinds
// restricts the scan to those kinds before the limit applies.
func (s *Schedules) ListDue(_ context.Context, now time.Time, limit int, kinds []string) ([]schedule.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var due []schedule.Schedule
	for _, sch := range s.byID {
		if _, pending := s.pending[sch.ID]; !pending && sch.Enabled && !sch.NextRunAt.After(now) && kindAllowed(kinds, sch.Kind) {
			due = append(due, cloneSchedule(sch))
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

// ClaimDue admits one occurrence under the same lock as schedule advancement.
func (s *Schedules) ClaimDue(ctx context.Context, expected schedule.Schedule, next, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sch, ok := s.byID[expected.ID]
	_, pending := s.pending[expected.ID]
	if !ok || pending || !sch.Enabled || sch.NextRunAt.After(now) || !next.After(now) || !sch.NextRunAt.Equal(expected.NextRunAt) || sch.Kind != expected.Kind || sch.Spec != expected.Spec {
		return false, nil
	}
	occurrence := schedule.Occurrence{JobID: schedule.OccurrenceID(sch.ID, sch.NextRunAt), ScheduleID: sch.ID, Kind: sch.Kind, TenantID: sch.TenantID, Payload: append([]byte(nil), sch.Payload...), Slot: sch.NextRunAt, ClaimedAt: now}
	s.pending[sch.ID] = occurrence
	sch.NextRunAt = next
	if now.After(sch.UpdatedAt) {
		sch.UpdatedAt = now
	}
	s.byID[sch.ID] = sch
	return true, nil
}

func (s *Schedules) ListPending(ctx context.Context, limit int, kinds []string) ([]schedule.Occurrence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []schedule.Occurrence
	for _, occurrence := range s.pending {
		if kindAllowed(kinds, occurrence.Kind) {
			occurrence.Payload = append([]byte(nil), occurrence.Payload...)
			items = append(items, occurrence)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Attempts != items[j].Attempts {
			return items[i].Attempts < items[j].Attempts
		}
		if items[i].Slot.Equal(items[j].Slot) {
			return items[i].JobID < items[j].JobID
		}
		return items[i].Slot.Before(items[j].Slot)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Schedules) AckOccurrence(ctx context.Context, jobID string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, occurrence := range s.pending {
		if occurrence.JobID != jobID {
			continue
		}
		if sch, ok := s.byID[id]; ok {
			sch.LastJobID = jobID
			claimed := occurrence.ClaimedAt
			sch.LastRunAt = &claimed
			if now.After(sch.UpdatedAt) {
				sch.UpdatedAt = now
			}
			s.byID[id] = sch
		}
		delete(s.pending, id)
		break
	}
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
	return cloneSchedule(sch), nil
}

// List returns a cursor-paginated page of schedules, ordered by (created_at, id)
// descending.
func (s *Schedules) List(_ context.Context, req list.Request) (list.Page[schedule.Schedule], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := make([]schedule.Schedule, 0, len(s.byID))
	for _, sch := range s.byID {
		all = append(all, cloneSchedule(sch))
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
	if now.After(sch.UpdatedAt) {
		sch.UpdatedAt = now
	}
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

func cloneSchedule(sch schedule.Schedule) schedule.Schedule {
	sch.Payload = append([]byte(nil), sch.Payload...)
	if sch.LastRunAt != nil {
		last := *sch.LastRunAt
		sch.LastRunAt = &last
	}
	return sch
}

func (s *Schedules) RecordAttempt(ctx context.Context, jobID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, occurrence := range s.pending {
		if occurrence.JobID == jobID {
			occurrence.Attempts++
			s.pending[id] = occurrence
			return true, nil
		}
	}
	return false, nil
}
