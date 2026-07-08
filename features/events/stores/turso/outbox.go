package turso

import (
	"context"
	"database/sql"
	"time"

	"github.com/gopernicus/gopernicus/features/events/logic/outbox"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
)

// outboxColumns is the event_outbox projection, in scanEntry's order.
const outboxColumns = "event_id, event_type, occurred_at, correlation_id, payload, aggregate_type, aggregate_id, tenant_id, created_at, published_at"

// Compile-time seam: Store fills the exact outbox.EntryRepository port.
var _ outbox.EntryRepository = (*Store)(nil)

// Store implements outbox.EntryRepository over a libSQL database. event_id is the
// primary key and the at-least-once de-dupe key, so a duplicate append surfaces
// as errs.ErrAlreadyExists (mapped from the UNIQUE constraint by the connector).
// Construct it via New, which runs the boot-time table probe (design §5).
type Store struct {
	db *tursodb.DB
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
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		return insertRecords(ctx, tx, recs...)
	})
}

// AppendTx persists records inside the caller's transaction tx — the
// dialect-typed transactional appender (design §5). It shares the emitting
// feature store's commit, so the domain rows and the outbox rows land atomically
// (true outbox semantics). No feature core ever sees *tursodb.Tx; a future
// emitting store consumer-declares a matching port that Store satisfies
// structurally. A duplicate event_id returns errs.ErrAlreadyExists and (because
// the caller's tx rolls back) commits nothing. Appending zero records is a no-op.
func (s *Store) AppendTx(ctx context.Context, tx *tursodb.Tx, recs ...sdkevents.Record) error {
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
	const q = `SELECT ` + outboxColumns + ` FROM event_outbox
		WHERE published_at IS NULL
		ORDER BY created_at, event_id LIMIT ?`
	lim := limit
	if lim <= 0 {
		lim = -1 // SQLite: no limit
	}
	rows, err := s.db.Query(ctx, q, lim)
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
	return out, tursodb.MapError(rows.Err())
}

// MarkPublished records the entry with the given eventID as published. It is
// idempotent: the WHERE published_at IS NULL guard makes re-marking an
// already-published entry a zero-row no-op, and an unknown eventID matches
// nothing — both return nil, so the poller can retry a mark without a hard error.
func (s *Store) MarkPublished(ctx context.Context, eventID string) error {
	const q = `UPDATE event_outbox SET published_at = ? WHERE event_id = ? AND published_at IS NULL`
	_, err := s.db.Exec(ctx, q, tursodb.FormatTime(time.Now().UTC()), eventID)
	return err
}

// PurgePublished deletes published entries whose created_at is strictly before
// the cutoff and returns the number removed. Unpublished entries are never purged
// regardless of age.
func (s *Store) PurgePublished(ctx context.Context, before time.Time) (int, error) {
	const q = `DELETE FROM event_outbox WHERE published_at IS NOT NULL AND created_at < ?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, tursodb.FormatTime(before.UTC()))
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
func insertRecords(ctx context.Context, q tursodb.Querier, recs ...sdkevents.Record) error {
	const insert = `INSERT INTO event_outbox (` + outboxColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`
	now := tursodb.FormatTime(time.Now().UTC())
	for _, rc := range recs {
		if _, err := q.Exec(ctx, insert,
			rc.EventID, rc.Type, tursodb.FormatTime(rc.OccurredAt), rc.CorrelationID,
			payloadValue(rc.Payload), nullStr(rc.AggregateType), nullStr(rc.AggregateID),
			nullStr(rc.TenantID), now); err != nil {
			return err
		}
	}
	return nil
}

// scanEntry scans one event_outbox row into an outbox.Entry, mapping
// sql.ErrNoRows to errs.ErrNotFound.
func scanEntry(sc scanner) (outbox.Entry, error) {
	var (
		e                        outbox.Entry
		occurredAt, createdAt    string
		correlationID, payload   string
		aggType, aggID, tenantID sql.NullString
		publishedAt              sql.NullString
	)
	err := sc.Scan(
		&e.EventID, &e.Type, &occurredAt, &correlationID, &payload,
		&aggType, &aggID, &tenantID, &createdAt, &publishedAt,
	)
	if err != nil {
		return outbox.Entry{}, tursodb.MapError(err)
	}

	e.CorrelationID = correlationID
	e.Payload = []byte(payload)
	e.AggregateType = nullStrPtr(aggType)
	e.AggregateID = nullStrPtr(aggID)
	e.TenantID = nullStrPtr(tenantID)

	if e.OccurredAt, err = tursodb.ParseTime(occurredAt); err != nil {
		return outbox.Entry{}, err
	}
	if e.CreatedAt, err = tursodb.ParseTime(createdAt); err != nil {
		return outbox.Entry{}, err
	}
	if publishedAt.Valid && publishedAt.String != "" {
		t, err := tursodb.ParseTime(publishedAt.String)
		if err != nil {
			return outbox.Entry{}, err
		}
		e.PublishedAt = &t
	}
	return e, nil
}

// payloadValue returns a non-empty JSON text for storage: the raw payload, or
// "{}" when it is empty (the column is NOT NULL).
func payloadValue(p []byte) string {
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}

// nullStr renders a nullable *string metadata field for storage: nil stores as
// NULL, any set value verbatim.
func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// nullStrPtr converts a scanned nullable column back to the *string the Record
// carries: an absent (NULL) column reads back as nil.
func nullStrPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}
