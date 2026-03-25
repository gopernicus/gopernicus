package moderncdb

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// =============================================================================
// extractSQLOperation Tests
// =============================================================================

func TestExtractSQLOperation(t *testing.T) {
	tests := []struct {
		sql      string
		expected string
	}{
		// Standard operations
		{"SELECT * FROM users", "SELECT"},
		{"INSERT INTO users (name) VALUES (?)", "INSERT"},
		{"UPDATE users SET name = ?", "UPDATE"},
		{"DELETE FROM users WHERE id = ?", "DELETE"},

		// CTE queries — scans for SELECT/INSERT/UPDATE/DELETE in order,
		// so CTEs containing SELECT in the body match SELECT first.
		// This is the same simplification as pgxdb's extractSQLOperation.
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", "SELECT"},
		{"WITH cte AS (SELECT 1) INSERT INTO t SELECT * FROM cte", "SELECT"},
		{"WITH cte AS (SELECT 1) UPDATE t SET x = 1", "SELECT"},
		{"WITH cte AS (SELECT 1) DELETE FROM t", "SELECT"},
		{"WITH RECURSIVE cte AS (SELECT 1)", "SELECT"},
		// CTE without SELECT in the body matches the real operation
		{"WITH nums (n) AS (VALUES (1)) INSERT INTO t SELECT * FROM nums", "SELECT"},

		// SQLite-specific
		{"PRAGMA journal_mode=WAL", "PRAGMA"},
		{"PRAGMA table_info(users)", "PRAGMA"},
		{"CREATE TABLE users (id INTEGER PRIMARY KEY)", "CREATE"},
		{"CREATE INDEX idx_name ON users(name)", "CREATE"},
		{"DROP TABLE users", "DROP"},
		{"DROP INDEX idx_name", "DROP"},
		{"ALTER TABLE users ADD COLUMN email TEXT", "ALTER"},

		// Transaction
		{"BEGIN", "BEGIN"},
		{"BEGIN IMMEDIATE", "BEGIN"},
		{"COMMIT", "COMMIT"},
		{"ROLLBACK", "ROLLBACK"},

		// Edge cases
		{"", "QUERY"},
		{"   ", "QUERY"},
		{"SELECT", "SELECT"},
		{"  SELECT * FROM t  ", "SELECT"},
		{"\n\tSELECT * FROM t", "SELECT"},
	}

	for _, tt := range tests {
		t.Run(tt.expected+"_"+tt.sql, func(t *testing.T) {
			result := extractSQLOperation(tt.sql)
			if result != tt.expected {
				t.Errorf("extractSQLOperation(%q) = %q, want %q", tt.sql, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// handleSQLiteError Tests
// =============================================================================

func TestHandleSQLiteError_Nil(t *testing.T) {
	if err := handleSQLiteError(nil); err != nil {
		t.Errorf("handleSQLiteError(nil) = %v, want nil", err)
	}
}

func TestHandleSQLiteError_UniqueConstraint(t *testing.T) {
	err := fmt.Errorf("UNIQUE constraint failed: users.email")
	result := handleSQLiteError(err)
	if !errors.Is(result, ErrDuplicateEntry) {
		t.Errorf("handleSQLiteError(%v) = %v, want ErrDuplicateEntry", err, result)
	}
}

func TestHandleSQLiteError_ForeignKeyConstraint(t *testing.T) {
	err := fmt.Errorf("FOREIGN KEY constraint failed")
	result := handleSQLiteError(err)
	if !errors.Is(result, ErrConstraintFailed) {
		t.Errorf("handleSQLiteError(%v) = %v, want ErrConstraintFailed", err, result)
	}
}

func TestHandleSQLiteError_CheckConstraint(t *testing.T) {
	err := fmt.Errorf("CHECK constraint failed: age_positive")
	result := handleSQLiteError(err)
	if !errors.Is(result, ErrConstraintFailed) {
		t.Errorf("handleSQLiteError(%v) = %v, want ErrConstraintFailed", err, result)
	}
}

func TestHandleSQLiteError_NotNullConstraint(t *testing.T) {
	err := fmt.Errorf("NOT NULL constraint failed: users.name")
	result := handleSQLiteError(err)
	if !errors.Is(result, ErrConstraintFailed) {
		t.Errorf("handleSQLiteError(%v) = %v, want ErrConstraintFailed", err, result)
	}
}

func TestHandleSQLiteError_UnknownError(t *testing.T) {
	original := fmt.Errorf("some other error")
	result := handleSQLiteError(original)
	if result != original {
		t.Errorf("handleSQLiteError(%v) = %v, want original error", original, result)
	}
}

// =============================================================================
// Constructor Tests (real in-memory SQLite, no external deps)
// =============================================================================

func TestNewInMemory(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	if err := db.Ping(context.Background()); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := NewFile(path)
	if err != nil {
		t.Fatalf("NewFile(%q) error = %v", path, err)
	}
	defer db.Close()

	if err := db.Ping(context.Background()); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestStatusCheck(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	if err := StatusCheck(context.Background(), db); err != nil {
		t.Errorf("StatusCheck() error = %v", err)
	}
}

func TestUnderlying(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	if db.Underlying() == nil {
		t.Error("Underlying() returned nil")
	}
}

// =============================================================================
// Schema Operation Tests
// =============================================================================

func TestDB_ExecSchema(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	schema := `CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		email TEXT UNIQUE NOT NULL
	)`

	if err := db.ExecSchema(ctx, schema); err != nil {
		t.Fatalf("ExecSchema() error = %v", err)
	}

	exists, err := db.TableExists(ctx, "users")
	if err != nil {
		t.Fatalf("TableExists() error = %v", err)
	}
	if !exists {
		t.Error("TableExists(users) = false, want true")
	}
}

func TestDB_TableExists_NotFound(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	exists, err := db.TableExists(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("TableExists() error = %v", err)
	}
	if exists {
		t.Error("TableExists(nonexistent) = true, want false")
	}
}

// =============================================================================
// Transaction Tests
// =============================================================================

func TestDB_InTx_Commit(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	if err := db.ExecSchema(ctx, "CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("ExecSchema() error = %v", err)
	}

	err = db.InTx(ctx, func(tx *Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO items (name) VALUES (?)", "test")
		return err
	})
	if err != nil {
		t.Fatalf("InTx() error = %v", err)
	}

	// Verify committed
	var name string
	row := db.QueryRow(ctx, "SELECT name FROM items WHERE id = 1")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if name != "test" {
		t.Errorf("name = %q, want %q", name, "test")
	}
}

func TestDB_InTx_Rollback(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	if err := db.ExecSchema(ctx, "CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("ExecSchema() error = %v", err)
	}

	rollbackErr := fmt.Errorf("intentional rollback")
	err = db.InTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO items (name) VALUES (?)", "test"); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("InTx() error = %v, want %v", err, rollbackErr)
	}

	// Verify rolled back
	var count int
	row := db.QueryRow(ctx, "SELECT COUNT(*) FROM items")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (should have rolled back)", count)
	}
}

// =============================================================================
// Error Mapping Integration Tests
// =============================================================================

func TestDB_Exec_UniqueConstraint(t *testing.T) {
	db, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory() error = %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	if err := db.ExecSchema(ctx, "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT UNIQUE)"); err != nil {
		t.Fatalf("ExecSchema() error = %v", err)
	}

	if _, err := db.Exec(ctx, "INSERT INTO users (email) VALUES (?)", "test@example.com"); err != nil {
		t.Fatalf("first insert error = %v", err)
	}

	_, err = db.Exec(ctx, "INSERT INTO users (email) VALUES (?)", "test@example.com")
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Errorf("duplicate insert error = %v, want ErrDuplicateEntry", err)
	}
}
