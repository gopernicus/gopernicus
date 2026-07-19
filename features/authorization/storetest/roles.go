package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

func assign(t *testing.T, s role.Storer, subjectType, subjectID, roleName, resourceType, resourceID string) {
	t.Helper()
	if err := s.Assign(context.Background(), role.Assignment{
		SubjectType: subjectType, SubjectID: subjectID, Role: roleName,
		ResourceType: resourceType, ResourceID: resourceID,
	}); err != nil {
		t.Fatalf("Assign: %v", err)
	}
}

func hasExact(t *testing.T, s role.Storer, subjectType, subjectID, roleName, resourceType, resourceID string) bool {
	t.Helper()
	ok, err := s.HasExactRole(context.Background(), subjectType, subjectID, roleName, resourceType, resourceID)
	if err != nil {
		t.Fatalf("HasExactRole: %v", err)
	}
	return ok
}

// runRoles is the Roles/* family (store-level layer (a)) plus the service-level
// GlobalFallback (layer (b)).
func runRoles(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("AssignIdempotent", func(t *testing.T) {
		s := newRepos(t).Roles
		// Duplicate assign is a no-op, including for the GLOBAL ("","") pair —
		// asserted via the listing so dedup is proven at the constraint level.
		assign(t, s, "user", "u1", "editor", "", "")
		first, _ := s.ListBySubject(ctx, "user", "u1", crud.ListRequest{})
		created := first.Items[0].CreatedAt

		assign(t, s, "user", "u1", "editor", "", "")
		again, _ := s.ListBySubject(ctx, "user", "u1", crud.ListRequest{})
		if len(again.Items) != 1 {
			t.Fatalf("duplicate global assign must keep one row, got %d", len(again.Items))
		}
		if !again.Items[0].CreatedAt.Equal(created) {
			t.Fatalf("duplicate assign must retain the original CreatedAt")
		}
	})

	t.Run("UnassignIdempotent", func(t *testing.T) {
		s := newRepos(t).Roles
		if err := s.Unassign(ctx, "user", "u1", "editor", "", ""); err != nil {
			t.Fatalf("unassign of absent: %v", err)
		}
		assign(t, s, "user", "u1", "editor", "", "")
		if err := s.Unassign(ctx, "user", "u1", "editor", "", ""); err != nil {
			t.Fatalf("unassign: %v", err)
		}
		if err := s.Unassign(ctx, "user", "u1", "editor", "", ""); err != nil {
			t.Fatalf("repeat unassign: %v", err)
		}
		if hasExact(t, s, "user", "u1", "editor", "", "") {
			t.Fatalf("assignment should be gone")
		}
	})

	t.Run("HasExactRole", func(t *testing.T) {
		s := newRepos(t).Roles
		// Global vs scoped are distinct at the store; scopedA never satisfies scopedB.
		assign(t, s, "user", "u1", "editor", "", "")      // global
		assign(t, s, "user", "u2", "viewer", "doc", "dA") // scoped to dA
		if hasExact(t, s, "user", "u1", "editor", "doc", "dA") {
			t.Fatalf("global grant must NOT satisfy a scoped store lookup")
		}
		if !hasExact(t, s, "user", "u1", "editor", "", "") {
			t.Fatalf("global grant must satisfy the exact global lookup")
		}
		if !hasExact(t, s, "user", "u2", "viewer", "doc", "dA") {
			t.Fatalf("scoped grant must satisfy its exact scope")
		}
		if hasExact(t, s, "user", "u2", "viewer", "doc", "dB") {
			t.Fatalf("scopedA grant must NOT satisfy a lookup on scope B")
		}
	})

	t.Run("DistinctAssignmentsCoexist", func(t *testing.T) {
		s := newRepos(t).Roles
		// Same subject, two roles → two rows.
		assign(t, s, "user", "u1", "editor", "doc", "dA")
		assign(t, s, "user", "u1", "viewer", "doc", "dA")
		// Same subject + role, two scopes → two rows.
		assign(t, s, "user", "u1", "editor", "doc", "dB")

		if !hasExact(t, s, "user", "u1", "editor", "doc", "dA") ||
			!hasExact(t, s, "user", "u1", "viewer", "doc", "dA") ||
			!hasExact(t, s, "user", "u1", "editor", "doc", "dB") {
			t.Fatalf("distinct assignments must all be present")
		}
		page, _ := s.ListBySubject(ctx, "user", "u1", crud.ListRequest{})
		if len(page.Items) != 3 {
			t.Fatalf("listing must return all 3 distinct assignments, got %d", len(page.Items))
		}
	})

	t.Run("ListPagination", func(t *testing.T) {
		s := newRepos(t).Roles
		assign(t, s, "user", "u1", "r1", "doc", "d1")
		assign(t, s, "user", "u1", "r2", "doc", "d1")
		assign(t, s, "user", "u1", "r3", "doc", "d1")

		// By subject: page size 2 then follow the cursor; full coverage, no overlap.
		assertRoleCoverage(t, func(req crud.ListRequest) (crud.Page[role.Assignment], error) {
			return s.ListBySubject(ctx, "user", "u1", req)
		}, 3)
		// By resource: same three assignments are scoped to doc:d1.
		assertRoleCoverage(t, func(req crud.ListRequest) (crud.Page[role.Assignment], error) {
			return s.ListByResource(ctx, "doc", "d1", req)
		}, 3)

		// Empty page shape (both methods).
		empty, err := s.ListByResource(ctx, "doc", "absent", crud.ListRequest{})
		if err != nil {
			t.Fatalf("empty list: %v", err)
		}
		if len(empty.Items) != 0 || empty.HasMore || empty.NextCursor != "" {
			t.Fatalf("empty page shape wrong: %+v", empty)
		}
	})

	t.Run("RejectsUnknownOrderField", func(t *testing.T) {
		s := newRepos(t).Roles
		assign(t, s, "user", "u1", "editor", "doc", "d1")
		// An order field outside role.OrderFields (created_at only) is rejected
		// with ErrInvalidInput identically across every backend.
		if _, err := s.ListBySubject(ctx, "user", "u1", crud.ListRequest{Order: crud.NewOrder("role", crud.ASC)}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unknown order field must reject with ErrInvalidInput, got %v", err)
		}
	})

	t.Run("EffectiveEnumerationAgreesWithHasRole", func(t *testing.T) {
		// The enumeration side of Q5: ListEffectiveByResource unions direct scoped
		// grants with the global grants HasRole falls back to, tagged by provenance,
		// and its grant set AGREES with HasRole across every backend.
		repos := newRepos(t)
		s := repos.Roles
		svc := newServiceFor(t, repos)

		assign(t, s, "user", "u1", "auditor", "", "")      // global only
		assign(t, s, "user", "u2", "auditor", "doc", "d1") // direct only
		assign(t, s, "user", "u3", "auditor", "doc", "d1") // both:
		assign(t, s, "user", "u3", "auditor", "", "")      //   direct + global
		assign(t, s, "user", "u4", "auditor", "doc", "d2") // different scope — must not leak

		got := effectiveProvenance(t, s, "doc", "d1")
		want := map[string]string{"u1": "global", "u2": "direct", "u3": "both"}
		if len(got) != len(want) {
			t.Fatalf("effective set = %v, want subjects %v", got, want)
		}
		for id, p := range want {
			if got[id] != p {
				t.Fatalf("subject %s provenance = %q, want %q (set %v)", id, got[id], p, got)
			}
		}
		if _, leaked := got["u4"]; leaked {
			t.Fatalf("a grant scoped to doc/d2 leaked into doc/d1 enumeration: %v", got)
		}

		// Symmetry with the decision side: every enumerated subject passes HasRole,
		// and HasRole denies the subject enumeration omits.
		for id := range want {
			if ok, err := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: id}, "auditor", "doc", "d1"); err != nil || !ok {
				t.Fatalf("HasRole(%s)=%v,%v; enumeration and decision must agree", id, ok, err)
			}
		}
		if ok, _ := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: "u4"}, "auditor", "doc", "d1"); ok {
			t.Fatalf("HasRole must deny u4 on doc/d1 just as enumeration omits it")
		}
	})

	t.Run("ScopedRevokeGlobalRoleRemains", func(t *testing.T) {
		// AZ3-1.5 receipts-split: the full mutation receipt (same_role_grant_remains)
		// is AZ3-3.3 work. Here we land the underlying QUERYABLE TRUTH it rests on —
		// after a scoped RAW unassign, the same GLOBAL role grant remains and still
		// confers the role, provable by HasRole + effective enumeration.
		repos := newRepos(t)
		s := repos.Roles
		svc := newServiceFor(t, repos)

		assign(t, s, "user", "u1", "editor", "", "")      // global
		assign(t, s, "user", "u1", "editor", "doc", "d1") // + direct scoped

		// Pre-revoke: provenance is "both".
		if got := effectiveProvenance(t, s, "doc", "d1"); got["u1"] != "both" {
			t.Fatalf("pre-revoke provenance for u1 = %q, want both", got["u1"])
		}

		// Raw scoped unassign (AZ3-3.4 owns the fate of the raw method; this is the
		// medium finding's exact scenario — a scoped revoke reporting success).
		if err := s.Unassign(ctx, "user", "u1", "editor", "doc", "d1"); err != nil {
			t.Fatalf("scoped Unassign: %v", err)
		}
		// The direct scoped row is gone at the store.
		if hasExact(t, s, "user", "u1", "editor", "doc", "d1") {
			t.Fatalf("scoped assignment must be removed")
		}

		// The global grant remains and still confers the role — the truth a future
		// same_role_grant_remains=true receipt will report.
		sameRoleGrantRemains, err := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: "u1"}, "editor", "doc", "d1")
		if err != nil {
			t.Fatalf("post-revoke HasRole: %v", err)
		}
		if !sameRoleGrantRemains {
			t.Fatalf("same_role_grant_remains truth: global grant must still confer the role after a scoped revoke")
		}
		// Enumeration agrees and now attributes the remaining access to the global
		// source — it does NOT claim a direct row survived.
		if got := effectiveProvenance(t, s, "doc", "d1"); got["u1"] != "global" {
			t.Fatalf("post-revoke provenance for u1 = %q, want global (the global grant remains, not a scoped row)", got["u1"])
		}
	})

	t.Run("EffectivePagination", func(t *testing.T) {
		s := newRepos(t).Roles
		// Three effective grants on doc/d1: two direct, one via global fallback (a
		// global grant is effective for every scoped resource).
		assign(t, s, "user", "u1", "auditor", "doc", "d1")
		assign(t, s, "user", "u2", "auditor", "doc", "d1")
		assign(t, s, "user", "u3", "auditor", "", "")

		assertEffectiveCoverage(t, func(req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
			return s.ListEffectiveByResource(ctx, "doc", "d1", req)
		}, 3)

		// Empty-page shape: a fresh store with no assignments at all (any global
		// grant would otherwise be effective for a scoped resource).
		fresh := newRepos(t).Roles
		empty, err := fresh.ListEffectiveByResource(ctx, "doc", "absent", crud.ListRequest{})
		if err != nil {
			t.Fatalf("empty effective list: %v", err)
		}
		if len(empty.Items) != 0 || empty.HasMore || empty.NextCursor != "" {
			t.Fatalf("empty effective page shape wrong: %+v", empty)
		}
	})

	t.Run("GlobalFallback", func(t *testing.T) {
		// Service-level (layer (b)): a global grant satisfies a scoped HasRole while
		// the store lookup stays exact; a scoped grant never satisfies another scope;
		// a miss is (false, nil).
		repos := newRepos(t)
		svc := newServiceFor(t, repos)

		// Seed via the role.Storer PORT (the raw Service.AssignRole was removed at
		// AZ3-3.4); the behavior under test is the SERVICE's HasRole global fallback.
		assign(t, repos.Roles, "user", "u1", "editor", "", "")
		if ok, err := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: "u1"}, "editor", "doc", "d1"); err != nil || !ok {
			t.Fatalf("global grant should satisfy scoped HasRole: ok=%v err=%v", ok, err)
		}
		// The store-level lookup for the scoped tuple stays exact (false).
		if hasExact(t, repos.Roles, "user", "u1", "editor", "doc", "d1") {
			t.Fatalf("store HasExactRole must stay exact (the fallback is the service's)")
		}

		assign(t, repos.Roles, "user", "u2", "viewer", "doc", "dA")
		if ok, _ := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: "u2"}, "viewer", "doc", "dB"); ok {
			t.Fatalf("scoped grant must not satisfy a different scope")
		}
		if ok, err := svc.HasRole(ctx, authorization.PrincipalRef{Type: "user", ID: "nobody"}, "editor", "doc", "d1"); err != nil || ok {
			t.Fatalf("miss must be (false, nil): ok=%v err=%v", ok, err)
		}
	})
}

// effectiveProvenance returns a subjectID → provenance-label map for the
// effective grants on a resource, draining all pages. It fails on any duplicate
// subject+role (the dedup contract) and on the zero provenance label (every grant
// must have at least one source).
func effectiveProvenance(t *testing.T, s role.Storer, resourceType, resourceID string) map[string]string {
	t.Helper()
	out := map[string]string{}
	cursor := ""
	for pages := 0; pages < 100; pages++ {
		page, err := s.ListEffectiveByResource(context.Background(), resourceType, resourceID, crud.ListRequest{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListEffectiveByResource: %v", err)
		}
		for _, g := range page.Items {
			key := g.SubjectType + "|" + g.SubjectID + "|" + g.Role
			if _, dup := out[key]; dup {
				t.Fatalf("effective grant %s appeared twice (dedup broken)", key)
			}
			if !g.Direct && !g.Global {
				t.Fatalf("effective grant %s has no provenance", key)
			}
			out[g.SubjectID] = g.Provenance()
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	return out
}

// assertEffectiveCoverage pages an effective listing two at a time and asserts
// every grant appears exactly once, want total.
func assertEffectiveCoverage(t *testing.T, list func(crud.ListRequest) (crud.Page[role.EffectiveGrant], error), want int) {
	t.Helper()
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < want+2; pages++ {
		page, err := list(crud.ListRequest{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("list page: %v", err)
		}
		for _, g := range page.Items {
			key := g.SubjectType + "|" + g.SubjectID + "|" + g.Role
			if seen[key] {
				t.Fatalf("effective grant %s appeared twice across pages", key)
			}
			seen[key] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != want {
		t.Fatalf("want %d effective grants across pages, got %d", want, len(seen))
	}
}

// assertRoleCoverage pages a role listing two at a time and asserts every
// assignment appears exactly once, want total.
func assertRoleCoverage(t *testing.T, list func(crud.ListRequest) (crud.Page[role.Assignment], error), want int) {
	t.Helper()
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < want+2; pages++ {
		page, err := list(crud.ListRequest{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("list page: %v", err)
		}
		for _, a := range page.Items {
			key := a.SubjectType + "|" + a.SubjectID + "|" + a.Role + "|" + a.ResourceType + "|" + a.ResourceID
			if seen[key] {
				t.Fatalf("assignment %s appeared twice across pages", key)
			}
			seen[key] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != want {
		t.Fatalf("want %d assignments across pages, got %d", want, len(seen))
	}
}
