// Deterministic pgx interleaving tests for the DecisionView primitives the
// permission walk reads (plan authorization-decisionview-permission, D5). They
// exist ONLY for pgx: memstore holds one mutex across the whole apply and turso
// serializes writers with BEGIN IMMEDIATE, so no concurrent writer can commit
// inside the guard boundary there. PostgreSQL runs the guard BEFORE the anchor
// locks, under READ COMMITTED, so a second connection CAN commit a revoke while
// the guard is mid-flight — the exact window record-before-read closes.
//
// The guard is the interposition point: the test's Guard closure calls the store
// primitives in the order the engine walk would, pausing between them, so no
// test-only hook is needed in store code. They require POSTGRES_TEST_DSN and skip
// loudly without it.
package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

func scopeOf(typ, id string) mutation.ScopeKey {
	return mutation.ScopeKey{Kind: mutation.ScopeResource, Type: typ, ID: id}
}

func edge(id mutation.MutationID, op mutation.Operation, scope mutation.ScopeKey, relation string, subject relationship.SubjectRef) mutation.Command {
	return mutation.Command{
		MutationID:    id,
		Scope:         scope,
		Operation:     op,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: subject}},
	}
}

// pausingGuard runs body inside the guard with a pause point: body signals
// `paused` when it has done its first read, then blocks on `resume` before the
// rest. The test commits a concurrent revoke in between.
type pausingGuard struct {
	paused chan struct{}
	resume chan struct{}
}

func newPausingGuard() *pausingGuard {
	return &pausingGuard{paused: make(chan struct{}), resume: make(chan struct{})}
}

func (p *pausingGuard) pause() {
	close(p.paused)
	<-p.resume
}

// runInterleaved starts the guarded mutation on one goroutine, waits for the
// guard to pause, runs between on the calling goroutine (a second connection),
// resumes the guard, and returns the guarded outcome.
func runInterleaved(t *testing.T, m mutation.MutationRepository, cmd mutation.Command, guard mutation.Guard, pg *pausingGuard, between func()) (*mutation.Receipt, error) {
	t.Helper()
	type outcome struct {
		rcpt *mutation.Receipt
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		rcpt, err := m.ApplyGuarded(context.Background(), cmd, guard, nil)
		done <- outcome{rcpt, err}
	}()
	select {
	case <-pg.paused:
	case o := <-done:
		t.Fatalf("guard finished before pausing: rcpt=%+v err=%v", o.rcpt, o.err)
	case <-time.After(30 * time.Second):
		t.Fatalf("guard never reached its pause point")
	}
	between()
	close(pg.resume)
	select {
	case o := <-done:
		return o.rcpt, o.err
	case <-time.After(30 * time.Second):
		t.Fatalf("guarded mutation did not finish after resume")
		return nil, nil
	}
}

func revisionOf(t *testing.T, db *pgxdb.DB, scope mutation.ScopeKey) mutation.Revision {
	t.Helper()
	var rev mutation.Revision
	if err := db.InTx(context.Background(), func(tx *pgxdb.Tx) error {
		r, err := scopeRevision(context.Background(), tx, testSchema(t), scope)
		rev = r
		return err
	}); err != nil {
		t.Fatalf("scopeRevision: %v", err)
	}
	return rev
}

func assertNoRow(t *testing.T, repos interface {
	CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)
}, typ, id, relation, subjectID string) {
	t.Helper()
	ok, err := repos.CheckRelationExists(context.Background(), typ, id, relation, "user", subjectID)
	if err != nil {
		t.Fatalf("CheckRelationExists: %v", err)
	}
	if ok {
		t.Fatalf("a stale guarded write must commit no row, but %s:%s#%s@user:%s exists", typ, id, relation, subjectID)
	}
}

// TestDecisionViewParentEdgeRevokedAfterTargetReadIsStale is the D5 parent-edge
// interleaving. The walk's Through-only branch first reads the dashboard's
// `space` targets; between that read and the space check a second connection
// revokes the parent edge and commits. The old target still leads to an
// otherwise valid grant (alice manages the space), so the guard ALLOWS — and
// commit validation must return ErrStaleRevision because RelationTargets recorded
// dashboard:d1 at its PRE-revoke revision. With read-then-record it would have
// recorded the post-revoke revision and validated the stale edge; this test
// fails in that world.
func TestDecisionViewParentEdgeRevokedAfterTargetReadIsStale(t *testing.T) {
	db, repos := liveReposNoGuardian(t)
	m := repos.Mutations

	dashboard, space := scopeOf("dashboard", "d1"), scopeOf("space", "s1")
	parentEdge := relationship.SubjectRef{Type: "space", ID: "s1"}
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, dashboard, "space", parentEdge))
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, space, "manager", relationship.SubjectRef{Type: "user", ID: "alice"}))
	revBefore := revisionOf(t, db, dashboard)

	pg := newPausingGuard()
	var sawTarget, spaceAllowed bool
	guard := func(gctx context.Context, view mutation.StoreDecisionView) error {
		targets, err := view.RelationTargets(gctx, dashboard, "space")
		if err != nil {
			return err
		}
		for _, tg := range targets {
			if tg.Type == "space" && tg.ID == "s1" {
				sawTarget = true
			}
		}
		pg.pause() // the revoke commits here
		ok, err := view.CheckRelationBounded(gctx, space, "manager", "user", "alice", 100)
		if err != nil {
			return err
		}
		spaceAllowed = ok
		return nil // the old target led to a valid grant: ALLOW
	}

	guarded := edge(mutID(t), mutation.OpGrant, dashboard, "viewer", relationship.SubjectRef{Type: "user", ID: "bob"})
	var revokeRcpt *mutation.Receipt
	rcpt, err := runInterleaved(t, m, guarded, guard, pg, func() {
		revokeRcpt = mustApplyLive(t, m, edge(mutID(t), mutation.OpRevoke, dashboard, "space", parentEdge))
	})

	if !sawTarget || !spaceAllowed {
		t.Fatalf("precondition: guard must have seen the parent edge (%v) and alice's space grant (%v)", sawTarget, spaceAllowed)
	}
	if revokeRcpt.Outcome != mutation.OutcomeApplied || revokeRcpt.Revision != revBefore+1 {
		t.Fatalf("revoke must commit with a revision bump: %+v (before %d)", revokeRcpt, revBefore)
	}
	if !errors.Is(err, mutation.ErrStaleRevision) {
		t.Fatalf("guarded write must abort as stale (parent edge revoked after the target read), got rcpt=%+v err=%v", rcpt, err)
	}
	if rcpt != nil {
		t.Fatalf("a stale guarded write returns no receipt, got %+v", rcpt)
	}
	assertNoRow(t, repos.Relationships, "dashboard", "d1", "viewer", "bob")
	if got := revisionOf(t, db, dashboard); got != revBefore+1 {
		t.Fatalf("dashboard revision must reflect only the revoke: got %d want %d", got, revBefore+1)
	}
}

// TestDecisionViewInheritedGrantRevokedAfterCheckIsStale is the D5 folder-grant
// interleaving: the guard's bounded check on doc:f1 succeeds through alice's
// membership in group:eng, then a second connection revokes that membership
// (under group:eng's scope, which the guarded command does not mutate) and
// commits. The guard allows on its earlier read; commit validation must see the
// group:eng bump the one-snapshot expansion recorded and abort as stale.
func TestDecisionViewInheritedGrantRevokedAfterCheckIsStale(t *testing.T) {
	db, repos := liveReposNoGuardian(t)
	m := repos.Mutations

	doc, group := scopeOf("doc", "f1"), scopeOf("group", "eng")
	alice := relationship.SubjectRef{Type: "user", ID: "alice"}
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, group, "member", alice))
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, doc, "editor", relationship.SubjectRef{Type: "group", ID: "eng", Relation: "member"}))
	groupBefore, docBefore := revisionOf(t, db, group), revisionOf(t, db, doc)

	pg := newPausingGuard()
	var allowed bool
	var deps []mutation.Dependency
	guard := func(gctx context.Context, view mutation.StoreDecisionView) error {
		ok, err := view.CheckRelationBounded(gctx, doc, "editor", "user", "alice", 100)
		if err != nil {
			return err
		}
		allowed = ok
		deps = view.Dependencies()
		pg.pause() // the membership revoke commits here
		return nil
	}

	guarded := edge(mutID(t), mutation.OpGrant, doc, "viewer", relationship.SubjectRef{Type: "user", ID: "bob"})
	var revokeRcpt *mutation.Receipt
	rcpt, err := runInterleaved(t, m, guarded, guard, pg, func() {
		revokeRcpt = mustApplyLive(t, m, edge(mutID(t), mutation.OpRevoke, group, "member", alice))
	})

	if !allowed {
		t.Fatalf("precondition: alice must be editor via group:eng before the revoke")
	}
	if !depsContain(deps, group) {
		t.Fatalf("the expansion must record group:eng as a dependency; deps=%+v", deps)
	}
	for _, d := range deps {
		if d.Scope.Canonical() == group.Canonical() && d.Revision != groupBefore {
			t.Fatalf("group:eng must be recorded at its pre-revoke revision %d, got %d", groupBefore, d.Revision)
		}
	}
	if revokeRcpt.Outcome != mutation.OutcomeApplied || revokeRcpt.Revision != groupBefore+1 {
		t.Fatalf("membership revoke must commit with a revision bump: %+v", revokeRcpt)
	}
	if !errors.Is(err, mutation.ErrStaleRevision) {
		t.Fatalf("guarded write must abort as stale (inherited grant revoked after the check), got rcpt=%+v err=%v", rcpt, err)
	}
	assertNoRow(t, repos.Relationships, "doc", "f1", "viewer", "bob")
	if got := revisionOf(t, db, doc); got != docBefore {
		t.Fatalf("doc:f1 revision must be untouched by the aborted write: got %d want %d", got, docBefore)
	}
}

// TestDecisionViewBoundedCheckOverflowMatchesReadSide proves the adapter seam
// the fake-reader parity cannot: the view's bounded check overflows at exactly
// the state count the read-side CheckRelationWithGroupExpansion overflows at,
// and never records more scopes than the budget allowed.
func TestDecisionViewBoundedCheckOverflowMatchesReadSide(t *testing.T) {
	ctx := context.Background()
	db, repos := liveReposNoGuardian(t)
	m := repos.Mutations

	// alice -> g1#member -> g2#member -> g3#member; doc:x#editor@g3#member.
	// Reachable states: alice, g1, g2, g3, and the doc grant state itself = 5
	// (the expansion follows every edge whose subject is reachable, the target
	// grant included — the same accounting as the storetest budget oracle).
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, scopeOf("group", "g1"), "member", relationship.SubjectRef{Type: "user", ID: "alice"}))
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, scopeOf("group", "g2"), "member", relationship.SubjectRef{Type: "group", ID: "g1", Relation: "member"}))
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, scopeOf("group", "g3"), "member", relationship.SubjectRef{Type: "group", ID: "g2", Relation: "member"}))
	mustApplyLive(t, m, edge(mutID(t), mutation.OpGrant, scopeOf("doc", "x"), "editor", relationship.SubjectRef{Type: "group", ID: "g3", Relation: "member"}))

	for _, bound := range []int{0, 3, 4, 5, 6} {
		wantOK, wantErr := repos.Relationships.CheckRelationWithGroupExpansion(ctx, "doc", "x", "editor", "user", "alice", bound)
		var gotOK bool
		var gotErr error
		var deps []mutation.Dependency
		if err := db.InTx(ctx, func(tx *pgxdb.Tx) error {
			view := newDecisionView(tx, testSchema(t))
			gotOK, gotErr = view.CheckRelationBounded(ctx, scopeOf("doc", "x"), "editor", "user", "alice", bound)
			deps = view.Dependencies()
			return nil
		}); err != nil {
			t.Fatalf("InTx: %v", err)
		}
		if gotOK != wantOK || !errors.Is(gotErr, wantErr) || (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("bound %d: read side (%v, %v) vs view (%v, %v)", bound, wantOK, wantErr, gotOK, gotErr)
		}
		if bound == 3 || bound == 4 {
			if !errors.Is(gotErr, relationship.ErrExpansionBudgetExceeded) {
				t.Fatalf("bound %d must overflow a 5-state expansion, got ok=%v err=%v", bound, gotOK, gotErr)
			}
			if len(deps) > bound+1 {
				t.Fatalf("dependency collection must stay bounded on overflow: %d deps for bound %d", len(deps), bound)
			}
		} else {
			if !gotOK {
				t.Fatalf("bound %d must allow the 5-state expansion", bound)
			}
			for _, g := range []string{"g1", "g2", "g3"} {
				if !depsContain(deps, scopeOf("group", g)) {
					t.Fatalf("bound %d: expansion scope group:%s must be a dependency; deps=%+v", bound, g, deps)
				}
			}
		}
	}
}
