package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

var _ contactchange.Repository = (*memContactChanges)(nil)

// memContactChanges is the inbound test's contactchange.Repository: replace-per-
// (user, kind) Create, single-use get-and-delete Consume.
type memContactChanges struct {
	mu  sync.Mutex
	m   map[string]contactchange.PendingChange
	seq int
}

func (f *memContactChanges) Create(_ context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	p.ID = "cc" + itoa(f.seq)
	f.m[p.UserID+"|"+string(p.Kind)] = p
	return p, nil
}

func (f *memContactChanges) Consume(_ context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := userID + "|" + string(kind)
	p, ok := f.m[k]
	if !ok {
		return contactchange.PendingChange{}, sdk.ErrNotFound
	}
	delete(f.m, k)
	if p.Expired(time.Now()) {
		return contactchange.PendingChange{}, sdk.ErrExpired
	}
	return p, nil
}

// identifierFixture is the transport harness for the identifier-management routes: a
// real authsvc.Service over the mem stores with the contact-change rail, the
// credential-mutation rail, the step-up grant store, and the browser-safe-mutation
// policy configured for the session cookie.
type identifierFixture struct {
	h         http.Handler
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
}

func newIdentifierFixture(t *testing.T) identifierFixture {
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
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, nil, "", MutationSecurity{
		AllowedOrigins:    []string{"https://app.example.com"},
		SessionCookieName: svc.SessionCookieName(),
	}, nil, nil)
	return identifierFixture{h: h, users: users, idents: idents, passwords: passwords}
}

// seedLoginUser inserts a user with a password and a verified login+recovery+
// notification primary email so /auth/token resolves it and the recent-login shortcut
// covers a following mutation.
func (f identifierFixture) seedLoginUser(userID, email string) {
	now := time.Now().UTC()
	f.users.mu.Lock()
	f.users.byID[userID] = user.User{ID: userID, DisplayName: "ID"}
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

func (f identifierFixture) bearerFor(t *testing.T, email string) string {
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

func TestIdentifierRoutesRequireLiveSession(t *testing.T) {
	f := newIdentifierFixture(t)
	cases := []struct{ method, path string }{
		{"POST", "/auth/identifiers/email"},
		{"POST", "/auth/identifiers/email/confirm"},
		{"POST", "/auth/identifiers/phone"},
		{"POST", "/auth/identifiers/phone/confirm"},
		{"PATCH", "/auth/identifiers/id-1"},
		{"DELETE", "/auth/identifiers/id-1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated status = %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

func TestIdentifierStartCookieRequiresCSRF(t *testing.T) {
	f := newIdentifierFixture(t)
	f.seedLoginUser("u-csrf", "csrf@example.com")
	rec := do(t, f.h, "POST", "/auth/login", `{"email":"csrf@example.com","password":"password123456789"}`)
	cookie := sessionCookie(rec)
	if cookie == nil {
		t.Fatal("login set no session cookie")
	}
	r := httptest.NewRequest("POST", "/auth/identifiers/email", strings.NewReader(`{"email":"new@example.com","uses":{"notification":true},"make_primary":false}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	got := httptest.NewRecorder()
	f.h.ServeHTTP(got, r)
	if got.Code != http.StatusForbidden {
		t.Fatalf("cookie start without CSRF status = %d, want 403; body=%s", got.Code, got.Body)
	}
}

func TestIdentifierEmailStartOverBearer(t *testing.T) {
	f := newIdentifierFixture(t)
	f.seedLoginUser("u-start", "start@example.com")
	token := f.bearerFor(t, "start@example.com")

	rec := jsonBearer(f.h, "/auth/identifiers/email", token,
		`{"email":"added@example.com","uses":{"notification":true},"make_primary":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("email start status = %d, want 200; body=%s", rec.Code, rec.Body)
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
		t.Fatal("email start returned no delivery receipt")
	}
}

func TestIdentifierPhoneStartKindNotSupported(t *testing.T) {
	f := newIdentifierFixture(t) // no phone notifier wired
	f.seedLoginUser("u-phone", "phone@example.com")
	token := f.bearerFor(t, "phone@example.com")

	rec := jsonBearer(f.h, "/auth/identifiers/phone", token,
		`{"phone":"+15551234567","uses":{"notification":true},"make_primary":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("phone start with no notifier status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "kind_not_supported" {
		t.Fatalf("error code = %q, want kind_not_supported", body.Code)
	}
}

func TestIdentifierDeleteOverBearer(t *testing.T) {
	f := newIdentifierFixture(t)
	f.seedLoginUser("u-del", "del@example.com")
	// A second verified login+recovery email so the primary removal leaves a login set.
	now := time.Now().UTC()
	second := f.idents.insert(identifier.Identifier{
		UserID: "u-del", Kind: identifier.KindEmail, NormalizedValue: "second@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	token := f.bearerFor(t, "del@example.com")

	rec := jsonBearerMethod(f.h, "DELETE", "/auth/identifiers/"+second.ID, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

// jsonBearerMethod is jsonBearer for a non-POST method with an optional body.
func jsonBearerMethod(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
