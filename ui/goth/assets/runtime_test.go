package assets

import (
	"io/fs"
	"strings"
	"testing"
)

// assetBody returns the embedded bytes for a logical asset name via the manifest.
func assetBody(t *testing.T, logical string) string {
	t.Helper()
	doc := loadManifest(t)
	for _, e := range doc.Assets {
		if e.LogicalName == logical {
			data, err := fs.ReadFile(FS, e.Path)
			if err != nil {
				t.Fatalf("read %s (%s): %v", logical, e.Path, err)
			}
			return string(data)
		}
	}
	t.Fatalf("manifest has no %q asset", logical)
	return ""
}

// TestRuntimeRegistersNamedControllers proves the built runtime.js registers every
// frozen goth-prefixed controller (README §8). The Alpine.data string keys survive
// minification, so a dropped registration fails here.
func TestRuntimeRegistersNamedControllers(t *testing.T) {
	body := assetBody(t, "runtime.js")
	for _, name := range []string{
		"gothDialog", "gothCollapse", "gothRovingFocus",
		"gothMenu", "gothTabs", "gothCombobox", "gothToast",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("runtime.js does not register controller %q", name)
		}
	}
}

// TestRuntimeIsCSPSafe proves the built runtime.js (the @alpinejs/csp build + GOTH
// controllers) contains no eval / new Function construct, so the Interactive/Full
// profiles need no 'unsafe-eval' (plan invariant 5 / README §4). The definitive
// no-unsafe-eval proof is the GOTH-1.5 browser CSP test; this is the asset-level
// guard.
func TestRuntimeIsCSPSafe(t *testing.T) {
	body := assetBody(t, "runtime.js")
	for _, bad := range []string{"eval(", "new Function", "Function("} {
		if strings.Contains(body, bad) {
			t.Errorf("runtime.js contains a dynamic-code construct %q; @alpinejs/csp must not use it", bad)
		}
	}
}

// TestHTMXAssetHasResponseConfig proves the built htmx.js is the vendored htmx
// 2.0.10 PLUS the Gopernicus response configuration: non-2xx fragment handling is
// configured explicitly (README §9 / plan HTMX rule 5) and does not rely on HTMX
// 4 defaults. The forbidden/conflict/validation swap codes must be present.
func TestHTMXAssetHasResponseConfig(t *testing.T) {
	body := assetBody(t, "htmx.js")
	if !strings.Contains(body, "responseHandling") {
		t.Fatal("htmx.js is missing the responseHandling configuration")
	}
	for _, code := range []string{`"403"`, `"409"`, `"422"`} {
		if !strings.Contains(body, code) {
			t.Errorf("htmx.js response config does not swap status %s", code)
		}
	}
}

// TestAssetsUseNoRemoteOrigin proves no JS asset references a CDN/remote origin:
// the runtime is entirely self-hosted (plan invariant 4, verify "no network
// request outside the showcase origin"). The definitive proof is the GOTH-1.5
// browser test; this is the asset-level guard. (Alpine's built-in error strings
// carry an alpinejs.dev docs link, which is not a fetch and is not a CDN host.)
func TestAssetsUseNoRemoteOrigin(t *testing.T) {
	for _, logical := range []string{"runtime.js", "htmx.js"} {
		body := strings.ToLower(assetBody(t, logical))
		for _, host := range []string{"unpkg", "jsdelivr", "cdnjs", "googleapis", "cdn."} {
			if strings.Contains(body, host) {
				t.Errorf("%s references remote origin %q; assets must be self-hosted", logical, host)
			}
		}
	}
}

// TestThemeCSSHasLiveRegionUtility proves the compiled theme.css carries the
// .goth-sr-only utility the runtime's shared live regions attach to their
// aria-live nodes, so announcements stay off-screen without an inline style
// (README §4 invariant a).
func TestThemeCSSHasLiveRegionUtility(t *testing.T) {
	body := assetBody(t, "theme.css")
	if !strings.Contains(body, "goth-sr-only") {
		t.Error("theme.css is missing the .goth-sr-only live-region utility")
	}
}
