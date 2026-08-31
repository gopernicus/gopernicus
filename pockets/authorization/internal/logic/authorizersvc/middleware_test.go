package authorizersvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// erroringStore is a fakeStore whose direct-relation check fails, exercising the
// engine-error → 500 fail-closed leg (the relFake precedent, an erroring
// relationship.Storer).
type erroringStore struct{ *fakeStore }

func (erroringStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return false, errors.New("store exploded")
}

// markerHandler asserts the wrapped middleware forwards the ORIGINAL request
// (it reads a header the test set upstream) and records that it ran.
func markerHandler(ran *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		w.Header().Set("X-Saw-Marker", r.Header.Get("X-Marker"))
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequirePermission(t *testing.T) {
	ownerTuple := relationship.CreateRelationship{
		ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
	}

	tests := []struct {
		name          string
		store         relationship.Storer
		resource      ResourceResolver
		withPrincipal bool
		principal     identity.Principal
		wantStatus    int
		wantNext      bool
	}{
		{
			name:       "no principal → 401",
			store:      &fakeStore{tuples: []relationship.CreateRelationship{ownerTuple}},
			resource:   FixedResource("post", "p1"),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "principal without grant → 403",
			store:         &fakeStore{tuples: []relationship.CreateRelationship{ownerTuple}},
			resource:      FixedResource("post", "p1"),
			withPrincipal: true,
			principal:     identity.Principal{Type: "user", ID: "u2"},
			wantStatus:    http.StatusForbidden,
		},
		{
			name:          "granted → next runs",
			store:         &fakeStore{tuples: []relationship.CreateRelationship{ownerTuple}},
			resource:      FixedResource("post", "p1"),
			withPrincipal: true,
			principal:     identity.Principal{Type: "user", ID: "u1"},
			wantStatus:    http.StatusOK,
			wantNext:      true,
		},
		{
			name:          "engine error → 500 fail closed",
			store:         erroringStore{fakeStore: &fakeStore{tuples: []relationship.CreateRelationship{ownerTuple}}},
			resource:      FixedResource("post", "p1"),
			withPrincipal: true,
			principal:     identity.Principal{Type: "user", ID: "u1"},
			wantStatus:    http.StatusInternalServerError,
		},
		{
			name:  "resolver error → 500 fail closed",
			store: &fakeStore{tuples: []relationship.CreateRelationship{ownerTuple}},
			resource: func(*http.Request) (Resource, error) {
				return Resource{}, errors.New("cannot resolve")
			},
			withPrincipal: true,
			principal:     identity.Principal{Type: "user", ID: "u1"},
			wantStatus:    http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewService(tt.store, testSchema(), Config{})
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}

			ran := false
			gate := svc.RequirePermission("delete", tt.resource)
			handler := gate(markerHandler(&ran))

			req := httptest.NewRequest(http.MethodGet, "/gated", nil)
			req.Header.Set("X-Marker", "kilroy")
			if tt.withPrincipal {
				req = req.WithContext(identity.WithPrincipal(req.Context(), tt.principal))
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status: want %d, got %d (body %q)", tt.wantStatus, rec.Code, rec.Body.String())
			}
			if ran != tt.wantNext {
				t.Fatalf("next ran: want %v, got %v", tt.wantNext, ran)
			}
			if tt.wantNext && rec.Header().Get("X-Saw-Marker") != "kilroy" {
				t.Fatalf("next did not see the original request header: got %q", rec.Header().Get("X-Saw-Marker"))
			}
		})
	}
}

// TestRequirePermissionCoordinates: the coordinate forms are RequirePermission
// over PathResource/FixedResource, and their coordinates are checked at
// registration — an undeclared pair or a nameless parameter panics when the
// route is mounted, never at request time.
func TestRequirePermissionCoordinates(t *testing.T) {
	ownerTuple := relationship.CreateRelationship{
		ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
	}
	svc, err := NewService(&fakeStore{tuples: []relationship.CreateRelationship{ownerTuple}}, testSchema(), Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	call := func(gate func(http.Handler) http.Handler, principal *identity.Principal, pathValue string) (int, bool) {
		ran := false
		req := httptest.NewRequest(http.MethodGet, "/gated", nil)
		if pathValue != "" {
			req.SetPathValue("postID", pathValue)
		}
		if principal != nil {
			req = req.WithContext(identity.WithPrincipal(req.Context(), *principal))
		}
		rec := httptest.NewRecorder()
		gate(markerHandler(&ran)).ServeHTTP(rec, req)
		return rec.Code, ran
	}
	owner := &identity.Principal{Type: "user", ID: "u1"}
	stranger := &identity.Principal{Type: "user", ID: "u2"}

	on := svc.RequirePermissionOn("post", "delete", "postID")
	if code, ran := call(on, owner, "p1"); code != http.StatusOK || !ran {
		t.Fatalf("owner on p1: %d ran=%v", code, ran)
	}
	if code, ran := call(on, stranger, "p1"); code != http.StatusForbidden || ran {
		t.Fatalf("stranger on p1: %d ran=%v", code, ran)
	}
	if code, ran := call(on, owner, ""); code != http.StatusInternalServerError || ran {
		t.Fatalf("empty path value must fail closed: %d ran=%v", code, ran)
	}
	fixed := svc.RequirePermissionFixed("post", "delete", "p1")
	if code, ran := call(fixed, owner, ""); code != http.StatusOK || !ran {
		t.Fatalf("fixed owner: %d ran=%v", code, ran)
	}

	for name, mount := range map[string]func(){
		"undeclared permission": func() { svc.RequirePermissionOn("post", "fly", "postID") },
		"undeclared resource":   func() { svc.RequirePermissionOn("comet", "delete", "postID") },
		"empty parameter":       func() { svc.RequirePermissionOn("post", "delete", "") },
		"empty fixed id":        func() { svc.RequirePermissionFixed("post", "delete", "") },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("must panic at registration")
				}
			}()
			mount()
		})
	}
}

// stubChecker answers every check with a fixed result/error — the Checker half
// of the extracted gate body, with no engine behind it.
type stubChecker struct {
	result CheckResult
	err    error
	seen   CheckRequest
}

func (c *stubChecker) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	c.seen = req
	return c.result, c.err
}

// stubDeclarer declares exactly the pairs it was given.
type stubDeclarer map[string]bool

func (d stubDeclarer) DeclaresPermission(resourceType, permission string) bool {
	return d[resourceType+"/"+permission]
}

// TestGatesLadder: the package-level builder over stub Checker/Declarer walks
// the SAME 401/403/500/503 ladder the Service methods do — it is the one
// implementation, so a sibling composite mounting it cannot drift.
func TestGatesLadder(t *testing.T) {
	tests := []struct {
		name          string
		checker       *stubChecker
		resource      ResourceResolver
		withPrincipal bool
		wantStatus    int
		wantNext      bool
	}{
		{
			name:       "no principal → 401",
			checker:    &stubChecker{result: CheckResult{Allowed: true}},
			resource:   FixedResource("post", "p1"),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "denied → 403",
			checker:       &stubChecker{result: CheckResult{Allowed: false, ReasonCode: ReasonDenied}},
			resource:      FixedResource("post", "p1"),
			withPrincipal: true,
			wantStatus:    http.StatusForbidden,
		},
		{
			name:          "allowed → next runs",
			checker:       &stubChecker{result: CheckResult{Allowed: true}},
			resource:      FixedResource("post", "p1"),
			withPrincipal: true,
			wantStatus:    http.StatusOK,
			wantNext:      true,
		},
		{
			name:          "check error → 500 fail closed",
			checker:       &stubChecker{err: errors.New("decider exploded")},
			resource:      FixedResource("post", "p1"),
			withPrincipal: true,
			wantStatus:    http.StatusInternalServerError,
		},
		{
			name:          "evaluation limit → 503 fail closed",
			checker:       &stubChecker{err: ErrEvaluationLimit},
			resource:      FixedResource("post", "p1"),
			withPrincipal: true,
			wantStatus:    http.StatusServiceUnavailable,
		},
		{
			name:    "resolver error → 500 fail closed",
			checker: &stubChecker{result: CheckResult{Allowed: true}},
			resource: func(*http.Request) (Resource, error) {
				return Resource{}, errors.New("cannot resolve")
			},
			withPrincipal: true,
			wantStatus:    http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gates := NewGates(tt.checker, stubDeclarer{"post/delete": true}, DefaultMaxBatchSize)

			ran := false
			handler := gates.RequirePermission("delete", tt.resource)(markerHandler(&ran))

			req := httptest.NewRequest(http.MethodGet, "/gated", nil)
			req.Header.Set("X-Marker", "kilroy")
			if tt.withPrincipal {
				req = req.WithContext(identity.WithPrincipal(req.Context(), identity.Principal{Type: "user", ID: "u1"}))
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status: want %d, got %d (body %q)", tt.wantStatus, rec.Code, rec.Body.String())
			}
			if ran != tt.wantNext {
				t.Fatalf("next ran: want %v, got %v", tt.wantNext, ran)
			}
			if tt.wantNext && rec.Header().Get("X-Saw-Marker") != "kilroy" {
				t.Fatalf("next did not see the original request header: got %q", rec.Header().Get("X-Saw-Marker"))
			}
		})
	}
}

// TestGatesCoordinates: the builder's coordinate forms carry the principal's
// coordinates into the Checker and run the legality check through the Declarer,
// panicking at mount for a pair no model declares.
func TestGatesCoordinates(t *testing.T) {
	checker := &stubChecker{result: CheckResult{Allowed: true}}
	gates := NewGates(checker, stubDeclarer{"post/delete": true}, DefaultMaxBatchSize)

	ran := false
	req := httptest.NewRequest(http.MethodGet, "/gated", nil)
	req.SetPathValue("postID", "p1")
	req = req.WithContext(identity.WithPrincipal(req.Context(), identity.Principal{Type: "user", ID: "u1"}))
	rec := httptest.NewRecorder()
	gates.RequirePermissionOn("post", "delete", "postID")(markerHandler(&ran)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !ran {
		t.Fatalf("coordinate gate: %d ran=%v", rec.Code, ran)
	}
	want := CheckRequest{
		Principal:  PrincipalRef{Type: "user", ID: "u1"},
		Permission: "delete",
		Resource:   Resource{Type: "post", ID: "p1"},
	}
	if checker.seen != want {
		t.Fatalf("check request: want %+v, got %+v", want, checker.seen)
	}

	for name, mount := range map[string]func(){
		"undeclared permission": func() { gates.RequirePermissionOn("post", "fly", "postID") },
		"undeclared resource":   func() { gates.RequirePermissionOn("comet", "delete", "postID") },
		"empty parameter":       func() { gates.RequirePermissionOn("post", "delete", "") },
		"empty fixed id":        func() { gates.RequirePermissionFixed("post", "delete", "") },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("must panic at registration")
				}
			}()
			mount()
		})
	}
}

// scriptedChecker answers each Check from a per-(resource, permission) script and
// records, IN ORDER, the coordinates it was consulted on — the counting Checker
// that proves short-circuit, in-order evaluation, and never-consulted
// alternatives.
type scriptedChecker struct {
	answers map[string]CheckResult
	errs    map[string]error
	seen    []string
}

func (c *scriptedChecker) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	key := req.Resource.Type + ":" + req.Resource.ID + "/" + req.Permission
	c.seen = append(c.seen, key)
	if err := c.errs[key]; err != nil {
		return CheckResult{}, err
	}
	return c.answers[key], nil
}

// TestGatesRequireAnyPermission: the disjunction walks the one shared ladder —
// 401, strictly in-order evaluation, short-circuit on the first allow, the
// ErrAlternativeNotApplicable skip, whole-request fail-closed on any other
// resolver error / type disagreement / Check error, and 403 when every
// alternative denied or did not apply.
func TestGatesRequireAnyPermission(t *testing.T) {
	allow := CheckResult{Allowed: true}
	deny := CheckResult{Allowed: false, ReasonCode: ReasonDenied}
	declarer := stubDeclarer{"post/delete": true, "org/admin": true}

	post := GateSpec{ResourceType: "post", Permission: "delete", Resource: FixedResource("post", "p1")}
	org := GateSpec{ResourceType: "org", Permission: "admin", Resource: FixedResource("org", "o1")}
	notApplicable := func(spec GateSpec) GateSpec {
		spec.Resource = func(*http.Request) (Resource, error) {
			return Resource{}, fmt.Errorf("the row names no organization: %w", ErrAlternativeNotApplicable)
		}
		return spec
	}
	broken := func(spec GateSpec) GateSpec {
		spec.Resource = func(*http.Request) (Resource, error) { return Resource{}, errors.New("store exploded") }
		return spec
	}
	mistyped := func(spec GateSpec) GateSpec {
		spec.Resource = func(*http.Request) (Resource, error) { return Resource{Type: "comet", ID: "c1"}, nil }
		return spec
	}

	tests := []struct {
		name          string
		alternatives  []GateSpec
		answers       map[string]CheckResult
		errs          map[string]error
		withPrincipal bool
		wantStatus    int
		wantNext      bool
		wantSeen      []string
	}{
		{
			name:         "no principal → 401 before any Check",
			alternatives: []GateSpec{post, org},
			answers:      map[string]CheckResult{"post:p1/delete": allow},
			wantStatus:   http.StatusUnauthorized,
		},
		{
			name:          "first alternative allows → short-circuits, second never consulted",
			alternatives:  []GateSpec{post, org},
			answers:       map[string]CheckResult{"post:p1/delete": allow, "org:o1/admin": allow},
			withPrincipal: true,
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantSeen:      []string{"post:p1/delete"},
		},
		{
			name:          "second alternative allows after a deny → in order, next runs",
			alternatives:  []GateSpec{post, org},
			answers:       map[string]CheckResult{"post:p1/delete": deny, "org:o1/admin": allow},
			withPrincipal: true,
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantSeen:      []string{"post:p1/delete", "org:o1/admin"},
		},
		{
			name:          "every alternative denies → 403",
			alternatives:  []GateSpec{post, org},
			answers:       map[string]CheckResult{"post:p1/delete": deny, "org:o1/admin": deny},
			withPrincipal: true,
			wantStatus:    http.StatusForbidden,
			wantSeen:      []string{"post:p1/delete", "org:o1/admin"},
		},
		{
			name:          "single alternative allows → RequirePermission behavior",
			alternatives:  []GateSpec{post},
			answers:       map[string]CheckResult{"post:p1/delete": allow},
			withPrincipal: true,
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantSeen:      []string{"post:p1/delete"},
		},
		{
			name:          "single alternative denies → RequirePermission behavior",
			alternatives:  []GateSpec{post},
			answers:       map[string]CheckResult{"post:p1/delete": deny},
			withPrincipal: true,
			wantStatus:    http.StatusForbidden,
			wantSeen:      []string{"post:p1/delete"},
		},
		{
			name: "duplicate pair with different resolvers is legal and evaluated independently",
			alternatives: []GateSpec{
				post,
				{ResourceType: "post", Permission: "delete", Resource: FixedResource("post", "p2")},
			},
			answers:       map[string]CheckResult{"post:p1/delete": deny, "post:p2/delete": allow},
			withPrincipal: true,
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantSeen:      []string{"post:p1/delete", "post:p2/delete"},
		},
		{
			name:          "first alternative Check error → 500, the allowing second never reached",
			alternatives:  []GateSpec{post, org},
			answers:       map[string]CheckResult{"org:o1/admin": allow},
			errs:          map[string]error{"post:p1/delete": errors.New("decider exploded")},
			withPrincipal: true,
			wantStatus:    http.StatusInternalServerError,
			wantSeen:      []string{"post:p1/delete"},
		},
		{
			name:          "second alternative Check error after a deny → 500",
			alternatives:  []GateSpec{post, org},
			answers:       map[string]CheckResult{"post:p1/delete": deny},
			errs:          map[string]error{"org:o1/admin": errors.New("decider exploded")},
			withPrincipal: true,
			wantStatus:    http.StatusInternalServerError,
			wantSeen:      []string{"post:p1/delete", "org:o1/admin"},
		},
		{
			name:          "evaluation limit → 503 fail closed",
			alternatives:  []GateSpec{post, org},
			answers:       map[string]CheckResult{"org:o1/admin": allow},
			errs:          map[string]error{"post:p1/delete": ErrEvaluationLimit},
			withPrincipal: true,
			wantStatus:    http.StatusServiceUnavailable,
			wantSeen:      []string{"post:p1/delete"},
		},
		{
			name:          "non-sentinel resolver error → 500, the allowing second never reached",
			alternatives:  []GateSpec{broken(post), org},
			answers:       map[string]CheckResult{"org:o1/admin": allow},
			withPrincipal: true,
			wantStatus:    http.StatusInternalServerError,
		},
		{
			name:          "non-sentinel resolver error after a deny → 500",
			alternatives:  []GateSpec{post, broken(org)},
			answers:       map[string]CheckResult{"post:p1/delete": deny},
			withPrincipal: true,
			wantStatus:    http.StatusInternalServerError,
			wantSeen:      []string{"post:p1/delete"},
		},
		{
			name:          "resolved type disagrees with the declared pair → 500 fail closed",
			alternatives:  []GateSpec{mistyped(post), org},
			answers:       map[string]CheckResult{"org:o1/admin": allow},
			withPrincipal: true,
			wantStatus:    http.StatusInternalServerError,
		},
		{
			name:          "not-applicable sentinel skips to the alternative that allows",
			alternatives:  []GateSpec{notApplicable(post), org},
			answers:       map[string]CheckResult{"org:o1/admin": allow},
			withPrincipal: true,
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantSeen:      []string{"org:o1/admin"},
		},
		{
			name:          "not-applicable on every alternative → 403, no Check consulted",
			alternatives:  []GateSpec{notApplicable(post), notApplicable(org)},
			withPrincipal: true,
			wantStatus:    http.StatusForbidden,
		},
		{
			name:          "not-applicable then a deny → 403",
			alternatives:  []GateSpec{notApplicable(post), org},
			answers:       map[string]CheckResult{"org:o1/admin": deny},
			withPrincipal: true,
			wantStatus:    http.StatusForbidden,
			wantSeen:      []string{"org:o1/admin"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &scriptedChecker{answers: tt.answers, errs: tt.errs}
			gates := NewGates(checker, declarer, DefaultMaxBatchSize)

			ran := false
			handler := gates.RequireAnyPermission(tt.alternatives...)(markerHandler(&ran))

			req := httptest.NewRequest(http.MethodGet, "/gated", nil)
			req.Header.Set("X-Marker", "kilroy")
			if tt.withPrincipal {
				req = req.WithContext(identity.WithPrincipal(req.Context(), identity.Principal{Type: "user", ID: "u1"}))
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status: want %d, got %d (body %q)", tt.wantStatus, rec.Code, rec.Body.String())
			}
			if ran != tt.wantNext {
				t.Fatalf("next ran: want %v, got %v", tt.wantNext, ran)
			}
			if tt.wantNext && rec.Header().Get("X-Saw-Marker") != "kilroy" {
				t.Fatalf("next did not see the original request header: got %q", rec.Header().Get("X-Saw-Marker"))
			}
			if !slices.Equal(checker.seen, tt.wantSeen) {
				t.Fatalf("checks consulted: want %v, got %v", tt.wantSeen, checker.seen)
			}
		})
	}
}

// TestGatesRequireAnyPermissionRegistration: every legality fault is a MOUNT
// panic naming the alternative's index and pair — never a gate that quietly
// checks something no model grants, and never an uncapped N-Check route line.
func TestGatesRequireAnyPermissionRegistration(t *testing.T) {
	post := GateSpec{ResourceType: "post", Permission: "delete", Resource: FixedResource("post", "p1")}
	gates := NewGates(&stubChecker{result: CheckResult{Allowed: true}}, stubDeclarer{"post/delete": true, "org/admin": true}, 2)

	for name, tc := range map[string]struct {
		mount func()
		want  string
	}{
		"zero alternatives": {
			mount: func() { gates.RequireAnyPermission() },
			want:  "at least one alternative",
		},
		"nil resolver": {
			mount: func() {
				gates.RequireAnyPermission(post, GateSpec{ResourceType: "org", Permission: "admin"})
			},
			want: `alternative 2 of 2 ("org", "admin") needs a Resource resolver`,
		},
		"undeclared permission": {
			mount: func() {
				gates.RequireAnyPermission(post, GateSpec{ResourceType: "post", Permission: "fly", Resource: FixedResource("post", "p1")})
			},
			want: `alternative 2 of 2: the model declares no permission "fly" on resource type "post"`,
		},
		"undeclared resource type": {
			mount: func() {
				gates.RequireAnyPermission(GateSpec{ResourceType: "comet", Permission: "delete", Resource: FixedResource("comet", "c1")})
			},
			want: `alternative 1 of 1: the model declares no permission "delete" on resource type "comet"`,
		},
		"over the alternatives cap": {
			mount: func() { gates.RequireAnyPermission(post, post, post) },
			want:  "at most 2 alternatives",
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				got, ok := recover().(string)
				if !ok {
					t.Fatal("must panic at registration with a string message")
				}
				if !strings.Contains(got, tc.want) {
					t.Fatalf("panic message: want it to contain %q, got %q", tc.want, got)
				}
			}()
			tc.mount()
		})
	}
}

// TestServiceRequireAnyPermission: the relationship engine's own delegation
// mounts the shared body against the compiled schema — the disjunction admits on
// either real grant, and an undeclared pair panics at mount.
func TestServiceRequireAnyPermission(t *testing.T) {
	svc, err := NewService(&fakeStore{tuples: []relationship.CreateRelationship{
		{ResourceType: "post", ResourceID: "p2", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	}}, testSchema(), Config{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	gate := svc.RequireAnyPermission(
		GateSpec{ResourceType: "post", Permission: "delete", Resource: FixedResource("post", "p1")},
		GateSpec{ResourceType: "post", Permission: "delete", Resource: FixedResource("post", "p2")},
	)
	call := func(principal identity.Principal) (int, bool) {
		ran := false
		req := httptest.NewRequest(http.MethodGet, "/gated", nil)
		req = req.WithContext(identity.WithPrincipal(req.Context(), principal))
		rec := httptest.NewRecorder()
		gate(markerHandler(&ran)).ServeHTTP(rec, req)
		return rec.Code, ran
	}
	if code, ran := call(identity.Principal{Type: "user", ID: "u1"}); code != http.StatusOK || !ran {
		t.Fatalf("owner of the second alternative: %d ran=%v", code, ran)
	}
	if code, ran := call(identity.Principal{Type: "user", ID: "u2"}); code != http.StatusForbidden || ran {
		t.Fatalf("stranger: %d ran=%v", code, ran)
	}

	t.Run("undeclared pair panics at mount", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("must panic at registration")
			}
		}()
		svc.RequireAnyPermission(GateSpec{ResourceType: "post", Permission: "fly", Resource: FixedResource("post", "p1")})
	})
}
