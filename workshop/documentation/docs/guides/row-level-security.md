---
title: Row-level security with pgxdb
description: Transaction-local settings, a store querier that knows it is scoped, fail-closed policies, schema and role rules, a policy-test checklist, copyable catalog posture checks, an owner/app preflight and an endpoint matrix.
---

# Row-level security with pgxdb

PostgreSQL row-level security (RLS) lets the database filter a table's rows by a per-transaction
value, such as the current tenant. Gopernicus ships two generic pgxdb primitives for it (pgxdb
v0.16.0 or later) and no tenant concept: no tenancy pocket, no policy generation, nothing
tenant-named. The policies, the tenant model and the error handling are the host's. This guide is
standard PostgreSQL guidance applied to pgxdb.

## Transaction-local settings

`(*DB).TransactWithSettings(ctx, settings, fn)` is `Transact` with one extra first statement:

```go
err := db.TransactWithSettings(ctx, []pgxdb.Setting{{Name: "app.tenant_id", Value: tenantID}},
	func(ctx context.Context) error {
		return programs.Create(ctx, p) // the store reads db.TxFrom(ctx)
	})
```

It runs `SELECT set_config($1, $2, true), …` with every name and value bound, so the settings
end with the transaction on commit, rollback or panic. Nesting, cleanup and the per-instance
ambient context are `Transact`'s: a second `Transact` or `TransactWithSettings` on the same `*DB`
inside `fn` returns `ErrNestedTransact`. Validation runs before the transaction begins and
acquires no connection. Every refusal wraps `sdk.ErrInvalidInput`:

- at least one setting;
- each name is a lowercase, dotted custom parameter (`app.tenant_id`). Core parameters such as
  `role` and `search_path` are undotted, so they are unreachable. Extension parameters
  (`plpgsql.*`) are dotted and are not blocked, so use a prefix you own. This is a guardrail, not
  a security boundary. PostgreSQL matches names case-insensitively; the lowercase rule keeps a
  policy's `current_setting('app.tenant_id')` identical to what was set;
- no duplicate names;
- each value is non-empty, valid UTF-8 and free of NUL bytes.

Settings unrelated to tenancy (for example `app.actor_id`, read by audit triggers) use the same
helper.

### Why not a session `SET`

A session `SET app.tenant_id = …` or `set_config(…, false)` stays on the connection after the
transaction ends. Under a pool, the next borrower inherits the previous request's tenant. A
transaction-local setting cannot leak that way.

`SET LOCAL` (or `set_config(..., true)`) outside a transaction block does not error. PostgreSQL
emits a WARNING and the setting has no lasting effect, because each autocommit statement is its
own transaction. That is why the helper opens the transaction itself.

The helper uses `SELECT set_config($1, $2, true)` and not `SET LOCAL name = $1` because `SET` is a
utility statement and cannot take bind parameters.

Transaction-mode PgBouncer works with transaction-local settings, because the transaction stays on
one server connection. It does not work with session `SET`, because the next transaction can land
on a different server connection.

## The store's querier

`QuerierFrom` falls back to the pool when there is no transaction. For a tenant-owned table under
FORCE RLS that fallback answers with a silent empty result. A plain `Transact` without the setting
answers the same way. A tenant-owned store therefore checks both conditions itself:

```go
tx, ok := db.TxFrom(ctx)
if !ok {
	return nil, ErrNoTenantScope
}
if _, ok := tx.Setting("app.tenant_id"); !ok {
	return nil, ErrNoTenantScope
}
return tx, nil
```

`tx.Setting(name)` reports what `TransactWithSettings` applied to this transaction, from memory,
with no round trip. It is `("", false)` for a transaction begun by `Transact` or
`TransactSnapshot`. Refuse loudly (log at ERROR, return an error) and never use `QuerierFrom` for
tenant-owned stores.

## Policies

Fail closed with `NULLIF`:

```sql
ALTER TABLE programs ENABLE ROW LEVEL SECURITY;
ALTER TABLE programs FORCE ROW LEVEL SECURITY;

CREATE POLICY programs_select ON programs FOR SELECT
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), ''));
CREATE POLICY programs_insert ON programs FOR INSERT
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), ''));
CREATE POLICY programs_update ON programs FOR UPDATE
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), ''))
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), ''));
CREATE POLICY programs_delete ON programs FOR DELETE
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), ''));
```

- `current_setting(name, true)` returns NULL instead of erroring when the parameter was never
  defined in the session.
- `NULLIF(…, '')` is required: once a session has set a placeholder, it reads back `''` (not
  NULL) after the local value reverts. Without `NULLIF`, a row with an empty `tenant_id` would
  match an unscoped transaction.
- Compare as text. Keep the tenant column text-typed (or compare `tenant_id::text`), so a
  malformed setting matches nothing instead of failing a cast.

## Schema

- Tenant-owned tables use a composite primary key `(tenant_id, id)`, and foreign keys between them
  carry `tenant_id`: `FOREIGN KEY (tenant_id, program_id) REFERENCES programs (tenant_id, id)`.
  Foreign-key and unique checks bypass RLS, so a single-column reference could point at another
  tenant's row.
- Unique constraints are tenant-scoped: `UNIQUE (tenant_id, slug)`, not `UNIQUE (slug)`. A global
  unique constraint also lets one tenant probe for another's values.

## Roles

- The runtime role is `NOSUPERUSER NOBYPASSRLS` and owns nothing. Superusers and roles with
  BYPASSRLS bypass RLS even with FORCE ROW LEVEL SECURITY; FORCE only makes the table owner
  subject to its policies. A docker image's default `postgres` role is a superuser.
- Keep FORCE on every tenant-owned table anyway, so a migrator that owns the table, or a runtime
  role that ends up owning one, is still filtered.

See *Production database posture*, "Two roles", for the migrator/runtime split and grants, and
[Roles: the owner/app split and a read-only preflight](#roles-the-ownerapp-split-and-a-read-only-preflight)
below for a release gate that checks it.

## Views and functions

- A view runs with its owner's privileges by default, so RLS sees the owner. Create views over
  tenant-owned tables `WITH (security_invoker = true)`.
- A `SECURITY DEFINER` function runs as its owner. If that owner is exempt from the table's
  policies, the function is too. Prefer `SECURITY INVOKER` for anything reading tenant data.

## Errors

- An `UPDATE` or `DELETE` that targets rows outside the policy silently affects 0 rows. Check
  `RowsAffected` where a missing row matters and translate 0 into `sdk.ErrNotFound`.
- An `INSERT` or `UPDATE` whose new row fails `WITH CHECK` raises SQLSTATE 42501, which pgxdb maps
  to `sdk.ErrForbidden` and keeps the `*pgconn.PgError` in the chain.
- Translate a missing or empty tenant before calling `TransactWithSettings`. Its own refusal is
  `sdk.ErrInvalidInput`, which becomes a 400 if it bubbles to the HTTP edge. An absent tenant is
  usually an authentication or routing problem, not bad input.

## Scope of protection

RLS here guards against application mistakes, such as a query that forgot its `WHERE tenant_id`
clause. It is not a SQL-injection boundary: the runtime role can call `set_config` itself, so SQL
an attacker controls can change the setting. Bind every value and keep statements parameterized.

## Cross-tenant access

Under FORCE RLS, a plain `Transact` sees zero tenant rows; it fails closed. A deliberate
cross-tenant path is a host choice. Two standard shapes:

1. **A policy branch on a second setting.** For example
   `USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '') OR current_setting('app.scope', true) = 'platform')`.
   Pass `app.scope` through `TransactWithSettings` only on admin paths, after the host's own
   authorization check. This is the simpler default.
2. **A separate runtime role with `BYPASSRLS`** on its own `*DB`. Its stores are not filtered at
   all, so keep that `*DB` out of tenant-facing code paths.

Never default `app.tenant_id` with `ALTER ROLE … SET app.tenant_id = …` (or `ALTER DATABASE … SET`).
A role default turns every unscoped transaction into a scoped one and defeats fail-closed.

## Gopernicus pocket tables stay global

The pocket tables (users, sessions, tuples, jobs and the rest) are global, not tenant-owned. **Do
not ENABLE ROW LEVEL SECURITY on them.** ENABLE with no policy denies every row to a non-owner, and
the pocket stores read them through `QuerierFrom`, outside any tenant scope.

No pocket ships policies, and none will: the canonical migration sets are global and
additive-only. Policies on pocket tables are a won't-do, not a gap.

### The jobs `tenant_id` is a label, not a wall

`job_queue`, `job_schedules`, `fenced_job_queue` and `job_schedule_occurrences` carry a
`tenant_id` column, and so does `event_outbox`. It is the optional host-defined boundary a job,
schedule or event is filed under. The host filters and reports on it. RLS does not enforce it:

- the worker is cross-tenant by construction. Its claim loop runs with no request and no
  tenant, so under FORCE ROW LEVEL SECURITY every claim would return zero rows;
- the jobs stores open their own transactions (`InTx`) and never join a host
  `TransactWithSettings`, so no tenant setting reaches them.

A handler that works for one tenant reads `TenantID` from the job it was handed and opens its own
scope (for example `Scope.Run(ctx, job.TenantID, …)`) before it touches tenant-owned tables.

### Enqueue after commit

An enqueue inside a held `Transact` or `TransactWithSettings` does not join it. It runs on a
second pool connection, so:

- the job can be claimed and run before the domain row commits, and see nothing;
- it survives a rollback of the domain transaction;
- it holds a second connection while the first is open, so a tight pool can starve: every
  request holds one connection and waits for another.

The authentication stores have the same shape: an invitation or a user created inside a host
transaction commits on its own connection, so "create the organization and invite its owner"
is not atomic. Keep enqueues and pocket writes after the commit, and make the second step
retryable (an idempotency id on the job, a converging invitation create). An atomic outbox,
in which pocket stores join a host transaction, is an open framework item. It is not
part of the RLS story, which keeps pocket tables global.

## Two databases

Ambient transactions are per `*DB` (pgxdb v0.15.0 or later). A host with a business database and
an identity database keeps the tenant scope on the business `*DB` only. A different instance's
transaction in the context is neither joined nor refused, so an identity store called inside the
business `TransactWithSettings` runs on its own pool, unscoped, which is what a global table wants.
See *Persistence & migrations*, "Two databases never share a transaction".

## Battleboard's `Scope`, against pgxdb v0.16.0

Battleboard's tenant scope (`internal/outbound/domains/tenancy/scope.go`) shrinks to host policy
over the two primitives. Written against pgxdb v0.16.0 (there is no package-level `TxFromContext`
since v0.15.0):

```go
// Scope binds tenant-owned stores to one tenant per transaction.
type Scope struct {
	db  *pgxdb.DB
	log *slog.Logger
}

// Run runs fn in one transaction bound to tenantID. A Run inside another
// transaction of the same *DB is refused (pgxdb.ErrNestedTransact).
func (s *Scope) Run(ctx context.Context, tenantID string, fn func(ctx context.Context) error) error {
	if tenantID == "" {
		return fmt.Errorf("tenant scope: empty tenant id: %w", sdk.ErrInvalidInput)
	}
	return s.db.TransactWithSettings(ctx, []pgxdb.Setting{{Name: "app.tenant_id", Value: tenantID}}, fn)
}

// Querier returns the scope's transaction, or ErrNoTenantScope (logged at
// ERROR) outside Run: RLS would otherwise answer with a silent empty result.
func (s *Scope) Querier(ctx context.Context) (pgxdb.Querier, error) {
	tx, ok := s.db.TxFrom(ctx)
	if ok {
		if _, scoped := tx.Setting("app.tenant_id"); scoped {
			return tx, nil
		}
	}
	s.log.ErrorContext(ctx, "tenant-owned store call outside a tenant scope", "error", ErrNoTenantScope)
	return nil, ErrNoTenantScope
}
```

No context marker of the host's own is needed: `TxFrom` proves the transaction is this `*DB`'s,
and `Setting` proves `TransactWithSettings` scoped it. Since v0.15.0 a different instance's
transaction in the context is neither joined nor refused, so `Run` on the business `*DB` nests
freely inside, or around, an identity database's `Transact`.

## Policy-test checklist

Run these against a real PostgreSQL as a non-owner, `NOSUPERUSER NOBYPASSRLS` role (a test can enter
one with `SET LOCAL ROLE` as the first statement of the callback, never a plain `SET ROLE`):

- with tenant A set, only A's rows are visible;
- inserting a row for tenant B fails with `sdk.ErrForbidden` (SQLSTATE 42501);
- updating or deleting B's row affects 0 rows;
- a plain `Transact` (no setting) sees zero rows;
- the table owner, under FORCE, still sees only A's rows;
- after commit, `current_user` on the pool equals the session user, and
  `current_setting('app.tenant_id', true)` is `''` or NULL;
- every foreign key and unique constraint on a tenant-owned table includes `tenant_id`;
- every view over a tenant-owned table is `security_invoker`.

pgxdb's own live suite runs this shape (`TestLive_TransactWithSettings_RowLevelSecurity`).

## Adding policies to your own tables

The host owns its migration ledger, so its row-security migration is a host file appended after
the exported canonical files:

```text
workshop/migrations/primary/
  0001_… through 0031_…          byte-identical copies of the stores' files
  0032_programs.sql              the host's own tables
  0033_programs_row_security.sql ENABLE, FORCE and the policies
```

Guard P9 (`gopernicus guard --posture`) requires a byte-identical copy of every mounted store's
files and permits any host file beside them, so this passes. Put the policies in their own file,
or in the file that creates the table, never in an edited copy of a store file. Policies go on
the host's own tables only, never on pocket tables (above).

## Catalog posture

The policy-test checklist proves behaviour on the tables you test. A catalog check proves that
every table is covered, including the one added last week. It is three read-only queries over
`pg_class`, `pg_policy`, `pg_attribute`, `pg_index` and `pg_roles`. They are standard
PostgreSQL. Copy them into your host, set the parameters at the top, and keep them there.

The rules:

- **Run them as the runtime role**, on the server's own DSN. The table checks read the same
  catalog for any role. The role check describes `current_user`, and a superuser bypasses RLS
  whatever the catalog says, so a superuser run proves nothing.
- **Output names objects only**: tables, policies, roles. Never setting values, rows or
  predicate text. A failing check is safe to print in CI.
- **Where it lives.** A host `workshop/` test (H7 lets `workshop/**` open pgxdb), run by the
  host's `make check` against the migrated development database: each returned row is one
  `t.Errorf`, and zero rows is a pass.
- **The exempt list is explicit.** Exempt means exempt whether or not the table has the column:
  `schema_migrations`, `ratelimit_windows`, and the pocket tables if they share the schema, the
  jobs tables included (their `tenant_id` is a label). A table that has neither the column nor
  an exempt entry is a finding, so a new table must choose. Mounting the pocket stores in their
  own schemas with `WithSchema` keeps this list short.

### Tables and policies

Each tenant-owned table, meaning a table in the schema that carries the column and is not
exempt, must be ENABLE and FORCE ROW LEVEL SECURITY. For each command (SELECT, INSERT, UPDATE,
DELETE) exactly one permissive `TO PUBLIC` policy must apply, either one per command or one
`FOR ALL` policy. Its predicate must be one you accept: USING for SELECT, UPDATE and DELETE, and
WITH CHECK for INSERT and UPDATE. An UPDATE or ALL policy with no WITH CHECK applies its USING
to new rows, and the query judges it that way. A second permissive policy is a finding because
permissive policies are OR-ed, so it widens access. Restrictive policies only narrow access and
are not judged.

`predicates` is yours. It holds every predicate text you accept, exactly as `pg_get_expr` prints
it back on PostgreSQL 18. That is not the text you wrote: PostgreSQL adds casts and
parentheses. Print it once from a table you trust and paste the result:

```sql
SELECT polname, pg_get_expr(polqual, polrelid) AS using_expr, pg_get_expr(polwithcheck, polrelid) AS check_expr
FROM pg_policy WHERE polrelid = 'app.programs'::regclass;
```

The three entries below are the `NULLIF` predicate from *Policies* on a text column, the same
predicate on a `uuid` column compared as text, and the `app.scope` branch from *Cross-tenant
access*. Keep the ones you use. A table whose SELECT carries the `app.scope` branch and whose
writes carry the plain predicate passes, because each clause is matched on its own.

```sql
WITH params AS (
  SELECT 'app'::text       AS schema_name,
         'tenant_id'::text AS column_name,
         -- skipped whether or not they carry the column; list the pocket tables
         -- only if they share this schema
         ARRAY['schema_migrations', 'ratelimit_windows',
               'job_queue', 'job_schedules', 'fenced_job_queue', 'job_schedule_occurrences',
               'event_outbox']::text[] AS exempt,
         -- every accepted USING / WITH CHECK text, exactly as pg_get_expr prints it
         ARRAY[
           $p$(tenant_id = NULLIF(current_setting('app.tenant_id'::text, true), ''::text))$p$,
           $p$((tenant_id)::text = NULLIF(current_setting('app.tenant_id'::text, true), ''::text))$p$,
           $p$((tenant_id = NULLIF(current_setting('app.tenant_id'::text, true), ''::text)) OR (current_setting('app.scope'::text, true) = 'platform'::text))$p$
         ]::text[] AS predicates
),
tables AS (
  SELECT c.oid, c.relname,
         c.relrowsecurity      AS enabled,
         c.relforcerowsecurity AS forced,
         c.relname = ANY (p.exempt) AS exempt,
         EXISTS (SELECT 1 FROM pg_attribute a
                 WHERE a.attrelid = c.oid AND a.attname = p.column_name
                   AND a.attnum > 0 AND NOT a.attisdropped) AS has_column,
         COALESCE((SELECT a.attname = p.column_name
                   FROM pg_index i
                   JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[0]
                   WHERE i.indrelid = c.oid AND i.indisprimary), false) AS keyed_first
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  CROSS JOIN params p
  WHERE n.nspname = p.schema_name AND c.relkind IN ('r', 'p')
),
scoped AS (
  SELECT * FROM tables WHERE has_column AND NOT exempt
),
commands(cmd, label, needs_using, needs_check) AS (
  VALUES ('r', 'SELECT', true, false), ('a', 'INSERT', false, true),
         ('w', 'UPDATE', true, true),  ('d', 'DELETE', true, false)
),
policies AS (
  SELECT s.relname, pol.polname, pol.polcmd::text AS polcmd, pol.polpermissive,
         pol.polroles = '{0}'::oid[] AS to_public,
         pg_get_expr(pol.polqual, pol.polrelid) AS using_expr,
         -- an UPDATE or ALL policy with no WITH CHECK applies its USING to new rows
         COALESCE(pg_get_expr(pol.polwithcheck, pol.polrelid),
                  CASE WHEN pol.polcmd IN ('w', '*') THEN pg_get_expr(pol.polqual, pol.polrelid) END) AS check_expr
  FROM scoped s
  JOIN pg_policy pol ON pol.polrelid = s.oid
),
violations(object, finding) AS (
  SELECT t.relname, 'no ' || p.column_name || ' column and not exempt'
  FROM tables t CROSS JOIN params p WHERE NOT t.has_column AND NOT t.exempt
  UNION ALL
  SELECT relname, 'row level security not enabled' FROM scoped WHERE NOT enabled
  UNION ALL
  SELECT relname, 'row level security not forced' FROM scoped WHERE NOT forced
  UNION ALL
  -- the Schema section's advice; drop this branch if your keys differ
  SELECT relname, 'primary key does not lead with the column' FROM scoped WHERE NOT keyed_first
  UNION ALL
  SELECT s.relname, c.label || ': ' || count(pol.polname) || ' permissive policies, want 1'
  FROM scoped s CROSS JOIN commands c
  LEFT JOIN policies pol ON pol.relname = s.relname AND pol.polpermissive AND pol.polcmd IN (c.cmd, '*')
  GROUP BY s.relname, c.label HAVING count(pol.polname) <> 1
  UNION ALL
  SELECT relname || '.' || polname, 'permissive policy is not TO PUBLIC'
  FROM policies WHERE polpermissive AND NOT to_public
  UNION ALL
  SELECT pol.relname || '.' || pol.polname, c.label || ' USING is not an accepted predicate'
  FROM policies pol JOIN commands c ON pol.polcmd IN (c.cmd, '*') CROSS JOIN params p
  WHERE pol.polpermissive AND c.needs_using AND NOT COALESCE(pol.using_expr = ANY (p.predicates), false)
  UNION ALL
  SELECT pol.relname || '.' || pol.polname, c.label || ' WITH CHECK is not an accepted predicate'
  FROM policies pol JOIN commands c ON pol.polcmd IN (c.cmd, '*') CROSS JOIN params p
  WHERE pol.polpermissive AND c.needs_check AND NOT COALESCE(pol.check_expr = ANY (p.predicates), false)
)
SELECT object, finding FROM violations ORDER BY object, finding;
```

A clean schema returns zero rows. Typical findings:

```text
 object                 | finding
------------------------+-----------------------------------------------
 invoices               | row level security not forced
 programs               | SELECT: 2 permissive policies, want 1
 programs.programs_peek | SELECT USING is not an accepted predicate
 widgets                | no tenant_id column and not exempt
```

The query does not check foreign keys or views. The checklist's items for those still stand, and
battleboard's `checkCompositeForeignKeys` is one way to automate the first.

### The runtime role

Run this as the runtime role. It refuses on the attributes that bypass RLS or let the role
grant itself out of it: SUPERUSER, BYPASSRLS, CREATEROLE, CREATEDB and REPLICATION. It also
refuses on membership in a role that bypasses RLS, and on owning, or being a member of the owner
of, the schema or its tables (an owner can `ALTER TABLE … DISABLE ROW LEVEL SECURITY`).
`pg_has_role(…, 'MEMBER')` follows memberships transitively, `NOINHERIT` and `SET FALSE`
grants included. Every other membership is listed for you to judge, not refused: managed
providers grant their own roles, and only you know which are acceptable.

```sql
WITH params AS (
  SELECT 'app'::text AS schema_name
),
me AS (
  SELECT * FROM pg_roles WHERE rolname = current_user
),
member_of AS (
  SELECT r.* FROM pg_roles r, me
  WHERE r.oid <> me.oid AND pg_has_role(me.oid, r.oid, 'MEMBER')
)
SELECT 'refuse' AS verdict, 'holds ' || v.attr AS finding
FROM me, LATERAL (VALUES ('SUPERUSER', me.rolsuper), ('BYPASSRLS', me.rolbypassrls),
                         ('CREATEROLE', me.rolcreaterole), ('CREATEDB', me.rolcreatedb),
                         ('REPLICATION', me.rolreplication)) v(attr, held)
WHERE v.held
UNION ALL
SELECT 'refuse', 'member of ' || rolname || ', which bypasses row level security'
FROM member_of WHERE rolsuper OR rolbypassrls
UNION ALL
SELECT DISTINCT 'refuse', 'is or is a member of ' || pg_get_userbyid(c.relowner) || ', which owns tables in ' || n.nspname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN params p CROSS JOIN me
WHERE n.nspname = p.schema_name AND c.relkind IN ('r', 'p')
  AND pg_has_role(me.oid, c.relowner, 'MEMBER')
UNION ALL
SELECT 'refuse', 'is or is a member of ' || pg_get_userbyid(n.nspowner) || ', which owns schema ' || n.nspname
FROM pg_namespace n
CROSS JOIN params p CROSS JOIN me
WHERE n.nspname = p.schema_name AND pg_has_role(me.oid, n.nspowner, 'MEMBER')
UNION ALL
SELECT 'judge', 'member of ' || rolname
FROM member_of WHERE NOT (rolsuper OR rolbypassrls)
ORDER BY 1 DESC, 2;
```

A test fails on any `refuse` row and compares the `judge` rows with the list of memberships
you have accepted. A runtime role you created by hand and granted nothing returns zero rows.

### A pgxdb probe, later

These queries are copied, not called, for now. A pgxdb probe that takes the schema, column,
predicates and exempt list from the caller and reports this evidence is planned once a second
host has its own copy to diff against this one. Until then, the host owns the SQL and its
judgement.

## Roles: the owner/app split and a read-only preflight

[Production database posture](./database-posture.md), "Two roles", splits a migrator (here,
the **owner**) from a runtime role (here, the **app** role). A host with RLS needs that split, plus a gate that proves
it before each release.

Create both roles by hand, once per environment, as an administrator. Set passwords with psql's
`\password`, a secret manager or the provider's API, never in a file in the repository:

```sql
CREATE ROLE app_owner   LOGIN NOSUPERUSER;
CREATE ROLE app_runtime LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE SCHEMA app AUTHORIZATION app_owner;
GRANT USAGE ON SCHEMA app TO app_runtime;
```

The owner runs the migrations, so it owns every object. The app role owns nothing and holds
exactly the privileges your matrix lists: which tables it reads, which it appends to, and which
it may update or delete. That matrix is the host's, and the audit-rail revokes in *Production
database posture* are part of it.

The preflight is read-only (`BEGIN READ ONLY … ROLLBACK`). It can run as the owner or as any
role that can read the catalog. It checks that:

- the two roles exist and are distinct;
- the app role holds no bypass attributes and is not a member of the owner;
- the app role owns nothing;
- the owner owns the schema and everything in it;
- the app role has `USAGE` on the schema and no `CREATE`.

Add one branch per matrix row where the comment says so:

```sql
BEGIN READ ONLY;

WITH params AS (
  SELECT 'app_runtime'::text  AS app_role,
         'app_owner'::text    AS owner_role,
         ARRAY['app']::text[] AS schemas
),
app AS (
  SELECT r.* FROM pg_roles r, params p WHERE r.rolname = p.app_role
),
violations(check_name, detail) AS (
  SELECT 'roles', 'app_role and owner_role must be two distinct existing roles'
  FROM params p
  WHERE p.app_role = p.owner_role
     OR (SELECT count(*) FROM pg_roles WHERE rolname IN (p.app_role, p.owner_role)) <> 2
  UNION ALL
  SELECT 'app_role attribute', 'holds ' || v.attr
  FROM app a, LATERAL (VALUES ('SUPERUSER', a.rolsuper), ('BYPASSRLS', a.rolbypassrls),
                              ('CREATEROLE', a.rolcreaterole), ('CREATEDB', a.rolcreatedb),
                              ('REPLICATION', a.rolreplication)) v(attr, held)
  WHERE v.held
  UNION ALL
  SELECT 'app_role membership', 'member of ' || p.owner_role
  FROM app a CROSS JOIN params p
  WHERE EXISTS (SELECT 1 FROM pg_roles o WHERE o.rolname = p.owner_role)
    AND pg_has_role(a.oid, p.owner_role, 'MEMBER')
  UNION ALL
  SELECT 'app_role owns', pg_describe_object(d.classid, d.objid, d.objsubid)
  FROM pg_shdepend d JOIN app a ON a.oid = d.refobjid
  WHERE d.refclassid = 'pg_authid'::regclass AND d.deptype = 'o'
    AND d.dbid IN (0, (SELECT oid FROM pg_database WHERE datname = current_database()))
  UNION ALL
  SELECT 'schema missing', s
  FROM params p, unnest(p.schemas) s
  WHERE NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = s)
  UNION ALL
  SELECT 'owner', 'schema ' || n.nspname || ' owned by ' || pg_get_userbyid(n.nspowner)
  FROM pg_namespace n CROSS JOIN params p
  WHERE n.nspname = ANY (p.schemas) AND pg_get_userbyid(n.nspowner) <> p.owner_role
  UNION ALL
  SELECT 'owner', c.relkind::text || ' ' || n.nspname || '.' || c.relname || ' owned by ' || pg_get_userbyid(c.relowner)
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace CROSS JOIN params p
  WHERE n.nspname = ANY (p.schemas) AND c.relkind IN ('r', 'p', 'S', 'v', 'm', 'f')
    AND pg_get_userbyid(c.relowner) <> p.owner_role
  UNION ALL
  SELECT 'owner', 'function ' || f.oid::regprocedure || ' owned by ' || pg_get_userbyid(f.proowner)
  FROM pg_proc f JOIN pg_namespace n ON n.oid = f.pronamespace CROSS JOIN params p
  WHERE n.nspname = ANY (p.schemas) AND pg_get_userbyid(f.proowner) <> p.owner_role
  UNION ALL
  SELECT 'schema usage', 'app_role lacks USAGE on ' || n.nspname
  FROM pg_namespace n CROSS JOIN app a CROSS JOIN params p
  WHERE n.nspname = ANY (p.schemas) AND NOT has_schema_privilege(a.oid, n.oid, 'USAGE')
  UNION ALL
  SELECT 'schema create', 'app_role can CREATE in ' || n.nspname
  FROM pg_namespace n CROSS JOIN app a CROSS JOIN params p
  WHERE n.nspname = ANY (p.schemas) AND has_schema_privilege(a.oid, n.oid, 'CREATE')
  -- your privilege matrix goes here: one branch per table and privilege it
  -- expects, compared with has_table_privilege(a.oid, c.oid, '<privilege>')
)
SELECT check_name, detail FROM violations ORDER BY check_name, detail;

ROLLBACK;
```

Zero rows is a pass. Run it in the release pipeline before the deploy, and fail the deploy on
any row. With `psql -X -f preflight.sql`, wrap the count in `\gset` and `\if` to get a non-zero
exit. Battleboard's `workshop/dbroles/preflight.sql` does this, with the matrix in
`_matrix.sql`.

## An endpoint matrix

RLS fences rows. It does not stop a route from confirming that another tenant's object exists,
for example a 403 for B's real id beside a 404 for an id that does not exist. An endpoint
matrix test catches that, and it catches the new route nobody classified.

The pattern, as host code under `cmd/server` (a `_test.go`, so H10 does not apply):

1. **Record every registration.** Build the router the way `cmd/server` does, through the one
   function that mounts every route, and list what it registered. From sdk v0.22.0,
   `web.WebHandler.Routes()` returns a copy of every registration as
   `web.Route{Method, Pattern, Raw}`, with `Method` upper-cased (`""` for any method) and
   `Pattern` the final path including any group prefix. Pocket routes are included, because a
   pocket registers through the host's router. On an older sdk, a recording wrapper does the
   same for anything mounted through `pockets.RouteRegistrar`:

   ```go
   type recordingRouter struct {
   	*web.WebHandler
   	routes []string
   }

   func (r *recordingRouter) Handle(method, path string, h http.HandlerFunc, mw ...web.Middleware) {
   	r.routes = append(r.routes, strings.ToUpper(method)+" "+path)
   	r.WebHandler.Handle(method, path, h, mw...)
   }
   ```

2. **Classify every route.** The classes are your authorization model, for example `public`,
   `self`, `cross-tenant` and `platform`. Keep a map from `"METHOD /pattern"` to its class. A
   registered route with no entry fails the test, and so does an entry for a route that is no
   longer registered:

   ```go
   got := map[string]bool{}
   for _, rt := range router.Routes() {
   	if rt.Raw {
   		t.Errorf("%s: raw registration; classify what it serves by hand", rt.Pattern)
   		continue
   	}
   	got[rt.Method+" "+rt.Pattern] = true
   }
   for route := range got {
   	if _, ok := matrix[route]; !ok {
   		t.Errorf("%s: registered but not in the matrix", route)
   	}
   }
   for route := range matrix {
   	if !got[route] {
   		t.Errorf("%s: in the matrix but not registered", route)
   	}
   }
   ```

   Routes served behind a `HandleRaw` subtree (a nested mux, a file server) are invisible to
   `Routes()`. List them by hand.

3. **Probe across tenants.** As tenant A's user, hit every route whose path names a tenant or a
   tenant's object twice: once with tenant B's real id, once with an id that does not exist.
   The two answers must be byte-identical (status and body) and never 2xx. Platform routes
   refuse a tenant user, and signed out, every route except the public ones refuses.

4. **Every new route needs an entry**, including the ones a pocket upgrade adds. Authentication
   v0.34.0 adds `POST /auth/invitations/decline`, which belongs in `public`.

Public routes that act on a mailed secret, such as invitation decline, are POST-only, so mail
scanners and link prefetchers cannot trigger them. The matrix can assert that a GET to each one
answers 405.

## Scaffolding

`gopernicus init` emits a host with no pockets: `go.mod`, a `cmd/server` with `/healthz`, a
`Makefile`, `.env.example`, the `workshop/migrations/primary` ledger and, for `--db pgx` or
`--db turso`, a migration runner (see [the `init` command](../workshop/commands.md#init)). It emits nothing for tenancy: no
scope adapter, no roles, no catalog check and no matrix.

An RLS host adds these, in these places:

| Piece | Where |
|---|---|
| the tenant-scope port | `internal/logic/domains/<d>/ports.go` |
| the pgxdb-backed `Scope` | `internal/outbound/domains/<d>/scope.go` |
| the row-security migration | `workshop/migrations/primary/NNNN_<name>_row_security.sql` |
| role creation, grants and the preflight | `workshop/dbroles/*.sql` |
| the catalog and policy tests | `workshop/tenancytest/`, opening pgxdb (H9 allows `Underlying()` in `workshop/`) |
| the endpoint matrix | `cmd/server/matrix_test.go` |

`init --tenancy=rls` is deferred, not refused. A scope adapter, a matrix and a bootstrap all
presuppose mounted pockets and a named domain: H8 requires the `internal/logic` counterpart of
every outbound domain, and `init` mounts no pocket and names no domain. The framework also has
one RLS host to copy from. A scaffold, like the pgxdb probe, waits for a second host's copy to
diff against, so that it encodes the shared shape and not one host's policy design.
