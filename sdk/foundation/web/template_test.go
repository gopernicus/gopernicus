package web

import (
	"context"
	"html/template"
	"strings"
	"testing"
)

// Template's return value must satisfy Renderer — it is the whole point of the
// adapter, and web.Render only accepts a Renderer.
var _ Renderer = Template(template.New("x"), "x", nil)

func TestTemplate_Render(t *testing.T) {
	tmpl := template.Must(template.New("greet").Parse(`Hello, {{.Name}}!`))

	tests := []struct {
		name string
		tmpl *template.Template
		exec string
		data any
		want string
	}{
		{"renders named template against data", tmpl, "greet", struct{ Name string }{"world"}, "Hello, world!"},
		{"escapes html in data", tmpl, "greet", struct{ Name string }{"<b>"}, "Hello, &lt;b&gt;!"},
		{"nil data renders zero values", template.Must(template.New("plain").Parse("static")), "plain", nil, "static"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			r := Template(tt.tmpl, tt.exec, tt.data)

			if err := r.Render(context.Background(), &buf); err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTemplate_Render_PropagatesTemplateError(t *testing.T) {
	tests := []struct {
		name string
		exec string
	}{
		{"unknown template name", "does-not-exist"},
		{"execution error surfaces from a bad field", "greet"},
	}

	// greet references .Missing on data that lacks it, so ExecuteTemplate fails.
	tmpl := template.Must(template.New("greet").Option("missingkey=error").Parse(`{{.Missing}}`))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			r := Template(tmpl, tt.exec, map[string]string{})

			if err := r.Render(context.Background(), &buf); err == nil {
				t.Fatalf("Render() error = nil, want a propagated template error")
			}
		})
	}
}

func TestTemplate_Render_HonorsCancelledContext(t *testing.T) {
	tmpl := template.Must(template.New("greet").Parse(`Hello`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var buf strings.Builder
	r := Template(tmpl, "greet", nil)

	if err := r.Render(ctx, &buf); err != context.Canceled {
		t.Errorf("Render() error = %v, want context.Canceled", err)
	}
	if buf.Len() != 0 {
		t.Errorf("Render() wrote %q to w on a cancelled context, want nothing written", buf.String())
	}
}
