package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
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
func orderAllowed(field string, fields map[string]list.OrderField) bool {
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

var _ roles.Storer = (*Roles)(nil)

// Assign inserts an assignment. It is idempotent: a duplicate (exact 5-tuple) is
// a no-op. Identity is the exact five-field assignment.
func (r *Roles) Assign(ctx context.Context, a roles.Assignment) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Roles{st: next}).assignLocked(ctx, a)
	})
}

func (r *Roles) assignLocked(ctx context.Context, a roles.Assignment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.index(a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID) >= 0 {
		return nil
	}
	r.st.role = append(r.st.role, roleRow{
		subjectType:  a.SubjectType,
		subjectID:    a.SubjectID,
		role:         a.Role,
		resourceType: a.ResourceType,
		resourceID:   a.ResourceID,
	})
	return nil
}

// Unassign removes an exact assignment (idempotent — absent is nil).
func (r *Roles) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	return r.st.write(ctx, func(next *state) error {
		return (&Roles{st: next}).unassignLocked(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
	})
}

func (r *Roles) unassignLocked(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
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
func (r *Roles) ListBySubject(ctx context.Context, subjectType, subjectID string, req list.Request) (list.Page[roles.Assignment], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []roles.Assignment
	for _, row := range r.st.role {
		if row.subjectType == subjectType && row.subjectID == subjectID {
			items = append(items, row.toAssignment())
		}
	}
	return pageMemByKey(items, req, roles.OrderFields, "role_key", assignmentKey)
}

// ListByResource pages the assignments scoped to a resource (direct-scope only).
func (r *Roles) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.Assignment], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	var items []roles.Assignment
	for _, row := range r.st.role {
		if row.resourceType == resourceType && row.resourceID == resourceID {
			items = append(items, row.toAssignment())
		}
	}
	return pageMemByKey(items, req, roles.OrderFields, "role_key", assignmentKey)
}

// LookupResourceIDsBySubjectAndRoles reports a global grant of any of roles as
// unrestricted, else the sorted distinct scoped resource ids of resourceType at
// which the subject holds any of roles, strictly after `after`, capped at limit.
func (r *Roles) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	if len(roles) == 0 {
		return nil, false, nil
	}
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	granting := make(map[string]bool, len(roles))
	for _, name := range roles {
		granting[name] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, row := range r.st.role {
		if row.subjectType != subjectType || row.subjectID != subjectID || !granting[row.role] {
			continue
		}
		if row.resourceType == "" && row.resourceID == "" {
			return nil, true, nil
		}
		if row.resourceType == resourceType && row.resourceID != "" && !seen[row.resourceID] {
			seen[row.resourceID] = true
			out = append(out, row.resourceID)
		}
	}
	sort.Strings(out)
	out = afterIDs(out, after)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, false, nil
}

// ListEffectiveByResource pages the EFFECTIVE role grants on a resource: the
// union of the direct scoped assignments at (resourceType, resourceID) with the
// global assignments a scoped HasRole satisfies, de-duplicated by (subject,
// role) with provenance. A global request has no fallback (every grant is
// Direct), mirroring the service's HasRole no-fallback path for an unscoped
// query.
func (r *Roles) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.EffectiveGrant], error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()

	scoped := resourceType != "" || resourceID != ""
	byKey := map[string]*roles.EffectiveGrant{}
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
			g = &roles.EffectiveGrant{SubjectType: row.subjectType, SubjectID: row.subjectID, Role: row.role}
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

	items := make([]roles.EffectiveGrant, 0, len(order))
	for _, k := range order {
		items = append(items, *byKey[k])
	}
	return pageMemByKey(items, req, roles.EffectiveOrderFields, "grant_key", effectiveGrantKeyOf)
}

// hasRoleEffectiveLocked reads the held snapshot with the same exact-resource,
// then global fallback used by roles.HasRole. A global query has no fallback.
func (r *Roles) hasRoleEffectiveLocked(target mutations.Target, roleName, subjectType, subjectID string) bool {
	var resourceType, resourceID string
	if target.Kind == mutations.TargetResource {
		resourceType, resourceID = target.Type, target.ID
	}
	if r.index(subjectType, subjectID, roleName, resourceType, resourceID) >= 0 {
		return true
	}
	return target.Kind == mutations.TargetResource && r.index(subjectType, subjectID, roleName, "", "") >= 0
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

func (row roleRow) toAssignment() roles.Assignment {
	return roles.Assignment{
		SubjectType:  row.subjectType,
		SubjectID:    row.subjectID,
		Role:         row.role,
		ResourceType: row.resourceType,
		ResourceID:   row.resourceID,
	}
}

// assignmentKey orders the validated full role assignment in byte order.
func assignmentKey(a roles.Assignment) string {
	return strings.Join([]string{a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID}, "\x01")
}

// effectiveGrantKey is the deterministic (subject_type, subject_id, role)
// ordering key for the effective listing. The SQL stores reproduce it as their
// derived grant_key column so pagination is stable within each backend.
func effectiveGrantKey(subjectType, subjectID, roleName string) string {
	return strings.Join([]string{subjectType, subjectID, roleName}, "\x00")
}

// effectiveGrantKeyOf returns an effective grant's ordering/keyset key.
func effectiveGrantKeyOf(g roles.EffectiveGrant) string {
	return effectiveGrantKey(g.SubjectType, g.SubjectID, g.Role)
}

// =============================================================================
// Shared in-memory keyset paginator
// =============================================================================

// pageMemByKey paginates items by a single deterministic string key (the
// relationship tuple_key, role assignment role_key, or effective grant_key).
// Order field == pk, so the cursor carries the key as both
// the order value and the pk. It rejects an order field absent from fields with
// sdk.ErrInvalidInput, exactly as the connectors' resolveOrder does. Direction
// defaults to ASC (the zero Order), matching role.DefaultEffectiveOrder.
func pageMemByKey[T any](all []T, req list.Request, fields map[string]list.OrderField, keyField string, keyOf func(T) string) (list.Page[T], error) {
	if err := req.Validate(); err != nil {
		return list.Page[T]{}, err
	}
	if req.Order.Field != "" && !orderAllowed(req.Order.Field, fields) {
		return list.Page[T]{}, unknownOrderField(req.Order.Field)
	}
	asc := true
	if req.Order.Field != "" {
		asc = req.Order.Direction != list.DESC
	}

	sort.SliceStable(all, func(i, j int) bool {
		ki, kj := keyOf(all[i]), keyOf(all[j])
		if asc {
			return ki < kj
		}
		return ki > kj
	})

	total := int64(len(all))
	limit := req.NormalizedLimit(list.Limits{})
	encode := func(item T) (string, error) {
		k := keyOf(item)
		return list.EncodeCursor(keyField, k, k)
	}

	if req.ResolvedStrategy() == list.StrategyOffset {
		window := all
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		page, err := list.TrimPage(window, limit, encode)
		if err != nil {
			return list.Page[T]{}, err
		}
		page.NextCursor = ""
		page.HasPrev = req.Offset > 0
		if req.WithCount {
			page.Total = &total
		}
		return page, nil
	}

	cur, err := list.DecodeCursor(req.Cursor, keyField)
	if err != nil {
		return list.Page[T]{}, err
	}

	var curKey string
	forward := all
	if cur != nil {
		var ok bool
		curKey, ok = cur.OrderValue.(string)
		if !ok {
			return list.Page[T]{}, fmt.Errorf("cursor order value must be a string: %w", sdk.ErrInvalidInput)
		}
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
	page, err := list.TrimPage(window, limit, encode)
	if err != nil {
		return list.Page[T]{}, err
	}

	if cur != nil {
		var before []T
		for _, item := range all {
			if !afterKeyMem(keyOf(item), curKey, asc) {
				before = append(before, item)
			}
		}
		if len(before) > limit+1 {
			before = before[len(before)-limit-1:]
		}
		if err := list.MarkPrevPage(&page, before, limit, encode); err != nil {
			return list.Page[T]{}, err
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
