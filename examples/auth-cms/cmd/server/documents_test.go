package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	documents "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func TestDocumentRouteComposition(t *testing.T) {
	components, err := newAuthorization(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedAuthorization(t.Context(), components.SystemMutator); err != nil {
		t.Fatal(err)
	}
	router := web.NewWebHandler()
	// The fixture resolves the bootstrap identity; production uses live auth.
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := sdk.WithPrincipal(r.Context(), sdk.Principal{Type: seedOwnerSubject.Type, ID: seedOwnerSubject.ID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	if err := registerDocumentRoutes(t.Context(), router, authenticate, components.Decisions, components.SystemMutator); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/demo/tenants/demo/documents?q=a&sort=-name&limit=2")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page documents.Page
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(page.Items) != 2 || page.Items[0].Name != "Beta" || page.Items[1].Name != "Alpha" || page.HasMore {
		t.Fatalf("wired document route: status=%d page=%+v", response.StatusCode, page)
	}
}
