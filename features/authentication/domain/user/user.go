// Package user is the identity domain: the User aggregate plus the two
// credential-hygiene-separated repository ports. UserRepository owns profile
// reads/writes; PasswordRepository owns credential material (the password hash)
// so a store can guard the password table independently of the users table.
// Other domains reference a user by ID only.
package user

import (
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// User is the identity aggregate. Email is stored normalized (trimmed,
// lowercased); EmailVerified tracks whether the address has been confirmed via
// a verification code.
type User struct {
	ID            string
	Email         string // normalized: trimmed + lowercased
	DisplayName   string
	EmailVerified bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewUser validates and normalizes the email, trims the display name, mints an
// ID from ids (empty under cryptids.Database — the store then assigns the key),
// and returns an unverified user. Validation failures wrap sdk.ErrInvalidInput.
func NewUser(ids cryptids.IDGenerator, email, displayName string, now time.Time) (User, error) {
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	now = now.UTC()
	return User{
		ID:          ids.MustGenerate(),
		Email:       normalized,
		DisplayName: strings.TrimSpace(displayName),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// NormalizeEmail trims, validates (RFC 5322 addr-spec via net/mail), and
// lowercases an email address. Failures wrap sdk.ErrInvalidInput. Callers key
// uniqueness and lookups on the normalized form.
func NormalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", fmt.Errorf("email is required: %w", sdk.ErrInvalidInput)
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", fmt.Errorf("invalid email address: %w", sdk.ErrInvalidInput)
	}
	return strings.ToLower(addr.Address), nil
}

// MarkVerified flips EmailVerified and bumps UpdatedAt.
func (u *User) MarkVerified(now time.Time) {
	u.EmailVerified = true
	u.UpdatedAt = now.UTC()
}
