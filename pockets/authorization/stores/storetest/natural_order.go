package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func runRelationshipNaturalOrder(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	ctx := context.Background()
	s := newRepos(t).Relationships
	rows := []relationships.CreateRelationship{
		ct("doc", "a", "owner", "group", "g"),
		ctUserset("doc", "a", "viewer", "group", "g", "admin"),
		ctUserset("doc", "a", "viewer", "group", "g", "member"),
		ct("doc", "a-", "viewer", "group", "g"),
		ct("doc", "é", "viewer", "group", "g"),
		ct("folder", "a", "viewer", "group", "g"),
		ct("doc", "a", "viewer", "group", "ga"),
		ct("doc", "a", "viewer", "user", "u"),
	}
	// Reverse insertion separates tuple order from any insertion metadata.
	for i := len(rows) - 1; i >= 0; i-- {
		mustCreate(t, s, rows[i])
	}
	bySubject := func(req list.Request) (list.Page[relationships.SubjectRelationship], error) {
		return s.ListRelationshipsBySubject(ctx, "group", "g", relationships.SubjectRelationshipFilter{}, req)
	}
	byResource := func(req list.Request) (list.Page[relationships.ResourceRelationship], error) {
		return s.ListRelationshipsByResource(ctx, "doc", "a", relationships.ResourceRelationshipFilter{}, req)
	}
	wantSubject := []relationships.SubjectRelationship{
		{ResourceType: "doc", ResourceID: "a", Relation: "owner"},
		{ResourceType: "doc", ResourceID: "a", Relation: "viewer", SubjectRelation: "admin"},
		{ResourceType: "doc", ResourceID: "a", Relation: "viewer", SubjectRelation: "member"},
		{ResourceType: "doc", ResourceID: "a-", Relation: "viewer"},
		{ResourceType: "doc", ResourceID: "é", Relation: "viewer"},
		{ResourceType: "folder", ResourceID: "a", Relation: "viewer"},
	}
	wantResource := []relationships.ResourceRelationship{
		{SubjectType: "group", SubjectID: "g", Relation: "owner"},
		{SubjectType: "group", SubjectID: "g", SubjectRelation: "admin", Relation: "viewer"},
		{SubjectType: "group", SubjectID: "g", SubjectRelation: "member", Relation: "viewer"},
		{SubjectType: "group", SubjectID: "ga", Relation: "viewer"},
		{SubjectType: "user", SubjectID: "u", Relation: "viewer"},
	}
	t.Run("Subject", func(t *testing.T) { assertNaturalPages(t, "tuple_key", bySubject, wantSubject) })
	t.Run("Resource", func(t *testing.T) { assertNaturalPages(t, "tuple_key", byResource, wantResource) })
	before, err := bySubject(list.Request{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	// Removing/recreating a tuple must preserve its place and cursor, including
	// the userset identity. There is no new insertion timestamp or surrogate ID.
	removed := rows[1]
	if err := s.DeleteRelationshipTarget(ctx, removed.ResourceType, removed.ResourceID, removed.Relation, removed.Subject()); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, removed)
	after, err := bySubject(list.Request{Limit: 2})
	if err != nil || !slices.Equal(before.Items, after.Items) || before.NextCursor != after.NextCursor {
		t.Fatalf("recreated tuple changed page identity: before=%+v after=%+v err=%v", before, after, err)
	}
}

func runRoleNaturalOrder(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	ctx := context.Background()
	s := newRepos(t).Roles
	rows := []roles.Assignment{
		{SubjectType: "user", SubjectID: "u", Role: "admin"},
		{SubjectType: "user", SubjectID: "u", Role: "admin", ResourceType: "doc", ResourceID: "a"},
		{SubjectType: "user", SubjectID: "u", Role: "admin", ResourceType: "doc", ResourceID: "a-"},
		{SubjectType: "user", SubjectID: "u", Role: "admin", ResourceType: "folder", ResourceID: "a"},
		{SubjectType: "user", SubjectID: "u", Role: "viewer", ResourceType: "doc", ResourceID: "a"},
		{SubjectType: "group", SubjectID: "g", Role: "viewer", ResourceType: "doc", ResourceID: "a"},
		{SubjectType: "user", SubjectID: "u-", Role: "admin", ResourceType: "doc", ResourceID: "a"},
		{SubjectType: "user", SubjectID: "é", Role: "admin", ResourceType: "doc", ResourceID: "a"},
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if err := s.Assign(ctx, rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	bySubject := func(req list.Request) (list.Page[roles.Assignment], error) {
		return s.ListBySubject(ctx, "user", "u", req)
	}
	byResource := func(req list.Request) (list.Page[roles.Assignment], error) {
		return s.ListByResource(ctx, "doc", "a", req)
	}
	t.Run("Subject", func(t *testing.T) { assertNaturalPages(t, "role_key", bySubject, rows[:5]) })
	t.Run("Resource", func(t *testing.T) {
		assertNaturalPages(t, "role_key", byResource, []roles.Assignment{rows[5], rows[1], rows[4], rows[6], rows[7]})
	})
	before, err := bySubject(list.Request{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	a := rows[1]
	if err := s.Unassign(ctx, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID); err != nil {
		t.Fatal(err)
	}
	if err := s.Assign(ctx, a); err != nil {
		t.Fatal(err)
	}
	after, err := bySubject(list.Request{Limit: 2})
	if err != nil || !slices.Equal(before.Items, after.Items) || before.NextCursor != after.NextCursor {
		t.Fatalf("reassigned role changed page identity: before=%+v after=%+v err=%v", before, after, err)
	}
}

// assertNaturalPages verifies independently authored expected ordering through
// the real list implementation, in both directions and pagination strategies.
func assertNaturalPages[T comparable](t *testing.T, field string, fetch func(list.Request) (list.Page[T], error), want []T) {
	t.Helper()
	first, err := fetch(list.Request{WithCount: true})
	if err != nil || !slices.Equal(first.Items, want) || first.Total == nil || *first.Total != int64(len(want)) {
		t.Fatalf("default order/count: page=%+v want=%+v err=%v", first, want, err)
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		ordered := slices.Clone(want)
		if direction == list.DESC {
			slices.Reverse(ordered)
		}
		for _, size := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/limit%d", direction, size), func(t *testing.T) {
				var previous []T
				cursor := ""
				for start := 0; start < len(ordered); start += size {
					req := list.Request{Limit: size, Order: list.NewOrder(field, direction), Cursor: cursor}
					page, err := fetch(req)
					end := min(start+size, len(ordered))
					if err != nil || !slices.Equal(page.Items, ordered[start:end]) || page.HasMore != (end < len(ordered)) || page.HasPrev != (start > 0) {
						t.Fatalf("cursor page at %d: %+v want=%+v err=%v", start, page, ordered[start:end], err)
					}
					if start > 0 {
						backReq := req
						backReq.Cursor = page.PreviousCursor
						back, err := fetch(backReq)
						if err != nil || !slices.Equal(back.Items, previous) {
							t.Fatalf("previous at %d: %+v want=%+v err=%v", start, back, previous, err)
						}
					}
					offset, err := fetch(list.Request{Limit: size, Offset: start, Strategy: list.StrategyOffset, Order: req.Order, WithCount: true})
					if err != nil || !slices.Equal(offset.Items, page.Items) || offset.Total == nil || *offset.Total != int64(len(ordered)) {
						t.Fatalf("offset page at %d: %+v want=%+v err=%v", start, offset, page.Items, err)
					}
					previous, cursor = page.Items, page.NextCursor
					if page.HasMore && cursor == "" {
						t.Fatal("missing continuation cursor")
					}
				}
			})
		}
	}
	for _, obsolete := range []string{"created_at", "relationship_id"} {
		if _, err := fetch(list.Request{Order: list.NewOrder(obsolete, list.ASC)}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("removed metadata order %q: %v", obsolete, err)
		}
	}
}
