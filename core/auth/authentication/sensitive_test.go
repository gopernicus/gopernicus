//go:build penetration

package authentication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// ---------------------------------------------------------------------------
// Mock OAuth account repo (only used by RemovePassword tests).
// ---------------------------------------------------------------------------

type mockOAuthAccountRepo struct {
	mu       sync.Mutex
	accounts map[string][]OAuthAccount // by user_id
}

func newMockOAuthAccountRepo() *mockOAuthAccountRepo {
	return &mockOAuthAccountRepo{accounts: make(map[string][]OAuthAccount)}
}

func (r *mockOAuthAccountRepo) GetByProvider(_ context.Context, provider, providerUserID string) (OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, list := range r.accounts {
		for _, a := range list {
			if a.Provider == provider && a.ProviderUserID == providerUserID {
				return a, nil
			}
		}
	}
	return OAuthAccount{}, errs.ErrNotFound
}

func (r *mockOAuthAccountRepo) GetByUserID(_ context.Context, userID string) ([]OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.accounts[userID]
	out := make([]OAuthAccount, len(list))
	copy(out, list)
	return out, nil
}

func (r *mockOAuthAccountRepo) Create(_ context.Context, a OAuthAccount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[a.UserID] = append(r.accounts[a.UserID], a)
	return nil
}

func (r *mockOAuthAccountRepo) Delete(_ context.Context, userID, provider string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.accounts[userID]
	out := list[:0]
	for _, a := range list {
		if a.Provider != provider {
			out = append(out, a)
		}
	}
	r.accounts[userID] = out
	return nil
}

// withOAuthRepo wires a mock OAuth account repo into the harness. Tests that
// exercise RemovePassword must call this — the base harness has no OAuth.
func (h *testHarness) withOAuthRepo() *mockOAuthAccountRepo {
	repo := newMockOAuthAccountRepo()
	h.auth.oauthRepo = repo
	// providers map left nil — RemovePassword doesn't go through requireOAuth.
	return repo
}

// ---------------------------------------------------------------------------
// SendSensitiveOpCode
// ---------------------------------------------------------------------------

func TestSendSensitiveOpCode_EmitsEventWithPurpose(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendSensitiveOpCode(ctx, userID)
	if err != nil {
		t.Fatalf("SendSensitiveOpCode: %v", err)
	}
	if code == "" {
		t.Fatal("expected non-empty code")
	}

	// Find the emitted event for our purpose.
	h.bus.mu.Lock()
	defer h.bus.mu.Unlock()

	var found *VerificationCodeRequestedEvent
	for _, e := range h.bus.events {
		if ev, ok := e.(VerificationCodeRequestedEvent); ok && ev.Purpose == PurposeSensitiveOp {
			ev := ev
			found = &ev
		}
	}
	if found == nil {
		t.Fatal("expected VerificationCodeRequestedEvent with PurposeSensitiveOp")
	}
	if found.UserID != userID {
		t.Errorf("event UserID = %q, want %q", found.UserID, userID)
	}
	if found.Code != code {
		t.Errorf("event Code = %q, want %q", found.Code, code)
	}
}

func TestSendSensitiveOpCode_StoresCodeKeyedByUserID(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if _, err := h.auth.SendSensitiveOpCode(ctx, userID); err != nil {
		t.Fatalf("SendSensitiveOpCode: %v", err)
	}

	// Code should be stored under userID, NOT email.
	if _, err := h.codes.Get(ctx, userID, PurposeSensitiveOp); err != nil {
		t.Fatalf("expected code stored under user_id, got: %v", err)
	}
	if _, err := h.codes.Get(ctx, "user@example.com", PurposeSensitiveOp); err == nil {
		t.Fatal("code should NOT be stored under email")
	}
}

func TestSendSensitiveOpCode_OverwritesPreviousCode(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	first, err := h.auth.SendSensitiveOpCode(ctx, userID)
	if err != nil {
		t.Fatalf("first SendSensitiveOpCode: %v", err)
	}
	second, err := h.auth.SendSensitiveOpCode(ctx, userID)
	if err != nil {
		t.Fatalf("second SendSensitiveOpCode: %v", err)
	}
	if first == second {
		t.Fatal("expected new code on re-issue")
	}

	// Old code must not verify.
	if err := h.auth.VerifySensitiveOpCode(ctx, userID, first); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("verifying superseded code: got %v, want ErrCodeInvalid", err)
	}
	// New code must verify.
	if err := h.auth.VerifySensitiveOpCode(ctx, userID, second); err != nil {
		t.Errorf("verifying current code: got %v, want nil", err)
	}
}

func TestSendSensitiveOpCode_UnknownUser(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()

	_, err := h.auth.SendSensitiveOpCode(ctx, "ghost_user")
	if !errors.Is(err, ErrUserInactive) {
		t.Errorf("got %v, want ErrUserInactive", err)
	}
}

// ---------------------------------------------------------------------------
// VerifySensitiveOpCode
// ---------------------------------------------------------------------------

func TestVerifySensitiveOpCode_HappyPath(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendSensitiveOpCode(ctx, userID)
	if err != nil {
		t.Fatalf("SendSensitiveOpCode: %v", err)
	}

	if err := h.auth.VerifySensitiveOpCode(ctx, userID, code); err != nil {
		t.Fatalf("VerifySensitiveOpCode: %v", err)
	}

	// Code is consumed (one-shot) — second use must fail.
	if err := h.auth.VerifySensitiveOpCode(ctx, userID, code); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("second use: got %v, want ErrCodeInvalid", err)
	}
}

func TestVerifySensitiveOpCode_DoesNotMutateUserState(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	// Snapshot user before verification.
	before, err := h.users.Get(ctx, userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	code, err := h.auth.SendSensitiveOpCode(ctx, userID)
	if err != nil {
		t.Fatalf("SendSensitiveOpCode: %v", err)
	}
	if err := h.auth.VerifySensitiveOpCode(ctx, userID, code); err != nil {
		t.Fatalf("VerifySensitiveOpCode: %v", err)
	}

	after, err := h.users.Get(ctx, userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if before != after {
		t.Errorf("user state mutated by VerifySensitiveOpCode\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func TestVerifySensitiveOpCode_WrongCode(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if _, err := h.auth.SendSensitiveOpCode(ctx, userID); err != nil {
		t.Fatalf("SendSensitiveOpCode: %v", err)
	}

	if err := h.auth.VerifySensitiveOpCode(ctx, userID, "000000"); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("got %v, want ErrCodeInvalid", err)
	}
}

func TestVerifySensitiveOpCode_NoCodeIssued(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if err := h.auth.VerifySensitiveOpCode(ctx, userID, "123456"); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("got %v, want ErrCodeInvalid", err)
	}
}

func TestVerifySensitiveOpCode_Expired(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendSensitiveOpCode(ctx, userID)
	if err != nil {
		t.Fatalf("SendSensitiveOpCode: %v", err)
	}

	// Force expiry by mutating the stored code in place.
	h.codes.mu.Lock()
	stored := h.codes.codes[codeKey(userID, PurposeSensitiveOp)]
	stored.ExpiresAt = time.Now().UTC().Add(-1 * time.Minute)
	h.codes.codes[codeKey(userID, PurposeSensitiveOp)] = stored
	h.codes.mu.Unlock()

	if err := h.auth.VerifySensitiveOpCode(ctx, userID, code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("got %v, want ErrCodeExpired", err)
	}

	// Expired code is deleted on lookup.
	if _, err := h.codes.Get(ctx, userID, PurposeSensitiveOp); err == nil {
		t.Error("expired code should be deleted")
	}
}

func TestVerifySensitiveOpCode_LockoutAfterMaxAttempts(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if _, err := h.auth.SendSensitiveOpCode(ctx, userID); err != nil {
		t.Fatalf("SendSensitiveOpCode: %v", err)
	}

	// Burn attempts up to the lockout threshold (default 5 in test config).
	// First MaxAttempts-1 wrong guesses return ErrCodeInvalid; the final one
	// returns ErrTooManyAttempts and consumes the code.
	for i := 0; i < 4; i++ {
		if err := h.auth.VerifySensitiveOpCode(ctx, userID, "000000"); !errors.Is(err, ErrCodeInvalid) {
			t.Fatalf("attempt %d: got %v, want ErrCodeInvalid", i+1, err)
		}
	}
	if err := h.auth.VerifySensitiveOpCode(ctx, userID, "000000"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("lockout attempt: got %v, want ErrTooManyAttempts", err)
	}

	// Code is gone — even the correct value would now fail.
	if _, err := h.codes.Get(ctx, userID, PurposeSensitiveOp); err == nil {
		t.Error("locked-out code should be deleted")
	}
}

// ---------------------------------------------------------------------------
// RemovePassword
// ---------------------------------------------------------------------------

func TestRemovePassword_HappyPath(t *testing.T) {
	h := newTestHarness()
	oauth := h.withOAuthRepo()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	// User must have at least one linked OAuth account.
	if err := oauth.Create(ctx, OAuthAccount{
		UserID:         userID,
		Provider:       "google",
		ProviderUserID: "google_123",
	}); err != nil {
		t.Fatalf("create oauth account: %v", err)
	}

	if err := h.auth.RemovePassword(ctx, userID); err != nil {
		t.Fatalf("RemovePassword: %v", err)
	}

	// Password row is gone.
	if _, err := h.passwords.GetByUserID(ctx, userID); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("password row still present: %v", err)
	}

	// Security event was logged.
	var sawRemoval bool
	for _, e := range h.secEvents.getEvents() {
		if e.EventType == SecEventPasswordRemoved && e.UserID == userID {
			sawRemoval = true
			break
		}
	}
	if !sawRemoval {
		t.Error("expected SecEventPasswordRemoved security event")
	}
}

func TestRemovePassword_NoOtherAuthMethod(t *testing.T) {
	h := newTestHarness()
	h.withOAuthRepo() // OAuth wired but user has no linked accounts
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if err := h.auth.RemovePassword(ctx, userID); !errors.Is(err, ErrCannotRemoveLastMethod) {
		t.Errorf("got %v, want ErrCannotRemoveLastMethod", err)
	}

	// Password row must still exist.
	if _, err := h.passwords.GetByUserID(ctx, userID); err != nil {
		t.Errorf("password should still exist after refused removal: %v", err)
	}
}

func TestRemovePassword_NoOAuthRepoConfigured(t *testing.T) {
	h := newTestHarness() // OAuth not wired at all
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if err := h.auth.RemovePassword(ctx, userID); !errors.Is(err, ErrCannotRemoveLastMethod) {
		t.Errorf("got %v, want ErrCannotRemoveLastMethod", err)
	}
}

func TestRemovePassword_NoPasswordSet(t *testing.T) {
	h := newTestHarness()
	h.withOAuthRepo()
	ctx := context.Background()

	// Create a user directly with no password row.
	user, err := h.users.Create(ctx, CreateUserInput{
		Email:         "oauth@example.com",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := h.auth.RemovePassword(ctx, user.UserID); !errors.Is(err, ErrPasswordNotSet) {
		t.Errorf("got %v, want ErrPasswordNotSet", err)
	}
}

func TestRemovePassword_ClearsPendingResetTokens(t *testing.T) {
	h := newTestHarness()
	oauth := h.withOAuthRepo()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if err := oauth.Create(ctx, OAuthAccount{
		UserID:         userID,
		Provider:       "google",
		ProviderUserID: "google_123",
	}); err != nil {
		t.Fatalf("create oauth: %v", err)
	}

	// Stash a pending reset token.
	if err := h.tokens.Create(ctx, VerificationToken{
		Identifier: "stale_hash",
		TokenHash:  "stale_hash",
		UserID:     userID,
		Purpose:    PurposePasswordReset,
		ExpiresAt:  time.Now().UTC().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}

	if err := h.auth.RemovePassword(ctx, userID); err != nil {
		t.Fatalf("RemovePassword: %v", err)
	}

	// Pending reset token should be gone.
	if _, err := h.tokens.Get(ctx, "stale_hash", PurposePasswordReset); err == nil {
		t.Error("pending password reset token should be cleared after RemovePassword")
	}
}
