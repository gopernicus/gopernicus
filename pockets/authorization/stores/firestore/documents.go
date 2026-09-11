package firestore

// The document shapes. Field names and semantics are pinned in SCHEMA.md; they
// mirror the SQL column names so the two documentation trees stay shared, plus
// the derived keys Firestore needs where SQL has a multi-column index.
//
// Every field is always WRITTEN, never omitted: Firestore's OrderBy drops
// documents that lack the ordered field and an absent field is an absent index
// entry, so "empty" is an empty string, never an absent field.

// relationshipDoc is one iam_relationships document — the ReBAC tuple. Its
// document id is relationshipDocID (the unique tuple), so the tuple's own
// uniqueness needs no claim.
type relationshipDoc struct {
	ResourceType    string `firestore:"resource_type"`
	ResourceID      string `firestore:"resource_id"`
	Relation        string `firestore:"relation"`
	SubjectType     string `firestore:"subject_type"`
	SubjectID       string `firestore:"subject_id"`
	SubjectRelation string `firestore:"subject_relation"`

	// ResourceKey and SubjectKey are the derived equality keys: they collapse
	// the two/three-column filters of the SQL secondary indexes into one
	// clause, which is what keeps the expansion hops inside the DNF
	// disjunction budget. Identities, never projections, never sort keys.
	ResourceKey string `firestore:"resource_key"`
	SubjectKey  string `firestore:"subject_key"`

	// Split at the final component to keep each indexed value below Firestore's
	// 1500-byte limit while preserving the full tuple's natural byte order.
	TupleKeyPrefix string `firestore:"tuple_key_prefix"`
	TupleKeySuffix string `firestore:"tuple_key_suffix"`
}

// subjectClaimDoc is one iam_relationship_subjects document: the claim that
// reproduces idx_iam_relationships_unique_subject — ONE relation per exact
// SubjectRef per resource. TupleID names the relationship document that owns
// this claim, so a delete path can drop both without re-deriving the tuple.
type subjectClaimDoc struct {
	Relation string `firestore:"relation"`
	TupleID  string `firestore:"tuple_id"`
}

// roleDoc is one iam_roles document — an opaque role grant, global when both
// scope fields are empty. Its document id is roleDocID (the unique 5-tuple).
type roleDoc struct {
	SubjectType  string `firestore:"subject_type"`
	SubjectID    string `firestore:"subject_id"`
	Role         string `firestore:"role"`
	ResourceType string `firestore:"resource_type"`
	ResourceID   string `firestore:"resource_id"`

	// SubjectKey and ResourceKey are the derived equality keys (the global
	// scope hashes the EMPTY pair, so it is matchable rather than absent).
	SubjectKey  string `firestore:"subject_key"`
	ResourceKey string `firestore:"resource_key"`

	// RoleKey and GrantKey are the CONTRACTUAL sort keys, raw and byte-identical
	// to the SQL adapters' derived columns (SCHEMA.md §4.1). They are stored
	// because Firestore has no computed column, and PKOf echoes the stored
	// value rather than recomputing it in Go.
	RoleKey  string `firestore:"role_key"`
	GrantKey string `firestore:"grant_key"`
}
