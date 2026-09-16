package memory

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// write stages owned fact state under the shared mutex. Audit preparation or
// cancellation can fail without exposing either facts or partial history.
func (s *state) write(ctx context.Context, apply func(*state) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.recordAudit {
		if _, err := audit.SourceFromContext(ctx); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := &state{facts: maps.Clone(s.facts)}
	if err := apply(next); err != nil {
		return err
	}
	var changes []audit.Change
	if s.recordAudit {
		changes = stateChanges(s, next)
	}

	var records []audit.Record
	if s.recordAudit {
		var err error
		records, err = audit.NewRecords(ctx, changes, time.Now())
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.facts = next.facts
	s.auditRecords = append(s.auditRecords, records...)
	return nil
}

func stateChanges(before, after *state) []audit.Change {
	var out []audit.Change
	for t := range before.facts {
		if _, ok := after.facts[t]; !ok {
			out = append(out, audit.Change{Action: audit.ActionRemoved, Tuple: t})
		}
	}
	for t := range after.facts {
		if _, ok := before.facts[t]; !ok {
			out = append(out, audit.Change{Action: audit.ActionAdded, Tuple: t})
		}
	}
	return out
}
func (s *state) applyLocked(c tuples.Changes) {
	for _, t := range c.Remove {
		delete(s.facts, t)
	}
	for _, t := range c.Add {
		s.facts[t] = struct{}{}
	}
}

// Audit reads the bundle's retained history. It cannot append or erase records.
type Audit struct{ st *state }

var _ audit.Reader = (*Audit)(nil)

func (a *Audit) List(ctx context.Context, filter audit.Filter, req list.Request) (list.Page[audit.Record], error) {
	if err := ctx.Err(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if err := filter.Validate(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if err := req.Validate(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if strings.TrimSpace(req.Search) != "" {
		return list.Page[audit.Record]{}, fmt.Errorf("audit search is not supported: %w", sdk.ErrInvalidInput)
	}
	if req.Order.Field == "" {
		req.Order = audit.DefaultOrder
	}
	if req.Order.Field != "occurred_at" {
		return list.Page[audit.Record]{}, unknownOrderField(req.Order.Field)
	}
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	var all []audit.Record
	for _, record := range a.st.auditRecords {
		if auditMatches(record, filter) {
			all = append(all, cloneAuditRecord(record))
		}
	}
	asc := req.Order.Direction != list.DESC
	compare := func(a, b audit.Record) int {
		if c := a.OccurredAt.Compare(b.OccurredAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	}
	sort.Slice(all, func(i, j int) bool {
		if asc {
			return compare(all[i], all[j]) < 0
		}
		return compare(all[i], all[j]) > 0
	})
	limit := req.NormalizedLimit(list.Limits{})
	total := int64(len(all))
	encode := func(r audit.Record) (string, error) { return list.EncodeCursor("occurred_at", r.OccurredAt, r.ID) }
	var previous []audit.Record
	forward := all
	if req.ResolvedStrategy() == list.StrategyOffset {
		forward = all[min(req.Offset, len(all)):]
	} else {
		cur, err := list.DecodeCursor(req.Cursor, "occurred_at")
		if err != nil {
			return list.Page[audit.Record]{}, err
		}
		if cur != nil {
			at, ok := cur.OrderValue.(time.Time)
			if !ok {
				return list.Page[audit.Record]{}, fmt.Errorf("audit cursor must contain a timestamp: %w", sdk.ErrInvalidInput)
			}
			boundary := audit.Record{OccurredAt: at, ID: cur.PK}
			forward = nil
			for _, r := range all {
				c := compare(r, boundary)
				after := (asc && c > 0) || (!asc && c < 0)
				if after {
					forward = append(forward, r)
				} else {
					previous = append(previous, r)
				}
			}
		}
	}
	page, err := list.TrimPage(forward[:min(len(forward), limit+1)], limit, encode)
	if err != nil {
		return list.Page[audit.Record]{}, err
	}
	if req.ResolvedStrategy() == list.StrategyOffset {
		page.NextCursor = ""
		page.HasPrev = req.Offset > 0
	} else if err := list.MarkPrevPage(&page, previous[max(0, len(previous)-limit-1):], limit, encode); err != nil {
		return list.Page[audit.Record]{}, err
	}
	if req.WithCount {
		page.Total = &total
	}
	if err := ctx.Err(); err != nil {
		return list.Page[audit.Record]{}, err
	}
	return page, nil
}

func cloneAuditRecord(r audit.Record) audit.Record { return r }
func auditMatches(r audit.Record, f audit.Filter) bool {
	if f.ActorType != "" && (r.Source.ActorType != f.ActorType || r.Source.ActorID != f.ActorID) {
		return false
	}
	t := r.Change.Tuple
	return (f.ResourceType == "" || (t.Scope.Kind == tuples.ResourceScope && t.Scope.Type == f.ResourceType && t.Scope.ID == f.ResourceID)) && (f.SubjectType == "" || (t.Subject.Type == f.SubjectType && t.Subject.ID == f.SubjectID))
}

func (s *state) writeScopes(ctx context.Context, scopes []tuples.Scope, apply func(*state) error) error {
	return s.write(ctx, func(next *state) error {
		if err := apply(next); err != nil {
			return err
		}
		if len(s.integrity.Rules) == 0 {
			return nil
		}
		seen := make(map[tuples.Scope]bool, len(scopes))
		for _, scope := range scopes {
			if seen[scope] || scope.Kind != tuples.ResourceScope {
				continue
			}
			seen[scope] = true
			facts := []tuples.Tuple{}
			for fact := range next.facts {
				if fact.Scope == scope {
					facts = append(facts, fact)
				}
			}
			if err := s.integrity.ValidateState(scope, facts); err != nil {
				return err
			}
		}
		return ctx.Err()
	})
}
