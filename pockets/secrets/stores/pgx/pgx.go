// Package pgx is the secrets pocket's PostgreSQL store: its own module so a
// host on another datastore never pulls pgx into its graph. It owns the SQL
// and the canonical migrations; the host applies them pre-boot through its
// own runner (ExportMigrations, or pgxdb.RunMigrations over MigrationsFS) and
// the store never migrates. Open probes the schema and the runtime role's
// posture on secrets_audit before returning the store.
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

// auditColumns is the secrets_audit column list in insert and scan order. seq
// and recorded_at are evidence for operators, filled by their defaults and
// never named here.
const auditColumns = "id, occurred_at, action, outcome, owner_type, owner_id, name, version, key_id, actor_type, actor_id, system, reason, request_id, trace_id"

// integrityMigration adds secrets_audit.outcome; StatusCheck names it when
// the column is missing.
const integrityMigration = "0002_secrets_audit_integrity.sql"

// auditTriggerFunction and auditTriggerBody are the trigger function
// 0003_secrets_audit_immutable.sql creates, its body exactly as the catalog's
// prosrc holds it: the posture probe verifies the installed function against
// it, and a test pins it to the migration file.
const (
	auditTriggerFunction = "secrets_audit_refuse_write"
	auditTriggerBody     = `
BEGIN
  IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN
    IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'secrets_audit is append-only' USING ERRCODE = '42501';
END
`
)

// probeColumnSQL reports whether the relation $1 names, resolved as the
// store's own statements resolve it, has the column $2.
const probeColumnSQL = `SELECT EXISTS (
	SELECT 1 FROM pg_catalog.pg_attribute
	WHERE attrelid = pg_catalog.to_regclass($1::pg_catalog.text) AND attname = $2::pg_catalog.text AND NOT attisdropped
)`

var _ vault.Repository = (*Repository)(nil)

// Option configures the store.
type Option func(*config)

type config struct {
	schema pgxdb.Schema
}

// WithSchema places every table in s. The zero Schema is the default,
// unqualified. Build s with pgxdb.NewSchema at the host and apply the
// migrations into the same schema with pgxdb.WithSchema.
func WithSchema(s pgxdb.Schema) Option { return func(c *config) { c.schema = s } }

// Repository implements vault.Repository over PostgreSQL.
type Repository struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

// Open is the store's boot constructor: it runs StatusCheck, then returns
// NewRepository. A database without the migrations, or a runtime role whose
// posture on secrets_audit production refuses, fails here instead of on the
// first request.
func Open(ctx context.Context, db *pgxdb.DB, opts ...Option) (vault.Repository, error) {
	if err := StatusCheck(ctx, db, opts...); err != nil {
		return nil, err
	}
	return NewRepository(db, opts...), nil
}

// NewRepository returns a store over db without probing anything, for a host
// that runs StatusCheck itself. It panics if db is nil; the caller owns the
// database lifecycle.
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

// StatusCheck is the boot gate Open runs. Every secrets table must exist
// under the configured schema and secrets_audit must have migration 0002's
// columns: either missing is sdk.ErrNotFound naming the table or the file.
// Then db.ProbeAppendOnly judges the runtime role on AuditTables under the
// mode db was opened with: production refuses a role that can modify or
// cannot append to secrets_audit, development logs one line. Run it at boot
// only; wired to a readiness probe it repeats that line on every probe.
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
	tables := make([]string, len(probeTables))
	for i, name := range probeTables {
		tables[i] = cfg.schema.Table(name)
	}
	if err := pgxdb.ProbeTables(ctx, db, tables...); err != nil {
		return fmt.Errorf("secrets store: apply the secrets migrations before boot: %w", err)
	}
	audit := cfg.schema.Table("secrets_audit")
	var exists bool
	if err := db.QueryRow(ctx, probeColumnSQL, audit, "outcome").Scan(&exists); err != nil {
		return fmt.Errorf("secrets store: probe %s columns: %w", audit, pgxdb.MapError(err))
	}
	if !exists {
		return fmt.Errorf("secrets store: %s.outcome column missing — apply migration %s before boot: %w", audit, integrityMigration, sdk.ErrNotFound)
	}
	return db.ProbeAppendOnly(ctx, AuditTables(opts...)...)
}

// AuditTables is the append-only table this store owns, as the pgxdb posture
// probe judges it: secrets_audit under the configured schema, verified
// against the trigger function its migrations install. StatusCheck probes
// it; a migrator preflights a runtime role with
// pgxdb.InspectTablePrivileges(ctx, q, role, AuditTables(opts...)...) and
// reaches the same verdict. It panics on a nil option.
func AuditTables(opts ...Option) []pgxdb.AppendOnlyTable {
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			panic("secrets pgx: AuditTables received a nil option")
		}
		opt(&cfg)
	}
	return []pgxdb.AppendOnlyTable{{
		Name:      cfg.schema.Table("secrets_audit"),
		Canonical: []pgxdb.TriggerBody{{Function: auditTriggerFunction, Body: auditTriggerBody}},
	}}
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
		return r.appendAudit(ctx, tx, stamped(audit, vault.ActionSecretRead, ref, sealed.Version, sealed.KeyID))
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
		var (
			version int
			keyID   string
		)
		if err := tx.QueryRow(ctx,
			`DELETE FROM `+r.table("secrets_values")+` WHERE owner_type = $1 AND owner_id = $2 AND name = $3 RETURNING version, key_id`,
			ref.Owner.Type, ref.Owner.ID, ref.Name).Scan(&version, &keyID); err != nil {
			return pgxdb.MapError(err)
		}
		return r.appendAudit(ctx, tx, stamped(audit, vault.ActionSecretDeleted, ref, version, keyID))
	})
}

func (r *Repository) DeleteOwner(ctx context.Context, owner vault.Owner, expected []string, audit []vault.AuditRecord) error {
	return r.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		removed, err := r.deleteOwnerSecrets(ctx, tx, owner)
		if err != nil {
			return err
		}
		n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+r.table("secrets_owner_keys")+` WHERE owner_type = $1 AND owner_id = $2`, owner.Type, owner.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return sdk.ErrNotFound
		}
		want := map[string]bool{}
		for _, name := range expected {
			want[name] = true
		}
		if len(want) != len(removed) {
			return fmt.Errorf("secrets pgx: owner %s held %d secrets, %d expected: %w", owner, len(removed), len(want), sdk.ErrConflict)
		}
		for name := range want {
			if _, ok := removed[name]; !ok {
				return fmt.Errorf("secrets pgx: owner %s held no secret %q: %w", owner, name, sdk.ErrConflict)
			}
		}
		for _, sec := range removed {
			audit = stamped(audit, vault.ActionSecretDeleted, sec.Ref, sec.Version, sec.KeyID)
		}
		return r.appendAudit(ctx, tx, audit)
	})
}

// deleteOwnerSecrets deletes every secret of owner inside tx and returns what
// it removed, by name.
func (r *Repository) deleteOwnerSecrets(ctx context.Context, tx *pgxdb.Tx, owner vault.Owner) (map[string]vault.SealedSecret, error) {
	rows, err := tx.Query(ctx, `DELETE FROM `+r.table("secrets_values")+` WHERE owner_type = $1 AND owner_id = $2 RETURNING name, version, key_id`, owner.Type, owner.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	removed := map[string]vault.SealedSecret{}
	for rows.Next() {
		sec := vault.SealedSecret{Ref: vault.Ref{Owner: owner}}
		if err := rows.Scan(&sec.Ref.Name, &sec.Version, &sec.KeyID); err != nil {
			return nil, pgxdb.MapError(err)
		}
		removed[sec.Ref.Name] = sec
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.MapError(err)
	}
	return removed, nil
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
	if filter.Outcome != "" {
		add("outcome = $%d", string(filter.Outcome))
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
		if err := rows.Scan(&rec.ID, &rec.OccurredAt, &rec.Action, &rec.Outcome, &rec.Owner.Type, &rec.Owner.ID, &rec.Name, &rec.Version, &rec.KeyID,
			&rec.Source.ActorType, &rec.Source.ActorID, &rec.Source.System, &rec.Source.Reason, &rec.RequestID, &rec.TraceID); err != nil {
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

func (r *Repository) AppendAudit(ctx context.Context, records []vault.AuditRecord) error {
	return r.db.InTx(ctx, func(tx *pgxdb.Tx) error { return r.appendAudit(ctx, tx, records) })
}

// appendAudit inserts records inside the caller's transaction. A duplicate id
// is sdk.ErrAlreadyExists, and a record without an id or refused by the
// table's action and outcome CHECKs sdk.ErrInvalidInput; either fails the
// whole transaction.
func (r *Repository) appendAudit(ctx context.Context, tx *pgxdb.Tx, records []vault.AuditRecord) error {
	for _, rec := range records {
		if rec.ID == "" {
			return fmt.Errorf("secrets pgx: audit record without id: %w", sdk.ErrInvalidInput)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+r.table("secrets_audit")+` (`+auditColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			rec.ID, utc(rec.OccurredAt), string(rec.Action), string(rec.Outcome), rec.Owner.Type, rec.Owner.ID, rec.Name, rec.Version, rec.KeyID,
			rec.Source.ActorType, rec.Source.ActorID, rec.Source.System, rec.Source.Reason, rec.RequestID, rec.TraceID); err != nil {
			return fmt.Errorf("secrets pgx: audit record %q: %w", rec.ID, pgxdb.MapError(err))
		}
	}
	return nil
}

// stamped returns audit with every action record naming ref given version
// and keyID: what the store read or removed, not what the caller expected.
func stamped(audit []vault.AuditRecord, action vault.Action, ref vault.Ref, version int, keyID string) []vault.AuditRecord {
	out := make([]vault.AuditRecord, len(audit))
	for i, rec := range audit {
		if rec.Action == action && rec.Owner == ref.Owner && rec.Name == ref.Name {
			rec.Version, rec.KeyID = version, keyID
		}
		out[i] = rec
	}
	return out
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
