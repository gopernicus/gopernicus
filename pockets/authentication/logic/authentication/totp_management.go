package authentication

import (
	"context"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// totpResetAttempts bounds ResetUserTOTP's retries of a lost revision race.
const totpResetAttempts = 3

// The Change values an mfa_change_notice renders.
const (
	mfaChangeTOTPEnrolled             = "totp_enrolled"
	mfaChangeTOTPRemoved              = "totp_removed"
	mfaChangeTOTPReset                = "totp_reset"
	mfaChangeRecoveryCodesRegenerated = "recovery_codes_regenerated"
	mfaChangeRecoveryCodeUsed         = "recovery_code_used"
)

// TOTPStatus is the caller's second-factor state: whether an authenticator app
// is confirmed or pending, when it was confirmed and last used, and how many
// recovery codes remain (with when the set was generated). It carries no
// secret, code or digest.
type TOTPStatus struct {
	Enrolled                 bool
	Pending                  bool
	EnrolledAt               *time.Time
	LastUsedAt               *time.Time
	RecoveryCodesRemaining   int
	RecoveryCodesGeneratedAt *time.Time
}

// TOTPRemoveInput removes the caller's authenticator app from their proven live
// session.
type TOTPRemoveInput struct {
	SessionID string
	UserID    string
}

// RegenerateRecoveryCodesInput replaces the caller's recovery codes.
// ExpectedGeneratedAt, when set, is the generation the caller last read: a
// newer set refuses with ErrRecoveryCodesStale, so a re-POSTed form never
// silently replaces codes the user just saved.
type RegenerateRecoveryCodesInput struct {
	SessionID           string
	UserID              string
	ExpectedGeneratedAt *time.Time
}

// TOTPStatus reports userID's second-factor state.
func (s *Service) TOTPStatus(ctx context.Context, userID string) (TOTPStatus, error) {
	if err := s.requireTOTP(); err != nil {
		return TOTPStatus{}, err
	}
	c, err := s.totp.Get(ctx, userID)
	switch {
	case errors.Is(err, sdk.ErrNotFound):
		return TOTPStatus{}, nil
	case err != nil:
		return TOTPStatus{}, err
	}
	if !c.Confirmed() {
		pending := c.ExpiresAt != nil && s.now().Before(*c.ExpiresAt)
		return TOTPStatus{Pending: pending}, nil
	}
	remaining, generated, err := s.totp.CountRecoveryCodes(ctx, userID)
	if err != nil {
		return TOTPStatus{}, err
	}
	return TOTPStatus{Enrolled: true, EnrolledAt: c.ConfirmedAt, LastUsedAt: c.LastUsedAt, RecoveryCodesRemaining: remaining, RecoveryCodesGeneratedAt: generated}, nil
}

// RemoveTOTP removes the caller's authenticator app — confirmed or pending — and
// every recovery code, and revokes every session of the user, the caller's
// included. It consumes a remove_totp grant under the second-factor gate's
// policy (AAL2 while the user holds an AAL2 factor, which a confirmed app is),
// and the removal is fenced on the revision read first: a concurrent credential
// change is ErrStepUpRequired. No app at all is sdk.ErrNotFound. Success records
// totp_removed and sends the mfa_change_notice.
func (s *Service) RemoveTOTP(ctx context.Context, in TOTPRemoveInput) error {
	if err := s.requireTOTP(); err != nil {
		return err
	}
	owner, err := s.users.Get(ctx, in.UserID)
	if err != nil {
		return err
	}
	if _, err := s.totp.Get(ctx, in.UserID); err != nil {
		return err
	}
	policy, _, err := s.secondFactorPolicy(ctx, in.UserID)
	if err != nil {
		return err
	}
	if _, err := s.RequireRecentAuthentication(ctx, in.SessionID, in.UserID, authgrant.PurposeRemoveTOTP, "", policy); err != nil {
		return err
	}
	removed, err := s.totp.Remove(ctx, in.UserID, owner.AuthRevision)
	if err != nil {
		if errors.Is(err, sdk.ErrConflict) {
			return ErrStepUpRequired
		}
		return err
	}
	if !removed.Credential {
		return sdk.ErrNotFound
	}
	s.recordSecurityEvent(ctx, securityEventInput{UserID: in.UserID, Type: securityevent.TypeTOTPRemoved, Status: securityevent.StatusSuccess})
	s.enqueueMFAChangeNotices(ctx, in.UserID, mfaChangeTOTPRemoved)
	return nil
}

// RegenerateRecoveryCodes replaces the caller's recovery codes with ten new ones
// — the documented step before retiring a challenge-pepper key. It needs a
// confirmed authenticator app (else sdk.ErrNotFound) and a
// regenerate_recovery_codes grant under the second-factor gate's policy. A set
// newer than in.ExpectedGeneratedAt is ErrRecoveryCodesStale, checked before the
// grant is spent and again atomically on write. Success records
// recovery_codes_generated (trigger regenerate) and sends the mfa_change_notice;
// the codes are returned once.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, in RegenerateRecoveryCodesInput) (RecoveryCodeSet, error) {
	if err := s.requireTOTP(); err != nil {
		return RecoveryCodeSet{}, err
	}
	c, err := s.totp.Get(ctx, in.UserID)
	if err != nil {
		return RecoveryCodeSet{}, err
	}
	if !c.Confirmed() {
		return RecoveryCodeSet{}, sdk.ErrNotFound
	}
	if in.ExpectedGeneratedAt != nil {
		_, current, err := s.totp.CountRecoveryCodes(ctx, in.UserID)
		if err != nil {
			return RecoveryCodeSet{}, err
		}
		if current == nil || !current.Equal(*in.ExpectedGeneratedAt) {
			return RecoveryCodeSet{}, ErrRecoveryCodesStale
		}
	}
	policy, _, err := s.secondFactorPolicy(ctx, in.UserID)
	if err != nil {
		return RecoveryCodeSet{}, err
	}
	if _, err := s.RequireRecentAuthentication(ctx, in.SessionID, in.UserID, authgrant.PurposeRegenerateRecoveryCodes, "", policy); err != nil {
		return RecoveryCodeSet{}, err
	}
	set, rows, err := s.mintRecoveryCodes(in.UserID, s.now())
	if err != nil {
		return RecoveryCodeSet{}, err
	}
	if err := s.totp.ReplaceRecoveryCodes(ctx, in.UserID, rows, in.ExpectedGeneratedAt); err != nil {
		if errors.Is(err, sdk.ErrConflict) {
			return RecoveryCodeSet{}, ErrRecoveryCodesStale
		}
		return RecoveryCodeSet{}, err
	}
	s.recordRecoveryCodesGenerated(ctx, in.UserID, len(set.Codes), recoveryTriggerRegenerate)
	s.enqueueMFAChangeNotices(ctx, in.UserID, mfaChangeRecoveryCodesRegenerated)
	return set, nil
}

// ResetUserTOTP removes userID's authenticator app and recovery codes and
// revokes their sessions: the operator path for a user who lost their phone and
// their codes, after which they enroll again from a primary login. It reports
// whether there was anything to remove.
//
// TRUSTED: it applies NO authorization. The bundled admin route runs the host's
// administration policy (and the admins MFA gate) first; it bypasses the
// second-factor gate (break-glass). Records totp_reset, attributed to actor, and
// sends the mfa_change_notice when something was removed.
func (s *Service) ResetUserTOTP(ctx context.Context, actor Principal, userID string) (bool, error) {
	if err := s.requireTOTP(); err != nil {
		return false, err
	}
	var removed totp.Removed
	var err error
	for range totpResetAttempts {
		owner, getErr := s.users.Get(ctx, userID)
		if getErr != nil {
			return false, getErr
		}
		if removed, err = s.totp.Remove(ctx, userID, owner.AuthRevision); err == nil || !errors.Is(err, sdk.ErrConflict) {
			break
		}
	}
	if err != nil {
		return false, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Actor:   securityevent.Principal{Type: actor.Type, ID: actor.ID},
		Type:    securityevent.TypeTOTPReset,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"removed": removed.Credential},
	})
	if removed.Credential {
		s.enqueueMFAChangeNotices(ctx, userID, mfaChangeTOTPReset)
	}
	return removed.Credential, nil
}

// enqueueMFAChangeNotices sends the mfa_change_notice to each of the user's
// verified contact channels — the identifier-change notice path. It is
// best-effort over the durable outbox: the change has committed, so a failed
// enqueue is a WARN, never a rollback. The notice names the change, never a
// secret or code.
func (s *Service) enqueueMFAChangeNotices(ctx context.Context, userID, change string) {
	if s.queue == nil || s.deliver == nil {
		return
	}
	info := clientInfoFromContext(ctx)
	when := s.now().UTC().Format(time.RFC3339)
	for _, it := range s.verifiedContactChannels(ctx, userID, "") {
		if !s.deliver.Supports(string(it.Kind)) {
			continue
		}
		key := s.idempotencyKey(string(it.Kind), it.NormalizedValue, delivery.PurposeMFAChangeNotice) + ":" + change + ":" + when
		if err := s.enqueueRendered(ctx, delivery.PurposeMFAChangeNotice, key, delivery.Request{
			Kind:            string(it.Kind),
			Purpose:         delivery.PurposeMFAChangeNotice,
			Destination:     it.NormalizedValue,
			ResolutionInput: it.NormalizedValue,
			Data: map[string]any{
				"Change":    change,
				"ChangedAt": when,
				"ClientIP":  info.ip,
			},
		}); err != nil {
			s.logger.Warn("mfa-change notice enqueue failed", "error_kind", ErrorKind(err))
		}
	}
}
