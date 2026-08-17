package email

import (
	"html/template"
	"reflect"
	"strings"
	"testing"
)

// registerLayoutHTML directly registers an HTML layout at a layer, used by tests
// to seed a host override without an embed.FS.
func registerLayoutHTML(t *testing.T, tr *TemplateRegistry, layoutType LayoutType, layer TemplateLayer, content string) {
	t.Helper()

	tmpl, err := template.New(string(layoutType)).Parse(content)
	if err != nil {
		t.Fatalf("template.Parse() error = %v", err)
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tr.layoutTemplates[layoutType] == nil {
		tr.layoutTemplates[layoutType] = make(map[TemplateLayer]*layoutPair)
	}
	tr.layoutTemplates[layoutType][layer] = &layoutPair{html: tmpl}
}

// fullBranding is every Branding field populated with a distinguishable marker
// so a layout matrix assertion can tell which fields a bundled layout renders.
func fullBranding() *Branding {
	return &Branding{
		Name:           "BrandNameMarker",
		Tagline:        "BrandTaglineMarker",
		LogoURL:        "https://cdn.example.com/logo-marker.png",
		Address:        "BrandAddressMarker",
		SocialLinks:    []SocialLink{{Name: "SocialNameMarker", URL: "https://social.example.com/marker"}},
		UnsubscribeURL: "https://example.com/unsubscribe-marker",
		PreferencesURL: "https://example.com/preferences-marker",
	}
}

// renderBranded renders a trivial content template through layoutType with the
// supplied branding and returns the HTML and text results.
func renderBranded(t *testing.T, branding *Branding, layoutType LayoutType) (string, string) {
	t.Helper()

	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}
	registerContent(t, tr, "test:body", LayerApp, `<p>BodyMarker</p>`)
	tr.SetBranding(branding)

	html, text, err := tr.RenderWithLayout("test:body", map[string]any{"Subject": "SubjectMarker"}, layoutType)
	if err != nil {
		t.Fatalf("RenderWithLayout(%q) error = %v", layoutType, err)
	}
	if !strings.Contains(html, "BodyMarker") {
		t.Fatalf("HTML lost the content body: %q", html)
	}
	return html, text
}

// TestBundledLayoutBrandingMatrix pins which branding fields each bundled layout
// renders. Changing a "no" to a "yes" is an adopter-visible output change and a
// deliberate design decision, not an incidental template edit.
func TestBundledLayoutBrandingMatrix(t *testing.T) {
	tests := []struct {
		layout      LayoutType
		logo        bool
		name        bool
		tagline     bool
		address     bool
		social      bool
		unsubscribe bool
	}{
		{
			layout: LayoutTransactional,
			logo:   true, name: true, tagline: true, address: true, social: true, unsubscribe: false,
		},
		{
			layout: LayoutMarketing,
			logo:   true, name: true, tagline: false, address: true, social: true, unsubscribe: true,
		},
		{
			layout: LayoutMinimal,
			logo:   false, name: false, tagline: false, address: false, social: false, unsubscribe: false,
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.layout), func(t *testing.T) {
			branding := fullBranding()
			html, _ := renderBranded(t, branding, tt.layout)

			fields := []struct {
				label  string
				marker string
				want   bool
			}{
				{"LogoURL", branding.LogoURL, tt.logo},
				{"Name", branding.Name, tt.name},
				{"Tagline", branding.Tagline, tt.tagline},
				{"Address", branding.Address, tt.address},
				{"SocialLinks", branding.SocialLinks[0].URL, tt.social},
				{"UnsubscribeURL", branding.UnsubscribeURL, tt.unsubscribe},
			}

			for _, f := range fields {
				got := strings.Contains(html, f.marker)
				if got != f.want {
					t.Errorf("layout %q renders %s = %v, want %v", tt.layout, f.label, got, f.want)
				}
			}
		})
	}
}

// TestTransactionalLayoutRendersLogo is the phase-4 fix: the auth-default
// transactional layout previously dropped Branding.LogoURL entirely.
func TestTransactionalLayoutRendersLogo(t *testing.T) {
	branding := fullBranding()
	html, _ := renderBranded(t, branding, LayoutTransactional)

	if !strings.Contains(html, `src="`+branding.LogoURL+`"`) {
		t.Errorf("transactional HTML missing logo img src: %q", html)
	}
	if !strings.Contains(html, `alt="`+branding.Name+`"`) {
		t.Errorf("transactional HTML logo alt is not the brand name: %q", html)
	}
	if !strings.Contains(html, "max-height: 48px") {
		t.Errorf("transactional logo missing conservative max-height: %q", html)
	}
	// The name and tagline must survive alongside the image so a blocked or
	// broken logo does not erase brand identity.
	if !strings.Contains(html, branding.Name) || !strings.Contains(html, branding.Tagline) {
		t.Errorf("transactional HTML dropped name/tagline text fallback: %q", html)
	}
}

// TestTransactionalLayoutOmitsLogoWhenUnset proves an empty LogoURL emits no
// <img> at all rather than an image with an empty source.
func TestTransactionalLayoutOmitsLogoWhenUnset(t *testing.T) {
	branding := fullBranding()
	branding.LogoURL = ""

	html, _ := renderBranded(t, branding, LayoutTransactional)

	if strings.Contains(html, "<img") {
		t.Errorf("transactional HTML emitted an <img> with no LogoURL: %q", html)
	}
	if !strings.Contains(html, branding.Name) {
		t.Errorf("transactional HTML lost the brand name: %q", html)
	}
}

// TestTransactionalLogoAltFallback documents the alt text used when the host
// sets a logo but no brand name. It matches the visible header fallback so the
// image and the text identity never disagree.
func TestTransactionalLogoAltFallback(t *testing.T) {
	html, _ := renderBranded(t, &Branding{LogoURL: "https://cdn.example.com/logo-marker.png"}, LayoutTransactional)

	if !strings.Contains(html, `alt="Your Company"`) {
		t.Errorf("transactional logo missing the documented alt fallback: %q", html)
	}
}

// TestTransactionalLogoEscaping proves html/template — not the caller — is the
// injection boundary for LogoURL and the brand name used as alt text.
func TestTransactionalLogoEscaping(t *testing.T) {
	tests := []struct {
		name    string
		brand   *Branding
		absent  []string
		present []string
	}{
		{
			name: "attribute break out",
			brand: &Branding{
				Name:    `"><script>alert(1)</script>`,
				LogoURL: `https://cdn.example.com/l.png" onerror="alert(1)`,
			},
			absent:  []string{"<script>", `onerror="alert(1)"`},
			present: []string{"&lt;script&gt;", "&#34;"},
		},
		{
			name: "javascript scheme",
			brand: &Branding{
				Name:    "Brand",
				LogoURL: "javascript:alert(1)",
			},
			absent:  []string{"javascript:alert(1)"},
			present: []string{"#ZgotmplZ"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, _ := renderBranded(t, tt.brand, LayoutTransactional)

			for _, want := range tt.present {
				if !strings.Contains(html, want) {
					t.Errorf("HTML missing expected escaped output %q: %q", want, html)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(html, bad) {
					t.Errorf("HTML contains unescaped %q: %q", bad, html)
				}
			}
		})
	}
}

// TestBundledTextLayoutsStayImageFree proves the plain-text alternatives are
// unchanged by the logo work: no markup, and no bare logo URL leaking into the
// text body just because branding carries one.
func TestBundledTextLayoutsStayImageFree(t *testing.T) {
	for _, layout := range []LayoutType{LayoutTransactional, LayoutMarketing, LayoutMinimal} {
		t.Run(string(layout), func(t *testing.T) {
			branding := fullBranding()
			_, text := renderBranded(t, branding, layout)

			if strings.Contains(text, "<img") {
				t.Errorf("text layout %q contains markup: %q", layout, text)
			}
			if strings.Contains(text, branding.LogoURL) {
				t.Errorf("text layout %q leaked the logo URL: %q", layout, text)
			}
		})
	}
}

// TestBrandingAbsentRendersCleanly covers the empty and nil branding postures.
// The registry's branding is never nil, so layouts never fail on a missing
// brand and never emit an image.
func TestBrandingAbsentRendersCleanly(t *testing.T) {
	tests := []struct {
		name  string
		brand *Branding
	}{
		{"empty branding", &Branding{}},
		{"nil branding", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, layout := range []LayoutType{LayoutTransactional, LayoutMarketing, LayoutMinimal} {
				html, text := renderBranded(t, tt.brand, layout)

				if strings.Contains(html, "<img") {
					t.Errorf("layout %q emitted an <img> without branding: %q", layout, html)
				}
				if !strings.Contains(text, "BodyMarker") {
					t.Errorf("layout %q text lost the content body: %q", layout, text)
				}
			}
		})
	}
}

// TestSetBrandingNilKeepsInvariant pins the non-nil branding invariant directly.
func TestSetBrandingNilKeepsInvariant(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	tr.SetBranding(nil)

	tr.mu.RLock()
	defer tr.mu.RUnlock()

	if tr.branding == nil {
		t.Fatal("SetBranding(nil) left the registry branding nil")
	}
	if !reflect.DeepEqual(*tr.branding, Branding{}) {
		t.Errorf("SetBranding(nil) = %+v, want zero Branding", *tr.branding)
	}
}

// TestAppLayoutOverrideWinsOverBundledLogo proves the phase-4 change does not
// affect hosts that replace the transactional layout at LayerApp.
func TestAppLayoutOverrideWinsOverBundledLogo(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}
	registerContent(t, tr, "test:body", LayerApp, `<p>BodyMarker</p>`)
	tr.SetBranding(fullBranding())

	registerLayoutHTML(t, tr, LayoutTransactional, LayerApp, `<html><body>AppLayout{{.Content}}</body></html>`)

	html, _, err := tr.RenderWithLayout("test:body", map[string]any{}, LayoutTransactional)
	if err != nil {
		t.Fatalf("RenderWithLayout() error = %v", err)
	}

	if !strings.Contains(html, "AppLayout") {
		t.Errorf("app layout override did not win: %q", html)
	}
	if strings.Contains(html, "<img") {
		t.Errorf("app layout override still rendered the bundled logo: %q", html)
	}
}
