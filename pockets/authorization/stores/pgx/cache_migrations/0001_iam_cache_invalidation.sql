-- Optional authorization-cache source; apply authorization through 0007 first.
CREATE TABLE iam_cache_invalidation (
    slot INTEGER PRIMARY KEY CHECK (slot = 1),
    protocol INTEGER NOT NULL CHECK (protocol = 1),
    epoch TEXT NOT NULL CHECK (epoch ~ '^[0-9a-f]{32}$'),
    generation BIGINT NOT NULL CHECK (generation >= 0)
);
INSERT INTO iam_cache_invalidation VALUES (1, 1, replace(gen_random_uuid()::text, '-', ''), 0);

CREATE FUNCTION iam_advance_cache_generation() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER AS $cache$
DECLARE affected BIGINT;
BEGIN
    EXECUTE format('UPDATE %I.iam_cache_invalidation SET generation = generation + 1
        WHERE slot = 1 AND protocol = 1
          AND generation >= 0 AND generation < 9223372036854775807
          AND epoch ~ ''^[0-9a-f]{32}$''', TG_TABLE_SCHEMA);
    GET DIAGNOSTICS affected = ROW_COUNT;
    IF affected <> 1 THEN
        RAISE EXCEPTION 'authorization cache head invalid';
    END IF;
    RETURN NULL;
END;
$cache$;

CREATE TRIGGER iam_relationships_cache_insert AFTER INSERT ON iam_relationships
FOR EACH ROW EXECUTE FUNCTION iam_advance_cache_generation();

CREATE TRIGGER iam_relationships_cache_delete AFTER DELETE ON iam_relationships
FOR EACH ROW EXECUTE FUNCTION iam_advance_cache_generation();

CREATE TRIGGER iam_relationships_cache_update AFTER UPDATE ON iam_relationships
FOR EACH ROW WHEN (OLD.* IS DISTINCT FROM NEW.*) EXECUTE FUNCTION iam_advance_cache_generation();

CREATE TRIGGER iam_relationships_cache_truncate AFTER TRUNCATE ON iam_relationships
FOR EACH STATEMENT EXECUTE FUNCTION iam_advance_cache_generation();

CREATE TRIGGER iam_roles_cache_insert AFTER INSERT ON iam_roles
FOR EACH ROW EXECUTE FUNCTION iam_advance_cache_generation();

CREATE TRIGGER iam_roles_cache_delete AFTER DELETE ON iam_roles
FOR EACH ROW EXECUTE FUNCTION iam_advance_cache_generation();

CREATE TRIGGER iam_roles_cache_update AFTER UPDATE ON iam_roles
FOR EACH ROW WHEN (OLD.* IS DISTINCT FROM NEW.*) EXECUTE FUNCTION iam_advance_cache_generation();

CREATE TRIGGER iam_roles_cache_truncate AFTER TRUNCATE ON iam_roles
FOR EACH STATEMENT EXECUTE FUNCTION iam_advance_cache_generation();
