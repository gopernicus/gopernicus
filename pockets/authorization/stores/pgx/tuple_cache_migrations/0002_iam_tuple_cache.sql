-- Optional TupleCache capture. Apply after the primary authorization schema
-- in the same database/schema, before constructing WithTupleCache stores.
LOCK TABLE iam_tuples IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE iam_tuple_cache (
    slot INTEGER PRIMARY KEY CHECK (slot = 1),
    protocol INTEGER NOT NULL CHECK (protocol = 2),
    identity TEXT NOT NULL CHECK (identity ~ '^[0-9a-f]{32}$'),
    receipt TEXT NOT NULL DEFAULT ''
);
INSERT INTO iam_tuple_cache (slot, protocol, identity)
VALUES (1, 2, replace(gen_random_uuid()::text, '-', ''));

-- IDs order changes to the same tuple, but are NOT a commit watermark: an older
-- ID in another transaction may become visible after a newer ID is acknowledged.
CREATE TABLE iam_tuple_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    before_tuple JSONB,
    after_tuple JSONB,
    reset BOOLEAN NOT NULL DEFAULT FALSE,
    CHECK ((reset AND before_tuple IS NULL AND after_tuple IS NULL)
        OR (NOT reset AND (before_tuple IS NOT NULL OR after_tuple IS NOT NULL)))
);

CREATE FUNCTION iam_capture_tuple_change() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $tuple$
DECLARE before_value JSONB; after_value JSONB; valid_identity BIGINT;
BEGIN
    EXECUTE format('SELECT count(*) FROM %I.iam_tuple_cache WHERE slot=1 AND protocol=2 AND identity ~ ''^[0-9a-f]{32}$''', TG_TABLE_SCHEMA)
        INTO valid_identity;
    IF valid_identity <> 1 THEN
        RAISE EXCEPTION 'authorization tuple cache identity invalid';
    END IF;
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        before_value := to_jsonb(OLD);
    END IF;
    IF TG_OP = 'INSERT' OR TG_OP = 'UPDATE' THEN
        after_value := to_jsonb(NEW);
    END IF;
    EXECUTE format('INSERT INTO %I.iam_tuple_outbox (before_tuple, after_tuple, reset) VALUES ($1, $2, $3)', TG_TABLE_SCHEMA)
        USING before_value, after_value, TG_OP = 'TRUNCATE';
    RETURN NULL;
END;
$tuple$;

CREATE TRIGGER iam_tuples_tuple_insert AFTER INSERT ON iam_tuples
FOR EACH ROW EXECUTE FUNCTION iam_capture_tuple_change();
CREATE TRIGGER iam_tuples_tuple_delete AFTER DELETE ON iam_tuples
FOR EACH ROW EXECUTE FUNCTION iam_capture_tuple_change();
CREATE TRIGGER iam_tuples_tuple_update AFTER UPDATE ON iam_tuples
FOR EACH ROW WHEN (OLD.* IS DISTINCT FROM NEW.*) EXECUTE FUNCTION iam_capture_tuple_change();
CREATE TRIGGER iam_tuples_tuple_truncate AFTER TRUNCATE ON iam_tuples
FOR EACH STATEMENT EXECUTE FUNCTION iam_capture_tuple_change();
