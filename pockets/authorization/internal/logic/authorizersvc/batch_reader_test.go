package authorizersvc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// errStubRead is the stub reader's injected failure.
var errStubRead = errors.New("stub read failed")

// stubReader is a minimal PermissionReader for memoReader unit tests: it counts
// calls, can fail its first call of each method, and returns a fixed answer.
type stubReader struct {
	targets      []relationship.RelationTarget
	allowed      bool
	failFirst    bool
	targetsCalls int
	directCalls  int
}

func (s *stubReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	s.targetsCalls++
	if s.failFirst && s.targetsCalls == 1 {
		return nil, errStubRead
	}
	return cloneTargets(s.targets), nil
}

func (s *stubReader) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	s.directCalls++
	if s.failFirst && s.directCalls == 1 {
		return false, errStubRead
	}
	return s.allowed, nil
}

// chainSchema adds a second Through hop above testSchema's: post.view goes
// through org.manage, and org.manage itself goes through tenant.manage. It gives
// the batch a container whose OWN evaluation performs both reader calls, so the
// memo's sharing (and the depth budget) are observable at the container.
func chainSchema() Schema {
	return NewSchema([]ResourceSchema{
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
				"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"org":   {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{"view": AnyOf(Direct("owner"), Through("org", "manage"))},
		}},
	})
}

func newLimitedService(t *testing.T, store relationship.Storer, schema Schema, limits EvaluationLimits) *Service {
	t.Helper()
	svc, err := NewService(store, schema, Config{Limits: limits, IDs: cryptids.IDGenerator{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// containerTuples builds one org (o1, administered by u1) holding n posts.
func containerTuples(n int) []relationship.CreateRelationship {
	out := []relationship.CreateRelationship{tuple("org", "o1", "admin", "user", "u1", "")}
	for i := 0; i < n; i++ {
		out = append(out, tuple("post", fmt.Sprintf("p%d", i), "org", "org", "o1", ""))
	}
	return out
}

// assertBatchParity pins CheckBatch to sequential Check: same error for the
// FIRST request Check fails on, otherwise the same result for every element.
func assertBatchParity(t *testing.T, svc *Service, reqs []CheckRequest) {
	t.Helper()
	ctx := context.Background()

	want := make([]CheckResult, len(reqs))
	var wantErr error
	for i, req := range reqs {
		res, err := svc.Check(ctx, req)
		if err != nil {
			wantErr = err
			break
		}
		want[i] = res
	}

	got, err := svc.CheckBatch(ctx, reqs)
	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("CheckBatch err = %v, want sequential Check err %v", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("CheckBatch: %v (sequential Check succeeded)", err)
	}
	if len(got) != len(want) {
		t.Fatalf("CheckBatch returned %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result[%d] = %+v, want %+v (req %+v)", i, got[i], want[i], reqs[i])
		}
	}
}

// TestCheckBatchThroughParityWithSequentialCheck proves the memoized batch
// reader changes nothing observable: a heterogeneous batch (different
// principals, permissions and resource types, plus an off-schema userset edge
// the walk must skip) matches per-request Check element by element.
func TestCheckBatchThroughParityWithSequentialCheck(t *testing.T) {
	store := &fakeStore{tuples: []relationship.CreateRelationship{
		tuple("org", "o1", "admin", "user", "u1", ""),
		tuple("org", "o2", "admin", "user", "u2", ""),
		tuple("post", "p1", "org", "org", "o1", ""),
		tuple("post", "p2", "org", "org", "o2", ""),
		tuple("post", "p3", "org", "org", "o1", ""),
		tuple("post", "p5", "owner", "user", "u3", ""),
		{ResourceType: "post", ResourceID: "p4", Relation: "org", SubjectType: "org", SubjectID: "o9", SubjectRelation: "member"},
	}}
	svc := newTestService(t, store, cryptids.IDGenerator{})

	u1 := PrincipalRef{Type: "user", ID: "u1"}
	u2 := PrincipalRef{Type: "user", ID: "u2"}
	u3 := PrincipalRef{Type: "user", ID: "u3"}

	assertBatchParity(t, svc, []CheckRequest{
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "p1"}},
		{Principal: u2, Permission: "view", Resource: Resource{Type: "post", ID: "p1"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "p2"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "p3"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "p4"}},
		{Principal: u3, Permission: "delete", Resource: Resource{Type: "post", ID: "p5"}},
		{Principal: u3, Permission: "view", Resource: Resource{Type: "post", ID: "p5"}},
		{Principal: u1, Permission: "manage", Resource: Resource{Type: "org", ID: "o1"}},
		{Principal: u2, Permission: "manage", Resource: Resource{Type: "org", ID: "o1"}},
	})
}

// TestCheckBatchLimitParityRelationTargets pins fan-out exhaustion: a shared
// read does not make a later request cheaper, so the batch fails exactly where
// sequential Check fails.
func TestCheckBatchLimitParityRelationTargets(t *testing.T) {
	store := &fakeStore{tuples: []relationship.CreateRelationship{
		tuple("post", "ok", "owner", "user", "u1", ""),
		tuple("post", "wide", "org", "org", "o1", ""),
		tuple("post", "wide", "org", "org", "o2", ""),
		tuple("post", "wide", "org", "org", "o3", ""),
	}}
	svc := newLimitedService(t, store, testSchema(), EvaluationLimits{MaxRelationTargets: 2})

	u1 := PrincipalRef{Type: "user", ID: "u1"}
	reqs := []CheckRequest{
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "ok"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "wide"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "ok"}},
	}
	if _, err := svc.Check(context.Background(), reqs[1]); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("Check(wide) = %v, want ErrEvaluationLimit (fixture is not offending)", err)
	}
	assertBatchParity(t, svc, reqs)
}

// TestCheckBatchLimitParityGraphStates pins the distinct-state budget: each
// request still gets its OWN fresh budget, so a Through request over a
// one-state ceiling is indeterminate in the batch exactly as it is alone.
func TestCheckBatchLimitParityGraphStates(t *testing.T) {
	store := &fakeStore{tuples: containerTuples(3)}
	svc := newLimitedService(t, store, testSchema(), EvaluationLimits{MaxGraphStates: 1})

	u1 := PrincipalRef{Type: "user", ID: "u1"}
	reqs := []CheckRequest{
		{Principal: u1, Permission: "delete", Resource: Resource{Type: "post", ID: "p0"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "p1"}},
	}
	if _, err := svc.Check(context.Background(), reqs[1]); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("Check(p1) = %v, want ErrEvaluationLimit (fixture is not offending)", err)
	}
	assertBatchParity(t, svc, reqs)
}

// TestCheckBatchLimitParityThroughDepth pins depth over a two-hop chain: depth
// is a stack property of each request's own evaluation, never shared by the memo.
func TestCheckBatchLimitParityThroughDepth(t *testing.T) {
	store := &fakeStore{tuples: []relationship.CreateRelationship{
		tuple("tenant", "t1", "admin", "user", "u1", ""),
		tuple("org", "o1", "tenant", "tenant", "t1", ""),
		tuple("post", "p0", "org", "org", "o1", ""),
		tuple("post", "p1", "org", "org", "o1", ""),
	}}
	svc := newLimitedService(t, store, chainSchema(), EvaluationLimits{MaxThroughDepth: 1})

	u1 := PrincipalRef{Type: "user", ID: "u1"}
	reqs := []CheckRequest{
		{Principal: u1, Permission: "manage", Resource: Resource{Type: "org", ID: "o1"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "p0"}},
		{Principal: u1, Permission: "view", Resource: Resource{Type: "post", ID: "p1"}},
	}
	if _, err := svc.Check(context.Background(), reqs[1]); !errors.Is(err, ErrEvaluationLimit) {
		t.Fatalf("Check(p0) = %v, want ErrEvaluationLimit (fixture is not offending)", err)
	}
	assertBatchParity(t, svc, reqs)
}

// TestCheckBatchIsIndependentOfLookupBudget proves the batch never builds a
// global accessible-container set: a principal administering more orgs than
// MaxLookupResults still filters a small batch inside ONE of them, and no
// Lookup* store method is called at all.
func TestCheckBatchIsIndependentOfLookupBudget(t *testing.T) {
	tuples := containerTuples(3)
	for i := 2; i <= 5; i++ {
		tuples = append(tuples, tuple("org", fmt.Sprintf("o%d", i), "admin", "user", "u1", ""))
	}
	store := &fakeStore{tuples: tuples}
	svc := newLimitedService(t, store, testSchema(), EvaluationLimits{MaxLookupResults: 2})

	allowed, err := svc.FilterAuthorized(context.Background(),
		PrincipalRef{Type: "user", ID: "u1"}, "view", "post", []string{"p0", "p1", "p2"})
	if err != nil {
		t.Fatalf("FilterAuthorized: %v", err)
	}
	if len(allowed) != 3 {
		t.Fatalf("allowed = %v, want all three posts", allowed)
	}
	if store.lookupCalls != 0 {
		t.Fatalf("store Lookup* calls = %d, want 0 (a bounded container listing must not enumerate)", store.lookupCalls)
	}
}

// TestCheckBatchSharesContainerReads is the B4 optimization itself: over a
// 1-container / N-item batch the container's two reads happen ONCE, while each
// distinct candidate still reads its own Through edge.
func TestCheckBatchSharesContainerReads(t *testing.T) {
	const n = 50

	store := &fakeStore{tuples: containerTuples(n)}
	svc := newLimitedService(t, store, chainSchema(), EvaluationLimits{})

	reqs := make([]CheckRequest, n)
	for i := range reqs {
		reqs[i] = CheckRequest{
			Principal:  PrincipalRef{Type: "user", ID: "u1"},
			Permission: "view",
			Resource:   Resource{Type: "post", ID: fmt.Sprintf("p%d", i)},
		}
	}
	results, err := svc.CheckBatch(context.Background(), reqs)
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	for i, res := range results {
		if !res.Allowed {
			t.Fatalf("result[%d] denied, want allowed via the container", i)
		}
	}

	if got := store.calls(store.targetsCalls, targetsCallKey("org", "o1", "tenant")); got != 1 {
		t.Fatalf("GetRelationTargets(org:o1#tenant) called %d times, want 1 (shared container read)", got)
	}
	if got := store.calls(store.directCalls, directCallKey("org", "o1", "admin", "user", "u1")); got != 1 {
		t.Fatalf("CheckRelationWithGroupExpansion(org:o1#admin) called %d times, want 1 (shared container check)", got)
	}
	total := 0
	for i := 0; i < n; i++ {
		key := targetsCallKey("post", fmt.Sprintf("p%d", i), "org")
		got := store.calls(store.targetsCalls, key)
		if got != 1 {
			t.Fatalf("GetRelationTargets(%s) called %d times, want 1", key, got)
		}
		total += got
	}
	if total != n {
		t.Fatalf("candidate Through reads = %d, want %d (one per distinct candidate)", total, n)
	}
}

// TestMemoReaderReturnsIsolatedCopies proves a caller cannot mutate memo state
// through the slice it was handed, on the first call or on a hit.
func TestMemoReaderReturnsIsolatedCopies(t *testing.T) {
	inner := &stubReader{targets: []relationship.RelationTarget{{Type: "org", ID: "o1"}}}
	m := newMemoReader(inner)
	ctx := context.Background()

	first, err := m.GetRelationTargets(ctx, "post", "p1", "org")
	if err != nil {
		t.Fatalf("GetRelationTargets: %v", err)
	}
	first[0].ID = "mutated"

	second, err := m.GetRelationTargets(ctx, "post", "p1", "org")
	if err != nil {
		t.Fatalf("GetRelationTargets (hit): %v", err)
	}
	if second[0].ID != "o1" {
		t.Fatalf("memo hit = %q, want o1 (caller mutation leaked into memo state)", second[0].ID)
	}
	second[0].ID = "mutated again"
	third, err := m.GetRelationTargets(ctx, "post", "p1", "org")
	if err != nil {
		t.Fatalf("GetRelationTargets (second hit): %v", err)
	}
	if third[0].ID != "o1" {
		t.Fatalf("memo hit = %q, want o1 (a returned copy must not alias memo state)", third[0].ID)
	}
	if inner.targetsCalls != 1 {
		t.Fatalf("inner reader called %d times, want 1", inner.targetsCalls)
	}
}

// TestMemoReaderDoesNotCacheErrors proves a failed read is retried: only
// successful results are memoized.
func TestMemoReaderDoesNotCacheErrors(t *testing.T) {
	inner := &stubReader{
		targets:   []relationship.RelationTarget{{Type: "org", ID: "o1"}},
		allowed:   true,
		failFirst: true,
	}
	m := newMemoReader(inner)
	ctx := context.Background()

	if _, err := m.GetRelationTargets(ctx, "post", "p1", "org"); !errors.Is(err, errStubRead) {
		t.Fatalf("GetRelationTargets err = %v, want errStubRead", err)
	}
	targets, err := m.GetRelationTargets(ctx, "post", "p1", "org")
	if err != nil {
		t.Fatalf("GetRelationTargets retry: %v", err)
	}
	if len(targets) != 1 || inner.targetsCalls != 2 {
		t.Fatalf("retry reached inner=%d calls with %d targets, want 2 calls and 1 target", inner.targetsCalls, len(targets))
	}

	if _, err := m.CheckRelationWithGroupExpansion(ctx, "org", "o1", "admin", "user", "u1", 10); !errors.Is(err, errStubRead) {
		t.Fatalf("CheckRelationWithGroupExpansion err = %v, want errStubRead", err)
	}
	allowed, err := m.CheckRelationWithGroupExpansion(ctx, "org", "o1", "admin", "user", "u1", 10)
	if err != nil {
		t.Fatalf("CheckRelationWithGroupExpansion retry: %v", err)
	}
	if !allowed || inner.directCalls != 2 {
		t.Fatalf("retry reached inner=%d calls, allowed=%v; want 2 calls and true", inner.directCalls, allowed)
	}
}

// TestMemoReaderSkipsCacheAfterCancellation proves a result observed after the
// caller canceled is never retained: the next batch item re-reads.
func TestMemoReaderSkipsCacheAfterCancellation(t *testing.T) {
	inner := &stubReader{targets: []relationship.RelationTarget{{Type: "org", ID: "o1"}}, allowed: true}
	m := newMemoReader(inner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := m.GetRelationTargets(ctx, "post", "p1", "org"); err != nil {
		t.Fatalf("GetRelationTargets: %v", err)
	}
	if _, err := m.CheckRelationWithGroupExpansion(ctx, "org", "o1", "admin", "user", "u1", 10); err != nil {
		t.Fatalf("CheckRelationWithGroupExpansion: %v", err)
	}
	if _, err := m.GetRelationTargets(context.Background(), "post", "p1", "org"); err != nil {
		t.Fatalf("GetRelationTargets (post-cancel): %v", err)
	}
	if _, err := m.CheckRelationWithGroupExpansion(context.Background(), "org", "o1", "admin", "user", "u1", 10); err != nil {
		t.Fatalf("CheckRelationWithGroupExpansion (post-cancel): %v", err)
	}
	if inner.targetsCalls != 2 || inner.directCalls != 2 {
		t.Fatalf("inner calls = (%d targets, %d direct), want 2 and 2 (canceled reads must not be cached)",
			inner.targetsCalls, inner.directCalls)
	}
}

// BenchmarkCheckBatchThrough measures the B4 shape: one container, 500 items,
// every item reached through the shared container. schema is testSchema (the
// container's own decision is one direct check) or chainSchema (the container's
// own decision also reads a relation edge, the shape the memo actually pays off
// on — with a real store every shared read is a round trip).
func BenchmarkCheckBatchThrough(b *testing.B) {
	b.Run("container-direct", func(b *testing.B) { benchmarkCheckBatchThrough(b, testSchema()) })
	b.Run("container-through", func(b *testing.B) { benchmarkCheckBatchThrough(b, chainSchema()) })
}

func benchmarkCheckBatchThrough(b *testing.B, schema Schema) {
	const n = 500

	store := &fakeStore{tuples: containerTuples(n)}
	svc, err := NewService(store, schema, Config{IDs: cryptids.IDGenerator{}})
	if err != nil {
		b.Fatalf("NewService: %v", err)
	}
	reqs := make([]CheckRequest, n)
	for i := range reqs {
		reqs[i] = CheckRequest{
			Principal:  PrincipalRef{Type: "user", ID: "u1"},
			Permission: "view",
			Resource:   Resource{Type: "post", ID: fmt.Sprintf("p%d", i)},
		}
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.CheckBatch(ctx, reqs); err != nil {
			b.Fatalf("CheckBatch: %v", err)
		}
	}
}
