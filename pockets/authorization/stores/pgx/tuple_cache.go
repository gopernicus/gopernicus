package pgx

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

// TupleCacheMigrationsFS holds optional raw tuple capture. Apply after the
// primary authorization migrations in the same database/schema.
//
//go:embed tuple_cache_migrations/*.sql
var TupleCacheMigrationsFS embed.FS

const TupleCacheMigrationsDir = "tuple_cache_migrations"
const TupleCacheMigrationSource = "authorization-cache-v2"

func ExportTupleCacheMigrations(dst string) error {
	return pgxdb.ExportMigrations(TupleCacheMigrationsFS, TupleCacheMigrationsDir, dst)
}

// WithTupleCache exposes an authoritative raw tuple source and read snapshots.
// The host-applied migration captures changes for every ordinary SQL writer.
func WithTupleCache() Option { return func(c *config) { c.tupleCache = true } }

func prepareTupleSource(ctx context.Context, db *pgxdb.DB, cfg *config) (*tupleSource, error) {
	if _, ambient := pgxdb.TxFromContext(ctx); ambient {
		return nil, fmt.Errorf("authorization cache: ambient construction: %w", sdk.ErrInvalidInput)
	}
	tx, err := db.BeginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	names := []string{cfg.schema.Table("iam_tuples"), cfg.schema.Table("iam_tuple_cache"), cfg.schema.Table("iam_tuple_outbox")}
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
			return nil, tuplecache.ErrUnavailable
		}
		resolved = schema
		count++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count != 3 {
		return nil, fmt.Errorf("authorization-cache requires fact/tuple cache tables in one schema: %w", tuplecache.ErrUnavailable)
	}
	cfg.schema, err = pgxdb.NewSchema(resolved)
	if err != nil {
		return nil, err
	}
	// Fixed catalog rendering makes deparsed trigger definitions deterministic.
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
		return nil, err
	}
	if err := probeTupleDefinitions(ctx, tx, cfg.schema); err != nil {
		return nil, err
	}
	source := &tupleSource{db: db, cfg: *cfg}
	identity, _, err := source.readReceipt(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	cfg.tupleBinding = fmt.Sprintf("%x", sha256.Sum256([]byte("pgx/authorization-tuples/v2/"+resolved+"/"+identity)))
	source.cfg, source.identity = *cfg, identity
	return source, nil
}

func probeTupleDefinitions(ctx context.Context, q pgxdb.Querier, schema pgxdb.Schema) error {
	head := schema.Table("iam_tuple_cache")
	rows, err := q.Query(ctx, `SELECT attname, atttypid::regtype::text, attnotnull FROM pg_catalog.pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped`, head)
	if err != nil {
		return err
	}
	wantColumns := map[string]string{"slot": "integer", "protocol": "integer", "identity": "text", "receipt": "text"}
	for rows.Next() {
		var name, typ string
		var nonnull bool
		if err := rows.Scan(&name, &typ, &nonnull); err != nil {
			rows.Close()
			return err
		}
		if wantColumns[name] != typ || !nonnull {
			rows.Close()
			return tuplecache.ErrUnavailable
		}
		delete(wantColumns, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(wantColumns) != 0 {
		return tuplecache.ErrUnavailable
	}
	rows, err = q.Query(ctx, `SELECT pg_catalog.pg_get_constraintdef(oid), convalidated FROM pg_catalog.pg_constraint WHERE conrelid=$1::regclass`, head)
	if err != nil {
		return err
	}
	wantConstraints := map[string]bool{"PRIMARY KEY (slot)": true, "CHECK ((slot = 1))": true, "CHECK ((protocol = 2))": true, "CHECK ((identity ~ '^[0-9a-f]{32}$'::text))": true}
	for rows.Next() {
		var def string
		var validated bool
		if err := rows.Scan(&def, &validated); err != nil {
			rows.Close()
			return err
		}
		if !validated {
			rows.Close()
			return tuplecache.ErrUnavailable
		}
		delete(wantConstraints, def)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(wantConstraints) != 0 {
		return tuplecache.ErrUnavailable
	}
	rows, err = q.Query(ctx, `SELECT attname, atttypid::regtype::text, attnotnull, attidentity::text
 FROM pg_catalog.pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped`, schema.Table("iam_tuple_outbox"))
	if err != nil {
		return err
	}
	type column struct {
		typ      string
		nonnull  bool
		identity string
	}
	wantOutbox := map[string]column{"id": {"bigint", true, "a"}, "before_tuple": {"jsonb", false, ""}, "after_tuple": {"jsonb", false, ""}, "reset": {"boolean", true, ""}}
	for rows.Next() {
		var name string
		var got column
		if err := rows.Scan(&name, &got.typ, &got.nonnull, &got.identity); err != nil {
			rows.Close()
			return err
		}
		want, ok := wantOutbox[name]
		if !ok || got != want {
			rows.Close()
			return tuplecache.ErrUnavailable
		}
		delete(wantOutbox, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(wantOutbox) != 0 {
		return tuplecache.ErrUnavailable
	}
	var primary bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
 WHERE conrelid=$1::regclass AND contype='p' AND convalidated AND pg_catalog.pg_get_constraintdef(oid)='PRIMARY KEY (id)')`, schema.Table("iam_tuple_outbox")).Scan(&primary); err != nil {
		return err
	}
	if !primary {
		return tuplecache.ErrUnavailable
	}
	data, err := TupleCacheMigrationsFS.ReadFile(TupleCacheMigrationsDir + "/0002_iam_tuple_cache.sql")
	if err != nil {
		return err
	}
	body := strings.Split(string(data), "$tuple$")
	if len(body) != 3 {
		return tuplecache.ErrUnavailable
	}
	var quoted string
	if err := q.QueryRow(ctx, "SELECT pg_catalog.quote_ident($1)", schema.String()).Scan(&quoted); err != nil {
		return err
	}
	for _, table := range []string{"iam_tuples"} {
		for _, op := range []string{"INSERT", "DELETE", "UPDATE", "TRUNCATE"} {
			name := table + "_tuple_" + strings.ToLower(op)
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
			want := "CREATE TRIGGER " + name + " AFTER " + op + " ON " + quoted + "." + table + " FOR EACH " + mode + condition + " EXECUTE FUNCTION " + quoted + ".iam_capture_tuple_change()"
			if definition != want || source != body[1] || language != "plpgsql" || fnSchema != schema.String() || fnName != "iam_capture_tuple_change" || !invoker || !settings || !signature || !enabled || !normal {
				return fmt.Errorf("authorization-cache trigger %s incompatible: %w", name, tuplecache.ErrUnavailable)
			}
		}
	}
	return nil
}

func (s *tupleSource) readReceipt(ctx context.Context, q pgxdb.Querier) (string, string, error) {
	rows, err := q.Query(ctx, "SELECT slot, protocol, identity, receipt FROM "+s.cfg.schema.Table("iam_tuple_cache"))
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	var identity, receipt string
	count := 0
	for rows.Next() {
		var slot, protocol int
		if err := rows.Scan(&slot, &protocol, &identity, &receipt); err != nil {
			return "", "", err
		}
		count++
		if count != 1 || slot != 1 || protocol != 2 || len(identity) != 32 || strings.Trim(identity, "0123456789abcdef") != "" || s.identity != "" && s.identity != identity {
			return "", "", tuplecache.ErrUnavailable
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if count != 1 {
		return "", "", tuplecache.ErrUnavailable
	}
	return identity, receipt, nil
}
