-- Sessions: the id-keyed revocable anchor carrying refresh-rotation state
-- (auth JWT + refresh milestone). The access credential is a self-validating
-- JWT and is never persisted; this row anchors revocation and refresh.
--
-- id is app-minted (the access JWT is signed with session_id BEFORE the insert,
-- so a DB default / RETURNING key cannot work): sessions stay outside the
-- id-default pattern. refresh_token_hash holds the SHA-256 of the live refresh
-- token; previous_refresh_token_hash is the single rotated-away (grace) slot and
-- is NULLABLE — never TEXT NOT NULL DEFAULT '', because every fresh row would then
-- share '' and GetByRefreshHash could match an arbitrary fresh row (cross-session
-- bleed). previous_used flags a consumed grace slot (native BOOLEAN). Timestamps
-- are TIMESTAMPTZ; Get filters on expires_at at the read clock (expired-at-read).
-- user_id references users.id by convention (no enforced FK).
--
-- authenticated_at/authentication_methods/assurance_level (auth-v3 §5.0) record
-- how and when the holder last performed a primary authentication, backing the
-- recent-primary-login shortcut that can satisfy a step-up grant without an extra
-- prompt. authenticated_at is NULLABLE (NULL ↔ the zero-value "not recorded"
-- sentinel); authentication_methods is a JSON array of honest method descriptors;
-- assurance_level is the recorded AssuranceLevel string ('' = unknown). The
-- session store maps these in auth-v3 phase 2.
CREATE TABLE IF NOT EXISTS sessions (
    id                          TEXT PRIMARY KEY,
    user_id                     TEXT NOT NULL,
    refresh_token_hash          TEXT NOT NULL,
    previous_refresh_token_hash TEXT,
    previous_used               BOOLEAN NOT NULL DEFAULT FALSE,
    rotation_count              INTEGER NOT NULL DEFAULT 0,
    authenticated_at            TIMESTAMPTZ,
    authentication_methods      TEXT NOT NULL DEFAULT '',
    assurance_level             TEXT NOT NULL DEFAULT '',
    created_at                  TIMESTAMPTZ NOT NULL,
    expires_at                  TIMESTAMPTZ NOT NULL
);

-- The live refresh credential is unique: a hash collision surfaces via MapError
-- as sdk.ErrAlreadyExists.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_refresh_token_hash ON sessions (refresh_token_hash);

-- Grace-slot lookup, PARTIAL over rotated rows only (fresh rows have NULL previous),
-- so GetByRefreshHash("") never lands on a fresh row (0011 partial-index precedent).
CREATE INDEX IF NOT EXISTS idx_sessions_previous_refresh_token_hash
    ON sessions (previous_refresh_token_hash)
    WHERE previous_refresh_token_hash IS NOT NULL;

-- DeleteByUser support (the logout-everywhere / password-change revoke primitive).
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions (user_id);
