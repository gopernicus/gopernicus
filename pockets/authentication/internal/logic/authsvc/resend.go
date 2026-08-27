package authsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Registration-verification resend budgets (coordination-hub-auth-upstream
// CHAU-2.1). They deliberately mirror the passwordless start budgets rather than
// inventing a cooldown table: both are unauthenticated, enumeration-sensitive
// starts that submit opaque work, and one shape is easier to reason about and to
// operate than two.
//
// Both run BEFORE any account resolution and key on PII-free digests, so they
// apply identically to known, unknown, malformed, verified, and deactivated
// addresses.
const (
	// verificationResendsPerIdentifierPerMinute bounds how many resends one
	// normalized address can trigger per minute.
	verificationResendsPerIdentifierPerMinute = 3
	// verificationResendsPerIPPerMinute bounds how many resends one client IP can
	// trigger per minute across all addresses.
	verificationResendsPerIPPerMinute = 10
)

// ErrVerificationResendRateLimited is returned when a resend budget is exhausted.
// It is deliberately distinct from every target-state outcome — a throttle is the
// ONLY thing a public resend response ever distinguishes, and it says nothing
// about whether the address exists. Like ErrRateLimited and
// ErrPasswordlessRateLimited it is a bare sentinel the transport maps to 429
// explicitly (the sdk kernel has no too-many-requests class). Checked with
// errors.Is.
var ErrVerificationResendRateLimited = errors.New("too many verification resend requests")

// ErrAlreadyVerified is returned by the ADMIN resend when the target's primary
// email is already proven. It wraps sdk.ErrConflict so the transport maps it to
// 409 with code "already_verified". It is never returned from the public resend,
// which cannot distinguish this from any other target state. Checked with
// errors.Is.
var ErrAlreadyVerified = fmt.Errorf("the account's primary email is already verified: %w", sdk.ErrConflict)

// ErrUserDeactivated is returned by the ADMIN resend when the target account is
// deactivated: re-issuing a verification code for an account that cannot
// authenticate is a no-op worth reporting honestly to an authorized operator. It
// wraps sdk.ErrConflict so the transport maps it to 409 with code
// "user_deactivated". It is never returned from the public resend. Checked with
// errors.Is.
var ErrUserDeactivated = fmt.Errorf("the account is deactivated: %w", sdk.ErrConflict)

// ErrNoVerifiableEmail is returned by the ADMIN resend when the target holds no
// active primary email identifier to verify. It wraps sdk.ErrNotFound. Never
// returned from the public resend.
var ErrNoVerifiableEmail = fmt.Errorf("the account has no active primary email identifier: %w", sdk.ErrNotFound)

// ResendVerification is the PUBLIC, enumeration-safe registration-verification
// resend (CHAU-2.2).
//
// The request path does exactly three things: normalize, rate-limit, and submit
// opaque replacement work. It performs NO account lookup, NO challenge issue, NO
// render, and NO provider call — every one of those is a timing and behavior
// signal, and all of them happen later in the worker. The outcome is therefore
// identical for unknown, malformed, verified, unverified, and deactivated
// addresses: nil.
//
// The ONLY non-uniform outcomes are a rate limit (429) and infrastructure
// failure (503 admission / 500), neither of which depends on the target's state.
//
// Delivery uses Replace, not Enqueue: a resend is by definition a caller-driven
// repeat, so it must SUPERSEDE any still-pending verification job for the same
// address rather than dedupe onto it. It shares the logical key registration
// itself uses, so a resend supersedes an undelivered original registration mail.
//
// Honest limitation: replacement cannot retract a provider call already accepted.
// A user may receive an old message and a new one — but only the NEWEST challenge
// can verify, because issuing a replacement invalidates the prior code.
func (s *Service) ResendVerification(ctx context.Context, emailAddr string) error {
	if s.queue == nil {
		return ErrDeliveryDisabled
	}
	// A malformed address takes the same accepted path as a valid unknown one, and
	// is limited under a stable digest of the RAW input so a flood of garbage still
	// costs the attacker budget. It never becomes a limiter or job key in the clear.
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		if budgetErr := s.verificationResendBudget(ctx, emailAddr); budgetErr != nil {
			return budgetErr
		}
		s.recordVerificationResend(ctx, securityevent.StatusSuccess)
		return nil
	}
	if err := s.verificationResendBudget(ctx, normalized); err != nil {
		if errors.Is(err, ErrVerificationResendRateLimited) {
			s.recordVerificationResend(ctx, securityevent.StatusBlocked)
		}
		return err
	}

	key := s.idempotencyKey(identity.KindEmail, normalized, delivery.PurposeRegistrationVerification)
	if _, err := s.queue.Replace(ctx, delivery.Command{
		Kind:           identity.KindEmail,
		Purpose:        delivery.PurposeRegistrationVerification,
		IdempotencyKey: key,
		Envelope:       delivery.Envelope{ResolutionInput: normalized},
	}); err != nil {
		return err
	}
	s.recordVerificationResend(ctx, securityevent.StatusSuccess)
	return nil
}

// verificationResendBudget applies the per-identifier and per-IP budgets. The
// keys derive from PII-free digests, never a raw address, and the per-identifier
// key is computed from whatever string the caller supplied — normalized when it
// parsed, raw when it did not — so malformed input is throttled without ever
// being stored or logged in the clear.
func (s *Service) verificationResendBudget(ctx context.Context, value string) error {
	perIdent, err := s.limiter.Allow(ctx,
		"verification_resend:"+string(identifier.KindEmail)+":"+s.identifierDigest(string(identifier.KindEmail), value),
		ratelimiter.PerMinute(verificationResendsPerIdentifierPerMinute))
	if err != nil {
		return err
	}
	if !perIdent.Allowed {
		return ErrVerificationResendRateLimited
	}
	ip := clientInfoFromContext(ctx).ip
	perIP, err := s.limiter.Allow(ctx, "verification_resend:ip:"+ip, ratelimiter.PerMinute(verificationResendsPerIPPerMinute))
	if err != nil {
		return err
	}
	if !perIP.Allowed {
		return ErrVerificationResendRateLimited
	}
	return nil
}

// recordVerificationResend appends a `verification_resend_requested` audit row for
// a PUBLIC request. It carries NO user id — the request path never resolves one —
// and no address or code. Status distinguishes an accepted submission from a
// throttled one, which is the same distinction the caller already observes.
func (s *Service) recordVerificationResend(ctx context.Context, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		Type:    securityevent.TypeVerificationResendRequested,
		Status:  status,
		Details: map[string]any{"kind": string(identifier.KindEmail)},
	})
}

// ResendVerificationForUser is the AUTHORIZED admin resend (CHAU-2.3). Unlike the
// public path it MAY report real target state, because the caller has already
// been authorized to see it: an unknown user is not-found, an already-verified or
// deactivated account is a typed 409.
//
// It reuses the same challenge and delivery-replacement helper as the public
// path, so the two cannot drift into parallel mail implementations. It returns a
// secret-free delivery receipt the operator can poll.
//
// TRUSTED: it applies NO authorization. The bundled handler runs
// Config.UserAdminCheck (action resend-verification) first; a host calling this
// from its own console owns that decision itself.
func (s *Service) ResendVerificationForUser(ctx context.Context, actor Principal, userID string) (StepUpReceipt, error) {
	if s.queue == nil || s.deliver == nil {
		return StepUpReceipt{}, ErrDeliveryDisabled
	}

	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return StepUpReceipt{}, err // sdk.ErrNotFound for an unknown user
	}
	if !u.Active() {
		return StepUpReceipt{}, ErrUserDeactivated
	}

	ident, err := s.activePrimaryEmail(ctx, userID)
	if err != nil {
		return StepUpReceipt{}, err
	}
	if ident.Verified() {
		return StepUpReceipt{}, ErrAlreadyVerified
	}

	code, err := s.IssueChallenge(ctx, userID, challenge.PurposeVerifyRegistration)
	if err != nil {
		return StepUpReceipt{}, err
	}
	key := s.idempotencyKey(identity.KindEmail, ident.NormalizedValue, delivery.PurposeRegistrationVerification)
	if err := s.enqueueRenderedReplace(ctx, delivery.PurposeRegistrationVerification, key, delivery.Request{
		Kind:            identity.KindEmail,
		Purpose:         delivery.PurposeRegistrationVerification,
		Destination:     ident.NormalizedValue,
		ResolutionInput: ident.NormalizedValue,
		Secret:          code,
	}); err != nil {
		return StepUpReceipt{}, err
	}

	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Actor:   securityevent.Principal{Type: actor.Type, ID: actor.ID},
		Type:    securityevent.TypeVerificationResendIssued,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"kind": string(identifier.KindEmail)},
	})
	return StepUpReceipt{Delivered: true, Receipt: key}, nil
}

// activePrimaryEmail resolves a user's active primary email identifier. It is the
// admin-side counterpart of the worker's ResolutionInput lookup; the public path
// never calls it, because resolving an account on the request path is exactly the
// enumeration signal the design forbids.
func (s *Service) activePrimaryEmail(ctx context.Context, userID string) (identifier.Identifier, error) {
	idents, err := s.identifiers.ListByUser(ctx, userID)
	if err != nil {
		return identifier.Identifier{}, err
	}
	for _, it := range idents {
		if it.Kind == identifier.KindEmail && it.IsPrimary && it.Active() {
			return it, nil
		}
	}
	return identifier.Identifier{}, ErrNoVerifiableEmail
}

// initRegistrationVerification is the worker initializer for a registration
// verification resend (CHAU-2.2). Everything enumeration-sensitive happens HERE,
// off the request path:
//
//  1. resolve the active primary email claim for the enqueued normalized address;
//  2. unknown, already-verified, or deactivated → deliver=false, so the job
//     terminates successfully with NO provider call; and
//  3. an active, unverified target gets a FRESH replacement challenge — which
//     invalidates the prior code — and a rendered verification message.
//
// The rendered envelope is checkpointed by the processor before the provider
// send, so a retry re-sends the same code rather than minting another.
func (s *Service) initRegistrationVerification(ctx context.Context, cmd delivery.Envelope) (delivery.Envelope, bool, error) {
	ident, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), cmd.ResolutionInput)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return delivery.Envelope{}, false, nil // no such account — indistinguishable from every other outcome
		}
		return delivery.Envelope{}, false, err
	}
	if ident.Verified() {
		return delivery.Envelope{}, false, nil // nothing left to verify
	}

	u, err := s.users.Get(ctx, ident.UserID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return delivery.Envelope{}, false, nil
		}
		return delivery.Envelope{}, false, err
	}
	if !u.Active() {
		return delivery.Envelope{}, false, nil // a deactivated account gets no mail
	}

	code, err := s.IssueChallenge(ctx, ident.UserID, challenge.PurposeVerifyRegistration)
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	env, err := s.deliver.Render(ctx, delivery.Request{
		Kind:            identity.KindEmail,
		Purpose:         delivery.PurposeRegistrationVerification,
		Destination:     ident.NormalizedValue,
		ResolutionInput: cmd.ResolutionInput,
		Secret:          code,
	})
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  ident.UserID,
		Type:    securityevent.TypeVerificationResendIssued,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"kind": string(identifier.KindEmail)},
	})
	return env, true, nil
}
