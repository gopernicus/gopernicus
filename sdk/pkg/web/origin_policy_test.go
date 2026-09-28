package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func testOriginPolicy(t *testing.T) OriginPolicy {
	t.Helper()
	p, err := NewOriginPolicy(OriginPolicyConfig{
		Exact: []string{"https://accounts.flight.deck.example.com", "http://localhost:5173"},
		Namespaces: []OriginNamespace{
			{Scheme: "https", Suffix: "flight.deck.example.com", Reserved: []string{"accounts", "API"}},
			{Scheme: "https", Suffix: "staging.example.com", Ports: []int{8443}},
		},
	})
	if err != nil {
		t.Fatalf("NewOriginPolicy: %v", err)
	}
	return p
}

func originRequest(values ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, v := range values {
		r.Header.Add("Origin", v)
	}
	return r
}

func TestOriginPolicy_AllowsRequest(t *testing.T) {
	p := testOriginPolicy(t)
	tests := []struct {
		name   string
		origin []string
		want   bool
	}{
		{"one label", []string{"https://acme.flight.deck.example.com"}, true},
		{"digits and inner hyphen", []string{"https://client-42.flight.deck.example.com"}, true},
		{"punycode label", []string{"https://xn--bcher-kva.flight.deck.example.com"}, true},
		{"63-char label", []string{"https://" + string(make63('a')) + ".flight.deck.example.com"}, true},
		{"uppercase normalized", []string{"HTTPS://ACME.Flight.Deck.Example.COM"}, true},
		{"exact entry", []string{"http://localhost:5173"}, true},
		{"reserved label admitted by exact entry", []string{"https://accounts.flight.deck.example.com"}, true},
		{"explicit allowed port", []string{"https://acme.staging.example.com:8443"}, true},
		{"bracketed namespace host", []string{"https://[acme.flight.deck.example.com]"}, false},
		{"bracketed namespace host with allowed port", []string{"https://[acme.staging.example.com]:8443"}, false},

		{"missing origin", nil, false},
		{"repeated origin", []string{"https://acme.flight.deck.example.com", "https://acme.flight.deck.example.com"}, false},
		{"null", []string{"null"}, false},
		{"empty", []string{""}, false},
		{"bare suffix", []string{"https://flight.deck.example.com"}, false},
		{"extra label", []string{"https://a.b.flight.deck.example.com"}, false},
		{"empty label", []string{"https://.flight.deck.example.com"}, false},
		{"lookalike prefix", []string{"https://evilflight.deck.example.com"}, false},
		{"lookalike suffix", []string{"https://x.flight.deck.example.com.evil.com"}, false},
		{"trailing dot", []string{"https://acme.flight.deck.example.com."}, false},
		{"reserved label", []string{"https://api.flight.deck.example.com"}, false},
		{"wrong scheme", []string{"http://acme.flight.deck.example.com"}, false},
		{"unexpected port", []string{"https://acme.flight.deck.example.com:8443"}, false},
		{"explicit default port", []string{"https://acme.flight.deck.example.com:443"}, false},
		{"default port where only 8443 allowed", []string{"https://acme.staging.example.com"}, false},
		{"leading zero port", []string{"https://acme.staging.example.com:08443"}, false},
		{"empty port", []string{"https://acme.flight.deck.example.com:"}, false},
		{"userinfo", []string{"https://user@acme.flight.deck.example.com"}, false},
		{"path", []string{"https://acme.flight.deck.example.com/"}, false},
		{"query", []string{"https://acme.flight.deck.example.com?x=1"}, false},
		{"fragment", []string{"https://acme.flight.deck.example.com#x"}, false},
		{"backslash", []string{"https://acme.flight.deck.example.com\\x"}, false},
		{"percent-encoded", []string{"https://acme%2e.flight.deck.example.com"}, false},
		{"leading hyphen", []string{"https://-acme.flight.deck.example.com"}, false},
		{"trailing hyphen", []string{"https://acme-.flight.deck.example.com"}, false},
		{"underscore", []string{"https://ac_me.flight.deck.example.com"}, false},
		{"unicode label", []string{"https://bücher.flight.deck.example.com"}, false},
		{"64-char label", []string{"https://" + string(make63('a')) + "a.flight.deck.example.com"}, false},
		{"no scheme", []string{"acme.flight.deck.example.com"}, false},
		{"other scheme", []string{"ftp://acme.flight.deck.example.com"}, false},
		{"wildcard literal", []string{"https://*.flight.deck.example.com"}, false},
		{"comma-joined list", []string{"https://acme.flight.deck.example.com, https://evil.com"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.AllowsRequest(originRequest(tt.origin...)); got != tt.want {
				t.Errorf("AllowsRequest(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestOriginPolicy_ZeroValueAdmitsNothing(t *testing.T) {
	var p OriginPolicy
	if p.AllowsRequest(originRequest("https://app.example.com")) {
		t.Fatal("zero OriginPolicy admitted an origin")
	}
}

func TestOriginPolicy_ExactIPOrigins(t *testing.T) {
	p, err := NewOriginPolicy(OriginPolicyConfig{Exact: []string{
		"https://127.0.0.1", "https://127.0.0.1:8443",
		"https://[2001:db8::1]", "https://[2001:db8::1]:8443",
		"https://[::ffff:127.0.0.1]",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for origin, want := range map[string]bool{
		"https://127.0.0.1":          true,
		"https://127.0.0.1:8443":     true,
		"https://[2001:db8::1]":      true,
		"https://[2001:DB8::1]:8443": true,
		"https://[::ffff:127.0.0.1]": true,
		"https://[127.0.0.1]":        false,
		"https://[127.0.0.1]:8443":   false,
		"https://[2001:db8::2]":      false,
		"https://[2001:db8::1]:9443": false,
	} {
		t.Run(origin, func(t *testing.T) {
			if got := p.AllowsRequest(originRequest(origin)); got != want {
				t.Errorf("AllowsRequest(%q) = %v, want %v", origin, got, want)
			}
		})
	}
}

func TestOriginPolicy_IgnoresForwardedHeaders(t *testing.T) {
	p := testOriginPolicy(t)
	r := originRequest("https://evil.com")
	r.Header.Set("X-Forwarded-Host", "acme.flight.deck.example.com")
	r.Header.Set("Forwarded", "host=acme.flight.deck.example.com")
	if p.AllowsRequest(r) {
		t.Fatal("policy consulted a forwarded header")
	}
}

func TestNewOriginPolicy_Rejects(t *testing.T) {
	tests := []struct {
		name string
		cfg  OriginPolicyConfig
	}{
		{"wildcard exact", OriginPolicyConfig{Exact: []string{"*"}}},
		{"exact with path", OriginPolicyConfig{Exact: []string{"https://app.example.com/"}}},
		{"exact with trailing dot", OriginPolicyConfig{Exact: []string{"https://app.example.com."}}},
		{"exact null", OriginPolicyConfig{Exact: []string{"null"}}},
		{"bracketed DNS exact", OriginPolicyConfig{Exact: []string{"https://[app.example.com]"}}},
		{"bracketed DNS exact with port", OriginPolicyConfig{Exact: []string{"https://[app.example.com]:8443"}}},
		{"bracketed IPv4 exact", OriginPolicyConfig{Exact: []string{"https://[127.0.0.1]"}}},
		{"bracketed IPv4 exact with port", OriginPolicyConfig{Exact: []string{"https://[127.0.0.1]:8443"}}},
		{"malformed IPv6 exact", OriginPolicyConfig{Exact: []string{"https://[2001:db8:::1]"}}},
		{"malformed IPv6 exact with port", OriginPolicyConfig{Exact: []string{"https://[2001:db8:::1]:8443"}}},
		{"bad scheme", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "ftp", Suffix: "example.com"}}}},
		{"single-label suffix", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "com"}}}},
		{"wildcard suffix", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "*.example.com"}}}},
		{"trailing-dot suffix", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "example.com."}}}},
		{"leading-dot suffix", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: ".example.com"}}}},
		{"port zero", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "example.com", Ports: []int{0}}}}},
		{"port too large", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "example.com", Ports: []int{65536}}}}},
		{"ipv4 suffix", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "10.0.0.1"}}}},
		{"unicode suffix", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "bücher.example"}}}},
		{"bad reserved label", OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "example.com", Reserved: []string{"a.b"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewOriginPolicy(tt.cfg); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("err = %v, want sdk.ErrInvalidInput", err)
			}
		})
	}
}

func TestOriginPolicy_ExplicitPortsAreExhaustive(t *testing.T) {
	p, err := NewOriginPolicy(OriginPolicyConfig{Namespaces: []OriginNamespace{
		{Scheme: "https", Suffix: "example.com", Ports: []int{443, 8443}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for origin, want := range map[string]bool{
		"https://a.example.com":      true,
		"https://a.example.com:8443": true,
		"https://a.example.com:9443": false,
	} {
		if got := p.AllowsRequest(originRequest(origin)); got != want {
			t.Errorf("AllowsRequest(%q) = %v, want %v", origin, got, want)
		}
	}
}

func TestOriginPolicy_HasHTTPNamespace(t *testing.T) {
	if testOriginPolicy(t).HasHTTPNamespace() {
		t.Error("https-only namespaces reported http (an http EXACT entry does not count)")
	}
	p, err := NewOriginPolicy(OriginPolicyConfig{Namespaces: []OriginNamespace{{Scheme: "HTTP", Suffix: "localtest.me"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasHTTPNamespace() {
		t.Error("http namespace not reported")
	}
}

func TestNewOriginPolicy_DoesNotAliasInput(t *testing.T) {
	cfg := OriginPolicyConfig{
		Exact:      []string{"https://app.example.com"},
		Namespaces: []OriginNamespace{{Scheme: "https", Suffix: "example.com", Reserved: []string{"api"}}},
	}
	p, err := NewOriginPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Exact[0] = "https://evil.com"
	cfg.Namespaces[0].Reserved[0] = "x"
	if !p.AllowsRequest(originRequest("https://app.example.com")) {
		t.Error("mutating Exact after construction changed the policy")
	}
	if p.AllowsRequest(originRequest("https://api.example.com")) {
		t.Error("mutating Reserved after construction changed the policy")
	}
}

func TestParseOriginNamespace(t *testing.T) {
	ns, err := ParseOriginNamespace("HTTPS://*.Flight.Deck.Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ns.Scheme != "https" || ns.Suffix != "flight.deck.example.com" || ns.Ports != nil {
		t.Errorf("got %+v", ns)
	}
	ns, err = ParseOriginNamespace("http://*.localtest.me:5173")
	if err != nil {
		t.Fatal(err)
	}
	if ns.Scheme != "http" || ns.Suffix != "localtest.me" || !slices.Equal(ns.Ports, []int{5173}) {
		t.Errorf("got %+v", ns)
	}

	for _, bad := range []string{
		"", "*.example.com", "https://example.com", "https://*example.com", "https://*.*.example.com",
		"https://*.com", "https://*.example.com:", "https://*.example.com:0", "https://*.example.com/",
		"https://a.*.example.com", "https://*.example.com.",
	} {
		if _, err := ParseOriginNamespace(bad); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ParseOriginNamespace(%q) err = %v, want sdk.ErrInvalidInput", bad, err)
		}
	}
}

func make63(c byte) []byte {
	b := make([]byte, 63)
	for i := range b {
		b[i] = c
	}
	return b
}
