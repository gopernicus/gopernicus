// Live erasure races. Each case parks an Erase on a row lock held by a fixture
// transaction, queues a competing write behind it, releases the lock and
// checks that exactly one of the two legal outcomes happened — never a half
// state. They run as a posture runtime role in a disposable schema (see
// maintenance_live_test.go) and skip loudly without a DSN.
package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
)

// eraseRace is one fixture: a posture runtime role's repositories, a user with
// a verified email and a pending invitation addressed to it.
type eraseRace struct {
	l       *auditLive
	runtime string
	repos   auth.Repositories
	user    user.User
	inv     invitations.Invitation
}

func newEraseRace(t *testing.T) *eraseRace {
	t.Helper()
	ctx := context.Background()
	l := newRetentionLive(t)
	l.migrate(t, MigrationsFS)
	r := &eraseRace{l: l, runtime: l.postureRole(t, "runtime", true)}
	repos, err := Repositories(ctx, l.openAs(t, r.runtime, ""), l.opts()...)
	if err != nil {
		t.Fatal(err)
	}
	r.repos = repos
	now := time.Now().UTC().Truncate(time.Microsecond)
	r.user, _, err = repos.Users.CreateWithPrimaryIdentifier(ctx,
		user.User{DisplayName: "Racer", Status: user.StatusActive, CreatedAt: now, UpdatedAt: now},
		identifier.Identifier{Kind: identifier.KindEmail, NormalizedValue: "racer@example.com", VerifiedAt: now,
			LoginEnabled: true, IsPrimary: true, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := invitations.NewInvitation(sdk.IDGenerator{}, "project", "p1", "member", "racer@example.com", "email", "inviter", "race-token", false, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.inv, err = repos.Invitations.Create(ctx, inv); err != nil {
		t.Fatal(err)
	}
	return r
}

// eraseNow erases the fixture user, as an administrator would.
func (r *eraseRace) eraseNow(ctx context.Context) (user.Erasure, error) {
	now := time.Now()
	return r.repos.UserErasure.Erase(ctx, user.EraseInput{ID: r.user.ID, Now: now, Event: securityevent.SecurityEvent{
		UserID: r.user.ID, Actor: securityevent.Principal{Type: "user", ID: "race-admin"}, EventType: securityevent.TypeUserErased,
		EventStatus: securityevent.StatusSuccess, CreatedAt: now}})
}

// lockInvitation holds FOR UPDATE on the fixture invitation in a transaction
// of its own until the returned release runs.
func (r *eraseRace) lockInvitation(t *testing.T) (release func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := r.l.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM `+r.l.schema.Table(invitationsTable)+` WHERE id = $1 FOR UPDATE`, r.inv.ID); err != nil {
		t.Fatal(err)
	}
	done := false
	release = func() {
		if !done {
			done = true
			if err := tx.Commit(); err != nil {
				t.Errorf("release the lock: %v", err)
			}
		}
	}
	t.Cleanup(release)
	return release
}

// awaitLockWaiters blocks until n backends of the runtime role wait on a lock.
func (r *eraseRace) awaitLockWaiters(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := r.l.admin.QueryRow(context.Background(), `SELECT count(*) FROM pg_catalog.pg_stat_activity
			WHERE usename = $1 AND wait_event_type = 'Lock'`, r.runtime).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d lock waiters after 10s", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type eraseOutcome struct {
	erasure user.Erasure
	err     error
}

// TestLive_EraseVersusClaim races ClaimAcceptance against Erase on the same
// invitation row. Erase queued first: it deletes the pending row and the claim
// then finds nothing (sdk.ErrNotFound). Claim queued first: the row turns
// accepting and the erase refuses with ErrEraseBlockedByClaim, writing nothing.
func TestLive_EraseVersusClaim(t *testing.T) {
	for _, eraseFirst := range []bool{true, false} {
		name := map[bool]string{true: "EraseFirst", false: "ClaimFirst"}[eraseFirst]
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			r := newEraseRace(t)
			claimRepos, err := Repositories(ctx, r.l.openAs(t, r.runtime, ""), r.l.opts()...)
			if err != nil {
				t.Fatal(err)
			}
			release := r.lockInvitation(t)
			erased := make(chan eraseOutcome, 1)
			claimed := make(chan error, 1)
			startErase := func() {
				go func() { e, err := r.eraseNow(ctx); erased <- eraseOutcome{e, err} }()
			}
			startClaim := func() {
				go func() {
					_, err := claimRepos.Invitations.ClaimAcceptance(ctx, r.inv.ID, invitations.Acceptance{
						TokenHash: r.inv.TokenHash, SubjectType: "user", SubjectID: r.user.ID, Now: time.Now()})
					claimed <- err
				}()
			}
			if eraseFirst {
				startErase()
				r.awaitLockWaiters(t, 1)
				startClaim()
			} else {
				startClaim()
				r.awaitLockWaiters(t, 1)
				startErase()
			}
			r.awaitLockWaiters(t, 2)
			release()
			e, claimErr := <-erased, <-claimed

			got, getErr := r.repos.Invitations.Get(ctx, r.inv.ID)
			u, err := r.repos.Users.Get(ctx, r.user.ID)
			if err != nil {
				t.Fatal(err)
			}
			if eraseFirst {
				if e.err != nil || !e.erasure.Changed || e.erasure.Counts.InvitationsDeleted != 1 {
					t.Fatalf("erase = %+v, %v; want it to delete the pending invitation", e.erasure, e.err)
				}
				if !errors.Is(claimErr, sdk.ErrNotFound) || !errors.Is(getErr, sdk.ErrNotFound) || u.Status != user.StatusErased {
					t.Fatalf("claim = %v, invitation = %+v (%v), user = %s; want the claim to find nothing", claimErr, got, getErr, u.Status)
				}
				return
			}
			if claimErr != nil || getErr != nil || got.Status != invitations.StatusAccepting {
				t.Fatalf("claim = %v, invitation = %+v (%v); want an accepting claim", claimErr, got, getErr)
			}
			if !errors.Is(e.err, user.ErrEraseBlockedByClaim) || u.Status != user.StatusActive || u.DisplayName != "Racer" {
				t.Fatalf("erase = %+v, %v; user = %+v; want ErrEraseBlockedByClaim with nothing written", e.erasure, e.err, u)
			}
			if _, err := r.repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "racer@example.com"); err != nil {
				t.Fatalf("the refused erase touched the identifier: %v", err)
			}
		})
	}
}

// TestLive_CreateServiceAccountVersusErase parks an Erase after it took FOR
// UPDATE on the user: a service-account insert naming the user as owner waits
// on its FOR SHARE, and once the erase commits it refuses with user.ErrErased
// — no account is left owned by an erased user.
func TestLive_CreateServiceAccountVersusErase(t *testing.T) {
	ctx := context.Background()
	r := newEraseRace(t)
	creator, err := Repositories(ctx, r.l.openAs(t, r.runtime, ""), r.l.opts()...)
	if err != nil {
		t.Fatal(err)
	}
	release := r.lockInvitation(t)
	erased := make(chan eraseOutcome, 1)
	go func() { e, err := r.eraseNow(ctx); erased <- eraseOutcome{e, err} }()
	r.awaitLockWaiters(t, 1)

	created := make(chan error, 1)
	go func() {
		sa, err := serviceaccount.New(sdk.IDGenerator{}, "personal", "", r.user.ID, true, r.user.ID, time.Now())
		if err != nil {
			created <- err
			return
		}
		_, err = creator.ServiceAccounts.Create(ctx, sa)
		created <- err
	}()
	r.awaitLockWaiters(t, 2)
	select {
	case err := <-created:
		t.Fatalf("the insert did not wait on the owner lock: %v", err)
	default:
	}
	release()
	if e := <-erased; e.err != nil || !e.erasure.Changed {
		t.Fatalf("erase = %+v, %v", e.erasure, e.err)
	}
	if err := <-created; !errors.Is(err, user.ErrErased) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("insert after the erase committed = %v, want user.ErrErased", err)
	}
	var owned int
	if err := r.l.admin.QueryRow(ctx, `SELECT count(*) FROM `+r.l.schema.Table(serviceAccountsTable)+` WHERE owner_user_id = $1`, r.user.ID).Scan(&owned); err != nil || owned != 0 {
		t.Fatalf("%d service accounts owned by the erased user (%v)", owned, err)
	}
	var erasedAt *time.Time
	if err := r.l.admin.QueryRow(ctx, `SELECT erased_at FROM `+r.l.schema.Table(usersTable)+` WHERE id = $1`, r.user.ID).Scan(&erasedAt); err != nil || erasedAt == nil {
		t.Fatalf("erased_at = %v, %v", erasedAt, err)
	}
}
