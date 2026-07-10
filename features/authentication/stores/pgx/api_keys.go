package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// APIKeyStore implements apikey.APIKeyRepository over a PostgreSQL database.
// GetByHash honors the pinned contract (design §4.1): it selects by key_hash ALONE
// and returns ANY present row — revoked and expired included; NULL expires_at means
// never-expires; sdk.ErrNotFound only for a genuinely-unknown hash. There is NO
// expiry filtering in SQL — revocation and expiry are SERVICE-layer branches, so
// the service can attribute the blocked/failure audit event to the record's
// service account. ListByServiceAccount is keyset-paginated created_at DESC,
// id DESC.
type APIKeyStore struct {
	db *pgxdb.DB
}

var _ apikey.APIKeyRepository = (*APIKeyStore)(nil)

// NewAPIKeyStore returns an APIKeyStore backed by db.
func NewAPIKeyStore(db *pgxdb.DB) *APIKeyStore {
	return &APIKeyStore{db: db}
}

const apiKeyColumns = "id, service_account_id, name, key_prefix, key_hash, expires_at, revoked_at, last_used_at, created_at"

// apiKeyRow is the store-local, db-tagged projection of an api_keys row. The
// nullable expiry/revoke/last-used columns are pointers so a NULL reads back as
// the zero time (the domain's "not set" sentinels); toDomain maps them.
type apiKeyRow struct {
	ID               string     `db:"id"`
	ServiceAccountID string     `db:"service_account_id"`
	Name             string     `db:"name"`
	KeyPrefix        string     `db:"key_prefix"`
	KeyHash          string     `db:"key_hash"`
	ExpiresAt        *time.Time `db:"expires_at"`
	RevokedAt        *time.Time `db:"revoked_at"`
	LastUsedAt       *time.Time `db:"last_used_at"`
	CreatedAt        time.Time  `db:"created_at"`
}

func (r apiKeyRow) toDomain() apikey.APIKey {
	return apikey.APIKey{
		ID:               r.ID,
		ServiceAccountID: r.ServiceAccountID,
		Name:             r.Name,
		KeyPrefix:        r.KeyPrefix,
		KeyHash:          r.KeyHash,
		ExpiresAt:        pgxdb.FromNullTime(r.ExpiresAt),
		RevokedAt:        pgxdb.FromNullTime(r.RevokedAt),
		LastUsedAt:       pgxdb.FromNullTime(r.LastUsedAt),
		CreatedAt:        r.CreatedAt.UTC(),
	}
}

// Create persists a new key; a colliding key_hash → sdk.ErrAlreadyExists.
func (s *APIKeyStore) Create(ctx context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	args := pgx.NamedArgs{
		"service_account_id": k.ServiceAccountID,
		"name":               k.Name,
		"key_prefix":         k.KeyPrefix,
		"key_hash":           k.KeyHash,
		"expires_at":         pgxdb.NullTime(k.ExpiresAt),
		"revoked_at":         pgxdb.NullTime(k.RevokedAt),
		"last_used_at":       pgxdb.NullTime(k.LastUsedAt),
		"created_at":         k.CreatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if k.ID == "" {
		const q = `INSERT INTO api_keys (service_account_id, name, key_prefix, key_hash, expires_at, revoked_at, last_used_at, created_at)
			VALUES (@service_account_id, @name, @key_prefix, @key_hash, @expires_at, @revoked_at, @last_used_at, @created_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&k.ID); err != nil {
			return apikey.APIKey{}, pgxdb.MapError(err)
		}
		return k, nil
	}
	const q = `INSERT INTO api_keys (` + apiKeyColumns + `)
		VALUES (@id, @service_account_id, @name, @key_prefix, @key_hash, @expires_at, @revoked_at, @last_used_at, @created_at)`
	args["id"] = k.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return apikey.APIKey{}, err
	}
	return k, nil
}

// GetByHash returns the key for keyHash regardless of revocation/expiry; unknown
// hash → sdk.ErrNotFound. No expiry filter (the pinned contract).
func (s *APIKeyStore) GetByHash(ctx context.Context, keyHash string) (apikey.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE key_hash = @key_hash`
	row, err := queryOne[apiKeyRow](ctx, s.db, q, pgx.NamedArgs{"key_hash": keyHash})
	if err != nil {
		return apikey.APIKey{}, err
	}
	return row.toDomain(), nil
}

// ListByServiceAccount returns a cursor-paginated page of a service account's
// keys, ordered created_at DESC, id DESC.
func (s *APIKeyStore) ListByServiceAccount(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	q := pgxdb.ListQuery[apiKeyRow]{
		BaseSQL:      `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE service_account_id = @service_account_id`,
		Args:         pgx.NamedArgs{"service_account_id": serviceAccountID},
		OrderFields:  apikey.OrderFields,
		DefaultOrder: apikey.DefaultOrder,
		PK:           "id",
		OrderValueOf: func(r apiKeyRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r apiKeyRow) string { return r.ID },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[apikey.APIKey]{}, err
	}
	return crud.MapPage(page, apiKeyRow.toDomain), nil
}

// Revoke marks the key revoked as of revokedAt; unknown id → sdk.ErrNotFound.
func (s *APIKeyStore) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "UPDATE api_keys SET revoked_at = @revoked_at WHERE id = @id", pgx.NamedArgs{
		"revoked_at": revokedAt.UTC(),
		"id":         id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// TouchLastUsed records that the key authenticated at usedAt; unknown id →
// sdk.ErrNotFound. It is a plain UPDATE (callers treat it as best-effort).
func (s *APIKeyStore) TouchLastUsed(ctx context.Context, id string, usedAt time.Time) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "UPDATE api_keys SET last_used_at = @last_used_at WHERE id = @id", pgx.NamedArgs{
		"last_used_at": usedAt.UTC(),
		"id":           id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}
