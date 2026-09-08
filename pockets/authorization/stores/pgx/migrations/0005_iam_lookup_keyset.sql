-- Keyset access paths for the lookup reads (migration source "authorization";
-- authorization-lookup-paging, A6). The three relationship lookups and the roles
-- resource-id lookup became KEYSET reads: `WHERE … AND resource_id > @after
-- ORDER BY resource_id LIMIT @limit`, one page per call. The existing
-- idx_iam_relationships_type_relation (resource_type, relation) does NOT carry
-- resource_id, so the keyset predicate could only be a filter and the order a
-- SORT of the whole matching set — the planner would read every tuple of the
-- type/relation on every page. With resource_id as the third key column the same
-- query becomes a range scan that starts AT the cursor and stops at LIMIT rows.
-- PostgreSQL flavor of the turso 0005 — IDENTICAL filename so the version set
-- matches across dialects; the turso index carries no COLLATE clause because
-- SQLite's BINARY is byte order already.
--
-- COLLATE "C" on resource_id is CONTRACTUAL, not cosmetic: the engine merges
-- several of these ID streams by Go string comparison, so the store must order
-- by the raw bytes. iam_relationships.resource_id is deliberately UNCOLLATED in
-- 0001 (it is a recursion column of the reachable userset-expansion CTE; pinning
-- it in the DDL raises a recursive-term collation mismatch, SQLSTATE 42P21), so
-- the store SQL pins COLLATE "C" per query and this index must carry the SAME
-- collation for the planner to match it. iam_roles needs no COLLATE clause here:
-- every one of its structural columns is already COLLATE "C" (0002).
--
-- ORDINARY, TRANSACTIONAL CREATE INDEX — deliberately NOT CONCURRENTLY.
-- pgxdb.RunMigrations applies the whole stream inside ONE transaction, and
-- CREATE INDEX CONCURRENTLY cannot run in a transaction block. The consequence
-- for an operator: each build takes a SHARE lock on its table for its duration —
-- concurrent READS proceed, concurrent WRITES (relationship grants/revokes, role
-- assignments) BLOCK until it finishes. Schedule this migration accordingly on a
-- large existing table; on an empty or small one it is instant. A host that
-- cannot take the write pause may create these two indexes CONCURRENTLY by hand
-- BEFORE deploying — IF NOT EXISTS then makes this file a no-op.
--
-- Hosts pin the migration ledger verbatim (they scaffold with ExportMigrations
-- and their own ledger test asserts the file set), so this file is a REQUIRED
-- re-export on upgrade: copy it into the host's migration tree beside 0001-0004.

-- The relationship lookups' keyset path: (resource_type, relation) equality then
-- an ordered resource_id range. Serves LookupResourceIDs (whose expansion join
-- filters on the subject side), LookupResourceIDsByRelationTarget (whose target
-- filter is a heap-side Filter — this index supplies the order and the cursor
-- start), and the descendant closure's terms.
CREATE INDEX IF NOT EXISTS idx_iam_relationships_type_relation_resource
    ON iam_relationships (resource_type, relation, resource_id COLLATE "C");

-- The roles lookup's keyset path, ordered for its actual predicate:
-- (subject_type, subject_id, resource_type) equality, then the ordered
-- resource_id range the cursor walks, with role last so the granting-role
-- IN-list is answered from the index instead of the heap. The global ('', '')
-- probe rides the same leading columns.
CREATE INDEX IF NOT EXISTS idx_iam_roles_subject_resource_lookup
    ON iam_roles (subject_type, subject_id, resource_type, resource_id, role);
