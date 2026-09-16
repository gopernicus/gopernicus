package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// lifecycleModel declares a resource type with the relations the lifecycle tests
// grant/replace; "edit" is a permission over owner/editor.
func lifecycleModel() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{{
		Name: "doc",
		Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"owner":  {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
				"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]decisions.Expression{"edit": decisions.AnyOf(decisions.Direct("owner"), decisions.Direct("editor"))},
		},
	}})
}

// newLifecycle builds a Service over the real memstore bundle with the
// integrity invariant disabled (empty policy), so the grant/revoke/replace/purge
// lifecycle is exercised without fighting last-owner protection (that is AZ3-3.2).
func newLifecycle(t *testing.T, limits authmodel.EvaluationLimits) Components {
	t.Helper()
	st := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{}))
	comps, err := New(Repositories{
		Tuples: st.Tuples(), Mutations: st.Mutations(),
	}, WithModel(lifecycleModel()), WithLimits(limits))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

func subjU(id string) relationships.SubjectRef { return relationships.SubjectRef{Type: "user", ID: id} }

func TestGrantRelationshipApplies(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()

	rcpt, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil {
		t.Fatalf("GrantRelationship: %v", err)
	}
	if rcpt == nil || rcpt.Outcome != mutations.OutcomeApplied {
		t.Fatalf("want applied non-replay receipt, got %+v", rcpt)
	}

	targets, err := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "editor")
	if err != nil || len(targets) != 1 {
		t.Fatalf("grant not visible to reads: targets=%+v err=%v", targets, err)
	}
}

func TestGrantRelationshipRepeatedCallReportsCurrentState(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	cmd := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2")}
	for i, want := range []mutations.Outcome{mutations.OutcomeApplied, mutations.OutcomeNoChange} {
		got, err := svc.Mutations.GrantRelationship(ctx, cmd)
		if err != nil || got == nil || got.Outcome != want {
			t.Fatalf("call %d: %+v, %v", i, got, err)
		}
	}
	if _, err := svc.Mutations.RevokeRelationship(ctx, mutations.RevokeRelationshipCommand(cmd)); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Mutations.GrantRelationship(ctx, cmd)
	if err != nil || got == nil || got.Outcome != mutations.OutcomeApplied {
		t.Fatalf("regrant after revoke: %+v, %v", got, err)
	}
}

func TestIndependentRelationshipLabelsAndExactAtomicSwap(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	for _, label := range []string{"viewer", "editor"} {
		got, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: label, Subject: subjU("u2")})
		if err != nil || got.Outcome != mutations.OutcomeApplied {
			t.Fatalf("grant %s: %+v, %v", label, got, err)
		}
	}
	fact := func(label string) tuples.Tuple {
		return tuples.Tuple{Scope: tuples.On("doc", "d1"), Relation: label, Subject: subjU("u2")}
	}
	result, err := svc.Mutations.Apply(ctx, mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"}, Operation: mutations.OpBatch, Tuples: tuples.Changes{Remove: []tuples.Tuple{fact("viewer")}, Add: []tuples.Tuple{fact("owner")}}})
	if err != nil || result.Outcome != mutations.OutcomeApplied {
		t.Fatalf("atomic swap: %+v, %v", result, err)
	}
	for label, want := range map[string]bool{"viewer": false, "editor": true, "owner": true} {
		got, err := svc.Roles.HasRoleIn(ctx, prinU("u2"), label, authmodel.Resource{Type: "doc", ID: "d1"})
		if err != nil || got != want {
			t.Fatalf("%s = %v, %v; want %v", label, got, err, want)
		}
	}
}

// TestRevokeRelationshipAppliedAndNotFound proves a revoke of a present tuple applies
// and a revoke of an absent tuple is a committed not_found no-op, never an error.
func TestRevokeRelationshipAppliedAndNotFound(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()

	if _, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	applied, err := svc.Mutations.RevokeRelationship(ctx, mutations.RevokeRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	})
	if err != nil || applied.Outcome != mutations.OutcomeApplied {
		t.Fatalf("revoke present: outcome=%v err=%v", applied.Outcome, err)
	}
	absent, err := svc.Mutations.RevokeRelationship(ctx, mutations.RevokeRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u9"),
	})
	if err != nil || absent.Outcome != mutations.OutcomeNotFound {
		t.Fatalf("revoke absent: want not_found no-op, outcome=%v err=%v", absent.Outcome, err)
	}
}

// TestPurgeResourceAuthorizationBound proves the bulk purge respects its
// affected-row bound (the resolved MaxBatchSize): a purge within the bound applies,
// and one that would exceed it is invariant_blocked with nothing removed.
func TestPurgeResourceAuthorizationBound(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{MaxBatchSize: 2})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
			ResourceType: "doc", ResourceID: "big", Relation: "viewer", Subject: subjU(u),
		}); err != nil {
			t.Fatalf("seed %s: %v", u, err)
		}
	}
	blocked, err := svc.Mutations.PurgeResourceAuthorization(ctx, mutations.PurgeResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "big",
	})
	if !errors.Is(err, mutations.ErrInvariantBlocked) || blocked != nil {
		t.Fatalf("over-bound purge: receipt=%+v err=%v", blocked, err)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "big", "viewer"); len(targets) != 3 {
		t.Fatalf("blocked purge removed rows: %+v", targets)
	}

	// A resource within the bound purges cleanly.
	if _, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "small", Relation: "viewer", Subject: subjU("u1"),
	}); err != nil {
		t.Fatalf("seed small: %v", err)
	}
	ok, err := svc.Mutations.PurgeResourceAuthorization(ctx, mutations.PurgeResourceAuthorizationCommand{
		ResourceType: "doc", ResourceID: "small",
	})
	if err != nil || ok.Outcome != mutations.OutcomeApplied {
		t.Fatalf("within-bound purge: outcome=%v err=%v", ok.Outcome, err)
	}
}

func TestGrantReadOnlyPosture(t *testing.T) {
	st := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{}))
	comps, err := New(Repositories{
		Tuples: st.Tuples(),
	}, WithModel(lifecycleModel())) // no Guard → read-only posture
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := comps.Mutations.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2"),
	}); !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("read-only grant: want ErrMutationsNotConfigured, got %v", err)
	}
}

func TestRelationshipWriteNeedsNoRawFacade(t *testing.T) {
	st := memory.New()
	comps, err := New(Repositories{Tuples: st.Tuples(), Mutations: st.Mutations()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := comps.Mutations.GrantRelationship(context.Background(), mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2")})
	if err != nil || got.Outcome != mutations.OutcomeApplied {
		t.Fatalf("canonical grant: %+v, %v", got, err)
	}
}

func TestGrantSemanticValidatorConstrainsDeclaredSubjectShapes(t *testing.T) {
	svc := newLifecycle(t, authmodel.EvaluationLimits{})
	ctx := context.Background()
	_, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: relationships.SubjectRef{Type: "service", ID: "s1"}})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("declared subject constraint: %v", err)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "d1", "editor"); len(targets) != 0 {
		t.Fatalf("invalid grant wrote facts: %+v", targets)
	}
	if _, err := svc.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "opaque", Subject: subjU("u2")}); err != nil {
		t.Fatalf("unconstrained opaque label: %v", err)
	}
}

func TestRepeatedGrantUsesCurrentSchema(t *testing.T) {
	st := memory.New(memory.WithIntegrityPolicy(mutations.IntegrityPolicy{}))
	ctx := context.Background()

	svcOld, err := New(Repositories{
		Tuples: st.Tuples(), Mutations: st.Mutations(),
	}, WithModel(lifecycleModel()))
	if err != nil {
		t.Fatalf("NewService old: %v", err)
	}

	cmd := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u2")}
	_, err = svcOld.Mutations.GrantRelationship(ctx, cmd)
	if err != nil {
		t.Fatalf("original grant: %v", err)
	}

	// A newer model constrains editor to service subjects, sharing the same facts.
	newerModel := decisions.NewSchema([]decisions.ResourceSchema{{
		Name: "doc",
		Def: decisions.ResourceTypeDef{
			Relations:   map[string]decisions.RelationDef{"owner": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}, "editor": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "service"}}}},
			Permissions: map[string]decisions.Expression{"edit": decisions.AnyOf(decisions.Direct("owner"))},
		},
	}})
	svcNew, err := New(Repositories{
		Tuples: st.Tuples(), Mutations: st.Mutations(),
	}, WithModel(newerModel))
	if err != nil {
		t.Fatalf("NewService newer: %v", err)
	}

	result, err := svcNew.Mutations.GrantRelationship(ctx, cmd)
	if err == nil || result != nil {
		t.Fatalf("repeated grant of disallowed subject shape: %+v, %v", result, err)
	}
	if _, err := svcNew.Mutations.GrantRelationship(ctx, cmd); err == nil {
		t.Fatal("trusted grant bypassed current schema")
	}
	if _, err := svcNew.Mutations.RevokeRelationship(ctx, mutations.RevokeRelationshipCommand(cmd)); err != nil {
		t.Fatalf("disallowed subject shape must stay revocable: %v", err)
	}
	// A NEW command with the now-disallowed subject shape is rejected by the current schema.
	if _, err := svcNew.Mutations.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: "doc", ResourceID: "d1", Relation: "editor", Subject: subjU("u9"),
	}); err == nil {
		t.Fatalf("a fresh grant of the disallowed subject shape must be rejected")
	}
}
