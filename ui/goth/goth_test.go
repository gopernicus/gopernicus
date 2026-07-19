package goth

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

func TestNewDefaults(t *testing.T) {
	b, err := New(Config{})
	if err != nil {
		t.Fatalf("New(zero): %v", err)
	}
	if b.Profile() != StylesOnly {
		t.Errorf("Profile = %v, want StylesOnly", b.Profile())
	}
	if b.AssetBasePath() != DefaultAssetBasePath {
		t.Errorf("AssetBasePath = %q, want %q", b.AssetBasePath(), DefaultAssetBasePath)
	}
}

func TestNewAssetBasePath(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty defaults", "", DefaultAssetBasePath, false},
		{"leading slash kept", "/assets/goth", "/assets/goth", false},
		{"no leading slash normalized", "assets/goth", "/assets/goth", false},
		{"trailing slash stripped", "/assets/goth/", "/assets/goth", false},
		{"absolute url rejected", "https://cdn.example.com/x", "", true},
		{"host rejected", "//cdn.example.com/x", "", true},
		{"dotdot rejected", "/assets/../secret", "", true},
		{"query rejected", "/assets?x=1", "", true},
		{"fragment rejected", "/assets#f", "", true},
		{"control char rejected", "/assets\x00", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := New(Config{AssetBasePath: tt.in})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New(%q) = nil error, want error", tt.in)
				}
				if b != nil {
					t.Fatalf("New(%q) returned a non-nil Bundle alongside an error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q): %v", tt.in, err)
			}
			if b.AssetBasePath() != tt.want {
				t.Errorf("AssetBasePath = %q, want %q", b.AssetBasePath(), tt.want)
			}
		})
	}
}

// TestNewThemeStylesheetPathValidation is the adversarial host-path validation
// matrix (amendment-1 D4): only an exactly-one-leading-slash root-relative path is
// accepted (and emitted verbatim); every browser-authority / traversal / URL form
// is a construction error.
func TestNewThemeStylesheetPathValidation(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty selects default", "", false},
		{"root-relative accepted", "/theme/host.css", false},
		{"root-relative with query-less path accepted", "/static/brand-theme.5f3a.css", false},
		{"relative rejected", "theme/host.css", true},
		{"dot-relative rejected", "./host.css", true},
		{"protocol-relative // rejected", "//cdn.example.com/x.css", true},
		{"triple-slash /// rejected", "///cdn.example.com/x.css", true},
		{"additional leading slash rejected", "////x.css", true},
		{"backslash rejected", "/theme\\host.css", true},
		{"leading backslash rejected", "\\\\evil/x.css", true},
		{"https scheme rejected", "https://cdn.example.com/x.css", true},
		{"http scheme rejected", "http://cdn.example.com/x.css", true},
		{"scheme-relative host rejected", "javascript:alert(1)", true},
		{"dotdot traversal rejected", "/assets/../secret.css", true},
		{"query rejected", "/theme/host.css?v=1", true},
		{"fragment rejected", "/theme/host.css#f", true},
		{"control char rejected", "/theme/host\x00.css", true},
		{"newline rejected", "/theme/host\n.css", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := New(Config{ThemeStylesheetPath: tt.in})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New(ThemeStylesheetPath=%q) = nil error, want error", tt.in)
				}
				if b != nil {
					t.Fatalf("New(ThemeStylesheetPath=%q) returned a non-nil Bundle alongside an error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(ThemeStylesheetPath=%q): %v", tt.in, err)
			}
		})
	}
}

func TestNewUnknownProfile(t *testing.T) {
	b, err := New(Config{Profile: Profile(99)})
	if err == nil {
		t.Fatalf("New(unknown profile) = nil error, want error")
	}
	if b != nil {
		t.Fatalf("New(unknown profile) returned a non-nil Bundle alongside an error")
	}
}

func TestRequirementsByProfile(t *testing.T) {
	tests := []struct {
		profile Profile
		script  bool
	}{
		{StylesOnly, false},
		{Interactive, true},
		{Full, true},
	}
	for _, tt := range tests {
		b, err := New(Config{Profile: tt.profile})
		if err != nil {
			t.Fatalf("New(profile %v): %v", tt.profile, err)
		}
		req := b.Requirements()
		src, ok := req.Sources(DirectiveStyle)
		if !ok {
			t.Errorf("profile %v: style-src not required", tt.profile)
		}
		// style-src is exactly 'self' with no nonce (amendment-1 D6): the kit emits
		// no server-rendered style attribute and no inline <style> element.
		if len(src) != 1 || src[0] != "'self'" {
			t.Errorf("profile %v: style-src = %v, want ['self'] (no nonce)", tt.profile, src)
		}
		for _, s := range src {
			if strings.Contains(s, "unsafe-inline") || strings.Contains(s, "nonce-") {
				t.Errorf("profile %v: style-src carried %q; style-src is 'self' only", tt.profile, s)
			}
		}
		_, hasScript := req.Sources(DirectiveScript)
		if hasScript != tt.script {
			t.Errorf("profile %v: script-src required = %v, want %v", tt.profile, hasScript, tt.script)
		}
		// Directives are deterministic and duplicate-free.
		seen := map[Directive]bool{}
		for _, d := range req.Directives() {
			if seen[d] {
				t.Errorf("duplicate directive %q", d)
			}
			seen[d] = true
		}
	}
}

func TestParseManifest(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		m, err := parseManifest([]byte(`{"assets":[]}`))
		if err != nil {
			t.Fatalf("parseManifest(empty): %v", err)
		}
		if len(m.Assets()) != 0 {
			t.Errorf("empty manifest should be empty, got %d assets", len(m.Assets()))
		}
		if _, ok := m.Lookup("theme.css"); ok {
			t.Errorf("Lookup on empty manifest should be ok=false")
		}
	})

	t.Run("populated and sorted", func(t *testing.T) {
		raw := []byte(`{"assets":[
			{"logicalName":"runtime.js","path":"dist/runtime.abc.js","integrity":"sha384-b","bytes":20},
			{"logicalName":"theme.css","path":"dist/theme.def.css","integrity":"sha384-a","bytes":10}
		]}`)
		m, err := parseManifest(raw)
		if err != nil {
			t.Fatalf("parseManifest: %v", err)
		}
		got := m.Assets()
		if len(got) != 2 {
			t.Fatalf("got %d assets, want 2", len(got))
		}
		if got[0].LogicalName != "runtime.js" || got[1].LogicalName != "theme.css" {
			t.Errorf("Assets not sorted by logical name: %v", got)
		}
		a, ok := m.Lookup("theme.css")
		if !ok || a.Path != "dist/theme.def.css" || a.Integrity != "sha384-a" || a.Bytes != 10 {
			t.Errorf("Lookup(theme.css) = %+v, ok=%v", a, ok)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		if _, err := parseManifest([]byte(`{not json`)); err == nil {
			t.Errorf("parseManifest(malformed) = nil error, want error")
		}
	})

	t.Run("missing logical name", func(t *testing.T) {
		if _, err := parseManifest([]byte(`{"assets":[{"path":"dist/x.css"}]}`)); err == nil {
			t.Errorf("parseManifest(missing logicalName) = nil error, want error")
		}
	})

	t.Run("duplicate logical name", func(t *testing.T) {
		raw := []byte(`{"assets":[{"logicalName":"x","path":"a"},{"logicalName":"x","path":"b"}]}`)
		if _, err := parseManifest(raw); err == nil {
			t.Errorf("parseManifest(duplicate) = nil error, want error")
		}
	})
}

// TestEmbeddedManifestHasFourAssets proves the committed manifest carries exactly
// the four logical assets the amended asset pipeline emits.
func TestEmbeddedManifestHasFourAssets(t *testing.T) {
	b, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := []string{"htmx.js", "runtime.js", "theme-default.css", "theme.css"}
	for _, name := range want {
		if _, ok := b.Manifest().Lookup(name); !ok {
			t.Errorf("embedded manifest missing %q", name)
		}
	}
	if got := len(b.Manifest().Assets()); got != len(want) {
		t.Errorf("embedded manifest has %d assets, want %d (%v)", got, len(want), want)
	}
}

func TestDocumentRenderSpecimen(t *testing.T) {
	b, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := templ.Raw("<main>hello</main>")
	comp := b.Document(DocumentOptions{
		Title:      "Specimen",
		Appearance: theme.AppearanceDark,
		Dir:        theme.DirectionRTL,
	}, body)

	var sb strings.Builder
	if err := comp.Render(context.Background(), &sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := sb.String()
	for _, want := range []string{
		"<!doctype html>",
		"<html",
		`lang="en"`,
		`dir="rtl"`,
		`data-theme="dark"`,
		"<title>Specimen</title>",
		"<main>hello</main>",
		"</body>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("document render missing %q\n---\n%s", want, out)
		}
	}
	// The kit emits no server-rendered inline <style> and no server-rendered style
	// attribute (amendment-1 frozen invariant).
	if strings.Contains(out, "<style") {
		t.Errorf("document shell emitted an inline <style>: %s", out)
	}
	if strings.Contains(out, "style=") {
		t.Errorf("document shell emitted a server-rendered style attribute: %s", out)
	}
}

// TestHeadRendersEmbeddedManifestAssets proves the manifest embed is live: New
// parses the real committed manifest and Head emits external, fingerprinted,
// SRI-guarded links for each profile asset.
func TestHeadRendersEmbeddedManifestAssets(t *testing.T) {
	b, err := New(Config{Profile: Full})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(b.Manifest().Assets()) == 0 {
		t.Fatal("embedded manifest is empty — the asset build did not run")
	}
	out := renderHead(t, b)
	for _, name := range []string{"theme.css", "runtime.js", "htmx.js"} {
		a, _ := b.Manifest().Lookup(name)
		href := b.AssetBasePath() + "/" + a.Path
		if !strings.Contains(out, href) {
			t.Errorf("Head missing fingerprinted href %q; got %q", href, out)
		}
		if a.Integrity != "" && !strings.Contains(out, a.Integrity) {
			t.Errorf("Head missing integrity %q for %q", a.Integrity, name)
		}
	}
	// Assets are external: the head carries no inline <style>.
	if strings.Contains(out, "<style") {
		t.Errorf("Head emitted an inline <style>: %s", out)
	}
}

func renderHead(t *testing.T, b *Bundle) string {
	t.Helper()
	var sb strings.Builder
	if err := b.Head().Render(context.Background(), &sb); err != nil {
		t.Fatalf("Render head: %v", err)
	}
	return sb.String()
}

// TestHeadProfileAwareAssetSelection proves Head emits exactly the profile's asset
// classes — never more (additive supersets). StylesOnly serves CSS only;
// Interactive adds the runtime; Full adds HTMX.
func TestHeadProfileAwareAssetSelection(t *testing.T) {
	tests := []struct {
		profile     Profile
		wantRuntime bool
		wantHTMX    bool
	}{
		{StylesOnly, false, false},
		{Interactive, true, false},
		{Full, true, true},
	}
	for _, tt := range tests {
		b, err := New(Config{Profile: tt.profile})
		if err != nil {
			t.Fatalf("New(%v): %v", tt.profile, err)
		}
		out := renderHead(t, b)
		css, _ := b.Manifest().Lookup("theme.css")
		if !strings.Contains(out, css.Path) {
			t.Errorf("profile %v: head missing theme.css %q", tt.profile, css.Path)
		}
		runtime, _ := b.Manifest().Lookup("runtime.js")
		if got := strings.Contains(out, runtime.Path); got != tt.wantRuntime {
			t.Errorf("profile %v: runtime.js present = %v, want %v", tt.profile, got, tt.wantRuntime)
		}
		htmx, _ := b.Manifest().Lookup("htmx.js")
		if got := strings.Contains(out, htmx.Path); got != tt.wantHTMX {
			t.Errorf("profile %v: htmx.js present = %v, want %v", tt.profile, got, tt.wantHTMX)
		}
		// Every emitted kit asset is SRI-guarded and cross-origin; no inline style.
		if !strings.Contains(out, `integrity="`) || !strings.Contains(out, `crossorigin="anonymous"`) {
			t.Errorf("profile %v: head assets missing integrity/crossorigin: %s", tt.profile, out)
		}
		if strings.Contains(out, "<style") {
			t.Errorf("profile %v: head emitted an inline <style>", tt.profile)
		}
	}
}

// TestProfileScriptEmission proves each profile's Head emits exactly its promised
// script assets: StylesOnly emits no <script; Interactive adds the runtime script
// but not HTMX; Full adds both. Promised scripts are deferred.
func TestProfileScriptEmission(t *testing.T) {
	tests := []struct {
		profile     Profile
		wantScript  bool
		wantRuntime bool
		wantHTMX    bool
	}{
		{StylesOnly, false, false, false},
		{Interactive, true, true, false},
		{Full, true, true, true},
	}
	for _, tt := range tests {
		b, err := New(Config{Profile: tt.profile})
		if err != nil {
			t.Fatalf("New(%v): %v", tt.profile, err)
		}
		out := renderHead(t, b)
		if got := strings.Contains(out, "<script"); got != tt.wantScript {
			t.Errorf("profile %v: <script present = %v, want %v", tt.profile, got, tt.wantScript)
		}
		runtime, _ := b.Manifest().Lookup("runtime.js")
		if got := strings.Contains(out, runtime.Path); got != tt.wantRuntime {
			t.Errorf("profile %v: runtime.js present = %v, want %v", tt.profile, got, tt.wantRuntime)
		}
		htmx, _ := b.Manifest().Lookup("htmx.js")
		if got := strings.Contains(out, htmx.Path); got != tt.wantHTMX {
			t.Errorf("profile %v: htmx.js present = %v, want %v", tt.profile, got, tt.wantHTMX)
		}
		if tt.wantScript && !strings.Contains(out, "defer") {
			t.Errorf("profile %v: script assets must be deferred", tt.profile)
		}
	}
}

// TestHeadDefaultThemeLink proves that with no ThemeStylesheetPath, Head injects the
// kit's embedded default theme (theme-default.css) as a stylesheet link AFTER the
// kit stylesheet, carrying integrity + crossorigin, and before any script.
func TestHeadDefaultThemeLink(t *testing.T) {
	b, err := New(Config{Profile: Full})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out := renderHead(t, b)
	kit, _ := b.Manifest().Lookup("theme.css")
	def, _ := b.Manifest().Lookup("theme-default.css")
	kitHref := b.AssetBasePath() + "/" + kit.Path
	defHref := b.AssetBasePath() + "/" + def.Path
	if !strings.Contains(out, defHref) {
		t.Fatalf("head missing the default theme link %q: %s", defHref, out)
	}
	if !strings.Contains(out, def.Integrity) {
		t.Errorf("default theme link missing integrity %q: %s", def.Integrity, out)
	}
	// Order: kit CSS → theme-default link → scripts.
	iKit := strings.Index(out, kitHref)
	iDef := strings.Index(out, defHref)
	iScript := strings.Index(out, "<script")
	if !(iKit < iDef && iDef < iScript) {
		t.Errorf("head order wrong: kit=%d default=%d script=%d\n%s", iKit, iDef, iScript, out)
	}
	if strings.Contains(out, "<style") {
		t.Errorf("head emitted an inline <style>: %s", out)
	}
}

// TestHeadHostThemeLink proves a configured host ThemeStylesheetPath is emitted
// verbatim AFTER the kit stylesheet with NO integrity attribute (the kit cannot
// know host bytes) and the kit's default theme asset is NOT linked.
func TestHeadHostThemeLink(t *testing.T) {
	const hostPath = "/static/brand-theme.css"
	b, err := New(Config{Profile: Full, ThemeStylesheetPath: hostPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out := renderHead(t, b)
	kit, _ := b.Manifest().Lookup("theme.css")
	def, _ := b.Manifest().Lookup("theme-default.css")
	kitHref := b.AssetBasePath() + "/" + kit.Path

	if !strings.Contains(out, `<link rel="stylesheet" href="`+hostPath+`">`) {
		t.Fatalf("head missing verbatim host theme link %q: %s", hostPath, out)
	}
	// The configured host link carries no integrity: assert the host href is not
	// immediately followed by an integrity attribute.
	linkTag := `<link rel="stylesheet" href="` + hostPath + `"`
	i := strings.Index(out, linkTag)
	tail := out[i+len(linkTag):]
	closer := strings.Index(tail, ">")
	if strings.Contains(tail[:closer], "integrity") {
		t.Errorf("configured host theme link carried an integrity attribute: %s", out[i:i+len(linkTag)+closer+1])
	}
	// The kit default theme asset must NOT be linked when a host path is set.
	if strings.Contains(out, def.Path) {
		t.Errorf("default theme asset linked despite a configured host path: %s", out)
	}
	// Host link loads after the kit stylesheet (source-order cascade).
	if strings.Index(out, kitHref) > strings.Index(out, hostPath) {
		t.Errorf("host theme link must come after the kit stylesheet: %s", out)
	}
	if strings.Contains(out, "<style") {
		t.Errorf("head emitted an inline <style>: %s", out)
	}
}

// ExampleNew shows the frozen construction call. It compiles as public-API proof.
func ExampleNew() {
	bundle, err := New(Config{
		AssetBasePath:       "/assets/goth",
		Profile:             Full,
		ThemeStylesheetPath: "/static/brand-theme.css",
	})
	if err != nil {
		return
	}
	_ = bundle.Requirements()
	_ = bundle.Manifest()
	_ = bundle.Head()
}
