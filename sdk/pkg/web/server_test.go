package web

import (
	"errors"
	"strings"
	"testing"
)

// Origin is exercised from struct literals: sdk/pkg/web may not import
// sdk/pkg/environment (guard G12b, tests included), so the tag-driven
// path is proven in environment's own suite over a mirror struct.
func TestServerConfig_Origin(t *testing.T) {
	tests := []struct {
		name string
		cfg  ServerConfig
		want string
	}{
		{
			name: "public base url trailing slash trimmed",
			cfg:  ServerConfig{Host: "localhost", Port: "8080", PublicBaseURL: "https://app.example.com/"},
			want: "https://app.example.com",
		},
		{
			name: "public base url clean",
			cfg:  ServerConfig{Host: "localhost", Port: "8080", PublicBaseURL: "https://app.example.com"},
			want: "https://app.example.com",
		},
		{
			name: "public base url unset falls back to the listen address",
			cfg:  ServerConfig{Host: "localhost", Port: "8080"},
			want: "http://localhost:8080",
		},
		{
			name: "empty host reads as localhost",
			cfg:  ServerConfig{Port: "8080"},
			want: "http://localhost:8080",
		},
		{
			name: "bind-all host reads as localhost",
			cfg:  ServerConfig{Host: "0.0.0.0", Port: "9000"},
			want: "http://localhost:9000",
		},
		{
			name: "public base url wins over a bind-all host",
			cfg:  ServerConfig{Host: "0.0.0.0", Port: "9000", PublicBaseURL: "https://edge.example.com//"},
			want: "https://edge.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Origin(); got != tc.want {
				t.Errorf("Origin() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServerConfig_OriginDoesNotMutateConfig(t *testing.T) {
	cfg := ServerConfig{Host: "0.0.0.0", Port: "8080"}
	if got := cfg.Origin(); got != "http://localhost:8080" {
		t.Fatalf("Origin() = %q, want %q", got, "http://localhost:8080")
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q (Origin must not alter the listen address)", cfg.Host, "0.0.0.0")
	}
}

func TestServerConfig_ValidateProduction(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		wantErr error
	}{
		{"unset", "", ErrPublicBaseURLRequired},
		{"slash only", "/", ErrPublicBaseURLRequired},
		{"http", "http://app.example.com", ErrPublicBaseURLInsecure},
		{"no scheme", "app.example.com", ErrPublicBaseURLInsecure},
		{"userinfo", "https://admin:hunter2@app.example.com", ErrPublicBaseURLMalformed},
		{"username only", "https://admin@app.example.com", ErrPublicBaseURLMalformed},
		{"path", "https://app.example.com/app", ErrPublicBaseURLMalformed},
		{"double slash path", "https://app.example.com//", ErrPublicBaseURLMalformed},
		{"escaped slash path", "https://app.example.com/%2F", ErrPublicBaseURLMalformed},
		{"query", "https://app.example.com?x=1", ErrPublicBaseURLMalformed},
		{"empty query", "https://app.example.com/?", ErrPublicBaseURLMalformed},
		{"fragment", "https://app.example.com#top", ErrPublicBaseURLMalformed},
		{"empty fragment", "https://app.example.com/#", ErrPublicBaseURLMalformed},
		{"no host", "https:///", ErrPublicBaseURLMalformed},
		{"opaque", "https:app.example.com", ErrPublicBaseURLMalformed},
		{"unparseable", "https://app.example.com:port", ErrPublicBaseURLMalformed},
		{"valid", "https://app.example.com", nil},
		{"valid trailing slash", "https://app.example.com/", nil},
		{"valid with port", "https://app.example.com:8443", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ServerConfig{PublicBaseURL: tc.base}.ValidateProduction()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateProduction() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateProduction() = %v, want errors.Is %v", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
				t.Errorf("error %q does not name PUBLIC_BASE_URL", err)
			}
			for _, secret := range []string{"admin", "hunter2", tc.base} {
				if tc.base != "" && strings.Contains(err.Error(), secret) {
					t.Errorf("error %q echoes %q from the configured value", err, secret)
				}
			}
		})
	}
}
