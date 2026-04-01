-- Thin transactional event outbox.
-- Guarantees atomicity between a business write and an event record.
-- A poller reads committed rows and publishes them to the event bus.

CREATE TABLE IF NOT EXISTS event_outbox (
    event_id     TEXT        NOT NULL PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published    BOOLEAN     NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_event_outbox_unpublished
    ON event_outbox (created_at)
    WHERE published = FALSE;
