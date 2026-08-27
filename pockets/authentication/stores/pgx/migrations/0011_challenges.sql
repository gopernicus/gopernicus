-- The atomic secret domain (auth-v3 §3.2) that replaces v1's plaintext-at-rest
-- verification codes/tokens. One uniform contract carries every purpose: a short
-- OTP code protected by keyed HMAC-SHA-256, or a high-entropy URL token digested
-- with SHA-256. The plaintext secret is NEVER persisted — only secret_digest — so
-- inspecting stored rows can never reveal a code (proven live in AV3-2.4).
--
-- Redemption is keyed by (user_id, purpose) for codes and (purpose, secret_digest)
-- for tokens, never by the secret's plaintext value: that inversion makes composite
-- single-use redemption structural. protector_key_id names the pepper key a code
-- digest was produced under (NULL for tokens, which use no pepper); during rotation
-- ConsumeCode selects the candidate digest whose key ID matches this row. context is
-- an opaque JSON binding blob (a code flow's context digest, or a token flow's
-- identifier binding) — a pure validator, never a payload channel. attempt_count is
-- the wrong-code counter; version is the row schema version.
-- id defaults DB-side (empty id on Replace → the schema default generates it).
CREATE TABLE IF NOT EXISTS challenges (
    id               TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    secret_digest    TEXT NOT NULL,
    protector_key_id TEXT,
    context          TEXT,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    version          INTEGER NOT NULL DEFAULT 1
);

-- Single active challenge per (user, purpose): Replace atomically deletes the prior
-- row before inserting, and this unique index is the concurrent backstop. The code
-- lookup is composite by (user_id, purpose).
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_user_purpose
    ON challenges (user_id, purpose);

-- Token lookup + single-use claim: ConsumeToken resolves by (purpose, secret_digest)
-- with a DELETE ... RETURNING, so exactly one concurrent redemption wins.
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_purpose_secret_digest
    ON challenges (purpose, secret_digest);
