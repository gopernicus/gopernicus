package firestore

import (
	"context"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/sdk"
)

// The api_keys collection AND its key-hash claim collection are owned here:
// putAPIKey is the only writer of the pair, and ownership_test.go refuses any
// other non-test file to name either collection.
//
// The claim reproduces `CREATE UNIQUE INDEX idx_api_keys_key_hash ON api_keys
// (key_hash)` (migration 0007). Two properties of that index are load-bearing
// and were re-read from the migration rather than assumed (SCHEMA.md §5.4):
//
//   - It is a FULL unique index, not a partial one. There is no
//     `WHERE revoked_at IS NULL`, so a REVOKED key still occupies its hash in
//     SQL and a re-mint of the same hash still collides. The claim therefore
//     survives revocation, and this file ships NO drop helper: nothing in the
//     port hard-deletes a key (there is no Delete method) and nothing takes a
//     key out of the index's predicate, so a release path would be dead code
//     with a re-mintable-credential footgun in it.
//   - It doubles as GetByHash's ACCESS PATH. Resolving a hash through the claim
//     document is a point read rather than an equality filter on credential
//     material, which keeps the lookup off an unbounded indexed value (§4.2) and
//     off the composite manifest entirely.
//
// Revoke and TouchLastUsed are FIELD updates on one document: they change a
// timestamp the index does not cover, so they touch no claim and need no
// transaction.

// The api_keys field paths the queries filter and order on, and the field
// updates address.
const (
	fieldAPIKeyServiceAccountID = "service_account_id"
	fieldAPIKeyName             = "name"
	fieldAPIKeyRevokedAt        = "revoked_at"
	fieldAPIKeyLastUsedAt       = "last_used_at"
	fieldAPIKeyID               = "id"
)

// apiKeyRef is the row's document — the KeyHash of its primary key.
func apiKeyRef(db *firestoredb.DB, id string) *gcfs.DocumentRef {
	return db.Doc(collectionAPIKeys, apiKeyDocID(id))
}

// apiKeyHashClaimRef is the uniqueness claim on a key hash, and GetByHash's
// access path.
func apiKeyHashClaimRef(db *firestoredb.DB, keyHash string) *gcfs.DocumentRef {
	return db.Doc(collectionAPIKeyHashClaims, apiKeyHashClaimDocID(keyHash))
}

// apiKeysForServiceAccountQuery is ListByServiceAccount's population: one
// parent's keys. It carries no order, limit, or cursor — the connector's List
// owns all three — and the parent equality is what makes the search a
// PARENT-SCOPED page fill rather than a collection scan (ruling R4).
func apiKeysForServiceAccountQuery(db *firestoredb.DB, serviceAccountID string) gcfs.Query {
	return db.Collection(collectionAPIKeys).Where(fieldAPIKeyServiceAccountID, "==", serviceAccountID)
}

// newAPIKeyDoc builds the document for a key being CREATED, minting the record
// id when the caller left it empty (the greenfield cryptids.Database
// convention). It is called INSIDE the transaction callback so a retried attempt
// mints a fresh id rather than reusing one a losing attempt claimed (N-D5).
func newAPIKeyDoc(k apikey.APIKey) apiKeyDoc {
	id := k.ID
	if id == "" {
		id = firestoredb.NewID()
	}
	return apiKeyDoc{
		ID:               id,
		ServiceAccountID: k.ServiceAccountID,
		Name:             k.Name,
		KeyPrefix:        k.KeyPrefix,
		KeyHash:          k.KeyHash,
		ExpiresAt:        firestoredb.NullTime(k.ExpiresAt),
		RevokedAt:        firestoredb.NullTime(k.RevokedAt),
		LastUsedAt:       firestoredb.NullTime(k.LastUsedAt),
		CreatedAt:        firestoredb.TruncateTime(k.CreatedAt),
	}
}

// putAPIKey writes a NEW key and claims its hash. Both are Creates, so BOTH
// uniqueness rules are arbitrated by the server at commit: a duplicate id loses
// on the row, a duplicate hash loses on the claim, and either way the whole
// transaction rolls back as sdk.ErrAlreadyExists with nothing written (ruling
// R3). Nothing is read to decide it — a claim never has to be read to be safely
// taken.
func putAPIKey(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row apiKeyDoc) error {
	plan.take(apiKeyHashClaimRef(db, row.KeyHash), apiKeyHashClaimDoc{
		DocID:    apiKeyDocID(row.ID),
		APIKeyID: row.ID,
	})
	return w.Create(ctx, apiKeyRef(db, row.ID), row)
}

// revokeAPIKey stamps revoked_at. It deliberately does NOT release the hash
// claim: `idx_api_keys_key_hash` is unconditional in SQL, so a revoked key keeps
// its hash reserved there and must keep it reserved here — releasing it would
// let a revoked credential's hash be minted again, which is a weaker store, not
// a tidier one. A missing document is sdk.ErrNotFound, the port's contract.
func revokeAPIKey(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, id string, revokedAt time.Time) error {
	return w.Update(ctx, apiKeyRef(db, id), []gcfs.Update{
		{Path: fieldAPIKeyRevokedAt, Value: firestoredb.TruncateTime(revokedAt)},
	})
}

// touchAPIKey stamps last_used_at. Callers treat it as best-effort; a missing
// document is still sdk.ErrNotFound.
func touchAPIKey(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, id string, usedAt time.Time) error {
	return w.Update(ctx, apiKeyRef(db, id), []gcfs.Update{
		{Path: fieldAPIKeyLastUsedAt, Value: firestoredb.TruncateTime(usedAt)},
	})
}

// readAPIKeyByHashClaim resolves a hash through its claim: the claim document
// names the row, and the row is returned VERBATIM — revoked and expired included
// — because revocation and expiry are service branches, never store filters (the
// port's pinned GetByHash contract).
//
// The two point reads need no snapshot to agree. A claim and its row are created
// in ONE transaction and neither is ever deleted or re-pointed, so a claim that
// is visible at all proves its row was committed before it was read: there is no
// window in which the pair disagrees. Absence at either hop is sdk.ErrNotFound,
// matching the SQL adapters' empty result for an unknown hash.
func readAPIKeyByHashClaim(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, keyHash string) (apiKeyDoc, error) {
	snap, err := r.Get(ctx, apiKeyHashClaimRef(db, keyHash))
	if err != nil {
		return apiKeyDoc{}, err
	}
	var claim apiKeyHashClaimDoc
	if err := snap.DataTo(&claim); err != nil {
		return apiKeyDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionAPIKeyHashClaims, err, sdk.ErrInvalidInput)
	}
	rowSnap, err := r.Get(ctx, apiKeyRef(db, claim.APIKeyID))
	if err != nil {
		return apiKeyDoc{}, err
	}
	row, err := decodeAPIKey(rowSnap)
	if err != nil {
		return apiKeyDoc{}, err
	}
	// The row's own hash is the authority, compared in constant time: a claim
	// that named the wrong key would authenticate the wrong service account,
	// and nothing downstream re-checks it. A mismatch is sdk.ErrNotFound, the
	// same answer SQL's `WHERE key_hash = ?` gives for an unknown hash.
	if !auth.ConstantTimeDigestEqual(keyHash, row.KeyHash) {
		return apiKeyDoc{}, sdk.ErrNotFound
	}
	return row, nil
}

// decodeAPIKey turns one snapshot into a key document.
func decodeAPIKey(snap *gcfs.DocumentSnapshot) (apiKeyDoc, error) {
	var row apiKeyDoc
	if err := snap.DataTo(&row); err != nil {
		return apiKeyDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionAPIKeys, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// decodeAPIKeyRow is the listing's Decode: one snapshot straight to the domain
// entity.
func decodeAPIKeyRow(snap *gcfs.DocumentSnapshot) (apikey.APIKey, error) {
	row, err := decodeAPIKey(snap)
	if err != nil {
		return apikey.APIKey{}, err
	}
	return row.toDomain()
}

// listAPIKeys is the ListQuery one service account's keys page with — the
// pocket's ONLY searchable list.
//
// The base query is the parent scope, and req.Search becomes a client-side
// PostFilter over apikey.SearchFields through the connector's SearchFilter,
// which is the ONE implementation of crud.MatchesSearch for Firestore (ruling
// R4). A blank term yields a NIL filter, which is not the same as a permissive
// one: nil lets the list take its cheap server-side paging path, and the count
// stays a server aggregation. A non-blank term makes every path page-fill,
// including the reverse HasPrev probe and the count, so the searched total is
// the searched population rather than the parent's.
//
// searchValueOf is what names the searchable columns, the way the SQL adapters
// name them in their LIKE clause.
func listAPIKeys(db *firestoredb.DB, serviceAccountID, search string) firestoredb.ListQuery[apikey.APIKey] {
	return firestoredb.ListQuery[apikey.APIKey]{
		Query:        apiKeysForServiceAccountQuery(db, serviceAccountID),
		OrderFields:  apikey.OrderFields,
		DefaultOrder: apikey.DefaultOrder,
		PK:           fieldAPIKeyID,
		Decode:       decodeAPIKeyRow,
		OrderValueOf: func(row apikey.APIKey, _ string) any { return row.CreatedAt },
		PKOf:         func(row apikey.APIKey) string { return row.ID },
		PostFilter:   firestoredb.SearchFilter(apikey.SearchFields, searchValueOf, search),
	}
}

// searchValueOf reads a key's value for one of apikey.SearchFields. An unknown
// column answers "" rather than panicking: the allow-list is the domain's, so a
// column that is not mapped here cannot be searched, and a search that silently
// matched everything would be worse than one that matches nothing.
func searchValueOf(row apikey.APIKey, column string) string {
	if column == fieldAPIKeyName {
		return row.Name
	}
	return ""
}

// toDomain projects the document onto the domain aggregate. The three nullable
// timestamps read back as the zero time, which is the domain's "never expires /
// not revoked / never used" sentinel.
func (d apiKeyDoc) toDomain() (apikey.APIKey, error) {
	expiresAt, err := firestoredb.ParseNullTime(d.ExpiresAt)
	if err != nil {
		return apikey.APIKey{}, err
	}
	revokedAt, err := firestoredb.ParseNullTime(d.RevokedAt)
	if err != nil {
		return apikey.APIKey{}, err
	}
	lastUsedAt, err := firestoredb.ParseNullTime(d.LastUsedAt)
	if err != nil {
		return apikey.APIKey{}, err
	}
	return apikey.APIKey{
		ID:               d.ID,
		ServiceAccountID: d.ServiceAccountID,
		Name:             d.Name,
		KeyPrefix:        d.KeyPrefix,
		KeyHash:          d.KeyHash,
		ExpiresAt:        expiresAt,
		RevokedAt:        revokedAt,
		LastUsedAt:       lastUsedAt,
		CreatedAt:        d.CreatedAt,
	}, nil
}
