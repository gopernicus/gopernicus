package storetest

import (
	"context"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

// RunPasswordHashUpgrade proves a store's optional user.PasswordHashUpgrader:
// a matching old hash swaps and reports true; a stale old hash reports false
// and changes nothing; a user with no password reports false with a nil error;
// an upgrade leaves auth_revision unchanged and revokes no session; and two
// concurrent upgrades from one old hash have exactly one winner. It runs
// outside Run so a store adopts it explicitly; a Passwords repository that
// does not implement the upgrader fails here.
func RunPasswordHashUpgrade(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("PasswordHashUpgrade", func(t *testing.T) {
		t.Run("MatchingOldHashSwaps", func(t *testing.T) { testUpgradeHashSwaps(t, newRepos(t)) })
		t.Run("StaleOldHashNoChange", func(t *testing.T) { testUpgradeHashStale(t, newRepos(t)) })
		t.Run("NoPasswordReportsFalse", func(t *testing.T) { testUpgradeHashNoPassword(t, newRepos(t)) })
		t.Run("NoRevisionBumpNoRevocation", func(t *testing.T) { testUpgradeHashKeepsFence(t, newRepos(t)) })
		t.Run("ConcurrentSingleWinner", func(t *testing.T) { testUpgradeHashConcurrent(t, newRepos(t)) })
	})
}

func hashUpgrader(t *testing.T, r auth.Repositories) user.PasswordHashUpgrader {
	t.Helper()
	upgrader, ok := r.Passwords.(user.PasswordHashUpgrader)
	if !ok {
		t.Fatalf("Repositories.Passwords (%T) does not implement user.PasswordHashUpgrader", r.Passwords)
	}
	return upgrader
}

// seedUpgradeUser creates a user with a stored password hash "cost10".
func seedUpgradeUser(t *testing.T, r auth.Repositories, id string) {
	t.Helper()
	ensurePasswordOwner(t, r, id)
	if err := r.Passwords.Set(context.Background(), id, "cost10"); err != nil {
		t.Fatal(err)
	}
}

func storedHash(t *testing.T, r auth.Repositories, id string) string {
	t.Helper()
	h, err := r.Passwords.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func testUpgradeHashSwaps(t *testing.T, r auth.Repositories) {
	seedUpgradeUser(t, r, "upgrade-user")
	swapped, err := hashUpgrader(t, r).UpgradeHash(context.Background(), "upgrade-user", "cost10", "cost12")
	if err != nil || !swapped {
		t.Fatalf("UpgradeHash = %v, %v; want true, nil", swapped, err)
	}
	if got := storedHash(t, r, "upgrade-user"); got != "cost12" {
		t.Fatalf("stored hash = %q, want the new hash", got)
	}
}

func testUpgradeHashStale(t *testing.T, r auth.Repositories) {
	seedUpgradeUser(t, r, "upgrade-user")
	swapped, err := hashUpgrader(t, r).UpgradeHash(context.Background(), "upgrade-user", "not-the-stored-hash", "cost12")
	if err != nil || swapped {
		t.Fatalf("UpgradeHash(stale) = %v, %v; want false, nil", swapped, err)
	}
	if got := storedHash(t, r, "upgrade-user"); got != "cost10" {
		t.Fatalf("a stale upgrade changed the hash to %q", got)
	}
}

func testUpgradeHashNoPassword(t *testing.T, r auth.Repositories) {
	ensurePasswordOwner(t, r, "upgrade-no-password")
	upgrader := hashUpgrader(t, r)
	for _, id := range []string{"upgrade-no-password", "upgrade-unknown-user"} {
		swapped, err := upgrader.UpgradeHash(context.Background(), id, "cost10", "cost12")
		if err != nil || swapped {
			t.Fatalf("UpgradeHash(%s) = %v, %v; want false, nil", id, swapped, err)
		}
	}
	if _, err := r.Passwords.Get(context.Background(), "upgrade-no-password"); err == nil {
		t.Fatal("an upgrade created a password row")
	}
}

func testUpgradeHashKeepsFence(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	seedUpgradeUser(t, r, "upgrade-user")
	before, err := r.Users.Get(ctx, "upgrade-user")
	if err != nil {
		t.Fatal(err)
	}
	existing := newSession(before.ID, "upgrade-refresh", time.Hour, time.Now())
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, existing, before.AuthRevision); err != nil {
		t.Fatal(err)
	}
	if swapped, err := hashUpgrader(t, r).UpgradeHash(ctx, before.ID, "cost10", "cost12"); err != nil || !swapped {
		t.Fatalf("UpgradeHash = %v, %v", swapped, err)
	}
	after, err := r.Users.Get(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AuthRevision != before.AuthRevision {
		t.Fatalf("auth_revision %d -> %d; an upgrade is not a credential change", before.AuthRevision, after.AuthRevision)
	}
	if _, err := r.Sessions.Get(ctx, existing.ID); err != nil {
		t.Fatalf("an upgrade revoked the existing session: %v", err)
	}
	fresh := newSession(before.ID, "upgrade-late", time.Hour, time.Now())
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, fresh, before.AuthRevision); err != nil {
		t.Fatalf("an admission fenced on the pre-upgrade revision was refused: %v", err)
	}
}

func testUpgradeHashConcurrent(t *testing.T, r auth.Repositories) {
	seedUpgradeUser(t, r, "upgrade-user")
	upgrader := hashUpgrader(t, r)
	const racers = 8
	var wg sync.WaitGroup
	results := make([]bool, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = upgrader.UpgradeHash(context.Background(), "upgrade-user", "cost10", "cost12-"+string(rune('a'+i)))
		}()
	}
	close(start)
	wg.Wait()
	winners, winner := 0, -1
	for i := range racers {
		if errs[i] != nil {
			t.Fatalf("racer %d: %v", i, errs[i])
		}
		if results[i] {
			winners++
			winner = i
		}
	}
	if winners != 1 {
		t.Fatalf("%d upgrades from one old hash reported true, want exactly 1", winners)
	}
	if got, want := storedHash(t, r, "upgrade-user"), "cost12-"+string(rune('a'+winner)); got != want {
		t.Fatalf("stored hash = %q, want the winner's %q", got, want)
	}
}
