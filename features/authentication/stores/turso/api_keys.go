package turso

import (
	"context"
	"database/sql"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// APIKeyStore implements apikey.APIKeyRepository over a libSQL database. GetByHash
// honors the pinned contract (design §4.1): it selects by key_hash ALONE and
// returns ANY present row — revoked and expired included; NULL expires_at means
// never-expires; errs.ErrNotFound only for a genuinely-unknown hash. There is NO
// expiry filtering in SQL — revocation and expiry are SERVICE-layer branches, so
// the service can attribute the blocked/failure audit event to the record's
// service account. ListByServiceAccount is keyset-paginated created_at DESC,
// id DESC.
type APIKeyStore struct {
	db *tursodb.DB
}

var _ apikey.APIKeyRepository = (*APIKeyStore)(nil)

// NewAPIKeyStore returns an APIKeyStore backed by db.
func NewAPIKeyStore(db *tursodb.DB) *APIKeyStore {
	return &APIKeyStore{db: db}
}

const apiKeyColumns = "id, service_account_id, name, key_prefix, key_hash, expires_at, revoked_at, last_used_at, created_at"

// Create persists a new key; a colliding key_hash → errs.ErrAlreadyExists.
func (s *APIKeyStore) Create(ctx context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	const q = `INSERT INTO api_keys (` + apiKeyColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q,
		k.ID, k.ServiceAccountID, k.Name, k.KeyPrefix, k.KeyHash,
		tursodb.NullTime(k.ExpiresAt), tursodb.NullTime(k.RevokedAt), tursodb.NullTime(k.LastUsedAt),
		tursodb.FormatTime(k.CreatedAt),
	)
	if err != nil {
		return apikey.APIKey{}, err
	}
	return k, nil
}

// GetByHash returns the key for keyHash regardless of revocation/expiry; unknown
// hash → errs.ErrNotFound. No expiry filter (the pinned contract).
func (s *APIKeyStore) GetByHash(ctx context.Context, keyHash string) (apikey.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE key_hash = ?`
	return scanAPIKey(s.db.QueryRow(ctx, q, keyHash))
}

// ListByServiceAccount returns a cursor-paginated page of a service account's
// keys, ordered created_at DESC, id DESC.
func (s *APIKeyStore) ListByServiceAccount(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	q := tursodb.ListQuery[apikey.APIKey]{
		BaseSQL:      `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE service_account_id = ?`,
		Args:         []any{serviceAccountID},
		OrderFields:  apikey.OrderFields,
		DefaultOrder: apikey.DefaultOrder,
		PK:           "id",
		Scan:         scanAPIKey,
		OrderValueOf: func(k apikey.APIKey, _ string) any { return k.CreatedAt },
		PKOf:         func(k apikey.APIKey) string { return k.ID },
	}
	return tursodb.List(ctx, s.db, q, req)
}

// Revoke marks the key revoked as of revokedAt; unknown id → errs.ErrNotFound.
func (s *APIKeyStore) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, "UPDATE api_keys SET revoked_at = ? WHERE id = ?", tursodb.FormatTime(revokedAt), id)
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
	n, err := tursodb.ExecAffecting(ctx, s.db, "UPDATE api_keys SET last_used_at = ? WHERE id = ?", tursodb.FormatTime(usedAt), id)
	if err != nil {
		return err
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// scanAPIKey scans one api_keys row, mapping sql.ErrNoRows to errs.ErrNotFound via
// the connector's MapError. The nullable expiry/revoke/last-used columns read back
// as the zero time when NULL (the domain's "not set" sentinels).
func scanAPIKey(sc scanner) (apikey.APIKey, error) {
	var (
		k                                apikey.APIKey
		expiresAt, revokedAt, lastUsedAt sql.NullString
		createdAt                        string
	)
	if err := sc.Scan(
		&k.ID, &k.ServiceAccountID, &k.Name, &k.KeyPrefix, &k.KeyHash,
		&expiresAt, &revokedAt, &lastUsedAt, &createdAt,
	); err != nil {
		return apikey.APIKey{}, tursodb.MapError(err)
	}
	var err error
	if k.ExpiresAt, err = tursodb.ParseNullTime(expiresAt); err != nil {
		return apikey.APIKey{}, err
	}
	if k.RevokedAt, err = tursodb.ParseNullTime(revokedAt); err != nil {
		return apikey.APIKey{}, err
	}
	if k.LastUsedAt, err = tursodb.ParseNullTime(lastUsedAt); err != nil {
		return apikey.APIKey{}, err
	}
	if k.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return apikey.APIKey{}, err
	}
	return k, nil
}
