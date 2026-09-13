# GPS-360-Go — adopt upstream follow-up patches

Use this prompt in `/Users/jrazmi/code/gps/three-sixty/gps-360-go`. Publication
status and exact source/checksums are recorded in the adjacent
`gps360-upstream-release-manifest.json`; only use versions marked published.

---

Continue the Gopernicus upgrade on the existing consumer branch, preserving all
current changes. Read `AGENTS.md`, the active plan, and
`workshop/migrations/UPGRADE-gopernicus-audit-2026-09.md` first. The large audit
upgrade was already completed; this task adopts the two upstream fixes requested
by that report, completes the durable PostgreSQL limiter wiring, and reruns the
affected verification. Inspect the current branch and migration ledgers before
editing. Do not reset the branch or redo the prior upgrade.

## Published target pins

| Module | Prior | Patch |
|---|---|---|
| `github.com/gopernicus/gopernicus/integrations/datastores/pgxdb` | v0.7.0 | **v0.7.1** |
| `github.com/gopernicus/gopernicus/pockets/authentication` | v0.11.0 | **v0.11.1** |
| `github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx` | v0.6.0 | **v0.6.1** |

The corresponding upstream sibling fixes are authentication/stores/turso
`v0.5.1` and authentication/stores/firestore `v0.1.1`. GPS-360-Go uses PostgreSQL;
do not add unused adapters. Keep the other audit pins unless another dependency
change is necessary and explained.

Resolve the three published modules normally, with no local `replace`, workspace
override or checksum exemption. Use the repository's Go/vendor workflow:

```sh
GOWORK=off go get \
  github.com/gopernicus/gopernicus/integrations/datastores/pgxdb@v0.7.1 \
  github.com/gopernicus/gopernicus/pockets/authentication@v0.11.1 \
  github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx@v0.6.1
GOWORK=off go mod tidy
GOWORK=off go mod vendor
```

Inspect the resulting module graph, `go.sum` and vendor diff; ensure the exact
patched core and adapter resolve and no local replacement remains.

## Credential ownership fix

The original request could promote another user's identifier via authenticated
DELETE `/auth/identifiers/{id}?replacement=...`, leaving that user's revision
unchanged. Password and delivery being disabled did not remove this route.

Core `v0.11.1` checks replacement ownership/eligibility before consuming a step-up
grant. Store `v0.6.1` repeats validation within the owning-user transaction and
locks the owned active identifier rows before updating. The same defense covers
direct retirement and identifier-use commands. A replacement must be distinct,
active, owned by the same user and of the same kind, replacing a primary target.
Foreign/missing replacements return 404 through the HTTP route; other invalid
replacement semantics return 400. No partial credential, revision or revocation
changes may occur. Same-user replacements and automatic same-kind selection
remain supported. Promotion does not grant verification or login/recovery uses;
contact-only unverified identifiers remain eligible.

No new authentication migration is required. Retain the already applied audit
migrations. Deploy patched core and store together, stopping old vulnerable
writers during rollout. This patch does not repair previously corrupted data;
if actual exploitation is suspected, report it and propose a host-specific audit
before any data repair. The reproducer established credential metadata mutation,
not a demonstrated account takeover.

Retain and rerun the full reproducer from the consumer report as a permanent
regression. Assert both users' identifiers and authentication revisions are
unchanged on rejection, along with the actor's sessions/grants/reset proof state.
Keep password and delivery disabled in that test. Add a valid same-user primary
replacement case so a passing rejection cannot merely mean the route stopped
working. Exercise the mounted HTTP stack over real PostgreSQL.

## Durable limiter on the existing pool

`pgxdb.WithLimiterSchema(schema)` now covers admission (`Allow`), reset and
startup probes. Use the existing unpinned `*pgxdb.DB` and the existing validated
`auth` schema value. Do not change pool `search_path`, add another datastore or
use a process-local production fallback.

The host owns this DDL. Add the next migration using GPS-360-Go's established
epoch naming and source/ledger conventions, after inspecting existing files.
The consumer reported no durable limiter table yet; confirm that precondition.
For a new table in the existing `auth` schema:

```sql
CREATE TABLE "auth".ratelimit_windows (
    key           TEXT        PRIMARY KEY,
    window_start  TIMESTAMPTZ NOT NULL,
    request_count BIGINT      NOT NULL,
    prev_count    BIGINT      NOT NULL,
    last_allowed  BOOLEAN     NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    window_ms     BIGINT      NOT NULL DEFAULT 0
);
CREATE INDEX ratelimit_windows_expires_at_idx
    ON "auth".ratelimit_windows (expires_at);
```

Use the host's migration role before serving. Runtime role needs schema `USAGE`
and table `SELECT`, `INSERT`, `UPDATE`, `DELETE`; the runtime never creates the
table. The index lands in the table's schema. If a table already exists, inspect
its shape before writing DDL: only a legacy table missing `window_ms` needs
`ALTER TABLE "auth".ratelimit_windows ADD COLUMN window_ms BIGINT NOT NULL DEFAULT 0;`.
Do not drop existing counters or silently recreate a table.

Wire at the host composition root, adapting existing names/imports:

```go
// Reuse the existing validated auth schema and shared pool when available.
schema, err := pgxdb.NewSchema("auth")
if err != nil {
    return err
}
limiter := pgxdb.NewLimiter(db, pgxdb.WithLimiterSchema(schema))
if err := limiter.StatusCheck(ctx); err != nil {
    return fmt.Errorf("authentication limiter startup: %w", err)
}
// Include in the existing authentication.New option list:
authentication.WithAbuseProtection(authentication.AbuseProtectionConfig{
    RateLimiter: limiter,
})
```

Preserve any existing abuse-policy settings in that coherent configuration record;
the option replaces the record, it does not merge fields.
Complete host lifecycle/production-mode checks as well as development tests.
The limiter borrows the pool; shutting it down must not close the shared database.

Keep the existing `ratelimit:v2:` key namespace unless the host already has a
deliberate prefix. Schedule host-owned pruning using qualified SQL and the host's
existing maintenance conventions:

```sql
DELETE FROM "auth".ratelimit_windows WHERE expires_at < now();
```

Schema selection does not transfer old windows. If an existing unqualified table
is discovered, coordinate all writers during cutover; parallel old/new schemas
would split quotas. Preserve or reset counters only through an explicit host
decision. Rollback may leave the new table in place; reverting schema selection
does not copy its counters back. Reverting the security patches reintroduces the
reported bug, so prefer a forward fix if deployment problems arise.

## Verification and report

Use owned disposable fixtures. Run repository formatting, builds, full tests,
vet, relevant race checks, guards and parity. Apply migrations on fresh and seeded
databases and verify ledger identity and no unintended schema/table changes.
Exercise the exact HTTP reproducer and valid replacement on real PostgreSQL.
Verify production authentication accepts the durable limiter and startup fails
when the selected table/column is unavailable, even with a compatible decoy in
`public`. On one shared unpinned pool, use identical keys/prefixes in two schemas
and prove independent admission/reset quotas. Confirm host middleware and startup
actually use the selected limiter, including a limited HTTP request.

Update the active plan and consumer upgrade report with exact pins, migration
filename/ledger results, changed files and command outcomes. Remove the two
upstream blockers only after these checks pass with normal published modules.
Report any remaining manual deployment tasks separately. Do not deploy or mutate
production as part of this handoff.

Preserve the prior consumer result accurately: the earlier audit upgrade passed
builds, full tests, vet, race checks, guards, parity, fresh/seeded migrations,
storage emulators, 50 runtime checks and a manual browser check. Browser OAuth
used a synthetic provider. Live Google OAuth and real GCP remained unverified;
these upstream patches do not establish that coverage. Attribute prior results
to the recorded consumer run and distinguish them from the checks run now.
