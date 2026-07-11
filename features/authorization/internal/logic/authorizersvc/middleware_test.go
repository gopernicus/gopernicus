package authorizersvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// erroringStore is a fakeStore whose direct-relation check fails, exercising the
// engine-error → 500 fail-closed leg (the relFake precedent, an erroring
// relationship.Storer).
type erroringStore struct{ *fakeStore }

func (erroringStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
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
