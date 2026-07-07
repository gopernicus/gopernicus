// Package storetest is the conformance suite for the auth feature's repository
// ports: every store that fills an auth.Repositories — the in-package reference
// implementation, a host memstore, and each dialect adapter (features/auth/
// stores/turso, .../postgres) — should pass Run against a freshly wired,
// isolated Repositories. The port doc comments are the spec; this suite is their
// executable form.
//
// It imports stdlib + sdk + the auth feature's own packages only (guard G2
// forbids a driver import here), so features/auth's own `go test ./...` runs it
// against the reference implementation (see reference_test.go).
//
// No auth v1 port paginates — the ports are all key/email lookups, so there is
// no cursor pagination or timestamp-precision-collision case to assert (those
// apply only where a port promises a paged List, which none here do). The suite
// exercises the sentinel contract, email/id uniqueness, upsert semantics, and
// expired-at-read session/code/token behavior.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/auth"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthstate"
	"github.com/gopernicus/gopernicus/features/auth/logic/session"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/features/auth/logic/verification"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// suiteBase is a fixed reference instant. Expiry cases offset from time.Now so
// the reference impl's real-clock expiry check observes the intended state.
var suiteBase = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// Run exercises the auth.Repositories contract against a clean, isolated set
// obtained from newRepos for each leaf subtest. newRepos MUST return a CLEAN,
// isolated Repositories per call: SQL-backed harnesses truncate their tables;
// memory harnesses return a fresh instance.
func Run(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()

	t.Run("Users", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testUsersCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testUsersAbsent(t, newRepos(t)) })
		t.Run("EmailUniqueness", func(t *testing.T) { testUsersEmailUniqueness(t, newRepos(t)) })
	})

	t.Run("Passwords", func(t *testing.T) {
		t.Run("SetGetUpsert", func(t *testing.T) { testPasswords(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testPasswordsAbsent(t, newRepos(t)) })
	})

	t.Run("Sessions", func(t *testing.T) {
		t.Run("CreateGetDelete", func(t *testing.T) { testSessionsCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testSessionsAbsent(t, newRepos(t)) })
		t.Run("ExpiredAtRead", func(t *testing.T) { testSessionsExpired(t, newRepos(t)) })
		t.Run("DeleteByUser", func(t *testing.T) { testSessionsDeleteByUser(t, newRepos(t)) })
	})

	t.Run("VerificationCodes", func(t *testing.T) {
		t.Run("CreateGetDelete", func(t *testing.T) { testCodesCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testCodesAbsent(t, newRepos(t)) })
		t.Run("ExpiredAtRead", func(t *testing.T) { testCodesExpired(t, newRepos(t)) })
	})

	t.Run("VerificationTokens", func(t *testing.T) {
		t.Run("CreateGetDelete", func(t *testing.T) { testTokensCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testTokensAbsent(t, newRepos(t)) })
		t.Run("ExpiredAtRead", func(t *testing.T) { testTokensExpired(t, newRepos(t)) })
	})

	// OAuth ports are optional (a host that wires no providers leaves them nil).
	// When present they are exercised in full; when absent the group skips
	// LOUDLY — a silent green would claim OAuth conformance nothing verified.
	t.Run("OAuthAccounts", func(t *testing.T) {
		if newRepos(t).OAuthAccounts == nil {
			t.Skip("OAuthAccounts not wired — OAuth account conformance NOT verified for this Repositories")
		}
		t.Run("CRUDRoundTrip", func(t *testing.T) { testOAuthAccountsCRUD(t, newRepos(t)) })
		t.Run("ProviderUniqueness", func(t *testing.T) { testOAuthAccountsUniqueness(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testOAuthAccountsAbsent(t, newRepos(t)) })
		t.Run("ListByUser", func(t *testing.T) { testOAuthAccountsListByUser(t, newRepos(t)) })
		t.Run("DeleteAbsentNotFound", func(t *testing.T) { testOAuthAccountsDeleteAbsent(t, newRepos(t)) })
	})

	t.Run("OAuthStates", func(t *testing.T) {
		if newRepos(t).OAuthStates == nil {
			t.Skip("OAuthStates not wired — OAuth state conformance NOT verified for this Repositories")
		}
		t.Run("ConsumeSingleUse", func(t *testing.T) { testOAuthStatesConsume(t, newRepos(t)) })
		t.Run("ConsumeExpiredDeletes", func(t *testing.T) { testOAuthStatesExpired(t, newRepos(t)) })
		t.Run("ConsumeUnknown", func(t *testing.T) { testOAuthStatesUnknown(t, newRepos(t)) })
	})
}

// --- Users ---

func testUsersCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	u, err := user.NewUser("Alice@Example.com", "Alice", suiteBase)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	created, err := repo.Create(ctx, u)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Email != "alice@example.com" {
		t.Errorf("Create did not persist normalized email: %q", created.Email)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.DisplayName != "Alice" {
		t.Fatalf("Get: name=%q err=%v", got.DisplayName, err)
	}
	byEmail, err := repo.GetByEmail(ctx, "alice@example.com")
	if err != nil || byEmail.ID != created.ID {
		t.Fatalf("GetByEmail: id=%q err=%v", byEmail.ID, err)
	}

	got.MarkVerified(suiteBase.Add(time.Minute))
	if _, err := repo.Update(ctx, got.ID, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reget, err := repo.Get(ctx, created.ID)
	if err != nil || !reget.EmailVerified {
		t.Fatalf("Get after Update: verified=%v err=%v", reget.EmailVerified, err)
	}
}

func testUsersAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetByEmail(ctx, "ghost@example.com"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByEmail(absent): err=%v, want ErrNotFound", err)
	}
	absent, _ := user.NewUser("ghost@example.com", "Ghost", suiteBase)
	if _, err := repo.Update(ctx, "nope", absent); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
}

func testUsersEmailUniqueness(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	a, _ := user.NewUser("dup@example.com", "First", suiteBase)
	if _, err := repo.Create(ctx, a); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Same address in a different case normalizes to the same email → collision.
	b, _ := user.NewUser("DUP@example.com", "Second", suiteBase)
	if _, err := repo.Create(ctx, b); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create colliding email: err=%v, want ErrAlreadyExists", err)
	}
	other, _ := user.NewUser("other@example.com", "Other", suiteBase)
	if _, err := repo.Create(ctx, other); err != nil {
		t.Errorf("Create distinct email: err=%v, want nil", err)
	}
}

// --- Passwords ---

func testPasswords(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Passwords

	if err := repo.Set(ctx, "u1", "hash-one"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := repo.Get(ctx, "u1")
	if err != nil || got != "hash-one" {
		t.Fatalf("Get: %q err=%v", got, err)
	}
	// Set upserts: a second Set replaces, never collides.
	if err := repo.Set(ctx, "u1", "hash-two"); err != nil {
		t.Fatalf("Set (replace): %v", err)
	}
	got, err = repo.Get(ctx, "u1")
	if err != nil || got != "hash-two" {
		t.Fatalf("Get after replace: %q err=%v", got, err)
	}
}

func testPasswordsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.Passwords.Get(ctx, "nobody"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
}

// --- Sessions ---

func testSessionsCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := session.NewSession("u1", time.Hour, time.Now())
	if _, err := repo.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, sess.Token)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("Get: user=%q err=%v", got.UserID, err)
	}
	if err := repo.Delete(ctx, sess.Token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, sess.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testSessionsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testSessionsExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	// A session created one hour ago with a one-minute lifetime is expired now.
	sess := session.NewSession("u1", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Get(ctx, sess.Token); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Get(expired): err=%v, want ErrExpired", err)
	}
}

// testSessionsDeleteByUser asserts the bulk, idempotent DeleteByUser contract:
// it removes every session for the target user, leaves other users' sessions
// intact, and returns nil on a second call when none remain (never ErrNotFound).
func testSessionsDeleteByUser(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	a1 := session.NewSession("userA", time.Hour, time.Now())
	a2 := session.NewSession("userA", time.Hour, time.Now())
	b1 := session.NewSession("userB", time.Hour, time.Now())
	for _, s := range []session.Session{a1, a2, b1} {
		if _, err := repo.Create(ctx, s); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	if err := repo.DeleteByUser(ctx, "userA"); err != nil {
		t.Fatalf("DeleteByUser(userA): %v", err)
	}
	for _, tok := range []string{a1.Token, a2.Token} {
		if _, err := repo.Get(ctx, tok); !errors.Is(err, errs.ErrNotFound) {
			t.Errorf("Get(userA session after DeleteByUser): err=%v, want ErrNotFound", err)
		}
	}
	if got, err := repo.Get(ctx, b1.Token); err != nil || got.UserID != "userB" {
		t.Errorf("userB session removed by DeleteByUser(userA): user=%q err=%v", got.UserID, err)
	}

	// Idempotent: a repeat with zero matching rows returns nil, not ErrNotFound.
	if err := repo.DeleteByUser(ctx, "userA"); err != nil {
		t.Errorf("second DeleteByUser(userA): err=%v, want nil", err)
	}
}

// --- VerificationCodes ---

func testCodesCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationCodes

	code := verification.NewCode("u1", time.Hour, time.Now())
	if _, err := repo.Create(ctx, code); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, code.Code)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("Get: user=%q err=%v", got.UserID, err)
	}
	if err := repo.Delete(ctx, code.Code); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, code.Code); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testCodesAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationCodes
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testCodesExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationCodes

	code := verification.NewCode("u1", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, code); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Get(ctx, code.Code); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Get(expired): err=%v, want ErrExpired", err)
	}
}

// --- VerificationTokens ---

func testTokensCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationTokens

	tok := verification.NewToken("u1", time.Hour, time.Now())
	if _, err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, tok.Token)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("Get: user=%q err=%v", got.UserID, err)
	}
	if err := repo.Delete(ctx, tok.Token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, tok.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testTokensAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationTokens
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testTokensExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationTokens

	tok := verification.NewToken("u1", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Get(ctx, tok.Token); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Get(expired): err=%v, want ErrExpired", err)
	}
}

// --- OAuthAccounts ---

func testOAuthAccountsCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthAccounts

	acct, err := oauthaccount.New("user1", "google", "google-123", suiteBase)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	acct.ProviderEmail = "u@example.com"
	acct.ProviderEmailVerified = true
	acct.AccessToken = "cipher-abc" // the store persists the opaque (encrypted) value verbatim
	acct.TokenType = "Bearer"

	created, err := repo.Create(ctx, acct)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.UserID != "user1" {
		t.Errorf("Create UserID = %q, want user1", created.UserID)
	}

	got, err := repo.GetByProvider(ctx, "google", "google-123")
	if err != nil {
		t.Fatalf("GetByProvider: %v", err)
	}
	if got.UserID != "user1" || got.ProviderEmail != "u@example.com" || got.AccessToken != "cipher-abc" {
		t.Errorf("GetByProvider round-trip lost data: %+v", got)
	}

	if err := repo.Delete(ctx, "user1", "google"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByProvider(ctx, "google", "google-123"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByProvider after Delete: err=%v, want ErrNotFound", err)
	}
}

func testOAuthAccountsUniqueness(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthAccounts

	a, _ := oauthaccount.New("user1", "google", "dup-123", suiteBase)
	if _, err := repo.Create(ctx, a); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// The SAME provider identity claimed by a different user → collision.
	b, _ := oauthaccount.New("user2", "google", "dup-123", suiteBase)
	if _, err := repo.Create(ctx, b); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create colliding (provider, provider_user_id): err=%v, want ErrAlreadyExists", err)
	}
	// A distinct provider identity is fine.
	c, _ := oauthaccount.New("user2", "google", "other-456", suiteBase)
	if _, err := repo.Create(ctx, c); err != nil {
		t.Errorf("Create distinct identity: err=%v, want nil", err)
	}
}

func testOAuthAccountsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.OAuthAccounts.GetByProvider(ctx, "google", "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByProvider(absent): err=%v, want ErrNotFound", err)
	}
}

func testOAuthAccountsListByUser(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthAccounts

	g1, _ := oauthaccount.New("user1", "google", "g1", suiteBase)
	gh1, _ := oauthaccount.New("user1", "github", "gh1", suiteBase)
	g2, _ := oauthaccount.New("user2", "google", "g2", suiteBase)
	for _, a := range []oauthaccount.OAuthAccount{g1, gh1, g2} {
		if _, err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	list, err := repo.ListByUser(ctx, "user1")
	if err != nil {
		t.Fatalf("ListByUser(user1): %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListByUser(user1) len = %d, want 2", len(list))
	}
	providers := map[string]bool{}
	for _, a := range list {
		providers[a.Provider] = true
		if a.UserID != "user1" {
			t.Errorf("ListByUser(user1) returned a foreign user: %+v", a)
		}
	}
	if !providers["google"] || !providers["github"] {
		t.Errorf("ListByUser(user1) providers = %v, want google+github", providers)
	}

	if list, _ := repo.ListByUser(ctx, "user2"); len(list) != 1 {
		t.Errorf("ListByUser(user2) len = %d, want 1", len(list))
	}
	empty, err := repo.ListByUser(ctx, "nobody")
	if err != nil {
		t.Errorf("ListByUser(nobody): err=%v, want nil", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListByUser(nobody) len = %d, want 0", len(empty))
	}
}

func testOAuthAccountsDeleteAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if err := repos.OAuthAccounts.Delete(ctx, "nobody", "google"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

// --- OAuthStates ---

func testOAuthStatesConsume(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthStates

	payload := []byte(`{"code_verifier":"v","nonce":"n"}`)
	st := oauthstate.New("google", oauthstate.PurposeFlow, payload, time.Hour, time.Now())
	if _, err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Consume(ctx, st.Token)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Provider != "google" || got.Purpose != oauthstate.PurposeFlow || string(got.Payload) != string(payload) {
		t.Errorf("Consume round-trip lost data: %+v", got)
	}

	// Single-use: a second Consume of the same token → ErrNotFound (it is gone).
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("second Consume: err=%v, want ErrNotFound", err)
	}
}

func testOAuthStatesExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthStates

	// Created one hour ago with a one-minute lifetime → expired now.
	st := oauthstate.New("google", oauthstate.PurposePendingLink, []byte("payload"), time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Expired Consume deletes the row AND returns ErrExpired.
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Consume(expired): err=%v, want ErrExpired", err)
	}
	// Row is gone (deleted regardless of expiry): a follow-up Consume → ErrNotFound.
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Consume after expired-delete: err=%v, want ErrNotFound", err)
	}
}

func testOAuthStatesUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.OAuthStates.Consume(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Consume(unknown): err=%v, want ErrNotFound", err)
	}
}
