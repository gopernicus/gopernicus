package firestore

import (
	"context"
	"sort"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

var _ mutation.StoreDecisionView = (*decisionView)(nil)

// decisionView is the transaction-bound mutation.StoreDecisionView a guard reads
// through. Every read runs on the mutation transaction's Reader — never on the
// client, never through the outer Service — and records the scope it touched
// together with the revision it observed, revision BEFORE rows, with a scope
// keeping its FIRST observed revision (the port's dependency-ordering contract).
//
// Commit-time validation is Firestore's own: an anchor READ inside the
// transaction is in its read set, so a concurrent bump aborts the commit and the
// vendor re-runs the whole attempt — guard included. The repository's explicit
// re-validation (validateDeps) is the parity mirror of the SQL siblings' locked
// re-read, not the primary mechanism.
//
// It is built INSIDE the transaction callback and never escapes it, so a retried
// attempt gets a fresh view with an empty dependency set rather than inheriting
// what a losing attempt observed.
type decisionView struct {
	db    *firestoredb.DB
	r     firestoredb.Reader
	deps  map[string]mutation.Dependency
	order []string
}

func newDecisionView(db *firestoredb.DB, r firestoredb.Reader) *decisionView {
	return &decisionView{db: db, r: r, deps: map[string]mutation.Dependency{}}
}

// CheckRelation is CheckRelationBounded in the unbounded mode.
func (v *decisionView) CheckRelation(ctx context.Context, scope mutation.ScopeKey, relation, subjectType, subjectID string) (bool, error) {
	return v.CheckRelationBounded(ctx, scope, relation, subjectType, subjectID, 0)
}

// CheckRelationBounded reports whether subjectType:subjectID holds relation on
// the resource named by scope, with exact-userset expansion bounded by
// maxExpansionStates (the read side's accounting: the seed counts, overflow is
// relationship.ErrExpansionBudgetExceeded, never an allow and never a truncated
// deny, and a non-positive bound is unbounded). It is the read-side
// CheckRelationWithGroupExpansion over the TRANSACTION's Reader — one walk, one
// snapshot — so the guard's decision and the rows it rests on are the same
// instant the commit validates.
//
// The resource scope is recorded BEFORE any row is read, and every resource the
// expansion traversed is recorded with it — INCLUDING when the expansion
// overflows its budget, since those scopes were still read. A membership edge
// lives under ITS resource's scope, so a concurrent revoke there bumps a
// revision this decision depends on. Recording the seed subject as a resource
// scope is the same harmless over-record the memstore and both SQL siblings
// make; UNDER-recording is the bug.
func (v *decisionView) CheckRelationBounded(ctx context.Context, scope mutation.ScopeKey, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := v.record(ctx, scope); err != nil {
		return false, err
	}
	reached, scopes, expandErr := expandScoped(ctx, v.db, v.r, subjectType, subjectID, maxExpansionStates)
	// The scopes are recorded BEFORE the error is returned, overflow included:
	// they are the resources this transaction actually READ, and a dependency
	// that is read but not recorded is a stale allow nothing catches at commit.
	// The memstore does the same (memstore/mutations.go — record every reached
	// scope, then report the overflow).
	for _, s := range scopes {
		if err := v.record(ctx, mutation.ScopeKey{Kind: mutation.ScopeResource, Type: s.resourceType, ID: s.resourceID}); err != nil {
			return false, err
		}
	}
	if expandErr != nil {
		return false, expandErr
	}
	return anyTupleWithSubject(ctx, v.db, v.r, scope.Type, scope.ID, relation, reached)
}

// RelationTargets records the scope's revision FIRST, then reads its targets
// through the transaction (record-before-read: the ordering that keeps a
// concurrently revoked edge from pairing with a post-revoke revision).
func (v *decisionView) RelationTargets(ctx context.Context, scope mutation.ScopeKey, relation string) ([]relationship.RelationTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := v.record(ctx, scope); err != nil {
		return nil, err
	}
	return relationTargets(ctx, v.db, v.r, scope.Type, scope.ID, relation)
}

// HasRole mirrors rolesvc.HasRole's EFFECTIVE semantics inside the boundary: an
// exact-scope match, plus the global fallback a resource-scoped query falls back
// to (a subject-scoped query has none). Each read is one Get on a deterministic
// document — the 5-tuple IS the id — so neither arm issues a query.
func (v *decisionView) HasRole(ctx context.Context, scope mutation.ScopeKey, roleName, subjectType, subjectID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := v.record(ctx, scope); err != nil {
		return false, err
	}
	var resourceType, resourceID string
	if scope.Kind == mutation.ScopeResource {
		resourceType, resourceID = scope.Type, scope.ID
	}
	ok, err := roleExists(ctx, v.db, v.r, subjectType, subjectID, roleName, resourceType, resourceID)
	if err != nil || ok {
		return ok, err
	}
	if scope.Kind != mutation.ScopeResource {
		return false, nil
	}
	// The exact-resource read missed, so the fallback reads the subject's GLOBAL
	// roles, which serialize into its SUBJECT scope. Record that scope regardless
	// of the fallback's answer: a concurrent global grant or revoke bumps its
	// revision and must invalidate the decision.
	if err := v.record(ctx, mutation.ScopeKey{Kind: mutation.ScopeSubject, Type: subjectType, ID: subjectID}); err != nil {
		return false, err
	}
	return roleExists(ctx, v.db, v.r, subjectType, subjectID, roleName, "", "")
}

// Dependencies returns the recorded scopes and revisions sorted by
// ScopeKey.Canonical().
func (v *decisionView) Dependencies() []mutation.Dependency {
	keys := append([]string(nil), v.order...)
	sort.Strings(keys)
	out := make([]mutation.Dependency, 0, len(keys))
	for _, k := range keys {
		out = append(out, v.deps[k])
	}
	return out
}

// record captures a scope dependency ONCE, reading its current revision through
// the transaction (an absent anchor is revision 0, never a materialized row).
// A scope recorded earlier keeps its FIRST revision: within one Firestore
// transaction every read observes one snapshot, so a second read could only
// return the same value anyway — keeping the first is the contract the SQL
// siblings state and costs nothing here.
func (v *decisionView) record(ctx context.Context, scope mutation.ScopeKey) error {
	key := scope.Canonical()
	if _, ok := v.deps[key]; ok {
		return nil
	}
	rev, err := readAnchor(ctx, v.db, v.r, scope)
	if err != nil {
		return err
	}
	v.deps[key] = mutation.Dependency{Scope: scope, Revision: rev}
	v.order = append(v.order, key)
	return nil
}
