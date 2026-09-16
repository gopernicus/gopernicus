package storetest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The public facade must work with only the required canonical port, without a
// separately injected relationship store or optional graph optimization methods.
type tupleOnlyFacadeStore struct{ tuples.Storer }

func runRelationshipFacades(t *testing.T, factory func(*testing.T) Repositories) {
	t.Run("RawParity", func(t *testing.T) { runRawRelationshipFacadeParity(t, factory) })
	t.Run("OneAuthority", func(t *testing.T) {
		store := factory(t).Tuples
		policy := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
			"doc": {Relations: map[string]decisions.RelationDef{
				"owner":  {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
				"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group"}, {Type: "group", Relation: "member"}}},
			}, Permissions: map[string]decisions.Expression{"view": decisions.Direct("viewer")}},
			"group": {Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}},
		}}
		c, err := authorization.New(authorization.Repositories{Tuples: tupleOnlyFacadeStore{store}}, authorization.WithModel(policy))
		if err != nil {
			t.Fatal(err)
		}
		ctx := t.Context()
		p := model.PrincipalRef{Type: "user", ID: "alice"}
		resource := model.Resource{Type: "doc", ID: "d"}
		if err := c.RelationshipWriter.CreateRelationships(ctx, []relationships.CreateRelationship{
			ct("doc", "d", "owner", "user", "alice"), ct("doc", "d", "viewer", "user", "alice"),
		}); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"owner", "viewer"} {
			if held, err := c.Roles.HasRoleIn(ctx, p, role, resource); err != nil || !held {
				t.Fatalf("relationship write invisible to role %s: %t/%v", role, held, err)
			}
		}
		request := model.CheckRequest{Principal: p, Permission: "view", Resource: resource}
		if result, err := c.Decisions.Check(ctx, request); err != nil || !result.Allowed {
			t.Fatalf("relationship write invisible to decision: %+v/%v", result, err)
		}
		grant := roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "viewer", Scope: tuples.On("doc", "d")}
		if err := c.RoleWriter.UnassignRole(ctx, grant); err != nil {
			t.Fatal(err)
		}
		if result, err := c.Decisions.Check(ctx, request); err != nil || result.Allowed {
			t.Fatalf("role revocation invisible to decision: %+v/%v", result, err)
		}
		if err := c.RoleWriter.AssignRole(ctx, grant); err != nil {
			t.Fatal(err)
		}
		if held, err := c.Relationships.CheckRelationExists(ctx, "doc", "d", "viewer", "user", "alice"); err != nil || !held {
			t.Fatalf("role write invisible to relationship: %t/%v", held, err)
		}
		member := roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "member", Scope: tuples.On("group", "g")}
		if err := c.RoleWriter.AssignRole(ctx, member); err != nil {
			t.Fatal(err)
		}
		group := relationships.SubjectRef{Type: "group", ID: "g"}
		userset := group
		userset.Relation = "member"
		if err := c.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", []relationships.SubjectRef{group, userset, userset}); err != nil {
			t.Fatal(err)
		}
		if held, err := c.Roles.HasRoleIn(ctx, p, "viewer", resource); err != nil || held {
			t.Fatalf("exact role expanded userset: %t/%v", held, err)
		}
		if result, err := c.Decisions.Check(ctx, request); err != nil || !result.Allowed {
			t.Fatalf("role fact failed graph membership: %+v/%v", result, err)
		}
		if count, err := c.Relationships.CountByResourceAndRelation(ctx, "doc", "d", "viewer"); err != nil || count != 2 {
			t.Fatalf("direct count must include stored concrete/userset only: %d/%v", count, err)
		}
		if err := c.RelationshipWriter.SetRelationTargets(ctx, resource, "viewer", []relationships.SubjectRef{{Type: "user", ID: "bad\n"}}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid reconciliation: %v", err)
		}
		if count, err := c.Relationships.CountByResourceAndRelation(ctx, "doc", "d", "viewer"); err != nil || count != 2 {
			t.Fatalf("invalid reconcile altered facts: %d/%v", count, err)
		}
		if err := c.RelationshipWriter.DeleteRelationship(ctx, resource, "viewer", userset); err != nil {
			t.Fatal(err)
		}
		if result, err := c.Decisions.Check(ctx, request); err != nil || result.Allowed {
			t.Fatalf("userset deletion invisible: %+v/%v", result, err)
		}
		if count, err := c.Relationships.CountByResourceAndRelation(ctx, "doc", "d", "viewer"); err != nil || count != 1 {
			t.Fatalf("exact deletion removed concrete grant: %d/%v", count, err)
		}
		if err := c.RelationshipWriter.DeleteResourceRelationships(ctx, resource); err != nil {
			t.Fatal(err)
		}
		if rows, err := c.Relationships.ListRelationshipsByResource(ctx, "doc", "d", relationships.ResourceRelationshipFilter{}, list.Request{}); err != nil || len(rows.Items) != 0 {
			t.Fatalf("scope deletion incomplete: %+v/%v", rows, err)
		}
		if held, err := c.Roles.HasRoleIn(ctx, p, "member", model.Resource{Type: "group", ID: "g"}); err != nil || !held {
			t.Fatalf("scope deletion crossed resources: %t/%v", held, err)
		}
	})
	t.Run("FilteredPages", func(t *testing.T) {
		store := factory(t).Tuples
		c, err := authorization.New(authorization.Repositories{Tuples: tupleOnlyFacadeStore{store}})
		if err != nil {
			t.Fatal(err)
		}
		facts := []tuples.Tuple{
			{Scope: tuples.Global(), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
			{Scope: tuples.On("doc", "a"), Relation: "owner", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
			{Scope: tuples.On("doc", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
			{Scope: tuples.On("doc", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}},
			{Scope: tuples.On("doc", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}},
			{Scope: tuples.On("doc", "b"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
			{Scope: tuples.On("space", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
		}
		if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
			t.Fatal(err)
		}
		rt, relation, st := "doc", "viewer", "group"
		first, err := c.Relationships.ListRelationshipsBySubject(t.Context(), st, "g", relationships.SubjectRelationshipFilter{ResourceType: &rt, Relation: &relation}, list.Request{Limit: 1, WithCount: true})
		want := []relationships.SubjectRelationship{{ResourceType: "doc", ResourceID: "a", Relation: "viewer"}}
		if err != nil || !reflect.DeepEqual(first.Items, want) || first.Total == nil || *first.Total != 3 || !first.HasMore || first.HasPrev {
			t.Fatalf("first subject page: %+v/%v", first, err)
		}
		second, err := c.Relationships.ListRelationshipsBySubject(t.Context(), st, "g", relationships.SubjectRelationshipFilter{ResourceType: &rt, Relation: &relation}, list.Request{Limit: 1, Cursor: first.NextCursor})
		want[0].SubjectRelation = "member"
		if err != nil || !reflect.DeepEqual(second.Items, want) || !second.HasPrev || !second.HasMore {
			t.Fatalf("second subject page: %+v/%v", second, err)
		}
		previous, err := c.Relationships.ListRelationshipsBySubject(t.Context(), st, "g", relationships.SubjectRelationshipFilter{ResourceType: &rt, Relation: &relation}, list.Request{Limit: 1, Cursor: second.PreviousCursor})
		if err != nil || !reflect.DeepEqual(previous.Items, first.Items) {
			t.Fatalf("previous subject page: %+v/%v", previous, err)
		}
		rows, err := c.Relationships.ListRelationshipsByResource(t.Context(), rt, "a", relationships.ResourceRelationshipFilter{SubjectType: &st, Relation: &relation}, list.Request{WithCount: true})
		subjects := []relationships.ResourceRelationship{{SubjectType: "group", SubjectID: "g", Relation: "viewer"}, {SubjectType: "group", SubjectID: "g", SubjectRelation: "member", Relation: "viewer"}}
		if err != nil || !reflect.DeepEqual(rows.Items, subjects) || rows.Total == nil || *rows.Total != 2 {
			t.Fatalf("resource projection: %+v/%v", rows, err)
		}
		absent, err := c.Relationships.ListRelationshipsByResource(t.Context(), rt, "absent", relationships.ResourceRelationshipFilter{}, list.Request{WithCount: true})
		if err != nil || absent.Items == nil || len(absent.Items) != 0 || absent.Total == nil || *absent.Total != 0 {
			t.Fatalf("empty resource projection: %+v/%v", absent, err)
		}
	})
	t.Run("RejectEmptySelectors", func(t *testing.T) {
		c, err := authorization.New(authorization.Repositories{Tuples: tupleOnlyFacadeStore{factory(t).Tuples}})
		if err != nil {
			t.Fatal(err)
		}
		empty := ""
		checks := []func() error{
			func() error { _, err := c.Relationships.GetRelationTargets(t.Context(), "doc", "d", ""); return err },
			func() error {
				_, err := c.Relationships.CountByResourceAndRelation(t.Context(), "doc", "d", "")
				return err
			},
			func() error {
				_, err := c.Relationships.ListRelationshipsBySubject(t.Context(), "", "g", relationships.SubjectRelationshipFilter{}, list.Request{})
				return err
			},
			func() error {
				_, err := c.Relationships.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{ResourceType: &empty}, list.Request{})
				return err
			},
			func() error {
				_, err := c.Relationships.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{Relation: &empty}, list.Request{})
				return err
			},
			func() error {
				_, err := c.Relationships.ListRelationshipsByResource(t.Context(), "doc", "", relationships.ResourceRelationshipFilter{}, list.Request{})
				return err
			},
			func() error {
				_, err := c.Relationships.ListRelationshipsByResource(t.Context(), "doc", "d", relationships.ResourceRelationshipFilter{SubjectType: &empty}, list.Request{})
				return err
			},
			func() error {
				_, err := c.Relationships.ListRelationshipsByResource(t.Context(), "doc", "d", relationships.ResourceRelationshipFilter{Relation: &empty}, list.Request{})
				return err
			},
		}
		for i, check := range checks {
			if err := check(); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("invalid selector %d widened query: %v", i, err)
			}
		}
	})
}
