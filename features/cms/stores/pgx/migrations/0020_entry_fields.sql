-- The custom fields: ACF fields (the WP wp_postmeta analog). Per-type fields
-- live here so adding a field is never a schema migration (plan §2). ordinal is
-- reserved for repeaters (deferred); flat fields use 0.
CREATE TABLE IF NOT EXISTS entry_fields (
    entry_id TEXT NOT NULL REFERENCES entries (id) ON DELETE CASCADE,
    key      TEXT NOT NULL,
    kind     TEXT NOT NULL,              -- mirrors FieldKind; for read coercion
    value    TEXT NOT NULL,              -- text/number/bool/date(RFC3339)/relation(entryID)/image(assetID)
    ordinal  INTEGER NOT NULL DEFAULT 0, -- reserved for repeaters; 0 for flat fields
    PRIMARY KEY (entry_id, key, ordinal)
);
