package authorizationhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func ExampleAdapter_Require() {
	store := memory.NewTuples()
	err := store.ApplyTuples(context.Background(), tuples.Changes{Add: []tuples.Tuple{
		{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "admin"}},
		{Scope: tuples.On("document", "one"), Relation: "editor", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}},
		{Scope: tuples.On("document", "one"), Relation: "reviewer", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}},
	}})
	if err != nil {
		panic(err)
	}
	components, err := authorization.New(authorization.Repositories{Tuples: store})
	if err != nil {
		panic(err)
	}
	document := authorizationhttp.Path("document", "documentID")
	guard := components.HTTP.Require(authorizationhttp.Any(
		authorizationhttp.HasRole("admin", authorizationhttp.Global()),
		authorizationhttp.All(
			authorizationhttp.HasRole("editor", document),
			authorizationhttp.HasRole("reviewer", document),
		),
	))
	mux := http.NewServeMux()
	mux.Handle("GET /documents/{documentID}", guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	for _, user := range []string{"", "outsider", "alice", "admin"} {
		r := httptest.NewRequest(http.MethodGet, "/documents/one", nil)
		if user != "" {
			r = r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: "user", ID: user}))
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, r)
		fmt.Println(response.Code)
	}
	// Output:
	// 401
	// 403
	// 204
	// 204
}

func ExampleWithDeniedHandler() {
	components, err := authorization.New(authorization.Repositories{Tuples: memory.NewTuples()})
	if err != nil {
		panic(err)
	}
	guard := components.HTTP.Require(
		authorizationhttp.HasRole("viewer", authorizationhttp.Fixed("document", "one")),
		authorizationhttp.WithDeniedHandler(http.NotFoundHandler()),
	)
	handler := guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/documents/one", nil)
	r = r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: "user", ID: "outsider"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	fmt.Println(response.Code)
	// Output: 404
}
