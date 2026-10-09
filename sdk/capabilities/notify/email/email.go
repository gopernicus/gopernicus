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
// # Unsubscribe headers
//
// Message.Unsubscribe (and SendRequest.Unsubscribe) asks the sender to add
// RFC 2369 List-Unsubscribe and, with OneClick, RFC 8058
// List-Unsubscribe-Post headers, which mail clients use for their native
// Unsubscribe button. This is per-send data, unlike Branding.UnsubscribeURL,
// which is a static link rendered in the LayoutMarketing body; a host may use
// both. A one-click URL is per recipient, so OneClick is refused on a message
// with more than one recipient. The URL's endpoint must accept a POST with the
// body List-Unsubscribe=One-Click, without cookies or CSRF tokens. RFC 8058 also
// requires the DKIM signature to cover both headers: the SMTP sender does not
// sign, so its relay must.
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
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// Message is an outbound email. Text is required; HTML is optional. From and
// each To value must be a bare mailbox of at most 254 bytes. Every To recipient
// is visible to the other recipients; send separate messages for private fan-out.
// A per-recipient Unsubscribe URL likewise needs one message per recipient.
type Message struct {
	From        string
	To          []string
	Subject     string
	Text        string
	HTML        string
	Unsubscribe Unsubscribe
}

// Unsubscribe requests RFC 2369 List-Unsubscribe and, with OneClick, RFC 8058
// List-Unsubscribe-Post headers. The zero value sends neither. It is separate
// from Branding.UnsubscribeURL, which is a static link in the marketing layout
// body. URL is an absolute ASCII https URL of at most 900 bytes; Mailto is a
// bare ASCII mailbox. OneClick requires URL and exactly one recipient, since a
// shared one-click link would let any visible recipient unsubscribe another.
type Unsubscribe struct {
	URL      string
	Mailto   string
	OneClick bool
}

// IsZero reports whether no unsubscribe headers are requested.
func (u Unsubscribe) IsZero() bool { return u == Unsubscribe{} }

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
	return m.validateUnsubscribe()
}

func (m Message) validateUnsubscribe() error {
	u := m.Unsubscribe
	if u.IsZero() {
		return nil
	}
	if u.URL == "" && u.Mailto == "" {
		return fmt.Errorf("email: unsubscribe requires a URL or mailbox: %w", sdk.ErrInvalidInput)
	}
	if u.OneClick && u.URL == "" {
		return fmt.Errorf("email: one-click unsubscribe requires a URL: %w", sdk.ErrInvalidInput)
	}
	if u.OneClick && len(m.To) != 1 {
		return fmt.Errorf("email: one-click unsubscribe requires exactly one recipient: %w", sdk.ErrInvalidInput)
	}
	if u.URL != "" && !validUnsubscribeURL(u.URL) {
		return fmt.Errorf("email: invalid unsubscribe URL: %w", sdk.ErrInvalidInput)
	}
	if u.Mailto != "" && (validateMailbox(u.Mailto) != nil || !plainHeaderValue(u.Mailto) || strings.ContainsAny(u.Mailto, `"?`)) {
		return fmt.Errorf("email: invalid unsubscribe mailbox: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// Each List-Unsubscribe entry is written on its own folded header line, so
// the 900-byte cap keeps it under the RFC 5322 998-byte line limit.
func validUnsubscribeURL(value string) bool {
	if len(value) > 900 || !plainHeaderValue(value) {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == "" && !strings.Contains(value, "#")
}

// A plain header value is printable ASCII without spaces or angle brackets,
// so it can be written verbatim inside List-Unsubscribe's <...> entries.
func plainHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c <= ' ' || c >= 127 || c == '<' || c == '>' {
			return false
		}
	}
	return true
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
// A Sender that cannot deliver a non-zero Message.Unsubscribe must return an
// error wrapping sdk.ErrInvalidInput rather than send without the headers.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}
