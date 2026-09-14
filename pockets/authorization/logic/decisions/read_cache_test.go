package decisions_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

type operationReadSource struct {
	store  relationships.Storer
	scoped relationships.CheckReader
	model  relationships.ReadModel
	err    error
	calls  int
}

func (s *operationReadSource) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	s.model = model
	if s.store == nil {
		return nil
	}
	s.scoped = s.store.ForModel(model)
	return s
}

func (s *operationReadSource) CheckRelationWithGroupExpansion(ctx context.Context, rt, id, relation, st, sid string, limit int) (bool, error) {
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	return s.scoped.CheckRelationWithGroupExpansion(ctx, rt, id, relation, st, sid, limit)
}
func (s *operationReadSource) GetRelationTargets(ctx context.Context, rt, id, relation string) ([]relationships.RelationTarget, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.scoped.GetRelationTargets(ctx, rt, id, relation)
}
func (s *operationReadSource) CheckBatchDirect(ctx context.Context, rt string, ids []string, relation, st, sid string, limit int) (map[string]bool, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.scoped.CheckBatchDirect(ctx, rt, ids, relation, st, sid, limit)
}

func TestOperationReadWorkersParity(t *testing.T) {
	ctx := context.Background()
	store := memory.NewRelationships()
	eng := newRelationshipEngine(t, store, authmodel.EvaluationLimits{})
	roleStore := memory.NewRoles()
	roleService := newTestRoles(t, roleStore)
	c := decisions.NewCompositeForTest(eng.Service, roleService, mustCompile(t, compositeRoleModel(), eng), eng.Limits())
	grant(t, eng, "org", "o1", "member", "user", "u1")
	grant(t, eng, "project", "p1", "org", "org", "o1")
	grant(t, eng, "project", "p2", "viewer", "user", "u1")
	if err := roleStore.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "auditor"}); err != nil {
		t.Fatal(err)
	}
	source := &operationReadSource{store: store}
	reqs := []authmodel.CheckRequest{
		request("u1", "view", "project", "p1"),
		request("u1", "audit", "project", "p1"),
		request("u1", "view", "project", "p2"),
		request("u1", "view", "project", "missing"),
		request("u1", "fly", "project", "p1"),
		request("u1", "view", "project", "p1"),
	}
	for _, req := range reqs {
		want, err := c.Check(ctx, req)
		got, gotErr := decisions.CheckWithForTest(c, ctx, source, roleStore.HasExactRole, req)
		if err != nil || gotErr != nil || got != want {
			t.Fatalf("check %+v: got %+v/%v want %+v/%v", req, got, gotErr, want, err)
		}
		want, trace, err := c.CheckExplain(ctx, req)
		got, gotTrace, gotErr := decisions.CheckExplainWithForTest(c, ctx, source, roleStore.HasExactRole, req)
		if err != nil || gotErr != nil || got != want || !reflect.DeepEqual(trace, gotTrace) {
			t.Fatalf("explain %+v: got %+v/%+v/%v want %+v/%+v/%v", req, got, gotTrace, gotErr, want, trace, err)
		}
	}
	for _, batch := range [][]authmodel.CheckRequest{nil, reqs, {request("u1", "enter", "org", "o1"), request("u1", "enter", "org", "o2"), request("u1", "enter", "org", "o1")}} {
		want, err := c.CheckBatch(ctx, batch)
		got, gotErr := decisions.CheckBatchWithForTest(c, ctx, source, roleStore.HasExactRole, batch)
		if err != nil || gotErr != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("batch: got %+v/%v want %+v/%v", got, gotErr, want, err)
		}
	}
	if !source.model.Allows("project", "viewer", "user", "") || source.model.Allows("project", "viewer", "group", "member") {
		t.Fatal("operation source did not receive the compiled model")
	}
}

func TestOperationReadWorkersDoNotRetainReaders(t *testing.T) {
	ctx := context.Background()
	c, _, _ := newBothKinds(t, authmodel.EvaluationLimits{})
	alternate := memory.NewRelationships()
	alternateEngine := newRelationshipEngine(t, alternate, authmodel.EvaluationLimits{})
	grant(t, alternateEngine, "org", "o1", "member", "user", "u1")
	source := &operationReadSource{store: alternate}
	req := request("u1", "enter", "org", "o1")
	closed := errors.New("operation view closed")
	for _, operation := range []string{"check", "explain", "batch"} {
		t.Run(operation, func(t *testing.T) {
			source.err = nil
			run := func() (bool, error) {
				switch operation {
				case "check":
					result, err := decisions.CheckWithForTest(c, ctx, source, nil, req)
					return result.Allowed, err
				case "explain":
					result, _, err := decisions.CheckExplainWithForTest(c, ctx, source, nil, req)
					return result.Allowed, err
				default:
					results, err := decisions.CheckBatchWithForTest(c, ctx, source, nil, []authmodel.CheckRequest{req, req})
					if err != nil {
						return false, err
					}
					return results[0].Allowed, nil
				}
			}
			if allowed, err := run(); err != nil || !allowed {
				t.Fatalf("open source = %v/%v", allowed, err)
			}
			source.err = closed
			if allowed, err := run(); allowed || !errors.Is(err, closed) {
				t.Fatalf("closed retained source = %v/%v", allowed, err)
			}
			direct, err := c.Check(ctx, req)
			if err != nil || direct.Allowed {
				t.Fatalf("operation changed direct service = %+v/%v", direct, err)
			}
		})
	}
}

func TestOperationReadWorkersValidationAndLimits(t *testing.T) {
	ctx := context.Background()
	c, eng, _ := newBothKinds(t, authmodel.EvaluationLimits{MaxBatchSize: 1, MaxEvaluationSteps: 1})
	source := &operationReadSource{store: memory.NewRelationships()}
	req := request("u1", "enter", "org", "o1")
	if _, err := decisions.CheckBatchWithForTest(c, ctx, source, nil, []authmodel.CheckRequest{req, req}); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatal(err)
	}
	if _, err := decisions.CheckBatchWithForTest(c, ctx, source, nil, []authmodel.CheckRequest{{}}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if source.calls != 0 {
		t.Fatal("invalid or oversized batch performed reads")
	}
	if _, err := decisions.CheckWithForTest(c, ctx, source, nil, req); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatal(err)
	}
	if _, _, err := decisions.CheckExplainWithForTest(c, ctx, source, nil, req); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatal(err)
	}
	if _, err := decisions.CheckBatchWithForTest(c, ctx, source, nil, []authmodel.CheckRequest{req}); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := decisions.CheckWithForTest(c, ctx, source, nil, req); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatal(err)
	}
	if _, err := decisions.CheckWithForTest(c, canceled, source, nil, req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, invalid := range []relationships.CheckReadSource{nil, (*operationReadSource)(nil), &operationReadSource{}} {
		if _, err := eng.Service.CheckWith(ctx, invalid, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("nil source check: %v", err)
		}
		if _, _, err := eng.Service.CheckExplainWith(ctx, invalid, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("nil source explain: %v", err)
		}
		if _, err := eng.Service.CheckBatchWith(ctx, invalid, []authmodel.CheckRequest{req}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("nil source batch: %v", err)
		}
	}
}

func TestRoleFilteringUsesDirectReadsAfterOperation(t *testing.T) {
	ctx := context.Background()
	c, _ := newRolesOnly(t, authmodel.EvaluationLimits{})
	req := request("u1", "audit", "project", "p1")
	operationRole := func(context.Context, string, string, string, string, string) (bool, error) { return true, nil }
	got, err := decisions.CheckBatchWithForTest(c, ctx, nil, operationRole, []authmodel.CheckRequest{req})
	if err != nil || !got[0].Allowed {
		t.Fatalf("operation role result: %+v/%v", got, err)
	}
	ids, err := c.FilterAuthorized(ctx, req.Principal, req.Permission, req.Resource.Type, []string{"p1", "p1"})
	if err != nil || len(ids) != 0 {
		t.Fatalf("filter reused operation role reader: %v/%v", ids, err)
	}
}
