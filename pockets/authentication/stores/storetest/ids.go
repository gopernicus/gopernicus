package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
)

// countingPrefix prefixes every id the RunIDs counting generator mints.
const countingPrefix = "fixed-"

// RunIDs proves the record ids the core mints reach the store verbatim:
// challenges, contact changes, passkeys, recovery codes and the identifier a
// confirmed contact change creates. Each case runs twice — an app-minted id
// (a counting generator: fixed-1, fixed-2, …) must persist and read back
// byte-for-byte, and a supplied id that collides with a stored row is
// sdk.ErrAlreadyExists; the sdk.DatabaseID leg (an empty id) must come back
// with the store-assigned id. A store that mints its own id over a supplied
// one fails here. It runs outside Run so a host store adopts it explicitly;
// the passkey and recovery-code cases skip LOUDLY when their ports are unwired.
func RunIDs(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("IDs", func(t *testing.T) {
		for _, leg := range []struct {
			name   string
			minted bool
		}{{"AppMintedID", true}, {"DatabaseID", false}} {
			t.Run(leg.name, func(t *testing.T) {
				t.Run("ChallengeIDVerbatim", func(t *testing.T) { testIDsChallenge(t, newRepos(t), leg.minted) })
				t.Run("ContactChangeIDVerbatim", func(t *testing.T) { testIDsContactChange(t, newRepos(t), leg.minted) })
				t.Run("ConfirmedChangeIdentifierID", func(t *testing.T) { testIDsConfirmedChange(t, newRepos(t), leg.minted) })
				t.Run("PasskeyIDVerbatim", func(t *testing.T) {
					r := newRepos(t)
					if r.Passkeys == nil {
						t.Skip("Passkeys not wired — passkey id persistence NOT verified")
					}
					testIDsPasskey(t, r, leg.minted)
				})
				t.Run("RecoveryCodeIDVerbatim", func(t *testing.T) {
					r := newRepos(t)
					if r.TOTP == nil {
						t.Skip("TOTP not wired — recovery-code id persistence NOT verified")
					}
					testIDsRecoveryCode(t, r, leg.minted)
				})
			})
		}
	})
}

// countingIDs returns a generator minting fixed-1, fixed-2, …. A constant
// generator would collide on the second insert and prove nothing.
func countingIDs() sdk.IDGenerator {
	var n atomic.Int64
	return sdk.NewIDGenerator(func() (string, error) {
		return fmt.Sprintf("%s%d", countingPrefix, n.Add(1)), nil
	})
}

// legIDs is the leg's generator: counting when minted, sdk.DatabaseID otherwise.
func legIDs(minted bool) sdk.IDGenerator {
	if minted {
		return countingIDs()
	}
	return dbIDs
}

// checkLegID asserts got is the supplied id (minted) or a store-assigned one.
func checkLegID(t *testing.T, what, supplied, got string, minted bool) {
	t.Helper()
	switch {
	case minted && got != supplied:
		t.Fatalf("%s id = %q, want the supplied %q verbatim", what, got, supplied)
	case minted && !strings.HasPrefix(got, countingPrefix):
		t.Fatalf("%s id = %q, want the %q prefix", what, got, countingPrefix)
	case !minted && (supplied != "" || got == ""):
		t.Fatalf("%s DatabaseID leg: supplied %q, got %q; want an empty supply and a store-assigned id", what, supplied, got)
	}
}

func testIDsChallenge(t *testing.T, repos auth.Repositories, minted bool) {
	ctx := context.Background()
	gen := legIDs(minted)
	c := newChallenge("u-ids", challenge.PurposeLoginMagicLink, "", "ids-token", []byte("bind"), 0, time.Hour, time.Now())
	c.ID = gen.MustGenerate()
	created, err := repos.Challenges.Replace(ctx, c)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	checkLegID(t, "challenge", c.ID, created.ID, minted)
	if minted {
		other := newChallenge("u-ids-other", challenge.PurposeChangeEmail, "k1", "ids-other", nil, 0, time.Hour, time.Now())
		other.ID = created.ID
		if _, err := repos.Challenges.Replace(ctx, other); !errors.Is(err, sdk.ErrAlreadyExists) {
			t.Fatalf("Replace with a colliding supplied id = %v, want ErrAlreadyExists", err)
		}
	}
	consumed, err := repos.Challenges.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "ids-token", time.Now())
	if err != nil || consumed.ID != created.ID {
		t.Fatalf("ConsumeToken id = %q err %v, want %q", consumed.ID, err, created.ID)
	}
}

func testIDsContactChange(t *testing.T, repos auth.Repositories, minted bool) {
	ctx := context.Background()
	gen := legIDs(minted)
	p := newEmailChange("u-ids", "ids@example.com", loginRecoveryUses, true, "", time.Hour)
	p.ID = gen.MustGenerate()
	created, err := repos.ContactChanges.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	checkLegID(t, "contact change", p.ID, created.ID, minted)
	if minted {
		other := newEmailChange("u-ids-other", "ids-other@example.com", loginRecoveryUses, true, "", time.Hour)
		other.ID = created.ID
		if _, err := repos.ContactChanges.Create(ctx, other); !errors.Is(err, sdk.ErrAlreadyExists) {
			t.Fatalf("Create with a colliding supplied id = %v, want ErrAlreadyExists", err)
		}
	}
	got, err := repos.ContactChanges.Get(ctx, "u-ids", identifier.KindEmail)
	if err != nil || got.ID != created.ID {
		t.Fatalf("Get id = %q err %v, want %q", got.ID, err, created.ID)
	}
}

func testIDsConfirmedChange(t *testing.T, repos auth.Repositories, minted bool) {
	ctx := context.Background()
	gen := legIDs(minted)
	u, first := seedUserWithIdentifier(t, repos, "ids", "ids-first@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	input := identifier.ApplyVerifiedChangeInput{
		ID:     gen.MustGenerate(),
		UserID: u.ID, Kind: identifier.KindEmail, NormalizedValue: "ids-second@example.com",
		LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true, MakePrimary: true,
	}
	if minted {
		collide := input
		collide.ID = first.ID
		if _, err := repos.Identifiers.ApplyVerifiedChange(ctx, collide, currentAuthRevision(ctx, repos.Users, u.ID), suiteBase.Add(time.Hour)); !errors.Is(err, sdk.ErrAlreadyExists) {
			t.Fatalf("ApplyVerifiedChange with a colliding supplied id = %v, want ErrAlreadyExists", err)
		}
	}
	applied, err := repos.Identifiers.ApplyVerifiedChange(ctx, input, currentAuthRevision(ctx, repos.Users, u.ID), suiteBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyVerifiedChange: %v", err)
	}
	checkLegID(t, "confirmed-change identifier", input.ID, applied.ID, minted)
	got, err := repos.Identifiers.Get(ctx, applied.ID)
	if err != nil || got.NormalizedValue != "ids-second@example.com" {
		t.Fatalf("Get(%q) = %+v err %v, want the confirmed identifier", applied.ID, got, err)
	}
}

func testIDsPasskey(t *testing.T, r auth.Repositories, minted bool) {
	ctx := context.Background()
	gen := legIDs(minted)
	u := seedPasskeyUser(t, r, "ids-passkey@example.com")
	p := newPasskey(u.ID, []byte("handle-ids"), "ids", retentionNow())
	p.ID = gen.MustGenerate()
	created := mustCreatePasskey(t, r, p)
	checkLegID(t, "passkey", p.ID, created.ID, minted)
	if minted {
		other := newPasskey(u.ID, []byte("handle-ids"), "ids-other", retentionNow())
		other.ID = created.ID
		if _, err := r.Passkeys.Create(ctx, createInput(r, other, 10)); !errors.Is(err, sdk.ErrAlreadyExists) {
			t.Fatalf("Create with a colliding supplied id = %v, want ErrAlreadyExists", err)
		}
	}
	got, err := r.Passkeys.GetByCredentialID(ctx, p.CredentialID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("GetByCredentialID id = %q err %v, want %q", got.ID, err, created.ID)
	}
}

// The recovery-code port reads no ids back, so the minted leg proves the
// supplied id is the row key by colliding with another user's code; the
// DatabaseID leg proves an empty id is assigned rather than refused.
func testIDsRecoveryCode(t *testing.T, r auth.Repositories, minted bool) {
	ctx := context.Background()
	gen := legIDs(minted)
	withIDs := func(codes []totp.RecoveryCode) []totp.RecoveryCode {
		for i := range codes {
			codes[i].ID = gen.MustGenerate()
		}
		return codes
	}
	owner := seedPasskeyUser(t, r, "ids-totp@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, owner.ID, "aal2")); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	codes := withIDs(recoveryCodes(owner.ID, "k1", 3, retentionNow()))
	if err := r.TOTP.Confirm(ctx, confirmation(r, owner.ID, 100, codes)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, owner.ID); n != 3 {
		t.Fatalf("CountRecoveryCodes = %d, want 3", n)
	}
	if !minted {
		if remaining, err := r.TOTP.ConsumeRecoveryCode(ctx, owner.ID, candidate(codes[0]), retentionNow()); err != nil || remaining != 2 {
			t.Fatalf("ConsumeRecoveryCode = %d, %v; want 2 remaining", remaining, err)
		}
		return
	}
	otherID, _ := mustConfirmTOTP(t, r, "ids-totp-other@example.com", 2)
	clash := recoveryCodes(otherID, "k1", 1, retentionNow().Add(time.Hour))
	clash[0].ID = codes[0].ID
	if err := r.TOTP.ReplaceRecoveryCodes(ctx, otherID, clash, nil); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("ReplaceRecoveryCodes with another user's code id = %v, want ErrAlreadyExists", err)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, otherID); n != 2 {
		t.Fatalf("a refused Replace left %d codes, want 2", n)
	}
}
