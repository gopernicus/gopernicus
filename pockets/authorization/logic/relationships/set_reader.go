package relationships

import "context"

// RelationSetReader is an optional batch-read capability. The authorization
// engines do not require it: readers without it retain sequential batch checks.
// CheckBatch uses it to combine pending direct and Through reads while each
// request retains ordinary Check semantics and its own evaluation budget.
//
// Assert it on the same view whose semantics the caller needs. On a Reader
// returned by ForModel, every expansion edge and returned tuple must satisfy
// that ReadModel and preserve its transaction. A raw Storer retains raw fact
// semantics; it is not a fallback for a scoped reader missing this capability.
//
// The caller bounds the candidate count. Adapters may chunk requests to fit
// provider parameter limits, but must combine all chunks into a complete result
// or return an error. The capability does not promise a single physical query.
type RelationSetReader interface {
	// FilterRelation returns the distinct subset of resourceIDs carrying relation
	// for the concrete subject, directly or through exact userset expansion.
	// Output is sorted in ascending byte order; repeated input IDs appear once.
	// Empty input returns an empty result without datastore I/O.
	// maxExpansionStates bounds the shared subject expansion just as it does for
	// CheckRelationWithGroupExpansion: exceeding it returns
	// ErrExpansionBudgetExceeded, never a partial result. A nonpositive bound
	// means unbounded.
	FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error)

	// RelationTargetsFor returns the subjects holding relation on each input
	// resource. It agrees with GetRelationTargets on the same raw or scoped view,
	// including userset targets. IDs without targets are absent from the map;
	// repeated input IDs have one entry. Empty input returns an empty map without
	// datastore I/O.
	RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]RelationTarget, error)
}
