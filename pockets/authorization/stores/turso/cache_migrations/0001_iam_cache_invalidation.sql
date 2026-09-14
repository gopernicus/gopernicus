-- Optional authorization-cache source; apply authorization through 0007 first.
CREATE TABLE iam_cache_invalidation (
    slot INTEGER PRIMARY KEY CHECK (slot = 1),
    protocol INTEGER NOT NULL CHECK (protocol = 1),
    epoch TEXT NOT NULL CHECK (typeof(epoch) = 'text' AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*'),
    generation INTEGER NOT NULL CHECK (typeof(generation) = 'integer' AND generation >= 0)
);
INSERT INTO iam_cache_invalidation VALUES (1, 1, lower(hex(randomblob(16))), 0);

CREATE TRIGGER iam_relationships_cache_insert AFTER INSERT ON iam_relationships
BEGIN
    UPDATE iam_cache_invalidation SET generation = generation + 1
    WHERE slot = 1 AND protocol = 1
      AND typeof(generation) = 'integer'
      AND generation >= 0 AND generation < 9223372036854775807
      AND typeof(epoch) = 'text' AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*';
    SELECT CASE WHEN changes() <> 1 THEN RAISE(ABORT, 'authorization cache head invalid') END;
END;

CREATE TRIGGER iam_relationships_cache_delete AFTER DELETE ON iam_relationships
BEGIN
    UPDATE iam_cache_invalidation SET generation = generation + 1
    WHERE slot = 1 AND protocol = 1
      AND typeof(generation) = 'integer'
      AND generation >= 0 AND generation < 9223372036854775807
      AND typeof(epoch) = 'text' AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*';
    SELECT CASE WHEN changes() <> 1 THEN RAISE(ABORT, 'authorization cache head invalid') END;
END;

CREATE TRIGGER iam_relationships_cache_update AFTER UPDATE ON iam_relationships
WHEN OLD.resource_type IS NOT NEW.resource_type OR OLD.resource_id IS NOT NEW.resource_id OR OLD.relation IS NOT NEW.relation OR OLD.subject_type IS NOT NEW.subject_type OR OLD.subject_id IS NOT NEW.subject_id OR OLD.subject_relation IS NOT NEW.subject_relation
BEGIN
    UPDATE iam_cache_invalidation SET generation = generation + 1
    WHERE slot = 1 AND protocol = 1
      AND typeof(generation) = 'integer'
      AND generation >= 0 AND generation < 9223372036854775807
      AND typeof(epoch) = 'text' AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*';
    SELECT CASE WHEN changes() <> 1 THEN RAISE(ABORT, 'authorization cache head invalid') END;
END;

CREATE TRIGGER iam_roles_cache_insert AFTER INSERT ON iam_roles
BEGIN
    UPDATE iam_cache_invalidation SET generation = generation + 1
    WHERE slot = 1 AND protocol = 1
      AND typeof(generation) = 'integer'
      AND generation >= 0 AND generation < 9223372036854775807
      AND typeof(epoch) = 'text' AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*';
    SELECT CASE WHEN changes() <> 1 THEN RAISE(ABORT, 'authorization cache head invalid') END;
END;

CREATE TRIGGER iam_roles_cache_delete AFTER DELETE ON iam_roles
BEGIN
    UPDATE iam_cache_invalidation SET generation = generation + 1
    WHERE slot = 1 AND protocol = 1
      AND typeof(generation) = 'integer'
      AND generation >= 0 AND generation < 9223372036854775807
      AND typeof(epoch) = 'text' AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*';
    SELECT CASE WHEN changes() <> 1 THEN RAISE(ABORT, 'authorization cache head invalid') END;
END;

CREATE TRIGGER iam_roles_cache_update AFTER UPDATE ON iam_roles
WHEN OLD.subject_type IS NOT NEW.subject_type OR OLD.subject_id IS NOT NEW.subject_id OR OLD.role IS NOT NEW.role OR OLD.resource_type IS NOT NEW.resource_type OR OLD.resource_id IS NOT NEW.resource_id
BEGIN
    UPDATE iam_cache_invalidation SET generation = generation + 1
    WHERE slot = 1 AND protocol = 1
      AND typeof(generation) = 'integer'
      AND generation >= 0 AND generation < 9223372036854775807
      AND typeof(epoch) = 'text' AND length(epoch) = 32 AND epoch NOT GLOB '*[^0-9a-f]*';
    SELECT CASE WHEN changes() <> 1 THEN RAISE(ABORT, 'authorization cache head invalid') END;
END;
