package authsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// deliveryQueue is the durable outbound outbox seam (design §6.1.1): every auth
// message now enqueues here instead of a request-time provider send, so account
// resolution and provider latency happen in the host-owned worker and can never
// leak identifier existence. The shared delivery.Service satisfies it; it is
// declared here (structural) so authsvc accepts an interface and a test can inject a
// synchronous driver. Nil → outbound is disabled (fail-closed: the send sites error
// rather than silently dropping mail).
type deliveryQueue interface {
	// Enqueue seals cmd's envelope and enqueues an at-least-once job, idempotent by
	// cmd.IdempotencyKey.
	Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error)
	// Replace supersedes any prior pending job holding cmd.IdempotencyKey (a
	// user-requested resend).
	Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error)
	// Status returns the read-only delivery status for a Receipt key (session-gated
	// callers poll it; unknown key → sdk.ErrNotFound).
	Status(ctx context.Context, idempotencyKey string) (delivery.Status, error)
}

// identifierKeyer derives a stable, non-reversible key from an identifier for
// PII-free outbox idempotency keys (design §4.4). auth.IdentifierKeyer satisfies it
// structurally; declared here so authsvc carries no import cycle with its host-facing
// package. delivery never imports it — the caller supplies the finished key.
type identifierKeyer interface {
	IdentifierKey(kind, normalizedValue string) string
}

// ErrDeliveryDisabled is returned by a send site when no delivery queue is wired.
// The outbound outbox is required to deliver any auth message (design §6.1.1), so a
// send with the subsystem off fails loudly rather than silently dropping the
// message.
var ErrDeliveryDisabled = fmt.Errorf("delivery outbox not wired: %w", sdk.ErrForbidden)

// idempotencyKey derives the PII-free outbox key for (kind, normalizedValue,
// purpose) by suffixing the shared identifier digest with the purpose (design
// §4.4/§6.1.1: raw PII never enters a job key). Delivery never imports the keyer —
// the caller supplies the finished key.
func (s *Service) idempotencyKey(kind, normalizedValue, purpose string) string {
	return s.identifierDigest(kind, normalizedValue) + ":" + purpose
}

// identifierDigest returns a stable, non-reversible key fragment for
// (kind, normalizedValue) so raw email/phone never enters a limiter or idempotency
// key (design §4.4). A wired host IdentifierKeyer supplies the shared keyed HMAC
// digest — required in production so every instance keys one identifier to one
// bucket (§8); without one, a per-instance SHA-256 digest still keeps the fragment
// PII-free (the development fallback, warned at construction). Equivalent
// normalized values always digest to the same fragment, so a login and a delivery
// for the same identifier share one bucket. It is the single seam the login limiter
// key and the outbox idempotency key both derive from.
func (s *Service) identifierDigest(kind, normalizedValue string) string {
	if s.identifierKeyer != nil {
		return s.identifierKeyer.IdentifierKey(kind, normalizedValue)
	}
	digest, err := s.tokenHasher.Hash(kind + ":" + normalizedValue)
	if err != nil {
		return kind + ":" + normalizedValue
	}
	return digest
}

// enqueueRendered renders req through the shared router on the request path (the
// account is already resolved, so this leaks no existence signal) and enqueues the
// sealed envelope as a durable job. It is the migration target for the
// account-resolved send sites (registration verification, OAuth pending link): the
// worker delivers the persisted message, so a provider outage never blocks or fails
// the request beyond the enqueue itself.
func (s *Service) enqueueRendered(ctx context.Context, purpose, idempotencyKey string, req delivery.Request) error {
	if s.queue == nil || s.deliver == nil {
		return ErrDeliveryDisabled
	}
	env, err := s.deliver.Render(ctx, req)
	if err != nil {
		return err
	}
	_, err = s.queue.Enqueue(ctx, delivery.Command{
		Kind:           req.Kind,
		Purpose:        purpose,
		IdempotencyKey: idempotencyKey,
		Envelope:       env,
	})
	return err
}

// enqueueRenderedReplace renders req and SUPERSEDES any prior pending job holding
// idempotencyKey (design §6.1.1: Replace is the user-requested-resend primitive). An
// identifier add/change start is a caller-driven (re)send — a restart to the same
// address must deliver a fresh proof code rather than dedupe onto the stale job — so
// it enqueues through Replace, not the idempotent Enqueue the account-resolved
// one-shot sends use.
func (s *Service) enqueueRenderedReplace(ctx context.Context, purpose, idempotencyKey string, req delivery.Request) error {
	if s.queue == nil || s.deliver == nil {
		return ErrDeliveryDisabled
	}
	env, err := s.deliver.Render(ctx, req)
	if err != nil {
		return err
	}
	_, err = s.queue.Replace(ctx, delivery.Command{
		Kind:           req.Kind,
		Purpose:        purpose,
		IdempotencyKey: idempotencyKey,
		Envelope:       env,
	})
	return err
}

// DeliveryStatus returns the read-only delivery status for a receipt key (design
// §6.1.1). A session-gated caller polls it to learn that delivery failed without
// holding the start request open; the handler enforces the live session, and
// possession of the receipt key (a PII-free digest the caller received) is the rest
// of the authorization. Unknown key → sdk.ErrNotFound.
func (s *Service) DeliveryStatus(ctx context.Context, receiptKey string) (delivery.Status, error) {
	if s.queue == nil {
		return delivery.Status{}, ErrDeliveryDisabled
	}
	return s.queue.Status(ctx, receiptKey)
}

// Initialize implements delivery.Initializer for opaque start jobs (design
// §6.1.1). The unauthenticated request path enqueued only the normalized identifier
// (ResolutionInput) with an empty body; here — off the request path, in the worker —
// the account is resolved, the challenge issued, and the message rendered exactly
// once. deliver=false means "nothing to deliver" (unknown/ineligible identifier):
// the worker terminates the job successfully with no send, so known and unknown
// identifiers are indistinguishable.
func (s *Service) Initialize(ctx context.Context, job deliveryjob.Job, cmd delivery.Envelope) (delivery.Envelope, bool, error) {
	switch job.Purpose {
	case delivery.PurposePasswordReset:
		return s.initPasswordReset(ctx, cmd)
	case delivery.PurposeMagicLink:
		return s.initPasswordlessLink(ctx, job.Kind, cmd)
	case delivery.PurposeLoginCode:
		return s.initPasswordlessCode(ctx, job.Kind, cmd)
	default:
		return delivery.Envelope{}, false, fmt.Errorf("delivery: no initializer for purpose %q: %w", job.Purpose, sdk.ErrInvalidInput)
	}
}

// initPasswordReset resolves the recovery identifier for the enqueued normalized
// address, issues the password_reset token challenge, and renders the reset email —
// all off the request path. An unknown or unverified recovery identifier resolves
// nothing (deliver=false), so a forgot-password start reveals no account existence.
func (s *Service) initPasswordReset(ctx context.Context, cmd delivery.Envelope) (delivery.Envelope, bool, error) {
	ident, err := s.identifiers.GetRecovery(ctx, string(identifier.KindEmail), cmd.ResolutionInput)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return delivery.Envelope{}, false, nil // never reveal whether a recovery identifier exists
		}
		return delivery.Envelope{}, false, err
	}
	if !ident.Verified() {
		return delivery.Envelope{}, false, nil // recovery requires a proven address (design §2.3)
	}
	token, err := s.IssueChallenge(ctx, ident.UserID, challenge.PurposePasswordReset)
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	env, err := s.deliver.Render(ctx, delivery.Request{
		Kind:            identity.KindEmail,
		Purpose:         delivery.PurposePasswordReset,
		Destination:     ident.NormalizedValue,
		ResolutionInput: cmd.ResolutionInput,
		Secret:          token,
	})
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	return env, true, nil
}

// Discard implements delivery.Initializer: on a terminal send failure it voids the
// challenge minted during Initialize (design §6.1.1: a terminal send failure deletes
// that challenge) so a never-delivered secret cannot linger. It is best-effort and
// idempotent — the token was never delivered, so voiding it costs nothing and a
// missing challenge is a no-op.
func (s *Service) Discard(ctx context.Context, job deliveryjob.Job, env delivery.Envelope) error {
	if env.Secret == "" {
		return nil
	}
	switch job.Purpose {
	case delivery.PurposePasswordReset:
		// RedeemToken atomically deletes the (purpose, token) challenge row; the
		// consumed value is discarded. Errors are ignored (best-effort void).
		_, _ = s.RedeemToken(ctx, challenge.PurposePasswordReset, env.Secret)
	case delivery.PurposeMagicLink:
		// A never-delivered magic-link token is voided by deleting its challenge row.
		_, _ = s.RedeemToken(ctx, challenge.PurposeLoginMagicLink, env.Secret)
	case delivery.PurposeLoginCode:
		// The login OTP is an HMAC-protected code, not a token: there is no
		// delete-by-secret path (that is the token rail). A never-delivered code cannot
		// be voided by secret, so it is left to expire under its short TTL + attempt
		// lockout — it was never delivered, so it discloses nothing meanwhile.
	}
	return nil
}
