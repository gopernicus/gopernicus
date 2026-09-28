package authenticationhttp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func TestHTMLFormOriginPolicy(t *testing.T) {
	policy, err := web.NewOriginPolicy(web.OriginPolicyConfig{Namespaces: []web.OriginNamespace{
		{Scheme: "https", Suffix: "flight.example.com", Ports: []int{443, 8443}, Reserved: []string{"api"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := newAccountFormFixture(t)
	f.seedLoginUser("origin-policy-user", "origin-policy@example.com")
	adapter, err := New(f.svc.Service, environment.ModeDevelopment, WithBrowser(BrowserConfig{
		AllowedOrigins: []string{"https://app.example.com"},
		OriginPolicy:   policy,
		Views:          stubViews{},
	}))
	if err != nil {
		t.Fatal(err)
	}
	h := web.NewWebHandler()
	if err := adapter.Register(pockets.Mount{Router: h}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"email": {"origin-policy@example.com"}, "password": {"password123456789"}}
	for _, tt := range []struct {
		name    string
		origins []string
		want    int
	}{
		{"namespace", []string{"https://acme.flight.example.com"}, http.StatusSeeOther},
		{"namespace allowed port", []string{"https://acme.flight.example.com:8443"}, http.StatusSeeOther},
		{"exact list", []string{"https://app.example.com"}, http.StatusSeeOther},
		{"reserved", []string{"https://api.flight.example.com"}, http.StatusForbidden},
		{"extra label", []string{"https://a.b.flight.example.com"}, http.StatusForbidden},
		{"lookalike", []string{"https://acme.flight.example.com.evil.com"}, http.StatusForbidden},
		{"bracketed DNS", []string{"https://[acme.flight.example.com]"}, http.StatusForbidden},
		{"bracketed DNS with port", []string{"https://[acme.flight.example.com]:8443"}, http.StatusForbidden},
		{"repeated", []string{"https://acme.flight.example.com", "https://acme.flight.example.com"}, http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Sec-Fetch-Site", "same-site")
			for _, origin := range tt.origins {
				r.Header.Add("Origin", origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body)
			}
			if got := sessionCookie(rec); (got != nil) != (tt.want == http.StatusSeeOther) {
				t.Fatalf("session cookie = %v, status = %d", got != nil, rec.Code)
			}
		})
	}
}
