-- Database-generated entity keys (segovia-lessons phase 04, amended D10): a
-- host that wires cryptids.Database sends Create an empty id; the store omits
-- the id column and these defaults generate the key (32 hex chars from 16
-- random bytes), read back with RETURNING. SQLite cannot ALTER a column
-- default, so each entity table is rebuilt: create-with-default, copy, drop,
-- rename, recreate indexes. Column order matches the original CREATEs exactly
-- (the INSERT ... SELECT * copy depends on it). The entries rebuild is safe
-- despite the entry_fields / entry_terms foreign keys: migrations run with
-- foreign-key enforcement off, so the drop does not cascade and the child
-- tables re-bind to the rebuilt entries by name.

-- terms (0009)
CREATE TABLE terms_new (
    id         TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    kind       TEXT NOT NULL,
    slug       TEXT NOT NULL,
    name       TEXT NOT NULL,
    parent_id  TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO terms_new SELECT * FROM terms;
DROP TABLE terms;
ALTER TABLE terms_new RENAME TO terms;
CREATE UNIQUE INDEX IF NOT EXISTS idx_terms_kind_slug ON terms (kind, slug);

-- menus (0013)
CREATE TABLE menus_new (
    id         TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO menus_new SELECT * FROM menus;
DROP TABLE menus;
ALTER TABLE menus_new RENAME TO menus;

-- menu_items (0014)
CREATE TABLE menu_items_new (
    id         TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    menu_id    TEXT NOT NULL,
    label      TEXT NOT NULL,
    url        TEXT NOT NULL DEFAULT '',
    parent_id  TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO menu_items_new SELECT * FROM menu_items;
DROP TABLE menu_items;
ALTER TABLE menu_items_new RENAME TO menu_items;
CREATE INDEX IF NOT EXISTS idx_menu_items_menu ON menu_items (menu_id, parent_id, position);

-- assets (0016)
CREATE TABLE assets_new (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size         INTEGER NOT NULL DEFAULT 0,
    storage_key  TEXT NOT NULL,
    alt          TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);
INSERT INTO assets_new SELECT * FROM assets;
DROP TABLE assets;
ALTER TABLE assets_new RENAME TO assets;

-- inquiries (0017)
CREATE TABLE inquiries_new (
    id         TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    message    TEXT NOT NULL,
    created_at TEXT NOT NULL
);
INSERT INTO inquiries_new SELECT * FROM inquiries;
DROP TABLE inquiries;
ALTER TABLE inquiries_new RENAME TO inquiries;

-- entries (0018)
CREATE TABLE entries_new (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    type         TEXT NOT NULL,            -- content type slug: 'article','page','product'
    slug         TEXT NOT NULL,
    title        TEXT NOT NULL,
    status       TEXT NOT NULL,            -- draft | published
    body         TEXT NOT NULL DEFAULT '', -- raw markdown (universal long-form field)
    excerpt      TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    template     TEXT NOT NULL DEFAULT 'default',
    parent_id    TEXT NOT NULL DEFAULT '', -- hierarchy (pages); '' otherwise
    menu_order   INTEGER NOT NULL DEFAULT 0,
    published_at TEXT,                      -- set the first time published
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (type, slug)
);
INSERT INTO entries_new SELECT * FROM entries;
DROP TABLE entries;
ALTER TABLE entries_new RENAME TO entries;
CREATE INDEX IF NOT EXISTS idx_entries_type_created ON entries (type, created_at, id);
CREATE INDEX IF NOT EXISTS idx_entries_type_status_pub ON entries (type, status, published_at);
