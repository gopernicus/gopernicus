package authorizersvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// TestEvaluationLimitsResolveDefaults proves a zero EvaluationLimits resolves
// every field to its safe nonzero default — zero never means unlimited.
func TestEvaluationLimitsResolveDefaults(t *testing.T) {
	got, err := EvaluationLimits{}.Resolve()
	if err != nil {
		t.Fatalf("Resolve zero: %v", err)
	}
	want := EvaluationLimits{
		MaxThroughDepth:    DefaultMaxThroughDepth,
		MaxGraphStates:     DefaultMaxGraphStates,
		MaxRelationTargets: DefaultMaxRelationTargets,
		MaxBatchSize:       DefaultMaxBatchSize,
		MaxLookupResults:   DefaultMaxLookupResults,
	}
	if got != want {
		t.Fatalf("Resolve zero = %+v, want %+v", got, want)
	}
}

// TestEvaluationLimitsResolveExplicitPreserved proves a positive field is kept
// as-is and a zero field beside it still takes its default (partial config).
func TestEvaluationLimitsResolveExplicitPreserved(t *testing.T) {
	got, err := EvaluationLimits{MaxThroughDepth: 3, MaxBatchSize: 25}.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.MaxThroughDepth != 3 {
		t.Fatalf("MaxThroughDepth = %d, want 3", got.MaxThroughDepth)
	}
	if got.MaxBatchSize != 25 {
		t.Fatalf("MaxBatchSize = %d, want 25", got.MaxBatchSize)
	}
	if got.MaxGraphStates != DefaultMaxGraphStates {
		t.Fatalf("zero MaxGraphStates should default, got %d", got.MaxGraphStates)
	}
}

// TestEvaluationLimitsResolveNegativeRejected proves EVERY field rejects a
// negative value with ErrInvalidLimits (wrapping sdk.ErrInvalidInput), one
// field at a time — no negative is silently clamped or treated as unlimited.
func TestEvaluationLimitsResolveNegativeRejected(t *testing.T) {
	cases := map[string]EvaluationLimits{
		"MaxThroughDepth":    {MaxThroughDepth: -1},
		"MaxGraphStates":     {MaxGraphStates: -1},
		"MaxRelationTargets": {MaxRelationTargets: -1},
		"MaxBatchSize":       {MaxBatchSize: -1},
		"MaxLookupResults":   {MaxLookupResults: -1},
	}
	for name, l := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := l.Resolve()
			if !errors.Is(err, ErrInvalidLimits) {
				t.Fatalf("Resolve(%s=-1): want ErrInvalidLimits, got %v", name, err)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("ErrInvalidLimits must wrap sdk.ErrInvalidInput, got %v", err)
			}
		})
	}
}

// TestNewServiceRejectsNegativeLimit proves the engine construction validates
// the limits and refuses a negative field with ErrInvalidLimits.
func TestNewServiceRejectsNegativeLimit(t *testing.T) {
	_, err := NewService(&fakeStore{}, testSchema(), Config{Limits: EvaluationLimits{MaxThroughDepth: -1}})
	if !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("want ErrInvalidLimits, got %v", err)
	}
}

// throughFolderSchema is a self-referential Through hierarchy: folder#view is
// granted directly to its owner or Through a parent folder's view.
func throughFolderSchema() Schema {
	return NewSchema([]ResourceSchema{{
		Name: "folder",
		Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "folder"}}},
			},
			Permissions: map[string]PermissionRule{
				"view": AnyOf(Direct("owner"), Through("parent", "view")),
			},
		},
	}})
}

// TestBudgetThroughDepthExhaustionIsError proves that exhausting the Through
// depth budget is an INDETERMINATE ErrEvaluationLimit (fail closed), not a
// silent deny and not a partial result. A deep-enough budget reaches the grant.
func TestBudgetThroughDepthExhaustionIsError(t *testing.T) {
	store := &fakeStore{tuples: []relationship.CreateRelationship{
		{ResourceType: "folder", ResourceID: "f1", Relation: "parent", SubjectType: "folder", SubjectID: "f2"},
		{ResourceType: "folder", ResourceID: "f2", Relation: "parent", SubjectType: "folder", SubjectID: "f3"},
		{ResourceType: "folder", ResourceID: "f3", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	}}

	// Budget of 1 Through hop cannot reach the owner three levels down.
	shallow, err := NewService(store, throughFolderSchema(), Config{Limits: EvaluationLimits{MaxThroughDepth: 1}})
	if err != nil {
		t.Fatalf("NewService shallow: %v", err)
	}
	_, err = shallow.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "folder", ID: "f1"},
	})
	if !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("depth exhaustion: want ErrEvaluationLimit, got %v", err)
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("ErrEvaluationLimit must wrap sdk.ErrUnavailable, got %v", err)
	}

	// A deep-enough budget reaches the grant — proving the error was the budget,
	// not a broken schema.
	deep, err := NewService(store, throughFolderSchema(), Config{Limits: EvaluationLimits{MaxThroughDepth: 5}})
	if err != nil {
		t.Fatalf("NewService deep: %v", err)
	}
	res, err := deep.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "folder", ID: "f1"},
	})
	if err != nil {
		t.Fatalf("deep Check: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("deep budget should reach the owner grant, got %+v", res)
	}
}

// TestBudgetDefaultDepthReachesGrant proves the default budget (zero Limits)
// resolves and traverses a modest hierarchy without exhaustion.
func TestBudgetDefaultDepthReachesGrant(t *testing.T) {
	store := &fakeStore{tuples: []relationship.CreateRelationship{
		{ResourceType: "folder", ResourceID: "f1", Relation: "parent", SubjectType: "folder", SubjectID: "f2"},
		{ResourceType: "folder", ResourceID: "f2", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	}}
	svc, err := NewService(store, throughFolderSchema(), Config{IDs: cryptids.IDGenerator{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	res, err := svc.Check(context.Background(), CheckRequest{
		Principal: PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: Resource{Type: "folder", ID: "f1"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("default budget should reach the grant, got %+v", res)
	}
}
