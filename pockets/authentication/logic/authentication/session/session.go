// Package session is the server-side session domain: the revocable anchor for an
// authenticated user. The session row carries no access credential — the access
// token is a self-validating JWT that is never persisted — and holds the refresh
// rotation state: the current refresh-token hash, a single previous (grace) slot
// with a consumed flag, and a rotation counter.
package session

import (
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// secrets mints session IDs and refresh-token material with the default nanoid
// shape. Deliberately NOT the app's entity-ID strategy (Config.IDs / the
// migrations' DB-side id defaults): a session ID and a refresh token are credentials, and
// their entropy must never follow a wiring choice like sdk.DatabaseID.
var secrets = sdk.IDGenerator{}

// Profile separates a first-party login from an independently revocable OAuth
// connection. A login method (including Google) does not determine its profile.
type Profile string

const (
	ProfileFirstParty Profile = "first_party"
	ProfileDelegated  Profile = "delegated"

	// The dot cannot occur in the first-party refresh generator's alphabet.
	delegatedRefreshTokenPrefix = "oauth2_rt."
)

// Delegation is the immutable authorization binding of a delegated session.
// ClientID is the consenting public client. ExchangeClientID may obtain tokens
// for ExchangeResource only; those tokens retain this session's revocation anchor.
// First-party sessions carry the zero value. Stores must preserve every field.
type Delegation struct {
	AuthRevision     int64
	Issuer           string
	ClientID         string
	Resource         string
	ExchangeResource string
	ExchangeClientID string
}

// RefreshMatch reports which slot a presented refresh-token hash matched in
// GetByRefreshHash — the current live token or the single previous (grace) slot.
type RefreshMatch int

const (
	// RefreshMatchCurrent means the hash matched refresh_token_hash: the live
	// token, eligible for rotation.
	RefreshMatchCurrent RefreshMatch = iota
	// RefreshMatchPrevious means the hash matched previous_refresh_token_hash:
	// the rotated-away token, eligible for a single-use grace refresh.
	RefreshMatchPrevious
)

// Session is the revocable server-side anchor for an authenticated user. It
// stores no access token (the access credential is a self-validating JWT, never
// persisted) and instead anchors refresh rotation:
//
//   - RefreshTokenHash is the SHA-256 hash of the live refresh token.
//   - PreviousRefreshTokenHash is the hash of the single rotated-away token
//     (empty for a fresh, never-rotated session). It backs the one-generation
//     grace window (D5).
//   - PreviousUsed marks that the previous slot's grace refresh was already
//     consumed; a second arrival on a consumed slot is token reuse.
//   - RotationCount counts completed rotations, for audit.
type Session struct {
	// ID is the app-minted primary key. It is minted unconditionally by
	// NewSession (the secrets generator), NOT governed by Config.IDs / the
	// migrations' DB-side id defaults: the access JWT is signed with session_id BEFORE the row
	// is inserted, so a DB-generated key (RETURNING) cannot work.
	ID                       string
	UserID                   string
	Profile                  Profile
	Delegation               Delegation
	RefreshTokenHash         string
	PreviousRefreshTokenHash string
	PreviousUsed             bool
	RotationCount            int
	CreatedAt                time.Time
	ExpiresAt                time.Time
	// Authentication records how and when the holder last performed a primary
	// authentication, backing the recent-primary-login shortcut for step-up
	// grants (design §5.0). Its zero value means none recorded; the persisted
	// columns land with the phase-2 schema.
	Authentication AuthenticationMetadata
}

// NewSession mints a fresh session for userID expiring ttl after now (the fixed
// refresh horizon, D2 — rotation never extends it). It app-mints the session ID
// (see Session.ID) and a high-entropy opaque refresh token, returning the
// session with its RefreshTokenHash still empty (the service stores the token's
// SHA-256 hash, never the raw value) alongside the raw refresh token to deliver
// to the client. The caller sets RefreshTokenHash to the token's hash.
func NewSession(userID string, ttl time.Duration, now time.Time) (Session, string) {
	now = now.UTC()
	return Session{
		ID:        secrets.MustGenerate(),
		UserID:    userID,
		Profile:   ProfileFirstParty,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}, NewRefreshToken()
}

// FirstParty accepts the empty profile only for legacy first-party rows. Any
// delegated binding on such a row is inconsistent and fails closed.
func (s Session) FirstParty() bool {
	return (s.Profile == "" || s.Profile == ProfileFirstParty) && s.Delegation == (Delegation{})
}

// NewRefreshToken mints a fresh high-entropy opaque refresh token — the value
// rotation issues on each successful refresh. It is never persisted raw (the
// service stores its SHA-256 hash).
func NewRefreshToken() string {
	return secrets.MustGenerate()
}

// NewDelegatedRefreshToken mints refresh material for an OAuth connection. Every
// delegated issuance and rotation must use this namespace so first-party
// lifecycle endpoints can refuse it even after its session/history is removed.
// Hash and persist the entire value, including its prefix; never persist it raw.
func NewDelegatedRefreshToken() string {
	return delegatedRefreshTokenPrefix + secrets.MustGenerate()
}

// IsDelegatedRefreshToken recognizes the reserved namespace for denial only.
// It proves neither token validity nor ownership; malformed values with the
// prefix are also refused by first-party lifecycle endpoints.
func IsDelegatedRefreshToken(raw string) bool {
	return strings.HasPrefix(raw, delegatedRefreshTokenPrefix)
}

// Expired reports whether the session is at or past its expiry at now.
func (s Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}
