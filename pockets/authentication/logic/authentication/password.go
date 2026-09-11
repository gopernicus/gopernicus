package authentication

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
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
	if err := s.requirePasswordFlows(); err != nil {
		return TokenPair{}, err
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
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
	revision, err := s.passwords.Change(ctx, userID, user.PasswordChange{ExpectedAuthRevision: u.AuthRevision, NewHash: hash, Now: s.now()})
	if err != nil {
		return TokenPair{}, err
	}
	pair, err := s.mintSession(ctx, userID, revision, s.primaryAuthentication(session.MethodPassword))
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

type credentialRemovalBinding struct {
	AuthRevision         int64  `json:"auth_revision"`
	RecoveryIdentifierID string `json:"recovery_identifier_id"`
	Provider             string `json:"provider,omitempty"`
}

func (s *Service) credentialRemovalProof(ctx context.Context, userID, provider string) (credentialRemovalBinding, identifier.Identifier, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return credentialRemovalBinding{}, identifier.Identifier{}, err
	}
	if !u.Active() {
		return credentialRemovalBinding{}, identifier.Identifier{}, session.ErrUserNotActive
	}
	dest, err := s.verifiedRecoveryIdentifier(ctx, userID)
	if err != nil {
		return credentialRemovalBinding{}, identifier.Identifier{}, err
	}
	return credentialRemovalBinding{AuthRevision: u.AuthRevision, RecoveryIdentifierID: dest.ID, Provider: provider}, dest, nil
}

// StartRemovePassword issues a remove_password code and delivers it to an existing
// active verified recovery identifier (design §5.3). Possession of that code is the
// reauthentication proof RemovePassword consumes, so the code never rides to a
// proposed new address — only to a channel the account already owns and has
// verified. The code rides the durable outbox; a delivery failure surfaces through
// the returned receipt (design §6.1). An account with no password is refused with
// ErrPasswordNotSet (404); no verified recovery identifier → ErrNoRecoveryIdentifier.
func (s *Service) StartRemovePassword(ctx context.Context, userID string) (StepUpReceipt, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return StepUpReceipt{}, err
	}
	if s.credentialMutations == nil {
		return StepUpReceipt{}, ErrCredentialMutationUnavailable
	}
	if s.challenges == nil || s.protector == nil {
		return StepUpReceipt{}, ErrStepUpUnavailable
	}
	if err := s.sensitiveCodeBudget(ctx, userID); err != nil {
		return StepUpReceipt{}, err
	}
	if _, err := s.passwords.Get(ctx, userID); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return StepUpReceipt{}, ErrPasswordNotSet
		}
		return StepUpReceipt{}, err
	}
	proof, dest, err := s.credentialRemovalProof(ctx, userID, "")
	if err != nil {
		return StepUpReceipt{}, err
	}
	key, err := s.issueAndEnqueueCode(ctx, userID, challenge.PurposeRemovePassword, string(dest.Kind), dest.NormalizedValue, withStoredContext(proof))
	if err != nil {
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
// atomically under revision-CAS. A concurrent change requires fresh proof. Any
// pending reset token is invalidated (the original's rule), every session is
// revoked, and a fresh caller pair is minted. An account with no password is refused
// with ErrPasswordNotSet (404); a removal that would leave no direct login method is
// the pinned cannot_remove_last_method (credential.ErrNoLoginMethod, 409).
func (s *Service) RemovePassword(ctx context.Context, userID, code string) (TokenPair, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return TokenPair{}, err
	}
	if s.credentialMutations == nil {
		return TokenPair{}, ErrCredentialMutationUnavailable
	}
	if _, err := s.passwords.Get(ctx, userID); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return TokenPair{}, ErrPasswordNotSet
		}
		return TokenPair{}, err
	}
	proof, dest, err := s.credentialRemovalProof(ctx, userID, "")
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := s.consumeChallenge(ctx, userID, challenge.PurposeRemovePassword, code, withExpectedContext(proof)); err != nil {
		return TokenPair{}, err
	}
	if err := s.applyCredentialMutationAtRevision(ctx, userID, proof.AuthRevision, credential.RemovePassword{}); err != nil {
		return TokenPair{}, err
	}
	pair, err := s.mintSession(ctx, userID, proof.AuthRevision+1, s.primaryAuthentication(passwordlessCodeMethod(string(dest.Kind))))
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

func (s *Service) applyCredentialMutationAtRevision(ctx context.Context, userID string, revision int64, m credential.Mutation) error {
	if s.credentialMutations == nil {
		return ErrCredentialMutationUnavailable
	}
	snap, err := s.credentialMutations.Snapshot(ctx, userID)
	if err != nil {
		return err
	}
	if snap.AuthRevision != revision {
		return sdk.ErrConflict
	}
	if err := s.credentialPolicy.EvaluateMutation(ctx, snap, snap.With(m)); err != nil {
		return err
	}
	return s.credentialMutations.Apply(ctx, userID, revision, m)
}
