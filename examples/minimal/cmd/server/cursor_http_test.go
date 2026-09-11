package main

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// Exercise the shipped CMS route, service, memstore and HTML error mapping.
func TestEntryListInvalidCursorHTTP(t *testing.T) {
	router := htmxProofRouter(t)
	wrongType, err := list.EncodeCursor("created_at", "not-a-timestamp", "e1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token string
	}{
		{"invalid_base64", "not!base64"},
		{"incomplete_object", base64.URLEncoding.EncodeToString([]byte(`{"order_field":"created_at","pk":"e1"}`))},
		{"wrong_order_type", wrongType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := do(t, router, http.MethodGet, "/articles?cursor="+url.QueryEscape(tc.token), nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", response.Code, response.Body)
			}
		})
	}
}
