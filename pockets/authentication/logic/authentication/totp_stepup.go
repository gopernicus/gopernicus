package authentication

import (
	"context"
	"errors"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
)

// The second-factor method kinds, as totp_failed and totp_step_up rows name them.
const (
	totpMethodTOTP         = session.MethodTOTP
	totpMethodRecoveryCode = session.MethodRecoveryCode
)

// StepUpCodeInput is a TOTP or recovery-code step-up from the caller's proven
// live session.
type StepUpCodeInput struct {
	SessionID string
	UserID    string
	Code      string
}

// StepUpWithTOTP steps the caller's live session up to AAL2 with a code from
// their confirmed authenticator app. In a fixed order: the second-factor budget
// (before any secret is opened), the owner (revision R) and the live session,
// the confirmed credential (else reason not_enrolled), its lockout, the secret,
// the code within ±1 step, a step newer than the last accepted one (else reason
// reused), and the use recorded with a compare-and-swap (a concurrent use loses
// with reason conflict). Success re-mints the session as a passkey step-up does
// — AuthenticatedAt now, totp added to its methods, AAL2, the replaced session's
// ExpiresAt — fenced on R, then deletes the replaced session and its grants.
// The re-minted session is a recent AAL2 login for every gate for the next five
// minutes. Every refusal is ErrTOTPRejected, or the lockout's
// SecondFactorLockedError; the fifth consecutive refusal locks for five minutes.
func (s *Service) StepUpWithTOTP(ctx context.Context, in StepUpCodeInput) (StepUpResult, error) {
	return s.secondFactorStepUp(ctx, in, totpMethodTOTP, ErrTOTPRejected, func(c totp.Credential, now time.Time) error {
		method := string(totpMethodTOTP)
		secret, err := totp.Open(s.totpEncrypter, in.UserID, c.Secret)
		if err != nil {
			s.recordTOTPFailure(ctx, in.UserID, method, securityevent.ReasonEnvelope)
			return ErrTOTPRejected
		}
		step, ok := totp.Validate(secret, in.Code, now, totpWindow)
		if !ok {
			return s.secondFactorFailure(ctx, in.UserID, method, securityevent.ReasonInvalid, ErrTOTPRejected)
		}
		if step <= c.LastUsedStep {
			return s.secondFactorFailure(ctx, in.UserID, method, securityevent.ReasonReused, ErrTOTPRejected)
		}
		if err := s.totp.RecordUse(ctx, in.UserID, c.LastUsedStep, step, now); err != nil {
			if errors.Is(err, sdk.ErrConflict) {
				s.recordTOTPFailure(ctx, in.UserID, method, securityevent.ReasonConflict)
				return ErrTOTPRejected
			}
			return err
		}
		return nil
	})
}

// StepUpWithRecoveryCode steps the caller's live session up to AAL2 with one of
// their recovery codes, which exist only beside a confirmed authenticator app.
// It follows StepUpWithTOTP's order; the code is normalized (case, hyphens and
// spaces), verified against every accepted pepper key and deleted — single use.
// Success also records recovery_code_used with the codes remaining and sends
// the mfa_change_notice. Every refusal is ErrRecoveryCodeRejected, or the
// lockout's SecondFactorLockedError.
func (s *Service) StepUpWithRecoveryCode(ctx context.Context, in StepUpCodeInput) (StepUpResult, error) {
	remaining := 0
	result, err := s.secondFactorStepUp(ctx, in, totpMethodRecoveryCode, ErrRecoveryCodeRejected, func(_ totp.Credential, now time.Time) error {
		method := string(totpMethodRecoveryCode)
		normalized, ok := totp.NormalizeRecoveryCode(in.Code)
		if !ok {
			return s.secondFactorFailure(ctx, in.UserID, method, securityevent.ReasonInvalid, ErrRecoveryCodeRejected)
		}
		candidates, err := s.protector.CandidateCodeDigests(in.UserID, challenge.PurposeRecoveryCode, normalized)
		if err != nil {
			return err
		}
		remaining, err = s.totp.ConsumeRecoveryCode(ctx, in.UserID, candidates, now)
		if errors.Is(err, sdk.ErrNotFound) {
			return s.secondFactorFailure(ctx, in.UserID, method, securityevent.ReasonInvalid, ErrRecoveryCodeRejected)
		}
		return err
	})
	if err != nil {
		return StepUpResult{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID, Type: securityevent.TypeRecoveryCodeUsed, Status: securityevent.StatusSuccess,
		Details: map[string]any{"remaining": remaining},
	})
	s.enqueueMFAChangeNotices(ctx, in.UserID, mfaChangeRecoveryCodeUsed)
	return result, nil
}

// secondFactorStepUp is the shared step-up path: verify proves the code against
// the confirmed credential and records its use, then the session is re-minted
// at AAL2 with method added.
func (s *Service) secondFactorStepUp(ctx context.Context, in StepUpCodeInput, method session.MethodKind, rejected error, verify func(totp.Credential, time.Time) error) (StepUpResult, error) {
	if err := s.requireTOTP(); err != nil {
		return StepUpResult{}, err
	}
	if err := s.secondFactorBudget(ctx, in.UserID); err != nil {
		return StepUpResult{}, err
	}
	owner, err := s.users.Get(ctx, in.UserID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return StepUpResult{}, ErrStepUpRequired
		}
		return StepUpResult{}, err
	}
	replaced, err := s.liveStepUpSession(ctx, in.SessionID, in.UserID, s.now())
	if err != nil {
		return StepUpResult{}, err
	}
	c, err := s.totp.Get(ctx, in.UserID)
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return StepUpResult{}, err
	}
	if err != nil || !c.Confirmed() {
		s.recordTOTPFailure(ctx, in.UserID, string(method), securityevent.ReasonNotEnrolled)
		return StepUpResult{}, rejected
	}
	now := s.now().UTC()
	if c.Locked(now) {
		s.recordTOTPFailure(ctx, in.UserID, string(method), securityevent.ReasonLocked)
		return StepUpResult{}, &SecondFactorLockedError{Until: *c.LockedUntil, Wait: c.LockedUntil.Sub(now)}
	}
	if err := verify(c, now); err != nil {
		return StepUpResult{}, err
	}
	auth := session.AuthenticationMetadata{AuthenticatedAt: now, Assurance: session.AssuranceAAL2}
	if d, ok := session.DescribeMethod(method); ok {
		auth.Methods = []session.AuthenticationMethod{d}
	}
	auth.Methods = mergeMethods(replaced.Authentication.Methods, auth)
	pair, created, err := s.mintSessionUntil(ctx, owner.ID, owner.AuthRevision, auth, replaced.ExpiresAt)
	if err != nil {
		if reason, ok := mintFailureReason(err); ok {
			s.recordTOTPFailure(ctx, in.UserID, string(method), reason)
			return StepUpResult{}, rejected
		}
		return StepUpResult{}, err
	}
	if err := s.sessions.Delete(ctx, replaced.ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return StepUpResult{}, err
	}
	if s.authGrants != nil {
		if err := s.authGrants.DeleteBySession(ctx, replaced.ID); err != nil {
			s.logger.Warn("delete session grants failed", "error_kind", ErrorKind(err))
		}
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID, Type: securityevent.TypeTOTPStepUp, Status: securityevent.StatusSuccess,
		Details: map[string]any{"method": string(method), "assurance": string(created.Authentication.Assurance)},
	})
	return StepUpResult{Tokens: pair, User: owner, Assurance: created.Authentication.Assurance}, nil
}
