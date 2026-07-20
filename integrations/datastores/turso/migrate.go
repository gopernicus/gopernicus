package turso

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
)

// defaultMigrationSource is the internal source name used for the host-owned
// migration stream. The connector carries no feature/domain name.
const defaultMigrationSource = "default"

// legacySource is the placeholder source assigned to rows migrated from the old
// schema_migrations shape (version-PK-only, no source column). Such rows are
// adopted into their real source by checksum on the next Apply; unmatched ones
// stay inert (forward-only).
const legacySource = "_legacy"

type migrationSource struct {
	Name string
	FS   fs.FS
	Dir  string
}

// RunMigrations applies the host-owned SQL migration stream at migrationsDir.
// Migrations are applied in filename order, in one transaction, and recorded in
// schema_migrations with a checksum guard. Files prefixed with "_" are skipped.
//
// One database, one stream: a host exports every feature's migrations into a
// single merged, filename-ordered directory and calls RunMigrations once per
// database. All rows share one ledger source ("default"), so filenames must be
// globally unique across the merged stream and are never renumbered — the
// (source, version=filename) pair is the ledger identity, and renaming or
// splitting the stream into multiple calls would make applied migrations look
// new. migrationsDir is only an fs.FS subpath, never a ledger namespace.
//
// On first run against a database with the old schema_migrations shape,
// RunMigrations rebuilds the table to carry `source` and adopts legacy rows
// into the internal default source by matching (version, checksum).
func RunMigrations(ctx context.Context, db *DB, migrationsFS fs.FS, migrationsDir string) error {
	if err := StatusCheck(ctx, db); err != nil {
		return fmt.Errorf("migration status check: %w", err)
	}

	src := migrationSource{Name: defaultMigrationSource, FS: migrationsFS, Dir: migrationsDir}

	slog.InfoContext(ctx, "running database migrations", slog.String("dir", migrationsDir))

	err := db.InTx(ctx, func(tx *Tx) error {
		if err := ensureMigrationsTable(ctx, tx); err != nil {
			return fmt.Errorf("ensure migrations table: %w", err)
		}
		files, err := getMigrationFiles(src.FS, src.Dir)
		if err != nil {
			return fmt.Errorf("get migration files for %q: %w", src.Dir, err)
		}
		for _, file := range files {
			if err := applyMigration(ctx, tx, src, file); err != nil {
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
// shape, or migrates an existing old-shape table (version PK only) to it.
func ensureMigrationsTable(ctx context.Context, tx *Tx) error {
	exists, hasSource, err := inspectMigrationsTable(ctx, tx)
	if err != nil {
		return err
	}

	switch {
	case !exists:
		return createMigrationsTable(ctx, tx)
	case hasSource:
		return nil
	default:
		return migrateLegacyMigrationsTable(ctx, tx)
	}
}

// inspectMigrationsTable reports whether schema_migrations exists and whether it
// already carries the `source` column.
func inspectMigrationsTable(ctx context.Context, tx *Tx) (exists, hasSource bool, err error) {
	var name string
	err = tx.QueryRow(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'",
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("inspect schema_migrations: %w", err)
	}

	rows, err := tx.Query(ctx, "PRAGMA table_info(schema_migrations)")
	if err != nil {
		return false, false, fmt.Errorf("read schema_migrations columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			colName   string
			colType   string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dfltValue, &pk); err != nil {
			return false, false, fmt.Errorf("scan schema_migrations column: %w", err)
		}
		if colName == "source" {
			hasSource = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, false, fmt.Errorf("iterate schema_migrations columns: %w", err)
	}
	return true, hasSource, nil
}

func createMigrationsTable(ctx context.Context, tx *Tx) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			source     TEXT NOT NULL,
			version    TEXT NOT NULL,
			checksum   TEXT NOT NULL,
			raw_sql    TEXT,
			applied_at TEXT NOT NULL,
			PRIMARY KEY (source, version)
		)
	`
	if _, err := tx.Exec(ctx, query); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// migrateLegacyMigrationsTable rebuilds an old-shape schema_migrations table
// (version PK only) into the (source, version) shape, tagging existing rows with
// legacySource. They are adopted into their real source by applyMigration.
func migrateLegacyMigrationsTable(ctx context.Context, tx *Tx) error {
	stmts := []string{
		`CREATE TABLE schema_migrations_new (
			source     TEXT NOT NULL,
			version    TEXT NOT NULL,
			checksum   TEXT NOT NULL,
			raw_sql    TEXT,
			applied_at TEXT NOT NULL,
			PRIMARY KEY (source, version)
		)`,
		`INSERT INTO schema_migrations_new (source, version, checksum, raw_sql, applied_at)
			SELECT '` + legacySource + `', version, checksum, raw_sql, applied_at FROM schema_migrations`,
		`DROP TABLE schema_migrations`,
		`ALTER TABLE schema_migrations_new RENAME TO schema_migrations`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate legacy schema_migrations: %w", err)
		}
	}
	slog.InfoContext(ctx, "migrated schema_migrations to (source, version) shape")
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
// verified and it is skipped. If a legacy-sourced row matches by (version,
// checksum), it is adopted (re-sourced) rather than re-applied.
func applyMigration(ctx context.Context, tx *Tx, src migrationSource, file string) error {
	version := file

	content, err := fs.ReadFile(src.FS, filepath.Join(src.Dir, file))
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(content))

	// Already applied under this source?
	var existingChecksum string
	err = tx.QueryRow(ctx,
		"SELECT checksum FROM schema_migrations WHERE source = ? AND version = ?",
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
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("query schema_migrations: %w", err)
	}

	// Adopt a legacy row carrying the same version (filename) if its checksum
	// matches — the DDL already ran, so re-source the row instead of re-applying.
	var legacyChecksum string
	err = tx.QueryRow(ctx,
		"SELECT checksum FROM schema_migrations WHERE source = ? AND version = ?",
		legacySource, version,
	).Scan(&legacyChecksum)
	switch {
	case err == nil:
		if legacyChecksum != checksum {
			return fmt.Errorf("checksum mismatch: legacy migration %s was modified after being applied (expected %s, got %s)",
				version, legacyChecksum, checksum)
		}
		if _, err := tx.Exec(ctx,
			"UPDATE schema_migrations SET source = ? WHERE source = ? AND version = ?",
			src.Name, legacySource, version,
		); err != nil {
			return fmt.Errorf("adopt legacy migration: %w", err)
		}
		slog.InfoContext(ctx, "adopted legacy migration",
			slog.String("source", src.Name), slog.String("version", version))
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("query legacy schema_migrations: %w", err)
	}

	// Fresh migration: apply DDL and record it.
	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("execute migration DDL: %w", err)
	}

	appliedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(ctx,
		"INSERT INTO schema_migrations (source, version, checksum, raw_sql, applied_at) VALUES (?, ?, ?, ?, ?)",
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
