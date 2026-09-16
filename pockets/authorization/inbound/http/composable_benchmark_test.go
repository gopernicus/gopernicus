package authorizationhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

// These include the memory snapshot and HTTP recorder costs. They measure the
// mounted request path; registration and model compilation happen before timing.
func BenchmarkComposableGuard(b *testing.B) {
	store := memory.NewTuples()
	seedGuard(b, store,
		guardFact(tuples.Global(), "admin", "user", "alice", ""),
		guardFact(tuples.On("document", "one"), "editor", "user", "alice", ""),
		guardFact(tuples.On("document", "one"), "reviewer", "user", "alice", ""),
	)
	adapter := guardAdapter(b, store, decisions.WithModel(guardModel()))
	document := Path("document", "documentID")
	for _, tc := range []struct {
		name   string
		policy Predicate
	}{
		{"global", HasRole("admin", Global())},
		{"two_exact_roles", All(HasRole("editor", document), HasRole("reviewer", document))},
		{"mixed", All(HasRole("reviewer", document), HasRelationship("editor", document), Can("edit", document))},
	} {
		b.Run(tc.name, func(b *testing.B) {
			handler := adapter.Require(tc.policy)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			request := guardRequest("alice")
			b.ReportAllocs()
			for b.Loop() {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != 204 {
					b.Fatalf("status=%d", response.Code)
				}
			}
		})
	}
}
