-- Optional authorization-cache upgrade. Stop old generation-cache readers first.
DROP TRIGGER IF EXISTS iam_relationships_cache_insert;
DROP TRIGGER IF EXISTS iam_relationships_cache_delete;
DROP TRIGGER IF EXISTS iam_relationships_cache_update;
DROP TRIGGER IF EXISTS iam_roles_cache_insert;
DROP TRIGGER IF EXISTS iam_roles_cache_delete;
DROP TRIGGER IF EXISTS iam_roles_cache_update;
DROP TABLE IF EXISTS iam_cache_invalidation;

CREATE TABLE iam_tuple_cache (
    slot INTEGER PRIMARY KEY CHECK (slot = 1),
    protocol INTEGER NOT NULL CHECK (protocol = 1),
    binding TEXT NOT NULL CHECK (typeof(binding) = 'text' AND length(binding) = 32 AND binding NOT GLOB '*[^0-9a-f]*'),
    receipt TEXT NOT NULL CHECK (typeof(receipt) = 'text')
);
INSERT INTO iam_tuple_cache VALUES (1, 1, lower(hex(randomblob(16))), '');

-- IDs identify exact acknowledgement work. AUTOINCREMENT prevents reuse after
-- processed rows are deleted; they are not a global cache invalidation version.
CREATE TABLE iam_tuple_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    before_tuple TEXT CHECK (before_tuple IS NULL OR json_valid(before_tuple)),
    after_tuple TEXT CHECK (after_tuple IS NULL OR json_valid(after_tuple)),
    CHECK (before_tuple IS NOT NULL OR after_tuple IS NOT NULL)
);

CREATE TRIGGER iam_relationships_tuple_insert AFTER INSERT ON iam_relationships
BEGIN
    SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM iam_tuple_cache WHERE slot = 1 AND protocol = 1)
        THEN RAISE(ABORT, 'authorization tuple cache metadata missing') END;
    INSERT INTO iam_tuple_outbox (after_tuple) VALUES
        (json_array(NEW.resource_type, NEW.resource_id, NEW.relation, NEW.subject_type, NEW.subject_id, NEW.subject_relation));
END;

CREATE TRIGGER iam_relationships_tuple_delete AFTER DELETE ON iam_relationships
BEGIN
    SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM iam_tuple_cache WHERE slot = 1 AND protocol = 1)
        THEN RAISE(ABORT, 'authorization tuple cache metadata missing') END;
    INSERT INTO iam_tuple_outbox (before_tuple) VALUES
        (json_array(OLD.resource_type, OLD.resource_id, OLD.relation, OLD.subject_type, OLD.subject_id, OLD.subject_relation));
END;

CREATE TRIGGER iam_relationships_tuple_update AFTER UPDATE ON iam_relationships
WHEN OLD.resource_type IS NOT NEW.resource_type OR OLD.resource_id IS NOT NEW.resource_id OR OLD.relation IS NOT NEW.relation OR OLD.subject_type IS NOT NEW.subject_type OR OLD.subject_id IS NOT NEW.subject_id OR OLD.subject_relation IS NOT NEW.subject_relation
BEGIN
    SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM iam_tuple_cache WHERE slot = 1 AND protocol = 1)
        THEN RAISE(ABORT, 'authorization tuple cache metadata missing') END;
    INSERT INTO iam_tuple_outbox (before_tuple, after_tuple) VALUES
        (json_array(OLD.resource_type, OLD.resource_id, OLD.relation, OLD.subject_type, OLD.subject_id, OLD.subject_relation),
         json_array(NEW.resource_type, NEW.resource_id, NEW.relation, NEW.subject_type, NEW.subject_id, NEW.subject_relation));
END;
