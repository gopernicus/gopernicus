//go:build penetration

package authentication

import (
	"context"
	"encoding/json"
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
// exercise OAuth-aware paths must call this — the base harness has no OAuth.
func (h *testHarness) withOAuthRepo() *mockOAuthAccountRepo {
	repo := newMockOAuthAccountRepo()
	h.auth.oauthRepo = repo
	// providers map left nil — the destructive ops don't go through requireOAuth.
	return repo
}

// ===========================================================================
// IssueVerificationCode / ConsumeVerificationCode (generic primitives)
// ===========================================================================

func TestIssueVerificationCode_StoresCodeKeyedByUserID(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	const purpose = "wire_transfer"

	code, err := h.auth.IssueVerificationCode(ctx, userID, purpose)
	if err != nil {
		t.Fatalf("IssueVerificationCode: %v", err)
	}
	if code == "" {
		t.Fatal("expected non-empty code")
	}

	// Code is stored under (user_id, purpose), NOT email.
	if _, err := h.codes.Get(ctx, userID, purpose); err != nil {
		t.Errorf("code not stored under user_id: %v", err)
	}
	if _, err := h.codes.Get(ctx, "user@example.com", purpose); err == nil {
		t.Error("code should NOT be stored under email")
	}
}

func TestIssueVerificationCode_DoesNotEmitEvent(t *testing.T) {
	// IssueVerificationCode is the building block — it must NOT emit any
	// event on its own. Callers (built-in or user) emit their own typed events.
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	// Drain any events emitted by registerAndVerify.
	h.bus.mu.Lock()
	h.bus.events = nil
	h.bus.mu.Unlock()

	if _, err := h.auth.IssueVerificationCode(ctx, userID, "anything"); err != nil {
		t.Fatalf("IssueVerificationCode: %v", err)
	}

	h.bus.mu.Lock()
	defer h.bus.mu.Unlock()
	if len(h.bus.events) != 0 {
		t.Errorf("IssueVerificationCode should not emit any events, got %d", len(h.bus.events))
	}
}

func TestIssueVerificationCode_OverwritesPriorCodeForSamePurpose(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	first, err := h.auth.IssueVerificationCode(ctx, userID, "vault")
	if err != nil {
		t.Fatalf("first issue: %v", err)
	}
	second, err := h.auth.IssueVerificationCode(ctx, userID, "vault")
	if err != nil {
		t.Fatalf("second issue: %v", err)
	}
	if first == second {
		t.Fatal("expected new code on re-issue")
	}

	// Old code must not consume.
	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, first, "vault"); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("consuming superseded code: got %v, want ErrCodeInvalid", err)
	}
	// New code consumes.
	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, second, "vault"); err != nil {
		t.Errorf("consuming current code: got %v, want nil", err)
	}
}

func TestIssueVerificationCode_EmptyPurposeRejected(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")
	if _, err := h.auth.IssueVerificationCode(ctx, userID, ""); err == nil {
		t.Fatal("expected error for empty purpose")
	}
}

func TestIssueVerificationCode_UnknownUser(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	if _, err := h.auth.IssueVerificationCode(context.Background(), "ghost", "purpose"); !errors.Is(err, ErrUserInactive) {
		t.Errorf("got %v, want ErrUserInactive", err)
	}
	_ = ctx
}

func TestIssueVerificationCode_WithStoredContext(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	type ctxShape struct {
		Resource string `json:"resource"`
		Amount   int    `json:"amount"`
	}

	in := ctxShape{Resource: "tx-42", Amount: 1000}
	code, err := h.auth.IssueVerificationCode(ctx, userID, "wire_transfer", WithStoredContext(in))
	if err != nil {
		t.Fatalf("IssueVerificationCode: %v", err)
	}

	storedContext, err := h.auth.ConsumeVerificationCode(ctx, userID, code, "wire_transfer")
	if err != nil {
		t.Fatalf("ConsumeVerificationCode: %v", err)
	}
	if len(storedContext) == 0 {
		t.Fatal("expected stored context bytes")
	}

	var out ctxShape
	if err := json.Unmarshal(storedContext, &out); err != nil {
		t.Fatalf("unmarshal stored context: %v", err)
	}
	if out != in {
		t.Errorf("stored context = %+v, want %+v", out, in)
	}
}

func TestConsumeVerificationCode_HappyPath(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.IssueVerificationCode(ctx, userID, "vault")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, code, "vault"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	// One-shot.
	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, code, "vault"); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("second consume: got %v, want ErrCodeInvalid", err)
	}
}

func TestConsumeVerificationCode_PurposeIsolation(t *testing.T) {
	// Codes issued under one purpose cannot be consumed under another.
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.IssueVerificationCode(ctx, userID, "wire_transfer")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, code, "vault"); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("got %v, want ErrCodeInvalid (purpose mismatch)", err)
	}
	// Original purpose still works.
	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, code, "wire_transfer"); err != nil {
		t.Errorf("original purpose: %v", err)
	}
}

func TestConsumeVerificationCode_Expired(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.IssueVerificationCode(ctx, userID, "vault")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Force expiry.
	h.codes.mu.Lock()
	stored := h.codes.codes[codeKey(userID, "vault")]
	stored.ExpiresAt = time.Now().UTC().Add(-1 * time.Minute)
	h.codes.codes[codeKey(userID, "vault")] = stored
	h.codes.mu.Unlock()

	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, code, "vault"); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("got %v, want ErrCodeExpired", err)
	}
	if _, err := h.codes.Get(ctx, userID, "vault"); err == nil {
		t.Error("expired code should be deleted")
	}
}

func TestConsumeVerificationCode_LockoutAfterMaxAttempts(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	if _, err := h.auth.IssueVerificationCode(ctx, userID, "vault"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Default MaxAttempts in test config = 5; first 4 wrong → ErrCodeInvalid,
	// 5th → ErrTooManyAttempts.
	for i := 0; i < 4; i++ {
		if _, err := h.auth.ConsumeVerificationCode(ctx, userID, "000000", "vault"); !errors.Is(err, ErrCodeInvalid) {
			t.Fatalf("attempt %d: got %v, want ErrCodeInvalid", i+1, err)
		}
	}
	if _, err := h.auth.ConsumeVerificationCode(ctx, userID, "000000", "vault"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("lockout: got %v, want ErrTooManyAttempts", err)
	}
	if _, err := h.codes.Get(ctx, userID, "vault"); err == nil {
		t.Error("locked-out code should be deleted")
	}
}

// ===========================================================================
// SendRemovePasswordCode / VerifyRemovePasswordCode
// ===========================================================================

func TestSendRemovePasswordCode_EmitsTypedEvent(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendRemovePasswordCode(ctx, userID)
	if err != nil {
		t.Fatalf("SendRemovePasswordCode: %v", err)
	}
	if code == "" {
		t.Fatal("expected non-empty code")
	}

	// Find the typed event — must be RemovePasswordCodeRequestedEvent, not
	// the generic VerificationCodeRequestedEvent.
	h.bus.mu.Lock()
	defer h.bus.mu.Unlock()
	var found *RemovePasswordCodeRequestedEvent
	for _, e := range h.bus.events {
		if ev, ok := e.(RemovePasswordCodeRequestedEvent); ok {
			ev := ev
			found = &ev
		}
	}
	if found == nil {
		t.Fatal("expected RemovePasswordCodeRequestedEvent")
	}
	if found.UserID != userID {
		t.Errorf("event UserID = %q, want %q", found.UserID, userID)
	}
	if found.Email != "user@example.com" {
		t.Errorf("event Email = %q, want user@example.com", found.Email)
	}
	if found.Code != code {
		t.Errorf("event Code = %q, want %q", found.Code, code)
	}
}

func TestVerifyRemovePasswordCode_HappyPath(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendRemovePasswordCode(ctx, userID)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := h.auth.VerifyRemovePasswordCode(ctx, userID, code); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// One-shot.
	if err := h.auth.VerifyRemovePasswordCode(ctx, userID, code); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("second verify: got %v, want ErrCodeInvalid", err)
	}
}

func TestVerifyRemovePasswordCode_DoesNotMutateUserState(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	before, err := h.users.Get(ctx, userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	code, _ := h.auth.SendRemovePasswordCode(ctx, userID)
	if err := h.auth.VerifyRemovePasswordCode(ctx, userID, code); err != nil {
		t.Fatalf("verify: %v", err)
	}
	after, _ := h.users.Get(ctx, userID)
	if before != after {
		t.Errorf("user state mutated\nbefore: %+v\nafter:  %+v", before, after)
	}
}

// ===========================================================================
// SendUnlinkOAuthCode / VerifyUnlinkOAuthCode
// ===========================================================================

func TestSendUnlinkOAuthCode_EmitsTypedEventWithProvider(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendUnlinkOAuthCode(ctx, userID, "google")
	if err != nil {
		t.Fatalf("SendUnlinkOAuthCode: %v", err)
	}
	if code == "" {
		t.Fatal("expected non-empty code")
	}

	h.bus.mu.Lock()
	defer h.bus.mu.Unlock()
	var found *UnlinkOAuthCodeRequestedEvent
	for _, e := range h.bus.events {
		if ev, ok := e.(UnlinkOAuthCodeRequestedEvent); ok {
			ev := ev
			found = &ev
		}
	}
	if found == nil {
		t.Fatal("expected UnlinkOAuthCodeRequestedEvent")
	}
	if found.Provider != "google" {
		t.Errorf("event Provider = %q, want google", found.Provider)
	}
	if found.Code != code {
		t.Errorf("event Code = %q, want %q", found.Code, code)
	}
}

func TestSendUnlinkOAuthCode_EmptyProviderRejected(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")
	if _, err := h.auth.SendUnlinkOAuthCode(ctx, userID, ""); err == nil {
		t.Fatal("expected error for empty provider")
	}
}

func TestVerifyUnlinkOAuthCode_HappyPath(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendUnlinkOAuthCode(ctx, userID, "google")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := h.auth.VerifyUnlinkOAuthCode(ctx, userID, code, "google"); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyUnlinkOAuthCode_ProviderMismatch(t *testing.T) {
	// Code issued for one provider must NOT verify against another. The code
	// is consumed regardless — wrong-provider attempts force a re-issue.
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

	code, err := h.auth.SendUnlinkOAuthCode(ctx, userID, "google")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := h.auth.VerifyUnlinkOAuthCode(ctx, userID, code, "github"); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("got %v, want ErrCodeInvalid (provider mismatch)", err)
	}

	// Code is gone — even the original provider with the original code now fails.
	if err := h.auth.VerifyUnlinkOAuthCode(ctx, userID, code, "google"); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("post-mismatch retry: got %v, want ErrCodeInvalid", err)
	}
}

func TestVerifyUnlinkOAuthCode_EmptyProviderRejected(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")
	if err := h.auth.VerifyUnlinkOAuthCode(ctx, userID, "123456", ""); err == nil {
		t.Fatal("expected error for empty provider")
	}
}

// ===========================================================================
// RemovePassword (existing core method, retested in this rebuild for sanity)
// ===========================================================================

func TestRemovePassword_HappyPath(t *testing.T) {
	h := newTestHarness()
	oauth := h.withOAuthRepo()
	ctx := context.Background()
	userID := h.registerAndVerify(t, "user@example.com", "Password1!")

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
	if _, err := h.passwords.GetByUserID(ctx, userID); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("password row still present: %v", err)
	}

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
	h.withOAuthRepo()
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
	user, err := h.users.Create(ctx, CreateUserInput{Email: "oauth@example.com", EmailVerified: true})
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
		UserID: userID, Provider: "google", ProviderUserID: "google_123",
	}); err != nil {
		t.Fatalf("create oauth: %v", err)
	}
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
	if _, err := h.tokens.Get(ctx, "stale_hash", PurposePasswordReset); err == nil {
		t.Error("pending password reset token should be cleared after RemovePassword")
	}
}
