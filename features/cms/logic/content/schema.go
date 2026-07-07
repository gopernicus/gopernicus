package content

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/slug"
)

// FieldDef declares one custom field of a content type: its storage key, its
// editor label, its kind, and validation/render hints. FieldDefs are
// code-authored data (locked decision 3) — there is no DB-stored schema and no
// field-builder UI in v1. The generated editor renders one input per FieldDef.
type FieldDef struct {
	Key      string    // storage key in entry_fields
	Label    string    // editor label; "" → derived from Key
	Kind     FieldKind // governs coercion + which editor input renders
	Required bool      // enforced on write by the Registry
	RelTo    string    // KindRelation: target content type slug
	Help     string    // optional editor help text
}

// DisplayLabel returns Label, or a title-cased fallback derived from Key.
func (d FieldDef) DisplayLabel() string {
	if d.Label != "" {
		return d.Label
	}
	if d.Key == "" {
		return ""
	}
	return strings.ToUpper(d.Key[:1]) + d.Key[1:]
}

// ContentType is a registered kind of content (locked decision 2: seed types
// Article and Page are registrations, not structs). It is the WordPress
// "post type": a slug, display names, the custom FieldDefs beyond the frozen
// spine, the templates it may render with, and the spine capabilities it opts
// into (Hierarchical → parent_id/menu_order; Routable → a public route).
type ContentType struct {
	Slug         string     // "article","page","product"
	Singular     string     // "Article"
	Plural       string     // "Articles"
	Fields       []FieldDef // custom fields beyond the spine
	Templates    []string   // supported template names; [0] = default
	Hierarchical bool       // gates parent_id/menu_order (pages)
	Routable     bool       // gets a public route
	RoutePrefix  string     // public URL prefix; "" → derive from slug
}

// AdminBase returns the URL path segment for this type's admin routes, derived
// from the plural display name (e.g. "Articles" → "articles"). Admin lives at
// /{AdminBase}, /{AdminBase}/new, /{AdminBase}/{id}/edit, etc.
func (c ContentType) AdminBase() string {
	return slug.Make(c.Plural)
}

// PublicBase returns the URL prefix segment for this routable type's public
// pages. RoutePrefix overrides; otherwise hierarchical types are flat at the
// site root ("" → /{slug}) and the rest sit under their plural
// (/{plural}/{slug}). Decision (plan §"Open", resolved 2026-06-22).
func (c ContentType) PublicBase() string {
	if c.RoutePrefix != "" {
		return c.RoutePrefix
	}
	if c.Hierarchical {
		return ""
	}
	return slug.Make(c.Plural)
}

// DefaultTemplate returns the type's default template name (Templates[0]), or
// "default" when none are declared.
func (c ContentType) DefaultTemplate() string {
	if len(c.Templates) == 0 {
		return "default"
	}
	return c.Templates[0]
}

// SupportsTemplate reports whether template is one of the type's declared
// templates. A type with no declared templates accepts only "default".
func (c ContentType) SupportsTemplate(template string) bool {
	if template == "" {
		return true
	}
	if slices.Contains(c.Templates, template) {
		return true
	}
	return len(c.Templates) == 0 && template == "default"
}

// Field returns the FieldDef with the given key and whether it was found.
func (c ContentType) Field(key string) (FieldDef, bool) {
	for _, f := range c.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return FieldDef{}, false
}

// validateType enforces ContentType invariants at registration time.
func validateType(c ContentType) error {
	if strings.TrimSpace(c.Slug) == "" {
		return fmt.Errorf("content type slug is required: %w", errs.ErrInvalidInput)
	}
	if strings.TrimSpace(c.Singular) == "" || strings.TrimSpace(c.Plural) == "" {
		return fmt.Errorf("content type %q needs Singular and Plural names: %w", c.Slug, errs.ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(c.Fields))
	for _, f := range c.Fields {
		if strings.TrimSpace(f.Key) == "" {
			return fmt.Errorf("content type %q has a field with no Key: %w", c.Slug, errs.ErrInvalidInput)
		}
		if !f.Kind.Valid() {
			return fmt.Errorf("content type %q field %q has invalid kind %q: %w", c.Slug, f.Key, f.Kind, errs.ErrInvalidInput)
		}
		if f.Kind == KindRelation && strings.TrimSpace(f.RelTo) == "" {
			return fmt.Errorf("content type %q relation field %q needs RelTo: %w", c.Slug, f.Key, errs.ErrInvalidInput)
		}
		if _, dup := seen[f.Key]; dup {
			return fmt.Errorf("content type %q has duplicate field key %q: %w", c.Slug, f.Key, errs.ErrInvalidInput)
		}
		seen[f.Key] = struct{}{}
	}
	return nil
}
