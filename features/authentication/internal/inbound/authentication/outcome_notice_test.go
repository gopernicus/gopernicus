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
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// PRG outcome notices: every completed auth mutation carries a closed outcome code on
// its 303/302 destination, and the destination GET maps that code — through a
// ROUTE-SPECIFIC whitelist — to canonical copy in the shared notice slot. These tests
// drive the round trip per code and pin the reader's closed posture: an unknown,
// malformed or wrong-destination code renders nothing and never reaches the body.
//
// They assert on the RENDERED body rather than diffing two GETs: every render mints a
// fresh CSRF token and CSP nonce, so two account pages are legitimately never
// byte-identical.

// noticeViews renders the notice slot INTO the response body (the marker-only
// stubViews hides it), so an assertion about what does and does not reach markup is
// about real output rather than a model field.
type noticeViews struct{ stubViews }

func (v noticeViews) AccountSecurity(m AccountSecurityPage) web.Renderer {
	return stubRenderer{"account MSG:" + m.Message}
}

func (v noticeViews) Login(m LoginPage) web.Renderer {
	return stubRenderer{"login MSG:" + m.Message}
}

// newNoticeFixture is the account-form harness rendering through noticeViews.
func newNoticeFixture(t *testing.T) accountFormFixture {
	t.Helper()
	return newAccountFormFixtureViews(t, noticeViews{})
}

// identifiedProvider is stubProvider carrying a provider-side account id, which an
// explicit link requires — the blank stub identity only reaches the route-level tests.
type identifiedProvider struct{ stubProvider }

func (identifiedProvider) GetUserInfo(context.Context, string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{ProviderUserID: "provider-user-notice", EmailVerified: true}, nil
}

// deliveredSecret recovers the out-of-band secret the challenge rail just minted for a
// user. The transport tests have no mailer to read, but memProtector's digests are
// deterministic ("hmac|key|user|purpose|code" for codes, "sha|token" for tokens), so
// the field after the last separator is what the message would have carried.
func (f accountFormFixture) deliveredSecret(t *testing.T, userID string) string {
	t.Helper()
	f.challenges.mu.Lock()
	defer f.challenges.mu.Unlock()
	for _, c := range f.challenges.byID {
		if userID != "" && c.UserID != userID {
			continue
		}
		if i := strings.LastIndex(c.SecretDigest, "|"); i >= 0 {
			return c.SecretDigest[i+1:]
		}
	}
	t.Fatalf("no challenge minted for user %q", userID)
	return ""
}

// assertRedirect pins the PRG destination a completed mutation carries.
func assertRedirect(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, status, rec.Body)
	}
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

// assertBody GETs a destination and pins its whole rendered body, which under
// noticeViews is exactly the page marker plus the notice slot.
func (f accountFormFixture) assertBody(t *testing.T, path, want string, cookies ...*http.Cookie) {
	t.Helper()
	rec := do(t, f.h, "GET", path, "", cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200; body=%s", path, rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != want {
		t.Fatalf("GET %s body = %q, want %q", path, got, want)
	}
}

// accountNotice is the body an account page renders for an outcome code.
func accountNotice(code string) string {
	return "PAGE:account MSG:" + accountOutcomes[code]
}

// loginNotice is the body a login page renders for an outcome code.
func loginNotice(code string) string {
	return "PAGE:login MSG:" + loginOutcomes[code]
}

// TestOutcomeVocabularyIsRouteSpecific unit-tests the pure code→copy mapper: each
// destination resolves only its OWN vocabulary, the other page's codes are unknown to
// it, and nothing outside either vocabulary resolves at all. The mapper never returns
// the submitted value, so no wire input can reach markup through it.
func TestOutcomeVocabularyIsRouteSpecific(t *testing.T) {
	accountCodes := []string{
		outcomePasswordChanged, outcomePasswordSet, outcomePasswordRemoved,
		outcomeIdentifierConfirmed, outcomeIdentifierRemoved, outcomeIdentifierUpdated,
		outcomeProviderLinked, outcomeProviderUnlinked,
	}
	loginCodes := []string{outcomeSignedOut, outcomePasswordReset}

	if len(accountOutcomes) != len(accountCodes) || len(loginOutcomes) != len(loginCodes) {
		t.Fatalf("vocabulary sizes = (%d,%d), want (%d,%d) — a new code needs a test",
			len(accountOutcomes), len(loginOutcomes), len(accountCodes), len(loginCodes))
	}

	for _, code := range accountCodes {
		if outcomeMessage(accountOutcomes, code) == "" {
			t.Errorf("account code %q resolved no copy", code)
		}
		if got := outcomeMessage(loginOutcomes, code); got != "" {
			t.Errorf("account code %q resolved %q on the LOGIN page, want nothing", code, got)
		}
	}
	for _, code := range loginCodes {
		if outcomeMessage(loginOutcomes, code) == "" {
			t.Errorf("login code %q resolved no copy", code)
		}
		if got := outcomeMessage(accountOutcomes, code); got != "" {
			t.Errorf("login code %q resolved %q on the ACCOUNT page, want nothing", code, got)
		}
	}

	// Absent, empty, near-miss, and hostile values are all simply unknown.
	for _, code := range []string{"", "link_sent", "password_changed ", "PASSWORD_CHANGED",
		"<script>alert(1)</script>", "password_changed&auth=signed_out", "../../etc/passwd"} {
		if got := outcomeMessage(accountOutcomes, code); got != "" {
			t.Errorf("outcomeMessage(account, %q) = %q, want nothing", code, got)
		}
		if got := outcomeMessage(loginOutcomes, code); got != "" {
			t.Errorf("outcomeMessage(login, %q) = %q, want nothing", code, got)
		}
	}
}

// TestAccountNoticeRejectsForeignCodes proves the closed reader end to end: a garbage,
// empty or login-page code renders the account page with an EMPTY notice slot, and the
// submitted value appears nowhere in the response body.
func TestAccountNoticeRejectsForeignCodes(t *testing.T) {
	f := newNoticeFixture(t)
	f.seedLoginUser("u-neg", "neg@example.com")
	sess := f.login(t, "neg@example.com")

	for _, raw := range []string{"", "nonsense", "<script>alert(1)</script>", "link_sent", outcomeSignedOut} {
		path := "/auth/account?auth=" + url.QueryEscape(raw)
		rec := do(t, f.h, "GET", path, "", sess)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body=%s", path, rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if body != "PAGE:account MSG:" {
			t.Errorf("GET %s body = %q, want an empty notice slot", path, body)
		}
		if raw != "" && strings.Contains(body, raw) {
			t.Errorf("GET %s echoed the submitted value %q into the body", path, raw)
		}
	}
}

// TestLoginNoticeRejectsForeignCodes is the login-page twin: an account-page code is
// unknown here, so the sign-in form renders no notice.
func TestLoginNoticeRejectsForeignCodes(t *testing.T) {
	f := newNoticeFixture(t)
	for _, raw := range []string{"", "nonsense", "<script>alert(1)</script>", outcomePasswordChanged, outcomeProviderLinked} {
		path := "/auth/login?auth=" + url.QueryEscape(raw)
		rec := do(t, f.h, "GET", path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body=%s", path, rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if body != "PAGE:login MSG:" {
			t.Errorf("GET %s body = %q, want an empty notice slot", path, body)
		}
		if raw != "" && strings.Contains(body, raw) {
			t.Errorf("GET %s echoed the submitted value %q into the body", path, raw)
		}
	}
}

// TestPasswordChangeNoticeRoundTrip — POST → 303 carrying the code → GET renders the
// copy.
func TestPasswordChangeNoticeRoundTrip(t *testing.T) {
	f := newNoticeFixture(t)
	f.seedLoginUser("u-pc", "pc@example.com")
	sess := f.login(t, "pc@example.com")

	rec := f.formCSRF(t, "/auth/password/change", url.Values{
		"current_password": {"password123456789"}, "password": {"newpassword123456"},
	}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, accountDone(outcomePasswordChanged))
	f.assertBody(t, rec.Header().Get("Location"), accountNotice(outcomePasswordChanged), sessionCookie(rec))
}

// TestPasswordRemoveAndSetNoticeRoundTrip drives the pair that can only be reached in
// sequence: removing the password leaves the account on its verified login identifier,
// and setting one again is then the no-password branch.
func TestPasswordRemoveAndSetNoticeRoundTrip(t *testing.T) {
	f := newNoticeFixture(t)
	f.seedLoginUser("u-pr", "pr@example.com")
	sess := f.login(t, "pr@example.com")

	start := f.formCSRF(t, "/auth/password/remove/start", url.Values{}, sess)
	assertRedirect(t, start, http.StatusSeeOther, "/auth/password/remove?sent=1")

	rec := f.formCSRF(t, "/auth/password/remove", url.Values{"code": {f.deliveredSecret(t, "u-pr")}}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, accountDone(outcomePasswordRemoved))
	sess = sessionCookie(rec)
	f.assertBody(t, rec.Header().Get("Location"), accountNotice(outcomePasswordRemoved), sess)

	rec = f.formCSRF(t, "/auth/password/set", url.Values{"password": {"newpassword123456"}}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, accountDone(outcomePasswordSet))
	f.assertBody(t, rec.Header().Get("Location"), accountNotice(outcomePasswordSet), sessionCookie(rec))
}

// TestIdentifierConfirmNoticeRoundTrip drives an identifier add through its delivered
// ownership-proof code to the account page.
func TestIdentifierConfirmNoticeRoundTrip(t *testing.T) {
	f := newNoticeFixture(t)
	f.seedLoginUser("u-ic", "ic@example.com")
	sess := f.login(t, "ic@example.com")

	start := f.formCSRF(t, "/auth/identifiers/email", url.Values{
		"value": {"added@example.com"}, "login": {"true"},
	}, sess)
	assertRedirect(t, start, http.StatusSeeOther, "/auth/identifiers/confirm?kind=email")

	rec := f.formCSRF(t, "/auth/identifiers/email/confirm", url.Values{
		"code": {f.deliveredSecret(t, "u-ic")},
	}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, accountDone(outcomeIdentifierConfirmed))
	f.assertBody(t, rec.Header().Get("Location"), accountNotice(outcomeIdentifierConfirmed), sess)
}

// TestIdentifierUpdateAndRemoveNoticeRoundTrip covers the two arms of the edit form:
// updating the uses, then removing an identifier that policy allows to go.
func TestIdentifierUpdateAndRemoveNoticeRoundTrip(t *testing.T) {
	f := newNoticeFixture(t)
	f.seedLoginUser("u-ie", "ie@example.com")
	sess := f.login(t, "ie@example.com")
	now := time.Now().UTC()
	f.idents.insert(identifier.Identifier{
		ID: "id-backup", UserID: "u-ie", Kind: identifier.KindEmail, NormalizedValue: "backup@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		CreatedAt: now, UpdatedAt: now,
	})

	rec := f.formCSRF(t, "/auth/identifiers/id-backup", url.Values{
		"login": {"true"}, "recovery": {"true"},
	}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, accountDone(outcomeIdentifierUpdated))
	f.assertBody(t, rec.Header().Get("Location"), accountNotice(outcomeIdentifierUpdated), sess)

	rec = f.formCSRF(t, "/auth/identifiers/id-backup", url.Values{"action": {"remove"}}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, accountDone(outcomeIdentifierRemoved))
	f.assertBody(t, rec.Header().Get("Location"), accountNotice(outcomeIdentifierRemoved), sess)
}

// TestOAuthLinkAndUnlinkNoticeRoundTrip is the owner's named case: an explicit link
// completes at the OAuth CALLBACK (a 302, not a form PRG), which now carries the same
// outcome code; the code-gated unlink then carries its own.
func TestOAuthLinkAndUnlinkNoticeRoundTrip(t *testing.T) {
	f := newAccountFormFixtureViews(t, noticeViews{}, identifiedProvider{})
	f.seedLoginUser("u-ol", "ol@example.com")
	sess := f.login(t, "ol@example.com")

	start := do(t, f.h, "GET", "/auth/oauth/google/link/start?redirect="+url.QueryEscape(accountPath), "", sess)
	if start.Code != http.StatusFound {
		t.Fatalf("link/start = %d, want 302; body=%s", start.Code, start.Body)
	}
	authorize, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	state := authorize.Query().Get("state")
	if state == "" {
		t.Fatal("authorize URL carried no state")
	}

	cb := do(t, f.h, "GET", "/auth/oauth/google/callback?code=x&state="+url.QueryEscape(state), "", sess)
	assertRedirect(t, cb, http.StatusFound, accountDone(outcomeProviderLinked))
	f.assertBody(t, cb.Header().Get("Location"), accountNotice(outcomeProviderLinked), sess)

	unlinkStart := f.formCSRF(t, "/auth/oauth/google/unlink/start", url.Values{}, sess)
	assertRedirect(t, unlinkStart, http.StatusSeeOther, "/auth/oauth/google/unlink?sent=1")

	rec := f.formCSRF(t, "/auth/oauth/google/unlink", url.Values{
		"code": {f.deliveredSecret(t, "u-ol")},
	}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, accountDone(outcomeProviderUnlinked))
	f.assertBody(t, rec.Header().Get("Location"), accountNotice(outcomeProviderUnlinked), sess)
}

// TestSignedOutNoticeRoundTrip — the form logout lands on a login page that says so.
func TestSignedOutNoticeRoundTrip(t *testing.T) {
	f := newNoticeFixture(t)
	f.seedLoginUser("u-so", "so@example.com")
	sess := f.login(t, "so@example.com")

	rec := f.formCSRF(t, "/auth/logout", url.Values{}, sess)
	assertRedirect(t, rec, http.StatusSeeOther, loginDone(outcomeSignedOut))
	f.assertBody(t, rec.Header().Get("Location"), loginNotice(outcomeSignedOut))
}

// TestPasswordResetNoticeRoundTrip — a reset revokes every session and does not
// auto-login, so the login page it lands on names the completed reset.
func TestPasswordResetNoticeRoundTrip(t *testing.T) {
	f := newNoticeFixture(t)
	f.seedLoginUser("u-pw", "pw@example.com")
	// The mailed reset token is minted straight from the challenge rail: the fixture's
	// delivery queue is a stub, so the forgot POST enqueues nothing to read back.
	token, err := f.svc.IssueChallenge(context.Background(), "u-pw", challenge.PurposePasswordReset)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}

	rec := f.formCSRF(t, "/auth/password/reset", url.Values{
		"token": {token}, "password": {"newpassword123456"},
	}, &http.Cookie{Name: "x", Value: "y"})
	assertRedirect(t, rec, http.StatusSeeOther, loginDone(outcomePasswordReset))
	f.assertBody(t, rec.Header().Get("Location"), loginNotice(outcomePasswordReset))
}

// TestLinkedRedirect pins the callback augmentation: it mirrors pendingLinkRedirect
// (existing query and fragment survive, an unparseable destination is returned
// unchanged) and names NO provider — the destination lists that itself.
func TestLinkedRedirect(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		wantPath  string
		wantFrag  string
		wantQuery map[string]string
	}{
		{
			name:      "empty target uses the same-origin default",
			target:    "",
			wantPath:  "/",
			wantQuery: map[string]string{"auth": outcomeProviderLinked},
		},
		{
			name:      "existing query is preserved",
			target:    "https://app.example.com/welcome?app=console",
			wantPath:  "/welcome",
			wantQuery: map[string]string{"app": "console", "auth": outcomeProviderLinked},
		},
		{
			name:      "existing fragment is preserved",
			target:    "/auth/account#methods",
			wantPath:  accountPath,
			wantFrag:  "methods",
			wantQuery: map[string]string{"auth": outcomeProviderLinked},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linkedRedirect(tt.target)
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("linkedRedirect(%q) = %q, unparseable: %v", tt.target, got, err)
			}
			if u.Path != tt.wantPath {
				t.Errorf("path = %q, want %q (from %q)", u.Path, tt.wantPath, got)
			}
			if u.Fragment != tt.wantFrag {
				t.Errorf("fragment = %q, want %q (from %q)", u.Fragment, tt.wantFrag, got)
			}
			q := u.Query()
			if len(q) != len(tt.wantQuery) {
				t.Errorf("query %v, want %v (from %q)", q, tt.wantQuery, got)
			}
			for k, want := range tt.wantQuery {
				if q.Get(k) != want {
					t.Errorf("query[%q] = %q, want %q (from %q)", k, q.Get(k), want, got)
				}
			}
			if q.Has("provider") {
				t.Errorf("linkedRedirect named a provider in %q, want the code alone", got)
			}
		})
	}
}
