-- Append-only audit rail (design §5.1): there is no updated_at and no
-- update/delete path — an audit trail that can be rewritten is not an audit trail.
-- user_id and actor_type/actor_id are optional (a failed login for an unknown
-- email has neither a user nor a distinct actor). details holds JSONB — a
-- nil/empty map stores '{}' and reads back as a NON-NIL empty map. created_at is
-- the ordering key, tie-broken by id. user_id/actor_id reference their subjects by
-- convention (no enforced FK). NO outbox columns (the durable rail is deferred).
CREATE TABLE IF NOT EXISTS security_events (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL DEFAULT '',
    actor_type   TEXT NOT NULL DEFAULT '',
    actor_id     TEXT NOT NULL DEFAULT '',
    event_type   TEXT NOT NULL,
    event_status TEXT NOT NULL,
    details      JSONB NOT NULL DEFAULT '{}',
    ip_address   TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL
);

-- Desc-order paging support (created_at DESC, id DESC) plus the List filter
-- dimensions (plan-cut named secondary indexes).
CREATE INDEX IF NOT EXISTS idx_security_events_created_at_id ON security_events (created_at, id);
CREATE INDEX IF NOT EXISTS idx_security_events_user_id ON security_events (user_id);
CREATE INDEX IF NOT EXISTS idx_security_events_event_type ON security_events (event_type);
CREATE INDEX IF NOT EXISTS idx_security_events_event_status ON security_events (event_status);
