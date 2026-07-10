package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// InvitationStore implements invitation.InvitationRepository over a libSQL
// database. The PARTIAL pending-tuple uniqueness (at most one PENDING invitation
// per (resource_type, resource_id, identifier, relation)) is a filtered unique
// index — once UpdateStatus moves a row off pending, a new pending invite for the
// same tuple succeeds. GetByTokenHash surfaces a read-time sdk.ErrExpired for a
// present row past ExpiresAt. Both listings page in the pinned created_at DESC,
// id DESC order.
type InvitationStore struct {
	db *tursodb.DB
}

var _ invitation.InvitationRepository = (*InvitationStore)(nil)

// NewInvitationStore returns an InvitationStore backed by db.
func NewInvitationStore(db *tursodb.DB) *InvitationStore {
	return &InvitationStore{db: db}
}

const invitationColumns = "id, resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id, invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at"

// invitationRow is the store-local, db-tagged projection of an invitations row.
// accepted_at is nullable (turso.NullTime, zero-time when NULL); toDomain maps it.
type invitationRow struct {
	ID                string           `db:"id"`
	ResourceType      string           `db:"resource_type"`
	ResourceID        string           `db:"resource_id"`
	Relation          string           `db:"relation"`
	Identifier        string           `db:"identifier"`
	IdentifierKind    string           `db:"identifier_kind"`
	ResolvedSubjectID string           `db:"resolved_subject_id"`
	InvitedBy         string           `db:"invited_by"`
	TokenHash         string           `db:"token_hash"`
	AutoAccept        tursodb.Bool     `db:"auto_accept"`
	Status            string           `db:"status"`
	ExpiresAt         tursodb.Time     `db:"expires_at"`
	AcceptedAt        tursodb.NullTime `db:"accepted_at"`
	CreatedAt         tursodb.Time     `db:"created_at"`
	UpdatedAt         tursodb.Time     `db:"updated_at"`
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
		AutoAccept:        bool(r.AutoAccept),
		Status:            r.Status,
		ExpiresAt:         r.ExpiresAt.Time,
		AcceptedAt:        r.AcceptedAt.Time,
		CreatedAt:         r.CreatedAt.Time,
		UpdatedAt:         r.UpdatedAt.Time,
	}
}

// Create persists a new pending invitation; a pending-tuple collision →
// sdk.ErrAlreadyExists (the partial unique index).
func (s *InvitationStore) Create(ctx context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if inv.ID == "" {
		const q = `INSERT INTO invitations (resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id,
			invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q,
			inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, inv.IdentifierKind,
			inv.ResolvedSubjectID, inv.InvitedBy, inv.TokenHash, tursodb.BoolToInt(inv.AutoAccept),
			inv.Status, tursodb.FormatTime(inv.ExpiresAt), tursodb.FormatNullTime(inv.AcceptedAt),
			tursodb.FormatTime(inv.CreatedAt), tursodb.FormatTime(inv.UpdatedAt),
		).Scan(&inv.ID); err != nil {
			return invitation.Invitation{}, tursodb.MapError(err)
		}
		return inv, nil
	}
	const q = `INSERT INTO invitations (` + invitationColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		inv.ID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, inv.IdentifierKind,
		inv.ResolvedSubjectID, inv.InvitedBy, inv.TokenHash, tursodb.BoolToInt(inv.AutoAccept),
		inv.Status, tursodb.FormatTime(inv.ExpiresAt), tursodb.FormatNullTime(inv.AcceptedAt),
		tursodb.FormatTime(inv.CreatedAt), tursodb.FormatTime(inv.UpdatedAt),
	)
	if err != nil {
		return invitation.Invitation{}, err
	}
	return inv, nil
}

// Get returns the invitation for id, or sdk.ErrNotFound.
func (s *InvitationStore) Get(ctx context.Context, id string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE id = ?`
	row, err := queryOne[invitationRow](ctx, s.db, q, id)
	if err != nil {
		return invitation.Invitation{}, err
	}
	return row.toDomain(), nil
}

// GetByTokenHash returns the invitation for tokenHash; unknown → sdk.ErrNotFound,
// present-but-past-ExpiresAt → sdk.ErrExpired, else the record.
func (s *InvitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE token_hash = ?`
	row, err := queryOne[invitationRow](ctx, s.db, q, tokenHash)
	if err != nil {
		return invitation.Invitation{}, err
	}
	inv := row.toDomain()
	if inv.Expired(time.Now()) {
		return invitation.Invitation{}, sdk.ErrExpired
	}
	return inv, nil
}

// ListByResource returns a cursor-paginated page of a resource's invitations,
// ordered created_at DESC, id DESC.
func (s *InvitationStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	q := tursodb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM invitations WHERE resource_type = ? AND resource_id = ?`,
		Args:         []any{resourceType, resourceID},
		OrderFields:  invitation.OrderFields,
		DefaultOrder: invitation.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return crud.MapPage(page, invitationRow.toDomain), nil
}

// ListBySubject returns a cursor-paginated page of invitations addressed to
// identifier (the invitee email), ordered created_at DESC, id DESC.
func (s *InvitationStore) ListBySubject(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	q := tursodb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM invitations WHERE identifier = ?`,
		Args:         []any{identifier},
		OrderFields:  invitation.OrderFields,
		DefaultOrder: invitation.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return crud.MapPage(page, invitationRow.toDomain), nil
}

// UpdateStatus applies a lifecycle transition and returns the full row via
// UPDATE … RETURNING; unknown id → sdk.ErrNotFound.
func (s *InvitationStore) UpdateStatus(ctx context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	const q = `UPDATE invitations
		SET status = ?, token_hash = ?, expires_at = ?, accepted_at = ?, resolved_subject_id = ?, updated_at = ?
		WHERE id = ?
		RETURNING ` + invitationColumns
	row, err := queryOne[invitationRow](ctx, s.db, q,
		upd.Status, upd.TokenHash, tursodb.FormatTime(upd.ExpiresAt), tursodb.FormatNullTime(upd.AcceptedAt),
		upd.ResolvedSubjectID, tursodb.FormatTime(upd.UpdatedAt), id,
	)
	if err != nil {
		return invitation.Invitation{}, err
	}
	return row.toDomain(), nil
}
