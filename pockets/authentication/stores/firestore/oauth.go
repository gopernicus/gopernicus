package firestore

import (
	"context"
	"errors"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
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

// Create links a provider identity only while its owner is active. The provider
// document ID arbitrates uniqueness inside the transaction.
func (s *oauthAccountStore) Create(ctx context.Context, a oauthaccount.OAuthAccount) (oauthaccount.OAuthAccount, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	if err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		owner, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), a.UserID)
		if err != nil {
			return err
		}
		if !user.NormalizeStatus(user.Status(owner.Status)).Active() {
			return session.ErrUserNotActive
		}
		return putOAuthAccount(ctx, s.db, s.db.WriterFrom(ctx), newOAuthAccountDoc(a))
	}); err != nil {
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
		owner, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), userID)
		if err != nil {
			return err
		}
		if !user.NormalizeStatus(user.Status(owner.Status)).Active() {
			return session.ErrUserNotActive
		}
		rows, err := queryOAuthAccounts(ctx, s.db.ReaderFrom(ctx), oauthAccountsForUserProviderQuery(s.db, userID, provider))
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return sdk.ErrNotFound
		}
		revoke, err := readCredentialRevocations(ctx, s.db, userID)
		if err != nil {
			return err
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := dropOAuthAccounts(ctx, s.db, w, rows); err != nil {
			return err
		}
		if err := advanceCredentialRevision(ctx, s.db, w, userID, owner.AuthRevision+1, time.Now()); err != nil {
			return err
		}
		if err := revoke.apply(ctx, s.db, w, plan); err != nil {
			return err
		}
		return plan.commit(ctx, w)
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

func (s *oauthAccountStore) Link(ctx context.Context, a oauthaccount.OAuthAccount, expectedAuthRevision int64, adoptIdentifierID string, now time.Time) (oauthaccount.OAuthAccount, int64, error) {
	if err := refuseAmbient(ctx); err != nil {
		return oauthaccount.OAuthAccount{}, 0, err
	}
	var revision int64
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		owner, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), a.UserID)
		if err != nil {
			return err
		}
		if !user.NormalizeStatus(user.Status(owner.Status)).Active() {
			return session.ErrUserNotActive
		}
		if owner.AuthRevision != expectedAuthRevision {
			return sdk.ErrConflict
		}
		var old, next identifierDoc
		var projection emailProjection
		var revoke credentialRevocations
		if adoptIdentifierID != "" {
			old, err = readIdentifier(ctx, s.db, s.db.ReaderFrom(ctx), adoptIdentifierID)
			if err != nil {
				return err
			}
			domain, err := old.toDomain()
			if err != nil {
				return err
			}
			if old.UserID != a.UserID || !domain.Active() || (!domain.LoginEnabled && !domain.RecoveryEnabled) || domain.Kind != identifier.KindEmail {
				return sdk.ErrConflict
			}
			next = old
			next.VerifiedAt = firestoredb.TruncateTime(now)
			next.UpdatedAt = firestoredb.TruncateTime(now)
			identifiers, err := queryIdentifiers(ctx, s.db.ReaderFrom(ctx), activeIdentifiersQuery(s.db, a.UserID))
			if err != nil {
				return err
			}
			projection, err = resolveEmailProjection(append(identifiers, next)...)
			if err != nil {
				return err
			}
			revoke, err = readCredentialRevocations(ctx, s.db, a.UserID)
			if err != nil {
				return err
			}
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if adoptIdentifierID != "" {
			if err := updateIdentifier(ctx, s.db, w, plan, old, next); err != nil {
				return err
			}
			if err := dropPassword(ctx, s.db, w, a.UserID); err != nil {
				return err
			}
			if err := revoke.apply(ctx, s.db, w, plan); err != nil {
				return err
			}
		}
		if err := putOAuthAccount(ctx, s.db, w, newOAuthAccountDoc(a)); err != nil {
			return err
		}
		revision = owner.AuthRevision + 1
		if adoptIdentifierID != "" {
			if err := advanceUserRevision(ctx, s.db, w, a.UserID, revision, now, projection); err != nil {
				return err
			}
		} else if err := advanceCredentialRevision(ctx, s.db, w, a.UserID, revision, now); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		return oauthaccount.OAuthAccount{}, 0, err
	}
	return a, revision, nil
}
