package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// fixtureScope translates shorthand used by fixture tables into explicit scopes.
// Production constructors never infer global from missing resource coordinates.
func fixtureScope(rt, rid string) tuples.Scope {
	if rt == "" && rid == "" {
		return tuples.Global()
	}
	return tuples.On(rt, rid)
}
func roleFact(st, sid, label, rt, rid string) tuples.Tuple {
	return tuples.Tuple{Scope: fixtureScope(rt, rid), Relation: label, Subject: tuples.SubjectRef{Type: st, ID: sid}}
}
func assignRole(ctx context.Context, s tuples.Storer, a roles.Assignment) error {
	w, e := roles.NewWriter(s)
	if e != nil {
		return e
	}
	return w.AssignRole(ctx, a)
}
func unassignRole(ctx context.Context, s tuples.Storer, st, sid, label, rt, rid string) error {
	w, e := roles.NewWriter(s)
	if e != nil {
		return e
	}
	return w.UnassignRole(ctx, roles.Assignment{SubjectType: st, SubjectID: sid, Role: label, Scope: fixtureScope(rt, rid)})
}
func roleListSubject(ctx context.Context, s tuples.Storer, st, sid string, req listing.Request) (listing.Page[roles.Assignment], error) {
	r, e := roles.NewService(s)
	if e != nil {
		return listing.Page[roles.Assignment]{}, e
	}
	return r.ListRoleAssignmentsBySubject(ctx, authmodel.PrincipalRef{Type: st, ID: sid}, req)
}
func roleListResource(ctx context.Context, s tuples.Storer, rt, rid string, req listing.Request) (listing.Page[roles.Assignment], error) {
	r, e := roles.NewService(s)
	if e != nil {
		return listing.Page[roles.Assignment]{}, e
	}
	return r.ListRoleAssignmentsByScope(ctx, fixtureScope(rt, rid), req)
}
func assign(t *testing.T, s tuples.Storer, st, sid, label, rt, rid string) {
	t.Helper()
	if err := assignRole(context.Background(), s, roles.Assignment{SubjectType: st, SubjectID: sid, Role: label, Scope: fixtureScope(rt, rid)}); err != nil {
		t.Fatal(err)
	}
}
func hasExact(t *testing.T, s tuples.Reader, st, sid, label, rt, rid string) bool {
	t.Helper()
	got, err := s.Contains(context.Background(), roleFact(st, sid, label, rt, rid))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func runRoles(t *testing.T, newRepos func(*testing.T) Repositories) {
	ctx := context.Background()
	t.Run("NaturalOrder", func(t *testing.T) { runRoleNaturalOrder(t, newRepos) })
	t.Run("ReferenceValidation", func(t *testing.T) { runRoleReferenceValidation(t, newRepos) })
	t.Run("ExactScopeAndExplicitFallback", func(t *testing.T) {
		r := newRepos(t)
		svc := newServiceFor(t, r)
		p := authmodel.PrincipalRef{Type: "user", ID: "u"}
		resource := authmodel.Resource{Type: "doc", ID: "d"}
		assign(t, r.Tuples, "user", "u", "opaque-admin", "", "")
		if held, err := svc.Roles.HasRole(ctx, p, "opaque-admin"); err != nil || !held {
			t.Fatalf("global: %v/%v", held, err)
		}
		if held, err := svc.Roles.HasRoleIn(ctx, p, "opaque-admin", resource); err != nil || held {
			t.Fatalf("implicit fallback: %v/%v", held, err)
		}
		if held, err := svc.Roles.HasRoleInOrGlobal(ctx, p, "opaque-admin", resource); err != nil || !held {
			t.Fatalf("explicit fallback: %v/%v", held, err)
		}
		assign(t, r.Tuples, "user", "u", "opaque-admin", "doc", "d")
		if err := unassignRole(ctx, r.Tuples, "user", "u", "opaque-admin", "doc", "d"); err != nil {
			t.Fatal(err)
		}
		if held, err := svc.Roles.HasRoleIn(ctx, p, "opaque-admin", resource); err != nil || held {
			t.Fatalf("scoped revoke: %v/%v", held, err)
		}
		if !hasExact(t, r.Tuples, "user", "u", "opaque-admin", "", "") {
			t.Fatal("scoped revoke deleted global fact")
		}
	})
	t.Run("KindFreeMembershipAndIndependentLabels", func(t *testing.T) {
		r := newRepos(t)
		svc := newServiceFor(t, r)
		p := authmodel.PrincipalRef{Type: "user", ID: "u"}
		scope := authmodel.Resource{Type: "doc", ID: "d"}
		mustCreate(t, r.Relationships, ct("doc", "d", "owner", "user", "u"))
		assign(t, r.Tuples, "user", "u", "owner", "doc", "d")
		assign(t, r.Tuples, "user", "u", "member", "doc", "d")
		for _, name := range []string{"owner", "member"} {
			if held, err := svc.Roles.HasRoleIn(ctx, p, name, scope); err != nil || !held {
				t.Fatalf("%s: %v/%v", name, held, err)
			}
		}
		page, err := roleListSubject(ctx, r.Tuples, "user", "u", listing.Request{})
		if err != nil || len(page.Items) != 2 {
			t.Fatalf("one identity per fact: %+v/%v", page, err)
		}
		if err := unassignRole(ctx, r.Tuples, "user", "u", "owner", "doc", "d"); err != nil {
			t.Fatal(err)
		}
		if ok, err := r.Relationships.CheckRelationExists(ctx, "doc", "d", "owner", "user", "u"); err != nil || ok {
			t.Fatalf("role revoke left duplicate graph fact: %v/%v", ok, err)
		}
		if !hasExact(t, r.Tuples, "user", "u", "member", "doc", "d") {
			t.Fatal("owner revoke deleted member")
		}
		if err := unassignRole(ctx, r.Tuples, "user", "u", "owner", "doc", "d"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ExactRolesNeverExpandUsersets", func(t *testing.T) {
		r := newRepos(t)
		svc := newServiceFor(t, r)
		facts := []tuples.Tuple{{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}}, ct("group", "g", "member", "user", "u").Tuple()}
		if err := r.Tuples.ApplyTuples(ctx, tuples.Changes{Add: facts}); err != nil {
			t.Fatal(err)
		}
		if held, err := svc.Roles.HasRole(ctx, authmodel.PrincipalRef{Type: "user", ID: "u"}, "admin"); err != nil || held {
			t.Fatalf("expanded role: %v/%v", held, err)
		}
		page, err := svc.Roles.ListRoleAssignmentsByScope(ctx, tuples.Global(), listing.Request{})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("userset was listed as concrete role: %+v/%v", page, err)
		}
	})
	t.Run("ListPagination", func(t *testing.T) {
		s := newRepos(t).Tuples
		for _, label := range []string{"r1", "r2", "r3"} {
			assign(t, s, "user", "u", label, "doc", "d")
		}
		assertRoleCoverage(t, func(req listing.Request) (listing.Page[roles.Assignment], error) {
			return roleListSubject(ctx, s, "user", "u", req)
		}, 3)
		assertRoleCoverage(t, func(req listing.Request) (listing.Page[roles.Assignment], error) {
			return roleListResource(ctx, s, "doc", "d", req)
		}, 3)
		empty, err := roleListResource(ctx, s, "doc", "absent", listing.Request{})
		if err != nil || len(empty.Items) != 0 || empty.HasMore || empty.NextCursor != "" {
			t.Fatalf("empty page: %+v/%v", empty, err)
		}
		if _, err := roleListSubject(ctx, s, "user", "u", listing.Request{Order: listing.NewOrder("role", listing.ASC)}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unknown order: %v", err)
		}
	})
	t.Run("RolesLookupKeyset", func(t *testing.T) { runRolesKeyset(t, newRepos) })
}
func assertRoleCoverage(t *testing.T, list func(listing.Request) (listing.Page[roles.Assignment], error), want int) {
	t.Helper()
	seen := map[tuples.Tuple]bool{}
	cursor := ""
	for pages := 0; pages < want+2; pages++ {
		page, err := list(listing.Request{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page.Items {
			if seen[a.Tuple()] {
				t.Fatalf("duplicate fact %v", a)
			}
			seen[a.Tuple()] = true
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" {
			t.Fatal("missing cursor")
		}
		cursor = page.NextCursor
	}
	if len(seen) != want {
		t.Fatal(fmt.Sprintf("want %d assignments, got %d", want, len(seen)))
	}
}
