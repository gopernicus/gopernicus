package authentication

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

const testSessionCookie = "session"

// csrfReq builds a POST request with the given CSRF/origin surface. A blank
// csrfCookie/csrfHeader is omitted so the missing-token cases are exercised.
type csrfReq struct {
	origin        string
	secFetchSite  string
	bearer        bool
	sessionCookie bool
	csrfCookie    string
	csrfHeader    string
}

func (c csrfReq) build() *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/auth/x", strings.NewReader("{}"))
	if c.origin != "" {
		r.Header.Set("Origin", c.origin)
	}
	if c.secFetchSite != "" {
		r.Header.Set("Sec-Fetch-Site", c.secFetchSite)
	}
	if c.bearer {
		r.Header.Set("Authorization", "Bearer tok")
	}
	if c.sessionCookie {
		r.AddCookie(&http.Cookie{Name: testSessionCookie, Value: "sess"})
	}
	if c.csrfCookie != "" {
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: c.csrfCookie})
	}
	if c.csrfHeader != "" {
		r.Header.Set(csrfHeaderName, c.csrfHeader)
	}
	return r
}

func TestRequireBrowserSafeMutation(t *testing.T) {
	mw := requireBrowserSafeMutation(csrfConfig{
		allowedOrigins:    []string{"https://app.example.com"},
		sessionCookieName: testSessionCookie,
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(next)

	tests := []struct {
		name string
		req  csrfReq
		want int
	}{
		{
			name: "same-origin with matching token passes",
			req:  csrfReq{secFetchSite: "same-origin", sessionCookie: true, csrfCookie: "abc", csrfHeader: "abc"},
			want: http.StatusOK,
		},
		{
			name: "allowlisted cross-origin with matching token passes",
			req:  csrfReq{origin: "https://app.example.com", secFetchSite: "cross-site", sessionCookie: true, csrfCookie: "abc", csrfHeader: "abc"},
			want: http.StatusOK,
		},
		{
			name: "missing csrf token is rejected",
			req:  csrfReq{secFetchSite: "same-origin", sessionCookie: true},
			want: http.StatusForbidden,
		},
		{
			name: "mismatched csrf token is rejected",
			req:  csrfReq{secFetchSite: "same-origin", sessionCookie: true, csrfCookie: "abc", csrfHeader: "def"},
			want: http.StatusForbidden,
		},
		{
			name: "cross-site fetch is rejected even with a matching token",
			req:  csrfReq{origin: "https://evil.example.com", secFetchSite: "cross-site", sessionCookie: true, csrfCookie: "abc", csrfHeader: "abc"},
			want: http.StatusForbidden,
		},
		{
			name: "non-allowlisted origin without sec-fetch-site is rejected",
			req:  csrfReq{origin: "https://evil.example.com", sessionCookie: true, csrfCookie: "abc", csrfHeader: "abc"},
			want: http.StatusForbidden,
		},
		{
			name: "bearer-only request skips the csrf gate",
			req:  csrfReq{bearer: true, secFetchSite: "cross-site", origin: "https://evil.example.com"},
			want: http.StatusOK,
		},
		{
			name: "bearer plus session cookie stays behind the gate",
			req:  csrfReq{bearer: true, sessionCookie: true, secFetchSite: "same-origin"},
			want: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tt.req.build())
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestIssueCSRFTokenRoundTrips(t *testing.T) {
	rec := httptest.NewRecorder()
	token, err := issueCSRFToken(rec)
	if err != nil {
		t.Fatalf("issueCSRFToken: %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	var got *http.Cookie
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == csrfCookieName {
			got = c
		}
	}
	if got == nil {
		t.Fatal("csrf cookie not set")
	}
	if got.Value != token {
		t.Fatalf("cookie value = %q, want %q", got.Value, token)
	}
	if got.HttpOnly {
		t.Fatal("csrf cookie must be readable by the page script (not HttpOnly)")
	}
	if !got.Secure {
		t.Fatal("csrf cookie must be Secure")
	}

	// The minted token satisfies the gate as a matching double-submit pair.
	mw := requireBrowserSafeMutation(csrfConfig{sessionCookieName: testSessionCookie})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := csrfReq{secFetchSite: "same-origin", sessionCookie: true, csrfCookie: token, csrfHeader: token}.build()
	rec2 := httptest.NewRecorder()
	mw(next).ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("minted token rejected: status %d", rec2.Code)
	}
}

func TestOriginAllowedIgnoresWildcard(t *testing.T) {
	if originAllowed("https://evil.example.com", []string{"*"}) {
		t.Fatal("wildcard entry must not authorize an arbitrary origin")
	}
	if !originAllowed("https://app.example.com", []string{"*", "https://app.example.com"}) {
		t.Fatal("exact allowlist match should pass despite a wildcard entry")
	}
	if originAllowed("", []string{"https://app.example.com"}) {
		t.Fatal("empty origin must not match")
	}
}

// TestCORSNeverWildcardWithCredentials pins the design §9.1 rule at the sdk
// middleware the host wires: a wildcard-configured origin echoes the request
// origin but never sets Access-Control-Allow-Credentials.
func TestCORSNeverWildcardWithCredentials(t *testing.T) {
	mw := web.CORSMiddleware([]string{"*"})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r := httptest.NewRequest(http.MethodPost, "/auth/x", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, r)
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("wildcard CORS set credentials header = %q", got)
	}
}

func TestRequireJSON(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		ok          bool
	}{
		{"exact json", "application/json", true},
		{"json with charset", "application/json; charset=utf-8", true},
		{"form", "application/x-www-form-urlencoded", false},
		{"missing", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/x", nil)
			if tt.contentType != "" {
				r.Header.Set("Content-Type", tt.contentType)
			}
			rec := httptest.NewRecorder()
			if got := requireJSON(rec, r); got != tt.ok {
				t.Fatalf("requireJSON = %v, want %v", got, tt.ok)
			}
			if !tt.ok && rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d, want 415", rec.Code)
			}
		})
	}
}

func TestStrictJSONBody(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name     string
		body     string
		maxBytes int64
		ok       bool
		want     int
	}{
		{"valid", `{"name":"a"}`, maxJSONBodyBytes, true, http.StatusOK},
		{"unknown field", `{"name":"a","x":1}`, maxJSONBodyBytes, false, http.StatusBadRequest},
		{"trailing object", `{"name":"a"}{"name":"b"}`, maxJSONBodyBytes, false, http.StatusBadRequest},
		{"trailing token", `{"name":"a"} 5`, maxJSONBodyBytes, false, http.StatusBadRequest},
		{"malformed", `{"name":`, maxJSONBodyBytes, false, http.StatusBadRequest},
		{"oversized", `{"name":"aaaaaaaaaa"}`, 8, false, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/x", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			var dst payload
			got := strictJSONBody(rec, r, &dst, tt.maxBytes)
			if got != tt.ok {
				t.Fatalf("strictJSONBody = %v, want %v (status %d)", got, tt.ok, rec.Code)
			}
			if !tt.ok && rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestWriteNoStore(t *testing.T) {
	rec := httptest.NewRecorder()
	writeNoStore(rec)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"ipv4 ignores spoofed forwarding header", "203.0.113.7:5555", "9.9.9.9", "203.0.113.7"},
		{"ipv6 remote addr", "[2001:db8::1]:5555", "", "2001:db8::1"},
		{"ipv4 no port malformed returned verbatim", "203.0.113.9", "", "203.0.113.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/auth/x", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := clientIP(r); got != tt.want {
				t.Fatalf("clientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSpoofedForwardedHeaderCannotRotateLimiterBucket proves the end-to-end
// invariant behind the login rate limiter (design §4.4): without a trusted proxy,
// clientInfoMiddleware stamps the RemoteAddr onto the client-info carrier — the very
// value loginKey uses as the bucket IP — and a raw X-Forwarded-For is ignored. So an
// attacker cannot forge a forwarding header to rotate off a victim's rate-limit
// bucket.
func TestSpoofedForwardedHeaderCannotRotateLimiterBucket(t *testing.T) {
	var carrierIP string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		carrierIP, _ = authsvc.ClientInfoFromContext(r.Context())
	})
	handler := clientInfoMiddleware(next)

	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "203.0.113.7:5555"
	r.Header.Set("X-Forwarded-For", "9.9.9.9") // attacker-supplied, no trusted proxy
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if carrierIP != "203.0.113.7" {
		t.Fatalf("carrier IP = %q, want RemoteAddr 203.0.113.7 (spoofed X-Forwarded-For must be ignored)", carrierIP)
	}
}

// TestClientIPUsesTrustedProxyResolution proves that when web.TrustProxies has
// run, clientIP takes the resolved IP from the context — the trusted-proxy
// configuration is the only source of forwarded client IPs.
func TestClientIPUsesTrustedProxyResolution(t *testing.T) {
	var got string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = clientIP(r)
	})
	handler := web.TrustProxies(1)(next)

	r := httptest.NewRequest(http.MethodGet, "/auth/x", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "198.51.100.23")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if got != "198.51.100.23" {
		t.Fatalf("clientIP = %q, want the trusted-proxy-resolved 198.51.100.23", got)
	}
}
