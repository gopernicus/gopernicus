package authentication

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

func TestTOTPStepUpReachesAAL2(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, pair, sessionID := h.totpAccount(t, "step@example.com")
	e, _ := h.enrollTOTP(t, sessionID, u.ID)
	before, _ := h.sess.Get(ctx, sessionID)
	if h.assurance(t, pair) != session.AssuranceAAL1 {
		t.Fatal("the login was not AAL1")
	}

	h.advance(totp.Period)
	result, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, e, 0)})
	if err != nil {
		t.Fatalf("StepUpWithTOTP: %v", err)
	}
	if result.Assurance != session.AssuranceAAL2 || h.assurance(t, result.Tokens) != session.AssuranceAAL2 {
		t.Fatalf("assurance = %s", result.Assurance)
	}
	_, cred, _ := h.svc.verifyAccessClaims(result.Tokens.AccessToken)
	if !slices.Equal(cred.Methods, []session.MethodKind{session.MethodPassword, session.MethodTOTP}) {
		t.Fatalf("methods = %v, want [password totp]", cred.Methods)
	}
	if _, err := h.sess.Get(ctx, sessionID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("the replaced session survived: %v", err)
	}
	created, err := h.sess.Get(ctx, cred.SessionID)
	if err != nil || !created.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("re-minted ExpiresAt = %v, want %v (%v)", created.ExpiresAt, before.ExpiresAt, err)
	}
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err == nil {
		t.Fatal("the replaced refresh token still rotates")
	}
	if row := h.lastRow(t, securityevent.TypeTOTPStepUp); row.Details["method"] != "totp" || row.Details["assurance"] != "aal2" {
		t.Fatalf("totp_step_up = %+v", row)
	}
	// The re-minted session is a recent AAL2 login: the AAL2-gated removal passes
	// on the shortcut.
	if err := h.svc.RemoveTOTP(ctx, TOTPRemoveInput{SessionID: cred.SessionID, UserID: u.ID}); err != nil {
		t.Fatalf("RemoveTOTP after a step-up: %v", err)
	}
}

func TestTOTPLockout(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "lock@example.com")
	e, _ := h.enrollTOTP(t, sessionID, u.ID)
	h.advance(totp.Period)
	wrong := "000000"
	if wrong == h.code(t, e, 0) || wrong == h.code(t, e, 1) || wrong == h.code(t, e, -1) {
		wrong = "111111"
	}
	for i := 1; i < totpLockAttempts; i++ {
		if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: wrong}); !errors.Is(err, ErrTOTPRejected) {
			t.Fatalf("wrong code %d = %v", i, err)
		}
	}
	_, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: wrong})
	var locked *SecondFactorLockedError
	if !errors.As(err, &locked) || !errors.Is(err, ErrTooManyAttempts) || locked.Until.Sub(h.now()) > totpLockFor || locked.Until.Sub(h.now()) < totpLockFor-time.Second {
		t.Fatalf("fifth wrong code = %v, want the five-minute lockout", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPLocked); row.Details["attempts"] != 5 || row.Details["locked_for_seconds"] != 300 || row.EventStatus != securityevent.StatusBlocked {
		t.Fatalf("totp_locked = %+v", row)
	}
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, e, 0)}); !errors.As(err, &locked) {
		t.Fatalf("a correct code while locked = %v, want the lockout", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPFailed); row.Details["reason"] != "locked" {
		t.Fatalf("totp_failed = %+v, want locked", row)
	}
	h.advance(totpLockFor + totp.Period)
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, e, 0)}); err != nil {
		t.Fatalf("a correct code after the lockout = %v", err)
	}
	if c, _ := h.totp.Get(ctx, u.ID); c.FailedAttempts != 0 || c.LockedUntil != nil {
		t.Fatalf("success left failures %+v", c)
	}
}

func TestTOTPStepUpRefusals(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "refuse@example.com")
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: "123456"}); !errors.Is(err, ErrTOTPRejected) {
		t.Fatalf("not enrolled = %v", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPFailed); row.Details["reason"] != "not_enrolled" {
		t.Fatalf("totp_failed = %+v, want not_enrolled", row)
	}
	if _, err := h.svc.StepUpWithRecoveryCode(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: "abcde-fghjk"}); !errors.Is(err, ErrRecoveryCodeRejected) {
		t.Fatalf("a recovery code with no confirmed app = %v (BE5)", err)
	}
	// A pending enrollment is not a second factor either.
	h.begin(t, sessionID, u.ID)
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: "123456"}); !errors.Is(err, ErrTOTPRejected) {
		t.Fatalf("pending = %v", err)
	}
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: "no-such", UserID: u.ID, Code: "123456"}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a dead session = %v", err)
	}
}

// TestTOTPStepUpConcurrentUseOneWins pins the RecordUse compare-and-swap: two
// step-ups with one code — exactly one re-mints.
func TestTOTPStepUpConcurrentUseOneWins(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, _ := h.totpAccount(t, "race@example.com")
	pair, _, _ := h.svc.Login(ctx, "race@example.com", passkeyPass)
	sessionID := h.sessionOf(t, pair)
	e, _ := h.enrollTOTP(t, sessionID, u.ID)
	other, _, _ := h.svc.Login(ctx, "race@example.com", passkeyPass)
	sessions := []string{sessionID, h.sessionOf(t, other)}
	h.advance(totp.Period)
	code := h.code(t, e, 0)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessions[i], UserID: u.ID, Code: code})
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("errs = %v, want exactly one winner", errs)
	}
}

func TestRecoveryCodeStepUp(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "recover@example.com")
	_, c := h.enrollTOTP(t, sessionID, u.ID)
	before := h.mfaNotices()
	code := strings.ToUpper(c.RecoveryCodes.Codes[3])
	result, err := h.svc.StepUpWithRecoveryCode(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: code})
	if err != nil {
		t.Fatalf("StepUpWithRecoveryCode: %v", err)
	}
	_, cred, _ := h.svc.verifyAccessClaims(result.Tokens.AccessToken)
	if result.Assurance != session.AssuranceAAL2 || !slices.Equal(cred.Methods, []session.MethodKind{session.MethodPassword, session.MethodRecoveryCode}) {
		t.Fatalf("recovery step-up = %s %v", result.Assurance, cred.Methods)
	}
	if row := h.lastRow(t, securityevent.TypeRecoveryCodeUsed); row.Details["remaining"] != 9 || len(row.Details) != 1 {
		t.Fatalf("recovery_code_used = %+v", row)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPStepUp); row.Details["method"] != "recovery_code" {
		t.Fatalf("totp_step_up = %+v", row)
	}
	if h.mfaNotices() != before+1 {
		t.Fatal("no notice for the recovery-code use")
	}
	if _, err := h.svc.StepUpWithRecoveryCode(ctx, StepUpCodeInput{SessionID: cred.SessionID, UserID: u.ID, Code: code}); !errors.Is(err, ErrRecoveryCodeRejected) {
		t.Fatalf("a spent code = %v", err)
	}
	if status, _ := h.svc.TOTPStatus(ctx, u.ID); status.RecoveryCodesRemaining != 9 {
		t.Fatalf("status = %+v", status)
	}
}

// TestSecondFactorBudget pins the per-user budget, checked before any secret is
// opened.
func TestSecondFactorBudget(t *testing.T) {
	h := newTOTPHarness(t)
	h.svc.authenticationLimits.SecondFactor = RequestBudget{PerSubject: ratelimiter.PerMinute(2), PerIP: ratelimiter.PerMinute(100)}
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "budget@example.com")
	for range 2 {
		_, _ = h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: "000000"})
	}
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: "000000"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over budget = %v, want ErrRateLimited", err)
	}
}

// TestMergeMethodsNeverLowersAtAAL2 pins the BE6 amendment: at AAL2 nothing
// carried is dropped; below it every AAL2-contributing descriptor is.
func TestMergeMethodsNeverLowersAtAAL2(t *testing.T) {
	pk, _ := session.DescribeMethod(session.MethodPasskey)
	code, _ := session.DescribeMethod(session.MethodTOTP)
	pw, _ := session.DescribeMethod(session.MethodPassword)
	weak, _ := session.DescribeMethod(session.MethodPasskeyUnverified)
	kinds := func(ms []session.AuthenticationMethod) []session.MethodKind {
		var out []session.MethodKind
		for _, m := range ms {
			out = append(out, m.Kind)
		}
		return out
	}
	got := mergeMethods([]session.AuthenticationMethod{pw, pk}, session.AuthenticationMetadata{Assurance: session.AssuranceAAL2, Methods: []session.AuthenticationMethod{code}})
	if !slices.Equal(kinds(got), []session.MethodKind{session.MethodPassword, session.MethodPasskey, session.MethodTOTP}) {
		t.Fatalf("AAL2 merge = %v", kinds(got))
	}
	got = mergeMethods([]session.AuthenticationMethod{pw, pk, code}, session.AuthenticationMetadata{Assurance: session.AssuranceAAL1, Methods: []session.AuthenticationMethod{weak}})
	if !slices.Equal(kinds(got), []session.MethodKind{session.MethodPassword, session.MethodPasskeyUnverified}) {
		t.Fatalf("AAL1 merge = %v", kinds(got))
	}
}
