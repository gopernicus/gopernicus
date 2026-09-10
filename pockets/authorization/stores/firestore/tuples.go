package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// putTuple and dropTuple are the ONLY two writers of a relationship tuple's
// document set. A tuple owns THREE documents — the row in iam_relationships plus
// the two claims that reproduce the SQL constraints a deterministic document id
// cannot carry (SCHEMA.md §5.2, §5.3) — and the failure mode this pair exists to
// prevent is a future write path that updates the row and forgets a claim,
// leaving the uniqueness invariant enforced by nothing. Every write path goes
// through them; a test asserts no other code touches the claim collections.
//
// Neither helper reads. That is deliberate and load-bearing: a Firestore
// transaction refuses any read issued after its first write, so the CALLER does
// the complete read phase (tuple + both claims, for every row in the batch) and
// only then calls these. A2c's CreateRelationships / SetRelationTargets / delete
// family own that phase; A2a/A2b use putTuple only to seed fixtures.

// tupleID is the relationship document's id for a row — the KeyHash of the
// unique six-part tuple, in the SQL unique index's column order.
func tupleID(row relationshipDoc) string {
	return relationshipDocID(row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID, row.SubjectRelation)
}

// claimRefs returns the three document references a tuple owns: the row itself,
// its subject claim, and its relationship_id claim.
func claimRefs(db *firestoredb.DB, row relationshipDoc) (tuple, subject, id *gcfs.DocumentRef) {
	return db.Doc(collectionRelationships, tupleID(row)),
		db.Doc(collectionSubjectClaims, subjectClaimDocID(row.ResourceType, row.ResourceID, row.SubjectType, row.SubjectID, row.SubjectRelation)),
		db.Doc(collectionIDClaims, idClaimDocID(row.RelationshipID))
}

// decodeSubjectClaim decodes one subject-claim document. It lives here rather
// than with the caller for the same reason putTuple and dropTuple do: every
// reference to the claim collections belongs to this file, so the compiler and
// TestClaimCollectionsAreOwnedByTuplesOnly can both see the whole set.
//
// The caller establishes PRESENCE (docRefs.exists); this only decodes, so it
// reports no second "found" boolean of its own.
func decodeSubjectClaim(snap *gcfs.DocumentSnapshot) (subjectClaimDoc, error) {
	var claim subjectClaimDoc
	if err := snap.DataTo(&claim); err != nil {
		return subjectClaimDoc{}, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionSubjectClaims, err, sdk.ErrInvalidInput)
	}
	return claim, nil
}

// putTuple writes one relationship tuple and both of its claims through w. The
// derived keys and the truncated timestamp are computed HERE rather than by the
// caller, so a row can never reach the collection with a resource_key that does
// not match its own fields.
//
// The verb is Create, never Set (SCHEMA.md §5.1): the document id IS the unique
// tuple, so a duplicate loses at the server instead of overwriting a row whose
// relationship_id and created_at the port promises to preserve. The caller has
// already established, in the transaction's read phase, that the row is missing.
func putTuple(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row relationshipDoc) error {
	id := tupleID(row)
	row.ResourceKey = resourceKey(row.ResourceType, row.ResourceID)
	row.SubjectKey = subjectKey(row.SubjectType, row.SubjectID, row.SubjectRelation)
	row.CreatedAt = firestoredb.TruncateTime(row.CreatedAt)

	if err := w.Create(ctx, db.Doc(collectionRelationships, id), row); err != nil {
		return err
	}
	claim := subjectClaimDoc{Relation: row.Relation, TupleID: id, RelationshipID: row.RelationshipID}
	if err := w.Create(ctx, db.Doc(collectionSubjectClaims, subjectClaimDocID(row.ResourceType, row.ResourceID, row.SubjectType, row.SubjectID, row.SubjectRelation)), claim); err != nil {
		return err
	}
	return w.Create(ctx, db.Doc(collectionIDClaims, idClaimDocID(row.RelationshipID)), idClaimDoc{RelationshipID: row.RelationshipID, TupleID: id})
}

// replaceTuple moves an existing tuple to a NEW relation in place — the
// mutation path's OpReplace, whose SQL siblings say `UPDATE iam_relationships
// SET relation = ?`. Firestore has no such update available here, because the
// relation is part of the ROW's document id, so the row moves; the two claims do
// NOT, because neither id carries the relation.
//
// The identity the SQL UPDATE preserves is preserved here too: the new row keeps
// the old row's relationship_id and created_at, so a replace is invisible to the
// listings' order and to the primary-key claim. The claims are Set rather than
// Created because their documents already exist and only their contents move
// (the subject claim's relation, and both claims' tuple_id) — a Create would
// fail AlreadyExists on the store's own consistent state.
//
// It performs no read, for the same reason putTuple and dropTuple do not: the
// caller established the old row inside the transaction's read phase.
func replaceTuple(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, old relationshipDoc, relation string) error {
	next := old
	next.Relation = relation
	next.ResourceKey = resourceKey(next.ResourceType, next.ResourceID)
	next.SubjectKey = subjectKey(next.SubjectType, next.SubjectID, next.SubjectRelation)
	next.CreatedAt = firestoredb.TruncateTime(next.CreatedAt)
	id := tupleID(next)

	if id == tupleID(old) {
		// The relation did not change, so the row does not move and there is
		// nothing to write. Without the guard this would queue a Delete and a
		// Create on the SAME document in one commit, which is not a rename —
		// it is a document whose final state depends on write ordering. The
		// evaluator already skips a same-relation row (mutations_eval.go
		// replace), so this is the structural backstop for any future caller.
		return nil
	}

	if err := w.Delete(ctx, db.Doc(collectionRelationships, tupleID(old))); err != nil {
		return err
	}
	if err := w.Create(ctx, db.Doc(collectionRelationships, id), next); err != nil {
		return err
	}
	claim := subjectClaimDoc{Relation: relation, TupleID: id, RelationshipID: next.RelationshipID}
	if err := w.Set(ctx, db.Doc(collectionSubjectClaims, subjectClaimDocID(next.ResourceType, next.ResourceID, next.SubjectType, next.SubjectID, next.SubjectRelation)), claim); err != nil {
		return err
	}
	return w.Set(ctx, db.Doc(collectionIDClaims, idClaimDocID(next.RelationshipID)), idClaimDoc{RelationshipID: next.RelationshipID, TupleID: id})
}

// dropTuple removes one relationship tuple and both of its claims through w.
// Delete is idempotent in Firestore (a missing document is not an error), which
// is exactly the idempotency every delete on this port promises.
func dropTuple(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row relationshipDoc) error {
	if err := w.Delete(ctx, db.Doc(collectionRelationships, tupleID(row))); err != nil {
		return err
	}
	if err := w.Delete(ctx, db.Doc(collectionSubjectClaims, subjectClaimDocID(row.ResourceType, row.ResourceID, row.SubjectType, row.SubjectID, row.SubjectRelation))); err != nil {
		return err
	}
	return w.Delete(ctx, db.Doc(collectionIDClaims, idClaimDocID(row.RelationshipID)))
}
