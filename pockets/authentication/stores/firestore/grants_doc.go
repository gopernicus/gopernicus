package firestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The grant collection's owner file. It has NO claim: the SQL index it mirrors
// (idx_authentication_grants_session_purpose_context) is not unique — a session
// may hold several unspent grants for one purpose and context — so uniqueness
// here is the document id alone (SCHEMA.md §5.8, §3.13).
//
// What the collection has instead is a DERIVED equality key. Consume selects on
// three columns; Firestore charges for query complexity, so the three collapse
// into ONE indexed equality, consume_key = h(session_id, purpose,
// context_digest), and the originals stay in their own fields and are what the
// port returns (SCHEMA.md §4.1). The key is written by newAuthGrantDoc and by
// nothing else, which is what keeps it an identity rather than a second source
// of truth.
//
// Spending a grant is a field update, NOT a delete: consumed_at is the
// single-use marker the SQL adapters set, and DeleteBySession is the only path
// that removes rows. The two revocation helpers take ALREADY-READ documents so
// N2b's SetStatus and N4d's adoption can finish their read phase before they
// revoke.

// The grant field paths the queries filter and order on.
const (
	fieldGrantConsumeKey = "consume_key"
	fieldGrantConsumedAt = "consumed_at"
	fieldGrantSessionID  = "session_id"
	fieldGrantUserID     = "user_id"
	fieldGrantCreatedAt  = "created_at"
	fieldGrantID         = "id"
)

// authGrantRef is the row's document — the KeyHash of its primary key.
func authGrantRef(db *firestoredb.DB, grantID string) *gcfs.DocumentRef {
	return db.Doc(collectionAuthGrants, authGrantDocID(grantID))
}

// unspentGrantQuery is Consume's selection, verbatim from the SQL: the OLDEST
// unspent grant matching (session, purpose, context). `consumed_at == null` is a
// Firestore IS_NULL filter, the same predicate as SQL's `consumed_at IS NULL`,
// and the order plus the limit are what make "oldest" deterministic when a
// session holds several.
func unspentGrantQuery(db *firestoredb.DB, consumeKey string) gcfs.Query {
	return db.Collection(collectionAuthGrants).
		Where(fieldGrantConsumeKey, "==", consumeKey).
		Where(fieldGrantConsumedAt, "==", nil).
		OrderBy(fieldGrantCreatedAt, gcfs.Asc).
		OrderBy(fieldGrantID, gcfs.Asc).
		Limit(1)
}

// grantsForSessionQuery is the session revocation cascade: revoking a session
// invalidates every grant bound to it, spent or not.
func grantsForSessionQuery(db *firestoredb.DB, sessionID string) gcfs.Query {
	return db.Collection(collectionAuthGrants).Where(fieldGrantSessionID, "==", sessionID)
}

// grantsForUserQuery is the user revocation cascade's other half: a lifecycle
// transition removes the grants a user OWNS as well as those bound to the
// sessions being deleted (the SQL adapters' `user_id = ? OR session_id IN (…)`).
func grantsForUserQuery(db *firestoredb.DB, userID string) gcfs.Query {
	return db.Collection(collectionAuthGrants).Where(fieldGrantUserID, "==", userID)
}

// newAuthGrantDoc builds the document for a grant being CREATED, minting the id
// when the caller left it empty (the port's "assigning its ID when empty") and
// deriving the consume key from the three columns Consume selects on.
//
// It is called INSIDE the write path so a retried attempt mints a fresh id
// rather than reusing one a losing attempt claimed (N-D5).
func newAuthGrantDoc(g authgrant.Grant) (authGrantDoc, error) {
	methods, err := encodeMethods(g.Methods)
	if err != nil {
		return authGrantDoc{}, err
	}
	grantID := g.ID
	if grantID == "" {
		grantID = firestoredb.NewID()
	}
	return authGrantDoc{
		ID:              grantID,
		SessionID:       g.SessionID,
		UserID:          g.UserID,
		Purpose:         g.Purpose,
		ContextDigest:   g.ContextDigest,
		Methods:         methods,
		Assurance:       string(g.Assurance),
		AuthenticatedAt: firestoredb.TruncateTime(g.AuthenticatedAt),
		ExpiresAt:       firestoredb.TruncateTime(g.ExpiresAt),
		CreatedAt:       firestoredb.TruncateTime(g.CreatedAt),
		ConsumedAt:      firestoredb.NullTime(g.ConsumedAt),
		ConsumeKey:      grantConsumeKey(g.SessionID, g.Purpose, g.ContextDigest),
	}, nil
}

// putAuthGrant writes a NEW grant. Create, never Set: the document id IS the
// primary key, so a host that supplies a duplicate id loses at the server rather
// than overwriting a live proof.
func putAuthGrant(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row authGrantDoc) error {
	return w.Create(ctx, authGrantRef(db, row.ID), row)
}

// spendAuthGrant marks a grant consumed. It is a FIELD update rather than a
// whole-document Set: consumed_at is the only thing that changes, and the row's
// remaining fields are the evidence the audit rail keeps.
func spendAuthGrant(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row authGrantDoc, now time.Time) error {
	return w.Update(ctx, authGrantRef(db, row.ID), []gcfs.Update{
		{Path: fieldGrantConsumedAt, Value: firestoredb.TruncateTime(now)},
	})
}

// dropAuthGrants enqueues the deletion of every grant in rows. It takes
// ALREADY-READ documents and reads nothing itself, so the callers that revoke
// more than grants in one transaction — DeleteBySession here, SetStatus and
// passwordless adoption later — can finish their read phase first.
func dropAuthGrants(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, rows []authGrantDoc) error {
	for _, row := range rows {
		if err := w.Delete(ctx, authGrantRef(db, row.ID)); err != nil {
			return err
		}
	}
	return nil
}

// readGrantsForSession reads every grant bound to a session — the READ half
// dropAuthGrants pairs with.
func readGrantsForSession(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, sessionID string) ([]authGrantDoc, error) {
	return queryAuthGrants(ctx, r, grantsForSessionQuery(db, sessionID))
}

// readGrantsForUser reads every grant a user owns.
func readGrantsForUser(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string) ([]authGrantDoc, error) {
	return queryAuthGrants(ctx, r, grantsForUserQuery(db, userID))
}

// queryAuthGrants runs one grant query and decodes every document it returns,
// consuming iterator.Done as the loop terminator and mapping every other Next
// error HERE, at the iteration boundary.
func queryAuthGrants(ctx context.Context, r firestoredb.Reader, q gcfs.Query) ([]authGrantDoc, error) {
	it := r.Documents(ctx, q)
	defer it.Stop()

	var out []authGrantDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		row, err := decodeAuthGrant(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
}

// decodeAuthGrant turns one snapshot into a grant document.
func decodeAuthGrant(snap *gcfs.DocumentSnapshot) (authGrantDoc, error) {
	var row authGrantDoc
	if err := snap.DataTo(&row); err != nil {
		return authGrantDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionAuthGrants, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// toDomain projects the document onto the domain aggregate.
func (d authGrantDoc) toDomain() (authgrant.Grant, error) {
	consumedAt, err := firestoredb.ParseNullTime(d.ConsumedAt)
	if err != nil {
		return authgrant.Grant{}, err
	}
	methods, err := decodeMethods(d.Methods)
	if err != nil {
		return authgrant.Grant{}, err
	}
	return authgrant.Grant{
		ID:              d.ID,
		SessionID:       d.SessionID,
		UserID:          d.UserID,
		Purpose:         d.Purpose,
		ContextDigest:   d.ContextDigest,
		Methods:         methods,
		Assurance:       session.AssuranceLevel(d.Assurance),
		AuthenticatedAt: d.AuthenticatedAt,
		ExpiresAt:       d.ExpiresAt,
		CreatedAt:       d.CreatedAt,
		ConsumedAt:      consumedAt,
	}, nil
}
