package decisions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

type tupleFixture struct {
	mu                                       sync.Mutex
	facts                                    map[tuples.Tuple]bool
	owner                                    *tupleFixture
	snapshotCalls, containsCalls, batchCalls int
	fail                                     error
	completion                               error
	afterRead                                func()
}

func (f *tupleFixture) root() *tupleFixture {
	if f.owner != nil {
		return f.owner
	}
	return f
}
func (f *tupleFixture) Contains(ctx context.Context, t tuples.Tuple) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	root := f.root()
	root.mu.Lock()
	root.containsCalls++
	err := root.fail
	hook := root.afterRead
	root.afterRead = nil
	root.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return false, err
	}
	return f.facts[t], nil
}
func (f *tupleFixture) ContainsMany(ctx context.Context, ts []tuples.Tuple) ([]bool, error) {
	root := f.root()
	root.mu.Lock()
	root.batchCalls++
	err := root.fail
	root.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]bool, len(ts))
	for i, t := range ts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[i] = f.facts[t]
	}
	return out, nil
}
func (f *tupleFixture) ReadSets(ctx context.Context, keys []tuples.SetKey, max int) ([][]tuples.Tuple, error) {
	out := make([][]tuples.Tuple, len(keys))
	count := 0
	for i, k := range keys {
		for fact := range f.facts {
			if (k.Reverse && fact.Subject == k.Subject) || (!k.Reverse && fact.Scope == k.Scope && fact.Relation == k.Relation) {
				out[i] = append(out[i], fact)
				count++
			}
		}
		slices.SortFunc(out[i], tuples.Compare)
	}
	if max > 0 && count > max {
		return nil, tuples.ErrReadLimit
	}
	return out, ctx.Err()
}
func (f *tupleFixture) Lookup(ctx context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	out := []tuples.Tuple{}
	for t := range f.facts {
		if q.Matches(t) && (q.After == nil || tuples.Compare(t, *q.After) > 0) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, tuples.Compare)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, ctx.Err()
}
func (f *tupleFixture) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	f.mu.Lock()
	facts := map[tuples.Tuple]bool{}
	for t, v := range f.facts {
		facts[t] = v
	}
	f.snapshotCalls++
	completion := f.completion
	f.mu.Unlock()
	if err := fn(ctx, &tupleFixture{facts: facts, owner: f}); err != nil {
		return err
	}
	return completion
}

var expressionPrincipal = authmodel.PrincipalRef{Type: "user", ID: "alice"}

func exact(scope tuples.Scope, label string) tuples.Tuple {
	return tuples.Tuple{Scope: scope, Relation: label, Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
}
func expressionService(t testing.TB, facts ...tuples.Tuple) (*Service, *tupleFixture) {
	t.Helper()
	f := &tupleFixture{facts: map[tuples.Tuple]bool{}}
	for _, fact := range facts {
		f.facts[fact] = true
	}
	s, err := NewService(f)
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}
func permissionModel(expr Expression) Model {
	return Model{ResourceTypes: map[string]ResourceTypeDef{"doc": {Permissions: map[string]Expression{"view": expr}}}}
}

func TestExactExpressionsAreConcreteAndScoped(t *testing.T) {
	resource := authmodel.Resource{Type: "doc", ID: "one"}
	global := exact(tuples.Global(), "editor")
	scoped := exact(tuples.On("doc", "one"), "viewer")
	s, f := expressionService(t, global, scoped, tuples.Tuple{Scope: tuples.On("doc", "one"), Relation: "group_role", Subject: tuples.SubjectRef{Type: "group", ID: "eng", Relation: "member"}})
	for _, tc := range []struct {
		expr Expression
		want bool
	}{{Role("editor"), true}, {RoleIn("editor", resource), false}, {RoleIn("viewer", resource), true}, {Role("viewer"), false}, {RoleIn("group_role", resource), false}, {Any(RoleIn("editor", resource), Role("editor")), true}, {All(Role("editor"), RoleIn("viewer", resource)), true}} {
		got, err := s.Evaluate(t.Context(), expressionPrincipal, tc.expr)
		if err != nil || got.Allowed != tc.want {
			t.Fatalf("%+v = %+v/%v", tc.expr, got, err)
		}
	}
	if f.snapshotCalls != 7 {
		t.Fatalf("snapshots=%d", f.snapshotCalls)
	}
}
func TestExpressionValidationPrecedesAllReads(t *testing.T) {
	s, f := expressionService(t, exact(tuples.Global(), "admin"))
	for _, expr := range []Expression{Any(), All(), Expression{}, Any(Role("admin"), All()), All(Role("missing"), Role("")), RoleIn("editor"), RoleIn("editor", authmodel.Resource{}, authmodel.Resource{}), Expression{RoleName: "admin", Relation: "viewer"}} {
		if _, err := s.Evaluate(t.Context(), expressionPrincipal, expr); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("malformed expression accepted: %+v/%v", expr, err)
		}
	}
	if f.snapshotCalls != 0 || f.containsCalls != 0 {
		t.Fatalf("invalid input read authority: %+v", f)
	}
}
func TestExpressionShortCircuitAndErrorOrder(t *testing.T) {
	s, f := expressionService(t, exact(tuples.Global(), "admin"))
	failure := errors.New("read failed")
	f.afterRead = func() { f.mu.Lock(); f.fail = failure; f.mu.Unlock() }
	got, err := s.Evaluate(t.Context(), expressionPrincipal, Any(Role("admin"), Role("later")))
	if err != nil || !got.Allowed || f.containsCalls != 1 {
		t.Fatalf("short circuit=%v/%v", got, err)
	}
	got, err = s.Evaluate(t.Context(), expressionPrincipal, Any(Role("later"), Role("admin")))
	if !errors.Is(err, failure) || got != (authmodel.CheckResult{}) {
		t.Fatalf("encountered error=%v/%v", got, err)
	}
}
func TestAllUsesOneSnapshotAcrossAtomicSwap(t *testing.T) {
	a, b := exact(tuples.Global(), "a"), exact(tuples.Global(), "b")
	s, f := expressionService(t, a)
	f.afterRead = func() { f.mu.Lock(); delete(f.facts, a); f.facts[b] = true; f.mu.Unlock() }
	got, err := s.Evaluate(t.Context(), expressionPrincipal, All(Role("a"), Role("b")))
	if err != nil || got.Allowed || f.snapshotCalls != 1 {
		t.Fatalf("impossible mixed-state conjunction=%v/%v snapshots=%d", got, err, f.snapshotCalls)
	}
}
func TestExpressionSnapshotCompletionDiscardsGrant(t *testing.T) {
	s, f := expressionService(t, exact(tuples.Global(), "admin"))
	f.completion = errors.New("completion failed")
	got, err := s.Evaluate(t.Context(), expressionPrincipal, Role("admin"))
	if !errors.Is(err, f.completion) || got != (authmodel.CheckResult{}) {
		t.Fatalf("provisional result=%v/%v", got, err)
	}
}
func TestUnifiedNamedExpressionsBatchExplainAndLookup(t *testing.T) {
	_, f := expressionService(t, exact(tuples.Global(), "employee"), exact(tuples.On("doc", "one"), "editor"), exact(tuples.On("doc", "two"), "viewer"))
	policy := permissionModel(All(Role("employee"), Any(RoleIn("editor"), RoleIn("viewer"))))
	s, err := NewService(f, WithModel(policy))
	if err != nil {
		t.Fatal(err)
	}
	requests := []authmodel.CheckRequest{}
	for _, id := range []string{"one", "two", "missing", "one"} {
		requests = append(requests, authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: id}})
	}
	batch, err := s.CheckBatch(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCalls != 3 || f.containsCalls != 0 || f.snapshotCalls != 1 {
		t.Fatalf("bulk role reads batch=%d single=%d snapshots=%d", f.batchCalls, f.containsCalls, f.snapshotCalls)
	}
	for i, req := range requests {
		got, trace, err := s.CheckExplain(t.Context(), req)
		if err != nil || got != batch[i] || trace.Decision != got.ReasonCode {
			t.Fatalf("batch/explain mismatch %d: %v/%v/%v", i, got, trace, err)
		}
	}
	set, err := s.LookupResources(t.Context(), expressionPrincipal, "view", "doc")
	if err != nil || !reflect.DeepEqual(set.IDs, []string{"one", "two"}) || set.Unrestricted {
		t.Fatalf("lookup=%v/%v", set, err)
	}
}
func TestExplicitGlobalExpressionLookupSetAlgebra(t *testing.T) {
	_, f := expressionService(t, exact(tuples.Global(), "admin"), exact(tuples.On("doc", "one"), "editor"))
	for _, tc := range []struct {
		expr         Expression
		unrestricted bool
		ids          []string
	}{{Any(Role("admin"), RoleIn("editor")), true, []string{}}, {All(Role("admin"), RoleIn("editor")), false, []string{"one"}}, {All(Role("missing"), RoleIn("editor")), false, []string{}}} {
		s, err := NewService(f, WithModel(permissionModel(tc.expr)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.LookupResources(t.Context(), expressionPrincipal, "view", "doc")
		if err != nil || got.Unrestricted != tc.unrestricted || !reflect.DeepEqual(got.IDs, tc.ids) {
			t.Fatalf("lookup=%v/%v", got, err)
		}
	}
}
func TestCompileValidatesNamedCyclesAndCopiesExpressions(t *testing.T) {
	bad := Model{ResourceTypes: map[string]ResourceTypeDef{"doc": {Permissions: map[string]Expression{"view": Permission("view")}}}}
	if _, err := Compile(bad); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("named cycle=%v", err)
	}
	model := permissionModel(Any(Role("admin"), RoleIn("editor")))
	compiled, err := Compile(model)
	if err != nil {
		t.Fatal(err)
	}
	before := compiled.Digest()
	model.ResourceTypes["doc"].Permissions["view"].AnyOf[0] = Role("changed")
	if compiled.Digest() != before || compiled.expression("doc", "view").AnyOf[0].RoleName != "admin" {
		t.Fatal("mutable compiled expression")
	}
}
func BenchmarkRoleBatchReads(b *testing.B) {
	for _, n := range []int{1, 20, 128} {
		b.Run(fmt.Sprintf("requests_%d", n), func(b *testing.B) {
			_, f := expressionService(b, exact(tuples.Global(), "admin"))
			s, err := NewService(f, WithModel(permissionModel(Any(RoleIn("editor"), Role("admin")))))
			if err != nil {
				b.Fatal(err)
			}
			requests := make([]authmodel.CheckRequest, n)
			for i := range requests {
				requests[i] = authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: fmt.Sprint(i)}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := s.CheckBatch(context.Background(), requests); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestSnapshotOwnsNestedExpressionsAndFixedResources(t *testing.T) {
	fixed := authmodel.Resource{Type: "organization", ID: "one"}
	model := Model{ResourceTypes: map[string]ResourceTypeDef{"doc": {Permissions: map[string]Expression{"read": All(RoleIn("editor"), Any(Role("admin"), RoleIn("member", fixed)))}}}}
	compiled, err := Compile(model)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := compiled.Snapshot()
	expression, ok := snapshot.Expression("doc", "read")
	if !ok {
		t.Fatal("permission missing from snapshot")
	}
	expression.AllOf[0].RoleName = "attacker"
	expression.AllOf[1].AnyOf[1].Resource.ID = "changed"
	again, _ := snapshot.Expression("doc", "read")
	if again.AllOf[0].RoleName != "editor" || again.AllOf[1].AnyOf[1].Resource.ID != "one" {
		t.Fatal("returned expression aliases snapshot")
	}
	snapshot.resourceTypes["doc"].permissions["read"].AllOf[1].AnyOf[0].RoleName = "changed"
	fresh, _ := compiled.Snapshot().Expression("doc", "read")
	if fresh.AllOf[1].AnyOf[0].RoleName != "admin" {
		t.Fatal("snapshot aliases compiled expression")
	}
	if snapshot.Checks("doc", "read") != nil {
		t.Fatal("conjunction was advertised as a flat graph policy")
	}
}

func TestExactDisjunctionPagesBeyondTotalResultBudget(t *testing.T) {
	_, f := expressionService(t)
	for i := 0; i < 7; i++ {
		f.facts[exact(tuples.On("doc", fmt.Sprintf("%02d", i)), "editor")] = true
		if i%2 == 0 {
			f.facts[exact(tuples.On("doc", fmt.Sprintf("%02d", i)), "viewer")] = true
		}
	}
	s, err := NewService(f, WithModel(permissionModel(Any(RoleIn("editor"), RoleIn("viewer")))), WithLimits(authmodel.EvaluationLimits{MaxLookupResults: 2}))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	cursor := ""
	for range 5 {
		page, err := s.LookupResourceIDPage(t.Context(), ResourceIDPageRequest{Principal: expressionPrincipal, Permission: "view", ResourceType: "doc", Limit: 2, After: cursor})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, page.IDs...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if !reflect.DeepEqual(ids, []string{"00", "01", "02", "03", "04", "05", "06"}) {
		t.Fatalf("paged exact disjunction: %v", ids)
	}
	if _, err := s.LookupAllResourceIDs(t.Context(), expressionPrincipal, "view", "doc"); !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("unpaged total remains bounded: %v", err)
	}
}
