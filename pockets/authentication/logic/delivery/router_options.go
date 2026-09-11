package delivery

import (
	"log/slog"
	"maps"
	"slices"

	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

// RouterOption configures NewRouter before construction. Nil options are invalid.
type RouterOption func(*routerConfig)

// TemplatesConfig customizes message templates, layouts, and branding.
type TemplatesConfig struct {
	AppTemplates []TemplateOverride
	// AppLayouts registers host email layouts at email.LayerApp — the highest
	// layer, and the higher layer wins — so a host layout named for a layout type
	// resolves ahead of the sdk's bundled default. Empty (the zero value) → the
	// sdk layouts render exactly as before.
	AppLayouts []LayoutOverride
	// Branding fills the shared email layouts' {{.Brand.*}} values; nil keeps
	// the layouts' own fallback ("Your Company").
	Branding *email.Branding
	// Subjects overrides the in-core email subject template per purpose (key =
	// Purpose*, value = text/template source). Parsed at construction with
	// missing-key errors enabled, so a data-contract mistake fails the render
	// rather than shipping "<no value>". Unknown purpose keys and empty sources
	// are ErrOverrideInvalid.
	Subjects map[string]string
	// SMSBodies overrides the in-core body-only SMS template per purpose, with the
	// same parsing rules as Subjects. A purpose whose core spec has no SMS rail
	// cannot be given one here (ErrOverrideInvalid): an override customizes an
	// existing rail, it never enables a new kind.
	SMSBodies map[string]string
}

// WithTemplates replaces the complete TemplatesConfig group, including zero values.
func WithTemplates(value TemplatesConfig) RouterOption {
	value = cloneTemplatesConfig(value)
	return func(c *routerConfig) {
		value := cloneTemplatesConfig(value)
		c.AppTemplates = value.AppTemplates
		c.AppLayouts = value.AppLayouts
		c.Branding = value.Branding
		c.Subjects = value.Subjects
		c.SMSBodies = value.SMSBodies
	}
}

func cloneTemplatesConfig(value TemplatesConfig) TemplatesConfig {
	value.AppTemplates = slices.Clone(value.AppTemplates)
	value.AppLayouts = slices.Clone(value.AppLayouts)
	if value.Branding != nil {
		copy := *value.Branding
		copy.SocialLinks = slices.Clone(copy.SocialLinks)
		value.Branding = &copy
	}
	value.Subjects = maps.Clone(value.Subjects)
	value.SMSBodies = maps.Clone(value.SMSBodies)
	return value
}

// WithMailFrom replaces MailFrom for this constructor.
func WithMailFrom(value string) RouterOption {
	return func(c *routerConfig) {
		c.MailFrom = value
	}
}

// WithBodySenders replaces BodySenders for this constructor.
func WithBodySenders(value map[string]BodySender) RouterOption {
	value = maps.Clone(value)
	return func(c *routerConfig) {
		value := maps.Clone(value)
		c.BodySenders = value
	}
}

// WithDataHook replaces DataHook for this constructor.
func WithDataHook(value DataHook) RouterOption {
	return func(c *routerConfig) {
		c.DataHook = value
	}
}

// WithRouterLogger supplies the rendering logger; nil selects slog.Default().
func WithRouterLogger(value *slog.Logger) RouterOption {
	return func(c *routerConfig) {
		c.Logger = value
	}
}
