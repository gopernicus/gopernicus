package firestore

import (
	"context"
	"errors"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	_ oauthaccount.OAuthAccountRepository = (*oauthAccountStore)(nil)
	_ oauthstate.StateRepository          = (*oauthStateStore)(nil)
)

// oauthAccountStore fills oauthaccount.OAuthAccountRepository over the account
// collection, whose document id IS the (provider, provider_user_id) primary key
// — so provider uniqueness needs no claim (SCHEMA.md §5.8). Every reference goes
// through oauth_doc.go.
type oauthAccountStore struct {
	db *firestoredb.DB
}

func newOAuthAccountStore(db *firestoredb.DB) *oauthAccountStore {
	return &oauthAccountStore{db: db}
}

// Create links a provider identity to a local user; a duplicate provider
// identity is sdk.ErrAlreadyExists at the server.
//
// It writes ONE document and reads none, so it takes no transaction: a
// single-document Create is already atomic, and its precondition IS the
// uniqueness arbitration (ruling R3). refuseAmbient has already established that
// no host transaction is in play.
func (s *oauthAccountStore) Create(ctx context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	if err := putOAuthAccount(ctx, s.db, s.db.WriterFrom(ctx), newOAuthAccountDoc(a)); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return a, nil
}

// GetByProvider reads the link by its (provider, provider_user_id) id; unknown
// → sdk.ErrNotFound.
func (s *oauthAccountStore) GetByProvider(ctx context.Context, provider, providerUserID string) (oauthaccount.OAuthAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	row, err := readOAuthAccount(ctx, s.db, s.db.ReaderFrom(ctx), provider, providerUserID)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return row.toDomain()
}

// ListByUser returns the user's links, ordered (linked_at DESC,
// provider_user_id DESC) — the SQL adapters' order. No links is an EMPTY slice
// with a nil error, never sdk.ErrNotFound.
func (s *oauthAccountStore) ListByUser(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	rows, err := queryOAuthAccounts(ctx, s.db.ReaderFrom(ctx), oauthAccountsForUserQuery(s.db, userID))
	if err != nil {
		return nil, err
	}
	out := make([]oauthaccount.OAuthAccount, 0, len(rows))
	for _, row := range rows {
		a, err := row.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Delete unlinks (userID, provider); no match → sdk.ErrNotFound.
//
// It is a QUERY rather than a point delete because the document id needs
// provider_user_id, which the port does not give it — the caller unlinks "this
// user's Google account", not a specific provider subject. Reading the match set
// and deleting it is therefore read-then-write and takes one transaction, so a
// link created between the two could not survive a delete that reported success.
func (s *oauthAccountStore) Delete(ctx context.Context, userID, provider string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		rows, err := queryOAuthAccounts(ctx, s.db.ReaderFrom(ctx), oauthAccountsForUserProviderQuery(s.db, userID, provider))
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return sdk.ErrNotFound
		}
		return dropOAuthAccounts(ctx, s.db, s.db.WriterFrom(ctx), rows)
	})
}

// oauthStateStore fills oauthstate.StateRepository: single-use flow secrets
// keyed by token, consumed by a transactional get-and-delete.
type oauthStateStore struct {
	db *firestoredb.DB
}

func newOAuthStateStore(db *firestoredb.DB) *oauthStateStore {
	return &oauthStateStore{db: db}
}

// Create persists a one-time state. One document, no read, no transaction (see
// the account store's Create).
func (s *oauthStateStore) Create(ctx context.Context, st oauthstate.State) (oauthstate.State, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthstate.State{}, err
	}
	if err := putOAuthState(ctx, s.db, s.db.WriterFrom(ctx), newOAuthStateDoc(st)); err != nil {
		return oauthstate.State{}, err
	}
	return st, nil
}

// Consume deletes and returns the state. The deletion COMMITS even when the row
// was expired; sdk.ErrExpired is reported afterwards (N-D2).
//
// That ordering is the port's contract, not an optimization: the row is deleted
// REGARDLESS of expiry, so a second Consume of any token is sdk.ErrNotFound. It
// is also why the expired outcome cannot be the callback's error — returning it
// from the callback would roll the deletion back and leave an expired secret
// replayable forever.
func (s *oauthStateStore) Consume(ctx context.Context, token string) (oauthstate.State, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthstate.State{}, err
	}
	var out stateConsumption
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return consumeState(ctx, s.db, token, &out)
	})
	if err != nil {
		return oauthstate.State{}, err
	}
	switch {
	case !out.found:
		return oauthstate.State{}, sdk.ErrNotFound
	case out.expired:
		return oauthstate.State{}, sdk.ErrExpired
	}
	return out.state, nil
}

// stateConsumption is ONE attempt's outcome of a single-use consume: what was
// read, and what the caller must report AFTER the transaction commits. It is a
// value rather than three returns because the transaction callback can only
// answer commit-or-roll-back, and this is the other half of the answer.
type stateConsumption struct {
	state   oauthstate.State
	found   bool
	expired bool
}

// consumeState is one attempt of the get-and-delete: it RESETS the outcome,
// reads the row, records what it found, and queues the deletion.
//
// The reset is the load-bearing line. A Firestore transaction callback may run
// more than once, and an attempt that observed an expired row and then lost its
// commit race must not leave "expired" behind for the attempt that finds nothing
// — that is a rolled-back attempt's opinion reported as a committed outcome. An
// absent row is NOT an error here: the callback returns nil, commits nothing,
// and Consume reports sdk.ErrNotFound from the outcome.
func consumeState(ctx context.Context, db *firestoredb.DB, token string, out *stateConsumption) error {
	*out = stateConsumption{}

	row, err := readOAuthState(ctx, db, db.ReaderFrom(ctx), token)
	if errors.Is(err, sdk.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	st := row.toDomain()
	out.state, out.found, out.expired = st, true, st.Expired(time.Now())
	return dropOAuthState(ctx, db, db.WriterFrom(ctx), token)
}
