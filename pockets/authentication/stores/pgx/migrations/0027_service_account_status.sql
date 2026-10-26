SET LOCAL lock_timeout = '5s';
-- Service-account status (SOC 2 S4d): the reversible disable of a machine
-- identity, one row per account that has ever been disabled. An account with
-- no row is active. REQUIRED only when the host sets
-- AdministrationConfig.ServiceAccountStatus: the store probes this table
-- through ServiceAccountStatusStore.ProbeSchema, which the pocket calls only
-- then, so an unwired host boots without it and may apply this file ahead of
-- time.
--
-- changed_by is the acting principal ("<type>:<id>"), kept like a security
-- event's actor. service_account_id references service_accounts.id by
-- convention (no enforced FK) and carries COLLATE "C" like every other
-- contractual id. A new, empty table: it touches no existing one.
CREATE TABLE IF NOT EXISTS service_account_status (
    service_account_id TEXT COLLATE "C" PRIMARY KEY,
    status             TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    changed_at         TIMESTAMPTZ NOT NULL,
    changed_by         TEXT NOT NULL DEFAULT ''
);
