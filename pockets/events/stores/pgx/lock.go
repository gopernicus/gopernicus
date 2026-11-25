package pgx

import (
	"context"
	"fmt"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// lockKeyPrefix namespaces the advisory-lock key text. The schema-qualified
	// outbox table follows it, so outboxes in different schemas lock
	// independently.
	lockKeyPrefix = "gopernicus.events.outbox_lock:"
)

// Compile-time seam: Lock fills the outbox.Lock port.
var _ outbox.Lock = (*Lock)(nil)

// Lock is the PostgreSQL outbox.Lock: a transaction-scoped advisory lock
// (pg_try_advisory_xact_lock) held for exactly one batch. Give every poller on
// every instance its own Lock over the same database and schema with
// outbox.WithLock, and one drains at a time. Nothing is held between batches,
// so it works through a transaction-mode pooler (PgBouncer pool_mode=transaction)
// and there is nothing to release at shutdown. It needs no migration and no
// grant beyond CONNECT.
//
// The pool needs MaxConns of at least 2: the lock's transaction holds one
// connection while the batch queries on another, so a one-connection pool
// deadlocks. pgxdb's default pool is larger; this only bites a host that sets
// DB_MAX_CONNS=1. Count the extra connection when sizing DB_MAX_CONNS.
//
// While a batch runs, the lock's transaction holds one extra pool connection
// and sits idle in transaction. An idle_in_transaction_session_timeout shorter
// than a batch ends it mid-batch, and so does a dropped connection; another
// instance can then start a batch while this one finishes. Delivery is at least
// once either way, so consumers deduplicate on EventID.
type Lock struct {
	db  *pgxdb.DB
	key string
}

// NewLock returns the outbox lock for db. WithSchema must match the outbox
// store's, because the key derives from the schema-qualified outbox table.
// NewLock does not probe the table, so a host dispatcher can hold the lock
// without a Store. The pool must allow at least 2 connections (see Lock).
func NewLock(db *pgxdb.DB, opts ...Option) (*Lock, error) {
	if db == nil {
		return nil, fmt.Errorf("events outbox lock: database is required: %w", sdk.ErrInvalidInput)
	}
	var cfg config
	for _, o := range opts {
		if o == nil {
			return nil, fmt.Errorf("events pgx: nil option: %w", sdk.ErrInvalidInput)
		}
		o(&cfg)
	}
	return &Lock{db: db, key: lockKeyPrefix + cfg.schema.Table(outboxTable)}, nil
}

// TryRun runs fn while holding the outbox's advisory lock. It begins a
// transaction, tries the lock, and rolls back when fn returns, errors, or
// panics, which releases it. The lock's transaction is private: fn's ctx is
// unchanged, so the batch's reads and marks run on their own connections and a
// failed batch does not undo marks already made. When another holder has the
// lock it returns (false, nil) without running fn. A failure to end the lock's
// transaction after fn ran is ignored: the lock is released either way.
func (l *Lock) TryRun(ctx context.Context, fn func(ctx context.Context) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	tx, err := l.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var held bool
	if err := tx.QueryRow(ctx,
		`SELECT pg_catalog.pg_try_advisory_xact_lock(pg_catalog.hashtextextended($1, 0))`, l.key,
	).Scan(&held); err != nil {
		return false, pgxdb.MapError(err)
	}
	if !held {
		return false, nil
	}
	return true, fn(ctx)
}
