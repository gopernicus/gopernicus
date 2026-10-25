package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
)

func TestTOTPEnrollmentBootstrapsFromAFreshLogin(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "enroll@example.com")

	first := h.begin(t, sessionID, u.ID)
	if len(first.Secret) != 32 || first.Issuer != "Example" || first.Account != "enroll@example.com" || !first.ExpiresAt.After(h.now()) {
		t.Fatalf("enrollment = %+v", first)
	}
	parsed, err := url.Parse(first.KeyURI)
	if err != nil || parsed.Scheme != "otpauth" || parsed.Query().Get("secret") != first.Secret {
		t.Fatalf("KeyURI = %q", first.KeyURI)
	}
	stored, _ := h.totp.Get(ctx, u.ID)
	if strings.Contains(stored.Secret, first.Secret) || stored.Tier != passkeyTierBootstrap || stored.SessionID != sessionID {
		t.Fatalf("pending row = %+v", stored)
	}
	pending, err := h.svc.PendingTOTPEnrollment(ctx, sessionID, u.ID)
	if err != nil || pending.Secret != first.Secret || pending.KeyURI != first.KeyURI {
		t.Fatalf("PendingTOTPEnrollment = %+v, %v", pending, err)
	}

	// A re-Begin replaces the pending row: the first secret no longer confirms.
	second := h.begin(t, sessionID, u.ID)
	if second.Secret == first.Secret {
		t.Fatal("a re-Begin kept the secret")
	}
	if _, err := h.svc.ConfirmTOTPEnrollment(ctx, TOTPConfirmInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, first, 0)}); !errors.Is(err, ErrTOTPRejected) {
		t.Fatalf("the replaced secret's code = %v, want ErrTOTPRejected", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPFailed); row.Details["reason"] != "invalid" || row.Details["method"] != "totp" || row.UserID != u.ID {
		t.Fatalf("totp_failed = %+v", row)
	}
	if again, err := h.svc.PendingTOTPEnrollment(ctx, sessionID, u.ID); err != nil || again.Secret != second.Secret {
		t.Fatalf("a wrong code dropped the pending row: %+v, %v", again, err)
	}

	confirmed, err := h.svc.ConfirmTOTPEnrollment(ctx, TOTPConfirmInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, second, 0)})
	if err != nil {
		t.Fatalf("ConfirmTOTPEnrollment: %v", err)
	}
	if len(confirmed.RecoveryCodes.Codes) != 10 || confirmed.RecoveryCodes.GeneratedAt.IsZero() {
		t.Fatalf("confirmation = %+v", confirmed)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPEnrolled); row.Details["tier"] != "bootstrap" || len(row.Details) != 1 {
		t.Fatalf("totp_enrolled = %+v", row)
	}
	if row := h.lastRow(t, securityevent.TypeRecoveryCodesGenerated); row.Details["count"] != 10 || row.Details["trigger"] != "enrollment" {
		t.Fatalf("recovery_codes_generated = %+v", row)
	}
	if n := h.mfaNotices(); n != 1 {
		t.Fatalf("mfa_change_notice mails = %d, want 1", n)
	}
	got, _ := h.totp.Get(ctx, u.ID)
	if !got.Confirmed() || got.LastUsedStep != totp.Step(h.now()) || got.SessionID != "" || got.ExpiresAt != nil {
		t.Fatalf("confirmed row = %+v", got)
	}
	if owner, _ := h.users.Get(ctx, u.ID); owner.AuthRevision == stored.AuthRevision {
		t.Fatal("Confirm did not advance auth_revision")
	}
	// The confirmed step never steps up afterwards (BE10).
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, second, 0)}); !errors.Is(err, ErrTOTPRejected) {
		t.Fatalf("the confirmed code stepped up: %v", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPFailed); row.Details["reason"] != "reused" {
		t.Fatalf("totp_failed = %+v, want reused", row)
	}
	// A fresh login cannot enroll again: the confirmed app answers first.
	if _, err := h.svc.BeginTOTPEnrollment(ctx, sessionID, u.ID); !errors.Is(err, ErrTOTPExists) {
		t.Fatalf("a second enrollment = %v, want ErrTOTPExists", err)
	}
	// No row carries the secret, a code or a digest.
	for _, evt := range h.events.recorded() {
		for k, v := range evt.Details {
			s, _ := v.(string)
			if s != "" && (strings.Contains(s, second.Secret) || strings.Contains(s, confirmed.RecoveryCodes.Codes[0])) {
				t.Fatalf("%s Details[%q] carries a secret", evt.EventType, k)
			}
		}
	}
}

func TestTOTPEnrollmentGateAndExpiry(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "gate@example.com")

	h.advance(6 * time.Minute)
	if _, err := h.svc.BeginTOTPEnrollment(ctx, sessionID, u.ID); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a stale login = %v, want ErrStepUpRequired", err)
	}
	if _, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{SessionID: sessionID, UserID: u.ID, Purpose: authgrant.PurposeRegisterTOTP}, passkeyPass); err != nil {
		t.Fatalf("password step-up: %v", err)
	}
	e := h.begin(t, sessionID, u.ID)

	_, other, otherSession := h.totpAccount(t, "other@example.com")
	_ = other
	if _, err := h.svc.ConfirmTOTPEnrollment(ctx, TOTPConfirmInput{SessionID: otherSession, UserID: u.ID, Code: h.code(t, e, 0)}); !errors.Is(err, ErrTOTPEnrollmentExpired) {
		t.Fatalf("another session's confirm = %v, want ErrTOTPEnrollmentExpired", err)
	}
	h.advance(11 * time.Minute)
	if _, err := h.svc.ConfirmTOTPEnrollment(ctx, TOTPConfirmInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, e, 0)}); !errors.Is(err, ErrTOTPEnrollmentExpired) {
		t.Fatalf("a lapsed enrollment = %v, want ErrTOTPEnrollmentExpired", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPFailed); row.Details["reason"] != "expired" {
		t.Fatalf("totp_failed = %+v, want expired", row)
	}
	if status, _ := h.svc.TOTPStatus(ctx, u.ID); status.Pending || status.Enrolled {
		t.Fatalf("a lapsed enrollment reads as %+v", status)
	}
}

// TestTOTPConfirmRefusesAMovedRevisionOrAClosedBootstrap pins Confirm's
// re-checks: a credential change after Begin, or an AAL2 passkey registered
// after a bootstrap-tier Begin, is ErrStepUpRequired.
func TestTOTPConfirmRefusesAMovedRevisionOrAClosedBootstrap(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "fence@example.com")
	e := h.begin(t, sessionID, u.ID)
	h.enroll(t, newPasskeyAuthenticator(true), sessionID, u.ID, "")
	if _, err := h.svc.ConfirmTOTPEnrollment(ctx, TOTPConfirmInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, e, 0)}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a bootstrap confirm beside a new AAL2 passkey = %v, want ErrStepUpRequired", err)
	}

	v, _, vSession := h.totpAccount(t, "revision@example.com")
	ev := h.begin(t, vSession, v.ID)
	h.users.mu.Lock()
	owner := h.users.byID[v.ID]
	owner.AuthRevision++
	h.users.byID[v.ID] = owner
	h.users.mu.Unlock()
	if _, err := h.svc.ConfirmTOTPEnrollment(ctx, TOTPConfirmInput{SessionID: vSession, UserID: v.ID, Code: h.code(t, ev, 0)}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a moved revision = %v, want ErrStepUpRequired", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPFailed); row.Details["reason"] != "conflict" {
		t.Fatalf("totp_failed = %+v, want conflict", row)
	}
}

// TestSecondFactorPredicateGatesBoth pins BE2: one predicate on both gates. A
// TOTP holder adds a passkey only at AAL2, and a holder of a user-verified
// passkey enrolls TOTP only at AAL2.
func TestSecondFactorPredicateGatesBoth(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "holder@example.com")
	e, _ := h.enrollTOTP(t, sessionID, u.ID)

	pair, _, err := h.svc.Login(ctx, "holder@example.com", passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	fresh := h.sessionOf(t, pair)
	if _, err := h.svc.BeginPasskeyRegistration(ctx, fresh, u.ID); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a TOTP holder's passkey at AAL1 = %v, want ErrStepUpRequired", err)
	}
	h.advance(totp.Period)
	result, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: fresh, UserID: u.ID, Code: h.code(t, e, 0)})
	if err != nil {
		t.Fatalf("StepUpWithTOTP: %v", err)
	}
	stepped := h.sessionOf(t, result.Tokens)
	ceremony, err := h.svc.BeginPasskeyRegistration(ctx, stepped, u.ID)
	if err != nil {
		t.Fatalf("a TOTP holder's passkey at AAL2 = %v", err)
	}
	if ceremony.Ceremony == "" {
		t.Fatal("no ceremony")
	}

	v, _, vSession := h.totpAccount(t, "passkey-holder@example.com")
	h.enroll(t, newPasskeyAuthenticator(true), vSession, v.ID, "")
	pair, _, err = h.svc.Login(ctx, "passkey-holder@example.com", passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.BeginTOTPEnrollment(ctx, h.sessionOf(t, pair), v.ID); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a passkey holder's TOTP at AAL1 = %v, want ErrStepUpRequired", err)
	}
}

// TestRecoveryCodeKeyWarn pins BE4's boot WARN: recovery codes stored under a
// key the protector no longer accepts are named once.
func TestRecoveryCodeKeyWarn(t *testing.T) {
	h := newTOTPHarness(t)
	h.totp.codes["u1"] = []totp.RecoveryCode{{KeyID: "k1"}, {KeyID: "retired-7"}, {KeyID: "retired-2"}}
	var buf bytes.Buffer
	h.svc.logger = slog.New(slog.NewTextHandler(&buf, nil))
	h.svc.warnOrphanedRecoveryCodes(context.Background())
	out := buf.String()
	if !strings.Contains(out, "auth: recovery codes sealed under retired pepper keys") || !strings.Contains(out, "key_ids=\"[retired-2 retired-7]\"") || strings.Contains(out, "k1") {
		t.Fatalf("WARN = %q", out)
	}
	buf.Reset()
	h.totp.codes["u1"] = []totp.RecoveryCode{{KeyID: "k1"}}
	h.svc.warnOrphanedRecoveryCodes(context.Background())
	if buf.Len() != 0 {
		t.Fatalf("a live key warned: %q", buf.String())
	}
}

// TestTOTPEnvelopeBoundToItsUser pins BE7: another user's sealed secret copied
// onto a row never opens.
func TestTOTPEnvelopeBoundToItsUser(t *testing.T) {
	h := newTOTPHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.totpAccount(t, "victim@example.com")
	v, _, vSession := h.totpAccount(t, "thief@example.com")
	e, _ := h.enrollTOTP(t, sessionID, u.ID)
	h.enrollTOTP(t, vSession, v.ID)
	h.totp.mu.Lock()
	thief := h.totp.creds[v.ID]
	thief.Secret = h.totp.creds[u.ID].Secret
	h.totp.creds[v.ID] = thief
	h.totp.mu.Unlock()
	h.advance(totp.Period)
	if _, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: vSession, UserID: v.ID, Code: h.code(t, e, 0)}); !errors.Is(err, ErrTOTPRejected) {
		t.Fatalf("a copied envelope = %v, want ErrTOTPRejected", err)
	}
	if row := h.lastRow(t, securityevent.TypeTOTPFailed); row.Details["reason"] != "envelope" {
		t.Fatalf("totp_failed = %+v, want envelope", row)
	}
}

// TestRecoveryCodesDigestedUnderThePepper pins the stored form: the protector's
// digest of the normalized code under the recovery_code purpose and its key id,
// never the code.
func TestRecoveryCodesDigestedUnderThePepper(t *testing.T) {
	h := newTOTPHarness(t)
	u, _, sessionID := h.totpAccount(t, "digest@example.com")
	_, c := h.enrollTOTP(t, sessionID, u.ID)
	normalized, _ := totp.NormalizeRecoveryCode(c.RecoveryCodes.Codes[0])
	want, _ := h.prot.DigestCode("k1", u.ID, challenge.PurposeRecoveryCode, normalized)
	found := false
	for _, rc := range h.totp.codes[u.ID] {
		found = found || (rc.Digest == want && rc.KeyID == "k1")
		if strings.Contains(rc.Digest, c.RecoveryCodes.Codes[0]) {
			t.Fatal("a stored recovery code carries its display form")
		}
	}
	if !found || len(h.totp.codes[u.ID]) != 10 {
		t.Fatalf("stored codes = %+v", h.totp.codes[u.ID])
	}
}
