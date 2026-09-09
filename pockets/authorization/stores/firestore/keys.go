package firestore

import (
	"strings"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// The collections. Names mirror the SQL table names (milestone convention) so
// the two documentation trees and the operator vocabulary stay shared; the two
// claim collections have no SQL table because they reproduce an INDEX (see
// SCHEMA.md §5). All are top-level collections — no subcollections, so every
// index in the manifest is COLLECTION scope.
const (
	collectionRelationships = "iam_relationships"
	collectionSubjectClaims = "iam_relationship_subjects"
	collectionIDClaims      = "iam_relationship_ids"
	collectionRoles         = "iam_roles"
	collectionScopes        = "iam_scopes"
	collectionMutations     = "iam_mutations"
)

// keySeparator is the byte the SQL adapters' derived sort keys join their
// components with: SQLite's char(1) and postgres's chr(1) both emit U+0001.
// role_key and grant_key are CONTRACTUAL sort keys, so their bytes must match
// the SQL families exactly — see SCHEMA.md §4.1 and keys_test.go.
const keySeparator = "\x01"

// relationshipDocID is the unique-tuple document id: the six components of
// idx_iam_relationships_unique_tuple, in the index's column order. It reproduces
// that unique index (R3a) — a duplicate Create fails AlreadyExists at the
// server.
func relationshipDocID(resourceType, resourceID, relation, subjectType, subjectID, subjectRelation string) string {
	return firestoredb.KeyHash(resourceType, resourceID, relation, subjectType, subjectID, subjectRelation)
}

// subjectClaimDocID is the one-relation-per-subject claim id: the five
// components of idx_iam_relationships_unique_subject, in the index's column
// order (the tuple WITHOUT relation). It reproduces that unique index.
func subjectClaimDocID(resourceType, resourceID, subjectType, subjectID, subjectRelation string) string {
	return firestoredb.KeyHash(resourceType, resourceID, subjectType, subjectID, subjectRelation)
}

// idClaimDocID is the relationship_id claim id, reproducing the SQL PRIMARY KEY
// that a deterministic tuple id cannot carry (SCHEMA.md §5.3).
func idClaimDocID(relationshipID string) string {
	return firestoredb.KeyHash(relationshipID)
}

// resourceKey is the equality key for (resource_type, resource_id) in both the
// relationship and the role collections. A role's GLOBAL scope is the empty
// pair, which hashes to a real, matchable value rather than an absent field.
func resourceKey(resourceType, resourceID string) string {
	return firestoredb.KeyHash(resourceType, resourceID)
}

// subjectKey is the relationship subject's equality key and the expansion
// frontier's state: the EXACT SubjectRef, subject_relation included, so
// group:eng#member and group:eng#admin are distinct states.
func subjectKey(subjectType, subjectID, subjectRelation string) string {
	return firestoredb.KeyHash(subjectType, subjectID, subjectRelation)
}

// roleSubjectKey is the ROLES kind's subject equality key. Roles carry no
// subject relation, so it is two parts where subjectKey is three; KeyHash is
// length-prefixed, so the two can never alias even before the collections do.
func roleSubjectKey(subjectType, subjectID string) string {
	return firestoredb.KeyHash(subjectType, subjectID)
}

// roleDocID is the unique-5-tuple document id: the components of
// idx_iam_roles_unique in the index's column order.
func roleDocID(subjectType, subjectID, roleName, resourceType, resourceID string) string {
	return firestoredb.KeyHash(subjectType, subjectID, roleName, resourceType, resourceID)
}

// scopeDocID is the revision anchor's id: the iam_scopes PRIMARY KEY. An ABSENT
// document reads as revision 0, exactly as an absent SQL row does.
func scopeDocID(scopeKind, scopeType, scopeID string) string {
	return firestoredb.KeyHash(scopeKind, scopeType, scopeID)
}

// mutationDocID is the receipt's id: the iam_mutations PRIMARY KEY.
func mutationDocID(mutationID string) string {
	return firestoredb.KeyHash(mutationID)
}

// roleKey is the roles listings' keyset tiebreak, reproducing the SQL adapters'
// derived role_key column BYTE-FOR-BYTE: the 5-tuple joined by U+0001 (turso's
// `subject_type || char(1) || … || resource_id`, pgx's chr(1) twin). It is a
// SORT key, so it is raw — never a hash — and Firestore's UTF-8 byte order is
// the same order SQLite's BINARY collation and postgres's COLLATE "C" give.
//
// Unlike SQL, Firestore has no computed column: the value is written as a field
// and echoed back by PKOf, never recomputed from a decoded row.
func roleKey(subjectType, subjectID, roleName, resourceType, resourceID string) string {
	return strings.Join([]string{subjectType, subjectID, roleName, resourceType, resourceID}, keySeparator)
}

// grantKey is the effective-role listing's ordering and keyset key, reproducing
// the SQL adapters' derived grant_key column byte-for-byte: the
// (subject_type, subject_id, role) triple joined by U+0001. The effective set is
// de-duplicated by that triple, which is why created_at cannot order it.
func grantKey(subjectType, subjectID, roleName string) string {
	return strings.Join([]string{subjectType, subjectID, roleName}, keySeparator)
}
