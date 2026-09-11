package firestore

import (
	"context"
	"errors"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	_ session.SessionRepository    = (*sessionStore)(nil)
	_ session.ActiveUserRepository = (*activeSessionStore)(nil)
)

// sessionStore fills session.SessionRepository over the session collection and
// the current-refresh-hash claim (SCHEMA.md §5.3). Every read and write goes
// through sessions_doc.go, which owns the row and the claim together.
type sessionStore struct {
	db *firestoredb.DB
}

func newSessionStore(db *firestoredb.DB) *sessionStore {
	return &sessionStore{db: db}
}

// Create persists a new session and claims its current refresh hash; a
// collision is sdk.ErrAlreadyExists.
//
// It performs NO READ. Both documents it writes must not already exist — the
// row's id IS the session's primary key and the claim's id IS the unique
// refresh hash — and both are written with Create, whose precondition the
// SERVER evaluates at commit, so a lost race rolls the pair back whole (ruling
// R3). The transaction is what makes the row and its claim ONE write.
func (s *sessionStore) Create(ctx context.Context, sess session.Session) (session.Session, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, err
	}
	row, err := newSessionDoc(sess)
	if err != nil {
		return session.Session{}, err
	}
	err = retryTransact(ctx, s.db, func(ctx context.Context) error {
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := putSession(ctx, s.db, w, plan, row); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		return session.Session{}, err
	}
	return sess, nil
}

// Get returns the live session: unknown → sdk.ErrNotFound, expired →
// sdk.ErrExpired at the read clock. The row is left in place — the port allows a
// store to delete it but does not require it, and deleting here would also have
// to release the claim, which is a write on a read path.
func (s *sessionStore) Get(ctx context.Context, id string) (session.Session, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, err
	}
	row, err := readSession(ctx, s.db, s.db.ReaderFrom(ctx), id)
	if err != nil {
		return session.Session{}, err
	}
	sess, err := row.toDomain()
	if err != nil {
		return session.Session{}, err
	}
	if sess.Expired(time.Now()) {
		return session.Session{}, sdk.ErrExpired
	}
	return sess, nil
}

// GetByRefreshHash returns the session whose CURRENT or PREVIOUS hash equals
// hash, verbatim (no expiry filter — expiry is a service branch), reporting
// which slot matched. An empty hash never matches: a fresh row's grace slot is
// an explicit null, and answering it would hand out an arbitrary session.
//
// The two slots take different paths because they are different mechanisms. The
// current hash is UNIQUE, so it has a claim document and the claim IS the access
// path — one point read resolves the credential to its row. The grace hash is
// not unique and has no claim, so it is an equality query on the row's own
// field, which is what the non-unique SQL index serves.
//
// Both run under ONE ReadSnapshot, for the reason GetLogin does: the claim and
// the row it names are two documents describing one fact, and a rotation landing
// between the reads would otherwise report a live credential as unknown.
func (s *sessionStore) GetByRefreshHash(ctx context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, 0, err
	}
	if hash == "" {
		return session.Session{}, 0, sdk.ErrNotFound
	}

	var (
		row   sessionDoc
		match session.RefreshMatch
	)
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		match = session.RefreshMatchCurrent
		current, err := readSessionByRefreshClaim(ctx, s.db, r, hash)
		if err == nil {
			row = current
			return nil
		}
		if !errors.Is(err, sdk.ErrNotFound) {
			return err
		}

		grace, err := querySessions(ctx, r, sessionByPreviousHashQuery(s.db, hash))
		if err != nil {
			return err
		}
		if len(grace) == 0 {
			return sdk.ErrNotFound
		}
		row, match = grace[0], session.RefreshMatchPrevious
		return nil
	})
	if err != nil {
		return session.Session{}, 0, err
	}
	sess, err := row.toDomain()
	if err != nil {
		return session.Session{}, 0, err
	}
	return sess, match, nil
}

// Rotate compare-and-swaps the live refresh token, moving the expected current
// hash into the grace slot and re-claiming the new one, without touching
// expires_at. A lost CAS is session.ErrRotationConflict.
//
// The CAS is the SQL adapters' `WHERE id = ? AND refresh_token_hash = ?`
// evaluated in Go against the row read INSIDE the transaction, which locks it: a
// concurrent rotation either aborts this one (and it re-reads) or loses to it. An
// unknown id is a conflict rather than sdk.ErrNotFound, because the SQL contract
// is "zero rows affected", and the service's one job here is to tell a benign
// concurrent rotation from token reuse.
//
// The new hash's claim is TAKEN, not read. If another session holds it the
// Create fails AlreadyExists at commit and the whole rotation rolls back as
// sdk.ErrAlreadyExists — the same arbitration Create uses, and a read would be
// both slower and weaker (ruling R3).
func (s *sessionStore) Rotate(ctx context.Context, id, expectedCurrentHash, newHash string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		row, err := readSession(ctx, s.db, s.db.ReaderFrom(ctx), id)
		if errors.Is(err, sdk.ErrNotFound) {
			return session.ErrRotationConflict
		}
		if err != nil {
			return err
		}
		if row.RefreshTokenHash != expectedCurrentHash {
			return session.ErrRotationConflict
		}

		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := updateSession(ctx, s.db, w, plan, row, row.rotated(expectedCurrentHash, newHash)); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
}

// ConsumeGrace flips the grace slot's consumed flag under CAS, RETAINING the
// previous hash so a later reuse still resolves the session for revocation.
//
// The CAS is on BOTH conjuncts of the SQL predicate — the stored previous hash
// equals previousHash AND previous_used is still false — so the second arrival
// on a spent slot is ErrRotationConflict, which is the signal the service reads
// as token reuse. An empty previousHash never matches, mirroring SQL's rule
// that NULL equals no value at all (and a fresh row's slot is an explicit null).
//
// The hash is RETAINED deliberately: dropping it would make the next reuse of
// that token unresolvable, and an unresolvable reuse cannot revoke the session it
// belongs to. It claims nothing, so retaining it costs no uniqueness.
func (s *sessionStore) ConsumeGrace(ctx context.Context, id, previousHash string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		row, err := readSession(ctx, s.db, s.db.ReaderFrom(ctx), id)
		if errors.Is(err, sdk.ErrNotFound) {
			return session.ErrRotationConflict
		}
		if err != nil {
			return err
		}
		stored, err := row.previousHash()
		if err != nil {
			return err
		}
		if previousHash == "" || stored != previousHash || row.PreviousUsed {
			return session.ErrRotationConflict
		}

		next := row
		next.PreviousUsed = true

		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := updateSession(ctx, s.db, w, plan, row, next); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
}

// Delete removes the session and releases its hash claim; unknown →
// sdk.ErrNotFound. It reads the row first because the claim's id is derived from
// the CURRENT hash, which only the row carries.
func (s *sessionStore) Delete(ctx context.Context, id string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		row, err := readSession(ctx, s.db, s.db.ReaderFrom(ctx), id)
		if err != nil {
			return err
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := dropSession(ctx, s.db, w, plan, row); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
}

// DeleteByUser removes every session of userID and their claims. It is bulk and
// idempotent: zero matches is nil, never sdk.ErrNotFound, because it is the
// logout-everywhere primitive a password change calls.
//
// The whole cascade is ONE transaction: reading the set and deleting it must not
// be split, or a session minted between two commits would survive a revocation
// that reported success.
func (s *sessionStore) DeleteByUser(ctx context.Context, userID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		rows, err := readSessionsForUser(ctx, s.db, s.db.ReaderFrom(ctx), userID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := dropSessionsForUser(ctx, s.db, w, plan, rows); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
}

// activeSessionStore fills session.ActiveUserRepository: the fenced mint that
// proves the owner active inside the transaction that inserts the session.
type activeSessionStore struct {
	db *firestoredb.DB
}

func newActiveSessionStore(db *firestoredb.DB) *activeSessionStore {
	return &activeSessionStore{db: db}
}

// CreateForActiveUser inserts sess only while its owning user is active:
// unknown → sdk.ErrNotFound, deactivated → session.ErrUserNotActive with nothing
// written.
//
// The fence is the READ SET. This transaction reads the user document, so
// Firestore locks it for the transaction's duration: a concurrent
// UserAdmin.SetStatus, which reads and writes that same document, cannot commit
// between the status proof and the insert. Exactly one of the port's two legal
// outcomes therefore wins — the mint commits first and the transition then
// deletes its session, or the transition commits first and this read observes
// the deactivated status and refuses. A service-level Users.Get followed by an
// ordinary Sessions.Create is precisely what the port forbids, and it is not
// what this is.
func (s *activeSessionStore) CreateForActiveUser(ctx context.Context, sess session.Session, expectedAuthRevision int64) (session.Session, error) {
	if err := refuseAmbient(ctx); err != nil {
		return session.Session{}, err
	}
	row, err := newSessionDoc(sess)
	if err != nil {
		return session.Session{}, err
	}
	err = retryTransact(ctx, s.db, func(ctx context.Context) error {
		owner, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), sess.UserID)
		if err != nil {
			return err
		}
		if !user.NormalizeStatus(user.Status(owner.Status)).Active() {
			return session.ErrUserNotActive
		}

		if owner.AuthRevision != expectedAuthRevision {
			return sdk.ErrConflict
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := putSession(ctx, s.db, w, plan, row); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		return session.Session{}, err
	}
	return sess, nil
}
