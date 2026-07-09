-- Database-generated entity keys (segovia-lessons phase 04, amended D10): a
-- host that wires cryptids.Database sends Create an empty id; the store omits
-- the id column and these defaults generate the key, read back with RETURNING.
-- gen_random_uuid() is built into PostgreSQL 13+; the ::text cast keeps the
-- column TEXT — bundled stores are text-keyed end to end.
ALTER TABLE entries ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE assets ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE menus ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE menu_items ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE inquiries ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE terms ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
