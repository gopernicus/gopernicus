package authenticationhttp

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
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
func newResetRetainFixture(t *testing.T) (http.Handler, *testService, *ResetPage, *bytes.Buffer) {
	t.Helper()
	users := newMemUsers()
	users.byID["u-reset"] = user.User{ID: "u-reset"}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	idents := newMemIdentifiers(users)
	idents.insert(identifier.Identifier{ID: "reset-recovery", UserID: "u-reset", Kind: identifier.KindEmail,
		NormalizedValue: "reset@example.com", VerifiedAt: time.Now(), RecoveryEnabled: true})
	router, err := delivery.NewRouter(nopMailer{},
		delivery.WithMailFrom("noreply@example.com"))
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := newServiceWithFakes(authenticationFixture{
		Users:             users,
		Identifiers:       idents,
		Passwords:         passwords,
		Sessions:          sessions,
		Challenges:        challenges,
		Protector:         memProtector{},
		PasswordResets:    &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:            fakeHasher{},
		Deliver:           router,
		Queue:             stubQueue{},
		Limiter:           ratelimiter.NewMemory(),
		TokenSigner:       newFakeSigner(),
		PublicAuthBaseURL: "https://auth.example.com",
	})
	page := &ResetPage{}
	buf := &bytes.Buffer{}
	h := web.NewWebHandler()
	h.Use(web.Logger(slog.New(slog.NewTextHandler(buf, nil))))
	mount(h, mountDeps{Auth: svc, Views: resetCaptureViews{page: page}})
	return h, svc, page, buf
}

// TestResetFormRetainsTokenAcrossErrorRerender is the IX-11 hermetic regression: a
// validation-error re-render of the reset form retains the SUBMITTED token so a corrected
// retry can succeed against a still-valid reset — and the token never reaches the logs.
func TestResetFormRetainsTokenAcrossErrorRerender(t *testing.T) {
	h, svc, page, buf := newResetRetainFixture(t)

	env, deliverable, err := svc.initializer.Initialize(context.Background(), sdk.AddressKindEmail, delivery.PurposePasswordReset, delivery.Envelope{ResolutionInput: "reset@example.com"})
	if err != nil {
		t.Fatalf("initialize reset delivery: %v", err)
	}
	if !deliverable {
		t.Fatal("reset fixture did not resolve a deliverable account")
	}
	token := env.Secret

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
