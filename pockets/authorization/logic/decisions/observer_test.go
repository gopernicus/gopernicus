package decisions

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// observedSnapshot reports whether a snapshot callback is running, so an
// observer can prove it is never called from inside one.
type observedSnapshot struct {
	*tupleFixture
	mu     sync.Mutex
	inside int
}

func (o *observedSnapshot) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	return o.tupleFixture.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
		o.mu.Lock()
		o.inside++
		o.mu.Unlock()
		defer func() {
			o.mu.Lock()
			o.inside--
			o.mu.Unlock()
		}()
		return fn(ctx, r)
	})
}

func (o *observedSnapshot) inCallback() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.inside > 0
}

type observedEvents struct {
	mu     sync.Mutex
	events []DecisionEvent
}

func (o *observedEvents) all() []DecisionEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]DecisionEvent(nil), o.events...)
}

// observedService builds loggerService's model over a snapshot that tracks its
// callbacks, with DEBUG disabled so events cannot depend on the log level.
func observedService(t *testing.T) (*Service, *observedSnapshot, *observedEvents, *decisionLogHandler) {
	t.Helper()
	_, f := expressionService(t, exact(tuples.On("doc", "a"), "viewer"), exact(tuples.Global(), "admin"))
	source := &observedSnapshot{tupleFixture: f}
	got := &observedEvents{}
	h := &decisionLogHandler{enabled: false}
	m := Model{ResourceTypes: map[string]ResourceTypeDef{"doc": {Relations: map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]Expression{"read": Direct("viewer"), "all": Role("admin"), "exact": RoleIn("viewer")}}}}
	s, err := NewService(source, WithModel(m), WithLogger(slog.New(h)), WithDecisionObserver(func(_ context.Context, event DecisionEvent) {
		if source.inCallback() {
			t.Error("observer called inside a snapshot callback")
		}
		got.mu.Lock()
		got.events = append(got.events, event)
		got.mu.Unlock()
	}))
	if err != nil {
		t.Fatal(err)
	}
	return s, source, got, h
}

func TestDecisionObserverPublicOperations(t *testing.T) {
	req := authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "read", Resource: authmodel.Resource{Type: "doc", ID: "a"}}
	missing := req
	missing.Resource.ID = "absent"
	page := authmodel.LookupRequest{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Limit: 2}
	for _, tc := range []struct {
		name    string
		want    DecisionEvent
		emitted bool
		call    func(context.Context, *Service, tuples.Reader) error
	}{
		{"Check", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", ResourceID: "a", Outcome: OutcomeAllowed}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.Check(ctx, req)
			return err
		}},
		{"Check", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", ResourceID: "absent", Outcome: OutcomeDenied}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.Check(ctx, missing)
			return err
		}},
		{"CheckBatch", DecisionEvent{Outcome: OutcomeComplete, Requests: 2, Allowed: 1, Denied: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.CheckBatch(ctx, []authmodel.CheckRequest{req, missing})
			return err
		}},
		{"EvaluateBatch", DecisionEvent{Outcome: OutcomeComplete, Requests: 2, Allowed: 1, Denied: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.EvaluateBatch(ctx, []ExpressionCheck{{Principal: expressionPrincipal, Expression: Role("admin")}, {Principal: expressionPrincipal, Expression: Role("absent")}})
			return err
		}},
		{"Evaluate", DecisionEvent{Principal: expressionPrincipal, Permission: "admin", Outcome: OutcomeAllowed}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.Evaluate(ctx, expressionPrincipal, Role("admin"))
			return err
		}},
		{"EvaluateResolved", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", ResourceID: "absent", Outcome: OutcomeDenied}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.EvaluateResolved(ctx, expressionPrincipal, BindResource("doc", "target", Permission("read")), func(context.Context, string) (authmodel.Resource, error) { return missing.Resource, nil })
			return err
		}},
		{"CheckExplain", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", ResourceID: "a", Outcome: OutcomeAllowed}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, _, err := s.CheckExplain(ctx, req)
			return err
		}},
		{"FilterAuthorized", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Outcome: OutcomeComplete, Requests: 3, Allowed: 2, Denied: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.FilterAuthorized(ctx, expressionPrincipal, "read", "doc", []string{"a", "absent", "a"})
			return err
		}},
		{"FilterPage", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Outcome: OutcomeComplete, Allowed: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := FilterPage(ctx, s, FilterPageRequest[string]{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Limit: 2, ID: func(id string) string { return id }, Source: func(context.Context, string, int) (CandidatePage[string], error) {
				return CandidatePage[string]{Items: []Candidate[string]{{Item: "absent", NextCursor: "absent"}, {Item: "a", NextCursor: "a"}}}, nil
			}})
			return err
		}},
		{"LookupResources", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Outcome: OutcomeComplete, Allowed: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.LookupResources(ctx, expressionPrincipal, "read", "doc")
			return err
		}},
		{"LookupResourcesPage", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Outcome: OutcomeComplete, Allowed: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.LookupResourcesPage(ctx, expressionPrincipal, "read", "doc", "", 2)
			return err
		}},
		{"LookupResourcesIn", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Outcome: OutcomeComplete, Allowed: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.LookupResourcesIn(ctx, page)
			return err
		}},
		{"LookupAllResourceIDs", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Outcome: OutcomeComplete, Allowed: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.LookupAllResourceIDs(ctx, expressionPrincipal, "read", "doc")
			return err
		}},
		{"LookupResourceIDPage", DecisionEvent{Principal: expressionPrincipal, Permission: "read", ResourceType: "doc", Outcome: OutcomeComplete, Allowed: 1}, true, func(ctx context.Context, s *Service, _ tuples.Reader) error {
			_, err := s.LookupResourceIDPage(ctx, page)
			return err
		}},
		{"CheckWith", DecisionEvent{}, false, func(ctx context.Context, s *Service, r tuples.Reader) error {
			_, err := s.CheckWith(ctx, r, missing)
			return err
		}},
		{"CheckBatchWith", DecisionEvent{}, false, func(ctx context.Context, s *Service, r tuples.Reader) error {
			_, err := s.CheckBatchWith(ctx, r, []authmodel.CheckRequest{req, missing})
			return err
		}},
		{"EvaluateWith", DecisionEvent{}, false, func(ctx context.Context, s *Service, r tuples.Reader) error {
			_, err := s.EvaluateWith(ctx, r, missing)
			return err
		}},
		{"EvaluateExpressionWith", DecisionEvent{}, false, func(ctx context.Context, s *Service, r tuples.Reader) error {
			_, err := s.EvaluateExpressionWith(ctx, r, expressionPrincipal, Role("absent"))
			return err
		}},
		{"CheckExplainWith", DecisionEvent{}, false, func(ctx context.Context, s *Service, r tuples.Reader) error {
			_, _, err := s.CheckExplainWith(ctx, r, missing)
			return err
		}},
	} {
		t.Run(tc.name+"/"+string(tc.want.Outcome), func(t *testing.T) {
			s, source, got, h := observedService(t)
			if err := tc.call(t.Context(), s, source.tupleFixture); err != nil {
				t.Fatal(err)
			}
			if len(h.entries()) != 0 {
				t.Fatal("disabled DEBUG emitted a record")
			}
			events := got.all()
			if !tc.emitted {
				if len(events) != 0 {
					t.Fatalf("bound view emitted %+v", events)
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("emitted %d events: %+v", len(events), events)
			}
			if source.snapshotCalls == 0 {
				t.Fatal("operation never entered a snapshot callback")
			}
			event := events[0]
			if event.Duration <= 0 {
				t.Fatalf("missing duration: %+v", event)
			}
			event.Duration = 0
			want := tc.want
			want.Operation = tc.name
			if event != want {
				t.Fatalf("event=%+v\nwant %+v", event, want)
			}
		})
	}
}

func TestDecisionObserverErrorsAreBounded(t *testing.T) {
	s, _, got, _ := observedService(t)
	principal := authmodel.PrincipalRef{Type: "user", ID: strings.Repeat("\xff", 1024)}
	if _, err := s.Check(t.Context(), authmodel.CheckRequest{Principal: principal, Permission: strings.Repeat("p", 1024), Resource: authmodel.Resource{Type: "doc", ID: "a"}}); err == nil {
		t.Fatal("invalid principal accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Evaluate(canceled, expressionPrincipal, Role("admin")); err == nil {
		t.Fatal("canceled evaluation succeeded")
	}
	events := got.all()
	if len(events) != 2 {
		t.Fatalf("events=%+v", events)
	}
	for i, want := range []string{"invalid_input", "canceled"} {
		if events[i].Outcome != OutcomeError || events[i].ErrorKind != want {
			t.Fatalf("event %d=%+v; want error %s", i, events[i], want)
		}
	}
	for _, value := range []string{events[0].Principal.ID, events[0].Permission} {
		if len(value) > tuples.MaxRefFieldLen || !utf8.ValidString(value) {
			t.Fatalf("unbounded event field of %d bytes", len(value))
		}
	}
}

func TestDecisionObserverIgnoresNestedBoundViewsAndRunsConcurrently(t *testing.T) {
	_, f := expressionService(t, exact(tuples.On("doc", "a"), "viewer"), exact(tuples.Global(), "admin"))
	got := &observedEvents{}
	s, err := NewService(f, WithModel(permissionModel(RoleIn("viewer"))), WithDecisionObserver(func(_ context.Context, event DecisionEvent) {
		got.mu.Lock()
		got.events = append(got.events, event)
		got.mu.Unlock()
	}))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := s.Evaluate(t.Context(), expressionPrincipal, Role("admin")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := len(got.all()); n != 16 {
		t.Fatalf("concurrent events=%d", n)
	}
	view := s.bound(t.Context(), f)
	if _, err := view.Check(t.Context(), authmodel.CheckRequest{Principal: expressionPrincipal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if n := len(got.all()); n != 16 {
		t.Fatalf("bound view emitted: %d events", n)
	}
}

func TestDecisionObserverDisabledByDefault(t *testing.T) {
	s, f := expressionService(t, exact(tuples.Global(), "admin"))
	if s.observer != nil {
		t.Fatal("observer enabled by default")
	}
	if _, err := s.Evaluate(t.Context(), expressionPrincipal, Role("admin")); err != nil {
		t.Fatal(err)
	}
	s, err := NewService(f, WithDecisionObserver(nil))
	if err != nil || s.observer != nil {
		t.Fatalf("nil observer: %v", err)
	}
	if _, err := s.Check(t.Context(), authmodel.CheckRequest{Principal: sdk.Principal{Type: "user", ID: "u"}, Permission: "read", Resource: authmodel.Resource{Type: "doc", ID: "a"}}); err != nil {
		t.Fatal(err)
	}
}

// TestDecisionObserverExpressionTargets pins what an expression evaluation
// names: a single leaf's permission, relation or role and its resolved resource
// or scope; for a composite, the first denied leaf when not allowed, otherwise
// the first leaf reached, plus the reached leaves' counts. The DEBUG line agrees.
func TestDecisionObserverExpressionTargets(t *testing.T) {
	m := Model{ResourceTypes: map[string]ResourceTypeDef{
		"folder": {Relations: map[string]RelationDef{"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}}, Permissions: map[string]Expression{"read": Direct("viewer")}},
		"doc": {Relations: map[string]RelationDef{
			"viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
			"parent": {AllowedSubjects: []SubjectTypeRef{{Type: "folder"}}},
		}, Permissions: map[string]Expression{"read": Any(Direct("viewer"), Through("parent", "read"))}},
	}}
	a, b := authmodel.Resource{Type: "doc", ID: "a"}, authmodel.Resource{Type: "doc", ID: "b"}
	parent := tuples.Tuple{Scope: tuples.On("doc", "b"), Relation: "parent", Subject: tuples.SubjectRef{Type: "folder", ID: "f"}}
	resolveTo := func(resource authmodel.Resource, err error) ResourceResolver {
		return func(context.Context, string) (authmodel.Resource, error) { return resource, err }
	}
	for _, tc := range []struct {
		name    string
		expr    Expression
		resolve ResourceResolver
		want    DecisionEvent
	}{
		{"permission leaf denied", On(b, Permission("read")), nil, DecisionEvent{Permission: "read", ResourceType: "doc", ResourceID: "b", Outcome: OutcomeDenied}},
		{"permission leaf allowed", On(a, Permission("read")), nil, DecisionEvent{Permission: "read", ResourceType: "doc", ResourceID: "a", Outcome: OutcomeAllowed}},
		{"global role leaf", Role("auditor"), nil, DecisionEvent{Permission: "auditor", Outcome: OutcomeDenied}},
		{"scoped role leaf", RoleIn("owner", a), nil, DecisionEvent{Permission: "owner", ResourceType: "doc", ResourceID: "a", Outcome: OutcomeDenied}},
		{"relation leaf", On(b, Direct("viewer")), nil, DecisionEvent{Permission: "viewer", ResourceType: "doc", ResourceID: "b", Outcome: OutcomeDenied}},
		{"through leaf", On(b, Through("parent", "read")), nil, DecisionEvent{Permission: "read", ResourceType: "doc", ResourceID: "b", Outcome: OutcomeDenied}},
		{"any both denied", Any(On(b, Permission("read")), RoleIn("owner", a)), nil, DecisionEvent{Permission: "read", ResourceType: "doc", ResourceID: "b", Outcome: OutcomeDenied, Requests: 2, Denied: 2}},
		{"all second denied", All(Role("admin"), RoleIn("owner", a)), nil, DecisionEvent{Permission: "owner", ResourceType: "doc", ResourceID: "a", Outcome: OutcomeDenied, Requests: 2, Allowed: 1, Denied: 1}},
		{"all allowed", All(Role("admin"), On(a, Permission("read"))), nil, DecisionEvent{Permission: "admin", Outcome: OutcomeAllowed, Requests: 2, Allowed: 2}},
		{"any short circuit", Any(Role("admin"), BindResource("doc", "never", Permission("read"))), resolveTo(authmodel.Resource{}, errors.New("unreached")), DecisionEvent{Permission: "admin", Outcome: OutcomeAllowed, Requests: 1, Allowed: 1}},
		{"any allowed after a denial", Any(Role("auditor"), Role("admin")), nil, DecisionEvent{Permission: "auditor", Outcome: OutcomeAllowed, Requests: 2, Allowed: 1, Denied: 1}},
		{"nested composite", Any(All(Role("admin"), RoleIn("owner", a)), On(b, Direct("viewer"))), nil, DecisionEvent{Permission: "owner", ResourceType: "doc", ResourceID: "a", Outcome: OutcomeDenied, Requests: 3, Allowed: 1, Denied: 2}},
		{"slot resolved", BindResource("doc", "target", Permission("read")), resolveTo(b, nil), DecisionEvent{Permission: "read", ResourceType: "doc", ResourceID: "b", Outcome: OutcomeDenied}},
		{"slot not applicable", Any(BindResource("doc", "target", Permission("read")), Role("auditor")), resolveTo(authmodel.Resource{}, ErrResourceNotApplicable), DecisionEvent{Permission: "read", Outcome: OutcomeDenied, Requests: 2, Denied: 2}},
		{"resolver error", Any(Role("auditor"), BindResource("doc", "target", Permission("read"))), resolveTo(authmodel.Resource{}, errors.New("input unavailable")), DecisionEvent{Outcome: OutcomeError, ErrorKind: "other"}},
		{"validation error", All(), nil, DecisionEvent{Outcome: OutcomeError, ErrorKind: "invalid_input"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, f := expressionService(t, exact(tuples.On("doc", "a"), "viewer"), exact(tuples.Global(), "admin"), parent)
			got := &observedEvents{}
			h := &decisionLogHandler{enabled: true}
			s, err := NewService(f, WithModel(m), WithLogger(slog.New(h)), WithDecisionObserver(func(_ context.Context, event DecisionEvent) {
				got.mu.Lock()
				got.events = append(got.events, event)
				got.mu.Unlock()
			}))
			if err != nil {
				t.Fatal(err)
			}
			operation := "Evaluate"
			if tc.resolve != nil {
				operation = "EvaluateResolved"
				_, _ = s.EvaluateResolved(t.Context(), expressionPrincipal, tc.expr, tc.resolve)
			} else {
				_, _ = s.Evaluate(t.Context(), expressionPrincipal, tc.expr)
			}
			events := got.all()
			if len(events) != 1 {
				t.Fatalf("events=%+v", events)
			}
			event := events[0]
			event.Duration = 0
			want := tc.want
			want.Operation, want.Principal = operation, expressionPrincipal
			if event != want {
				t.Fatalf("event=%+v\nwant %+v", event, want)
			}
			entry := assertDecisionLog(t, h, operation, string(want.Outcome), false)
			for key, value := range map[string]string{"permission": want.Permission, "resource_type": want.ResourceType, "resource_id": want.ResourceID} {
				if logged, ok := entry.attrs[key]; ok != (value != "") || (ok && logged != value) {
					t.Errorf("log %s=%v, event %q", key, logged, value)
				}
			}
			for key, value := range map[string]int{"request_count": want.Requests, "allowed_count": want.Allowed, "denied_count": want.Denied} {
				if logged, ok := entry.attrs[key]; ok != (want.Requests > 0) || (ok && logged != int64(value)) {
					t.Errorf("log %s=%v, event %d", key, logged, value)
				}
			}
		})
	}
}

// TestDecisionObserverFailedExpressionNamesNoTarget pins that an evaluation
// failing after a leaf decided reports neither that leaf nor its counts.
func TestDecisionObserverFailedExpressionNamesNoTarget(t *testing.T) {
	s, _, got, _ := observedService(t)
	s.limits.MaxEvaluationSteps = 2
	if _, err := s.Evaluate(t.Context(), expressionPrincipal, All(Role("admin"), Role("admin"))); err == nil {
		t.Fatal("exhausted evaluation succeeded")
	}
	events := got.all()
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
	event := events[0]
	event.Duration = 0
	if want := (DecisionEvent{Operation: "Evaluate", Principal: expressionPrincipal, Outcome: OutcomeError, ErrorKind: "evaluation_limit"}); event != want {
		t.Fatalf("event=%+v\nwant %+v", event, want)
	}
}
