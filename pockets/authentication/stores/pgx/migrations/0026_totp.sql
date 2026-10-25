SET LOCAL lock_timeout = '5s';
-- TOTP fallback and recovery codes (SOC 2 S4c): the second-factor aggregate,
-- one authenticator-app credential per user and its recovery codes. REQUIRED
-- only when the host wires TOTP: the store probes these tables through
-- totp.SchemaProber, which the pocket calls only under WithTOTP, so an unwired
-- host boots without them and may apply this file ahead of time.
--
-- secret is the sealed envelope {u: user_id, s: secret} — the plaintext secret
-- never rests, pending or confirmed. session_id, tier, auth_revision and
-- expires_at describe a pending enrollment and are cleared by the confirm;
-- confirmed_at is NULL while pending. last_used_step is the newest accepted time
-- step (no code is accepted twice); failed_attempts counts consecutive refusals
-- and locked_until is the lockout they set. user_id references users.id by
-- convention (no enforced FK).
CREATE TABLE IF NOT EXISTS totp_credentials (
    user_id         TEXT COLLATE "C" PRIMARY KEY,
    secret          TEXT NOT NULL,
    session_id      TEXT NOT NULL DEFAULT '',
    tier            TEXT NOT NULL DEFAULT '',
    auth_revision   BIGINT NOT NULL DEFAULT 0,
    expires_at      TIMESTAMPTZ,
    confirmed_at    TIMESTAMPTZ,
    last_used_step  BIGINT NOT NULL DEFAULT 0,
    failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ
);

-- One row per unspent recovery code: its digest under the challenge protector's
-- key key_id, never the code. A consumed code's row is deleted. generated_at is
-- the generation the set was minted at. id carries COLLATE "C" like every other
-- contractual id; user_id references users.id by convention (no enforced FK).
-- Both tables are new and empty: they touch no existing one, so their indexes
-- build in place.
CREATE TABLE IF NOT EXISTS recovery_codes (
    id           TEXT COLLATE "C" PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id      TEXT NOT NULL,
    key_id       TEXT NOT NULL,
    code_digest  TEXT NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, code_digest)
);

-- The boot WARN's distinct key ids (codes sealed under a retired pepper key).
CREATE INDEX IF NOT EXISTS idx_recovery_codes_key_id ON recovery_codes (key_id);
