-- OAuth2 refresh-reuse grace: when a delegated refresh token was rotated
-- (rotated_at) and the ciphertext of the refresh token that replaced it
-- (successor_ciphertext), so a second refresh of the same token inside the
-- host's short window gets the rotation already issued instead of revoking
-- the connection. NULL on every existing row and on every row a plain
-- RotateRefresh writes. REQUIRED only when the host sets
-- oauth2.Config.RefreshReuseGrace: the store probes these columns through
-- OAuth2Store.ProbeRefreshGraceSchema, which the pocket calls only then, so
-- an unwired host boots without them and may apply this file ahead of time.
-- The ciphertext is sealed by the host's encrypter; the store never sees the
-- raw token. A rotation clears the session's older ciphertexts, and Prune
-- clears any older than the 30s maximum window. No index: rows are read by
-- their primary key, and Prune's sweep runs on the retention schedule.
ALTER TABLE oauth_refresh_history ADD COLUMN rotated_at TEXT;
ALTER TABLE oauth_refresh_history ADD COLUMN successor_ciphertext TEXT;
