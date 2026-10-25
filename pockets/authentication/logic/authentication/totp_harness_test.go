package authentication

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// fakeTOTP is the totp.Repository the flow tests drive. Confirm and Remove run
// under the user lock the credential rail serializes on, as the contract asks.
type fakeTOTP struct {
	users    *fakeUsers
	passkeys *fakePasskeys

	mu    sync.Mutex
	creds map[string]totp.Credential
	codes map[string][]totp.RecoveryCode
	// beforeRecordUse runs once before RecordUse's compare-and-swap.
	beforeRecordUse func()
}

func newFakeTOTP(users *fakeUsers, passkeys *fakePasskeys) *fakeTOTP {
	return &fakeTOTP{users: users, passkeys: passkeys, creds: map[string]totp.Credential{}, codes: map[string][]totp.RecoveryCode{}}
}

func (f *fakeTOTP) Begin(_ context.Context, c totp.Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ex, ok := f.creds[c.UserID]; ok && ex.Confirmed() {
		return totp.ErrAlreadyEnrolled
	}
	c.ConfirmedAt, c.FailedAttempts, c.LockedUntil, c.LastUsedStep, c.LastUsedAt = nil, 0, nil, 0, nil
	f.creds[c.UserID] = c
	return nil
}

func (f *fakeTOTP) Get(_ context.Context, userID string) (totp.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[userID]
	if !ok {
		return totp.Credential{}, sdk.ErrNotFound
	}
	return c, nil
}

func (f *fakeTOTP) Confirm(_ context.Context, in totp.Confirmation) error {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[in.UserID]
	if !ok {
		return sdk.ErrNotFound
	}
	if !u.Active() {
		return session.ErrUserNotActive
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[in.UserID]
	switch {
	case !ok:
		return sdk.ErrNotFound
	case c.Confirmed():
		return totp.ErrAlreadyEnrolled
	case c.Secret != in.ExpectedSecret || c.SessionID != in.ExpectedSessionID:
		return sdk.ErrNotFound
	case u.AuthRevision != in.ExpectedAuthRevision:
		return sdk.ErrConflict
	}
	if in.Bootstrap && f.passkeys != nil {
		f.passkeys.mu.Lock()
		for _, p := range f.passkeys.m {
			if p.UserID == in.UserID && p.AAL2Capable() {
				f.passkeys.mu.Unlock()
				return totp.ErrBootstrapClosed
			}
		}
		f.passkeys.mu.Unlock()
	}
	at := in.ConfirmedAt
	c.ConfirmedAt, c.LastUsedStep = &at, in.Step
	c.SessionID, c.Tier, c.AuthRevision, c.ExpiresAt, c.FailedAttempts, c.LockedUntil = "", "", 0, nil, 0, nil
	f.creds[in.UserID] = c
	f.codes[in.UserID] = slices.Clone(in.Codes)
	u.AuthRevision++
	f.users.byID[in.UserID] = u
	return nil
}

func (f *fakeTOTP) RecordUse(_ context.Context, userID string, expectedStep, step int64, at time.Time) error {
	f.mu.Lock()
	hook := f.beforeRecordUse
	f.beforeRecordUse = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[userID]
	if !ok || !c.Confirmed() || c.LastUsedStep != expectedStep || step <= expectedStep || (c.LockedUntil != nil && c.LockedUntil.After(at)) {
		return sdk.ErrConflict
	}
	c.LastUsedStep, c.LastUsedAt, c.FailedAttempts, c.LockedUntil = step, &at, 0, nil
	f.creds[userID] = c
	return nil
}

func (f *fakeTOTP) RecordFailure(_ context.Context, userID string, lockAt int, lockFor time.Duration, now time.Time) (totp.Failure, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[userID]
	if !ok {
		return totp.Failure{}, sdk.ErrNotFound
	}
	c.FailedAttempts++
	out := totp.Failure{Attempts: c.FailedAttempts}
	if c.FailedAttempts >= lockAt {
		until := now.Add(lockFor)
		c.FailedAttempts, c.LockedUntil = 0, &until
		out.LockedUntil = &until
	}
	f.creds[userID] = c
	return out, nil
}

func (f *fakeTOTP) ReplaceRecoveryCodes(_ context.Context, userID string, codes []totp.RecoveryCode, expected *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.creds[userID]
	if !ok || !c.Confirmed() {
		return sdk.ErrNotFound
	}
	if expected != nil {
		current := generatedAt(f.codes[userID])
		if current == nil || !current.Equal(*expected) {
			return sdk.ErrConflict
		}
	}
	f.codes[userID] = slices.Clone(codes)
	return nil
}

func (f *fakeTOTP) ConsumeRecoveryCode(_ context.Context, userID string, candidates []challenge.DigestCandidate, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	codes := f.codes[userID]
	for i, rc := range codes {
		if slices.ContainsFunc(candidates, func(c challenge.DigestCandidate) bool { return c.KeyID == rc.KeyID && c.Digest == rc.Digest }) {
			f.codes[userID] = slices.Delete(slices.Clone(codes), i, i+1)
			if c, ok := f.creds[userID]; ok {
				c.FailedAttempts = 0
				f.creds[userID] = c
			}
			return len(f.codes[userID]), nil
		}
	}
	return 0, sdk.ErrNotFound
}

func (f *fakeTOTP) CountRecoveryCodes(_ context.Context, userID string) (int, *time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.codes[userID]), generatedAt(f.codes[userID]), nil
}

func (f *fakeTOTP) Remove(_ context.Context, userID string, expectedRevision int64) (totp.Removed, error) {
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[userID]
	if !ok {
		return totp.Removed{}, sdk.ErrNotFound
	}
	if u.AuthRevision != expectedRevision {
		return totp.Removed{}, sdk.ErrConflict
	}
	f.mu.Lock()
	c, had := f.creds[userID]
	n := len(f.codes[userID])
	delete(f.creds, userID)
	delete(f.codes, userID)
	f.mu.Unlock()
	if !had {
		return totp.Removed{}, nil
	}
	if !c.Confirmed() {
		return totp.Removed{Pending: true}, nil
	}
	u.AuthRevision++
	f.users.byID[userID] = u
	f.users.revokeCredentialStateLocked(userID)
	return totp.Removed{Credential: true, RecoveryCodes: n}, nil
}

func (f *fakeTOTP) RecoveryCodeKeyIDs(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, codes := range f.codes {
		for _, c := range codes {
			if !slices.Contains(out, c.KeyID) {
				out = append(out, c.KeyID)
			}
		}
	}
	return out, nil
}

func generatedAt(codes []totp.RecoveryCode) *time.Time {
	if len(codes) == 0 {
		return nil
	}
	at := codes[0].GeneratedAt
	return &at
}

// totpHarness is the passkey harness with TOTP wired over the fake store and a
// one-key AES-GCM ring.
type totpHarness struct {
	*passkeyHarness
	totp *fakeTOTP
	ring *cryptids.AESGCMRing
}

func newTOTPHarness(t *testing.T) *totpHarness {
	t.Helper()
	h := &totpHarness{passkeyHarness: newPasskeyHarness(t)}
	h.totp = newFakeTOTP(h.users, h.passkeys)
	ring, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: "t1", Keys: map[string][]byte{"t1": []byte(strings.Repeat("t", 32))}})
	if err != nil {
		t.Fatal(err)
	}
	h.ring = ring
	h.svc.totp, h.svc.totpEncrypter, h.svc.totpIssuer = h.totp, ring, "Example"
	return h
}

// begin starts an enrollment for the caller's fresh session.
func (h *totpHarness) begin(t *testing.T, sessionID, userID string) TOTPEnrollment {
	t.Helper()
	e, err := h.svc.BeginTOTPEnrollment(context.Background(), sessionID, userID)
	if err != nil {
		t.Fatalf("BeginTOTPEnrollment: %v", err)
	}
	return e
}

// code is the TOTP code of an enrollment's secret at the harness clock plus
// offset steps.
func (h *totpHarness) code(t *testing.T, e TOTPEnrollment, offset int64) string {
	t.Helper()
	secret, err := totp.DecodeSecret(e.Secret)
	if err != nil {
		t.Fatal(err)
	}
	return totp.Code(secret, totp.Step(h.now())+offset)
}

// enrollTOTP begins and confirms an enrollment, returning the enrollment and the
// recovery codes.
func (h *totpHarness) enrollTOTP(t *testing.T, sessionID, userID string) (TOTPEnrollment, TOTPConfirmation) {
	t.Helper()
	e := h.begin(t, sessionID, userID)
	c, err := h.svc.ConfirmTOTPEnrollment(context.Background(), TOTPConfirmInput{SessionID: sessionID, UserID: userID, Code: h.code(t, e, 0)})
	if err != nil {
		t.Fatalf("ConfirmTOTPEnrollment: %v", err)
	}
	return e, c
}

// advance moves the harness clock by d.
func (h *totpHarness) advance(d time.Duration) { h.offset += d }

func (h *totpHarness) mfaNotices() int {
	h.mailer.mu.Lock()
	defer h.mailer.mu.Unlock()
	n := 0
	for _, msg := range h.mailer.sent {
		if msg.Subject == "Your two-step verification changed" {
			n++
		}
	}
	return n
}

// totpAccount registers, verifies and signs in a user (a fresh AAL1 password
// login).
func (h *totpHarness) totpAccount(t *testing.T, email string) (user.User, TokenPair, string) {
	t.Helper()
	return h.account(t, email)
}

// lastRow returns the newest row of eventType, or fails.
func (h *totpHarness) lastRow(t *testing.T, eventType string) securityevent.SecurityEvent {
	t.Helper()
	rows := h.rows(eventType)
	if len(rows) == 0 {
		t.Fatalf("no %s row", eventType)
	}
	return rows[len(rows)-1]
}
