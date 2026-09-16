package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

type membershipFacts struct {
	*memory.Tuples
	snapshots int
	failFirst bool
}

func (s *membershipFacts) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	s.snapshots++
	if s.failFirst && s.snapshots == 1 {
		return errors.New("private snapshot failure")
	}
	return s.Tuples.ReadTupleSnapshot(ctx, fn)
}

func TestMembershipPolicySharesSnapshotAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, principal string
		failFirst       bool
		status, reads   int
	}{
		{"unauthenticated", "", false, 401, 0},
		{"outsider", "outsider", false, 403, 1},
		{"member", "member", false, 204, 1},
		{"platform administrator", "admin", false, 204, 1},
		{"admin check failure aborts member fallback", "member", true, 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &membershipFacts{Tuples: memory.NewTuples(), failFirst: tc.failFirst}
			if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{
				{Scope: tuples.On(platformResourceType, platformResourceID), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "admin"}},
				{Scope: tuples.On(demoResourceType, demoResourceID), Relation: "member", Subject: tuples.SubjectRef{Type: "user", ID: "member"}},
			}}); err != nil {
				t.Fatal(err)
			}
			components, err := authorization.New(authorization.Repositories{Tuples: store}, authorization.WithModel(authzSchema()))
			if err != nil {
				t.Fatal(err)
			}
			handler := requireMembership(components.HTTP)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/demo/members-only", nil)
			if tc.principal != "" {
				request = request.WithContext(sdk.WithPrincipal(request.Context(), sdk.Principal{Type: "user", ID: tc.principal}))
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || store.snapshots != tc.reads {
				t.Fatalf("status=%d snapshots=%d; want %d/%d; body=%s", response.Code, store.snapshots, tc.status, tc.reads, response.Body)
			}
		})
	}
}
