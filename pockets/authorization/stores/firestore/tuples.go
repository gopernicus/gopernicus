package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// putTuple and dropTuple own a relationship's row and subject claim together.
// Callers complete every read first: Firestore transactions reject reads after
// the first write. The claim enforces one relation per exact subject/resource.
// tupleID is the relationship document's id for a row — the KeyHash of the
// unique six-part tuple, in the SQL unique index's column order.
func tupleID(row relationshipDoc) string {
	return relationshipDocID(row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID, row.SubjectRelation)
}

// claimRefs returns the tuple row and its one-relation-per-subject claim.
func claimRefs(db *firestoredb.DB, row relationshipDoc) (tuple, subject *gcfs.DocumentRef) {
	return db.Doc(collectionRelationships, tupleID(row)),
		db.Doc(collectionSubjectClaims, subjectClaimDocID(row.ResourceType, row.ResourceID, row.SubjectType, row.SubjectID, row.SubjectRelation))
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

// putTuple writes the natural tuple and its subject claim. Deriving all keys
// here prevents a writer from storing a tuple that its listings cannot find.
// Create detects a concurrent duplicate instead of overwriting it.
func putTuple(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row relationshipDoc) error {
	id := tupleID(row)
	row.ResourceKey = resourceKey(row.ResourceType, row.ResourceID)
	row.SubjectKey = subjectKey(row.SubjectType, row.SubjectID, row.SubjectRelation)
	row.TupleKeyPrefix, row.TupleKeySuffix = tupleSortKeys(row)

	if err := w.Create(ctx, db.Doc(collectionRelationships, id), row); err != nil {
		return err
	}
	claim := subjectClaimDoc{Relation: row.Relation, TupleID: id}
	return w.Create(ctx, db.Doc(collectionSubjectClaims, subjectClaimDocID(row.ResourceType, row.ResourceID, row.SubjectType, row.SubjectID, row.SubjectRelation)), claim)
}

// replaceTuple atomically moves a tuple to another relation and updates its
// subject claim in place. The new relation changes the natural identity and
// sort position. The caller established the old tuple during the read phase.
func replaceTuple(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, old relationshipDoc, relation string) error {
	next := old
	next.Relation = relation
	next.ResourceKey = resourceKey(next.ResourceType, next.ResourceID)
	next.SubjectKey = subjectKey(next.SubjectType, next.SubjectID, next.SubjectRelation)
	next.TupleKeyPrefix, next.TupleKeySuffix = tupleSortKeys(next)
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
	claim := subjectClaimDoc{Relation: relation, TupleID: id}
	return w.Set(ctx, db.Doc(collectionSubjectClaims, subjectClaimDocID(next.ResourceType, next.ResourceID, next.SubjectType, next.SubjectID, next.SubjectRelation)), claim)
}

// dropTuple removes a tuple and its subject claim. Firestore Delete is
// idempotent, so a missing document is not an error.
func dropTuple(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row relationshipDoc) error {
	if err := w.Delete(ctx, db.Doc(collectionRelationships, tupleID(row))); err != nil {
		return err
	}
	return w.Delete(ctx, db.Doc(collectionSubjectClaims, subjectClaimDocID(row.ResourceType, row.ResourceID, row.SubjectType, row.SubjectID, row.SubjectRelation)))
}
