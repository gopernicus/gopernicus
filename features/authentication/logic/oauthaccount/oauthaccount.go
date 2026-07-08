// Package oauthaccount is the domain for a user's link to an external OAuth /
// OIDC identity: which provider account belongs to which local user, plus the
// provider tokens held for that link. It is a new table, distinct from the v1
// verification ports (design §3): OAuth state and pending links live in the
// sibling oauthstate domain, never in the verification-codes table.
package oauthaccount

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// OAuthAccount links a local user to one external provider identity. The token
// fields hold CIPHERTEXT when the auth feature is wired with a
// Config.TokenEncrypter, and are empty when it is not (design §3): a host
// without an encrypter can still log in and link, it just persists no provider
// tokens (no offline API access). Uniqueness is on (Provider, ProviderUserID) —
// one local user may claim a given provider identity.
type OAuthAccount struct {
	UserID                string
	Provider              string
	ProviderUserID        string
	ProviderEmail         string
	ProviderEmailVerified bool
	AccountVerified       bool
	LinkedAt              time.Time
	AccessToken           string // ciphertext when an encrypter is wired, else empty
	RefreshToken          string // ciphertext when an encrypter is wired, else empty
	TokenExpiresAt        time.Time
	TokenType             string
	Scope                 string
}

// New builds a verified OAuthAccount linking userID to (provider,
// providerUserID) as of now. Token and provider-email fields are set by the
// caller after construction (they depend on the exchanged token and whether an
// encrypter is wired). A blank userID, provider, or providerUserID wraps
// errs.ErrInvalidInput.
func New(userID, provider, providerUserID string, now time.Time) (OAuthAccount, error) {
	userID = strings.TrimSpace(userID)
	provider = strings.TrimSpace(provider)
	providerUserID = strings.TrimSpace(providerUserID)
	if userID == "" {
		return OAuthAccount{}, fmt.Errorf("user id is required: %w", errs.ErrInvalidInput)
	}
	if provider == "" {
		return OAuthAccount{}, fmt.Errorf("provider is required: %w", errs.ErrInvalidInput)
	}
	if providerUserID == "" {
		return OAuthAccount{}, fmt.Errorf("provider user id is required: %w", errs.ErrInvalidInput)
	}
	return OAuthAccount{
		UserID:          userID,
		Provider:        provider,
		ProviderUserID:  providerUserID,
		AccountVerified: true,
		LinkedAt:        now.UTC(),
	}, nil
}
