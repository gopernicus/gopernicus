// Package pgx is the secrets pocket's PostgreSQL store: its own module so a
// host on another datastore never pulls pgx into its graph. It owns the SQL
// and the canonical migration; the host applies the migration pre-boot through
// its own runner (ExportMigrations, or pgxdb.RunMigrations over MigrationsFS)
// and the store never migrates.
//
// Every write and GetSecret run in one transaction with their audit rows, so
// a change without its record, or a record without its change, cannot be
// committed.
package pgx

import (
	"context"
	"embed"
	"fmt"
	"strings"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
)

// MigrationsFS holds the canonical migration the host applies pre-boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// probeTables is every relation this store reads or writes; StatusCheck
// probes each under the configured schema.
var probeTables = []string{"secrets_owner_keys", "secrets_values", "secrets_audit"}

// auditColumns is the secrets_audit column list in insert and scan order.
const auditColumns = "id, occurred_at, action, owner_type, owner_id, name, version, key_id, actor_type, actor_id, system, reason"

var _ vault.Repository = (*Repository)(nil)

// Option configures the store.
type Option func(*config)

type config struct {
	schema pgxdb.Schema
}

// WithSchema places every table in s. The zero Schema is the default,
// unqualified. Build s with pgxdb.NewSchema at the host; apply the migration
// into the same schema with pgxdb.WithSchema and call StatusCheck at boot.
func WithSchema(s pgxdb.Schema) Option { return func(c *config) { c.schema = s } }

// Repository implements vault.Repository over PostgreSQL.
type Repository struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

// NewRepository returns a store over db. It panics if db is nil; the caller
// owns the database lifecycle.
func NewRepository(db *pgxdb.DB, opts ...Option) *Repository {
	if db == nil {
		panic("secrets pgx: NewRepository received a nil database")
	}
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			panic("secrets pgx: NewRepository received a nil option")
		}
		opt(&cfg)
	}
	return &Repository{db: db, schema: cfg.schema}
}

// StatusCheck verifies every secrets table exists under the configured schema:
// the boot gate for WithSchema. A missing table is sdk.ErrNotFound naming it.
func StatusCheck(ctx context.Context, db *pgxdb.DB, opts ...Option) error {
	if db == nil {
		return fmt.Errorf("secrets pgx: nil database: %w", sdk.ErrInvalidInput)
	}
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return fmt.Errorf("secrets pgx: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	for _, name := range probeTables {
		table := cfg.schema.Table(name)
		if err := pgxdb.ProbeTable(ctx, db, table); err != nil {
			return fmt.Errorf("secrets store: %s table missing — apply the secrets migration before boot: %w", table, err)
		}
	}
	return nil
}

// ExportMigrations copies the canonical migration files into dst; after that
// they are the host's.
func ExportMigrations(dst string) error {
	return pgxdb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}

// table renders a table name under the configured schema: the one chokepoint
// every statement qualifies through.
func (r *Repository) table(name string) string { return r.schema.Table(name) }

func (r *Repository) GetOwnerKey(ctx context.Context, owner vault.Owner) (vault.OwnerKey, error) {
	return scanOwnerKey(r.db.QueryRow(ctx,
		`SELECT owner_type, owner_id, key_id, wrapped_key, wrapper_key_id, created_at, rotated_at FROM `+r.table("secrets_owner_keys")+
			` WHERE owner_type = $1 AND owner_id = $2`, owner.Type, owner.ID))
}

func (r *Repository) CreateOwnerKey(ctx context.Context, key vault.OwnerKey, audit []vault.AuditRecord) error {
	return r.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+r.table("secrets_owner_keys")+` (owner_type, owner_id, key_id, wrapped_key, wrapper_key_id, created_at, rotated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			key.Owner.Type, key.Owner.ID, key.KeyID, key.WrappedKey, key.WrapperKeyID, utc(key.CreatedAt), utc(key.RotatedAt)); err != nil {
			return err
		}
		return r.appendAudit(ctx, tx, audit)
	})
}

func (r *Repository) ReplaceOwnerKey(ctx context.Context, key vault.OwnerKey, expectedKeyID string, resealed []vault.SealedSecret, audit []vault.AuditRecord) error {
	return r.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		n, err := pgxdb.ExecAffecting(ctx, tx,
			`UPDATE `+r.table("secrets_owner_keys")+` SET key_id = $3, wrapped_key = $4, wrapper_key_id = $5, created_at = $6, rotated_at = $7 WHERE owner_type = $1 AND owner_id = $2 AND key_id = $8`,
			key.Owner.Type, key.Owner.ID, key.KeyID, key.WrappedKey, key.WrapperKeyID, utc(key.CreatedAt), utc(key.RotatedAt), expectedKeyID)
		if err != nil {
			return err
		}
		if n == 0 {
			current, err := scanOwnerKey(tx.QueryRow(ctx,
				`SELECT owner_type, owner_id, key_id, wrapped_key, wrapper_key_id, created_at, rotated_at FROM `+r.table("secrets_owner_keys")+
					` WHERE owner_type = $1 AND owner_id = $2 FOR UPDATE`, key.Owner.Type, key.Owner.ID))
			if err != nil {
				return err
			}
			return fmt.Errorf("secrets pgx: owner key is %q, expected %q: %w", current.KeyID, expectedKeyID, sdk.ErrConflict)
		}
		for _, sec := range resealed {
			if sec.Ref.Owner != key.Owner {
				return fmt.Errorf("secrets pgx: resealed %s belongs to another owner: %w", sec.Ref, sdk.ErrInvalidInput)
			}
			n, err := pgxdb.ExecAffecting(ctx, tx,
				`UPDATE `+r.table("secrets_values")+` SET ciphertext = $4, key_id = $5, updated_at = $6 WHERE owner_type = $1 AND owner_id = $2 AND name = $3`,
				sec.Ref.Owner.Type, sec.Ref.Owner.ID, sec.Ref.Name, sec.Ciphertext, sec.KeyID, utc(sec.UpdatedAt))
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("secrets pgx: resealed %s does not exist: %w", sec.Ref, sdk.ErrNotFound)
			}
		}
		return r.appendAudit(ctx, tx, audit)
	})
}

func (r *Repository) ListOwnerKeys(ctx context.Context, after string, limit int) ([]vault.OwnerKey, string, error) {
	if limit <= 0 {
		return nil, "", fmt.Errorf("secrets pgx: limit must be positive: %w", sdk.ErrInvalidInput)
	}
	rows, err := r.db.Query(ctx,
		`SELECT owner_type, owner_id, key_id, wrapped_key, wrapper_key_id, created_at, rotated_at FROM `+r.table("secrets_owner_keys")+
			` WHERE (owner_type || ':' || owner_id) COLLATE "C" > $1 ORDER BY (owner_type || ':' || owner_id) COLLATE "C" LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	keys := []vault.OwnerKey{}
	for rows.Next() {
		key, err := scanOwnerKey(rows)
		if err != nil {
			return nil, "", err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, "", pgxdb.MapError(err)
	}
	if len(keys) > limit {
		keys = keys[:limit]
		return keys, keys[limit-1].Owner.String(), nil
	}
	return keys, "", nil
}

func (r *Repository) GetSecret(ctx context.Context, ref vault.Ref, audit []vault.AuditRecord) (vault.SealedSecret, error) {
	var sealed vault.SealedSecret
	err := r.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var err error
		sealed, err = scanSealed(tx.QueryRow(ctx,
			`SELECT owner_type, owner_id, name, version, key_id, ciphertext, created_at, updated_at FROM `+r.table("secrets_values")+
				` WHERE owner_type = $1 AND owner_id = $2 AND name = $3`, ref.Owner.Type, ref.Owner.ID, ref.Name))
		if err != nil {
			return err
		}
		return r.appendAudit(ctx, tx, audit)
	})
	if err != nil {
		return vault.SealedSecret{}, err
	}
	return sealed, nil
}

func (r *Repository) PutSecret(ctx context.Context, sealed vault.SealedSecret, audit vault.AuditRecord) (vault.SealedSecret, error) {
	stored := sealed
	err := r.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		err := tx.QueryRow(ctx,
			`INSERT INTO `+r.table("secrets_values")+` (owner_type, owner_id, name, version, key_id, ciphertext, created_at, updated_at) VALUES ($1, $2, $3, 1, $4, $5, $6, $7)
			 ON CONFLICT (owner_type, owner_id, name) DO UPDATE SET version = `+r.table("secrets_values")+`.version + 1, key_id = EXCLUDED.key_id, ciphertext = EXCLUDED.ciphertext, updated_at = EXCLUDED.updated_at
			 RETURNING version, created_at, updated_at`,
			sealed.Ref.Owner.Type, sealed.Ref.Owner.ID, sealed.Ref.Name, sealed.KeyID, sealed.Ciphertext, utc(sealed.CreatedAt), utc(sealed.UpdatedAt),
		).Scan(&stored.Version, &stored.CreatedAt, &stored.UpdatedAt)
		if err != nil {
			return pgxdb.MapError(err)
		}
		stored.CreatedAt, stored.UpdatedAt = stored.CreatedAt.UTC(), stored.UpdatedAt.UTC()
		audit.Version = stored.Version
		return r.appendAudit(ctx, tx, []vault.AuditRecord{audit})
	})
	if err != nil {
		return vault.SealedSecret{}, err
	}
	return stored, nil
}

func (r *Repository) DeleteSecret(ctx context.Context, ref vault.Ref, audit []vault.AuditRecord) error {
	return r.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+r.table("secrets_values")+` WHERE owner_type = $1 AND owner_id = $2 AND name = $3`, ref.Owner.Type, ref.Owner.ID, ref.Name)
		if err != nil {
			return err
		}
		if n == 0 {
			return sdk.ErrNotFound
		}
		return r.appendAudit(ctx, tx, audit)
	})
}

func (r *Repository) DeleteOwner(ctx context.Context, owner vault.Owner, audit []vault.AuditRecord) error {
	return r.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM `+r.table("secrets_values")+` WHERE owner_type = $1 AND owner_id = $2`, owner.Type, owner.ID); err != nil {
			return err
		}
		n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+r.table("secrets_owner_keys")+` WHERE owner_type = $1 AND owner_id = $2`, owner.Type, owner.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return sdk.ErrNotFound
		}
		return r.appendAudit(ctx, tx, audit)
	})
}

func (r *Repository) ListSecrets(ctx context.Context, owner vault.Owner) ([]vault.Secret, error) {
	sealed, err := r.ListSealedSecrets(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]vault.Secret, len(sealed))
	for i, sec := range sealed {
		out[i] = vault.Secret{Ref: sec.Ref, Version: sec.Version, KeyID: sec.KeyID, CreatedAt: sec.CreatedAt, UpdatedAt: sec.UpdatedAt}
	}
	return out, nil
}

func (r *Repository) ListSealedSecrets(ctx context.Context, owner vault.Owner) ([]vault.SealedSecret, error) {
	rows, err := r.db.Query(ctx,
		`SELECT owner_type, owner_id, name, version, key_id, ciphertext, created_at, updated_at FROM `+r.table("secrets_values")+
			` WHERE owner_type = $1 AND owner_id = $2 ORDER BY name COLLATE "C"`, owner.Type, owner.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []vault.SealedSecret{}
	for rows.Next() {
		sec, err := scanSealed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	return out, nil
}

func (r *Repository) ListAudit(ctx context.Context, filter vault.AuditFilter, after string, limit int) ([]vault.AuditRecord, string, error) {
	if limit <= 0 {
		return nil, "", fmt.Errorf("secrets pgx: limit must be positive: %w", sdk.ErrInvalidInput)
	}
	if err := filter.Validate(); err != nil {
		return nil, "", err
	}
	args := []any{after}
	where := []string{`id COLLATE "C" > $1`}
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if filter.Owner != nil {
		add("owner_type = $%d", filter.Owner.Type)
		add("owner_id = $%d", filter.Owner.ID)
	}
	if filter.Name != "" {
		add("name = $%d", filter.Name)
	}
	if filter.Action != "" {
		add("action = $%d", string(filter.Action))
	}
	if !filter.Since.IsZero() {
		add("occurred_at >= $%d", utc(filter.Since))
	}
	if !filter.Until.IsZero() {
		add("occurred_at < $%d", utc(filter.Until))
	}
	if filter.ActorType != "" {
		add("actor_type = $%d", filter.ActorType)
	}
	if filter.ActorID != "" {
		add("actor_id = $%d", filter.ActorID)
	}
	if filter.System != "" {
		add("system = $%d", filter.System)
	}
	args = append(args, limit+1)
	rows, err := r.db.Query(ctx,
		`SELECT `+auditColumns+` FROM `+r.table("secrets_audit")+` WHERE `+strings.Join(where, " AND ")+
			` ORDER BY occurred_at, id COLLATE "C" LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []vault.AuditRecord{}
	for rows.Next() {
		var rec vault.AuditRecord
		if err := rows.Scan(&rec.ID, &rec.OccurredAt, &rec.Action, &rec.Owner.Type, &rec.Owner.ID, &rec.Name, &rec.Version, &rec.KeyID,
			&rec.Source.ActorType, &rec.Source.ActorID, &rec.Source.System, &rec.Source.Reason); err != nil {
			return nil, "", pgxdb.MapError(err)
		}
		rec.OccurredAt = rec.OccurredAt.UTC()
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, "", pgxdb.MapError(err)
	}
	if len(out) > limit {
		out = out[:limit]
		return out, out[limit-1].ID, nil
	}
	return out, "", nil
}

// appendAudit inserts records inside the caller's transaction. A duplicate id
// is sdk.ErrAlreadyExists and fails the whole transaction.
func (r *Repository) appendAudit(ctx context.Context, tx *pgxdb.Tx, records []vault.AuditRecord) error {
	for _, rec := range records {
		if rec.ID == "" {
			return fmt.Errorf("secrets pgx: audit record without id: %w", sdk.ErrInvalidInput)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+r.table("secrets_audit")+` (`+auditColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			rec.ID, utc(rec.OccurredAt), string(rec.Action), rec.Owner.Type, rec.Owner.ID, rec.Name, rec.Version, rec.KeyID,
			rec.Source.ActorType, rec.Source.ActorID, rec.Source.System, rec.Source.Reason); err != nil {
			return fmt.Errorf("secrets pgx: audit record %q: %w", rec.ID, err)
		}
	}
	return nil
}

func scanOwnerKey(row pgxdb.Scanner) (vault.OwnerKey, error) {
	var key vault.OwnerKey
	if err := row.Scan(&key.Owner.Type, &key.Owner.ID, &key.KeyID, &key.WrappedKey, &key.WrapperKeyID, &key.CreatedAt, &key.RotatedAt); err != nil {
		return vault.OwnerKey{}, pgxdb.MapError(err)
	}
	key.CreatedAt, key.RotatedAt = key.CreatedAt.UTC(), key.RotatedAt.UTC()
	return key, nil
}

func scanSealed(row pgxdb.Scanner) (vault.SealedSecret, error) {
	var sec vault.SealedSecret
	if err := row.Scan(&sec.Ref.Owner.Type, &sec.Ref.Owner.ID, &sec.Ref.Name, &sec.Version, &sec.KeyID, &sec.Ciphertext, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
		return vault.SealedSecret{}, pgxdb.MapError(err)
	}
	sec.CreatedAt, sec.UpdatedAt = sec.CreatedAt.UTC(), sec.UpdatedAt.UTC()
	return sec, nil
}

func utc(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }
