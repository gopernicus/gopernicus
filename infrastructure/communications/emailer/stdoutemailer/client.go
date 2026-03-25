// Package stdoutemailer provides a stdout-based email client for development.
// Emails are logged to stdout instead of being sent.
package stdoutemailer

import (
	"context"
	"log/slog"
	"strings"

	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
)

var _ emailer.Client = (*Client)(nil)

// Client is a console-based email client that logs emails instead of sending them.
// Useful for development and testing environments.
type Client struct {
	log *slog.Logger
}

// New creates a new console email client.
func New(log *slog.Logger) *Client {
	return &Client{log: log}
}

// Send logs the email to the console instead of sending it.
func (c *Client) Send(ctx context.Context, email emailer.Email) error {
	bodyPreview := email.Text
	if bodyPreview == "" {
		bodyPreview = stripHTMLTags(email.HTML)
	}
	if len(bodyPreview) > 200 {
		bodyPreview = bodyPreview[:200] + "..."
	}

	c.log.InfoContext(ctx, "EMAIL (console mode)",
		slog.String("to", email.To),
		slog.String("from", email.From),
		slog.String("subject", email.Subject),
		slog.String("body_preview", bodyPreview),
		slog.Bool("has_html", email.HTML != ""),
		slog.Bool("has_text", email.Text != ""),
	)

	return nil
}

// stripHTMLTags removes HTML tags from a string for preview purposes.
func stripHTMLTags(html string) string {
	var result strings.Builder
	inTag := false
	for _, char := range html {
		if char == '<' {
			inTag = true
			continue
		}
		if char == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(char)
		}
	}
	return strings.Join(strings.Fields(result.String()), " ")
}
