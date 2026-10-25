package authentication

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/webauthnstub"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	passkeyRPID   = "example.test"
	passkeyOrigin = "https://example.test"
	passkeyPass   = "password123456789"
)

// fakePasskeys is the passkey.Repository the ceremony tests drive. Create runs
// under the user lock the credential rail serializes on. beforeRecordUse fires
// once, before the compare-and-swap.
type fakePasskeys struct {
	users *fakeUsers

	mu              sync.Mutex
	m               map[string]passkey.Passkey
	beforeRecordUse func()
}

func newFakePasskeys(users *fakeUsers) *fakePasskeys {
	return &fakePasskeys{users: users, m: map[string]passkey.Passkey{}}
}

func (f *fakePasskeys) Create(_ context.Context, in passkey.CreateInput) (passkey.Passkey, error) {
	p := in.Passkey
	f.users.mu.Lock()
	defer f.users.mu.Unlock()
	u, ok := f.users.byID[p.UserID]
	if !ok {
		return passkey.Passkey{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return passkey.Passkey{}, session.ErrUserNotActive
	}
	if u.AuthRevision != in.ExpectedAuthRevision {
		return passkey.Passkey{}, sdk.ErrConflict
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var owned []passkey.Passkey
	for _, ex := range f.m {
		if ex.UserID == p.UserID {
			owned = append(owned, ex)
			if in.Bootstrap && ex.AAL2Capable() {
				return passkey.Passkey{}, passkey.ErrBootstrapClosed
			}
		}
	}
	for _, ex := range f.m {
		if bytes.Equal(ex.CredentialID, p.CredentialID) {
			return passkey.Passkey{}, sdk.ErrAlreadyExists
		}
	}
	if len(owned) >= in.MaxPerUser {
		return passkey.Passkey{}, passkey.ErrPasskeyLimit
	}
	if len(owned) > 0 && !bytes.Equal(owned[0].UserHandle, p.UserHandle) {
		return passkey.Passkey{}, sdk.ErrConflict
	}
	if p.ID == "" {
		p.ID = sdk.IDGenerator{}.MustGenerate()
	}
	f.m[p.ID] = p
	return p, nil
}

func (f *fakePasskeys) GetByCredentialID(_ context.Context, credentialID []byte) (passkey.Passkey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.m {
		if bytes.Equal(p.CredentialID, credentialID) {
			return p, nil
		}
	}
	return passkey.Passkey{}, sdk.ErrNotFound
}

func (f *fakePasskeys) ListByUser(_ context.Context, userID string) ([]passkey.Passkey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []passkey.Passkey{}
	for _, p := range f.m {
		if p.UserID == userID {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b passkey.Passkey) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (f *fakePasskeys) Rename(_ context.Context, id, userID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	if !ok || p.UserID != userID {
		return sdk.ErrNotFound
	}
	p.Name = name
	f.m[id] = p
	return nil
}

func (f *fakePasskeys) RecordUse(_ context.Context, id string, expected, count uint32, backupState bool, at time.Time) error {
	f.mu.Lock()
	hook := f.beforeRecordUse
	f.beforeRecordUse = nil
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	if !ok || p.SignCount != expected {
		return sdk.ErrConflict
	}
	p.SignCount, p.BackupState, p.LastUsedAt = count, backupState, &at
	f.m[id] = p
	return nil
}

func (f *fakePasskeys) owns(userID, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	return ok && p.UserID == userID
}

func (f *fakePasskeys) delete(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, id)
}

func (f *fakePasskeys) deleteUser(userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, p := range f.m {
		if p.UserID == userID {
			delete(f.m, id)
		}
	}
}

// passkeyHarness is the logic harness with passkeys wired over the conformance-
// verified stub relying party and an adjustable clock.
type passkeyHarness struct {
	*harness
	passkeys *fakePasskeys
	rp       *webauthnstub.RelyingParty
	offset   time.Duration
}

func newPasskeyHarness(t *testing.T) *passkeyHarness {
	t.Helper()
	h := &passkeyHarness{harness: newHarness(t, nil)}
	h.passkeys = newFakePasskeys(h.users)
	h.creds.passkeys = h.passkeys
	policy, err := web.NewOriginPolicy(web.OriginPolicyConfig{Exact: []string{passkeyOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	h.rp, err = webauthnstub.New(webauthn.Config{RPID: passkeyRPID, RPName: "Example", Origins: policy})
	if err != nil {
		t.Fatal(err)
	}
	h.rp.Now = h.now
	h.svc.now = h.now
	h.svc.passkeys, h.svc.relyingParty = h.passkeys, h.rp
	h.svc.passkeyUV, h.svc.passkeyMaxPerUser = webauthn.Preferred, defaultPasskeysPerUser
	return h
}

func (h *passkeyHarness) now() time.Time { return time.Now().Add(h.offset) }

// account registers, verifies and signs in a user, returning it and its
// session (a fresh AAL1 password login).
func (h *passkeyHarness) account(t *testing.T, email string) (user.User, TokenPair, string) {
	t.Helper()
	u := h.mustRegister(t, email, passkeyPass)
	h.mustVerify(t, email)
	pair, _, err := h.svc.Login(context.Background(), email, passkeyPass)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return u, pair, h.sessionOf(t, pair)
}

func (h *passkeyHarness) sessionOf(t *testing.T, pair TokenPair) string {
	t.Helper()
	_, cred, ok := h.svc.verifyAccessClaims(pair.AccessToken)
	if !ok || cred.SessionID == "" {
		t.Fatalf("access token %q did not verify", pair.AccessToken)
	}
	return cred.SessionID
}

func newPasskeyAuthenticator(verifying bool) *webauthntest.Authenticator {
	return webauthntest.NewAuthenticator(webauthntest.AuthenticatorConfig{RPID: passkeyRPID, Origin: passkeyOrigin, UserVerifying: verifying})
}

// enroll registers a passkey on a for the caller's session.
func (h *passkeyHarness) enroll(t *testing.T, a *webauthntest.Authenticator, sessionID, userID, name string) passkey.Passkey {
	t.Helper()
	ceremony, err := h.svc.BeginPasskeyRegistration(context.Background(), sessionID, userID)
	if err != nil {
		t.Fatalf("BeginPasskeyRegistration: %v", err)
	}
	response, err := a.Register(ceremony.PublicKey)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	p, err := h.svc.FinishPasskeyRegistration(context.Background(), PasskeyRegistrationInput{SessionID: sessionID, UserID: userID, Ceremony: ceremony.Ceremony, Response: response, Name: name})
	if err != nil {
		t.Fatalf("FinishPasskeyRegistration: %v", err)
	}
	return p
}

func (h *passkeyHarness) signIn(t *testing.T, a *webauthntest.Authenticator) (TokenPair, user.User, error) {
	t.Helper()
	ceremony, err := h.svc.BeginPasskeySignIn(context.Background())
	if err != nil {
		t.Fatalf("BeginPasskeySignIn: %v", err)
	}
	response, err := a.Assert(ceremony.PublicKey)
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	return h.svc.FinishPasskeySignIn(context.Background(), PasskeySignInInput{Ceremony: ceremony.Ceremony, Response: response})
}

func (h *passkeyHarness) stepUp(t *testing.T, a *webauthntest.Authenticator, sessionID, userID string) (StepUpResult, error) {
	t.Helper()
	ceremony, err := h.svc.BeginPasskeyStepUp(context.Background(), sessionID, userID)
	if err != nil {
		t.Fatalf("BeginPasskeyStepUp: %v", err)
	}
	response, err := a.Assert(ceremony.PublicKey)
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	return h.svc.FinishPasskeyStepUp(context.Background(), PasskeyStepUpInput{SessionID: sessionID, UserID: userID, Ceremony: ceremony.Ceremony, Response: response})
}

func (h *passkeyHarness) rows(eventType string) []securityevent.SecurityEvent {
	var out []securityevent.SecurityEvent
	for _, evt := range h.events.recorded() {
		if evt.EventType == eventType {
			out = append(out, evt)
		}
	}
	return out
}

func (h *passkeyHarness) assurance(t *testing.T, pair TokenPair) session.AssuranceLevel {
	t.Helper()
	_, cred, ok := h.svc.verifyAccessClaims(pair.AccessToken)
	if !ok {
		t.Fatal("access token did not verify")
	}
	return cred.Assurance
}

func (h *passkeyHarness) notices() int {
	h.mailer.mu.Lock()
	defer h.mailer.mu.Unlock()
	n := 0
	for _, msg := range h.mailer.sent {
		if msg.Subject == "Your passkeys were changed" {
			n++
		}
	}
	return n
}

func TestPasskeyRegistrationBootstrapsFromAFreshLogin(t *testing.T) {
	h := newPasskeyHarness(t)
	u, _, sessionID := h.account(t, "first@example.com")
	p := h.enroll(t, newPasskeyAuthenticator(true), sessionID, u.ID, "")
	if p.Name != "Passkey 1" || !p.UserVerified || p.UserID != u.ID || len(p.UserHandle) != passkeyHandleBytes || p.ID == "" {
		t.Fatalf("passkey = %+v", p)
	}
	rows := h.rows(securityevent.TypePasskeyRegistered)
	if len(rows) != 1 || rows[0].UserID != u.ID {
		t.Fatalf("passkey_registered rows = %+v", rows)
	}
	d := rows[0].Details
	if d["passkey_id"] != p.ID || d["user_verified"] != true || d["backup_eligible"] != false || d["aaguid"] != "776562617574686e746573742d616167" || len(d) != 4 {
		t.Fatalf("passkey_registered Details = %v", d)
	}
	if n := h.notices(); n != 1 {
		t.Fatalf("passkey_change_notice mails = %d, want 1", n)
	}
}

func TestPasskeyRegistrationGate(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "gate@example.com")

	h.offset = 6 * time.Minute
	if _, err := h.svc.BeginPasskeyRegistration(ctx, sessionID, u.ID); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a stale login's first passkey = %v, want ErrStepUpRequired", err)
	}
	if _, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{SessionID: sessionID, UserID: u.ID, Purpose: authgrant.PurposeRegisterPasskey}, passkeyPass); err != nil {
		t.Fatalf("password step-up: %v", err)
	}
	verifying := newPasskeyAuthenticator(true)
	h.enroll(t, verifying, sessionID, u.ID, "From the grant")

	// A holder of a user-verified passkey now needs AAL2: a fresh password login
	// or a password step-up grant no longer suffices.
	h.offset = 0
	pair, _, err := h.svc.Login(ctx, "gate@example.com", passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	fresh := h.sessionOf(t, pair)
	if _, err := h.svc.BeginPasskeyRegistration(ctx, fresh, u.ID); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a UV holder at AAL1 = %v, want ErrStepUpRequired", err)
	}
	if _, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{SessionID: fresh, UserID: u.ID, Purpose: authgrant.PurposeRegisterPasskey}, passkeyPass); err != nil {
		t.Fatalf("password step-up: %v", err)
	}
	if _, err := h.svc.BeginPasskeyRegistration(ctx, fresh, u.ID); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("a UV holder with an AAL1 grant = %v, want ErrStepUpRequired", err)
	}
	result, err := h.stepUp(t, verifying, fresh, u.ID)
	if err != nil || result.Assurance != session.AssuranceAAL2 {
		t.Fatalf("passkey step-up = %+v, %v", result, err)
	}
	h.enroll(t, newPasskeyAuthenticator(true), h.sessionOf(t, result.Tokens), u.ID, "Second")
	if list, _ := h.passkeys.ListByUser(ctx, u.ID); len(list) != 2 {
		t.Fatalf("passkeys = %d, want 2", len(list))
	}
}

func TestPasskeyRegistrationCapAndNames(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	h.svc.passkeyMaxPerUser = 2
	u, _, sessionID := h.account(t, "cap@example.com")
	if p := h.enroll(t, newPasskeyAuthenticator(false), sessionID, u.ID, "  Office YubiKey  "); p.Name != "Office YubiKey" {
		t.Fatalf("valid name = %q", p.Name)
	}
	if p := h.enroll(t, newPasskeyAuthenticator(false), sessionID, u.ID, "bad\x00name"); p.Name != "Passkey 2" {
		t.Fatalf("invalid name fell back to %q, want Passkey 2", p.Name)
	}
	if _, err := h.svc.BeginPasskeyRegistration(ctx, sessionID, u.ID); !errors.Is(err, ErrPasskeyLimit) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("Begin at the cap = %v, want ErrPasskeyLimit", err)
	}
}

func TestPasskeyCeremonyHandles(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "handles@example.com")
	other, _, otherSession := h.account(t, "handles-other@example.com")
	a := newPasskeyAuthenticator(true)
	begin := func() (PasskeyCeremony, []byte) {
		ceremony, err := h.svc.BeginPasskeyRegistration(ctx, sessionID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		response, err := a.Register(ceremony.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		return ceremony, response
	}
	ceremony, response := begin()
	if _, err := h.svc.FinishPasskeyRegistration(ctx, PasskeyRegistrationInput{SessionID: otherSession, UserID: other.ID, Ceremony: ceremony.Ceremony, Response: response}); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("another session's finish = %v, want ErrPasskeyRejected", err)
	}
	if _, err := h.svc.FinishPasskeyRegistration(ctx, PasskeyRegistrationInput{SessionID: sessionID, UserID: u.ID, Ceremony: ceremony.Ceremony, Response: response}); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("a spent handle = %v, want ErrPasskeyRejected", err)
	}
	ceremony, response = begin()
	h.offset = webauthn.MaxTimeout + time.Second
	if _, err := h.svc.FinishPasskeyRegistration(ctx, PasskeyRegistrationInput{SessionID: sessionID, UserID: u.ID, Ceremony: ceremony.Ceremony, Response: response}); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("an expired handle = %v, want ErrChallengeExpired", err)
	}
	reasons := []string{}
	for _, row := range h.rows(securityevent.TypePasskeyFailed) {
		reasons = append(reasons, row.Details["reason"].(string))
		if row.Details["ceremony"] != passkeyCeremonyRegistration || row.EventStatus != securityevent.StatusFailure {
			t.Errorf("failure row = %+v", row)
		}
	}
	if !slices.Equal(reasons, []string{"rejected", "rejected", "expired"}) {
		t.Fatalf("failure reasons = %v", reasons)
	}
}

func TestPasskeySignIn(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "signin@example.com")
	a := newPasskeyAuthenticator(true)
	p := h.enroll(t, a, sessionID, u.ID, "")

	pair, got, err := h.signIn(t, a)
	if err != nil || got.ID != u.ID {
		t.Fatalf("sign-in = %v user %q", err, got.ID)
	}
	if level := h.assurance(t, pair); level != session.AssuranceAAL2 {
		t.Fatalf("UV sign-in assurance = %q, want aal2", level)
	}
	sess, err := h.sess.Get(ctx, h.sessionOf(t, pair))
	if err != nil || sess.Authentication.Assurance != session.AssuranceAAL2 || sess.Authentication.Methods[0].Kind != session.MethodPasskey {
		t.Fatalf("session authentication = %+v, %v", sess.Authentication, err)
	}
	stored, _ := h.passkeys.GetByCredentialID(ctx, p.CredentialID)
	if stored.SignCount <= p.SignCount || stored.LastUsedAt == nil {
		t.Fatalf("use not recorded: %+v", stored)
	}
	rows := h.rows(securityevent.TypePasskeyLogin)
	if len(rows) != 1 || rows[0].UserID != u.ID || rows[0].Details["ceremony"] != "sign_in" || rows[0].Details["assurance"] != "aal2" || rows[0].Details["backup_state"] != false || len(rows[0].Details) != 3 {
		t.Fatalf("passkey_login rows = %+v", rows)
	}

	// The same credential without user verification signs in at AAL1.
	a.SetUserVerified(false)
	pair, _, err = h.signIn(t, a)
	if err != nil || h.assurance(t, pair) != session.AssuranceAAL1 {
		t.Fatalf("presence-only sign-in = %v assurance %q", err, h.assurance(t, pair))
	}
}

func TestPasskeySignInRefusals(t *testing.T) {
	ctx := context.Background()
	failure := func(t *testing.T, h *passkeyHarness, reason string) {
		t.Helper()
		rows := h.rows(securityevent.TypePasskeyFailed)
		if len(rows) == 0 || rows[len(rows)-1].Details["reason"] != reason || rows[len(rows)-1].UserID != "" || rows[len(rows)-1].Details["ceremony"] != "sign_in" {
			t.Fatalf("last passkey_failed = %+v, want reason %s with no user", rows, reason)
		}
	}
	t.Run("unknown credential", func(t *testing.T) {
		h := newPasskeyHarness(t)
		u, _, sessionID := h.account(t, "unknown@example.com")
		a := newPasskeyAuthenticator(true)
		p := h.enroll(t, a, sessionID, u.ID, "")
		h.passkeys.delete(p.ID)
		if _, _, err := h.signIn(t, a); !errors.Is(err, ErrPasskeyRejected) {
			t.Fatalf("err = %v", err)
		}
		failure(t, h, "unknown_credential")
	})
	t.Run("inactive", func(t *testing.T) {
		h := newPasskeyHarness(t)
		u, _, sessionID := h.account(t, "inactive@example.com")
		a := newPasskeyAuthenticator(true)
		h.enroll(t, a, sessionID, u.ID, "")
		h.users.mu.Lock()
		deactivated := h.users.byID[u.ID]
		deactivated.Status = user.StatusDeactivated
		h.users.byID[u.ID] = deactivated
		h.users.mu.Unlock()
		if _, _, err := h.signIn(t, a); !errors.Is(err, ErrPasskeyRejected) {
			t.Fatalf("err = %v", err)
		}
		failure(t, h, "inactive")
	})
	t.Run("removed between lookup and record", func(t *testing.T) {
		h := newPasskeyHarness(t)
		u, _, sessionID := h.account(t, "race-record@example.com")
		a := newPasskeyAuthenticator(true)
		p := h.enroll(t, a, sessionID, u.ID, "")
		h.passkeys.beforeRecordUse = func() { h.passkeys.delete(p.ID) }
		if _, _, err := h.signIn(t, a); !errors.Is(err, ErrPasskeyRejected) {
			t.Fatalf("err = %v", err)
		}
		failure(t, h, "conflict")
	})
	t.Run("removed between lookup and mint", func(t *testing.T) {
		h := newPasskeyHarness(t)
		u, _, sessionID := h.account(t, "race-mint@example.com")
		a := newPasskeyAuthenticator(true)
		h.enroll(t, a, sessionID, u.ID, "")
		h.passkeys.beforeRecordUse = func() {
			if err := h.users.applyRevision(u.ID, h.currentAuthRevision(u.ID)); err != nil {
				t.Error(err)
			}
		}
		before := h.sess.count()
		if _, _, err := h.signIn(t, a); !errors.Is(err, ErrPasskeyRejected) {
			t.Fatalf("err = %v", err)
		}
		failure(t, h, "conflict")
		if h.sess.count() != before {
			t.Fatal("a fenced mint created a session")
		}
	})
	t.Run("counter regressed", func(t *testing.T) {
		h := newPasskeyHarness(t)
		u, _, sessionID := h.account(t, "clone@example.com")
		a := newPasskeyAuthenticator(true)
		p := h.enroll(t, a, sessionID, u.ID, "")
		if _, _, err := h.signIn(t, a); err != nil {
			t.Fatal(err)
		}
		stored, _ := h.passkeys.GetByCredentialID(ctx, p.CredentialID)
		a.SetSignCount(0)
		if _, _, err := h.signIn(t, a); !errors.Is(err, ErrPasskeyRejected) {
			t.Fatalf("err = %v", err)
		}
		rows := h.rows(securityevent.TypePasskeySignCountRegressed)
		if len(rows) != 1 || rows[0].UserID != u.ID || rows[0].EventStatus != securityevent.StatusBlocked || rows[0].Details["passkey_id"] != p.ID || rows[0].Details["stored"] != stored.SignCount || rows[0].Details["presented"] != uint32(0) {
			t.Fatalf("regression rows = %+v", rows)
		}
		if kept, _ := h.passkeys.GetByCredentialID(ctx, p.CredentialID); kept.SignCount != stored.SignCount {
			t.Fatalf("stored count moved: %d → %d", stored.SignCount, kept.SignCount)
		}
	})
	t.Run("required verification", func(t *testing.T) {
		h := newPasskeyHarness(t)
		h.svc.passkeyUV = webauthn.Required
		u, _, sessionID := h.account(t, "uv@example.com")
		a := newPasskeyAuthenticator(true)
		h.enroll(t, a, sessionID, u.ID, "")
		a.SetUserVerified(false)
		if _, _, err := h.signIn(t, a); !errors.Is(err, ErrPasskeyRejected) {
			t.Fatalf("err = %v", err)
		}
		failure(t, h, "uv_required")
	})
}

func TestPasskeyStepUp(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, pair, sessionID := h.account(t, "stepup@example.com")
	other, _, otherSession := h.account(t, "stepup-other@example.com")
	first, second, theirs := newPasskeyAuthenticator(true), newPasskeyAuthenticator(true), newPasskeyAuthenticator(true)
	h.enroll(t, first, sessionID, u.ID, "")
	h.offset = 0
	result, err := h.stepUp(t, first, sessionID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	newSession := h.sessionOf(t, result.Tokens)
	replaced := h.sessionOf(t, pair)
	if result.Assurance != session.AssuranceAAL2 || h.assurance(t, result.Tokens) != session.AssuranceAAL2 || result.User.ID != u.ID || newSession == replaced {
		t.Fatalf("step-up = %+v", result)
	}
	if _, err := h.sess.Get(ctx, replaced); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("replaced session = %v, want deleted", err)
	}
	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("old refresh token = %v, want refused", err)
	}
	minted, _ := h.sess.Get(ctx, newSession)
	kinds := []session.MethodKind{}
	for _, m := range minted.Authentication.Methods {
		kinds = append(kinds, m.Kind)
	}
	if !slices.Equal(kinds, []session.MethodKind{session.MethodPassword, session.MethodPasskey}) {
		t.Fatalf("re-minted methods = %v", kinds)
	}
	rows := h.rows(securityevent.TypePasskeyLogin)
	if len(rows) != 1 || rows[0].Details["ceremony"] != "step_up" || rows[0].Details["assurance"] != "aal2" {
		t.Fatalf("passkey_login = %+v", rows)
	}

	// A second user-verified passkey of the same user steps up too.
	h.enroll(t, second, newSession, u.ID, "")
	if result, err := h.stepUp(t, second, newSession, u.ID); err != nil || result.Assurance != session.AssuranceAAL2 {
		t.Fatalf("second passkey step-up = %+v, %v", result, err)
	}

	// Another user's passkey is refused, attributed to the caller.
	h.enroll(t, theirs, otherSession, other.ID, "")
	current, _, err := h.svc.Login(ctx, "stepup@example.com", passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.stepUp(t, theirs, h.sessionOf(t, current), u.ID); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("another user's passkey = %v, want ErrPasskeyRejected", err)
	}
	failed := h.rows(securityevent.TypePasskeyFailed)
	if len(failed) != 1 || failed[0].UserID != u.ID || failed[0].Details["reason"] != "user_mismatch" || failed[0].Details["ceremony"] != "step_up" {
		t.Fatalf("passkey_failed = %+v", failed)
	}
}

func TestPasskeyStepUpKeepsTheSessionHorizon(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, pair, sessionID := h.account(t, "horizon@example.com")
	a := newPasskeyAuthenticator(false)
	h.enroll(t, a, sessionID, u.ID, "")
	before, _ := h.sess.Get(ctx, h.sessionOf(t, pair))
	h.offset = time.Hour
	result, err := h.stepUp(t, a, sessionID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := h.sess.Get(ctx, h.sessionOf(t, result.Tokens))
	if !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("ExpiresAt %v → %v, want unchanged", before.ExpiresAt, after.ExpiresAt)
	}
	if result.Assurance != session.AssuranceAAL1 || after.Authentication.Assurance != session.AssuranceAAL1 {
		t.Fatalf("presence-only step-up = %q / %q, want aal1", result.Assurance, after.Authentication.Assurance)
	}
}

func TestRemovePasskey(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "remove@example.com")
	a := newPasskeyAuthenticator(true)
	p := h.enroll(t, a, sessionID, u.ID, "")
	aal2, err := h.stepUp(t, a, sessionID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.enroll(t, newPasskeyAuthenticator(true), h.sessionOf(t, aal2.Tokens), u.ID, "")

	pair, _, err := h.svc.Login(ctx, "remove@example.com", passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	fresh := h.sessionOf(t, pair)
	if err := h.svc.RemovePasskey(ctx, PasskeyRemoveInput{SessionID: fresh, UserID: u.ID, PasskeyID: p.ID}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("removal at AAL1 = %v, want ErrStepUpRequired", err)
	}
	result, err := h.stepUp(t, a, fresh, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	stepped := h.sessionOf(t, result.Tokens)
	if err := h.svc.RemovePasskey(ctx, PasskeyRemoveInput{SessionID: stepped, UserID: u.ID, PasskeyID: "not-mine"}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("removing an unknown passkey = %v, want ErrNotFound", err)
	}
	notices := h.notices()
	if err := h.svc.RemovePasskey(ctx, PasskeyRemoveInput{SessionID: stepped, UserID: u.ID, PasskeyID: p.ID}); err != nil {
		t.Fatalf("removal at AAL2: %v", err)
	}
	if _, err := h.sess.Get(ctx, stepped); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("caller session after removal = %v, want revoked", err)
	}
	rows := h.rows(securityevent.TypePasskeyRemoved)
	if len(rows) != 1 || rows[0].Details["passkey_id"] != p.ID || rows[0].Details["remaining"] != 1 {
		t.Fatalf("passkey_removed = %+v", rows)
	}
	if h.notices() != notices+1 {
		t.Fatal("no passkey_change_notice for the removal")
	}
	if _, _, err := h.signIn(t, a); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("removed passkey sign-in = %v", err)
	}
}

func TestRemoveLastPasskeyKeepsALoginMethod(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "last@example.com")
	p := h.enroll(t, newPasskeyAuthenticator(false), sessionID, u.ID, "")
	h.pw.delete(u.ID)
	h.idents.mu.Lock()
	for id, it := range h.idents.byID {
		if it.UserID == u.ID {
			it.LoginEnabled = false
			h.idents.byID[id] = it
		}
	}
	h.idents.mu.Unlock()
	if err := h.svc.RemovePasskey(ctx, PasskeyRemoveInput{SessionID: sessionID, UserID: u.ID, PasskeyID: p.ID}); !errors.Is(err, credential.ErrNoLoginMethod) {
		t.Fatalf("removing the only login method = %v, want ErrNoLoginMethod", err)
	}
}

func TestResetUserPasskeys(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "reset@example.com")
	a := newPasskeyAuthenticator(true)
	h.enroll(t, a, sessionID, u.ID, "")
	aal2, err := h.stepUp(t, a, sessionID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionID = h.sessionOf(t, aal2.Tokens)
	h.enroll(t, newPasskeyAuthenticator(true), sessionID, u.ID, "")
	actor := Principal{Type: PrincipalUser, ID: "admin-1"}
	removed, err := h.svc.ResetUserPasskeys(ctx, actor, u.ID)
	if err != nil || removed != 2 {
		t.Fatalf("reset = %d, %v", removed, err)
	}
	if list, _ := h.passkeys.ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatalf("passkeys after reset = %d", len(list))
	}
	if _, err := h.sess.Get(ctx, sessionID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("session after reset = %v, want revoked", err)
	}
	rows := h.rows(securityevent.TypePasskeysReset)
	if len(rows) != 1 || rows[0].UserID != u.ID || rows[0].Actor.ID != "admin-1" || rows[0].Details["removed"] != 2 {
		t.Fatalf("passkeys_reset = %+v", rows)
	}
	// The user enrolls again from a fresh password login, at AAL1.
	pair, _, err := h.svc.Login(ctx, "reset@example.com", passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	h.enroll(t, newPasskeyAuthenticator(true), h.sessionOf(t, pair), u.ID, "")
}

// TestPasskeyRowsCarryNoCredentialMaterial scans every row the ceremonies wrote
// for a credential id, public key, user handle or passkey name.
func TestPasskeyRowsCarryNoCredentialMaterial(t *testing.T) {
	h := newPasskeyHarness(t)
	u, _, sessionID := h.account(t, "hygiene@example.com")
	a := newPasskeyAuthenticator(true)
	p := h.enroll(t, a, sessionID, u.ID, "Secret Laptop")
	if _, _, err := h.signIn(t, a); err != nil {
		t.Fatal(err)
	}
	a.SetSignCount(0)
	_, _, _ = h.signIn(t, a)
	needles := []string{p.Name, string(p.CredentialID), base64.RawURLEncoding.EncodeToString(p.CredentialID), base64.RawURLEncoding.EncodeToString(p.UserHandle), base64.RawURLEncoding.EncodeToString(p.PublicKey)}
	for _, evt := range h.events.recorded() {
		for k, v := range evt.Details {
			s, ok := v.(string)
			if !ok {
				continue
			}
			for _, needle := range needles {
				if needle != "" && strings.Contains(s, needle) {
					t.Errorf("%s Details[%q] carries credential material", evt.EventType, k)
				}
			}
		}
	}
}

func TestPasskeysUnavailable(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	if h.svc.PasskeysEnabled() {
		t.Fatal("PasskeysEnabled without a relying party")
	}
	if _, err := h.svc.BeginPasskeySignIn(ctx); !errors.Is(err, ErrPasskeysUnavailable) || !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("BeginPasskeySignIn = %v", err)
	}
	if _, err := h.svc.ListPasskeys(ctx, "u"); !errors.Is(err, ErrPasskeysUnavailable) {
		t.Fatalf("ListPasskeys = %v", err)
	}
}

// TestPasskeyAssuranceIsCappedByRegistration pins finding 1: a passkey
// registered without user verification never mints AAL2, even when a later
// assertion reports verification, and the enrollment gate keeps treating its
// holder as having no AAL2-capable passkey (bootstrap stays open).
func TestPasskeyAssuranceIsCappedByRegistration(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "capped@example.com")
	a := newPasskeyAuthenticator(false)
	h.enroll(t, a, sessionID, u.ID, "")
	a.SetUserVerified(true)
	pair, _, err := h.signIn(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if level := h.assurance(t, pair); level != session.AssuranceAAL1 {
		t.Fatalf("UV assertion of a presence-only registration = %q, want aal1", level)
	}
	if result, err := h.stepUp(t, a, sessionID, u.ID); err != nil || result.Assurance != session.AssuranceAAL1 {
		t.Fatalf("step-up = %+v, %v; want aal1", result, err)
	}
	fresh, _, err := h.svc.Login(ctx, "capped@example.com", passkeyPass)
	if err != nil {
		t.Fatal(err)
	}
	h.enroll(t, newPasskeyAuthenticator(true), h.sessionOf(t, fresh), u.ID, "bootstrap still open")
	rows := h.rows(securityevent.TypePasskeyLogin)
	for _, row := range rows {
		if row.Details["assurance"] != "aal1" {
			t.Fatalf("passkey_login = %+v, want aal1", row.Details)
		}
	}
}

// TestPasskeyRegistrationGuardAtCreate pins finding 3: the Begin-time tier and
// revision are re-checked when the passkey is stored.
func TestPasskeyRegistrationGuardAtCreate(t *testing.T) {
	ctx := context.Background()
	begin := func(t *testing.T, h *passkeyHarness, a *webauthntest.Authenticator, sessionID, userID string) (string, []byte) {
		t.Helper()
		ceremony, err := h.svc.BeginPasskeyRegistration(ctx, sessionID, userID)
		if err != nil {
			t.Fatal(err)
		}
		response, err := a.Register(ceremony.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		return ceremony.Ceremony, response
	}
	t.Run("bootstrap closed by a concurrent UV enrollment", func(t *testing.T) {
		h := newPasskeyHarness(t)
		u, _, attacker := h.account(t, "race-gate@example.com")
		pair, _, err := h.svc.Login(ctx, "race-gate@example.com", passkeyPass)
		if err != nil {
			t.Fatal(err)
		}
		ceremony, response := begin(t, h, newPasskeyAuthenticator(true), attacker, u.ID)
		h.enroll(t, newPasskeyAuthenticator(true), h.sessionOf(t, pair), u.ID, "victim")
		_, err = h.svc.FinishPasskeyRegistration(ctx, PasskeyRegistrationInput{SessionID: attacker, UserID: u.ID, Ceremony: ceremony, Response: response})
		if !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("a bootstrap Finish after a UV enrollment = %v, want ErrStepUpRequired", err)
		}
		if list, _ := h.passkeys.ListByUser(ctx, u.ID); len(list) != 1 {
			t.Fatalf("passkeys = %d, want only the victim's", len(list))
		}
		if rows := h.rows(securityevent.TypePasskeyFailed); len(rows) != 1 || rows[0].Details["reason"] != "conflict" || rows[0].Details["ceremony"] != "registration" {
			t.Fatalf("passkey_failed = %+v", rows)
		}
	})
	t.Run("revision moved between Begin and Create", func(t *testing.T) {
		h := newPasskeyHarness(t)
		u, _, sessionID := h.account(t, "race-revision@example.com")
		ceremony, response := begin(t, h, newPasskeyAuthenticator(true), sessionID, u.ID)
		if err := h.users.applyRevision(u.ID, h.currentAuthRevision(u.ID)); err != nil {
			t.Fatal(err)
		}
		_, err := h.svc.FinishPasskeyRegistration(ctx, PasskeyRegistrationInput{SessionID: sessionID, UserID: u.ID, Ceremony: ceremony, Response: response})
		if !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("Finish at a moved revision = %v, want ErrStepUpRequired", err)
		}
		if list, _ := h.passkeys.ListByUser(ctx, u.ID); len(list) != 0 {
			t.Fatalf("passkeys = %d, want none", len(list))
		}
	})
}

// TestPasskeyStepUpFencedOnTheFirstRead pins finding 5: step-up mints fenced on
// the revision read before the session check.
func TestPasskeyStepUpFencedOnTheFirstRead(t *testing.T) {
	h := newPasskeyHarness(t)
	u, _, sessionID := h.account(t, "stepup-fence@example.com")
	a := newPasskeyAuthenticator(true)
	h.enroll(t, a, sessionID, u.ID, "")
	h.passkeys.beforeRecordUse = func() {
		if err := h.users.applyRevision(u.ID, h.currentAuthRevision(u.ID)); err != nil {
			t.Error(err)
		}
	}
	if _, err := h.stepUp(t, a, sessionID, u.ID); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("step-up across a revision move = %v, want ErrPasskeyRejected", err)
	}
	if rows := h.rows(securityevent.TypePasskeyFailed); len(rows) != 1 || rows[0].Details["reason"] != "conflict" {
		t.Fatalf("passkey_failed = %+v", rows)
	}
}

// TestPresenceOnlyStepUpDropsTheAAL2Descriptor pins finding 7.
func TestPresenceOnlyStepUpDropsTheAAL2Descriptor(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "descriptors@example.com")
	verified, presence := newPasskeyAuthenticator(true), newPasskeyAuthenticator(false)
	h.enroll(t, verified, sessionID, u.ID, "")
	first, err := h.stepUp(t, verified, sessionID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	aal2 := h.sessionOf(t, first.Tokens)
	h.enroll(t, presence, aal2, u.ID, "")
	second, err := h.stepUp(t, presence, aal2, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	minted, _ := h.sess.Get(ctx, h.sessionOf(t, second.Tokens))
	for _, m := range minted.Authentication.Methods {
		if m.Kind == session.MethodPasskey {
			t.Fatalf("a presence-only step-up kept the AAL2 descriptor: %+v", minted.Authentication.Methods)
		}
	}
	if minted.Authentication.Assurance != session.AssuranceAAL1 || h.assurance(t, second.Tokens) != session.AssuranceAAL1 {
		t.Fatalf("presence-only step-up = %q, token %q", minted.Authentication.Assurance, h.assurance(t, second.Tokens))
	}
}

// TestResetUserPasskeysRetriesARace pins finding 8: the reset counts inside the
// Snapshot→Apply loop and survives one lost revision race.
func TestResetUserPasskeysRetriesARace(t *testing.T) {
	h := newPasskeyHarness(t)
	ctx := context.Background()
	u, _, sessionID := h.account(t, "reset-race@example.com")
	h.enroll(t, newPasskeyAuthenticator(false), sessionID, u.ID, "")
	h.enroll(t, newPasskeyAuthenticator(false), sessionID, u.ID, "")
	h.creds.beforeApply = func() {
		if err := h.users.applyRevision(u.ID, h.currentAuthRevision(u.ID)); err != nil {
			t.Error(err)
		}
	}
	removed, err := h.svc.ResetUserPasskeys(ctx, Principal{Type: PrincipalUser, ID: "admin"}, u.ID)
	if err != nil || removed != 2 {
		t.Fatalf("reset across one race = %d, %v; want 2", removed, err)
	}
	if list, _ := h.passkeys.ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatalf("passkeys after reset = %d", len(list))
	}
}

// TestPasskeyUserVerificationDiscouragedRefused pins finding 2.
func TestPasskeyUserVerificationDiscouragedRefused(t *testing.T) {
	d := constructorConfig{Passkey: PasskeyConfig{RelyingParty: struct{ webauthn.RelyingParty }{}, UserVerification: webauthn.Discouraged},
		Passkeys: newFakePasskeys(newFakeUsers()), CredentialMutations: &fakeCredentials{}, AuthenticationGrants: newFakeAuthGrants(),
		Challenges: newFakeChallenges(), Protector: newFakeProtector("k1", "k1")}
	err := validatePasskeys(d)
	if !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "no native AAL2 path") {
		t.Fatalf("discouraged = %v, want ErrInvalidInput naming the missing AAL2 path", err)
	}
	for _, uv := range []webauthn.Requirement{"", webauthn.Preferred, webauthn.Required} {
		d.Passkey.UserVerification = uv
		if err := validatePasskeys(d); err != nil {
			t.Errorf("%q refused: %v", uv, err)
		}
	}
}
