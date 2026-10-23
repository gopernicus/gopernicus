SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION iam_audit_refuse_write() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $iam_audit_refuse_write$
BEGIN
  IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN
    IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'iam_audit is append-only' USING ERRCODE = '42501';
END
$iam_audit_refuse_write$;
CREATE OR REPLACE FUNCTION iam_operations_refuse_write() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $iam_operations_refuse_write$
BEGIN
  IF pg_catalog.pg_has_role(current_user, (SELECT c.relowner FROM pg_catalog.pg_class c WHERE c.oid = TG_RELID), 'MEMBER') THEN
    IF TG_OP = 'UPDATE' THEN RETURN NEW; END IF;
    RETURN OLD;
  END IF;
  IF TG_OP = 'DELETE' THEN
    IF OLD.outcome <> 'fenced' THEN RETURN OLD; END IF;
  END IF;
  RAISE EXCEPTION 'iam_operations is append-only' USING ERRCODE = '42501';
END
$iam_operations_refuse_write$;
CREATE OR REPLACE TRIGGER iam_audit_immutable BEFORE UPDATE OR DELETE ON iam_audit FOR EACH ROW EXECUTE FUNCTION iam_audit_refuse_write();
CREATE OR REPLACE TRIGGER iam_audit_no_truncate BEFORE TRUNCATE ON iam_audit FOR EACH STATEMENT EXECUTE FUNCTION iam_audit_refuse_write();
CREATE OR REPLACE TRIGGER iam_operations_immutable BEFORE UPDATE OR DELETE ON iam_operations FOR EACH ROW EXECUTE FUNCTION iam_operations_refuse_write();
CREATE OR REPLACE TRIGGER iam_operations_no_truncate BEFORE TRUNCATE ON iam_operations FOR EACH STATEMENT EXECUTE FUNCTION iam_operations_refuse_write();
ALTER TABLE iam_audit ENABLE ALWAYS TRIGGER iam_audit_immutable;
ALTER TABLE iam_audit ENABLE ALWAYS TRIGGER iam_audit_no_truncate;
ALTER TABLE iam_operations ENABLE ALWAYS TRIGGER iam_operations_immutable;
ALTER TABLE iam_operations ENABLE ALWAYS TRIGGER iam_operations_no_truncate;
