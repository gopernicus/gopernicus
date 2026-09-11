package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
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
	next := &state{rel: slices.Clone(s.rel), role: slices.Clone(s.role)}
	if err := apply(next); err != nil {
		return err
	}
	var records []audit.Record
	if s.recordAudit {
		var err error
		records, err = audit.NewRecords(ctx, stateChanges(s, next), time.Now())
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.rel, s.role = next.rel, next.role
	s.auditRecords = append(s.auditRecords, records...)
	return nil
}

func stateChanges(before, after *state) []audit.Change {
	var changes []audit.Change
	beforeRel, afterRel := map[relRow]bool{}, map[relRow]bool{}
	for _, r := range before.rel {
		beforeRel[r] = true
	}
	for _, r := range after.rel {
		afterRel[r] = true
	}
	for r := range beforeRel {
		if !afterRel[r] {
			value := r.toRelationship()
			changes = append(changes, audit.Change{Action: audit.ActionRemoved, Relationship: &value})
		}
	}
	for r := range afterRel {
		if !beforeRel[r] {
			value := r.toRelationship()
			changes = append(changes, audit.Change{Action: audit.ActionAdded, Relationship: &value})
		}
	}
	beforeRole, afterRole := map[roleRow]bool{}, map[roleRow]bool{}
	for _, r := range before.role {
		beforeRole[r] = true
	}
	for _, r := range after.role {
		afterRole[r] = true
	}
	for r := range beforeRole {
		if !afterRole[r] {
			value := r.toAssignment()
			changes = append(changes, audit.Change{Action: audit.ActionRemoved, Role: &value})
		}
	}
	for r := range afterRole {
		if !beforeRole[r] {
			value := r.toAssignment()
			changes = append(changes, audit.Change{Action: audit.ActionAdded, Role: &value})
		}
	}
	return changes
}

func (r relRow) toRelationship() relationships.CreateRelationship {
	return relationships.CreateRelationship{ResourceType: r.resourceType, ResourceID: r.resourceID, Relation: r.relation, SubjectType: r.subjectType, SubjectID: r.subjectID, SubjectRelation: r.subjectRelation}
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

func cloneAuditRecord(r audit.Record) audit.Record {
	if r.Change.Relationship != nil {
		value := *r.Change.Relationship
		r.Change.Relationship = &value
	}
	if r.Change.Role != nil {
		value := *r.Change.Role
		r.Change.Role = &value
	}
	return r
}
func auditMatches(r audit.Record, f audit.Filter) bool {
	if f.ActorType != "" && (r.Source.ActorType != f.ActorType || r.Source.ActorID != f.ActorID) {
		return false
	}
	var rt, rid, st, sid string
	if c := r.Change.Relationship; c != nil {
		rt, rid, st, sid = c.ResourceType, c.ResourceID, c.SubjectType, c.SubjectID
	} else if c := r.Change.Role; c != nil {
		rt, rid, st, sid = c.ResourceType, c.ResourceID, c.SubjectType, c.SubjectID
	}
	return (f.ResourceType == "" || (rt == f.ResourceType && rid == f.ResourceID)) && (f.SubjectType == "" || (st == f.SubjectType && sid == f.SubjectID))
}
