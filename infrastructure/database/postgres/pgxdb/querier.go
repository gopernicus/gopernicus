package pgxdb

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the common interface satisfied by both *pgxpool.Pool and pgx.Tx.
// Generated stores accept Querier, enabling transaction composition — callers
// can pass a pool for normal operations or a tx for transactional workflows.
// Begin starts a transaction (pool) or creates a savepoint (tx), allowing
// stores to manage transactions without knowing which context they run in.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	Begin(ctx context.Context) (pgx.Tx, error)
}
