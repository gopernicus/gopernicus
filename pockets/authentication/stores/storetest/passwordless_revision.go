package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

func testPasswordlessKnownOwnerRevision(t *testing.T, r auth.Repositories, verified bool, mutation string) {
	ctx := context.Background()
	now := time.Now()
	addr := "bound-magic@example.com"
	var owner user.User
	var ident identifier.Identifier
	if verified {
		owner = seedVerifiedOwner(t, r, addr)
		var err error
		ident, err = r.Identifiers.GetLogin(ctx, "email", addr)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		owner, ident = seedUnverifiedRegistration(t, r, addr)
	}
	if err := r.Passwords.Set(ctx, owner.ID, "original"); err != nil {
		t.Fatal(err)
	}
	recovery := ident
	if mutation == "reset" && !verified {
		current, err := r.Users.Get(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		recovery, err = r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{UserID: owner.ID, Kind: identifier.KindEmail, NormalizedValue: "recovery@example.com", RecoveryEnabled: true}, current.AuthRevision, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := r.Users.Get(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding := passwordless.Binding{Version: passwordless.BindingVersion, Kind: "email", NormalizedValue: addr, UserID: owner.ID, IdentifierID: ident.ID, AuthRevision: &current.AuthRevision, ProvisionIfAbsent: true}
	if mutation == "missing-revision" {
		binding.AuthRevision = nil
	}
	blob, _ := json.Marshal(binding)
	if _, err := r.Challenges.Replace(ctx, challenge.Challenge{UserID: owner.ID, SubjectKey: "bound-magic", Purpose: challenge.PurposeLoginMagicLink, SecretDigest: "bound-proof", Context: blob, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	switch mutation {
	case "change":
		if _, err := r.Passwords.Change(ctx, owner.ID, user.PasswordChange{ExpectedAuthRevision: current.AuthRevision, ExpectedHash: "original", NewHash: "replacement", Now: now}); err != nil {
			t.Fatal(err)
		}
	case "reset":
		resetBlob, _ := json.Marshal(passwordreset.Binding{Version: passwordreset.BindingVersion, AuthRevision: current.AuthRevision, IdentifierID: recovery.ID})
		if _, err := r.Challenges.Replace(ctx, challenge.Challenge{UserID: owner.ID, Purpose: challenge.PurposePasswordReset, SecretDigest: "reset-proof", Context: resetBlob, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{Purpose: challenge.PurposePasswordReset, TokenDigest: "reset-proof", NewPasswordHash: "replacement", Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	input := redeemInput("bound-proof", "bound-session", now)
	if _, err := r.Passwordless.Redeem(ctx, input); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("stale known-owner magic link admitted: %v", err)
	}
	expected := "replacement"
	if mutation == "missing-revision" {
		expected = "original"
	}
	if hash, err := r.Passwords.Get(ctx, owner.ID); err != nil || hash != expected {
		t.Fatalf("rejected adoption changed password: %v", err)
	}
	if _, err := r.Sessions.Get(ctx, input.Session.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("rejected proof persisted session: %v", err)
	}
	if _, err := r.Challenges.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "bound-proof", now); err != nil {
		t.Fatalf("rejected transaction consumed token: %v", err)
	}
}

func testPasswordlessLegacyOwnerlessRejected(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	now := time.Now()
	addr := "legacy-unverified@example.com"
	owner, ident := seedUnverifiedRegistration(t, r, addr)
	if err := r.Passwords.Set(ctx, owner.ID, "must-survive"); err != nil {
		t.Fatal(err)
	}
	// The old initializer emitted this ownerless shape for an existing unverified
	// claimant. It must not gain new address-only authority after the upgrade.
	legacy := provisioningBinding(addr)
	legacy.Version = 1
	seedLinkChallenge(t, r, "legacy-v1-proof", legacy, now.Add(time.Hour))
	input := redeemInput("legacy-v1-proof", "legacy-v1-session", now)
	if _, err := r.Passwordless.Redeem(ctx, input); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("legacy ownerless v1 adopted current user: %v", err)
	}
	if hash, err := r.Passwords.Get(ctx, owner.ID); err != nil || hash != "must-survive" {
		t.Fatalf("legacy proof removed password: %v", err)
	}
	current, err := r.Identifiers.Get(ctx, ident.ID)
	if err != nil || current.Verified() {
		t.Fatalf("legacy proof verified account: %v", err)
	}
}
