---
title: Row-level security with pgxdb
description: Transaction-local settings, a store querier that knows it is scoped, fail-closed policies, schema and role rules, and a policy-test checklist.
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

See *Production database posture*, "Two roles", for the migrator/runtime split and grants.

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
