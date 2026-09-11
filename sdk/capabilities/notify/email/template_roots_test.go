package email_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

func TestRendererRejectsEmptyExecutableRoots(t *testing.T) {
	for _, extension := range []string{".html", ".txt"} {
		for _, content := range []string{"", " \n\t{{/* no content */}}", "{{define \"wrong-name\"}}Hidden{{end}}"} {
			for _, layout := range []bool{false, true} {
				files := fstest.MapFS{"templates/body" + extension: {Data: []byte(content)}}
				option := email.WithContentTemplates("test", files, email.LayerApp)
				if layout {
					option = email.WithLayouts(files, "templates", email.LayerApp)
				}
				renderer, err := email.NewRenderer(option)
				if renderer != nil || !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("extension=%s layout=%v content=%q: renderer=%v err=%v", extension, layout, content, renderer, err)
				}
			}
		}
	}
}

func TestRendererExecutesNamedRootsAndNestedTemplates(t *testing.T) {
	files := fstest.MapFS{
		"templates/body.html": {Data: []byte("{{define \"test:body\"}}<p>{{template \"value\" .}}</p>{{end}}{{define \"value\"}}{{.}}{{end}}")},
		"templates/body.txt":  {Data: []byte("{{define \"test:body.text\"}}{{template \"value\" .}}{{end}}{{define \"value\"}}{{.}}{{end}}")},
	}
	layouts := fstest.MapFS{
		"layouts/transactional.html": {Data: []byte("{{define \"layout:transactional\"}}<article>{{.Content}}</article>{{end}}")},
		"layouts/transactional.txt":  {Data: []byte("{{define \"layout:transactional.text\"}}Text: {{.Content}}{{end}}")},
	}
	renderer, err := email.NewRenderer(
		email.WithContentTemplates("test", files, email.LayerApp),
		email.WithLayouts(layouts, "layouts", email.LayerApp),
	)
	if err != nil {
		t.Fatal(err)
	}
	html, text, err := renderer.Render(email.RenderRequest{Template: "test:body", Data: "A & B"})
	if err != nil || !strings.Contains(html, "<article><p>A &amp; B</p></article>") || text != "Text: A & B" {
		t.Fatalf("named root/layout lost: html=%q text=%q err=%v", html, text, err)
	}
}
