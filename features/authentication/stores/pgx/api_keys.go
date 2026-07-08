package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// APIKeyStore implements apikey.APIKeyRepository over a PostgreSQL database.
// GetByHash honors the pinned contract (design §4.1): it selects by key_hash ALONE
// and returns ANY present row — revoked and expired included; NULL expires_at means
// never-expires; errs.ErrNotFound only for a genuinely-unknown hash. There is NO
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

// Create persists a new key; a colliding key_hash → errs.ErrAlreadyExists.
func (s *APIKeyStore) Create(ctx context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	const q = `INSERT INTO api_keys (` + apiKeyColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := s.db.Exec(ctx, q,
		k.ID, k.ServiceAccountID, k.Name, k.KeyPrefix, k.KeyHash,
		pgxdb.NullTime(k.ExpiresAt), pgxdb.NullTime(k.RevokedAt), pgxdb.NullTime(k.LastUsedAt),
		k.CreatedAt.UTC(),
	)
	if err != nil {
		return apikey.APIKey{}, err
	}
	return k, nil
}

// GetByHash returns the key for keyHash regardless of revocation/expiry; unknown
// hash → errs.ErrNotFound. No expiry filter (the pinned contract).
func (s *APIKeyStore) GetByHash(ctx context.Context, keyHash string) (apikey.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE key_hash = $1`
	return scanAPIKey(s.db.QueryRow(ctx, q, keyHash))
}

// ListByServiceAccount returns a cursor-paginated page of a service account's
// keys, ordered created_at DESC, id DESC.
func (s *APIKeyStore) ListByServiceAccount(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	return pgxdb.ListPage(ctx, s.db, apiKeyColumns, "api_keys", "WHERE service_account_id = $1", []any{serviceAccountID}, orderField, "id", req,
		scanAPIKey,
		func(k apikey.APIKey) (time.Time, string) { return k.CreatedAt, k.ID },
	)
}

// Revoke marks the key revoked as of revokedAt; unknown id → errs.ErrNotFound.
func (s *APIKeyStore) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "UPDATE api_keys SET revoked_at = $1 WHERE id = $2", revokedAt.UTC(), id)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// TouchLastUsed records that the key authenticated at usedAt; unknown id →
// errs.ErrNotFound. It is a plain UPDATE (callers treat it as best-effort).
func (s *APIKeyStore) TouchLastUsed(ctx context.Context, id string, usedAt time.Time) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "UPDATE api_keys SET last_used_at = $1 WHERE id = $2", usedAt.UTC(), id)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// scanAPIKey scans one api_keys row, mapping pgx.ErrNoRows to errs.ErrNotFound via
// the connector's MapError. The nullable expiry/revoke/last-used columns read back
// as the zero time when NULL (the domain's "not set" sentinels).
func scanAPIKey(sc scanner) (apikey.APIKey, error) {
	var (
		k                                apikey.APIKey
		expiresAt, revokedAt, lastUsedAt *time.Time
		createdAt                        time.Time
	)
	if err := sc.Scan(
		&k.ID, &k.ServiceAccountID, &k.Name, &k.KeyPrefix, &k.KeyHash,
		&expiresAt, &revokedAt, &lastUsedAt, &createdAt,
	); err != nil {
		return apikey.APIKey{}, pgxdb.MapError(err)
	}
	k.ExpiresAt = pgxdb.FromNullTime(expiresAt)
	k.RevokedAt = pgxdb.FromNullTime(revokedAt)
	k.LastUsedAt = pgxdb.FromNullTime(lastUsedAt)
	k.CreatedAt = createdAt.UTC()
	return k, nil
}
