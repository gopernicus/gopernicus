// Live pgx-specific mutation tests beyond the shared storetest suite. They prove
// two dialect properties the shared suite exercises only indirectly: an ABSENT
// scope anchor cannot create a phantom bypass (a guard-observed revision-0
// dependency turns a concurrent first writer into a detectable 0→1 stale), and a
// guard panic rolls the whole transaction back to a coarse infrastructure error
// with no relationship, revision, or receipt trace. They require POSTGRES_TEST_DSN
// and skip loudly without it, like the conformance suite.
package pgx

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

func liveRepos(t *testing.T) (*pgxdb.DB, authorization.Repositories) {
	t.Helper()
	db := openAndMigrate(t, requireDSN(t))
	repos, err := Repositories(db)
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	return db, repos
}

func mutID(t *testing.T) mutation.MutationID {
	t.Helper()
	id, err := mutation.NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return id
}

func grantCmd(id mutation.MutationID, resourceID, relation, subjectID string) mutation.Command {
	return mutation.Command{
		MutationID:    id,
		Scope:         mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: resourceID},
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: relationship.SubjectRef{Type: "user", ID: subjectID}}},
	}
}

// TestMutationAbsentAnchorNoPhantom proves that a scope observed by a guard with NO
// revision anchor records revision 0, so a concurrent FIRST writer to that scope
// (0→1) is detected as a stale dependency and the guarded mutation commits nothing.
// The guard records dependency scope S at revision 0, then a concurrent transaction
// establishes S (creating its anchor and bumping it to 1). When the guarded
// transaction locks S FOR UPDATE and re-reads, the observed 0 no longer matches, so
// it returns the stale-revision command error rather than a phantom allow.
func TestMutationAbsentAnchorNoPhantom(t *testing.T) {
	ctx := context.Background()
	_, repos := liveRepos(t)
	m := repos.Mutations

	// The mutation target M is established with an owner so the write itself is valid.
	mustApplyLive(t, m, grantCmd(mutID(t), "M", "owner", "u1"))

	sScope := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: "S"}

	guard := func(gctx context.Context, view mutation.DecisionView) error {
		// 1. Record dependency on S while it has no anchor → observed revision 0.
		if _, err := view.CheckRelation(gctx, sScope, "owner", "user", "u9"); err != nil {
			return err
		}
		// 2. A concurrent transaction establishes S (0→1) and commits before the
		//    guarded transaction acquires S's anchor lock.
		if _, err := m.Apply(ctx, grantCmd(mutID(t), "S", "owner", "u2"), nil); err != nil {
			t.Errorf("concurrent establish of S failed: %v", err)
		}
		// 3. Allow — the guarded mutation would proceed if the dependency held.
		return nil
	}

	guarded := grantCmd(mutID(t), "M", "viewer", "u3")
	rcpt, err := m.ApplyGuarded(ctx, guarded, guard, nil)
	if err == nil {
		t.Fatalf("a concurrent 0→1 change to an absent-anchor dependency must be stale, got receipt %+v", rcpt)
	}
	if rcpt != nil {
		t.Fatalf("a stale guarded mutation must return no receipt")
	}
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("absent-anchor phantom detection must be a stale-revision conflict, got %v", err)
	}
	// The guarded viewer grant committed nothing — no phantom bypass.
	if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "M", "viewer", "user", "u3"); ok {
		t.Fatalf("a detected stale guard must not commit its row (phantom bypass)")
	}
}

// TestMutationGuardPanicRollsBack proves a guard panic is recovered into a coarse
// infrastructure error and the whole transaction rolls back: no relationship row,
// no revision bump, and no receipt.
func TestMutationGuardPanicRollsBack(t *testing.T) {
	ctx := context.Background()
	db, repos := liveRepos(t)
	m := repos.Mutations
	mustApplyLive(t, m, grantCmd(mutID(t), "P", "owner", "u1")) // revision 1

	panicGuard := func(context.Context, mutation.DecisionView) error {
		panic("guard blew up")
	}

	id := mutID(t)
	cmd := grantCmd(id, "P", "viewer", "u2")
	rcpt, err := m.ApplyGuarded(ctx, cmd, panicGuard, nil)
	if err == nil {
		t.Fatalf("a guard panic must be a command error, got receipt %+v", rcpt)
	}
	if rcpt != nil {
		t.Fatalf("a panicked guard must return no receipt")
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("a recovered guard panic must be a coarse infrastructure error, got %v", err)
	}
	// No relationship written.
	if ok, _ := repos.Relationships.CheckRelationExists(ctx, "doc", "P", "viewer", "user", "u2"); ok {
		t.Fatalf("a rolled-back guard panic must write no relationship")
	}
	// No revision bump: the P scope anchor stays at revision 1.
	var rev int64
	if err := db.QueryRow(ctx,
		`SELECT revision FROM iam_scopes WHERE scope_kind = 'resource' AND scope_type = 'doc' AND scope_id = 'P'`).Scan(&rev); err != nil {
		t.Fatalf("read P revision: %v", err)
	}
	if rev != 1 {
		t.Fatalf("a rolled-back guard panic must not bump the revision, got %d", rev)
	}
	// No receipt persisted for the mutation id.
	var n int
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM iam_mutations WHERE mutation_id = $1`, string(id)).Scan(&n); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if n != 0 {
		t.Fatalf("a rolled-back guard panic must persist no receipt, got %d", n)
	}
}

func mustApplyLive(t *testing.T, m mutation.MutationRepository, cmd mutation.Command) *mutation.Receipt {
	t.Helper()
	rcpt, err := m.Apply(context.Background(), cmd, nil)
	if err != nil {
		t.Fatalf("Apply(%s): %v", cmd.Operation, err)
	}
	return rcpt
}

// TestMutationStormForensics is the pgx SQL forensic the shared storetest
// ConcurrentReceiptRevisionForensics complements: after a concurrent grant storm
// it inspects the STORED ROWS directly for the anomalies the port cannot surface —
// duplicate receipts, revision gaps, scope anchors that disagree with row state,
// permanent-retention receipts that grew an expiry, and phantom userset relations
// on concrete-subject grants. It drives every grant to a terminal state before
// inspecting (recipe: no partial storm at teardown).
func TestMutationStormForensics(t *testing.T) {
	ctx := context.Background()
	db, repos := liveRepos(t)
	m := repos.Mutations

	const n = 24
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.Apply(ctx, grantCmd(mutID(t), "F", "owner", "o"+strconv.Itoa(i)), nil)
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("storm grant %d errored: %v", i, errs[i])
		}
	}

	// Anchor agrees with committed row state: the scope revision equals the number
	// of committed owner grants.
	var rev, owners int64
	if err := db.QueryRow(ctx,
		`SELECT revision FROM iam_scopes WHERE scope_kind='resource' AND scope_type='doc' AND scope_id='F'`).Scan(&rev); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM iam_relationships WHERE resource_type='doc' AND resource_id='F' AND relation='owner'`).Scan(&owners); err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if rev != n || owners != n {
		t.Fatalf("anchor/row disagreement: revision=%d owners=%d want %d/%d", rev, owners, n, n)
	}

	// No duplicate receipts and a gapless applied revision run 1..n for the scope.
	var total, distinctID, minRev, maxRev int64
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(DISTINCT mutation_id), MIN(revision), MAX(revision)
		   FROM iam_mutations WHERE scope_kind='resource' AND scope_type='doc' AND scope_id='F' AND outcome='applied'`).
		Scan(&total, &distinctID, &minRev, &maxRev); err != nil {
		t.Fatalf("inspect receipts: %v", err)
	}
	if total != n || distinctID != n {
		t.Fatalf("duplicate receipts: %d rows, %d distinct ids, want %d/%d", total, distinctID, n, n)
	}
	if minRev != 1 || maxRev != n {
		t.Fatalf("applied revision run not gapless: min=%d max=%d over %d rows, want 1..%d", minRev, maxRev, total, n)
	}
	var distinctRev int64
	if err := db.QueryRow(ctx,
		`SELECT COUNT(DISTINCT revision) FROM iam_mutations WHERE scope_kind='resource' AND scope_type='doc' AND scope_id='F' AND outcome='applied'`).
		Scan(&distinctRev); err != nil {
		t.Fatalf("distinct revisions: %v", err)
	}
	if distinctRev != n {
		t.Fatalf("two applied receipts claimed one revision: %d distinct over %d rows", distinctRev, n)
	}

	// Permanent retention (default #2): no stored receipt ever grew an expiry.
	var withExpiry int64
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM iam_mutations WHERE expires_at IS NOT NULL`).Scan(&withExpiry); err != nil {
		t.Fatalf("count expiring receipts: %v", err)
	}
	if withExpiry != 0 {
		t.Fatalf("permanent retention violated: %d receipts carry an expires_at", withExpiry)
	}

	// No invalid userset: a concrete-subject grant must never store a userset
	// relation (subject_relation stays the empty string).
	var phantomUsersets int64
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM iam_relationships WHERE resource_type='doc' AND resource_id='F' AND subject_relation <> ''`).
		Scan(&phantomUsersets); err != nil {
		t.Fatalf("scan usersets: %v", err)
	}
	if phantomUsersets != 0 {
		t.Fatalf("concrete-subject grants grew %d phantom userset relations", phantomUsersets)
	}
}
