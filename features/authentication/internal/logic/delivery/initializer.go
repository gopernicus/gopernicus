package delivery

import "context"

// Initializer resolves an opaque delivery command off the request path: an
// unauthenticated start (forgot-password, passwordless) enqueues only the
// normalized identifier (Envelope.ResolutionInput) with an empty body, and the
// delivery processor invokes Initialize later — off the request path — to resolve
// the account, issue the challenge, and render the message exactly once. The auth
// service (authsvc.Service) is the concrete implementer; account lookup and
// challenge issuance live there, not in this package.
//
// kind and purpose are the secret-free routing metadata the sealed command carries
// (never a recipient or the raw logical key); the processor reads them from the
// versioned command envelope and hands them here so the implementer routes its
// resolve/issue/render policy without re-parsing the payload.
type Initializer interface {
	// Initialize resolves the account for cmd (the opened opaque envelope, carrying
	// ResolutionInput), issues/renders the challenge, and returns the fully rendered
	// Envelope with deliver=true. deliver=false means "nothing to deliver" (unknown/
	// ineligible identifier): the processor terminates the command successfully with no
	// send, so known and unknown identifiers are indistinguishable. A returned error is
	// treated as transient and retried with backoff.
	Initialize(ctx context.Context, kind, purpose string, cmd Envelope) (env Envelope, deliver bool, err error)
	// Discard cancels the challenge minted during a prior Initialize, called once when
	// the command fails terminally (design §6.1.1: a terminal send failure deletes that
	// challenge). It is idempotent and best-effort; env is the current opened payload so
	// the implementation can re-derive the challenge identity.
	Discard(ctx context.Context, kind, purpose string, env Envelope) error
}
