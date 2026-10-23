-- The immutability trigger pair (SOC 2 S2): UPDATE and DELETE on
-- security_events abort for every connection — SQLite has no roles, so no
-- owner is exempt. INSERT OR REPLACE deletes rows WITHOUT firing DELETE
-- triggers, so no path may use it on this table. The store's boot probe
-- compares both statements with its canonical copies, so edit neither.
-- Re-running this file is safe. A host that declines the triggers deletes this
-- file from its ledger before applying.
DROP TRIGGER IF EXISTS security_events_immutable_update;
CREATE TRIGGER security_events_immutable_update BEFORE UPDATE ON security_events
BEGIN
  SELECT RAISE(ABORT, 'security_events is append-only');
END;
DROP TRIGGER IF EXISTS security_events_immutable_delete;
CREATE TRIGGER security_events_immutable_delete BEFORE DELETE ON security_events
BEGIN
  SELECT RAISE(ABORT, 'security_events is append-only');
END;
