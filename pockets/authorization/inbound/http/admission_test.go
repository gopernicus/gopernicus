package authorizationhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func TestRequireRevocationAfterAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	store := memory.NewTuples()
	fact := guardFact(tuples.On("document", "one"), "editor", "user", "alice", "")
	seedGuard(t, store, fact)
	adapter := guardAdapter(t, store)
	admitted, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := adapter.Require(HasRole("editor", Path("document", "documentID")))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(admitted)
		select {
		case <-finish:
			w.WriteHeader(http.StatusNoContent)
		case <-ctx.Done():
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))
	first := httptest.NewRecorder()
	go func() {
		defer close(done)
		handler.ServeHTTP(first, guardRequest("alice"))
	}()
	select {
	case <-admitted:
	case <-ctx.Done():
		t.Fatal("authorized request did not enter handler")
	}
	// Revocation completes while the admitted operation is still running.
	if err := store.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{fact}}); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, guardRequest("alice"))
	if second.Code != http.StatusForbidden {
		t.Fatalf("request after revocation: status=%d", second.Code)
	}
	close(finish)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("admitted request did not complete")
	}
	if first.Code != http.StatusNoContent {
		t.Fatalf("admitted request was retroactively denied: status=%d", first.Code)
	}
}
