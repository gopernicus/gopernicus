package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
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

// logLines decodes each JSON line a test logger wrote.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("invalid JSON log line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func oneLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	entries := logLines(t, buf)
	if len(entries) != 1 {
		t.Fatalf("got %d log lines, want 1: %s", len(entries), buf.String())
	}
	return entries[0]
}

// attachPrincipal stands in for an authentication middleware: it attaches p on
// a DERIVED request the outer Logger never sees, then answers status itself
// when status is non-zero (a refusal that does not clear the principal).
func attachPrincipal(p sdk.Principal, status int) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(sdk.WithPrincipal(r.Context(), p))
			if status != 0 {
				w.WriteHeader(status)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func TestLogger_RequestAttributes(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := TrustProxies(1)(RequestID()(Logger(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))))

	req := httptest.NewRequest(http.MethodGet, "/healthz?token=QUERY-SECRET", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set(RequestIDHeader, "s6probe")
	req.Header.Set("User-Agent", strings.Repeat("U", 300))
	h.ServeHTTP(httptest.NewRecorder(), req)

	entry := oneLogLine(t, &logs)
	for key, want := range map[string]any{
		"path": "/healthz", "remote_addr": "127.0.0.1:5555", "client_ip": "203.0.113.9",
		"request_id": "s6probe", "user_agent": strings.Repeat("U", maxLoggedUserAgent),
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %v", key, entry[key], want)
		}
	}
	for _, key := range []string{"principal_type", "principal_id"} {
		if _, ok := entry[key]; ok {
			t.Errorf("%s logged for an unauthenticated request", key)
		}
	}
	if strings.Contains(logs.String(), "QUERY-SECRET") {
		t.Errorf("the query string reached the log: %s", logs.String())
	}
}

func TestLogger_OmitsUnknownAttributes(t *testing.T) {
	var logs bytes.Buffer
	h := Logger(slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Del("User-Agent")
	h.ServeHTTP(httptest.NewRecorder(), req)
	entry := oneLogLine(t, &logs)
	for _, key := range []string{"client_ip", "request_id", "user_agent", "principal_type", "principal_id"} {
		if _, ok := entry[key]; ok {
			t.Errorf("%s logged without its source (TrustProxies/RequestID/header/principal)", key)
		}
	}
}

func TestTruncateUTF8(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 256, "short"},
		{strings.Repeat("a", 255) + "é", 256, strings.Repeat("a", 255)},
		{strings.Repeat("a", 254) + "☕", 256, strings.Repeat("a", 254)},
		{strings.Repeat("é", 200), 256, strings.Repeat("é", 128)},
	} {
		got := truncateUTF8(tc.in, tc.n)
		if got != tc.want || len(got) > tc.n || !utf8.ValidString(got) {
			t.Errorf("truncateUTF8(%d bytes) = %d bytes %q, want %d bytes", len(tc.in), len(got), got, len(tc.want))
		}
	}
}

func TestLogger_Principal(t *testing.T) {
	user := sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u-1"}
	machine := sdk.Principal{Type: sdk.PrincipalTypeServiceAccount, ID: "sa-1"}
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, tc := range []struct {
		name       string
		handler    http.Handler
		outer      *sdk.Principal
		wantStatus float64
		want       *sdk.Principal
	}{
		{name: "set by an inner handler", handler: attachPrincipal(user, 0)(ok), wantStatus: 200, want: &user},
		{name: "refused kind is logged", handler: attachPrincipal(machine, http.StatusUnauthorized)(ok), wantStatus: 401, want: &machine},
		{name: "cleared before the response", handler: attachPrincipal(user, 0)(attachPrincipal(sdk.Principal{}, http.StatusUnauthorized)(ok)), wantStatus: 401},
		{name: "outer-attached fallback", handler: ok, outer: &user, wantStatus: 200, want: &user},
		{name: "outer-attached then replaced", handler: attachPrincipal(machine, 0)(ok), outer: &user, wantStatus: 200, want: &machine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := Logger(slog.New(slog.NewJSONHandler(&logs, nil)))(tc.handler)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.outer != nil {
				req = req.WithContext(sdk.WithPrincipal(req.Context(), *tc.outer))
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			entry := oneLogLine(t, &logs)
			if entry["status"] != tc.wantStatus {
				t.Errorf("status = %v, want %v", entry["status"], tc.wantStatus)
			}
			if tc.want == nil {
				if _, ok := entry["principal_type"]; ok {
					t.Errorf("principal logged: %v", entry)
				}
				return
			}
			if entry["principal_type"] != tc.want.Type || entry["principal_id"] != tc.want.ID {
				t.Errorf("principal = %v/%v, want %+v", entry["principal_type"], entry["principal_id"], *tc.want)
			}
		})
	}
}

// A nested router (the cms pocket's standalone stack) keeps the host's request
// id, and both Loggers report the principal attached inside the inner one.
func TestLogger_NestedAgreesWithOuter(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	user := sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u-1"}
	var innerID string
	inner := RequestID()(Logger(log)(attachPrincipal(user, 0)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		innerID, _ = sdk.RequestIDFromContext(r.Context())
	}))))
	h := RequestID()(Logger(log)(inner))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	entries := logLines(t, &logs)
	if len(entries) != 2 {
		t.Fatalf("got %d lines, want inner + outer: %s", len(entries), logs.String())
	}
	outerID := rec.Header().Get(RequestIDHeader)
	if outerID == "" || innerID != outerID || len(rec.Header().Values(RequestIDHeader)) != 1 {
		t.Fatalf("nested RequestID minted %q beside the host's %q", innerID, outerID)
	}
	for i, entry := range entries {
		if entry["request_id"] != outerID || entry["principal_type"] != user.Type || entry["principal_id"] != user.ID {
			t.Errorf("line %d = %v; want request_id %s and principal %+v", i, entry, outerID, user)
		}
	}
}

func TestRequestID_ReusesContextID(t *testing.T) {
	var seen string
	h := RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = sdk.RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "inbound-ignored")
	req = req.WithContext(sdk.WithRequestID(req.Context(), "host-id"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "host-id" || rec.Header().Get(RequestIDHeader) != "host-id" {
		t.Fatalf("context %q, header %q; want the id already on the context", seen, rec.Header().Get(RequestIDHeader))
	}
}

// Two requests on one base context that carries a long-lived capture (a CLI or
// cron install) each log their own principal.
func TestLogger_NoBleedAcrossRequestsOnOneBase(t *testing.T) {
	base, _ := sdk.WithPrincipalCapture(context.Background())
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Logger(log)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Who"); id != "" {
			sdk.WithPrincipal(r.Context(), sdk.Principal{Type: sdk.PrincipalTypeUser, ID: id})
		}
	}))
	for _, who := range []string{"a", "", "b"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(base)
		if who != "" {
			req.Header.Set("X-Who", who)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	entries := logLines(t, &logs)
	if len(entries) != 3 || entries[0]["principal_id"] != "a" || entries[2]["principal_id"] != "b" {
		t.Fatalf("lines = %v", entries)
	}
	if _, ok := entries[1]["principal_id"]; ok {
		t.Fatalf("an unauthenticated request logged another request's principal: %v", entries[1])
	}
}

// A goroutine the handler started may still attach a principal after the
// request line was written; the capture is atomic (run with -race).
func TestLogger_WriteAfterHandlerReturned(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	release := make(chan struct{})
	var wg sync.WaitGroup
	h := Logger(log)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		wg.Go(func() {
			<-release
			sdk.WithPrincipal(ctx, sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "late"})
		})
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	close(release)
	wg.Wait()
	if entry := oneLogLine(t, &logs); entry["principal_id"] != nil {
		t.Fatalf("line = %v; a late write must not be on the line already written", entry)
	}
}
