-- The durable, enumeration-safe outbound outbox (auth-v3 §6.1.1). Every auth
-- message that must not resolve an account or call a provider on the request path —
-- passwordless start, contact-change codes, sensitive-op codes — is enqueued here
-- as an OPAQUE job and delivered later by the phase-4 worker. Known and unknown
-- identifiers share one bounded request path so provider latency can never become
-- an enumeration signal.
--
-- payload is the WHOLE encrypted envelope (destination, rendered secret/message,
-- and account-resolution input) sealed through the required DeliveryEncrypter —
-- there is deliberately NO plaintext destination/message/identifier column, so
-- inspecting rows can never reveal them (proven live in AV3-2.4). It is BLOB:
-- AES-GCM ciphertext is binary, not text. idempotency_key is an opaque, PII-free
-- keyed digest that both deduplicates a double-submitted enqueue and groups the
-- pending job a resend replaces. state is the lifecycle ('pending' is the only
-- non-terminal state); an in-flight claim is the lease (lease_id/leased_until), not
-- a separate state, so a crashed worker's lease expires and the still-pending job is
-- reclaimable (at-least-once). attempt_count increments on each Claim; available_at
-- is the due time a retry pushes forward with backoff; last_error is a redacted
-- reason (never a raw secret); terminal_at is set at a terminal state (the purge
-- cursor). Timestamps are fixed-width TEXT.
-- id defaults DB-side under the greenfield convention.
CREATE TABLE IF NOT EXISTS delivery_jobs (
    id              TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    kind            TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload         BLOB NOT NULL DEFAULT x'',
    state           TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    available_at    TEXT NOT NULL,
    lease_id        TEXT NOT NULL DEFAULT '',
    leased_until    TEXT,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    terminal_at     TEXT
);

-- Enqueue idempotency + resend supersession: at most one non-terminal (pending) job
-- per idempotency_key. PARTIAL over pending rows only, so the key frees once a job
-- reaches a terminal state.
CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_jobs_idempotency
    ON delivery_jobs (idempotency_key)
    WHERE state = 'pending';

-- Claim leases the oldest due job by (available_at, created_at, id) among pending
-- rows. PARTIAL over pending rows keeps the due scan tight.
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_due
    ON delivery_jobs (available_at, created_at, id)
    WHERE state = 'pending';

-- PurgeTerminal cursor over terminal rows by terminal_at.
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_terminal
    ON delivery_jobs (terminal_at)
    WHERE state <> 'pending';
