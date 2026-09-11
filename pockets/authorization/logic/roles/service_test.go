package roles

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// fakeRoleStore is an in-package role.Storer: a set of exact assignments plus an
// optional injected error to prove fail-closed propagation.
type fakeRoleStore struct {
	rows map[string]bool // key: type|id|role|rtype|rid
	err  error

	lastLookup *lookupArgs // the arguments of the last resource-id lookup
}

// lookupArgs captures one LookupResourceIDsBySubjectAndRoles call, so the
// passthrough can be proven to forward every argument unchanged.
type lookupArgs struct {
	subjectType, subjectID, resourceType string
	roles                                []string
	after                                string
	limit                                int
}

func key(subjectType, subjectID, roleName, resourceType, resourceID string) string {
	return subjectType + "|" + subjectID + "|" + roleName + "|" + resourceType + "|" + resourceID
}

func (f *fakeRoleStore) Assign(ctx context.Context, a Assignment) error {
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

func (f *fakeRoleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req list.Request) (list.Page[Assignment], error) {
	return list.Page[Assignment]{}, f.err
}

func (f *fakeRoleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[Assignment], error) {
	return list.Page[Assignment]{}, f.err
}

// ListEffectiveByResource unions the direct scoped rows with the global rows a
// scoped query would fall back to, keyed by (subject, role) with provenance — a
// faithful-enough reference for the service delegation/validation tests.
func (f *fakeRoleStore) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	f.lastLookup = &lookupArgs{subjectType, subjectID, resourceType, roles, after, limit}
	if f.err != nil {
		return nil, false, f.err
	}
	return []string{"r2", "r3"}, false, nil
}

func (f *fakeRoleStore) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[EffectiveGrant], error) {
	if f.err != nil {
		return list.Page[EffectiveGrant]{}, f.err
	}
	scoped := resourceType != "" || resourceID != ""
	byKey := map[string]*EffectiveGrant{}
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
			g = &EffectiveGrant{SubjectType: st, SubjectID: sid, Role: roleName}
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
	items := make([]EffectiveGrant, 0, len(order))
	for _, gk := range order {
		items = append(items, *byKey[gk])
	}
	return list.Page[EffectiveGrant]{Items: items}, nil
}

func TestAssignRoleIdempotentPassThrough(t *testing.T) {
	store := &fakeRoleStore{}
	svc := newRoleFixture(t, store)
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
	svc := newRoleFixture(t, &fakeRoleStore{})
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
	svc := newRoleFixture(t, store)

	// Global grant satisfies a scoped query (Q5 fallback), scoped query for a
	// DIFFERENT scope does not.
	if err := svc.AssignRole(context.Background(), "user", "u1", "editor", "", ""); err != nil {
		t.Fatalf("assign global: %v", err)
	}
	if ok, err := svc.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "editor", "doc", "d1"); err != nil || !ok {
		t.Fatalf("global grant should satisfy scoped check: ok=%v err=%v", ok, err)
	}

	// A scoped grant satisfies its own scope but not another.
	if err := svc.AssignRole(context.Background(), "user", "u2", "viewer", "doc", "d1"); err != nil {
		t.Fatalf("assign scoped: %v", err)
	}
	if ok, _ := svc.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u2"}, "viewer", "doc", "d1"); !ok {
		t.Fatalf("scoped grant should satisfy its exact scope")
	}
	if ok, _ := svc.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u2"}, "viewer", "doc", "d2"); ok {
		t.Fatalf("scoped grant must NOT satisfy a different scope")
	}

	// A miss is (false, nil).
	if ok, err := svc.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "nobody"}, "editor", "doc", "d1"); err != nil || ok {
		t.Fatalf("miss must be (false, nil): ok=%v err=%v", ok, err)
	}
}

func TestEffectiveEnumerationAgreesWithHasRole(t *testing.T) {
	store := &fakeRoleStore{}
	svc := newRoleFixture(t, store)
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

	page, err := svc.ListEffectiveRoleGrantsByResource(ctx, "doc", "d1", list.Request{})
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
		if ok, err := svc.HasRole(ctx, authmodel.PrincipalRef{Type: "user", ID: id}, "auditor", "doc", "d1"); err != nil || !ok {
			t.Fatalf("HasRole(%s) = %v,%v; enumeration and decision must agree", id, ok, err)
		}
	}
	if ok, _ := svc.HasRole(ctx, authmodel.PrincipalRef{Type: "user", ID: "u4"}, "auditor", "doc", "d1"); ok {
		t.Fatalf("HasRole must deny u4 on d1 just as enumeration omits it")
	}
}

func TestListValidationSymmetry(t *testing.T) {
	svc := newRoleFixture(t, &fakeRoleStore{})
	ctx := context.Background()

	if _, err := svc.ListRoleAssignmentsBySubject(ctx, authmodel.PrincipalRef{Type: "", ID: "u1"}, list.Request{}); !errors.Is(err, ErrInvalidRoleAssignment) {
		t.Fatalf("ListBySubject empty subject: want ErrInvalidRoleAssignment, got %v", err)
	}
	if _, err := svc.ListRoleAssignmentsByResource(ctx, "doc", "", list.Request{}); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("ListByResource half-scoped: want ErrHalfScopedAssignment, got %v", err)
	}
	if _, err := svc.ListEffectiveRoleGrantsByResource(ctx, "doc", "", list.Request{}); !errors.Is(err, ErrHalfScopedAssignment) {
		t.Fatalf("ListEffective half-scoped: want ErrHalfScopedAssignment, got %v", err)
	}
	// A global ("","") resource listing is a valid shape (not half-scoped).
	if _, err := svc.ListEffectiveRoleGrantsByResource(ctx, "", "", list.Request{}); err != nil {
		t.Fatalf("ListEffective global scope must be accepted, got %v", err)
	}
}

func TestHasRoleFailClosedOnStoreError(t *testing.T) {
	boom := errors.New("store down")
	svc := newRoleFixture(t, &fakeRoleStore{err: boom})
	ok, err := svc.HasRole(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "editor", "doc", "d1")
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
	svc := newRoleFixture(t, store)

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
		{"scoped row matches exactly", "u2", "doc", "d1", true, ProvenanceDirect},
		{"global fallback satisfies a scoped query", "u1", "doc", "d1", true, ProvenanceGlobal},
		{"held at both scopes reports direct", "u3", "doc", "d1", true, ProvenanceDirect},
		{"a scoped row never satisfies another scope", "u2", "doc", "d2", false, ""},
		{"an unscoped query's exact row is the global one", "u1", "", "", true, ProvenanceDirect},
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
			ok, err := svc.HasRole(ctx, authmodel.PrincipalRef{Type: "user", ID: tt.subjectID}, "editor", tt.resourceType, tt.resourceID)
			if err != nil || ok != tt.wantHeld {
				t.Fatalf("HasRole = (%v, %v), want (%v, nil)", ok, err, tt.wantHeld)
			}
		})
	}
}

func TestHasRoleWhereValidatesLikeHasRole(t *testing.T) {
	svc := newRoleFixture(t, &fakeRoleStore{})
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
	svc := newRoleFixture(t, &fakeRoleStore{err: boom})
	held, provenance, err := svc.HasRoleWhere(context.Background(), "user", "u1", "editor", "doc", "d1")
	if held || provenance != "" {
		t.Fatalf("a store error must not grant access, got (%v, %q)", held, provenance)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("store error must propagate, got %v", err)
	}
}

// TestLookupResourceIDsBySubjectAndRolesPassesThrough proves the roles kind's
// resource-id lookup is a PASSTHROUGH: every argument reaches the store
// unchanged (the caller passes the compiled granting roles — this service holds
// no model), the store's answer is returned verbatim, and a store failure is
// propagated rather than read as "no access".
func TestLookupResourceIDsBySubjectAndRolesPassesThrough(t *testing.T) {
	store := &fakeRoleStore{}
	svc := newRoleFixture(t, store)

	ids, unrestricted, err := svc.LookupResourceIDsBySubjectAndRoles(context.Background(),
		"user", "u1", "organization", []string{"viewer", "steward"}, "r1", 3)
	if err != nil {
		t.Fatalf("LookupResourceIDsBySubjectAndRoles: %v", err)
	}
	if unrestricted || !reflect.DeepEqual(ids, []string{"r2", "r3"}) {
		t.Fatalf("got (%v, %v), want the store answer verbatim", ids, unrestricted)
	}
	want := &lookupArgs{"user", "u1", "organization", []string{"viewer", "steward"}, "r1", 3}
	if !reflect.DeepEqual(store.lastLookup, want) {
		t.Fatalf("store received %+v, want %+v", store.lastLookup, want)
	}

	failing := newRoleFixture(t, &fakeRoleStore{err: errors.New("store exploded")})
	if _, _, err := failing.LookupResourceIDsBySubjectAndRoles(context.Background(),
		"user", "u1", "organization", []string{"viewer"}, "", 3); err == nil {
		t.Fatal("a store failure must never read as an empty page")
	}
}

// TestLookupResourceIDsBySubjectAndRolesValidatesItsRefFields proves the three
// reference fields are validated before any store read — the same emptiness rule
// the other methods apply, plus the type scope a lookup always needs.
func TestLookupResourceIDsBySubjectAndRolesValidatesItsRefFields(t *testing.T) {
	store := &fakeRoleStore{}
	svc := newRoleFixture(t, store)

	cases := map[string]struct{ subjectType, subjectID, resourceType string }{
		"empty subject type":  {"", "u1", "organization"},
		"empty subject id":    {"user", "", "organization"},
		"empty resource type": {"user", "u1", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.LookupResourceIDsBySubjectAndRoles(context.Background(),
				tc.subjectType, tc.subjectID, tc.resourceType, []string{"viewer"}, "", 3)
			if !errors.Is(err, ErrInvalidRoleAssignment) {
				t.Fatalf("want ErrInvalidRoleAssignment, got %v", err)
			}
			if store.lastLookup != nil {
				t.Fatalf("no store read may happen before validation, got %+v", store.lastLookup)
			}
		})
	}
}
