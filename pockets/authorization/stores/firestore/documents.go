package firestore

import "time"

// The document shapes. Field names and semantics are pinned in SCHEMA.md; they
// mirror the SQL column names so the two documentation trees stay shared, plus
// the derived keys Firestore needs where SQL has a multi-column index.
//
// Every field is always WRITTEN, never omitted: Firestore's OrderBy drops
// documents that lack the ordered field and an absent field is an absent index
// entry, so "empty" is the empty string (or an explicit null through the
// connector's NullTime helpers), never an absent field. Timestamps are written
// through firestoredb.TruncateTime so a read-back compares equal.

// relationshipDoc is one iam_relationships document — the ReBAC tuple. Its
// document id is relationshipDocID (the unique tuple), so the tuple's own
// uniqueness needs no claim.
type relationshipDoc struct {
	RelationshipID  string    `firestore:"relationship_id"`
	ResourceType    string    `firestore:"resource_type"`
	ResourceID      string    `firestore:"resource_id"`
	Relation        string    `firestore:"relation"`
	SubjectType     string    `firestore:"subject_type"`
	SubjectID       string    `firestore:"subject_id"`
	SubjectRelation string    `firestore:"subject_relation"`
	CreatedAt       time.Time `firestore:"created_at"`

	// ResourceKey and SubjectKey are the derived equality keys: they collapse
	// the two/three-column filters of the SQL secondary indexes into one
	// clause, which is what keeps the expansion hops inside the DNF
	// disjunction budget. Identities, never projections, never sort keys.
	ResourceKey string `firestore:"resource_key"`
	SubjectKey  string `firestore:"subject_key"`
}

// subjectClaimDoc is one iam_relationship_subjects document: the claim that
// reproduces idx_iam_relationships_unique_subject — ONE relation per exact
// SubjectRef per resource. TupleID names the relationship document that owns
// this claim, so a delete path can drop both without re-deriving the tuple.
type subjectClaimDoc struct {
	Relation       string `firestore:"relation"`
	TupleID        string `firestore:"tuple_id"`
	RelationshipID string `firestore:"relationship_id"`
}

// idClaimDoc is one iam_relationship_ids document: the claim that reproduces
// the iam_relationships PRIMARY KEY, which a tuple-derived document id cannot
// carry (SCHEMA.md §5.3).
type idClaimDoc struct {
	RelationshipID string `firestore:"relationship_id"`
	TupleID        string `firestore:"tuple_id"`
}

// roleDoc is one iam_roles document — an opaque role grant, global when both
// scope fields are empty. Its document id is roleDocID (the unique 5-tuple).
type roleDoc struct {
	SubjectType  string    `firestore:"subject_type"`
	SubjectID    string    `firestore:"subject_id"`
	Role         string    `firestore:"role"`
	ResourceType string    `firestore:"resource_type"`
	ResourceID   string    `firestore:"resource_id"`
	CreatedAt    time.Time `firestore:"created_at"`

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

// scopeDoc is one iam_scopes document — a mutation scope's revision anchor. An
// ABSENT document reads as revision 0, the same contract an absent SQL row has,
// so a bare revision-0 anchor is never written merely to exist.
type scopeDoc struct {
	ScopeKind string `firestore:"scope_kind"`
	ScopeType string `firestore:"scope_type"`
	ScopeID   string `firestore:"scope_id"`
	Revision  int64  `firestore:"revision"`
}

// mutationDoc is one iam_mutations document — the durable receipt of an applied
// command. It is Created, never Set, so a concurrent double-apply loses at the
// server instead of overwriting a receipt. Replayed and SameRoleGrantRemains are
// computed annotations in every dialect and are deliberately absent here.
type mutationDoc struct {
	MutationID      string    `firestore:"mutation_id"`
	ScopeKind       string    `firestore:"scope_kind"`
	ScopeType       string    `firestore:"scope_type"`
	ScopeID         string    `firestore:"scope_id"`
	Operation       string    `firestore:"operation"`
	PayloadEncoding string    `firestore:"payload_encoding"`
	PayloadDigest   string    `firestore:"payload_digest"`
	Outcome         string    `firestore:"outcome"`
	Revision        int64     `firestore:"revision"`
	SchemaDigest    string    `firestore:"schema_digest"`
	CreatedAt       time.Time `firestore:"created_at"`

	// ExpiresAt mirrors the nullable SQL column: the default write path leaves
	// it null (written through firestoredb.NullTime — an explicit null value,
	// never an absent field) and nothing orders by it. It exists for the future
	// finite-retention posture the migration documents.
	ExpiresAt any `firestore:"expires_at"`
}
