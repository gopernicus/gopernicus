package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ authgrant.Repository = (*authGrantStore)(nil)

// authGrantStore fills authgrant.Repository over the step-up grant collection,
// selected by the derived consume key (SCHEMA.md §4.1). It owns no claim — the
// SQL index it mirrors is not unique — and every reference goes through
// grants_doc.go, whose revocation helpers N2b's SetStatus reuses.
type authGrantStore struct {
	db *firestoredb.DB
}

func newAuthGrantStore(db *firestoredb.DB) *authGrantStore {
	return &authGrantStore{db: db}
}

// Create persists a step-up grant, minting the id when the caller sends none.
//
// One document, no read, no transaction: a single-document Create is already
// atomic and there is no claim to keep beside it.
func (s *authGrantStore) Create(ctx context.Context, g authgrant.Grant) (authgrant.Grant, error) {
	if err := refuseAmbient(ctx); err != nil {
		return authgrant.Grant{}, err
	}
	row, err := newAuthGrantDoc(g)
	if err != nil {
		return authgrant.Grant{}, err
	}
	if err := putAuthGrant(ctx, s.db, s.db.WriterFrom(ctx), row); err != nil {
		return authgrant.Grant{}, err
	}
	g.ID = row.ID
	return g, nil
}

// Consume spends the oldest unspent grant matching (session, purpose, context)
// in one transaction. An expired grant's consumption COMMITS, then
// sdk.ErrExpired is returned; no unconsumed match → sdk.ErrNotFound.
//
// Expiry, single use, session binding, and context mismatch are all decided by
// that one selection, which is what makes the operation atomic: a grant earned
// for one context is simply not in the match set of another, and a grant already
// spent is not in any. Committing the expired consume is the port's contract —
// an expired grant is still SPENT, so a replay cannot retry it — and it is why
// the expired outcome is reported after the transaction rather than returned
// from the callback, which would roll the consumption back.
func (s *authGrantStore) Consume(ctx context.Context, sessionID, purpose, contextDigest string, now time.Time) (authgrant.Grant, error) {
	if err := refuseAmbient(ctx); err != nil {
		return authgrant.Grant{}, err
	}
	var out grantConsumption
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return consumeGrant(ctx, s.db, grantConsumeKey(sessionID, purpose, contextDigest), now, &out)
	})
	if err != nil {
		return authgrant.Grant{}, err
	}
	switch {
	case !out.found:
		return authgrant.Grant{}, sdk.ErrNotFound
	case out.expired:
		return authgrant.Grant{}, sdk.ErrExpired
	}
	return out.grant, nil
}

// DeleteBySession removes every grant bound to the session — the revocation
// cascade a session deletion carries. Bulk and idempotent: zero matches is nil.
//
// Reading the set and deleting it is ONE transaction, so a grant issued between
// the two could not survive a cascade that reported success.
func (s *authGrantStore) DeleteBySession(ctx context.Context, sessionID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		rows, err := readGrantsForSession(ctx, s.db, s.db.ReaderFrom(ctx), sessionID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return dropAuthGrants(ctx, s.db, s.db.WriterFrom(ctx), rows)
	})
}

// grantConsumption is ONE attempt's outcome of a consume: the grant that was
// spent, and what the caller must report AFTER the transaction commits.
type grantConsumption struct {
	grant   authgrant.Grant
	found   bool
	expired bool
}

// consumeGrant is one attempt of the spend: it RESETS the outcome, selects the
// oldest unspent match, records what it found, and queues the consumed_at write.
//
// The reset is the load-bearing line, for the reason consumeState states: a
// Firestore transaction callback may run more than once, and an attempt that
// observed an expired grant and then lost its commit race must not leave
// "expired" behind for the attempt that finds nothing — which is exactly what
// eight concurrent consumers of one grant produce. No match is NOT an error
// here: the callback returns nil, commits nothing, and Consume reports
// sdk.ErrNotFound from the outcome.
func consumeGrant(ctx context.Context, db *firestoredb.DB, consumeKey string, now time.Time, out *grantConsumption) error {
	*out = grantConsumption{}

	rows, err := queryAuthGrants(ctx, db.ReaderFrom(ctx), unspentGrantQuery(db, consumeKey))
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	g, err := rows[0].toDomain()
	if err != nil {
		return err
	}
	g.ConsumedAt = firestoredb.TruncateTime(now)
	out.grant, out.found, out.expired = g, true, g.Expired(now)
	return spendAuthGrant(ctx, db, db.WriterFrom(ctx), rows[0], now)
}
