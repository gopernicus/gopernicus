-- The pgxdb durable rate limiter's table: the connector's reference DDL
-- (integrations/datastores/pgxdb README, "Reference DDL — host-owned"). The
-- connector creates and migrates nothing; the server probes it with
-- Limiter.StatusCheck before serving.
CREATE TABLE ratelimit_windows (
    key           TEXT        PRIMARY KEY,
    window_start  TIMESTAMPTZ NOT NULL,
    request_count BIGINT      NOT NULL,
    prev_count    BIGINT      NOT NULL,
    last_allowed  BOOLEAN     NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    window_ms     BIGINT      NOT NULL DEFAULT 0
);

CREATE INDEX ratelimit_windows_expires_at_idx ON ratelimit_windows (expires_at);
