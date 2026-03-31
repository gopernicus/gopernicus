package pgxdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// InTx executes fn inside a database transaction. If fn returns nil the
// transaction is committed; otherwise it is rolled back and the error is
// returned. The Querier must support Begin (both *pgxpool.Pool and pgx.Tx do).
//
// When db is already a pgx.Tx, Begin creates a savepoint so InTx composes
// naturally for nested transactional scopes.
func InTx(ctx context.Context, db Querier, fn func(tx pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
