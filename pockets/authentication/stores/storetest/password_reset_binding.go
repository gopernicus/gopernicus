package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/sdk"
)

// prepareResetProof upgrades the registration fixture to a verified recovery
// address, then captures the revision. Call it before seeding live sessions.
func prepareResetProof(t *testing.T, repos auth.Repositories, userID string) json.RawMessage {
	t.Helper()
	ctx := context.Background()
	u, err := repos.Users.Get(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := repos.Identifiers.GetRecovery(ctx, "email", userID+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !ident.Verified() {
		ident, err = repos.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
			UserID: userID, Kind: identifier.KindEmail, NormalizedValue: ident.NormalizedValue,
			LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true, MakePrimary: true,
			ReplacesIdentifierID: ident.ID,
		}, u.AuthRevision, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		u, err = repos.Users.Get(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(passwordreset.Binding{Version: passwordreset.BindingVersion, AuthRevision: u.AuthRevision, IdentifierID: ident.ID})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testPasswordResetStaleBinding(t *testing.T, repos auth.Repositories, mode string) {
	ctx := context.Background()
	const userID = "reset-binding"
	ensurePasswordOwner(t, repos, userID)
	if err := repos.Passwords.Set(ctx, userID, "retained-hash"); err != nil {
		t.Fatal(err)
	}
	raw := prepareResetProof(t, repos, userID)
	binding, _ := passwordreset.ParseBinding(raw)
	switch mode {
	case "legacy":
		raw = nil
	case "unknown_version":
		binding.Version++
		raw, _ = json.Marshal(binding)
	case "replaced_recovery", "later_revision":
		in := identifier.ApplyVerifiedChangeInput{UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "new-reset@example.com", NotificationEnabled: true}
		if mode == "replaced_recovery" {
			in.ReplacesIdentifierID, in.MakePrimary, in.LoginEnabled, in.RecoveryEnabled = binding.IdentifierID, true, true, true
		}
		if _, err := repos.Identifiers.ApplyVerifiedChange(ctx, in, binding.AuthRevision, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := repos.Sessions.Create(ctx, newSession(userID, "retained-refresh", time.Hour, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	// A delayed initializer may commit its old proof after the mutation. The
	// reset must reject it independently of challenge purging by other writers.
	if _, err := repos.Challenges.Replace(ctx, newChallenge(userID, passwordResetPurpose, "", "stale-reset", raw, 0, time.Hour, time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{Purpose: passwordResetPurpose, TokenDigest: "stale-reset", NewPasswordHash: "must-not-write", Now: time.Now()}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("stale binding redeemed: %v", err)
	}
	if hash, err := repos.Passwords.Get(ctx, userID); err != nil || hash != "retained-hash" {
		t.Fatalf("rejected reset changed password: %q, %v", hash, err)
	}
	if _, err := repos.Sessions.Get(ctx, sess.ID); err != nil {
		t.Fatalf("rejected reset revoked session: %v", err)
	}
	if _, err := repos.Challenges.ConsumeToken(ctx, passwordResetPurpose, "stale-reset", time.Now()); err != nil {
		t.Fatalf("rejected reset consumed token: %v", err)
	}
}

func testPasswordResetRecoveryRace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const userID = "reset-recovery-race"
	ensurePasswordOwner(t, repos, userID)
	if err := repos.Passwords.Set(ctx, userID, "old"); err != nil {
		t.Fatal(err)
	}
	raw := prepareResetProof(t, repos, userID)
	binding, _ := passwordreset.ParseBinding(raw)
	if _, err := repos.Challenges.Replace(ctx, newChallenge(userID, passwordResetPurpose, "", "racing-reset", raw, 0, time.Hour, time.Now())); err != nil {
		t.Fatal(err)
	}
	var resetErr, changeErr error
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, resetErr = repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{Purpose: passwordResetPurpose, TokenDigest: "racing-reset", NewPasswordHash: "reset", Now: time.Now()})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, changeErr = repos.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
			UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "race-winner@example.com",
			LoginEnabled: true, RecoveryEnabled: true, MakePrimary: true, ReplacesIdentifierID: binding.IdentifierID,
		}, binding.AuthRevision, time.Now())
	}()
	close(start)
	wg.Wait()
	if resetErr == nil {
		if !errors.Is(changeErr, sdk.ErrConflict) {
			t.Fatalf("reset won but recovery change rebased: %v", changeErr)
		}
	} else if changeErr != nil || !errors.Is(resetErr, sdk.ErrNotFound) {
		t.Fatalf("recovery/reset race: change=%v, reset=%v", changeErr, resetErr)
	}
	u, _ := repos.Users.Get(ctx, userID)
	if u.AuthRevision != binding.AuthRevision+1 {
		t.Fatalf("race committed multiple revisions: got %d, started %d", u.AuthRevision, binding.AuthRevision)
	}
}
