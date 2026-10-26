package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
)

// The service-account status conformance cases (SOC 2 S4d). The port is
// OPTIONAL, so the group skips LOUDLY when unwired.

// runServiceAccountStatus registers the serviceaccount.StatusRepository group.
func runServiceAccountStatus(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()
	t.Run("ServiceAccountStatus", func(t *testing.T) {
		repos := newRepos(t)
		if repos.ServiceAccountStatus == nil || repos.ServiceAccounts == nil || repos.APIKeys == nil {
			t.Skip("ServiceAccountStatus not wired — service-account status conformance NOT verified for this Repositories")
		}
		t.Run("ProbeSchemaPresent", func(t *testing.T) {
			if err := newRepos(t).ServiceAccountStatus.ProbeSchema(context.Background()); err != nil {
				t.Fatalf("ProbeSchema on a migrated store = %v, want nil", err)
			}
		})
		t.Run("AbsentRowIsActive", func(t *testing.T) { testStatusAbsentIsActive(t, newRepos(t)) })
		t.Run("DisableRevokesKeysOnce", func(t *testing.T) { testStatusDisable(t, newRepos(t)) })
		t.Run("EnableRestoresStatusOnly", func(t *testing.T) { testStatusEnable(t, newRepos(t)) })
		t.Run("UnknownAccountNotFound", func(t *testing.T) { testStatusUnknown(t, newRepos(t)) })
		t.Run("DeleteForgetsTheRow", func(t *testing.T) { testStatusDelete(t, newRepos(t)) })
	})
}

// seedStatusAccount creates a service account holding two keys, one of them
// already revoked.
func seedStatusAccount(t *testing.T, repos auth.Repositories, name string) (serviceaccount.ServiceAccount, []string) {
	t.Helper()
	ctx := context.Background()
	sa, err := serviceaccount.New(ids, name, "", "admin", false, "", suiteBase)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repos.ServiceAccounts.Create(ctx, sa)
	if err != nil {
		t.Fatalf("ServiceAccounts.Create: %v", err)
	}
	live := mustCreateAPIKey(t, repos.APIKeys, created.ID, "live", "hash-status-live-"+name, time.Time{}, suiteBase)
	gone := mustCreateAPIKey(t, repos.APIKeys, created.ID, "gone", "hash-status-gone-"+name, time.Time{}, suiteBase)
	if err := repos.APIKeys.Revoke(ctx, gone.ID, suiteBase.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	return created, []string{"hash-status-live-" + name, "hash-status-gone-" + name, live.ID}
}

func testStatusAbsentIsActive(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	sa, _ := seedStatusAccount(t, repos, "absent")
	got, err := repos.ServiceAccountStatus.GetStatus(ctx, sa.ID)
	if err != nil || got != serviceaccount.StatusActive {
		t.Fatalf("GetStatus(no row) = %q, %v; want active", got, err)
	}
	statuses, err := repos.ServiceAccountStatus.Statuses(ctx, []string{sa.ID, "never-seen"})
	if err != nil || statuses[sa.ID] != serviceaccount.StatusActive || statuses["never-seen"] != serviceaccount.StatusActive {
		t.Fatalf("Statuses = %v, %v; want both active", statuses, err)
	}
}

func testStatusDisable(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	sa, hashes := seedStatusAccount(t, repos, "disable")
	other, otherHashes := seedStatusAccount(t, repos, "bystander")

	change, err := repos.ServiceAccountStatus.Disable(ctx, sa.ID, "admin-1", suiteBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if !change.Changed || change.KeysRevoked != 1 {
		t.Fatalf("Disable = %+v, want Changed and one key revoked (the other was already)", change)
	}
	if got, _ := repos.ServiceAccountStatus.GetStatus(ctx, sa.ID); got != serviceaccount.StatusDisabled {
		t.Fatalf("GetStatus after disable = %q, want disabled", got)
	}
	live, err := repos.APIKeys.GetByHash(ctx, hashes[0])
	if err != nil || !live.Revoked() {
		t.Fatalf("the account's live key after disable = %+v, %v; want revoked", live, err)
	}
	bystander, err := repos.APIKeys.GetByHash(ctx, otherHashes[0])
	if err != nil || bystander.Revoked() {
		t.Fatalf("another account's key = %+v, %v; want untouched", bystander, err)
	}
	statuses, err := repos.ServiceAccountStatus.Statuses(ctx, []string{sa.ID, other.ID})
	if err != nil || statuses[sa.ID] != serviceaccount.StatusDisabled || statuses[other.ID] != serviceaccount.StatusActive {
		t.Fatalf("Statuses = %v, %v", statuses, err)
	}

	again, err := repos.ServiceAccountStatus.Disable(ctx, sa.ID, "admin-1", suiteBase.Add(2*time.Hour))
	if err != nil || again.Changed || again.KeysRevoked != 0 {
		t.Fatalf("repeat Disable = %+v, %v; want Changed=false, nothing revoked", again, err)
	}
}

func testStatusEnable(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	sa, hashes := seedStatusAccount(t, repos, "enable")
	if change, err := repos.ServiceAccountStatus.Enable(ctx, sa.ID, "admin-1", suiteBase.Add(time.Hour)); err != nil || change.Changed {
		t.Fatalf("Enable of an active account = %+v, %v; want Changed=false", change, err)
	}
	if _, err := repos.ServiceAccountStatus.Disable(ctx, sa.ID, "admin-1", suiteBase.Add(2*time.Hour)); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	change, err := repos.ServiceAccountStatus.Enable(ctx, sa.ID, "admin-1", suiteBase.Add(3*time.Hour))
	if err != nil || !change.Changed || change.KeysRevoked != 0 {
		t.Fatalf("Enable = %+v, %v; want Changed, nothing revoked", change, err)
	}
	if got, _ := repos.ServiceAccountStatus.GetStatus(ctx, sa.ID); got != serviceaccount.StatusActive {
		t.Fatalf("GetStatus after enable = %q, want active", got)
	}
	live, err := repos.APIKeys.GetByHash(ctx, hashes[0])
	if err != nil || !live.Revoked() {
		t.Fatalf("a key the disable revoked = %+v, %v; want still revoked", live, err)
	}
}

func testStatusUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.ServiceAccountStatus.Disable(ctx, "sa-unknown", "admin-1", suiteBase); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Disable(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := repos.ServiceAccountStatus.Enable(ctx, "sa-unknown", "admin-1", suiteBase); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Enable(unknown) = %v, want ErrNotFound", err)
	}
}

func testStatusDelete(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	sa, _ := seedStatusAccount(t, repos, "delete")
	if _, err := repos.ServiceAccountStatus.Disable(ctx, sa.ID, "admin-1", suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := repos.ServiceAccountStatus.Delete(ctx, sa.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, _ := repos.ServiceAccountStatus.GetStatus(ctx, sa.ID); got != serviceaccount.StatusActive {
		t.Fatalf("GetStatus after Delete = %q, want active (no row)", got)
	}
	if err := repos.ServiceAccountStatus.Delete(ctx, sa.ID); err != nil {
		t.Fatalf("Delete of an absent row = %v, want nil", err)
	}
}

// RunServiceAccountStatusSchemaProbe runs the probe contract against a store
// WITHOUT migration 0027: ProbeSchema must report sdk.ErrNotFound, the one
// answer New maps to ErrServiceAccountStatusSchemaMissing.
func RunServiceAccountStatusSchemaProbe(t *testing.T, newRepo func(t *testing.T) serviceaccount.StatusRepository) {
	t.Helper()
	if err := newRepo(t).ProbeSchema(context.Background()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeSchema on an unmigrated store = %v, want sdk.ErrNotFound", err)
	}
}
