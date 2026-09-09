package firestore

import (
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// listRelationships is the ListQuery both relationship listings share. Only the
// base query differs between them: the ORDER contract, the keyset tiebreak, and
// the decoding are one definition, so the two listings cannot drift.
//
// The order allow-list and default come straight from the pocket
// (relationship.OrderFields, relationship.DefaultOrder) — the same values the
// two SQL adapters pass — and PK is relationship_id, so equal created_at values
// (a bulk create stamps ONE timestamp for the whole batch) break the tie on the
// contractual column rather than on the hashed document name. OrderValueOf
// returns the stored time.Time, which Firestore hands back as UTC at microsecond
// precision, exactly what TruncateTime wrote.
//
// Neither listing declares a PostFilter, so a non-blank Search is refused with
// sdk.ErrInvalidInput by the connector's List rather than answered with an
// unfiltered page (ruling R4: the relationship listings have no SearchFields).
func listRelationships(base gcfs.Query) firestoredb.ListQuery[relationshipDoc] {
	return firestoredb.ListQuery[relationshipDoc]{
		Query:        base,
		OrderFields:  relationship.OrderFields,
		DefaultOrder: relationship.DefaultOrder,
		PK:           "relationship_id",
		Decode:       decodeRelationship,
		OrderValueOf: func(row relationshipDoc, _ string) any { return row.CreatedAt },
		PKOf:         func(row relationshipDoc) string { return row.RelationshipID },
	}
}

// decodeRelationship turns one document snapshot into a row. Firestore has no
// struct scan that also sees the document id, so every list writes this; the
// relationship_id is a stored FIELD here (the document id is the tuple hash),
// which is why nothing reads snap.Ref.ID.
func decodeRelationship(snap *gcfs.DocumentSnapshot) (relationshipDoc, error) {
	var row relationshipDoc
	if err := snap.DataTo(&row); err != nil {
		return relationshipDoc{}, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionRelationships, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// toSubjectRelationship projects a row for ListRelationshipsBySubject.
func (r relationshipDoc) toSubjectRelationship() relationship.SubjectRelationship {
	return relationship.SubjectRelationship{
		ID:           r.RelationshipID,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		Relation:     r.Relation,
		CreatedAt:    r.CreatedAt,
	}
}

// toResourceRelationship projects a row for ListRelationshipsByResource.
func (r relationshipDoc) toResourceRelationship() relationship.ResourceRelationship {
	return relationship.ResourceRelationship{
		ID:          r.RelationshipID,
		SubjectType: r.SubjectType,
		SubjectID:   r.SubjectID,
		Relation:    r.Relation,
		CreatedAt:   r.CreatedAt,
	}
}
