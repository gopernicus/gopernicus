// Package pgxfixtures provides database cleanup helpers for integration tests.
// These helpers work directly with the database pool for truncating and deleting test data.
//
// For creating test data, use the generated fixtures package.
// For database setup, use the testpgx package.
package pgxfixtures

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/stretchr/testify/require"
)

// TruncateTable truncates a table with CASCADE to remove all records.
func TruncateTable(t *testing.T, ctx context.Context, pool *pgxdb.Pool, tableName string) {
	t.Helper()

	query := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", tableName)
	_, err := pool.Exec(ctx, query)
	require.NoError(t, err, "failed to truncate table %s", tableName)
}

// TruncateTables truncates multiple tables with CASCADE in a single statement.
func TruncateTables(t *testing.T, ctx context.Context, pool *pgxdb.Pool, tableNames ...string) {
	t.Helper()

	if len(tableNames) == 0 {
		return
	}

	query := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", strings.Join(tableNames, ", "))
	_, err := pool.Exec(ctx, query)
	require.NoError(t, err, "failed to truncate tables")
}

// DeleteByID deletes a record from a table by its primary key.
func DeleteByID(t *testing.T, ctx context.Context, pool *pgxdb.Pool, tableName, pkColumn, id string) {
	t.Helper()

	query := fmt.Sprintf("DELETE FROM %s WHERE %s = $1", tableName, pkColumn)
	_, err := pool.Exec(ctx, query, id)
	require.NoError(t, err, "failed to delete from %s where %s = %s", tableName, pkColumn, id)
}

// TruncateSchema truncates all tables in the specified schema except system tables.
// Dynamically discovers tables from pg_tables so it stays in sync with the database.
// Excludes: schema_migrations (migration tracking).
func TruncateSchema(t *testing.T, ctx context.Context, pool *pgxdb.Pool, schemaName string) {
	t.Helper()

	query := `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = $1
		  AND tablename NOT IN ('schema_migrations')
		ORDER BY tablename
	`

	rows, err := pool.Query(ctx, query, schemaName)
	require.NoError(t, err, "failed to query tables in schema %s", schemaName)
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var tableName string
		err := rows.Scan(&tableName)
		require.NoError(t, err, "failed to scan table name")
		tables = append(tables, fmt.Sprintf("%s.%s", schemaName, tableName))
	}

	if len(tables) == 0 {
		return
	}

	TruncateTables(t, ctx, pool, tables...)
}

// TruncatePublicSchema truncates all tables in the public schema.
func TruncatePublicSchema(t *testing.T, ctx context.Context, pool *pgxdb.Pool) {
	t.Helper()
	TruncateSchema(t, ctx, pool, "public")
}
