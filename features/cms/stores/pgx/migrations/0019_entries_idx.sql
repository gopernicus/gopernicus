-- Keyset list index: type-scoped lists order by (created_at, id) descending,
-- matching the cursor's order field. Covers List/ListByTerm filtering by type.
CREATE INDEX IF NOT EXISTS idx_entries_type_created ON entries (type, created_at, id);

-- Public-listing index (plan §2): published entries of a type by publish date.
CREATE INDEX IF NOT EXISTS idx_entries_type_status_pub ON entries (type, status, published_at);
