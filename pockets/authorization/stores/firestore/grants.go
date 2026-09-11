package firestore

import (
	"context"
	"errors"
	"fmt"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"google.golang.org/api/iterator"
)

// This file OWNS the iam_roles collection, the way tuples.go owns a relationship
// tuple's two documents: putRole and dropRole are its only writers, roleRef
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
func newRoleDoc(a roles.Assignment) roleDoc {
	return roleDoc{
		SubjectType:  a.SubjectType,
		SubjectID:    a.SubjectID,
		Role:         a.Role,
		ResourceType: a.ResourceType,
		ResourceID:   a.ResourceID,
	}
}

// toAssignment projects a stored row back to the port's type.
func (r roleDoc) toAssignment() roles.Assignment {
	return roles.Assignment{
		SubjectType:  r.SubjectType,
		SubjectID:    r.SubjectID,
		Role:         r.Role,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
	}
}

// putRole derives the equality and natural sort keys from the grant's fields.
// Create detects a concurrent duplicate without overwriting an existing grant.
func putRole(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row roleDoc) error {
	row.SubjectKey = roleSubjectKey(row.SubjectType, row.SubjectID)
	row.ResourceKey = resourceKey(row.ResourceType, row.ResourceID)
	row.RoleKey = roleKey(row.SubjectType, row.SubjectID, row.Role, row.ResourceType, row.ResourceID)
	row.GrantKey = grantKey(row.SubjectType, row.SubjectID, row.Role)

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

// queryRoles runs one role query and decodes every document it returns. It is
// the roles-kind twin of queryRelationships and, like it, consumes iterator.Done
// as the loop terminator and maps every other Next error HERE, at the iteration
// boundary. The guarded-mutation path's teardown sweep is its caller: a
// transaction cannot count, so the rows a teardown removes are the rows it read.
func queryRoles(ctx context.Context, r firestoredb.Reader, q gcfs.Query) ([]roleDoc, error) {
	it := r.Documents(ctx, q)
	defer it.Stop()

	var out []roleDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		row, err := decodeRole(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
}

// scopedRolesQuery is the query for every role grant stored EXACTLY at
// (resourceType, resourceID) — the teardown sweep's population and the global
// scope's own listing when the pair is empty.
func scopedRolesQuery(db *firestoredb.DB, resourceType, resourceID string) gcfs.Query {
	return rolesQuery(db).Where("resource_key", "==", resourceKey(resourceType, resourceID))
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

// listRoles gives both raw role listings the same natural role_key order and
// cursor semantics. No PostFilter is declared, so Search is refused.
func listRoles(base gcfs.Query) firestoredb.ListQuery[roleDoc] {
	return firestoredb.ListQuery[roleDoc]{
		Query:        base,
		OrderFields:  roles.OrderFields,
		DefaultOrder: roles.DefaultOrder,
		PK:           "role_key",
		Decode:       decodeRole,
		OrderValueOf: func(row roleDoc, _ string) any { return row.RoleKey },
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
func effectiveOrderField(order list.Order) (string, gcfs.Direction, error) {
	if order.Field == "" {
		order = roles.DefaultEffectiveOrder
	}
	direction := gcfs.Asc
	if order.Direction == list.DESC {
		direction = gcfs.Desc
	}
	for _, of := range roles.EffectiveOrderFields {
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
