-- The transactional-outbox ledger (migration source "events"; unique vs
-- "cms"/"auth"/"jobs"). One row per durable event: the events.Record envelope
-- (event_id PK / de-dupe key, event_type, occurred_at, correlation_id, payload,
-- and the nullable aggregate/tenant metadata) plus delivery bookkeeping
-- (created_at = append time; published_at nullable, NULL = unpublished). Postgres
-- flavor of the turso 0001 — identical filename so the version set matches across
-- dialects. Timestamps are TIMESTAMPTZ (postgres orders them natively; no
-- lexicographic-TEXT convention needed). published_at NULL is the "not yet
-- emitted" sentinel the poller drains.
--
-- payload is JSON, not JSONB: the payload is opaque to this store (no jsonb
-- operators or indexes are used), and JSON preserves the caller's exact bytes
-- while JSONB re-canonicalizes whitespace/key order. The shared storetest suite
-- asserts a byte-exact payload round-trip, which only JSON satisfies — same
-- decision and rationale as features/jobs/stores/pgx (jobs-v1 precedent). This is
-- a deliberate deviation from the design's illustrative JSONB.
CREATE TABLE IF NOT EXISTS event_outbox (
    event_id       TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL,
    correlation_id TEXT        NOT NULL DEFAULT '',
    payload        JSON        NOT NULL DEFAULT '{}',
    aggregate_type TEXT,
    aggregate_id   TEXT,
    tenant_id      TEXT,
    created_at     TIMESTAMPTZ NOT NULL,
    published_at   TIMESTAMPTZ,
    CONSTRAINT event_outbox_pk PRIMARY KEY (event_id)
);

-- Partial index over the poller's hot path: the unpublished drain scans
-- WHERE published_at IS NULL ORDER BY created_at.
CREATE INDEX IF NOT EXISTS idx_event_outbox_unpublished
    ON event_outbox (created_at) WHERE published_at IS NULL;
