package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// listRelationships maps the public natural tuple order to two bounded indexed
// fields. A full key can exceed Firestore's 1500-byte indexed-value limit; the
// prefix/suffix pair preserves its exact order without truncation or hashing.
func listRelationships(ctx context.Context, reader firestoredb.Reader, base gcfs.Query, req list.Request) (list.Page[relationshipDoc], error) {
	if req.Order.Field == "" {
		req.Order = relationships.DefaultOrder
	}
	if req.Order.Field != "tuple_key" {
		return list.Page[relationshipDoc]{}, fmt.Errorf("unknown order field %q: %w", req.Order.Field, sdk.ErrInvalidInput)
	}
	req.Order.Field = "tuple_key_prefix"
	query := firestoredb.ListQuery[relationshipDoc]{
		Query:        base,
		OrderFields:  map[string]list.OrderField{"tuple_key": {Column: "tuple_key_prefix"}},
		PK:           "tuple_key_suffix",
		Decode:       decodeRelationship,
		OrderValueOf: func(row relationshipDoc, _ string) any { return row.TupleKeyPrefix },
		PKOf:         func(row relationshipDoc) string { return row.TupleKeySuffix },
	}
	return firestoredb.List(ctx, reader, query, req)
}

// decodeRelationship reads stored tuple fields and derived listing keys.
func decodeRelationship(snap *gcfs.DocumentSnapshot) (relationshipDoc, error) {
	var row relationshipDoc
	if err := snap.DataTo(&row); err != nil {
		return relationshipDoc{}, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionRelationships, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// relationshipResourceID is the keyset lookup's id projection for a relationship
// document (idStream.idOf).
func relationshipResourceID(snap *gcfs.DocumentSnapshot) (string, error) {
	row, err := decodeRelationship(snap)
	if err != nil {
		return "", err
	}
	return row.ResourceID, nil
}

// toSubjectRelationship projects a row for ListRelationshipsBySubject.
func (r relationshipDoc) toSubjectRelationship() relationships.SubjectRelationship {
	return relationships.SubjectRelationship{
		ResourceType:    r.ResourceType,
		ResourceID:      r.ResourceID,
		Relation:        r.Relation,
		SubjectRelation: r.SubjectRelation,
	}
}

// toResourceRelationship projects a row for ListRelationshipsByResource.
func (r relationshipDoc) toResourceRelationship() relationships.ResourceRelationship {
	return relationships.ResourceRelationship{
		SubjectType:     r.SubjectType,
		SubjectID:       r.SubjectID,
		Relation:        r.Relation,
		SubjectRelation: r.SubjectRelation,
	}
}
