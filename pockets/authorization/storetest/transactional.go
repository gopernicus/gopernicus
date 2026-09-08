package storetest

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// visibilityTimeout bounds every read a spec makes with the ORIGINAL
// (non-ambient) context while a transaction is open. Such a read must land on a
// connection OTHER than the transaction's; a store that routed it onto the
// transaction's connection, or a locking mistake that makes the outside reader
// wait for the open transaction, fails promptly instead of hanging the suite.
const visibilityTimeout = 15 * time.Second

// errInjected is the host-side failure a spec returns from a Transact callback to
// force a rollback. It is asserted back through errors.Is, which also proves the
// transactor returns the callback's error rather than replacing it.
var errInjected = errors.New("storetest: injected host failure")

// RunTransactional executes the ambient-transaction family: the proof that
// every baseline relationship and role write (and every read) JOINS the
// connector's Transact-owned transaction when the context carries one, so a
// host's application row and the tuple that projects it commit or roll back
// together — and that the guarded mutation path refuses to run inside one.
//
// newRepos returns a FRESH, empty Repositories and the crud.Transactor of the
// SAME connector the repositories were built over (the connector's *DB is the
// transactor). A nil transactor skips the family loudly — the in-core memstore
// has no connector and no transaction concept, so the pocket's own hermetic run
// reports the family as skipped rather than silently green. A nil Relationships
// kind skips the family; a nil Roles or Mutations kind skips only its own specs.
//
// Each spec creates its fixture ONCE, before entering Transact, and makes its
// "outside" visibility checks through those SAME repositories with a bounded
// context that carries no transaction — the store then selects the pool and a
// second connection. Specs never call newRepos for an observer: the bundled
// fixtures clear tables on construction, which would destroy seeded state or
// block against the open transaction. The fixture must therefore permit at
// least two simultaneous connections.
//
// A rollback alone proves nothing (a write that never happened also "rolled
// back"), so every spec proves the join from BOTH sides: the ambient read sees
// the uncommitted change, the outside read does not, the injected error undoes
// it, and a nil callback persists it.
func RunTransactional(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	repos, tx := newRepos(t)
	if tx == nil {
		t.Skip("no crud.Transactor supplied — ambient-transaction family NOT verified")
	}
	if repos.Relationships == nil {
		t.Skip("relationship kind not wired")
	}
	t.Run("CreateJoinsTransaction", func(t *testing.T) { specCreateJoinsTransaction(t, newRepos) })
	t.Run("SetRelationTargetsJoinsTransaction", func(t *testing.T) { specSetRelationTargetsJoinsTransaction(t, newRepos) })
	t.Run("SetRelationTargetsConflictRollsBackHostWork", func(t *testing.T) { specSetRelationTargetsConflictRollsBackHostWork(t, newRepos) })
	t.Run("DeletesJoinTransaction", func(t *testing.T) { specDeletesJoinTransaction(t, newRepos) })
	t.Run("RolesJoinTransaction", func(t *testing.T) { specRolesJoinTransaction(t, newRepos) })
	t.Run("ReadsJoinTransaction", func(t *testing.T) { specReadsJoinTransaction(t, newRepos) })
	t.Run("MutationRefusesAmbientTransaction", func(t *testing.T) { specMutationRefusesAmbientTransaction(t, newRepos) })
	t.Run("StandaloneUnchanged", func(t *testing.T) { specStandaloneUnchanged(t, newRepos) })
}

// outside returns a bounded context that carries NO transaction — the "other
// connection" view of the store while a spec's transaction is open.
func outside(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), visibilityTimeout)
	t.Cleanup(cancel)
	return ctx
}

// transact runs fn under tx and returns the transactor's result. It exists so
// every spec spells the host shape the same way: a Background parent context,
// the callback's own ctx handed to every store call.
func transact(tx crud.Transactor, fn func(ctx context.Context) error) error {
	return tx.Transact(context.Background(), fn)
}

// createAt is mustCreate with an explicit context (ambient or outside).
func createAt(t *testing.T, ctx context.Context, s relationship.Storer, tuples ...relationship.CreateRelationship) {
	t.Helper()
	if err := s.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
}

// existsAt is a CheckRelationExists probe with an explicit context.
func existsAt(t *testing.T, ctx context.Context, s relationship.Storer, rt, rid, relation, st, sid string) bool {
	t.Helper()
	ok, err := s.CheckRelationExists(ctx, rt, rid, relation, st, sid)
	if err != nil {
		t.Fatalf("CheckRelationExists(%s:%s#%s@%s:%s): %v", rt, rid, relation, st, sid, err)
	}
	return ok
}

// targetIDsAt returns the sorted target IDs GetRelationTargets reports under ctx.
func targetIDsAt(t *testing.T, ctx context.Context, s relationship.Storer, rt, rid, relation string) []string {
	t.Helper()
	targets, err := s.GetRelationTargets(ctx, rt, rid, relation)
	if err != nil {
		t.Fatalf("GetRelationTargets(%s:%s#%s): %v", rt, rid, relation, err)
	}
	ids := make([]string, 0, len(targets))
	for _, tgt := range targets {
		ids = append(ids, tgt.ID)
	}
	sort.Strings(ids)
	return ids
}

// setTargetsAt reconciles one space#parent key to ids under ctx.
func setTargetsAt(ctx context.Context, s relationship.Storer, ids ...string) error {
	rows := make([]relationship.CreateRelationship, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, ct("space", "child", "parent", "space", id))
	}
	return s.SetRelationTargets(ctx, "space", "child", "parent", rows)
}

// hasExactAt is a HasExactRole probe with an explicit context.
func hasExactAt(t *testing.T, ctx context.Context, s role.Storer, st, sid, roleName, rt, rid string) bool {
	t.Helper()
	ok, err := s.HasExactRole(ctx, st, sid, roleName, rt, rid)
	if err != nil {
		t.Fatalf("HasExactRole: %v", err)
	}
	return ok
}

// assignAt is assign with an explicit context.
func assignAt(t *testing.T, ctx context.Context, s role.Storer, st, sid, roleName, rt, rid string) {
	t.Helper()
	if err := s.Assign(ctx, role.Assignment{SubjectType: st, SubjectID: sid, Role: roleName, ResourceType: rt, ResourceID: rid}); err != nil {
		t.Fatalf("Assign: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// specCreateJoinsTransaction: CreateRelationships inside Transact is visible to
// the ambient reader, invisible outside, gone after an injected error, and
// present after a nil callback.
func specCreateJoinsTransaction(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	repos, tx := newRepos(t)
	s := repos.Relationships
	tuple := ct("doc", "d1", "owner", "user", "u1")

	err := transact(tx, func(ctx context.Context) error {
		createAt(t, ctx, s, tuple)
		if !existsAt(t, ctx, s, "doc", "d1", "owner", "user", "u1") {
			t.Fatalf("ambient read must see the uncommitted tuple")
		}
		if existsAt(t, outside(t), s, "doc", "d1", "owner", "user", "u1") {
			t.Fatalf("outside read must NOT see the uncommitted tuple (the write escaped the transaction)")
		}
		return errInjected
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("Transact must return the callback's error, got %v", err)
	}
	if existsAt(t, outside(t), s, "doc", "d1", "owner", "user", "u1") {
		t.Fatalf("tuple survived the rollback — CreateRelationships did not join the transaction")
	}

	if err := transact(tx, func(ctx context.Context) error {
		createAt(t, ctx, s, tuple)
		return nil
	}); err != nil {
		t.Fatalf("commit shape: %v", err)
	}
	if !existsAt(t, outside(t), s, "doc", "d1", "owner", "user", "u1") {
		t.Fatalf("tuple absent after commit")
	}
}

// specSetRelationTargetsJoinsTransaction is the two-sided proof of the join:
// while the transaction is open, GetRelationTargets on the SAME store sees the
// new state with the ambient context and the old state with the outside
// context; the injected error restores old, the nil callback persists new.
func specSetRelationTargetsJoinsTransaction(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	repos, tx := newRepos(t)
	s := repos.Relationships
	if err := setTargetsAt(outside(t), s, "old"); err != nil {
		t.Fatalf("seed old: %v", err)
	}

	err := transact(tx, func(ctx context.Context) error {
		if err := setTargetsAt(ctx, s, "new"); err != nil {
			t.Fatalf("ambient set new: %v", err)
		}
		if got := targetIDsAt(t, ctx, s, "space", "child", "parent"); !equalStrings(got, []string{"new"}) {
			t.Fatalf("ambient read must see the new parent, got %v", got)
		}
		if got := targetIDsAt(t, outside(t), s, "space", "child", "parent"); !equalStrings(got, []string{"old"}) {
			t.Fatalf("outside read must still see the old parent until commit, got %v", got)
		}
		return errInjected
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("Transact must return the callback's error, got %v", err)
	}
	if got := targetIDsAt(t, outside(t), s, "space", "child", "parent"); !equalStrings(got, []string{"old"}) {
		t.Fatalf("rollback must restore the old parent, got %v", got)
	}

	if err := transact(tx, func(ctx context.Context) error { return setTargetsAt(ctx, s, "new") }); err != nil {
		t.Fatalf("commit shape: %v", err)
	}
	if got := targetIDsAt(t, outside(t), s, "space", "child", "parent"); !equalStrings(got, []string{"new"}) {
		t.Fatalf("commit must persist the new parent, got %v", got)
	}
}

// specSetRelationTargetsConflictRollsBackHostWork: the store's own conflict
// sentinel, propagated from the callback, undoes the host's EARLIER write in the
// same transaction — the D15 property (never a tuple beside a row that was
// rolled back, never a row beside a tuple that was refused).
func specSetRelationTargetsConflictRollsBackHostWork(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	repos, tx := newRepos(t)
	s := repos.Relationships
	createAt(t, outside(t), s, ct("space", "child", "owner", "space", "occupied"))

	hostRow := ct("doc", "d1", "owner", "user", "u1")
	err := transact(tx, func(ctx context.Context) error {
		createAt(t, ctx, s, hostRow)
		if !existsAt(t, ctx, s, "doc", "d1", "owner", "user", "u1") {
			t.Fatalf("ambient read must see the host's earlier write")
		}
		// "occupied" already holds owner on the resource: the desired parent
		// state is impossible under one-relation-per-subject → sdk.ErrConflict.
		return setTargetsAt(ctx, s, "occupied")
	})
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("Transact must surface the store's conflict, got %v", err)
	}
	if existsAt(t, outside(t), s, "doc", "d1", "owner", "user", "u1") {
		t.Fatalf("the host's earlier write survived the propagated conflict — atomicity split")
	}
	if got := targetIDsAt(t, outside(t), s, "space", "child", "parent"); len(got) != 0 {
		t.Fatalf("conflicting reconciliation must leave the parent relation untouched, got %v", got)
	}
	if !existsAt(t, outside(t), s, "space", "child", "owner", "space", "occupied") {
		t.Fatalf("the seeded owner tuple must survive")
	}
}

// specDeletesJoinTransaction exercises each delete method separately: the
// ambient read sees the deletion, the outside read still sees the seeded tuple,
// rollback leaves it in place, and a nil callback removes it.
func specDeletesJoinTransaction(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	type deleteCase struct {
		name    string
		seed    []relationship.CreateRelationship
		remove  func(ctx context.Context, s relationship.Storer) error
		present func(ctx context.Context, s relationship.Storer) bool
	}
	cases := []deleteCase{
		{
			name: "DeleteRelationshipTarget",
			seed: []relationship.CreateRelationship{ctUserset("doc", "d1", "viewer", "group", "eng", "member")},
			remove: func(ctx context.Context, s relationship.Storer) error {
				return s.DeleteRelationshipTarget(ctx, "doc", "d1", "viewer", relationship.SubjectRef{Type: "group", ID: "eng", Relation: "member"})
			},
			present: func(ctx context.Context, s relationship.Storer) bool {
				return len(targetIDsAt(t, ctx, s, "doc", "d1", "viewer")) == 1
			},
		},
		{
			name: "DeleteRelationship",
			seed: []relationship.CreateRelationship{ct("doc", "d1", "owner", "user", "u1")},
			remove: func(ctx context.Context, s relationship.Storer) error {
				return s.DeleteRelationship(ctx, "doc", "d1", "owner", "user", "u1")
			},
			present: func(ctx context.Context, s relationship.Storer) bool {
				return existsAt(t, ctx, s, "doc", "d1", "owner", "user", "u1")
			},
		},
		{
			name: "DeleteResourceRelationships",
			seed: []relationship.CreateRelationship{ct("doc", "d1", "owner", "user", "u1"), ct("doc", "d1", "viewer", "user", "u2")},
			remove: func(ctx context.Context, s relationship.Storer) error {
				return s.DeleteResourceRelationships(ctx, "doc", "d1")
			},
			present: func(ctx context.Context, s relationship.Storer) bool {
				return existsAt(t, ctx, s, "doc", "d1", "owner", "user", "u1") && existsAt(t, ctx, s, "doc", "d1", "viewer", "user", "u2")
			},
		},
		{
			name: "DeleteByResourceAndSubject",
			seed: []relationship.CreateRelationship{ct("doc", "d1", "owner", "user", "u1"), ct("doc", "d2", "owner", "user", "u1")},
			remove: func(ctx context.Context, s relationship.Storer) error {
				return s.DeleteByResourceAndSubject(ctx, "doc", "d1", "user", "u1")
			},
			present: func(ctx context.Context, s relationship.Storer) bool {
				// d2 must survive in every shape; the probe is d1 alone.
				if !existsAt(t, ctx, s, "doc", "d2", "owner", "user", "u1") {
					t.Fatalf("u1's d2 tuple must survive DeleteByResourceAndSubject(d1)")
				}
				return existsAt(t, ctx, s, "doc", "d1", "owner", "user", "u1")
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repos, tx := newRepos(t)
			s := repos.Relationships
			createAt(t, outside(t), s, tc.seed...)

			err := transact(tx, func(ctx context.Context) error {
				if err := tc.remove(ctx, s); err != nil {
					t.Fatalf("ambient delete: %v", err)
				}
				if tc.present(ctx, s) {
					t.Fatalf("ambient read must see the deletion")
				}
				if !tc.present(outside(t), s) {
					t.Fatalf("outside read must still see the seeded tuple(s) until commit")
				}
				return errInjected
			})
			if !errors.Is(err, errInjected) {
				t.Fatalf("Transact must return the callback's error, got %v", err)
			}
			if !tc.present(outside(t), s) {
				t.Fatalf("rollback must leave the seeded tuple(s) in place — the delete escaped the transaction")
			}

			if err := transact(tx, func(ctx context.Context) error { return tc.remove(ctx, s) }); err != nil {
				t.Fatalf("commit shape: %v", err)
			}
			if tc.present(outside(t), s) {
				t.Fatalf("commit must persist the deletion")
			}
		})
	}
}

// specRolesJoinTransaction exercises Assign and Unassign separately with the
// same four-way shape. Skipped when the roles kind is not wired.
func specRolesJoinTransaction(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	if repos, _ := newRepos(t); repos.Roles == nil {
		t.Skip("roles kind not wired")
	}

	t.Run("Assign", func(t *testing.T) {
		repos, tx := newRepos(t)
		s := repos.Roles
		err := transact(tx, func(ctx context.Context) error {
			assignAt(t, ctx, s, "user", "u1", "editor", "doc", "d1")
			if !hasExactAt(t, ctx, s, "user", "u1", "editor", "doc", "d1") {
				t.Fatalf("ambient read must see the uncommitted assignment")
			}
			if hasExactAt(t, outside(t), s, "user", "u1", "editor", "doc", "d1") {
				t.Fatalf("outside read must NOT see the uncommitted assignment")
			}
			return errInjected
		})
		if !errors.Is(err, errInjected) {
			t.Fatalf("Transact must return the callback's error, got %v", err)
		}
		if hasExactAt(t, outside(t), s, "user", "u1", "editor", "doc", "d1") {
			t.Fatalf("assignment survived the rollback — Assign did not join the transaction")
		}
		if err := transact(tx, func(ctx context.Context) error {
			assignAt(t, ctx, s, "user", "u1", "editor", "doc", "d1")
			return nil
		}); err != nil {
			t.Fatalf("commit shape: %v", err)
		}
		if !hasExactAt(t, outside(t), s, "user", "u1", "editor", "doc", "d1") {
			t.Fatalf("assignment absent after commit")
		}
	})

	t.Run("Unassign", func(t *testing.T) {
		repos, tx := newRepos(t)
		s := repos.Roles
		assignAt(t, outside(t), s, "user", "u1", "editor", "doc", "d1")
		err := transact(tx, func(ctx context.Context) error {
			if err := s.Unassign(ctx, "user", "u1", "editor", "doc", "d1"); err != nil {
				t.Fatalf("ambient Unassign: %v", err)
			}
			if hasExactAt(t, ctx, s, "user", "u1", "editor", "doc", "d1") {
				t.Fatalf("ambient read must see the unassignment")
			}
			if !hasExactAt(t, outside(t), s, "user", "u1", "editor", "doc", "d1") {
				t.Fatalf("outside read must still see the assignment until commit")
			}
			return errInjected
		})
		if !errors.Is(err, errInjected) {
			t.Fatalf("Transact must return the callback's error, got %v", err)
		}
		if !hasExactAt(t, outside(t), s, "user", "u1", "editor", "doc", "d1") {
			t.Fatalf("rollback must restore the assignment — Unassign did not join the transaction")
		}
		if err := transact(tx, func(ctx context.Context) error {
			return s.Unassign(ctx, "user", "u1", "editor", "doc", "d1")
		}); err != nil {
			t.Fatalf("commit shape: %v", err)
		}
		if hasExactAt(t, outside(t), s, "user", "u1", "editor", "doc", "d1") {
			t.Fatalf("assignment present after committed unassign")
		}
	})
}

// relationshipSnapshot is what every relationship read method reports under one
// context; ReadsJoinTransaction compares the ambient and outside snapshots.
type relationshipSnapshot struct {
	expandUnbounded, expandBounded bool
	batchUnbounded, batchBounded   map[string]bool
	exists                         bool
	targets                        []string
	count                          int
	bySubject                      []string
	bySubjectTotal                 int64
	byResource                     []string
	byResourceTotal                int64
	lookup, byTarget, descendants  []string
}

// walkSubject pages a subject's relationships ONE at a time under ctx, with a
// count on the first page, following the cursor to the end — so the connector's
// COUNT wrap, the keyset predicate, and the reverse probe (HasPrev on a cursored
// page) all run on the same querier as the listing itself.
func walkSubject(t *testing.T, ctx context.Context, s relationship.Storer, st, sid string) ([]string, int64) {
	t.Helper()
	var ids []string
	var total int64
	cursor := ""
	for page := 0; page < 10; page++ {
		p, err := s.ListRelationshipsBySubject(ctx, st, sid, relationship.SubjectRelationshipFilter{}, crud.ListRequest{Limit: 1, Cursor: cursor, WithCount: page == 0})
		if err != nil {
			t.Fatalf("ListRelationshipsBySubject page %d: %v", page, err)
		}
		if page == 0 {
			if p.Total == nil {
				t.Fatalf("WithCount must populate Total")
			}
			total = *p.Total
		}
		for _, it := range p.Items {
			ids = append(ids, it.ResourceID)
		}
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
	}
	sort.Strings(ids)
	return ids, total
}

func snapshotRelationships(t *testing.T, ctx context.Context, s relationship.Storer) relationshipSnapshot {
	t.Helper()
	var snap relationshipSnapshot
	var err error
	if snap.expandUnbounded, err = s.CheckRelationWithGroupExpansion(ctx, "doc", "d3", "viewer", "user", "u1", 0); err != nil {
		t.Fatalf("CheckRelationWithGroupExpansion(unbounded): %v", err)
	}
	if snap.expandBounded, err = s.CheckRelationWithGroupExpansion(ctx, "doc", "d3", "viewer", "user", "u1", 100); err != nil {
		t.Fatalf("CheckRelationWithGroupExpansion(bounded): %v", err)
	}
	if snap.batchUnbounded, err = s.CheckBatchDirect(ctx, "doc", []string{"d1", "d2", "d3"}, "viewer", "user", "u1", 0); err != nil {
		t.Fatalf("CheckBatchDirect(unbounded): %v", err)
	}
	if snap.batchBounded, err = s.CheckBatchDirect(ctx, "doc", []string{"d1", "d2", "d3"}, "viewer", "user", "u1", 100); err != nil {
		t.Fatalf("CheckBatchDirect(bounded): %v", err)
	}
	snap.exists = existsAt(t, ctx, s, "doc", "d2", "viewer", "user", "u1")
	snap.targets = targetIDsAt(t, ctx, s, "doc", "d2", "viewer")
	if snap.count, err = s.CountByResourceAndRelation(ctx, "doc", "d2", "viewer"); err != nil {
		t.Fatalf("CountByResourceAndRelation: %v", err)
	}
	snap.bySubject, snap.bySubjectTotal = walkSubject(t, ctx, s, "user", "u1")
	byRes, err := s.ListRelationshipsByResource(ctx, "doc", "d2", relationship.ResourceRelationshipFilter{}, crud.ListRequest{WithCount: true})
	if err != nil {
		t.Fatalf("ListRelationshipsByResource: %v", err)
	}
	if byRes.Total == nil {
		t.Fatalf("WithCount must populate Total")
	}
	snap.byResourceTotal = *byRes.Total
	for _, it := range byRes.Items {
		snap.byResource = append(snap.byResource, it.SubjectID)
	}
	if snap.lookup, err = s.LookupResourceIDs(ctx, "doc", []string{"viewer"}, "user", "u1", "", 100); err != nil {
		t.Fatalf("LookupResourceIDs: %v", err)
	}
	if snap.byTarget, err = s.LookupResourceIDsByRelationTarget(ctx, "space", "parent", "space", []string{"s1"}, "", 100); err != nil {
		t.Fatalf("LookupResourceIDsByRelationTarget: %v", err)
	}
	if snap.descendants, err = s.LookupDescendantResourceIDs(ctx, "space", []string{"parent"}, "space", []string{"s1"}, "", 100); err != nil {
		t.Fatalf("LookupDescendantResourceIDs: %v", err)
	}
	return snap
}

// assertRelationshipSnapshot compares one method at a time so a failure names
// the read that escaped (or failed to escape) the transaction.
func assertRelationshipSnapshot(t *testing.T, label string, got, want relationshipSnapshot) {
	t.Helper()
	if got.expandUnbounded != want.expandUnbounded {
		t.Errorf("%s CheckRelationWithGroupExpansion(unbounded) = %v, want %v", label, got.expandUnbounded, want.expandUnbounded)
	}
	if got.expandBounded != want.expandBounded {
		t.Errorf("%s CheckRelationWithGroupExpansion(bounded) = %v, want %v", label, got.expandBounded, want.expandBounded)
	}
	for _, id := range []string{"d1", "d2", "d3"} {
		if got.batchUnbounded[id] != want.batchUnbounded[id] {
			t.Errorf("%s CheckBatchDirect(unbounded)[%s] = %v, want %v", label, id, got.batchUnbounded[id], want.batchUnbounded[id])
		}
		if got.batchBounded[id] != want.batchBounded[id] {
			t.Errorf("%s CheckBatchDirect(bounded)[%s] = %v, want %v", label, id, got.batchBounded[id], want.batchBounded[id])
		}
	}
	if got.exists != want.exists {
		t.Errorf("%s CheckRelationExists = %v, want %v", label, got.exists, want.exists)
	}
	if !equalStrings(got.targets, want.targets) {
		t.Errorf("%s GetRelationTargets = %v, want %v", label, got.targets, want.targets)
	}
	if got.count != want.count {
		t.Errorf("%s CountByResourceAndRelation = %d, want %d", label, got.count, want.count)
	}
	if !equalStrings(got.bySubject, want.bySubject) || got.bySubjectTotal != want.bySubjectTotal {
		t.Errorf("%s ListRelationshipsBySubject = %v (total %d), want %v (total %d)", label, got.bySubject, got.bySubjectTotal, want.bySubject, want.bySubjectTotal)
	}
	if !equalStrings(got.byResource, want.byResource) || got.byResourceTotal != want.byResourceTotal {
		t.Errorf("%s ListRelationshipsByResource = %v (total %d), want %v (total %d)", label, got.byResource, got.byResourceTotal, want.byResource, want.byResourceTotal)
	}
	if !equalStrings(got.lookup, want.lookup) {
		t.Errorf("%s LookupResourceIDs = %v, want %v", label, got.lookup, want.lookup)
	}
	if !equalStrings(got.byTarget, want.byTarget) {
		t.Errorf("%s LookupResourceIDsByRelationTarget = %v, want %v", label, got.byTarget, want.byTarget)
	}
	if !equalStrings(got.descendants, want.descendants) {
		t.Errorf("%s LookupDescendantResourceIDs = %v, want %v", label, got.descendants, want.descendants)
	}
}

// roleSnapshot is what every role read method reports under one context.
type roleSnapshot struct {
	hasExact        bool
	bySubject       []string // resource IDs of u1's assignments
	byResource      []string // subject IDs assigned directly at doc:d2
	effective       []string // "subject/role/provenance" at doc:d2
	bySubjectTotal  int64
	byResourceTotal int64
	effectiveTotal  int64
}

func snapshotRoles(t *testing.T, ctx context.Context, s role.Storer) roleSnapshot {
	t.Helper()
	var snap roleSnapshot
	snap.hasExact = hasExactAt(t, ctx, s, "user", "u1", "editor", "doc", "d2")
	// One-at-a-time cursor walk with a count on the first page (see walkSubject).
	cursor := ""
	for page := 0; page < 10; page++ {
		p, err := s.ListBySubject(ctx, "user", "u1", crud.ListRequest{Limit: 1, Cursor: cursor, WithCount: page == 0})
		if err != nil {
			t.Fatalf("ListBySubject page %d: %v", page, err)
		}
		if page == 0 {
			if p.Total == nil {
				t.Fatalf("WithCount must populate Total")
			}
			snap.bySubjectTotal = *p.Total
		}
		for _, it := range p.Items {
			snap.bySubject = append(snap.bySubject, it.ResourceID)
		}
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
	}
	sort.Strings(snap.bySubject)
	byRes, err := s.ListByResource(ctx, "doc", "d2", crud.ListRequest{WithCount: true})
	if err != nil {
		t.Fatalf("ListByResource: %v", err)
	}
	snap.byResourceTotal = *byRes.Total
	for _, it := range byRes.Items {
		snap.byResource = append(snap.byResource, it.SubjectID)
	}
	eff, err := s.ListEffectiveByResource(ctx, "doc", "d2", crud.ListRequest{WithCount: true})
	if err != nil {
		t.Fatalf("ListEffectiveByResource: %v", err)
	}
	snap.effectiveTotal = *eff.Total
	for _, g := range eff.Items {
		snap.effective = append(snap.effective, g.SubjectID+"/"+g.Role+"/"+g.Provenance())
	}
	sort.Strings(snap.effective)
	return snap
}

func assertRoleSnapshot(t *testing.T, label string, got, want roleSnapshot) {
	t.Helper()
	if got.hasExact != want.hasExact {
		t.Errorf("%s HasExactRole = %v, want %v", label, got.hasExact, want.hasExact)
	}
	if !equalStrings(got.bySubject, want.bySubject) || got.bySubjectTotal != want.bySubjectTotal {
		t.Errorf("%s ListBySubject = %v (total %d), want %v (total %d)", label, got.bySubject, got.bySubjectTotal, want.bySubject, want.bySubjectTotal)
	}
	if !equalStrings(got.byResource, want.byResource) || got.byResourceTotal != want.byResourceTotal {
		t.Errorf("%s ListByResource = %v (total %d), want %v (total %d)", label, got.byResource, got.byResourceTotal, want.byResource, want.byResourceTotal)
	}
	if !equalStrings(got.effective, want.effective) || got.effectiveTotal != want.effectiveTotal {
		t.Errorf("%s ListEffectiveByResource = %v (total %d), want %v (total %d)", label, got.effective, got.effectiveTotal, want.effective, want.effectiveTotal)
	}
}

// specReadsJoinTransaction: after uncommitted relationship and role changes,
// every read method sees the ambient state under the ambient context and the
// committed state under the outside context — including the group-expansion
// CTEs (both budget branches), the connector-backed listings with a count and a
// cursor follow-up, and the three lookups. Roles are asserted only when wired.
func specReadsJoinTransaction(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	repos, tx := newRepos(t)
	s := repos.Relationships
	roles := repos.Roles

	// Committed baseline.
	createAt(t, outside(t), s,
		ct("doc", "d1", "viewer", "user", "u1"),
		ct("space", "s2", "parent", "space", "s1"),
	)
	committed := relationshipSnapshot{
		expandUnbounded: false, expandBounded: false,
		batchUnbounded: map[string]bool{"d1": true, "d2": false, "d3": false},
		batchBounded:   map[string]bool{"d1": true, "d2": false, "d3": false},
		exists:         false,
		targets:        nil,
		count:          0,
		bySubject:      []string{"d1"}, bySubjectTotal: 1,
		byResource: nil, byResourceTotal: 0,
		lookup:      []string{"d1"},
		byTarget:    []string{"s2"},
		descendants: []string{"s2"},
	}
	ambient := relationshipSnapshot{
		expandUnbounded: true, expandBounded: true,
		batchUnbounded: map[string]bool{"d1": true, "d2": true, "d3": true},
		batchBounded:   map[string]bool{"d1": true, "d2": true, "d3": true},
		exists:         true,
		targets:        []string{"u1"},
		count:          1,
		bySubject:      []string{"d1", "d2", "eng"}, bySubjectTotal: 3, // the group membership is u1's row too
		byResource: []string{"u1"}, byResourceTotal: 1,
		lookup:      []string{"d1", "d2", "d3"},
		byTarget:    []string{"s2", "s3"},
		descendants: []string{"s2", "s3"},
	}
	var committedRoles, ambientRoles roleSnapshot
	if roles != nil {
		assignAt(t, outside(t), roles, "user", "u1", "editor", "doc", "d1")
		assignAt(t, outside(t), roles, "user", "u3", "admin", "", "")
		committedRoles = roleSnapshot{
			hasExact:  false,
			bySubject: []string{"d1"}, bySubjectTotal: 1,
			byResource: nil, byResourceTotal: 0,
			effective: []string{"u3/admin/global"}, effectiveTotal: 1,
		}
		ambientRoles = roleSnapshot{
			hasExact:  true,
			bySubject: []string{"d1", "d2"}, bySubjectTotal: 2,
			byResource: []string{"u1"}, byResourceTotal: 1,
			effective: []string{"u1/editor/direct", "u3/admin/global"}, effectiveTotal: 2,
		}
	}
	assertRelationshipSnapshot(t, "committed baseline (outside)", snapshotRelationships(t, outside(t), s), committed)
	if roles != nil {
		assertRoleSnapshot(t, "committed baseline (outside)", snapshotRoles(t, outside(t), roles), committedRoles)
	}

	err := transact(tx, func(ctx context.Context) error {
		// Uncommitted changes: a direct viewer on d2, a group-expanded viewer on
		// d3 (u1 → group:eng#member → doc:d3#viewer), a second child of s1.
		createAt(t, ctx, s,
			ct("doc", "d2", "viewer", "user", "u1"),
			ct("group", "eng", "member", "user", "u1"),
			ctUserset("doc", "d3", "viewer", "group", "eng", "member"),
			ct("space", "s3", "parent", "space", "s1"),
		)
		if roles != nil {
			assignAt(t, ctx, roles, "user", "u1", "editor", "doc", "d2")
		}
		assertRelationshipSnapshot(t, "ambient", snapshotRelationships(t, ctx, s), ambient)
		assertRelationshipSnapshot(t, "outside (transaction open)", snapshotRelationships(t, outside(t), s), committed)
		if roles != nil {
			assertRoleSnapshot(t, "ambient", snapshotRoles(t, ctx, roles), ambientRoles)
			assertRoleSnapshot(t, "outside (transaction open)", snapshotRoles(t, outside(t), roles), committedRoles)
		}
		return errInjected
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("Transact must return the callback's error, got %v", err)
	}
	assertRelationshipSnapshot(t, "after rollback", snapshotRelationships(t, outside(t), s), committed)
	if roles != nil {
		assertRoleSnapshot(t, "after rollback", snapshotRoles(t, outside(t), roles), committedRoles)
	}
}

// specMutationRefusesAmbientTransaction: Apply and ApplyGuarded, called inside
// Transact, return mutation.ErrGuardedInsideTransaction (wrapping
// sdk.ErrInvalidInput) with a nil receipt, run neither the guard nor the
// semantic validator, and change no mutation state — checked while the outer
// transaction is still OPEN (so the assertion does not rely on the rollback to
// hide effects) and again after it. Returning the refusal from the callback
// also rolls back the host's preceding baseline write. Skipped when the
// mutation repository is not wired. Adapter-local tests additionally assert the
// revision anchors and receipt rows through SQL.
func specMutationRefusesAmbientTransaction(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	if repos, _ := newRepos(t); repos.Mutations == nil {
		t.Skip("mutation repository not wired")
	}
	for _, guarded := range []bool{false, true} {
		guarded := guarded
		name := "Apply"
		if guarded {
			name = "ApplyGuarded"
		}
		t.Run(name, func(t *testing.T) {
			repos, tx := newRepos(t)
			s, m := repos.Relationships, repos.Mutations

			// Committed: doc:M has an owner, so its anchor exists at revision 1
			// and a viewer grant would be a legitimate, non-blocked write.
			mustApply(t, m, grant(mustID(t), "M", "owner", "u1"))
			base := anchorRevision(t, m, resScope("M"))

			hostRow := ct("doc", "d1", "owner", "user", "u1")
			var validateRan, guardRan bool
			cmd := grant(mustID(t), "M", "viewer", "u2")
			err := transact(tx, func(ctx context.Context) error {
				createAt(t, ctx, s, hostRow)

				validate := func(mutation.Command) error { validateRan = true; return nil }
				var rcpt *mutation.Receipt
				var err error
				if guarded {
					guard := func(context.Context, mutation.StoreDecisionView) error { guardRan = true; return nil }
					rcpt, err = m.ApplyGuarded(ctx, cmd, guard, validate)
				} else {
					rcpt, err = m.Apply(ctx, cmd, validate)
				}
				if !errors.Is(err, mutation.ErrGuardedInsideTransaction) {
					t.Fatalf("%s inside Transact must refuse with ErrGuardedInsideTransaction, got %v", name, err)
				}
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Fatalf("the refusal must wrap sdk.ErrInvalidInput, got %v", err)
				}
				if rcpt != nil {
					t.Fatalf("the refusal must return a nil receipt, got %+v", rcpt)
				}
				if validateRan {
					t.Fatalf("the semantic validator must not run before the refusal")
				}
				if guardRan {
					t.Fatalf("the guard must not run before the refusal")
				}
				// While the transaction is still open: no mutation tuple on either
				// side; the host's baseline write is visible only ambiently.
				if existsAt(t, ctx, s, "doc", "M", "viewer", "user", "u2") {
					t.Fatalf("refused mutation wrote its tuple onto the ambient transaction")
				}
				if existsAt(t, outside(t), s, "doc", "M", "viewer", "user", "u2") {
					t.Fatalf("refused mutation wrote its tuple on its own connection (atomicity split)")
				}
				if !existsAt(t, ctx, s, "doc", "d1", "owner", "user", "u1") {
					t.Fatalf("the host's baseline write must remain visible ambiently")
				}
				return err
			})
			if !errors.Is(err, mutation.ErrGuardedInsideTransaction) {
				t.Fatalf("Transact must return the refusal, got %v", err)
			}
			if existsAt(t, outside(t), s, "doc", "d1", "owner", "user", "u1") {
				t.Fatalf("returning the refusal must roll back the host's preceding baseline write")
			}
			if existsAt(t, outside(t), s, "doc", "M", "viewer", "user", "u2") {
				t.Fatalf("refused mutation left its tuple behind")
			}
			if got := anchorRevision(t, m, resScope("M")); got != base {
				t.Fatalf("refused mutation moved the scope revision: %d → %d", base, got)
			}
			// The MutationID was never consumed: the same command applies cleanly
			// outside a transaction as a FIRST application, never a replay.
			rcpt := mustApply(t, m, cmd)
			if rcpt.Replayed {
				t.Fatalf("refused mutation persisted a receipt (a later apply replayed)")
			}
		})
	}
}

// specStandaloneUnchanged re-proves the two standalone SetRelationTargets
// properties — concurrent callers serialize to one winner, a conflict rolls the
// store's OWN transaction back — through the ambient-aware dispatch with no
// transaction in the context.
func specStandaloneUnchanged(t *testing.T, newRepos func(t *testing.T) (authorization.Repositories, crud.Transactor)) {
	t.Run("SetRelationTargetsConcurrentCallsDoNotUnion", func(t *testing.T) {
		repos, _ := newRepos(t)
		specSetRelationTargetsConcurrentCallsDoNotUnion(t, repos.Relationships)
	})
	t.Run("SetRelationTargetsConflictRollsBack", func(t *testing.T) {
		repos, _ := newRepos(t)
		specSetRelationTargetsConflictRollsBack(t, repos.Relationships)
	})
}
