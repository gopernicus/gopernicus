-- The transactional-outbox ledger (migration source "events"; unique vs
-- "cms"/"auth"/"jobs"). One row per durable event: the events.Record envelope
-- (event_id PK / de-dupe key, event_type, occurred_at, correlation_id, payload,
-- and the nullable aggregate/tenant metadata) plus delivery bookkeeping
-- (created_at = append time; published_at nullable, NULL = unpublished). Turso
-- dialect: timestamps are fixed-width ISO-8601 TEXT (lexicographic order ==
-- chronological order, which the ListUnpublished ORDER BY relies on); payload is
-- TEXT JSON. published_at NULL is the "not yet emitted" sentinel the poller
-- drains.
CREATE TABLE IF NOT EXISTS event_outbox (
    event_id       TEXT NOT NULL,
    event_type     TEXT NOT NULL,
    occurred_at    TEXT NOT NULL,
    correlation_id TEXT NOT NULL DEFAULT '',
    payload        TEXT NOT NULL DEFAULT '{}',
    aggregate_type TEXT,
    aggregate_id   TEXT,
    tenant_id      TEXT,
    created_at     TEXT NOT NULL,
    published_at   TEXT,
    CONSTRAINT event_outbox_pk PRIMARY KEY (event_id)
);

-- Partial index over the poller's hot path (SQLite supports partial indexes):
-- the unpublished drain scans WHERE published_at IS NULL ORDER BY created_at.
CREATE INDEX IF NOT EXISTS idx_event_outbox_unpublished
    ON event_outbox (created_at) WHERE published_at IS NULL;
