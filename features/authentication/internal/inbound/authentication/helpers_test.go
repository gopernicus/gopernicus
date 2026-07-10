package authentication

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
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
			return user.User{}, sdk.ErrAlreadyExists
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
		return user.User{}, sdk.ErrNotFound
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
	return user.User{}, sdk.ErrNotFound
}
func (m *memUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return user.User{}, sdk.ErrNotFound
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
		return "", sdk.ErrNotFound
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
		return session.Session{}, sdk.ErrNotFound
	}
	if sess.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return sess, nil
}
func (s *memSessions) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[token]; !ok {
		return sdk.ErrNotFound
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
		return verification.Code{}, sdk.ErrNotFound
	}
	if v.Expired(time.Now()) {
		return verification.Code{}, sdk.ErrExpired
	}
	return v, nil
}
func (c *memCodes) Delete(_ context.Context, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[code]; !ok {
		return sdk.ErrNotFound
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
		return verification.Token{}, sdk.ErrNotFound
	}
	if v.Expired(time.Now()) {
		return verification.Token{}, sdk.ErrExpired
	}
	return v, nil
}
func (tk *memTokens) Delete(_ context.Context, token string) error {
	tk.mu.Lock()
	defer tk.mu.Unlock()
	if _, ok := tk.m[token]; !ok {
		return sdk.ErrNotFound
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
	Mount(h, svc, nil, "")
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

// denyLimiter always refuses.
type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: false}, nil
}
func (denyLimiter) Reset(context.Context, string) error { return nil }
func (denyLimiter) Close() error                        { return nil }
