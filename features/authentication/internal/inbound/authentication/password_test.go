package authentication

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// passwordFixture is the transport harness for the credential-suite password
// routes: a real authsvc.Service over the mem stores with the credential-mutation
// rail, the step-up grant store, the challenge rail, and the browser-safe-mutation
// policy configured for the session cookie.
type passwordFixture struct {
	h         http.Handler
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	grants    *memAuthGrants
}

func newPasswordFixture(t *testing.T) passwordFixture {
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
		CredentialMutations:  &memCredentialMutations{users: users, idents: idents, passwords: passwords},
		AuthenticationGrants: grants,
		Hasher:               fakeHasher{},
		Deliver:              router,
		Queue:                stubQueue{},
		Limiter:              ratelimiter.NewMemory(),
		Cookie:               authsvc.CookieConfig{},
		TokenSigner:          newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, nil, "", MutationSecurity{
		AllowedOrigins:    []string{"https://app.example.com"},
		SessionCookieName: svc.SessionCookieName(),
	}, nil)
	return passwordFixture{h: h, users: users, idents: idents, passwords: passwords, grants: grants}
}

// seedLoginUser inserts a user with a password and a verified login+recovery+
// notification primary email so /auth/token resolves it and remove-password can
// select a verified recovery identifier.
func (f passwordFixture) seedLoginUser(userID, email string) {
	now := time.Now().UTC()
	f.users.mu.Lock()
	f.users.byID[userID] = user.User{ID: userID, DisplayName: "PW"}
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

// bearerFor logs the seeded email in over /auth/token and returns the access token.
func (f passwordFixture) bearerFor(t *testing.T, email string) string {
	t.Helper()
	rec := do(t, f.h, "POST", "/auth/token", `{"email":"`+email+`","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d; body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	return resp.AccessToken
}

func TestPasswordRoutesRequireLiveSession(t *testing.T) {
	f := newPasswordFixture(t)
	for _, path := range []string{"/auth/password/set", "/auth/password/remove/start", "/auth/password/remove"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status = %d, want 401", path, rec.Code)
		}
	}
}

func TestPasswordSetCookieRequiresCSRF(t *testing.T) {
	f := newPasswordFixture(t)
	f.seedLoginUser("u-csrf", "csrf@example.com")
	// Log in over the cookie lane to obtain a live session cookie.
	rec := do(t, f.h, "POST", "/auth/login", `{"email":"csrf@example.com","password":"password123456789"}`)
	cookie := sessionCookie(rec)
	if cookie == nil {
		t.Fatal("login set no session cookie")
	}
	r := httptest.NewRequest("POST", "/auth/password/set", strings.NewReader(`{"new_password":"password123456789"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	got := httptest.NewRecorder()
	f.h.ServeHTTP(got, r)
	if got.Code != http.StatusForbidden {
		t.Fatalf("cookie set-password without CSRF status = %d, want 403; body=%s", got.Code, got.Body)
	}
}

func TestPasswordSetAlreadySetOverBearer(t *testing.T) {
	f := newPasswordFixture(t)
	f.seedLoginUser("u-set", "set@example.com")
	token := f.bearerFor(t, "set@example.com")

	// Earn a set_password grant by re-verifying the existing password.
	grantRec := jsonBearer(f.h, "/auth/step-up/password", token,
		`{"purpose":"`+authgrant.PurposeSetPassword+`","context":"","password":"password123456789"}`)
	if grantRec.Code != http.StatusOK {
		t.Fatalf("step-up status = %d; body=%s", grantRec.Code, grantRec.Body)
	}

	rec := jsonBearer(f.h, "/auth/password/set", token, `{"new_password":"password123456789"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("set on already-set account status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "password_already_set" {
		t.Fatalf("error code = %q, want password_already_set", body.Code)
	}
}

func TestPasswordRemoveStartOverBearer(t *testing.T) {
	f := newPasswordFixture(t)
	f.seedLoginUser("u-rem", "rem@example.com")
	token := f.bearerFor(t, "rem@example.com")

	rec := jsonBearer(f.h, "/auth/password/remove/start", token, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove/start status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		Status  string `json:"status"`
		Receipt string `json:"receipt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode start body: %v", err)
	}
	if body.Receipt == "" {
		t.Fatal("remove/start returned no delivery receipt")
	}
}
