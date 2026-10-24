# Host handoff — retention and erasure (SOC 2 slice S3, released 2026-10-23)

**Status:** OPEN. **The handoff is written now and applied at the roadmap's end.** Maintenance
rule (owner, 2026-10-23): host handoffs are written and updated per slice but are NOT applied
to the hosts until the SOC 2 roadmap is complete; the hosts adopt once, at the end. Release
record: `RELEASING.md` "Retention and erasure". Plan of record: `plans/s3-retention-erasure.md`.
Manifest: `plans/s3-retention-erasure-release-manifest.json`. This handoff builds on the S1
handoff (`plans/soc2-s1-production-posture-handoff.md`) and the S2 handoff
(`plans/soc2-s2-audit-completeness-handoff.md`), both still open in all three hosts: S3 adds one
schema step, one repin step with the retention wiring, and moves the workshop pin.

cms admin paging/delete: separate plan, not yet ratified.

## The versions, in repin order

Apply the S2 handoff's steps 1–3 first, as written, each a deploy of its own, until production
boots clean on authentication v0.22.0 and authorization v0.29.1. Skip its step 4 (workshop
v0.6.1): step C below supersedes it. Then, with `go mod tidy` at `GOWORK=off` after each `go get`
(and `go mod vendor` in a host that vendors):

- **A. The S3 schema — migrate, no binary change.** Pre-build the indexes on a large PostgreSQL
  host ("Index pre-builds", below), export and apply the S3 files in their own runner invocation
  ("The migration files", below) and apply the role lines ("The roles", below). The S2 binaries
  keep running on the migrated database.
- **B. The S3 repin and the retention wiring — one deploy.** `integrations/datastores/pgxdb`
  **v0.12.0** → `pockets/authentication` **v0.23.0** + `stores/pgx` **v0.11.0** / `stores/turso`
  **v0.10.0** → `pockets/authorization` **v0.30.0** + `stores/pgx` **v0.21.0** / `stores/turso`
  **v0.20.0** (`stores/goredis` unchanged) → `pockets/jobs` **v0.7.0** + `stores/pgx` **v0.7.0** /
  `stores/turso` **v0.6.0** where mounted → `pockets/cms` **v0.5.0** + `stores/pgx` **v0.5.0** /
  `stores/turso` **v0.4.0** where mounted. `go get` every store you pin directly, by name: `go mod
  tidy` does not raise a direct pin. Before the server rolls, preflight with `-posture-role` from
  this step's build (only authentication `stores/pgx` v0.11.0's `AuditTables` lists the purge
  function, so a step-A build prints no `functions=` line). Wire retention in the same deploy
  ("Wiring retention", below); without it production logs one `maintenance_runner=unacknowledged`
  WARN.
- **C.** `workshop/gopernicus` **v0.7.0** for `gopernicus guard --posture` (P5 recognizes
  `MaintenanceRunnerAcknowledged` and prints its `ack`) and the runner template's
  `functions=`/`floor=` evidence.

`pockets/secrets`, `pockets/events`, `sdk` and the turso connector do not move in S3. pgxdb
v0.12.0 is pulled by authentication `stores/pgx` v0.11.0; the other stores keep their own
connector pins, and the release cold-built every released pgx store on v0.12.0.

## Rolling deploys and rollback

- Step A before step B, always: authentication `stores/pgx` v0.11.0 / `stores/turso` v0.10.0
  refuse, in every mode, a database without `0023` (the `users.erased_at` column) or `0024` (the
  pgx functions, the turso floor row), naming the file — zero requests served.
- The S3 schema is admitted by the previous binaries (a nullable column, indexes, functions they
  never call), so step A runs under the S2 binaries, a rolling deploy of step B is safe, and a
  binary rollback after step B is safe: an older binary reads an erased user as `deactivated`,
  which every credential path refuses.
- Schema rollback is forward-fix only: there are no down files, and dropping `erased_at` after
  an erase loses the marker. Author any reverse as a new forward ledger entry.

## The migration files to export, in order and alone

Copy each file byte-for-byte from the store module's `migrations/` directory (`go mod download
-json <module>@<version>` prints the module's `Dir`) as the next number of the ledger that holds
that store's tables. Never renumber or edit an applied file; `guard --posture` (P9) checks the
copies.

| store | files, in order | status | hosts |
|---|---|---|---|
| `pockets/authentication/stores/pgx` or `/turso` | `0023_retention.sql`, then `0024_security_events_retention.sql` | REQUIRED from v0.11.0 / v0.10.0 | all three |
| `pockets/authorization/stores/pgx` or `/turso` | `0007_iam_operations_expiry.sql` | admitted; the pgx `Repositories` WARNs `index_missing=idx_iam_operations_committed` without it | all three |
| `pockets/jobs/stores/pgx` or `/turso` | `0005_job_queue_terminal_purge.sql` | admitted; the pgx `StatusCheck` WARNs `index_missing=idx_job_queue_terminal` | hosts mounting jobs |
| `pockets/cms/stores/pgx` or `/turso` | `0022_inquiries_created_at.sql` | admitted; the pgx `StatusCheck` WARNs `index_missing=idx_inquiries_created_at` | hosts mounting cms (none of the three today) |

**Apply them alone**: one runner invocation with nothing else pending. The runner applies every
pending file in one transaction, every S3 PostgreSQL file begins `SET LOCAL lock_timeout =
'5s'`, and `lock_timeout` bounds only the wait to acquire a lock — never how long an index build
holds one. `0023` builds its indexes first and adds `users.erased_at` last, so `users`' ACCESS
EXCLUSIVE lock is held for one metadata-only statement. `0024` refuses to apply unless the
runner's schema is the one holding `security_events` and the migrator is a member of that
table's owner — the fix is in its error. The reference host's ledger is the worked example:
`examples/production-baseline/workshop/migrations/primary` carries
`0029_iam_operations_expiry.sql`, `0030_retention.sql` and `0031_security_events_retention.sql`.

## Index pre-builds (PostgreSQL, large tables)

A plain `CREATE INDEX` blocks writes to its table while it builds. On any table large enough
that a blocked minute matters (sessions, the OAuth2 tables, `iam_operations`, `job_queue`), build
each index first, outside the runner, under the SAME name and the SAME definition — `IF NOT
EXISTS` compares names only — so the file's statement is a no-op. `CONCURRENTLY` cannot run in a
transaction block: issue each statement on its own (psql autocommit). Prefix the table with the
stores' schema when they are mounted `WithSchema`.

```sql
-- authentication 0023
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_authentication_grants_expires_at ON authentication_grants (expires_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_contact_changes_expires_at ON contact_changes (expires_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_oauth_states_expires_at ON oauth_states (expires_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_challenges_expires_at ON challenges (expires_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_oauth_authorization_codes_expires_at ON oauth_authorization_codes (expires_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_oauth_clients_expires_at ON oauth_clients (expires_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_oauth_refresh_history_expires_at ON oauth_refresh_history (expires_at);
-- authorization 0007
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_iam_operations_committed ON iam_operations (committed_at, operation_id);
-- jobs 0005 (hosts mounting jobs)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_job_queue_terminal ON job_queue (updated_at, job_id) WHERE status IN ('completed','dead_letter');
-- cms 0022 (hosts mounting cms)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_inquiries_created_at ON inquiries (created_at);

-- then: every row here is an INVALID leftover of a failed concurrent build
SELECT c.relname FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE NOT i.indisvalid;
```

A failed concurrent build leaves an INVALID index that `IF NOT EXISTS` keeps: `DROP INDEX
CONCURRENTLY <name>;`, build it again, and re-run the check until it returns no row before
applying the files. Turso has no concurrent build: the turso `0023` (six indexes — `0019` already
indexes the OAuth2 codes and refresh history), `0007`, `0005` and `0022` build under the
database's write lock; apply them in a quiet window.

## The roles: three lines the two-role recipe gains

On top of the S2 handoff's two-role recipe, as the migrator, after step A's files (adjust
`public` to the stores' schema):

```sql
-- the rail's only purge path: 0024 revokes EXECUTE from PUBLIC and cannot know your runtime role
GRANT EXECUTE ON FUNCTION security_events_purge_before(pg_catalog.timestamptz, pg_catalog.int4) TO runtime;
-- no role but the owner creates objects in the stores' schema
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
-- the migrator must not be a superuser: this returns false
SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user;
```

The migrator is `NOSUPERUSER` and a member of the role that owns `security_events` (*Production
database posture*, "Two roles"). `DELETE` stays granted on `iam_operations` (S2): the expiry
needs it, and its trigger still refuses a fenced row. What the server then meets:

| state | production boot | the maintenance pass |
|---|---|---|
| EXECUTE granted, function as 0024 left it | boots clean | the rail step archives and purges daily |
| EXECUTE not granted | one `maintenance_execute=missing … fix=GRANT EXECUTE …` WARN | `step=security_events error_kind=forbidden` every pass; every other step runs |
| PUBLIC can execute it (a hand `DROP` + `CREATE` re-grants it), or its `search_path` is not the pinned value, or it resolves outside the rail's schema | refused: `pgxdb.ErrDefinerFunctionUnsafe`, not waivable | — |
| not SECURITY DEFINER, owner not a member of the table's owner, a superuser owner, or the runtime role can CREATE in the schema | one `definer_function=inoperative` WARN naming the fix | the rail step fails at call time |
| the function or floor missing (0024 not applied) | refused in every mode, naming `0024_security_events_retention.sql` | — |

**Preflight with `-posture-role` and archive the output.** From the migrator's connection and
the step-B build, `go run ./workshop/migrations -posture-role <runtime role>` (the runner
template of workshop v0.7.0; a host-written runner copies `postureTables` and `reportPosture`
from `examples/production-baseline/workshop/migrations/main.go`). Beside S2's `table=…
protected_by=…` lines it now prints, for `security_events`:

```text
functions=[security_events_purge_before:execute=true unsafe=none inoperative=none] floor=365 days
```

It exits non-zero on a missing or unsafe function (the boot refusals) and reports a missing
`EXECUTE` or an inoperative function as the boot WARNs. After a `pg_restore -x`, revoke `EXECUTE`
from `PUBLIC` and grant it to the runtime role again; after a restore into a renamed schema,
re-run 0024's pin `DO` block (*Operations runbooks*, "Retention").

## Wiring retention (step B)

The pockets ship bounded disposal calls and start none of them. Per host, the calls and their
schedule (*Operations runbooks*, "Retention", is the table of record):

- **Authentication.** A host that passes the store's `Repositories(...)` gets
  `Repositories.Maintenance` and `Repositories.UserErasure` filled; a host that assembles
  `authentication.Repositories` by field sets `Maintenance` from the store's
  `NewMaintenanceStore` (and `UserErasure` from `NewErasureStore` only beside `ActiveSessions`).
  In `cmd/`:
  `authentication.WithMaintenance(authentication.MaintenanceConfig{Config: maintenance.Config{ArchiveSink: <sink>, ArchiveLocation: "<label>"}, MaintenanceRunnerAcknowledged: true})`
  — a literal, never an env knob; the defaults (sessions 24 h past expiry, OAuth states 1 h,
  grants and contact changes 24 h, invitations 90 days, the rail 400 days with a 365-day floor)
  are Go values a host raises. Then ONE of the two runner forms:
  - **(a) In process.** Copy `examples/production-baseline/pockets/retention/inbound/runner`
    (and its test) into a host pocket at `pockets/retention/inbound/runner` — never `cmd/` (H6,
    H10). It runs `Components.Maintenance.Run(ctx)` (a first pass within a tenth of an hour, then
    hourly; the rail step daily) and an hourly `Components.Mutations.ExpireOperations` drain, and
    its `Health()` feeds `/healthz`. Run it on ONE replica: concurrent passes are safe, not cheap,
    and a disk sink is per instance. `Run` has no interval option on the root; for another
    cadence call `Pass` from the host's own ticker.
  - **(b) A jobs schedule.** The three-line `queue.HandlerFunc` over `Components.Maintenance.Pass`
    with `schedules.Ensure{Name: "authentication-maintenance", Kind: "authentication.maintenance",
    Spec: schedules.Spec{Every: time.Hour}}` (`pockets/authentication/README.md`, "Retention and
    the maintenance service"). The schedule enqueues one job per tick, so one worker runs each
    pass whatever the replica count; keep `PassBudget` (5 min) shorter than the jobs lease
    window. This form is documented, not exercised by an example.
- **Authorization — `OperationRetention` versus the oldest accepting claim.** The bundled stores
  fill `Repositories.OperationExpiry`; set `authorization.WithOperationRetention` (default 180
  days; below 7 days refused) and call `Components.Mutations.ExpireOperations(ctx, 500)` hourly
  until it returns less than 500 (form (a) does). An expired operation id reads as never
  recorded, so a retried command applies again: the retention must exceed every producer's retry
  window and the age of the oldest open `accepting` invitation claim, with authentication's
  `InvitationRetention` (90 days) at or below half of it. Before choosing, read the claims:

  ```sql
  SELECT count(*) AS accepting, min(updated_at) AS oldest FROM invitations WHERE status = 'accepting';
  ```

  Settle or cancel any old claim through the host's grant path first — maintenance never cancels
  one, and the pass WARNs `invitations_accepting_stale` (at most daily) while claims older than
  `InvitationRetention` remain. A held claim also blocks erasing its user (409).
- **Jobs** (hosts mounting it): `Components.Queue.PurgeTerminalJobs(ctx, now − <retention>, 500)`
  on a schedule for the ordinary queue, never with a retention below the scheduler's longest retry
  window (a purged job frees its id for a late refire); `Queue.PurgeTerminal` keeps purging the
  fenced queue as before. Payloads are stored as submitted and `failure_reason` is the handler's
  error text: handlers must not put payload data in errors.
- **Events** (hosts mounting it): `outbox.PurgePublished(ctx, before)` is unbounded, so it runs as
  its own step, and only after a one-off manual drain of the backlog, as the runtime role, in
  batches — repeat until it deletes no row:

  ```sql
  DELETE FROM event_outbox WHERE event_id IN (
    SELECT event_id FROM event_outbox
     WHERE published_at IS NOT NULL AND created_at < now() - interval '<retention>'
     ORDER BY created_at LIMIT 10000);
  ```
- **`ratelimit_windows`** (hosts on the pgxdb limiter): the host cron `DELETE FROM ratelimit_windows
  WHERE expires_at < now();` (pgxdb README).
- **The erase route** is an opt-in, not a default: `AdministrationConfig.UserErasureRoute: true`
  mounts `POST /auth/admin/users/{id}/erase` and needs a `UserAdminCheck`, which receives
  `UserAdminErase` — require step-up there if the host wants it. Do not opt in while the host's
  check ignores its `Action`. Without the route, `EraseUser` is the service call.

## The archive sink and its lifecycle

- In production back `ArchiveSink` with an object store: `integrations/filestorage/gcs` or
  `integrations/filestorage/s3` (any `filestorage.Storer` is a `maintenance.ArchiveSink`). A disk
  sink is per instance and does not fsync; on ephemeral storage it is not an archive. Objects land
  at `security-events/<cutoff>/<id>.ndjson` (`ArchivePrefix`), one per chunk of up to 5000 rows,
  read back (size, lines, SHA-256) and evidenced by a `security_events_archived` row before the
  chunk is purged; a key is never overwritten. `ArchiveLocation` is the label every archived row
  carries.
- With no sink the rail is purged unarchived and the registration line says `archive_sink=none`:
  a valid policy, if it is the host's written one.
- The lifecycle is host policy — write it in the host's posture doc: the objects' retention,
  justified on the host's legal basis (archived rows are outside erasure's reach); backup;
  access control (the objects hold IP addresses and User-Agents — restrict them to the people who
  audit); an object-lock or bucket retention policy on a real sink; and reconciliation of an
  object whose chunk was not purged (a failure after the upload leaves it; the next pass writes a
  fresh object under a new key).
- **Verify the archive** on a schedule (monthly is a reasonable start): *Operations runbooks*,
  "Retention" — fetch each object an archived row names, check its digest and line count, compare
  rows archived with rows purged.

## The first week — the backlog

The first enablement on an old host is a sustained DELETE load. Size it before step B, from a
read-only connection (PostgreSQL; the cutoffs are the defaults):

```sql
SELECT 'sessions' AS rows, count(*) FROM sessions WHERE expires_at <= now() - interval '24 hours'
UNION ALL SELECT 'oauth_states', count(*) FROM oauth_states WHERE expires_at <= now() - interval '1 hour'
UNION ALL SELECT 'authentication_grants', count(*) FROM authentication_grants WHERE expires_at <= now() - interval '24 hours'
UNION ALL SELECT 'contact_changes', count(*) FROM contact_changes WHERE expires_at <= now() - interval '24 hours'
UNION ALL SELECT 'invitations', count(*) FROM invitations
  WHERE (status IN ('accepted', 'declined', 'cancelled') AND updated_at <= now() - interval '90 days')
     OR (status = 'pending' AND expires_at <= now() - interval '90 days')
UNION ALL SELECT 'security_events', count(*) FROM security_events WHERE created_at <= now() - interval '400 days'
UNION ALL SELECT 'iam_operations', count(*) FROM iam_operations
  WHERE outcome IN ('applied', 'no_change', 'not_found') AND committed_at <= now() - interval '180 days';
```

- Each pass drains `BatchSize` (1000) rows per statement until a short batch or until
  `PassBudget` (5 min) is spent, checked between batches — never a cancelled statement. A backlog
  larger than one budget drains over many passes: `Report.BudgetExhausted` is set and
  `maintenance_budget_exhausted` WARNs while it persists. That is expected in the first week and
  a page after it.
- The rail step runs on the first pass after each start, then daily, archiving and purging the
  rows older than 400 days in 5000-row chunks.
- Watch WAL volume, replica lag, backup size and autovacuum on the drained tables (a mass delete
  leaves dead tuples; a manual `VACUUM (ANALYZE)` after the first drain is reasonable). If the load
  is too much, lower `BatchSize` or `PassBudget` rather than stopping the runner. Never drain the
  rail by hand: the function is its only path, and only the service archives first.
- Turso: each batch holds the database's one write lock and logins wait behind it (~14 ms per
  1000-row batch measured locally; a remote database adds round trips) — size `BatchSize` from
  your own measurement and pause between passes if logins queue.
- Tell support before it runs: pending invitations past expiry and settled invitations older than
  90 days disappear on the first pass.

## Dashboards and the 48 h alert

- **The canonical alert — page on it: no `security_events_purged` row in 48 h** (the runner is
  stopped or the rail step fails every pass). Run it from the monitor as the owner or a read-only
  role:

  ```sh
  psql -Atc "SELECT coalesce(max(created_at) < now() - interval '48 hours', true) AS retention_stalled
             FROM security_events WHERE event_type = 'security_events_purged'"
  ```
- Panels: per day, `security_events_purged` rows and the sum of their `count`, and
  `security_events_archived` rows and the sum of their `rows`; the `msg=maintenance_step` lines by
  `step` and `error_kind`; with form (a), the `/healthz` detail `maintenance_last_pass`,
  `maintenance_steps_failed` and `maintenance_last_error_kind` (`none`, `forbidden`,
  `below_floor`, `archive_failed`, `budget_exhausted`, `other` — never error text; the status code
  never changes for a maintenance failure).
- Operator actions an auditor samples: `user_erased` (counts, the admin as actor) and
  `service_account_deleted` rows.
- WARNs worth a ticket: `maintenance_budget_exhausted` after the first week,
  `invitations_accepting_stale`, `archive_purge_count_mismatch`, `maintenance_execute=missing`,
  `definer_function=inoperative`, `index_missing=` at boot (step A was skipped) and, on turso, the
  ERROR `rail_triggers_changed`.
- User-activity dashboards filter `actor_type='system'`: the maintenance rows carry
  `actor_id='maintenance'` and a blank `user_id`.

## Erasure and the sweep

Erasure composes across pockets; *Operations runbooks*, "Erase a person", is the procedure:
settle any accepting claim → `POST /auth/admin/users/{id}/erase` or `EraseUser` → remove the
person's authorization facts for `user:<id>` through the role routes or `Mutations` (an executed
step) → cms `DeleteByEmail` where cms is mounted → `PurgeTerminal` for sealed delivery rows. It
is not a ban: the next external login — the person's still-valid provider token included —
provisions a fresh, empty user; deactivate, or remove the person at the provider, to keep them
out. The database keeps `status='deactivated'` with `erased_at` set; the API says `erased`, so a
client validating a closed two-value status enum breaks.

Then sweep, as the migrator — every table's text and JSON columns, counting rows that contain the
person's email or last IP:

```sql
DO $sweep$
DECLARE
  needles pg_catalog.text[] := ARRAY['<email>', '<last ip>'];
  needle pg_catalog.text;
  col record;
  n pg_catalog.int8;
BEGIN
  FOR col IN
    SELECT c.table_schema, c.table_name, c.column_name
      FROM information_schema.columns c
      JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
     WHERE t.table_type = 'BASE TABLE'
       AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
       AND c.data_type IN ('text', 'character varying', 'json', 'jsonb')
  LOOP
    FOREACH needle IN ARRAY needles LOOP
      EXECUTE pg_catalog.format('SELECT count(*) FROM %I.%I WHERE %I::text ILIKE %L',
        col.table_schema, col.table_name, col.column_name, '%' || needle || '%') INTO n;
      IF n > 0 THEN
        RAISE NOTICE '%.%.%: % rows contain %', col.table_schema, col.table_name, col.column_name, n, needle;
      END IF;
    END LOOP;
  END LOOP;
END
$sweep$;
```

Expect no notice except on `security_events`, whose rows within retention keep the IP (and, on
rows older than AUDIT-054, the raw address) — R-7, by design. A host table that names the person
is the host's to clear; archive the sweep's output with the erasure record.

## gps-360-go

- **Today:** as in the S2 handoff ("gps-360-go", "Today"); re-read `go.mod` before starting. It
  vendors: `go mod vendor` after each tidy.
- **Step A:** export authentication `0023` → `0024` and authorization `0007` into the ledger that
  holds those tables, and jobs `0005` into the jobs stores' ledger; pre-build the indexes if the
  session or job tables are large; `GRANT EXECUTE` to the server role named in the S2 handoff
  (______, owner fills in). No turso store, no secrets, no cms.
- **Step B:** pgxdb v0.12.0, authentication v0.23.0 with `stores/pgx` v0.11.0, authorization
  v0.30.0 with `stores/pgx` v0.21.0 (`stores/goredis` unchanged), jobs v0.7.0 with `stores/pgx`
  v0.7.0; `-posture-role` from this build before the server rolls; archive the output.
- **Runner form (owner fills in):** ______ — (a) in process on one replica, or (b) a jobs
  schedule (gps-360-go mounts jobs). `PurgeTerminalJobs` on the host's schedule either way.
- **Archive sink (owner fills in):** ______ (gps-360-go runs on Cloud SQL; on GCP
  `integrations/filestorage/gcs` is the natural sink) and its lifecycle in the host posture doc.
- **Retention values (owner fills in):** `OperationRetention` ______ (default 180 days; read the
  accepting claims first), jobs retention ______.
- **Erase route (owner fills in):** ______ (opt in, or `EraseUser` only).

## Segovia (v2)

- **Today:** as in the S2 handoff ("Segovia (v2)", "Today"); re-read `go.mod` before starting and
  work from a branch or worktree off `main`.
- **Step A:** authentication `0023` → `0024`, authorization `0007`, jobs `0005`, each into the
  ledger that holds its tables (the events store has no S3 file); pre-builds as needed; `GRANT
  EXECUTE` to the server role (______, owner fills in).
- **Step B:** pgxdb v0.12.0, authentication v0.23.0 with `stores/pgx` v0.11.0, authorization
  v0.30.0 with `stores/pgx` v0.21.0 (`stores/goredis` unchanged), jobs v0.7.0 with `stores/pgx`
  v0.7.0; events unchanged; `-posture-role` from this build before the server rolls; archive the
  output.
- **Events:** the one-off `event_outbox` drain above, then `outbox.PurgePublished` as its own
  scheduled step.
- **Runner form, archive sink, retention values, erase route (owner fills in):** ______.

## coordination-hub

- **Today:** unknown from here — read the hub's current `go.mod` and confirm its branch and HEAD
  before starting; another session may be working in that repo.
- **Step A / B:** the same steps for whichever stores the hub mounts. If it mounts the turso
  stores: no pre-build exists, the S3 files build their indexes under the write lock (a quiet
  window), the floor is a footgun guard rather than a control, and each rail purge batch holds the
  write lock (the "first week" turso note).
- **Runner form, archive sink, retention values, erase route (owner fills in):** ______.

## After the repin

Run `guard --posture` (workshop v0.7.0) in CI and require a zero exit; it prints one `ack` line
for `MaintenanceRunnerAcknowledged`. Archive the `-posture-role` output — now with its
`functions=` and `floor=` line — with every deploy. Record in the host's own posture doc: the
runner form and the replica it runs on, the archive sink and its lifecycle, every retention value
the host raised, and the erase-route decision. Fire the 48 h alert once on purpose (stop the
runner in a staging copy) so the page is known to work, and run "Verify the archive" on its
schedule.
