package firestore

import (
	"context"
	"errors"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"google.golang.org/api/iterator"
)

// Credential mutations revoke reset proofs even though the general Firestore
// challenge repository is still an explicitly unfinished capability.
func readPasswordResetChallenges(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string) ([]challengeDoc, error) {
	q := db.Collection(collectionChallenges).Where("user_id", "==", userID).Where("purpose", "==", "password_reset")
	it := r.Documents(ctx, q)
	defer it.Stop()
	var rows []challengeDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return rows, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		var row challengeDoc
		if err := snap.DataTo(&row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
}
func dropPasswordResetChallenges(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, rows []challengeDoc) error {
	for _, row := range rows {
		subject := row.SubjectKey
		if subject == "" {
			subject = row.UserID
		}
		if err := w.Delete(ctx, db.Doc(collectionChallenges, challengeDocID(subject, row.Purpose))); err != nil {
			return err
		}
		plan.release(challengeDigestRef(db, row.Purpose, row.SecretDigest))
	}
	return nil
}
func challengeDigestRef(db *firestoredb.DB, purpose, digest string) *gcfs.DocumentRef {
	return db.Doc(collectionChallengeDigests, challengeDigestClaimDocID(purpose, digest))
}
