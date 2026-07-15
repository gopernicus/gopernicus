# Authorization v1 → v3 data conversion — VALIDATED

Status: **VALIDATED** (authorizationv3; drafted at AZ3-2.1, live-validated at
AZ3-5.1, 2026-07-14). Every detection query below was executed against a
populated v1 fixture on live PostgreSQL (C-collation) and libSQL/SQLite and
returned the expected rows; the scope-seeding SQL was corrected and re-validated
during that run. See [`UPGRADE.md` — Executed evidence](UPGRADE.md#executed-evidence-az3-51-2026-07-14).
This is the *detection-and-repair protocol* the data-preserving adopter path is
built from. It is intentionally
NOT a file in the canonical migration tree — the canonical `0001`–`0004` are the
greenfield final schema (folded clean because no `features/authorization` tag
exists). The full host upgrade runbook (backup, maintenance window, binary
swap, migration export/apply order, rollback boundary, and the gain/lose/retain
access assessment) is [`UPGRADE.md`](UPGRADE.md), which builds directly on this
draft. Both dialect store modules
(`stores/pgx`, `stores/turso`) reference this one document; the detection queries
below are ANSI SQL and run identically against either v1 schema, with the two
dialect notes called out where they differ.

## What actually changed for stored data

Authorization v1 stored the same two tables (`iam_relationships`, `iam_roles`)
with the same columns v3 keeps, so **no column is added or dropped**. What
changed is *meaning and enforcement*:

1. **The userset relation became load-bearing (AZ3-1.1).** v1 expansion
   hard-coded `relation = 'member'`; a stored `subject_relation` of `admin` (or
   any non-`member` value) was silently evaluated as `member`. In v3 the stored
   `subject_relation` is the exact userset relation — `group:eng#member` and
   `group:eng#admin` are distinct and never compare equal.
2. **v3 adds CHECK constraints** the v1 tables did not carry: non-empty
   structural columns, and a consistent global/scoped role pair. Rows that
   violate them must be repaired *before* the v3 schema applies.
3. **New tables** `iam_scopes` (revision anchors) and `iam_mutations` (receipts)
   are empty at cutover. Pre-v3 writes carried no MutationID, so there are **no
   receipts to backfill**; scope anchors are *seeded*, not reconstructed.

## The one rule

> **Never guess `member` for an ambiguous userset relation.** A row whose
> intended userset relation cannot be established from the data alone is a
> **runbook decision by an operator**, never an automatic broad grant. Guessing
> `member` is exactly the v1 defect v3 exists to remove; re-introducing it during
> conversion would silently widen access.

## Step 1 — audit before touching anything

Run every detection query below **read-only** and capture the output. A non-empty
result on a blocking query (marked ⛔) means the v3 schema will not apply until
the rows are repaired or removed by an explicit operator decision.

### 1a. ⛔ Empty structural relationship columns (blocks `ck_iam_relationships_nonempty`)

```sql
SELECT relationship_id, resource_type, resource_id, relation, subject_type, subject_id, subject_relation
FROM iam_relationships
WHERE resource_type = '' OR resource_id = '' OR relation = ''
   OR subject_type = '' OR subject_id = '';
```

`subject_relation` is deliberately absent from this query: empty means a concrete
subject, which is valid. Any row returned is malformed v1 data; decide per row
whether to repair the missing field or delete the tuple.

### 1b. ⛔ Empty structural role columns / half-populated scope pair

```sql
-- non-empty subject/role (blocks ck_iam_roles_nonempty)
SELECT * FROM iam_roles WHERE subject_type = '' OR subject_id = '' OR role = '';

-- consistent global/scoped pair (blocks ck_iam_roles_scope_pair):
-- both empty = global, both non-empty = scoped, never one-and-not-the-other
SELECT * FROM iam_roles WHERE (resource_type = '') <> (resource_id = '');
```

### 1c. Missing / concrete subjects where a userset is required

The schema (declared in Go, not the database) says which `(subject_type,
relation)` positions require a userset. Enumerate the distinct stored shapes and
review each against the compiled schema:

```sql
SELECT resource_type, relation, subject_type, subject_relation, COUNT(*) AS n
FROM iam_relationships
GROUP BY resource_type, relation, subject_type, subject_relation
ORDER BY resource_type, relation, subject_type, subject_relation;
```

For any shape where the schema requires a userset but `subject_relation = ''`,
the row is **ambiguous** — resolve it deliberately (set the intended relation, or
delete the row). Do **not** default it to `member`.

### 1d. Non-`member` usersets that v1 silently evaluated as `member`

These rows *change meaning* under v3 (now evaluated as their exact relation).
Confirm each is intended before cutover:

```sql
SELECT DISTINCT subject_type, subject_relation
FROM iam_relationships
WHERE subject_relation <> '' AND subject_relation <> 'member';
```

### 1e. Silent-conflict rows (v1 data-loss awareness)

v1 already carried the `idx_iam_relationships_unique_subject` index (one relation
per exact SubjectRef per resource), so a second, different relation for the same
subject was **silently dropped at write time** — the first write won and the
conflicting intent was never stored. That lost intent cannot be recovered from
the table. Export the full subject→relation mapping for reconciliation against
the host's own source of truth / application logs:

```sql
SELECT resource_type, resource_id, subject_type, subject_id, subject_relation, relation
FROM iam_relationships
ORDER BY resource_type, resource_id, subject_type, subject_id, subject_relation;
```

Under v3 the same conflict is no longer silent — it surfaces as an explicit
`semantic_conflict` outcome and is resolved atomically with `OpReplace`. There is
nothing to repair in the table here; this step exists so the operator *knows*
whether v1 dropped any intended grants.

## Step 2 — repair

Apply the per-row decisions from Step 1 (repair or delete). This is deliberately
manual: there is no automatic transform, because every ambiguous case is a policy
choice. Re-run 1a and 1b until both return zero rows — only then will the v3
constraints apply.

## Step 3 — seed scope revisions

After the v3 schema is applied, materialize a revision-0 anchor for every scope
that has current state, so the scope set is enumerable and the first v3 mutation
is a clean `0 → 1`. Seed at revision **0** (the default) — never invent a nonzero
revision; the existing rows are the baseline state, not applied v3 changes.

```sql
-- Resource scopes: every resource that has a relationship OR a scoped role.
-- revision is omitted so the column DEFAULT 0 fills it — seed at revision 0.
INSERT INTO iam_scopes (scope_kind, scope_type, scope_id)
SELECT DISTINCT 'resource', resource_type, resource_id FROM iam_relationships
UNION
SELECT DISTINCT 'resource', resource_type, resource_id FROM iam_roles WHERE resource_type <> ''
ON CONFLICT DO NOTHING;

-- Subject scopes: every subject holding a GLOBAL role (empty resource pair).
INSERT INTO iam_scopes (scope_kind, scope_type, scope_id)
SELECT DISTINCT 'subject', subject_type, subject_id FROM iam_roles WHERE resource_type = ''
ON CONFLICT DO NOTHING;
```

Dialect note: `ON CONFLICT DO NOTHING` works on both PostgreSQL and libSQL/SQLite
(SQLite ≥ 3.24). On older SQLite substitute `INSERT OR IGNORE INTO iam_scopes …`.
Seeding is idempotent and safe to re-run. Because an absent anchor already reads
as revision 0 in the mutation contract, seeding is a *materialization* convenience
(enumerable, lockable rows) rather than a correctness requirement — but seed it so
audits see the full scope set.

`iam_mutations` is **not** seeded: pre-v3 writes have no MutationID, so there are
no receipts to backfill. It begins empty and fills as v3 commands apply.

## Destructive path for example / dev hosts

An example or dev host with disposable data does **not** run any of the above.
It drops the old `iam_*` tables (and their `schema_migrations` rows for source
`authorization`) and applies the canonical v3 set fresh. AZ3-2.6 documents that
destructive reset path alongside the data-preserving adopter path above.
