package pgxdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	defaultMigrationSource = "default"

	// migrationsTable is the ledger relation name. It is qualified through
	// Schema.Table at every use site, never left to search_path.
	migrationsTable = "schema_migrations"

	// insufficientPrivilege is the SQLSTATE Postgres raises when the current
	// role lacks a required grant. The runner translates it into an error that
	// names the missing grant instead of leaking the driver message.
	insufficientPrivilege = "42501"
)

type migrationSource struct {
	Name string
	FS   fs.FS
	Dir  string
}

// MigrateOption configures RunMigrations.
type MigrateOption func(*migrateConfig)

type migrateConfig struct {
	schema Schema
}

// WithSchema applies the stream inside s: the migration transaction runs
// SET LOCAL search_path TO "<schema>" (transaction-scoped; the pool is never
// touched), creates the schema if absent, and keeps this stream's
// schema_migrations ledger there. The zero Schema is the default and leaves
// every statement unqualified.
func WithSchema(s Schema) MigrateOption {
	return func(c *migrateConfig) { c.schema = s }
}

// RunMigrations applies the host-owned SQL migration stream at migrationsDir.
// Migrations are applied in filename order, in one transaction, and recorded in
// schema_migrations with a checksum guard. Files prefixed with "_" are skipped.
//
// One stream per schema. Without WithSchema the stream is the database's
// default-schema stream: a host exports every pocket's migrations into a
// single merged, filename-ordered directory and calls RunMigrations once. All
// rows in a stream share one ledger source ("default"), so filenames must be
// unique within the stream and are never renumbered — the (source,
// version=filename) pair is the ledger identity, and renaming or splitting a
// stream into multiple calls over the same schema would make applied
// migrations look new. migrationsDir is only an fs.FS subpath, never a ledger
// namespace.
//
// With WithSchema(s) the stream lives in s and keeps its OWN
// schema_migrations ledger in s. A host that wants pocket tables in "auth"
// and its own tables in the default schema therefore makes two calls over two
// directories (migrations/auth/, migrations/). The ledgers are disjoint:
// filename uniqueness is per (schema, source) and cross-schema ordering is
// unexpressed by the ledgers, so the call order is the host's to fix and keep
// deterministic.
//
// One call is one transaction; two streams are two transactions. There is NO
// cross-schema atomicity: if the first call commits and the second fails, the
// database is partially upgraded and nothing rolls the committed stream back.
// Recovery is to correct the failing stream and rerun — every committed stream
// is idempotent by its own ledger. Cross-schema dependencies (foreign keys,
// views, functions) must be explicitly schema-qualified and applied after the
// stream they depend on; the per-schema ledgers do not encode that order.
func RunMigrations(ctx context.Context, db *DB, migrationsFS fs.FS, migrationsDir string, opts ...MigrateOption) error {
	var cfg migrateConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	if err := StatusCheck(ctx, db); err != nil {
		return fmt.Errorf("migration status check: %w", err)
	}

	src := migrationSource{Name: defaultMigrationSource, FS: migrationsFS, Dir: migrationsDir}

	slog.InfoContext(ctx, "running database migrations",
		slog.String("dir", migrationsDir), slog.String("schema", cfg.schema.String()))

	err := db.InTx(ctx, func(tx *Tx) error {
		if err := prepareSchema(ctx, tx, cfg.schema); err != nil {
			return err
		}
		if err := ensureMigrationsTable(ctx, tx, cfg.schema); err != nil {
			return fmt.Errorf("ensure migrations table: %w", err)
		}
		files, err := getMigrationFiles(src.FS, src.Dir)
		if err != nil {
			return fmt.Errorf("get migration files for %q: %w", src.Dir, err)
		}
		for _, file := range files {
			if err := applyMigration(ctx, tx, src, file, cfg.schema); err != nil {
				return fmt.Errorf("apply migration %s: %w", file, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	slog.InfoContext(ctx, "migrations complete")
	return nil
}

// prepareSchema makes the migration transaction run inside schema: it creates
// the namespace when absent, verifies the current role can use and extend it,
// and pins search_path to it for the duration of the transaction. The zero
// Schema is a no-op, so the default stream touches nothing.
//
// search_path is set STRICTLY, with no public fallback: with public on the
// path an "ALTER TABLE users" whose "<schema>".users is missing would silently
// alter the host's public.users. pg_catalog stays implicitly searched, so
// gen_random_uuid(), hashtext/hashtextextended, and pg_advisory_xact_lock
// resolve; host migrations calling extension functions installed in public
// must qualify them.
//
// Every grant failure — schema creation without CREATE ON DATABASE, or a
// pre-created schema without USAGE/CREATE — names the missing grant and wraps
// sdk.ErrForbidden.
func prepareSchema(ctx context.Context, tx *Tx, schema Schema) error {
	if schema.IsZero() {
		return nil
	}
	name := schema.String()

	var exists bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", name).Scan(&exists); err != nil {
		return fmt.Errorf("inspect schema %q: %w", name, MapError(err))
	}

	if !exists {
		if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS "`+name+`"`); err != nil {
			if isInsufficientPrivilege(err) {
				return fmt.Errorf("schema %q does not exist and could not be created; grant CREATE ON DATABASE or pre-create it (%v): %w",
					name, err, sdk.ErrForbidden)
			}
			return fmt.Errorf("create schema %q: %w", name, err)
		}
	}

	var usage, create bool
	if err := tx.QueryRow(ctx,
		"SELECT has_schema_privilege($1, 'USAGE'), has_schema_privilege($1, 'CREATE')", name).Scan(&usage, &create); err != nil {
		return fmt.Errorf("inspect privileges on schema %q: %w", name, MapError(err))
	}
	if !usage {
		return fmt.Errorf("schema %q exists but the current role lacks USAGE on it; grant USAGE ON SCHEMA %q: %w",
			name, name, sdk.ErrForbidden)
	}
	if !create {
		return fmt.Errorf("schema %q exists but the current role lacks CREATE on it; grant CREATE ON SCHEMA %q: %w",
			name, name, sdk.ErrForbidden)
	}

	if _, err := tx.Exec(ctx, `SET LOCAL search_path TO "`+name+`"`); err != nil {
		return fmt.Errorf("set search_path to %q: %w", name, err)
	}
	var current *string
	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&current); err != nil {
		return fmt.Errorf("read current_schema after setting search_path to %q: %w", name, MapError(err))
	}
	if current == nil || *current != name {
		got := "<null>"
		if current != nil {
			got = *current
		}
		return fmt.Errorf("search_path pin failed: current_schema() = %s, want %q", got, name)
	}
	return nil
}

// ExportMigrations copies the *.sql files at dir within migrationsFS into dst,
// creating dst if needed. It is the scaffold step a store adapter exposes to
// hosts: after export the files are the HOST's, applied by the host's own runner
// and extended with the host's own migrations in the same directory, under one
// app-owned schema_migrations ledger. Directory entries are skipped; the
// connector never reads or applies the host's copies.
func ExportMigrations(migrationsFS fs.FS, dir, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(migrationsFS, path.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ensureMigrationsTable creates schema_migrations with the (source, version)
// shape when it does not already exist. Existence is probed via to_regclass
// (never sqlite_master/PRAGMA). No legacy-adoption path exists: no pre-(source,
// version) Postgres databases are in scope. With a schema set the ledger is
// addressed explicitly, never through search_path.
func ensureMigrationsTable(ctx context.Context, tx *Tx, schema Schema) error {
	exists, err := migrationsTableExists(ctx, tx, schema)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return createMigrationsTable(ctx, tx, schema)
}

// migrationsTableExists reports whether schema_migrations exists, using
// to_regclass (returns NULL for an absent relation).
func migrationsTableExists(ctx context.Context, tx *Tx, schema Schema) (bool, error) {
	var regclass *string
	query := "SELECT to_regclass('" + schema.Table(migrationsTable) + "')"
	if err := tx.QueryRow(ctx, query).Scan(&regclass); err != nil {
		return false, fmt.Errorf("inspect schema_migrations: %w", err)
	}
	return regclass != nil, nil
}

func createMigrationsTable(ctx context.Context, tx *Tx, schema Schema) error {
	query := `
		CREATE TABLE IF NOT EXISTS ` + schema.Table(migrationsTable) + ` (
			source     TEXT NOT NULL,
			version    TEXT NOT NULL,
			checksum   TEXT NOT NULL,
			raw_sql    TEXT,
			applied_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (source, version)
		)
	`
	if _, err := tx.Exec(ctx, query); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func getMigrationFiles(migrationsFS fs.FS, dir string) ([]string, error) {
	var files []string

	err := fs.WalkDir(migrationsFS, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if !d.IsDir() && strings.HasSuffix(name, ".sql") && !strings.HasPrefix(name, "_") {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

// applyMigration applies one file for a source. Identity is (source, version).
// If the migration is already recorded under the source, its checksum is
// verified and it is skipped; otherwise the DDL runs and the row is recorded.
// The ledger reads and writes are explicitly qualified by schema; the DDL
// itself resolves against the transaction's search_path.
func applyMigration(ctx context.Context, tx *Tx, src migrationSource, file string, schema Schema) error {
	version := file
	ledger := schema.Table(migrationsTable)

	content, err := fs.ReadFile(src.FS, filepath.Join(src.Dir, file))
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(content))

	// Already applied under this source?
	var existingChecksum string
	err = tx.QueryRow(ctx,
		"SELECT checksum FROM "+ledger+" WHERE source = $1 AND version = $2",
		src.Name, version,
	).Scan(&existingChecksum)
	if err == nil {
		if existingChecksum != checksum {
			return fmt.Errorf("checksum mismatch: migration %s:%s was modified after being applied (expected %s, got %s)",
				src.Name, version, existingChecksum, checksum)
		}
		slog.InfoContext(ctx, "migration already applied",
			slog.String("source", src.Name), slog.String("version", version))
		return nil
	}
	if !errors.Is(err, jackpgx.ErrNoRows) {
		return fmt.Errorf("query schema_migrations: %w", err)
	}

	// Fresh migration: apply DDL and record it.
	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("execute migration DDL: %w", err)
	}

	appliedAt := time.Now().UTC()
	if _, err := tx.Exec(ctx,
		"INSERT INTO "+ledger+" (source, version, checksum, raw_sql, applied_at) VALUES ($1, $2, $3, $4, $5)",
		src.Name, version, checksum, string(content), appliedAt,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	slog.InfoContext(ctx, "migration applied",
		slog.String("source", src.Name),
		slog.String("version", version),
		slog.String("checksum", checksum[:8]),
	)
	return nil
}

// isInsufficientPrivilege reports whether err is Postgres' 42501.
func isInsufficientPrivilege(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == insufficientPrivilege
}
