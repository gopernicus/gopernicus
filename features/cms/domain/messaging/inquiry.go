// Package messaging is the bounded context for inbound contact inquiries: it
// persists each submission and notifies the operator via the email facility.
package messaging

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/id"
)

// Inquiry is a submitted contact-form message.
type Inquiry struct {
	ID        string
	Name      string
	Email     string
	Message   string
	CreatedAt time.Time
}

// NewInquiry validates the inputs and returns a new Inquiry. Validation
// failures wrap errs.ErrInvalidInput.
func NewInquiry(name, email, message string, now time.Time) (Inquiry, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	message = strings.TrimSpace(message)

	if name == "" {
		return Inquiry{}, fmt.Errorf("name is required: %w", errs.ErrInvalidInput)
	}
	if !looksLikeEmail(email) {
		return Inquiry{}, fmt.Errorf("a valid email is required: %w", errs.ErrInvalidInput)
	}
	if message == "" {
		return Inquiry{}, fmt.Errorf("message is required: %w", errs.ErrInvalidInput)
	}

	return Inquiry{
		ID:        id.New(),
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
