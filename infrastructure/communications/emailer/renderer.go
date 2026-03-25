package emailer

import "context"

// =============================================================================
// Layout Types
// =============================================================================

// LayoutType defines available email layout types.
// Layouts provide the HTML structure (header/footer) that wraps content templates.
type LayoutType string

const (
	// LayoutTransactional is the default layout for transactional emails.
	// Used for: verification emails, password resets, account notifications.
	LayoutTransactional LayoutType = "transactional"

	// LayoutMarketing is for promotional and marketing emails.
	// Used for: newsletters, announcements, promotional content.
	LayoutMarketing LayoutType = "marketing"

	// LayoutMinimal is a plain wrapper with minimal styling.
	// Used for: system notifications, developer alerts, plain text preference.
	LayoutMinimal LayoutType = "minimal"
)

// =============================================================================
// Renderer Interface
// =============================================================================

// Renderer defines the interface for rendering and sending templated emails.
//
// This interface abstracts:
//   - Template rendering (content + layout composition)
//   - Email sending (SMTP, SendGrid, etc.)
//   - Branding (app-specific headers, footers, styling)
type Renderer interface {
	// RenderAndSend renders a content template with the specified layout
	// and sends it to the recipient.
	//
	// The template parameter should be in the format "namespace:templatename"
	// (e.g., "authentication:verification").
	//
	// If no layout is specified, LayoutTransactional is used by default.
	RenderAndSend(ctx context.Context, req SendRequest, opts ...RenderOption) error

	// Render renders a content template with the specified layout.
	// Returns both HTML and plain text versions.
	Render(template string, data any, opts ...RenderOption) (html, text string, err error)
}

// =============================================================================
// Send Request
// =============================================================================

// SendRequest contains all information needed to send a templated email.
type SendRequest struct {
	// To is the recipient email address (required).
	To string

	// Subject is the email subject line (required).
	Subject string

	// Template is the content template name in "namespace:name" format
	// (e.g., "authentication:verification").
	Template string

	// Data is the template data passed to both content and layout templates.
	Data any
}

// =============================================================================
// Render Options
// =============================================================================

// RenderConfig holds configuration for a single render/send call.
type RenderConfig struct {
	Layout LayoutType
}

// DefaultRenderConfig returns the default render configuration.
func DefaultRenderConfig() RenderConfig {
	return RenderConfig{
		Layout: LayoutTransactional,
	}
}

// RenderOption configures a render/send call.
type RenderOption func(*RenderConfig)

// WithLayout specifies which layout to use for rendering.
func WithLayout(layout LayoutType) RenderOption {
	return func(c *RenderConfig) {
		c.Layout = layout
	}
}

// ApplyOptions applies all options to a config and returns it.
func ApplyOptions(opts ...RenderOption) RenderConfig {
	cfg := DefaultRenderConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}
