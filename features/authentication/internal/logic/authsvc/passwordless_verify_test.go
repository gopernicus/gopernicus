package authsvc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
)

// startPasswordlessCode drives a code start through the synchronous-draining
// harness queue so the worker issues the login_otp and delivers the plaintext code,
// then recovers it from the transport (the digest at rest is not reversible).
func startPasswordlessCode(t *testing.T, h *harness, kind, addr, normalized string) string {
	t.Helper()
	h.mailer.sent = nil
	if h.phone != nil {
		h.phone.sent = nil
	}
	if err := h.svc.StartPasswordless(context.Background(), kind, addr, "code"); err != nil {
		t.Fatalf("StartPasswordless(code): %v", err)
	}
	if kind == string(identifier.KindPhone) {
		return h.phone.codeFor(t, normalized)
	}
	return extractCode(t, h.mailer.last().Text)
}

// TestVerifyPasswordlessSuccessMintsSession proves the OTP happy path mints a live
// session through mintSession (a usable access/refresh pair), stamps the email_code
// method, and consumes the challenge — and that the minted refresh token rotates
// through the standard refresh path afterward (design §4.1/§4.3).
func TestVerifyPasswordlessSuccessMintsSession(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "otp@example.com", "password123456789")
	h.mustVerify(t, "otp@example.com")
	enablePasswordless(h, string(identifier.KindEmail))

	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "OTP@Example.com", "otp@example.com")
	pair, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "OTP@Example.com", code)
	if err != nil {
		t.Fatalf("VerifyPasswordless: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("minted pair incomplete: access=%q refresh=%q", pair.AccessToken, pair.RefreshToken)
	}
	if n := h.loginChallengeCount(challenge.PurposeLoginOTP); n != 0 {
		t.Errorf("login_otp challenge count after verify = %d, want 0 (consumed)", n)
	}
	// The session records how the primary authentication happened (email_code).
	h.sess.mu.Lock()
	var got session.Session
	for _, s := range h.sess.m {
		if s.UserID == u.ID {
			got = s
		}
	}
	h.sess.mu.Unlock()
	if len(got.Authentication.Methods) != 1 || got.Authentication.Methods[0].Kind != session.MethodEmailCode {
		t.Errorf("session methods = %+v, want single email_code", got.Authentication.Methods)
	}
	// The minted refresh token rotates through the standard refresh path.
	rotated, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh after passwordless login: %v", err)
	}
	if rotated.RefreshToken == "" || rotated.RefreshToken == pair.RefreshToken {
		t.Errorf("refresh did not rotate the token: old=%q new=%q", pair.RefreshToken, rotated.RefreshToken)
	}
}

// TestVerifyPasswordlessGenericFailures proves every failure reason collapses to the
// one generic ErrPasswordlessLogin (design §5.8): a disabled kind, a malformed
// identifier, an unknown identifier, and a wrong code are indistinguishable and mint
// nothing.
func TestVerifyPasswordlessGenericFailures(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "known@example.com", "known@example.com")
	ctx := context.Background()

	cases := []struct {
		name           string
		kind, id, code string
	}{
		{"disabled kind", string(identifier.KindPhone), "+15551112222", code},
		{"malformed identifier", string(identifier.KindEmail), "not-an-email", code},
		{"unknown identifier", string(identifier.KindEmail), "ghost@example.com", "000000"},
		{"wrong code", string(identifier.KindEmail), "known@example.com", "999999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.svc.VerifyPasswordless(ctx, tc.kind, tc.id, tc.code); !errors.Is(err, ErrPasswordlessLogin) {
				t.Fatalf("err = %v, want ErrPasswordlessLogin", err)
			}
		})
	}
	if len(h.sess.m) != 0 {
		t.Errorf("failed verifies minted %d sessions, want 0 (no auto-provision)", len(h.sess.m))
	}
}

// TestVerifyPasswordlessNoAutoProvision proves an OTP verify for an identifier that
// does not exist never creates a user or session — passwordless is login-only
// (design §4.1/V3).
func TestVerifyPasswordlessNoAutoProvision(t *testing.T) {
	h := newHarness(t, nil)
	enablePasswordless(h, string(identifier.KindEmail))

	if _, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "nobody@example.com", "123456"); !errors.Is(err, ErrPasswordlessLogin) {
		t.Fatalf("err = %v, want ErrPasswordlessLogin", err)
	}
	if len(h.users.byID) != 0 {
		t.Errorf("auto-provisioned %d users, want 0", len(h.users.byID))
	}
	if len(h.sess.m) != 0 {
		t.Errorf("minted %d sessions, want 0", len(h.sess.m))
	}
}

// TestVerifyPasswordlessWrongCodeLockout proves wrong codes count attempts and lock
// out, and the lockout surfaces to the caller as the SAME generic failure as an
// invalid code — never a distinct disposition (design §5.8).
func TestVerifyPasswordlessWrongCodeLockout(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "lock@example.com", "password123456789")
	h.mustVerify(t, "lock@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	startPasswordlessCode(t, h, string(identifier.KindEmail), "lock@example.com", "lock@example.com")
	ctx := context.Background()

	var lastErr error
	for i := 0; i < challenge.MaxAttempts+1; i++ {
		_, lastErr = h.svc.VerifyPasswordless(ctx, string(identifier.KindEmail), "lock@example.com", "111111")
		if !errors.Is(lastErr, ErrPasswordlessLogin) {
			t.Fatalf("attempt %d err = %v, want ErrPasswordlessLogin", i, lastErr)
		}
	}
	// After lockout the challenge row is gone; a subsequent verify is still the same
	// generic failure.
	if n := h.loginChallengeCount(challenge.PurposeLoginOTP); n != 0 {
		t.Errorf("login_otp challenge count after lockout = %d, want 0", n)
	}
}

// TestVerifyPasswordlessIdentifierRemoved proves a code cannot log in after its
// identifier was removed: GetLogin resolves nothing and the verify is the generic
// failure with nothing minted (design §4.1).
func TestVerifyPasswordlessIdentifierRemoved(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "gone@example.com", "password123456789")
	h.mustVerify(t, "gone@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "gone@example.com", "gone@example.com")

	h.idents.retireAll(u.ID, time.Now())

	if _, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "gone@example.com", code); !errors.Is(err, ErrPasswordlessLogin) {
		t.Fatalf("verify after identifier removed err = %v, want ErrPasswordlessLogin", err)
	}
	if len(h.sess.m) != 0 {
		t.Errorf("verify after removal minted %d sessions, want 0", len(h.sess.m))
	}
}

// TestVerifyPasswordlessIdentifierReplaced proves a code cannot log in after its
// identifier was replaced by a different row claiming the same value: the current
// row's ID no longer matches the challenge's stored binding, so the atomic consume
// rejects it as the generic failure and spends nothing usable (design §4.1).
func TestVerifyPasswordlessIdentifierReplaced(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "swap@example.com", "password123456789")
	h.mustVerify(t, "swap@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "swap@example.com", "swap@example.com")

	// Replace: retire the bound row and claim the same value with a fresh identifier
	// ID, so GetLogin resolves a row whose ID differs from the challenge binding.
	now := time.Now()
	h.idents.retireAll(u.ID, now)
	h.idents.insert(identifier.Identifier{
		ID: "replacement", UserID: u.ID, Kind: identifier.KindEmail, NormalizedValue: "swap@example.com",
		VerifiedAt: now, LoginEnabled: true, CreatedAt: now,
	})

	if _, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "swap@example.com", code); !errors.Is(err, ErrPasswordlessLogin) {
		t.Fatalf("verify after identifier replaced err = %v, want ErrPasswordlessLogin", err)
	}
	if len(h.sess.m) != 0 {
		t.Errorf("verify after replacement minted %d sessions, want 0", len(h.sess.m))
	}
}

// TestVerifyPasswordlessOldKeyStillVerifies proves a code issued under a
// now-rotated-away pepper key still verifies while that key is still accepted
// (design §3.3): the atomic consume selects the candidate matching the row's key.
func TestVerifyPasswordlessOldKeyStillVerifies(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "rotate@example.com", "password123456789")
	h.mustVerify(t, "rotate@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "rotate@example.com", "rotate@example.com")

	// Rotate: k2 is active, k1 (the issuing key) remains accepted.
	h.prot.active = "k2"
	h.prot.accepted = []string{"k1", "k2"}

	pair, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "rotate@example.com", code)
	if err != nil {
		t.Fatalf("VerifyPasswordless after key rotation: %v", err)
	}
	if pair.AccessToken == "" {
		t.Error("no session minted after old-key verify")
	}
}

// TestVerifyPasswordlessRateLimited proves the §4.4 verify budget refuses before any
// lookup with the distinct rate-limit error (not the generic 401), and does not
// consume the challenge — a throttled attempt is retryable once the window resets.
func TestVerifyPasswordlessRateLimited(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "limited@example.com", "password123456789")
	h.mustVerify(t, "limited@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "limited@example.com", "limited@example.com")

	// Swap in a denying limiter for the verify budget only (the start already ran).
	h.svc.limiter = denyLimiter{}
	if _, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "limited@example.com", code); !errors.Is(err, ErrPasswordlessRateLimited) {
		t.Fatalf("rate-limited verify err = %v, want ErrPasswordlessRateLimited", err)
	}
	if n := h.loginChallengeCount(challenge.PurposeLoginOTP); n != 1 {
		t.Errorf("challenge consumed under rate limit (count=%d, want 1)", n)
	}
}

// TestVerifyPasswordlessSingleWinner proves two concurrent correct verifies of the
// same code produce exactly one minted session — the atomic consume elects one
// winner and the loser is the generic failure (design §3.2 single-use invariant).
func TestVerifyPasswordlessSingleWinner(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "race@example.com", "password123456789")
	h.mustVerify(t, "race@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "race@example.com", "race@example.com")

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		wins   int
		losses int
		badErr error
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "race@example.com", code)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrPasswordlessLogin):
				losses++
			default:
				badErr = err
			}
		}()
	}
	wg.Wait()
	if badErr != nil {
		t.Fatalf("unexpected verify error: %v", badErr)
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins=%d losses=%d, want exactly one winner", wins, losses)
	}
	if len(h.sess.m) != 1 {
		t.Errorf("minted %d sessions, want exactly 1", len(h.sess.m))
	}
}

// TestVerifyPasswordlessPhoneSMSCode proves the phone OTP rail mints through the same
// path and stamps the sms_code method.
func TestVerifyPasswordlessPhoneSMSCode(t *testing.T) {
	h := newHarness(t, nil)
	h.enablePhone(t)
	enablePasswordless(h, string(identifier.KindPhone))
	now := time.Now()
	h.users.byID["phoneuser"] = user.User{ID: "phoneuser"}
	h.idents.byID["ph1"] = identifier.Identifier{
		ID: "ph1", UserID: "phoneuser", Kind: identifier.KindPhone, NormalizedValue: "+15551112222",
		VerifiedAt: now, LoginEnabled: true, CreatedAt: now,
	}

	code := startPasswordlessCode(t, h, string(identifier.KindPhone), "+1 (555) 111-2222", "+15551112222")
	pair, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindPhone), "+1 (555) 111-2222", code)
	if err != nil {
		t.Fatalf("VerifyPasswordless(phone): %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("no session minted for phone OTP verify")
	}
	h.sess.mu.Lock()
	var got session.Session
	for _, s := range h.sess.m {
		got = s
	}
	h.sess.mu.Unlock()
	if len(got.Authentication.Methods) != 1 || got.Authentication.Methods[0].Kind != session.MethodSMSCode {
		t.Errorf("session methods = %+v, want single sms_code", got.Authentication.Methods)
	}
}
