package emailer

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

// =============================================================================
// Template Layers
// =============================================================================

// TemplateLayer represents a source of templates with priority.
// Higher values have higher priority (app overrides core, core overrides infra).
type TemplateLayer int

const (
	// LayerInfra is the lowest priority — generic fallback templates from infrastructure.
	LayerInfra TemplateLayer = iota

	// LayerCore is the middle priority — domain defaults (e.g., auth templates).
	LayerCore

	// LayerApp is the highest priority — app-level overrides and branded layouts.
	LayerApp
)

// =============================================================================
// Branding Configuration
// =============================================================================

// Branding holds app-specific branding data for email templates.
// Templates can access this via {{.Brand.Name}}, {{.Brand.LogoURL}}, etc.
type Branding struct {
	Name           string       // Company/app name
	Tagline        string       // Short tagline or slogan
	LogoURL        string       // URL to logo image
	Address        string       // Physical address (for CAN-SPAM compliance)
	SocialLinks    []SocialLink // Social media links
	UnsubscribeURL string       // Unsubscribe link (for marketing emails)
	PreferencesURL string       // Email preferences link
}

// SocialLink represents a social media link.
type SocialLink struct {
	Name string // Display name (e.g., "Twitter", "LinkedIn")
	URL  string // Full URL
}

// =============================================================================
// Template Registry
// =============================================================================

// TemplateRegistry manages email templates from multiple sources with layered resolution.
// Templates can be registered from Infrastructure (fallback), Core (defaults), or App (overrides).
type TemplateRegistry struct {
	// Content templates: "namespace:name" -> layer -> parsed template
	contentTemplates map[string]map[TemplateLayer]*template.Template

	// Layout templates: layoutType -> layer -> parsed template pair (html + text)
	layoutTemplates map[LayoutType]map[TemplateLayer]*layoutPair

	// Branding configuration for templates
	branding *Branding

	mu sync.RWMutex
}

// layoutPair holds both HTML and text versions of a layout template.
type layoutPair struct {
	html *template.Template
	text *template.Template
}

// newTemplateRegistry creates a new template registry with infrastructure layouts loaded.
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

// =============================================================================
// Content Template Registration
// =============================================================================

// RegisterTemplates registers content templates from an embed.FS under a namespace.
// For example: RegisterTemplates("authentication", authTemplates, LayerCore)
// makes templates available as "authentication:templatename".
//
// Templates are loaded from the "templates" subdirectory of the embed.FS.
func (tr *TemplateRegistry) RegisterTemplates(namespace string, fsys embed.FS, layer TemplateLayer) error {
	return tr.RegisterTemplatesFromDir(namespace, fsys, "templates", layer)
}

// RegisterTemplatesFromDir registers content templates from a specific directory.
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

		// Skip partials (files starting with _)
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

// =============================================================================
// Layout Template Registration
// =============================================================================

// RegisterLayouts registers layout templates from an embed.FS.
// Layout templates should be named: transactional.html, transactional.txt, etc.
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

// =============================================================================
// Template Resolution
// =============================================================================

// ResolveContent finds the highest-priority content template for the given name.
// Resolution order: App > Core > Infra
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

// ResolveLayout finds the highest-priority layout template for the given type.
// Resolution order: App > Core > Infra
func (tr *TemplateRegistry) ResolveLayout(layoutType LayoutType) (*layoutPair, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	layers, exists := tr.layoutTemplates[layoutType]
	if !exists {
		// Fallback to transactional if requested layout doesn't exist
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

// =============================================================================
// Rendering
// =============================================================================

// layoutData is the data structure passed to layout templates.
type layoutData struct {
	Content template.HTML  // Rendered content template (marked safe)
	Subject string         // Email subject
	Brand   *Branding      // Branding configuration
	Data    map[string]any // Original template data
}

// RenderWithLayout renders a content template wrapped in a layout.
// Returns both HTML and plain text versions.
func (tr *TemplateRegistry) RenderWithLayout(templateName string, data any, layoutType LayoutType) (string, string, error) {
	tr.mu.RLock()
	branding := tr.branding
	tr.mu.RUnlock()

	// Step 1: Render content template (HTML)
	contentHTML, err := tr.renderContent(templateName, data)
	if err != nil {
		return "", "", fmt.Errorf("render content HTML: %w", err)
	}

	// Step 2: Render content template (text)
	textTemplateName := templateName + ".text"
	contentText, textErr := tr.renderContent(textTemplateName, data)
	if textErr != nil {
		// Fallback: strip HTML tags for text version
		contentText = stripHTMLTags(contentHTML)
	}

	// Step 3: Get layout
	layout, err := tr.ResolveLayout(layoutType)
	if err != nil {
		return "", "", fmt.Errorf("resolve layout: %w", err)
	}

	// Step 4: Build layout data
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

	// Step 5: Render HTML layout
	var htmlResult string
	var htmlBuf bytes.Buffer
	if layout.html != nil {
		if err := layout.html.Execute(&htmlBuf, ld); err != nil {
			return "", "", fmt.Errorf("render HTML layout: %w", err)
		}
		htmlResult = htmlBuf.String()
	} else {
		htmlResult = contentHTML
	}

	// Step 6: Render text layout
	ldText := layoutData{
		Content: template.HTML(contentText),
		Subject: subject,
		Brand:   branding,
		Data:    dataMap,
	}

	var textResult string
	var textBuf bytes.Buffer
	if layout.text != nil {
		if err := layout.text.Execute(&textBuf, ldText); err != nil {
			return "", "", fmt.Errorf("render text layout: %w", err)
		}
		textResult = textBuf.String()
	} else {
		textResult = contentText
	}

	return htmlResult, textResult, nil
}

// renderContent renders a single content template.
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

// =============================================================================
// Utilities
// =============================================================================

// stripHTMLTags removes HTML tags from a string.
// Used as a fallback when no text template is available.
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
