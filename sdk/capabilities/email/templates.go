package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed templates/layouts/*
var infraLayoutTemplates embed.FS

// TemplateLayer is a source of templates with a priority. Higher values win:
// App overrides Core, Core overrides Infra.
type TemplateLayer int

const (
	// LayerInfra is the lowest priority — generic fallback templates shipped
	// with sdk/capabilities/email.
	LayerInfra TemplateLayer = iota

	// LayerCore is the middle priority — domain defaults (e.g. auth templates).
	LayerCore

	// LayerApp is the highest priority — app-level overrides and branded layouts.
	LayerApp
)

// Branding holds app-specific data for layout templates, reachable via
// {{.Brand.Name}}, {{.Brand.LogoURL}}, and so on.
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

// TemplateRegistry manages email templates from multiple sources with layered
// resolution. Templates may be registered at the Infra (fallback), Core
// (defaults), or App (overrides) layer; resolution walks App → Core → Infra.
type TemplateRegistry struct {
	// contentTemplates maps "namespace:name" → layer → parsed template.
	contentTemplates map[string]map[TemplateLayer]*template.Template

	// layoutTemplates maps layout type → layer → parsed html/text pair.
	layoutTemplates map[LayoutType]map[TemplateLayer]*layoutPair

	branding *Branding

	mu sync.RWMutex
}

// layoutPair holds the HTML and text versions of one layout.
type layoutPair struct {
	html *template.Template
	text *template.Template
}

// layoutData is passed to layout templates during rendering.
type layoutData struct {
	Content template.HTML
	Subject string
	Brand   *Branding
	Data    map[string]any
}

// newTemplateRegistry creates a registry pre-loaded with the embedded
// infrastructure layouts.
func newTemplateRegistry() (*TemplateRegistry, error) {
	tr := &TemplateRegistry{
		contentTemplates: make(map[string]map[TemplateLayer]*template.Template),
		layoutTemplates:  make(map[LayoutType]map[TemplateLayer]*layoutPair),
		branding:         &Branding{},
	}

	if err := tr.RegisterLayouts(infraLayoutTemplates, "templates/layouts", LayerInfra); err != nil {
		return nil, fmt.Errorf("load infrastructure layouts: %w", err)
	}

	return tr, nil
}

// SetBranding sets the branding configuration used by layout templates.
func (tr *TemplateRegistry) SetBranding(branding *Branding) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.branding = branding
}

// RegisterTemplates registers content templates from an embed.FS under a
// namespace, loading from the "templates" subdirectory. For example,
// RegisterTemplates("authentication", authTemplates, LayerCore) makes files
// available as "authentication:name".
func (tr *TemplateRegistry) RegisterTemplates(namespace string, fsys embed.FS, layer TemplateLayer) error {
	return tr.RegisterTemplatesFromDir(namespace, fsys, "templates", layer)
}

// RegisterTemplatesFromDir registers content templates from a specific
// directory within an embed.FS. Files ending in .txt become the ".text"
// variant of their base name; partials (names starting with "_") are skipped.
func (tr *TemplateRegistry) RegisterTemplatesFromDir(namespace string, fsys embed.FS, dir string, layer TemplateLayer) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".txt") {
			return nil
		}

		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("read template file %s: %w", path, err)
		}

		filename := filepath.Base(path)
		var templateName string
		if strings.HasSuffix(path, ".txt") {
			templateName = strings.TrimSuffix(filename, ".txt") + ".text"
		} else {
			templateName = strings.TrimSuffix(filename, ".html")
		}

		if strings.HasPrefix(templateName, "_") {
			return nil
		}

		fullName := fmt.Sprintf("%s:%s", namespace, templateName)

		tmpl, err := template.New(fullName).Parse(string(content))
		if err != nil {
			return fmt.Errorf("parse template %s: %w", path, err)
		}

		if tr.contentTemplates[fullName] == nil {
			tr.contentTemplates[fullName] = make(map[TemplateLayer]*template.Template)
		}
		tr.contentTemplates[fullName][layer] = tmpl

		return nil
	})
}

// RegisterLayouts registers layout templates from an embed.FS. Layout files
// should be named transactional.html, transactional.txt, and so on; the base
// name (minus extension) is the layout type.
func (tr *TemplateRegistry) RegisterLayouts(fsys embed.FS, dir string, layer TemplateLayer) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	layouts := make(map[LayoutType]*layoutPair)

	err := fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".txt") {
			return nil
		}

		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("read layout file %s: %w", path, err)
		}

		filename := filepath.Base(path)
		isText := strings.HasSuffix(path, ".txt")
		layoutName := strings.TrimSuffix(strings.TrimSuffix(filename, ".html"), ".txt")
		layoutType := LayoutType(layoutName)

		tmplName := fmt.Sprintf("layout:%s", layoutName)
		if isText {
			tmplName += ".text"
		}
		tmpl, err := template.New(tmplName).Parse(string(content))
		if err != nil {
			return fmt.Errorf("parse layout %s: %w", path, err)
		}

		if layouts[layoutType] == nil {
			layouts[layoutType] = &layoutPair{}
		}
		if isText {
			layouts[layoutType].text = tmpl
		} else {
			layouts[layoutType].html = tmpl
		}

		return nil
	})
	if err != nil {
		return err
	}

	for layoutType, pair := range layouts {
		if tr.layoutTemplates[layoutType] == nil {
			tr.layoutTemplates[layoutType] = make(map[TemplateLayer]*layoutPair)
		}
		tr.layoutTemplates[layoutType][layer] = pair
	}

	return nil
}

// ResolveContent returns the highest-priority content template for name,
// walking App → Core → Infra.
func (tr *TemplateRegistry) ResolveContent(templateName string) (*template.Template, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	layers, exists := tr.contentTemplates[templateName]
	if !exists {
		return nil, fmt.Errorf("template %q not found", templateName)
	}

	for layer := LayerApp; layer >= LayerInfra; layer-- {
		if tmpl, ok := layers[layer]; ok {
			return tmpl, nil
		}
	}

	return nil, fmt.Errorf("template %q not found in any layer", templateName)
}

// ResolveLayout returns the highest-priority layout for layoutType, walking
// App → Core → Infra. Unknown layouts fall back to the transactional layout.
func (tr *TemplateRegistry) ResolveLayout(layoutType LayoutType) (*layoutPair, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	layers, exists := tr.layoutTemplates[layoutType]
	if !exists {
		layers, exists = tr.layoutTemplates[LayoutTransactional]
		if !exists {
			return nil, fmt.Errorf("layout %q not found and no fallback available", layoutType)
		}
	}

	for layer := LayerApp; layer >= LayerInfra; layer-- {
		if pair, ok := layers[layer]; ok {
			return pair, nil
		}
	}

	return nil, fmt.Errorf("layout %q not found in any layer", layoutType)
}

// RenderWithLayout renders a content template wrapped in a layout and returns
// both the HTML and plain-text versions. When no ".text" content template
// exists, the text version is derived by stripping HTML tags from the rendered
// content.
func (tr *TemplateRegistry) RenderWithLayout(templateName string, data any, layoutType LayoutType) (string, string, error) {
	tr.mu.RLock()
	branding := tr.branding
	tr.mu.RUnlock()

	contentHTML, err := tr.renderContent(templateName, data)
	if err != nil {
		return "", "", fmt.Errorf("render content HTML: %w", err)
	}

	contentText, textErr := tr.renderContent(templateName+".text", data)
	if textErr != nil {
		contentText = stripHTMLTags(contentHTML)
	}

	layout, err := tr.ResolveLayout(layoutType)
	if err != nil {
		return "", "", fmt.Errorf("resolve layout: %w", err)
	}

	var dataMap map[string]any
	if m, ok := data.(map[string]any); ok {
		dataMap = m
	}

	subject := ""
	if dataMap != nil {
		if s, ok := dataMap["Subject"].(string); ok {
			subject = s
		}
	}

	ld := layoutData{
		Content: template.HTML(contentHTML),
		Subject: subject,
		Brand:   branding,
		Data:    dataMap,
	}

	var htmlResult string
	if layout.html != nil {
		var htmlBuf bytes.Buffer
		if err := layout.html.Execute(&htmlBuf, ld); err != nil {
			return "", "", fmt.Errorf("render HTML layout: %w", err)
		}
		htmlResult = htmlBuf.String()
	} else {
		htmlResult = contentHTML
	}

	ldText := layoutData{
		Content: template.HTML(contentText),
		Subject: subject,
		Brand:   branding,
		Data:    dataMap,
	}

	var textResult string
	if layout.text != nil {
		var textBuf bytes.Buffer
		if err := layout.text.Execute(&textBuf, ldText); err != nil {
			return "", "", fmt.Errorf("render text layout: %w", err)
		}
		textResult = textBuf.String()
	} else {
		textResult = contentText
	}

	return htmlResult, textResult, nil
}

// renderContent renders a single content template to a string.
func (tr *TemplateRegistry) renderContent(templateName string, data any) (string, error) {
	tmpl, err := tr.ResolveContent(templateName)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", templateName, err)
	}

	return buf.String(), nil
}

// stripHTMLTags removes HTML tags, used as the text fallback when no ".text"
// content template is registered.
func stripHTMLTags(html string) string {
	var result strings.Builder
	inTag := false

	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}

	return strings.TrimSpace(result.String())
}
