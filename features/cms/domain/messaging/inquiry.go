// Package messaging is the bounded context for inbound contact inquiries: it
// persists each submission and notifies the operator via the email facility.
package messaging

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Inquiry is a submitted contact-form message.
type Inquiry struct {
	ID        string
	Name      string
	Email     string
	Message   string
	CreatedAt time.Time
}

// NewInquiry validates the inputs and returns a new Inquiry, minting its ID from
// ids (empty under cryptids.Database — the store then assigns the key).
// Validation failures wrap sdk.ErrInvalidInput.
func NewInquiry(ids cryptids.IDGenerator, name, email, message string, now time.Time) (Inquiry, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	message = strings.TrimSpace(message)

	if name == "" {
		return Inquiry{}, fmt.Errorf("name is required: %w", sdk.ErrInvalidInput)
	}
	if !looksLikeEmail(email) {
		return Inquiry{}, fmt.Errorf("a valid email is required: %w", sdk.ErrInvalidInput)
	}
	if message == "" {
		return Inquiry{}, fmt.Errorf("message is required: %w", sdk.ErrInvalidInput)
	}

	return Inquiry{
		ID:        ids.MustGenerate(),
		Name:      name,
		Email:     email,
		Message:   message,
		CreatedAt: now.UTC(),
	}, nil
}

// looksLikeEmail is a deliberately loose check (one @, a dot in the domain).
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}
