SET LOCAL lock_timeout = '5s';
-- The immutability trigger pair (SOC 2 S2): UPDATE, DELETE and TRUNCATE on
-- security_events raise 42501 for every role that is not a member of the
-- table's owning role. The owner — the migrator, or a SECURITY DEFINER
-- function it owns — passes, so retention purges need no DISABLE TRIGGER.
-- ENABLE ALWAYS keeps the triggers firing under session_replication_role =
-- replica. The store's boot probe compares the function body below with its
-- canonical copy, so edit neither. Re-running this file is safe. A host that
-- declines the trigger deletes this file from its ledger before applying.
CREATE OR REPLACE FUNCTION security_events_refuse_write() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $security_events_refuse_write$
BEGIN
  IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN
    IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'security_events is append-only' USING ERRCODE = '42501';
END
$security_events_refuse_write$;
CREATE OR REPLACE TRIGGER security_events_immutable BEFORE UPDATE OR DELETE ON security_events FOR EACH ROW EXECUTE FUNCTION security_events_refuse_write();
CREATE OR REPLACE TRIGGER security_events_no_truncate BEFORE TRUNCATE ON security_events FOR EACH STATEMENT EXECUTE FUNCTION security_events_refuse_write();
ALTER TABLE security_events ENABLE ALWAYS TRIGGER security_events_immutable;
ALTER TABLE security_events ENABLE ALWAYS TRIGGER security_events_no_truncate;
