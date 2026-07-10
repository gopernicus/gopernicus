package email

import "context"

// LayoutType selects the HTML/text structure (header/footer) that wraps a
// rendered content template.
type LayoutType string

const (
	// LayoutTransactional is the default layout, for verification emails,
	// password resets, and account notifications.
	LayoutTransactional LayoutType = "transactional"

	// LayoutMarketing is for newsletters, announcements, and promotional
	// content.
	LayoutMarketing LayoutType = "marketing"

	// LayoutMinimal is a plain wrapper with minimal styling, for system
	// notifications and developer alerts.
	LayoutMinimal LayoutType = "minimal"
)

// Renderer renders templated emails and sends them via an underlying Sender.
type Renderer interface {
	// RenderAndSend renders the content template named by req.Template
	// ("namespace:name") wrapped in the configured layout and sends the result.
	// LayoutTransactional is used when no layout is specified.
	RenderAndSend(ctx context.Context, req SendRequest, opts ...RenderOption) error

	// Render renders a content template with the configured layout and returns
	// both the HTML and plain-text versions.
	Render(template string, data any, opts ...RenderOption) (html, text string, err error)
}

// SendRequest carries everything needed to render and send a templated email.
type SendRequest struct {
	// To is the recipient email address (required).
	To string

	// Subject is the email subject line (required).
	Subject string

	// Template is the content template name in "namespace:name" format.
	Template string

	// Data is passed to both the content and layout templates.
	Data any
}

// RenderConfig holds configuration for a single render/send call.
type RenderConfig struct {
	Layout LayoutType
}

// DefaultRenderConfig returns the default render configuration.
func DefaultRenderConfig() RenderConfig {
	return RenderConfig{Layout: LayoutTransactional}
}

// RenderOption configures a render/send call.
type RenderOption func(*RenderConfig)

// WithLayout selects the layout used for rendering.
func WithLayout(layout LayoutType) RenderOption {
	return func(c *RenderConfig) {
		c.Layout = layout
	}
}

// ApplyOptions applies opts to a default config and returns it.
func ApplyOptions(opts ...RenderOption) RenderConfig {
	cfg := DefaultRenderConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}
