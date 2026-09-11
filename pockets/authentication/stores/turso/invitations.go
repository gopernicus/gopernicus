package turso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// InvitationStore persists invitations and conditional acceptance claims in turso.
// Active-tuple uniqueness covers pending and accepting rows. A matching durable
// claim can resume after token expiry; unclaimed expired tokens cannot be claimed.
type InvitationStore struct {
	db *tursodb.DB
}

var _ invitations.InvitationRepository = (*InvitationStore)(nil)

// NewInvitationStore returns an InvitationStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewInvitationStore(db *tursodb.DB) *InvitationStore {
	if db == nil {
		panic("authentication turso: NewInvitationStore received a nil database")
	}
	return &InvitationStore{db: db}
}

const invitationColumns = "id, resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id, invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at, metadata, resolved_subject_type"

// metadataJSON is a sql.Scanner that reads the stored TEXT JSON metadata bag into
// a non-nil map through unmarshalMetadata, so a db-tagged row-struct field
// performs the decode inside rows.Scan — surfacing a malformed-JSON error rather
// than swallowing it. A NULL or empty column reads back as a non-nil empty map
// (the uniform round-trip contract).
type metadataJSON map[string]string

func (m *metadataJSON) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		s = ""
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("authentication turso: cannot scan %T into invitation metadata JSON", src)
	}
	decoded, err := unmarshalMetadata(s)
	if err != nil {
		return err
	}
	*m = decoded
	return nil
}

// invitationRow is the store-local, db-tagged projection of an invitations row.
// accepted_at is nullable (turso.NullTime, zero-time when NULL); toDomain maps it.
type invitationRow struct {
	ID                  string           `db:"id"`
	ResourceType        string           `db:"resource_type"`
	ResourceID          string           `db:"resource_id"`
	Relation            string           `db:"relation"`
	Identifier          string           `db:"identifier"`
	IdentifierKind      string           `db:"identifier_kind"`
	ResolvedSubjectID   string           `db:"resolved_subject_id"`
	ResolvedSubjectType string           `db:"resolved_subject_type"`
	InvitedBy           string           `db:"invited_by"`
	TokenHash           string           `db:"token_hash"`
	AutoAccept          tursodb.Bool     `db:"auto_accept"`
	Status              string           `db:"status"`
	ExpiresAt           tursodb.Time     `db:"expires_at"`
	AcceptedAt          tursodb.NullTime `db:"accepted_at"`
	CreatedAt           tursodb.Time     `db:"created_at"`
	UpdatedAt           tursodb.Time     `db:"updated_at"`
	Metadata            metadataJSON     `db:"metadata"`
}

func (r invitationRow) toDomain() invitations.Invitation {
	metadata := map[string]string(r.Metadata)
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
		AutoAccept:          bool(r.AutoAccept),
		Status:              r.Status,
		Metadata:            metadata,
		ExpiresAt:           r.ExpiresAt.Time,
		AcceptedAt:          r.AcceptedAt.Time,
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
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
	// Empty ID → the sdk.DatabaseID strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if inv.ID == "" {
		const q = `INSERT INTO invitations (resource_type, resource_id, relation, identifier, identifier_kind, resolved_subject_id,
			invited_by, token_hash, auto_accept, status, expires_at, accepted_at, created_at, updated_at, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q,
			inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, inv.IdentifierKind,
			inv.ResolvedSubjectID, inv.InvitedBy, inv.TokenHash, tursodb.BoolToInt(inv.AutoAccept),
			inv.Status, tursodb.FormatTime(inv.ExpiresAt), tursodb.FormatNullTime(inv.AcceptedAt),
			tursodb.FormatTime(inv.CreatedAt), tursodb.FormatTime(inv.UpdatedAt), metadata,
		).Scan(&inv.ID); err != nil {
			return invitations.Invitation{}, tursodb.MapError(err)
		}
		return inv, nil
	}
	const q = `INSERT INTO invitations (` + invitationColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.db.Exec(ctx, q,
		inv.ID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, inv.IdentifierKind,
		inv.ResolvedSubjectID, inv.InvitedBy, inv.TokenHash, tursodb.BoolToInt(inv.AutoAccept),
		inv.Status, tursodb.FormatTime(inv.ExpiresAt), tursodb.FormatNullTime(inv.AcceptedAt),
		tursodb.FormatTime(inv.CreatedAt), tursodb.FormatTime(inv.UpdatedAt), metadata, inv.ResolvedSubjectType,
	)
	if err != nil {
		return invitations.Invitation{}, err
	}
	return inv, nil
}

// Get returns the invitation for id, or sdk.ErrNotFound.
func (s *InvitationStore) Get(ctx context.Context, id string) (invitations.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE id = ?`
	row, err := tursodb.QueryOne[invitationRow](ctx, s.db, q, id)
	if err != nil {
		return invitations.Invitation{}, err
	}
	return row.toDomain(), nil
}

// GetByTokenHash returns the invitation for tokenHash; unknown → sdk.ErrNotFound,
// present-but-past-ExpiresAt → sdk.ErrExpired, else the record.
func (s *InvitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitations.Invitation, error) {
	const q = `SELECT ` + invitationColumns + ` FROM invitations WHERE token_hash = ?`
	row, err := tursodb.QueryOne[invitationRow](ctx, s.db, q, tokenHash)
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
	q := tursodb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM invitations WHERE resource_type = ? AND resource_id = ?`,
		Args:         []any{resourceType, resourceID},
		OrderFields:  invitations.OrderFields,
		DefaultOrder: invitations.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
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
	q := tursodb.ListQuery[invitationRow]{
		BaseSQL:      `SELECT ` + invitationColumns + ` FROM invitations WHERE identifier_kind = ? AND identifier = ?`,
		Args:         []any{kind, identifier},
		OrderFields:  invitations.OrderFields,
		DefaultOrder: invitations.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r invitationRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r invitationRow) string { return r.ID },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
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
	q := `UPDATE invitations SET status=?,token_hash=?,expires_at=?,resolved_subject_id=?,updated_at=?
 WHERE id=? AND status IN ('pending','expired') AND token_hash=? RETURNING ` + invitationColumns
	row, err := tursodb.QueryOne[invitationRow](ctx, s.db, q, upd.Status, upd.TokenHash, tursodb.FormatTime(upd.ExpiresAt), upd.ResolvedSubjectID, tursodb.FormatTime(upd.UpdatedAt), id, upd.ExpectedTokenHash)
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
	q := `UPDATE invitations SET status='accepting',resolved_subject_type=?,resolved_subject_id=?,updated_at=?
 WHERE id=? AND status='pending' AND token_hash=? AND expires_at>? RETURNING ` + invitationColumns
	row, err := tursodb.QueryOne[invitationRow](ctx, s.db, q, claim.SubjectType, claim.SubjectID, tursodb.FormatTime(claim.Now), id, claim.TokenHash, tursodb.FormatTime(claim.Now))
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
	q := `UPDATE invitations SET status='accepted',accepted_at=?,updated_at=?
 WHERE id=? AND status='accepting' AND token_hash=? AND resolved_subject_type=? AND resolved_subject_id=? RETURNING ` + invitationColumns
	row, err := tursodb.QueryOne[invitationRow](ctx, s.db, q, tursodb.FormatTime(claim.Now), tursodb.FormatTime(claim.Now), id, claim.TokenHash, claim.SubjectType, claim.SubjectID)
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

// marshalMetadata renders opaque host invitation metadata as TEXT JSON. A nil or
// empty map stores '{}' — never JSON null, which would bypass the column DEFAULT —
// so it reads back as a non-nil empty map (the uniform round-trip contract). The
// domain has already bounded the map.
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

// unmarshalMetadata parses stored TEXT JSON into a NON-NIL map: '{}', NULL, and an
// empty column all yield a non-nil empty map; malformed stored JSON is an error.
func unmarshalMetadata(s string) (map[string]string, error) {
	m := map[string]string{}
	if s == "" || s == "null" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}
