-- Taxonomy associations for entries (the entries-era analog of post_terms).
-- Taxonomy stays typed (plan §5); it still associates with entries.
CREATE TABLE IF NOT EXISTS entry_terms (
    entry_id TEXT NOT NULL REFERENCES entries (id) ON DELETE CASCADE,
    term_id  TEXT NOT NULL,
    PRIMARY KEY (entry_id, term_id)
);
CREATE INDEX IF NOT EXISTS idx_entry_terms_term ON entry_terms (term_id);
