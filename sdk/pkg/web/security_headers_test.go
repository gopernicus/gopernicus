package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var securityHeaderNames = []string{
	"Strict-Transport-Security",
	"X-Content-Type-Options",
	"Referrer-Policy",
	"X-Frame-Options",
	"Permissions-Policy",
}

func serveSecurityHeaders(t *testing.T, mw Middleware, handler http.HandlerFunc) http.Header {
	t.Helper()
	if handler == nil {
		handler = func(http.ResponseWriter, *http.Request) {}
	}
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Header()
}

func TestSecurityHeaders_Matrix(t *testing.T) {
	const (
		permissions = "camera=(), microphone=(), geolocation=()"
		referrer    = "strict-origin-when-cross-origin"
	)
	tests := []struct {
		name string
		mw   Middleware
		want map[string]string // "" means the header is absent
	}{
		{
			name: "preset",
			mw:   SecurityHeaders(),
			want: map[string]string{
				"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
				"X-Content-Type-Options":    "nosniff",
				"Referrer-Policy":           referrer,
				"X-Frame-Options":           "DENY",
				"Permissions-Policy":        permissions,
			},
		},
		{
			name: "zero config is the preset",
			mw:   SecurityHeadersWithConfig(SecurityHeadersConfig{}),
			want: map[string]string{
				"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
				"X-Content-Type-Options":    "nosniff",
				"Referrer-Policy":           referrer,
				"X-Frame-Options":           "DENY",
				"Permissions-Policy":        permissions,
			},
		},
		{
			name: "HSTS-only adjustment keeps DENY",
			mw:   SecurityHeadersWithConfig(SecurityHeadersConfig{HSTSMaxAge: 48 * time.Hour}),
			want: map[string]string{
				"Strict-Transport-Security": "max-age=172800; includeSubDomains",
				"X-Frame-Options":           "DENY",
			},
		},
		{
			name: "frame options disabled",
			mw:   SecurityHeadersWithConfig(SecurityHeadersConfig{DisableFrameOptions: true, FrameOptions: "SAMEORIGIN"}),
			want: map[string]string{
				"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
				"X-Content-Type-Options":    "nosniff",
				"Referrer-Policy":           referrer,
				"X-Frame-Options":           "",
				"Permissions-Policy":        permissions,
			},
		},
		{
			name: "custom max-age, preload, SAMEORIGIN",
			mw: SecurityHeadersWithConfig(SecurityHeadersConfig{
				HSTSMaxAge:   2 * time.Hour,
				HSTSPreload:  true,
				FrameOptions: "SAMEORIGIN",
			}),
			want: map[string]string{
				"Strict-Transport-Security": "max-age=7200; includeSubDomains; preload",
				"X-Content-Type-Options":    "nosniff",
				"Referrer-Policy":           referrer,
				"X-Frame-Options":           "SAMEORIGIN",
				"Permissions-Policy":        permissions,
			},
		},
		{
			name: "negative max-age un-pins",
			mw:   SecurityHeadersWithConfig(SecurityHeadersConfig{HSTSMaxAge: -1}),
			want: map[string]string{
				"Strict-Transport-Security": "max-age=0; includeSubDomains",
				"X-Frame-Options":           "DENY",
			},
		},
		{
			name: "subdomains excluded",
			mw:   SecurityHeadersWithConfig(SecurityHeadersConfig{HSTSExcludeSubDomains: true}),
			want: map[string]string{
				"Strict-Transport-Security": "max-age=31536000",
			},
		},
		{
			name: "HSTS disabled",
			mw:   SecurityHeadersWithConfig(SecurityHeadersConfig{DisableHSTS: true, HSTSPreload: true}),
			want: map[string]string{
				"Strict-Transport-Security": "",
				"X-Content-Type-Options":    "nosniff",
				"X-Frame-Options":           "DENY",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serveSecurityHeaders(t, tt.mw, nil)
			for name, want := range tt.want {
				if values := got.Values(name); want == "" && len(values) != 0 {
					t.Errorf("%s = %q, want absent", name, values)
				} else if want != "" && got.Get(name) != want {
					t.Errorf("%s = %q, want %q", name, got.Get(name), want)
				}
			}
			if csp := got.Get("Content-Security-Policy"); csp != "" {
				t.Errorf("Content-Security-Policy = %q, want none: pages own CSP", csp)
			}
		})
	}
}

func TestSecurityHeaders_HandlerCanSetAndDelete(t *testing.T) {
	for _, name := range securityHeaderNames {
		t.Run("set "+name, func(t *testing.T) {
			got := serveSecurityHeaders(t, SecurityHeaders(), func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(name, "handler-value")
			})
			if values := got.Values(name); len(values) != 1 || values[0] != "handler-value" {
				t.Errorf("%s = %q, want the handler's value alone", name, values)
			}
		})
		t.Run("del "+name, func(t *testing.T) {
			got := serveSecurityHeaders(t, SecurityHeaders(), func(w http.ResponseWriter, r *http.Request) {
				w.Header().Del(name)
			})
			if values := got.Values(name); len(values) != 0 {
				t.Errorf("%s = %q, want the handler's Del to win", name, values)
			}
			for _, other := range securityHeaderNames {
				if other != name && got.Get(other) == "" {
					t.Errorf("%s missing after the handler deleted only %s", other, name)
				}
			}
		})
	}
}
