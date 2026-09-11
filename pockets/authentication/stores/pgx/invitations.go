package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/jackc/pgx/v5"
)

// InvitationStore persists invitations and conditional acceptance claims in pgx.
// Active-tuple uniqueness covers pending and accepting rows. A matching durable
// claim can resume after token expiry; unclaimed expired tokens cannot be claimed.
type InvitationStore struct {
	db *pgxdb.DB
	qualified
}

var _ invitations.InvitationRepository = (*InvitationStore)(nil)

// NewInvitationStore returns an InvitationStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewInvitationStore(db *pgxdb.DB, opts ...Option) *InvitationStore {
	if db == nil {
		panic("authentication pgx: NewInvitationStore received a nil database")
	}
	return &InvitationStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

const invitationColumns = "id, resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id, invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at, metadata, resolved_subject_type"

// invitationRow is the store-local, db-tagged projection of an invitations row.
// accepted_at is nullable (a pointer, zero-time when NULL); toDomain maps it.
type invitationRow struct {
	ID                  string            `db:"id"`
	ResourceType        string            `db:"resource_type"`
	ResourceID          string            `db:"resource_id"`
	Relation            string            `db:"relation"`
	Identifier          string            `db:"identifier"`
	IdentifierKind      string            `db:"identifier_kind"`
	ResolvedSubjectID   string            `db:"resolved_subject_id"`
	ResolvedSubjectType string            `db:"resolved_subject_type"`
	InvitedBy           string            `db:"invited_by"`
	TokenHash           string            `db:"token_hash"`
	AutoAccept          bool              `db:"auto_accept"`
	Status              string            `db:"status"`
	ExpiresAt           time.Time         `db:"expires_at"`
	AcceptedAt          *time.Time        `db:"accepted_at"`
	CreatedAt           time.Time         `db:"created_at"`
	UpdatedAt           time.Time         `db:"updated_at"`
	Metadata            map[string]string `db:"metadata"`
}

func (r invitationRow) toDomain() invitations.Invitation {
	metadata := r.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	return invitations.Invitation{
		ID:                  r.ID,
		ResourceType:        r.ResourceType,
		ResourceID:          r.ResourceID,
		Relation:            r.Relation,
		Identifier:          r.Identifier,
		IdentifierKind:      r.IdentifierKind,
		ResolvedSubjectID:   r.ResolvedSubjectID,
		ResolvedSubjectType: r.ResolvedSubjectType,
		InvitedBy:           r.InvitedBy,
		TokenHash:           r.TokenHash,
		AutoAccept:          r.AutoAccept,
		Status:              r.Status,
		Metadata:            metadata,
		ExpiresAt:           r.ExpiresAt.UTC(),
		AcceptedAt:          pgxdb.FromNullTime(r.AcceptedAt),
		CreatedAt:           r.CreatedAt.UTC(),
		UpdatedAt:           r.UpdatedAt.UTC(),
	}
}

// Create persists a new pending invitation; a pending-tuple collision →
// sdk.ErrAlreadyExists (the partial unique index).
func (s *InvitationStore) Create(ctx context.Context, inv invitations.Invitation) (invitations.Invitation, error) {
	if inv.Status != invitations.StatusPending || inv.ResolvedSubjectType != "" {
		return invitations.Invitation{}, sdk.ErrInvalidInput
	}
	metadata, err := marshalMetadata(inv.Metadata)
	if err != nil {
		return invitations.Invitation{}, err
	}
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
		"metadata":            metadata,
	}
	// Empty ID → the sdk.DatabaseID strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if inv.ID == "" {
		q := `INSERT INTO ` + s.table(invitationsTable) + ` (resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id,
			invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at, metadata)
			VALUES (@resource_type, @resource_id, @relation, @identifier, @identifier_kind, @resolved_subject_id,
				@invited_by, @token_hash, @auto_accept, @status, @expires_at, @accepted_at, @created_at, @updated_at, @metadata)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&inv.ID); err != nil {
			return invitations.Invitation{}, pgxdb.MapError(err)
		}
		return inv, nil
	}
	q := `INSERT INTO ` + s.table(invitationsTable) + ` (` + invitationColumns + `)
		VALUES (@id, @resource_type, @resource_id, @relation, @identifier, @identifier_kind, @resolved_subject_id,
			@invited_by, @token_hash, @auto_accept, @status, @expires_at, @accepted_at, @created_at, @updated_at, @metadata, @resolved_subject_type)`
	args["id"] = inv.ID
	args["resolved_subject_type"] = inv.ResolvedSubjectType
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return invitations.Invitation{}, err
	}
	return inv, nil
}

// Get returns the invitation for id, or sdk.ErrNotFound.
func (s *InvitationStore) Get(ctx context.Context, id string) (invitations.Invitation, error) {
	q := `SELECT ` + invitationColumns + ` FROM ` + s.table(invitationsTable) + ` WHERE id = @id`
	row, err := pgxdb.QueryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return invitations.Invitation{}, err
	}
	return row.toDomain(), nil
}

// GetByTokenHash returns the invitation for tokenHash; unknown → sdk.ErrNotFound,
// present-but-past-ExpiresAt → sdk.ErrExpired, else the record.
func (s *InvitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitations.Invitation, error) {
	q := `SELECT ` + invitationColumns + ` FROM ` + s.table(invitationsTable) + ` WHERE token_hash = @token_hash`
	row, err := pgxdb.QueryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{"token_hash": tokenHash})
	if err != nil {
		return invitations.Invitation{}, err
	}
	inv := row.toDomain()
	if inv.Status != invitations.StatusAccepting && inv.Status != invitations.StatusAccepted && inv.Expired(time.Now()) {
		return invitations.Invitation{}, sdk.ErrExpired
	}
	return inv, nil
}

// ListByResource returns a cursor-paginated page of a resource's invitations,
// ordered created_at DESC, id DESC.
func (s *InvitationStore) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[invitations.Invitation], error) {
	q := pgxdb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM ` + s.table(invitationsTable) + ` WHERE resource_type = @resource_type AND resource_id = @resource_id`,
		Args:         pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID},
		OrderFields:  invitations.OrderFields,
		DefaultOrder: invitations.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return list.Page[invitations.Invitation]{}, err
	}
	return list.MapPage(page, invitationRow.toDomain), nil
}

// ListBySubject returns a cursor-paginated page of invitations addressed to
// (kind, identifier) — the invitee address and its kind — ordered created_at
// DESC, id DESC. Both columns filter so a value shared across kinds never
// cross-resolves (design §7 re-key).
func (s *InvitationStore) ListBySubject(ctx context.Context, kind, identifier string, req list.Request) (list.Page[invitations.Invitation], error) {
	q := pgxdb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM ` + s.table(invitationsTable) + ` WHERE identifier_kind = @kind AND identifier = @identifier`,
		Args:         pgx.NamedArgs{"kind": kind, "identifier": identifier},
		OrderFields:  invitations.OrderFields,
		DefaultOrder: invitations.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return list.Page[invitations.Invitation]{}, err
	}
	return list.MapPage(page, invitationRow.toDomain), nil
}

// UpdateStatus applies only a current-token transition on an unclaimed row.
func (s *InvitationStore) UpdateStatus(ctx context.Context, id string, upd invitations.StatusUpdate) (invitations.Invitation, error) {
	if err := upd.Validate(); err != nil {
		return invitations.Invitation{}, err
	}
	q := `UPDATE ` + s.table(invitationsTable) + ` SET status=@status, token_hash=@token_hash, expires_at=@expires_at, resolved_subject_id=@subject_id, updated_at=@now
 WHERE id=@id AND status IN ('pending','expired') AND token_hash=@expected_token RETURNING ` + invitationColumns
	row, err := pgxdb.QueryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{"id": id, "status": upd.Status, "token_hash": upd.TokenHash, "expires_at": upd.ExpiresAt.UTC(), "subject_id": upd.ResolvedSubjectID, "now": upd.UpdatedAt.UTC(), "expected_token": upd.ExpectedTokenHash})
	if errors.Is(err, sdk.ErrNotFound) {
		if _, getErr := s.Get(ctx, id); getErr != nil {
			return invitations.Invitation{}, getErr
		}
		return invitations.Invitation{}, sdk.ErrConflict
	}
	if err != nil {
		return invitations.Invitation{}, err
	}
	return row.toDomain(), nil
}

func (s *InvitationStore) ClaimAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	if err := claim.Validate(); err != nil {
		return invitations.Invitation{}, err
	}
	q := `UPDATE ` + s.table(invitationsTable) + ` SET status='accepting', resolved_subject_type=@subject_type, resolved_subject_id=@subject_id, updated_at=@now
 WHERE id=@id AND status='pending' AND token_hash=@token_hash AND expires_at>@now RETURNING ` + invitationColumns
	row, err := pgxdb.QueryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{"id": id, "subject_type": claim.SubjectType, "subject_id": claim.SubjectID, "now": claim.Now.UTC(), "token_hash": claim.TokenHash})
	if errors.Is(err, sdk.ErrNotFound) {
		return s.acceptanceResult(ctx, id, claim, false)
	}
	if err != nil {
		return invitations.Invitation{}, err
	}
	return row.toDomain(), nil
}

func (s *InvitationStore) CompleteAcceptance(ctx context.Context, id string, claim invitations.Acceptance) (invitations.Invitation, error) {
	if err := claim.Validate(); err != nil {
		return invitations.Invitation{}, err
	}
	q := `UPDATE ` + s.table(invitationsTable) + ` SET status='accepted', accepted_at=@now, updated_at=@now
 WHERE id=@id AND status='accepting' AND token_hash=@token_hash AND resolved_subject_type=@subject_type AND resolved_subject_id=@subject_id RETURNING ` + invitationColumns
	row, err := pgxdb.QueryOne[invitationRow](ctx, s.db, q, pgx.NamedArgs{"id": id, "subject_type": claim.SubjectType, "subject_id": claim.SubjectID, "now": claim.Now.UTC(), "token_hash": claim.TokenHash})
	if errors.Is(err, sdk.ErrNotFound) {
		return s.acceptanceResult(ctx, id, claim, true)
	}
	if err != nil {
		return invitations.Invitation{}, err
	}
	return row.toDomain(), nil
}

// A matching committed claim can resume even after expiry. This read never
// creates a claim; only the conditional UPDATE above may do that.
func (s *InvitationStore) acceptanceResult(ctx context.Context, id string, claim invitations.Acceptance, complete bool) (invitations.Invitation, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return invitations.Invitation{}, err
	}
	if claim.Matches(current) && (current.Status == invitations.StatusAccepted || (!complete && current.Status == invitations.StatusAccepting)) {
		return current, nil
	}
	if !complete && current.Status == invitations.StatusPending && current.TokenHash == claim.TokenHash && current.Expired(claim.Now) {
		return invitations.Invitation{}, sdk.ErrExpired
	}
	return invitations.Invitation{}, sdk.ErrConflict
}

// marshalMetadata renders opaque host invitation metadata as JSON text for the
// jsonb column. A nil or empty map stores '{}' — never JSON null, which would
// bypass the column DEFAULT — so it reads back as an empty map (the uniform
// round-trip contract). The domain has already bounded the map.
func marshalMetadata(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
