package pgx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestMutationNegativeMembershipWriteSkewLive(t *testing.T) {
	for _, mode := range []string{"direct", "nested_cycle", "current_model"} {
		t.Run(mode, func(t *testing.T) {
			db, repos := liveReposNoGuardian(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			grantAt := func(typ, id, relation string, subject relationships.SubjectRef) mutations.Command {
				return mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: typ, ID: id}, Operation: mutations.OpGrant,
					Relationships: []mutations.RelationshipRow{{Relation: relation, Subject: subject}}}
			}
			for _, n := range []string{"1", "2"} {
				group := "g" + n
				if mode == "nested_cycle" {
					mustApplyLive(t, repos.Mutations, grantAt("group", "middle"+n, "member", relationships.SubjectRef{Type: "group", ID: group, Relation: "member"}))
					mustApplyLive(t, repos.Mutations, grantAt("group", group, "member", relationships.SubjectRef{Type: "group", ID: "middle" + n, Relation: "member"}))
					group = "middle" + n
				}
				mustApplyLive(t, repos.Mutations, grantAt("doc", "d"+n, "blocked", relationships.SubjectRef{Type: "group", ID: group, Relation: "member"}))
			}
			model := relationships.NewReadModel([]relationships.SubjectRule{
				{ResourceType: "doc", Relation: "blocked", SubjectType: "group", SubjectRelation: "member"},
				{ResourceType: "group", Relation: "member", SubjectType: "group", SubjectRelation: "member"},
				{ResourceType: "group", Relation: "member", SubjectType: "user"},
			})
			if mode == "current_model" {
				mustApplyLive(t, repos.Mutations, grantAt("group", "old", "old_member", relationships.SubjectRef{Type: "user", ID: "alice"}))
				for _, id := range []string{"d1", "d2"} {
					mustApplyLive(t, repos.Mutations, grantAt("doc", id, "blocked", relationships.SubjectRef{Type: "group", ID: "old", Relation: "old_member"}))
				}
				if raw, err := repos.Relationships.CheckRelationWithGroupExpansion(ctx, "doc", "d1", "blocked", "user", "alice", 20); err != nil || !raw {
					t.Fatalf("legacy setup must grant through the raw reader: %v, %v", raw, err)
				}
			}
			entered, release := make(chan struct{}, 2), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			type result struct {
				result *mutations.Result
				err    error
			}
			results := make(chan result, 2)
			for i := range 2 {
				cmd := grantAt("group", fmt.Sprintf("g%d", i+1), "member", relationships.SubjectRef{Type: "user", ID: "alice"})
				checked := fmt.Sprintf("d%d", 2-i)
				go func() {
					rcpt, err := repos.Mutations.ApplyGuarded(ctx, cmd, func(ctx context.Context, view mutations.StoreDecisionView) error {
						var blocked bool
						var err error
						if mode == "current_model" {
							blocked, err = view.ForModel(model).CheckRelationWithGroupExpansion(ctx, "doc", checked, "blocked", "user", "alice", 20)
						} else {
							blocked, err = view.CheckRelationBounded(ctx, mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: checked}, "blocked", "user", "alice", 20)
						}
						if err != nil {
							return err
						}
						if blocked {
							return sdk.ErrForbidden
						}
						entered <- struct{}{}
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}, nil)
					results <- result{rcpt, err}
				}()
			}
			select {
			case <-entered:
			case r := <-results:
				t.Fatalf("guard exited before barrier: %+v", r)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			waitForAuthorizationLock(t, ctx, db, nil)
			unblock()
			applied := 0
			for range 2 {
				r := <-results
				switch {
				case r.err == nil && r.result != nil && r.result.Outcome == mutations.OutcomeApplied:
					applied++
				case errors.Is(r.err, sdk.ErrForbidden) && r.result == nil:
				default:
					t.Fatalf("second guard must observe the first committed membership: %+v", r)
				}
			}
			if applied != 1 {
				t.Fatal("both negative guards committed; no serial execution permits this state")
			}
		})
	}
}

func TestSetRelationTargetsConcurrentConflictRollsBackLive(t *testing.T) {
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
			db, repos := liveReposNoGuardian(t)
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
			if err := <-done; !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("concurrent different relation must conflict: %v", err)
			}
			targets, err := repos.Relationships.GetRelationTargets(ctx, "doc", "d1", "reader")
			if err != nil || len(targets) != 1 || targets[0].ID != "old" {
				t.Fatalf("conflict changed the original set: %+v, %v", targets, err)
			}
			if exists, err := repos.Relationships.CheckRelationExists(ctx, "doc", "d1", "marker", "user", "host-before-set"); err != nil || exists {
				t.Fatalf("host work preceding a failed reconciliation committed: %v, %v", exists, err)
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
			AND query LIKE '%LOCK TABLE %iam_relationships%')`).Scan(&blocked); err != nil {
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
func TestGuardWaitsForRawAuthorizationWriters(t *testing.T) {
	for _, mode := range []string{"relationship", "role", "direct_sql"} {
		t.Run(mode, func(t *testing.T) {
			db, repos := liveRepos(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			mustApplyLive(t, repos.Mutations, edge(mutations.OpGrant, scopeOf("doc", "d"), "blocked", relationships.SubjectRef{Type: "group", ID: "g", Relation: "member"}))
			ready := make(chan error, 1)
			release := make(chan struct{})
			writerDone := make(chan error, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			go func() {
				writerDone <- db.Transact(ctx, func(ctx context.Context) error {
					var err error
					switch mode {
					case "relationship":
						err = repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "group", ResourceID: "g", Relation: "member", SubjectType: "user", SubjectID: "alice"}})
					case "role":
						err = repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "blocked"})
					case "direct_sql":
						tx, _ := pgxdb.TxFromContext(ctx)
						_, err = tx.Exec(ctx, `INSERT INTO `+qualify(t, "iam_relationships")+` (resource_type,resource_id,relation,subject_type,subject_id,subject_relation) VALUES ('group','g','member','user','alice','')`)
					}
					ready <- err
					if err != nil {
						return err
					}
					select {
					case <-release:
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
				r, err := repos.Mutations.ApplyGuarded(ctx, grantCmd("protected", "viewer", "alice"), func(ctx context.Context, view mutations.StoreDecisionView) error {
					var blocked bool
					var err error
					if mode == "role" {
						blocked, err = view.HasRole(ctx, mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "someone-else"}, "blocked", "user", "alice")
					} else {
						blocked, err = view.CheckRelationBounded(ctx, scopeOf("doc", "d"), "blocked", "user", "alice", 8)
					}
					if err != nil {
						return err
					}
					if blocked {
						return sdk.ErrForbidden
					}
					return nil
				}, nil)
				if r != nil {
					err = fmt.Errorf("guard ignored committed raw authority: %+v (%v)", r, err)
				}
				done <- err
			}()
			waitForAuthorizationLock(t, ctx, db, done)
			unblock()
			if err := <-writerDone; err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, sdk.ErrForbidden) {
				t.Fatalf("guard must read raw writer's committed state: %v", err)
			}
			if ok, err := repos.Relationships.CheckRelationExists(ctx, "doc", "protected", "viewer", "user", "alice"); err != nil || ok {
				t.Fatalf("denied guard wrote: %v %v", ok, err)
			}
		})
	}
}
