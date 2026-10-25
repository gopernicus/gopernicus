package authentication

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// passkeyResetAttempts bounds ResetUserPasskeys' retries of a lost revision race.
const passkeyResetAttempts = 3

// The Change values a passkey_change_notice renders.
const (
	passkeyChangeAdded   = "added"
	passkeyChangeRemoved = "removed"
	passkeyChangeReset   = "reset"
)

// PasskeyRenameInput renames one of the caller's passkeys.
type PasskeyRenameInput struct {
	UserID    string
	PasskeyID string
	Name      string
}

// PasskeyRemoveInput removes one of the caller's passkeys from their proven live
// session.
type PasskeyRemoveInput struct {
	SessionID string
	UserID    string
	PasskeyID string
}

// ListPasskeys returns userID's passkeys, oldest first.
func (s *Service) ListPasskeys(ctx context.Context, userID string) ([]passkey.Passkey, error) {
	if err := s.requirePasskeys(); err != nil {
		return nil, err
	}
	return s.passkeys.ListByUser(ctx, userID)
}

// RenamePasskey sets a passkey's display name. An invalid name is
// sdk.ErrInvalidInput; another user's passkey is sdk.ErrNotFound. Renaming
// changes no credential, so it needs no step-up.
func (s *Service) RenamePasskey(ctx context.Context, in PasskeyRenameInput) error {
	if err := s.requirePasskeys(); err != nil {
		return err
	}
	name, ok := passkey.NormalizeName(in.Name)
	if !ok {
		return fmt.Errorf("authentication: a passkey name is 1 to %d characters with no control character: %w", passkey.MaxNameRunes, sdk.ErrInvalidInput)
	}
	return s.passkeys.Rename(ctx, in.PasskeyID, in.UserID, name)
}

// RemovePasskey removes one of the caller's passkeys through the
// revision-serialized credential rail, which revokes every session of the user,
// the caller's included. It consumes a remove_passkey grant bound to the
// passkey under the second-factor gate's policy — AAL2 while the user holds a
// user-verified passkey or a confirmed authenticator app — and the credential
// policy must leave a login method.
// Success records passkey_removed and sends the passkey_change_notice.
func (s *Service) RemovePasskey(ctx context.Context, in PasskeyRemoveInput) error {
	if err := s.requirePasskeys(); err != nil {
		return err
	}
	owner, err := s.users.Get(ctx, in.UserID)
	if err != nil {
		return err
	}
	existing, err := s.passkeys.ListByUser(ctx, in.UserID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(existing, func(p passkey.Passkey) bool { return p.ID == in.PasskeyID }) {
		return fmt.Errorf("authentication: passkey %q not found: %w", in.PasskeyID, sdk.ErrNotFound)
	}
	policy, _, err := s.secondFactorPolicy(ctx, in.UserID)
	if err != nil {
		return err
	}
	if _, err := s.RequireRecentAuthentication(ctx, in.SessionID, in.UserID, authgrant.PurposeRemovePasskey, in.PasskeyID, policy); err != nil {
		return err
	}
	if err := s.applyCredentialMutationAtRevision(ctx, in.UserID, owner.AuthRevision, credential.RemovePasskey{PasskeyID: in.PasskeyID}); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  in.UserID,
		Type:    securityevent.TypePasskeyRemoved,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"passkey_id": in.PasskeyID, "remaining": len(existing) - 1},
	})
	s.enqueuePasskeyChangeNotices(ctx, in.UserID, passkeyChangeRemoved, in.PasskeyID)
	return nil
}

// ResetUserPasskeys deletes every passkey userID holds and revokes their
// sessions: the operator path for a user who lost their passkeys, after which
// they enroll again from a primary login. It returns how many were removed.
//
// TRUSTED: it applies NO authorization. The bundled admin route runs the host's
// administration policy (and the admins MFA gate) first. It bypasses the credential
// policy: the user keeps their other login and recovery methods, and a reset
// that left none is recoverable through those recovery methods. Records
// passkeys_reset, attributed to actor, and sends the passkey_change_notice.
func (s *Service) ResetUserPasskeys(ctx context.Context, actor Principal, userID string) (int, error) {
	if err := s.requirePasskeys(); err != nil {
		return 0, err
	}
	if s.credentialMutations == nil {
		return 0, ErrCredentialMutationUnavailable
	}
	removed, err := s.applyPasskeyReset(ctx, userID)
	if err != nil {
		return 0, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Actor:   securityevent.Principal{Type: actor.Type, ID: actor.ID},
		Type:    securityevent.TypePasskeysReset,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"removed": removed},
	})
	if removed > 0 {
		s.enqueuePasskeyChangeNotices(ctx, userID, passkeyChangeReset, "all")
	}
	return removed, nil
}

// applyPasskeyReset applies RemovePasskeys at the snapshot it counted, retrying
// a lost revision race a bounded number of times, so the count is exactly what
// the committed mutation removed.
func (s *Service) applyPasskeyReset(ctx context.Context, userID string) (int, error) {
	var err error
	for range passkeyResetAttempts {
		var snap credential.MethodSet
		if snap, err = s.credentialMutations.Snapshot(ctx, userID); err != nil {
			return 0, err
		}
		if err = s.credentialMutations.Apply(ctx, userID, snap.AuthRevision, credential.RemovePasskeys{}); err == nil {
			return len(snap.Passkeys), nil
		}
		if !errors.Is(err, sdk.ErrConflict) {
			return 0, err
		}
	}
	return 0, err
}

// enqueuePasskeyChangeNotices sends the passkey_change_notice to each of the
// user's verified contact channels — the identifier-change notice path. It is
// best-effort over the durable outbox: the change has committed, so a failed
// enqueue is a WARN, never a rollback. The notice names the change, never the
// passkey.
func (s *Service) enqueuePasskeyChangeNotices(ctx context.Context, userID, change, subject string) {
	if s.queue == nil || s.deliver == nil {
		return
	}
	info := clientInfoFromContext(ctx)
	when := s.now().UTC().Format(time.RFC3339)
	for _, it := range s.verifiedContactChannels(ctx, userID, "") {
		if !s.deliver.Supports(string(it.Kind)) {
			continue
		}
		key := s.idempotencyKey(string(it.Kind), it.NormalizedValue, delivery.PurposePasskeyChangeNotice) + ":" + change + ":" + subject + ":" + when
		if err := s.enqueueRendered(ctx, delivery.PurposePasskeyChangeNotice, key, delivery.Request{
			Kind:            string(it.Kind),
			Purpose:         delivery.PurposePasskeyChangeNotice,
			Destination:     it.NormalizedValue,
			ResolutionInput: it.NormalizedValue,
			Data: map[string]any{
				"Change":    change,
				"ChangedAt": when,
				"ClientIP":  info.ip,
			},
		}); err != nil {
			s.logger.Warn("passkey-change notice enqueue failed", "error_kind", ErrorKind(err))
		}
	}
}
