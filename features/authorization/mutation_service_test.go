package authorization

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// -----------------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------------

// stubDecisionView records every scope a guard reads, mimicking the repository's
// dependency-tracking view.
type stubDecisionView struct {
	deps []Dependency
}

func (v *stubDecisionView) CheckRelation(_ context.Context, scope ScopeKey, _, _, _ string) (bool, error) {
	v.deps = append(v.deps, Dependency{Scope: scope})
	return false, nil
}

func (v *stubDecisionView) HasRole(_ context.Context, scope ScopeKey, _, _, _ string) (bool, error) {
	v.deps = append(v.deps, Dependency{Scope: scope})
	return false, nil
}

func (v *stubDecisionView) Dependencies() []Dependency { return v.deps }

// stubMutationRepo runs guards through its view, proving the guarded write path
// executes inside the repository (not the outer Service).
type stubMutationRepo struct {
	view            *stubDecisionView
	receipt         *Receipt
	applyErr        error
	gotCmd          Command
	applyGuarded    bool
	applyTrusted    bool
	guardGotNilView bool
}

func (r *stubMutationRepo) Apply(_ context.Context, cmd Command, _ SemanticValidator) (*Receipt, error) {
	r.applyTrusted = true
	r.gotCmd = cmd
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	return r.receipt, nil
}

func (r *stubMutationRepo) ApplyGuarded(ctx context.Context, cmd Command, guard Guard, _ SemanticValidator) (*Receipt, error) {
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
	gotAttempt MutationAttempt
	gotView    DecisionView
	readScope  *ScopeKey
	err        error
}

func (g *stubGuard) AuthorizeMutation(ctx context.Context, attempt MutationAttempt, view DecisionView) error {
	g.gotAttempt = attempt
	g.gotView = view
	if g.readScope != nil {
		_, _ = view.CheckRelation(ctx, *g.readScope, "owner", "user", "u1")
	}
	return g.err
}

// stubAuditSink records events and can be made to fail.
type stubAuditSink struct {
	events []AuditEvent
	err    error
}

func (s *stubAuditSink) RecordMutation(_ context.Context, e AuditEvent) error {
	s.events = append(s.events, e)
	return s.err
}

func validGrantCommand(t *testing.T) Command {
	t.Helper()
	id, err := NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return Command{
		MutationID:    id,
		Scope:         ScopeKey{Kind: ScopeResource, Type: "doc", ID: "d1"},
		Operation:     OpGrant,
		Relationships: []RelationshipRow{{Relation: "viewer", Subject: SubjectRef{Type: "user", ID: "u1"}}},
	}
}

func actorU1() Actor { return Actor{PrincipalRef: PrincipalRef{Type: "user", ID: "u1"}} }

// -----------------------------------------------------------------------------
// Actor
// -----------------------------------------------------------------------------

func TestActorValidateRejectsEmpty(t *testing.T) {
	if err := (Actor{}).Validate(); err == nil {
		t.Fatalf("empty actor must be invalid")
	}
	if err := (Actor{PrincipalRef: PrincipalRef{Type: "user"}}).Validate(); err == nil {
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
	at := reflect.TypeOf(Actor{})
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

// TestGuardComposesIntoMutationGuard proves Actor + MutationGuard fold into the
// repository-level Guard closure: the closure carries the actor, operation, scope,
// and proposed change into the MutationAttempt and passes the repository's view
// straight through.
func TestGuardComposesIntoMutationGuard(t *testing.T) {
	guard := &stubGuard{}
	cmd := validGrantCommand(t)
	actor := actorU1()
	closure := composeGuard(actor, guard, cmd)

	view := &stubDecisionView{}
	if err := closure(context.Background(), view); err != nil {
		t.Fatalf("closure returned %v", err)
	}
	if guard.gotAttempt.Actor != actor {
		t.Fatalf("actor not propagated: got %+v", guard.gotAttempt.Actor)
	}
	if guard.gotAttempt.Operation != cmd.Operation || guard.gotAttempt.Scope != cmd.Scope {
		t.Fatalf("operation/scope not propagated: %+v", guard.gotAttempt)
	}
	if len(guard.gotAttempt.Change.Relationships) != 1 {
		t.Fatalf("proposed change not propagated: %+v", guard.gotAttempt.Change)
	}
	if guard.gotView != view {
		t.Fatalf("repository view not passed through to the guard")
	}
}

// TestGuardDependenciesRecordedThroughView proves a guard's authorization reads
// flow through the repository's dependency-tracking view (recorded for
// revision validation at commit), never the outer Service.
func TestGuardDependenciesRecordedThroughView(t *testing.T) {
	depScope := ScopeKey{Kind: ScopeResource, Type: "doc", ID: "d1"}
	guard := &stubGuard{readScope: &depScope}
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{Guard: guard})

	if _, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t)); err != nil {
		t.Fatalf("ApplyMutation: %v", err)
	}
	if !repo.applyGuarded {
		t.Fatalf("ApplyMutation did not go through the repository ApplyGuarded boundary")
	}
	deps := repo.view.Dependencies()
	if len(deps) != 1 || deps[0].Scope != depScope {
		t.Fatalf("guard dependency not recorded through the boundary view: %+v", deps)
	}
	if guard.gotView == nil {
		t.Fatalf("guard did not receive the repository view")
	}
}

// -----------------------------------------------------------------------------
// Construction matrix
// -----------------------------------------------------------------------------

func mustComponents(t *testing.T, repos Repositories, cfg Config) Components {
	t.Helper()
	comps, err := NewService(repos, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// TestConstructionReturnsComponents proves NewService returns the bundle: a
// host-facing Service and a separately held SystemMutator, both non-nil.
func TestConstructionReturnsComponents(t *testing.T) {
	comps := mustComponents(t, Repositories{Relationships: &relFake{}, Roles: &roleFake{}}, Config{Model: validModel()})
	if comps.Service == nil {
		t.Fatalf("Components.Service is nil")
	}
	if comps.SystemMutator == nil {
		t.Fatalf("Components.SystemMutator is nil")
	}
}

// TestConstructionNilGuardIsReadOnlyPosture proves a nil Guard yields the
// read-only posture: actor-facing ApplyMutation fails closed, while the trusted
// SystemMutator remains available.
func TestConstructionNilGuardIsReadOnlyPosture(t *testing.T) {
	repo := &stubMutationRepo{receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{})

	if _, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t)); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("read-only actor mutation: want ErrMutationsNotConfigured, got %v", err)
	}
	if repo.applyGuarded {
		t.Fatalf("read-only posture must not reach the repository")
	}
	if _, err := comps.SystemMutator.Apply(context.Background(), validGrantCommand(t)); err != nil {
		t.Fatalf("SystemMutator.Apply must remain available in read-only posture, got %v", err)
	}
	if !repo.applyTrusted {
		t.Fatalf("SystemMutator.Apply did not reach the repository")
	}
}

// TestConstructionReadOnlyWithoutMutations proves both actor and trusted paths
// fail closed with no Mutations repository wired at all.
func TestConstructionReadOnlyWithoutMutations(t *testing.T) {
	comps := mustComponents(t, Repositories{Roles: &roleFake{}}, Config{})
	if _, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t)); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("actor path: want ErrMutationsNotConfigured, got %v", err)
	}
	if _, err := comps.SystemMutator.Apply(context.Background(), validGrantCommand(t)); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("system path: want ErrMutationsNotConfigured, got %v", err)
	}
}

// TestConstructionGuardWithoutMutationsFails proves a guard with no atomic write
// path is a half-enabled system that fails at boot.
func TestConstructionGuardWithoutMutationsFails(t *testing.T) {
	_, err := NewService(Repositories{Roles: &roleFake{}}, Config{Guard: &stubGuard{}})
	if !errors.Is(err, ErrGuardWithoutMutations) {
		t.Fatalf("want ErrGuardWithoutMutations, got %v", err)
	}
}

// TestConstructionAuditWithoutGuardFails proves an orphaned actor-mutation setting
// (an AuditSink with no Guard) fails construction when a kind is wired.
func TestConstructionAuditWithoutGuardFails(t *testing.T) {
	_, err := NewService(Repositories{Roles: &roleFake{}, Mutations: &stubMutationRepo{}}, Config{Audit: &stubAuditSink{}})
	if !errors.Is(err, ErrAuditWithoutGuard) {
		t.Fatalf("want ErrAuditWithoutGuard, got %v", err)
	}
}

// TestConstructionMutationsNotConfiguredKind pins the sentinel's sdk kind: the
// not-configured posture is a precondition refusal (sdk.ErrInvalidInput), never
// sdk.ErrUnavailable (saturation) and never sdk.ErrForbidden.
func TestConstructionMutationsNotConfiguredKind(t *testing.T) {
	if !errors.Is(ErrMutationsNotConfigured, sdk.ErrInvalidInput) {
		t.Fatalf("ErrMutationsNotConfigured must wrap sdk.ErrInvalidInput")
	}
	if errors.Is(ErrMutationsNotConfigured, sdk.ErrUnavailable) || errors.Is(ErrMutationsNotConfigured, sdk.ErrForbidden) {
		t.Fatalf("ErrMutationsNotConfigured must not wrap ErrUnavailable/ErrForbidden")
	}
}

// TestConstructionFullActorMutationPostureApplies proves the full posture: guard
// runs inside the boundary, a receipt returns, and the attempt is audited as
// accepted.
func TestConstructionFullActorMutationPostureApplies(t *testing.T) {
	guard := &stubGuard{}
	sink := &stubAuditSink{}
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{Guard: guard, Audit: sink})

	receipt, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t))
	if err != nil {
		t.Fatalf("ApplyMutation: %v", err)
	}
	if receipt == nil || receipt.Outcome != OutcomeApplied {
		t.Fatalf("want applied receipt, got %+v", receipt)
	}
	if guard.gotAttempt.Actor != actorU1() {
		t.Fatalf("guard did not receive the actor")
	}
	if len(sink.events) != 1 || sink.events[0].Decision != AuditAccepted || sink.events[0].Outcome != OutcomeApplied {
		t.Fatalf("expected one accepted audit event, got %+v", sink.events)
	}
}

// -----------------------------------------------------------------------------
// Audit
// -----------------------------------------------------------------------------

// TestAuditRecordsDeniedAttempt proves a guard denial is audited as denied and
// commits nothing.
func TestAuditRecordsDeniedAttempt(t *testing.T) {
	guard := &stubGuard{err: fmt.Errorf("nope: %w", sdk.ErrForbidden)}
	sink := &stubAuditSink{}
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{Guard: guard, Audit: sink})

	if _, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t)); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("denied mutation: want forbidden, got %v", err)
	}
	if len(sink.events) != 1 || sink.events[0].Decision != AuditDenied {
		t.Fatalf("expected one denied audit event, got %+v", sink.events)
	}
}

// TestAuditSinkFailureDoesNotChangeMutation proves a failing sink is best-effort:
// the committed receipt is returned unchanged.
func TestAuditSinkFailureDoesNotChangeMutation(t *testing.T) {
	sink := &stubAuditSink{err: errors.New("sink down")}
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{Guard: &stubGuard{}, Audit: sink})

	receipt, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t))
	if err != nil {
		t.Fatalf("sink failure must not fail the mutation, got %v", err)
	}
	if receipt == nil || receipt.Outcome != OutcomeApplied {
		t.Fatalf("sink failure changed the committed result: %+v", receipt)
	}
}

// -----------------------------------------------------------------------------
// SystemMutator structural separation
// -----------------------------------------------------------------------------

// TestSystemMutatorUnreachableFromService pins the capability separation: no
// Service method returns a *SystemMutator and Service holds no *SystemMutator
// field, so HTTP handlers that receive a Service cannot reach trusted mutation.
func TestSystemMutatorUnreachableFromService(t *testing.T) {
	sysType := reflect.TypeOf(&SystemMutator{})

	svcPtr := reflect.TypeOf(&Service{})
	for i := 0; i < svcPtr.NumMethod(); i++ {
		m := svcPtr.Method(i)
		for j := 0; j < m.Type.NumOut(); j++ {
			if m.Type.Out(j) == sysType {
				t.Fatalf("Service.%s exposes *SystemMutator", m.Name)
			}
		}
	}

	svc := reflect.TypeOf(Service{})
	for i := 0; i < svc.NumField(); i++ {
		if svc.Field(i).Type == sysType {
			t.Fatalf("Service.%s is a *SystemMutator field", svc.Field(i).Name)
		}
	}
}

// -----------------------------------------------------------------------------
// TeardownAuthorizationScope (AZ3-3.2)
// -----------------------------------------------------------------------------

func teardownCmd(t *testing.T, reason string) TeardownAuthorizationScopeCommand {
	t.Helper()
	id, err := NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return TeardownAuthorizationScopeCommand{
		MutationID:   id,
		ResourceType: "doc",
		ResourceID:   "d1",
		Reason:       reason,
	}
}

// TestTeardownReasonRequired proves an empty or whitespace-only reason is refused
// before any write.
func TestTeardownReasonRequired(t *testing.T) {
	repo := &stubMutationRepo{receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{})

	for _, reason := range []string{"", "   "} {
		if _, err := comps.SystemMutator.TeardownAuthorizationScope(context.Background(), teardownCmd(t, reason)); !errors.Is(err, ErrTeardownReasonRequired) {
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
	repo := &stubMutationRepo{receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{})

	long := strings.Repeat("x", MaxTeardownReasonLen+1)
	if _, err := comps.SystemMutator.TeardownAuthorizationScope(context.Background(), teardownCmd(t, long)); !errors.Is(err, ErrTeardownReasonRequired) {
		t.Fatalf("over-long reason: want ErrTeardownReasonRequired, got %v", err)
	}
	if repo.applyTrusted {
		t.Fatalf("an over-long-reason teardown must not reach the repository")
	}
}

// TestTeardownAppliesTrustedAndAudits proves a valid teardown runs the trusted
// (unguarded) Apply as OpTeardown, returns its receipt, and records the reason on
// the audit sink as an accepted, actor-free event.
func TestTeardownAppliesTrustedAndAudits(t *testing.T) {
	sink := &stubAuditSink{}
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{Guard: &stubGuard{}, Audit: sink})

	receipt, err := comps.SystemMutator.TeardownAuthorizationScope(context.Background(), teardownCmd(t, "resource deleted by admin"))
	if err != nil {
		t.Fatalf("TeardownAuthorizationScope: %v", err)
	}
	if receipt == nil || receipt.Outcome != OutcomeApplied {
		t.Fatalf("want applied receipt, got %+v", receipt)
	}
	if !repo.applyTrusted || repo.applyGuarded {
		t.Fatalf("teardown must use the trusted Apply and bypass the guard (trusted=%v guarded=%v)", repo.applyTrusted, repo.applyGuarded)
	}
	if repo.gotCmd.Operation != OpTeardown {
		t.Fatalf("teardown command operation = %q, want %q", repo.gotCmd.Operation, OpTeardown)
	}
	if repo.gotCmd.Scope.Kind != ScopeResource || repo.gotCmd.Scope.Type != "doc" || repo.gotCmd.Scope.ID != "d1" {
		t.Fatalf("teardown scope not built from the command: %+v", repo.gotCmd.Scope)
	}
	if len(sink.events) != 1 {
		t.Fatalf("want one audit event, got %+v", sink.events)
	}
	ev := sink.events[0]
	if ev.Decision != AuditAccepted || ev.Detail != "resource deleted by admin" || ev.Operation != OpTeardown {
		t.Fatalf("teardown audit event mismatch: %+v", ev)
	}
	if (ev.Actor != Actor{}) {
		t.Fatalf("teardown is actor-free; audit event must carry a zero Actor, got %+v", ev.Actor)
	}
}

// TestTeardownNotConfigured proves teardown fails closed with no atomic write path.
func TestTeardownNotConfigured(t *testing.T) {
	comps := mustComponents(t, Repositories{Roles: &roleFake{}}, Config{})
	if _, err := comps.SystemMutator.TeardownAuthorizationScope(context.Background(), teardownCmd(t, "why")); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("want ErrMutationsNotConfigured, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// F1 remediation — the actor-facing seam rejects trusted-only ops and forces the
// purge blast-radius bound (privilege-escalation defense in depth)
// -----------------------------------------------------------------------------

// teardownSeamCommand builds a structurally valid OpTeardown Command, the shape a
// malicious/mistaken caller would push at the generic seam.
func teardownSeamCommand(t *testing.T) Command {
	t.Helper()
	return Command{
		MutationID: mustID(t),
		Scope:      ScopeKey{Kind: ScopeResource, Type: "doc", ID: "d1"},
		Operation:  OpTeardown,
	}
}

// TestActorSeamRejectsTrustedTeardown proves the (now-unexported) actor-facing seam
// refuses OpTeardown before composing the guard: teardown is trusted-only, so a
// guarded actor cannot zero a protected scope's last owner. Nothing reaches the
// repository, so the MutationID is unconsumed and no receipt is written.
func TestActorSeamRejectsTrustedTeardown(t *testing.T) {
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{Guard: &stubGuard{}})

	_, err := comps.Service.applyMutation(context.Background(), actorU1(), teardownSeamCommand(t))
	if !errors.Is(err, ErrTrustedOperationRequired) {
		t.Fatalf("actor teardown: want ErrTrustedOperationRequired, got %v", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ErrTrustedOperationRequired must wrap sdk.ErrInvalidInput")
	}
	if errors.Is(err, sdk.ErrForbidden) || errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("ErrTrustedOperationRequired must not wrap ErrForbidden/ErrUnavailable")
	}
	if repo.applyGuarded || repo.applyTrusted {
		t.Fatalf("rejected teardown must not reach the repository (guarded=%v trusted=%v)", repo.applyGuarded, repo.applyTrusted)
	}
}

// TestServiceExposesNoActorTeardownMethod pins that there is no public Service method
// through which an actor could reach OpTeardown: teardown is reachable only through
// the separately held SystemMutator.TeardownAuthorizationScope. Complements
// TestActorSeamRejectsTrustedTeardown (which proves the seam itself rejects it).
func TestServiceExposesNoActorTeardownMethod(t *testing.T) {
	svcType := reflect.TypeOf(&Service{})
	for i := 0; i < svcType.NumMethod(); i++ {
		if strings.Contains(strings.ToLower(svcType.Method(i).Name), "teardown") {
			t.Fatalf("Service exposes a teardown-shaped method %q; teardown must go through SystemMutator only", svcType.Method(i).Name)
		}
	}
}

// TestActorPurgeBoundNormalizedToMaxBatchSize proves the seam deterministically
// overwrites the caller-supplied MaxAffectedRows: an actor purge is forced to the
// resolved EvaluationLimits.MaxBatchSize, and every other actor operation carries no
// bound — a caller cannot smuggle its own blast-radius ceiling in.
func TestActorPurgeBoundNormalizedToMaxBatchSize(t *testing.T) {
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Relationships: &relFake{}, Mutations: repo}, Config{
		Model: validModel(), Guard: &stubGuard{}, Limits: EvaluationLimits{MaxBatchSize: 5},
	})
	ctx := context.Background()

	purge := Command{
		MutationID:      mustID(t),
		Scope:           ScopeKey{Kind: ScopeResource, Type: "post", ID: "p1"},
		Operation:       OpPurge,
		MaxAffectedRows: 999999,
	}
	if _, err := comps.Service.applyMutation(ctx, actorU1(), purge); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if repo.gotCmd.MaxAffectedRows != 5 {
		t.Fatalf("purge bound not forced to maxBatchSize: got %d want 5", repo.gotCmd.MaxAffectedRows)
	}

	grant := Command{
		MutationID:      mustID(t),
		Scope:           ScopeKey{Kind: ScopeResource, Type: "post", ID: "p1"},
		Operation:       OpGrant,
		Relationships:   []RelationshipRow{{Relation: "owner", Subject: SubjectRef{Type: "user", ID: "u1"}}},
		MaxAffectedRows: 999999,
	}
	if _, err := comps.Service.applyMutation(ctx, actorU1(), grant); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if repo.gotCmd.MaxAffectedRows != 0 {
		t.Fatalf("non-purge bound not zeroed: got %d want 0", repo.gotCmd.MaxAffectedRows)
	}
}

// TestActorPurgeCannotWidenBound proves the end-to-end guarantee over the REAL
// memstore: a crafted actor purge supplying an oversized MaxAffectedRows cannot widen
// the blast radius. The seam overwrites it with the resolved ceiling (2), so the
// store bound bites — three rows removed > 2 → invariant_blocked, nothing removed.
func TestActorPurgeCannotWidenBound(t *testing.T) {
	svc := newGuardedLifecycle(t, &opGuard{}, EvaluationLimits{MaxBatchSize: 2})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, err := svc.GrantRelationship(ctx, actorU1(), GrantRelationshipCommand{
			MutationID: mustID(t), ResourceType: "doc", ResourceID: "big", Relation: "viewer", Subject: subjU(u),
		}); err != nil {
			t.Fatalf("seed %s: %v", u, err)
		}
	}
	widen := Command{
		MutationID:      mustID(t),
		Scope:           ScopeKey{Kind: ScopeResource, Type: "doc", ID: "big"},
		Operation:       OpPurge,
		MaxAffectedRows: 999999,
	}
	rcpt, err := svc.applyMutation(ctx, actorU1(), widen)
	if err != nil || rcpt.Outcome != OutcomeInvariantBlocked {
		t.Fatalf("widened actor purge: want invariant_blocked, outcome=%v err=%v", rcpt.Outcome, err)
	}
	if targets, _ := svc.GetRelationTargets(ctx, "doc", "big", "viewer"); len(targets) != 3 {
		t.Fatalf("blocked purge removed rows: %+v", targets)
	}
}

// TestSystemMutatorApplyRejectsTeardown proves the trusted generic Apply seam also
// refuses OpTeardown, forcing teardown through the reason-bearing typed method so the
// mandated teardown reason and audit are never bypassable — while
// TeardownAuthorizationScope with a reason still reaches the trusted Apply path.
func TestSystemMutatorApplyRejectsTeardown(t *testing.T) {
	repo := &stubMutationRepo{receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{})

	_, err := comps.SystemMutator.Apply(context.Background(), teardownSeamCommand(t))
	if !errors.Is(err, ErrTeardownViaTypedMethod) {
		t.Fatalf("SystemMutator.Apply teardown: want ErrTeardownViaTypedMethod, got %v", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ErrTeardownViaTypedMethod must wrap sdk.ErrInvalidInput")
	}
	if repo.applyTrusted {
		t.Fatalf("rejected teardown must not reach the repository")
	}

	if _, err := comps.SystemMutator.TeardownAuthorizationScope(context.Background(), teardownCmd(t, "resource deleted")); err != nil {
		t.Fatalf("TeardownAuthorizationScope with a reason must still work, got %v", err)
	}
	if !repo.applyTrusted {
		t.Fatalf("typed teardown did not reach the trusted Apply path")
	}
}

// TestSystemMutatorRevokeRelationship proves the trusted revoke seam: it drives
// an OpRevoke command through the unguarded trusted Apply (not ApplyGuarded),
// building the SAME command the guarded Service.RevokeRelationship builds, and
// surfaces an invariant-blocked outcome (the guardian owner floor) in the
// receipt rather than bypassing it.
func TestSystemMutatorRevokeRelationship(t *testing.T) {
	repo := &stubMutationRepo{receipt: &Receipt{Outcome: OutcomeApplied}}
	comps := mustComponents(t, Repositories{Roles: &roleFake{}, Mutations: repo}, Config{})

	cmd := RevokeRelationshipCommand{
		MutationID:   DeriveMutationID("test/revoke", "dashboard", "d1", "owner", "user", "u1"),
		ResourceType: "dashboard",
		ResourceID:   "d1",
		Relation:     "owner",
		Subject:      SubjectRef{Type: "user", ID: "u1"},
	}
	if _, err := comps.SystemMutator.RevokeRelationship(context.Background(), cmd); err != nil {
		t.Fatalf("SystemMutator.RevokeRelationship: %v", err)
	}
	if !repo.applyTrusted || repo.applyGuarded {
		t.Fatalf("revoke did not take the trusted unguarded Apply path (trusted=%v guarded=%v)", repo.applyTrusted, repo.applyGuarded)
	}
	if repo.gotCmd.Operation != OpRevoke {
		t.Errorf("command operation = %q, want OpRevoke", repo.gotCmd.Operation)
	}
	if got := revokeRelationshipCommand(cmd); repo.gotCmd.Scope != got.Scope || len(repo.gotCmd.Relationships) != 1 {
		t.Errorf("trusted revoke command diverges from the shared builder: %+v", repo.gotCmd)
	}

	// An invariant-blocked outcome (last owner) surfaces in the receipt.
	repo.receipt = &Receipt{Outcome: OutcomeInvariantBlocked}
	rec, err := comps.SystemMutator.RevokeRelationship(context.Background(), cmd)
	if err != nil {
		t.Fatalf("invariant-blocked revoke must not error: %v", err)
	}
	if rec.Outcome != OutcomeInvariantBlocked {
		t.Errorf("outcome = %q, want invariant_blocked (guardian honored)", rec.Outcome)
	}
}
