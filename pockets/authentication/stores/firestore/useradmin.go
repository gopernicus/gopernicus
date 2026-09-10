package firestore

import (
	"context"
	"fmt"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ user.AdminRepository = (*userAdminStore)(nil)

// userAdminStore fills user.AdminRepository: the operator directory and the
// atomic lifecycle transition. Both directory reads answer from the users
// document's PROJECTION fields (SCHEMA.md §6) — Firestore has no join, and one
// identifier read per listed user would make a page O(page) round trips. Bodies
// land in N2b (SetStatus) and N2c (List/GetSummary).
type userAdminStore struct {
	db *firestoredb.DB
}

func newUserAdminStore(db *firestoredb.DB) *userAdminStore {
	return &userAdminStore{db: db}
}

// List pages the directory, ordered (created_at DESC, id DESC) by default.
//
// It is ONE query over the users collection and reads NOTHING else: the active
// primary email and its verification come from the row's own projection fields,
// which is how this store satisfies the port's explicit rule that an
// implementation must not issue one identifier read per user (SCHEMA.md §6.1).
//
// A non-blank req.Search is refused with sdk.ErrInvalidInput by the connector's
// List, because this ListQuery declares no PostFilter: user.OrderFields exposes
// nothing searchable, and a top-level users-directory search is a plan-level
// decision rather than an accidental full collection scan (ruling R4).
func (s *userAdminStore) List(ctx context.Context, req crud.ListRequest) (crud.Page[user.Summary], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[user.Summary]{}, err
	}
	return firestoredb.List(ctx, s.db.ReaderFrom(ctx), listUsers(s.db), req)
}

// GetSummary returns one user's directory projection, or sdk.ErrNotFound. It
// reads the SAME projection the page does, from the same document, so the two
// can never disagree.
func (s *userAdminStore) GetSummary(ctx context.Context, id string) (user.Summary, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.Summary{}, err
	}
	row, err := readUser(ctx, s.db, s.db.ReaderFrom(ctx), id)
	if err != nil {
		return user.Summary{}, err
	}
	return row.summary()
}

// SetStatus transitions the lifecycle status in ONE transaction: the status and
// its timestamp, auth_revision + 1, and the deletion of every session (with its
// refresh claim) and every grant owned by the user or bound to those sessions.
// Replaying the same status changes nothing and revokes nothing.
//
// An invalid status is refused BEFORE the transaction opens (user.ErrInvalidStatus
// wrapping sdk.ErrInvalidInput), so a rejected value cannot have written
// anything — the port states that as its own case, and the closed vocabulary is
// the domain's, not this store's.
//
// The fence against a concurrent mint is the READ SET: this transaction reads
// AND writes the users document, and session.ActiveUserRepository.CreateForActiveUser
// reads that same document to prove the subject is active. Firestore therefore
// serializes the two — either the mint commits first and the revocation below
// deletes its session, or the transition commits first and the mint re-reads a
// deactivated subject and refuses. That is the same guarantee turso obtains from
// BEGIN IMMEDIATE and pgx from row locking.
func (s *userAdminStore) SetStatus(ctx context.Context, id string, status user.Status, now time.Time) (user.StatusChange, error) {
	if err := refuseAmbient(ctx); err != nil {
		return user.StatusChange{}, err
	}
	if !status.Valid() {
		return user.StatusChange{}, fmt.Errorf("authentication firestore store: %q: %w", status, user.ErrInvalidStatus)
	}

	var out user.StatusChange
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return s.transition(ctx, id, status, now, &out)
	})
	if err != nil {
		return user.StatusChange{}, err
	}
	return out, nil
}

// transition is ONE attempt at the lifecycle change, reads strictly before
// writes, recording its outcome in out.
//
// out is RESET first. A Firestore callback may run more than once, and the
// outcome an aborted attempt computed — how many sessions IT saw — is not the
// outcome of the transaction that commits. Leaving a previous attempt's count in
// place would report a revocation that never happened.
//
// The read phase is the whole revocation set, because the vendor refuses any
// read issued after the transaction's first write: the users document, every
// session of the user (each carries the refresh-hash claim its deletion must
// release), and every grant the user owns or that is bound to one of those
// sessions. Reading them is also what makes them the CONTENTION set.
func (s *userAdminStore) transition(ctx context.Context, id string, status user.Status, now time.Time, out *user.StatusChange) error {
	*out = user.StatusChange{}
	r := s.db.ReaderFrom(ctx)

	row, err := readUser(ctx, s.db, r, id)
	if err != nil {
		return err
	}
	changedAt, err := firestoredb.ParseNullTime(row.StatusChangedAt)
	if err != nil {
		return err
	}
	// The idempotent replay: the desired status is already the stored one, so
	// nothing is written — no revision increment, no revocation — and the
	// previously recorded transition time is reported unchanged.
	if user.NormalizeStatus(user.Status(row.Status)) == status {
		*out = user.StatusChange{Status: status, ChangedAt: changedAt}
		return nil
	}

	live, err := readSessionsForUser(ctx, s.db, r, id)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(live))
	for _, sess := range live {
		ids = append(ids, sess.ID)
	}
	grants, err := readGrantsForRevocation(ctx, s.db, r, id, ids)
	if err != nil {
		return err
	}

	// WRITE PHASE. Nothing below reads. The status, the revision, the sessions
	// with their claims, and the grants commit together or not at all: an
	// update-then-best-effort-delete would leave a deactivated subject holding
	// live credentials after a crash, which is the failure the port names.
	w := s.db.WriterFrom(ctx)
	plan := newClaimPlan()
	if err := transitionUserStatus(ctx, s.db, w, id, status, row.AuthRevision+1, now); err != nil {
		return err
	}
	if err := dropAuthGrants(ctx, s.db, w, grants); err != nil {
		return err
	}
	if err := dropSessionsForUser(ctx, s.db, w, plan, live); err != nil {
		return err
	}
	if err := plan.commit(ctx, w); err != nil {
		return err
	}

	*out = user.StatusChange{
		Status:          status,
		Changed:         true,
		ChangedAt:       firestoredb.TruncateTime(now),
		RevokedSessions: len(live),
	}
	return nil
}
