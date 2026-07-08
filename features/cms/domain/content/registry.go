package content

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// templateSep separates type slug from template name in the render map. The NUL
// byte cannot appear in a slug or template name, so it is a collision-free join.
const templateSep = "\x00"

// TemplateFunc renders one entry of a given (type, template) to a web.Renderer.
// It is dev-authored templ (locked decision 3): presentation stays typed even
// though the content core is dynamic. The host binds these via RegisterTemplate.
type TemplateFunc func(e Entry) web.Renderer

// TemplateBinding binds a render func to a registered type's template. It is the
// per-entry render seam as data: a Views value carries its seed bindings (see
// cms.Register), and a host supplies custom ones via cms.Config.Templates.
type TemplateBinding struct {
	Type     string
	Template string
	Fn       TemplateFunc
}

// Registry holds the registered content types and their per-(type, template)
// render funcs (plan §3). It is the WordPress "post type" table built as code,
// not data: adding a type is a registration, never a migration. Construct one
// with NewRegistry, populate it during feature/host registration, then treat it
// as read-only while serving.
type Registry struct {
	types     map[string]ContentType
	order     []string // registration order, for stable Types()
	templates map[string]TemplateFunc
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		types:     make(map[string]ContentType),
		templates: make(map[string]TemplateFunc),
	}
}

// Register adds a content type. It validates the type and rejects a duplicate
// slug. Registration order is preserved for Types().
func (r *Registry) Register(ct ContentType) error {
	if err := validateType(ct); err != nil {
		return err
	}
	if _, dup := r.types[ct.Slug]; dup {
		return fmt.Errorf("content type %q already registered: %w", ct.Slug, errs.ErrAlreadyExists)
	}
	r.types[ct.Slug] = ct
	r.order = append(r.order, ct.Slug)
	return nil
}

// RegisterTemplate binds a render func to a (type, template) pair. The type must
// be registered and must declare the template (per ContentType.SupportsTemplate);
// fn must be non-nil. An empty template binds the type's default.
func (r *Registry) RegisterTemplate(typeSlug, template string, fn TemplateFunc) error {
	ct, ok := r.types[typeSlug]
	if !ok {
		return fmt.Errorf("content type %q not registered: %w", typeSlug, errs.ErrNotFound)
	}
	if fn == nil {
		return fmt.Errorf("template func for %q/%q is nil: %w", typeSlug, template, errs.ErrInvalidInput)
	}
	if template == "" {
		template = ct.DefaultTemplate()
	}
	if !ct.SupportsTemplate(template) {
		return fmt.Errorf("content type %q does not declare template %q: %w", typeSlug, template, errs.ErrInvalidInput)
	}
	r.templates[typeSlug+templateSep+template] = fn
	return nil
}

// Type returns the registered content type for slug and whether it was found.
func (r *Registry) Type(slug string) (ContentType, bool) {
	ct, ok := r.types[slug]
	return ct, ok
}

// Types returns the registered content types in registration order.
func (r *Registry) Types() []ContentType {
	out := make([]ContentType, 0, len(r.order))
	for _, slug := range r.order {
		out = append(out, r.types[slug])
	}
	return out
}

// Render resolves the render func for e's (Type, Template) and applies it. When
// the exact template is unbound it falls back to the type's default template.
// Returns false when no func resolves (caller renders a not-found / error page).
func (r *Registry) Render(e Entry) (web.Renderer, bool) {
	if fn, ok := r.templates[e.Type+templateSep+e.Template]; ok {
		return fn(e), true
	}
	if ct, ok := r.types[e.Type]; ok {
		if fn, ok := r.templates[e.Type+templateSep+ct.DefaultTemplate()]; ok {
			return fn(e), true
		}
	}
	return nil, false
}

// ValidateFields checks a custom-field bag against the type's FieldDefs and
// returns a normalized copy: every value tagged with its declared kind and
// coerced/trimmed to canonical form. It enforces required fields and per-kind
// parseability (number, bool, date). Relation/image existence is NOT checked
// here — that needs the store and is verified by the service. Unknown keys and
// invalid values wrap errs.ErrInvalidInput.
func (r *Registry) ValidateFields(typeSlug string, in Fields) (Fields, error) {
	ct, ok := r.types[typeSlug]
	if !ok {
		return nil, fmt.Errorf("content type %q not registered: %w", typeSlug, errs.ErrNotFound)
	}

	for key := range in {
		if _, declared := ct.Field(key); !declared {
			return nil, fmt.Errorf("content type %q has no field %q: %w", typeSlug, key, errs.ErrInvalidInput)
		}
	}

	out := make(Fields, len(ct.Fields))
	for _, def := range ct.Fields {
		raw := strings.TrimSpace(in[def.Key].Raw)
		if raw == "" {
			if def.Required {
				return nil, fmt.Errorf("field %q is required: %w", def.Key, errs.ErrInvalidInput)
			}
			continue
		}
		norm, err := coerce(def, raw)
		if err != nil {
			return nil, err
		}
		out[def.Key] = Value{Kind: def.Kind, Raw: norm}
	}
	return out, nil
}

// coerce validates raw against def.Kind and returns its canonical stored form.
func coerce(def FieldDef, raw string) (string, error) {
	switch def.Kind {
	case KindNumber:
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return "", fmt.Errorf("field %q must be a number: %w", def.Key, errs.ErrInvalidInput)
		}
	case KindBool:
		switch raw {
		case "true", "false":
		default:
			return "", fmt.Errorf("field %q must be true or false: %w", def.Key, errs.ErrInvalidInput)
		}
	case KindDate:
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return "", fmt.Errorf("field %q must be an RFC3339 date: %w", def.Key, errs.ErrInvalidInput)
		}
		return t.UTC().Format(time.RFC3339), nil
	case KindText, KindRichText, KindImage, KindRelation:
		// stored as-is (asset ID / entry ID / text)
	default:
		return "", fmt.Errorf("field %q has invalid kind %q: %w", def.Key, def.Kind, errs.ErrInvalidInput)
	}
	return raw, nil
}
