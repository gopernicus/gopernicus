package decisions_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/internal/decisionmode"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

// invalidSpaceID is an off-contract stored reference: one byte over the limit.
var invalidSpaceID = strings.Repeat("x", tuples.MaxRefFieldLen+1)

// readCounts records the reads one service performed through its snapshots.
type readCounts struct {
	exact      int
	expansions int
	expanded   [][]string
}

// observedTuples wraps the memory authority so every snapshot exposes counted
// exact reads and counted subject-first expansions. extraTargets injects stored
// Through targets the memory store itself would refuse to hold.
type observedTuples struct {
	*memory.Tuples
	counts       *readCounts
	extraTargets map[string][]relationships.RelationTarget
}

func (o observedTuples) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	return o.Tuples.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
		return fn(ctx, observedReader{Reader: r, owner: o})
	})
}

type observedReader struct {
	tuples.Reader
	owner observedTuples
}

func (r observedReader) Contains(ctx context.Context, t tuples.Tuple) (bool, error) {
	r.owner.counts.exact++
	return r.Reader.Contains(ctx, t)
}

func (r observedReader) ContainsMany(ctx context.Context, ts []tuples.Tuple) ([]bool, error) {
	r.owner.counts.exact++
	return r.Reader.ContainsMany(ctx, ts)
}

func (r observedReader) ForModel(m relationships.ReadModel) relationships.Reader {
	inner := r.Reader.(interface {
		ForModel(relationships.ReadModel) relationships.Reader
	}).ForModel(m)
	return observedGraph{Reader: inner, owner: r.owner}
}

type observedGraph struct {
	relationships.Reader
	owner observedTuples
}

func (g observedGraph) CheckRelationWithGroupExpansion(ctx context.Context, rt, id, rel, st, sid string, max int) (bool, error) {
	g.owner.counts.expansions++
	g.owner.counts.expanded = append(g.owner.counts.expanded, []string{id})
	return g.Reader.CheckRelationWithGroupExpansion(ctx, rt, id, rel, st, sid, max)
}

func (g observedGraph) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) (map[string]bool, error) {
	g.owner.counts.expansions++
	g.owner.counts.expanded = append(g.owner.counts.expanded, slices.Clone(ids))
	return g.Reader.CheckBatchDirect(ctx, rt, ids, rel, st, sid, max)
}

func (g observedGraph) FilterRelation(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) ([]string, error) {
	g.owner.counts.expansions++
	g.owner.counts.expanded = append(g.owner.counts.expanded, slices.Clone(ids))
	return g.Reader.(relationships.RelationSetReader).FilterRelation(ctx, rt, ids, rel, st, sid, max)
}

func (g observedGraph) GetRelationTargets(ctx context.Context, rt, id, rel string) ([]relationships.RelationTarget, error) {
	targets, err := g.Reader.GetRelationTargets(ctx, rt, id, rel)
	return append(targets, g.owner.extraTargets[rt+":"+id+"#"+rel]...), err
}

func (g observedGraph) RelationTargetsFor(ctx context.Context, rt string, ids []string, rel string) (map[string][]relationships.RelationTarget, error) {
	out, err := g.Reader.(relationships.RelationSetReader).RelationTargetsFor(ctx, rt, ids, rel)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = append(out[id], g.owner.extraTargets[rt+":"+id+"#"+rel]...)
	}
	return out, nil
}

// shortCircuitModel has a relation that admits usersets (space.viewer) and one
// that admits only concrete users (space.owner), plus a Through onto both.
func shortCircuitModel() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "group", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{
			"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
		}}},
		{Name: "space", Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"owner":  {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]decisions.Expression{
				"view": decisions.AnyOf(decisions.Direct("viewer")),
				"own":  decisions.AnyOf(decisions.Direct("owner")),
			},
		}},
		{Name: "doc", Def: decisions.ResourceTypeDef{
			Relations:   map[string]decisions.RelationDef{"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "space"}}}},
			Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Through("parent", "view"))},
		}},
	})
}

// newShortCircuitService seeds alice with direct grants on target and s1..s5,
// bob with a grant on shared only through group g, and doc d under target.
func newShortCircuitService(t *testing.T, limits authmodel.EvaluationLimits) (*decisions.Service, *readCounts, observedTuples) {
	t.Helper()
	store := memory.NewTuples()
	fact := func(rt, id, rel, st, sid, srel string) tuples.Tuple {
		return tuples.Tuple{Scope: tuples.On(rt, id), Relation: rel, Subject: tuples.SubjectRef{Type: st, ID: sid, Relation: srel}}
	}
	facts := []tuples.Tuple{
		fact("space", "target", "viewer", "user", "alice", ""),
		fact("space", "target", "owner", "user", "alice", ""),
		fact("group", "g", "member", "user", "bob", ""),
		fact("space", "shared", "viewer", "group", "g", "member"),
		fact("doc", "d", "parent", "space", "target", ""),
		fact("doc", "bad", "parent", "space", "shared", ""),
	}
	for _, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		facts = append(facts, fact("space", id, "viewer", "user", "alice", ""))
	}
	if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
	counts := &readCounts{}
	observed := observedTuples{Tuples: store, counts: counts, extraTargets: map[string][]relationships.RelationTarget{
		"doc:bad#parent": {{Type: "space", ID: invalidSpaceID}},
	}}
	svc, err := decisions.NewService(observed, decisions.WithModel(shortCircuitModel()), decisions.WithLimits(limits))
	if err != nil {
		t.Fatal(err)
	}
	return svc, counts, observed
}

func spaceCheck(principal, permission, id string) authmodel.CheckRequest {
	return authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: principal}, Permission: permission, Resource: authmodel.Resource{Type: "space", ID: id}}
}

func TestDirectShortCircuitSingleChecks(t *testing.T) {
	for _, tc := range []struct {
		name       string
		req        authmodel.CheckRequest
		allowed    bool
		expansions int
	}{
		{"hit grants without expansion", spaceCheck("alice", "view", "target"), true, 0},
		{"miss on a relation without usersets denies without expansion", spaceCheck("alice", "own", "shared"), false, 0},
		{"miss on a userset relation falls through to a grant", spaceCheck("bob", "view", "shared"), true, 1},
		{"miss on a userset relation falls through to a deny", spaceCheck("carol", "view", "target"), false, 1},
		{"hit behind a Through grants without expansion", authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "alice"}, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "d"}}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, counts, _ := newShortCircuitService(t, authmodel.EvaluationLimits{})
			got, err := svc.Check(t.Context(), tc.req)
			if err != nil || got.Allowed != tc.allowed {
				t.Fatalf("check: %+v/%v, want allowed=%v", got, err, tc.allowed)
			}
			if counts.expansions != tc.expansions || counts.exact != 1 {
				t.Fatalf("reads: %d expansions/%d exact, want %d/1", counts.expansions, counts.exact, tc.expansions)
			}
			before := *counts
			pure, err := svc.Check(decisionmode.WithSubjectFirst(t.Context()), tc.req)
			if err != nil || pure != got {
				t.Fatalf("subject-first: %+v/%v, short circuit: %+v", pure, err, got)
			}
			if counts.exact != before.exact || counts.expansions != before.expansions+1 {
				t.Fatalf("subject-first must read only by expansion: %+v -> %+v", before, *counts)
			}
		})
	}
}

func decisionCode(allowed bool) authmodel.Reason {
	if allowed {
		return authmodel.ReasonGranted
	}
	return authmodel.ReasonDenied
}

func TestDirectShortCircuitExplainTrace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		req     authmodel.CheckRequest
		outcome authmodel.Reason
	}{
		{"hit", spaceCheck("alice", "view", "target"), authmodel.ReasonDirectHit},
		{"miss without usersets", spaceCheck("alice", "own", "shared"), authmodel.ReasonDenied},
		{"fall through", spaceCheck("bob", "view", "shared"), authmodel.ReasonGranted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, counts, _ := newShortCircuitService(t, authmodel.EvaluationLimits{})
			result, explanation, err := svc.CheckExplain(t.Context(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("result=%+v explanation=%+v", result, explanation)
			if explanation.Decision != result.ReasonCode || (result.ReasonCode != authmodel.ReasonGranted && result.ReasonCode != authmodel.ReasonDenied) {
				t.Fatalf("decision %q / code %q must stay granted or denied", explanation.Decision, result.ReasonCode)
			}
			if result.Allowed && result.Reason != "direct:"+relationFor(tc.req.Permission) {
				t.Fatalf("Reason %q", result.Reason)
			}
			if len(explanation.Steps) != 1 {
				t.Fatalf("steps: %+v", explanation.Steps)
			}
			step := explanation.Steps[0]
			want := authmodel.ExplainStep{ResourceType: "space", ResourceID: tc.req.Resource.ID, Permission: tc.req.Permission, Relation: relationFor(tc.req.Permission), Kind: authmodel.ExplainKindDirect, Outcome: tc.outcome, Scope: tuples.On("space", tc.req.Resource.ID)}
			if step != want {
				t.Fatalf("step %+v, want %+v", step, want)
			}
			if tc.outcome != authmodel.ReasonGranted && counts.expansions != 0 {
				t.Fatalf("short circuit expanded: %+v", counts)
			}
		})
	}
}

func relationFor(permission string) string {
	if permission == "own" {
		return "owner"
	}
	return "viewer"
}

func TestDirectShortCircuitChargesNoExtraStep(t *testing.T) {
	// view = AnyOf(Direct(viewer)) charges three steps: permission, AnyOf, Direct.
	for _, steps := range []int{3, 2} {
		for _, req := range []authmodel.CheckRequest{spaceCheck("alice", "view", "target"), spaceCheck("bob", "view", "shared"), spaceCheck("alice", "own", "shared")} {
			svc, _, _ := newShortCircuitService(t, authmodel.EvaluationLimits{MaxEvaluationSteps: steps})
			got, err := svc.Check(t.Context(), req)
			pure, pureErr := svc.Check(decisionmode.WithSubjectFirst(t.Context()), req)
			if steps == 3 && (err != nil || pureErr != nil || got.Allowed != pure.Allowed) {
				t.Fatalf("steps=3 %+v: %+v/%v vs subject-first %+v/%v", req, got, err, pure, pureErr)
			}
			if steps == 2 && (!errors.Is(err, authmodel.ErrEvaluationLimit) || !errors.Is(pureErr, authmodel.ErrEvaluationLimit)) {
				t.Fatalf("steps=2 %+v: %v / subject-first %v", req, err, pureErr)
			}
		}
	}
}

// TestDirectShortCircuitGraphStateEdge pins the budget-edge answer change.
// Alice reaches eight expansion states, above MaxGraphStates=3.
func TestDirectShortCircuitGraphStateEdge(t *testing.T) {
	svc, _, _ := newShortCircuitService(t, authmodel.EvaluationLimits{MaxGraphStates: 3})
	pureCtx := decisionmode.WithSubjectFirst(t.Context())
	for _, tc := range []struct {
		name string
		req  authmodel.CheckRequest
		// want is the Phase 1 answer; nil means ErrEvaluationLimit.
		want *bool
	}{
		{"direct hit now grants", spaceCheck("alice", "view", "target"), ptr(true)},
		{"miss without usersets now denies", spaceCheck("alice", "own", "shared"), ptr(false)},
		{"miss on a userset relation still errors", spaceCheck("alice", "view", "shared"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Check(pureCtx, tc.req); !errors.Is(err, authmodel.ErrEvaluationLimit) {
				t.Fatalf("subject-first must overflow: %v", err)
			}
			got, err := svc.Check(t.Context(), tc.req)
			if tc.want == nil {
				if !errors.Is(err, authmodel.ErrEvaluationLimit) || !errors.Is(err, sdk.ErrUnavailable) || got.Allowed {
					t.Fatalf("overflow must stay an error, never a deny: %+v/%v", got, err)
				}
				return
			}
			if err != nil || got.Allowed != *tc.want {
				t.Fatalf("got %+v/%v, want allowed=%v", got, err, *tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestDirectShortCircuitBatchPaths(t *testing.T) {
	alice := authmodel.PrincipalRef{Type: "user", ID: "alice"}
	docCheck := func(id string) authmodel.CheckRequest {
		return authmodel.CheckRequest{Principal: alice, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: id}}
	}
	for _, tc := range []struct {
		name       string
		reqs       []authmodel.CheckRequest
		want       []bool
		expansions int
		expanded   [][]string
	}{
		{"optimized all hits", []authmodel.CheckRequest{spaceCheck("alice", "view", "target"), spaceCheck("alice", "view", "s1")}, []bool{true, true}, 0, nil},
		{"optimized expands only misses", []authmodel.CheckRequest{spaceCheck("alice", "view", "target"), spaceCheck("alice", "view", "shared"), spaceCheck("alice", "view", "s2")}, []bool{true, false, true}, 1, [][]string{{"shared"}}},
		{"optimized misses without usersets deny", []authmodel.CheckRequest{spaceCheck("alice", "own", "target"), spaceCheck("alice", "own", "shared")}, []bool{true, false}, 0, nil},
		{"traversal", []authmodel.CheckRequest{spaceCheck("alice", "view", "target"), spaceCheck("alice", "own", "target"), spaceCheck("alice", "own", "s1")}, []bool{true, true, false}, 0, nil},
		{"sequential", []authmodel.CheckRequest{docCheck("d")}, []bool{true}, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, counts, _ := newShortCircuitService(t, authmodel.EvaluationLimits{})
			got, err := svc.CheckBatch(t.Context(), tc.reqs)
			if err != nil || len(got) != len(tc.want) {
				t.Fatalf("batch: %+v/%v", got, err)
			}
			for i, result := range got {
				if result.Allowed != tc.want[i] {
					t.Fatalf("result %d: %+v, want allowed=%v", i, result, tc.want[i])
				}
			}
			if counts.expansions != tc.expansions || !slices.EqualFunc(counts.expanded, tc.expanded, slices.Equal) {
				t.Fatalf("expansions %d %v, want %d %v", counts.expansions, counts.expanded, tc.expansions, tc.expanded)
			}
			pure, err := svc.CheckBatch(decisionmode.WithSubjectFirst(t.Context()), tc.reqs)
			if err != nil {
				t.Fatal(err)
			}
			for i := range pure {
				if pure[i] != got[i] {
					t.Fatalf("subject-first %d: %+v, short circuit %+v", i, pure[i], got[i])
				}
			}
		})
	}

	t.Run("budget edge", func(t *testing.T) {
		svc, _, _ := newShortCircuitService(t, authmodel.EvaluationLimits{MaxGraphStates: 3})
		pureCtx := decisionmode.WithSubjectFirst(t.Context())
		for name, reqs := range map[string][]authmodel.CheckRequest{
			"optimized":  {spaceCheck("alice", "view", "target"), spaceCheck("alice", "view", "s1")},
			"traversal":  {spaceCheck("alice", "view", "target"), spaceCheck("alice", "own", "shared")},
			"sequential": {docCheck("d")},
		} {
			if _, err := svc.CheckBatch(pureCtx, reqs); !errors.Is(err, authmodel.ErrEvaluationLimit) {
				t.Fatalf("%s subject-first must overflow: %v", name, err)
			}
			got, err := svc.CheckBatch(t.Context(), reqs)
			if err != nil || !got[0].Allowed {
				t.Fatalf("%s: direct hits must decide at the budget edge: %+v/%v", name, got, err)
			}
		}
		for name, reqs := range map[string][]authmodel.CheckRequest{
			"optimized": {spaceCheck("alice", "view", "target"), spaceCheck("alice", "view", "shared")},
			"traversal": {spaceCheck("alice", "own", "target"), spaceCheck("alice", "view", "shared")},
		} {
			if got, err := svc.CheckBatch(t.Context(), reqs); !errors.Is(err, authmodel.ErrEvaluationLimit) || got != nil {
				t.Fatalf("%s: a userset miss over budget must error, never deny: %+v/%v", name, got, err)
			}
		}
		ids, err := svc.FilterAuthorized(t.Context(), alice, "view", "space", []string{"s1", "target", "s1"})
		if err != nil || !slices.Equal(ids, []string{"s1", "target", "s1"}) {
			t.Fatalf("filter at the budget edge: %v/%v", ids, err)
		}
	})
}

// TestDirectShortCircuitInvalidStoredReference proves an off-contract stored ID
// reached through a Through edge still denies quietly, as expansion always did,
// instead of failing the exact read with invalid input.
func TestDirectShortCircuitInvalidStoredReference(t *testing.T) {
	svc, _, observed := newShortCircuitService(t, authmodel.EvaluationLimits{})
	malformed := tuples.Tuple{Scope: tuples.On("space", invalidSpaceID), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
	if _, err := observed.Tuples.Contains(t.Context(), malformed); !errors.Is(err, tuples.ErrInvalidRef) {
		t.Fatalf("an exact read must reject the malformed reference, got %v", err)
	}
	alice := authmodel.PrincipalRef{Type: "user", ID: "alice"}
	bad := authmodel.CheckRequest{Principal: alice, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "bad"}}
	good := authmodel.CheckRequest{Principal: alice, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "d"}}
	for _, ctx := range []context.Context{t.Context(), decisionmode.WithSubjectFirst(t.Context())} {
		if got, err := svc.Check(ctx, bad); err != nil || got.Allowed {
			t.Fatalf("check through a malformed target: %+v/%v", got, err)
		}
		results, err := svc.CheckBatch(ctx, []authmodel.CheckRequest{bad, good})
		if err != nil || results[0].Allowed || !results[1].Allowed {
			t.Fatalf("traversal batch through a malformed target: %+v/%v", results, err)
		}
		if got, err := svc.CheckBatch(ctx, []authmodel.CheckRequest{bad}); err != nil || got[0].Allowed {
			t.Fatalf("sequential batch through a malformed target: %+v/%v", got, err)
		}
		// Request, literal and slot references are validated before any read.
		if _, err := svc.FilterAuthorized(ctx, alice, "view", "space", []string{"target", invalidSpaceID}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("malformed batch ID: %v", err)
		}
		literal := decisions.On(authmodel.Resource{Type: "space", ID: invalidSpaceID}, decisions.Direct("viewer"))
		if _, err := svc.EvaluateResolved(ctx, alice, literal, nil); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("malformed literal: %v", err)
		}
		slot := decisions.Expression{Relation: "viewer", ResourceSlot: &decisions.ResourceSlot{Type: "space", Key: "space"}}
		resolve := func(context.Context, string) (authmodel.Resource, error) {
			return authmodel.Resource{Type: "space", ID: invalidSpaceID}, nil
		}
		if _, err := svc.EvaluateResolved(ctx, alice, slot, resolve); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("malformed slot: %v", err)
		}
		hit := decisions.On(authmodel.Resource{Type: "space", ID: "target"}, decisions.Direct("viewer"))
		if got, err := svc.EvaluateResolved(ctx, alice, hit, nil); err != nil || !got.Allowed {
			t.Fatalf("literal hit: %+v/%v", got, err)
		}
	}
}
