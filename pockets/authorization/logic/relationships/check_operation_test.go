package relationships

import (
	"context"
	"errors"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

type checkSnapshotReader struct {
	Reader
	read func(context.Context, func(context.Context, Reader) error) error
}

func (r checkSnapshotReader) ReadLookupSnapshot(ctx context.Context, fn func(context.Context, Reader) error) error {
	return r.read(ctx, fn)
}

type explicitCheckSource struct{ CheckReader }

func (s explicitCheckSource) ForChecks(ReadModel) CheckReader { return s.CheckReader }

func TestCheckSnapshotCompletionFailureDiscardsResults(t *testing.T) {
	req := authmodel.CheckRequest{Principal: setPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: "a"}}
	operations := map[string]func(context.Context, *Service) (bool, error){
		"Check": func(ctx context.Context, svc *Service) (bool, error) {
			result, err := svc.Check(ctx, req)
			return result != (authmodel.CheckResult{}), err
		},
		"CheckExplain": func(ctx context.Context, svc *Service) (bool, error) {
			result, trace, err := svc.CheckExplain(ctx, req)
			return result != (authmodel.CheckResult{}) || trace.Decision != "" || len(trace.Steps) != 0, err
		},
		"CheckBatch": func(ctx context.Context, svc *Service) (bool, error) {
			results, err := svc.CheckBatch(ctx, []authmodel.CheckRequest{req, req})
			return results != nil, err
		},
		"FilterAuthorized": func(ctx context.Context, svc *Service) (bool, error) {
			ids, err := svc.FilterAuthorized(ctx, setPrincipal, "view", "space", []string{"a", "a"})
			return ids != nil, err
		},
	}
	for name, operation := range operations {
		for _, failure := range []string{"completion", "canceled", "nil reader", "typed nil reader"} {
			t.Run(name+"/"+failure, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				svc := newLimitedService(t, &fakeStore{tuples: []CreateRelationship{tuple("space", "a", "viewer", "user", "u1", "")}}, setHierarchySchema(), authmodel.EvaluationLimits{})
				reader := svc.reader
				completionErr := errors.New("snapshot completion failed")
				calls := 0
				svc.reader = checkSnapshotReader{Reader: reader, read: func(ctx context.Context, fn func(context.Context, Reader) error) error {
					calls++
					switch failure {
					case "nil reader":
						return fn(ctx, nil)
					case "typed nil reader":
						var absent *checkSnapshotReader
						return fn(ctx, absent)
					}
					if err := fn(ctx, reader); err != nil {
						return err
					}
					if failure == "canceled" {
						cancel()
						return nil
					}
					return completionErr
				}}
				want := completionErr
				switch failure {
				case "canceled":
					want = context.Canceled
				case "nil reader", "typed nil reader":
					want = sdk.ErrInvalidInput
				}
				provisional, err := operation(ctx, svc)
				if provisional || !errors.Is(err, want) || calls != 1 {
					t.Fatalf("provisional=%v err=%v snapshots=%d; want zero result/%v/1", provisional, err, calls, want)
				}
			})
		}
	}
}

func TestCheckSnapshotValidationAndReadFreePaths(t *testing.T) {
	svc := newLimitedService(t, &fakeStore{}, setHierarchySchema(), authmodel.EvaluationLimits{MaxBatchSize: 2})
	svc.reader = checkSnapshotReader{Reader: svc.reader, read: func(context.Context, func(context.Context, Reader) error) error {
		t.Fatal("opened a snapshot before validation or for a read-free operation")
		return nil
	}}
	valid := authmodel.CheckRequest{Principal: setPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: "a"}}
	unknown := valid
	unknown.Permission = "missing"
	if _, err := svc.Check(t.Context(), authmodel.CheckRequest{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, _, err := svc.CheckExplain(t.Context(), authmodel.CheckRequest{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := svc.CheckBatch(t.Context(), []authmodel.CheckRequest{valid, {}}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := svc.CheckBatch(t.Context(), []authmodel.CheckRequest{valid, valid, valid}); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatal(err)
	}
	if got, err := svc.CheckBatch(t.Context(), nil); got != nil || err != nil {
		t.Fatalf("empty batch: %v/%v", got, err)
	}
	if got, err := svc.Check(t.Context(), unknown); got.Allowed || err != nil {
		t.Fatalf("unknown check: %v/%v", got, err)
	}
	if got, _, err := svc.CheckExplain(t.Context(), unknown); got.Allowed || err != nil {
		t.Fatalf("unknown explain: %v/%v", got, err)
	}
	if got, err := svc.CheckBatch(t.Context(), []authmodel.CheckRequest{unknown, unknown}); len(got) != 2 || err != nil {
		t.Fatalf("unknown batch: %v/%v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.Check(ctx, valid); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCheckSnapshotsDoNotNestOrReplaceExplicitReads(t *testing.T) {
	svc := newLimitedService(t, &fakeStore{tuples: []CreateRelationship{tuple("space", "a", "viewer", "user", "u1", "")}}, setHierarchySchema(), authmodel.EvaluationLimits{})
	calls := 0
	var reader checkSnapshotReader
	reader = checkSnapshotReader{Reader: svc.reader, read: func(ctx context.Context, fn func(context.Context, Reader) error) error {
		calls++
		return fn(ctx, reader)
	}}
	svc.reader = reader
	if got, err := svc.LookupResources(t.Context(), setPrincipal, "view", "space"); err != nil || len(got.IDs) != 1 || calls != 1 {
		t.Fatalf("lookup result=%v err=%v snapshots=%d; want one snapshot including verification", got, err, calls)
	}
	reader.read = func(context.Context, func(context.Context, Reader) error) error {
		t.Fatal("explicit operation reader started another snapshot")
		return nil
	}
	source := explicitCheckSource{CheckReader: reader}
	req := authmodel.CheckRequest{Principal: setPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "space", ID: "a"}}
	if got, err := svc.CheckWith(t.Context(), source, req); err != nil || !got.Allowed {
		t.Fatalf("CheckWith: %v/%v", got, err)
	}
	if got, _, err := svc.CheckExplainWith(t.Context(), source, req); err != nil || !got.Allowed {
		t.Fatalf("CheckExplainWith: %v/%v", got, err)
	}
	if got, err := svc.CheckBatchWith(t.Context(), source, []authmodel.CheckRequest{req}); err != nil || len(got) != 1 || !got[0].Allowed {
		t.Fatalf("CheckBatchWith: %v/%v", got, err)
	}
}
