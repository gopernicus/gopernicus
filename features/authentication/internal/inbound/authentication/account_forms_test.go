package authentication

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Account-security HTML form tests (AV3-8.4). They prove the form arm of the account
// dispatch: the body double-submit CSRF gate, the stale-session login redirect, the
// recent-auth step-up redirect, safe PRG on success, masked page output, the failed
// re-render status, and the generic re-render on a rejected code.

// capturedModels records the view models the account handlers render, so a test can
// assert masked/generic output the marker-only stubViews would hide.
type capturedModels struct {
	account AccountSecurityPage
	stepUp  StepUpPage
	oauth   OAuthUnlinkPage
	idForm  IdentifierFormPage
	pwForm  PasswordFormPage
}

// captureViews wraps stubViews, recording the account page models before delegating
// to the marker renderer.
type captureViews struct {
	stubViews
	c *capturedModels
}

func (v captureViews) AccountSecurity(m AccountSecurityPage) web.Renderer {
	v.c.account = m
	return v.stubViews.AccountSecurity(m)
}
func (v captureViews) StepUp(m StepUpPage) web.Renderer {
	v.c.stepUp = m
	return v.stubViews.StepUp(m)
}
func (v captureViews) OAuthUnlink(m OAuthUnlinkPage) web.Renderer {
	v.c.oauth = m
	return v.stubViews.OAuthUnlink(m)
}
func (v captureViews) IdentifierForm(m IdentifierFormPage) web.Renderer {
	v.c.idForm = m
	return v.stubViews.IdentifierForm(m)
}
func (v captureViews) PasswordForm(m PasswordFormPage) web.Renderer {
	v.c.pwForm = m
	return v.stubViews.PasswordForm(m)
}

// accountFormFixture is the transport harness for the account-security HTML forms: a
// real authsvc.Service over the full mem rail (identifiers, contact-change, credential
// mutations, step-up grants, OAuth) mounted with a capturing Views.
type accountFormFixture struct {
	h         http.Handler
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	sessions  *memSessions
	grants    *memAuthGrants
	cap       *capturedModels
}

func newAccountFormFixture(t *testing.T) accountFormFixture {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	grants := &memAuthGrants{m: map[string]authgrant.Grant{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:                users,
		Identifiers:          idents,
		Passwords:            passwords,
		Sessions:             sessions,
		Challenges:           challenges,
		Protector:            memProtector{},
		PasswordResets:       &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		ContactChanges:       &memContactChanges{m: map[string]contactchange.PendingChange{}},
		CredentialMutations:  &memCredentialMutations{users: users, idents: idents, passwords: passwords},
		AuthenticationGrants: grants,
		Hasher:               fakeHasher{},
		Deliver:              router,
		Queue:                stubQueue{},
		Limiter:              ratelimiter.NewMemory(),
		Cookie:               authsvc.CookieConfig{},
		TokenSigner:          newFakeSigner(),
		OAuthAccounts:        &memOAuthAccounts{},
		OAuthStates:          &memOAuthStates{m: map[string]oauthstate.State{}},
		Providers:            []oauth.Provider{stubProvider{}},
		OAuthCallbackBase:    "https://app.example.com",
		PublicAuthBaseURL:    "https://auth.example.com",
	})
	cap := &capturedModels{}
	h := web.NewWebHandler()
	Mount(h, svc, nil, nil, "", MutationSecurity{
		AllowedOrigins:    []string{"https://app.example.com"},
		SessionCookieName: svc.SessionCookieName(),
	}, captureViews{c: cap})
	return accountFormFixture{h: h, users: users, idents: idents, passwords: passwords, sessions: sessions, grants: grants, cap: cap}
}

// ageRecentAuth pushes every session's recorded primary-authentication time far into
// the past so the recent-login shortcut no longer satisfies a sensitive mutation —
// the session stays live, but a step-up grant is now required (design §5.0).
func (f accountFormFixture) ageRecentAuth() {
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	for id, s := range f.sessions.m {
		s.Authentication.AuthenticatedAt = time.Now().Add(-365 * 24 * time.Hour)
		f.sessions.m[id] = s
	}
}

// seedLoginUser inserts a user with a password and a verified login+recovery+
// notification primary email so cookie login resolves it.
func (f accountFormFixture) seedLoginUser(userID, email string) {
	now := time.Now().UTC()
	f.users.mu.Lock()
	f.users.byID[userID] = user.User{ID: userID, DisplayName: "AF"}
	f.users.mu.Unlock()
	f.passwords.mu.Lock()
	f.passwords.m[userID] = "hash:password123456789"
	f.passwords.mu.Unlock()
	f.idents.insert(identifier.Identifier{
		ID: "id-" + userID, UserID: userID, Kind: identifier.KindEmail, NormalizedValue: email,
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
}

// login logs the seeded email in over the cookie lane and returns the session cookie.
func (f accountFormFixture) login(t *testing.T, email string) *http.Cookie {
	t.Helper()
	rec := do(t, f.h, "POST", "/auth/login", `{"email":"`+email+`","password":"password123456789"}`)
	c := sessionCookie(rec)
	if c == nil {
		t.Fatalf("login set no session cookie; status=%d body=%s", rec.Code, rec.Body)
	}
	return c
}

// formCSRF POSTs a urlencoded body with a matching auth_csrf cookie + csrf_token field
// (a valid double-submit) plus the given session cookie.
func (f accountFormFixture) formCSRF(t *testing.T, path string, form url.Values, sess *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf_token", "tok")
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(sess)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

// formNoCSRF POSTs a urlencoded body with the session cookie but no CSRF token.
func (f accountFormFixture) formNoCSRF(t *testing.T, path string, form url.Values, sess *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(sess)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

func TestAccountFormRequiresFormCSRF(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-csrf", "csrf@example.com")
	sess := f.login(t, "csrf@example.com")
	rec := f.formNoCSRF(t, "/auth/password/change", url.Values{
		"current_password": {"password123456789"}, "password": {"newpassword123456"},
	}, sess)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("form change without csrf_token = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}

func TestAccountFormChangePasswordPRG(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-chg", "chg@example.com")
	sess := f.login(t, "chg@example.com")
	rec := f.formCSRF(t, "/auth/password/change", url.Values{
		"current_password": {"password123456789"}, "password": {"newpassword123456"},
	}, sess)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form change = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != accountPath {
		t.Errorf("form change Location = %q, want %q", loc, accountPath)
	}
	if sessionCookie(rec) == nil {
		t.Error("form change set no fresh session cookie")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("form change redirect missing Cache-Control: no-store")
	}
}

func TestAccountFormChangePasswordWrongCurrentReRenders(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-wrong", "wrong@example.com")
	sess := f.login(t, "wrong@example.com")
	rec := f.formCSRF(t, "/auth/password/change", url.Values{
		"current_password": {"totally-wrong-pass"}, "password": {"newpassword123456"},
	}, sess)
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("form change with wrong current = 303, want a re-rendered form")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("form change wrong-current status = %d, want 401 (mapped domain status)", rec.Code)
	}
	if f.cap.pwForm.Message == "" {
		t.Error("re-rendered password form carried no generic message")
	}
	if strings.Contains(rec.Body.String(), "password123456789") {
		t.Error("re-render leaked a password value")
	}
}

// TestAccountFormSetPasswordAlreadySetReRenders proves a set-password form on an
// account that already has one re-renders at the mapped 409 with generic copy — the
// error-renderer-status path — rather than redirecting or echoing a secret.
func TestAccountFormSetPasswordAlreadySetReRenders(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-set", "set@example.com")
	sess := f.login(t, "set@example.com")
	rec := f.formCSRF(t, "/auth/password/set", url.Values{"password": {"newpassword123456"}}, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("form set on already-set account = %d, want 409 re-render; body=%s", rec.Code, rec.Body)
	}
	if f.cap.pwForm.Mode != "set" || f.cap.pwForm.Message == "" {
		t.Errorf("re-rendered set form = %+v, want mode=set with a generic message", f.cap.pwForm)
	}
	if strings.Contains(rec.Body.String(), "newpassword123456") {
		t.Error("re-render leaked the submitted password")
	}
}

func TestAccountFormIdentifierAddStepUpRedirect(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-idsu", "idsu@example.com")
	sess := f.login(t, "idsu@example.com")
	// Aged session: adding an identifier requires a change_email recent-auth grant the
	// caller no longer has, so the form redirects to step-up.
	f.ageRecentAuth()
	rec := f.formCSRF(t, "/auth/identifiers/email", url.Values{
		"value": {"new@example.com"}, "login": {"true"},
	}, sess)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form add-identifier without grant = %d, want 303 to step-up; body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "purpose="+authgrant.PurposeChangeEmail) {
		t.Errorf("step-up redirect %q missing the change_email purpose binding", loc)
	}
}

// TestAccountFormIdentifierAddPRG proves a recent-auth-satisfied caller (fresh login)
// adds an identifier through the same StartIdentifierChange service the JSON arm uses
// and lands on the kind-bound confirm page via 303, with no grant spent.
func TestAccountFormIdentifierAddPRG(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-idok", "idok@example.com")
	sess := f.login(t, "idok@example.com")
	rec := f.formCSRF(t, "/auth/identifiers/email", url.Values{
		"value": {"added@example.com"}, "login": {"true"}, "recovery": {"true"},
	}, sess)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form add-identifier = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/auth/identifiers/confirm?kind=email" {
		t.Errorf("form add-identifier Location = %q, want the kind-bound confirm page", loc)
	}
}

func TestAccountPageStaleSessionRedirectsToLogin(t *testing.T) {
	f := newAccountFormFixture(t)
	rec := do(t, f.h, "GET", "/auth/account", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("stale-session GET /auth/account = %d, want 303 to login", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/auth/login") {
		t.Fatalf("stale-session redirect Location = %q, want the login page", loc)
	}
	if !strings.Contains(loc, "return_to=%2Fauth%2Faccount") {
		t.Errorf("stale-session redirect %q missing the validated safe return-to", loc)
	}
}

func TestAccountPageMasksActor(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-mask", "masked@example.com")
	sess := f.login(t, "masked@example.com")
	rec := do(t, f.h, "GET", "/auth/account", "", sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/account = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if f.cap.account.Actor == "" {
		t.Fatal("account page rendered no masked actor")
	}
	if strings.Contains(f.cap.account.Actor, "masked@example.com") {
		t.Errorf("account actor %q leaked the full address, want it masked", f.cap.account.Actor)
	}
}

func TestAccountFormOAuthUnlinkBadCodeReRenders(t *testing.T) {
	f := newAccountFormFixture(t)
	f.seedLoginUser("u-unl", "unl@example.com")
	sess := f.login(t, "unl@example.com")
	// A rejected/mismatched code (here: no matching challenge, the same failure a
	// wrong-provider code produces at consume) re-renders generically, never a 303 to
	// the account page and never revealing why.
	rec := f.formCSRF(t, "/auth/oauth/google/unlink", url.Values{
		"provider": {"google"}, "code": {"000000"},
	}, sess)
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("unlink with bad code = 303, want a re-rendered confirmation")
	}
	if f.cap.oauth.Provider != "google" {
		t.Errorf("re-rendered unlink provider = %q, want google", f.cap.oauth.Provider)
	}
	if f.cap.oauth.Message == "" {
		t.Error("re-rendered unlink carried no generic message")
	}
}

// TestAccountFailureMapping proves the account error mapper keeps enumeration-resistant
// generic copy for a collision and the actionable policy copy for a last-method
// refusal, never surfacing the raw error.
func TestAccountFailureMapping(t *testing.T) {
	if st, msg := accountFailure(sdk.ErrAlreadyExists); st != http.StatusConflict || msg != accountErrMsg {
		t.Errorf("accountFailure(ErrAlreadyExists) = (%d,%q), want (409,%q)", st, msg, accountErrMsg)
	}
	if _, msg := accountFailure(credential.ErrNoLoginMethod); msg != accountPolicyMsg {
		t.Errorf("accountFailure(ErrNoLoginMethod) msg = %q, want the policy copy", msg)
	}
}

// TestSafeRelativePath proves the stale-session return-to validator accepts only safe
// same-origin relative paths and rejects open-redirect vectors.
func TestSafeRelativePath(t *testing.T) {
	for _, ok := range []string{"/auth/account", "/auth/identifiers/id1/edit"} {
		if safeRelativePath(ok) != ok {
			t.Errorf("safeRelativePath(%q) rejected a safe relative path", ok)
		}
	}
	for _, bad := range []string{"", "https://evil.example.com", "//evil.example.com", "/\\evil.example.com", "http://x/y"} {
		if got := safeRelativePath(bad); got != "" {
			t.Errorf("safeRelativePath(%q) = %q, want rejected", bad, got)
		}
	}
}
