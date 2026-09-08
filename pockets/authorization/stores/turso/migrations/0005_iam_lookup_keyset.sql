-- Keyset access paths for the lookup reads (migration source "authorization";
-- authorization-lookup-paging, A6/A7). The three relationship lookups and the
-- roles resource-id lookup became KEYSET reads: `WHERE … AND resource_id > ?
-- ORDER BY resource_id LIMIT ?`, one page per call. The existing
-- idx_iam_relationships_type_relation (resource_type, relation) does NOT carry
-- resource_id, so the keyset predicate could only be a filter and the order a
-- SORT of the whole matching set. With resource_id as the third key column the
-- same query is a range scan that starts AT the cursor and stops at LIMIT rows.
-- Turso dialect of the pgx 0005 — IDENTICAL filename so the version set matches
-- across dialects; structure and semantics are the same.
--
-- No COLLATE clause, deliberately: the ordering contract is RAW BYTE order (the
-- engine merges several of these ID streams by Go string comparison), and
-- SQLite's default BINARY collation IS byte order. No column in this schema
-- declares a collation, so index order and query order agree by construction.
-- The pgx sibling must spell COLLATE "C" out because its cluster collation is a
-- locale.
--
-- Index creation is ordinary DDL here — SQLite has no CONCURRENTLY, and the
-- migration runner applies the stream in one transaction. The build holds the
-- database's write lock for its duration (reads proceed under WAL); on a large
-- existing table schedule this migration like any other write-blocking step.
--
-- Hosts pin the migration ledger verbatim (they scaffold with ExportMigrations
-- and their own ledger test asserts the file set), so this file is a REQUIRED
-- re-export on upgrade: copy it into the host's migration tree beside 0001-0004.

-- The relationship lookups' keyset path: (resource_type, relation) equality then
-- an ordered resource_id range. Serves LookupResourceIDs (whose expansion join
-- filters on the subject side), LookupResourceIDsByRelationTarget (whose target
-- filter is a row-side filter — this index supplies the order and the cursor
-- start), and the descendant closure's terms.
CREATE INDEX IF NOT EXISTS idx_iam_relationships_type_relation_resource
    ON iam_relationships (resource_type, relation, resource_id);

-- The roles lookup's keyset path, ordered for its actual predicate:
-- (subject_type, subject_id, resource_type) equality, then the ordered
-- resource_id range the cursor walks, with role last so the granting-role
-- IN-list is answered from the index instead of the row. The global probe (both
-- scope columns empty) rides the same leading columns.
CREATE INDEX IF NOT EXISTS idx_iam_roles_subject_resource_lookup
    ON iam_roles (subject_type, subject_id, resource_type, resource_id, role);
