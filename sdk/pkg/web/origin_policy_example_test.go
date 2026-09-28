package web

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
)

// ExampleNewOriginPolicy builds one policy at boot and hands the same value to
// CORS and to every pocket that gates on Origin (the authentication pocket's
// BrowserConfig.OriginPolicy), so CORS and CSRF admission cannot drift.
func ExampleNewOriginPolicy() {
	clients, err := ParseOriginNamespace("https://*.flight.example.com")
	if err != nil {
		log.Fatal(err)
	}
	clients.Reserved = []string{"accounts", "api"}

	policy, err := NewOriginPolicy(OriginPolicyConfig{
		Exact:      []string{"https://accounts.flight.example.com"},
		Namespaces: []OriginNamespace{clients},
	})
	if err != nil {
		log.Fatal(err) // fail the boot: a zero policy would admit nothing
	}

	cors := CORSWithConfig(CORSConfig{OriginPolicy: policy})
	// authentication.WithBrowser(authentication.BrowserConfig{OriginPolicy: policy, ...})

	h := cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, origin := range []string{
		"https://acme.flight.example.com",
		"https://accounts.flight.example.com",
		"https://api.flight.example.com",
		"https://evilflight.example.com",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		fmt.Printf("%s credentials=%q\n", origin, rec.Header().Get("Access-Control-Allow-Credentials"))
	}
	// Output:
	// https://acme.flight.example.com credentials="true"
	// https://accounts.flight.example.com credentials="true"
	// https://api.flight.example.com credentials=""
	// https://evilflight.example.com credentials=""
}
