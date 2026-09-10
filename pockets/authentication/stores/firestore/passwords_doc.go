package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// The user_passwords collection's owner file. It is the smallest of the three:
// the document id IS the user id, there is no claim, and the SQL upsert
// (`INSERT … ON CONFLICT (user_id) DO UPDATE`) is a document Set. It is owned
// anyway so the credential row has ONE writer — the reason the pocket keeps
// credential material in its own table in the first place is that a store can
// guard it independently, and a second write path would quietly undo that.

// passwordRef is the credential document for a user.
func passwordRef(db *firestoredb.DB, userID string) *gcfs.DocumentRef {
	return db.Doc(collectionPasswords, passwordDocID(userID))
}

// putPassword upserts the hash. Set, not Create: a password CHANGE must replace
// rather than collide, which is exactly what the SQL adapters' ON CONFLICT DO
// UPDATE says.
func putPassword(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, userID, hash string) error {
	return w.Set(ctx, passwordRef(db, userID), passwordDoc{UserID: userID, Hash: hash})
}

// dropPassword deletes the credential row. It is unguarded on purpose: the SQL
// `DELETE FROM user_passwords WHERE user_id = ?` that CredentialMutations.Apply
// runs affects zero rows when the user has no password and reports no error, so
// removing an absent password is a successful no-op in all three families. It
// also needs no read — the document id is derived from the user id, and a
// Firestore delete has no precondition unless one is given.
func dropPassword(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, userID string) error {
	return w.Delete(ctx, passwordRef(db, userID))
}

// readPassword returns the stored hash, or sdk.ErrNotFound.
func readPassword(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string) (string, error) {
	snap, err := r.Get(ctx, passwordRef(db, userID))
	if err != nil {
		return "", err
	}
	var row passwordDoc
	if err := snap.DataTo(&row); err != nil {
		return "", fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionPasswords, err, sdk.ErrInvalidInput)
	}
	return row.Hash, nil
}
