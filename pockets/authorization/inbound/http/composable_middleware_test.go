package authorizationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func guardFact(scope tuples.Scope, label, subjectType, subjectID, subjectRelation string) tuples.Tuple {
	return tuples.Tuple{Scope: scope, Relation: label, Subject: tuples.SubjectRef{Type: subjectType, ID: subjectID, Relation: subjectRelation}}
}

func guardModel() decisions.Model {
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"group": {Relations: map[string]decisions.RelationDef{
			"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
		}},
		"document": {
			Relations: map[string]decisions.RelationDef{
				"editor": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			},
			Permissions: map[string]decisions.Expression{"edit": decisions.Direct("editor")},
		},
	}}
}

func guardAdapter(t testing.TB, reader tuples.Reader, options ...decisions.Option) *Adapter {
	t.Helper()
	service, err := decisions.NewService(reader, options...)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(Services{Decisions: service})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func seedGuard(t testing.TB, store *memory.Tuples, facts ...tuples.Tuple) {
	t.Helper()
	if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
}

func guardRequest(principal string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/documents/one", nil)
	r.SetPathValue("documentID", "one")
	if principal != "" {
		r = r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: "user", ID: principal}))
	}
	return r
}

func serveGuard(gate web.Middleware, r *http.Request) (*httptest.ResponseRecorder, int) {
	calls := 0
	recorder := httptest.NewRecorder()
	gate(http.HandlerFunc(func(w http.ResponseWriter, actual *http.Request) {
		calls++
		if actual != r {
			panic("guard changed the request passed to next")
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, r)
	return recorder, calls
}

func TestComposableGuardMixedPolicy(t *testing.T) {
	store := memory.NewTuples()
	seedGuard(t, store,
		guardFact(tuples.Global(), "admin", "user", "admin", ""),
		guardFact(tuples.On("organization", "acme"), "member", "user", "alice", ""),
		guardFact(tuples.On("document", "one"), "editor", "group", "editors", "member"),
		guardFact(tuples.On("group", "editors"), "member", "user", "alice", ""),
	)
	adapter := guardAdapter(t, store, decisions.WithModel(guardModel()))
	document := Path("document", "documentID")
	gate := adapter.Require(Any(
		HasRole("admin", Global()),
		All(HasRole("member", Fixed("organization", "acme")),
			HasRelationship("editor", document), Can("edit", document)),
	))
	for _, tc := range []struct {
		principal string
		status    int
	}{{"", 401}, {"outsider", 403}, {"alice", 204}, {"admin", 204}} {
		t.Run(tc.principal, func(t *testing.T) {
			rec, calls := serveGuard(gate, guardRequest(tc.principal))
			if rec.Code != tc.status || calls != boolInt(tc.status == 204) {
				t.Fatalf("status=%d next=%d body=%s", rec.Code, calls, rec.Body)
			}
		})
	}
	// The same userset fact satisfies the relationship; exact role membership
	// still requires a concrete tuple for the current principal.
	rec, _ := serveGuard(adapter.Require(HasRole("editor", document)), guardRequest("alice"))
	if rec.Code != 403 {
		t.Fatalf("userset expanded through exact HasRole: %d", rec.Code)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestComposableGuardModelFreeAndScope(t *testing.T) {
	store := memory.NewTuples()
	seedGuard(t, store,
		guardFact(tuples.Global(), "employee", "user", "alice", ""),
		guardFact(tuples.On("document", "one"), "editor", "user", "alice", ""),
	)
	adapter := guardAdapter(t, store)
	for _, tc := range []struct {
		name   string
		policy Predicate
		status int
	}{
		{"both present", All(HasRole("employee", Global()), HasRole("editor", Fixed("document", "one"))), 204},
		{"one missing", All(HasRole("employee", Global()), HasRole("reviewer", Fixed("document", "one"))), 403},
		{"either present", Any(HasRole("reviewer", Global()), HasRole("editor", Fixed("document", "one"))), 204},
		{"global is exact", HasRole("employee", Fixed("document", "one")), 403},
		{"resource is exact", HasRole("editor", Fixed("document", "two")), 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := serveGuard(adapter.Require(tc.policy), guardRequest("alice"))
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
		})
	}
}

func TestComposableGuardLazyInputs(t *testing.T) {
	store := memory.NewTuples()
	seedGuard(t, store, guardFact(tuples.Global(), "admin", "user", "alice", ""))
	adapter := guardAdapter(t, store)
	var calls atomic.Int64
	broken := Resource("document", func(*http.Request) (authmodel.Resource, error) {
		calls.Add(1)
		return authmodel.Resource{}, errors.New("private resolver error")
	})
	for _, tc := range []struct {
		name      string
		policy    Predicate
		principal string
		status    int
		resolved  int64
	}{
		{"anonymous", HasRole("owner", broken), "", 401, 0},
		{"Any stops true", Any(HasRole("admin", Global()), HasRole("owner", broken)), "alice", 204, 0},
		{"All stops false", All(HasRole("missing", Global()), HasRole("owner", broken)), "alice", 403, 0},
		{"error aborts Any", Any(HasRole("owner", broken), HasRole("admin", Global())), "alice", 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls.Store(0)
			gate := adapter.Require(tc.policy)
			if calls.Load() != 0 {
				t.Fatal("mount invoked resolver")
			}
			rec, _ := serveGuard(gate, guardRequest(tc.principal))
			if rec.Code != tc.status || calls.Load() != tc.resolved || strings.Contains(rec.Body.String(), "private") {
				t.Fatalf("status=%d resolver=%d body=%s", rec.Code, calls.Load(), rec.Body)
			}
		})
	}
}

func TestComposableGuardResolverFailures(t *testing.T) {
	store := memory.NewTuples()
	seedGuard(t, store, guardFact(tuples.Global(), "admin", "user", "alice", ""))
	adapter := guardAdapter(t, store)
	for _, tc := range []struct {
		name     string
		resource authmodel.Resource
		err      error
		status   int
	}{
		{"inapplicable", authmodel.Resource{}, fmt.Errorf("no document: %w", ErrAlternativeNotApplicable), 204},
		{"ordinary failure", authmodel.Resource{}, errors.New("resolver failed"), 500},
		{"wrong type", authmodel.Resource{Type: "organization", ID: "one"}, nil, 500},
		{"empty id", authmodel.Resource{Type: "document"}, nil, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := Resource("document", func(*http.Request) (authmodel.Resource, error) { return tc.resource, tc.err })
			rec, _ := serveGuard(adapter.Require(Any(HasRole("editor", target), HasRole("admin", Global()))), guardRequest("alice"))
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
		})
	}
	absent := Resource("document", func(*http.Request) (authmodel.Resource, error) {
		return authmodel.Resource{}, ErrAlternativeNotApplicable
	})
	rec, _ := serveGuard(adapter.Require(All(HasRole("admin", Global()), HasRole("editor", absent))), guardRequest("alice"))
	if rec.Code != 403 {
		t.Fatalf("All accepted absent input: %d", rec.Code)
	}
}

func TestComposableGuardSharedInputAndConcurrency(t *testing.T) {
	store := memory.NewTuples()
	seedGuard(t, store,
		guardFact(tuples.On("document", "one"), "editor", "user", "alice", ""),
		guardFact(tuples.On("document", "one"), "reviewer", "user", "alice", ""),
	)
	adapter := guardAdapter(t, store)
	var calls atomic.Int64
	target := Resource("document", func(r *http.Request) (authmodel.Resource, error) {
		calls.Add(1)
		return authmodel.Resource{Type: "document", ID: r.PathValue("documentID")}, nil
	})
	children := []Predicate{HasRole("editor", target), HasRole("reviewer", target)}
	policy := All(children...)
	children[0] = HasRole("missing", Global())
	gate := adapter.Require(policy)
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Go(func() {
			r := guardRequest("alice")
			want := 204
			if i%2 == 1 {
				r.SetPathValue("documentID", "two")
				want = 403
			}
			rec, next := serveGuard(gate, r)
			if rec.Code != want || next != boolInt(want == 204) {
				t.Errorf("status=%d next=%d", rec.Code, next)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 24 {
		t.Fatalf("shared target resolved %d times for 24 requests", calls.Load())
	}
	// Independently constructed inputs of the same type remain independent.
	var independent int
	resolver := func(*http.Request) (authmodel.Resource, error) {
		independent++
		return authmodel.Resource{Type: "document", ID: "one"}, nil
	}
	rec, _ := serveGuard(adapter.Require(All(
		HasRole("editor", Resource("document", resolver)),
		HasRole("reviewer", Resource("document", resolver)),
	)), guardRequest("alice"))
	if rec.Code != 204 || independent != 2 {
		t.Fatalf("independent inputs: status=%d resolved=%d", rec.Code, independent)
	}
}

func TestComposableGuardMountValidation(t *testing.T) {
	store := memory.NewTuples()
	adapter := guardAdapter(t, store, decisions.WithModel(guardModel()))
	var resolves int
	validTarget := Resource("document", func(*http.Request) (authmodel.Resource, error) {
		resolves++
		return authmodel.Resource{Type: "document", ID: "one"}, nil
	})
	deep := HasRole("admin", Global())
	for range decisions.MaxExpressionDepth + 1 {
		deep = All(deep)
	}
	wide := make([]Predicate, decisions.MaxExpressionNodes)
	for i := range wide {
		wide[i] = HasRole("admin", Global())
	}
	for name, policy := range map[string]Predicate{
		"zero": Predicate{}, "empty All": All(), "empty Any": Any(),
		"empty label": HasRole("", Global()), "zero target": HasRole("editor", Target{}),
		"global graph": HasRelationship("editor", Global()), "global permission": Can("edit", Global()),
		"nil resolver":          HasRole("editor", Resource("document", nil)),
		"empty type":            HasRole("editor", Resource("", validTarget.value.resolve)),
		"bad fixed":             HasRole("editor", Fixed("document", "")),
		"empty path":            HasRole("editor", Path("document", "")),
		"undeclared relation":   HasRelationship("unknown", validTarget),
		"undeclared permission": Can("unknown", validTarget),
		"invalid skipped leaf":  Any(HasRole("admin", Global()), Can("unknown", validTarget)),
		"deep":                  deep, "wide": All(wide...),
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid guard mounted")
				}
			}()
			adapter.Require(policy)
		})
	}
	if resolves != 0 {
		t.Fatalf("mount called resolver %d times", resolves)
	}
	for name, mount := range map[string]func(){
		"nil adapter":         func() { (*Adapter)(nil).Require(HasRole("admin", Global())) },
		"no decisions":        func() { (&Adapter{}).Require(HasRole("admin", Global())) },
		"typed nil decisions": func() { (&Adapter{decisions: (*narrowDecisions)(nil)}).Require(HasRole("admin", Global())) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("missing capability accepted")
				}
			}()
			mount()
		})
	}
}

func TestComposableGuardEvaluationFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{{"store", errors.New("store failed"), 500}, {"budget", authmodel.ErrEvaluationLimit, 503}, {"store inapplicability is an error", decisions.ErrResourceNotApplicable, 500}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &failedFacts{Tuples: memory.NewTuples(), err: tc.err}
			adapter := guardAdapter(t, store)
			rec, next := serveGuard(adapter.Require(Any(HasRole("a", Global()), HasRole("b", Global()))), guardRequest("alice"))
			if rec.Code != tc.status || next != 0 {
				t.Fatalf("status=%d next=%d", rec.Code, next)
			}
		})
	}
	store := memory.NewTuples()
	seedGuard(t, store, guardFact(tuples.Global(), "admin", "user", "alice", ""))
	adapter := guardAdapter(t, store)
	r := guardRequest("alice")
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	target := Resource("document", func(*http.Request) (authmodel.Resource, error) {
		cancel()
		return authmodel.Resource{}, ErrAlternativeNotApplicable
	})
	rec, next := serveGuard(adapter.Require(Any(HasRole("editor", target), HasRole("admin", Global()))), r)
	if rec.Code != 500 || next != 0 {
		t.Fatalf("cancellation admitted request: %d next=%d", rec.Code, next)
	}
}

func TestComposableGuardOneSnapshotAcrossResourceSwap(t *testing.T) {
	store := memory.NewTuples()
	old := guardFact(tuples.On("organization", "acme"), "member", "user", "alice", "")
	newFact := guardFact(tuples.On("document", "one"), "editor", "user", "alice", "")
	seedGuard(t, store, old)
	adapter := guardAdapter(t, store, decisions.WithModel(guardModel()))
	document := Resource("document", func(r *http.Request) (authmodel.Resource, error) {
		err := store.ApplyTuples(r.Context(), tuples.Changes{Remove: []tuples.Tuple{old}, Add: []tuples.Tuple{newFact}})
		return authmodel.Resource{Type: "document", ID: "one"}, err
	})
	gate := adapter.Require(All(HasRole("member", Fixed("organization", "acme")), Can("edit", document)))
	rec, next := serveGuard(gate, guardRequest("alice"))
	if rec.Code != 403 || next != 0 {
		t.Fatalf("granted from facts that never coexisted: status=%d next=%d", rec.Code, next)
	}
	if present, err := store.Contains(t.Context(), newFact); err != nil || !present {
		t.Fatalf("concurrent mutation did not run: present=%t err=%v", present, err)
	}
}

func TestComposableGuardSharedBudget(t *testing.T) {
	store := memory.NewTuples()
	seedGuard(t, store, guardFact(tuples.Global(), "admin", "user", "alice", ""))
	adapter := guardAdapter(t, store, decisions.WithLimits(authmodel.EvaluationLimits{MaxEvaluationSteps: 2}))
	rec, next := serveGuard(adapter.Require(All(HasRole("admin", Global()), HasRole("admin", Global()))), guardRequest("alice"))
	if rec.Code != 503 || next != 0 {
		t.Fatalf("separate leaf budgets: status=%d next=%d", rec.Code, next)
	}
}
