package pgx

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/sdk"
)

// CacheMigrationsFS is the optional authorization-cache source. Apply the base
// authorization source through 0007 in the same schema before this source.
//
//go:embed cache_migrations/*.sql
var CacheMigrationsFS embed.FS

const CacheMigrationsDir = "cache_migrations"
const CacheMigrationSource = "authorization-cache"

func ExportCacheMigrations(dst string) error {
	return pgxdb.ExportMigrations(CacheMigrationsFS, CacheMigrationsDir, dst)
}

// WithCacheReads probes and exposes snapshots; migration installation activates
// invalidation database-wide, including writers that do not use this option.
func WithCacheReads() Option { return func(c *config) { c.cacheReads = true } }

func prepareCacheSource(ctx context.Context, db *pgxdb.DB, cfg *config) (*cacheSource, error) {
	if _, ambient := pgxdb.TxFromContext(ctx); ambient {
		return nil, fmt.Errorf("authorization cache: ambient construction: %w", sdk.ErrInvalidInput)
	}
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	names := []string{cfg.schema.Table("iam_relationships"), cfg.schema.Table("iam_roles"), cfg.schema.Table("iam_cache_invalidation")}
	rows, err := tx.Query(ctx, `SELECT n.nspname, c.relkind::text, c.relpersistence::text, c.relrowsecurity, c.relforcerowsecurity,
EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhparent=c.oid OR i.inhrelid=c.oid)
FROM unnest($1::text[]) names(name)
JOIN pg_catalog.pg_class c ON c.oid=pg_catalog.to_regclass(names.name)
JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace`, names)
	if err != nil {
		return nil, err
	}
	resolved, count := "", 0
	for rows.Next() {
		var schema, kind, persistence string
		var rls, forceRLS, inherited bool
		if err := rows.Scan(&schema, &kind, &persistence, &rls, &forceRLS, &inherited); err != nil {
			rows.Close()
			return nil, err
		}
		if rls || forceRLS || inherited || kind != "r" || persistence != "p" || resolved != "" && resolved != schema {
			rows.Close()
			return nil, decisions.ErrCacheVersion
		}
		resolved = schema
		count++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count != 3 {
		return nil, fmt.Errorf("authorization-cache requires fact/head tables in one schema: %w", decisions.ErrCacheVersion)
	}
	cfg.schema, err = pgxdb.NewSchema(resolved)
	if err != nil {
		return nil, err
	}
	// Fixed catalog rendering makes deparsed trigger definitions deterministic.
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
		return nil, err
	}
	if err := probeCacheDefinitions(ctx, tx, cfg.schema); err != nil {
		return nil, err
	}
	source := &cacheSource{db: db, cfg: *cfg}
	v, err := source.readHead(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	cfg.cacheBinding = fmt.Sprintf("%x", sha256.Sum256([]byte("pgx/authorization-check-reads/v1/"+resolved+"/"+v.Epoch)))
	source.cfg, source.epoch = *cfg, v.Epoch
	return source, nil
}

func probeCacheDefinitions(ctx context.Context, q pgxdb.Querier, schema pgxdb.Schema) error {
	head := schema.Table("iam_cache_invalidation")
	rows, err := q.Query(ctx, `SELECT attname, atttypid::regtype::text, attnotnull FROM pg_catalog.pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped`, head)
	if err != nil {
		return err
	}
	wantColumns := map[string]string{"slot": "integer", "protocol": "integer", "epoch": "text", "generation": "bigint"}
	for rows.Next() {
		var name, typ string
		var nonnull bool
		if err := rows.Scan(&name, &typ, &nonnull); err != nil {
			rows.Close()
			return err
		}
		if wantColumns[name] != typ || !nonnull {
			rows.Close()
			return decisions.ErrCacheVersion
		}
		delete(wantColumns, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(wantColumns) != 0 {
		return decisions.ErrCacheVersion
	}
	rows, err = q.Query(ctx, `SELECT pg_catalog.pg_get_constraintdef(oid), convalidated FROM pg_catalog.pg_constraint WHERE conrelid=$1::regclass`, head)
	if err != nil {
		return err
	}
	wantConstraints := map[string]bool{"PRIMARY KEY (slot)": true, "CHECK ((slot = 1))": true, "CHECK ((protocol = 1))": true, "CHECK ((generation >= 0))": true, "CHECK ((epoch ~ '^[0-9a-f]{32}$'::text))": true}
	for rows.Next() {
		var def string
		var validated bool
		if err := rows.Scan(&def, &validated); err != nil {
			rows.Close()
			return err
		}
		if !validated {
			rows.Close()
			return decisions.ErrCacheVersion
		}
		delete(wantConstraints, def)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(wantConstraints) != 0 {
		return decisions.ErrCacheVersion
	}
	data, err := CacheMigrationsFS.ReadFile(CacheMigrationsDir + "/0001_iam_cache_invalidation.sql")
	if err != nil {
		return err
	}
	body := strings.Split(string(data), "$cache$")
	if len(body) != 3 {
		return decisions.ErrCacheVersion
	}
	var quoted string
	if err := q.QueryRow(ctx, "SELECT pg_catalog.quote_ident($1)", schema.String()).Scan(&quoted); err != nil {
		return err
	}
	for _, table := range []string{"iam_relationships", "iam_roles"} {
		for _, op := range []string{"INSERT", "DELETE", "UPDATE", "TRUNCATE"} {
			name := table + "_cache_" + strings.ToLower(op)
			var definition, source, language, fnSchema, fnName string
			var invoker, settings, signature, enabled, normal bool
			err := q.QueryRow(ctx, `SELECT pg_catalog.pg_get_triggerdef(t.oid,false),p.prosrc,l.lanname,n.nspname,p.proname,
NOT p.prosecdef, p.proconfig IS NULL AND p.provolatile='v' AND p.proparallel='u',
p.pronargs=0 AND p.prorettype='trigger'::regtype,
t.tgenabled IN ('O','A'), NOT t.tgisinternal AND NOT t.tgdeferrable AND NOT t.tginitdeferred
FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid
JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace JOIN pg_catalog.pg_language l ON l.oid=p.prolang
WHERE t.tgrelid=$1::regclass AND t.tgname=$2`, schema.Table(table), name).Scan(&definition, &source, &language, &fnSchema, &fnName, &invoker, &settings, &signature, &enabled, &normal)
			if err != nil {
				return fmt.Errorf("authorization-cache trigger %s: %w", name, err)
			}
			mode, condition := "ROW", ""
			if op == "TRUNCATE" {
				mode = "STATEMENT"
			}
			if op == "UPDATE" {
				condition = " WHEN ((old.* IS DISTINCT FROM new.*))"
			}
			want := "CREATE TRIGGER " + name + " AFTER " + op + " ON " + quoted + "." + table + " FOR EACH " + mode + condition + " EXECUTE FUNCTION " + quoted + ".iam_advance_cache_generation()"
			if definition != want || source != body[1] || language != "plpgsql" || fnSchema != schema.String() || fnName != "iam_advance_cache_generation" || !invoker || !settings || !signature || !enabled || !normal {
				return fmt.Errorf("authorization-cache trigger %s incompatible: %w", name, decisions.ErrCacheVersion)
			}
		}
	}
	return nil
}

func (s *cacheSource) readHead(ctx context.Context, q pgxdb.Querier) (decisions.CacheVersion, error) {
	rows, err := q.Query(ctx, "SELECT slot,protocol,epoch,generation FROM "+s.cfg.schema.Table("iam_cache_invalidation"))
	if err != nil {
		return decisions.CacheVersion{}, err
	}
	defer rows.Close()
	var v decisions.CacheVersion
	count := 0
	for rows.Next() {
		var slot, protocol int64
		if err := rows.Scan(&slot, &protocol, &v.Epoch, &v.Generation); err != nil {
			return decisions.CacheVersion{}, fmt.Errorf("%w: %v", decisions.ErrCacheVersion, err)
		}
		count++
		if count != 1 || slot != 1 || protocol != 1 {
			return decisions.CacheVersion{}, decisions.ErrCacheVersion
		}
	}
	if err := rows.Err(); err != nil {
		return decisions.CacheVersion{}, err
	}
	if count != 1 || v.Validate() != nil || s.epoch != "" && v.Epoch != s.epoch {
		return decisions.CacheVersion{}, decisions.ErrCacheVersion
	}
	return v, nil
}
