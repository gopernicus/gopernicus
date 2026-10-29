package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// errFactorRead is the factor store failure the snapshot tests inject.
var errFactorRead = errors.New("factor store down")

// failingTOTP fails every Get while fail is set, so a test can break the
// enrollment snapshot's factor read and then restore it.
type failingTOTP struct {
	*fakeTOTP
	fail *bool
}

func (f failingTOTP) Get(ctx context.Context, userID string) (totp.Credential, error) {
	if *f.fail {
		return totp.Credential{}, errFactorRead
	}
	return f.fakeTOTP.Get(ctx, userID)
}

// newEnrollmentHarness is the TOTP harness under native all: the window opens
// at start and lasts grace.
func newEnrollmentHarness(t *testing.T, start time.Time, grace time.Duration) *totpHarness {
	t.Helper()
	h := newTOTPHarness(t)
	h.svc.mfaEnrollment = MFAEnrollmentConfig{Start: start, Grace: grace}
	return h
}

// userWith is a stored user the snapshot reads only for its creation time.
func (h *totpHarness) userWith(id string, createdAt time.Time) user.User {
	return user.User{ID: id, CreatedAt: createdAt}
}

// enrollmentOf verifies a pair's access token and returns its credential.
func enrollmentOf(t *testing.T, h *totpHarness, pair TokenPair) Credential {
	t.Helper()
	_, cred, ok := h.svc.verifyAccessClaims(pair.AccessToken)
	if !ok {
		t.Fatal("the access token did not verify")
	}
	return cred
}

// TestEnrollmentDeadlineMath pins max(Start, CreatedAt) + Grace, a zero
// CreatedAt counting from Start, and the snapshot's zero cases: the floor off,
// an AAL2 authentication.
func TestEnrollmentDeadlineMath(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	grace := 7 * 24 * time.Hour
	h := newEnrollmentHarness(t, start, grace)
	aal1 := session.AuthenticationMetadata{Assurance: session.AssuranceAAL1}

	for name, tc := range map[string]struct {
		createdAt time.Time
		want      time.Time
	}{
		"created before start": {start.Add(-time.Hour), start.Add(grace)},
		"created after start":  {start.Add(48 * time.Hour), start.Add(48*time.Hour + grace)},
		"zero created at":      {time.Time{}, start.Add(grace)},
	} {
		id := "u-" + name
		h.users.byID[id] = h.userWith(id, tc.createdAt)
		got, err := h.svc.enrollmentDeadline(ctx, id, aal1)
		if err != nil || got.Enrolled || !got.EnrollBy.Equal(tc.want) {
			t.Errorf("%s: snapshot = %+v, %v; want EnrollBy %s", name, got, err, tc.want)
		}
	}

	h.users.byID["u-aal2"] = h.userWith("u-aal2", start)
	if got, err := h.svc.enrollmentDeadline(ctx, "u-aal2", session.AuthenticationMetadata{Assurance: session.AssuranceAAL2}); err != nil || got != (enrollmentSnapshot{}) {
		t.Errorf("AAL2: snapshot = %+v, %v; want none", got, err)
	}
	h.svc.mfaEnrollment = MFAEnrollmentConfig{}
	if got, err := h.svc.enrollmentDeadline(ctx, "u-aal2", aal1); err != nil || got != (enrollmentSnapshot{}) {
		t.Errorf("floor off: snapshot = %+v, %v; want none", got, err)
	}
	if got, err := h.svc.enrollmentDeadline(ctx, "missing", aal1); err != nil || got != (enrollmentSnapshot{}) {
		t.Errorf("floor off, missing user: snapshot = %+v, %v; want none and no read", got, err)
	}
	h.svc.mfaEnrollment = MFAEnrollmentConfig{Start: start, Grace: grace}
	if _, err := h.svc.enrollmentDeadline(ctx, "missing", aal1); !errors.Is(err, session.ErrUserNotActive) {
		t.Errorf("missing user: err = %v, want session.ErrUserNotActive", err)
	}
}

// TestEnrollmentClaimsAcrossTheFlow drives a password login, refresh, TOTP
// enrollment and step-up under native all: an unenrolled AAL1 token carries
// mfa_enroll_by, the refresh re-evaluates it, an enrolled AAL1 token carries
// mfa_enrolled (never a deadline, so the floor answers step-up rather than a
// refresh loop), and the AAL2 token carries neither.
func TestEnrollmentClaimsAcrossTheFlow(t *testing.T) {
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	h := newEnrollmentHarness(t, start, 3*time.Minute)
	u, pair, sessionID := h.totpAccount(t, "enrol@example.com")

	cred := enrollmentOf(t, h, pair)
	stored, _ := h.users.Get(ctx, u.ID)
	want := stored.CreatedAt.Add(3 * time.Minute).Truncate(time.Second)
	if cred.MFAEnrolled || !cred.MFAEnrollBy.Truncate(time.Second).Equal(want) {
		t.Fatalf("login: enroll_by = %s enrolled = %v, want %s", cred.MFAEnrollBy, cred.MFAEnrolled, want)
	}

	e, _ := h.enrollTOTP(t, sessionID, u.ID)
	refreshed, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	cred = enrollmentOf(t, h, refreshed)
	if !cred.MFAEnrolled || !cred.MFAEnrollBy.IsZero() {
		t.Fatalf("refresh after enrolment: enroll_by = %s enrolled = %v, want enrolled", cred.MFAEnrollBy, cred.MFAEnrolled)
	}
	again, err := h.svc.Refresh(ctx, refreshed.RefreshToken)
	if err != nil || !enrollmentOf(t, h, again).MFAEnrolled {
		t.Fatalf("a second refresh = %v, want mfa_enrolled again (no claimless loop)", err)
	}

	h.advance(totp.Period)
	result, err := h.svc.StepUpWithTOTP(ctx, StepUpCodeInput{SessionID: sessionID, UserID: u.ID, Code: h.code(t, e, 0)})
	if err != nil {
		t.Fatalf("StepUpWithTOTP: %v", err)
	}
	claims, err := h.signer.Verify(result.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := claims[tokenClaimMFAEnrollBy]; present {
		t.Error("the AAL2 token carries mfa_enroll_by")
	}
	if _, present := claims[tokenClaimMFAEnrolled]; present {
		t.Error("the AAL2 token carries mfa_enrolled")
	}
}

// TestEnrollmentClaimsOffByDefault pins that a host without native all signs
// neither claim.
func TestEnrollmentClaimsOffByDefault(t *testing.T) {
	h := newTOTPHarness(t)
	_, pair, _ := h.totpAccount(t, "off@example.com")
	claims, err := h.signer.Verify(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{tokenClaimMFAEnrollBy, tokenClaimMFAEnrolled} {
		if _, present := claims[name]; present {
			t.Errorf("floor off: the token carries %s", name)
		}
	}
}

// TestEnrollmentReadErrorFailsClosed proves a factor read error fails the mint
// as sdk.ErrUnavailable rather than signing a claimless token, and that the
// refresh grace lane computes the snapshot before consuming its slot: a failed
// snapshot leaves the slot unconsumed, so the client's retry succeeds instead of
// reading as reuse and revoking the session.
func TestEnrollmentReadErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	h := newEnrollmentHarness(t, time.Now().Add(-time.Hour), time.Hour)
	_, pair, sessionID := h.totpAccount(t, "fail@example.com")
	fail := false
	h.svc.totp = failingTOTP{fakeTOTP: h.totp, fail: &fail}

	fail = true
	if _, _, err := h.svc.Login(ctx, "fail@example.com", passkeyPass); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("Login with the factor store down = %v, want sdk.ErrUnavailable", err)
	}
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("rotate with the factor store down = %v, want sdk.ErrUnavailable", err)
	}

	fail = false
	rotated, err := h.svc.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("rotate after recovery: %v (the failed rotation consumed the current slot)", err)
	}
	fail = true
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("grace with the factor store down = %v, want sdk.ErrUnavailable", err)
	}
	fail = false
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("grace retry after recovery = %v, want the single-use grace (the failed attempt consumed it)", err)
	}
	if _, err := h.sess.Get(ctx, sessionID); err != nil {
		t.Fatalf("the session was revoked: %v", err)
	}
	if _, err := h.svc.Refresh(ctx, rotated.RefreshToken); err != nil {
		t.Fatalf("the winning refresh token no longer refreshes: %v", err)
	}
}
