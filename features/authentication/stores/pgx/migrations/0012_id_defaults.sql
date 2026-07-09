-- Database-generated entity keys (segovia-lessons phase 04, amended D10): a
-- host that wires cryptids.Database sends Create an empty id; the store omits
-- the id column and these defaults generate the key, read back with RETURNING.
-- gen_random_uuid() is built into PostgreSQL 13+; the ::text cast keeps the
-- column TEXT — bundled stores are text-keyed end to end. Secret-keyed tables
-- (sessions, verification codes/tokens, oauth states) get NO default: an empty
-- secret is a bug, never a strategy.
ALTER TABLE users ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE service_accounts ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE api_keys ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE security_events ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
ALTER TABLE invitations ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;
