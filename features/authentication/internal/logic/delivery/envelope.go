// Package delivery holds the auth feature's shared outbound-delivery logic
// (design §6.1): the kind-aware renderer/router, the transport-neutral dispatcher
// seam, the versioned encrypted command envelope and its reusable processor (the
// command subpackage), and the two host-owned execution runtimes — the durable
// jobs-mode processor and the bounded in-process pool. It imports sdk ports only
// (never inbound/outbound/integrations) and is constructor-injected, never a
// registry.
//
// The Envelope is the plaintext a delivery command must carry but must never
// persist in the clear: the destination address, the rendered secret/message, and
// the account-resolution input the processor uses to resolve the account off the
// request path (design §6.1.1). It is the producer vocabulary; the versioned,
// sealed command.Envelope the durable transport stores is derived from it
// (commandcodec.go). Because none of these fields is a durable column, a leaked
// job row exposes ciphertext only — the enumeration-resistance and secret-at-rest
// invariants are structural, not disciplinary.
package delivery

// Envelope is the plaintext delivery instruction the producer builds and the
// processor renders. It is never stored or logged in the clear. Destination is the
// resolved address the processor sends to; ResolutionInput is the normalized
// identifier the processor uses to resolve the account (present because
// unauthenticated starts do NOT resolve on the request path); Subject/Body are the
// rendered message (Subject is empty for SMS). HTML is the rendered email body when
// present (the email rail persists both HTML and text so a retry resends the
// identical message; SMS leaves it empty). Secret is the rendered OTP/token, kept
// separate so it can be scrubbed from diagnostics.
type Envelope struct {
	Destination     string `json:"destination"`
	ResolutionInput string `json:"resolution_input"`
	Subject         string `json:"subject,omitempty"`
	Body            string `json:"body"`
	HTML            string `json:"html,omitempty"`
	Secret          string `json:"secret,omitempty"`
}
