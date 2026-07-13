package authsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// defaultRecentAuthMaxAge is the default maximum age of a recent-authentication
// grant and of the recent-primary-login shortcut window (design §5.0).
const defaultRecentAuthMaxAge = 5 * time.Minute

// Step-up / recent-authentication errors (design §5.0/§5.8). They are stable and
// deliberately coarse: a rejected proof never distinguishes "no password set" from
// "wrong password", and a missing grant never names which check failed.
var (
	// ErrStepUpUnavailable is returned when the recent-authentication subsystem is
	// not wired (nil grant repository / challenge rail). A wiring fault, not a
	// user-facing outcome; wraps sdk.ErrForbidden (→ 403).
	ErrStepUpUnavailable = fmt.Errorf("recent-authentication subsystem not wired: %w", sdk.ErrForbidden)
	// ErrStepUpRequired is returned by RequireRecentAuthentication when neither a
	// consumable grant nor a sufficiently recent primary login satisfies the
	// operation's policy: the caller must complete a fresh step-up. Wraps
	// sdk.ErrForbidden (→ 403).
	ErrStepUpRequired = fmt.Errorf("recent authentication required: %w", sdk.ErrForbidden)
	// ErrStepUpProof is the single generic failure for a rejected step-up proof (a
	// wrong or missing password, an absent password credential). It never names the
	// reason. Wraps sdk.ErrUnauthorized (→ 401).
	ErrStepUpProof = fmt.Errorf("step-up proof rejected: %w", sdk.ErrUnauthorized)
	// ErrStepUpDestination is returned by BeginStepUp when the account has no active
	// verified identifier of the requested kind to deliver a step-up code to. Wraps
	// sdk.ErrNotFound (→ 404).
	ErrStepUpDestination = fmt.Errorf("no verified identifier available for step-up: %w", sdk.ErrNotFound)
)

// RecentAuthPolicy is the per-operation recency/strength policy the
// recent-primary-login shortcut and grant lifetime honor (design §5.0). MaxAge
// bounds how old a proving authentication may be (zero → the five-minute default);
// MinAssurance is the least assurance level a recorded login must meet for the
// shortcut to fire (zero → AAL1, every v3 method). An operation can force an
// explicit fresh step-up — refusing the shortcut — by requiring an assurance above
// what v3 primary logins record.
type RecentAuthPolicy struct {
	MaxAge       time.Duration
	MinAssurance session.AssuranceLevel
}

func (p RecentAuthPolicy) maxAge() time.Duration {
	if p.MaxAge <= 0 {
		return defaultRecentAuthMaxAge
	}
	return p.MaxAge
}

func (p RecentAuthPolicy) minAssurance() session.AssuranceLevel {
	if p.MinAssurance == session.AssuranceUnknown {
		return session.AssuranceAAL1
	}
	return p.MinAssurance
}

// StepUpStart is the input to BeginStepUp: the live session and user, the sensitive
// operation the grant will authorize (Purpose + Context), and the identifier Kind
// to deliver the step-up code to (empty → email).
type StepUpStart struct {
	SessionID string
	UserID    string
	Purpose   string
	Context   string
	Kind      string
}

// StepUpReceipt is the result of BeginStepUp: whether a code was enqueued and the
// PII-free delivery receipt the caller can poll (design §6.1.1). It never carries
// the destination or the code.
type StepUpReceipt struct {
	Delivered bool
	Receipt   string
}

// StepUpCompletion is the input shared by the step-up completion methods: the live
// session and user, the operation the earned grant will authorize, and the policy
// bounding the grant's lifetime.
type StepUpCompletion struct {
	SessionID string
	UserID    string
	Purpose   string
	Context   string
	Policy    RecentAuthPolicy
}

// stepUpBinding is the challenge stored-context that pins a step-up code to one
// operation (design §5.0): a code earned to change an email cannot complete a
// password removal because the purpose/context digest will not match.
type stepUpBinding struct {
	Purpose string `json:"purpose"`
	Context string `json:"context"`
}

// grantContextDigest hashes an operation's context string to the stable digest a
// grant is bound by (design §5.0). Empty context digests consistently, so a
// context-free operation still round-trips through Create/Consume unchanged.
func grantContextDigest(context string) string {
	sum := sha256.Sum256([]byte(context))
	return hex.EncodeToString(sum[:])
}

// assuranceRank orders assurance levels so a policy can compare "at least".
func assuranceRank(a session.AssuranceLevel) int {
	switch a {
	case session.AssuranceAAL1:
		return 1
	case session.AssuranceAAL2:
		return 2
	case session.AssuranceAAL3:
		return 3
	default:
		return 0
	}
}

// RequireRecentAuthentication is the consume-before-mutation primitive every
// sensitive credential/identifier mutation calls immediately before it runs (design
// §5.0). It is satisfied in one of two ways:
//
//   - the recent-primary-login shortcut: a login recorded on the live session that
//     is within the policy's MaxAge and meets its MinAssurance satisfies the
//     operation without spending a grant; or
//   - an explicit grant: the single-use, session- and purpose+context-bound grant
//     the caller earned through a step-up completion, consumed atomically here.
//
// The shortcut is tried first so a recent login never wastes an explicit grant.
// When neither applies it returns ErrStepUpRequired; a grant bound to another user
// is treated as absent (defensive). A nil grant repository → ErrStepUpUnavailable.
func (s *Service) RequireRecentAuthentication(ctx context.Context, sessionID, userID, purpose, context string, policy RecentAuthPolicy) (authgrant.Grant, error) {
	if s.authGrants == nil {
		return authgrant.Grant{}, ErrStepUpUnavailable
	}
	if sessionID == "" || userID == "" {
		return authgrant.Grant{}, ErrStepUpRequired
	}
	now := s.now().UTC()

	if g, ok := s.recentLoginGrant(ctx, sessionID, userID, purpose, context, policy, now); ok {
		return g, nil
	}

	g, err := s.authGrants.Consume(ctx, sessionID, purpose, grantContextDigest(context), now)
	switch {
	case err == nil:
		if g.UserID != userID {
			return authgrant.Grant{}, ErrStepUpRequired
		}
		return g, nil
	case errors.Is(err, sdk.ErrExpired) || errors.Is(err, sdk.ErrNotFound):
		return authgrant.Grant{}, ErrStepUpRequired
	default:
		return authgrant.Grant{}, err
	}
}

// recentLoginGrant reports whether the live session's recorded primary
// authentication satisfies the policy, and if so synthesizes (never persists) the
// grant that stands in for an explicit step-up (design §5.0). It fails closed on any
// session lookup error, a user mismatch, no recorded login, an over-age login, or an
// under-assurance login.
func (s *Service) recentLoginGrant(ctx context.Context, sessionID, userID, purpose, context string, policy RecentAuthPolicy, now time.Time) (authgrant.Grant, bool) {
	sess, err := s.sessions.Get(ctx, sessionID)
	if err != nil || sess.UserID != userID {
		return authgrant.Grant{}, false
	}
	meta := sess.Authentication
	if !meta.Recorded() {
		return authgrant.Grant{}, false
	}
	if now.Sub(meta.AuthenticatedAt) > policy.maxAge() {
		return authgrant.Grant{}, false
	}
	if assuranceRank(meta.Assurance) < assuranceRank(policy.minAssurance()) {
		return authgrant.Grant{}, false
	}
	return authgrant.Grant{
		SessionID:       sessionID,
		UserID:          userID,
		Purpose:         purpose,
		ContextDigest:   grantContextDigest(context),
		Methods:         meta.Methods,
		Assurance:       meta.Assurance,
		AuthenticatedAt: meta.AuthenticatedAt,
		ExpiresAt:       meta.AuthenticatedAt.Add(policy.maxAge()),
		CreatedAt:       now,
	}, true
}

// BeginStepUp issues a recent-authentication step-up code and delivers it to an
// existing active verified identifier of the requested kind (design §5.0). The code
// is bound to the operation (purpose + context) so it cannot complete a different
// mutation, and it rides the durable outbox — a delivery failure surfaces through
// the returned receipt, never synchronously. Proving a proposed NEW address can
// never earn a grant: the code goes to an identifier the account already owns and
// has verified. No verified identifier of the kind → ErrStepUpDestination.
func (s *Service) BeginStepUp(ctx context.Context, in StepUpStart) (StepUpReceipt, error) {
	if s.authGrants == nil || s.challenges == nil || s.protector == nil {
		return StepUpReceipt{}, ErrStepUpUnavailable
	}
	kind := in.Kind
	if kind == "" {
		kind = string(identifier.KindEmail)
	}
	dest, err := s.ActiveVerifiedIdentifier(ctx, in.UserID, kind)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return StepUpReceipt{}, ErrStepUpDestination
		}
		return StepUpReceipt{}, err
	}
	code, err := s.IssueChallenge(ctx, in.UserID, challenge.PurposeStepUp,
		WithStoredContext(stepUpBinding{Purpose: in.Purpose, Context: in.Context}))
	if err != nil {
		return StepUpReceipt{}, err
	}
	key := s.idempotencyKey(kind, dest, delivery.PurposeSensitiveCode)
	if err := s.enqueueRendered(ctx, delivery.PurposeSensitiveCode, key, delivery.Request{
		Kind:            kind,
		Purpose:         delivery.PurposeSensitiveCode,
		Destination:     dest,
		ResolutionInput: dest,
		Secret:          code,
	}); err != nil {
		return StepUpReceipt{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  in.UserID,
		Type:    securityevent.TypeStepUpChallengeSent,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"purpose": in.Purpose},
	})
	return StepUpReceipt{Delivered: true, Receipt: key}, nil
}

// CompleteStepUpWithPassword earns a grant by re-verifying the account's existing
// password (design §5.0). A missing password credential or a wrong password is the
// single generic ErrStepUpProof (never distinguished) and records a step-up failure.
// On success it persists the operation-bound grant and records a step-up success.
func (s *Service) CompleteStepUpWithPassword(ctx context.Context, in StepUpCompletion, password string) (authgrant.Grant, error) {
	if s.authGrants == nil {
		return authgrant.Grant{}, ErrStepUpUnavailable
	}
	hash, err := s.passwords.Get(ctx, in.UserID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			s.recordStepUp(ctx, in.UserID, in.Purpose, securityevent.StatusFailure)
			return authgrant.Grant{}, ErrStepUpProof
		}
		return authgrant.Grant{}, err
	}
	if err := s.hasher.VerifyPassword(hash, password); err != nil {
		s.recordStepUp(ctx, in.UserID, in.Purpose, securityevent.StatusFailure)
		return authgrant.Grant{}, ErrStepUpProof
	}
	return s.issueGrant(ctx, in, s.primaryAuthentication(session.MethodPassword))
}

// CompleteStepUpWithIdentifierCode earns a grant by consuming the step-up code
// BeginStepUp delivered to an existing verified identifier (design §5.0). The code's
// stored context is validated against this operation (purpose + context): a code
// earned for a different mutation is consumed and rejected as ErrChallengeInvalid,
// so it can never be replayed against another operation. The stable challenge errors
// (expired / invalid / too-many) propagate unchanged.
func (s *Service) CompleteStepUpWithIdentifierCode(ctx context.Context, in StepUpCompletion, code string) (authgrant.Grant, error) {
	if s.authGrants == nil {
		return authgrant.Grant{}, ErrStepUpUnavailable
	}
	if _, err := s.ConsumeChallenge(ctx, in.UserID, challenge.PurposeStepUp, code,
		WithExpectedContext(stepUpBinding{Purpose: in.Purpose, Context: in.Context})); err != nil {
		status := securityevent.StatusFailure
		if errors.Is(err, ErrTooManyAttempts) {
			status = securityevent.StatusBlocked
		}
		s.recordStepUp(ctx, in.UserID, in.Purpose, status)
		return authgrant.Grant{}, err
	}
	return s.issueGrant(ctx, in, s.primaryAuthentication(session.MethodEmailCode))
}

// issueGrant persists the operation-bound recent-authentication grant a completed
// step-up earned, recording the method/assurance it was proven with and bounding its
// lifetime by the operation's policy (design §5.0). It records a step-up success.
func (s *Service) issueGrant(ctx context.Context, in StepUpCompletion, meta session.AuthenticationMetadata) (authgrant.Grant, error) {
	now := s.now().UTC()
	g, err := s.authGrants.Create(ctx, authgrant.Grant{
		SessionID:       in.SessionID,
		UserID:          in.UserID,
		Purpose:         in.Purpose,
		ContextDigest:   grantContextDigest(in.Context),
		Methods:         meta.Methods,
		Assurance:       meta.Assurance,
		AuthenticatedAt: meta.AuthenticatedAt,
		ExpiresAt:       now.Add(in.Policy.maxAge()),
		CreatedAt:       now,
	})
	if err != nil {
		return authgrant.Grant{}, err
	}
	s.recordStepUp(ctx, in.UserID, in.Purpose, securityevent.StatusSuccess)
	return g, nil
}

// recordStepUp appends a step-up audit row carrying the operation purpose only —
// never a secret, code, or destination (design §5.1/§5.0).
func (s *Service) recordStepUp(ctx context.Context, userID, purpose, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Type:    securityevent.TypeStepUp,
		Status:  status,
		Details: map[string]any{"purpose": purpose},
	})
}
