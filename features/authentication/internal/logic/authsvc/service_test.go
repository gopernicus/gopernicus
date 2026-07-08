package authsvc

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
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// --- compile-time seam assertions ---

var (
	_ Hasher                       = (*fakeHasher)(nil)
	_ email.Sender                 = (*recordingMailer)(nil)
	_ user.UserRepository          = (*fakeUsers)(nil)
	_ user.PasswordRepository      = (*fakePasswords)(nil)
	_ session.SessionRepository    = (*fakeSessions)(nil)
	_ verification.CodeRepository  = (*fakeCodes)(nil)
	_ verification.TokenRepository = (*fakeTokens)(nil)
	_ ratelimiter.Limiter          = denyLimiter{}
)

// --- fakes ---

// fakeHasher is a deterministic, reversible stand-in for a real password hasher:
// the "hash" is "hash:"+password, so VerifyPassword is an exact-match check.
type fakeHasher struct {
	hashErr error
}

func (h *fakeHasher) HashPassword(password string) (string, error) {
	if h.hashErr != nil {
		return "", h.hashErr
	}
	return "hash:" + password, nil
}

func (h *fakeHasher) VerifyPassword(hash, password string) error {
	if hash == "hash:"+password {
		return nil
	}
	return errors.New("mismatch")
}

// recordingMailer captures every message sent.
type recordingMailer struct {
	mu   sync.Mutex
	sent []email.Message
	err  error
}

func (m *recordingMailer) Send(_ context.Context, msg email.Message) error {
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return nil
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (m *recordingMailer) last() email.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[len(m.sent)-1]
}

type fakeUsers struct {
	mu    sync.Mutex
	byID  map[string]user.User
	calls int // GetByEmail calls, for rate-limit ordering assertions
}

func newFakeUsers() *fakeUsers { return &fakeUsers{byID: map[string]user.User{}} }

func (f *fakeUsers) Create(_ context.Context, u user.User) (user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.byID {
		if strings.EqualFold(ex.Email, u.Email) {
			return user.User{}, errs.ErrAlreadyExists
		}
	}
	f.byID[u.ID] = u
	return u, nil
}

func (f *fakeUsers) Get(_ context.Context, id string) (user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byID[id]
	if !ok {
		return user.User{}, errs.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) GetByEmail(_ context.Context, emailAddr string) (user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	for _, u := range f.byID {
		if strings.EqualFold(u.Email, emailAddr) {
			return u, nil
		}
	}
	return user.User{}, errs.ErrNotFound
}

func (f *fakeUsers) Update(_ context.Context, id string, u user.User) (user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byID[id]; !ok {
		return user.User{}, errs.ErrNotFound
	}
	f.byID[id] = u
	return u, nil
}

type fakePasswords struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakePasswords() *fakePasswords { return &fakePasswords{m: map[string]string{}} }

func (f *fakePasswords) Set(_ context.Context, userID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[userID] = hash
	return nil
}

func (f *fakePasswords) Get(_ context.Context, userID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.m[userID]
	if !ok {
		return "", errs.ErrNotFound
	}
	return h, nil
}

type fakeSessions struct {
	mu sync.Mutex
	m  map[string]session.Session
}

func newFakeSessions() *fakeSessions { return &fakeSessions{m: map[string]session.Session{}} }

func (f *fakeSessions) Create(_ context.Context, s session.Session) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[s.Token] = s
	return s, nil
}

func (f *fakeSessions) Get(_ context.Context, token string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.m[token]
	if !ok {
		return session.Session{}, errs.ErrNotFound
	}
	if s.Expired(time.Now()) {
		return session.Session{}, errs.ErrExpired
	}
	return s, nil
}

func (f *fakeSessions) Delete(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[token]; !ok {
		return errs.ErrNotFound
	}
	delete(f.m, token)
	return nil
}

func (f *fakeSessions) DeleteByUser(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for token, s := range f.m {
		if s.UserID == userID {
			delete(f.m, token)
		}
	}
	return nil
}

type fakeCodes struct {
	mu sync.Mutex
	m  map[string]verification.Code
}

func newFakeCodes() *fakeCodes { return &fakeCodes{m: map[string]verification.Code{}} }

func (f *fakeCodes) Create(_ context.Context, c verification.Code) (verification.Code, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[c.Code] = c
	return c, nil
}

func (f *fakeCodes) Get(_ context.Context, code string) (verification.Code, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.m[code]
	if !ok {
		return verification.Code{}, errs.ErrNotFound
	}
	if c.Expired(time.Now()) {
		return verification.Code{}, errs.ErrExpired
	}
	return c, nil
}

func (f *fakeCodes) Delete(_ context.Context, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[code]; !ok {
		return errs.ErrNotFound
	}
	delete(f.m, code)
	return nil
}

type fakeTokens struct {
	mu sync.Mutex
	m  map[string]verification.Token
}

func newFakeTokens() *fakeTokens { return &fakeTokens{m: map[string]verification.Token{}} }

func (f *fakeTokens) Create(_ context.Context, t verification.Token) (verification.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[t.Token] = t
	return t, nil
}

func (f *fakeTokens) Get(_ context.Context, token string) (verification.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.m[token]
	if !ok {
		return verification.Token{}, errs.ErrNotFound
	}
	if t.Expired(time.Now()) {
		return verification.Token{}, errs.ErrExpired
	}
	return t, nil
}

func (f *fakeTokens) Delete(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[token]; !ok {
		return errs.ErrNotFound
	}
	delete(f.m, token)
	return nil
}

// denyLimiter always refuses — used to drive the rate-limit path.
type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: false}, nil
}
func (denyLimiter) Reset(context.Context, string) error { return nil }
func (denyLimiter) Close() error                        { return nil }

// --- harness ---

type harness struct {
	svc    *Service
	users  *fakeUsers
	pw     *fakePasswords
	sess   *fakeSessions
	codes  *fakeCodes
	tokens *fakeTokens
	hasher *fakeHasher
	mailer *recordingMailer
	events *spySecurityEvents
}

func newHarness(t *testing.T, limiter ratelimiter.Limiter) *harness {
	return newHarnessDeps(t, limiter, false)
}

// newHarnessDeps builds a harness, optionally requiring verified email at login.
func newHarnessDeps(t *testing.T, limiter ratelimiter.Limiter, requireVerifiedEmail bool) *harness {
	t.Helper()
	h := &harness{
		users:  newFakeUsers(),
		pw:     newFakePasswords(),
		sess:   newFakeSessions(),
		codes:  newFakeCodes(),
		tokens: newFakeTokens(),
		hasher: &fakeHasher{},
		mailer: &recordingMailer{},
		events: newSpySecurityEvents(),
	}
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	h.svc = NewService(Deps{
		Users:                h.users,
		Passwords:            h.pw,
		Sessions:             h.sess,
		Codes:                h.codes,
		Tokens:               h.tokens,
		Hasher:               h.hasher,
		Mailer:               h.mailer,
		MailFrom:             "noreply@example.com",
		Limiter:              limiter,
		Cookie:               CookieConfig{},
		RequireVerifiedEmail: requireVerifiedEmail,
		SecurityEvents:       h.events,
	})
	return h
}

// mustRegister registers a user and returns it.
func (h *harness) mustRegister(t *testing.T, email, password string) user.User {
	t.Helper()
	u, err := h.svc.Register(context.Background(), email, password, "Test User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return u
}

// --- tests ---

func TestRegister(t *testing.T) {
	h := newHarness(t, nil)
	u, err := h.svc.Register(context.Background(), "New@Example.com", "password123", "New User")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.Email != "new@example.com" {
		t.Errorf("email not normalized: %q", u.Email)
	}
	if _, err := h.pw.Get(context.Background(), u.ID); err != nil {
		t.Errorf("password not stored: %v", err)
	}
	if len(h.codes.m) != 1 {
		t.Errorf("verification code count = %d, want 1", len(h.codes.m))
	}
	if h.mailer.count() != 1 {
		t.Errorf("mail count = %d, want 1", h.mailer.count())
	}
	if to := h.mailer.last().To; len(to) != 1 || to[0] != "new@example.com" {
		t.Errorf("mail To = %v", to)
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "dup@example.com", "password123")
	_, err := h.svc.Register(context.Background(), "DUP@example.com", "password456", "Other")
	if !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("duplicate register: err=%v, want ErrAlreadyExists", err)
	}
}

func TestRegisterShortPassword(t *testing.T) {
	h := newHarness(t, nil)
	_, err := h.svc.Register(context.Background(), "short@example.com", "short", "X")
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("short password: err=%v, want ErrInvalidInput", err)
	}
	if h.mailer.count() != 0 {
		t.Errorf("mail sent for invalid registration")
	}
}

func TestVerify(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "verify@example.com", "password123")
	var code string
	for c := range h.codes.m {
		code = c
	}
	if err := h.svc.Verify(context.Background(), code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	u, _ := h.users.GetByEmail(context.Background(), "verify@example.com")
	if !u.EmailVerified {
		t.Error("user not marked verified")
	}
	if len(h.codes.m) != 0 {
		t.Error("verification code not consumed")
	}
}

func TestVerifyUnknownCode(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.svc.Verify(context.Background(), "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Verify(unknown): err=%v, want ErrNotFound", err)
	}
}

func TestLogin(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "login@example.com", "password123")
	token, gotU, err := h.svc.Login(context.Background(), "Login@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token == "" || gotU.ID != u.ID {
		t.Errorf("bad login: token=%q user=%+v", token, gotU)
	}
	if _, err := h.svc.ValidateSession(context.Background(), token); err != nil {
		t.Errorf("minted session not valid: %v", err)
	}
}

// TestSessionTokenHashRoundTrip proves the service-side hashing (design §7.3):
// the plaintext cookie Login returns is NOT what the store holds (the store is
// keyed by the token's SHA-256 hash), ValidateSession still matches via the same
// hashing, and Logout deletes by the hash.
func TestSessionTokenHashRoundTrip(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "hash@example.com", "password123")
	token, _, err := h.svc.Login(context.Background(), "hash@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	hashed, err := h.svc.hashSessionToken(token)
	if err != nil {
		t.Fatalf("hashSessionToken: %v", err)
	}
	if hashed == token {
		t.Fatal("stored token equals plaintext — not hashed")
	}
	h.sess.mu.Lock()
	_, rawPresent := h.sess.m[token]
	_, hashPresent := h.sess.m[hashed]
	h.sess.mu.Unlock()
	if rawPresent {
		t.Error("store holds the raw plaintext token")
	}
	if !hashPresent {
		t.Error("store is not keyed by the hashed token")
	}
	if _, err := h.svc.ValidateSession(context.Background(), token); err != nil {
		t.Errorf("ValidateSession(plaintext): %v", err)
	}
	if err := h.svc.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.svc.ValidateSession(context.Background(), token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("session survived logout: err=%v, want ErrNotFound", err)
	}
}

func TestLoginRequireVerifiedEmailBlocksUnverified(t *testing.T) {
	h := newHarnessDeps(t, nil, true)
	h.mustRegister(t, "unv@example.com", "password123") // unverified by default
	_, _, err := h.svc.Login(context.Background(), "unv@example.com", "password123")
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Errorf("unverified login: err=%v, want ErrEmailNotVerified", err)
	}
	if !errors.Is(err, errs.ErrForbidden) {
		t.Errorf("ErrEmailNotVerified must wrap ErrForbidden (403 mapping): %v", err)
	}
}

func TestLoginRequireVerifiedEmailAllowsVerified(t *testing.T) {
	h := newHarnessDeps(t, nil, true)
	h.mustRegister(t, "ver@example.com", "password123")
	var code string
	for c := range h.codes.m {
		code = c
	}
	if err := h.svc.Verify(context.Background(), code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, _, err := h.svc.Login(context.Background(), "ver@example.com", "password123"); err != nil {
		t.Errorf("verified login: %v", err)
	}
}

func TestLoginVerifiedGateOffByDefault(t *testing.T) {
	h := newHarness(t, nil) // RequireVerifiedEmail defaults false
	h.mustRegister(t, "off@example.com", "password123")
	if _, _, err := h.svc.Login(context.Background(), "off@example.com", "password123"); err != nil {
		t.Errorf("gate-off unverified login: %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "wp@example.com", "password123")
	_, _, err := h.svc.Login(context.Background(), "wp@example.com", "wrongpassword")
	if !errors.Is(err, errs.ErrUnauthorized) {
		t.Errorf("wrong password: err=%v, want ErrUnauthorized", err)
	}
}

func TestLoginUnknownEmail(t *testing.T) {
	h := newHarness(t, nil)
	_, _, err := h.svc.Login(context.Background(), "ghost@example.com", "password123")
	if !errors.Is(err, errs.ErrUnauthorized) {
		t.Errorf("unknown email: err=%v, want ErrUnauthorized", err)
	}
}

func TestLoginRateLimitedFirst(t *testing.T) {
	h := newHarness(t, denyLimiter{})
	h.mustRegister(t, "rl@example.com", "password123")
	before := h.users.calls
	_, _, err := h.svc.Login(context.Background(), "rl@example.com", "password123")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("rate limited: err=%v, want ErrRateLimited", err)
	}
	if h.users.calls != before {
		t.Error("rate limit did not short-circuit before touching the user store")
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "lo@example.com", "password123")
	token, _, _ := h.svc.Login(context.Background(), "lo@example.com", "password123")
	if err := h.svc.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.svc.ValidateSession(context.Background(), token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("session not deleted: err=%v, want ErrNotFound", err)
	}
	// Idempotent: logging out an already-gone token is not an error.
	if err := h.svc.Logout(context.Background(), token); err != nil {
		t.Errorf("second Logout: %v", err)
	}
}

// TestChangePassword covers the amended §7.2 policy: the new password works, ALL
// pre-existing sessions are revoked, a fresh session is minted for the caller,
// and the old password no longer authenticates.
func TestChangePassword(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "cp@example.com", "password123")
	tokenA, _, err := h.svc.Login(context.Background(), "cp@example.com", "password123")
	if err != nil {
		t.Fatalf("Login A: %v", err)
	}
	tokenB, _, err := h.svc.Login(context.Background(), "cp@example.com", "password123")
	if err != nil {
		t.Fatalf("Login B: %v", err)
	}

	newToken, err := h.svc.ChangePassword(context.Background(), u.ID, "password123", "newpassword456")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// Every pre-existing session is revoked.
	for name, tok := range map[string]string{"A": tokenA, "B": tokenB} {
		if _, err := h.svc.ValidateSession(context.Background(), tok); !errors.Is(err, errs.ErrNotFound) {
			t.Errorf("session %s survived password change: err=%v, want ErrNotFound", name, err)
		}
	}
	// A fresh session is minted for the caller.
	if newToken == "" {
		t.Fatal("ChangePassword returned an empty session token")
	}
	if _, err := h.svc.ValidateSession(context.Background(), newToken); err != nil {
		t.Errorf("minted session not valid: %v", err)
	}
	// New password works; old password no longer does.
	if _, _, err := h.svc.Login(context.Background(), "cp@example.com", "newpassword456"); err != nil {
		t.Errorf("login with new password: %v", err)
	}
	if _, _, err := h.svc.Login(context.Background(), "cp@example.com", "password123"); !errors.Is(err, errs.ErrUnauthorized) {
		t.Errorf("login with old password: err=%v, want ErrUnauthorized", err)
	}
}

func TestChangePasswordWrongCurrent(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "cpw@example.com", "password123")
	_, err := h.svc.ChangePassword(context.Background(), u.ID, "wrongcurrent", "newpassword456")
	if !errors.Is(err, errs.ErrUnauthorized) {
		t.Errorf("wrong current: err=%v, want ErrUnauthorized", err)
	}
}

func TestForgotPasswordUnknownEmailNoReveal(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.svc.ForgotPassword(context.Background(), "ghost@example.com"); err != nil {
		t.Fatalf("ForgotPassword(unknown): %v", err)
	}
	if len(h.tokens.m) != 0 {
		t.Error("reset token issued for unknown email")
	}
	if h.mailer.count() != 0 {
		t.Error("mail sent for unknown email (enumeration leak)")
	}
}

func TestForgotAndResetPassword(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "fr@example.com", "password123")
	h.mailer.sent = nil // drop the verification mail

	if err := h.svc.ForgotPassword(context.Background(), "fr@example.com"); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	if h.mailer.count() != 1 {
		t.Fatalf("reset mail count = %d, want 1", h.mailer.count())
	}
	var token string
	for tk := range h.tokens.m {
		token = tk
	}
	if err := h.svc.ResetPassword(context.Background(), token, "brandnewpass"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if len(h.tokens.m) != 0 {
		t.Error("reset token not consumed")
	}
	if _, _, err := h.svc.Login(context.Background(), "fr@example.com", "brandnewpass"); err != nil {
		t.Errorf("login after reset: %v", err)
	}
}

func TestResetPasswordExpiredToken(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "rex@example.com", "password123")
	expired := verification.NewToken(u.ID, time.Minute, time.Now().Add(-time.Hour))
	h.tokens.Create(context.Background(), expired)
	err := h.svc.ResetPassword(context.Background(), expired.Token, "brandnewpass")
	if !errors.Is(err, errs.ErrExpired) {
		t.Errorf("expired token: err=%v, want ErrExpired", err)
	}
}

func TestRequireUserValidSession(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "ru@example.com", "password123")
	token, u, _ := h.svc.Login(context.Background(), "ru@example.com", "password123")

	var gotUserID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.svc.CurrentUser(r.Context())
		if !ok {
			t.Error("CurrentUser not set inside RequireUser")
		}
		gotUserID = id
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: token})
	rec := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if gotUserID != u.ID {
		t.Errorf("CurrentUser = %q, want %q", gotUserID, u.ID)
	}
}

func TestRequireUserNoCookie(t *testing.T) {
	h := newHarness(t, nil)
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	req := httptest.NewRequest("GET", "/x", nil)
	rec := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("next handler called without a session")
	}
}

func TestRequireUserExpiredSession(t *testing.T) {
	h := newHarness(t, nil)
	// Store the expired session under the HASHED token, mirroring a real mint, so
	// the plaintext cookie resolves to it through the service's hashing.
	plaintext := "raw-expired-token"
	hashed, err := h.svc.hashSessionToken(plaintext)
	if err != nil {
		t.Fatalf("hashSessionToken: %v", err)
	}
	expired := session.NewSession("u1", time.Minute, time.Now().Add(-time.Hour))
	expired.Token = hashed
	h.sess.Create(context.Background(), expired)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: plaintext})
	rec := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCurrentUserAbsent(t *testing.T) {
	h := newHarness(t, nil)
	if _, ok := h.svc.CurrentUser(context.Background()); ok {
		t.Error("CurrentUser reported ok on a bare context")
	}
}
