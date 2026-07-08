package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// var _ pins the seam: *authsvc.Service is the concrete authService the router
// mounts.
var _ authService = (*authsvc.Service)(nil)

// --- minimal fakes (transport tests wire a real authsvc.Service over these) ---

type fakeHasher struct{}

func (fakeHasher) HashPassword(pw string) (string, error) { return "hash:" + pw, nil }
func (fakeHasher) VerifyPassword(hash, pw string) error {
	if hash == "hash:"+pw {
		return nil
	}
	return errors.New("mismatch")
}

type nopMailer struct{}

func (nopMailer) Send(context.Context, email.Message) error { return nil }

type memUsers struct {
	mu   sync.Mutex
	byID map[string]user.User
}

func (m *memUsers) Create(_ context.Context, u user.User) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.byID {
		if strings.EqualFold(ex.Email, u.Email) {
			return user.User{}, errs.ErrAlreadyExists
		}
	}
	m.byID[u.ID] = u
	return u, nil
}
func (m *memUsers) Get(_ context.Context, id string) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return user.User{}, errs.ErrNotFound
	}
	return u, nil
}
func (m *memUsers) GetByEmail(_ context.Context, e string) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.byID {
		if strings.EqualFold(u.Email, e) {
			return u, nil
		}
	}
	return user.User{}, errs.ErrNotFound
}
func (m *memUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return user.User{}, errs.ErrNotFound
	}
	m.byID[id] = u
	return u, nil
}

type memPasswords struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *memPasswords) Set(_ context.Context, id, h string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[id] = h
	return nil
}
func (p *memPasswords) Get(_ context.Context, id string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.m[id]
	if !ok {
		return "", errs.ErrNotFound
	}
	return h, nil
}

type memSessions struct {
	mu sync.Mutex
	m  map[string]session.Session
}

func (s *memSessions) Create(_ context.Context, sess session.Session) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sess.Token] = sess
	return sess, nil
}
func (s *memSessions) Get(_ context.Context, token string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[token]
	if !ok {
		return session.Session{}, errs.ErrNotFound
	}
	if sess.Expired(time.Now()) {
		return session.Session{}, errs.ErrExpired
	}
	return sess, nil
}
func (s *memSessions) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[token]; !ok {
		return errs.ErrNotFound
	}
	delete(s.m, token)
	return nil
}
func (s *memSessions) DeleteByUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, sess := range s.m {
		if sess.UserID == userID {
			delete(s.m, token)
		}
	}
	return nil
}

type memCodes struct {
	mu sync.Mutex
	m  map[string]verification.Code
}

func (c *memCodes) Create(_ context.Context, v verification.Code) (verification.Code, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[v.Code] = v
	return v, nil
}
func (c *memCodes) Get(_ context.Context, code string) (verification.Code, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[code]
	if !ok {
		return verification.Code{}, errs.ErrNotFound
	}
	if v.Expired(time.Now()) {
		return verification.Code{}, errs.ErrExpired
	}
	return v, nil
}
func (c *memCodes) Delete(_ context.Context, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[code]; !ok {
		return errs.ErrNotFound
	}
	delete(c.m, code)
	return nil
}

type memTokens struct {
	mu sync.Mutex
	m  map[string]verification.Token
}

func (tk *memTokens) Create(_ context.Context, v verification.Token) (verification.Token, error) {
	tk.mu.Lock()
	defer tk.mu.Unlock()
	tk.m[v.Token] = v
	return v, nil
}
func (tk *memTokens) Get(_ context.Context, token string) (verification.Token, error) {
	tk.mu.Lock()
	defer tk.mu.Unlock()
	v, ok := tk.m[token]
	if !ok {
		return verification.Token{}, errs.ErrNotFound
	}
	if v.Expired(time.Now()) {
		return verification.Token{}, errs.ErrExpired
	}
	return v, nil
}
func (tk *memTokens) Delete(_ context.Context, token string) error {
	tk.mu.Lock()
	defer tk.mu.Unlock()
	if _, ok := tk.m[token]; !ok {
		return errs.ErrNotFound
	}
	delete(tk.m, token)
	return nil
}

// newTestHandler builds a real authsvc.Service over in-memory fakes, mounts the
// routes on a web.WebHandler, and returns the handler. limiter nil → memory.
func newTestHandler(t *testing.T, limiter ratelimiter.Limiter) http.Handler {
	t.Helper()
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:     &memUsers{byID: map[string]user.User{}},
		Passwords: &memPasswords{m: map[string]string{}},
		Sessions:  &memSessions{m: map[string]session.Session{}},
		Codes:     &memCodes{m: map[string]verification.Code{}},
		Tokens:    &memTokens{m: map[string]verification.Token{}},
		Hasher:    fakeHasher{},
		Mailer:    nopMailer{},
		MailFrom:  "noreply@example.com",
		Limiter:   limiter,
		Cookie:    authsvc.CookieConfig{},
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil)
	return h
}

func do(t *testing.T, h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	return nil
}

func TestRegisterRoute(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{"email":"a@example.com","password":"password123","display_name":"A"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["email"] != "a@example.com" {
		t.Errorf("email = %v", resp["email"])
	}
}

func TestRegisterRouteBadJSON(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", rec.Code)
	}
}

func TestRegisterRouteShortPassword(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/register", `{"email":"b@example.com","password":"short","display_name":"B"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("short password status = %d, want 400", rec.Code)
	}
}

func TestLoginRouteSetsCookie(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"c@example.com","password":"password123","display_name":"C"}`)
	rec := do(t, h, "POST", "/auth/login", `{"email":"c@example.com","password":"password123"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if sessionCookie(rec) == nil {
		t.Error("login did not set a session cookie")
	}
}

func TestLoginRouteWrongPassword(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"d@example.com","password":"password123","display_name":"D"}`)
	rec := do(t, h, "POST", "/auth/login", `{"email":"d@example.com","password":"wrongpass"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rec.Code)
	}
}

func TestLoginRouteRateLimited(t *testing.T) {
	h := newTestHandler(t, denyLimiter{})
	rec := do(t, h, "POST", "/auth/login", `{"email":"e@example.com","password":"password123"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited status = %d, want 429", rec.Code)
	}
}

func TestVerifyRouteUnknown(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/verify", `{"code":"nope"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("verify unknown status = %d, want 404", rec.Code)
	}
}

func TestForgotPasswordRouteAlwaysAccepted(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/forgot", `{"email":"ghost@example.com"}`)
	if rec.Code != http.StatusAccepted {
		t.Errorf("forgot status = %d, want 202", rec.Code)
	}
}

func TestResetPasswordRouteUnknownToken(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/reset", `{"token":"nope","password":"newpassword"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("reset unknown token status = %d, want 404", rec.Code)
	}
}

func TestLogoutRouteRequiresSession(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/logout", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("logout without session status = %d, want 401", rec.Code)
	}
}

func TestLogoutRouteClearsSession(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"f@example.com","password":"password123","display_name":"F"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"f@example.com","password":"password123"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}

	rec := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", rec.Code)
	}
	cleared := sessionCookie(rec)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: %+v", cleared)
	}

	// The now-deleted session no longer authorizes logout.
	again := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: c.Name, Value: c.Value})
	if again.Code != http.StatusUnauthorized {
		t.Errorf("logout after logout status = %d, want 401", again.Code)
	}
}

func TestChangePasswordRouteRequiresSession(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/password/change", `{"current_password":"password123","new_password":"newpassword456"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("change without session status = %d, want 401", rec.Code)
	}
}

func TestChangePasswordRouteStrictDecode(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"g@example.com","password":"password123","display_name":"G"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"g@example.com","password":"password123"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}
	rec := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"password123","new_password":"newpassword456","extra":1}`,
		&http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field body status = %d, want 400", rec.Code)
	}
}

func TestChangePasswordRouteWrongCurrent(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"h@example.com","password":"password123","display_name":"H"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"h@example.com","password":"password123"}`)
	c := sessionCookie(login)
	rec := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"wrongcurrent","new_password":"newpassword456"}`,
		&http.Cookie{Name: c.Name, Value: c.Value})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong-current status = %d, want 401", rec.Code)
	}
}

func TestChangePasswordRouteHappyPath(t *testing.T) {
	h := newTestHandler(t, nil)
	do(t, h, "POST", "/auth/register", `{"email":"i@example.com","password":"password123","display_name":"I"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"i@example.com","password":"password123"}`)
	old := sessionCookie(login)
	if old == nil {
		t.Fatal("no session cookie from login")
	}

	rec := do(t, h, "POST", "/auth/password/change",
		`{"current_password":"password123","new_password":"newpassword456"}`,
		&http.Cookie{Name: old.Name, Value: old.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("change status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	fresh := sessionCookie(rec)
	if fresh == nil || fresh.Value == "" || fresh.Value == old.Value {
		t.Errorf("change did not set a fresh session cookie: old=%v new=%v", old, fresh)
	}

	// The old session cookie is revoked; a gated route now rejects it.
	stale := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: old.Name, Value: old.Value})
	if stale.Code != http.StatusUnauthorized {
		t.Errorf("old cookie after change status = %d, want 401", stale.Code)
	}
	// The fresh session cookie works.
	ok := do(t, h, "POST", "/auth/logout", "", &http.Cookie{Name: fresh.Name, Value: fresh.Value})
	if ok.Code != http.StatusOK {
		t.Errorf("fresh cookie logout status = %d, want 200", ok.Code)
	}
	// Re-login with the new password succeeds.
	relogin := do(t, h, "POST", "/auth/login", `{"email":"i@example.com","password":"newpassword456"}`)
	if relogin.Code != http.StatusOK {
		t.Errorf("re-login with new password status = %d, want 200", relogin.Code)
	}
}

// denyLimiter always refuses.
type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: false}, nil
}
func (denyLimiter) Reset(context.Context, string) error { return nil }
func (denyLimiter) Close() error                        { return nil }
