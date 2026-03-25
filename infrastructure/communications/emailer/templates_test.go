package emailer

import (
	"html/template"
	"strings"
	"testing"
)

func TestNewTemplateRegistry(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	// Infrastructure layouts should be loaded.
	for _, lt := range []LayoutType{LayoutTransactional, LayoutMarketing, LayoutMinimal} {
		pair, err := tr.ResolveLayout(lt)
		if err != nil {
			t.Errorf("ResolveLayout(%q) error = %v", lt, err)
			continue
		}
		if pair.html == nil {
			t.Errorf("ResolveLayout(%q) HTML template is nil", lt)
		}
		if pair.text == nil {
			t.Errorf("ResolveLayout(%q) text template is nil", lt)
		}
	}
}

func TestResolveContent_NotFound(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	_, err = tr.ResolveContent("nonexistent:template")
	if err == nil {
		t.Fatal("ResolveContent() should return error for nonexistent template")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain 'not found'", err.Error())
	}
}

func TestResolveLayout_Fallback(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	// Unknown layout types should fallback to transactional.
	pair, err := tr.ResolveLayout(LayoutType("unknown"))
	if err != nil {
		t.Fatalf("ResolveLayout(unknown) error = %v", err)
	}
	if pair.html == nil {
		t.Error("fallback layout should have HTML template")
	}
}

func TestLayerPriority(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	// Register the same template at different layers.
	registerContent(t, tr, "test:greeting", LayerInfra, `<p>Infra version</p>`)
	registerContent(t, tr, "test:greeting", LayerCore, `<p>Core version</p>`)

	// Core should win over Infra.
	tmpl, err := tr.ResolveContent("test:greeting")
	if err != nil {
		t.Fatalf("ResolveContent() error = %v", err)
	}

	result := executeTemplate(t, tmpl, nil)
	if !strings.Contains(result, "Core version") {
		t.Errorf("expected Core version, got %q", result)
	}

	// Register App layer — should override Core.
	registerContent(t, tr, "test:greeting", LayerApp, `<p>App version</p>`)

	tmpl, err = tr.ResolveContent("test:greeting")
	if err != nil {
		t.Fatalf("ResolveContent() error = %v", err)
	}

	result = executeTemplate(t, tmpl, nil)
	if !strings.Contains(result, "App version") {
		t.Errorf("expected App version, got %q", result)
	}
}

func TestSetBranding(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	branding := &Branding{
		Name:    "TestApp",
		Tagline: "The best app",
		LogoURL: "https://example.com/logo.png",
		Address: "123 Main St",
		SocialLinks: []SocialLink{
			{Name: "Twitter", URL: "https://twitter.com/test"},
		},
		UnsubscribeURL: "https://example.com/unsub",
		PreferencesURL: "https://example.com/prefs",
	}

	tr.SetBranding(branding)

	tr.mu.RLock()
	defer tr.mu.RUnlock()

	if tr.branding.Name != "TestApp" {
		t.Errorf("branding.Name = %q, want %q", tr.branding.Name, "TestApp")
	}
	if len(tr.branding.SocialLinks) != 1 {
		t.Errorf("branding.SocialLinks length = %d, want 1", len(tr.branding.SocialLinks))
	}
}

func TestRenderWithLayout(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	// Register a simple content template.
	registerContent(t, tr, "test:welcome", LayerApp, `<h1>Welcome {{.Name}}</h1>`)

	data := map[string]any{
		"Name": "Alice",
	}

	html, text, err := tr.RenderWithLayout("test:welcome", data, LayoutTransactional)
	if err != nil {
		t.Fatalf("RenderWithLayout() error = %v", err)
	}

	// HTML should contain the rendered content wrapped in the layout.
	if !strings.Contains(html, "Welcome Alice") {
		t.Errorf("HTML does not contain rendered content: %q", html)
	}

	// Text should have a stripped version.
	if !strings.Contains(text, "Welcome Alice") {
		t.Errorf("text does not contain rendered content: %q", text)
	}
}

func TestRenderWithLayout_WithTextTemplate(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	// Register both HTML and text content templates.
	registerContent(t, tr, "test:alert", LayerApp, `<h1>Alert: {{.Message}}</h1>`)
	registerContent(t, tr, "test:alert.text", LayerApp, `ALERT: {{.Message}}`)

	data := map[string]any{
		"Message": "System is down",
	}

	html, text, err := tr.RenderWithLayout("test:alert", data, LayoutMinimal)
	if err != nil {
		t.Fatalf("RenderWithLayout() error = %v", err)
	}

	if !strings.Contains(html, "Alert: System is down") {
		t.Errorf("HTML does not contain content: %q", html)
	}
	if !strings.Contains(text, "ALERT: System is down") {
		t.Errorf("text does not contain custom text: %q", text)
	}
}

func TestRenderWithLayout_MissingTemplate(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	_, _, err = tr.RenderWithLayout("nonexistent:template", nil, LayoutTransactional)
	if err == nil {
		t.Fatal("RenderWithLayout() should return error for missing template")
	}
}

func TestRegisterTemplatesFromDir(t *testing.T) {
	tr, err := newTemplateRegistry()
	if err != nil {
		t.Fatalf("newTemplateRegistry() error = %v", err)
	}

	// Use the infrastructure layout templates as a test embed.FS.
	err = tr.RegisterTemplatesFromDir("layouts", infraLayoutTemplates, "templates/layouts", LayerApp)
	if err != nil {
		t.Fatalf("RegisterTemplatesFromDir() error = %v", err)
	}

	// Should have registered templates with "layouts:" prefix.
	tmpl, err := tr.ResolveContent("layouts:transactional")
	if err != nil {
		t.Fatalf("ResolveContent() error = %v", err)
	}
	if tmpl == nil {
		t.Error("template should not be nil")
	}
}

func TestStripHTMLTags_Comprehensive(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple paragraph", "<p>Hello</p>", "Hello"},
		{"nested tags", "<div><h1>Title</h1><p>Body</p></div>", "TitleBody"},
		{"self-closing tag", "Line 1<br/>Line 2", "Line 1Line 2"},
		{"no tags", "Plain text", "Plain text"},
		{"empty string", "", ""},
		{"only tags", "<div><span></span></div>", ""},
		{"attributes", `<a href="https://example.com">Link</a>`, "Link"},
		{"multiline", "<p>Line 1</p>\n<p>Line 2</p>", "Line 1\nLine 2"},
		{"whitespace around tags", "  <p>Hello</p>  ", "Hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripHTMLTags(tt.input)
			if result != tt.expected {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// Test Helpers
// =============================================================================

// registerContent directly registers a parsed template in the registry.
func registerContent(t *testing.T, tr *TemplateRegistry, name string, layer TemplateLayer, content string) {
	t.Helper()

	tr.mu.Lock()
	defer tr.mu.Unlock()

	tmpl, err := template.New(name).Parse(content)
	if err != nil {
		t.Fatalf("template.Parse(%q) error = %v", name, err)
	}

	if tr.contentTemplates[name] == nil {
		tr.contentTemplates[name] = make(map[TemplateLayer]*template.Template)
	}
	tr.contentTemplates[name][layer] = tmpl
}

// executeTemplate renders a template to a string.
func executeTemplate(t *testing.T, tmpl *template.Template, data any) string {
	t.Helper()

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute() error = %v", err)
	}
	return buf.String()
}
