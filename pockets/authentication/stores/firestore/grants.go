package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
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

// Create reads the owning user and session in the same transaction as the grant
// insertion. Their read set fences revocation and the expected credential revision.
// The grant ID is assigned once before retries.
func (s *authGrantStore) Create(ctx context.Context, g authgrant.Grant, expectedAuthRevision int64, now time.Time) (authgrant.Grant, error) {
	if err := refuseAmbient(ctx); err != nil {
		return authgrant.Grant{}, err
	}
	row, err := newAuthGrantDoc(g)
	if err != nil {
		return authgrant.Grant{}, err
	}
	if err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		revision, err := s.checkGrantSession(ctx, g.SessionID, g.UserID, now)
		if err != nil {
			return err
		}
		if revision != expectedAuthRevision {
			return sdk.ErrConflict
		}
		return putAuthGrant(ctx, s.db, s.db.WriterFrom(ctx), row)
	}); err != nil {
		return authgrant.Grant{}, err
	}
	g.ID = row.ID
	return g, nil
}

// Consume checks the live session and spends the oldest matching grant that is
// expired or satisfies the requested age/assurance, all in one transaction. An expired grant's consumption COMMITS, then
// sdk.ErrExpired is returned; no unconsumed match → sdk.ErrNotFound.
//
// Expiry, single use, session binding, and context mismatch are all decided by
// that one selection, which is what makes the operation atomic: a grant earned
// for one context is simply not in the match set of another, and a grant already
// spent is not in any. Committing the expired consume is the port's contract —
// an expired grant is still SPENT, so a replay cannot retry it — and it is why
// the expired outcome is reported after the transaction rather than returned
// from the callback, which would roll the consumption back.
func (s *authGrantStore) Consume(ctx context.Context, requirement authgrant.Requirement, now time.Time) (authgrant.Grant, error) {
	if err := refuseAmbient(ctx); err != nil {
		return authgrant.Grant{}, err
	}
	if err := requirement.Validate(); err != nil {
		return authgrant.Grant{}, err
	}
	var out grantConsumption
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		if _, err := s.checkGrantSession(ctx, requirement.SessionID, requirement.UserID, now); err != nil {
			return err
		}
		return consumeGrant(ctx, s.db, requirement, now, &out)
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
func consumeGrant(ctx context.Context, db *firestoredb.DB, requirement authgrant.Requirement, now time.Time, out *grantConsumption) error {
	*out = grantConsumption{}
	query := unspentGrantQuery(db, grantConsumeKey(requirement.SessionID, requirement.Purpose, requirement.ContextDigest)).Limit(32)
	for {
		rows, err := queryAuthGrants(ctx, db.ReaderFrom(ctx), query)
		if err != nil {
			return err
		}
		for _, row := range rows {
			g, err := row.toDomain()
			if err != nil {
				return err
			}
			if g.UserID != requirement.UserID || (!g.Expired(now) && !requirement.Accepts(g.AuthenticatedAt, g.Assurance, now)) {
				continue
			}
			g.ConsumedAt = firestoredb.TruncateTime(now)
			out.grant, out.found, out.expired = g, true, g.Expired(now)
			return spendAuthGrant(ctx, db, db.WriterFrom(ctx), row, now)
		}
		if len(rows) < 32 {
			return nil
		}
		last := rows[len(rows)-1]
		query = query.StartAfter(last.CreatedAt, last.ID)
	}
}

func (s *authGrantStore) checkGrantSession(ctx context.Context, sessionID, userID string, now time.Time) (int64, error) {
	r := s.db.ReaderFrom(ctx)
	owner, err := readUser(ctx, s.db, r, userID)
	if err != nil {
		return 0, err
	}
	if !user.NormalizeStatus(user.Status(owner.Status)).Active() {
		return 0, sdk.ErrUnauthorized
	}
	sess, err := readSession(ctx, s.db, r, sessionID)
	if err != nil {
		return 0, err
	}
	if sess.UserID != userID || !now.Before(sess.ExpiresAt) {
		return 0, sdk.ErrUnauthorized
	}
	return owner.AuthRevision, nil
}
