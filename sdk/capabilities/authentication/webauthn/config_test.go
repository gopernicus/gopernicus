package webauthn

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

func testPolicy(t *testing.T, exact []string, namespaces ...string) web.OriginPolicy {
	t.Helper()
	cfg := web.OriginPolicyConfig{Exact: exact}
	for _, pattern := range namespaces {
		ns, err := web.ParseOriginNamespace(pattern)
		if err != nil {
			t.Fatalf("ParseOriginNamespace(%q): %v", pattern, err)
		}
		cfg.Namespaces = append(cfg.Namespaces, ns)
	}
	p, err := web.NewOriginPolicy(cfg)
	if err != nil {
		t.Fatalf("NewOriginPolicy: %v", err)
	}
	return p
}

func TestConfigValidateAccepts(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"exact and namespace under the RP ID", Config{
			RPID: "example.com", RPName: "Example",
			Origins: testPolicy(t, []string{"https://example.com", "https://login.example.com"}, "https://*.tenants.example.com"),
		}},
		{"localhost on a port", Config{RPID: "localhost", RPName: "Dev", Origins: testPolicy(t, []string{"http://localhost:8082"})}},
		{"subdomain RP ID", Config{
			RPID: "login.example.com", RPName: "Example",
			Origins: testPolicy(t, []string{"https://login.example.com"}, "https://*.login.example.com"),
		}},
		{"punycode", Config{RPID: "xn--bcher-kva.example", RPName: "B", Origins: testPolicy(t, []string{"https://xn--bcher-kva.example"})}},
		{"timeout at the maximum", Config{RPID: "example.com", RPName: "E", Origins: testPolicy(t, []string{"https://example.com"}), Timeout: MaxTimeout}},
		{"short timeout", Config{RPID: "example.com", RPName: "E", Origins: testPolicy(t, []string{"https://example.com"}), Timeout: time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestConfigValidateRefuses(t *testing.T) {
	origins := testPolicy(t, []string{"https://example.com"})
	tests := []struct {
		name      string
		cfg       Config
		outside   string // non-empty: also ErrOriginOutsideRPID, naming this host
		wantInMsg string
	}{
		{name: "empty RP ID", cfg: Config{RPName: "E", Origins: origins}},
		{name: "uppercase RP ID", cfg: Config{RPID: "Example.com", RPName: "E", Origins: origins}},
		{name: "scheme", cfg: Config{RPID: "https://example.com", RPName: "E", Origins: origins}},
		{name: "port", cfg: Config{RPID: "example.com:443", RPName: "E", Origins: origins}},
		{name: "path", cfg: Config{RPID: "example.com/login", RPName: "E", Origins: origins}},
		{name: "trailing dot", cfg: Config{RPID: "example.com.", RPName: "E", Origins: origins}},
		{name: "empty label", cfg: Config{RPID: "example..com", RPName: "E", Origins: origins}},
		{name: "underscore", cfg: Config{RPID: "ex_ample.com", RPName: "E", Origins: origins}},
		{name: "IPv4", cfg: Config{RPID: "127.0.0.1", RPName: "E", Origins: testPolicy(t, []string{"https://127.0.0.1"})}},
		{name: "IPv6", cfg: Config{RPID: "::1", RPName: "E", Origins: testPolicy(t, []string{"https://[::1]"})}},
		{name: "overlong", cfg: Config{RPID: strings.Repeat("a.", 127) + "com", RPName: "E", Origins: origins}},
		{name: "empty RP name", cfg: Config{RPID: "example.com", Origins: origins}},
		{name: "blank RP name", cfg: Config{RPID: "example.com", RPName: "  ", Origins: origins}},
		{name: "zero origins", cfg: Config{RPID: "example.com", RPName: "E"}},
		{
			name: "exact origin outside", outside: "example.org",
			cfg: Config{RPID: "example.com", RPName: "E", Origins: testPolicy(t, []string{"https://example.com", "https://example.org"})},
		},
		{
			name: "namespace outside", outside: "other.test",
			cfg: Config{RPID: "example.com", RPName: "E", Origins: testPolicy(t, nil, "https://*.other.test")},
		},
		{
			name: "suffix without a label boundary", outside: "notexample.com",
			cfg: Config{RPID: "example.com", RPName: "E", Origins: testPolicy(t, []string{"https://notexample.com"})},
		},
		{
			name: "origin above the RP ID", outside: "example.com",
			cfg: Config{RPID: "login.example.com", RPName: "E", Origins: origins},
		},
		{
			name: "IP origin beside a domain RP ID", outside: "127.0.0.1",
			cfg: Config{RPID: "localhost", RPName: "E", Origins: testPolicy(t, []string{"http://localhost:8082", "http://127.0.0.1:8082"})},
		},
		{name: "negative timeout", cfg: Config{RPID: "example.com", RPName: "E", Origins: origins, Timeout: -time.Second}},
		{name: "timeout above the maximum", cfg: Config{RPID: "example.com", RPName: "E", Origins: origins, Timeout: 6 * time.Minute}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Validate() = %v, want sdk.ErrInvalidInput", err)
			}
			if tt.outside == "" {
				if errors.Is(err, ErrOriginOutsideRPID) {
					t.Errorf("Validate() = %v, unexpectedly ErrOriginOutsideRPID", err)
				}
				return
			}
			if !errors.Is(err, ErrOriginOutsideRPID) {
				t.Errorf("Validate() = %v, want ErrOriginOutsideRPID", err)
			}
			if !strings.Contains(err.Error(), `"`+tt.outside+`"`) {
				t.Errorf("Validate() = %q, want it to name %q", err, tt.outside)
			}
		})
	}
}
