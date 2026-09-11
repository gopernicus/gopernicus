package firestore

import (
	"context"
	"errors"
	"fmt"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"google.golang.org/api/iterator"
)

// UpgradeTupleStorage backfills natural tuple ordering, removes retired tuple
// and role metadata, and deletes obsolete surrogate-ID claims. It is an explicit
// host maintenance operation, never called by a constructor. Stop authorization
// readers and writers first, deploy the current indexes, run this function to
// completion, then start the new adapter. See UPGRADE.md.
//
// Work is paged and rerunnable after interruption. Each document write has an
// update-time precondition so an unexpected concurrent write fails instead of
// being overwritten. Document identities and subject claims are preserved.
func UpgradeTupleStorage(ctx context.Context, db *firestoredb.DB) error {
	if db == nil {
		return fmt.Errorf("authorization firestore upgrade requires a database: %w", sdk.ErrInvalidInput)
	}
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	w := db.WriterFrom(ctx)
	if err := upgradeDocuments(ctx, db, collectionRelationships, func(snap *gcfs.DocumentSnapshot) error {
		row, err := decodeRelationship(snap)
		if err != nil {
			return err
		}
		input := relationships.CreateRelationship{
			ResourceType: row.ResourceType, ResourceID: row.ResourceID, Relation: row.Relation,
			SubjectType: row.SubjectType, SubjectID: row.SubjectID, SubjectRelation: row.SubjectRelation,
		}
		if err := input.Validate(); err != nil {
			return err
		}
		if snap.Ref.ID != tupleID(row) {
			return fmt.Errorf("authorization firestore upgrade: tuple %s has a mismatched document ID: %w", snap.Ref.ID, sdk.ErrInvalidInput)
		}
		prefix, suffix := tupleSortKeys(row)
		return w.Update(ctx, snap.Ref, []gcfs.Update{
			{Path: "tuple_key_prefix", Value: prefix},
			{Path: "tuple_key_suffix", Value: suffix},
			{Path: "relationship_id", Value: gcfs.Delete},
			{Path: "created_at", Value: gcfs.Delete},
		}, gcfs.LastUpdateTime(snap.UpdateTime))
	}); err != nil {
		return err
	}
	if err := upgradeDocuments(ctx, db, collectionRoles, func(snap *gcfs.DocumentSnapshot) error {
		row, err := decodeRole(snap)
		if err != nil {
			return err
		}
		if err := row.toAssignment().Validate(); err != nil {
			return err
		}
		if snap.Ref.ID != roleDocID(row.SubjectType, row.SubjectID, row.Role, row.ResourceType, row.ResourceID) {
			return fmt.Errorf("authorization firestore upgrade: role %s has a mismatched document ID: %w", snap.Ref.ID, sdk.ErrInvalidInput)
		}
		return w.Update(ctx, snap.Ref, []gcfs.Update{
			{Path: "created_at", Value: gcfs.Delete},
		}, gcfs.LastUpdateTime(snap.UpdateTime))
	}); err != nil {
		return err
	}
	if err := upgradeDocuments(ctx, db, collectionSubjectClaims, func(snap *gcfs.DocumentSnapshot) error {
		return w.Update(ctx, snap.Ref, []gcfs.Update{{Path: "relationship_id", Value: gcfs.Delete}}, gcfs.LastUpdateTime(snap.UpdateTime))
	}); err != nil {
		return err
	}
	return upgradeDocuments(ctx, db, "iam_relationship_ids", func(snap *gcfs.DocumentSnapshot) error {
		return w.Delete(ctx, snap.Ref, gcfs.LastUpdateTime(snap.UpdateTime))
	})
}

func upgradeDocuments(ctx context.Context, db *firestoredb.DB, collection string, apply func(*gcfs.DocumentSnapshot) error) error {
	const pageSize = 100
	var after string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		query := db.Collection(collection).OrderBy(gcfs.DocumentID, gcfs.Asc).Limit(pageSize)
		if after != "" {
			query = query.StartAfter(after)
		}
		next, count, err := upgradeDocumentPage(ctx, db.ReaderFrom(ctx), query, apply)
		if err != nil {
			return fmt.Errorf("authorization firestore upgrade %s: %w", collection, err)
		}
		if count < pageSize {
			return nil
		}
		after = next
	}
}

func upgradeDocumentPage(ctx context.Context, reader firestoredb.Reader, query gcfs.Query, apply func(*gcfs.DocumentSnapshot) error) (last string, count int, err error) {
	it := reader.Documents(ctx, query)
	defer it.Stop()
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return last, count, nil
		}
		if err != nil {
			return last, count, firestoredb.MapError(err)
		}
		if err := apply(snap); err != nil {
			return last, count, fmt.Errorf("document %s: %w", snap.Ref.Path, err)
		}
		last, count = snap.Ref.ID, count+1
	}
}

// RemoveLegacyMutationStorage deletes retired receipt and revision collections.
// Archive them first if needed and stop old writers until this operation and the
// adapter upgrade finish. It is rerunnable and creates no historical audit facts.
func RemoveLegacyMutationStorage(ctx context.Context, db *firestoredb.DB) error {
	if db == nil {
		return fmt.Errorf("authorization firestore cleanup requires a database: %w", sdk.ErrInvalidInput)
	}
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	for _, collection := range []string{"iam_mutations", "iam_scopes"} {
		if err := upgradeDocuments(ctx, db, collection, func(snap *gcfs.DocumentSnapshot) error {
			return db.WriterFrom(ctx).Delete(ctx, snap.Ref, gcfs.LastUpdateTime(snap.UpdateTime))
		}); err != nil {
			return err
		}
	}
	return nil
}
