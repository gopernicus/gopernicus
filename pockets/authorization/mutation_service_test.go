package authorization

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// -----------------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------------

type stubDecisionView struct {
	reads []mutations.Target
}

func (v *stubDecisionView) ForModel(relationships.ReadModel) relationships.PermissionReader {
	return v // This stub has no stored tuples; every model-scoped read denies.
}

func (v *stubDecisionView) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, limit int) (bool, error) {
	return v.CheckRelationBounded(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation, subjectType, subjectID, limit)
}

func (v *stubDecisionView) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	return v.RelationTargets(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation)
}

func (v *stubDecisionView) CheckRelation(_ context.Context, scope mutations.Target, _, _, _ string) (bool, error) {
	v.reads = append(v.reads, scope)
	return false, nil
}

func (v *stubDecisionView) CheckRelationBounded(_ context.Context, scope mutations.Target, _, _, _ string, _ int) (bool, error) {
	v.reads = append(v.reads, scope)
	return false, nil
}

func (v *stubDecisionView) RelationTargets(_ context.Context, scope mutations.Target, _ string) ([]relationships.RelationTarget, error) {
	v.reads = append(v.reads, scope)
	return nil, nil
}

func (v *stubDecisionView) HasRole(_ context.Context, scope mutations.Target, _, _, _ string) (bool, error) {
	v.reads = append(v.reads, scope)
	return false, nil
}

// stubMutationRepo runs guards through its view, proving the guarded write path
// executes inside the repository (not the outer Service).
type stubMutationRepo struct {
	view            mutations.StoreDecisionView
	receipt         *mutations.Result
	applyErr        error
	gotCmd          mutations.Command
	applyGuarded    bool
	applyTrusted    bool
	guardGotNilView bool
}

func (r *stubMutationRepo) GuardianPolicy() mutations.GuardianPolicy {
	return mutations.GuardianPolicy{}
}

func (r *stubMutationRepo) Apply(_ context.Context, cmd mutations.Command, _ mutations.SemanticValidator) (*mutations.Result, error) {
	r.applyTrusted = true
	r.gotCmd = cmd
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	return r.receipt, nil
}

func (r *stubMutationRepo) ApplyGuarded(ctx context.Context, cmd mutations.Command, guard mutations.Guard, _ mutations.SemanticValidator) (*mutations.Result, error) {
	r.applyGuarded = true
	r.gotCmd = cmd
	if r.view == nil {
		r.guardGotNilView = true
	}
	if err := guard(ctx, r.view); err != nil {
		return nil, err
	}
	return r.receipt, nil
}

// stubGuard captures what AuthorizeMutation received and optionally reads a scope
// through the supplied view.
type stubGuard struct {
	gotAttempt mutations.MutationAttempt
	gotView    mutations.DecisionView
	readScope  *mutations.Target
	err        error
}

func (g *stubGuard) AuthorizeMutation(ctx context.Context, attempt mutations.MutationAttempt, view mutations.DecisionView) error {
	g.gotAttempt = attempt
	g.gotView = view
	if g.readScope != nil {
		_, _ = view.CheckRelation(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "owner", authmodel.Resource{Type: g.readScope.Type, ID: g.readScope.ID})
	}
	return g.err
}

func validGrantCommand(t *testing.T) mutations.Command {
	t.Helper()
	return mutations.Command{

		Target:        mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"},
		Operation:     mutations.OpGrant,
		Relationships: []mutations.RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}},
	}
}

func actorU1() mutations.Actor {
	return mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: "u1"}}
}

// -----------------------------------------------------------------------------
// Actor
// -----------------------------------------------------------------------------

func TestActorValidateRejectsEmpty(t *testing.T) {
	if err := (mutations.Actor{}).Validate(); err == nil {
		t.Fatalf("empty actor must be invalid")
	}
	if err := (mutations.Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user"}}).Validate(); err == nil {
		t.Fatalf("actor missing id must be invalid")
	}
	if err := actorU1().Validate(); err != nil {
		t.Fatalf("concrete actor must be valid, got %v", err)
	}
}

// TestActorHasNoConstructibleSystemKind pins the structural rule: an Actor is only
// the concrete principal pair — there is no field a caller can set to claim
// "system"/trusted status. Trusted writes go through SystemMutator only.
func TestActorHasNoConstructibleSystemKind(t *testing.T) {
	at := reflect.TypeOf(mutations.Actor{})
	for i := 0; i < at.NumField(); i++ {
		name := strings.ToLower(at.Field(i).Name)
		if strings.Contains(name, "system") || strings.Contains(name, "kind") || strings.Contains(name, "trust") {
			t.Fatalf("Actor exposes a privilege field %q; trusted writes must go through SystemMutator", at.Field(i).Name)
		}
	}
}

// -----------------------------------------------------------------------------
// Guard composition (the AZ3-0.4 seam)
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Construction matrix
// -----------------------------------------------------------------------------

func mustComponents(t *testing.T, repos Repositories, opts ...Option) Components {
	t.Helper()
	comps, err := New(repos, opts...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// TestConstructionReturnsComponents proves New returns the bundle: a
// host-facing Service and a separately held SystemMutator, both non-nil.
func TestConstructionReturnsComponents(t *testing.T) {
	comps := mustComponents(t, Repositories{Relationships: &relFake{}, Roles: &roleFake{}}, WithRelationshipModel(validModel()))
	if comps.Mutations == nil {
		t.Fatalf("Components is nil")
	}
	if comps.SystemMutator == nil {
		t.Fatalf("Components.SystemMutator is nil")
	}
}

// TestConstructionGuardWithoutMutationsFails proves a guard with no atomic write
// path is a half-enabled system that fails at boot.
func TestConstructionGuardWithoutMutationsFails(t *testing.T) {
	_, err := New(Repositories{Roles: &roleFake{}}, WithGuard(&stubGuard{}))
	if !errors.Is(err, mutations.ErrGuardWithoutMutations) {
		t.Fatalf("want ErrGuardWithoutMutations, got %v", err)
	}
}

// TestConstructionMutationsNotConfiguredKind pins the sentinel's sdk kind: the
// not-configured posture is a precondition refusal (sdk.ErrInvalidInput), never
// sdk.ErrUnavailable (saturation) and never sdk.ErrForbidden.
func TestConstructionMutationsNotConfiguredKind(t *testing.T) {
	if !errors.Is(mutations.ErrMutationsNotConfigured, sdk.ErrInvalidInput) {
		t.Fatalf("ErrMutationsNotConfigured must wrap sdk.ErrInvalidInput")
	}
	if errors.Is(mutations.ErrMutationsNotConfigured, sdk.ErrUnavailable) || errors.Is(mutations.ErrMutationsNotConfigured, sdk.ErrForbidden) {
		t.Fatalf("ErrMutationsNotConfigured must not wrap ErrUnavailable/ErrForbidden")
	}
}

// -----------------------------------------------------------------------------
// Audit
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// SystemMutator structural separation
// -----------------------------------------------------------------------------

// TestSystemMutatorUnreachableFromService pins the capability separation: no
// Service method returns a *SystemMutator and Service holds no *SystemMutator
// field, so HTTP handlers that receive a Service cannot reach trusted mutation.
func TestSystemMutatorUnreachableFromService(t *testing.T) {
	sysType := reflect.TypeOf(&mutations.SystemMutator{})

	svcPtr := reflect.TypeOf(&mutations.Service{})
	for i := 0; i < svcPtr.NumMethod(); i++ {
		m := svcPtr.Method(i)
		for j := 0; j < m.Type.NumOut(); j++ {
			if m.Type.Out(j) == sysType {
				t.Fatalf("Service.%s exposes *SystemMutator", m.Name)
			}
		}
	}

	svc := reflect.TypeOf(mutations.Service{})
	for i := 0; i < svc.NumField(); i++ {
		if svc.Field(i).Type == sysType {
			t.Fatalf("Service.%s is a *SystemMutator field", svc.Field(i).Name)
		}
	}
}

// -----------------------------------------------------------------------------
// TeardownResourceAuthorization (AZ3-3.2)
// -----------------------------------------------------------------------------

func teardownCmd(t *testing.T, reason string) mutations.TeardownResourceAuthorizationCommand {
	t.Helper()
	return mutations.TeardownResourceAuthorizationCommand{

		ResourceType: "doc",
		ResourceID:   "d1",
		Reason:       reason,
	}
}

// TestTeardownReasonRequired proves malformed or empty reasons are refused
// before any write.
func TestTeardownReasonRequired(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo})

	for _, reason := range []string{"", "   ", "bad\x00reason", string([]byte{0xff})} {
		if _, err := comps.SystemMutator.TeardownResourceAuthorization(context.Background(), teardownCmd(t, reason)); !errors.Is(err, mutations.ErrTeardownReasonRequired) {
			t.Fatalf("reason %q: want ErrTeardownReasonRequired, got %v", reason, err)
		}
	}
	if repo.applyTrusted {
		t.Fatalf("a reasonless teardown must not reach the repository")
	}
}

// TestTeardownReasonTooLong proves an over-bound reason is refused (the audit
// record must stay bounded).
func TestTeardownReasonTooLong(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo})

	long := strings.Repeat("x", mutations.MaxTeardownReasonLen+1)
	if _, err := comps.SystemMutator.TeardownResourceAuthorization(context.Background(), teardownCmd(t, long)); !errors.Is(err, mutations.ErrTeardownReasonRequired) {
		t.Fatalf("over-long reason: want ErrTeardownReasonRequired, got %v", err)
	}
	if repo.applyTrusted {
		t.Fatalf("an over-long-reason teardown must not reach the repository")
	}
}

// TestTeardownNotConfigured proves teardown fails closed with no atomic write path.
func TestTeardownNotConfigured(t *testing.T) {
	comps := mustComponents(t, Repositories{Roles: &roleFake{}})
	if _, err := comps.SystemMutator.TeardownResourceAuthorization(context.Background(), teardownCmd(t, "why")); !errors.Is(err, mutations.ErrMutationsNotConfigured) {
		t.Fatalf("want ErrMutationsNotConfigured, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// F1 remediation — the actor-facing seam rejects trusted-only ops and forces the
// purge blast-radius bound (privilege-escalation defense in depth)
// -----------------------------------------------------------------------------

// teardownSeamCommand builds a structurally valid OpTeardown Command, the shape a
// malicious/mistaken caller would push at the generic seam.
func teardownSeamCommand(t *testing.T) mutations.Command {
	t.Helper()
	return mutations.Command{

		Target:    mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d1"},
		Operation: mutations.OpTeardown,
	}
}

// TestServiceExposesNoActorTeardownMethod pins that there is no public Service method
// through which an actor could reach OpTeardown: teardown is reachable only through
// the separately held SystemMutator.TeardownResourceAuthorization. Complements
// TestActorSeamRejectsTrustedTeardown (which proves the seam itself rejects it).
func TestServiceExposesNoActorTeardownMethod(t *testing.T) {
	svcType := reflect.TypeOf(&mutations.Service{})
	for i := 0; i < svcType.NumMethod(); i++ {
		if strings.Contains(strings.ToLower(svcType.Method(i).Name), "teardown") {
			t.Fatalf("Service exposes a teardown-shaped method %q; teardown must go through SystemMutator only", svcType.Method(i).Name)
		}
	}
}

// TestActorPurgeCannotWidenBound proves the end-to-end guarantee over the REAL
// memstore: a crafted actor purge supplying an oversized MaxAffectedRows cannot widen
// the blast radius. The seam overwrites it with the resolved ceiling (2), so the
// store bound bites — three rows removed > 2 → invariant_blocked, nothing removed.
func TestActorPurgeCannotWidenBound(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, authmodel.EvaluationLimits{MaxBatchSize: 2})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, err := svc.Mutations.GrantRelationship(ctx, actorU1(), mutations.GrantRelationshipCommand{
			ResourceType: "doc", ResourceID: "big", Relation: "viewer", Subject: subjU(u),
		}); err != nil {
			t.Fatalf("seed %s: %v", u, err)
		}
	}
	rcpt, err := svc.Mutations.PurgeResourceAuthorization(ctx, actorU1(), mutations.PurgeResourceAuthorizationCommand{ResourceType: "doc", ResourceID: "big"})
	if !errors.Is(err, mutations.ErrInvariantBlocked) || rcpt != nil {
		t.Fatalf("widened actor purge: receipt=%+v err=%v", rcpt, err)
	}
	if targets, _ := svc.Relationships.GetRelationTargets(ctx, "doc", "big", "viewer"); len(targets) != 3 {
		t.Fatalf("blocked purge removed rows: %+v", targets)
	}
}

// TestSystemMutatorApplyRejectsTeardown proves the trusted generic Apply seam also
// refuses OpTeardown, forcing teardown through the reason-bearing typed method so the
// mandated teardown reason and audit are never bypassable — while
// TeardownResourceAuthorization with a reason still reaches the trusted Apply path.
func TestSystemMutatorApplyRejectsTeardown(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo})

	_, err := comps.SystemMutator.Apply(context.Background(), teardownSeamCommand(t))
	if !errors.Is(err, mutations.ErrTeardownViaTypedMethod) {
		t.Fatalf("SystemMutator.Apply teardown: want ErrTeardownViaTypedMethod, got %v", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ErrTeardownViaTypedMethod must wrap sdk.ErrInvalidInput")
	}
	if repo.applyTrusted {
		t.Fatalf("rejected teardown must not reach the repository")
	}

	if _, err := comps.SystemMutator.TeardownResourceAuthorization(context.Background(), teardownCmd(t, "resource deleted")); err != nil {
		t.Fatalf("TeardownResourceAuthorization with a reason must still work, got %v", err)
	}
	if !repo.applyTrusted {
		t.Fatalf("typed teardown did not reach the trusted Apply path")
	}
}

func TestSystemMutatorRevokeRelationship(t *testing.T) {
	repo := &stubMutationRepo{receipt: &mutations.Result{Outcome: mutations.OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo})

	cmd := mutations.RevokeRelationshipCommand{

		ResourceType: "dashboard",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      relationships.SubjectRef{Type: "user", ID: "u1"},
	}
	if _, err := comps.SystemMutator.RevokeRelationship(context.Background(), cmd); err != nil {
		t.Fatalf("SystemMutator.RevokeRelationship: %v", err)
	}
	if !repo.applyTrusted || repo.applyGuarded {
		t.Fatalf("revoke did not take the trusted unguarded Apply path (trusted=%v guarded=%v)", repo.applyTrusted, repo.applyGuarded)
	}
	if repo.gotCmd.Operation != mutations.OpRevoke {
		t.Errorf("command operation = %q, want OpRevoke", repo.gotCmd.Operation)
	}
	if repo.gotCmd.Target != (mutations.Target{Kind: mutations.TargetResource, Type: cmd.ResourceType, ID: cmd.ResourceID}) || len(repo.gotCmd.Relationships) != 1 || repo.gotCmd.Relationships[0].Subject != cmd.Subject {
		t.Errorf("trusted revoke command diverges from the shared builder: %+v", repo.gotCmd)
	}

	// The trusted path still reports guardian refusal as an error.
	repo.applyErr = mutations.ErrInvariantBlocked
	rec, err := comps.SystemMutator.RevokeRelationship(context.Background(), cmd)
	if !errors.Is(err, mutations.ErrInvariantBlocked) || rec != nil {
		t.Fatalf("guardian refusal: receipt=%+v err=%v", rec, err)
	}
}
