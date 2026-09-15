# Turso connector: local file profile, env-tagged config and the SQLite driver package

Status: RATIFIED and IMPLEMENTED on branch `feat/turso-local-file-profile` 2026-09-15 (decisions 1–6 taken as recommended); not tagged, not released; host adoption not started. Owner accepted 2026-09-15: modernc.org/sqlite as the local driver, isolated in the opt-in `localfile` package; the connector's tags carry no `AUTH` prefix (the host supplies the namespace). Origin: the Segovia v2 and
GPS-360-Go auth-storage program (workspace plan
`/Users/jrazmi/code/.claude/plans/segovia-gps360-auth-storage-cache-coordination.md`),
whose two hosts each carry a byte-identical `pockets/auth/outbound/authdb.go` because
the connector cannot yet do what that file does. Owner: "we should make these a
standard... and maybe even in gopernicus." This plan moves the standard into the
connector. Planning only; implementation, release and host adoption follow separate
"build it" rulings.

## Why

Both hosts open their auth database the same way: parse `AUTH_DATABASE_*` from the
environment into `turso.Config`, and for a `file:` URL append `_pragma=busy_timeout(…)`,
`_pragma=foreign_keys(1)` and `_pragma=journal_mode(WAL)` so every pooled connection
gets them, create the parent directory, and blank-import `modernc.org/sqlite` because
the pinned libsql client dispatches `file:` URLs only to a registered `sqlite`/`sqlite3`
driver. None of that is host knowledge. The connector already documents the busy-timeout
fixture and already requires `modernc.org/sqlite v1.52.0` (test-only today).

## Current state (verified 2026-09-15)

- Connector at `v0.5.1` (main `49e7c27a`, release commit `5aa0b29e`). `Config` has no
  env tags; `dsn()` (`turso.go:63-80`) already parses the URL, rewrites the query and
  re-encodes it; `Open` (`turso.go:92`) has no `file:` awareness.
- `go.mod` requires `modernc.org/sqlite v1.52.0`, imported only by
  `migrate_internal_test.go` and store tests. libsql-client-go
  (`v0.0.0-20260528064733-9d5d30a29a60`, `libsql/sql.go:119-134`) resolves a `file:`
  URL by `sql.Open("sqlite"|"sqlite3", …)` and otherwise errors
  "no sqlite driver present".
- Env-tag precedent: `pgxdb.Config` uses `DB_URL`, `DB_MAX_CONNS`,
  `DB_MAX_CONN_LIFETIME`, `DB_CONNECT_TIMEOUT`, `DB_LOG_QUERIES`; `goredis.Config` uses
  `REDIS_*`. `environment.ParseEnvTags(namespace, &cfg)` prefixes every key
  `<namespace>_<key>`; the hosts already read Redis as `AUTH_CACHE_REDIS_*` that way.
- Framework tests that open `file:` URLs without their own `_pragma`: two
  (`integrations/datastores/turso/open_context_test.go`,
  `pockets/authorization/stores/turso/read_snapshot_test.go`); they would receive the
  policy and must still pass.
- Guard: `TestIntegrationBoundaries` (workshop/gopernicus) forbids peer-integration,
  pocket, UI and host imports/requirements; third-party imports are allowed. A driver
  sub-package inside the connector module passes it.

## Design

### 1. Env tags on `turso.Config` (additive)

| Field | Tag | Note |
|---|---|---|
| `URL` | `DB_URL` | required by `Open` as today |
| `AuthToken` | `DB_AUTH_TOKEN` | hosted token; overrides URL credentials as today |
| `MaxOpenConns` | `DB_MAX_CONNS` | zero keeps `database/sql`'s unlimited default, as today |
| `MaxIdleConns` | `DB_MAX_IDLE_CONNS` | |
| `ConnMaxLifetime` | `DB_MAX_CONN_LIFETIME` | |
| `ConnectTimeout` | `DB_CONNECT_TIMEOUT` | 10s when zero, as today |
| `LogQueries` | `DB_LOG_QUERIES` `default:"false"` | matches pgxdb |
| `BusyTimeout` (new) | `DB_BUSY_TIMEOUT` | `file:` URLs only; 5s when zero |

Same names as `pgxdb.Config` where the concept matches, so a Turso-primary host reads
`DB_URL` exactly as a PostgreSQL host does, and a host using Turso for its auth database
reads `AUTH_DB_URL` with `ParseEnvTags("AUTH", &cfg)`, the same namespace mechanism
that already yields `AUTH_CACHE_REDIS_*`. No existing caller parses `turso.Config` from
the environment today (no tags), so this is backward compatible.

### 2. The local file profile in `Open`

For a URL whose scheme is `file` (`dsn()` already has the parsed URL):

- Create the parent directory of the path (opaque or path form) if missing.
- If the query carries no `_pragma`, append, in this order:
  `busy_timeout(<BusyTimeout ms>)`, `foreign_keys(1)`, `journal_mode(WAL)`. modernc
  applies `_pragma=` parameters on every new pooled connection, which is the only way
  to satisfy the per-connection requirement the release fixture relies on.
- A URL that already names a `_pragma` is an explicit opt-out and is left untouched.
- Non-`file:` URLs are unchanged.
- If libsql reports "no sqlite driver present", wrap it:
  `turso: file: URLs need a registered sqlite driver; import
  github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile`.

### 3. The driver package `integrations/datastores/turso/localfile`

Same module, one file: a package comment and `import _ "modernc.org/sqlite"`. Hosts
that use `file:` URLs blank-import it where they open the database (H7 allows the
import in `cmd/**`, `internal/outbound/**`, host `pockets/*/outbound/**`,
`workshop/**`). Hosted-only hosts never import it and never link modernc. The module
graph does not change (`modernc.org/sqlite` is already required). ARCHITECTURE.md's
"one external dependency each" line gets a one-sentence footnote for this connector:
libsql's local mode delegates to a registered SQLite driver by design.

### 4. Tests

- `config_test.go`: `ParseEnvTags("AUTH", &cfg)` populates `AUTH_DB_URL`,
  `AUTH_DB_AUTH_TOKEN`, `AUTH_DB_MAX_CONNS`, `AUTH_DB_BUSY_TIMEOUT`; namespace-free
  parsing populates `DB_URL`.
- DSN policy table test: relative `file:` path stays relative; absolute path; parent
  directory created; own `_pragma` untouched; `libsql://` untouched; zero
  `BusyTimeout` yields 5000ms, never 0.
- `localfile` package test: the host standard's proof, moved here — four concurrent
  pinned connections (`BeginRead`) each report `journal_mode=wal`, `foreign_keys=1`,
  `busy_timeout=5000`; a second `Open` of the same file works.
- No-driver error test in a test package that does not import modernc (the main
  package's tests already register it), asserting the wrapped message.
- Existing suites: `cd integrations/datastores/turso && go test ./...`, every
  `pockets/*/stores/turso` suite (`-tags=integration` where applicable), and
  `make guard` at the root, including `TestIntegrationBoundaries`.

### 5. Documentation and release

- README: a "Local file profile" section with the canonical host snippet (below), the
  opt-out rule, the driver package, and the per-connection rationale.
- `AUDIT.md`: `AUDIT-036: Turso local file profile and env-tagged config` in the
  AUDIT-035 format (Implemented / Modules / Impact, then behavior, then "records
  implemented behavior, not release approval").
- `RELEASING.md`: "Unreleased: Turso local file profile" section; on publication a
  release plan + manifest per the existing pattern. Version: connector `v0.6.0` (minor:
  additive fields plus a behavior change for `file:` URLs without `_pragma`). No other
  module changes.

Canonical host snippet (replaces both hosts' `authdb.go`):

```go
import (
    tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
    _ "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile" // file: URLs locally
    "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func openAuthDatabase(ctx context.Context) (*tursodb.DB, error) {
    var cfg tursodb.Config
    if err := environment.ParseEnvTags("AUTH", &cfg); err != nil {
        return nil, err
    }
    return tursodb.Open(ctx, cfg) // AUTH_DB_URL, AUTH_DB_AUTH_TOKEN, AUTH_DB_MAX_CONNS, AUTH_DB_BUSY_TIMEOUT
}
```

### 6. Host adoption (separate per-app plans, after the release is public)

Bump the connector to `v0.6.0`; delete `pockets/auth/outbound/authdb.go` and its test
(the proof now lives in the framework); open through the snippet; rename the host
fields `AUTH_DATABASE_URL` → `AUTH_DB_URL`, `AUTH_DATABASE_AUTH_TOKEN` →
`AUTH_DB_AUTH_TOKEN`, `AUTH_DATABASE_MAX_OPEN_CONNS` → `AUTH_DB_MAX_CONNS`,
`AUTH_DATABASE_BUSY_TIMEOUT` → `AUTH_DB_BUSY_TIMEOUT` in `.env.example`, devstack /
compose env, instructions and the coordination doc; test harnesses build a
`tursodb.Config` literal and call `Open` (no environment). Developers rename the keys
in their own `.env` files (workers never edit `.env`). `.env.example` keeps
`AUTH_DB_MAX_CONNS=4` for the file profile.

## Decisions for the owner

1. **YOUR CALL:** env tag names `DB_*` on `turso.Config` with the `AUTH` namespace
   (recommended: matches pgxdb and the existing `AUTH_CACHE_REDIS_*` pattern; costs a
   one-time host rename) versus keeping the hosts' `AUTH_DATABASE_*` names by tagging
   the connector `DATABASE_*` (no rename, but inconsistent with pgxdb).
2. **YOUR CALL:** `Open` creates the parent directory for `file:` paths (recommended;
   a hosted URL never reaches it) versus failing with a clear error.
3. **YOUR CALL:** opt-out of the pragma policy by naming any `_pragma` in the URL
   (recommended; explicit and needs no new field) versus a `Config` flag.
4. **YOUR CALL:** package name `localfile` for the driver import.
5. **YOUR CALL:** no default for `MaxOpenConns` (recommended; hosts set
   `AUTH_DB_MAX_CONNS=4`) versus a `file:`-mode default.
6. **YOUR CALL:** release as connector `v0.6.0` on its own, hosts adopt afterwards.

## Adjacent connector gaps, not in this plan

- `RunMigrations` has one internal source; hosts sequence three calls and rely on
  filename uniqueness. A `WithMigrationSource(name)` option is its own change.
- `StatusCheck` is a bare `SELECT 1` and cannot detect a mid-run fault on an open local
  file; a table-reading probe option would let host health checks catch it.
- `RelationshipWriter` has no multi-relation `SetRelationTargets`; hosts wrap two calls
  in one transaction (savepoints work, undocumented).
- `transaction.Transactor` has no post-commit hook.

## Verification for the implementation phase

```sh
cd integrations/datastores/turso && go build ./... && go vet ./... && go test ./...
cd pockets/authorization/stores/turso && go test -tags=integration ./...
cd pockets/authentication/stores/turso && go test -tags=integration ./...
make guard   # repo root; includes TestIntegrationBoundaries
```
Then the isolated versioned-consumer build/test/vet per RELEASING.md before tagging.
