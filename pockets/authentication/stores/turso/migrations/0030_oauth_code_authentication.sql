-- OAuth2 code authentication (SOC 2 S4d part 4): a copy of the approving
-- session's authentication columns, in the sessions encodings — when
-- (authenticated_at, a fixed-width TEXT timestamp in created_at's encoding),
-- how (authentication_methods, the JSON array of method descriptors) and at
-- what level (assurance_level) the approver last authenticated. NULL on every
-- existing row and on every row a plain CreateCode writes. REQUIRED only when
-- the host sets OAuth2Config.DelegatedAssurance: the store probes these
-- columns through OAuth2Store.ProbeSchema, which the pocket calls only then,
-- so an unwired host boots without them and may apply this file ahead of
-- time. Only OAuth2Store.CreateCodeWithAuthentication writes them, inside the
-- approval transaction, from the approving session row it reads under the
-- write lock that transaction already holds; every other statement names its
-- columns explicitly, so a binary built before this file keeps working on a
-- migrated table. No index: the table holds only short-lived codes and is
-- pruned. The file applies in one transaction; the ALTERs briefly hold the
-- database write lock.
ALTER TABLE oauth_authorization_codes ADD COLUMN authenticated_at TEXT;
ALTER TABLE oauth_authorization_codes ADD COLUMN authentication_methods TEXT;
ALTER TABLE oauth_authorization_codes ADD COLUMN assurance_level TEXT;
