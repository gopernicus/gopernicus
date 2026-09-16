package pgx

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func TestSetRelationTargetsConcurrentDifferentLabelsCoexistLive(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		ambient, competingSet bool
	}{
		{"create/standalone", false, false},
		{"create/ambient", true, false},
		{"set/standalone", false, true},
		{"set/ambient", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, repos := liveReposNoIntegrity(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			row := func(relation, subject string) relationships.CreateRelationship {
				return relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d1", Relation: relation, SubjectType: "user", SubjectID: subject}
			}
			if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{row("reader", "old")}); err != nil {
				t.Fatal(err)
			}
			ready, competitorDone := make(chan error, 1), make(chan error, 1)
			commitCompetitor := make(chan struct{})
			go func() {
				competitorDone <- db.Transact(ctx, func(ctx context.Context) error {
					rows := []relationships.CreateRelationship{row("editor", "new")}
					var err error
					if tc.competingSet {
						err = repos.Relationships.SetRelationTargets(ctx, "doc", "d1", "editor", rows)
					} else {
						err = repos.Relationships.CreateRelationships(ctx, rows)
					}
					ready <- err
					if err != nil {
						return err
					}
					select {
					case <-commitCompetitor:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			if err := <-ready; err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				set := func(ctx context.Context) error {
					return repos.Relationships.SetRelationTargets(ctx, "doc", "d1", "reader", []relationships.CreateRelationship{row("reader", "new")})
				}
				if tc.ambient {
					done <- db.Transact(ctx, func(ctx context.Context) error {
						if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{row("marker", "host-before-set")}); err != nil {
							return err
						}
						return set(ctx)
					})
				} else {
					done <- set(ctx)
				}
			}()
			waitForAuthorizationLock(t, ctx, db, done)
			close(commitCompetitor)
			if err := <-competitorDone; err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("concurrent different label failed: %v", err)
			}
			targets, err := repos.Relationships.GetRelationTargets(ctx, "doc", "d1", "reader")
			if err != nil || len(targets) != 1 || targets[0].ID != "new" {
				t.Fatalf("conflict changed the original set: %+v, %v", targets, err)
			}
			if exists, err := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "marker", "user", "host-before-set"); err != nil || exists != tc.ambient {
				t.Fatalf("host work preceding reconciliation mismatch: %v, %v", exists, err)
			}
		})
	}
}

func waitForAuthorizationLock(t *testing.T, ctx context.Context, db *pgxdb.DB, done <-chan error) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query LIKE '%LOCK TABLE %iam_tuples%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("reconciliation returned before its conflict insertion: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

// Default READ COMMITTED host transactions and direct SQL DML must participate
// without knowing about a private revision or advisory-lock protocol.
