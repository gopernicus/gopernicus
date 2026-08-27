package authorizersvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
			gates := NewGates(tt.checker, stubDeclarer{"post/delete": true})

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
	gates := NewGates(checker, stubDeclarer{"post/delete": true})

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
