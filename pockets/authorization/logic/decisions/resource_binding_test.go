package decisions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestResourceBindingPreservesPriorModelDigest(t *testing.T) {
	c, err := Compile(orgProjectSchema())
	if err != nil {
		t.Fatal(err)
	}
	const beforeResourceBindings = "43faa41f34a17408a248b0fa324a6f829c6f0b9325e06241a03c0ac3c1715c88"
	if got := c.Digest(); got != beforeResourceBindings {
		t.Fatalf("model digest = %q, want %q", got, beforeResourceBindings)
	}
}

func bindingModel() Model {
	return Model{ResourceTypes: map[string]ResourceTypeDef{
		"group": {Relations: map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}}},
		"org":   {Relations: map[string]RelationDef{"member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]Expression{"manage": Direct("member")}},
		"doc": {Relations: map[string]RelationDef{
			"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
			"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
		}, Permissions: map[string]Expression{"read": Direct("viewer"), "viewer": Direct("viewer"), "edit": All(RoleIn("editor"), Through("parent", "manage"))}},
	}}
}
func bindingService(t *testing.T, facts ...tuples.Tuple) (*Service, *tupleFixture) {
	t.Helper()
	_, f := expressionService(t, facts...)
	s, err := NewService(f, WithModel(bindingModel()))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}
func TestResourceBindingMixedFixedLeavesAndUsersets(t *testing.T) {
	doc := authmodel.Resource{Type: "doc", ID: "one"}
	org := authmodel.Resource{Type: "org", ID: "tenant"}
	facts := []tuples.Tuple{
		exact(tuples.Global(), "employee"), exact(tuples.On("org", "tenant"), "member"), exact(tuples.On("doc", "one"), "editor"),
		exact(tuples.On("group", "eng"), "member"),
		{Scope: tuples.On("doc", "one"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "eng", Relation: "member"}},
		{Scope: tuples.On("doc", "one"), Relation: "parent", Subject: tuples.SubjectRef{Type: "org", ID: "tenant"}},
	}
	s, f := bindingService(t, facts...)
	expr := All(On(org, RoleIn("member")), On(doc, Direct("viewer")), On(doc, Permission("edit")), On(doc, Through("parent", "manage")), Role("employee"))
	got, err := s.Evaluate(t.Context(), expressionPrincipal, expr)
	if err != nil || !got.Allowed || f.snapshotCalls != 1 {
		t.Fatalf("mixed fixed=%+v/%v snapshots=%d", got, err, f.snapshotCalls)
	}
	exactResult, err := s.Evaluate(t.Context(), expressionPrincipal, On(doc, RoleIn("viewer")))
	if err != nil || exactResult.Allowed {
		t.Fatalf("exact expanded userset=%+v/%v", exactResult, err)
	}
	got, err = s.EvaluateExpressionWith(t.Context(), f, expressionPrincipal, expr)
	if err != nil || !got.Allowed || f.snapshotCalls != 2 {
		t.Fatalf("bound fixed=%+v/%v snapshots=%d", got, err, f.snapshotCalls)
	}
	// The fixed graph target must not become the implicit scope of its sibling.
	got, err = s.Evaluate(t.Context(), expressionPrincipal, All(On(doc, Direct("viewer")), Role("editor")))
	if err != nil || got.Allowed {
		t.Fatalf("binding leaked to sibling=%+v/%v", got, err)
	}
}
func TestResourceBindingMixedSnapshotAndCompletion(t *testing.T) {
	org := authmodel.Resource{Type: "org", ID: "tenant"}
	doc := authmodel.Resource{Type: "doc", ID: "one"}
	membership := exact(tuples.On(org.Type, org.ID), "member")
	viewer := exact(tuples.On(doc.Type, doc.ID), "viewer")
	s, f := bindingService(t, membership)
	f.afterRead = func() { f.mu.Lock(); delete(f.facts, membership); f.facts[viewer] = true; f.mu.Unlock() }
	expr := All(On(org, RoleIn("member")), On(doc, Direct("viewer")), On(doc, Permission("read")))
	got, err := s.Evaluate(t.Context(), expressionPrincipal, expr)
	if err != nil || got.Allowed || f.snapshotCalls != 1 {
		t.Fatalf("mixed states allowed=%+v/%v", got, err)
	}
	f.facts[membership] = true
	f.completion = errors.New("snapshot completion failed")
	got, err = s.Evaluate(t.Context(), expressionPrincipal, expr)
	if !errors.Is(err, f.completion) || got != (authmodel.CheckResult{}) {
		t.Fatalf("completion leaked allow=%+v/%v", got, err)
	}
}
func TestResourceBindingValidationBeforeResolverOrSnapshot(t *testing.T) {
	doc := authmodel.Resource{Type: "doc", ID: "one"}
	malformed := []Expression{
		BindResource("doc", "", Direct("viewer")), BindResource("", "x", RoleIn("member")),
		BindResource("doc", "x\n", RoleIn("member")),
		All(BindResource("doc", "same", RoleIn("member")), BindResource("org", "same", RoleIn("member"))),
		On(doc, All(Role("admin"))), BindResource("doc", "x", Any(Role("admin"))),
		On(doc, RoleIn("member", doc)), BindResource("doc", "x", RoleIn("member", doc)),
		On(doc, BindResource("doc", "x", Direct("viewer"))), BindResource("doc", "x", BindResource("doc", "x", Direct("viewer"))),
		On(doc, Expression{Relation: "viewer", CurrentResource: true}), BindResource("doc", "x", Expression{NamedPermission: "read", CurrentResource: true}),
		{RoleName: "member", Resource: &doc, ResourceSlot: &ResourceSlot{Type: "doc", Key: "x"}},
		{RoleName: "member", CurrentResource: true, ResourceSlot: &ResourceSlot{Type: "doc", Key: "x"}},
		On(authmodel.Resource{Type: "doc"}, Direct("viewer")), On(authmodel.Resource{Type: "unknown", ID: "x"}, Direct("viewer")),
		BindResource("unknown", "x", Permission("read")), On(doc, Direct("missing")), On(doc, Permission("missing")),
		Direct("viewer"), Permission("read"), RoleIn("member"), All(), Any(), {},
	}
	s, f := bindingService(t, exact(tuples.Global(), "admin"))
	calls := 0
	resolve := func(context.Context, string) (authmodel.Resource, error) { calls++; return doc, nil }
	for i, e := range malformed {
		for _, expr := range []Expression{e, Any(Role("admin"), e), All(Role("missing"), e)} {
			if err := s.ValidateExpression(expr); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("case %d pure validation=%v", i, err)
			}
			if got, err := s.EvaluateResolved(t.Context(), expressionPrincipal, expr, resolve); !errors.Is(err, sdk.ErrInvalidInput) || got != (authmodel.CheckResult{}) {
				t.Fatalf("case %d=%+v/%v", i, got, err)
			}
		}
	}
	if f.snapshotCalls != 0 || calls != 0 {
		t.Fatalf("invalid input caused I/O: snapshots=%d resolvers=%d", f.snapshotCalls, calls)
	}
	validSlot := BindResource("doc", "x", RoleIn("editor"))
	if err := s.ValidateExpression(validSlot); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evaluate(t.Context(), expressionPrincipal, Any(Role("admin"), validSlot)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing resolver accepted=%v", err)
	}
	if _, err := s.EvaluateExpressionWith(t.Context(), f, expressionPrincipal, validSlot); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("bound missing resolver accepted=%v", err)
	}
	if f.snapshotCalls != 0 {
		t.Fatal("missing resolver opened snapshot")
	}
}
func TestResourceBindingStructuralLimits(t *testing.T) {
	s, f := expressionService(t)
	deep := Role("a")
	for i := 0; i <= MaxExpressionDepth; i++ {
		deep = All(deep)
	}
	wide := All(make([]Expression, MaxExpressionNodes)...)
	for i := range wide.AllOf {
		wide.AllOf[i] = Role("a")
	}
	for _, expr := range []Expression{deep, wide} {
		if err := s.ValidateExpression(expr); !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("structural limit=%v", err)
		}
		if _, err := s.Evaluate(t.Context(), expressionPrincipal, expr); !errors.Is(err, authmodel.ErrEvaluationLimit) {
			t.Fatalf("evaluation structural limit=%v", err)
		}
	}
	if f.snapshotCalls != 0 {
		t.Fatal("structural validation read facts")
	}
}
func TestResourceBindingNamedModelsRejectRuntimeTargets(t *testing.T) {
	doc := authmodel.Resource{Type: "doc", ID: "one"}
	for _, expr := range []Expression{BindResource("doc", "x", RoleIn("editor")), BindResource("doc", "x", Direct("viewer")), On(doc, Direct("viewer")), On(doc, Through("parent", "manage")), On(doc, Permission("read"))} {
		m := bindingModel()
		d := m.ResourceTypes["doc"]
		d.Permissions["bound"] = expr
		if _, err := Compile(m); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("named runtime binding accepted: %+v/%v", expr, err)
		}
	}
	// Existing named exact fixed and current resource predicates remain valid.
	m := bindingModel()
	d := m.ResourceTypes["doc"]
	d.Permissions["bound"] = All(RoleIn("editor", doc), RoleIn("member"))
	if _, err := Compile(m); err != nil {
		t.Fatal(err)
	}
}
func TestResourceBindingAdhocThroughRequiresConcreteResourceTargets(t *testing.T) {
	for _, subjects := range [][]SubjectTypeRef{{{Type: "user"}}, {{Type: "group", Relation: "member"}}, {{Type: "org"}, {Type: "user"}}} {
		m := bindingModel()
		d := m.ResourceTypes["doc"]
		d.Relations["untraversed"] = RelationDef{AllowedSubjects: subjects}
		_, f := expressionService(t)
		s, err := NewService(f, WithModel(m))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ValidateExpression(On(authmodel.Resource{Type: "doc", ID: "one"}, Through("untraversed", "manage"))); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unsafe ad hoc through accepted=%v", err)
		}
	}
}
func TestResourceBindingLazyResolutionAndInputSnapshot(t *testing.T) {
	doc := authmodel.Resource{Type: "doc", ID: "one"}
	org := authmodel.Resource{Type: "org", ID: "tenant"}
	s, f := bindingService(t, exact(tuples.Global(), "admin"), exact(tuples.On("org", "tenant"), "member"), exact(tuples.On("doc", "one"), "viewer"))
	order := []string{}
	resolve := func(_ context.Context, key string) (authmodel.Resource, error) {
		order = append(order, key)
		if key == "org" {
			return org, nil
		}
		if key == "doc" {
			return doc, nil
		}
		return authmodel.Resource{}, errors.New("should be skipped")
	}
	skipped := BindResource("doc", "skipped", Direct("viewer"))
	for _, expr := range []Expression{Any(Role("admin"), skipped), All(Role("absent"), skipped)} {
		if _, err := s.EvaluateResolved(t.Context(), expressionPrincipal, expr, resolve); err != nil {
			t.Fatal(err)
		}
	}
	if len(order) != 0 {
		t.Fatalf("eager inputs=%v", order)
	}
	expr := All(BindResource("org", "org", RoleIn("member")), BindResource("doc", "doc", Direct("viewer")), BindResource("doc", "doc", Permission("read")))
	got, err := s.EvaluateResolved(t.Context(), expressionPrincipal, expr, resolve)
	if err != nil || !got.Allowed || !reflect.DeepEqual(order, []string{"org", "doc"}) {
		t.Fatalf("ordered memo=%+v/%v order=%v", got, err, order)
	}
	// Mutating caller-owned selectors during the first callback cannot retarget a later leaf.
	fixed := On(doc, Direct("viewer"))
	slot := BindResource("doc", "doc", Permission("read"))
	expr = All(BindResource("org", "org", RoleIn("member")), fixed, slot)
	order = nil
	got, err = s.EvaluateResolved(t.Context(), expressionPrincipal, expr, func(ctx context.Context, key string) (authmodel.Resource, error) {
		fixed.Resource.ID = "changed"
		slot.ResourceSlot.Key = "changed"
		return resolve(ctx, key)
	})
	if err != nil || !got.Allowed || !reflect.DeepEqual(order, []string{"org", "doc"}) {
		t.Fatalf("mutable selectors changed operation=%+v/%v order=%v", got, err, order)
	}
	if f.snapshotCalls != 4 {
		t.Fatalf("snapshots=%d", f.snapshotCalls)
	}
}
func TestResourceBindingResolverErrorsAndCancellation(t *testing.T) {
	doc := authmodel.Resource{Type: "doc", ID: "one"}
	failure := errors.New("host lookup failed")
	for _, tc := range []struct {
		name     string
		resource authmodel.Resource
		failure  error
		want     error
		allow    bool
	}{
		{"not applicable", authmodel.Resource{}, fmt.Errorf("optional: %w", ErrResourceNotApplicable), nil, true},
		{"ordinary not found", authmodel.Resource{}, sdk.ErrNotFound, sdk.ErrNotFound, false},
		{"failure", authmodel.Resource{}, failure, failure, false},
		{"type mismatch", authmodel.Resource{Type: "org", ID: "tenant"}, nil, sdk.ErrInvalidInput, false},
		{"invalid result", authmodel.Resource{Type: "doc"}, nil, sdk.ErrInvalidInput, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := expressionService(t, exact(tuples.Global(), "admin"))
			expr := Any(BindResource("doc", "doc", RoleIn("editor")), Role("admin"))
			got, err := s.EvaluateResolved(t.Context(), expressionPrincipal, expr, func(context.Context, string) (authmodel.Resource, error) { return tc.resource, tc.failure })
			if !errors.Is(err, tc.want) || got.Allowed != tc.allow {
				t.Fatalf("result=%+v/%v", got, err)
			}
			if tc.want != nil && (got != (authmodel.CheckResult{}) || f.containsCalls != 0) {
				t.Fatalf("resolver failure read facts: %+v/%d", got, f.containsCalls)
			}
		})
	}
	s, f := expressionService(t, exact(tuples.On("doc", "one"), "editor"))
	f.fail = ErrResourceNotApplicable
	got, err := s.EvaluateResolved(t.Context(), expressionPrincipal, BindResource("doc", "doc", RoleIn("editor")), func(context.Context, string) (authmodel.Resource, error) { return doc, nil })
	if !errors.Is(err, ErrResourceNotApplicable) || got != (authmodel.CheckResult{}) {
		t.Fatalf("reader sentinel suppressed=%+v/%v", got, err)
	}
	f.fail = nil
	before := f.containsCalls
	ctx, cancel := context.WithCancel(t.Context())
	got, err = s.EvaluateResolved(ctx, expressionPrincipal, BindResource("doc", "doc", RoleIn("editor")), func(context.Context, string) (authmodel.Resource, error) { cancel(); return doc, nil })
	if !errors.Is(err, context.Canceled) || got != (authmodel.CheckResult{}) || f.containsCalls != before {
		t.Fatalf("callback cancellation=%+v/%v", got, err)
	}
	calls := 0
	got, err = s.EvaluateResolved(ctx, expressionPrincipal, BindResource("doc", "doc", RoleIn("editor")), func(context.Context, string) (authmodel.Resource, error) { calls++; return doc, nil })
	if !errors.Is(err, context.Canceled) || calls != 0 || got != (authmodel.CheckResult{}) {
		t.Fatalf("canceled resolver ran=%+v/%v calls=%d", got, err, calls)
	}
}
func TestResourceBindingSharedBudgets(t *testing.T) {
	doc := authmodel.Resource{Type: "doc", ID: "one"}
	other := authmodel.Resource{Type: "doc", ID: "two"}
	for _, tc := range []struct {
		name   string
		expr   Expression
		limits authmodel.EvaluationLimits
		want   error
	}{
		{"distinct direct roots", Any(On(doc, Direct("viewer")), On(other, Direct("viewer"))), authmodel.EvaluationLimits{MaxGraphStates: 1}, authmodel.ErrEvaluationLimit},
		{"repeated direct root", Any(On(doc, Direct("viewer")), On(doc, Direct("viewer"))), authmodel.EvaluationLimits{MaxGraphStates: 1}, nil},
		{"primitive permission collision", Any(On(doc, Direct("viewer")), On(doc, Permission("viewer"))), authmodel.EvaluationLimits{MaxGraphStates: 1}, authmodel.ErrEvaluationLimit},
		{"distinct named roots", Any(On(doc, Permission("read")), On(other, Permission("read"))), authmodel.EvaluationLimits{MaxGraphStates: 1}, authmodel.ErrEvaluationLimit},
		{"aggregate permission steps", Any(On(doc, Permission("read")), On(other, Permission("read"))), authmodel.EvaluationLimits{MaxEvaluationSteps: 6}, authmodel.ErrEvaluationLimit},
		{"distinct through roots", Any(On(doc, Through("parent", "manage")), On(other, Through("parent", "manage"))), authmodel.EvaluationLimits{MaxGraphStates: 1}, authmodel.ErrEvaluationLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, f := bindingService(t)
			m := bindingModel()
			d := m.ResourceTypes["doc"]
			d.Relations["viewer"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}
			s, err := NewService(f, WithModel(m), WithLimits(tc.limits))
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Evaluate(t.Context(), expressionPrincipal, tc.expr)
			if !errors.Is(err, tc.want) || (tc.want != nil && got != (authmodel.CheckResult{})) {
				t.Fatalf("budget result=%+v/%v want=%v", got, err, tc.want)
			}
			if tc.want == nil && got.Allowed {
				t.Fatalf("missing facts allowed=%+v", got)
			}
		})
	}
}

func TestResourceBindingRequiresCoherentSnapshot(t *testing.T) {
	_, f := expressionService(t, exact(tuples.Global(), "admin"))
	raw := struct{ tuples.Reader }{f}
	s, err := NewService(raw)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	got, err := s.EvaluateResolved(t.Context(), expressionPrincipal, BindResource("doc", "x", RoleIn("member")), func(context.Context, string) (authmodel.Resource, error) {
		calls++
		return authmodel.Resource{Type: "doc", ID: "one"}, nil
	})
	if !errors.Is(err, sdk.ErrInvalidInput) || got != (authmodel.CheckResult{}) || calls != 0 || f.containsCalls != 0 {
		t.Fatalf("missing snapshot=%+v/%v calls=%d", got, err, calls)
	}
}
