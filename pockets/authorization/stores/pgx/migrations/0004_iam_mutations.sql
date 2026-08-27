-- The mutation receipts (migration source "authorization"). One row per applied,
-- replayable command, keyed by its cryptographically strong MutationID. It is the
-- durable idempotency ledger the atomic mutation repositories consult: a repeat
-- command with the same MutationID and matching payload_digest returns the stored
-- receipt (Replayed=true, computed at read time — never stored), and a repeat with
-- a DIFFERENT payload_digest is the stable payload-mismatch command error, never a
-- silent overwrite (mutation.Receipt, AZ3-0.4). PostgreSQL flavor of the turso
-- 0004 — IDENTICAL filename so the version set matches across dialects.
--
-- Persistence contract: ONLY committed applied / no_change / not_found outcomes
-- mint a receipt (mutation.Outcome.Persisted); semantic_conflict and
-- invariant_blocked commit nothing, so they persist no receipt and their
-- MutationID stays unconsumed — ck_iam_mutations_outcome enforces that at the
-- storage layer. Replay is NOT an outcome and is not a column.
--
-- Retention (default #2): PERMANENT retention is the default posture. expires_at
-- is a NULLABLE column present for a future, explicitly weaker finite-window
-- posture with its own ratified minimum and cleanup runbook; NULL means the
-- receipt never expires. The default write path leaves it NULL — idempotency is
-- then guaranteed for all time and MutationID reuse stays forbidden.
--
-- Bounded by construction: the receipt stores the payload DIGEST (a fixed SHA-256
-- hex under payload_encoding = MutationEncodingVersion), never the payload itself,
-- and no display data, secrets, or request headers — only what identifies the
-- write and its result.
--
-- Constraints: ck_iam_mutations_kind (valid scope kind), ck_iam_mutations_outcome
-- (persisted-outcome set only), ck_iam_mutations_revision (nonnegative resulting
-- revision), and ck_iam_mutations_nonempty (non-empty structural columns).
-- schema_digest records the compiled-schema digest that GOVERNED the original
-- application, so an exact replay returns the original result even if the current
-- schema no longer accepts that relation (AZ3-0.2/AZ3-0.4). created_at is
-- TIMESTAMPTZ (postgres orders it natively); a replay preserves it.
CREATE TABLE IF NOT EXISTS iam_mutations (
    mutation_id      TEXT        NOT NULL PRIMARY KEY,
    scope_kind       TEXT        NOT NULL,
    scope_type       TEXT        NOT NULL,
    scope_id         TEXT        NOT NULL,
    operation        TEXT        NOT NULL,
    payload_encoding TEXT        NOT NULL,
    payload_digest   TEXT        NOT NULL,
    outcome          TEXT        NOT NULL,
    revision         BIGINT      NOT NULL,
    schema_digest    TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ,
    CONSTRAINT ck_iam_mutations_kind     CHECK (scope_kind IN ('resource', 'subject')),
    CONSTRAINT ck_iam_mutations_outcome  CHECK (outcome IN ('applied', 'no_change', 'not_found')),
    CONSTRAINT ck_iam_mutations_revision CHECK (revision >= 0),
    CONSTRAINT ck_iam_mutations_nonempty CHECK (
        scope_type <> '' AND scope_id <> '' AND operation <> ''
        AND payload_encoding <> '' AND payload_digest <> '' AND schema_digest <> ''
    )
);
