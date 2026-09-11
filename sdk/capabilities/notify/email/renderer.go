package email

import (
	"fmt"
	"io/fs"

	"github.com/gopernicus/gopernicus/sdk"
)

// LayoutType selects the layout wrapping rendered email content.
type LayoutType string

const (
	LayoutTransactional LayoutType = "transactional"
	LayoutMarketing     LayoutType = "marketing"
	LayoutMinimal       LayoutType = "minimal"
)

// RenderRequest supplies content and layout data. Subject is available to the
// layout without duplicating it in Data. Data is passed unchanged to content
// templates and as .Data in layouts, whether it is a struct, map or other value.
type RenderRequest struct {
	Template string
	Subject  string
	Data     any
	// Empty uses LayoutTransactional. An unknown explicit layout is an error.
	Layout LayoutType
}

// Renderer owns immutable parsed templates and branding. It needs no sender and
// is safe for concurrent rendering when the caller's Data is safe to read.
type Renderer struct{ templates *templateRegistry }

// Option configures templates or branding during construction only.
type Option struct {
	apply func(*templateRegistry) error
}

// WithContentTemplates loads .html and .txt content under templates/ in fsys.
// Each is resolved independently by App > Core > Infra. Rendering requires both.
func WithContentTemplates(namespace string, fsys fs.FS, layer TemplateLayer) Option {
	return Option{apply: func(tr *templateRegistry) error {
		return tr.registerTemplates(namespace, fsys, "templates", layer)
	}}
}

// WithLayouts loads named layout pairs from dir. The highest-priority layer owns
// the complete pair; an absent format leaves that format's content unwrapped.
func WithLayouts(fsys fs.FS, dir string, layer TemplateLayer) Option {
	return Option{apply: func(tr *templateRegistry) error { return tr.registerLayouts(fsys, dir, layer) }}
}

// WithBranding snapshots branding and its social links during construction.
// Nil selects empty branding.
func WithBranding(branding *Branding) Option {
	return Option{apply: func(tr *templateRegistry) error { tr.setBranding(branding); return nil }}
}

// NewRenderer parses all configuration before publishing a renderer. Failed
// construction returns no partially configured instance.
func NewRenderer(opts ...Option) (*Renderer, error) {
	templates, err := newTemplateRegistry()
	if err != nil {
		return nil, err
	}
	for _, opt := range opts {
		if opt.apply == nil {
			return nil, fmt.Errorf("email: empty renderer option: %w", sdk.ErrInvalidInput)
		}
		if err := opt.apply(templates); err != nil {
			return nil, fmt.Errorf("email: configure renderer: %w", err)
		}
	}
	return &Renderer{templates: templates}, nil
}

// Render returns deliberate HTML and plain-text alternatives. It never sends,
// guesses text from HTML, or hides an error in a registered template.
func (r *Renderer) Render(req RenderRequest) (html, text string, err error) {
	return r.templates.render(req)
}
