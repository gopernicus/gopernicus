package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/invitation"
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

const invitationColumns = "id, resource_type, resource_id, relation, identifier, resolved_subject_id, invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at"

// Create persists a new pending invitation; a pending-tuple collision →
// errs.ErrAlreadyExists (the partial unique index).
func (s *InvitationStore) Create(ctx context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	const q = `INSERT INTO invitations (` + invitationColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
	_, err := s.db.Exec(ctx, q,
		inv.ID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier,
		inv.ResolvedSubjectID, inv.InvitedBy, inv.TokenHash, inv.AutoAccept,
		inv.Status, inv.ExpiresAt.UTC(), pgxdb.NullTime(inv.AcceptedAt),
		inv.CreatedAt.UTC(), inv.UpdatedAt.UTC(),
	)
	if err != nil {
		return invitation.Invitation{}, err
	}
	return inv, nil
}

// Get returns the invitation for id, or errs.ErrNotFound.
func (s *InvitationStore) Get(ctx context.Context, id string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE id = $1`
	return scanInvitation(s.db.QueryRow(ctx, q, id))
}

// GetByTokenHash returns the invitation for tokenHash; unknown → errs.ErrNotFound,
// present-but-past-ExpiresAt → errs.ErrExpired, else the record.
func (s *InvitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE token_hash = $1`
	inv, err := scanInvitation(s.db.QueryRow(ctx, q, tokenHash))
	if err != nil {
		return invitation.Invitation{}, err
	}
	if inv.Expired(time.Now()) {
		return invitation.Invitation{}, errs.ErrExpired
	}
	return inv, nil
}

// ListByResource returns a cursor-paginated page of a resource's invitations,
// ordered created_at DESC, id DESC.
func (s *InvitationStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return listPage(ctx, s.db, invitationColumns, "invitations", "WHERE resource_type = $1 AND resource_id = $2", []any{resourceType, resourceID}, "id", req,
		scanInvitation,
		func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID },
	)
}

// ListBySubject returns a cursor-paginated page of invitations addressed to
// identifier (the invitee email), ordered created_at DESC, id DESC.
func (s *InvitationStore) ListBySubject(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return listPage(ctx, s.db, invitationColumns, "invitations", "WHERE identifier = $1", []any{identifier}, "id", req,
		scanInvitation,
		func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID },
	)
}

// UpdateStatus applies a lifecycle transition and returns the full row via
// UPDATE … RETURNING; unknown id → errs.ErrNotFound.
func (s *InvitationStore) UpdateStatus(ctx context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	const q = `UPDATE invitations
		SET status = $1, token_hash = $2, expires_at = $3, accepted_at = $4, resolved_subject_id = $5, updated_at = $6
		WHERE id = $7
		RETURNING ` + invitationColumns
	return scanInvitation(s.db.QueryRow(ctx, q,
		upd.Status, upd.TokenHash, upd.ExpiresAt.UTC(), pgxdb.NullTime(upd.AcceptedAt),
		upd.ResolvedSubjectID, upd.UpdatedAt.UTC(), id,
	))
}

// scanInvitation scans one invitations row, mapping pgx.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanInvitation(sc scanner) (invitation.Invitation, error) {
	var (
		inv                             invitation.Invitation
		expiresAt, createdAt, updatedAt time.Time
		acceptedAt                      *time.Time
	)
	if err := sc.Scan(
		&inv.ID, &inv.ResourceType, &inv.ResourceID, &inv.Relation, &inv.Identifier,
		&inv.ResolvedSubjectID, &inv.InvitedBy, &inv.TokenHash, &inv.AutoAccept, &inv.Status,
		&expiresAt, &acceptedAt, &createdAt, &updatedAt,
	); err != nil {
		return invitation.Invitation{}, pgxdb.MapError(err)
	}
	inv.ExpiresAt = expiresAt.UTC()
	inv.AcceptedAt = pgxdb.FromNullTime(acceptedAt)
	inv.CreatedAt = createdAt.UTC()
	inv.UpdatedAt = updatedAt.UTC()
	return inv, nil
}
