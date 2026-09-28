package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddleware_PreflightShortCircuit(t *testing.T) {
	nextCalled := false
	h := CORSMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if nextCalled {
		t.Error("preflight OPTIONS should short-circuit, next was called")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("ACAO = %q, want echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Accept, Content-Type, Authorization" {
		t.Errorf("allow-headers = %q, want the compatibility default", got)
	}
}

// TestCORSWithConfig_AllowedHeadersReplaceDefault is the host-opt-in seam: a
// non-nil list replaces the default, so a host can allow its own echo header
// (a CSRF token header, here) without the sdk knowing that header exists.
func TestCORSWithConfig_AllowedHeadersReplaceDefault(t *testing.T) {
	h := CORSWithConfig(CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedHeaders: []string{"Accept", "Content-Type", "Authorization", "X-CSRF-Token"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/auth/password/change", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type, x-csrf-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	want := "Accept, Content-Type, Authorization, X-CSRF-Token"
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != want {
		t.Errorf("allow-headers = %q, want %q", got, want)
	}
}

func TestCORSMiddleware_ExposesRequestIDByDefault(t *testing.T) {
	h := CORSMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != RequestIDHeader {
		t.Errorf("expose-headers = %q, want %q", got, RequestIDHeader)
	}
}

func TestCORSWithConfig_ExposedHeaders(t *testing.T) {
	tests := []struct {
		name    string
		exposed []string
		want    string
	}{
		{"nil defaults to request id", nil, RequestIDHeader},
		{"explicit empty suppresses", []string{}, ""},
		{"explicit list replaces", []string{"X-Total-Count", "Link"}, "X-Total-Count, Link"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := CORSWithConfig(CORSConfig{
				AllowedOrigins: []string{"https://app.example.com"},
				ExposedHeaders: tt.exposed,
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Origin", "https://app.example.com")
			h.ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Expose-Headers"); got != tt.want {
				t.Errorf("expose-headers = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCORSMiddleware_VaryOriginOnAllowedAndRejected(t *testing.T) {
	h := CORSMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	for _, origin := range []string{"https://app.example.com", "https://evil.example.com", ""} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		h.ServeHTTP(rec, req)

		if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Origin" {
			t.Errorf("origin %q: Vary = %v, want [Origin]", origin, got)
		}
	}
}

func TestCORSMiddleware_VaryPreservesExistingDimensions(t *testing.T) {
	// An upstream middleware already recorded a Vary dimension; CORS must extend
	// it, never overwrite it, and never duplicate Origin when it is already listed.
	preset := func(value string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", value)
			CORSMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, r)
		})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	preset("Accept-Encoding").ServeHTTP(rec, req)

	got := rec.Header().Values("Vary")
	if len(got) != 2 || got[0] != "Accept-Encoding" || got[1] != "Origin" {
		t.Errorf("Vary = %v, want [Accept-Encoding Origin]", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	preset("accept-encoding, origin").ServeHTTP(rec, req)

	got = rec.Header().Values("Vary")
	if len(got) != 1 || got[0] != "accept-encoding, origin" {
		t.Errorf("Vary = %v, want the existing value untouched (Origin already listed)", got)
	}
}

func TestCORSMiddleware_OnlyGenuinePreflightShortCircuits(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		requestedBy string
		wantNext    bool
	}{
		{"genuine preflight", "https://app.example.com", http.MethodPost, false},
		{"options without request-method", "https://app.example.com", "", true},
		{"options without origin", "", http.MethodPost, true},
		{"bare options", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			h := CORSMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.Header().Set("Allow", "GET, OPTIONS")
				w.WriteHeader(http.StatusOK)
			}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodOptions, "/", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.requestedBy != "" {
				req.Header.Set("Access-Control-Request-Method", tt.requestedBy)
			}
			h.ServeHTTP(rec, req)

			if nextCalled != tt.wantNext {
				t.Fatalf("next called = %v, want %v", nextCalled, tt.wantNext)
			}
			if tt.wantNext && rec.Code != http.StatusOK {
				t.Errorf("status = %d, want the handler's 200", rec.Code)
			}
			if !tt.wantNext && rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204", rec.Code)
			}
		})
	}
}

func TestCORSMiddleware_AllowlistedOriginEchoWithCredentials(t *testing.T) {
	h := CORSMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("ACAO = %q, want echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("credentials = %q, want true for explicit allowlist match", got)
	}
}

func TestCORSMiddleware_WildcardEchoWithoutCredentials(t *testing.T) {
	h := CORSMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example.com" {
		t.Errorf("ACAO = %q, want echoed origin under wildcard", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("credentials = %q, want unset under wildcard config", got)
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	nextCalled := false
	h := CORSMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want no CORS headers for disallowed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("credentials = %q, want unset for disallowed origin", got)
	}
	if !nextCalled {
		t.Error("non-preflight request should reach next even when origin is disallowed")
	}
}

func TestDefaultHeadersMiddleware_Applies(t *testing.T) {
	h := DefaultHeadersMiddleware(map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestDefaultHeadersMiddleware_HandlerCanOverride(t *testing.T) {
	h := DefaultHeadersMiddleware(map[string]string{
		"X-Frame-Options": "DENY",
		"Cache-Control":   "no-store",
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want handler override to win", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want default preserved when handler does not set it", got)
	}
}

func TestNoStore_WritesCacheControl(t *testing.T) {
	h := NoStore()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "" {
		t.Errorf("Pragma = %q, want no Pragma header", got)
	}
	if got := rec.Header().Get("Expires"); got != "" {
		t.Errorf("Expires = %q, want no Expires header", got)
	}
}

func TestNoStore_HandlerCanOverride(t *testing.T) {
	h := NoStore()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want handler override to win", got)
	}
}

func TestCORSWithConfig_OriginPolicy(t *testing.T) {
	policy, err := NewOriginPolicy(OriginPolicyConfig{
		Exact: []string{"https://[2001:db8::1]", "https://[2001:db8::1]:8443"},
		Namespaces: []OriginNamespace{
			{Scheme: "https", Suffix: "flight.example.com", Ports: []int{443, 8443}, Reserved: []string{"api"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		allowed   []string
		method    string
		origin    string
		wantACAO  string
		wantCreds string
		wantCode  int
	}{
		{"namespace match is credentialed", nil, http.MethodGet, "https://Acme.flight.example.com", "https://Acme.flight.example.com", "true", 200},
		{"allowed port is credentialed", nil, http.MethodGet, "https://acme.flight.example.com:8443", "https://acme.flight.example.com:8443", "true", 200},
		{"bracketed DNS refused", nil, http.MethodGet, "https://[acme.flight.example.com]", "", "", 200},
		{"bracketed DNS with allowed port refused", nil, http.MethodGet, "https://[acme.flight.example.com]:8443", "", "", 200},
		{"IPv6 exact is credentialed", nil, http.MethodGet, "https://[2001:db8::1]", "https://[2001:db8::1]", "true", 200},
		{"IPv6 exact with port is credentialed", nil, http.MethodGet, "https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443", "true", 200},
		{"policy wins over wildcard", []string{"*"}, http.MethodGet, "https://acme.flight.example.com", "https://acme.flight.example.com", "true", 200},
		{"wildcard still uncredentialed off-policy", []string{"*"}, http.MethodGet, "https://other.example.com", "https://other.example.com", "", 200},
		{"exact list still credentialed", []string{"https://app.example.com"}, http.MethodGet, "https://app.example.com", "https://app.example.com", "true", 200},
		{"reserved label refused", nil, http.MethodGet, "https://api.flight.example.com", "", "", 200},
		{"lookalike refused", nil, http.MethodGet, "https://acme.flight.example.com.evil.com", "", "", 200},
		{"preflight match", nil, http.MethodOptions, "https://acme.flight.example.com", "https://acme.flight.example.com", "true", 204},
		{"preflight allowed port", nil, http.MethodOptions, "https://acme.flight.example.com:8443", "https://acme.flight.example.com:8443", "true", 204},
		{"preflight bracketed DNS refused", nil, http.MethodOptions, "https://[acme.flight.example.com]", "", "", 204},
		{"preflight bracketed DNS with allowed port refused", nil, http.MethodOptions, "https://[acme.flight.example.com]:8443", "", "", 204},
		{"preflight IPv6 exact", nil, http.MethodOptions, "https://[2001:db8::1]", "https://[2001:db8::1]", "true", 204},
		{"preflight IPv6 exact with port", nil, http.MethodOptions, "https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443", "true", 204},
		{"preflight miss", nil, http.MethodOptions, "https://a.b.flight.example.com", "", "", 204},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := CORSWithConfig(CORSConfig{AllowedOrigins: tt.allowed, OriginPolicy: policy})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			req := httptest.NewRequest(tt.method, "/", nil)
			req.Header.Set("Origin", tt.origin)
			if tt.method == http.MethodOptions {
				req.Header.Set("Access-Control-Request-Method", http.MethodPost)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Errorf("ACAO = %q, want %q", got, tt.wantACAO)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != tt.wantCreds {
				t.Errorf("credentials = %q, want %q", got, tt.wantCreds)
			}
			if got := rec.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, want Origin", got)
			}
		})
	}
}
