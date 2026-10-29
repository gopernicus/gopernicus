SET LOCAL lock_timeout = '5s';
-- OAuth2 code authentication (SOC 2 S4d part 4): a copy of the approving
-- session's authentication columns, in the sessions encodings — when
-- (authenticated_at), how (authentication_methods, the JSON array of method
-- descriptors) and at what level (assurance_level) the approver last
-- authenticated. NULL on every existing row and on every row a plain
-- CreateCode writes. REQUIRED only when the host sets
-- OAuth2Config.DelegatedAssurance: the store probes these columns through
-- OAuth2Store.ProbeSchema, which the pocket calls only then, so an unwired
-- host boots without them and may apply this file ahead of time. Only
-- OAuth2Store.CreateCodeWithAuthentication writes them, inside the approval
-- transaction, from the approving session row it already holds locked;
-- every other statement names its columns explicitly, so a binary built
-- before this file keeps working on a migrated table. No index: the table
-- holds only short-lived codes and is pruned.
--
-- ADD COLUMN with no default is metadata-only but takes ACCESS EXCLUSIVE on
-- oauth_authorization_codes: lock_timeout aborts the file instead of
-- queueing approvals and redemptions behind it. On lock timeout, re-run the
-- migration (make migrate).
ALTER TABLE oauth_authorization_codes ADD COLUMN IF NOT EXISTS authenticated_at TIMESTAMPTZ;
ALTER TABLE oauth_authorization_codes ADD COLUMN IF NOT EXISTS authentication_methods TEXT;
ALTER TABLE oauth_authorization_codes ADD COLUMN IF NOT EXISTS assurance_level TEXT;
