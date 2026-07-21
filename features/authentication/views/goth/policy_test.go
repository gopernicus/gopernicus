package goth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
)

// findDirective returns the produced directive of the given kind, or ok=false.
func findDirective(dirs []authentication.HTMLResourceDirective, kind authentication.HTMLResourceKind) (authentication.HTMLResourceDirective, bool) {
	for _, d := range dirs {
		if d.Kind == kind {
			return d, true
		}
	}
	return authentication.HTMLResourceDirective{}, false
}

func hasSource(sources []string, want string) bool {
	for _, s := range sources {
		if s == want {
			return true
		}
	}
	return false
}

func viewsForProfile(t *testing.T, p uigoth.Profile) Views {
	t.Helper()
	b, err := uigoth.New(uigoth.Config{Profile: p})
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	v, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

// TestHTMLPolicy_ScriptSrcCarriesNonce is the Gate C C5 contract test: the adapter's
// produced policy MUST carry a script-src directive with Nonce:true (and 'self' for
// the externalized fragment reader). A non-nil policy REPLACES the feature's default
// script-src tail entirely, so a policy omitting script-src (or its nonce) fails
// closed and the fragment readers never run. Proven across every profile — the
// script-src is the adapter's own surface, not the bundle's.
func TestHTMLPolicy_ScriptSrcCarriesNonce(t *testing.T) {
	for _, p := range []uigoth.Profile{uigoth.StylesOnly, uigoth.Interactive, uigoth.Full} {
		dirs := viewsForProfile(t, p).resourceDirectives()
		script, ok := findDirective(dirs, authentication.HTMLScriptSrc)
		if !ok {
			t.Fatalf("profile %d: produced policy has no script-src directive", p)
		}
		if !script.Nonce {
			t.Errorf("profile %d: script-src directive is missing Nonce:true (fragment readers/host inline scripts fail closed)", p)
		}
		if !hasSource(script.Sources, "'self'") {
			t.Errorf("profile %d: script-src directive is missing 'self' (externalized fragment reader cannot load): %v", p, script.Sources)
		}
	}
}

// TestHTMLPolicy_StyleSrcSelf proves the mapped policy widens style-src to 'self' so
// the ui/goth stylesheet loads under the feature's default-src 'none' CSP.
func TestHTMLPolicy_StyleSrcSelf(t *testing.T) {
	dirs := viewsForProfile(t, uigoth.StylesOnly).resourceDirectives()
	style, ok := findDirective(dirs, authentication.HTMLStyleSrc)
	if !ok {
		t.Fatal("produced policy has no style-src directive")
	}
	if !hasSource(style.Sources, "'self'") {
		t.Errorf("style-src missing 'self': %v", style.Sources)
	}
}

// TestHTMLPolicy_Deterministic proves the adapter owns emitting sources
// deterministically (Gate C C3): the produced directives are identical across calls,
// so the emitted CSP header is byte-stable run-to-run.
func TestHTMLPolicy_Deterministic(t *testing.T) {
	v := viewsForProfile(t, uigoth.Full)
	a := v.resourceDirectives()
	b := v.resourceDirectives()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("resourceDirectives not deterministic:\n%#v\n%#v", a, b)
	}
}

// TestHTMLPolicy_Constructs proves HTMLPolicy returns a usable, non-nil policy the
// feature accepts (the construction never fails for a valid bundle).
func TestHTMLPolicy_Constructs(t *testing.T) {
	v := viewsForProfile(t, uigoth.StylesOnly)
	if p := v.HTMLPolicy(); p == nil {
		t.Fatal("HTMLPolicy returned nil")
	}
	if _, err := v.NewHTMLPolicy(); err != nil {
		t.Fatalf("NewHTMLPolicy: %v", err)
	}
}

// TestFragmentScriptHandler serves the externalized reader as JavaScript and never
// leaks a secret (it is a static, no-token asset).
func TestFragmentScriptHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	FragmentScriptHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultFragmentScriptPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:22] != "application/javascript" {
		t.Fatalf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Fatal("empty fragment script")
	}
	if len(FragmentScript()) == 0 {
		t.Fatal("FragmentScript returned empty")
	}
}

// TestWithFragmentScriptPath overrides the served path for the reader script.
func TestWithFragmentScriptPath(t *testing.T) {
	b, err := uigoth.New(uigoth.Config{})
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	v, err := New(b, WithFragmentScriptPath("/custom/reader.js"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := render(t, v.ResetPassword(authentication.ResetPage{RedeemPath: "/auth/password/reset"}))
	mustContain(t, "custom path", body, `src="/custom/reader.js"`)
}

// TestHTMLPolicy_ImgAndFontSrcSelf proves the mapped policy widens img-src and
// font-src to 'self' across every profile: the host theme stylesheet's
// @font-face files and a WithBrand component's logo imagery are same-origin
// surfaces of the ADAPTER, not the bundle, and must load under the feature's
// default-src 'none'.
func TestHTMLPolicy_ImgAndFontSrcSelf(t *testing.T) {
	for _, p := range []uigoth.Profile{uigoth.StylesOnly, uigoth.Interactive, uigoth.Full} {
		dirs := viewsForProfile(t, p).resourceDirectives()
		for _, kind := range []authentication.HTMLResourceKind{authentication.HTMLImgSrc, authentication.HTMLFontSrc} {
			dir, ok := findDirective(dirs, kind)
			if !ok {
				t.Fatalf("profile %d: produced policy has no %s directive", p, kind)
			}
			if !hasSource(dir.Sources, "'self'") {
				t.Errorf("profile %d: %s missing 'self': %v", p, kind, dir.Sources)
			}
		}
	}
}
