// Package authgrant is the recent-authentication / step-up grant domain (design
// §5.0). A live session proves revocation state; it does not prove the human
// recently presented an existing authenticator. Every sensitive credential or
// identifier mutation additionally requires a Grant: a short-lived, single-use,
// server-side token bound to the live session, user, intended operation, and the
// operation's context (a provider name or identifier-change ID). The user earns a
// grant by reauthenticating with an EXISTING enrolled method; proving a proposed
// new email/phone can never satisfy it.
//
// The abstraction is intentionally broader than v3 needs: auth-v4 MFA adds
// passkey/TOTP/recovery-code grant producers without changing this contract
// (design §12.1). The grant records the methods and assurance it was earned under
// (session.AuthenticationMethod / session.AssuranceLevel) so a sufficiently
// recent primary login can satisfy it without an extra prompt.
package authgrant

import (
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
)

// Grant purposes are the closed set of sensitive operations a recent-authentication
// grant can authorize (design §5.2–5.5). A grant is single-purpose: one earned for
// PurposeSetPassword can never authorize a PurposeUnlinkOAuth mutation. The
// operation's own context (a provider name or an identifier id) rides ContextDigest,
// so purpose plus context together pin a grant to exactly one mutation.
const (
	// PurposeSetPassword gates setting an initial password on an account that has
	// none (design §5.2).
	PurposeSetPassword = "set_password"
	// PurposeRemovePassword gates deleting the account password (design §5.3).
	PurposeRemovePassword = "remove_password"
	// PurposeUnlinkOAuth gates unlinking an OAuth provider; the provider name is the
	// grant context (design §5.4).
	PurposeUnlinkOAuth = "unlink_oauth"
	// PurposeChangeEmail gates adding or changing an email identifier (design §5.5).
	PurposeChangeEmail = "change_email"
	// PurposeChangePhone gates adding or changing a phone identifier (design §5.5).
	PurposeChangePhone = "change_phone"
	// PurposeRemoveIdentifier gates retiring an identifier; the identifier id is the
	// grant context (design §5.5).
	PurposeRemoveIdentifier = "remove_identifier"
	// PurposeChangeIdentifierUses gates changing an identifier's use flags; the
	// identifier id is the grant context (design §5.5).
	PurposeChangeIdentifierUses = "change_identifier_uses"
)

// Grant is a consumed-once recent-authentication token. ContextDigest binds the
// operation's context (e.g. a provider name or an identifier-change ID) so a
// grant earned for one operation cannot authorize another. Methods and Assurance
// record how the grant was earned. AuthenticatedAt is when the proving
// authentication happened; ExpiresAt bounds the grant's maximum age (default 5
// minutes, design §5.0); ConsumedAt is set once the mutation consumes it.
type Grant struct {
	ID              string
	SessionID       string
	UserID          string
	Purpose         string
	ContextDigest   string
	Methods         []session.AuthenticationMethod
	Assurance       session.AssuranceLevel
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
	CreatedAt       time.Time
	ConsumedAt      time.Time
}

// Expired reports whether the grant is at or past its expiry at now.
func (g Grant) Expired(now time.Time) bool {
	return !now.Before(g.ExpiresAt)
}

// Consumed reports whether the grant has already been spent.
func (g Grant) Consumed() bool {
	return !g.ConsumedAt.IsZero()
}
