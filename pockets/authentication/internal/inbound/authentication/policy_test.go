package authentication

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// The historical asset-free CSP strings buildCSP MUST reproduce byte-for-byte for a
// nil policy (GOTH-0.4: nil provably preserves the current posture). These are the
// exact values writeHTMLSecurity emitted before the resource-policy seam existed.
const (
	assetFreeCSPWithNonce = "default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; script-src 'nonce-abc123'"
	assetFreeCSPNoNonce   = "default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; script-src 'none'"
)

// TestBuildCSPNilPreservesAssetFreePosture proves a nil policy reproduces the exact
// historical CSP for both the nonce and no-nonce (fail-safe) paths (GOTH-0.4).
func TestBuildCSPNilPreservesAssetFreePosture(t *testing.T) {
	if got := buildCSP(nil, "abc123"); got != assetFreeCSPWithNonce {
		t.Errorf("nil policy + nonce CSP = %q, want %q", got, assetFreeCSPWithNonce)
	}
	if got := buildCSP(nil, ""); got != assetFreeCSPNoNonce {
		t.Errorf("nil policy + no nonce CSP = %q, want %q", got, assetFreeCSPNoNonce)
	}
}

// TestWriteHTMLSecurityNilPolicyHeaders proves the full header set a nil policy emits
// is unchanged: the four fixed headers plus the asset-free CSP.
func TestWriteHTMLSecurityNilPolicyHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	writeHTMLSecurity(rec, nil, "abc123")
	want := map[string]string{
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": assetFreeCSPWithNonce,
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

// TestNewHTMLResourcePolicyValidWidening builds a realistic widening policy and proves
// its rendered directives are deterministically ordered, carry the fixed prefix, and
// substitute the per-render nonce only through the Nonce channel.
func TestNewHTMLResourcePolicyValidWidening(t *testing.T) {
	p, err := NewHTMLResourcePolicy(
		HTMLResourceDirective{Kind: HTMLStyleSrc, Sources: []string{"'self'"}},
		HTMLResourceDirective{Kind: HTMLScriptSrc, Sources: []string{"'self'"}, Nonce: true},
		HTMLResourceDirective{Kind: HTMLImgSrc, Sources: []string{"'self'", "data:"}},
		HTMLResourceDirective{Kind: HTMLFontSrc, Sources: []string{"'self'"}},
		HTMLResourceDirective{Kind: HTMLConnectSrc, Sources: []string{"'self'"}},
	)
	if err != nil {
		t.Fatalf("NewHTMLResourcePolicy: %v", err)
	}

	csp := buildCSP(p, "n0nce")
	// Fixed prefix always leads.
	if !strings.HasPrefix(csp, fixedCSPPrefix+"; ") {
		t.Errorf("CSP does not lead with the fixed prefix: %q", csp)
	}
	// Deterministic order: connect-src, font-src, img-src, script-src, style-src.
	wantTail := "; connect-src 'self'; font-src 'self'; img-src 'self' data:; script-src 'self' 'nonce-n0nce'; style-src 'self'"
	if got := strings.TrimPrefix(csp, fixedCSPPrefix); got != wantTail {
		t.Errorf("policy tail = %q, want %q", got, wantTail)
	}
	// The nonce reaches only the directive that requested it.
	if strings.Count(csp, "'nonce-n0nce'") != 1 {
		t.Errorf("nonce should appear exactly once, in script-src: %q", csp)
	}
}

// TestBuildCSPPolicyNonceFailSafe proves a Nonce-only directive with no minted nonce
// renders 'none' rather than silently widening.
func TestBuildCSPPolicyNonceFailSafe(t *testing.T) {
	p, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: HTMLScriptSrc, Nonce: true})
	if err != nil {
		t.Fatalf("NewHTMLResourcePolicy: %v", err)
	}
	if got := buildCSP(p, ""); !strings.HasSuffix(got, "; script-src 'none'") {
		t.Errorf("nonce-only directive with no nonce = %q, want it to end in script-src 'none'", got)
	}
	if got := buildCSP(p, "xyz"); !strings.HasSuffix(got, "; script-src 'nonce-xyz'") {
		t.Errorf("nonce-only directive with nonce = %q, want it to end in script-src 'nonce-xyz'", got)
	}
}

// TestNewHTMLResourcePolicyRejectsFixedAndUnknownKinds proves a policy structurally
// cannot name a fixed protection directive or an unknown resource class — the fixed
// protections can never be removed or relaxed through the seam (GOTH-0.4).
func TestNewHTMLResourcePolicyRejectsFixedAndUnknownKinds(t *testing.T) {
	for _, kind := range []HTMLResourceKind{
		"default-src", "base-uri", "form-action", "frame-ancestors",
		"object-src", "script-src-elem", "", "SCRIPT-SRC",
	} {
		_, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: kind, Sources: []string{"'self'"}})
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("kind %q: err=%v, want sdk.ErrInvalidInput", kind, err)
		}
	}
}

// TestNewHTMLResourcePolicyRejectsMaliciousSources proves directive/source validation
// rejects control characters and CSP-structural delimiters at construction, so a policy
// can never split a directive or inject a second header (GOTH-0.4).
func TestNewHTMLResourcePolicyRejectsMaliciousSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{"empty", ""},
		{"semicolon splits directive", "'self'; script-src *"},
		{"comma splits source list", "'self',*"},
		{"space splits token", "'self' *"},
		{"tab", "'self'\t*"},
		{"newline injects header", "'self'\nX-Evil: 1"},
		{"carriage return injects header", "'self'\rX-Evil: 1"},
		{"null byte", "'self'\x00"},
		{"del control", "'self'\x7f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: HTMLScriptSrc, Sources: []string{tt.source}})
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("source %q: err=%v, want sdk.ErrInvalidInput", tt.source, err)
			}
		})
	}
}

// TestNewHTMLResourcePolicyRequiresSourceOrNonce proves a directive that neither lists a
// source nor requests the nonce is rejected — an empty directive is never emitted.
func TestNewHTMLResourcePolicyRequiresSourceOrNonce(t *testing.T) {
	_, err := NewHTMLResourcePolicy(HTMLResourceDirective{Kind: HTMLStyleSrc})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty directive: err=%v, want sdk.ErrInvalidInput", err)
	}
}

// TestNewHTMLResourcePolicyMergesAndDedups proves duplicate kinds merge, duplicate
// sources collapse, and the rendered output is deterministic regardless of input order.
func TestNewHTMLResourcePolicyMergesAndDedups(t *testing.T) {
	p, err := NewHTMLResourcePolicy(
		HTMLResourceDirective{Kind: HTMLImgSrc, Sources: []string{"'self'", "data:"}},
		HTMLResourceDirective{Kind: HTMLImgSrc, Sources: []string{"'self'", "https://cdn.example.com"}},
	)
	if err != nil {
		t.Fatalf("NewHTMLResourcePolicy: %v", err)
	}
	got := buildCSP(p, "")
	want := fixedCSPPrefix + "; img-src 'self' data: https://cdn.example.com"
	if got != want {
		t.Errorf("merged/deduped CSP = %q, want %q", got, want)
	}
}

// TestHTMLPolicyWidensLiveResponse is the end-to-end proof: a wired policy widens the
// CSP on a real HTML GET while every fixed protection persists (GOTH-0.4). The bundled
// pages are unchanged; only the header the handler emits changes.
func TestHTMLPolicyWidensLiveResponse(t *testing.T) {
	policy, err := NewHTMLResourcePolicy(
		HTMLResourceDirective{Kind: HTMLScriptSrc, Sources: []string{"'self'"}, Nonce: true},
		HTMLResourceDirective{Kind: HTMLStyleSrc, Sources: []string{"'self'"}},
		HTMLResourceDirective{Kind: HTMLImgSrc, Sources: []string{"'self'", "data:"}},
	)
	if err != nil {
		t.Fatalf("NewHTMLResourcePolicy: %v", err)
	}
	h := newHTMLTestHandlerWithPolicy(t, stubViews{}, policy)
	rec := do(t, h, "GET", "/auth/login", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/login = %d, want 200", rec.Code)
	}
	// Fixed headers persist under a widening policy.
	fixed := map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
	}
	for k, v := range fixed {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("fixed header %s = %q, want %q", k, got, v)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	// Fixed CSP directives remain, unremovable.
	for _, fixedDir := range []string{"default-src 'none'", "base-uri 'none'", "form-action 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, fixedDir) {
			t.Errorf("CSP %q missing fixed directive %q", csp, fixedDir)
		}
	}
	// The policy's widening directives are present.
	for _, widened := range []string{"style-src 'self'", "img-src 'self' data:", "script-src 'self' 'nonce-"} {
		if !strings.Contains(csp, widened) {
			t.Errorf("CSP %q missing widened directive %q", csp, widened)
		}
	}
}
