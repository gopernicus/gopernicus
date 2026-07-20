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
)

const defaultMigrationSource = "default"

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
// shape when it does not already exist. Existence is probed via to_regclass
// (never sqlite_master/PRAGMA). No legacy-adoption path exists: no pre-(source,
// version) Postgres databases are in scope.
func ensureMigrationsTable(ctx context.Context, tx *Tx) error {
	exists, err := migrationsTableExists(ctx, tx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return createMigrationsTable(ctx, tx)
}

// migrationsTableExists reports whether schema_migrations exists, using
// to_regclass (returns NULL for an absent relation).
func migrationsTableExists(ctx context.Context, tx *Tx) (bool, error) {
	var regclass *string
	if err := tx.QueryRow(ctx, "SELECT to_regclass('schema_migrations')").Scan(&regclass); err != nil {
		return false, fmt.Errorf("inspect schema_migrations: %w", err)
	}
	return regclass != nil, nil
}

func createMigrationsTable(ctx context.Context, tx *Tx) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
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
		"SELECT checksum FROM schema_migrations WHERE source = $1 AND version = $2",
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
		"INSERT INTO schema_migrations (source, version, checksum, raw_sql, applied_at) VALUES ($1, $2, $3, $4, $5)",
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
