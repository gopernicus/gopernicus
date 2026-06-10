package moderncdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunMigrations applies pending SQL migrations from the given filesystem (an
// embed.FS, os.DirFS, or any fs.FS) — the moderncdb port of the pgxdb
// migration runner. Files prefixed with "_" are skipped; they are reflect
// artifacts (_public.sql), not migrations.
//
// Migrations are applied in alphabetical order (use numeric prefixes:
// 0001_xxx.sql, 0002_xxx.sql). Already-applied migrations are tracked in a
// schema_migrations table. This is a forward-only system — no rollbacks.
// sha256 checksums prevent accidental modification of previously applied
// files.
//
// Concurrent-boot guard: the entire runner — version check + apply loop —
// runs inside a single exclusive write lock acquired via BEGIN IMMEDIATE on a
// pinned *sql.Conn. Two processes booting against the same file serialize:
// the second blocks until the first commits, then sees every migration
// already applied and exits cleanly. (The IMMEDIATE statement is issued
// directly on the pinned connection rather than through BeginImmediate so no
// implicit BEGIN precedes it.)
func RunMigrations(ctx context.Context, db *DB, migrationsFS fs.FS, migrationsDir string) error {
	if err := StatusCheck(ctx, db); err != nil {
		return fmt.Errorf("migration status check: %w", err)
	}

	slog.Info("running database migrations")

	conn, err := db.Underlying().Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if err := createMigrationsTable(ctx, conn); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	files, err := getMigrationFiles(migrationsFS, migrationsDir)
	if err != nil {
		return fmt.Errorf("get migration files: %w", err)
	}

	for _, file := range files {
		if err := applyMigration(ctx, conn, migrationsFS, filepath.Join(migrationsDir, file)); err != nil {
			return fmt.Errorf("apply migration %s: %w", file, err)
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true

	slog.Info("migrations complete")
	return nil
}

// createMigrationsTable runs inside the caller's IMMEDIATE transaction.
func createMigrationsTable(ctx context.Context, conn *sql.Conn) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT    PRIMARY KEY,
			checksum   TEXT    NOT NULL,
			raw_sql    TEXT,
			applied_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)
	`
	if _, err := conn.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// getMigrationFiles returns the sorted .sql base names under dir, skipping
// "_"-prefixed reflect artifacts.
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

// applyMigration executes one migration file on the caller's locked conn and
// records it in schema_migrations. Checksum drift on an already-applied file
// is a hard error.
func applyMigration(ctx context.Context, conn *sql.Conn, migrationsFS fs.FS, filePath string) error {
	version := filepath.Base(filePath)

	content, err := fs.ReadFile(migrationsFS, filePath)
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}

	checksum := fmt.Sprintf("%x", sha256.Sum256(content))

	var existingChecksum string
	err = conn.QueryRowContext(ctx,
		"SELECT checksum FROM schema_migrations WHERE version = ?", version,
	).Scan(&existingChecksum)
	if err == nil {
		if existingChecksum != checksum {
			return fmt.Errorf("checksum mismatch: migration %s was modified after being applied (expected %s, got %s)",
				version, existingChecksum, checksum)
		}
		slog.Info("migration already applied", slog.String("version", version))
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("query schema_migrations: %w", err)
	}

	if _, err := conn.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("execute migration DDL: %w", err)
	}

	appliedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, checksum, raw_sql, applied_at) VALUES (?, ?, ?, ?)",
		version, checksum, string(content), appliedAt,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	slog.Info("migration applied",
		slog.String("version", version),
		slog.String("checksum", checksum[:8]),
	)
	return nil
}
