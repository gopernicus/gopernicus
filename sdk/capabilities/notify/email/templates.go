package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	texttemplate "text/template"
	"text/template/parse"

	"github.com/gopernicus/gopernicus/sdk"
)

//go:embed templates/layouts/*
var infraLayoutTemplates embed.FS

// TemplateLayer is a source of templates with a priority. Higher values win:
// App overrides Core, Core overrides Infra.
type TemplateLayer int

const (
	// LayerInfra is the lowest priority — generic fallback templates shipped
	// with sdk/capabilities/notify/email.
	LayerInfra TemplateLayer = iota

	// LayerCore is the middle priority — domain defaults (e.g. auth templates).
	LayerCore

	// LayerApp is the highest priority — app-level overrides and branded layouts.
	LayerApp
)

// Branding holds app-specific data for layout templates, reachable via
// {{.Brand.Name}}, {{.Brand.LogoURL}}, and so on.
//
// Bundled layouts do not all render every field. The shipped matrix is:
//
//	layout         logo  name  tagline  address  social  unsubscribe
//	transactional  yes   yes   yes      yes      yes     no
//	marketing      yes   yes   no       yes      yes     yes
//	minimal        no    no    no       no       no      no
//
// LayoutMinimal is deliberately unbranded and is the content-only fallback.
// LogoURL should be an absolute, publicly fetchable HTTPS image URL: the
// renderer never fetches, resolves, or validates it, and mail clients block
// external images by default, so the brand name and tagline stay visible as the
// text fallback. The URL is interpolated through html/template, which is the
// injection safety boundary; it is never treated as template.HTML.
type Branding struct {
	Name           string
	Tagline        string
	LogoURL        string
	Address        string
	SocialLinks    []SocialLink
	UnsubscribeURL string
	PreferencesURL string
}

// SocialLink is a single social-media link rendered in layout footers.
type SocialLink struct {
	Name string
	URL  string
}

// templateRegistry is built once and remains private and immutable after
// construction. Both template engines support the same execution operation.
type templateExecutor interface{ Execute(io.Writer, any) error }

type templateRegistry struct {
	content  map[string]map[TemplateLayer]templateExecutor
	layouts  map[LayoutType]map[TemplateLayer]*layoutPair
	branding *Branding
}

type layoutPair struct{ html, text templateExecutor }

func newTemplateRegistry() (*templateRegistry, error) {
	tr := &templateRegistry{
		content:  make(map[string]map[TemplateLayer]templateExecutor),
		layouts:  make(map[LayoutType]map[TemplateLayer]*layoutPair),
		branding: &Branding{},
	}
	if err := tr.registerLayouts(infraLayoutTemplates, "templates/layouts", LayerInfra); err != nil {
		return nil, err
	}
	return tr, nil
}

func (tr *templateRegistry) setBranding(branding *Branding) {
	snapshot := Branding{}
	if branding != nil {
		snapshot = *branding
		snapshot.SocialLinks = slices.Clone(branding.SocialLinks)
	}
	tr.branding = &snapshot
}

func checkLayer(layer TemplateLayer) error {
	if layer < LayerInfra || layer > LayerApp {
		return fmt.Errorf("email: invalid template layer %d: %w", layer, sdk.ErrInvalidInput)
	}
	return nil
}

func parseTemplate(name, extension, content string) (templateExecutor, error) {
	if extension == ".txt" {
		tmpl, err := texttemplate.New(name).Parse(content)
		if err != nil {
			return nil, err
		}
		if tmpl.Tree == nil || parse.IsEmptyTree(tmpl.Tree.Root) {
			return nil, fmt.Errorf("empty template root %q: %w", name, sdk.ErrInvalidInput)
		}
		return tmpl, nil
	}
	tmpl, err := template.New(name).Parse(content)
	if err != nil {
		return nil, err
	}
	if tmpl.Tree == nil || parse.IsEmptyTree(tmpl.Tree.Root) {
		return nil, fmt.Errorf("empty template root %q: %w", name, sdk.ErrInvalidInput)
	}
	return tmpl, nil
}

// walkTemplates keeps embedded and other fs.FS paths portable. Basenames form
// public template names; registration rejects collisions instead of last-wins.
func walkTemplates(fsys fs.FS, dir string, visit func(name, extension, content string) error) error {
	if fsys == nil || !fs.ValidPath(dir) {
		return fmt.Errorf("email: invalid template filesystem or directory: %w", sdk.ErrInvalidInput)
	}
	return fs.WalkDir(fsys, dir, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		base := path.Base(filename)
		extension := path.Ext(base)
		if strings.HasPrefix(base, "_") || (extension != ".html" && extension != ".txt") {
			return nil
		}
		content, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return err
		}
		if err := visit(strings.TrimSuffix(base, extension), extension, string(content)); err != nil {
			return fmt.Errorf("email: template file %q: %w", filename, err)
		}
		return nil
	})
}

func (tr *templateRegistry) registerTemplates(namespace string, fsys fs.FS, dir string, layer TemplateLayer) error {
	if err := checkLayer(layer); err != nil {
		return err
	}
	if namespace == "" || namespace != strings.TrimSpace(namespace) || strings.Contains(namespace, ":") {
		return fmt.Errorf("email: invalid template namespace: %w", sdk.ErrInvalidInput)
	}
	return walkTemplates(fsys, dir, func(name, extension, content string) error {
		key := namespace + ":" + name
		if extension == ".txt" {
			key += ".text"
		}
		if tr.content[key] == nil {
			tr.content[key] = make(map[TemplateLayer]templateExecutor)
		}
		if tr.content[key][layer] != nil {
			return fmt.Errorf("duplicate template %q at layer %d: %w", key, layer, sdk.ErrInvalidInput)
		}
		tmpl, err := parseTemplate(key, extension, content)
		if err != nil {
			return err
		}
		tr.content[key][layer] = tmpl
		return nil
	})
}

func (tr *templateRegistry) registerLayouts(fsys fs.FS, dir string, layer TemplateLayer) error {
	if err := checkLayer(layer); err != nil {
		return err
	}
	return walkTemplates(fsys, dir, func(name, extension, content string) error {
		key := LayoutType(name)
		if tr.layouts[key] == nil {
			tr.layouts[key] = make(map[TemplateLayer]*layoutPair)
		}
		if tr.layouts[key][layer] == nil {
			tr.layouts[key][layer] = &layoutPair{}
		}
		pair := tr.layouts[key][layer]
		if (extension == ".txt" && pair.text != nil) || (extension == ".html" && pair.html != nil) {
			return fmt.Errorf("duplicate layout %q at layer %d: %w", name, layer, sdk.ErrInvalidInput)
		}
		templateName := "layout:" + name
		if extension == ".txt" {
			templateName += ".text"
		}
		tmpl, err := parseTemplate(templateName, extension, content)
		if err != nil {
			return err
		}
		if extension == ".txt" {
			pair.text = tmpl
		} else {
			pair.html = tmpl
		}
		return nil
	})
}

func (tr *templateRegistry) resolveContent(name string) (templateExecutor, error) {
	for layer := LayerApp; layer >= LayerInfra; layer-- {
		if tmpl := tr.content[name][layer]; tmpl != nil {
			return tmpl, nil
		}
	}
	return nil, fmt.Errorf("email: required content template %q not found: %w", name, sdk.ErrInvalidInput)
}

func (tr *templateRegistry) resolveLayout(layout LayoutType) (*layoutPair, error) {
	if layout == "" {
		layout = LayoutTransactional
	}
	for layer := LayerApp; layer >= LayerInfra; layer-- {
		if pair := tr.layouts[layout][layer]; pair != nil {
			return pair, nil
		}
	}
	return nil, fmt.Errorf("email: layout %q not found: %w", layout, sdk.ErrInvalidInput)
}

func execute(tmpl templateExecutor, data any) (string, error) {
	var result bytes.Buffer
	if err := tmpl.Execute(&result, data); err != nil {
		return "", err
	}
	return result.String(), nil
}

func (tr *templateRegistry) render(req RenderRequest) (string, string, error) {
	htmlTemplate, err := tr.resolveContent(req.Template)
	if err != nil {
		return "", "", err
	}
	textTemplate, err := tr.resolveContent(req.Template + ".text")
	if err != nil {
		return "", "", err
	}
	layout, err := tr.resolveLayout(req.Layout)
	if err != nil {
		return "", "", err
	}
	contentHTML, err := execute(htmlTemplate, req.Data)
	if err != nil {
		return "", "", fmt.Errorf("email: render HTML content: %w", err)
	}
	contentText, err := execute(textTemplate, req.Data)
	if err != nil {
		return "", "", fmt.Errorf("email: render text content: %w", err)
	}

	resultHTML, resultText := contentHTML, contentText
	if layout.html != nil {
		data := struct {
			Content template.HTML
			Subject string
			Brand   *Branding
			Data    any
		}{template.HTML(contentHTML), req.Subject, tr.branding, req.Data}
		resultHTML, err = execute(layout.html, data)
		if err != nil {
			return "", "", fmt.Errorf("email: render HTML layout: %w", err)
		}
	}
	if layout.text != nil {
		data := struct {
			Content string
			Subject string
			Brand   *Branding
			Data    any
		}{contentText, req.Subject, tr.branding, req.Data}
		resultText, err = execute(layout.text, data)
		if err != nil {
			return "", "", fmt.Errorf("email: render text layout: %w", err)
		}
	}
	return resultHTML, resultText, nil
}
