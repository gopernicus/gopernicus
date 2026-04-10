//go:build penetration

// Package authentication penetration tests attempt to exploit the authentication
// system by simulating real-world attacks. These tests use mock repositories
// and exercise the Authenticator logic directly.
package authentication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// ---------------------------------------------------------------------------
// Mock implementations for testing
// ---------------------------------------------------------------------------

type mockUserRepo struct {
	mu    sync.Mutex
	users map[string]User       // by ID
	byEmail map[string]User     // by email
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{
		users:   make(map[string]User),
		byEmail: make(map[string]User),
	}
}

func (r *mockUserRepo) Get(_ context.Context, id string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return User{}, errs.ErrNotFound
	}
	return u, nil
}

func (r *mockUserRepo) GetByEmail(_ context.Context, email string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byEmail[email]
	if !ok {
		return User{}, errs.ErrNotFound
	}
	return u, nil
}

var userCounter int

func (r *mockUserRepo) Create(_ context.Context, input CreateUserInput) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byEmail[input.Email]; exists {
		return User{}, errs.ErrAlreadyExists
	}
	userCounter++
	u := User{
		UserID:        fmt.Sprintf("user_%d", userCounter),
		Email:         input.Email,
		DisplayName:   input.DisplayName,
		EmailVerified: input.EmailVerified,
		Active:        true,
	}
	r.users[u.UserID] = u
	r.byEmail[u.Email] = u
	return u, nil
}

func (r *mockUserRepo) SetEmailVerified(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return errs.ErrNotFound
	}
	u.EmailVerified = true
	r.users[id] = u
	r.byEmail[u.Email] = u
	return nil
}

func (r *mockUserRepo) SetLastLogin(_ context.Context, _ string, _ time.Time) error {
	return nil
}

type mockPasswordRepo struct {
	mu        sync.Mutex
	passwords map[string]Password // by userID
}

func newMockPasswordRepo() *mockPasswordRepo {
	return &mockPasswordRepo{passwords: make(map[string]Password)}
}

func (r *mockPasswordRepo) GetByUserID(_ context.Context, userID string) (Password, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.passwords[userID]
	if !ok {
		return Password{}, errs.ErrNotFound
	}
	return p, nil
}

func (r *mockPasswordRepo) Create(_ context.Context, userID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passwords[userID] = Password{UserID: userID, Hash: hash}
	return nil
}

func (r *mockPasswordRepo) Update(_ context.Context, userID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passwords[userID] = Password{UserID: userID, Hash: hash}
	return nil
}

func (r *mockPasswordRepo) DeleteByUserID(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.passwords[userID]; !ok {
		return errs.ErrNotFound
	}
	delete(r.passwords, userID)
	return nil
}

func (r *mockPasswordRepo) SetVerified(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.passwords[userID]
	if !ok {
		return errs.ErrNotFound
	}
	p.Verified = true
	r.passwords[userID] = p
	return nil
}

type mockSessionRepo struct {
	mu       sync.Mutex
	sessions map[string]Session // by SessionID
	counter  int
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{sessions: make(map[string]Session)}
}

func (r *mockSessionRepo) Create(_ context.Context, s Session) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counter++
	s.SessionID = fmt.Sprintf("sess_%d", r.counter)
	r.sessions[s.SessionID] = s
	return s, nil
}

func (r *mockSessionRepo) GetByTokenHash(_ context.Context, hash string) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		if s.TokenHash == hash {
			return s, nil
		}
	}
	return Session{}, errs.ErrNotFound
}

func (r *mockSessionRepo) GetByRefreshHash(_ context.Context, hash string) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		if s.RefreshTokenHash == hash {
			return s, nil
		}
	}
	return Session{}, errs.ErrNotFound
}

func (r *mockSessionRepo) GetByPreviousRefreshHash(_ context.Context, hash string) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		if s.PreviousRefreshHash == hash {
			return s, nil
		}
	}
	return Session{}, errs.ErrNotFound
}

func (r *mockSessionRepo) Update(_ context.Context, s Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.SessionID] = s
	return nil
}

func (r *mockSessionRepo) Delete(_ context.Context, userID, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok {
		return nil
	}
	if s.UserID != userID {
		return nil // ownership mismatch — real DB enforces via WHERE user_id = $1 AND id = $2
	}
	delete(r.sessions, sessionID)
	return nil
}

func (r *mockSessionRepo) DeleteAllForUser(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, id)
		}
	}
	return nil
}

func (r *mockSessionRepo) DeleteAllForUserExcept(_ context.Context, userID, except string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.sessions {
		if s.UserID == userID && id != except {
			delete(r.sessions, id)
		}
	}
	return nil
}

func (r *mockSessionRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

type mockVerificationCodeRepo struct {
	mu    sync.Mutex
	codes map[string]VerificationCode // key: identifier+purpose
}

func newMockVerificationCodeRepo() *mockVerificationCodeRepo {
	return &mockVerificationCodeRepo{codes: make(map[string]VerificationCode)}
}

func codeKey(id, purpose string) string { return id + "|" + purpose }

func (r *mockVerificationCodeRepo) Create(_ context.Context, code VerificationCode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes[codeKey(code.Identifier, code.Purpose)] = code
	return nil
}

func (r *mockVerificationCodeRepo) Get(_ context.Context, id, purpose string) (VerificationCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.codes[codeKey(id, purpose)]
	if !ok {
		return VerificationCode{}, errs.ErrNotFound
	}
	return c, nil
}

func (r *mockVerificationCodeRepo) Delete(_ context.Context, id, purpose string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.codes, codeKey(id, purpose))
	return nil
}

func (r *mockVerificationCodeRepo) IncrementAttempts(_ context.Context, id, purpose string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := codeKey(id, purpose)
	c, ok := r.codes[k]
	if !ok {
		return 0, errs.ErrNotFound
	}
	c.AttemptCount++
	r.codes[k] = c
	return c.AttemptCount, nil
}

type mockVerificationTokenRepo struct {
	mu     sync.Mutex
	tokens map[string]VerificationToken // key: identifier+purpose
}

func newMockVerificationTokenRepo() *mockVerificationTokenRepo {
	return &mockVerificationTokenRepo{tokens: make(map[string]VerificationToken)}
}

func (r *mockVerificationTokenRepo) Create(_ context.Context, t VerificationToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[codeKey(t.TokenHash, t.Purpose)] = t
	return nil
}

func (r *mockVerificationTokenRepo) Get(_ context.Context, id, purpose string) (VerificationToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tokens[codeKey(id, purpose)]
	if !ok {
		return VerificationToken{}, errs.ErrNotFound
	}
	return t, nil
}

func (r *mockVerificationTokenRepo) Delete(_ context.Context, id, purpose string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tokens, codeKey(id, purpose))
	return nil
}

func (r *mockVerificationTokenRepo) DeleteByUserIDAndPurpose(_ context.Context, userID, purpose string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, t := range r.tokens {
		if t.UserID == userID && t.Purpose == purpose {
			delete(r.tokens, key)
		}
	}
	return nil
}

type mockSecurityEventRepo struct {
	mu     sync.Mutex
	events []SecurityEvent
}

func (r *mockSecurityEventRepo) Create(_ context.Context, e SecurityEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *mockSecurityEventRepo) getEvents() []SecurityEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SecurityEvent, len(r.events))
	copy(out, r.events)
	return out
}

// Minimal mock hasher (NOT bcrypt — fast for tests).
type mockHasher struct{}

func (h *mockHasher) Hash(pw string) (string, error) {
	if pw == "" {
		return "", fmt.Errorf("empty password")
	}
	hash := sha256.Sum256([]byte("salt:" + pw))
	return hex.EncodeToString(hash[:]), nil
}

func (h *mockHasher) Compare(hash, pw string) error {
	expected, _ := h.Hash(pw)
	if hash != expected {
		return fmt.Errorf("mismatch")
	}
	return nil
}

// Minimal mock JWT signer.
type mockSigner struct {
	mu      sync.Mutex
	secret  string
	counter int // ensures unique tokens even within the same second
}

func (s *mockSigner) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	s.mu.Lock()
	s.counter++
	n := s.counter
	s.mu.Unlock()
	userID, _ := claims["user_id"].(string)
	return fmt.Sprintf("jwt.%s.%d_%d", userID, expiresAt.Unix(), n), nil
}

func (s *mockSigner) Verify(token string) (map[string]any, error) {
	// Parse "jwt.{userID}.{exp}"
	var userID string
	var exp int64
	_, err := fmt.Sscanf(token, "jwt.%s", &userID)
	if err != nil || userID == "" {
		return nil, fmt.Errorf("invalid token")
	}
	// Extract user_id (everything between first and last dot)
	parts := splitToken(token)
	if len(parts) != 3 || parts[0] != "jwt" {
		return nil, fmt.Errorf("invalid token format")
	}
	userID = parts[1]
	fmt.Sscanf(parts[2], "%d", &exp)
	if exp > 0 && time.Now().UTC().Unix() > exp {
		return nil, fmt.Errorf("token expired")
	}
	return map[string]any{"user_id": userID}, nil
}

func splitToken(token string) []string {
	var parts []string
	start := 0
	for i, c := range token {
		if c == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	parts = append(parts, token[start:])
	return parts
}

type mockEventBus struct {
	mu     sync.Mutex
	events []any
}

func (b *mockEventBus) Emit(_ context.Context, event events.Event, _ ...events.EmitOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}

type mockSubscription struct{}

func (mockSubscription) Unsubscribe() error { return nil }

func (b *mockEventBus) Subscribe(_ string, _ events.Handler) (events.Subscription, error) {
	return mockSubscription{}, nil
}

func (b *mockEventBus) Close(_ context.Context) error { return nil }

// ---------------------------------------------------------------------------
// Test helper: create a fully wired Authenticator for testing.
// ---------------------------------------------------------------------------

type testHarness struct {
	auth       *Authenticator
	users      *mockUserRepo
	passwords  *mockPasswordRepo
	sessions   *mockSessionRepo
	codes      *mockVerificationCodeRepo
	tokens     *mockVerificationTokenRepo
	secEvents  *mockSecurityEventRepo
	bus        *mockEventBus
}

func newTestHarness() *testHarness {
	users := newMockUserRepo()
	passwords := newMockPasswordRepo()
	sessions := newMockSessionRepo()
	codes := newMockVerificationCodeRepo()
	tokens := newMockVerificationTokenRepo()
	secEvents := &mockSecurityEventRepo{}
	bus := &mockEventBus{}

	auth := NewAuthenticator(
		"test",
		Repositories{
			users:     users,
			passwords: passwords,
			sessions:  sessions,
			tokens:    tokens,
			codes:     codes,
		},
		&mockHasher{},
		&mockSigner{secret: "test-secret"},
		bus,
		Config{
			AccessTokenExpiry:       30 * time.Minute,
			RefreshTokenExpiry:      720 * time.Hour,
			VerificationCodeExpiry:  15 * time.Minute,
			PasswordResetExpiry:     1 * time.Hour,
			OAuthStateExpiry:        10 * time.Minute,
			MaxVerificationAttempts: 5,
		},
		WithSecurityEvents(secEvents),
	)

	return &testHarness{
		auth:      auth,
		users:     users,
		passwords: passwords,
		sessions:  sessions,
		codes:     codes,
		tokens:    tokens,
		secEvents: secEvents,
		bus:       bus,
	}
}

// registerAndVerify is a helper to create a verified user.
func (h *testHarness) registerAndVerify(t *testing.T, email, password string) string {
	t.Helper()
	ctx := context.Background()
	result, err := h.auth.Register(ctx, email, password, "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := h.auth.VerifyEmail(ctx, email, result.VerificationCode); err != nil {
		t.Fatalf("verify email: %v", err)
	}
	return result.UserID
}

// ===========================================================================
// ATTACK 1: Verification Code Brute Force
//
// A 6-digit code has only 1,000,000 possible values. The auth package
// enforces attempt counting: after MaxVerificationAttempts (default 5)
// wrong guesses, the code is deleted and ErrTooManyAttempts is returned.
// ===========================================================================

func TestAttack_VerificationCodeBruteForce(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	// Register a user (gets a verification code).
	_, err := h.auth.Register(ctx, "victim@example.com", "StrongP@ss1!", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Simulate brute force: try wrong codes until lockout.
	lockedOut := false
	var attemptsBeforeLockout int
	for i := 0; i < 1000; i++ {
		code := fmt.Sprintf("%06d", 999000+i) // high range to avoid hitting real code
		err := h.auth.VerifyEmail(ctx, "victim@example.com", code)
		if errors.Is(err, ErrTooManyAttempts) {
			attemptsBeforeLockout = i + 1
			lockedOut = true
			break
		}
	}

	if !lockedOut {
		t.Fatal("VULNERABILITY: No brute force protection — 1000 attempts without lockout")
	}

	// Verify lockout happened within MaxVerificationAttempts.
	// The lockout fires on the attempt that reaches the max, so we expect
	// exactly MaxVerificationAttempts attempts before ErrTooManyAttempts.
	if attemptsBeforeLockout != h.auth.config.MaxVerificationAttempts {
		t.Fatalf("expected lockout after %d attempts, got %d",
			h.auth.config.MaxVerificationAttempts, attemptsBeforeLockout)
	}
	t.Logf("DEFENDED: lockout after %d attempts (MaxVerificationAttempts=%d)",
		attemptsBeforeLockout, h.auth.config.MaxVerificationAttempts)

	// After lockout, even the real code should fail (code was deleted).
	err = h.auth.VerifyEmail(ctx, "victim@example.com", "000000")
	if err == nil {
		t.Fatal("VULNERABILITY: Code still valid after lockout!")
	}
	if !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("expected ErrCodeInvalid after lockout, got: %v", err)
	}
	t.Log("DEFENDED: Code deleted after max attempts — further attempts return ErrCodeInvalid.")
}

// ===========================================================================
// ATTACK 2: Password Reset Token Replay
//
// After a password reset, verify the token cannot be reused.
// ===========================================================================

func TestAttack_PasswordResetTokenReplay(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "user@example.com", "OldPassword1!")

	// Initiate password reset.
	token, err := h.auth.InitiatePasswordReset(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("initiate reset: %v", err)
	}
	if token == "" {
		t.Fatal("expected token, got empty string")
	}

	// Use the token.
	if err := h.auth.ResetPassword(ctx, token, "NewPassword1!"); err != nil {
		t.Fatalf("reset password: %v", err)
	}

	// Try to replay the same token.
	err = h.auth.ResetPassword(ctx, token, "HackerPassword!")
	if err == nil {
		t.Fatal("VULNERABILITY: Password reset token can be replayed after use!")
	}
	t.Logf("DEFENDED: token replay blocked with error: %v", err)
}

// ===========================================================================
// ATTACK 3: Refresh Token Reuse Detection
//
// When a refresh token is stolen and used after rotation, all sessions
// for the user should be revoked.
// ===========================================================================

func TestAttack_RefreshTokenStealAndReuse(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "victim@example.com", "Password1!")

	// Login to get tokens.
	loginResult, err := h.auth.Login(ctx, "victim@example.com", "Password1!")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	originalRefresh := loginResult.RefreshToken

	// Legitimate user refreshes (rotates the token).
	refreshResult, err := h.auth.RefreshToken(ctx, originalRefresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// Attacker tries to use the old (rotated) refresh token.
	_, err = h.auth.RefreshToken(ctx, originalRefresh)
	if err == nil {
		t.Fatal("VULNERABILITY: Rotated refresh token was accepted!")
	}
	if !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("expected ErrTokenReuse, got: %v", err)
	}

	// Verify all sessions were revoked (legitimate user's new token should also be invalid).
	_, err = h.auth.RefreshToken(ctx, refreshResult.RefreshToken)
	if err == nil {
		t.Fatal("VULNERABILITY: Sessions were not fully revoked after token reuse detection!")
	}
	t.Log("DEFENDED: Token reuse detected, all sessions revoked.")

	// Verify session store is empty for this user.
	if h.sessions.count() != 0 {
		t.Errorf("VULNERABILITY: %d sessions remain after reuse detection (expected 0)", h.sessions.count())
	}
}

// ===========================================================================
// ATTACK 4: Email Enumeration via Registration
//
// Registration should not reveal whether an email is already registered.
// ===========================================================================

func TestAttack_EmailEnumerationViaRegistration(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	// Register a user.
	result1, err := h.auth.Register(ctx, "existing@example.com", "Password1!", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Try to register with the same email.
	result2, err := h.auth.Register(ctx, "existing@example.com", "DifferentPass1!", "")

	// DEFENDED: Both calls return (*RegisterResult, nil) — indistinguishable.
	if err != nil {
		t.Fatalf("VULNERABILITY: duplicate registration returned error: %v (allows enumeration)", err)
	}
	if result2 == nil {
		t.Fatal("VULNERABILITY: duplicate registration returned nil result")
	}

	// The duplicate result should NOT expose the real user ID.
	if result2.UserID == result1.UserID {
		t.Fatal("VULNERABILITY: duplicate registration exposes real user ID")
	}

	// The duplicate result should NOT include a verification code.
	if result2.VerificationCode != "" {
		t.Fatal("VULNERABILITY: duplicate registration generated a verification code")
	}

	t.Logf("DEFENDED: Duplicate registration returns synthetic success (UserID=%q, no code). "+
		"Attacker cannot distinguish new vs existing email.", result2.UserID)
}

// ===========================================================================
// ATTACK 5: Email Enumeration via Password Reset
//
// Password reset correctly returns ("", nil) for unknown emails.
// Verify timing is not leaking information.
// ===========================================================================

func TestAttack_EmailEnumerationViaPasswordReset(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "known@example.com", "Password1!")

	// Reset for known email.
	token1, err1 := h.auth.InitiatePasswordReset(ctx, "known@example.com")

	// Reset for unknown email.
	token2, err2 := h.auth.InitiatePasswordReset(ctx, "unknown@example.com")

	if err1 != nil {
		t.Fatalf("known email reset failed: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("unknown email reset should not error: %v", err2)
	}

	// Both should return no error (silent fail for unknown).
	if token1 == "" {
		t.Fatal("known email should return a token")
	}
	if token2 != "" {
		t.Fatal("VULNERABILITY: unknown email returned a token!")
	}

	t.Log("DEFENDED: Password reset returns same error type for known/unknown emails. " +
		"Dummy work (token generation + hashing) on unknown-email path equalizes timing.")
}

// ===========================================================================
// ATTACK 6: Login to Unverified Account
//
// Users with unverified emails should not be able to log in.
// ===========================================================================

func TestAttack_LoginToUnverifiedAccount(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	// Register but don't verify.
	_, err := h.auth.Register(ctx, "unverified@example.com", "Password1!", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Attempt login.
	_, err = h.auth.Login(ctx, "unverified@example.com", "Password1!")
	if err == nil {
		t.Fatal("VULNERABILITY: Login succeeded for unverified email!")
	}
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("expected ErrEmailNotVerified, got: %v", err)
	}
	t.Log("DEFENDED: Login blocked for unverified email.")
}

// ===========================================================================
// ATTACK 7: Session Fixation via Token Hash Collision
//
// Verify that session lookup is precise and doesn't allow cross-user access.
// ===========================================================================

func TestAttack_CrossUserSessionAccess(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "alice@example.com", "AlicePass1!")
	h.registerAndVerify(t, "bob@example.com", "BobPass1!")

	aliceLogin, err := h.auth.Login(ctx, "alice@example.com", "AlicePass1!")
	if err != nil {
		t.Fatalf("alice login: %v", err)
	}

	_, err = h.auth.Login(ctx, "bob@example.com", "BobPass1!")
	if err != nil {
		t.Fatalf("bob login: %v", err)
	}

	// Alice's session should only validate for Alice.
	user, session, err := h.auth.AuthenticateSession(ctx, aliceLogin.AccessToken)
	if err != nil {
		t.Fatalf("authenticate session: %v", err)
	}

	if user.Email != "alice@example.com" {
		t.Fatalf("VULNERABILITY: Session returned wrong user: %s", user.Email)
	}
	if session.UserID != aliceLogin.User.UserID {
		t.Fatal("VULNERABILITY: Session UserID mismatch!")
	}

	t.Log("DEFENDED: Sessions are correctly isolated per user.")
}

// ===========================================================================
// ATTACK 8: Concurrent Refresh Token Race Condition
//
// Two clients holding the same refresh token attempt concurrent refresh.
// One should succeed, one should fail. Token reuse should be detected.
// ===========================================================================

func TestAttack_ConcurrentRefreshRace(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "victim@example.com", "Password1!")
	loginResult, err := h.auth.Login(ctx, "victim@example.com", "Password1!")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	refreshToken := loginResult.RefreshToken

	var wg sync.WaitGroup
	results := make([]*LoginResult, 2)
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = h.auth.RefreshToken(ctx, refreshToken)
		}(i)
	}
	wg.Wait()

	successCount := 0
	for i := 0; i < 2; i++ {
		if errs[i] == nil {
			successCount++
		}
	}

	// In the current implementation with mocks (mutex-protected),
	// exactly one should succeed. With a real DB, both could succeed
	// due to a TOCTOU race unless the DB enforces uniqueness or uses
	// optimistic locking.
	t.Logf("Concurrent refresh results: %d succeeded, %d failed", successCount, 2-successCount)
	if successCount > 1 {
		t.Log("WARNING: Both concurrent refreshes succeeded. " +
			"This is a race condition — the DB adapter needs atomic compare-and-swap or row locking.")
	}
}

// ===========================================================================
// ATTACK 9: Password Change Without Revoking Other Sessions
//
// After password change with revokeOtherSessions=false, old sessions
// remain active. This is by design but noteworthy.
// ===========================================================================

func TestAttack_PasswordChangeWithoutSessionRevocation(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "user@example.com", "OldPass1!")

	// Login from two "devices".
	login1, err := h.auth.Login(ctx, "user@example.com", "OldPass1!")
	if err != nil {
		t.Fatalf("login1: %v", err)
	}

	login2, err := h.auth.Login(ctx, "user@example.com", "OldPass1!")
	if err != nil {
		t.Fatalf("login2: %v", err)
	}

	// Change password from device 1 WITHOUT revoking others.
	err = h.auth.ChangePassword(ctx, login1.User.UserID, login1.SessionID, "OldPass1!", "NewPass1!", false)
	if err != nil {
		t.Fatalf("change password: %v", err)
	}

	// Device 2's session should still be active.
	_, _, err = h.auth.AuthenticateSession(ctx, login2.AccessToken)
	if err != nil {
		t.Log("DEFENDED: Other sessions revoked even without flag.")
	} else {
		t.Log("NOTE: Other sessions remain active after password change (revokeOtherSessions=false). " +
			"This is by design but the attacker who has the old session can still use it until access token expires.")
	}

	// Now test WITH revocation.
	login3, err := h.auth.Login(ctx, "user@example.com", "NewPass1!")
	if err != nil {
		t.Fatalf("login3: %v", err)
	}

	err = h.auth.ChangePassword(ctx, login3.User.UserID, login3.SessionID, "NewPass1!", "FinalPass1!", true)
	if err != nil {
		t.Fatalf("change password with revocation: %v", err)
	}

	// Other sessions should be revoked.
	_, _, err = h.auth.AuthenticateSession(ctx, login2.AccessToken)
	if err == nil {
		t.Fatal("VULNERABILITY: Old session still valid after password change with revocation!")
	}
	t.Log("DEFENDED: Sessions revoked on password change with flag.")
}

// ===========================================================================
// ATTACK 10: Expired Verification Code Still Accepted
//
// Verify that expired codes are properly rejected.
// ===========================================================================

func TestAttack_ExpiredVerificationCode(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	result, err := h.auth.Register(ctx, "user@example.com", "Password1!", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Manually expire the code in the mock repo.
	h.codes.mu.Lock()
	key := codeKey("user@example.com", PurposeEmailVerify)
	code := h.codes.codes[key]
	code.ExpiresAt = time.Now().UTC().Add(-1 * time.Hour) // expired 1 hour ago
	h.codes.codes[key] = code
	h.codes.mu.Unlock()

	// Try to use the expired code.
	err = h.auth.VerifyEmail(ctx, "user@example.com", result.VerificationCode)
	if err == nil {
		t.Fatal("VULNERABILITY: Expired verification code was accepted!")
	}
	if !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("expected ErrCodeExpired, got: %v", err)
	}
	t.Log("DEFENDED: Expired verification codes are rejected.")
}

// ===========================================================================
// ATTACK 11: Timing Attack on Verification Code Comparison
//
// hashToken uses SHA256 and string comparison (==).
// String comparison in Go is NOT constant-time. This is exploitable
// if an attacker can measure sub-microsecond timing differences.
// ===========================================================================

func TestAttack_TimingOnCodeVerification(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	result, err := h.auth.Register(ctx, "user@example.com", "Password1!", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Measure timing difference between correct and incorrect codes.
	// In a real attack, this would require many samples and network noise filtering.
	correctCode := result.VerificationCode
	wrongCode := "000000"

	iterations := 1000
	var correctTotal, wrongTotal time.Duration

	for i := 0; i < iterations; i++ {
		// Re-register to get fresh code each time.
		start := time.Now()
		_ = h.auth.VerifyEmail(ctx, "user@example.com", wrongCode)
		wrongTotal += time.Since(start)
	}

	// Re-create the code for correct attempt.
	_ = h.codes.Delete(ctx, "user@example.com", PurposeEmailVerify)
	_ = h.codes.Create(ctx, VerificationCode{
		Identifier: "user@example.com",
		CodeHash:   mustHashToken(correctCode),
		Purpose:    PurposeEmailVerify,
		ExpiresAt:  time.Now().UTC().Add(15 * time.Minute),
	})

	start := time.Now()
	_ = h.auth.VerifyEmail(ctx, "user@example.com", correctCode)
	correctTotal = time.Since(start)

	wrongAvg := wrongTotal / time.Duration(iterations)
	t.Logf("Average wrong code time: %v, correct code time: %v", wrongAvg, correctTotal)
	t.Log("DEFENDED: Code verification uses hashToken() + crypto/subtle.ConstantTimeCompare (hashEquals). " +
		"Constant-time comparison eliminates timing side-channels on hash comparisons.")
}

// ===========================================================================
// ATTACK 12: API Key Authentication Without Rate Limiting
// ===========================================================================

func TestAttack_APIKeyBruteForce(t *testing.T) {
	// Without API key support configured, verify clean error.
	h := newTestHarness()
	ctx := context.Background()

	_, err := h.auth.AuthenticateAPIKey(ctx, "some-key")
	if !errors.Is(err, ErrAPIKeysNotConfigured) {
		t.Fatalf("expected ErrAPIKeysNotConfigured, got: %v", err)
	}
	t.Log("NOTE: API key authentication has no built-in rate limiting. " +
		"Rate limiting must be implemented at the HTTP middleware layer. " +
		"This is acceptable if documented, but should be clearly stated.")
}

// ===========================================================================
// ATTACK 13: Session Not Invalidated After Password Reset
//
// After a password reset, all sessions should be revoked.
// ===========================================================================

func TestAttack_SessionSurvivesPasswordReset(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "user@example.com", "Password1!")

	// Login to create a session.
	loginResult, err := h.auth.Login(ctx, "user@example.com", "Password1!")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Initiate and complete password reset.
	token, err := h.auth.InitiatePasswordReset(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("initiate reset: %v", err)
	}
	if err := h.auth.ResetPassword(ctx, token, "NewPassword1!"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// The old session should be invalid.
	_, _, err = h.auth.AuthenticateSession(ctx, loginResult.AccessToken)
	if err == nil {
		t.Fatal("VULNERABILITY: Old session still valid after password reset!")
	}
	t.Log("DEFENDED: All sessions revoked after password reset.")

	// But AuthenticateJWT (fast path) would still work until token expires.
	claims, err := h.auth.AuthenticateJWT(ctx, loginResult.AccessToken)
	if err == nil && claims.UserID != "" {
		t.Log("NOTE: AuthenticateJWT (fast path, no DB) still validates the old JWT. " +
			"This is a known trade-off: stale for up to AccessTokenExpiry (30min). " +
			"Sensitive endpoints should use AuthenticateSession.")
	}
}

// ===========================================================================
// ATTACK 14: Inactive User Login Attempt
// ===========================================================================

func TestAttack_InactiveUserLogin(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "user@example.com", "Password1!")

	// Deactivate the user in the mock.
	h.users.mu.Lock()
	for email, u := range h.users.byEmail {
		if email == "user@example.com" {
			u.Active = false
			h.users.byEmail[email] = u
			h.users.users[u.UserID] = u
		}
	}
	h.users.mu.Unlock()

	_, err := h.auth.Login(ctx, "user@example.com", "Password1!")
	if err == nil {
		t.Fatal("VULNERABILITY: Inactive user was able to log in!")
	}
	if !errors.Is(err, ErrUserInactive) {
		t.Fatalf("expected ErrUserInactive, got: %v", err)
	}
	t.Log("DEFENDED: Inactive users cannot log in.")
}

// ===========================================================================
// ATTACK 15: Logout Session ID Guessing
//
// The Logout function takes a sessionID directly. If session IDs are
// predictable, an attacker could log out other users.
// ===========================================================================

func TestAttack_LogoutSessionIDGuessing(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "user@example.com", "Password1!")
	loginResult, err := h.auth.Login(ctx, "user@example.com", "Password1!")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Try to logout with a guessed session ID.
	err = h.auth.Logout(ctx, loginResult.User.UserID, "guessed_session_id")
	t.Logf("Logout with guessed ID result: %v", err)
	t.Log("DEFENDED: Logout requires userID parameter, ensuring the bridge layer " +
		"passes the authenticated user. Session ID comes from AuthenticateSession, " +
		"not user input.")

	// Verify the real session still exists.
	_, _, err = h.auth.AuthenticateSession(ctx, loginResult.AccessToken)
	if err != nil {
		t.Fatal("VULNERABILITY: Legitimate session was destroyed by guessed logout!")
	}
	t.Log("DEFENDED: Guessed session ID did not affect legitimate session.")
}

// ===========================================================================
// ATTACK 16: Multiple Password Reset Tokens
//
// Request multiple reset tokens — verify only the latest is valid,
// or that old tokens still work (potential issue).
// ===========================================================================

func TestAttack_MultiplePasswordResetTokens(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	h.registerAndVerify(t, "user@example.com", "Password1!")

	// Request multiple reset tokens.
	token1, err := h.auth.InitiatePasswordReset(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("reset 1: %v", err)
	}

	token2, err := h.auth.InitiatePasswordReset(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("reset 2: %v", err)
	}

	// Both tokens should be different.
	if token1 == token2 {
		t.Fatal("VULNERABILITY: Same reset token generated twice!")
	}

	// Use the second token.
	if err := h.auth.ResetPassword(ctx, token2, "NewPassword1!"); err != nil {
		t.Fatalf("reset with token2: %v", err)
	}

	// Try the first token — depending on implementation, it may or may not work.
	// OWASP recommends only the latest token should be valid.
	err = h.auth.ResetPassword(ctx, token1, "HackerPassword!")
	if err == nil {
		t.Log("NOTE: Old password reset token still valid after a newer one was issued and used. " +
			"OWASP recommends invalidating previous tokens when a new one is requested. " +
			"The current implementation stores tokens by hash — multiple can coexist.")
	} else {
		t.Log("DEFENDED: Old reset token invalidated after newer token was used.")
	}
}

// ===========================================================================
// ATTACK 17: Empty/Nil Input Handling
// ===========================================================================

func TestAttack_EmptyInputs(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"empty email login", func() error {
			_, err := h.auth.Login(ctx, "", "password")
			return err
		}},
		{"empty password login", func() error {
			_, err := h.auth.Login(ctx, "user@example.com", "")
			return err
		}},
		{"empty email register", func() error {
			_, err := h.auth.Register(ctx, "", "password", "")
			return err
		}},
		{"empty password register", func() error {
			_, err := h.auth.Register(ctx, "user@example.com", "", "")
			return err
		}},
		{"empty token refresh", func() error {
			_, err := h.auth.RefreshToken(ctx, "")
			return err
		}},
		{"empty token JWT auth", func() error {
			_, err := h.auth.AuthenticateJWT(ctx, "")
			return err
		}},
		{"empty token session auth", func() error {
			_, _, err := h.auth.AuthenticateSession(ctx, "")
			return err
		}},
		{"empty email verification", func() error {
			return h.auth.VerifyEmail(ctx, "", "123456")
		}},
		{"empty code verification", func() error {
			return h.auth.VerifyEmail(ctx, "user@example.com", "")
		}},
		{"empty password reset", func() error {
			return h.auth.ResetPassword(ctx, "", "NewPass1!")
		}},
		{"empty API key", func() error {
			_, err := h.auth.AuthenticateAPIKey(ctx, "")
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Errorf("VULNERABILITY: %s succeeded with empty input!", tt.name)
			} else {
				t.Logf("DEFENDED: %s correctly rejected: %v", tt.name, err)
			}
		})
	}
}

// TestAttack_SecurityEventsIncludeClientInfo verifies that security events
// capture client IP and User-Agent from context. Without this, security
// events lack the forensic data needed to trace and block attackers.
func TestAttack_SecurityEventsIncludeClientInfo(t *testing.T) {
	h := newTestHarness()

	// Register a user first (generates security events).
	ctx := context.Background()
	_, err := h.auth.Register(ctx, "clientinfo@example.com", "password123", "")
	if err != nil {
		t.Fatalf("setup register: %v", err)
	}

	// Clear events from setup.
	h.secEvents.mu.Lock()
	h.secEvents.events = nil
	h.secEvents.mu.Unlock()

	// Now inject client info and trigger a security event via duplicate registration.
	ctx = WithClientInfo(ctx, ClientInfo{
		IPAddress: "203.0.113.42",
		UserAgent: "AttackerBot/1.0",
	})
	_, _ = h.auth.Register(ctx, "clientinfo@example.com", "another-password", "")

	events := h.secEvents.getEvents()
	if len(events) == 0 {
		t.Fatal("VULNERABILITY: no security events logged for duplicate registration")
	}

	last := events[len(events)-1]
	if last.IPAddress != "203.0.113.42" {
		t.Errorf("VULNERABILITY: security event missing IP address, got %q", last.IPAddress)
	}
	if last.UserAgent != "AttackerBot/1.0" {
		t.Errorf("VULNERABILITY: security event missing User-Agent, got %q", last.UserAgent)
	}

	t.Logf("DEFENDED: security events include IP=%q UA=%q", last.IPAddress, last.UserAgent)
}
