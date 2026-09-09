package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// This file OWNS the iam_roles collection, the way tuples.go owns a relationship
// tuple's three documents: putRole and dropRole are its only writers, roleRef
// and rolesQuery are the only two places a role document or a role query is
// addressed, and decodeRole is the only decoder. Every other file — the port
// methods in roles.go, the effective listing in effective.go, and (from A4) the
// mutation path — reaches iam_roles through these, so a future write path cannot
// store a row whose derived keys disagree with its own fields.
// TestRoleCollectionIsOwnedByGrantsOnly keeps that true.
//
// A role grant is ONE document (no claim beside it): its document id IS the
// unique 5-tuple (SCHEMA.md §3.2, §5.4), so uniqueness is the server's answer
// rather than a second document's.

// roleRef is the role grant's document reference — the KeyHash of the unique
// 5-tuple, in the SQL unique index's column order. It is deterministic, so a
// caller can read the exact grant without a query and a duplicate Create loses
// at the server.
func roleRef(db *firestoredb.DB, subjectType, subjectID, roleName, resourceType, resourceID string) *gcfs.DocumentRef {
	return db.Doc(collectionRoles, roleDocID(subjectType, subjectID, roleName, resourceType, resourceID))
}

// rolesQuery is the collection-wide query root every role read starts from.
func rolesQuery(db *firestoredb.DB) gcfs.Query {
	return db.Collection(collectionRoles).Query
}

// newRoleDoc builds the document for an assignment, deriving the equality keys
// and both contractual sort keys from the row's OWN fields. The GLOBAL scope is
// the empty (resource_type, resource_id) pair, exactly as the SQL families store
// it — empty strings, never null or absent — so resourceKey("", "") is a real,
// matchable value and a global grant participates in every index.
func newRoleDoc(a role.Assignment) roleDoc {
	return roleDoc{
		SubjectType:  a.SubjectType,
		SubjectID:    a.SubjectID,
		Role:         a.Role,
		ResourceType: a.ResourceType,
		ResourceID:   a.ResourceID,
		CreatedAt:    a.CreatedAt,
	}
}

// toAssignment projects a stored row back to the port's type.
func (r roleDoc) toAssignment() role.Assignment {
	return role.Assignment{
		SubjectType:  r.SubjectType,
		SubjectID:    r.SubjectID,
		Role:         r.Role,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		CreatedAt:    r.CreatedAt,
	}
}

// putRole writes one role grant through w. The derived keys and the truncated
// timestamp are computed HERE rather than by the caller, so a row can never
// reach the collection with a role_key that does not match its own fields.
//
// The verb is Create, never Set (SCHEMA.md §5.4): the document id IS the unique
// 5-tuple, so a duplicate loses at the server instead of overwriting a row whose
// created_at the port promises to retain. The caller has already established, in
// the transaction's read phase, that the grant is absent.
func putRole(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row roleDoc) error {
	row.SubjectKey = roleSubjectKey(row.SubjectType, row.SubjectID)
	row.ResourceKey = resourceKey(row.ResourceType, row.ResourceID)
	row.RoleKey = roleKey(row.SubjectType, row.SubjectID, row.Role, row.ResourceType, row.ResourceID)
	row.GrantKey = grantKey(row.SubjectType, row.SubjectID, row.Role)
	row.CreatedAt = firestoredb.TruncateTime(row.CreatedAt)

	return w.Create(ctx, roleRef(db, row.SubjectType, row.SubjectID, row.Role, row.ResourceType, row.ResourceID), row)
}

// dropRole removes one role grant through w. Delete is idempotent in Firestore
// (a missing document is not an error) and NO precondition is attached, which is
// exactly the idempotency Unassign promises: removing an absent assignment is
// nil, never a port error.
func dropRole(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	return w.Delete(ctx, roleRef(db, subjectType, subjectID, roleName, resourceType, resourceID))
}

// decodeRole turns one document snapshot into a row. The natural key lives in
// stored FIELDS (the document id is the hash of the 5-tuple), so nothing reads
// snap.Ref.ID.
func decodeRole(snap *gcfs.DocumentSnapshot) (roleDoc, error) {
	var row roleDoc
	if err := snap.DataTo(&row); err != nil {
		return roleDoc{}, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionRoles, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// roleResourceID is the keyset lookup's id projection for a role document — the
// roles-kind twin of relationshipResourceID.
func roleResourceID(snap *gcfs.DocumentSnapshot) (string, error) {
	row, err := decodeRole(snap)
	if err != nil {
		return "", err
	}
	return row.ResourceID, nil
}

// listRoles is the ListQuery both RAW role listings share. Only the base query
// differs between them, so the ORDER contract, the keyset tiebreak, and the
// decoding are one definition and the two listings cannot drift.
//
// The order allow-list and default come straight from the pocket
// (role.OrderFields, role.DefaultOrder) — the same values both SQL adapters
// pass — and PK is role_key, the derived 5-tuple sort key iam_roles has instead
// of a surrogate id. Equal created_at values therefore break the tie on the
// contractual key rather than on the hashed document name, byte-for-byte as the
// SQL families do (SCHEMA.md §4.1). PKOf echoes the STORED role_key; it is never
// recomputed in Go, which is the same invariant the SQL stores reach from the
// other side by scanning the computed column.
//
// No PostFilter is declared, so a non-blank Search is refused with
// sdk.ErrInvalidInput by the connector's List rather than answered with an
// unfiltered page (ruling R4: the role listings have no SearchFields).
func listRoles(base gcfs.Query) firestoredb.ListQuery[roleDoc] {
	return firestoredb.ListQuery[roleDoc]{
		Query:        base,
		OrderFields:  role.OrderFields,
		DefaultOrder: role.DefaultOrder,
		PK:           "role_key",
		Decode:       decodeRole,
		OrderValueOf: func(row roleDoc, _ string) any { return row.CreatedAt },
		PKOf:         func(row roleDoc) string { return row.RoleKey },
	}
}

// effectiveOrderField resolves an effective-listing order request against
// role.EffectiveOrderFields (grant_key alone), returning the document field path
// and the vendor direction. It is the connector List's resolveOrder in this
// store's own hands, because the effective listing is a store-owned merge rather
// than one query: an unknown field is sdk.ErrInvalidInput and CastLower is
// REFUSED rather than silently served in raw byte order, exactly as the
// connector answers for every other list here.
func effectiveOrderField(order crud.Order) (string, gcfs.Direction, error) {
	if order.Field == "" {
		order = role.DefaultEffectiveOrder
	}
	direction := gcfs.Asc
	if order.Direction == crud.DESC {
		direction = gcfs.Desc
	}
	for _, of := range role.EffectiveOrderFields {
		if of.Column != order.Field {
			continue
		}
		if of.CastLower {
			return "", direction, fmt.Errorf("authorization firestore store: order field %q asks for case-folded ordering, which Firestore cannot do: %w", of.Column, sdk.ErrInvalidInput)
		}
		return of.Column, direction, nil
	}
	return "", direction, fmt.Errorf("unknown order field %q: %w", order.Field, sdk.ErrInvalidInput)
}
