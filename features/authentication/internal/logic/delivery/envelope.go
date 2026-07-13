// Package delivery holds the auth feature's shared outbound-delivery logic
// (design §6.1). Phase 0 lands only the piece the durable outbox contract needs:
// the encrypted payload envelope. The kind-aware router/renderer and the
// at-least-once worker are phase-4 orchestration and are added here later; this
// package imports sdk ports only (never inbound/outbound/integrations) and is
// constructor-injected, never a registry.
//
// The Envelope is the plaintext a delivery job must carry but must never persist
// in the clear: the destination address, the rendered secret/message, and the
// account-resolution input the worker uses to resolve the account off the request
// path (design §6.1.1). Seal encrypts the whole envelope through the required
// DeliveryEncrypter into the opaque deliveryjob.Job.Payload; Open reverses it in
// the worker. Because none of these fields is a job column, a leaked outbox row
// exposes ciphertext only — the enumeration-resistance and secret-at-rest
// invariants are structural, not disciplinary.
package delivery

import (
	"encoding/json"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Envelope is the plaintext delivery instruction sealed into a job's Payload. It
// is never stored or logged in the clear. Destination is the resolved address the
// worker sends to; ResolutionInput is the normalized identifier the worker uses to
// resolve the account (present because unauthenticated starts do NOT resolve on
// the request path); Subject/Body are the rendered message (Subject is empty for
// SMS). HTML is the rendered email body when present (the email rail persists both
// HTML and text so a retry resends the identical message; SMS leaves it empty).
// Secret is the rendered OTP/token, kept separate so it can be scrubbed from
// diagnostics.
type Envelope struct {
	Destination     string `json:"destination"`
	ResolutionInput string `json:"resolution_input"`
	Subject         string `json:"subject,omitempty"`
	Body            string `json:"body"`
	HTML            string `json:"html,omitempty"`
	Secret          string `json:"secret,omitempty"`
}

// Seal serializes and encrypts env through enc into the opaque ciphertext a
// delivery job stores as its Payload. A nil enc is a programming error
// (DeliveryEncrypter is required), reported as sdk.ErrInvalidInput.
func Seal(enc cryptids.Encrypter, env Envelope) ([]byte, error) {
	if enc == nil {
		return nil, fmt.Errorf("delivery encrypter is required: %w", sdk.ErrInvalidInput)
	}
	plaintext, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal delivery envelope: %w", err)
	}
	ciphertext, err := enc.Encrypt(string(plaintext))
	if err != nil {
		return nil, fmt.Errorf("seal delivery envelope: %w", err)
	}
	return []byte(ciphertext), nil
}

// Open decrypts and deserializes a job payload back into its Envelope. A nil enc
// is sdk.ErrInvalidInput.
func Open(enc cryptids.Encrypter, payload []byte) (Envelope, error) {
	if enc == nil {
		return Envelope{}, fmt.Errorf("delivery encrypter is required: %w", sdk.ErrInvalidInput)
	}
	plaintext, err := enc.Decrypt(string(payload))
	if err != nil {
		return Envelope{}, fmt.Errorf("open delivery envelope: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal([]byte(plaintext), &env); err != nil {
		return Envelope{}, fmt.Errorf("unmarshal delivery envelope: %w", err)
	}
	return env, nil
}
