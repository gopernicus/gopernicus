// Package oauthstate is the short-lived, single-use secret domain the OAuth flow
// hangs its server-side state off of: the PKCE verifier + OIDC nonce during an
// authorization round-trip, and the pending-link secret that gates the
// anti-takeover flow (design §3). It is deliberately its own table, NOT a widening
// of the v1 verification ports — the verification Code/Token ports have no
// single-use-with-payload contract, and the anti-takeover gate needs both.
package oauthstate

import (
	"time"

	"github.com/gopernicus/gopernicus/sdk/id"
)

// Purposes label a State so Consume's caller can reject a token minted for a
// different step of the flow.
const (
	// PurposeFlow tags an in-flight authorization round-trip: the payload
	// carries the PKCE verifier, the OIDC nonce, the post-login redirect target,
	// and (for a session-gated link) the linking user id.
	PurposeFlow = "flow"
	// PurposePendingLink tags the anti-takeover pending link: the payload carries
	// the OAuthAccount to create once the user proves control of the matching
	// email via the mailed secret.
	PurposePendingLink = "pending_link"
)

// State is a one-time, expiring flow secret. Token is the opaque lookup key
// (sdk/id); Payload is an opaque, purpose-specific blob (JSON, set by the
// service). A State is redeemed exactly once via StateRepository.Consume.
type State struct {
	Token     string
	Provider  string
	Purpose   string
	Payload   []byte
	ExpiresAt time.Time
}

// New mints a State for provider/purpose carrying payload, expiring ttl after
// now. The token is an opaque random value (sdk/id).
func New(provider, purpose string, payload []byte, ttl time.Duration, now time.Time) State {
	return State{
		Token:     id.New(),
		Provider:  provider,
		Purpose:   purpose,
		Payload:   payload,
		ExpiresAt: now.UTC().Add(ttl),
	}
}

// Expired reports whether the state is at or past its expiry at now.
func (s State) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}
