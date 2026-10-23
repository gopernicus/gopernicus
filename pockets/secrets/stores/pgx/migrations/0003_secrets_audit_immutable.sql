SET LOCAL lock_timeout = '5s';
-- secrets_audit is append-only: UPDATE, DELETE and TRUNCATE raise
-- insufficient_privilege (42501) for every role that is not a member of the
-- table's owner, so the migrator can still maintain it. ENABLE ALWAYS keeps
-- both triggers firing whatever the session's replication role. The store's
-- posture probe compares the function body byte for byte; do not edit it. A
-- host that declines the triggers deletes this file from its ledger before
-- applying it.
CREATE OR REPLACE FUNCTION secrets_audit_refuse_write() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $secrets_audit_refuse_write$
BEGIN
  IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN
    IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'secrets_audit is append-only' USING ERRCODE = '42501';
END
$secrets_audit_refuse_write$;
CREATE OR REPLACE TRIGGER secrets_audit_immutable BEFORE UPDATE OR DELETE ON secrets_audit FOR EACH ROW EXECUTE FUNCTION secrets_audit_refuse_write();
CREATE OR REPLACE TRIGGER secrets_audit_no_truncate BEFORE TRUNCATE ON secrets_audit FOR EACH STATEMENT EXECUTE FUNCTION secrets_audit_refuse_write();
ALTER TABLE secrets_audit ENABLE ALWAYS TRIGGER secrets_audit_immutable;
ALTER TABLE secrets_audit ENABLE ALWAYS TRIGGER secrets_audit_no_truncate;
