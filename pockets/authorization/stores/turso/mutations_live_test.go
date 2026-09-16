//go:build integration

package turso

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func liveRepos(t *testing.T) (*tursodb.DB, storetest.Repositories) { return liveReposWith(t) }
func liveReposNoIntegrity(t *testing.T) (*tursodb.DB, storetest.Repositories) {
	return liveReposWith(t)
}
func liveReposWith(t *testing.T, opts ...Option) (*tursodb.DB, storetest.Repositories) {
	t.Helper()
	url, token := requireTursoEnv(t)
	db := openAndMigrate(t, url, token)
	repos, err := testRepositories(context.Background(), db, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return db, repos
}
func grantCmd(resourceID, relation, subjectID string) mutations.Command {
	return mutations.Command{Target: mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: resourceID}, Operation: mutations.OpGrant, Relationships: []mutations.RelationshipRow{{Relation: relation, Subject: relationships.SubjectRef{Type: "user", ID: subjectID}}}}
}
func mustApplyLive(t *testing.T, m mutations.MutationRepository, cmd mutations.Command) *mutations.Result {
	t.Helper()
	r, err := m.Apply(context.Background(), cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func scopeOf(typ, id string) mutations.Target {
	return mutations.Target{Kind: mutations.TargetResource, Type: typ, ID: id}
}
func edge(op mutations.Operation, target mutations.Target, relation string, subject relationships.SubjectRef) mutations.Command {
	return mutations.Command{Target: target, Operation: op, Relationships: []mutations.RelationshipRow{{Relation: relation, Subject: subject}}}
}
func auditContext() context.Context {
	return audit.WithSource(context.Background(), audit.Source{System: "sql-test", Reason: "test change"})
}
func auditRecords(t *testing.T, r storetest.Repositories) []audit.Record {
	t.Helper()
	page, err := r.Audit.List(context.Background(), audit.Filter{}, list.Request{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}
func TestMutationConcurrentNaturalNoOpsAndAudit(t *testing.T) {
	_, repos := liveReposWith(t, WithAudit())
	ctx := auditContext()
	const n = 24
	for wave := range 2 {
		var wg sync.WaitGroup
		errs := make([]error, n)
		results := make([]*mutations.Result, n)
		for i := range n {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], errs[i] = repos.Mutations.Apply(ctx, grantCmd("storm", "viewer", fmt.Sprint(i)), nil)
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			want := mutations.OutcomeApplied
			if wave == 1 {
				want = mutations.OutcomeNoChange
			}
			if err != nil || results[i] == nil || results[i].Outcome != want {
				t.Fatalf("wave=%d writer=%d result=%+v err=%v", wave, i, results[i], err)
			}
		}
	}
	records := auditRecords(t, repos)
	if len(records) != n {
		t.Fatalf("audit count=%d want=%d", len(records), n)
	}
	seen := map[string]bool{}
	for _, r := range records {
		if r.Change.Tuple.Scope.Kind != tuples.ResourceScope || r.Change.Tuple.Subject.Relation != "" || seen[r.ID] {
			t.Fatalf("invalid/duplicate record: %+v", r)
		}
		seen[r.ID] = true
	}
}
func TestMutationRefusesAmbientBeforeAnyWrite(t *testing.T) {
	db, repos := liveReposWith(t, WithAudit())
	ctx := auditContext()
	err := db.Transact(ctx, func(ctx context.Context) error {
		r, err := repos.Mutations.Apply(ctx, grantCmd("ambient", "viewer", "alice"), nil)
		if r != nil || !errors.Is(err, mutations.ErrMutationInsideTransaction) {
			t.Fatalf("ambient result=%+v err=%v", r, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if records := auditRecords(t, repos); len(records) != 0 {
		t.Fatalf("refusal audit: %+v", records)
	}
}
