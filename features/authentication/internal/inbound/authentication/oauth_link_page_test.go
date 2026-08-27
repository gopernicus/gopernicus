package authentication

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// The bundled HTML completion of the anti-takeover pending-link branch: the public
// GET /auth/oauth/link landing (registered only inside the OAuth branch of mountHTML)
// and the form arm of POST /auth/oauth/verify-link, which completes the link over the
// same service method the JSON arm calls and lands on the account page.

// matchingEmailProvider is a provider whose verified email matches an existing
// account, so the callback resolves to the PENDING-LINK branch (design §5.7) — the
// branch the landing page exists to complete.
type matchingEmailProvider struct{ stubProvider }

func (matchingEmailProvider) GetUserInfo(context.Context, string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{ProviderUserID: "provider-user-1", Email: pendingLinkEmail, EmailVerified: true}, nil
}

const pendingLinkEmail = "pending@example.com"

// pendingLinkFixture mounts the HTML surface over a real oauth-enabled service whose
// provider returns a verified email, so a test can drive the whole anti-takeover
// branch: start → callback (mails the secret) → the landing form POST.
type pendingLinkFixture struct {
	h        http.Handler
	states   *memOAuthStates
	accounts *memOAuthAccounts
}

func newPendingLinkFixture(t *testing.T, views Views) pendingLinkFixture {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	states := &memOAuthStates{m: map[string]oauthstate.State{}}
	accounts := &memOAuthAccounts{}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:             users,
		Identifiers:       idents,
		Passwords:         &memPasswords{m: map[string]string{}},
		Sessions:          &memSessions{m: map[string]session.Session{}},
		Challenges:        &memChallenges{byID: map[string]challenge.Challenge{}},
		Protector:         memProtector{},
		Hasher:            fakeHasher{},
		Deliver:           router,
		Queue:             stubQueue{},
		Limiter:           ratelimiter.NewMemory(),
		Cookie:            authsvc.CookieConfig{},
		TokenSigner:       newFakeSigner(),
		OAuthAccounts:     accounts,
		OAuthStates:       states,
		Providers:         []oauth.Provider{matchingEmailProvider{}},
		OAuthCallbackBase: "https://app.example.com",
	})
	now := time.Now().UTC()
	users.mu.Lock()
	users.byID["u-pending"] = user.User{ID: "u-pending", DisplayName: "Pending"}
	users.mu.Unlock()
	idents.insert(identifier.Identifier{
		ID: "id-pending", UserID: "u-pending", Kind: identifier.KindEmail, NormalizedValue: pendingLinkEmail,
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
	h := web.NewWebHandler()
	Mount(h, Deps{Auth: svc, Mutation: MutationSecurity{AllowedOrigins: []string{"https://app.example.com"}}, Views: views})
	return pendingLinkFixture{h: h, states: states, accounts: accounts}
}

// pendingLinkToken drives start + callback and returns the single-use secret the
// pending-link mail carries (read from the state store, which is where the mailed
// token lives).
func (f pendingLinkFixture) pendingLinkToken(t *testing.T) string {
	t.Helper()
	start := do(t, f.h, "GET", "/auth/oauth/google/start", "")
	if start.Code != http.StatusFound {
		t.Fatalf("oauth start = %d, want 302", start.Code)
	}
	u, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	flowState := u.Query().Get("state")
	if flowState == "" {
		t.Fatal("authorize URL carried no state")
	}
	cb := do(t, f.h, "GET", "/auth/oauth/google/callback?code=x&state="+url.QueryEscape(flowState), "")
	if cb.Code != http.StatusFound {
		t.Fatalf("oauth callback = %d, want 302; body=%s", cb.Code, cb.Body)
	}
	if loc := cb.Header().Get("Location"); !strings.Contains(loc, "auth=link_sent") {
		t.Fatalf("callback Location = %q, want the pending-link outcome", loc)
	}
	f.states.mu.Lock()
	defer f.states.mu.Unlock()
	for token, st := range f.states.m {
		if st.Purpose == oauthstate.PurposePendingLink {
			return token
		}
	}
	t.Fatal("callback minted no pending-link state")
	return ""
}

// postForm submits a urlencoded body (the HTML transport) with an optional Origin.
func (f pendingLinkFixture) postForm(t *testing.T, path string, form url.Values, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

// TestOAuthLinkLandingIsPublic proves the landing renders WITHOUT a session: the
// caller completing an anti-takeover link holds a mailed secret, not a cookie, so a
// live-session gate would make the branch uncompletable in a browser.
func TestOAuthLinkLandingIsPublic(t *testing.T) {
	f := newPendingLinkFixture(t, stubViews{})
	rec := do(t, f.h, "GET", "/auth/oauth/link", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/oauth/link = %d, want 200 (public); body=%s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); !strings.Contains(got, "PAGE:oauth_link") {
		t.Errorf("GET /auth/oauth/link body = %q, want it rendered through the port", got)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("public landing redirected to %q, want a rendered page", loc)
	}
}

// TestOAuthLinkLandingDenyByAbsence proves the landing is registered only inside the
// OAuth branch of mountHTML: with Views wired but no provider it does not exist.
func TestOAuthLinkLandingDenyByAbsence(t *testing.T) {
	users := newMemUsers()
	svc := authsvc.NewService(authsvc.Deps{
		Users:       users,
		Identifiers: newMemIdentifiers(users),
		Passwords:   &memPasswords{m: map[string]string{}},
		Sessions:    &memSessions{m: map[string]session.Session{}},
		Hasher:      fakeHasher{},
		Limiter:     ratelimiter.NewMemory(),
		Cookie:      authsvc.CookieConfig{},
		TokenSigner: newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, Deps{Auth: svc, Views: stubViews{}})
	if rec := do(t, h, "GET", "/auth/oauth/link", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /auth/oauth/link with no provider = %d, want 404", rec.Code)
	}
}

// TestOAuthVerifyLinkFormCompletesToAccountPage drives the whole anti-takeover branch
// through the HTML transport: the landing's form POST completes the link over the same
// VerifyLink service method the JSON arm calls, mints the session, and 303s to the
// account page where the new link now appears in the masked inventory.
func TestOAuthVerifyLinkFormCompletesToAccountPage(t *testing.T) {
	f := newPendingLinkFixture(t, stubViews{})
	token := f.pendingLinkToken(t)

	rec := f.postForm(t, "/auth/oauth/verify-link", url.Values{"token": {token}}, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("verify-link form = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if want := accountDone(outcomeProviderLinked); rec.Header().Get("Location") != want {
		t.Errorf("verify-link form Location = %q, want %q", rec.Header().Get("Location"), want)
	}
	if sessionCookie(rec) == nil {
		t.Error("verify-link form set no session cookie")
	}
	linked, err := f.accounts.ListByUser(context.Background(), "u-pending")
	if err != nil || len(linked) != 1 {
		t.Fatalf("links after form completion = %v (err %v), want exactly one", linked, err)
	}
}

// TestOAuthVerifyLinkFormRejectsCrossSiteOrigin proves the form arm carries the
// credential-establishment origin policy: a cross-site submit is refused before the
// token is ever consumed.
func TestOAuthVerifyLinkFormRejectsCrossSiteOrigin(t *testing.T) {
	f := newPendingLinkFixture(t, stubViews{})
	token := f.pendingLinkToken(t)

	rec := f.postForm(t, "/auth/oauth/verify-link", url.Values{"token": {token}}, "https://evil.example.com")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site verify-link form = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	// The token survives an origin rejection — nothing was consumed.
	if again := f.postForm(t, "/auth/oauth/verify-link", url.Values{"token": {token}}, ""); again.Code != http.StatusSeeOther {
		t.Errorf("same-origin retry after rejection = %d, want 303 (the token was not consumed)", again.Code)
	}
}

// TestOAuthVerifyLinkFormInvalidTokenReRenders proves a dead/unknown secret re-renders
// the landing at the mapped status with generic copy and echoes no token — the secret
// is single-use, so there is nothing to retain.
func TestOAuthVerifyLinkFormInvalidTokenReRenders(t *testing.T) {
	f := newPendingLinkFixture(t, stubViews{})
	rec := f.postForm(t, "/auth/oauth/verify-link", url.Values{"token": {"not-a-real-token"}}, "")
	if rec.Code == http.StatusSeeOther {
		t.Fatal("verify-link form with a dead token = 303, want a re-rendered landing")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("verify-link form dead-token status = %d, want 404 (the mapped domain status)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "PAGE:oauth_link") {
		t.Errorf("body = %q, want the landing re-rendered through the port", body)
	}
	if strings.Contains(body, "not-a-real-token") {
		t.Error("re-render echoed the submitted token")
	}
	if sessionCookie(rec) != nil {
		t.Error("a failed completion set a session cookie")
	}
}

// TestOAuthVerifyLinkFormCarriesGenericCopy proves the re-rendered landing carries the
// generic, enumeration-resistant message (never the raw service error).
func TestOAuthVerifyLinkFormCarriesGenericCopy(t *testing.T) {
	captured := &capturedModels{}
	f := newPendingLinkFixture(t, captureLinkViews{c: captured})
	f.postForm(t, "/auth/oauth/verify-link", url.Values{"token": {"not-a-real-token"}}, "")
	if captured.oauthLink.Message != linkErrMsg {
		t.Errorf("re-rendered landing Message = %q, want the generic copy %q", captured.oauthLink.Message, linkErrMsg)
	}
	if captured.oauthLink.RedeemPath != verifyLinkPath {
		t.Errorf("re-rendered landing RedeemPath = %q, want %q", captured.oauthLink.RedeemPath, verifyLinkPath)
	}
}

// TestOAuthVerifyLinkJSONArmUnchanged proves the dispatch left the JSON contract
// alone: a JSON body still completes the link and returns the user JSON body (never a
// 303), so existing API clients are unaffected by the HTML arm.
func TestOAuthVerifyLinkJSONArmUnchanged(t *testing.T) {
	f := newPendingLinkFixture(t, stubViews{})
	token := f.pendingLinkToken(t)
	rec := do(t, f.h, "POST", "/auth/oauth/verify-link", `{"token":"`+token+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify-link JSON = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("verify-link JSON content-type = %q, want JSON", ct)
	}
	if sessionCookie(rec) == nil {
		t.Error("verify-link JSON set no session cookie")
	}
}

// TestOAuthVerifyLinkFormWithoutViews415 proves the API-only posture: with a nil Views
// a form body has no page to render, so the dispatcher answers 415 rather than
// inventing an HTML surface.
func TestOAuthVerifyLinkFormWithoutViews415(t *testing.T) {
	f := newPendingLinkFixture(t, nil)
	rec := f.postForm(t, "/auth/oauth/verify-link", url.Values{"token": {"x"}}, "")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form verify-link with nil Views = %d, want 415", rec.Code)
	}
}

// captureLinkViews records the pending-link landing model so a test can assert the
// generic re-render copy the marker-only stubViews would hide.
type captureLinkViews struct {
	stubViews
	c *capturedModels
}

func (v captureLinkViews) OAuthLinkLanding(m OAuthLinkPage) web.Renderer {
	v.c.oauthLink = m
	return v.stubViews.OAuthLinkLanding(m)
}
