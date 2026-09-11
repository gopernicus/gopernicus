package relationships

import (
	"context"
	"errors"
	"strings"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestEvaluationLimitsReserveLookahead(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name   string
		limits authmodel.EvaluationLimits
	}{
		{"MaxLookupResults", authmodel.EvaluationLimits{MaxLookupResults: maxInt}},
		{"MaxGraphStates", authmodel.EvaluationLimits{MaxGraphStates: maxInt}},
		{"MaxThroughDepth", authmodel.EvaluationLimits{MaxThroughDepth: maxInt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.limits.Resolve()
			if !errors.Is(err, authmodel.ErrInvalidLimits) || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("overflowing bound accepted: %v", err)
			}
			if _, err := newService(&fakeStore{}, testSchema(), serviceConfig{limits: tc.limits}); !errors.Is(err, authmodel.ErrInvalidLimits) {
				t.Fatalf("constructor accepted overflowing bound: %v", err)
			}
		})
	}
	limits, err := (authmodel.EvaluationLimits{MaxLookupResults: maxInt - 1, MaxGraphStates: maxInt - 1, MaxThroughDepth: maxInt - 1, MaxBatchSize: maxInt}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := newBudget(limits, nil).resultFetchCap(); got != maxInt {
		t.Fatalf("lookahead cap %d, want %d", got, maxInt)
	}
	store := &recordingStore{fakeStore: &fakeStore{}}
	svc := newServiceWith(t, store, testSchema(), limits)
	for _, page := range []bool{false, true} {
		if page {
			_, err = svc.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u"}, "delete", "post", "", maxInt-1)
		} else {
			_, err = svc.LookupResources(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u"}, "delete", "post")
		}
		if err != nil {
			t.Fatal(err)
		}
		if cap := store.calls[len(store.calls)-1].limit; cap != maxInt {
			t.Fatalf("store cap=%d, want %d", cap, maxInt)
		}
	}
	before := len(store.calls)
	_, err = svc.LookupResourcesPage(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u"}, "delete", "post", "", maxInt)
	if !errors.Is(err, sdk.ErrInvalidInput) || len(store.calls) != before {
		t.Fatalf("overflow page made read: %v calls=%d", err, len(store.calls))
	}
}

type cancelLookupStore struct {
	*fakeStore
	cancel   context.CancelFunc
	cancelAt string
	calls    []string
}

func (s *cancelLookupStore) ForModel(model ReadModel) Reader {
	return cancelLookupReader{Reader: s.fakeStore.ForModel(model), owner: s}
}

type cancelLookupReader struct {
	Reader
	owner *cancelLookupStore
}

func (r cancelLookupReader) cancel(method string) bool {
	r.owner.calls = append(r.owner.calls, method)
	if method == r.owner.cancelAt {
		r.owner.cancel()
		return true
	}
	return false
}
func (r cancelLookupReader) LookupResourceIDs(ctx context.Context, rt string, relations []string, st, sid, after string, limit int) ([]string, error) {
	if r.cancel("direct") {
		return nil, nil
	}
	return r.Reader.LookupResourceIDs(ctx, rt, relations, st, sid, after, limit)
}
func (r cancelLookupReader) LookupResourceIDsByRelationTarget(ctx context.Context, rt, relation, target string, ids []string, after string, limit int) ([]string, error) {
	if r.cancel("through") {
		return nil, nil
	}
	return r.Reader.LookupResourceIDsByRelationTarget(ctx, rt, relation, target, ids, after, limit)
}
func (r cancelLookupReader) LookupDescendantResourceIDs(ctx context.Context, rt string, relations []string, st string, ids []string, after string, limit int) ([]string, error) {
	if r.cancel("descendants") {
		return nil, nil
	}
	return r.Reader.LookupDescendantResourceIDs(ctx, rt, relations, st, ids, after, limit)
}

func TestLookupObservesCancellationAfterSuccessfulRead(t *testing.T) {
	for _, page := range []bool{false, true} {
		for _, kind := range []string{"direct", "through", "descendants"} {
			t.Run(kind+map[bool]string{false: "/all", true: "/page"}[page], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				store := &cancelLookupStore{fakeStore: &fakeStore{}, cancel: cancel, cancelAt: kind}
				schema, resource := testSchema(), "post"
				if kind == "direct" {
					def := schema.ResourceTypes["post"]
					def.Relations["second"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}
					def.Permissions["view"] = AnyOf(Direct("owner"), Direct("second"))
				}
				if kind == "through" {
					store.tuples = []CreateRelationship{{ResourceType: "org", ResourceID: "o", Relation: "admin", SubjectType: "user", SubjectID: "u"}}
				}
				if kind == "descendants" {
					schema, resource = hierarchySchema(), "space"
					store.tuples = []CreateRelationship{{ResourceType: "space", ResourceID: "s", Relation: "viewer", SubjectType: "user", SubjectID: "u"}}
				}
				svc := newServiceWith(t, store, schema, authmodel.EvaluationLimits{})
				var result authmodel.LookupResult
				var err error
				if page {
					result, err = svc.LookupResourcesPage(ctx, authmodel.PrincipalRef{Type: "user", ID: "u"}, "view", resource, "", 2)
				} else {
					result, err = svc.LookupResources(ctx, authmodel.PrincipalRef{Type: "user", ID: "u"}, "view", resource)
				}
				if !errors.Is(err, context.Canceled) || result.IDs != nil || result.HasMore {
					t.Fatalf("canceled empty read became success: %+v %v calls=%v", result, err, store.calls)
				}
				if kind == "direct" && len(store.calls) != 1 {
					t.Fatalf("read began after cancellation: %v", store.calls)
				}
			})
		}
	}
}

func TestLookupCanceledEmptyModelAndVerification(t *testing.T) {
	svc := newServiceWith(t, &fakeStore{}, testSchema(), authmodel.EvaluationLimits{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	principal := authmodel.PrincipalRef{Type: "user", ID: "u"}
	if _, err := svc.LookupResources(ctx, principal, "unknown", "post"); !errors.Is(err, context.Canceled) {
		t.Fatalf("all: %v", err)
	}
	if _, err := svc.LookupResourcesPage(ctx, principal, "unknown", "post", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("page: %v", err)
	}
	if _, err := svc.verifyLookup(ctx, principal, "unknown", "post", authmodel.LookupResult{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("final empty verification: %v", err)
	}
}
