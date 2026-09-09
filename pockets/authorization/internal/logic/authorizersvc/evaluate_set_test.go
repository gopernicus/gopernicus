package authorizersvc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

// parityIterations is how many random (schema, tuple set, candidate set) worlds
// the property case builds. Each one is cheap (an in-memory graph store), and
// the seed is the failure's reproduction handle.
const parityIterations = 300

// setPrincipal is the one principal every set case decides for.
var setPrincipal = PrincipalRef{Type: "user", ID: "u1"}

// =============================================================================
// S4 — Check/Filter parity
// =============================================================================

// TestFilterAuthorizedMatchesSequentialCheck is the property the whole set
// evaluation stands on: over random schemas in the supported grammar (Direct,
// Through, AnyOf, usersets on the permitted relations, a self-referential
// Through hierarchy inside the depth cap), random tuple sets and random
// candidate sets, FilterAuthorized returns exactly the ids a per-resource Check
// allows — and errors exactly when one of those Checks errors.
func TestFilterAuthorizedMatchesSequentialCheck(t *testing.T) {
	ctx := context.Background()
	for seed := 0; seed < parityIterations; seed++ {
		rnd := rand.New(rand.NewSource(int64(seed)))
		schema := randomSetSchema(rnd)
		store := &graphStore{}
		store.tuples = randomSetTuples(rnd)
		svc, err := NewService(store, schema, Config{})
		if err != nil {
			t.Fatalf("seed %d: NewService: %v (the generator must only emit compilable schemas)", seed, err)
		}

		for _, resourceType := range []string{"space", "doc"} {
			ids := randomCandidates(rnd, resourceType)

			want := make([]string, 0, len(ids))
			var wantErr error
			for _, id := range ids {
				res, err := svc.Check(ctx, CheckRequest{
					Principal: setPrincipal, Permission: "view", Resource: Resource{Type: resourceType, ID: id},
				})
				if err != nil {
					wantErr = err
					break
				}
				if res.Allowed {
					want = append(want, id)
				}
			}

			got, err := svc.FilterAuthorized(ctx, setPrincipal, "view", resourceType, ids)
			if wantErr != nil {
				if !errors.Is(err, wantErr) {
					t.Fatalf("seed %d %s: FilterAuthorized err = %v, want the sequential Check err %v", seed, resourceType, err, wantErr)
				}
				continue
			}
			if err != nil {
				t.Fatalf("seed %d %s: FilterAuthorized: %v (every Check succeeded)", seed, resourceType, err)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("seed %d %s: FilterAuthorized = %v, want the Check-allowed ids %v (candidates %v, tuples %v)",
					seed, resourceType, got, want, ids, store.tuples)
			}
		}
	}
}

// TestFilterAuthorizedGrantsThroughACandidatesOwnAncestor is the case a
// path-local cycle rule applied to a SET would get wrong: the candidate set
// holds a space AND its parent, and the parent's own grant arrives from further
// up the hierarchy. A set walk that treated the in-frontier parent as "already
// in progress" would deny the child; the least fixpoint grants both, exactly as
// two independent Checks do.
func TestFilterAuthorizedGrantsThroughACandidatesOwnAncestor(t *testing.T) {
	store := &graphStore{}
	store.tuples = []relationship.CreateRelationship{
		tuple("space", "child", "parent", "space", "middle", ""),
		tuple("space", "middle", "parent", "space", "root", ""),
		tuple("space", "root", "viewer", "user", "u1", ""),
	}
	svc := newLimitedService(t, store, setHierarchySchema(), EvaluationLimits{})

	// Both orders: the ancestor before the descendant and after it.
	for _, ids := range [][]string{{"middle", "child"}, {"child", "middle"}} {
		got, err := svc.FilterAuthorized(context.Background(), setPrincipal, "view", "space", ids)
		if err != nil {
			t.Fatalf("FilterAuthorized(%v): %v", ids, err)
		}
		if !slices.Equal(got, ids) {
			t.Fatalf("FilterAuthorized(%v) = %v, want both: a candidate's grant may arrive through another CANDIDATE", ids, got)
		}
	}
}

// TestFilterAuthorizedDeniesOnACycleWithoutError pins the other half of the
// fixpoint: a hierarchy cycle with no grant anywhere denies quietly, exactly as
// Check's path-local cycle rule denies it — it is never a depth overflow.
func TestFilterAuthorizedDeniesOnACycleWithoutError(t *testing.T) {
	store := &graphStore{}
	store.tuples = []relationship.CreateRelationship{
		tuple("space", "c1", "parent", "space", "c2", ""),
		tuple("space", "c2", "parent", "space", "c1", ""),
	}
	svc := newLimitedService(t, store, setHierarchySchema(), EvaluationLimits{})

	got, err := svc.FilterAuthorized(context.Background(), setPrincipal, "view", "space", []string{"c1", "c2"})
	if err != nil {
		t.Fatalf("a cycle must DENY, not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("FilterAuthorized over an ungranted cycle = %v, want none", got)
	}
}

// TestFilterAuthorizedBudgetOverflowMatchesCheck proves the indeterminate
// outcome travels: where a per-resource Check reports ErrEvaluationLimit for a
// candidate, the set evaluation reports it for the whole call — the same
// posture CheckBatch has always had for a batch containing one offending
// request.
func TestFilterAuthorizedBudgetOverflowMatchesCheck(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		limits EvaluationLimits
		tuples []relationship.CreateRelationship
		ids    []string
	}{
		{
			name:   "through depth",
			limits: EvaluationLimits{MaxThroughDepth: 1},
			tuples: []relationship.CreateRelationship{
				tuple("space", "leaf", "parent", "space", "mid", ""),
				tuple("space", "mid", "parent", "space", "root", ""),
				tuple("space", "root", "viewer", "user", "u1", ""),
			},
			ids: []string{"leaf"},
		},
		{
			name:   "per-hop fan-out",
			limits: EvaluationLimits{MaxRelationTargets: 1},
			tuples: []relationship.CreateRelationship{
				tuple("space", "wide", "parent", "space", "p1", ""),
				tuple("space", "wide", "parent", "space", "p2", ""),
			},
			ids: []string{"wide"},
		},
		{
			name:   "group expansion states",
			limits: EvaluationLimits{MaxGraphStates: 2},
			tuples: []relationship.CreateRelationship{
				tuple("group", "g1", "member", "user", "u1", ""),
				tuple("group", "g2", "member", "group", "g1", "member"),
				tuple("space", "grouped", "viewer", "group", "g2", "member"),
			},
			ids: []string{"grouped"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &graphStore{}
			store.tuples = tc.tuples
			svc := newLimitedService(t, store, setHierarchySchema(), tc.limits)

			for _, id := range tc.ids {
				if _, err := svc.Check(ctx, CheckRequest{
					Principal: setPrincipal, Permission: "view", Resource: Resource{Type: "space", ID: id},
				}); !errors.Is(err, ErrEvaluationLimit) {
					t.Fatalf("Check(%s) = %v, want ErrEvaluationLimit (the fixture must offend)", id, err)
				}
			}
			if _, err := svc.FilterAuthorized(ctx, setPrincipal, "view", "space", tc.ids); !errors.Is(err, ErrEvaluationLimit) {
				t.Fatalf("FilterAuthorized = %v, want the ErrEvaluationLimit Check reports", err)
			}
		})
	}
}

// TestFilterAuthorizedRejectsOverSizeBeforeAnyRead pins the MaxBatchSize
// ceiling to the same place CheckBatch charges it: before validation and before
// the first store call.
func TestFilterAuthorizedRejectsOverSizeBeforeAnyRead(t *testing.T) {
	store := &graphStore{}
	svc := newLimitedService(t, store, setHierarchySchema(), EvaluationLimits{MaxBatchSize: 2})

	if _, err := svc.FilterAuthorized(context.Background(), setPrincipal, "view", "space", []string{"a", "b", "c"}); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("over-size FilterAuthorized = %v, want ErrEvaluationLimit", err)
	}
	if reads := store.setReads(); reads != 0 {
		t.Fatalf("an over-size candidate set cost %d store reads, want 0", reads)
	}
}

// =============================================================================
// S4 — the cost model
// =============================================================================

// TestFilterAuthorizedReadsScaleWithBranchesNotCandidates is the claim the
// whole plan exists for: over the DENIED shape — five direct branches, one of
// them userset-bearing, plus two Through hops — the store reads a set
// evaluation makes depend on the SCHEMA (branches + hops), not on how many
// candidates are being decided.
func TestFilterAuthorizedReadsScaleWithBranchesNotCandidates(t *testing.T) {
	// 6 reads at the post node (5 direct branches + 1 Through hop), 2 at the org
	// node (1 direct + 1 hop), 1 at the tenant node: 9, whatever N is.
	const wantReads = 9

	var counts []int
	for _, n := range []int{1, 50, 500} {
		store := &graphStore{}
		store.tuples = wideDeniedTuples(n)
		svc := newLimitedService(t, store, wideSchema(), EvaluationLimits{})

		allowed, err := svc.FilterAuthorized(context.Background(), setPrincipal, "view", "post", deniedCandidates(n))
		if err != nil {
			t.Fatalf("n=%d: FilterAuthorized: %v", n, err)
		}
		if len(allowed) != 0 {
			t.Fatalf("n=%d: fixture must deny every candidate, got %v", n, allowed)
		}
		if got := store.setReads(); got != wantReads {
			t.Fatalf("n=%d: %d store reads, want %d (branches + hops, never per candidate)", n, got, wantReads)
		}
		counts = append(counts, store.setReads())
	}
	if counts[0] != counts[len(counts)-1] {
		t.Fatalf("read counts %v differ across candidate-set sizes", counts)
	}
}

// BenchmarkFilterAuthorizedDeniedCandidates measures the set evaluation over
// the denied shape and reports the store reads it spent, so the cost claim in
// the README is a number this suite prints rather than a promise.
func BenchmarkFilterAuthorizedDeniedCandidates(b *testing.B) {
	for _, n := range []int{50, 300} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ids := deniedCandidates(n)
			reads := 0
			for b.Loop() {
				store := &graphStore{}
				store.tuples = wideDeniedTuples(n)
				svc, err := NewService(store, wideSchema(), Config{})
				if err != nil {
					b.Fatalf("NewService: %v", err)
				}
				if _, err := svc.FilterAuthorized(context.Background(), setPrincipal, "view", "post", ids); err != nil {
					b.Fatalf("FilterAuthorized: %v", err)
				}
				reads = store.setReads()
			}
			b.ReportMetric(float64(reads), "reads/op")
		})
	}
}

// BenchmarkCheckBatchDeniedCandidates is the SAME work through the per-request
// path FilterAuthorized used to take (CheckBatch over N requests, memoized
// reader). Its reads/op is the "before" number the set evaluation is measured
// against; CheckBatch keeps this shape deliberately (R2).
func BenchmarkCheckBatchDeniedCandidates(b *testing.B) {
	for _, n := range []int{50, 300} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			reqs := make([]CheckRequest, n)
			for i, id := range deniedCandidates(n) {
				reqs[i] = CheckRequest{Principal: setPrincipal, Permission: "view", Resource: Resource{Type: "post", ID: id}}
			}
			reads := 0
			for b.Loop() {
				store := &graphStore{}
				store.tuples = wideDeniedTuples(n)
				svc, err := NewService(store, wideSchema(), Config{})
				if err != nil {
					b.Fatalf("NewService: %v", err)
				}
				if _, err := svc.CheckBatch(context.Background(), reqs); err != nil {
					b.Fatalf("CheckBatch: %v", err)
				}
				reads = store.perResourceReads()
			}
			b.ReportMetric(float64(reads), "reads/op")
		})
	}
}

// =============================================================================
// Fixtures
// =============================================================================

// setHierarchySchema is the small world the fixed set cases use: a
// self-referential space hierarchy, a userset-bearing viewer relation, and a
// doc that inherits through its space.
func setHierarchySchema() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "group", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "space", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("viewer"), Through("parent", "view"))},
		}},
	})
}

// wideSchema is the DENIED shape the cost model is stated over: five direct
// branches on post (one of them userset-bearing) plus a two-hop Through chain
// post -> org -> tenant.
func wideSchema() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "group", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
		}},
		{Name: "tenant", Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
		}},
		{Name: "org", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"admin":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"tenant": {AllowedSubjects: []SubjectTypeRef{{Type: "tenant"}}},
			},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"), Through("tenant", "manage"))},
		}},
		{Name: "post", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner":     {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"editor":    {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"reviewer":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"publisher": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"viewer":    {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"org":       {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(
				Direct("owner"), Direct("editor"), Direct("reviewer"), Direct("publisher"), Direct("viewer"),
				Through("org", "manage"),
			)},
		}},
	})
}

// wideDeniedTuples gives n posts one shared org and that org one tenant, with
// every grant held by ANOTHER user: every branch of every candidate must be
// exhausted, which is the expensive shape.
func wideDeniedTuples(n int) []relationship.CreateRelationship {
	out := []relationship.CreateRelationship{
		tuple("org", "o1", "tenant", "tenant", "t1", ""),
		tuple("org", "o1", "admin", "user", "other", ""),
		tuple("tenant", "t1", "admin", "user", "other", ""),
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%04d", i)
		out = append(out,
			tuple("post", id, "org", "org", "o1", ""),
			tuple("post", id, "owner", "user", "other", ""),
		)
	}
	return out
}

func deniedCandidates(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("p%04d", i)
	}
	return out
}

// =============================================================================
// The random world the property case explores
// =============================================================================

// randomSetSchema builds a compilable schema inside the supported grammar: two
// leaf permissions on org, a self-referential space hierarchy that may also
// inherit from its org, and a doc that may inherit from its space. Every
// permission carries at least one Direct check, so no rule is unsatisfiable and
// the generator never emits a schema Compile rejects.
func randomSetSchema(rnd *rand.Rand) Schema {
	orgChecks := atLeastOne(rnd, []PermissionCheck{Direct("admin"), Direct("member")})

	spaceChecks := atLeastOne(rnd, []PermissionCheck{Direct("viewer"), Direct("editor")})
	if rnd.Intn(2) == 0 {
		spaceChecks = append(spaceChecks, Through("parent", "view"))
	}
	if rnd.Intn(2) == 0 {
		spaceChecks = append(spaceChecks, Through("org", "view"))
	}

	docChecks := atLeastOne(rnd, []PermissionCheck{Direct("owner"), Direct("reader")})
	if rnd.Intn(3) > 0 {
		docChecks = append(docChecks, Through("space", "view"))
	}

	return NewSchema([]ResourceSchema{
		{Name: "group", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
		}},
		{Name: "org", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"admin":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(orgChecks...)},
		}},
		{Name: "space", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"org":    {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
				"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
				"editor": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(spaceChecks...)},
		}},
		{Name: "doc", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"space":  {AllowedSubjects: []SubjectTypeRef{{Type: "space"}}},
				"owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"reader": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(docChecks...)},
		}},
	})
}

// atLeastOne picks a non-empty random subset of options, preserving order.
func atLeastOne(rnd *rand.Rand, options []PermissionCheck) []PermissionCheck {
	var out []PermissionCheck
	for _, opt := range options {
		if rnd.Intn(2) == 0 {
			out = append(out, opt)
		}
	}
	if len(out) == 0 {
		out = append(out, options[rnd.Intn(len(options))])
	}
	return out
}

const (
	randomSpaces = 6
	randomDocs   = 8
	randomOrgs   = 2
)

// randomSetTuples populates the random world: nested group memberships, org
// grants, a space FOREST (each space's parent has a lower index, so the
// hierarchy is acyclic and well inside the depth cap), org edges, and grants
// held by the principal, by another user, or through a group userset.
func randomSetTuples(rnd *rand.Rand) []relationship.CreateRelationship {
	var out []relationship.CreateRelationship

	if rnd.Intn(2) == 0 {
		out = append(out, tuple("group", "g1", "member", "user", "u1", ""))
	}
	if rnd.Intn(2) == 0 {
		out = append(out, tuple("group", "g2", "member", "group", "g1", "member"))
	}

	subject := func() (string, string, string) {
		switch rnd.Intn(4) {
		case 0:
			return "user", "u1", ""
		case 1:
			return "group", "g1", "member"
		case 2:
			return "group", "g2", "member"
		default:
			return "user", "u2", ""
		}
	}

	for i := 0; i < randomOrgs; i++ {
		org := fmt.Sprintf("o%d", i)
		if rnd.Intn(2) == 0 {
			st, sid, srel := subject()
			out = append(out, tuple("org", org, "admin", st, sid, srel))
		}
		if rnd.Intn(3) == 0 {
			out = append(out, tuple("org", org, "member", "user", "u1", ""))
		}
	}

	for i := 0; i < randomSpaces; i++ {
		space := fmt.Sprintf("s%d", i)
		if i > 0 && rnd.Intn(3) > 0 {
			out = append(out, tuple("space", space, "parent", "space", fmt.Sprintf("s%d", rnd.Intn(i)), ""))
		}
		if rnd.Intn(2) == 0 {
			out = append(out, tuple("space", space, "org", "org", fmt.Sprintf("o%d", rnd.Intn(randomOrgs)), ""))
		}
		if rnd.Intn(4) == 0 {
			st, sid, srel := subject()
			out = append(out, tuple("space", space, "viewer", st, sid, srel))
		}
		if rnd.Intn(6) == 0 {
			out = append(out, tuple("space", space, "editor", "user", "u1", ""))
		}
	}

	for i := 0; i < randomDocs; i++ {
		doc := fmt.Sprintf("d%d", i)
		if rnd.Intn(4) > 0 {
			out = append(out, tuple("doc", doc, "space", "space", fmt.Sprintf("s%d", rnd.Intn(randomSpaces)), ""))
		}
		if rnd.Intn(6) == 0 {
			out = append(out, tuple("doc", doc, "owner", "user", "u1", ""))
		}
		if rnd.Intn(5) == 0 {
			st, sid, srel := subject()
			out = append(out, tuple("doc", doc, "reader", st, sid, srel))
		}
	}
	return out
}

// randomCandidates draws a candidate list that deliberately includes repeats,
// ids in no particular order, and ids no tuple mentions.
func randomCandidates(rnd *rand.Rand, resourceType string) []string {
	universe := randomSpaces
	prefix := "s"
	if resourceType == "doc" {
		universe = randomDocs
		prefix = "d"
	}
	var out []string
	for i := 0; i < universe; i++ {
		if rnd.Intn(2) == 0 {
			out = append(out, fmt.Sprintf("%s%d", prefix, i))
		}
	}
	if len(out) == 0 {
		out = append(out, fmt.Sprintf("%s%d", prefix, rnd.Intn(universe)))
	}
	if rnd.Intn(3) == 0 {
		out = append(out, out[0]) // a repeat: the answer keeps the caller's multiplicity
	}
	if rnd.Intn(3) == 0 {
		out = append(out, prefix+"absent")
	}
	rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// perResourceReads totals the PER-RESOURCE reads the fake counted, the "before"
// number the set evaluation is compared against.
func (f *fakeStore) perResourceReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.directCalls {
		total += n
	}
	for _, n := range f.targetsCalls {
		total += n
	}
	return total
}
