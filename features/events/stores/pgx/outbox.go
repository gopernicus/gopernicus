package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/events/domain/outbox"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
)

// outboxColumns is the event_outbox projection, in scanEntry's order.
const outboxColumns = "event_id, event_type, occurred_at, correlation_id, payload, aggregate_type, aggregate_id, tenant_id, created_at, published_at"

// Compile-time seam: Store fills the exact outbox.EntryRepository port.
var _ outbox.EntryRepository = (*Store)(nil)

// Store implements outbox.EntryRepository over a PostgreSQL database. event_id is
// the primary key and the at-least-once de-dupe key, so a duplicate append
// surfaces as errs.ErrAlreadyExists (mapped from the UNIQUE constraint by the
// connector). Construct it via New, which runs the boot-time table probe
// (design §5).
type Store struct {
	db *pgxdb.DB
}

// Append persists records in their own transaction — the non-transactional
// convenience path. The whole batch commits or none of it does, so a batch
// carrying a duplicate event_id (or a collision with an existing row) leaves the
// store untouched and returns errs.ErrAlreadyExists. Appending zero records is a
// no-op that returns nil.
func (s *Store) Append(ctx context.Context, recs ...sdkevents.Record) error {
	if len(recs) == 0 {
		return nil
	}
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		return insertRecords(ctx, tx, recs...)
	})
}

// AppendTx persists records inside the caller's transaction tx — the
// dialect-typed transactional appender (design §5). It shares the emitting
// feature store's commit, so the domain rows and the outbox rows land atomically
// (true outbox semantics). No feature core ever sees *pgxdb.Tx; a future emitting
// store consumer-declares a matching port that Store satisfies structurally. A
// duplicate event_id returns errs.ErrAlreadyExists and (because the caller's tx
// rolls back) commits nothing. Appending zero records is a no-op.
func (s *Store) AppendTx(ctx context.Context, tx *pgxdb.Tx, recs ...sdkevents.Record) error {
	if len(recs) == 0 {
		return nil
	}
	return insertRecords(ctx, tx, recs...)
}

// ListUnpublished returns up to limit unpublished entries (published_at NULL)
// ordered by created_at ascending — oldest first, so the poller drains in append
// order — with event_id breaking ties for determinism. A non-positive limit
// returns all unpublished entries.
func (s *Store) ListUnpublished(ctx context.Context, limit int) ([]outbox.Entry, error) {
	const base = `SELECT ` + outboxColumns + ` FROM event_outbox
		WHERE published_at IS NULL
		ORDER BY created_at, event_id `

	// A non-positive limit drains everything (LIMIT ALL); a positive limit binds
	// as $1 — one Query call either way, so rows keeps its inferred pgx type.
	limitClause := "LIMIT ALL"
	var args []any
	if limit > 0 {
		limitClause = "LIMIT $1"
		args = append(args, limit)
	}

	rows, err := s.db.Query(ctx, base+limitClause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []outbox.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, pgxdb.MapError(rows.Err())
}

// MarkPublished records the entry with the given eventID as published. It is
// idempotent: the WHERE published_at IS NULL guard makes re-marking an
// already-published entry a zero-row no-op, and an unknown eventID matches
// nothing — both return nil, so the poller can retry a mark without a hard error.
func (s *Store) MarkPublished(ctx context.Context, eventID string) error {
	const q = `UPDATE event_outbox SET published_at = $1 WHERE event_id = $2 AND published_at IS NULL`
	_, err := s.db.Exec(ctx, q, time.Now().UTC(), eventID)
	return err
}

// PurgePublished deletes published entries whose created_at is strictly before
// the cutoff and returns the number removed. Unpublished entries are never purged
// regardless of age.
func (s *Store) PurgePublished(ctx context.Context, before time.Time) (int, error) {
	const q = `DELETE FROM event_outbox WHERE published_at IS NOT NULL AND created_at < $1`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, before.UTC())
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// insertRecords writes each record as an unpublished row (published_at NULL),
// stamping created_at with the current time. It runs against a *DB connection or
// a *Tx (both satisfy Querier), so Append and AppendTx share one INSERT. A UNIQUE
// constraint violation on event_id is mapped to errs.ErrAlreadyExists by the
// connector's Exec.
func insertRecords(ctx context.Context, q pgxdb.Querier, recs ...sdkevents.Record) error {
	const insert = `INSERT INTO event_outbox (` + outboxColumns + `)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULL)`
	now := time.Now().UTC()
	for _, rc := range recs {
		if _, err := q.Exec(ctx, insert,
			rc.EventID, rc.Type, rc.OccurredAt.UTC(), rc.CorrelationID,
			payloadValue(rc.Payload), rc.AggregateType, rc.AggregateID,
			rc.TenantID, now); err != nil {
			return err
		}
	}
	return nil
}

// scanEntry scans one event_outbox row into an outbox.Entry, mapping pgx.ErrNoRows
// to errs.ErrNotFound via the connector's MapError. Nullable metadata columns scan
// into *string (NULL → nil), and published_at scans into a nullable *time.Time
// normalized to UTC via FromNullTimePtr (NULL → nil = unpublished).
func scanEntry(sc scanner) (outbox.Entry, error) {
	var (
		e                        outbox.Entry
		occurredAt, createdAt    time.Time
		payload                  []byte
		aggType, aggID, tenantID *string
		publishedAt              *time.Time
	)
	err := sc.Scan(
		&e.EventID, &e.Type, &occurredAt, &e.CorrelationID, &payload,
		&aggType, &aggID, &tenantID, &createdAt, &publishedAt,
	)
	if err != nil {
		return outbox.Entry{}, pgxdb.MapError(err)
	}

	e.Payload = payload
	e.AggregateType = aggType
	e.AggregateID = aggID
	e.TenantID = tenantID
	e.OccurredAt = occurredAt.UTC()
	e.CreatedAt = createdAt.UTC()
	e.PublishedAt = pgxdb.FromNullTimePtr(publishedAt)
	return e, nil
}

// payloadValue returns a non-empty JSON text for storage: the raw payload, or
// "{}" when it is empty (the column is NOT NULL). It is stored into a JSON (not
// JSONB) column, so these exact bytes round-trip verbatim.
func payloadValue(p []byte) string {
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}
