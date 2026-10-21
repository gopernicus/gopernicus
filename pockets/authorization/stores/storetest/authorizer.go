package storetest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/authorizer"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	sdkauthorization "github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authorization/authorizationtest"
)

// RunAuthorizer runs the sdk/capabilities/authorization conformance suite
// against the engine adapter (Components.Authorizer over this pocket's engine)
// on fresh, empty repositories from newRepos. The declared grammar is the
// pocket's own (model.ValidateCheckRequest), never the portable one.
func RunAuthorizer(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	t.Helper()
	authorizationtest.Run(t, authorizationtest.Engine, func(t *testing.T, f authorizationtest.Fixture) sdkauthorization.Authorizer {
		return NewAuthorizer(t, newRepos(t), f).Authorizer
	}, authorizationtest.WithGrammar(model.ValidateCheckRequest))
}

// AuthorizerModel returns an authorizationtest fixture's schema in the
// engine's dialect. A fixture without one is an error, never a skip.
func AuthorizerModel(name string) (decisions.Model, error) {
	switch name {
	case "documents":
		return documentsModel(), nil
	case "usersets":
		return usersetsModel(), nil
	case "segovia-chain":
		return segoviaChainModel(), nil
	case "gps-global":
		return gpsGlobalModel(), nil
	}
	return decisions.Model{}, fmt.Errorf("storetest: no engine schema for authorization fixture %q", name)
}

// NewAuthorizer composes the pocket over repos with f's engine model, seeds
// f.Facts through the tuple authority (a fact on f.Global is a global role,
// tuples.Global()) and returns the components. Their Authorizer maps f.Global
// with authorizer.WithGlobalResource.
func NewAuthorizer(t testing.TB, repos authorization.Repositories, f authorizationtest.Fixture) authorization.Components {
	t.Helper()
	model, err := AuthorizerModel(f.Name)
	if err != nil {
		t.Fatal(err)
	}
	add := make([]tuples.Tuple, 0, len(f.Facts))
	for _, fact := range f.Facts {
		scope, relation := tuples.On(fact.Resource.Type, fact.Resource.ID), fact.Relation
		if g := f.Global; g != nil && g.Holds(fact.Resource) {
			label, ok := g.Label(fact.Relation)
			if !ok {
				t.Fatalf("fixture %s: %s is not a global role relation", f.Name, fact.Relation)
			}
			scope, relation = tuples.Global(), label
		}
		add = append(add, tuples.Tuple{Scope: scope, Relation: relation, Subject: tuples.SubjectRef{Type: fact.Subject.Type, ID: fact.Subject.ID, Relation: fact.Subject.Relation}})
	}
	if err := repos.Tuples.ApplyTuples(context.Background(), tuples.Changes{Add: add}); err != nil {
		t.Fatalf("seed fixture %s: %v", f.Name, err)
	}
	components, err := authorization.New(repos, authorization.WithModel(model), authorization.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatalf("authorization.New(%s): %v", f.Name, err)
	}
	if g := f.Global; g != nil {
		components.Authorizer, err = authorizer.New(components.Decisions, authorizer.WithGlobalResource(g.Type, g.ID, g.RelationFor))
		if err != nil {
			t.Fatalf("authorizer.New(%s): %v", f.Name, err)
		}
	}
	return components
}

func subjectTypes(types ...string) []decisions.SubjectTypeRef {
	out := make([]decisions.SubjectTypeRef, 0, len(types))
	for _, t := range types {
		out = append(out, decisions.SubjectTypeRef{Type: t})
	}
	return out
}

func declare(subjects []decisions.SubjectTypeRef, names ...string) map[string]decisions.RelationDef {
	out := make(map[string]decisions.RelationDef, len(names))
	for _, n := range names {
		out[n] = decisions.RelationDef{AllowedSubjects: subjects}
	}
	return out
}

func directAny(names ...string) decisions.Expression {
	parts := make([]decisions.Expression, 0, len(names))
	for _, n := range names {
		parts = append(parts, decisions.Direct(n))
	}
	return decisions.Any(parts...)
}

// documentsModel is examples/auth-cms's schema with platform's admin relation
// renamed administrator, and the RoleIn label auditor declared as a relation
// so the protocol can answer and SchemaReader admits its facts.
func documentsModel() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "document", Def: decisions.ResourceTypeDef{
			Relations:   declare(subjectTypes("user"), "viewer"),
			Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"))},
		}},
		{Name: "project", Def: decisions.ResourceTypeDef{
			Relations: declare(subjectTypes("user"), "owner", "member", "auditor"),
			Permissions: map[string]decisions.Expression{
				"audit":         decisions.RoleIn("auditor"),
				"view":          decisions.AnyOf(decisions.Direct("owner"), decisions.Direct("member")),
				"manage_access": decisions.AnyOf(decisions.Direct("owner")),
			},
		}},
		{Name: "platform", Def: decisions.ResourceTypeDef{
			Relations:   declare(subjectTypes("user"), "administrator"),
			Permissions: map[string]decisions.Expression{"admin": decisions.AnyOf(decisions.Direct("administrator"))},
		}},
	})
}

func usersetsModel() decisions.Model {
	members := []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "group", Def: decisions.ResourceTypeDef{Relations: declare(members, "member")}},
		{Name: "document", Def: decisions.ResourceTypeDef{
			Relations:   declare(members, "viewer"),
			Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"))},
		}},
	})
}

// segoviaChainModel is a tenant/space/item hierarchy, the global resource type
// platform, and the test-only recursive node chain.
func segoviaChainModel() decisions.Model {
	users := subjectTypes("user")
	tenant := decisions.ResourceTypeDef{Relations: declare(users, "owner", "admin", "gps_team"), Permissions: map[string]decisions.Expression{
		"create_space": directAny("owner", "admin", "gps_team"), "list": directAny("owner", "admin", "gps_team"),
		"read": directAny("owner", "admin", "gps_team"), "manage": directAny("owner", "admin", "gps_team"), "delete": directAny("owner"),
		"manage_entitlements": directAny("gps_team"), "manage_gps_link": directAny("gps_team"),
	}}
	space := decisions.ResourceTypeDef{Relations: declare(users, "owner", "admin", "editor", "viewer", "vendor_collaborator"), Permissions: map[string]decisions.Expression{
		"view_timeline_space": directAny("owner", "admin", "editor", "viewer", "vendor_collaborator"),
		"collaborate_own":     directAny("vendor_collaborator"),
		"view":                directAny("owner", "admin", "editor", "viewer"),
		"propose":             directAny("owner", "admin", "editor"),
		"contribute":          directAny("owner", "admin", "editor"),
		"manage":              directAny("owner", "admin"),
		"manage_tool_limits":  decisions.Any(decisions.Through("tenant", "manage_entitlements")),
	}}
	space.Relations["tenant"] = decisions.RelationDef{AllowedSubjects: subjectTypes("tenant")}
	space.Relations["parent"] = decisions.RelationDef{AllowedSubjects: subjectTypes("space")}
	dashboard := decisions.ResourceTypeDef{Relations: declare(users, "owner", "admin", "editor", "viewer", "vendor_collaborator"), Permissions: map[string]decisions.Expression{
		"view":    decisions.Any(directAny("owner", "admin", "editor", "viewer", "vendor_collaborator"), decisions.Through("space", "view")),
		"comment": decisions.Any(directAny("owner", "admin", "editor", "viewer", "vendor_collaborator"), decisions.Through("space", "view")),
		"edit":    decisions.Any(directAny("owner", "admin", "editor"), decisions.Through("space", "contribute")),
		"manage":  decisions.Any(directAny("owner", "admin"), decisions.Through("space", "manage")),
	}}
	dashboard.Relations["space"] = decisions.RelationDef{AllowedSubjects: subjectTypes("space")}
	timeline := decisions.ResourceTypeDef{Relations: declare(users, "owner", "admin", "editor", "viewer", "vendor_collaborator", "personal_submitter"), Permissions: map[string]decisions.Expression{
		"submit_personal": directAny("personal_submitter"),
		"collaborate_own": decisions.Any(directAny("vendor_collaborator"), decisions.Through("space", "collaborate_own")),
		"view":            decisions.Any(directAny("owner", "admin", "editor", "viewer", "vendor_collaborator"), decisions.Through("space", "view_timeline_space")),
		"comment":         decisions.Any(directAny("owner", "admin", "editor", "viewer", "vendor_collaborator"), decisions.Through("space", "view_timeline_space")),
		"propose":         decisions.Any(directAny("owner", "admin", "editor", "vendor_collaborator"), decisions.Through("space", "propose"), decisions.Through("space", "collaborate_own")),
		"edit":            decisions.Any(directAny("owner", "admin", "editor"), decisions.Through("space", "contribute")),
		"review":          decisions.Any(directAny("owner", "admin", "editor"), decisions.Through("space", "contribute")),
		"manage":          decisions.Any(directAny("owner", "admin"), decisions.Through("space", "manage")),
	}}
	timeline.Relations["space"] = decisions.RelationDef{AllowedSubjects: subjectTypes("space")}
	clipbook := decisions.ResourceTypeDef{Relations: declare(users, "owner", "admin", "editor", "viewer"), Permissions: map[string]decisions.Expression{
		"view":   decisions.Any(directAny("owner", "admin", "editor", "viewer"), decisions.Through("space", "view")),
		"edit":   decisions.Any(directAny("owner", "admin", "editor"), decisions.Through("space", "contribute")),
		"manage": decisions.Any(directAny("owner", "admin"), decisions.Through("space", "manage")),
	}}
	clipbook.Relations["space"] = decisions.RelationDef{AllowedSubjects: subjectTypes("space")}
	node := decisions.ResourceTypeDef{Relations: declare(users, "viewer"), Permissions: map[string]decisions.Expression{
		"view": decisions.Any(decisions.Direct("viewer"), decisions.Through("parent", "view")),
	}}
	node.Relations["parent"] = decisions.RelationDef{AllowedSubjects: subjectTypes("node")}
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "tenant", Def: tenant}, {Name: "space", Def: space}, {Name: "dashboard", Def: dashboard}, {Name: "timeline", Def: timeline},
		{Name: "clipbook", Def: clipbook},
		{Name: "vendor_org", Def: decisions.ResourceTypeDef{Relations: declare(users, "member", "admin")}},
		{Name: "platform", Def: decisions.ResourceTypeDef{Relations: declare(users, "platform_admin")}},
		{Name: "node", Def: node},
	})
}

// gpsGlobalModel keeps global Role branches and exact RoleIn grants, declares
// the scoped role labels as relations so the protocol can answer them, and
// declares role_<label> on platform: the relations the engine adapter maps
// back to exact global role reads.
func gpsGlobalModel() decisions.Model {
	actors := subjectTypes("user", "service_account")
	role := decisions.Role
	scoped := func(roles ...string) decisions.Expression {
		checks := []decisions.Expression{role("steward")}
		for _, r := range roles {
			checks = append(checks, decisions.RoleIn(r))
		}
		return decisions.Any(checks...)
	}
	hub := func(roles ...string) decisions.Expression {
		checks := []decisions.Expression{role("steward"), role("firmwide_clients")}
		for _, r := range roles {
			checks = append(checks, decisions.RoleIn(r))
		}
		return decisions.Any(checks...)
	}
	link := func(def decisions.ResourceTypeDef) decisions.ResourceTypeDef {
		def.Relations["platform"] = decisions.RelationDef{AllowedSubjects: subjectTypes("platform")}
		return def
	}
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "platform", Def: decisions.ResourceTypeDef{
			Relations: declare(actors, "role_steward", "role_developer", "role_firmwide_clients", "role_librarian"),
			Permissions: map[string]decisions.Expression{
				"steward":                role("steward"),
				"developer":              decisions.Any(role("steward"), role("developer")),
				"delete":                 role("steward"),
				"partnership_financials": role("steward"),
				"changelog_viewer":       role("steward"),
				"librarian":              decisions.Any(role("steward"), role("librarian")),
			},
		}},
		{Name: "client_hub", Def: link(decisions.ResourceTypeDef{Relations: declare(actors, "viewer", "member"), Permissions: map[string]decisions.Expression{
			"view": hub("viewer", "member"), "contribute": hub("member"),
		}})},
		{Name: "section", Def: link(decisions.ResourceTypeDef{Relations: declare(actors, "member", "editor"), Permissions: map[string]decisions.Expression{
			"enter": scoped("member", "editor"), "edit": scoped("editor"),
		}})},
		{Name: "page", Def: link(decisions.ResourceTypeDef{Relations: declare(actors, "viewer"), Permissions: map[string]decisions.Expression{
			"view": scoped("viewer"),
		}})},
		{Name: "ledger", Def: decisions.ResourceTypeDef{Relations: declare(actors, "viewer", "editor"), Permissions: map[string]decisions.Expression{
			"view": decisions.Any(decisions.RoleIn("viewer"), decisions.RoleIn("editor")), "edit": decisions.RoleIn("editor"),
		}}},
	})
}
