package authentication

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

const signInPassword = "password123456789"

// signInRecorder is a BeforeSession hook that records each call and answers
// with err.
type signInRecorder struct {
	mu       sync.Mutex
	calls    []SignIn
	deadline time.Duration
	err      error
}

func (r *signInRecorder) hook(ctx context.Context, in SignIn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, in)
	if d, ok := ctx.Deadline(); ok {
		r.deadline = time.Until(d)
	}
	return r.err
}

func (r *signInRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *signInRecorder) last(t *testing.T) SignIn {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatal("BeforeSession was not called")
	}
	return r.calls[len(r.calls)-1]
}

// refusals are the hook errors a sign-in can see and how each must surface.
var refusals = []struct {
	name    string
	err     error
	kind    error
	status  string
	reason  string
	browser bool
}{
	{"forbidden", fmt.Errorf("left the organization: %w", sdk.ErrForbidden), sdk.ErrForbidden, securityevent.StatusBlocked, securityevent.ReasonHostRefused, true},
	{"conflict", fmt.Errorf("address reused: %w", sdk.ErrConflict), sdk.ErrConflict, securityevent.StatusBlocked, securityevent.ReasonHostRefused, true},
	{"unkinded", errors.New("host database down"), nil, securityevent.StatusFailure, "", false},
}

// requireHookRow asserts the last eventType row carries status, reason (""
// asserts no reason key) and userID.
func requireHookRow(t *testing.T, spy *spySecurityEvents, eventType, status, reason, userID string) {
	t.Helper()
	if reason != "" {
		requireDenial(t, spy, eventType, status, reason, userID)
		return
	}
	var found *securityevent.SecurityEvent
	for _, evt := range spy.recorded() {
		if evt.EventType == eventType {
			evt := evt
			found = &evt
		}
	}
	if found == nil {
		t.Fatalf("no %s row recorded", eventType)
	}
	if _, has := found.Details["reason"]; found.EventStatus != status || has || found.UserID != userID {
		t.Fatalf("%s row = %s reason=%v user=%q, want %s with no reason for %q", eventType, found.EventStatus, found.Details["reason"], found.UserID, status, userID)
	}
}

func requireRefused(t *testing.T, err error, kind error) {
	t.Helper()
	if !errors.Is(err, ErrSignInRefusedByHook) {
		t.Fatalf("err = %v, want ErrSignInRefusedByHook", err)
	}
	if kind != nil && !errors.Is(err, kind) {
		t.Fatalf("err = %v, want the hook's kind %v", err, kind)
	}
}

func TestBeforeSessionPasswordSignIn(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{securityevent.TypeLogin, securityevent.TypeTokenIssued} {
		signIn := func(h *harness) error {
			if op == securityevent.TypeLogin {
				_, _, err := h.svc.Login(ctx, "staff@example.com", signInPassword)
				return err
			}
			_, err := h.svc.IssueToken(ctx, "staff@example.com", signInPassword)
			return err
		}
		t.Run(op+"/admitted", func(t *testing.T) {
			h := newHarness(t, nil)
			u := h.mustRegister(t, "staff@example.com", signInPassword)
			h.mustVerify(t, "staff@example.com")
			rec := &signInRecorder{}
			h.svc.beforeSession = rec.hook
			if err := signIn(h); err != nil {
				t.Fatalf("sign-in: %v", err)
			}
			got := rec.last(t)
			if got.UserID != u.ID || got.Method != session.MethodPassword || got.Created {
				t.Errorf("SignIn = %+v, want user %s, password, not created", got, u.ID)
			}
			if len(got.Identifiers) != 1 || got.Identifiers[0] != (sdk.IdentityAddress{Kind: sdk.AddressKindEmail, Value: "staff@example.com"}) {
				t.Errorf("Identifiers = %+v, want the verified email", got.Identifiers)
			}
			if rec.deadline <= 0 || rec.deadline > SignInHookTimeout {
				t.Errorf("hook deadline = %v, want within SignInHookTimeout", rec.deadline)
			}
		})
		t.Run(op+"/wrong password never calls the hook", func(t *testing.T) {
			h := newHarness(t, nil)
			h.mustRegister(t, "staff@example.com", signInPassword)
			rec := &signInRecorder{}
			h.svc.beforeSession = rec.hook
			if _, _, err := h.svc.Login(ctx, "staff@example.com", "wrong-password-0451"); err == nil {
				t.Fatal("wrong password signed in")
			}
			if rec.count() != 0 {
				t.Error("BeforeSession ran before the credential proof")
			}
		})
		for _, tc := range refusals {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				h := newHarness(t, nil)
				u := h.mustRegister(t, "staff@example.com", signInPassword)
				rec := &signInRecorder{err: tc.err}
				h.svc.beforeSession = rec.hook
				before := h.sess.count()
				err := signIn(h)
				requireRefused(t, err, tc.kind)
				if IsSignInRefusal(err) != tc.browser {
					t.Errorf("IsSignInRefusal = %v, want %v", IsSignInRefusal(err), tc.browser)
				}
				if h.sess.count() != before {
					t.Error("a refused sign-in minted a session")
				}
				requireHookRow(t, h.events, op, tc.status, tc.reason, u.ID)
			})
		}
	}
}

// TestBeforeSessionSkipsNonSignIns pins the exclusions: a refresh and a
// password change re-mint for someone already signed in and never consult the
// hook, even one that refuses everything.
func TestBeforeSessionSkipsNonSignIns(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	u := h.mustRegister(t, "staff@example.com", signInPassword)
	pair, _, err := h.svc.Login(ctx, "staff@example.com", signInPassword)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	rec := &signInRecorder{err: sdk.ErrForbidden}
	h.svc.beforeSession = rec.hook

	if _, err := h.svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := h.svc.ChangePassword(ctx, u.ID, signInPassword, "another-password-0451"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if rec.count() != 0 {
		t.Errorf("BeforeSession ran %d times on a refresh or password change", rec.count())
	}
}

func TestBeforeSessionOAuthExistingLink(t *testing.T) {
	ctx := context.Background()
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvider{name: "google", trust: true, providerUserID: "g-1", email: "staff@example.com", emailVerified: true}
			h := newOAuthHarness(t, p, nil)
			first, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(h.startState(t, "")))
			if err != nil || first.Action != ActionRegister {
				t.Fatalf("seed registration: action=%q err=%v", first.Action, err)
			}
			rec := &signInRecorder{err: tc.err}
			h.svc.beforeSession = rec.hook
			before := h.sess.count()

			_, err = h.svc.OAuthCallback(ctx, "google", h.callbackRequest(h.startState(t, "https://app.example.com/welcome")))
			requireRefused(t, err, tc.kind)
			var refusal *SignInRefusal
			if errors.As(err, &refusal) != tc.browser {
				t.Fatalf("SignInRefusal = %v, want %v (err %v)", refusal != nil, tc.browser, err)
			}
			if tc.browser && refusal.RedirectTo != "https://app.example.com/welcome" {
				t.Errorf("RedirectTo = %q, want the flow's destination", refusal.RedirectTo)
			}
			if h.sess.count() != before {
				t.Error("a refused OAuth sign-in minted a session")
			}
			got := rec.last(t)
			if got.UserID != first.User.ID || got.Method != session.MethodOAuth || got.Created {
				t.Errorf("SignIn = %+v, want the linked user, oauth, not created", got)
			}
			requireHookRow(t, h.events, securityevent.TypeOAuthLogin, tc.status, tc.reason, first.User.ID)
		})
	}
}

// TestBeforeSessionOAuthRegistration pins Created on the registration branch,
// and that a refused registration keeps the user and its link so the next
// sign-in arrives through the existing link with Created false.
func TestBeforeSessionOAuthRegistration(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-new", email: "new@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	rec := &signInRecorder{err: sdk.ErrForbidden}
	h.svc.beforeSession = rec.hook

	_, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(h.startState(t, "")))
	var refusal *SignInRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a SignInRefusal", err)
	}
	created := rec.last(t)
	if !created.Created || created.Method != session.MethodOAuth {
		t.Errorf("SignIn = %+v, want Created on the registration", created)
	}
	if len(created.Identifiers) != 1 || created.Identifiers[0].Value != "new@example.com" {
		t.Errorf("Identifiers = %+v, want the provider-verified email", created.Identifiers)
	}
	if h.sess.count() != 0 {
		t.Error("a refused registration minted a session")
	}
	requireDenial(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusBlocked, securityevent.ReasonHostRefused, created.UserID)

	rec.err = nil
	res, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(h.startState(t, "")))
	if err != nil || res.Action != ActionLogin {
		t.Fatalf("second sign-in: action=%q err=%v, want login through the kept link", res.Action, err)
	}
	if again := rec.last(t); again.Created || again.UserID != created.UserID {
		t.Errorf("second SignIn = %+v, want the same user with Created false", again)
	}
}

func TestBeforeSessionOAuthVerifyLink(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-link", email: "linked@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "linked@example.com")
	token := h.completePendingLink(t)
	rec := &signInRecorder{err: sdk.ErrForbidden}
	h.svc.beforeSession = rec.hook

	_, err := h.svc.VerifyLink(ctx, token)
	requireRefused(t, err, sdk.ErrForbidden)
	if h.sess.count() != 0 {
		t.Error("a refused verify-link minted a session")
	}
	if got := rec.last(t); got.UserID != u.ID || got.Created {
		t.Errorf("SignIn = %+v, want the existing user, not created", got)
	}
	requireDenial(t, h.events, securityevent.TypeOAuthLinkVerified, securityevent.StatusBlocked, securityevent.ReasonHostRefused, u.ID)
}

func TestBeforeSessionPasskeySignIn(t *testing.T) {
	h := newPasskeyHarness(t)
	u, _, sessionID := h.account(t, "keys@example.com")
	a := newPasskeyAuthenticator(true)
	h.enroll(t, a, sessionID, u.ID, "laptop")
	rec := &signInRecorder{err: fmt.Errorf("shared mailbox: %w", sdk.ErrForbidden)}
	h.svc.beforeSession = rec.hook
	before := h.sess.count()

	_, _, err := h.signIn(t, a)
	requireRefused(t, err, sdk.ErrForbidden)
	if h.sess.count() != before {
		t.Error("a refused passkey sign-in minted a session")
	}
	if got := rec.last(t); got.UserID != u.ID || got.Method != session.MethodPasskey {
		t.Errorf("SignIn = %+v, want the passkey owner and the UV method", got)
	}
	requireDenial(t, h.events, securityevent.TypePasskeyFailed, securityevent.StatusBlocked, securityevent.ReasonHostRefused, u.ID)

	// A step-up re-mints for a signed-in session and never consults the hook.
	calls := rec.count()
	if _, err := h.stepUp(t, a, sessionID, u.ID); err != nil {
		t.Fatalf("stepUp: %v", err)
	}
	if rec.count() != calls {
		t.Error("BeforeSession ran on a passkey step-up")
	}
}

func TestBeforeSessionPasswordlessCode(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "otp@example.com", signInPassword)
	h.mustVerify(t, "otp@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	rec := &signInRecorder{err: sdk.ErrConflict}
	h.svc.beforeSession = rec.hook

	code := startPasswordlessCode(t, h, string(identifier.KindEmail), "otp@example.com", "otp@example.com")
	before := h.sess.count()
	_, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "otp@example.com", code)
	requireRefused(t, err, sdk.ErrConflict)
	if h.sess.count() != before {
		t.Error("a refused passwordless code minted a session")
	}
	if got := rec.last(t); got.UserID != u.ID || got.Method != session.MethodEmailCode {
		t.Errorf("SignIn = %+v, want the email_code method", got)
	}
	requireDenial(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusBlocked, securityevent.ReasonHostRefused, u.ID)
}
