// Package email is the facility port for sending mail. Stdlib-only senders
// (SMTP via net/smtp, Console for dev) ship right here as defaults and
// implement Sender; a third-party SaaS sender would live in its own
// integrations/email/<tech> module. sdk/capabilities/notify/email is stdlib-only and knows
// nothing about any backend beyond the Sender interface.
//
// # Branding and bundled layouts
//
// The optional template layer (Renderer + Emailer) wraps a rendered
// content template in a layout. Three layouts ship at LayerInfra, and they do
// not all render every Branding field:
//
//	layout                 logo  name  tagline  address  social  unsubscribe
//	LayoutTransactional    yes   yes   yes      yes      yes     no
//	LayoutMarketing        yes   yes   no       yes      yes     yes
//	LayoutMinimal          no    no    no       no       no      no
//
// LayoutTransactional is the normal application and authentication layout;
// LayoutMarketing is for campaign mail; LayoutMinimal is deliberately unbranded
// and exists as a content-only fallback. Turning one of those "no" cells into a
// "yes" changes rendered output for every adopter on a bundled layout, so it is
// a design decision rather than a template tweak. TestBundledLayoutBrandingMatrix
// pins the table.
//
// Branding is a data override, not a structural one. A host that needs a
// different shell registers its own layout at LayerApp with WithLayouts,
// which wins over the bundled template entirely — including the bundled logo
// block, so an overriding host must render Brand.LogoURL itself if it wants one.
//
// # Logo rendering
//
// Branding.LogoURL should be an absolute, publicly fetchable HTTPS image URL.
// The renderer never fetches, resolves, validates, or inlines it: the URL is
// interpolated straight into the layout's src attribute through html/template,
// which is the injection safety boundary. A hostile URL is escaped (a
// javascript: scheme becomes the html/template #ZgotmplZ marker) rather than
// sanitized by this package. Never wrap a logo URL in template.HTML.
//
// An empty LogoURL emits no <img> element at all. Because most mail clients
// block external images by default, the bundled layouts keep the brand name and
// tagline as visible text next to the image, and the alt text is the brand name
// (falling back to "Your Company" when Branding.Name is empty, matching the
// visible header fallback). The plain-text alternatives are image-free and never
// carry the logo URL.
//
// A minimal branded send:
//
//	em, err := email.New(sender, "no-reply@example.com",
//		email.WithContentTemplates("acme", acmeTemplates, email.LayerApp),
//		email.WithBranding(&email.Branding{
//			Name:    "Acme",
//			Tagline: "We ship things",
//			LogoURL: "https://cdn.acme.example/logo.png",
//		}),
//	)
//	err = em.RenderAndSend(ctx, email.SendRequest{
//		To:       "user@example.com",
//		Subject:  "Welcome",
//		Template: "acme:welcome",
//		Data:     map[string]any{"Name": "Ada"},
//		Layout: email.LayoutTransactional,
//	})
package email

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// Message is an outbound email. Text is required; HTML is optional. From and
// each To value must be a bare mailbox of at most 254 bytes. Every To recipient
// is visible to the other recipients; send separate messages for private fan-out.
type Message struct {
	From    string
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Validate checks required fields, bare mailboxes, header safety and UTF-8. Failures wrap
// sdk.ErrInvalidInput.
func (m Message) Validate() error {
	if !utf8.ValidString(m.Subject) || !utf8.ValidString(m.Text) || !utf8.ValidString(m.HTML) {
		return fmt.Errorf("email: subject and bodies must be valid UTF-8: %w", sdk.ErrInvalidInput)
	}
	if err := validateMailbox(m.From); err != nil {
		return fmt.Errorf("email: invalid From mailbox: %w", sdk.ErrInvalidInput)
	}
	for i, to := range m.To {
		if err := validateMailbox(to); err != nil {
			return fmt.Errorf("email: invalid To mailbox at index %d: %w", i, sdk.ErrInvalidInput)
		}
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("email: subject must not contain line breaks: %w", sdk.ErrInvalidInput)
	}
	for _, r := range m.Subject {
		if (r < 32 && r != '\t') || r == 127 {
			return fmt.Errorf("email: subject contains a control character: %w", sdk.ErrInvalidInput)
		}
	}
	if len(m.To) == 0 {
		return fmt.Errorf("at least one recipient is required: %w", sdk.ErrInvalidInput)
	}
	if strings.TrimSpace(m.Subject) == "" {
		return fmt.Errorf("subject is required: %w", sdk.ErrInvalidInput)
	}
	if strings.TrimSpace(m.Text) == "" {
		return fmt.Errorf("body is required: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// The port accepts one bare mailbox per value, without display names, address
// lists or surrounding whitespace. Do not include invalid input in diagnostics.
func validateMailbox(value string) error {
	if len(value) > 254 || strings.ContainsAny(value, "\r\n") || value != strings.TrimSpace(value) {
		return sdk.ErrInvalidInput
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Name != "" || address.Address != value {
		return sdk.ErrInvalidInput
	}
	return nil
}

// Sender delivers a Message. Implemented by bundled or integration senders.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}
