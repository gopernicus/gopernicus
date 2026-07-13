package authsvc

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// Stable password-mutation errors (design §5.2/§5.3/§5.8). Each wraps a stable sdk
// kind so the transport maps it to the pinned machine code; callers detect them
// with errors.Is.
var (
	// ErrPasswordAlreadySet rejects SetPassword when the account already has a
	// password (the pinned password_already_set 409, design §5.2/§5.8).
	ErrPasswordAlreadySet = fmt.Errorf("password already set: %w", sdk.ErrConflict)
	// ErrPasswordNotSet rejects a remove flow on an account with no password (the
	// pinned password_not_set 404, design §5.3/§5.8).
	ErrPasswordNotSet = fmt.Errorf("password not set: %w", sdk.ErrNotFound)
	// ErrCredentialMutationUnavailable is returned when the revision-serialized
	// credential-mutation rail is not wired (nil CredentialMutations). A wiring
	// fault; the removal fails CLOSED rather than bypassing the rail. Wraps
	// sdk.ErrForbidden (→ 403).
	ErrCredentialMutationUnavailable = fmt.Errorf("credential-mutation rail not wired: %w", sdk.ErrForbidden)
	// ErrNoRecoveryIdentifier is returned by the remove-password start when the
	// account has no active verified recovery identifier to deliver the
	// remove_password code to (design §5.3). Wraps sdk.ErrNotFound (→ 404).
	ErrNoRecoveryIdentifier = fmt.Errorf("no verified recovery identifier available: %w", sdk.ErrNotFound)
)

// SetPassword sets an initial password on an account that has none (design §5.2).
// It requires a consumed set_password recent-authentication grant bound to the
// live session, validates the new password through the shared phase-3 policy, and
// — a new credential class having appeared — revokes every session and mints a
// fresh caller pair (the change-password posture). An account that already has a
// password is refused with ErrPasswordAlreadySet (409) before the grant is spent,
// so the OAuth-only reset-flow abuse the original allowed is closed.
func (s *Service) SetPassword(ctx context.Context, sessionID, userID, newPassword string) (TokenPair, error) {
	// Refuse an already-set password before spending the grant (409).
	if _, err := s.passwords.Get(ctx, userID); err == nil {
		return TokenPair{}, ErrPasswordAlreadySet
	} else if !errors.Is(err, sdk.ErrNotFound) {
		return TokenPair{}, err
	}
	if err := s.validatePassword(ctx, newPassword); err != nil {
		return TokenPair{}, err
	}
	// Consume the set_password grant immediately before the mutation (design §5.0).
	if _, err := s.RequireRecentAuthentication(ctx, sessionID, userID, authgrant.PurposeSetPassword, "", RecentAuthPolicy{}); err != nil {
		return TokenPair{}, err
	}
	hash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return TokenPair{}, fmt.Errorf("hash password: %w", err)
	}
	if err := s.passwords.Set(ctx, userID, hash); err != nil {
		return TokenPair{}, err
	}
	pair, err := s.revokeAndRemintForPassword(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Type:   securityevent.TypePasswordSet,
		Status: securityevent.StatusSuccess,
	})
	return pair, nil
}

// StartRemovePassword issues a remove_password code and delivers it to an existing
// active verified recovery identifier (design §5.3). Possession of that code is the
// reauthentication proof RemovePassword consumes, so the code never rides to a
// proposed new address — only to a channel the account already owns and has
// verified. The code rides the durable outbox; a delivery failure surfaces through
// the returned receipt (design §6.1). An account with no password is refused with
// ErrPasswordNotSet (404); no verified recovery identifier → ErrNoRecoveryIdentifier.
func (s *Service) StartRemovePassword(ctx context.Context, userID string) (StepUpReceipt, error) {
	if s.challenges == nil || s.protector == nil {
		return StepUpReceipt{}, ErrStepUpUnavailable
	}
	if _, err := s.passwords.Get(ctx, userID); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return StepUpReceipt{}, ErrPasswordNotSet
		}
		return StepUpReceipt{}, err
	}
	dest, err := s.verifiedRecoveryIdentifier(ctx, userID)
	if err != nil {
		return StepUpReceipt{}, err
	}
	code, err := s.IssueChallenge(ctx, userID, challenge.PurposeRemovePassword)
	if err != nil {
		return StepUpReceipt{}, err
	}
	kind := string(dest.Kind)
	key := s.idempotencyKey(kind, dest.NormalizedValue, delivery.PurposeSensitiveCode)
	if err := s.enqueueRendered(ctx, delivery.PurposeSensitiveCode, key, delivery.Request{
		Kind:            kind,
		Purpose:         delivery.PurposeSensitiveCode,
		Destination:     dest.NormalizedValue,
		ResolutionInput: dest.NormalizedValue,
		Secret:          code,
	}); err != nil {
		return StepUpReceipt{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Type:   securityevent.TypePasswordRemoveCodeSent,
		Status: securityevent.StatusSuccess,
	})
	return StepUpReceipt{Delivered: true, Receipt: key}, nil
}

// RemovePassword completes a code-gated password removal (design §5.3). Consuming
// the remove_password code proves the caller controls a verified recovery channel
// (the step-up proof for this flow); the credential policy then guards the proposed
// method set (§5.6), and the password is deleted and the user's auth_revision bumped
// atomically under revision-CAS, re-evaluating policy on a concurrent conflict. Any
// pending reset token is invalidated (the original's rule), every session is
// revoked, and a fresh caller pair is minted. An account with no password is refused
// with ErrPasswordNotSet (404); a removal that would leave no direct login method is
// the pinned cannot_remove_last_method (credential.ErrNoLoginMethod, 409).
func (s *Service) RemovePassword(ctx context.Context, userID, code string) (TokenPair, error) {
	if s.credentialMutations == nil {
		return TokenPair{}, ErrCredentialMutationUnavailable
	}
	if _, err := s.passwords.Get(ctx, userID); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return TokenPair{}, ErrPasswordNotSet
		}
		return TokenPair{}, err
	}
	// The remove_password code is this flow's reauthentication proof; a wrong,
	// expired, or locked-out code is the stable challenge error (design §5.3).
	if _, err := s.ConsumeChallenge(ctx, userID, challenge.PurposeRemovePassword, code); err != nil {
		return TokenPair{}, err
	}
	// Invalidate any pending reset token BEFORE the removal: a killed reset with the
	// password still present is benign, but a live reset after removal could restore
	// a password (design §5.3, the original's rule).
	if err := s.invalidatePendingReset(ctx, userID); err != nil {
		return TokenPair{}, err
	}
	if err := s.applyCredentialMutation(ctx, userID, credential.RemovePassword{}); err != nil {
		return TokenPair{}, err
	}
	pair, err := s.revokeAndRemintForPassword(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Type:   securityevent.TypePasswordRemoved,
		Status: securityevent.StatusSuccess,
	})
	return pair, nil
}

// revokeAndRemintForPassword revokes ALL of userID's sessions and mints a fresh
// caller session recording a password authentication — the shared tail of every
// password mutation (set/change/remove, design §5.2/§5.3/§7.2). The DeleteByUser
// failure is RETURNED, never best-effort-logged: the password changed but stale
// sessions may survive, and that must surface to the operator (the §7.2 pin).
func (s *Service) revokeAndRemintForPassword(ctx context.Context, userID string) (TokenPair, error) {
	if err := s.sessions.DeleteByUser(ctx, userID); err != nil {
		return TokenPair{}, err
	}
	return s.mintSession(ctx, userID, s.primaryAuthentication(session.MethodPassword))
}

// applyCredentialMutation evaluates the credential policy against the proposed
// method set and performs the typed mutation atomically under the user's
// auth_revision (design §5.6). It reads the current MethodSet, evaluates
// EvaluateMutation(current, current.With(m)) immediately before Apply, and on an
// sdk.ErrConflict reloads and re-evaluates rather than committing a stale
// safe-looking set — the seam that closes concurrent self-removal. A nil
// CredentialMutations rail fails CLOSED (ErrCredentialMutationUnavailable).
func (s *Service) applyCredentialMutation(ctx context.Context, userID string, m credential.Mutation) error {
	if s.credentialMutations == nil {
		return ErrCredentialMutationUnavailable
	}
	var lastErr error
	for attempt := 0; attempt < adoptionRevisionRetries; attempt++ {
		snap, err := s.credentialMutations.Snapshot(ctx, userID)
		if err != nil {
			return err
		}
		if err := s.credentialPolicy.EvaluateMutation(ctx, snap, snap.With(m)); err != nil {
			return err
		}
		err = s.credentialMutations.Apply(ctx, userID, snap.AuthRevision, m)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sdk.ErrConflict) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// invalidatePendingReset atomically deletes the user's live password_reset
// challenge (design §5.3). The challenge port exposes no targeted user+purpose
// delete outside the passwordreset composition, so this rides Replace's contract —
// it atomically deletes the prior (user, password_reset) row — writing an
// already-expired, secret-free tombstone in its place (unusable, and reaped by
// PurgeExpired). A nil challenge rail is a no-op (nothing to invalidate).
func (s *Service) invalidatePendingReset(ctx context.Context, userID string) error {
	if s.challenges == nil || s.protector == nil {
		return nil
	}
	tombstone, err := generateToken()
	if err != nil {
		return err
	}
	now := s.now()
	_, err = s.challenges.Replace(ctx, challenge.Challenge{
		UserID:       userID,
		Purpose:      challenge.PurposePasswordReset,
		SecretDigest: s.protector.DigestToken(tombstone),
		ExpiresAt:    now, // at/past expiry: never redeemable, and PurgeExpired reaps it
		CreatedAt:    now,
	})
	return err
}

// verifiedRecoveryIdentifier returns the user's active verified recovery-enabled
// identifier the remove_password code is delivered to (design §5.3), primary-first
// then oldest for a stable selection. No such identifier → ErrNoRecoveryIdentifier.
func (s *Service) verifiedRecoveryIdentifier(ctx context.Context, userID string) (identifier.Identifier, error) {
	if s.identifiers == nil {
		return identifier.Identifier{}, ErrNoRecoveryIdentifier
	}
	idents, err := s.identifiers.ListByUser(ctx, userID)
	if err != nil {
		return identifier.Identifier{}, err
	}
	candidates := make([]identifier.Identifier, 0, len(idents))
	for _, it := range idents {
		if it.Active() && it.Verified() && it.RecoveryEnabled {
			candidates = append(candidates, it)
		}
	}
	if len(candidates) == 0 {
		return identifier.Identifier{}, ErrNoRecoveryIdentifier
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.IsPrimary != b.IsPrimary {
			return a.IsPrimary
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	return candidates[0], nil
}
