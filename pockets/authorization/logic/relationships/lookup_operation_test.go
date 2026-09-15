package relationships

import (
	"context"
	"errors"
	"fmt"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

type retryLookupReader struct {
	Reader
	discover func(context.Context) ([]string, error)
}

func (r retryLookupReader) LookupResourceIDs(ctx context.Context, _ string, _ []string, _, _, _ string, _ int) ([]string, error) {
	return r.discover(ctx)
}

func TestLookupRetryBoundaries(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		for _, page := range []bool{false, true} {
			for _, outcome := range []string{"exhausted", "third attempt succeeds", "store conflict", "budget", "canceled", "deadline"} {
				t.Run(fmt.Sprintf("snapshot=%v/page=%v/%s", snapshot, page, outcome), func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					// The reader discovers candidates but the empty store denies them. Each
					// mismatch must restart discovery, including a page's withheld lookahead.
					schema := setHierarchySchema()
					def := schema.ResourceTypes["space"]
					def.Permissions["view"] = AnyOf(Direct("viewer"))
					schema.ResourceTypes["space"] = def
					svc := newLimitedService(t, &fakeStore{}, schema, authmodel.EvaluationLimits{})
					calls := 0
					svc.reader = retryLookupReader{Reader: svc.reader, discover: func(context.Context) ([]string, error) {
						calls++
						switch outcome {
						case "third attempt succeeds":
							if calls == 3 {
								return []string{}, nil
							}
						case "store conflict":
							return nil, sdk.ErrConflict
						case "budget":
							return nil, authmodel.ErrEvaluationLimit
						case "canceled":
							cancel()
						case "deadline":
							return nil, context.DeadlineExceeded
						}
						return []string{"a", "b"}, nil
					}}
					source := &failingLookupSnapshot{Reader: svc.reader}
					if snapshot {
						svc.reader = source
					}
					var got authmodel.LookupResult
					var err error
					if page {
						got, err = svc.LookupResourcesPage(ctx, setPrincipal, "view", "space", "", 1)
					} else {
						got, err = svc.LookupResources(ctx, setPrincipal, "view", "space")
					}
					wantCalls := 1
					var wantErr error
					switch outcome {
					case "exhausted":
						wantCalls = 3
						wantErr = authmodel.ErrEnumerationContended
					case "third attempt succeeds":
						wantCalls = 3
					case "store conflict":
						wantErr = sdk.ErrConflict
					case "budget":
						wantErr = authmodel.ErrEvaluationLimit
					case "canceled":
						wantErr = context.Canceled
					case "deadline":
						wantErr = context.DeadlineExceeded
					}
					if calls != wantCalls || !errors.Is(err, wantErr) || len(got.IDs) != 0 || got.HasMore {
						t.Fatalf("calls=%d result=%v err=%v; want %d/%v", calls, got, err, wantCalls, wantErr)
					}
					if snapshot && source.calls != wantCalls {
						t.Fatalf("snapshot attempts=%d; want %d", source.calls, wantCalls)
					}
					if err == nil && got.IDs == nil {
						t.Fatal("successful empty result must be nonnil")
					}
					if err != nil && got.IDs != nil {
						t.Fatal("failed result must be zero")
					}
					if outcome == "exhausted" && (!errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, sdk.ErrConflict)) {
						t.Fatalf("wrong exhausted error classification: %v", err)
					}
				})
			}
		}
	}
}

type failingLookupSnapshot struct {
	Reader
	calls     int
	fail      error
	nilReader bool
}

func (r *failingLookupSnapshot) ReadLookupSnapshot(ctx context.Context, fn func(context.Context, Reader) error) error {
	r.calls++
	reader := r.Reader
	if r.nilReader {
		reader = nil
	}
	if err := fn(ctx, reader); err != nil {
		return err
	}
	return r.fail
}
func TestLookupSnapshotFailureDiscardsResults(t *testing.T) {
	for _, nilReader := range []bool{false, true} {
		t.Run(fmt.Sprintf("nil=%v", nilReader), func(t *testing.T) {
			svc := newLimitedService(t, &fakeStore{tuples: []CreateRelationship{tuple("space", "a", "viewer", "user", "u1", "")}}, setHierarchySchema(), authmodel.EvaluationLimits{})
			failure := errors.New("commit failed")
			source := &failingLookupSnapshot{Reader: svc.reader, fail: failure, nilReader: nilReader}
			svc.reader = source
			got, err := svc.LookupResources(t.Context(), setPrincipal, "view", "space")
			want := failure
			if nilReader {
				want = sdk.ErrInvalidInput
			}
			if !errors.Is(err, want) || source.calls != 1 || got.IDs != nil || got.HasMore {
				t.Fatalf("got=%v err=%v calls=%d", got, err, source.calls)
			}
		})
	}
}
