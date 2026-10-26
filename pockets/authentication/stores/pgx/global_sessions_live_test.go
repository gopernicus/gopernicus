package pgx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestGlobalRevocationRacesMints_Postgres races fenced session mints against a
// full RevokeAllBatch loop: once it reports Done, no session minted with a
// pre-bump revision survives and every user was bumped exactly once. It
// asserts the outcome, never the timing.
func TestGlobalRevocationRacesMints_Postgres(t *testing.T) {
	db := probeDial(t)
	ctx := context.Background()
	schema := disposableSchema(t, "global_revoke_race")
	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	const users, minters = 200, 8
	if _, err := db.Exec(ctx, `INSERT INTO `+schema.Table(usersTable)+` (id, display_name, created_at, updated_at)
		SELECT 'race-' || lpad(g::text, 4, '0'), '', now(), now() FROM generate_series(1, $1) g`, users); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var mu sync.Mutex
	var stale []string
	var wg sync.WaitGroup
	for w := range minters {
		wg.Go(func() {
			for i := 0; !stop.Load(); i++ {
				userID := fmt.Sprintf("race-%04d", 1+(w*31+i*7)%users)
				u, err := repos.Users.Get(ctx, userID)
				if err != nil {
					t.Errorf("Users.Get: %v", err)
					return
				}
				s, _ := session.NewSession(userID, time.Hour, time.Now())
				s.RefreshTokenHash = fmt.Sprintf("race-%d-%d", w, i)
				_, err = repos.ActiveSessions.CreateForActiveUser(ctx, s, u.AuthRevision)
				switch {
				case errors.Is(err, sdk.ErrConflict):
				case err != nil:
					t.Errorf("CreateForActiveUser: %v", err)
					return
				case u.AuthRevision == 0:
					mu.Lock()
					stale = append(stale, s.ID)
					mu.Unlock()
				}
			}
		})
	}

	time.Sleep(50 * time.Millisecond)
	after, bumped := "", 0
	for {
		batch, err := repos.GlobalSessions.RevokeAllBatch(ctx, after, 50, time.Now())
		if err != nil {
			t.Fatalf("RevokeAllBatch: %v", err)
		}
		bumped += batch.Users
		if batch.Done {
			break
		}
		after = batch.LastUserID
	}
	stop.Store(true)
	wg.Wait()

	if bumped != users {
		t.Errorf("batches bumped %d users, want %d", bumped, users)
	}
	var wrong int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM `+schema.Table(usersTable)+` WHERE auth_revision <> 1`).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong != 0 {
		t.Errorf("%d users not bumped exactly once", wrong)
	}
	var survivors int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM `+schema.Table(sessionsTable)+` WHERE id = ANY($1)`, stale).Scan(&survivors); err != nil {
		t.Fatal(err)
	}
	if survivors != 0 {
		t.Fatalf("%d of %d sessions minted at the pre-bump revision survived the revocation", survivors, len(stale))
	}
	t.Logf("%d pre-bump mints, none survived", len(stale))
}
