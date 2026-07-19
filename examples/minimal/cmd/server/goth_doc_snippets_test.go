package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// This file is the executed proof for the load-bearing adopter recipes documented
// in ui/goth/README.md §11 (Requirements→CSP directive string; the boot-time
// asset-route reachability self-check). The helpers below ARE the documented
// snippets — keeping them compiled and run guarantees the README stays in step with
// the real public API (goth.Requirements / goth.Bundle.Manifest / goth.Manifest.Assets).

// cspHeader is the documented Requirements→CSP-directive-string formatter recipe: a
// host maps a bundle's deterministic, minimal Requirements into one CSP header value
// it writes (the kit never writes a header). Ordering is stable, so the emitted value
// is byte-identical run-to-run.
func cspHeader(req uigoth.Requirements) string {
	var b strings.Builder
	for i, d := range req.Directives() {
		if i > 0 {
			b.WriteString("; ")
		}
		sources, _ := req.Sources(d)
		b.WriteString(string(d))
		for _, s := range sources {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}
	return b.String()
}

// assertAssetsReachable is the documented boot-time self-check: every manifest asset
// must resolve over the host's asset route. A host that forgot to mount the asset
// route (the web.NewStaticFileServer(...).AddRoutes call) then fails loudly at boot
// instead of silently serving unstyled pages whose fingerprinted stylesheet 404s.
func assertAssetsReachable(bundle *uigoth.Bundle, serve func(path string) int) error {
	base := bundle.AssetBasePath()
	for _, a := range bundle.Manifest().Assets() {
		url := base + "/" + a.Path
		if code := serve(url); code != http.StatusOK {
			return fmt.Errorf("ui/goth asset %q not reachable at %s (got %d) — is the asset route mounted?", a.LogicalName, url, code)
		}
	}
	return nil
}

// TestDocSnippet_CSPHeaderFormatter proves the Requirements→CSP recipe against the
// real StylesOnly bundle this host builds: the mapped header is exactly style-src 'self'
// with no remote origin, no nonce, and no unsafe-* source.
func TestDocSnippet_CSPHeaderFormatter(t *testing.T) {
	bundle, err := uigoth.New(uigoth.Config{AssetBasePath: gothAssetBasePath})
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	csp := cspHeader(bundle.Requirements())
	if !strings.Contains(csp, "style-src 'self'") {
		t.Errorf("CSP header = %q, want it to carry style-src 'self'", csp)
	}
	for _, unwanted := range []string{"unsafe-inline", "unsafe-eval", "nonce-", "http://", "https://"} {
		if strings.Contains(csp, unwanted) {
			t.Errorf("self-hosted bundle CSP %q must not contain %q", csp, unwanted)
		}
	}
}

// TestDocSnippet_AssetReachabilitySelfCheck proves the boot-time self-check both
// passes on a correctly wired host and CATCHES the "host forgot to mount the asset
// route" failure mode.
func TestDocSnippet_AssetReachabilitySelfCheck(t *testing.T) {
	bundle, err := uigoth.New(uigoth.Config{AssetBasePath: gothAssetBasePath})
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}

	// A correctly wired router (the real presentation graph) passes the self-check.
	wired := htmxProofRouter(t)
	serveWired := func(path string) int { return do(t, wired, http.MethodGet, path, nil).Code }
	if err := assertAssetsReachable(bundle, serveWired); err != nil {
		t.Errorf("wired host failed the asset self-check: %v", err)
	}

	// A router with the CMS feature mounted but the asset route NOT mounted fails the
	// self-check — the exact "forgot to mount the asset route" mistake, caught at boot.
	bare := web.NewWebHandler()
	serveBare := func(path string) int { return do(t, bare, http.MethodGet, path, nil).Code }
	if err := assertAssetsReachable(bundle, serveBare); err == nil {
		t.Error("asset self-check should fail when the asset route is not mounted")
	}
}
