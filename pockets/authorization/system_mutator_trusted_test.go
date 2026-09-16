package authorization

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func newTrustedComponents(t *testing.T) Components {
	t.Helper()
	st := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{}))
	comps, err := New(Repositories{
		Tuples: st.Tuples(), Mutations: st.Mutations(),
	}, WithModel(lifecycleModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func TestTupleWriterGrantRelationshipTrustedApplies(t *testing.T) {
	comps := newTrustedComponents(t)
	ctx := context.Background()

	rcpt, err := comps.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{

		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      subjU("u1"),
	})
	if err != nil {
		t.Fatalf("GrantRelationship: %v", err)
	}
	if rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("want applied, got %s", rcpt.Outcome)
	}
	res, err := comps.Decisions.Check(ctx, authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "edit", Resource: authmodel.Resource{Type: "doc", ID: "d1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("trusted grant not visible to Check: allowed=%v err=%v", res.Allowed, err)
	}
}

func TestTupleWriterTrustedRunsSemanticValidator(t *testing.T) {
	comps := newTrustedComponents(t)
	_, err := comps.Mutations.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{

		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "editor",
		Subject:      tuples.SubjectRef{Type: "service", ID: "s1"},
	})
	if err == nil {
		t.Fatalf("trusted grant with a disallowed subject must be rejected by the semantic validator")
	}
}

func TestTupleWriterAssignRoleTrusted(t *testing.T) {
	comps := newTrustedComponents(t)
	ctx := context.Background()

	if _, err := comps.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{

		Subject: authmodel.PrincipalRef{Type: "user", ID: "u1"},
		Role:    "auditor", Scope: tuples.Global(),
	}); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	ok, err := comps.Roles.HasRole(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "auditor")
	if err != nil || !ok {
		t.Fatalf("global trusted role not visible via exact HasRole: ok=%v err=%v", ok, err)
	}
}

func TestTrustedDuplicateGrantReportsNoChange(t *testing.T) {
	comps := newTrustedComponents(t)
	ctx := context.Background()

	// Deterministic id from the invitation operation identity (its resulting tuple).
	cmd := mutations.GrantRelationshipCommand{

		ResourceType: "doc",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      subjU("u1"),
	}

	first, err := comps.Mutations.GrantRelationship(ctx, cmd)
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if first.Outcome != mutations.OutcomeApplied {
		t.Fatalf("first grant: want applied, got outcome=%s", first.Outcome)
	}

	replay, err := comps.Mutations.GrantRelationship(ctx, cmd)
	if err != nil {
		t.Fatalf("replay grant: %v", err)
	}
	if replay.Outcome != mutations.OutcomeNoChange {
		t.Fatalf("duplicate grant: %+v", replay)
	}
}
