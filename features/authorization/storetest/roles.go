package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
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
		if _, err := s.ListBySubject(ctx, "user", "u1", crud.ListRequest{Order: crud.NewOrder("role", crud.ASC)}); !errors.Is(err, errs.ErrInvalidInput) {
			t.Fatalf("unknown order field must reject with ErrInvalidInput, got %v", err)
		}
	})

	t.Run("GlobalFallback", func(t *testing.T) {
		// Service-level (layer (b)): a global grant satisfies a scoped HasRole while
		// the store lookup stays exact; a scoped grant never satisfies another scope;
		// a miss is (false, nil).
		repos := newRepos(t)
		svc := newServiceFor(t, repos)

		if err := svc.AssignRole(ctx, authorization.Subject{Type: "user", ID: "u1"}, "editor", "", ""); err != nil {
			t.Fatalf("AssignRole global: %v", err)
		}
		if ok, err := svc.HasRole(ctx, authorization.Subject{Type: "user", ID: "u1"}, "editor", "doc", "d1"); err != nil || !ok {
			t.Fatalf("global grant should satisfy scoped HasRole: ok=%v err=%v", ok, err)
		}
		// The store-level lookup for the scoped tuple stays exact (false).
		if hasExact(t, repos.Roles, "user", "u1", "editor", "doc", "d1") {
			t.Fatalf("store HasExactRole must stay exact (the fallback is the service's)")
		}

		if err := svc.AssignRole(ctx, authorization.Subject{Type: "user", ID: "u2"}, "viewer", "doc", "dA"); err != nil {
			t.Fatalf("AssignRole scoped: %v", err)
		}
		if ok, _ := svc.HasRole(ctx, authorization.Subject{Type: "user", ID: "u2"}, "viewer", "doc", "dB"); ok {
			t.Fatalf("scoped grant must not satisfy a different scope")
		}
		if ok, err := svc.HasRole(ctx, authorization.Subject{Type: "user", ID: "nobody"}, "editor", "doc", "d1"); err != nil || ok {
			t.Fatalf("miss must be (false, nil): ok=%v err=%v", ok, err)
		}
	})
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
