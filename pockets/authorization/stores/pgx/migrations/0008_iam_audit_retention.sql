SET LOCAL lock_timeout = '5s';
-- Audit retention. The runtime role holds no DELETE on iam_audit (0006), so the
-- purge runs through a SECURITY DEFINER function owned by the table's owner:
-- the 0006 trigger exempts the owner, and the runtime role needs only EXECUTE,
-- granted by the host:
--   GRANT EXECUTE ON FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO <runtime role>;
-- The function refuses a cutoff younger than iam_audit_retention_floor()
-- (365 days, a framework invariant: no procedure lowers it) and appends one
-- iam_audit_purges row per call, a zero count included, in the same
-- transaction. iam_audit itself takes no summary row: every iam_audit row is a
-- tuple change. Every function and operator in the bodies is
-- pg_catalog-qualified. Re-running this file is safe for the role that owns
-- the functions.
--
-- The assertions run first: the function is pinned to the schema that holds
-- iam_audit, which must be the schema this file runs in, and its owner (the
-- migrator) must be a member of the table's owner or the definer cannot delete
-- past the trigger.
DO $iam_audit_retention_assert$
DECLARE
  table_schema pg_catalog.name;
  table_owner pg_catalog.oid;
BEGIN
  SELECT n.nspname, c.relowner INTO table_schema, table_owner
    FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
    WHERE c.oid OPERATOR(pg_catalog.=) 'iam_audit'::pg_catalog.regclass;
  IF pg_catalog.current_schema() IS DISTINCT FROM table_schema THEN
    RAISE EXCEPTION 'iam_audit lives in schema %, migration runs in %', table_schema, pg_catalog.current_schema();
  END IF;
  IF NOT pg_catalog.pg_has_role(CURRENT_USER, table_owner, 'MEMBER') THEN
    RAISE EXCEPTION 'the migrator must be a member of the owner of iam_audit; fix: GRANT % TO %',
      pg_catalog.quote_ident(pg_catalog.pg_get_userbyid(table_owner)), pg_catalog.quote_ident(CURRENT_USER::pg_catalog.text);
  END IF;
END
$iam_audit_retention_assert$;

-- One row per purge call: the evidence a summary row in iam_audit would fake.
-- Append-only by the 0006 trigger function; the runtime role is granted nothing.
CREATE TABLE IF NOT EXISTS iam_audit_purges (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    cutoff TIMESTAMPTZ NOT NULL,
    purged BIGINT NOT NULL CONSTRAINT ck_iam_audit_purges_purged CHECK (purged >= 0),
    floor_days INTEGER NOT NULL,
    max_rows INTEGER NOT NULL,
    session_role TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
REVOKE ALL ON TABLE iam_audit_purges FROM PUBLIC;
CREATE OR REPLACE TRIGGER iam_audit_purges_immutable BEFORE UPDATE OR DELETE ON iam_audit_purges FOR EACH ROW EXECUTE FUNCTION iam_audit_refuse_write();
CREATE OR REPLACE TRIGGER iam_audit_purges_no_truncate BEFORE TRUNCATE ON iam_audit_purges FOR EACH STATEMENT EXECUTE FUNCTION iam_audit_refuse_write();
ALTER TABLE iam_audit_purges ENABLE ALWAYS TRIGGER iam_audit_purges_immutable;
ALTER TABLE iam_audit_purges ENABLE ALWAYS TRIGGER iam_audit_purges_no_truncate;

CREATE OR REPLACE FUNCTION iam_audit_retention_floor() RETURNS pg_catalog.interval LANGUAGE sql STABLE AS $iam_audit_retention_floor$
SELECT '365 days'::pg_catalog.interval
$iam_audit_retention_floor$;

CREATE OR REPLACE FUNCTION iam_audit_purge_before(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4)
RETURNS TABLE (cutoff pg_catalog.timestamptz, purged pg_catalog.int8)
LANGUAGE plpgsql SECURITY DEFINER AS $iam_audit_purge_before$
DECLARE
  n pg_catalog.int8;
  floor_interval pg_catalog.interval;
BEGIN
  IF p_before IS NULL THEN
    RAISE EXCEPTION 'iam_audit_purge_before: the cutoff is NULL' USING ERRCODE = '22023';
  END IF;
  floor_interval := iam_audit_retention_floor();
  IF p_before OPERATOR(pg_catalog.>) (pg_catalog.now() OPERATOR(pg_catalog.-) floor_interval) THEN
    RAISE EXCEPTION 'iam_audit_purge_before: cutoff % is below the retention floor of %', p_before, floor_interval USING ERRCODE = '22023';
  END IF;
  p_max_rows := LEAST(GREATEST(p_max_rows, 1), 10000);
  DELETE FROM iam_audit WHERE id IN (
    SELECT id FROM iam_audit WHERE occurred_at OPERATOR(pg_catalog.<=) p_before
    ORDER BY occurred_at, id LIMIT p_max_rows FOR UPDATE SKIP LOCKED);
  GET DIAGNOSTICS n = ROW_COUNT;
  INSERT INTO iam_audit_purges (cutoff, purged, floor_days, max_rows, session_role, recorded_at)
  VALUES (p_before, n, pg_catalog.date_part('day'::pg_catalog.text, floor_interval)::pg_catalog.int4, p_max_rows, SESSION_USER::pg_catalog.text, pg_catalog.now());
  cutoff := p_before;
  purged := n;
  RETURN NEXT;
END
$iam_audit_purge_before$;

-- Pin the definer's search_path to the table's schema. The schema is read
-- from the table, never current_schema(), and pg_temp is listed LAST so a
-- caller's temporary table cannot stand in for iam_audit. After a restore into
-- a renamed schema, re-run this block.
DO $iam_audit_retention_pin$
BEGIN
  EXECUTE pg_catalog.format('ALTER FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) SET search_path = pg_catalog, %I, pg_temp',
    (SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
      WHERE c.oid OPERATOR(pg_catalog.=) 'iam_audit'::pg_catalog.regclass));
END
$iam_audit_retention_pin$;

REVOKE EXECUTE ON FUNCTION iam_audit_purge_before(pg_catalog.timestamptz, pg_catalog.int4) FROM PUBLIC;
