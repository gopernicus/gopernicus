package firestore

import (
	"context"
	"errors"
	"fmt"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"google.golang.org/api/iterator"
)

// The sessions collection AND its refresh-hash claim collection are owned here:
// putSession, updateSession and dropSession are the only writers, and
// ownership_test.go refuses any other non-test file to name either collection.
//
// The rule this pair exists to keep is SCHEMA.md §5.3: exactly ONE claim
// document, on the CURRENT refresh hash, reproduces the unique index
// idx_sessions_refresh_token_hash. The rotated-away (grace) hash takes NO claim
// — its SQL index is not unique — so it stays a plain queryable field on the
// row, before and after the grace slot is consumed. That asymmetry is the whole
// reason a reused grace token still resolves its session for revocation, and it
// is also the easiest thing in this store to get wrong from a call site: a write
// path that claimed the previous hash would make a rotation collide with itself,
// and one that forgot to release the old current claim would make the next
// login by that session impossible to arbitrate.
//
// Neither writer READS. A Firestore transaction refuses a read issued after its
// first write, so the CALLER owns the complete read phase — the same discipline
// identifiers_doc.go states.

// The sessions field paths the queries filter on. There is no ordered listing
// of sessions in this pocket (the port has none), so no order field appears
// here and the two shapes below need only single-field indexes.
const (
	fieldSessionUserID       = "user_id"
	fieldSessionPreviousHash = "previous_refresh_token_hash"
)

// sessionRef is the row's document — the KeyHash of the app-minted session id.
// The id is never store-minted: the access JWT is signed with it BEFORE the row
// exists (SCHEMA.md §3.3).
func sessionRef(db *firestoredb.DB, sessionID string) *gcfs.DocumentRef {
	return db.Doc(collectionSessions, sessionDocID(sessionID))
}

// refreshClaimRef is the claim on a CURRENT refresh hash. It doubles as
// GetByRefreshHash's access path for the current slot: one point read resolves
// the credential to its session without an equality filter on the hash field.
func refreshClaimRef(db *firestoredb.DB, hash string) *gcfs.DocumentRef {
	return db.Doc(collectionRefreshHashClaims, refreshHashClaimDocID(hash))
}

// sessionsForUserQuery is the revocation cascade's population — every session
// of one user, unordered (the callers delete the whole set).
func sessionsForUserQuery(db *firestoredb.DB, userID string) gcfs.Query {
	return db.Collection(collectionSessions).Where(fieldSessionUserID, "==", userID)
}

// sessionByPreviousHashQuery resolves the GRACE slot, which has no claim to
// read. It is an equality on the row's own field, which is exactly what the
// non-unique SQL index idx_sessions_previous_refresh_token_hash serves.
func sessionByPreviousHashQuery(db *firestoredb.DB, hash string) gcfs.Query {
	return db.Collection(collectionSessions).Where(fieldSessionPreviousHash, "==", hash).Limit(1)
}

// newSessionDoc builds the document for a session being CREATED. The previous
// slot is written as an EXPLICIT null rather than "": every fresh row would
// otherwise share the empty string and a lookup for an empty hash could match an
// arbitrary session — the cross-session bleed migration 0003 calls out, and the
// port's own EmptyPreviousGuard case.
func newSessionDoc(sess session.Session) (sessionDoc, error) {
	methods, err := encodeMethods(sess.Authentication.Methods)
	if err != nil {
		return sessionDoc{}, err
	}
	return sessionDoc{
		ID:                       sess.ID,
		UserID:                   sess.UserID,
		RefreshTokenHash:         sess.RefreshTokenHash,
		PreviousRefreshTokenHash: nullHash(sess.PreviousRefreshTokenHash),
		PreviousUsed:             sess.PreviousUsed,
		RotationCount:            sess.RotationCount,
		AuthenticatedAt:          firestoredb.NullTime(sess.Authentication.AuthenticatedAt),
		AuthenticationMethods:    methods,
		AssuranceLevel:           string(sess.Authentication.Assurance),
		CreatedAt:                firestoredb.TruncateTime(sess.CreatedAt),
		ExpiresAt:                firestoredb.TruncateTime(sess.ExpiresAt),
	}, nil
}

// nullHash maps an empty hash to Firestore's null, the analogue of the SQL
// adapters' nullHash and for the same reason (see newSessionDoc).
func nullHash(h string) any {
	if h == "" {
		return nil
	}
	return h
}

// putSession writes a NEW session row and records the claim on its current
// refresh hash. The verb is Create for both: the document id IS the primary key
// and the claim id IS the unique key, so a duplicate session id or a colliding
// refresh hash loses at the SERVER as sdk.ErrAlreadyExists rather than
// overwriting a live credential (ruling R3 — a free claim is taken, never read).
func putSession(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row sessionDoc) error {
	takeRefreshClaim(db, plan, row)
	return w.Create(ctx, sessionRef(db, row.ID), row)
}

// updateSession rewrites an EXISTING session row and moves its refresh claim to
// match the new state: a rotation releases the old current hash and takes the
// new one in this same transaction, so the claim can never name a hash the row
// no longer carries. A state that keeps the same current hash (ConsumeGrace)
// releases and re-takes ONE document, which claimPlan collapses into a single
// Set rather than a delete-then-create whose outcome would depend on write
// ordering.
func updateSession(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, old, next sessionDoc) error {
	plan.release(refreshClaimRef(db, old.RefreshTokenHash))
	takeRefreshClaim(db, plan, next)
	return w.Set(ctx, sessionRef(db, next.ID), next)
}

// dropSession deletes one session row and releases its current-hash claim. The
// grace hash is released by nothing, because it claimed nothing.
func dropSession(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row sessionDoc) error {
	plan.release(refreshClaimRef(db, row.RefreshTokenHash))
	return w.Delete(ctx, sessionRef(db, row.ID))
}

// dropSessionsForUser enqueues the deletion of every session in rows together
// with its claim. It takes ALREADY-READ documents and reads nothing itself,
// which is what lets a caller that has more to do in the same transaction —
// UserAdmin.SetStatus revoking sessions and grants, passwordless adoption
// revoking a squatter's credentials — finish its read phase first and still
// revoke atomically.
func dropSessionsForUser(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, rows []sessionDoc) error {
	for _, row := range rows {
		if err := dropSession(ctx, db, w, plan, row); err != nil {
			return err
		}
	}
	return nil
}

// takeRefreshClaim records the claim row's CURRENT hash takes.
func takeRefreshClaim(db *firestoredb.DB, plan *claimPlan, row sessionDoc) {
	plan.take(refreshClaimRef(db, row.RefreshTokenHash), refreshHashClaimDoc{
		DocID:     sessionDocID(row.ID),
		SessionID: row.ID,
	})
}

// readSession reads one row through r. An absent document is sdk.ErrNotFound.
func readSession(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, sessionID string) (sessionDoc, error) {
	snap, err := r.Get(ctx, sessionRef(db, sessionID))
	if err != nil {
		return sessionDoc{}, err
	}
	return decodeSession(snap)
}

// readSessionByRefreshClaim resolves a CURRENT refresh hash through its claim:
// the claim document names the row, and the row is then read. Absence at either
// hop is sdk.ErrNotFound.
func readSessionByRefreshClaim(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, hash string) (sessionDoc, error) {
	snap, err := r.Get(ctx, refreshClaimRef(db, hash))
	if err != nil {
		return sessionDoc{}, err
	}
	var claim refreshHashClaimDoc
	if err := snap.DataTo(&claim); err != nil {
		return sessionDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionRefreshHashClaims, err, sdk.ErrInvalidInput)
	}
	return readSession(ctx, db, r, claim.SessionID)
}

// readSessionsForUser reads every session of one user. It is the READ half the
// revocation cascades pair with dropSessionsForUser.
func readSessionsForUser(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string) ([]sessionDoc, error) {
	return querySessions(ctx, r, sessionsForUserQuery(db, userID))
}

// querySessions runs one session query and decodes every document it returns,
// consuming iterator.Done as the loop terminator and mapping every other Next
// error HERE, at the iteration boundary.
func querySessions(ctx context.Context, r firestoredb.Reader, q gcfs.Query) ([]sessionDoc, error) {
	it := r.Documents(ctx, q)
	defer it.Stop()

	var out []sessionDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		row, err := decodeSession(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
}

// decodeSession turns one snapshot into a session document.
func decodeSession(snap *gcfs.DocumentSnapshot) (sessionDoc, error) {
	var row sessionDoc
	if err := snap.DataTo(&row); err != nil {
		return sessionDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionSessions, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// rotated returns the row as of a completed rotation: the expected current hash
// moves into the grace slot with its consumed flag cleared, the counter
// advances, and the new hash becomes current. expires_at is deliberately
// untouched — the refresh horizon is fixed (D2), and rotation must never extend
// a session's life.
func (d sessionDoc) rotated(expectedCurrentHash, newHash string) sessionDoc {
	next := d
	next.RefreshTokenHash = newHash
	next.PreviousRefreshTokenHash = nullHash(expectedCurrentHash)
	next.PreviousUsed = false
	next.RotationCount = d.RotationCount + 1
	return next
}

// previousHash reads the nullable grace slot. Its zero value is the domain's
// "never rotated" sentinel, and a null must read back as "" rather than as a
// matchable value.
func (d sessionDoc) previousHash() (string, error) {
	switch v := d.PreviousRefreshTokenHash.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	default:
		return "", fmt.Errorf("authentication firestore store: %s.%s is %T, want a string or null: %w",
			collectionSessions, fieldSessionPreviousHash, v, sdk.ErrInvalidInput)
	}
}

// toDomain projects the document onto the domain aggregate.
func (d sessionDoc) toDomain() (session.Session, error) {
	previous, err := d.previousHash()
	if err != nil {
		return session.Session{}, err
	}
	authenticatedAt, err := firestoredb.ParseNullTime(d.AuthenticatedAt)
	if err != nil {
		return session.Session{}, err
	}
	methods, err := decodeMethods(d.AuthenticationMethods)
	if err != nil {
		return session.Session{}, err
	}
	return session.Session{
		ID:                       d.ID,
		UserID:                   d.UserID,
		RefreshTokenHash:         d.RefreshTokenHash,
		PreviousRefreshTokenHash: previous,
		PreviousUsed:             d.PreviousUsed,
		RotationCount:            d.RotationCount,
		CreatedAt:                d.CreatedAt,
		ExpiresAt:                d.ExpiresAt,
		Authentication: session.AuthenticationMetadata{
			AuthenticatedAt: authenticatedAt,
			Methods:         methods,
			Assurance:       session.AssuranceLevel(d.AssuranceLevel),
		},
	}, nil
}
