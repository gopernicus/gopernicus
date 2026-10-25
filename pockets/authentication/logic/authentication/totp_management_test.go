package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestRemoveTOTPNeedsAAL2AndRevokesEverySession(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "remove@example.com")
	e, _ := h.enrollTOTP(t, sessionID, u.ID)
	pair, _, _ := h.svc.Login(ctx, "remove@example.com", passkeyPass)
	fresh := h.sessionOf(t, pair)
	if err := h.svc.RemoveTOTP(ctx, TOTPRemoveInput{SessionID: fresh, UserID: u.ID}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("an AAL1 removal = %v, want ErrStepUpRequired", err)
	}
	h.advance(totp.Period)
	result, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: fresh, UserID: u.ID, Code: h.code(t, e, 0)})
	if err != nil {
		t.Fatal(err)
	}
	before := h.mfaNotices()
	if err := h.svc.RemoveTOTP(ctx, TOTPRemoveInput{SessionID: h.sessionOf(t, result.Tokens), UserID: u.ID}); err != nil {
		t.Fatalf("RemoveTOTP: %v", err)
	}
	if h.sess.count() != 0 {
		t.Fatalf("sessions after removal = %d, want 0", h.sess.count())
	}
	if _, err := h.totp.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) || len(h.totp.codes[u.ID]) != 0 {
		t.Fatalf("the credential or codes survived: %v", err)
	}
	if rows := h.rows(securityevent.TypeTOTPRemoved); len(rows) != 1 || rows[0].UserID != u.ID {
		t.Fatalf("totp_removed = %+v", rows)
	}
	if h.mfaNotices() != before+1 {
		t.Fatal("no removal notice")
	}
	// The user re-enrolls from a fresh login at the bootstrap tier.
	pair, _, _ = h.svc.Login(ctx, "remove@example.com", passkeyPass)
	h.enrollTOTP(t, h.sessionOf(t, pair), u.ID)
	if row := h.lastRow(t, securityevent.TypeTOTPEnrolled); row.Details["tier"] != "bootstrap" {
		t.Fatalf("re-enrollment tier = %v", row.Details)
	}
}

func TestRegenerateRecoveryCodes(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, login := h.totpAccount(t, "regen@example.com")
	e, c := h.enrollTOTP(t, login, u.ID)
	if _, err := h.svc.RegenerateRecoveryCodes(ctx, RegenerateRecoveryCodesInput{SessionID: login, UserID: u.ID}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("an AAL1 regenerate = %v, want ErrStepUpRequired", err)
	}
	h.advance(totp.Period)
	stepped, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: login, UserID: u.ID, Code: h.code(t, e, 0)})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := h.sessionOf(t, stepped.Tokens)
	stale := c.RecoveryCodes.GeneratedAt.Add(-time.Hour)
	if _, err := h.svc.RegenerateRecoveryCodes(ctx, RegenerateRecoveryCodesInput{SessionID: sessionID, UserID: u.ID, ExpectedGeneratedAt: &stale}); !errors.Is(err, ErrRecoveryCodesStale) {
		t.Fatalf("a stale precondition = %v", err)
	}
	h.advance(2 * time.Second)
	current := c.RecoveryCodes.GeneratedAt
	set, err := h.svc.RegenerateRecoveryCodes(ctx, RegenerateRecoveryCodesInput{SessionID: sessionID, UserID: u.ID, ExpectedGeneratedAt: &current})
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes: %v", err)
	}
	if len(set.Codes) != 10 || !set.GeneratedAt.After(current) {
		t.Fatalf("set = %+v", set)
	}
	if row := h.lastRow(t, securityevent.TypeRecoveryCodesGenerated); row.Details["trigger"] != "regenerate" {
		t.Fatalf("recovery_codes_generated = %+v", row)
	}
	if _, err := h.svc.RegenerateRecoveryCodes(ctx, RegenerateRecoveryCodesInput{SessionID: sessionID, UserID: u.ID, ExpectedGeneratedAt: &current}); !errors.Is(err, ErrRecoveryCodesStale) {
		t.Fatalf("a replayed regenerate = %v, want stale", err)
	}
	if _, err := h.svc.StepUpWithRecoveryCode(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: c.RecoveryCodes.Codes[0]}); !errors.Is(err, ErrRecoveryCodeRejected) {
		t.Fatalf("an old code = %v", err)
	}
	if _, err := h.svc.StepUpWithRecoveryCode(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: set.Codes[0]}); err != nil {
		t.Fatalf("a new code = %v", err)
	}
}

func TestResetUserTOTP(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "reset@example.com")
	h.enrollTOTP(t, sessionID, u.ID)
	admin := Principal{Type: PrincipalUser, ID: "admin-1"}
	removed, err := h.svc.ResetUserTOTP(ctx, admin, u.ID)
	if err != nil || !removed {
		t.Fatalf("ResetUserTOTP = %v, %v", removed, err)
	}
	if h.sess.count() != 0 {
		t.Fatal("the reset left sessions")
	}
	row := h.lastRow(t, securityevent.TypeTOTPReset)
	if row.UserID != u.ID || row.Actor.ID != "admin-1" || row.Details["removed"] != true {
		t.Fatalf("totp_reset = %+v", row)
	}
	if removed, err := h.svc.ResetUserTOTP(ctx, admin, u.ID); err != nil || removed {
		t.Fatalf("a second reset = %v, %v", removed, err)
	}
}

// TestTOTPOptIn pins the host opt-in: without WithTOTP every TOTP method
// refuses, the inventory carries no TOTP fields, the passkey gate keeps its
// S4b behaviour, and erasure is told TOTP is off.
func TestTOTPOptIn(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "off@example.com")
	if h.svc.TOTPEnabled() {
		t.Fatal("TOTP enabled without WithTOTP")
	}
	if _, err := h.svc.BeginTOTPEnrollment(ctx, sessionID, u.ID); !errors.Is(err, ErrTOTPUnavailable) {
		t.Fatalf("Begin = %v", err)
	}
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: "123456"}); !errors.Is(err, ErrTOTPUnavailable) {
		t.Fatalf("StepUp = %v", err)
	}
	if _, err := h.svc.ResetUserTOTP(ctx, Principal{}, u.ID); !errors.Is(err, ErrTOTPUnavailable) {
		t.Fatalf("Reset = %v", err)
	}
	view, err := h.svc.Methods(ctx, u.ID)
	if err != nil || view.TOTP != nil || view.RecoveryCodes != nil {
		t.Fatalf("Methods = %+v, %v", view, err)
	}
	h.enroll(t, newPasskeyAuthenticator(true), sessionID, u.ID, "")
	if rows := h.rows(securityevent.TypeTOTPFailed); len(rows) != 0 {
		t.Fatalf("totp rows on a host without TOTP: %+v", rows)
	}
}

// fakeProbingTOTP is a TOTP store that implements totp.SchemaProber.
type fakeProbingTOTP struct {
	*fakeTOTP
	err    error
	probed int
}

func (f *fakeProbingTOTP) ProbeSchema(context.Context) error {
	f.probed++
	return f.err
}

// TestTOTPSchemaProbe pins the owner ruling (0026 conditional): a store's schema
// probe runs at construction only when TOTP is wired; a failing probe refuses
// construction naming the migration; an unwired host is never probed.
func TestTOTPSchemaProbe(t *testing.T) {
	h := newTOTPHarness(t)
	base := constructorConfig{
		TOTPConfig: TOTPConfig{Encrypter: h.ring, Issuer: "Example"}, AuthenticationGrants: h.grants,
		Protector: h.prot, CredentialMutations: h.creds,
	}
	missing := &fakeProbingTOTP{fakeTOTP: h.totp, err: errors.New("totp_credentials missing")}
	cfg := base
	cfg.TOTP = missing
	if err := validateTOTP(cfg); !errors.Is(err, ErrTOTPSchemaMissing) || missing.probed != 1 {
		t.Fatalf("missing schema = %v (probed %d)", err, missing.probed)
	}
	present := &fakeProbingTOTP{fakeTOTP: h.totp}
	cfg.TOTP = present
	if err := validateTOTP(cfg); err != nil || present.probed != 1 {
		t.Fatalf("present schema = %v (probed %d)", err, present.probed)
	}
	unwired := &fakeProbingTOTP{fakeTOTP: h.totp, err: errors.New("missing")}
	cfg = constructorConfig{TOTP: unwired}
	if err := validateTOTP(cfg); err != nil || unwired.probed != 0 {
		t.Fatalf("unwired = %v (probed %d)", err, unwired.probed)
	}
}
