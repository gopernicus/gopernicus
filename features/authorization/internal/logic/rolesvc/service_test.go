package rolesvc

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// fakeRoleStore is an in-package role.Storer: a set of exact assignments plus an
// optional injected error to prove fail-closed propagation.
type fakeRoleStore struct {
	rows map[string]bool // key: type|id|role|rtype|rid
	err  error
}

func key(subjectType, subjectID, roleName, resourceType, resourceID string) string {
	return subjectType + "|" + subjectID + "|" + roleName + "|" + resourceType + "|" + resourceID
}

func (f *fakeRoleStore) Assign(ctx context.Context, a role.Assignment) error {
	if f.err != nil {
		return f.err
	}
	if f.rows == nil {
		f.rows = map[string]bool{}
	}
	f.rows[key(a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID)] = true
	return nil
}

func (f *fakeRoleStore) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if f.err != nil {
		return f.err
	}
	delete(f.rows, key(subjectType, subjectID, roleName, resourceType, resourceID))
	return nil
}

func (f *fakeRoleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.rows[key(subjectType, subjectID, roleName, resourceType, resourceID)], nil
}

func (f *fakeRoleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, f.err
}

func (f *fakeRoleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	return crud.Page[role.Assignment]{}, f.err
}

// ListEffectiveByResource unions the direct scoped rows with the global rows a
// scoped query would fall back to, keyed by (subject, role) with provenance — a
// faithful-enough reference for the service delegation/validation tests.
func (f *fakeRoleStore) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	if f.err != nil {
		return crud.Page[role.EffectiveGrant]{}, f.err
	}
	scoped := resourceType != "" || resourceID != ""
	byKey := map[string]*role.EffectiveGrant{}
	var order []string
	for k := range f.rows {
		parts := strings.SplitN(k, "|", 5)
		st, sid, roleName, rt, rid := parts[0], parts[1], parts[2], parts[3], parts[4]
		directMatch := rt == resourceType && rid == resourceID
		globalMatch := scoped && rt == "" && rid == ""
		if !directMatch && !globalMatch {
			continue
		}
		gk := st + "|" + sid + "|" + roleName
		g := byKey[gk]
		if g == nil {
			g = &role.EffectiveGrant{SubjectType: st, SubjectID: sid, Role: roleName}
			byKey[gk] = g
			order = append(order, gk)
		}
		if directMatch {
			g.Direct = true
		}
		if globalMatch {
			g.Global = true
		}
	}
	sort.Strings(order)
	items := make([]role.EffectiveGrant, 0, len(order))
	for _, gk := range order {
		items = append(items, *byKey[gk])
	}
	return crud.Page[role.EffectiveGrant]{Items: items}, nil
}

func TestAssignRoleIdempotentPassThrough(t *testing.T) {
	store := &fakeRoleStore{}
	svc := NewService(store)
	for range 2 {
		if err := svc.AssignRole(context.Background(), "user", "u1", "editor", "doc", "d1"); err != nil {
			t.Fatalf("AssignRole: %v", err)
		}
	}
	if len(store.rows) != 1 {
		t.Fatalf("duplicate assign must yield one row, got %d", len(store.rows))
	}
}

func TestValidationRejections(t *testing.T) {
	svc := NewService(&fakeRoleStore{})
	if err := svc.AssignRole(context.Background(), "", "u1", "editor", "", ""); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("empty subject type: want ErrInvalidRoleAssignment, got %v", err)
	}
	if err := svc.AssignRole(context.Background(), "user", "u1", "", "", ""); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("empty role: want ErrInvalidRoleAssignment, got %v", err)
	}
	if err := svc.AssignRole(context.Background(), "user", "u1", "editor", "doc", ""); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("half-scoped (rtype only): want ErrHalfScopedAssignment, got %v", err)
	}
	if err := svc.UnassignRole(context.Background(), "user", "u1", "editor", "", "d1"); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("half-scoped (rid only): want ErrHalfScopedAssignment, got %v", err)
	}
}

func TestHasRoleScopeRule(t *testing.T) {
	store := &fakeRoleStore{}
	svc := NewService(store)

	// Global grant satisfies a scoped query (Q5 fallback), scoped query for a
	// DIFFERENT scope does not.
	if err := svc.AssignRole(context.Background(), "user", "u1", "editor", "", ""); err != nil {
		t.Fatalf("assign global: %v", err)
	}
	if ok, err := svc.HasRole(context.Background(), "user", "u1", "editor", "doc", "d1"); err != nil || !ok {
		t.Fatalf("global grant should satisfy scoped check: ok=%v err=%v", ok, err)
	}

	// A scoped grant satisfies its own scope but not another.
	if err := svc.AssignRole(context.Background(), "user", "u2", "viewer", "doc", "d1"); err != nil {
		t.Fatalf("assign scoped: %v", err)
	}
	if ok, _ := svc.HasRole(context.Background(), "user", "u2", "viewer", "doc", "d1"); !ok {
		t.Fatalf("scoped grant should satisfy its exact scope")
	}
	if ok, _ := svc.HasRole(context.Background(), "user", "u2", "viewer", "doc", "d2"); ok {
		t.Fatalf("scoped grant must NOT satisfy a different scope")
	}

	// A miss is (false, nil).
	if ok, err := svc.HasRole(context.Background(), "user", "nobody", "editor", "doc", "d1"); err != nil || ok {
		t.Fatalf("miss must be (false, nil): ok=%v err=%v", ok, err)
	}
}

func TestEffectiveEnumerationAgreesWithHasRole(t *testing.T) {
	store := &fakeRoleStore{}
	svc := NewService(store)
	ctx := context.Background()

	// u1: global auditor (satisfies a scoped HasRole via fallback, no direct row).
	// u2: direct scoped auditor. u3: BOTH direct and global.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	must(svc.AssignRole(ctx, "user", "u1", "auditor", "", ""))
	must(svc.AssignRole(ctx, "user", "u2", "auditor", "doc", "d1"))
	must(svc.AssignRole(ctx, "user", "u3", "auditor", "doc", "d1"))
	must(svc.AssignRole(ctx, "user", "u3", "auditor", "", ""))
	// A grant on a DIFFERENT scope must not leak into d1's enumeration.
	must(svc.AssignRole(ctx, "user", "u4", "auditor", "doc", "d2"))

	page, err := svc.ListEffectiveRoleGrantsByResource(ctx, "doc", "d1", crud.ListRequest{})
	if err != nil {
		t.Fatalf("ListEffectiveRoleGrantsByResource: %v", err)
	}

	prov := map[string]string{}
	for _, g := range page.Items {
		prov[g.SubjectID] = g.Provenance()
	}
	want := map[string]string{"u1": "global", "u2": "direct", "u3": "both"}
	if len(prov) != len(want) {
		t.Fatalf("effective set = %v, want subjects %v", prov, want)
	}
	for id, p := range want {
		if prov[id] != p {
			t.Fatalf("subject %s provenance = %q, want %q (set %v)", id, prov[id], p, prov)
		}
	}
	if _, leaked := prov["u4"]; leaked {
		t.Fatalf("a grant scoped to doc/d2 leaked into doc/d1 enumeration: %v", prov)
	}

	// Symmetry: every enumerated subject passes HasRole at the same scope, and
	// HasRole grants nobody the enumeration omits.
	for id := range want {
		if ok, err := svc.HasRole(ctx, "user", id, "auditor", "doc", "d1"); err != nil || !ok {
			t.Fatalf("HasRole(%s) = %v,%v; enumeration and decision must agree", id, ok, err)
		}
	}
	if ok, _ := svc.HasRole(ctx, "user", "u4", "auditor", "doc", "d1"); ok {
		t.Fatalf("HasRole must deny u4 on d1 just as enumeration omits it")
	}
}

func TestListValidationSymmetry(t *testing.T) {
	svc := NewService(&fakeRoleStore{})
	ctx := context.Background()

	if _, err := svc.ListRoleAssignmentsBySubject(ctx, "", "u1", crud.ListRequest{}); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("ListBySubject empty subject: want ErrInvalidRoleAssignment, got %v", err)
	}
	if _, err := svc.ListRoleAssignmentsByResource(ctx, "doc", "", crud.ListRequest{}); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("ListByResource half-scoped: want ErrHalfScopedAssignment, got %v", err)
	}
	if _, err := svc.ListEffectiveRoleGrantsByResource(ctx, "doc", "", crud.ListRequest{}); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("ListEffective half-scoped: want ErrHalfScopedAssignment, got %v", err)
	}
	// A global ("","") resource listing is a valid shape (not half-scoped).
	if _, err := svc.ListEffectiveRoleGrantsByResource(ctx, "", "", crud.ListRequest{}); err != nil {
		t.Fatalf("ListEffective global scope must be accepted, got %v", err)
	}
}

func TestHasRoleFailClosedOnStoreError(t *testing.T) {
	boom := errors.New("store down")
	svc := NewService(&fakeRoleStore{err: boom})
	ok, err := svc.HasRole(context.Background(), "user", "u1", "editor", "doc", "d1")
	if ok {
		t.Fatalf("store error must not grant access")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("store error must propagate, got %v", err)
	}
}

func TestHasRoleWhereProvenance(t *testing.T) {
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("assign: %v", err)
		}
	}

	store := &fakeRoleStore{}
	svc := NewService(store)

	// u1: global only. u2: scoped only. u3: BOTH.
	must(svc.AssignRole(ctx, "user", "u1", "editor", "", ""))
	must(svc.AssignRole(ctx, "user", "u2", "editor", "doc", "d1"))
	must(svc.AssignRole(ctx, "user", "u3", "editor", "", ""))
	must(svc.AssignRole(ctx, "user", "u3", "editor", "doc", "d1"))

	tests := []struct {
		name           string
		subjectID      string
		resourceType   string
		resourceID     string
		wantHeld       bool
		wantProvenance string
	}{
		{"scoped row matches exactly", "u2", "doc", "d1", true, role.ProvenanceDirect},
		{"global fallback satisfies a scoped query", "u1", "doc", "d1", true, role.ProvenanceGlobal},
		{"held at both scopes reports direct", "u3", "doc", "d1", true, role.ProvenanceDirect},
		{"a scoped row never satisfies another scope", "u2", "doc", "d2", false, ""},
		{"an unscoped query's exact row is the global one", "u1", "", "", true, role.ProvenanceDirect},
		{"a scoped grant is invisible to an unscoped query", "u2", "", "", false, ""},
		{"a miss reports no provenance", "nobody", "doc", "d1", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			held, provenance, err := svc.HasRoleWhere(ctx, "user", tt.subjectID, "editor", tt.resourceType, tt.resourceID)
			if err != nil {
				t.Fatalf("HasRoleWhere: %v", err)
			}
			if held != tt.wantHeld || provenance != tt.wantProvenance {
				t.Fatalf("HasRoleWhere = (%v, %q), want (%v, %q)", held, provenance, tt.wantHeld, tt.wantProvenance)
			}
			// HasRole is HasRoleWhere with the provenance dropped: one scope rule.
			ok, err := svc.HasRole(ctx, "user", tt.subjectID, "editor", tt.resourceType, tt.resourceID)
			if err != nil || ok != tt.wantHeld {
				t.Fatalf("HasRole = (%v, %v), want (%v, nil)", ok, err, tt.wantHeld)
			}
		})
	}
}

func TestHasRoleWhereValidatesLikeHasRole(t *testing.T) {
	svc := NewService(&fakeRoleStore{})
	ctx := context.Background()

	if _, _, err := svc.HasRoleWhere(ctx, "user", "", "editor", "doc", "d1"); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("want ErrInvalidRoleAssignment, got %v", err)
	}
	if _, _, err := svc.HasRoleWhere(ctx, "user", "u1", "", "doc", "d1"); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("want ErrInvalidRoleAssignment, got %v", err)
	}
	if _, _, err := svc.HasRoleWhere(ctx, "user", "u1", "editor", "doc", ""); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("want ErrHalfScopedAssignment, got %v", err)
	}
}

func TestHasRoleWhereFailClosedOnStoreError(t *testing.T) {
	boom := errors.New("store down")
	svc := NewService(&fakeRoleStore{err: boom})
	held, provenance, err := svc.HasRoleWhere(context.Background(), "user", "u1", "editor", "doc", "d1")
	if held || provenance != "" {
		t.Fatalf("a store error must not grant access, got (%v, %q)", held, provenance)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("store error must propagate, got %v", err)
	}
}
