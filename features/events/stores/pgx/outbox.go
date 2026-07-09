package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/events/domain/outbox"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
)

// outboxColumns is the event_outbox projection, matching outboxRow's db tags.
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

// outboxRow is the store-local, db-tagged projection of an event_outbox row that
// pgx.RowToStructByName scans into; toDomain maps it to the persistence-free
// domain entity. Nullable metadata columns scan into *string (NULL → nil) and
// published_at into *time.Time (NULL → nil = unpublished).
type outboxRow struct {
	EventID       string     `db:"event_id"`
	Type          string     `db:"event_type"`
	OccurredAt    time.Time  `db:"occurred_at"`
	CorrelationID string     `db:"correlation_id"`
	Payload       []byte     `db:"payload"`
	AggregateType *string    `db:"aggregate_type"`
	AggregateID   *string    `db:"aggregate_id"`
	TenantID      *string    `db:"tenant_id"`
	CreatedAt     time.Time  `db:"created_at"`
	PublishedAt   *time.Time `db:"published_at"`
}

func (r outboxRow) toDomain() outbox.Entry {
	return outbox.Entry{
		Record: sdkevents.Record{
			EventID:       r.EventID,
			Type:          r.Type,
			OccurredAt:    r.OccurredAt.UTC(),
			CorrelationID: r.CorrelationID,
			Payload:       r.Payload,
			AggregateType: r.AggregateType,
			AggregateID:   r.AggregateID,
			TenantID:      r.TenantID,
		},
		CreatedAt:   r.CreatedAt.UTC(),
		PublishedAt: pgxdb.FromNullTimePtr(r.PublishedAt),
	}
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
	// as @limit — one Query call either way.
	query := base + "LIMIT ALL"
	args := pgx.NamedArgs{}
	if limit > 0 {
		query = base + "LIMIT @limit"
		args["limit"] = limit
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByName[outboxRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}

	out := make([]outbox.Entry, len(items))
	for i, r := range items {
		out[i] = r.toDomain()
	}
	return out, nil
}

// MarkPublished records the entry with the given eventID as published. It is
// idempotent: the WHERE published_at IS NULL guard makes re-marking an
// already-published entry a zero-row no-op, and an unknown eventID matches
// nothing — both return nil, so the poller can retry a mark without a hard error.
func (s *Store) MarkPublished(ctx context.Context, eventID string) error {
	const q = `UPDATE event_outbox SET published_at = @published_at WHERE event_id = @event_id AND published_at IS NULL`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{"published_at": time.Now().UTC(), "event_id": eventID})
	return err
}

// PurgePublished deletes published entries whose created_at is strictly before
// the cutoff and returns the number removed. Unpublished entries are never purged
// regardless of age.
func (s *Store) PurgePublished(ctx context.Context, before time.Time) (int, error) {
	const q = `DELETE FROM event_outbox WHERE published_at IS NOT NULL AND created_at < @before`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{"before": before.UTC()})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// insertRecords writes the batch as unpublished rows (published_at NULL) in one
// UNNEST array-param INSERT, stamping every row's created_at with the current
// time. It runs against a *DB connection or a *Tx (both satisfy Querier), so
// Append and AppendTx share one statement. The rows all carry the same created_at
// (event_id is the ListUnpublished tie-break), so array order is not load-bearing
// for the oldest-first ordering guarantee. A UNIQUE constraint violation on
// event_id is mapped to errs.ErrAlreadyExists by the connector's Exec.
func insertRecords(ctx context.Context, q pgxdb.Querier, recs ...sdkevents.Record) error {
	n := len(recs)
	eventIDs := make([]string, n)
	types := make([]string, n)
	occurred := make([]time.Time, n)
	correlations := make([]string, n)
	payloads := make([]string, n)
	aggTypes := make([]*string, n)
	aggIDs := make([]*string, n)
	tenants := make([]*string, n)
	for i, rc := range recs {
		eventIDs[i] = rc.EventID
		types[i] = rc.Type
		occurred[i] = rc.OccurredAt.UTC()
		correlations[i] = rc.CorrelationID
		payloads[i] = payloadValue(rc.Payload)
		aggTypes[i] = rc.AggregateType
		aggIDs[i] = rc.AggregateID
		tenants[i] = rc.TenantID
	}

	const insert = `INSERT INTO event_outbox (` + outboxColumns + `)
		SELECT event_id, event_type, occurred_at, correlation_id, payload::json, aggregate_type, aggregate_id, tenant_id, @created_at, NULL
		FROM UNNEST(@event_ids::text[], @types::text[], @occurred::timestamptz[], @correlations::text[], @payloads::text[], @agg_types::text[], @agg_ids::text[], @tenants::text[])
			AS r(event_id, event_type, occurred_at, correlation_id, payload, aggregate_type, aggregate_id, tenant_id)`
	_, err := q.Exec(ctx, insert, pgx.NamedArgs{
		"event_ids":    eventIDs,
		"types":        types,
		"occurred":     occurred,
		"correlations": correlations,
		"payloads":     payloads,
		"agg_types":    aggTypes,
		"agg_ids":      aggIDs,
		"tenants":      tenants,
		"created_at":   time.Now().UTC(),
	})
	return err
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
