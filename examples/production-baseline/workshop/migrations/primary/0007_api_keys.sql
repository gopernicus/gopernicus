-- Hashed machine credentials (design §4.1). key_hash is the SHA-256 of the
-- plaintext key (shown once at mint); key_prefix is stored plain for display.
-- GetByHash selects by key_hash ALONE and returns ANY present row — revoked and
-- expired included; revocation/expiry are SERVICE-layer branches, never store
-- filters. expires_at/revoked_at/last_used_at are nullable (NULL → never expires
-- / not revoked / never used). created_at is TIMESTAMPTZ. service_account_id
-- references service_accounts.id by convention (no enforced FK).
-- id defaults DB-side (a cryptids.Database host sends Create an empty id; the
-- store omits the column and RETURNING reads the generated key back).
-- id carries COLLATE "C": it is the keyset tiebreak of the created_at DESC, id
-- DESC ListByServiceAccount, and the storetest collision suite pins that tiebreak
-- to the reference store's byte-wise (Go string) order. The per-column collation
-- makes that byte-order contract travel with the schema (AAH-5 / plan D5).
CREATE TABLE IF NOT EXISTS api_keys (
    id                 TEXT COLLATE "C" PRIMARY KEY DEFAULT gen_random_uuid()::text,
    service_account_id TEXT NOT NULL,
    name               TEXT NOT NULL DEFAULT '',
    key_prefix         TEXT NOT NULL DEFAULT '',
    key_hash           TEXT NOT NULL,
    expires_at         TIMESTAMPTZ,
    revoked_at         TIMESTAMPTZ,
    last_used_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL
);

-- key_hash uniqueness + the GetByHash lookup index (a colliding mint →
-- errs.ErrAlreadyExists via MapError).
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys (key_hash);

-- ListByServiceAccount support (plan-cut named secondary index).
CREATE INDEX IF NOT EXISTS idx_api_keys_service_account_id ON api_keys (service_account_id);
