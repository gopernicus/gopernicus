package turso

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestGlobalRevocationRacesMints_LocalSQLite races fenced session mints
// against a full RevokeAllBatch loop, untagged so `make check` runs it: once
// it reports Done, no session minted with a pre-bump revision survives and
// every user was bumped exactly once. It asserts the outcome, never the timing.
func TestGlobalRevocationRacesMints_LocalSQLite(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	ctx := t.Context()
	const users, minters = 200, 8
	now := tursodb.FormatTime(time.Now())
	for g := 1; g <= users; g++ {
		if _, err := db.Exec(ctx, `INSERT INTO users (id, display_name, created_at, updated_at) VALUES (?, '', ?, ?)`,
			fmt.Sprintf("race-%04d", g), now, now); err != nil {
			t.Fatal(err)
		}
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
	if err := db.QueryRow(ctx, `SELECT count(*) FROM users WHERE auth_revision <> 1`).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong != 0 {
		t.Errorf("%d users not bumped exactly once", wrong)
	}
	survivors := 0
	for len(stale) > 0 {
		chunk := stale[:min(len(stale), 500)]
		stale = stale[len(chunk):]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id IN (?`+strings.Repeat(", ?", len(chunk)-1)+`)`, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		survivors += n
	}
	if survivors != 0 {
		t.Fatalf("%d sessions minted at the pre-bump revision survived the revocation", survivors)
	}
}
