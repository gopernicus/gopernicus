-- Matches the PostgreSQL payload-bytes migration version. SQLite's existing
-- TEXT-affinity column already accepts BLOB values; new writers bind bytes and
-- readers still accept historical TEXT. No schema or historical data rewrite.
SELECT 1;
