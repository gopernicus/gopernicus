SET LOCAL lock_timeout = '5s';
-- Session activity (SOC 2 S4d part 2): the stamp of a first-party session's
-- last refresh rotation, NULL for every existing row. REQUIRED only when the
-- host sets SessionsConfig.IdleTimeout: the store probes this column through
-- SessionStore.ProbeSchema, which the pocket calls only then, so an unwired
-- host boots without it and may apply this file ahead of time. Only
-- SessionStore.RotateAt writes it, from the app clock; every other statement
-- names its columns explicitly, so a binary built before this file keeps
-- working on a migrated table.
--
-- ADD COLUMN with no default is metadata-only but takes ACCESS EXCLUSIVE on
-- sessions, the hottest table: lock_timeout aborts the file instead of
-- queueing logins and refreshes behind it. On lock timeout, re-run the
-- migration (make migrate).
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_rotated_at TIMESTAMPTZ;
