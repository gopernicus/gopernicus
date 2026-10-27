-- Session activity (SOC 2 S4d part 2): the stamp of a first-party session's
-- last refresh rotation, NULL for every existing row. REQUIRED only when the
-- host sets SessionsConfig.IdleTimeout: the store probes this column through
-- SessionStore.ProbeSchema, which the pocket calls only then, so an unwired
-- host boots without it and may apply this file ahead of time. Only
-- SessionStore.RotateAt writes it, from the app clock, as a fixed-width TEXT
-- timestamp in created_at's encoding; every other statement names its columns
-- explicitly, so a binary built before this file keeps working on a migrated
-- table. The ALTER briefly holds the database write lock.
ALTER TABLE sessions ADD COLUMN last_rotated_at TEXT;
