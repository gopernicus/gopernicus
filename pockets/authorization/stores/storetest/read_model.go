package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

func runReadModel(t *testing.T, newRepos func(*testing.T) authorization.Repositories) {
	for _, mode := range []string{"direct_subject", "direct_relation", "userset_subject", "userset_relation", "intermediate_membership", "nested_userset", "through_target_type"} {
		t.Run(mode, func(t *testing.T) { specCurrentReadModel(t, newRepos(t), mode) })
	}
	t.Run("ContainmentMethodsAndZeroModel", func(t *testing.T) { specReadModelContainment(t, newRepos(t)) })
}

func readModelFixture() relationships.Schema {
	users := relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}
	return relationships.Schema{ResourceTypes: map[string]relationships.ResourceTypeDef{
		"doc": {Relations: map[string]relationships.RelationDef{
			"owner": users, "member": users,
			"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "service_account"}, {Type: "group", Relation: "member"}, {Type: "group", Relation: "admin"}}},
			"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}, {Type: "folder"}}},
		}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("owner"), relationships.Direct("viewer"), relationships.Through("parent", "view"))}},
		"group": {Relations: map[string]relationships.RelationDef{
			"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}, {Type: "service_account"}}},
			"admin":  users,
		}},
		"space":  {Relations: map[string]relationships.RelationDef{"viewer": users}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))}},
		"folder": {Relations: map[string]relationships.RelationDef{"viewer": users}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))}},
	}}
}

func fixtureReadModel(schema relationships.Schema) relationships.ReadModel {
	var rules []relationships.SubjectRule
	for resourceType, def := range schema.ResourceTypes {
		for relation, def := range def.Relations {
			for _, subject := range def.AllowedSubjects {
				rules = append(rules, relationships.SubjectRule{ResourceType: resourceType, Relation: relation, SubjectType: subject.Type, SubjectRelation: subject.Relation})
			}
		}
	}
	return relationships.NewReadModel(rules)
}

type staleModelGuard struct{}

func (staleModelGuard) AuthorizeMutation(ctx context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
	result, err := view.Check(ctx, authmodel.CheckRequest{Principal: attempt.Actor.PrincipalRef, Permission: "view", Resource: authmodel.Resource{Type: attempt.Target.Type, ID: attempt.Target.ID}})
	ok := result.Allowed
	if err != nil {
		return err
	}
	if !ok {
		return sdk.ErrForbidden
	}
	return nil
}

func specCurrentReadModel(t *testing.T, repos authorization.Repositories, mode string) {
	ctx := context.Background()
	current := readModelFixture()
	doc, group := current.ResourceTypes["doc"], current.ResourceTypes["group"]
	rows := []relationships.CreateRelationship{ct("doc", "keep", "owner", "user", "u1")}
	relation, subjectType, subjectID := "viewer", "user", "u1"
	switch mode {
	case "direct_subject":
		rows = append(rows, ct("doc", "stale", "viewer", "user", "u1"))
		doc.Relations["viewer"] = relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "service_account"}}}
	case "direct_relation":
		rows = append(rows, ct("doc", "stale", "viewer", "user", "u1"))
		delete(doc.Relations, "viewer")
		doc.Permissions["view"] = relationships.AnyOf(relationships.Direct("owner"), relationships.Through("parent", "view"))
	case "userset_subject", "intermediate_membership":
		rows = append(rows, ctUserset("doc", "stale", "viewer", "group", "g1", "member"), ct("group", "g1", "member", "user", "u1"))
		if mode == "userset_subject" {
			doc.Relations["viewer"] = relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}
		} else {
			group.Relations["member"] = relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "service_account"}}}
		}
	case "userset_relation":
		rows = append(rows, ctUserset("doc", "stale", "viewer", "group", "g1", "admin"), ct("group", "g1", "admin", "user", "u1"))
		doc.Relations["viewer"] = relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}
		delete(group.Relations, "admin")
	case "nested_userset":
		rows = append(rows, ctUserset("doc", "stale", "viewer", "group", "outer", "member"), ctUserset("group", "outer", "member", "group", "inner", "member"), ct("group", "inner", "member", "user", "u1"))
		group.Relations["member"] = relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}
	case "through_target_type":
		rows = append(rows, ct("doc", "stale", "parent", "space", "s1"), ct("space", "s1", "viewer", "user", "u1"))
		doc.Relations["parent"] = relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "folder"}}}
		relation, subjectType, subjectID = "parent", "space", "s1"
	}
	mustCreate(t, repos.Relationships, rows...)
	principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}
	request := authmodel.CheckRequest{Principal: principal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "stale"}}
	old, err := authorization.New(authorization.Repositories{Relationships: repos.Relationships}, authorization.WithRelationshipModel(readModelFixture()))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := old.Decisions.Check(ctx, request); err != nil || !result.Allowed {
		t.Fatalf("fixture did not grant under original model: %+v %v", result, err)
	}
	reader := repos.Relationships.ForModel(fixtureReadModel(current))
	for _, bound := range []int{0, 100} {
		if ok, err := reader.CheckRelationWithGroupExpansion(ctx, "doc", "stale", relation, subjectType, subjectID, bound); err != nil || ok {
			t.Fatalf("scoped expansion retained stale authority, bound=%d: %v %v", bound, ok, err)
		}
	}
	if sets, ok := reader.(relationships.RelationSetReader); ok {
		if ids, err := sets.FilterRelation(ctx, "doc", []string{"stale", "stale"}, relation, subjectType, subjectID, 100); err != nil || len(ids) != 0 {
			t.Fatalf("scoped filter retained stale authority: %v %v", ids, err)
		}
	}
	if results, err := reader.CheckBatchDirect(ctx, "doc", []string{"stale"}, relation, subjectType, subjectID, 100); err != nil || results["stale"] {
		t.Fatalf("scoped batch retained stale authority: %v %v", results, err)
	}
	if ids, err := reader.LookupResourceIDs(ctx, "doc", []string{relation}, subjectType, subjectID, "", 20); err != nil || len(ids) != 0 {
		t.Fatalf("scoped lookup retained stale authority: %v %v", ids, err)
	}
	wantTargets := 0
	if mode == "intermediate_membership" || mode == "nested_userset" {
		wantTargets = 1
	}
	if targets, err := reader.GetRelationTargets(ctx, "doc", "stale", relation); err != nil || len(targets) != wantTargets {
		t.Fatalf("scoped targets retained an unsupported tuple: %v %v", targets, err)
	}
	if sets, ok := reader.(relationships.RelationSetReader); ok {
		if targets, err := sets.RelationTargetsFor(ctx, "doc", []string{"stale"}, relation); err != nil || len(targets["stale"]) != wantTargets {
			t.Fatalf("scoped target batch retained an unsupported tuple: %v %v", targets, err)
		}
	}
	// Raw facts remain inspectable after narrowing the model.
	if targets, err := repos.Relationships.GetRelationTargets(ctx, "doc", "stale", relation); err != nil || len(targets) != 1 {
		t.Fatalf("raw stored fact changed: %v %v", targets, err)
	}
	cfg := []authorization.Option{authorization.WithRelationshipModel(current)}
	if repos.Mutations != nil {
		cfg = append(cfg, authorization.WithGuard(staleModelGuard{}))
	}
	components, err := authorization.New(authorization.Repositories{Relationships: repos.Relationships, Mutations: repos.Mutations}, cfg...)
	if err != nil {
		t.Fatal(err)
	}
	svc := components
	if result, err := svc.Decisions.Check(ctx, request); err != nil || result.Allowed {
		t.Fatalf("Check retained stale authority: %+v %v", result, err)
	}
	if result, _, err := svc.Decisions.CheckExplain(ctx, request); err != nil || result.Allowed {
		t.Fatalf("Explain retained stale authority: %+v %v", result, err)
	}
	keep := request
	keep.Resource.ID = "keep"
	results, err := svc.Decisions.CheckBatch(ctx, []authmodel.CheckRequest{request, keep, request})
	if err != nil || len(results) != 3 || results[0].Allowed || !results[1].Allowed || results[2].Allowed {
		t.Fatalf("host batch model/order parity: %+v %v", results, err)
	}
	if ids, err := svc.Decisions.FilterAuthorized(ctx, principal, "view", "doc", []string{"stale", "keep", "stale"}); err != nil || !slices.Equal(ids, []string{"keep"}) {
		t.Fatalf("host filter model parity: %v %v", ids, err)
	}
	if set, err := svc.Decisions.LookupAllResourceIDs(ctx, principal, "view", "doc"); err != nil || set.Unrestricted || !slices.Equal(set.IDs, []string{"keep"}) {
		t.Fatalf("host complete set model parity: %+v %v", set, err)
	}
	if page, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{Principal: principal, Permission: "view", ResourceType: "doc", Limit: 20}); err != nil || page.Unrestricted || page.HasMore || !slices.Equal(page.IDs, []string{"keep"}) {
		t.Fatalf("host ID page model parity: %+v %v", page, err)
	}
	// Model scoping is per reader/service, not a mutable switch on the shared
	// store. A concurrently deployed old model keeps its explicit old policy.
	if result, err := old.Decisions.Check(ctx, request); err != nil || !result.Allowed {
		t.Fatalf("new reader mutated the old service's model: %+v %v", result, err)
	}
	if repos.Mutations == nil {
		t.Run("Guarded", func(t *testing.T) { t.Skip("mutation repository not wired") })
		return
	}
	command := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "stale", Relation: "member", Subject: relationships.SubjectRef{Type: "user", ID: "recipient"}}
	if _, err := svc.Mutations.GrantRelationship(ctx, mutations.Actor{PrincipalRef: principal}, command); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("guard retained stale authority: %v", err)
	}
	if ok, err := repos.Relationships.CheckRelationExists(ctx, "doc", "stale", "member", "user", "recipient"); err != nil || ok {
		t.Fatalf("denied guard wrote state: %v %v", ok, err)
	}
	command.ResourceID = "keep"
	if _, err := svc.Mutations.GrantRelationship(ctx, mutations.Actor{PrincipalRef: principal}, command); err != nil {
		t.Fatalf("current authority stopped granting: %v", err)
	}
}

func specReadModelContainment(t *testing.T, repos authorization.Repositories) {
	ctx := context.Background()
	mustCreate(t, repos.Relationships,
		ct("doc", "good", "parent", "doc", "root"), ct("doc", "stale", "retired_parent", "doc", "root"),
		ct("doc", "stale-grandchild", "parent", "doc", "stale"))
	model := relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "doc", Relation: "parent", SubjectType: "doc"}})
	for _, zero := range []bool{false, true} {
		active := model
		if zero {
			active = relationships.ReadModel{}
		}
		reader := repos.Relationships.ForModel(active)
		if targets, err := reader.GetRelationTargets(ctx, "doc", "stale", "retired_parent"); err != nil || len(targets) != 0 {
			t.Fatalf("scoped targets returned retired edge: %v %v", targets, err)
		}
		if sets, ok := reader.(relationships.RelationSetReader); ok {
			if targets, err := sets.RelationTargetsFor(ctx, "doc", []string{"stale"}, "retired_parent"); err != nil || len(targets["stale"]) != 0 {
				t.Fatalf("scoped target batch returned retired edge: %v %v", targets, err)
			}
		}
		if ids, err := reader.LookupResourceIDsByRelationTarget(ctx, "doc", "retired_parent", "doc", []string{"root"}, "", 20); err != nil || len(ids) != 0 {
			t.Fatalf("reverse target lookup returned retired edge: %v %v", ids, err)
		}
		want := []string{"good"}
		if zero {
			want = nil
		}
		if sets, ok := reader.(relationships.RelationSetReader); ok {
			if ids, err := sets.FilterRelation(ctx, "doc", []string{"good", "good", "stale"}, "parent", "doc", "root", 100); err != nil || !slices.Equal(ids, want) {
				t.Fatalf("valid/zero filter mismatch: %v %v", ids, err)
			}
		}
		if results, err := reader.CheckBatchDirect(ctx, "doc", []string{"good", "stale"}, "parent", "doc", "root", 100); err != nil || results["good"] == zero || results["stale"] {
			t.Fatalf("valid/zero batch mismatch: %v %v", results, err)
		}
		if ids, err := reader.LookupResourceIDs(ctx, "doc", []string{"parent"}, "doc", "root", "", 20); err != nil || !slices.Equal(ids, want) {
			t.Fatalf("valid/zero ID lookup mismatch: %v %v", ids, err)
		}
		if ids, err := reader.LookupResourceIDsByRelationTarget(ctx, "doc", "parent", "doc", []string{"root"}, "", 20); err != nil || !slices.Equal(ids, want) {
			t.Fatalf("valid/zero reverse lookup mismatch: %v %v", ids, err)
		}
		if sets, ok := reader.(relationships.RelationSetReader); ok {
			if targets, err := sets.RelationTargetsFor(ctx, "doc", []string{"good"}, "parent"); err != nil || len(targets["good"]) != len(want) {
				t.Fatalf("valid/zero target batch mismatch: %v %v", targets, err)
			}
		}
		if ids, err := reader.LookupDescendantResourceIDs(ctx, "doc", []string{"parent", "retired_parent"}, "doc", []string{"root"}, "", 20); err != nil || !slices.Equal(ids, want) {
			t.Fatalf("descendant lookup traversed retired intermediate edge (zero=%v): %v %v", zero, ids, err)
		}
		if targets, err := reader.GetRelationTargets(ctx, "doc", "good", "parent"); err != nil || len(targets) != len(want) {
			t.Fatalf("valid/zero target model mismatch: %v %v", targets, err)
		}
		if allowed, err := reader.CheckRelationWithGroupExpansion(ctx, "doc", "good", "parent", "doc", "root", 0); err != nil || allowed == zero {
			t.Fatalf("valid/zero expansion mismatch: %v %v", allowed, err)
		}
	}
}
