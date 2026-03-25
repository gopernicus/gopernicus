package pgxdb

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunMigrations runs all pending SQL migrations from the given embedded filesystem.
// Migrations are applied in alphabetical order (use numeric prefixes: 001_xxx.sql, 002_xxx.sql).
// Already-applied migrations are tracked in a schema_migrations table.
// This is a forward-only system — no rollbacks. Checksums prevent accidental modification
// of previously applied migrations.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsFS embed.FS, migrationsDir string) error {
	if err := StatusCheck(ctx, pool); err != nil {
		return fmt.Errorf("status check database: %w", err)
	}

	slog.Info("running database migrations")

	if err := createMigrationsTable(ctx, pool); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	files, err := getMigrationFiles(migrationsFS, migrationsDir)
	if err != nil {
		return fmt.Errorf("get migration files: %w", err)
	}

	for _, file := range files {
		if err := applyMigration(ctx, pool, migrationsFS, filepath.Join(migrationsDir, file)); err != nil {
			return fmt.Errorf("apply migration %s: %w", file, err)
		}
	}

	slog.Info("migrations complete")
	return nil
}

func createMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			checksum VARCHAR(64) NOT NULL,
			raw_sql TEXT,
			applied_at TIMESTAMP NOT NULL DEFAULT NOW(),
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`
	_, err := pool.Exec(ctx, query)
	return err
}

func getMigrationFiles(migrationsFS embed.FS, migrationsDir string) ([]string, error) {
	var files []string

	err := fs.WalkDir(migrationsFS, migrationsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".sql") {
			files = append(files, filepath.Base(path))
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, migrationsFS embed.FS, filePath string) error {
	version := filepath.Base(filePath)

	content, err := fs.ReadFile(migrationsFS, filePath)
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}

	checksum := fmt.Sprintf("%x", sha256.Sum256(content))

	var existingChecksum string
	var existingRawSQL *string
	err = pool.QueryRow(ctx, "SELECT checksum, raw_sql FROM schema_migrations WHERE version = $1", version).Scan(&existingChecksum, &existingRawSQL)
	if err == nil {
		if existingChecksum != checksum {
			return fmt.Errorf("checksum mismatch: migration %s has been modified after being applied (expected: %s, got: %s)",
				version, existingChecksum, checksum)
		}
		slog.Info("migration already applied", slog.String("version", version))
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("execute migration: %w", err)
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO schema_migrations (version, checksum, raw_sql) VALUES ($1, $2, $3)",
		version, checksum, string(content)); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	slog.Info("migration applied", slog.String("version", version), slog.String("checksum", checksum[:8]))
	return nil
}
