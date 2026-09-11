package mutations

import (
	"context"
	"errors"
	"reflect"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type stubDecisionView struct {
	reads []Target
}

func (v *stubDecisionView) ForModel(relationships.ReadModel) relationships.PermissionReader {
	return v // This stub has no stored tuples; every model-scoped read denies.
}

func (v *stubDecisionView) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, limit int) (bool, error) {
	return v.CheckRelationBounded(ctx, Target{Kind: TargetResource, Type: resourceType, ID: resourceID}, relation, subjectType, subjectID, limit)
}

func (v *stubDecisionView) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	return v.RelationTargets(ctx, Target{Kind: TargetResource, Type: resourceType, ID: resourceID}, relation)
}

func (v *stubDecisionView) CheckRelation(_ context.Context, scope Target, _, _, _ string) (bool, error) {
	v.reads = append(v.reads, scope)
	return false, nil
}

func (v *stubDecisionView) CheckRelationBounded(_ context.Context, scope Target, _, _, _ string, _ int) (bool, error) {
	v.reads = append(v.reads, scope)
	return false, nil
}

func (v *stubDecisionView) RelationTargets(_ context.Context, scope Target, _ string) ([]relationships.RelationTarget, error) {
	v.reads = append(v.reads, scope)
	return nil, nil
}

func (v *stubDecisionView) HasRole(_ context.Context, scope Target, _, _, _ string) (bool, error) {
	v.reads = append(v.reads, scope)
	return false, nil
}

// stubMutationRepo runs guards through its view, proving the guarded write path
// executes inside the repository (not the outer Service).
type stubMutationRepo struct {
	view            *stubDecisionView
	receipt         *Result
	applyErr        error
	gotCmd          Command
	applyGuarded    bool
	applyTrusted    bool
	guardGotNilView bool
}

func (r *stubMutationRepo) GuardianPolicy() GuardianPolicy {
	return GuardianPolicy{}
}

func (r *stubMutationRepo) Apply(_ context.Context, cmd Command, _ SemanticValidator) (*Result, error) {
	r.applyTrusted = true
	r.gotCmd = cmd
	if r.applyErr != nil {
		return nil, r.applyErr
	}
	return r.receipt, nil
}

func (r *stubMutationRepo) ApplyGuarded(ctx context.Context, cmd Command, guard Guard, _ SemanticValidator) (*Result, error) {
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
	readScope  *Target
	err        error
}

func (g *stubGuard) AuthorizeMutation(ctx context.Context, attempt MutationAttempt, view DecisionView) error {
	g.gotAttempt = attempt
	g.gotView = view
	if g.readScope != nil {
		_, _ = view.CheckRelation(ctx, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "owner", authmodel.Resource{Type: g.readScope.Type, ID: g.readScope.ID})
	}
	return g.err
}

func validGrantCommand(t *testing.T) Command {
	t.Helper()
	return Command{

		Target:        Target{Kind: TargetResource, Type: "doc", ID: "d1"},
		Operation:     OpGrant,
		Relationships: []RelationshipRow{{Relation: "viewer", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}},
	}
}

func actorU1() Actor {
	return Actor{PrincipalRef: authmodel.PrincipalRef{Type: "user", ID: "u1"}}
}

func teardownSeamCommand(t *testing.T) Command {
	t.Helper()
	return Command{

		Target:    Target{Kind: TargetResource, Type: "doc", ID: "d1"},
		Operation: OpTeardown,
	}
}

type relFake struct{ checkCalls int }

func (f *relFake) ForModel(relationships.ReadModel) relationships.Reader { return f }

func (f *relFake) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	f.checkCalls++
	return false, nil
}
func (f *relFake) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	return nil, nil
}
func (f *relFake) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	for range resourceIDs {
		f.checkCalls++
	}
	return nil, nil
}
func (f *relFake) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationships.RelationTarget, error) {
	return nil, nil
}
func (f *relFake) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return false, nil
}
func (f *relFake) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (f *relFake) CreateRelationships(ctx context.Context, relationships []relationships.CreateRelationship) error {
	return nil
}
func (f *relFake) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, targets []relationships.CreateRelationship) error {
	return nil
}
func (f *relFake) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target relationships.SubjectRef) error {
	return nil
}
func (f *relFake) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return nil
}
func (f *relFake) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return nil
}
func (f *relFake) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return nil
}
func (f *relFake) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return 0, nil
}
func (f *relFake) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationships.SubjectRelationshipFilter, req list.Request) (list.Page[relationships.SubjectRelationship], error) {
	return list.Page[relationships.SubjectRelationship]{}, nil
}
func (f *relFake) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationships.ResourceRelationshipFilter, req list.Request) (list.Page[relationships.ResourceRelationship], error) {
	return list.Page[relationships.ResourceRelationship]{}, nil
}
func (f *relFake) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	return nil, nil
}
func (f *relFake) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	return nil, nil
}

// roleFake is a trivial role.Storer for socket wiring/delegation tests.
type roleFake struct {
	hasCalls int
}

func (f *roleFake) Assign(ctx context.Context, a roles.Assignment) error { return nil }
func (f *roleFake) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	return nil
}
func (f *roleFake) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	f.hasCalls++
	return false, nil
}
func (f *roleFake) ListBySubject(ctx context.Context, subjectType, subjectID string, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{}, nil
}
func (f *roleFake) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.Assignment], error) {
	return list.Page[roles.Assignment]{}, nil
}
func (f *roleFake) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	return nil, false, nil
}
func (f *roleFake) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.EffectiveGrant], error) {
	return list.Page[roles.EffectiveGrant]{}, nil
}

func validModel() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "post",
		Def: relationships.ResourceTypeDef{
			Relations:   map[string]relationships.RelationDef{"owner": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]relationships.PermissionRule{"delete": relationships.AnyOf(relationships.Direct("owner"))},
		},
	}})
}

func TestGuardRetryReceivesFreshProposal(t *testing.T) {
	cmd := validGrantCommand(t)
	cmd.Roles = []RoleRow{{Role: "reader", SubjectType: "user", SubjectID: "u1"}}
	before := ProposedChange{Relationships: append([]RelationshipRow(nil), cmd.Relationships...), Roles: append([]RoleRow(nil), cmd.Roles...)}
	calls := 0
	guard := contractGuard(func(_ context.Context, attempt MutationAttempt, _ DecisionView) error {
		calls++
		if !reflect.DeepEqual(attempt.Change, before) {
			t.Fatalf("attempt %d saw another callback's edits: %+v", calls, attempt.Change)
		}
		attempt.Change.Relationships[0].Relation = "owner"
		attempt.Change.Roles[0].Role = "admin"
		return nil
	})
	callback := composeGuard(actorU1(), guard, cmd, nil, nil, authmodel.EvaluationLimits{})
	for range 2 {
		if err := callback(context.Background(), &stubDecisionView{}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGuardComposesIntoMutationGuard proves Actor + MutationGuard fold into the
// repository-level Guard closure: the closure carries the actor, operation, scope,
// and proposed change into the MutationAttempt and hands the guard the
// repository's view wrapped as the permission view (the store primitives pass
// through; CheckPermission rides on top).
func TestGuardComposesIntoMutationGuard(t *testing.T) {
	guard := &stubGuard{}
	cmd := validGrantCommand(t)
	actor := actorU1()
	closure := composeGuard(actor, guard, cmd, nil, nil, authmodel.EvaluationLimits{MaxGraphStates: authmodel.DefaultMaxGraphStates, MaxRelationTargets: authmodel.DefaultMaxRelationTargets})

	view := &stubDecisionView{}
	if err := closure(context.Background(), view); err != nil {
		t.Fatalf("closure returned %v", err)
	}
	if guard.gotAttempt.Actor != actor {
		t.Fatalf("actor not propagated: got %+v", guard.gotAttempt.Actor)
	}
	if guard.gotAttempt.Operation != cmd.Operation || guard.gotAttempt.Target != cmd.Target {
		t.Fatalf("operation/scope not propagated: %+v", guard.gotAttempt)
	}
	if len(guard.gotAttempt.Change.Relationships) != 1 {
		t.Fatalf("proposed change not propagated: %+v", guard.gotAttempt.Change)
	}
	pv, ok := guard.gotView.(permissionView)
	if !ok || pv.store != view {
		t.Fatalf("repository view not passed through to the guard: got %T", guard.gotView)
	}
}

func TestGuardReadsUseRepositoryView(t *testing.T) {
	depScope := Target{Kind: TargetResource, Type: "doc", ID: "d1"}
	guard := &stubGuard{readScope: &depScope}
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Result{Outcome: OutcomeApplied}}
	comps := mustComponents(t, testRepositories{Roles: &roleFake{}, Mutations: repo}, testConfig{Guard: guard})

	if _, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t)); err != nil {
		t.Fatalf("ApplyMutation: %v", err)
	}
	if !repo.applyGuarded {
		t.Fatalf("ApplyMutation did not go through the repository ApplyGuarded boundary")
	}
	deps := repo.view.reads
	if len(deps) != 1 || deps[0] != depScope {
		t.Fatalf("guard dependency not recorded through the boundary view: %+v", deps)
	}
	if guard.gotView == nil {
		t.Fatalf("guard did not receive the repository view")
	}
}

// TestConstructionNilGuardIsReadOnlyPosture proves a nil Guard yields the
// read-only posture: actor-facing ApplyMutation fails closed, while the trusted
// SystemMutator remains available.
func TestConstructionNilGuardIsReadOnlyPosture(t *testing.T) {
	repo := &stubMutationRepo{receipt: &Result{Outcome: OutcomeApplied}}
	comps := mustComponents(t, testRepositories{Roles: &roleFake{}, Mutations: repo}, testConfig{})

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
	comps := mustComponents(t, testRepositories{Roles: &roleFake{}}, testConfig{})
	if _, err := comps.Service.applyMutation(context.Background(), actorU1(), validGrantCommand(t)); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("actor path: want ErrMutationsNotConfigured, got %v", err)
	}
	if _, err := comps.SystemMutator.Apply(context.Background(), validGrantCommand(t)); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatalf("system path: want ErrMutationsNotConfigured, got %v", err)
	}
}

func TestConstructionFullActorMutationPostureApplies(t *testing.T) {
	guard := &stubGuard{}
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Result{Outcome: OutcomeApplied}}
	comps := mustComponents(t, testRepositories{Roles: &roleFake{}, Mutations: repo}, testConfig{Guard: guard})

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
}

func TestActorSeamRejectsTrustedTeardown(t *testing.T) {
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Result{Outcome: OutcomeApplied}}
	comps := mustComponents(t, testRepositories{Roles: &roleFake{}, Mutations: repo}, testConfig{Guard: &stubGuard{}})

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

// TestActorPurgeBoundNormalizedToMaxBatchSize proves the seam deterministically
// overwrites the caller-supplied MaxAffectedRows: an actor purge is forced to the
// resolved EvaluationLimits.MaxBatchSize, and every other actor operation carries no
// bound — a caller cannot smuggle its own blast-radius ceiling in.
func TestActorPurgeBoundNormalizedToMaxBatchSize(t *testing.T) {
	repo := &stubMutationRepo{view: &stubDecisionView{}, receipt: &Result{Outcome: OutcomeApplied}}
	comps := mustComponents(t, testRepositories{Relationships: &relFake{}, Mutations: repo}, testConfig{
		RelationshipModel: validModel(), Guard: &stubGuard{}, Limits: authmodel.EvaluationLimits{MaxBatchSize: 5},
	})
	ctx := context.Background()

	purge := Command{

		Target:          Target{Kind: TargetResource, Type: "post", ID: "p1"},
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

		Target:          Target{Kind: TargetResource, Type: "post", ID: "p1"},
		Operation:       OpGrant,
		Relationships:   []RelationshipRow{{Relation: "owner", Subject: relationships.SubjectRef{Type: "user", ID: "u1"}}},
		MaxAffectedRows: 999999,
	}
	if _, err := comps.Service.applyMutation(ctx, actorU1(), grant); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if repo.gotCmd.MaxAffectedRows != 0 {
		t.Fatalf("non-purge bound not zeroed: got %d want 0", repo.gotCmd.MaxAffectedRows)
	}
}

type contractGuard func(context.Context, MutationAttempt, DecisionView) error

func (f contractGuard) AuthorizeMutation(ctx context.Context, attempt MutationAttempt, view DecisionView) error {
	return f(ctx, attempt, view)
}

type testRepositories struct {
	Relationships relationships.Storer
	Roles         roles.Storer
	Mutations     MutationRepository
}
type testConfig struct {
	RelationshipModel relationships.Schema
	Guard             MutationGuard
	Limits            authmodel.EvaluationLimits
}

func mustComponents(t *testing.T, repos testRepositories, cfg testConfig) Components {
	t.Helper()
	services := Services{}
	if repos.Relationships != nil {
		parts, err := relationships.NewService(repos.Relationships, cfg.RelationshipModel, relationships.WithLimits(cfg.Limits))
		if err != nil {
			t.Fatal(err)
		}
		services.Relationships = parts.Service
	}
	if repos.Roles != nil {
		svc, err := roles.NewService(repos.Roles)
		if err != nil {
			t.Fatal(err)
		}
		services.Roles = svc
	}
	parts, err := NewService(repos.Mutations, services, WithGuard(cfg.Guard), WithLimits(cfg.Limits))
	if err != nil {
		t.Fatal(err)
	}
	return parts
}
