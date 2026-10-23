SET LOCAL lock_timeout = '5s';
-- Rail retention (SOC 2 S3). The runtime role holds no DELETE on
-- security_events (0022), so the purge runs through a SECURITY DEFINER
-- function owned by the table's owner: the 0022 trigger exempts the owner, and
-- the runtime role needs only EXECUTE, granted by the host:
--   GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO <runtime role>;
-- The function refuses a cutoff younger than security_events_retention_floor()
-- (365 days, a framework invariant: no procedure lowers it) and appends one
-- security_events_purged row per call, a zero count included. Every function
-- and operator in the bodies is pg_catalog-qualified. Re-running this file is
-- safe for the role that owns the functions.
--
-- The assertions run first: the function is pinned to the schema that holds
-- security_events, which must be the schema this file runs in, and its owner
-- (the migrator) must be a member of the table's owner or the definer cannot
-- delete past the trigger.
DO $security_events_retention_assert$
DECLARE
  table_schema pg_catalog.name;
  table_owner pg_catalog.oid;
BEGIN
  SELECT n.nspname, c.relowner INTO table_schema, table_owner
    FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
    WHERE c.oid OPERATOR(pg_catalog.=) 'security_events'::pg_catalog.regclass;
  IF pg_catalog.current_schema() IS DISTINCT FROM table_schema THEN
    RAISE EXCEPTION 'security_events lives in schema %, migration runs in %', table_schema, pg_catalog.current_schema();
  END IF;
  IF NOT pg_catalog.pg_has_role(CURRENT_USER, table_owner, 'MEMBER') THEN
    RAISE EXCEPTION 'the migrator must be a member of the owner of security_events; fix: GRANT % TO %',
      pg_catalog.quote_ident(pg_catalog.pg_get_userbyid(table_owner)), pg_catalog.quote_ident(CURRENT_USER::pg_catalog.text);
  END IF;
END
$security_events_retention_assert$;

CREATE OR REPLACE FUNCTION security_events_retention_floor() RETURNS pg_catalog.interval LANGUAGE sql STABLE AS $security_events_retention_floor$
SELECT '365 days'::pg_catalog.interval
$security_events_retention_floor$;

CREATE OR REPLACE FUNCTION security_events_purge_before(p_before pg_catalog.timestamptz, p_max_rows pg_catalog.int4)
RETURNS TABLE (cutoff pg_catalog.timestamptz, purged pg_catalog.int8)
LANGUAGE plpgsql SECURITY DEFINER AS $security_events_purge_before$
DECLARE
  n pg_catalog.int8;
  floor_interval pg_catalog.interval;
BEGIN
  IF p_before IS NULL THEN
    RAISE EXCEPTION 'security_events_purge_before: the cutoff is NULL' USING ERRCODE = '22023';
  END IF;
  floor_interval := security_events_retention_floor();
  IF p_before OPERATOR(pg_catalog.>) (pg_catalog.now() OPERATOR(pg_catalog.-) floor_interval) THEN
    RAISE EXCEPTION 'security_events_purge_before: cutoff % is below the retention floor of %', p_before, floor_interval USING ERRCODE = '22023';
  END IF;
  p_max_rows := LEAST(GREATEST(p_max_rows, 1), 10000);
  DELETE FROM security_events WHERE id IN (
    SELECT id FROM security_events WHERE created_at OPERATOR(pg_catalog.<=) p_before
    ORDER BY created_at, id LIMIT p_max_rows FOR UPDATE SKIP LOCKED);
  GET DIAGNOSTICS n = ROW_COUNT;
  INSERT INTO security_events (user_id, actor_type, actor_id, event_type, event_status, details, ip_address, user_agent, request_id, trace_id, created_at)
  VALUES (''::pg_catalog.text, 'system'::pg_catalog.text, 'maintenance'::pg_catalog.text, 'security_events_purged'::pg_catalog.text, 'success'::pg_catalog.text,
    pg_catalog.jsonb_build_object(
      'cutoff'::pg_catalog.text, pg_catalog.to_char(pg_catalog.timezone('UTC'::pg_catalog.text, p_before), 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'::pg_catalog.text),
      'count'::pg_catalog.text, n,
      'floor_days'::pg_catalog.text, pg_catalog.date_part('day'::pg_catalog.text, floor_interval)::pg_catalog.int4,
      'session_user'::pg_catalog.text, SESSION_USER::pg_catalog.text,
      'max_rows'::pg_catalog.text, p_max_rows),
    ''::pg_catalog.text, ''::pg_catalog.text, ''::pg_catalog.text, ''::pg_catalog.text, pg_catalog.now());
  cutoff := p_before;
  purged := n;
  RETURN NEXT;
END
$security_events_purge_before$;

-- Pin the definer's search_path to the table's schema. The schema is read
-- from the table, never current_schema(), and pg_temp is listed LAST so a
-- caller's temporary table cannot stand in for security_events. After a
-- restore into a renamed schema, re-run this block.
DO $security_events_retention_pin$
BEGIN
  EXECUTE pg_catalog.format('ALTER FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) SET search_path = pg_catalog, %I, pg_temp',
    (SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
      WHERE c.oid OPERATOR(pg_catalog.=) 'security_events'::pg_catalog.regclass));
END
$security_events_retention_pin$;

REVOKE EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) FROM PUBLIC;
