package storetest

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/decisionmode"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// equivalenceSeeds are fixed so a failure names a reproducible universe. Seeds
// with seed%3 == 0 use a tiny MaxGraphStates and seed%3 == 1 a tiny
// MaxEvaluationSteps, so overflow occurs and its assertions are exercised.
var equivalenceSeeds = []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

var (
	equivalenceUsers  = []string{"u0", "u1", "u2", "u3", "u4"}
	equivalenceGroups = []string{"g0", "g1", "g2", "g3"}
	equivalenceSpaces = []string{"s0", "s1", "s2", "s3", "s4"}
	equivalenceDocs   = []string{"d0", "d1", "d2", "d3", "d4", "d5"}
)

// Limit kinds drawn per seed: a tiny graph-state budget, a tiny step budget,
// or generous limits.
const (
	limitGraphStates = "MaxGraphStates"
	limitSteps       = "MaxEvaluationSteps"
	limitNone        = "generous"
)

// equivalenceTally counts the outcomes that prove the overflow and short-circuit
// assertions ran.
type equivalenceTally struct {
	seeds     int
	decisions int
	overflows map[string]int
	drawn     map[string]bool
	edge      int
}

// RunLookupEquivalence proves the direct-hit short circuit answers exactly as
// pure subject-first expansion over random models and tuples, including cycles
// and off-model rows. Where both modes decide, they agree with an unbounded
// oracle; overflow is always ErrEvaluationLimit, never a deny; and the short
// circuit never overflows where subject-first decided. factory returns fresh,
// empty repositories; rows are written through the canonical tuple port.
func RunLookupEquivalence(t *testing.T, factory func(t *testing.T) Repositories) {
	runLookupEquivalence(t, factory, nil, equivalenceSeeds)
}

// RunLookupEquivalenceTupleCache runs the same proof with a polled tuple cache
// in front of the store. It polls after every mutation and requires every
// decision to be served by the cache: Stats().Fallbacks == 0 and Hits > 0.
// factory must return repositories with TupleSource; backend returns a fresh,
// empty backend for one service.
func RunLookupEquivalenceTupleCache(t *testing.T, factory func(t *testing.T) Repositories, backend func(t *testing.T) tuplecache.Backend) {
	runLookupEquivalence(t, factory, backend, equivalenceSeeds[:6])
}

func runLookupEquivalence(t *testing.T, factory func(t *testing.T) Repositories, backend func(t *testing.T) tuplecache.Backend, seeds []int64) {
	tally := &equivalenceTally{overflows: map[string]int{}, drawn: map[string]bool{}}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Logf("lookup equivalence seed %d", seed)
			repos := factory(t)
			if backend != nil && repos.TupleSource == nil {
				t.Fatal("tuple cache equivalence requires a TupleSource")
			}
			runEquivalenceSeed(t, repos, seed, backend, tally)
			tally.seeds++
		})
	}
	if t.Failed() || tally.seeds == 0 {
		return
	}
	t.Logf("lookup equivalence: %d seeds, %d decisions, subject-first overflows %v, %d decided only by the short circuit", tally.seeds, tally.decisions, tally.overflows, tally.edge)
	for _, kind := range []string{limitGraphStates, limitSteps} {
		if tally.drawn[kind] && tally.overflows[kind] == 0 {
			t.Errorf("no subject-first evaluation overflowed %s: the overflow assertions were never exercised for it", kind)
		}
	}
	if tally.edge == 0 {
		t.Error("the short circuit never decided where subject-first overflowed: it may have stopped firing")
	}
}

func runEquivalenceSeed(t *testing.T, repos Repositories, seed int64, backend func(t *testing.T) tuplecache.Backend, tally *equivalenceTally) {
	ctx := context.Background()
	rnd := rand.New(rand.NewSource(seed))
	model := randomEquivalenceModel(rnd)
	limits := generousLimits()
	kind := limitNone
	switch seed % 3 {
	case 0:
		kind, limits.MaxGraphStates = limitGraphStates, 2+rnd.Intn(4)
	case 1:
		kind, limits.MaxEvaluationSteps = limitSteps, 3+rnd.Intn(6)
	}
	tally.drawn[kind] = true
	truth, err := authorization.New(repos.Repositories, authorization.WithModel(model), authorization.WithLimits(generousLimits()))
	if err != nil {
		t.Fatal(err)
	}
	opts := []authorization.Option{authorization.WithModel(model), authorization.WithLimits(limits)}
	if backend != nil {
		opts = append(opts, authorization.WithTupleCache(backend(t), tuplecache.Policy{MaxStaleness: time.Hour}))
	}
	svc, err := authorization.New(repos.Repositories, opts...)
	if err != nil {
		t.Fatal(err)
	}
	poll := func() {
		t.Helper()
		if svc.TupleCache == nil {
			return
		}
		if err := svc.TupleCache.Poll(ctx); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if svc.TupleCache != nil {
		t.Cleanup(func() { _ = svc.TupleCache.Close() })
	}
	poll()

	e := &equivalence{t: t, rnd: rnd, truth: truth.Decisions, svc: svc.Decisions, tuples: repos.Tuples, kind: kind, tally: tally}
	apply := func(changes tuples.Changes) {
		t.Helper()
		if err := repos.Tuples.ApplyTuples(ctx, changes); err != nil {
			t.Fatalf("ApplyTuples: %v", err)
		}
		poll()
	}
	facts := randomEquivalenceTuples(rnd)
	written := slices.Clone(facts)
	for len(facts) > 0 {
		n := min(len(facts), 1+len(facts)/3)
		apply(tuples.Changes{Add: facts[:n]})
		facts = facts[n:]
		e.sweep(ctx)
	}
	for range 4 {
		apply(tuples.Changes{Remove: []tuples.Tuple{written[rnd.Intn(len(written))]}})
	}
	e.sweep(ctx)

	if svc.TupleCache != nil {
		stats := svc.TupleCache.Stats()
		if stats.Fallbacks != 0 || stats.Hits == 0 {
			t.Fatalf("decisions must be served by the polled cache: %+v", stats)
		}
	}
}

// randomEquivalenceModel draws subject shapes per relation. Owner and editor
// stay concrete-only; viewer and member may admit nested usersets, and a quarter
// of models admit no usersets anywhere.
func randomEquivalenceModel(rnd *rand.Rand) decisions.Model {
	flat := rnd.Intn(4) == 0
	maybe := func(p float64, ref decisions.SubjectTypeRef) []decisions.SubjectTypeRef {
		if flat || rnd.Float64() >= p {
			return nil
		}
		return []decisions.SubjectTypeRef{ref}
	}
	concrete := func(p float64, ref decisions.SubjectTypeRef) []decisions.SubjectTypeRef {
		if rnd.Float64() >= p {
			return nil
		}
		return []decisions.SubjectTypeRef{ref}
	}
	user := decisions.SubjectTypeRef{Type: "user"}
	group := decisions.SubjectTypeRef{Type: "group"}
	member := decisions.SubjectTypeRef{Type: "group", Relation: "member"}
	spaceViewer := decisions.SubjectTypeRef{Type: "space", Relation: "viewer"}
	subjects := func(extra ...[]decisions.SubjectTypeRef) decisions.RelationDef {
		return decisions.RelationDef{AllowedSubjects: append([]decisions.SubjectTypeRef{user}, slices.Concat(extra...)...)}
	}
	shuffled := func(es ...decisions.Expression) decisions.Expression {
		rnd.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		return decisions.AnyOf(es...)
	}
	spaceView := []decisions.Expression{decisions.Direct("viewer"), decisions.Direct("editor")}
	if rnd.Intn(2) == 0 {
		spaceView = append(spaceView, decisions.Through("parent", "view"))
	}
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "group", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{
			"member": subjects(maybe(0.7, member)),
		}}},
		{Name: "space", Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"viewer": subjects(maybe(0.6, member), maybe(0.4, spaceViewer)),
				"editor": subjects(),
				"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]decisions.Expression{
				"view": shuffled(spaceView...),
				"edit": decisions.AnyOf(decisions.Direct("editor")),
			},
		}},
		{Name: "doc", Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				// Admitting both group and group#member lets a group principal
				// hit directly while its members still expand.
				"viewer": subjects(maybe(0.6, member), maybe(0.4, spaceViewer), concrete(0.6, group)),
				"owner":  subjects(),
				"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "space"}}},
			},
			Permissions: map[string]decisions.Expression{
				// read has no Through, so a uniform batch takes the optimized path.
				"read": shuffled(decisions.Direct("owner"), decisions.Direct("viewer")),
				"view": shuffled(decisions.Direct("owner"), decisions.Direct("viewer"), decisions.Through("parent", "view")),
			},
		}},
	})
}

// randomEquivalenceTuples draws raw canonical rows, so shapes outside the drawn
// model are stored too. u0 always holds many direct grants.
func randomEquivalenceTuples(rnd *rand.Rand) []tuples.Tuple {
	seen := map[tuples.Tuple]bool{}
	var out []tuples.Tuple
	add := func(rt, id, rel, st, sid, srel string) {
		c := tuples.Tuple{Scope: tuples.On(rt, id), Relation: rel, Subject: tuples.SubjectRef{Type: st, ID: sid, Relation: srel}}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, s := range equivalenceSpaces {
		if rnd.Intn(5) < 3 {
			add("space", s, "viewer", "user", "u0", "")
		}
	}
	for _, d := range equivalenceDocs {
		if rnd.Intn(2) == 0 {
			add("doc", d, pick(rnd, []string{"owner", "viewer"}), "user", "u0", "")
		}
	}
	for range 8 {
		if rnd.Intn(2) == 0 {
			add("space", pick(rnd, equivalenceSpaces), pick(rnd, []string{"viewer", "editor"}), "user", pick(rnd, equivalenceUsers), "")
		} else {
			add("doc", pick(rnd, equivalenceDocs), pick(rnd, []string{"viewer", "owner"}), "user", pick(rnd, equivalenceUsers), "")
		}
	}
	for range 5 {
		add("group", pick(rnd, equivalenceGroups), "member", "user", pick(rnd, equivalenceUsers), "")
	}
	if rnd.Intn(10) < 7 {
		add("group", "g0", "member", "group", "g1", "member")
		add("group", "g1", "member", "group", "g0", "member")
	}
	for range 2 {
		add("group", pick(rnd, equivalenceGroups), "member", "group", pick(rnd, equivalenceGroups), "member")
	}
	for range 4 {
		add("space", pick(rnd, equivalenceSpaces), "viewer", "group", pick(rnd, equivalenceGroups), "member")
	}
	for range 3 {
		add("doc", pick(rnd, equivalenceDocs), "viewer", "group", pick(rnd, equivalenceGroups), "member")
	}
	if rnd.Intn(10) < 6 {
		add("space", "s0", "viewer", "space", "s1", "viewer")
		add("space", "s1", "viewer", "space", "s0", "viewer")
	}
	for range 2 {
		add("doc", pick(rnd, equivalenceDocs), "viewer", "space", pick(rnd, equivalenceSpaces), "viewer")
	}
	for range 3 {
		add("doc", pick(rnd, equivalenceDocs), "viewer", "group", pick(rnd, equivalenceGroups), "")
	}
	for _, d := range equivalenceDocs {
		if rnd.Intn(5) < 3 {
			add("doc", d, "parent", "space", pick(rnd, equivalenceSpaces), "")
		}
	}
	for range 3 {
		add("space", pick(rnd, equivalenceSpaces), "parent", "space", pick(rnd, equivalenceSpaces), "")
	}
	// Off-model rows: no drawn model admits these subject shapes.
	add("space", pick(rnd, equivalenceSpaces), "viewer", "group", pick(rnd, equivalenceGroups), "")
	add("doc", pick(rnd, equivalenceDocs), "owner", "group", pick(rnd, equivalenceGroups), "member")
	add("space", pick(rnd, equivalenceSpaces), "editor", "space", pick(rnd, equivalenceSpaces), "viewer")
	add("doc", pick(rnd, equivalenceDocs), "parent", "space", pick(rnd, equivalenceSpaces), "viewer")
	add("space", pick(rnd, equivalenceSpaces), "viewer", "user", pick(rnd, equivalenceUsers), "member")
	rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func pick(rnd *rand.Rand, values []string) string { return values[rnd.Intn(len(values))] }

type equivalence struct {
	t      *testing.T
	rnd    *rand.Rand
	truth  *decisions.Service
	svc    *decisions.Service
	tuples tuples.Storer
	kind   string
	tally  *equivalenceTally
}

func (e *equivalence) principal() authmodel.PrincipalRef {
	if e.rnd.Intn(5) == 0 {
		return authmodel.PrincipalRef{Type: "group", ID: pick(e.rnd, equivalenceGroups)}
	}
	if e.rnd.Intn(3) == 0 {
		return authmodel.PrincipalRef{Type: "user", ID: "u0"}
	}
	return authmodel.PrincipalRef{Type: "user", ID: pick(e.rnd, equivalenceUsers)}
}

func (e *equivalence) request(principal authmodel.PrincipalRef) authmodel.CheckRequest {
	if e.rnd.Intn(2) == 0 {
		return authmodel.CheckRequest{Principal: principal, Permission: pick(e.rnd, []string{"view", "edit"}), Resource: authmodel.Resource{Type: "space", ID: pick(e.rnd, equivalenceSpaces)}}
	}
	return authmodel.CheckRequest{Principal: principal, Permission: pick(e.rnd, []string{"view", "read"}), Resource: authmodel.Resource{Type: "doc", ID: pick(e.rnd, equivalenceDocs)}}
}

func (e *equivalence) docs(principal authmodel.PrincipalRef, permission string, n int) []authmodel.CheckRequest {
	reqs := make([]authmodel.CheckRequest, n)
	for i := range reqs {
		reqs[i] = authmodel.CheckRequest{Principal: principal, Permission: permission, Resource: authmodel.Resource{Type: "doc", ID: pick(e.rnd, equivalenceDocs)}}
	}
	return reqs
}

// sweep compares single checks, the three CheckBatch dispatch paths and
// FilterAuthorized under the short circuit and under pure subject-first.
func (e *equivalence) sweep(ctx context.Context) {
	e.t.Helper()
	single := func(name string, evaluate func(context.Context, *decisions.Service, authmodel.CheckRequest) (authmodel.CheckResult, error)) {
		req := e.request(e.principal())
		e.compare(ctx, fmt.Sprintf("%s %+v", name, req), []authmodel.CheckRequest{req}, func(ctx context.Context, svc *decisions.Service, reqs []authmodel.CheckRequest) ([]bool, error) {
			result, err := evaluate(ctx, svc, reqs[0])
			if err != nil {
				if result != (authmodel.CheckResult{}) {
					e.t.Fatalf("a failed %s returned a decision: %+v/%v", name, result, err)
				}
				return nil, err
			}
			if want := outcome(result.Allowed); result.ReasonCode != want {
				e.t.Fatalf("%s ReasonCode %q, want %q", name, result.ReasonCode, want)
			}
			return []bool{result.Allowed}, nil
		})
	}
	for range 6 {
		single("Check", func(ctx context.Context, svc *decisions.Service, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
			return svc.Check(ctx, req)
		})
		single("CheckExplain", func(ctx context.Context, svc *decisions.Service, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
			result, explanation, err := svc.CheckExplain(ctx, req)
			if err == nil && explanation.Decision != result.ReasonCode {
				e.t.Fatalf("Explanation.Decision %q, result %q", explanation.Decision, result.ReasonCode)
			}
			for _, step := range explanation.Steps {
				if step.Outcome == authmodel.ReasonDirectHit && step.Kind != authmodel.ExplainKindDirect {
					e.t.Fatalf("direct_hit on a %q step", step.Kind)
				}
			}
			return result, err
		})
		single("CheckWith", func(ctx context.Context, svc *decisions.Service, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
			var result authmodel.CheckResult
			var checkErr error
			if err := e.tuples.ReadTupleSnapshot(ctx, func(ctx context.Context, reader tuples.Reader) error {
				result, checkErr = svc.CheckWith(ctx, reader, req)
				return nil
			}); err != nil {
				e.t.Fatalf("snapshot: %v", err)
			}
			return result, checkErr
		})
		single("EvaluateResolved", func(ctx context.Context, svc *decisions.Service, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
			relation := map[string]string{"view": "viewer", "edit": "editor", "read": "owner"}[req.Permission]
			return svc.EvaluateResolved(ctx, req.Principal, decisions.On(req.Resource, decisions.Direct(relation)), nil)
		})
	}
	for range 2 {
		principal := e.principal()
		resourceType, universe := "doc", equivalenceDocs
		permission := pick(e.rnd, []string{"view", "read"})
		if e.rnd.Intn(2) == 0 {
			resourceType, universe, permission = "space", equivalenceSpaces, pick(e.rnd, []string{"view", "edit"})
		}
		reqs := make([]authmodel.CheckRequest, len(universe))
		for i, id := range universe {
			reqs[i] = authmodel.CheckRequest{Principal: principal, Permission: permission, Resource: authmodel.Resource{Type: resourceType, ID: id}}
		}
		// Lookup verifies every candidate through the ordinary check, so the short
		// circuit decides there too.
		e.compare(ctx, "LookupResources", reqs, func(ctx context.Context, svc *decisions.Service, reqs []authmodel.CheckRequest) ([]bool, error) {
			found, err := svc.LookupResources(ctx, principal, permission, resourceType)
			if err != nil {
				if found.IDs != nil {
					e.t.Fatalf("a failed lookup returned ids: %v/%v", found.IDs, err)
				}
				return nil, err
			}
			for _, id := range found.IDs {
				if !slices.Contains(universe, id) {
					e.t.Fatalf("lookup invented %s:%s", resourceType, id)
				}
			}
			out := make([]bool, len(reqs)+1)
			for i, req := range reqs {
				out[i] = slices.Contains(found.IDs, req.Resource.ID)
			}
			out[len(reqs)] = found.Unrestricted
			return out, nil
		})
	}
	batch := func(ctx context.Context, svc *decisions.Service, reqs []authmodel.CheckRequest) ([]bool, error) {
		results, err := svc.CheckBatch(ctx, reqs)
		if err != nil {
			if results != nil {
				e.t.Fatalf("a failed batch returned decisions: %+v/%v", results, err)
			}
			return nil, err
		}
		out := make([]bool, len(results))
		for i, result := range results {
			out[i] = result.Allowed
			if want := outcome(result.Allowed); result.ReasonCode != want {
				e.t.Fatalf("batch ReasonCode %q, want %q", result.ReasonCode, want)
			}
		}
		return out, nil
	}
	principal := e.principal()
	// Uniform requests on a permission without Through take the optimized path;
	// with Through, two or more take the traversal path and one the sequential.
	e.compare(ctx, "CheckBatch optimized", e.docs(principal, "read", 1+e.rnd.Intn(5)), batch)
	e.compare(ctx, "CheckBatch traversal", e.docs(principal, "view", 2+e.rnd.Intn(4)), batch)
	e.compare(ctx, "CheckBatch sequential", e.docs(principal, "view", 1), batch)
	mixed := []authmodel.CheckRequest{e.request(e.principal()), e.request(e.principal()), e.request(e.principal())}
	e.compare(ctx, "CheckBatch mixed traversal", mixed, batch)
	for _, permission := range []string{"read", "view"} {
		reqs := e.docs(principal, permission, 1+e.rnd.Intn(6))
		e.compare(ctx, "FilterAuthorized "+permission, reqs, func(ctx context.Context, svc *decisions.Service, reqs []authmodel.CheckRequest) ([]bool, error) {
			ids := make([]string, len(reqs))
			for i, req := range reqs {
				ids[i] = req.Resource.ID
			}
			allowed, err := svc.FilterAuthorized(ctx, reqs[0].Principal, reqs[0].Permission, "doc", ids)
			if err != nil {
				if allowed != nil {
					e.t.Fatalf("a failed filter returned ids: %v/%v", allowed, err)
				}
				return nil, err
			}
			out := make([]bool, len(ids))
			for i, id := range ids {
				out[i] = slices.Contains(allowed, id)
			}
			return out, nil
		})
	}
}

func outcome(allowed bool) authmodel.Reason {
	if allowed {
		return authmodel.ReasonGranted
	}
	return authmodel.ReasonDenied
}

// compare evaluates reqs under the unbounded oracle, the short circuit and pure
// subject-first, then applies the three equivalence rules.
func (e *equivalence) compare(ctx context.Context, name string, reqs []authmodel.CheckRequest, run func(context.Context, *decisions.Service, []authmodel.CheckRequest) ([]bool, error)) {
	e.t.Helper()
	pureCtx := decisionmode.WithSubjectFirst(ctx)
	want, err := run(pureCtx, e.truth, reqs)
	if err != nil {
		e.t.Fatalf("%s: unbounded oracle failed: %v", name, err)
	}
	short, shortErr := run(ctx, e.svc, reqs)
	pure, pureErr := run(pureCtx, e.svc, reqs)
	for mode, err := range map[string]error{"short circuit": shortErr, "subject-first": pureErr} {
		if err != nil && !errors.Is(err, authmodel.ErrEvaluationLimit) {
			e.t.Fatalf("%s: %s failed with a non-budget error: %v", name, mode, err)
		}
	}
	if pureErr == nil && shortErr != nil {
		e.t.Fatalf("%s: short circuit overflowed where subject-first decided: %v", name, shortErr)
	}
	if shortErr == nil && !slices.Equal(short, want) {
		e.t.Fatalf("%s %+v: short circuit %v, oracle %v", name, reqs, short, want)
	}
	if pureErr == nil && !slices.Equal(pure, want) {
		e.t.Fatalf("%s %+v: subject-first %v, oracle %v", name, reqs, pure, want)
	}
	e.tally.decisions++
	if pureErr != nil {
		e.tally.overflows[e.kind]++
		if shortErr == nil {
			e.tally.edge++
		}
	}
}
