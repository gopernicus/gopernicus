package authorizersvc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// graphStore is an in-package relationship.Storer with REAL exact-userset
// expansion (bounded, overflow-signalling) so the EvaluateWith parity test can
// cover usersets and the expansion budget without importing memstore (which
// would cycle through the root package). It reuses fakeStore for everything the
// walk does not read.
type graphStore struct {
	fakeStore
}

type graphNode struct{ t, id, rel string }

// expand returns the exact-userset reachable set of the concrete subject and
// whether it overflowed maxExpansionStates (<= 0 = unbounded). The seed counts
// as a state, matching the adapters' accounting.
func (g *graphStore) expand(subjectType, subjectID string, maxExpansionStates int) (map[graphNode]bool, bool) {
	seen := map[graphNode]bool{{subjectType, subjectID, ""}: true}
	for changed := true; changed; {
		changed = false
		for _, t := range g.tuples {
			if !seen[graphNode{t.SubjectType, t.SubjectID, t.SubjectRelation}] {
				continue
			}
			n := graphNode{t.ResourceType, t.ResourceID, t.Relation}
			if seen[n] {
				continue
			}
			if maxExpansionStates > 0 && len(seen) >= maxExpansionStates {
				return seen, true
			}
			seen[n] = true
			changed = true
		}
	}
	return seen, false
}

func (g *graphStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	seen, overflow := g.expand(subjectType, subjectID, maxExpansionStates)
	if overflow {
		return false, relationship.ErrExpansionBudgetExceeded
	}
	for _, t := range g.tuples {
		if t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation &&
			seen[graphNode{t.SubjectType, t.SubjectID, t.SubjectRelation}] {
			return true, nil
		}
	}
	return false, nil
}

// recordingReader wraps a PermissionReader and logs every call the walk makes,
// so the test can assert EvaluateWith drives the reader with exactly the calls
// Check makes against the store.
type recordingReader struct {
	inner PermissionReader
	calls []string
}

func (r *recordingReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, rid, rel, st, sid string, max int) (bool, error) {
	r.calls = append(r.calls, fmt.Sprintf("check %s:%s#%s@%s:%s max=%d", rt, rid, rel, st, sid, max))
	return r.inner.CheckRelationWithGroupExpansion(ctx, rt, rid, rel, st, sid, max)
}

func (r *recordingReader) GetRelationTargets(ctx context.Context, rt, rid, rel string) ([]relationship.RelationTarget, error) {
	r.calls = append(r.calls, fmt.Sprintf("targets %s:%s#%s", rt, rid, rel))
	return r.inner.GetRelationTargets(ctx, rt, rid, rel)
}

// evaluateWithSchema: a self-referential hierarchy (Through to the same type)
// plus a userset-bearing relation, so one schema covers direct, userset,
// Through, diamond, cycle, depth, graph-state and expansion cases.
func evaluateWithSchema() Schema {
	return NewSchema([]ResourceSchema{
		{
			Name: "group",
			Def: ResourceTypeDef{
				Relations: map[string]RelationDef{
					"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				},
			},
		},
		{
			Name: "space",
			Def: ResourceTypeDef{
				Relations: map[string]RelationDef{
					"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
					"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				},
				Permissions: map[string]PermissionRule{
					"view": AnyOf(Direct("viewer"), Through("parent", "view")),
				},
			},
		},
	})
}

func tuple(rt, rid, rel, st, sid, srel string) relationship.CreateRelationship {
	return relationship.CreateRelationship{ResourceType: rt, ResourceID: rid, Relation: rel, SubjectType: st, SubjectID: sid, SubjectRelation: srel}
}

func TestEvaluateWithMatchesCheckOverStore(t *testing.T) {
	graph := &graphStore{}
	graph.tuples = []relationship.CreateRelationship{
		// direct
		tuple("space", "direct", "viewer", "user", "alice", ""),
		// userset: alice -> g1#member -> g2#member; space:grouped viewer = g2#member
		tuple("group", "g1", "member", "user", "alice", ""),
		tuple("group", "g2", "member", "group", "g1", "member"),
		tuple("space", "grouped", "viewer", "group", "g2", "member"),
		// through: leaf -> mid -> root (viewer on root only)
		tuple("space", "leaf", "parent", "space", "mid", ""),
		tuple("space", "mid", "parent", "space", "root", ""),
		tuple("space", "root", "viewer", "user", "bob", ""),
		// diamond: d -> a, d -> b, a -> top, b -> top (viewer on top)
		tuple("space", "d", "parent", "space", "a", ""),
		tuple("space", "d", "parent", "space", "b", ""),
		tuple("space", "a", "parent", "space", "top", ""),
		tuple("space", "b", "parent", "space", "top", ""),
		tuple("space", "top", "viewer", "user", "carol", ""),
		// cycle: c1 <-> c2, nobody a viewer
		tuple("space", "c1", "parent", "space", "c2", ""),
		tuple("space", "c2", "parent", "space", "c1", ""),
	}

	cases := []struct {
		name          string
		limits        EvaluationLimits
		req           CheckRequest
		wantAllowed   bool
		wantErr       error
		wantReasonSub string
	}{
		{name: "direct", req: check("alice", "space", "direct"), wantAllowed: true},
		{name: "direct deny", req: check("bob", "space", "direct")},
		{name: "userset", req: check("alice", "space", "grouped"), wantAllowed: true},
		{name: "through two hops", req: check("bob", "space", "leaf"), wantAllowed: true, wantReasonSub: "through:parent->through:parent->direct:viewer"},
		{name: "through deny", req: check("alice", "space", "leaf")},
		{name: "diamond", req: check("carol", "space", "d"), wantAllowed: true},
		{name: "cycle denies without error", req: check("alice", "space", "c1")},
		{name: "depth budget", limits: EvaluationLimits{MaxThroughDepth: 1}, req: check("bob", "space", "leaf"), wantErr: ErrEvaluationLimit},
		{name: "graph-state budget", limits: EvaluationLimits{MaxGraphStates: 2}, req: check("carol", "space", "d"), wantErr: ErrEvaluationLimit},
		// alice's reachable set is {alice, g1#member, g2#member} = 3 states; a
		// bound of 2 overflows in the store and surfaces as ErrEvaluationLimit.
		{name: "expansion budget", limits: EvaluationLimits{MaxGraphStates: 2}, req: check("alice", "space", "grouped"), wantErr: ErrEvaluationLimit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			overStore, err := NewService(graph, evaluateWithSchema(), Config{Limits: tc.limits})
			if err != nil {
				t.Fatalf("NewService(graph): %v", err)
			}
			// The service under EvaluateWith is built over an EMPTY store: if the
			// walk ever read s.store instead of the reader, every allow below would
			// become a deny.
			overEmpty, err := NewService(&fakeStore{}, evaluateWithSchema(), Config{Limits: tc.limits})
			if err != nil {
				t.Fatalf("NewService(empty): %v", err)
			}

			storeRec := &recordingReader{inner: graph}
			want, wantErr := overStore.check(context.Background(), tc.req, newBudget(overStore.limits, storeRec))
			readerRec := &recordingReader{inner: graph}
			got, gotErr := overEmpty.EvaluateWith(context.Background(), readerRec, tc.req)

			if tc.wantErr != nil {
				if !errors.Is(wantErr, tc.wantErr) || !errors.Is(gotErr, tc.wantErr) {
					t.Fatalf("want %v from both: check=%v evaluate=%v", tc.wantErr, wantErr, gotErr)
				}
			} else {
				if wantErr != nil || gotErr != nil {
					t.Fatalf("unexpected error: check=%v evaluate=%v", wantErr, gotErr)
				}
				if want.Allowed != tc.wantAllowed || got.Allowed != tc.wantAllowed {
					t.Fatalf("allowed: want %v, check=%v evaluate=%v", tc.wantAllowed, want.Allowed, got.Allowed)
				}
				if want != got {
					t.Fatalf("result parity: check=%+v evaluate=%+v", want, got)
				}
				if tc.wantReasonSub != "" && got.Reason != tc.wantReasonSub {
					t.Fatalf("reason: want %q got %q", tc.wantReasonSub, got.Reason)
				}
			}
			if fmt.Sprint(storeRec.calls) != fmt.Sprint(readerRec.calls) {
				t.Fatalf("call parity:\n check:    %v\n evaluate: %v", storeRec.calls, readerRec.calls)
			}
			if len(readerRec.calls) == 0 {
				t.Fatalf("EvaluateWith made no reader calls")
			}
		})
	}
}

func TestEvaluateWithReadsOnlyTheReader(t *testing.T) {
	graph := &graphStore{}
	graph.tuples = []relationship.CreateRelationship{tuple("space", "s", "viewer", "user", "alice", "")}
	svc, err := NewService(&fakeStore{}, evaluateWithSchema(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	// Check over the service's own (empty) store denies…
	res, err := svc.Check(context.Background(), check("alice", "space", "s"))
	if err != nil || res.Allowed {
		t.Fatalf("Check over empty store: allowed=%v err=%v", res.Allowed, err)
	}
	// …while EvaluateWith over the populated reader allows.
	res, err = svc.EvaluateWith(context.Background(), graph, check("alice", "space", "s"))
	if err != nil || !res.Allowed {
		t.Fatalf("EvaluateWith over reader: allowed=%v err=%v", res.Allowed, err)
	}
}

func TestEvaluateWithRejectsNilReaderAndInvalidRequest(t *testing.T) {
	svc, err := NewService(&fakeStore{}, evaluateWithSchema(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EvaluateWith(context.Background(), nil, check("alice", "space", "s")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil reader: want sdk.ErrInvalidInput, got %v", err)
	}
	if _, err := svc.EvaluateWith(context.Background(), &graphStore{}, CheckRequest{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty request: want sdk.ErrInvalidInput, got %v", err)
	}
}

func check(user, rt, rid string) CheckRequest {
	return CheckRequest{Principal: PrincipalRef{Type: "user", ID: user}, Permission: "view", Resource: Resource{Type: rt, ID: rid}}
}
