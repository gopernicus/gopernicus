package authentication

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// resetCaptureViews records the ResetPage the reset handler renders so a test can assert
// the retained token the marker-only stubViews would hide.
type resetCaptureViews struct {
	stubViews
	page *ResetPage
}

func (v resetCaptureViews) ResetPassword(m ResetPage) web.Renderer {
	*v.page = m
	return v.stubViews.ResetPassword(m)
}

// newResetRetainFixture builds a service with the password-reset rail wired, a capturing
// Views, and a buffered logger, returning the handler, the service (to mint a real reset
// token), the captured ResetPage, and the log buffer.
func newResetRetainFixture(t *testing.T) (http.Handler, *authsvc.Service, *ResetPage, *bytes.Buffer) {
	t.Helper()
	users := newMemUsers()
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:             users,
		Identifiers:       newMemIdentifiers(users),
		Passwords:         passwords,
		Sessions:          sessions,
		Challenges:        challenges,
		Protector:         memProtector{},
		PasswordResets:    &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:            fakeHasher{},
		Deliver:           router,
		Queue:             stubQueue{},
		Limiter:           ratelimiter.NewMemory(),
		Cookie:            authsvc.CookieConfig{},
		TokenSigner:       newFakeSigner(),
		PublicAuthBaseURL: "https://auth.example.com",
	})
	page := &ResetPage{}
	buf := &bytes.Buffer{}
	h := web.NewWebHandler(web.WithLogging(slog.New(slog.NewTextHandler(buf, nil))))
	Mount(h, svc, nil, nil, "", MutationSecurity{}, resetCaptureViews{page: page})
	return h, svc, page, buf
}

// TestResetFormRetainsTokenAcrossErrorRerender is the IX-11 hermetic regression: a
// validation-error re-render of the reset form retains the SUBMITTED token so a corrected
// retry can succeed against a still-valid reset — and the token never reaches the logs.
func TestResetFormRetainsTokenAcrossErrorRerender(t *testing.T) {
	h, svc, page, buf := newResetRetainFixture(t)

	token, err := svc.IssueChallenge(context.Background(), "u-reset", challenge.PurposePasswordReset)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}

	// Step A: a valid token with a too-short password fails password validation BEFORE the
	// token is redeemed, so the form re-renders and must carry the submitted token back.
	postReset(t, h, token, "short")
	if page.Token != token {
		t.Fatalf("error re-render dropped the token: got %q, want the submitted token", page.Token)
	}

	// Step B: the SAME still-valid token with a valid password now succeeds (303 PRG).
	recB := postReset(t, h, token, "newpassword123456")
	if recB.Code != http.StatusSeeOther {
		t.Fatalf("corrected retry = %d, want 303; body=%s", recB.Code, recB.Body)
	}

	// The token must never appear in server logs.
	if strings.Contains(buf.String(), token) {
		t.Fatalf("reset token leaked into server logs: %s", buf.String())
	}
}

func postReset(t *testing.T, h http.Handler, token, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := url.Values{"token": {token}, "password": {password}}.Encode()
	r := httptest.NewRequest("POST", "/auth/password/reset", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
