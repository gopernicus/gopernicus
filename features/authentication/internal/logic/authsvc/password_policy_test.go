package authsvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// baselinePassword is a policy-valid password (24 code points, no breach) used to
// register/seed accounts in the policy tests so the flow under test is exercised
// with a known-good starting credential.
const baselinePassword = "passphrase-correct-horse"

// stubChecker is a deterministic CompromisedPasswordChecker for the policy tests.
// It flags exactly the password equal to bad (empty bad → nothing is compromised)
// and, when err is set, reports that the check could not complete — driving the
// fail-closed/fail-open policy. calls counts how often it was consulted, proving
// the length gates short-circuit before the (potentially remote) check.
type stubChecker struct {
	bad   string
	err   error
	calls int
}

func (c *stubChecker) IsCompromised(_ context.Context, pw string) (bool, error) {
	c.calls++
	if c.err != nil {
		return false, c.err
	}
	return c.bad != "" && pw == c.bad, nil
}

// serviceWithChecker rebuilds the harness service with a wired compromised-password
// checker and the chosen fail-open policy, reusing the harness fakes so the full
// register/change/reset flow is available.
func serviceWithChecker(t *testing.T, checker compromisedChecker, failOpen bool) *harness {
	t.Helper()
	h := newHarness(t, nil)
	h.svc = NewService(Deps{
		Users:               h.users,
		Identifiers:         h.idents,
		Passwords:           h.pw,
		Sessions:            h.sess,
		Challenges:          h.ch,
		Protector:           h.prot,
		PasswordResets:      h.pr,
		Hasher:              h.hasher,
		Compromised:         checker,
		CompromisedFailOpen: failOpen,
		Limiter:             ratelimiter.NewMemory(),
		SecurityEvents:      h.events,
		TokenSigner:         h.signer,
	})
	wireSyncDelivery(t, h.svc, h.mailer, nil)
	return h
}

// rejectedByValidation reports, per entry point, whether candidate is rejected by
// the SHARED password policy (sdk.ErrInvalidInput). Register/change/reset all route
// through the one validatePassword, so this drives all three to prove the policy is
// identical across flows. Reset validates before token redemption, so a bogus token
// still surfaces the length verdict: a length-valid candidate returns the generic
// reset failure (not ErrInvalidInput), i.e. it passed validation.
func rejectedByValidation(t *testing.T, candidate string) (reg, change, reset bool) {
	t.Helper()
	ctx := context.Background()

	hr := newHarness(t, nil)
	_, err := hr.svc.Register(ctx, "reg@example.com", candidate, "R")
	reg = errors.Is(err, sdk.ErrInvalidInput)

	hc := newHarness(t, nil)
	u := hc.mustRegister(t, "chg@example.com", baselinePassword)
	_, err = hc.svc.ChangePassword(ctx, u.ID, baselinePassword, candidate)
	change = errors.Is(err, sdk.ErrInvalidInput)

	hp := newHarness(t, nil)
	err = hp.svc.ResetPassword(ctx, "bogus-token", candidate)
	reset = errors.Is(err, sdk.ErrInvalidInput)
	return
}

// TestPasswordPolicyLengthIdenticalAcrossFlows proves the §5.9 length policy — at
// least 15 Unicode CODE POINTS, at most 64, a finite pre-hash byte cap — and that
// register, change, and reset apply it identically. Multibyte cases prove the
// minimum counts code points, not bytes.
func TestPasswordPolicyLengthIdenticalAcrossFlows(t *testing.T) {
	cases := []struct {
		name   string
		pw     string
		accept bool
	}{
		{"14 ascii below min", strings.Repeat("a", 14), false},
		{"15 ascii at min", strings.Repeat("a", 15), true},
		{"64 ascii at max", strings.Repeat("a", 64), true},
		{"65 ascii over max", strings.Repeat("a", 65), false},
		{"far over pre-hash byte cap", strings.Repeat("a", 100_000), false},
		{"14 multibyte runes below min", strings.Repeat("é", 14), false}, // 28 bytes, 14 code points
		{"15 multibyte runes at min", strings.Repeat("é", 15), true},     // 30 bytes, 15 code points
		{"64 multibyte runes at max", strings.Repeat("é", 64), true},     // 128 bytes, 64 code points
		{"65 multibyte runes over max", strings.Repeat("é", 65), false},  // 65 code points
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, change, reset := rejectedByValidation(t, tc.pw)
			wantRejected := !tc.accept
			if reg != wantRejected || change != wantRejected || reset != wantRejected {
				t.Fatalf("policy drift: register-rejected=%v change-rejected=%v reset-rejected=%v, want all %v",
					reg, change, reset, wantRejected)
			}
		})
	}
}

// TestPasswordCompromisedRejected proves a wired checker's compromised verdict
// rejects the password with the stable ErrPasswordCompromised (400) and writes no
// account, and that change/reset consult the same checker.
func TestPasswordCompromisedRejected(t *testing.T) {
	const bad = "hunter2-breached-passphrase" // policy-valid length, breached
	ctx := context.Background()

	t.Run("register", func(t *testing.T) {
		chk := &stubChecker{bad: bad}
		h := serviceWithChecker(t, chk, false)
		_, err := h.svc.Register(ctx, "reg@example.com", bad, "R")
		if !errors.Is(err, ErrPasswordCompromised) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Register(compromised): err=%v, want ErrPasswordCompromised(ErrInvalidInput)", err)
		}
		if chk.calls != 1 {
			t.Errorf("checker calls = %d, want 1", chk.calls)
		}
		if _, err := h.idents.GetLogin(ctx, string(identifier.KindEmail), "reg@example.com"); !errors.Is(err, sdk.ErrNotFound) {
			t.Error("account created for a compromised password")
		}
		if h.mailer.count() != 0 {
			t.Error("mail sent for a compromised registration")
		}
	})

	t.Run("change", func(t *testing.T) {
		chk := &stubChecker{bad: bad}
		h := serviceWithChecker(t, chk, false)
		u := h.mustRegister(t, "chg@example.com", baselinePassword)
		_, err := h.svc.ChangePassword(ctx, u.ID, baselinePassword, bad)
		if !errors.Is(err, ErrPasswordCompromised) {
			t.Fatalf("ChangePassword(compromised): err=%v, want ErrPasswordCompromised", err)
		}
	})

	t.Run("reset", func(t *testing.T) {
		chk := &stubChecker{bad: bad}
		h := serviceWithChecker(t, chk, false)
		err := h.svc.ResetPassword(ctx, "bogus-token", bad)
		if !errors.Is(err, ErrPasswordCompromised) {
			t.Fatalf("ResetPassword(compromised): err=%v, want ErrPasswordCompromised", err)
		}
	})
}

// TestPasswordCompromisedCleanPasses proves a wired checker that clears the
// candidate does not block a policy-valid password.
func TestPasswordCompromisedCleanPasses(t *testing.T) {
	chk := &stubChecker{} // nothing is compromised
	h := serviceWithChecker(t, chk, false)
	if _, err := h.svc.Register(context.Background(), "clean@example.com", baselinePassword, "C"); err != nil {
		t.Fatalf("Register(clean): %v", err)
	}
	if chk.calls != 1 {
		t.Errorf("checker calls = %d, want 1", chk.calls)
	}
}

// TestPasswordCompromisedCheckerFailClosed is the PRODUCTION DEFAULT (design §5.9,
// V15): when a wired checker cannot complete, the password is REJECTED so an
// unavailable breach service never becomes a silent bypass. No account is written.
func TestPasswordCompromisedCheckerFailClosed(t *testing.T) {
	down := errors.New("breach corpus unreachable")
	chk := &stubChecker{err: down}
	h := serviceWithChecker(t, chk, false) // fail closed is the default
	_, err := h.svc.Register(context.Background(), "closed@example.com", baselinePassword, "C")
	if err == nil {
		t.Fatal("Register with an unavailable checker succeeded under fail-closed policy")
	}
	if !errors.Is(err, down) {
		t.Errorf("fail-closed error does not wrap the checker cause: %v", err)
	}
	if _, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "closed@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Error("account created despite an unavailable breach checker (fail closed)")
	}
}

// TestPasswordCompromisedCheckerFailOpen proves the opt-in availability trade: when
// the checker cannot complete AND the host set fail-open, the password is accepted.
func TestPasswordCompromisedCheckerFailOpen(t *testing.T) {
	chk := &stubChecker{err: errors.New("breach corpus unreachable")}
	h := serviceWithChecker(t, chk, true)
	if _, err := h.svc.Register(context.Background(), "open@example.com", baselinePassword, "O"); err != nil {
		t.Fatalf("Register under fail-open with an unavailable checker: %v", err)
	}
	if chk.calls != 1 {
		t.Errorf("checker calls = %d, want 1", chk.calls)
	}
	if _, err := h.idents.GetLogin(context.Background(), string(identifier.KindEmail), "open@example.com"); err != nil {
		t.Errorf("account not created under fail-open: %v", err)
	}
}

// TestPasswordCheckerNotConsultedWhenLengthInvalid proves the breach check runs
// LAST: a length-invalid candidate is rejected on length alone and never reaches
// the (potentially remote) checker.
func TestPasswordCheckerNotConsultedWhenLengthInvalid(t *testing.T) {
	chk := &stubChecker{bad: "short"}
	h := serviceWithChecker(t, chk, false)
	_, err := h.svc.Register(context.Background(), "short@example.com", "short", "S")
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Register(too short): err=%v, want ErrInvalidInput", err)
	}
	if chk.calls != 0 {
		t.Errorf("checker consulted for a length-invalid password: calls=%d, want 0", chk.calls)
	}
}
