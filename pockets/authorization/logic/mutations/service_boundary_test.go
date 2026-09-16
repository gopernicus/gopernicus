package mutations

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

type stubMutationRepo struct {
	policy IntegrityPolicy
	got    Command
	calls  int
}

func (r *stubMutationRepo) IntegrityPolicy() IntegrityPolicy { return r.policy }
func (r *stubMutationRepo) Apply(ctx context.Context, cmd Command, validate SemanticValidator) (*Result, error) {
	r.calls++
	r.got = cmd
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if validate != nil {
		if err := validate(cmd); err != nil {
			return nil, err
		}
	}
	return &Result{Outcome: OutcomeApplied}, nil
}
func validGrantCommand() Command {
	return Command{Target: Target{Kind: TargetResource, Type: "doc", ID: "one"}, Operation: OpRoleAssign, Roles: []RoleRow{{SubjectType: "user", SubjectID: "u", Role: "viewer"}}}
}
func TestServiceAppliesWithoutPrincipalAndKeepsBounds(t *testing.T) {
	repo := &stubMutationRepo{}
	svc, err := NewService(repo, WithLimits(authmodel.EvaluationLimits{MaxBatchSize: 2}))
	if err != nil {
		t.Fatal(err)
	}
	cmd := validGrantCommand()
	cmd.MaxAffectedRows = 100
	if _, err := svc.Apply(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	if repo.got.MaxAffectedRows != 2 {
		t.Fatal("caller widened bound")
	}
	cmd.MaxAffectedRows = 1
	if _, err := svc.Apply(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	if repo.got.MaxAffectedRows != 1 {
		t.Fatal("caller tighter bound ignored")
	}
	cmd.Roles = append(cmd.Roles, cmd.Roles[0], cmd.Roles[0])
	if _, err := svc.Apply(t.Context(), cmd); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("unbounded request: %v", err)
	}
}
func TestServiceRetainsModelShapeValidationWithoutDecisions(t *testing.T) {
	model, err := decisions.Compile(decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{"doc": {Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "service"}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &stubMutationRepo{}
	svc, err := NewService(repo, WithModel(model))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Apply(t.Context(), validGrantCommand()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("shape bypass: %v", err)
	}
}
func TestServiceRejectsCanceledMalformedAndGenericTeardown(t *testing.T) {
	repo := &stubMutationRepo{}
	svc, _ := NewService(repo)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.Apply(ctx, validGrantCommand()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := svc.Apply(t.Context(), Command{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := svc.Apply(t.Context(), Command{Target: validGrantCommand().Target, Operation: OpTeardown}); !errors.Is(err, ErrTeardownViaTypedMethod) {
		t.Fatal(err)
	}
	if repo.calls != 0 {
		t.Fatal("invalid command reached repository")
	}
}
func TestServiceConstructionAndUnconfigured(t *testing.T) {
	for _, opts := range [][]Option{{nil}, {WithLimits(authmodel.EvaluationLimits{MaxBatchSize: -1})}} {
		if _, err := NewService(nil, opts...); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	if _, err := NewService((*stubMutationRepo)(nil)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	svc, err := NewService(nil)
	if !errors.Is(err, ErrMutationsNotConfigured) || svc != nil {
		t.Fatalf("nil repository constructed writer: %v %v", svc, err)
	}
	if _, err := svc.Apply(t.Context(), validGrantCommand()); !errors.Is(err, ErrMutationsNotConfigured) {
		t.Fatal(err)
	}
	repo := &stubMutationRepo{policy: IntegrityPolicy{Rules: []IntegrityRule{{Relation: "owner", MinSubjects: -1}}}}
	if _, err := NewService(repo); !errors.Is(err, ErrInvalidIntegrityPolicy) {
		t.Fatal(err)
	}
}
func TestIntegrityStateDeduplicatesAndRejectsForeignFacts(t *testing.T) {
	p := IntegrityPolicy{Rules: []IntegrityRule{{Relation: "owner", MinSubjects: 2}}}
	scope := tuples.On("doc", "d")
	f := tuples.Tuple{Scope: scope, Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "one"}}
	if err := p.ValidateState(scope, []tuples.Tuple{f, f}); !errors.Is(err, ErrInvariantBlocked) {
		t.Fatalf("duplicates inflated minimum: %v", err)
	}
	other := f
	other.Subject.ID = "two"
	if err := p.ValidateState(scope, []tuples.Tuple{f, other}); err != nil {
		t.Fatal(err)
	}
	other.Scope = tuples.On("doc", "elsewhere")
	if err := p.ValidateState(scope, []tuples.Tuple{f, other}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
}
