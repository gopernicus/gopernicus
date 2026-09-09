package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// maxDisjunctions is Firestore's query-complexity cap, and it is a property of
// the WHOLE query rather than of one clause: after expansion to disjunctive
// normal form a query may carry at most 30 disjunctions, and an `in` filter of N
// values contributes N of them. So every query this store issues carries AT MOST
// ONE `in`, of at most this many values, beside equality filters (which
// contribute one disjunct each and are counted in the separate 100-filter cap).
// The Go client validates none of it — the server answers InvalidArgument — so
// chunking is entirely this store's responsibility (milestone README, "vendor
// facts"; ruling R2).
const maxDisjunctions = 30

// maxQueryComplexity is Firestore's SECOND query cap, and the one a
// disjunction-only budget misses: the sum of FILTERS and SORT ORDERS, counted
// AFTER expansion to disjunctive normal form, may not exceed 100. Every
// disjunct carries the whole conjunction, so a query with four filters per
// disjunct is capped at twenty-four disjunctions, not thirty — thirty would be
// 120 filters and an InvalidArgument the Go client does not pre-validate.
const maxQueryComplexity = 100

// maxChunk returns the largest number of disjunctions one query may carry, given
// how many filters each disjunct contributes and how many sort orders the query
// adds. It is BOTH vendor caps in one place: the DNF disjunction cap and the
// filters-plus-orders cap. It never returns less than one, so a pathological
// filter count still produces a legal (if useless) chunk rather than an empty
// one that would silently drop values.
func maxChunk(filtersPerDisjunct, orders int) int {
	if filtersPerDisjunct < 1 {
		filtersPerDisjunct = 1
	}
	return min(maxDisjunctions, max((maxQueryComplexity-orders)/filtersPerDisjunct, 1))
}

// expand is the engine-side group walk (ruling R2), the Firestore port of the
// memstore's expandReachable: it returns the set of subject KEYS the concrete
// subject IS, transitively — the seed subjectKey(type, id, "") plus every exact
// userset (resource_type:resource_id#relation) reached through stored
// subject_relation state, so a member edge never yields an admin userset.
//
// It is BFS over the frontier, one batch of queries per hop: the frontier is
// chunked to maxDisjunctions and each chunk is ONE `subject_key in [...]` query,
// so the query count is bounded by hops × ceil(frontier/30) rather than by the
// number of frontier states. Cycle-safe via the seen set.
//
// budget is the port's maxExpansionStates and its rule is the memstore's,
// byte-for-byte: the instant a NEW distinct state would push the visited count
// PAST the budget the walk stops and answers
// relationship.ErrExpansionBudgetExceeded — never a truncated reachable set,
// never a deny. A non-positive budget is UNBOUNDED (the lookup and guard-path
// callers that opt out). Because the rule is set growth, the batched walk
// overflows on exactly the same graphs the memstore's one-node-at-a-time walk
// does: overflow happens if and only if the reachable set is larger than budget.
//
// It takes a Reader rather than a *DB on purpose: every caller runs it inside
// ONE firestoredb.ReadSnapshot (or, for the guarded-mutation path, inside the
// transaction's Reader), so all hops observe the same instant.
func expand(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, subjectType, subjectID string, budget int) (map[string]struct{}, error) {
	reached, _, err := expandScoped(ctx, db, r, subjectType, subjectID, budget)
	return reached, err
}

// subjectScope is one resource the walk traversed: the (type, id) pair whose
// membership edges produced a reached state. The guarded-mutation decision view
// turns each into a mutation dependency, because a concurrent revoke of one of
// those edges bumps THAT resource's revision and must invalidate the decision.
// The read-side callers have no use for it and ignore it.
type subjectScope struct {
	resourceType string
	resourceID   string
}

// expandScoped is [expand] plus the traversed resource scopes, in first-seen
// order: the seed (the subject itself, read as a resource scope — the same
// harmless over-record the memstore and both SQL siblings make) followed by
// every resource whose row contributed a reachable userset state. Under-recording
// is the bug it exists to prevent: a dependency the guard actually read but did
// not record is a stale allow nothing would catch at commit.
func expandScoped(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, subjectType, subjectID string, budget int) (map[string]struct{}, []subjectScope, error) {
	seed := subjectKey(subjectType, subjectID, "")
	seen := map[string]struct{}{seed: {}}
	frontier := []string{seed}

	scopes := []subjectScope{{resourceType: subjectType, resourceID: subjectID}}
	scopeSeen := map[subjectScope]struct{}{scopes[0]: {}}

	for len(frontier) > 0 {
		var next []string
		for _, chunk := range chunkStrings(frontier, maxDisjunctions) {
			q := db.Collection(collectionRelationships).Where("subject_key", "in", chunk)
			rows, err := queryRelationships(ctx, r, q)
			if err != nil {
				return nil, nil, err
			}
			for _, row := range rows {
				scope := subjectScope{resourceType: row.ResourceType, resourceID: row.ResourceID}
				if _, ok := scopeSeen[scope]; !ok {
					scopeSeen[scope] = struct{}{}
					scopes = append(scopes, scope)
				}
				state := subjectKey(row.ResourceType, row.ResourceID, row.Relation)
				if _, ok := seen[state]; ok {
					continue
				}
				if budget > 0 && len(seen) >= budget {
					// Adding this state would push the distinct count past the
					// budget: the call is indeterminate. Never a truncated set.
					return nil, nil, relationship.ErrExpansionBudgetExceeded
				}
				seen[state] = struct{}{}
				next = append(next, state)
			}
		}
		frontier = next
	}
	return seen, scopes, nil
}

// anyTupleWithSubject reports whether the resource+relation carries a tuple whose
// subject key is any of the reached states — the second half of an expanded
// check. The reached set is chunked to maxDisjunctions and each chunk is one
// Limit(1) query, so a hit costs one document read rather than the whole target
// list.
func anyTupleWithSubject(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID, relation string, reached map[string]struct{}) (bool, error) {
	keys := sortedKeys(reached)
	for _, chunk := range chunkStrings(keys, maxDisjunctions) {
		q := db.Collection(collectionRelationships).
			Where("resource_key", "==", resourceKey(resourceType, resourceID)).
			Where("relation", "==", relation).
			Where("subject_key", "in", chunk).
			Limit(1)
		rows, err := queryRelationships(ctx, r, q)
		if err != nil {
			return false, err
		}
		if len(rows) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// relationTargets is the one relation-targets read, shared by the public
// GetRelationTargets and (from A4c) the mutation repository's transaction-bound
// decision view: same query, same mapping, same order. Userset targets are
// returned AS STORED — the subject_relation is the target's Relation.
func relationTargets(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	q := db.Collection(collectionRelationships).
		Where("resource_key", "==", resourceKey(resourceType, resourceID)).
		Where("relation", "==", relation)
	rows, err := queryRelationships(ctx, r, q)
	if err != nil {
		return nil, err
	}
	var out []relationship.RelationTarget
	for _, row := range rows {
		out = append(out, relationship.RelationTarget{Type: row.SubjectType, ID: row.SubjectID, Relation: row.SubjectRelation})
	}
	return out, nil
}

// scanCandidates reads the tuples of resourceType+relation whose resource_id is
// any of ids, chunked to maxDisjunctions, and hands each row to visit. It is the
// one candidate-set reader the three set reads share (CheckBatchDirect,
// FilterRelation, RelationTargetsFor), which is what keeps them from drifting —
// and what makes "one query per CHUNK, never one per candidate" a property of
// one function.
//
// ids must already be the distinct, byte-sorted candidate set (distinctSortedIDs).
func scanCandidates(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType string, ids []string, relation string, visit func(relationshipDoc)) error {
	for _, chunk := range chunkStrings(ids, maxDisjunctions) {
		q := db.Collection(collectionRelationships).
			Where("resource_type", "==", resourceType).
			Where("relation", "==", relation).
			Where("resource_id", "in", chunk)
		rows, err := queryRelationships(ctx, r, q)
		if err != nil {
			return err
		}
		for _, row := range rows {
			visit(row)
		}
	}
	return nil
}

// queryRelationships runs one query and decodes every document it returns. The
// iterator is always stopped, iterator.Done is consumed as the loop terminator,
// and every other Next error goes through MapError HERE, at the iteration
// boundary — the missing-index FAILED_PRECONDITION and a transaction's
// read-after-write refusal both arrive nowhere else (connector README).
func queryRelationships(ctx context.Context, r firestoredb.Reader, q gcfs.Query) ([]relationshipDoc, error) {
	it := r.Documents(ctx, q)
	defer it.Stop()

	var out []relationshipDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		var row relationshipDoc
		if err := snap.DataTo(&row); err != nil {
			return nil, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionRelationships, err, sdk.ErrInvalidInput)
		}
		out = append(out, row)
	}
}

// chunkStrings splits in into consecutive runs of at most size, preserving
// order. An empty input yields no chunks, so a caller loops zero times and
// issues no query at all.
func chunkStrings(in []string, size int) [][]string {
	var out [][]string
	for start := 0; start < len(in); start += size {
		out = append(out, in[start:min(start+size, len(in))])
	}
	return out
}

// distinctSortedIDs folds a candidate list to the DISTINCT ids in byte order —
// the shape FilterRelation's output contract demands and the shape that makes a
// duplicated input id cost nothing in every set read.
func distinctSortedIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

// sortedKeys is distinctSortedIDs for a set: the chunk boundaries of an
// expansion result must not depend on Go's map iteration order, or two runs of
// the same query would bill different reads and a failure would be
// irreproducible.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
