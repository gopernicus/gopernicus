package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

func ensurePasswordOwner(t *testing.T, repos auth.Repositories, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := repos.Users.Get(ctx, id); err == nil {
		return
	} else if !errors.Is(err, sdk.ErrNotFound) {
		t.Fatal(err)
	}
	u := user.NewUser(ids, "Credential fixture", time.Now())
	u.ID = id
	ident, err := identifier.NewRegistrationEmail(ids, idNorm, id, id+"@example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx, u, ident); err != nil {
		t.Fatal(err)
	}
}
func runCredentialAdmission(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Run("CredentialAdmission", func(t *testing.T) {
		for _, mode := range []string{"change", "trusted_set", "reset", "initial_set", "oauth_delete"} {
			t.Run(mode, func(t *testing.T) { testCredentialAdmission(t, newRepos(t), mode) })
		}
		t.Run("ConcurrentChangeSingleWinner", func(t *testing.T) { testConcurrentPasswordChange(t, newRepos(t), false) })
		t.Run("ConcurrentInitialPasswordSingleWinner", func(t *testing.T) { testConcurrentPasswordChange(t, newRepos(t), true) })
		t.Run("ProvisionRollbackOnProviderCollision", func(t *testing.T) { testProvisionCollision(t, newRepos(t)) })
		t.Run("ProfileUpdatePreservesCredentialRevision", func(t *testing.T) { testProfileUpdatePreservesCredentialRevision(t, newRepos(t)) })
		t.Run("OAuthAdoptionRejectsContactOnlyIdentifier", func(t *testing.T) { testOAuthAdoptionContactOnly(t, newRepos(t)) })
		t.Run("OAuthAdoptionAtomic", func(t *testing.T) { testAtomicOAuthAdoption(t, newRepos(t)) })
	})
}
func testCredentialAdmission(t *testing.T, r auth.Repositories, mode string) {
	ctx := context.Background()
	ensurePasswordOwner(t, r, "fenced-user")
	if mode != "initial_set" {
		if err := r.Passwords.Set(ctx, "fenced-user", "old"); err != nil {
			t.Fatal(err)
		}
	}
	var resetProof []byte
	if mode == "reset" {
		resetProof = prepareResetProof(t, r, "fenced-user")
	}
	before, err := r.Users.Get(ctx, "fenced-user")
	if err != nil {
		t.Fatal(err)
	}
	existing := newSession(before.ID, "existing-refresh", time.Hour, time.Now())
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, existing, before.AuthRevision); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "oauth_delete":
		account, _ := oauthaccount.New(before.ID, "google", "removed-identity", time.Now())
		if _, err := r.OAuthAccounts.Create(ctx, account); err != nil {
			t.Fatal(err)
		}
		err = r.OAuthAccounts.Delete(ctx, before.ID, account.Provider)
	case "trusted_set":
		err = r.Passwords.Set(ctx, before.ID, "new")
	case "reset":
		if _, e := r.Challenges.Replace(ctx, newChallenge(before.ID, challenge.PurposePasswordReset, "", "reset-proof", resetProof, 0, time.Hour, time.Now())); e != nil {
			t.Fatal(e)
		}
		_, err = r.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{Purpose: challenge.PurposePasswordReset, TokenDigest: "reset-proof", NewPasswordHash: "new", Now: time.Now()})
	default:
		expected := "old"
		if mode == "initial_set" {
			expected = ""
		}
		_, err = r.Passwords.Change(ctx, before.ID, user.PasswordChange{ExpectedAuthRevision: before.AuthRevision, ExpectedHash: expected, NewHash: "new", Now: time.Now()})
	}
	if err != nil {
		t.Fatal(err)
	}
	after, err := r.Users.Get(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AuthRevision != before.AuthRevision+1 {
		t.Fatalf("revision %d -> %d", before.AuthRevision, after.AuthRevision)
	}
	if _, err := r.Sessions.Get(ctx, existing.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("old session survived: %v", err)
	}
	proposed := newSession(before.ID, "late-proof", time.Hour, time.Now())
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, proposed, before.AuthRevision); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale admission: %v", err)
	}
	if _, err := r.Sessions.Get(ctx, proposed.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("stale session persisted: %v", err)
	}
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, proposed, after.AuthRevision); err != nil {
		t.Fatalf("fresh admission: %v", err)
	}
}
func testConcurrentPasswordChange(t *testing.T, r auth.Repositories, initial bool) {
	ctx := context.Background()
	ensurePasswordOwner(t, r, "concurrent-change")
	expected := ""
	if !initial {
		expected = "old"
		if err := r.Passwords.Set(ctx, "concurrent-change", expected); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := r.Users.Get(ctx, "concurrent-change")
	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = r.Passwords.Change(ctx, u.ID, user.PasswordChange{ExpectedAuthRevision: u.AuthRevision, ExpectedHash: expected, NewHash: []string{"new-a", "new-b"}[i], Now: time.Now()})
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, sdk.ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners=%d errs=%v", wins, errs)
	}
}
func testProvisionCollision(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	now := time.Now()
	ensurePasswordOwner(t, r, "provider-owner")
	account, _ := oauthaccount.New("provider-owner", "google", "occupied-provider-id", now)
	if _, err := r.OAuthAccounts.Create(ctx, account); err != nil {
		t.Fatal(err)
	}
	u := user.NewUser(ids, "must rollback", now)
	ident, err := identifier.NewRegistrationEmail(ids, idNorm, u.ID, "provision-collision@example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Users.Provision(ctx, u, ident, user.InitialCredentials{PasswordHash: "hash", OAuth: &account}); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("collision: %v", err)
	}
	if _, err := r.Users.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("orphan user: %v", err)
	}
	if _, err := r.Passwords.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("orphan password: %v", err)
	}
	if _, err := r.Identifiers.GetLogin(ctx, "email", ident.NormalizedValue); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("orphan claim: %v", err)
	}
}
func testAtomicOAuthAdoption(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	now := time.Now()
	ensurePasswordOwner(t, r, "adopt-user")
	if err := r.Passwords.Set(ctx, "adopt-user", "squatter"); err != nil {
		t.Fatal(err)
	}
	u, _ := r.Users.Get(ctx, "adopt-user")
	ident, err := r.Identifiers.GetLogin(ctx, "email", "adopt-user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	old := newSession(u.ID, "squatter-refresh", time.Hour, now)
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, old, u.AuthRevision); err != nil {
		t.Fatal(err)
	}
	account, _ := oauthaccount.New(u.ID, "google", "adopting-provider-id", now)
	if _, revision, err := r.OAuthAccounts.Link(ctx, account, u.AuthRevision, ident.ID, now); err != nil || revision != u.AuthRevision+1 {
		t.Fatalf("adopt revision=%d err=%v", revision, err)
	}
	if _, err := r.Passwords.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("squatter password: %v", err)
	}
	if _, err := r.Sessions.Get(ctx, old.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("squatter session: %v", err)
	}
	verified, err := r.Identifiers.Get(ctx, ident.ID)
	if err != nil || !verified.Verified() {
		t.Fatalf("identifier proof absent: %v", err)
	}
	late := newSession(u.ID, "late-squatter", time.Hour, now)
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, late, u.AuthRevision); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("late proof: %v", err)
	}
}

func testProfileUpdatePreservesCredentialRevision(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	ensurePasswordOwner(t, r, "profile-owner")
	stale, err := r.Users.Get(ctx, "profile-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Passwords.Set(ctx, stale.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	stale.DisplayName = "Updated profile"
	stale.UpdatedAt = time.Now()
	if _, err := r.Users.Update(ctx, stale.ID, stale); err != nil {
		t.Fatal(err)
	}
	current, err := r.Users.Get(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AuthRevision != stale.AuthRevision+1 || current.DisplayName != stale.DisplayName {
		t.Fatalf("profile update changed credential state: %+v", current)
	}
	proposed := newSession(stale.ID, "stale-profile-proof", time.Hour, time.Now())
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, proposed, stale.AuthRevision); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale profile revived credential proof: %v", err)
	}
}

func testOAuthAdoptionContactOnly(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	now := time.Now()
	u := user.NewUser(ids, "Contact only", now)
	ident, err := identifier.New(ids, idNorm, u.ID, identifier.KindEmail, "contact-only@example.com", identifier.Uses{Notification: true}, true, time.Time{}, now)
	if err != nil {
		t.Fatal(err)
	}
	u, ident, err = r.Users.Provision(ctx, u, ident, user.InitialCredentials{PasswordHash: "retained"})
	if err != nil {
		t.Fatal(err)
	}
	account, _ := oauthaccount.New(u.ID, "google", "contact-only-provider", now)
	if _, _, err := r.OAuthAccounts.Link(ctx, account, u.AuthRevision, ident.ID, now); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("contact-only identifier adopted: %v", err)
	}
	if hash, err := r.Passwords.Get(ctx, u.ID); err != nil || hash != "retained" {
		t.Fatalf("rejected adoption changed credential: %v", err)
	}
}
