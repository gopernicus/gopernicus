package firestore

import (
	"context"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// The users collection's owner file: every reference to it, every decode of it,
// and every write of it lives here (ownership_test.go refuses any other
// non-test file to so much as name the collection). The reason is the DIRECTORY
// PROJECTION: the document carries two fields the domain user.User has no place
// for, so a write path that built a users document from a domain value would
// blank them. There is exactly ONE whole-document write here — putUser, which
// takes the projection explicitly — and every other write is a FIELD update
// (SCHEMA.md §6.2).

// The users field paths a field update addresses. The two projection fields are
// deliberately NOT here: they belong to projection.go, which is the only place
// allowed to name them.
const (
	fieldUserDisplayName     = "display_name"
	fieldUserUpdatedAt       = "updated_at"
	fieldUserAuthRevision    = "auth_revision"
	fieldUserStatus          = "status"
	fieldUserStatusChangedAt = "status_changed_at"
)

// userRef is the users document for a user id. The document id is the KeyHash of
// the id, and the id itself is kept in a field (SCHEMA.md §4.2).
func userRef(db *firestoredb.DB, userID string) *gcfs.DocumentRef {
	return db.Doc(collectionUsers, userDocID(userID))
}

// usersQuery is the unfiltered collection — the directory listing's base.
func usersQuery(db *firestoredb.DB) gcfs.Query {
	return db.Collection(collectionUsers).Query
}

// newUserDoc builds the document for a user being CREATED, minting the id when
// the caller left it empty (the greenfield cryptids.Database convention, which
// the SQL adapters serve with `RETURNING id`).
//
// It is called INSIDE the transaction callback so a retried attempt mints a
// fresh id rather than reusing one a losing attempt claimed. The directory
// projection is NOT set here — putUser takes it, so the create path cannot
// forget it.
func newUserDoc(u user.User) userDoc {
	id := u.ID
	if id == "" {
		id = firestoredb.NewID()
	}
	return userDoc{
		ID:          id,
		DisplayName: u.DisplayName,
		// status is written explicitly rather than defaulted, so the persisted
		// posture is the one the domain constructed. A zero-value Status (a
		// caller building a User by hand) normalizes to active, matching the
		// reader — the same rule the turso adapter applies.
		Status:          string(user.NormalizeStatus(u.Status)),
		AuthRevision:    u.AuthRevision,
		StatusChangedAt: firestoredb.NullTime(u.StatusChangedAt),
		CreatedAt:       firestoredb.TruncateTime(u.CreatedAt),
		UpdatedAt:       firestoredb.TruncateTime(u.UpdatedAt),
	}
}

// putUser creates the users document. The verb is Create, never Set: the
// document id IS the primary key, so a duplicate loses at the server as
// sdk.ErrAlreadyExists instead of overwriting a live account.
//
// It takes the projection as an EXPLICIT argument because this is the store's
// only whole-document users write, and the whole-document write is the exact
// shape SCHEMA.md §6.2 forbids doing from a domain user.User: the caller cannot
// reach it without having decided what the two projection fields are.
func putUser(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, row userDoc, projection emailProjection) error {
	projection.apply(&row)
	return w.Create(ctx, userRef(db, row.ID), row)
}

// updateUserProfile writes the profile half of a users document — the fields
// UserRepository.Update owns. It leaves id, created_at, auth_revision, the
// lifecycle columns, and the projection alone, exactly as the SQL adapters'
// `UPDATE users SET display_name=?, updated_at=?` does: status transitions go
// through the atomic AdminRepository.SetStatus, so a profile write can never
// reactivate a deactivated account as a side effect.
//
// A missing document fails sdk.ErrNotFound, which is the port's absent contract.
func updateUserProfile(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, userID, displayName string, updatedAt time.Time) error {
	return w.Update(ctx, userRef(db, userID), []gcfs.Update{
		{Path: fieldUserDisplayName, Value: displayName},
		{Path: fieldUserUpdatedAt, Value: firestoredb.TruncateTime(updatedAt)},
	})
}

// advanceUserRevision writes the users-document half of an identifier or
// credential mutation: the new auth_revision, the mutation time, and the
// RECOMPUTED directory projection, as field updates.
//
// The revision is written as an absolute value rather than a field increment
// because the caller has just read it under the transaction's lock for the CAS —
// the read and the write are one decision, and an increment transform would
// hide it.
func advanceUserRevision(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, userID string, revision int64, now time.Time, projection emailProjection) error {
	updates := append([]gcfs.Update{
		{Path: fieldUserAuthRevision, Value: revision},
		{Path: fieldUserUpdatedAt, Value: firestoredb.TruncateTime(now)},
	}, projection.updates()...)
	return w.Update(ctx, userRef(db, userID), updates)
}

// transitionUserStatus writes the LIFECYCLE half of a users document: the new
// status, its transition time, the mutation time, and the revision the caller
// already computed — as FIELD updates, so the directory projection survives
// untouched (SCHEMA.md §6.3 row 14; every Summary field except the two
// projection fields is written here).
//
// The revision is the caller's absolute value rather than a field increment for
// the same reason advanceUserRevision takes one: this transaction read the
// document to decide whether the status changes at all, so the read and the
// write are one decision.
//
// It writes ONLY the users document. The session and grant revocation the
// transition owes is the caller's, in the SAME transaction, through the helpers
// that own those collections — an update-then-best-effort-delete is exactly what
// the port forbids.
func transitionUserStatus(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, userID string, status user.Status, revision int64, now time.Time) error {
	stamp := firestoredb.TruncateTime(now)
	return w.Update(ctx, userRef(db, userID), []gcfs.Update{
		{Path: fieldUserStatus, Value: string(status)},
		{Path: fieldUserStatusChangedAt, Value: stamp},
		{Path: fieldUserUpdatedAt, Value: stamp},
		{Path: fieldUserAuthRevision, Value: revision},
	})
}

// readUser reads one users document through r, so the same code serves a
// standalone read and a transaction's read phase. An absent document is
// sdk.ErrNotFound (the connector's Get maps it), which is every port's absent
// contract.
func readUser(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string) (userDoc, error) {
	snap, err := r.Get(ctx, userRef(db, userID))
	if err != nil {
		return userDoc{}, err
	}
	return decodeUser(snap)
}

// decodeUser turns one snapshot into a users document.
func decodeUser(snap *gcfs.DocumentSnapshot) (userDoc, error) {
	var row userDoc
	if err := snap.DataTo(&row); err != nil {
		return userDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionUsers, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// decodeUserSummary is the directory listing's Decode: one snapshot straight to
// the port's projection, so a page costs exactly the documents it returns.
func decodeUserSummary(snap *gcfs.DocumentSnapshot) (user.Summary, error) {
	row, err := decodeUser(snap)
	if err != nil {
		return user.Summary{}, err
	}
	return row.summary()
}

// listUsers is the ListQuery the directory pages with. The order allow-list and
// default come straight from the pocket (user.OrderFields, user.DefaultOrder) —
// the same values both SQL adapters pass — and PK is the row's own `id` field,
// so equal created_at values break the tie on the contractual column rather than
// on the hashed document name. Firestore orders strings by UTF-8 bytes, which is
// the byte order pgx pins with COLLATE "C" and turso gets from SQLite's BINARY
// collation, so the cursor is byte-identical across the three families.
//
// No PostFilter is declared, so a non-blank Search is refused with
// sdk.ErrInvalidInput by the connector's List rather than answered with an
// unfiltered page: user.OrderFields declares no searchable field, and a
// top-level users search is a plan-level decision (ruling R4), not an accident.
func listUsers(db *firestoredb.DB) firestoredb.ListQuery[user.Summary] {
	return firestoredb.ListQuery[user.Summary]{
		Query:        usersQuery(db),
		OrderFields:  user.OrderFields,
		DefaultOrder: user.DefaultOrder,
		PK:           "id",
		Decode:       decodeUserSummary,
		OrderValueOf: func(row user.Summary, _ string) any { return row.CreatedAt },
		PKOf:         func(row user.Summary) string { return row.ID },
	}
}

// user projects the document onto the domain aggregate. A legacy empty status
// normalizes to active, matching the SQL readers.
func (d userDoc) user() (user.User, error) {
	changedAt, err := firestoredb.ParseNullTime(d.StatusChangedAt)
	if err != nil {
		return user.User{}, err
	}
	return user.User{
		ID:              d.ID,
		DisplayName:     d.DisplayName,
		AuthRevision:    d.AuthRevision,
		Status:          user.NormalizeStatus(user.Status(d.Status)),
		StatusChangedAt: changedAt,
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
	}, nil
}

// summary projects the document onto the operator-directory row, answering
// PrimaryEmail and EmailVerified from the persisted projection — the whole point
// of persisting it (SCHEMA.md §6.1).
func (d userDoc) summary() (user.Summary, error) {
	changedAt, err := firestoredb.ParseNullTime(d.StatusChangedAt)
	if err != nil {
		return user.Summary{}, err
	}
	s := user.Summary{
		ID:              d.ID,
		DisplayName:     d.DisplayName,
		Status:          user.NormalizeStatus(user.Status(d.Status)),
		StatusChangedAt: changedAt,
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
	}
	projectionOfUser(d).fill(&s)
	return s, nil
}
