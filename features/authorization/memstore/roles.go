package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// unknownOrderField is the error pageMem returns for an order field absent from
// the kind's rim allow-list — the same sdk.ErrInvalidInput-class error the SQL
// stores' resolveOrder produces, so storetest asserts one rejection shape across
// every backend.
func unknownOrderField(field string) error {
	return fmt.Errorf("unknown order field %q: %w", field, sdk.ErrInvalidInput)
}

// orderAllowed reports whether field names a column in the kind's rim allow-list,
// mirroring the connectors' resolveOrder membership check (match by column).
func orderAllowed(field string, fields map[string]crud.OrderField) bool {
	for _, of := range fields {
		if of.Column == field {
			return true
		}
	}
	return false
}

// roleRow is one stored role assignment. The empty (resourceType, resourceID)
// pair is a global grant.
type roleRow struct {
	subjectType  string
	subjectID    string
	role         string
	resourceType string
	resourceID   string
	createdAt    time.Time
}

// Roles is the in-core role.Storer: plain mutex-backed maps, exact-scope lookups,
// no graph walk.
type Roles struct {
	st *state
}

// NewRoles builds an empty roles store over its own private state. Use [New] when
// the role store must share one lock and one snapshot with the relationship and
// mutation stores (the atomic write path).
func NewRoles() *Roles {
	return &Roles{st: newState()}
}

var _ role.Storer = (*Roles)(nil)

// Assign inserts an assignment. It is idempotent: a duplicate (exact 5-tuple) is
// a no-op that RETAINS the original CreatedAt (the ON CONFLICT DO NOTHING mirror).
func (r *Roles) Assign(ctx context.Context, a role.Assignment) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if r.index(a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID) >= 0 {
		return nil
	}
	r.st.role = append(r.st.role, roleRow{
		subjectType:  a.SubjectType,
		subjectID:    a.SubjectID,
		role:         a.Role,
		resourceType: a.ResourceType,
		resourceID:   a.ResourceID,
		createdAt:    time.Now().UTC(),
	})
	return nil
}

// Unassign removes an exact assignment (idempotent — absent is nil).
func (r *Roles) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	i := r.index(subjectType, subjectID, roleName, resourceType, resourceID)
	if i < 0 {
		return nil
	}
	r.st.role = append(r.st.role[:i], r.st.role[i+1:]...)
	return nil
}

// HasExactRole reports whether an assignment exists at the EXACT scope.
func (r *Roles) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	return r.index(subjectType, subjectID, roleName, resourceType, resourceID) >= 0, nil
}

// ListBySubject pages a subject's assignments.
func (r *Roles) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []role.Assignment
	for _, row := range r.st.role {
		if row.subjectType == subjectType && row.subjectID == subjectID {
			items = append(items, row.toAssignment())
		}
	}
	return pageMem(items, req, role.OrderFields, assignmentKey)
}

// ListByResource pages the assignments scoped to a resource (direct-scope only).
func (r *Roles) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []role.Assignment
	for _, row := range r.st.role {
		if row.resourceType == resourceType && row.resourceID == resourceID {
			items = append(items, row.toAssignment())
		}
	}
	return pageMem(items, req, role.OrderFields, assignmentKey)
}

// ListEffectiveByResource pages the EFFECTIVE role grants on a resource: the
// union of the direct scoped assignments at (resourceType, resourceID) with the
// global assignments a scoped HasRole satisfies, de-duplicated by (subject,
// role) with provenance. A global request has no fallback (every grant is
// Direct), mirroring the service's HasRole no-fallback path for an unscoped
// query.
func (r *Roles) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()

	scoped := resourceType != "" || resourceID != ""
	byKey := map[string]*role.EffectiveGrant{}
	var order []string
	for _, row := range r.st.role {
		directMatch := row.resourceType == resourceType && row.resourceID == resourceID
		globalMatch := scoped && row.resourceType == "" && row.resourceID == ""
		if !directMatch && !globalMatch {
			continue
		}
		k := effectiveGrantKey(row.subjectType, row.subjectID, row.role)
		g := byKey[k]
		if g == nil {
			g = &role.EffectiveGrant{SubjectType: row.subjectType, SubjectID: row.subjectID, Role: row.role}
			byKey[k] = g
			order = append(order, k)
		}
		if directMatch {
			g.Direct = true
		}
		if globalMatch {
			g.Global = true
		}
	}

	items := make([]role.EffectiveGrant, 0, len(order))
	for _, k := range order {
		items = append(items, *byKey[k])
	}
	return pageMemByKey(items, req, role.EffectiveOrderFields, effectiveGrantKeyOf)
}

// hasRoleEffectiveScopesLocked is the non-locking EFFECTIVE role read the mutation
// repository's DecisionView uses (caller holds st.mu): an exact-scope match, plus
// the service-level global fallback (a global assignment satisfies a scoped
// query), so a guard reasons about effective access exactly as rolesvc.HasRole
// does. A subject-scoped (global) query has no fallback. It also reports whether
// the global fallback was consulted — the exact-resource check missed on a
// resource-scoped query — so the DecisionView records the subject scope in that
// case; a concurrent global grant/revoke then invalidates a guarded decision.
func (r *Roles) hasRoleEffectiveScopesLocked(scope mutation.ScopeKey, roleName, subjectType, subjectID string) (has, consultedGlobal bool) {
	var resourceType, resourceID string
	if scope.Kind == mutation.ScopeResource {
		resourceType, resourceID = scope.Type, scope.ID
	}
	if r.index(subjectType, subjectID, roleName, resourceType, resourceID) >= 0 {
		return true, false
	}
	if scope.Kind == mutation.ScopeResource {
		return r.index(subjectType, subjectID, roleName, "", "") >= 0, true
	}
	return false, false
}

// index returns the row position of an exact 5-tuple, or -1. Caller holds lock.
func (r *Roles) index(subjectType, subjectID, roleName, resourceType, resourceID string) int {
	for i, row := range r.st.role {
		if row.subjectType == subjectType && row.subjectID == subjectID && row.role == roleName &&
			row.resourceType == resourceType && row.resourceID == resourceID {
			return i
		}
	}
	return -1
}

func (row roleRow) toAssignment() role.Assignment {
	return role.Assignment{
		SubjectType:  row.subjectType,
		SubjectID:    row.subjectID,
		Role:         row.role,
		ResourceType: row.resourceType,
		ResourceID:   row.resourceID,
		CreatedAt:    row.createdAt,
	}
}

// assignmentKey is the keyset (created_at, tiebreak) for a role assignment. With
// no surrogate id, the tiebreak is the assignment's own 5-tuple composite — a
// stable deterministic key the SQL stores reproduce in their ORDER BY.
func assignmentKey(a role.Assignment) (time.Time, string) {
	return a.CreatedAt, strings.Join([]string{a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID}, "\x00")
}

// effectiveGrantKey is the deterministic (subject_type, subject_id, role)
// ordering key for the effective listing. The SQL stores reproduce it as their
// derived grant_key column so pagination is stable within each backend.
func effectiveGrantKey(subjectType, subjectID, roleName string) string {
	return strings.Join([]string{subjectType, subjectID, roleName}, "\x00")
}

// effectiveGrantKeyOf returns an effective grant's ordering/keyset key.
func effectiveGrantKeyOf(g role.EffectiveGrant) string {
	return effectiveGrantKey(g.SubjectType, g.SubjectID, g.Role)
}

// =============================================================================
// Shared in-memory keyset paginator
// =============================================================================

// pageMem paginates items by the contractual order (created_at DESC, tiebreak
// DESC) with cursor and offset strategies, mirroring the SQL stores' keyset
// contract so storetest proves the same shape against every backend. It rejects
// an order field absent from the kind's rim allow-list (fields) with
// sdk.ErrInvalidInput, exactly as the connectors' resolveOrder does. keyOf
// returns each item's (created_at, tiebreak-pk).
func pageMem[T any](all []T, req crud.ListRequest, fields map[string]crud.OrderField, keyOf func(T) (time.Time, string)) (crud.Page[T], error) {
	if err := req.Validate(); err != nil {
		return crud.Page[T]{}, err
	}
	if req.Order.Field != "" && !orderAllowed(req.Order.Field, fields) {
		return crud.Page[T]{}, unknownOrderField(req.Order.Field)
	}
	asc := req.Order.Direction == crud.ASC

	sort.SliceStable(all, func(i, j int) bool {
		ti, idi := keyOf(all[i])
		tj, idj := keyOf(all[j])
		if !ti.Equal(tj) {
			if asc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if asc {
			return idi < idj
		}
		return idi > idj
	})

	total := int64(len(all))
	limit := req.NormalizedLimit(crud.Limits{})
	encode := func(item T) (string, error) {
		t, itemID := keyOf(item)
		return crud.EncodeCursor("created_at", t, itemID)
	}

	if req.ResolvedStrategy() == crud.StrategyOffset {
		window := all
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		page, err := crud.TrimPage(window, limit, encode)
		if err != nil {
			return crud.Page[T]{}, err
		}
		page.NextCursor = ""
		page.HasPrev = req.Offset > 0
		if req.WithCount {
			page.Total = &total
		}
		return page, nil
	}

	cur, err := crud.DecodeCursor(req.Cursor, "created_at")
	if err != nil {
		return crud.Page[T]{}, err
	}

	forward := all
	if cur != nil {
		curTime, _ := cur.OrderValue.(time.Time)
		forward = forward[:0:0]
		for _, item := range all {
			t, itemID := keyOf(item)
			if afterCursorMem(t, itemID, curTime, cur.PK, asc) {
				forward = append(forward, item)
			}
		}
	}
	window := forward
	if len(window) > limit+1 {
		window = window[:limit+1]
	}
	page, err := crud.TrimPage(window, limit, encode)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if cur != nil {
		curTime, _ := cur.OrderValue.(time.Time)
		var before []T
		for _, item := range all {
			t, itemID := keyOf(item)
			if beforeCursorMem(t, itemID, curTime, cur.PK, asc) {
				before = append(before, item)
			}
		}
		if len(before) > limit {
			before = before[len(before)-limit:]
		}
		if err := crud.MarkPrevPage(&page, before, limit, encode); err != nil {
			return crud.Page[T]{}, err
		}
	}

	if req.WithCount {
		page.Total = &total
	}
	return page, nil
}

// pageMemByKey paginates items by a single deterministic string key (the
// effective listing's grant_key), mirroring the SQL stores' keyset contract for
// the effective family: order field == pk, so the cursor carries the key as both
// the order value and the pk. It rejects an order field absent from fields with
// sdk.ErrInvalidInput, exactly as the connectors' resolveOrder does. Direction
// defaults to ASC (the zero Order), matching role.DefaultEffectiveOrder.
func pageMemByKey[T any](all []T, req crud.ListRequest, fields map[string]crud.OrderField, keyOf func(T) string) (crud.Page[T], error) {
	if err := req.Validate(); err != nil {
		return crud.Page[T]{}, err
	}
	if req.Order.Field != "" && !orderAllowed(req.Order.Field, fields) {
		return crud.Page[T]{}, unknownOrderField(req.Order.Field)
	}
	asc := true
	if req.Order.Field != "" {
		asc = req.Order.Direction != crud.DESC
	}

	sort.SliceStable(all, func(i, j int) bool {
		ki, kj := keyOf(all[i]), keyOf(all[j])
		if asc {
			return ki < kj
		}
		return ki > kj
	})

	total := int64(len(all))
	limit := req.NormalizedLimit(crud.Limits{})
	encode := func(item T) (string, error) {
		k := keyOf(item)
		return crud.EncodeCursor("grant_key", k, k)
	}

	if req.ResolvedStrategy() == crud.StrategyOffset {
		window := all
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		page, err := crud.TrimPage(window, limit, encode)
		if err != nil {
			return crud.Page[T]{}, err
		}
		page.NextCursor = ""
		page.HasPrev = req.Offset > 0
		if req.WithCount {
			page.Total = &total
		}
		return page, nil
	}

	cur, err := crud.DecodeCursor(req.Cursor, "grant_key")
	if err != nil {
		return crud.Page[T]{}, err
	}

	forward := all
	if cur != nil {
		curKey, _ := cur.OrderValue.(string)
		forward = forward[:0:0]
		for _, item := range all {
			if afterKeyMem(keyOf(item), curKey, asc) {
				forward = append(forward, item)
			}
		}
	}
	window := forward
	if len(window) > limit+1 {
		window = window[:limit+1]
	}
	page, err := crud.TrimPage(window, limit, encode)
	if err != nil {
		return crud.Page[T]{}, err
	}

	if cur != nil {
		curKey, _ := cur.OrderValue.(string)
		var before []T
		for _, item := range all {
			if afterKeyMem(curKey, keyOf(item), asc) {
				before = append(before, item)
			}
		}
		if len(before) > limit {
			before = before[len(before)-limit:]
		}
		if err := crud.MarkPrevPage(&page, before, limit, encode); err != nil {
			return crud.Page[T]{}, err
		}
	}

	if req.WithCount {
		page.Total = &total
	}
	return page, nil
}

// afterKeyMem reports whether itemKey sorts strictly after curKey in the
// traversal direction (asc → greater keys, desc → lesser keys).
func afterKeyMem(itemKey, curKey string, asc bool) bool {
	if asc {
		return itemKey > curKey
	}
	return itemKey < curKey
}

func afterCursorMem(itemTime time.Time, itemID string, curTime time.Time, curID string, asc bool) bool {
	if !itemTime.Equal(curTime) {
		if asc {
			return itemTime.After(curTime)
		}
		return itemTime.Before(curTime)
	}
	if asc {
		return itemID > curID
	}
	return itemID < curID
}

func beforeCursorMem(itemTime time.Time, itemID string, curTime time.Time, curID string, asc bool) bool {
	if !itemTime.Equal(curTime) {
		if asc {
			return itemTime.Before(curTime)
		}
		return itemTime.After(curTime)
	}
	if asc {
		return itemID < curID
	}
	return itemID > curID
}
