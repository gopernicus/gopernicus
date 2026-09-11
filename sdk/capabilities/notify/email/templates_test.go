package email

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/sdk"
)

func templateFiles(html, text string) fstest.MapFS {
	return fstest.MapFS{
		"templates/body.html": {Data: []byte(html)},
		"templates/body.txt":  {Data: []byte(text)},
	}
}

func TestRenderKeepsTextURLsAndStructLayoutData(t *testing.T) {
	files := templateFiles("<p>{{.Link}}</p>", "{{.Link}}")
	layouts := fstest.MapFS{
		"layouts/transactional.html": {Data: []byte("<title>{{.Subject}}</title>{{.Content}}<p>{{.Data.Name}}</p>")},
		"layouts/transactional.txt":  {Data: []byte("{{.Subject}} / {{.Brand.Name}} / {{.Data.Name}} / {{.Content}}")},
	}
	r, err := NewRenderer(WithContentTemplates("test", files, LayerApp), WithLayouts(layouts, "layouts", LayerApp), WithBranding(&Branding{Name: "A & B"}))
	if err != nil {
		t.Fatal(err)
	}
	data := struct{ Link, Name string }{"https://example.test/reset?a=1&b=2", "C & D"}
	html, text, err := r.Render(RenderRequest{Template: "test:body", Subject: "Subject & title", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "a=1&amp;b=2") || !strings.Contains(html, "<title>Subject &amp; title</title>") || !strings.Contains(html, "C &amp; D") {
		t.Fatalf("HTML escaping or layout data lost: %s", html)
	}
	if text != "Subject & title / A & B / C & D / "+data.Link {
		t.Fatalf("plain-text content was changed: %q", text)
	}
}

func TestRendererRequiresTextAndReportsTextExecutionErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		files fstest.MapFS
	}{
		{"missing text", fstest.MapFS{"templates/body.html": {Data: []byte("<a>Reset</a>")}}},
		{"broken text", templateFiles("<p>valid HTML</p>", "{{.MissingField}}")},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, err := NewRenderer(WithContentTemplates("test", test.files, LayerApp))
			if err != nil {
				t.Fatal(err)
			}
			html, text, err := r.Render(RenderRequest{Template: "test:body", Data: struct{ Value string }{"value"}})
			if err == nil || html != "" || text != "" {
				t.Fatalf("render hid failure: html=%q text=%q err=%v", html, text, err)
			}
		})
	}
}

func TestRendererLayerPriorityAndExplicitLayouts(t *testing.T) {
	r, err := NewRenderer(
		WithContentTemplates("test", templateFiles("<p>infra</p>", "infra"), LayerInfra),
		WithContentTemplates("test", templateFiles("<p>core</p>", "core"), LayerCore),
		WithContentTemplates("test", templateFiles("<p>app</p>", "app"), LayerApp),
	)
	if err != nil {
		t.Fatal(err)
	}
	html, text, err := r.Render(RenderRequest{Template: "test:body", Layout: LayoutMinimal})
	if err != nil || !strings.Contains(html, "app") || !strings.Contains(text, "app") {
		t.Fatalf("priority: %q %q %v", html, text, err)
	}
	_, _, err = r.Render(RenderRequest{Template: "test:body", Layout: "typo"})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unknown explicit layout: %v", err)
	}
	if _, _, err := r.Render(RenderRequest{Template: "test:body"}); err != nil {
		t.Fatalf("default layout: %v", err)
	}
}

func TestRendererRejectsInvalidAndAmbiguousConfiguration(t *testing.T) {
	files := templateFiles("<p>HTML</p>", "text")
	collision := fstest.MapFS{
		"templates/one/body.html": {Data: []byte("one")},
		"templates/two/body.html": {Data: []byte("two")},
	}
	for _, opts := range [][]Option{
		{WithContentTemplates("test", files, TemplateLayer(99))},
		{WithContentTemplates("", files, LayerApp)},
		{WithContentTemplates("test", collision, LayerApp)},
		{WithContentTemplates("test", files, LayerApp), WithContentTemplates("test", files, LayerApp)},
		{WithLayouts(files, "templates", TemplateLayer(-1))},
		{Option{}},
	} {
		r, err := NewRenderer(opts...)
		if r != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid configuration published: renderer=%v err=%v", r, err)
		}
	}
}

func TestRendererSnapshotsBrandingForConcurrentUse(t *testing.T) {
	branding := &Branding{Name: "Original", SocialLinks: []SocialLink{{Name: "Social", URL: "https://example.test/social"}}}
	r, err := NewRenderer(WithContentTemplates("test", templateFiles("HTML", "text"), LayerApp), WithBranding(branding))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			branding.Name = "Changed"
			branding.SocialLinks[0].Name = "Changed"
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			html, _, err := r.Render(RenderRequest{Template: "test:body"})
			if err != nil || !strings.Contains(html, "Original") || strings.Contains(html, "Changed") {
				t.Errorf("snapshot lost: err=%v", err)
				return
			}
		}
	}()
	wg.Wait()
}

// Branding matrix fixtures use explicit HTML/text, independent of fallback logic.
func registerBrandingContent(t *testing.T, tr *templateRegistry) {
	t.Helper()
	if err := tr.registerTemplates("test", templateFiles("<p>BodyMarker</p>", "BodyMarker"), "templates", LayerApp); err != nil {
		t.Fatal(err)
	}
}
