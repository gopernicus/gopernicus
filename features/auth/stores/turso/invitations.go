package turso

import (
	"context"
	"database/sql"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/invitation"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// InvitationStore implements invitation.InvitationRepository over a libSQL
// database. The PARTIAL pending-tuple uniqueness (at most one PENDING invitation
// per (resource_type, resource_id, identifier, relation)) is a filtered unique
// index — once UpdateStatus moves a row off pending, a new pending invite for the
// same tuple succeeds. GetByTokenHash surfaces a read-time errs.ErrExpired for a
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

const invitationColumns = "id, resource_type, resource_id, relation, identifier, resolved_subject_id, invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at"

// Create persists a new pending invitation; a pending-tuple collision →
// errs.ErrAlreadyExists (the partial unique index).
func (s *InvitationStore) Create(ctx context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	const q = `INSERT INTO invitations (` + invitationColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		inv.ID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier,
		inv.ResolvedSubjectID, inv.InvitedBy, inv.TokenHash, boolToInt(inv.AutoAccept),
		inv.Status, formatTS(inv.ExpiresAt), nullableTS(inv.AcceptedAt),
		formatTS(inv.CreatedAt), formatTS(inv.UpdatedAt),
	)
	if err != nil {
		return invitation.Invitation{}, err
	}
	return inv, nil
}

// Get returns the invitation for id, or errs.ErrNotFound.
func (s *InvitationStore) Get(ctx context.Context, id string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE id = ?`
	return scanInvitation(s.db.QueryRow(ctx, q, id))
}

// GetByTokenHash returns the invitation for tokenHash; unknown → errs.ErrNotFound,
// present-but-past-ExpiresAt → errs.ErrExpired, else the record.
func (s *InvitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitation.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE token_hash = ?`
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
	return listPage(ctx, s.db, invitationColumns, "invitations", "WHERE resource_type = ? AND resource_id = ?", []any{resourceType, resourceID}, "id", req,
		scanInvitation,
		func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID },
	)
}

// ListBySubject returns a cursor-paginated page of invitations addressed to
// identifier (the invitee email), ordered created_at DESC, id DESC.
func (s *InvitationStore) ListBySubject(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return listPage(ctx, s.db, invitationColumns, "invitations", "WHERE identifier = ?", []any{identifier}, "id", req,
		scanInvitation,
		func(inv invitation.Invitation) (time.Time, string) { return inv.CreatedAt, inv.ID },
	)
}

// UpdateStatus applies a lifecycle transition and returns the full row via
// UPDATE … RETURNING; unknown id → errs.ErrNotFound.
func (s *InvitationStore) UpdateStatus(ctx context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	const q = `UPDATE invitations
		SET status = ?, token_hash = ?, expires_at = ?, accepted_at = ?, resolved_subject_id = ?, updated_at = ?
		WHERE id = ?
		RETURNING ` + invitationColumns
	return scanInvitation(s.db.QueryRow(ctx, q,
		upd.Status, upd.TokenHash, formatTS(upd.ExpiresAt), nullableTS(upd.AcceptedAt),
		upd.ResolvedSubjectID, formatTS(upd.UpdatedAt), id,
	))
}

// scanInvitation scans one invitations row, mapping sql.ErrNoRows to
// errs.ErrNotFound via the connector's MapError.
func scanInvitation(sc scanner) (invitation.Invitation, error) {
	var (
		inv                             invitation.Invitation
		autoAccept                      int64
		expiresAt, createdAt, updatedAt string
		acceptedAt                      sql.NullString
	)
	if err := sc.Scan(
		&inv.ID, &inv.ResourceType, &inv.ResourceID, &inv.Relation, &inv.Identifier,
		&inv.ResolvedSubjectID, &inv.InvitedBy, &inv.TokenHash, &autoAccept, &inv.Status,
		&expiresAt, &acceptedAt, &createdAt, &updatedAt,
	); err != nil {
		return invitation.Invitation{}, tursodb.MapError(err)
	}
	inv.AutoAccept = autoAccept != 0
	var err error
	if inv.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return invitation.Invitation{}, err
	}
	if inv.AcceptedAt, err = parseNullTime(acceptedAt); err != nil {
		return invitation.Invitation{}, err
	}
	if inv.CreatedAt, err = parseTime(createdAt); err != nil {
		return invitation.Invitation{}, err
	}
	if inv.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return invitation.Invitation{}, err
	}
	return inv, nil
}
