package firestore

import (
	"context"
	"errors"
	"fmt"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/sdk"
	"google.golang.org/api/iterator"
)

// The two OAuth collections are owned here. Neither has a CLAIM document, and
// that is the point worth stating: both of their uniqueness rules are carried by
// the DOCUMENT ID itself (SCHEMA.md §5.8).
//
//	oauth_accounts  id = h(provider, provider_user_id)  — the composite PK, and
//	                the only uniqueness this table has: a provider identity
//	                belongs to at most one local user.
//	oauth_states    id = h(token)                       — the PK; the token IS
//	                the lookup key, and Consume is a single-use get-and-delete.
//
// So `Create` on the derived id IS the uniqueness check: the SERVER evaluates
// the precondition at commit and a duplicate loses as sdk.ErrAlreadyExists,
// exactly as the SQL adapters' plain INSERT does (there is no upsert on either —
// a colliding link is an error, never a silent overwrite).
//
// The original provider/token values are kept in FIELDS and returned verbatim;
// the hashed id is an identity, never a projection (SCHEMA.md §4.2). That also
// keeps the uniqueness off an equality filter on a provider-issued subject,
// whose index entry would truncate past 1500 bytes.

// The OAuth field paths the queries filter and order on. `Delete` filters on
// (user_id, provider) — the doc id needs provider_user_id, which the caller does
// not supply — and both are equalities, which Firestore serves from single-field
// indexes without a composite.
const (
	fieldOAuthUserID         = "user_id"
	fieldOAuthProvider       = "provider"
	fieldOAuthLinkedAt       = "linked_at"
	fieldOAuthProviderUserID = "provider_user_id"
)

// oauthAccountRef is the link's document — the KeyHash of its composite primary
// key.
func oauthAccountRef(db *firestoredb.DB, provider, providerUserID string) *gcfs.DocumentRef {
	return db.Doc(collectionOAuthAccounts, oauthAccountDocID(provider, providerUserID))
}

// oauthAccountsForUserQuery is ListByUser's population, in the SQL adapters'
// `ORDER BY linked_at DESC, provider_user_id DESC`. The port returns a slice
// rather than a page, so there is no reversed direction of its own.
func oauthAccountsForUserQuery(db *firestoredb.DB, userID string) gcfs.Query {
	return db.Collection(collectionOAuthAccounts).
		Where(fieldOAuthUserID, "==", userID).
		OrderBy(fieldOAuthLinkedAt, gcfs.Desc).
		OrderBy(fieldOAuthProviderUserID, gcfs.Desc)
}

// oauthAccountsForUserProviderQuery is Delete's selection: the SQL
// `WHERE user_id = ? AND provider = ?`. It is unordered, because the whole
// match set is removed.
func oauthAccountsForUserProviderQuery(db *firestoredb.DB, userID, provider string) gcfs.Query {
	return db.Collection(collectionOAuthAccounts).
		Where(fieldOAuthUserID, "==", userID).
		Where(fieldOAuthProvider, "==", provider)
}

// oauthStateRef is the flow secret's document — the KeyHash of its token.
func oauthStateRef(db *firestoredb.DB, token string) *gcfs.DocumentRef {
	return db.Doc(collectionOAuthStates, oauthStateDocID(token))
}

// newOAuthAccountDoc builds the document for a link being CREATED. The token
// columns are persisted verbatim: ciphertext when the pocket is wired with an
// encrypter, empty when it is not — this store never inspects them.
func newOAuthAccountDoc(a oauthaccount.OAuthAccount) oauthAccountDoc {
	return oauthAccountDoc{
		Provider:              a.Provider,
		ProviderUserID:        a.ProviderUserID,
		UserID:                a.UserID,
		ProviderEmail:         a.ProviderEmail,
		ProviderEmailVerified: a.ProviderEmailVerified,
		AccountVerified:       a.AccountVerified,
		LinkedAt:              firestoredb.TruncateTime(a.LinkedAt),
		AccessToken:           a.AccessToken,
		RefreshToken:          a.RefreshToken,
		TokenExpiresAt:        firestoredb.NullTime(a.TokenExpiresAt),
		TokenType:             a.TokenType,
		Scope:                 a.Scope,
	}
}

// putOAuthAccount writes a NEW link. Create, never Set: the document id IS the
// (provider, provider_user_id) primary key, so a second local user claiming the
// same provider identity loses at the server as sdk.ErrAlreadyExists instead of
// taking the link over.
func putOAuthAccount(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row oauthAccountDoc) error {
	return w.Create(ctx, oauthAccountRef(db, row.Provider, row.ProviderUserID), row)
}

// dropOAuthAccounts enqueues the deletion of every link in rows. It takes
// ALREADY-READ documents and reads nothing, so a caller can finish its read
// phase first and still unlink atomically.
func dropOAuthAccounts(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, rows []oauthAccountDoc) error {
	for _, row := range rows {
		if err := w.Delete(ctx, oauthAccountRef(db, row.Provider, row.ProviderUserID)); err != nil {
			return err
		}
	}
	return nil
}

// readOAuthAccount reads one link by its provider identity; absent is
// sdk.ErrNotFound.
func readOAuthAccount(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, provider, providerUserID string) (oauthAccountDoc, error) {
	snap, err := r.Get(ctx, oauthAccountRef(db, provider, providerUserID))
	if err != nil {
		return oauthAccountDoc{}, err
	}
	return decodeOAuthAccount(snap)
}

// queryOAuthAccounts runs one link query and decodes every document it returns,
// consuming iterator.Done as the loop terminator and mapping every other Next
// error HERE, at the iteration boundary.
func queryOAuthAccounts(ctx context.Context, r firestoredb.Reader, q gcfs.Query) ([]oauthAccountDoc, error) {
	it := r.Documents(ctx, q)
	defer it.Stop()

	var out []oauthAccountDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		row, err := decodeOAuthAccount(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
}

// decodeOAuthAccount turns one snapshot into a link document.
func decodeOAuthAccount(snap *gcfs.DocumentSnapshot) (oauthAccountDoc, error) {
	var row oauthAccountDoc
	if err := snap.DataTo(&row); err != nil {
		return oauthAccountDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionOAuthAccounts, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// toDomain projects the link document onto the domain entity.
func (d oauthAccountDoc) toDomain() (oauthaccount.OAuthAccount, error) {
	tokenExpiresAt, err := firestoredb.ParseNullTime(d.TokenExpiresAt)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return oauthaccount.OAuthAccount{
		Provider:              d.Provider,
		ProviderUserID:        d.ProviderUserID,
		UserID:                d.UserID,
		ProviderEmail:         d.ProviderEmail,
		ProviderEmailVerified: d.ProviderEmailVerified,
		AccountVerified:       d.AccountVerified,
		LinkedAt:              d.LinkedAt,
		AccessToken:           d.AccessToken,
		RefreshToken:          d.RefreshToken,
		TokenExpiresAt:        tokenExpiresAt,
		TokenType:             d.TokenType,
		Scope:                 d.Scope,
	}, nil
}

// newOAuthStateDoc builds the document for a flow secret being CREATED. The
// payload is opaque text stored verbatim — the service owns its shape.
func newOAuthStateDoc(st oauthstate.State) oauthStateDoc {
	return oauthStateDoc{
		Token:     st.Token,
		Provider:  st.Provider,
		Purpose:   st.Purpose,
		Payload:   string(st.Payload),
		ExpiresAt: firestoredb.TruncateTime(st.ExpiresAt),
	}
}

// putOAuthState writes a NEW flow secret. Create, never Set: the token is the
// primary key, and a re-minted token must not silently replace a live one.
func putOAuthState(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row oauthStateDoc) error {
	return w.Create(ctx, oauthStateRef(db, row.Token), row)
}

// dropOAuthState enqueues the single-use deletion.
func dropOAuthState(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, token string) error {
	return w.Delete(ctx, oauthStateRef(db, token))
}

// readOAuthState reads one flow secret by token; absent is sdk.ErrNotFound.
func readOAuthState(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, token string) (oauthStateDoc, error) {
	snap, err := r.Get(ctx, oauthStateRef(db, token))
	if err != nil {
		return oauthStateDoc{}, err
	}
	var row oauthStateDoc
	if err := snap.DataTo(&row); err != nil {
		return oauthStateDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionOAuthStates, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// toDomain projects the flow-secret document onto the domain entity. An empty
// payload reads back as a nil slice, matching the SQL adapters.
func (d oauthStateDoc) toDomain() oauthstate.State {
	var payload []byte
	if d.Payload != "" {
		payload = []byte(d.Payload)
	}
	return oauthstate.State{
		Token:     d.Token,
		Provider:  d.Provider,
		Purpose:   d.Purpose,
		Payload:   payload,
		ExpiresAt: d.ExpiresAt,
	}
}
