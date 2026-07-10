package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// InvitationStore implements invitation.InvitationRepository over a PostgreSQL
// database. The PARTIAL pending-tuple uniqueness (at most one PENDING invitation
// per (resource_type, resource_id, identifier, relation)) is a filtered unique
// index — once UpdateStatus moves a row off pending, a new pending invite for the
// same tuple succeeds. GetByTokenHash surfaces a read-time errs.ErrExpired for a
// present row past ExpiresAt. Both listings page in the pinned created_at DESC,
// id DESC order.
type InvitationStore struct {
	db *pgxdb.DB
}

var _ invitation.InvitationRepository = (*InvitationStore)(nil)

// NewInvitationStore returns an InvitationStore backed by db.
func NewInvitationStore(db *pgxdb.DB) *InvitationStore {
	return &InvitationStore{db: db}
}

const invitationColumns = "id, resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id, invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at"

// invitationRow is the store-local, db-tagged projection of an invitations row.
// accepted_at is nullable (a pointer, zero-time when NULL); toDomain maps it.
type invitationRow struct {
	ID                string     `db:"id"`
	ResourceType      string     `db:"resource_type"`
	ResourceID        string     `db:"resource_id"`
	Relation          string     `db:"relation"`
	Identifier        string     `db:"identifier"`
	IdentifierKind    string     `db:"identifier_kind"`
	ResolvedSubjectID string     `db:"resolved_subject_id"`
	InvitedBy         string     `db:"invited_by"`
	TokenHash         string     `db:"token_hash"`
	AutoAccept        bool       `db:"auto_accept"`
	Status            string     `db:"status"`
	ExpiresAt         time.Time  `db:"expires_at"`
	AcceptedAt        *time.Time `db:"accepted_at"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
}

func (r invitationRow) toDomain() invitation.Invitation {
	return invitation.Invitation{
		ID:                r.ID,
		ResourceType:      r.ResourceType,
		ResourceID:        r.ResourceID,
		Relation:          r.Relation,
		Identifier:        r.Identifier,
		IdentifierKind:    r.IdentifierKind,
		ResolvedSubjectID: r.ResolvedSubjectID,
		InvitedBy:         r.InvitedBy,
		TokenHash:         r.TokenHash,
		AutoAccept:        r.AutoAccept,
		Status:            r.Status,
		ExpiresAt:         r.ExpiresAt.UTC(),
		AcceptedAt:        pgxdb.FromNullTime(r.AcceptedAt),
		CreatedAt:         r.CreatedAt.UTC(),
		UpdatedAt:         r.UpdatedAt.UTC(),
	}
}

// Create persists a new pending invitation; a pending-tuple collision →
// errs.ErrAlreadyExists (the partial unique index).
func (s *InvitationStore) Create(ctx context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	args := pgx.NamedArgs{
		"resource_type":       inv.ResourceType,
		"resource_id":         inv.ResourceID,
		"relation":            inv.Relation,
		"identifier":          inv.Identifier,
		"identifier_kind":     inv.IdentifierKind,
		"resolved_subject_id": inv.ResolvedSubjectID,
		"invited_by":          inv.InvitedBy,
		"token_hash":          inv.TokenHash,
		"auto_accept":         inv.AutoAccept,
		"status":              inv.Status,
		"expires_at":          inv.ExpiresAt.UTC(),
		"accepted_at":         pgxdb.NullTime(inv.AcceptedAt),
		"created_at":          inv.CreatedAt.UTC(),
		"updated_at":          inv.UpdatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if inv.ID == "" {
		const q = `INSERT INTO invitations (resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id,
			invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at)
			VALUES (@resource_type, @resource_id, @relation, @identifier, @identifier_kind, @resolved_subject_id,
				@invited_by, @token_hash, @auto_accept, @status, @expires_at, @accepted_at, @created_at, @updated_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&inv.ID); err != nil {
			return invitation.Invitation{}, pgxdb.MapError(err)
		}
		return inv, nil
	}
	const q = `INSERT INTO invitations (` + invitationColumns + `)
		VALUES (@id, @resource_type, @resource_id, @relation, @identifier, @identifier_kind, @resolved_subject_id,
			@invited_by, @token_hash, @auto_accept, @status, @expires_at, @accepted_at, @created_at, @updated_at)`
	args["id"] = inv.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return invitation.Invitation{}, err
	}
	return inv, nil
}

// Get returns the invitation for id, or errs.ErrNotFound.
func (s *InvitationStore) Get(ctx context.Context, id string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE id = @id`
	row, err := queryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return invitation.Invitation{}, err
	}
	return row.toDomain(), nil
}

// GetByTokenHash returns the invitation for tokenHash; unknown → errs.ErrNotFound,
// present-but-past-ExpiresAt → errs.ErrExpired, else the record.
func (s *InvitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE token_hash = @token_hash`
	row, err := queryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{"token_hash": tokenHash})
	if err != nil {
		return invitation.Invitation{}, err
	}
	inv := row.toDomain()
	if inv.Expired(time.Now()) {
		return invitation.Invitation{}, errs.ErrExpired
	}
	return inv, nil
}

// ListByResource returns a cursor-paginated page of a resource's invitations,
// ordered created_at DESC, id DESC.
func (s *InvitationStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	q := pgxdb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM invitations WHERE resource_type = @resource_type AND resource_id = @resource_id`,
		Args:         pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID},
		OrderFields:  invitation.OrderFields,
		DefaultOrder: invitation.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return crud.MapPage(page, invitationRow.toDomain), nil
}

// ListBySubject returns a cursor-paginated page of invitations addressed to
// identifier (the invitee email), ordered created_at DESC, id DESC.
func (s *InvitationStore) ListBySubject(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	q := pgxdb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM invitations WHERE identifier = @identifier`,
		Args:         pgx.NamedArgs{"identifier": identifier},
		OrderFields:  invitation.OrderFields,
		DefaultOrder: invitation.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return crud.MapPage(page, invitationRow.toDomain), nil
}

// UpdateStatus applies a lifecycle transition and returns the full row via
// UPDATE … RETURNING, scanned through the db-tagged row struct; unknown id →
// errs.ErrNotFound.
func (s *InvitationStore) UpdateStatus(ctx context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	const q = `UPDATE invitations
		SET status = @status, token_hash = @token_hash, expires_at = @expires_at,
			accepted_at = @accepted_at, resolved_subject_id = @resolved_subject_id, updated_at = @updated_at
		WHERE id = @id
		RETURNING ` + invitationColumns
	row, err := queryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{
		"status":              upd.Status,
		"token_hash":          upd.TokenHash,
		"expires_at":          upd.ExpiresAt.UTC(),
		"accepted_at":         pgxdb.NullTime(upd.AcceptedAt),
		"resolved_subject_id": upd.ResolvedSubjectID,
		"updated_at":          upd.UpdatedAt.UTC(),
		"id":                  id,
	})
	if err != nil {
		return invitation.Invitation{}, err
	}
	return row.toDomain(), nil
}
