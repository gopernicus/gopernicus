package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// recoveryOnlyUses is a verified contact that recovers the account but does
// not sign in, so a fixture's login methods are exactly its password and
// passkeys.
var recoveryOnlyUses = identifier.Uses{Recovery: true, Notification: true}

// RunPasskeys exercises Repositories.Passkeys and its coverage by the
// credential-mutation rail and erasure (SOC 2 S4b). It is separate from Run so
// a store adopts it with its passkey migration; Run is unchanged. Supply fresh
// disposable repositories for each invocation of newRepos.
func RunPasskeys(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("Passkeys", func(t *testing.T) {
		if r := newRepos(t); r.Passkeys == nil || r.CredentialMutations == nil {
			t.Skip("Passkeys/CredentialMutations not wired — passkey persistence NOT verified")
		}
		for _, tc := range []struct {
			name string
			test func(*testing.T, auth.Repositories)
		}{
			{"CreateRoundTripAndAssignsID", testPasskeyRoundTrip},
			{"CreateRefusals", testPasskeyCreateRefusals},
			{"CreateRefusesInactiveUser", testPasskeyCreateInactive},
			{"CreateLeavesRevision", testPasskeyCreateLeavesRevision},
			{"ListByUserOldestFirst", testPasskeyList},
			{"RenameScopedToOwner", testPasskeyRename},
			{"RecordUseCompareAndSwap", testPasskeyRecordUse},
			{"SnapshotCountsPasskeys", testPasskeySnapshot},
			{"ApplyRemovePasskeyRevokesSessions", testPasskeyApplyRemove},
			{"ApplyRemovePasskeyOfAnotherUser", testPasskeyApplyRemoveForeign},
			{"ApplyRemovePasskeys", testPasskeyApplyRemoveAll},
			{"ConcurrentRemovalsKeepALoginMethod", testPasskeyConcurrentRemovals},
			{"CreateGuardRevisionMoved", testPasskeyGuardRevision},
			{"CreateGuardBootstrapClosed", testPasskeyGuardBootstrap},
			{"ConcurrentCreatesAtTheCap", testPasskeyConcurrentCap},
			{"ConcurrentFirstCreatesOneHandle", testPasskeyConcurrentFirstHandles},
			{"ConcurrentRecordUseOneWins", testPasskeyConcurrentRecordUse},
			{"ConcurrentBootstrapCreatesOneWins", testPasskeyConcurrentBootstrap},
			{"ScopedSnapshotOmitsPasskeys", testPasskeyScopedSnapshot},
			{"SchemaProbeReportsMigrated", testPasskeyProbeMigrated},
		} {
			t.Run(tc.name, func(t *testing.T) { tc.test(t, newRepos(t)) })
		}
		t.Run("EraseDeletesPasskeys", func(t *testing.T) {
			r := newRepos(t)
			if r.UserErasure == nil {
				t.Skip("UserErasure not wired — passkey erasure NOT verified")
			}
			testPasskeyErase(t, r)
		})
		t.Run("EraseLeavesPasskeysWhenUnwired", func(t *testing.T) {
			r := newRepos(t)
			if r.UserErasure == nil {
				t.Skip("UserErasure not wired — passkey erasure NOT verified")
			}
			testPasskeyEraseUnwired(t, r)
		})
	})
}

// RunPasskeySchemaProbe proves a store's boot probe: unmigrated returns a
// passkey repository over a store WITHOUT migration 0025, and its ProbeSchema
// must refuse, naming the migration. The pocket probes only when passkeys are
// wired.
func RunPasskeySchemaProbe(t *testing.T, unmigrated func(*testing.T) passkey.Repository) {
	t.Helper()
	t.Run("PasskeySchemaProbe", func(t *testing.T) {
		prober, ok := unmigrated(t).(passkey.SchemaProber)
		if !ok {
			t.Fatal("the passkey repository does not implement passkey.SchemaProber")
		}
		err := prober.ProbeSchema(context.Background())
		if err == nil || !strings.Contains(err.Error(), "0025") {
			t.Fatalf("ProbeSchema on an unmigrated store = %v, want an error naming 0025", err)
		}
	})
}

// RunPasskeysUnwired proves an unwired host never needs migration 0025:
// unmigrated returns repositories over a store WITHOUT passkey_credentials,
// and the credential rail and erasure — with passkeys off, as the pocket asks
// for them then — must still work: a scoped Snapshot, an Apply of a
// non-passkey mutation, and an Erase with EraseInput.Passkeys false.
func RunPasskeysUnwired(t *testing.T, unmigrated func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("PasskeysUnwired", func(t *testing.T) {
		r := unmigrated(t)
		scoped, ok := r.CredentialMutations.(credential.ScopedSnapshotter)
		if !ok {
			t.Fatal("the credential-mutation repository does not implement credential.ScopedSnapshotter")
		}
		ctx := context.Background()
		u := seedPasskeyUser(t, r, "passkeys-unwired@example.com")
		if err := r.Passwords.Set(ctx, u.ID, "hash-unwired"); err != nil {
			t.Fatal(err)
		}
		set, err := scoped.SnapshotScoped(ctx, u.ID, credential.SnapshotScope{Passkeys: false})
		if err != nil {
			t.Fatalf("SnapshotScoped without passkeys over a store without 0025 = %v", err)
		}
		if !set.HasPassword || len(set.Identifiers) != 1 || len(set.Passkeys) != 0 {
			t.Fatalf("SnapshotScoped = %+v, want the password, one identifier and no passkeys", set)
		}
		if _, err := scoped.SnapshotScoped(ctx, "no-such-user", credential.SnapshotScope{}); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("SnapshotScoped of an unknown user = %v, want ErrNotFound", err)
		}
		if err := r.CredentialMutations.Apply(ctx, u.ID, set.AuthRevision, credential.RemovePassword{}); err != nil {
			t.Fatalf("Apply RemovePassword over a store without 0025 = %v", err)
		}
		if r.UserErasure == nil {
			t.Skip("UserErasure not wired — unwired-passkey erasure NOT verified")
		}
		got := mustErase(t, r, eraseInput(u.ID, retentionNow()))
		if got.Counts.Passkeys != 0 {
			t.Errorf("an erase with passkeys off counted %d passkeys", got.Counts.Passkeys)
		}
	})
}

// testPasskeyScopedSnapshot: a Snapshot scoped without passkeys omits them and
// reports the same revision as the full one.
func testPasskeyScopedSnapshot(t *testing.T, r auth.Repositories) {
	scoped, ok := r.CredentialMutations.(credential.ScopedSnapshotter)
	if !ok {
		t.Skip("the credential-mutation repository does not scope its Snapshot")
	}
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "scoped-snapshot@example.com")
	mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-scoped"), "scoped", retentionNow()))
	full, err := r.CredentialMutations.Snapshot(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	with, err := scoped.SnapshotScoped(ctx, u.ID, credential.SnapshotScope{Passkeys: true})
	if err != nil || len(with.Passkeys) != 1 {
		t.Fatalf("SnapshotScoped with passkeys = %+v, %v; want one passkey", with, err)
	}
	without, err := scoped.SnapshotScoped(ctx, u.ID, credential.SnapshotScope{Passkeys: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(without.Passkeys) != 0 || without.AuthRevision != full.AuthRevision || len(without.Identifiers) != len(full.Identifiers) {
		t.Fatalf("SnapshotScoped without passkeys = %+v, want %+v minus its passkeys", without, full)
	}
}

func testPasskeyProbeMigrated(t *testing.T, r auth.Repositories) {
	prober, ok := r.Passkeys.(passkey.SchemaProber)
	if !ok {
		t.Skip("the passkey repository does not probe its schema")
	}
	if err := prober.ProbeSchema(context.Background()); err != nil {
		t.Fatalf("ProbeSchema on a migrated store = %v", err)
	}
}

// seedPasskeyUser creates an active user whose verified email recovers but does
// not sign in.
func seedPasskeyUser(t *testing.T, r auth.Repositories, email string) user.User {
	t.Helper()
	now := retentionNow()
	u := user.NewUser(dbIDs, "Passkey User", now)
	ident, err := identifier.New(dbIDs, idNorm, "", identifier.KindEmail, email, recoveryOnlyUses, true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := r.Users.CreateWithPrimaryIdentifier(context.Background(), u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier(%q): %v", email, err)
	}
	return created
}

// newPasskey is a passkey of userID with a unique credential id under handle.
func newPasskey(userID string, handle []byte, label string, at time.Time) passkey.Passkey {
	return passkey.Passkey{
		UserID:         userID,
		Name:           "Passkey " + label,
		CredentialID:   []byte("credential-" + label + "-" + ids.MustGenerate()),
		PublicKey:      []byte{0xa5, 0x01, 0x02, 0x03, 0x26},
		AAGUID:         bytes.Repeat([]byte{0x7a}, 16),
		UserHandle:     handle,
		SignCount:      7,
		Transports:     []string{"internal", "hybrid"},
		UserVerified:   true,
		BackupEligible: true,
		BackupState:    true,
		CreatedAt:      at.UTC().Truncate(time.Microsecond),
	}
}

// createInput is an AAL2-tier input for p at the owner's current revision.
func createInput(r auth.Repositories, p passkey.Passkey, maxPerUser int) passkey.CreateInput {
	return passkey.CreateInput{Passkey: p, MaxPerUser: maxPerUser, ExpectedAuthRevision: currentAuthRevision(context.Background(), r.Users, p.UserID)}
}

func mustCreatePasskey(t *testing.T, r auth.Repositories, p passkey.Passkey) passkey.Passkey {
	t.Helper()
	created, err := r.Passkeys.Create(context.Background(), createInput(r, p, 10))
	if err != nil {
		t.Fatalf("Passkeys.Create(%s): %v", p.Name, err)
	}
	return created
}

func testPasskeyRoundTrip(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "roundtrip@example.com")
	p := newPasskey(u.ID, []byte("handle-roundtrip"), "one", retentionNow())
	p.SignCount = math.MaxUint32
	created := mustCreatePasskey(t, r, p)
	if created.ID == "" {
		t.Fatal("Create assigned no ID")
	}
	got, err := r.Passkeys.GetByCredentialID(ctx, p.CredentialID)
	if err != nil {
		t.Fatalf("GetByCredentialID: %v", err)
	}
	want := p
	want.ID = created.ID
	if got.LastUsedAt != nil || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("read back LastUsedAt %v CreatedAt %v, want nil and %v", got.LastUsedAt, got.CreatedAt, want.CreatedAt)
	}
	got.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, want)
	}
	if _, err := r.Passkeys.GetByCredentialID(ctx, []byte("no-such-credential")); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("GetByCredentialID(unknown) = %v, want ErrNotFound", err)
	}
}

func testPasskeyCreateRefusals(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	now := retentionNow()
	u := seedPasskeyUser(t, r, "refusals@example.com")
	other := seedPasskeyUser(t, r, "refusals-other@example.com")
	handle := []byte("handle-refusals")
	first := mustCreatePasskey(t, r, newPasskey(u.ID, handle, "first", now))

	if _, err := r.Passkeys.Create(ctx, createInput(r, newPasskey("no-such-user", handle, "ghost", now), 10)); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Create for an unknown user = %v, want ErrNotFound", err)
	}
	dup := newPasskey(other.ID, []byte("handle-other"), "dup", now)
	dup.CredentialID = first.CredentialID
	if _, err := r.Passkeys.Create(ctx, createInput(r, dup, 10)); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("Create of a stored credential id = %v, want ErrAlreadyExists", err)
	}
	if _, err := r.Passkeys.Create(ctx, createInput(r, newPasskey(u.ID, []byte("handle-differs"), "foreign-handle", now), 10)); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("Create with a differing user handle = %v, want ErrConflict", err)
	}
	if _, err := r.Passkeys.Create(ctx, createInput(r, newPasskey(u.ID, handle, "second", now.Add(time.Second)), 2)); err != nil {
		t.Fatalf("Create under the cap: %v", err)
	}
	if _, err := r.Passkeys.Create(ctx, createInput(r, newPasskey(u.ID, handle, "third", now.Add(2*time.Second)), 2)); !errors.Is(err, passkey.ErrPasskeyLimit) {
		t.Errorf("Create at the cap = %v, want ErrPasskeyLimit", err)
	}
	list, err := r.Passkeys.ListByUser(ctx, u.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListByUser after refusals = %d, %v; want the 2 created", len(list), err)
	}
}

func testPasskeyCreateInactive(t *testing.T, r auth.Repositories) {
	if r.UserAdmin == nil {
		t.Skip("UserAdmin not wired — Create on an inactive user NOT verified")
	}
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "inactive@example.com")
	if _, err := r.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, retentionNow()); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if _, err := r.Passkeys.Create(ctx, createInput(r, newPasskey(u.ID, []byte("handle-inactive"), "inactive", retentionNow()), 10)); !errors.Is(err, session.ErrUserNotActive) {
		t.Fatalf("Create for a deactivated user = %v, want session.ErrUserNotActive", err)
	}
}

func testPasskeyCreateLeavesRevision(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "revision@example.com")
	s := createSession(t, r, u.ID, "passkey-create")
	mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-revision"), "one", retentionNow()))
	if rev := currentAuthRevision(ctx, r.Users, u.ID); rev != u.AuthRevision {
		t.Errorf("auth_revision after Create = %d, want %d (enrollment bumps nothing)", rev, u.AuthRevision)
	}
	if _, err := r.Sessions.Get(ctx, s.ID); err != nil {
		t.Errorf("session after Create: %v, want it live", err)
	}
}

func testPasskeyList(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	now := retentionNow()
	u := seedPasskeyUser(t, r, "list@example.com")
	other := seedPasskeyUser(t, r, "list-other@example.com")
	if list, err := r.Passkeys.ListByUser(ctx, u.ID); err != nil || len(list) != 0 {
		t.Fatalf("ListByUser(none) = %v, %v; want empty", list, err)
	}
	handle := []byte("handle-list")
	newer := mustCreatePasskey(t, r, newPasskey(u.ID, handle, "newer", now.Add(time.Minute)))
	older := mustCreatePasskey(t, r, newPasskey(u.ID, handle, "older", now))
	mustCreatePasskey(t, r, newPasskey(other.ID, []byte("handle-list-other"), "other", now))
	list, err := r.Passkeys.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != older.ID || list[1].ID != newer.ID {
		t.Fatalf("ListByUser = %+v, want older then newer, the other user's excluded", list)
	}
}

func testPasskeyRename(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "rename@example.com")
	other := seedPasskeyUser(t, r, "rename-other@example.com")
	p := mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-rename"), "one", retentionNow()))
	if err := r.Passkeys.Rename(ctx, p.ID, u.ID, "Work laptop"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := r.Passkeys.Rename(ctx, p.ID, other.ID, "Stolen"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Rename by another user = %v, want ErrNotFound", err)
	}
	if err := r.Passkeys.Rename(ctx, "no-such-passkey", u.ID, "Ghost"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Rename(unknown) = %v, want ErrNotFound", err)
	}
	got, err := r.Passkeys.GetByCredentialID(ctx, p.CredentialID)
	if err != nil || got.Name != "Work laptop" {
		t.Fatalf("name after renames = %q, %v; want the owner's", got.Name, err)
	}
}

func testPasskeyRecordUse(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "recorduse@example.com")
	p := mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-recorduse"), "one", retentionNow()))
	at := retentionNow().Add(time.Minute)
	if err := r.Passkeys.RecordUse(ctx, p.ID, 7, 8, false, at); err != nil {
		t.Fatalf("RecordUse: %v", err)
	}
	got, err := r.Passkeys.GetByCredentialID(ctx, p.CredentialID)
	if err != nil || got.SignCount != 8 || got.BackupState || got.LastUsedAt == nil || !got.LastUsedAt.Equal(at) {
		t.Fatalf("after RecordUse = %+v, %v; want count 8, backup state false, last used %v", got, err, at)
	}
	if err := r.Passkeys.RecordUse(ctx, p.ID, 7, 9, true, at.Add(time.Minute)); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("RecordUse at a stale count = %v, want ErrConflict", err)
	}
	if got, _ := r.Passkeys.GetByCredentialID(ctx, p.CredentialID); got.SignCount != 8 || got.BackupState {
		t.Errorf("a refused RecordUse changed the row: %+v", got)
	}
	if err := r.Passkeys.RecordUse(ctx, "no-such-passkey", 0, 1, false, at); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("RecordUse(missing row) = %v, want ErrConflict", err)
	}
}

func testPasskeySnapshot(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "snapshot@example.com")
	verified := newPasskey(u.ID, []byte("handle-snapshot"), "verified", retentionNow())
	presence := newPasskey(u.ID, []byte("handle-snapshot"), "presence", retentionNow().Add(time.Second))
	presence.UserVerified = false
	a, b := mustCreatePasskey(t, r, verified), mustCreatePasskey(t, r, presence)
	set, err := r.CredentialMutations.Snapshot(ctx, u.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got := map[string]bool{}
	for _, p := range set.Passkeys {
		got[p.ID] = p.UserVerified
	}
	if len(set.Passkeys) != 2 || got[a.ID] != true || got[b.ID] != false {
		t.Fatalf("Snapshot Passkeys = %+v, want %s verified and %s presence-only", set.Passkeys, a.ID, b.ID)
	}
	if len(set.LoginMethods()) != 2 {
		t.Errorf("LoginMethods = %+v, want the two passkeys", set.LoginMethods())
	}
}

func testPasskeyApplyRemove(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "remove@example.com")
	handle := []byte("handle-remove")
	gone := mustCreatePasskey(t, r, newPasskey(u.ID, handle, "gone", retentionNow()))
	kept := mustCreatePasskey(t, r, newPasskey(u.ID, handle, "kept", retentionNow().Add(time.Second)))
	s := createSession(t, r, u.ID, "remove")
	if err := r.CredentialMutations.Apply(ctx, u.ID, u.AuthRevision, credential.RemovePasskey{PasskeyID: gone.ID}); err != nil {
		t.Fatalf("Apply(RemovePasskey): %v", err)
	}
	if _, err := r.Passkeys.GetByCredentialID(ctx, gone.CredentialID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("removed passkey read = %v, want ErrNotFound", err)
	}
	if _, err := r.Passkeys.GetByCredentialID(ctx, kept.CredentialID); err != nil {
		t.Errorf("the other passkey: %v, want it kept", err)
	}
	if rev := currentAuthRevision(ctx, r.Users, u.ID); rev != u.AuthRevision+1 {
		t.Errorf("auth_revision after RemovePasskey = %d, want %d", rev, u.AuthRevision+1)
	}
	if _, err := r.Sessions.Get(ctx, s.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("session after RemovePasskey = %v, want revoked (ErrNotFound)", err)
	}
	if err := r.CredentialMutations.Apply(ctx, u.ID, u.AuthRevision, credential.RemovePasskey{PasskeyID: kept.ID}); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("Apply at the stale revision = %v, want ErrConflict", err)
	}
}

func testPasskeyApplyRemoveForeign(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "foreign@example.com")
	other := seedPasskeyUser(t, r, "foreign-other@example.com")
	theirs := mustCreatePasskey(t, r, newPasskey(other.ID, []byte("handle-foreign"), "theirs", retentionNow()))
	for name, id := range map[string]string{"another user's": theirs.ID, "an unknown": "no-such-passkey"} {
		if err := r.CredentialMutations.Apply(ctx, u.ID, u.AuthRevision, credential.RemovePasskey{PasskeyID: id}); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("Apply(RemovePasskey of %s passkey) = %v, want ErrNotFound", name, err)
		}
	}
	if rev := currentAuthRevision(ctx, r.Users, u.ID); rev != u.AuthRevision {
		t.Errorf("auth_revision after refused removals = %d, want %d (nothing applied)", rev, u.AuthRevision)
	}
	if _, err := r.Passkeys.GetByCredentialID(ctx, theirs.CredentialID); err != nil {
		t.Errorf("the other user's passkey: %v, want it kept", err)
	}
}

func testPasskeyApplyRemoveAll(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "removeall@example.com")
	other := seedPasskeyUser(t, r, "removeall-other@example.com")
	handle := []byte("handle-removeall")
	mustCreatePasskey(t, r, newPasskey(u.ID, handle, "a", retentionNow()))
	mustCreatePasskey(t, r, newPasskey(u.ID, handle, "b", retentionNow().Add(time.Second)))
	bystander := mustCreatePasskey(t, r, newPasskey(other.ID, []byte("handle-bystander"), "bystander", retentionNow()))
	if err := r.CredentialMutations.Apply(ctx, u.ID, u.AuthRevision, credential.RemovePasskeys{}); err != nil {
		t.Fatalf("Apply(RemovePasskeys): %v", err)
	}
	if list, err := r.Passkeys.ListByUser(ctx, u.ID); err != nil || len(list) != 0 {
		t.Fatalf("passkeys after RemovePasskeys = %d, %v; want none", len(list), err)
	}
	if _, err := r.Passkeys.GetByCredentialID(ctx, bystander.CredentialID); err != nil {
		t.Errorf("bystander passkey: %v, want it kept", err)
	}
	if err := r.CredentialMutations.Apply(ctx, u.ID, u.AuthRevision+1, credential.RemovePasskeys{}); err != nil {
		t.Errorf("Apply(RemovePasskeys) of none = %v, want nil", err)
	}
}

// testPasskeyConcurrentRemovals races a password removal against a passkey
// removal through the Snapshot → policy → Apply loop a service runs, on a user
// whose only login methods they are: exactly one commits.
func testPasskeyConcurrentRemovals(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "race@example.com")
	if err := r.Passwords.Set(ctx, u.ID, "hash:race"); err != nil {
		t.Fatal(err)
	}
	p := mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-race"), "only", retentionNow()))
	u.AuthRevision = currentAuthRevision(ctx, r.Users, u.ID)
	policy := credential.NewDefaultPolicy(credential.PolicyConfig{})
	mutations := []credential.Mutation{credential.RemovePassword{}, credential.RemovePasskey{PasskeyID: p.ID}}
	committed := make([]bool, len(mutations))
	errs := make([]error, len(mutations))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, m := range mutations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			committed[i], errs[i] = applyUnderPolicy(ctx, r.CredentialMutations, policy, u.ID, m)
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected Apply error: %v", err)
		}
	}
	if committed[0] == committed[1] {
		t.Fatalf("committed = %v, want exactly one removal", committed)
	}
	set, err := r.CredentialMutations.Snapshot(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(set.LoginMethods()); n != 1 {
		t.Fatalf("login methods after the race = %d (%+v), want 1", n, set)
	}
}

// applyUnderPolicy is the bounded Snapshot → policy → Apply retry a service runs:
// a policy refusal is a clean loss, a revision conflict reloads.
func applyUnderPolicy(ctx context.Context, repo credential.MutationRepository, policy credential.Policy, userID string, m credential.Mutation) (bool, error) {
	for range 16 {
		current, err := repo.Snapshot(ctx, userID)
		if err != nil {
			return false, err
		}
		if policy.EvaluateMutation(ctx, current, current.With(m)) != nil {
			return false, nil
		}
		err = repo.Apply(ctx, userID, current.AuthRevision, m)
		switch {
		case err == nil:
			return true, nil
		case !errors.Is(err, sdk.ErrConflict):
			return false, err
		}
	}
	return false, fmt.Errorf("storetest: %T never settled", m)
}

func testPasskeyErase(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "erase-passkeys@example.com")
	other := seedPasskeyUser(t, r, "erase-passkeys-other@example.com")
	handle := []byte("handle-erase")
	a := mustCreatePasskey(t, r, newPasskey(u.ID, handle, "a", retentionNow()))
	b := mustCreatePasskey(t, r, newPasskey(u.ID, handle, "b", retentionNow().Add(time.Second)))
	bystander := mustCreatePasskey(t, r, newPasskey(other.ID, []byte("handle-erase-other"), "bystander", retentionNow()))
	in := eraseInput(u.ID, retentionNow())
	in.Passkeys = true
	got := mustErase(t, r, in)
	if got.Counts.Passkeys != 2 {
		t.Errorf("EraseCounts.Passkeys = %d, want 2", got.Counts.Passkeys)
	}
	for _, p := range []passkey.Passkey{a, b} {
		if _, err := r.Passkeys.GetByCredentialID(ctx, p.CredentialID); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("erased user's passkey %s read = %v, want ErrNotFound", p.ID, err)
		}
	}
	if list, err := r.Passkeys.ListByUser(ctx, u.ID); err != nil || len(list) != 0 {
		t.Errorf("erased user's passkeys = %d, %v; want none", len(list), err)
	}
	if _, err := r.Passkeys.GetByCredentialID(ctx, bystander.CredentialID); err != nil {
		t.Errorf("bystander passkey after erase: %v, want it kept", err)
	}
}

// testPasskeyGuardRevision pins the revision fence: a ceremony begun at a
// revision that has since moved is refused with sdk.ErrConflict, nothing stored.
func testPasskeyGuardRevision(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "guard-revision@example.com")
	if err := r.Passwords.Set(ctx, u.ID, "hash:guard"); err != nil {
		t.Fatal(err)
	}
	begun := currentAuthRevision(ctx, r.Users, u.ID)
	if err := r.CredentialMutations.Apply(ctx, u.ID, begun, credential.RemovePassword{}); err != nil {
		t.Fatalf("Apply(RemovePassword): %v", err)
	}
	stale := passkey.CreateInput{Passkey: newPasskey(u.ID, []byte("handle-guard-revision"), "stale", retentionNow()), MaxPerUser: 10, ExpectedAuthRevision: begun}
	if _, err := r.Passkeys.Create(ctx, stale); !errors.Is(err, sdk.ErrConflict) || errors.Is(err, passkey.ErrPasskeyLimit) {
		t.Fatalf("Create at a moved revision = %v, want sdk.ErrConflict", err)
	}
	if list, _ := r.Passkeys.ListByUser(ctx, u.ID); len(list) != 0 {
		t.Fatalf("a refused Create stored %d passkeys", len(list))
	}
	stale.ExpectedAuthRevision = currentAuthRevision(ctx, r.Users, u.ID)
	if _, err := r.Passkeys.Create(ctx, stale); err != nil {
		t.Fatalf("Create at the current revision: %v", err)
	}
}

// testPasskeyGuardBootstrap pins the bootstrap guard: a bootstrap-tier input is
// refused once the owner holds an AAL2-capable passkey, admitted beside
// presence-only ones, and an AAL2-tier input is unaffected.
func testPasskeyGuardBootstrap(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "guard-bootstrap@example.com")
	handle := []byte("handle-guard-bootstrap")
	presence := newPasskey(u.ID, handle, "presence", retentionNow())
	presence.UserVerified = false
	mustCreatePasskey(t, r, presence)
	in := createInput(r, newPasskey(u.ID, handle, "verified", retentionNow().Add(time.Second)), 10)
	in.Bootstrap = true
	if _, err := r.Passkeys.Create(ctx, in); err != nil {
		t.Fatalf("bootstrap beside a presence-only passkey: %v", err)
	}
	in = createInput(r, newPasskey(u.ID, handle, "late", retentionNow().Add(2*time.Second)), 10)
	in.Bootstrap = true
	if _, err := r.Passkeys.Create(ctx, in); !errors.Is(err, passkey.ErrBootstrapClosed) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("bootstrap beside an AAL2-capable passkey = %v, want ErrBootstrapClosed", err)
	}
	in.Bootstrap = false
	if _, err := r.Passkeys.Create(ctx, in); err != nil {
		t.Fatalf("an AAL2-tier Create beside an AAL2-capable passkey: %v", err)
	}
}

// race runs fn n times at once and returns each result.
func race(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// oneWins asserts exactly one nil error and every other matching want.
func oneWins(t *testing.T, errs []error, want error) {
	t.Helper()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, want):
			t.Fatalf("unexpected error %v (want %v)", err, want)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1 (%v)", wins, errs)
	}
}

// testPasskeyConcurrentCap: N Creates for an owner one short of the cap commit
// exactly one.
func testPasskeyConcurrentCap(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	const limit, racers = 3, 6
	u := seedPasskeyUser(t, r, "race-cap@example.com")
	handle := []byte("handle-race-cap")
	for i := range limit - 1 {
		mustCreatePasskey(t, r, newPasskey(u.ID, handle, fmt.Sprintf("seed-%d", i), retentionNow().Add(time.Duration(i)*time.Second)))
	}
	revision := currentAuthRevision(ctx, r.Users, u.ID)
	oneWins(t, race(racers, func(i int) error {
		_, err := r.Passkeys.Create(ctx, passkey.CreateInput{Passkey: newPasskey(u.ID, handle, fmt.Sprintf("racer-%d", i), retentionNow()), MaxPerUser: limit, ExpectedAuthRevision: revision})
		return err
	}), passkey.ErrPasskeyLimit)
	if list, _ := r.Passkeys.ListByUser(ctx, u.ID); len(list) != limit {
		t.Fatalf("passkeys after the race = %d, want %d", len(list), limit)
	}
}

// testPasskeyConcurrentFirstHandles: two first Creates minting different user
// handles commit exactly one; the other is sdk.ErrConflict.
func testPasskeyConcurrentFirstHandles(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "race-handles@example.com")
	revision := currentAuthRevision(ctx, r.Users, u.ID)
	oneWins(t, race(2, func(i int) error {
		_, err := r.Passkeys.Create(ctx, passkey.CreateInput{Passkey: newPasskey(u.ID, []byte(fmt.Sprintf("handle-race-%d", i)), fmt.Sprintf("first-%d", i), retentionNow()), MaxPerUser: 10, ExpectedAuthRevision: revision})
		return err
	}), sdk.ErrConflict)
	list, _ := r.Passkeys.ListByUser(ctx, u.ID)
	if len(list) != 1 {
		t.Fatalf("passkeys after the race = %d, want 1", len(list))
	}
}

// testPasskeyConcurrentRecordUse: two uses at one expected count — exactly one
// records.
func testPasskeyConcurrentRecordUse(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "race-use@example.com")
	p := mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-race-use"), "one", retentionNow()))
	oneWins(t, race(2, func(i int) error {
		return r.Passkeys.RecordUse(ctx, p.ID, p.SignCount, p.SignCount+uint32(i)+1, false, retentionNow())
	}), sdk.ErrConflict)
	got, err := r.Passkeys.GetByCredentialID(ctx, p.CredentialID)
	if err != nil || (got.SignCount != p.SignCount+1 && got.SignCount != p.SignCount+2) {
		t.Fatalf("count after the race = %d, %v", got.SignCount, err)
	}
}

// testPasskeyConcurrentBootstrap: two bootstrap-tier Creates of AAL2-capable
// passkeys for an owner holding none commit exactly one; the other is
// ErrBootstrapClosed.
func testPasskeyConcurrentBootstrap(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "race-bootstrap@example.com")
	handle := []byte("handle-race-bootstrap")
	revision := currentAuthRevision(ctx, r.Users, u.ID)
	oneWins(t, race(2, func(i int) error {
		_, err := r.Passkeys.Create(ctx, passkey.CreateInput{Passkey: newPasskey(u.ID, handle, fmt.Sprintf("bootstrap-%d", i), retentionNow()), MaxPerUser: 10, ExpectedAuthRevision: revision, Bootstrap: true})
		return err
	}), passkey.ErrBootstrapClosed)
}

// testPasskeyEraseUnwired: with passkeys off erasure leaves passkey_credentials
// untouched and counts none (owner ruling: 0025 is required only when passkeys
// are wired).
func testPasskeyEraseUnwired(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "erase-passkeys-off@example.com")
	p := mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-erase-off"), "off", retentionNow()))
	got := mustErase(t, r, eraseInput(u.ID, retentionNow()))
	if got.Counts.Passkeys != 0 {
		t.Errorf("an erase with passkeys off counted %d passkeys", got.Counts.Passkeys)
	}
	if _, err := r.Passkeys.GetByCredentialID(ctx, p.CredentialID); err != nil {
		t.Errorf("an erase with passkeys off touched the passkey: %v", err)
	}
}
