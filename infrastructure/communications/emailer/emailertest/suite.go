// Package emailertest provides compliance tests for emailer.Client implementations.
//
// Example:
//
//	func TestCompliance(t *testing.T) {
//	    client := console.New(slog.Default())
//	    emailertest.RunSuite(t, client)
//	}
package emailertest

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
)

// RunSuite runs the standard compliance tests against any emailer.Client implementation.
func RunSuite(t *testing.T, c emailer.Client) {
	t.Helper()

	t.Run("SendBasicEmail", func(t *testing.T) {
		ctx := context.Background()
		email := emailer.Email{
			To:      "test@example.com",
			From:    "noreply@example.com",
			Subject: "Test Email",
			Text:    "This is a test email.",
		}
		if err := c.Send(ctx, email); err != nil {
			t.Fatalf("Send: %v", err)
		}
	})

	t.Run("SendHTMLEmail", func(t *testing.T) {
		ctx := context.Background()
		email := emailer.Email{
			To:      "test@example.com",
			From:    "noreply@example.com",
			Subject: "HTML Test",
			HTML:    "<h1>Hello</h1>",
			Text:    "Hello",
		}
		if err := c.Send(ctx, email); err != nil {
			t.Fatalf("Send HTML: %v", err)
		}
	})
}
