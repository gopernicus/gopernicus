# workshop/gopernicus — the scaffolding CLI

The developer-time tool that EMITS gopernicus anatomies and never links
them: **stdlib-only** (`flag` + `embed` + `text/template` +
`os/exec`; the go.mod has zero require lines, structurally), isolated
both directions by guard G11 (nothing imports `workshop/`; workshop
imports no pocket core and no example). Milestone of record:
`workshop-v2-scaffolding` (2026-07-09), the scaffold-once slice of the
workshop-v2 scope brief (`.claude/plans/restructure/workshop-v2-brief.md`).

```sh
go install github.com/gopernicus/gopernicus/workshop/gopernicus@v0.3.0
gopernicus <command>
# To exercise this checkout: go run . <command>
```

## Commands

| command | what it emits / does |
|---|---|
| `init --module <path> [--db=turso\|pgx\|none] [dir]` | a host: `cmd/server` composition root (explicit wiring, mounts nothing, `/healthz`), host Makefile (build/vet/test/run/migrate + the one-rule layering grep + the G9/G10 hygiene shapes), `.env.example`, `workshop/migrations/primary/` ledger (+ the dialect runner with a `-status` mode when `--db` ≠ none), README with configuration and optional local framework development instructions |
| `new pocket <name> --module <path> [--aggregate <agg>]` | a standalone charter-conformant pocket: root constructor, public `logic/<agg>/` service and contracts, `stores/storetest/` with the six-case pagination family + `DBGeneratedIDOnEmpty`, public `stores/memory/`, and BOTH dialect store modules (boot probe, `ExportMigrations`, inline-id-DEFAULT migration, env-gated conformance). Monorepo-shaped targets get the manual registration checklist printed |
| `db create <slug> [--db=<ledger>]` | the next `NNNN_<slug>.sql` in the host ledger (4-digit max+1, never renumber) |
| `db migrate` | delegates to the HOST-OWNED runner (`go run ./workshop/migrations`) — the CLI carries no database driver, ever |
| `db status` | the runner's `-status` (applied vs pending), with a file-only all-pending fallback when the runner is absent or the DB unreachable |
| `version` | version + module path |

Templates take **identity parameters only** (module path, pocket name,
aggregate name). The moment a template would want a per-field loop or a
`queries.sql`, that is workshop-v2b's trigger firing — not a feature
request against this tool.

## How the templates stay honest

The scaffold-compile tests are load-bearing guard infrastructure: every
`make check` emits a host and a pocket into temp dirs, adds framework
`replace` directives to absolute repo paths, builds them with
`GOWORK=off` (hermetic legs fully offline, driver legs `GOPROXY=off`
against the warm module cache), runs the emitted pocket's own storetest
against its emitted memory store, and greps the guard SHAPES over the
emitted output (host: the one-rule + G9/G10; pocket: FS1 + G2/G6/G10).
Template rot fails the build — no repo guard can see inside `.tmpl`
files, so these tests are the only gate on emitted content.

The release gate verifies generated hosts for all three database choices and a
pocket with both stores using their pinned framework modules, with `GOWORK=off`
and no framework replacements:

```sh
go test -tags=release -run TestReleaseScaffoldsResolvePinnedModules -count=1 -v ./internal/commands
```

Before tagging, run this against the candidate module proxy through `GOPROXY`.
After tagging, run it against the published modules. The generated user pocket
still uses its local `v0.0.0` core replacement until its owner publishes it. This
gate runs core and migration-export tests and compiles both store conformance
suites; it clears database test settings and does not claim live conformance.

## What this tool deliberately does NOT do yet (workshop-v2b, demand-gated)

- **Store-adapter emission** from an entity spec (runtime-generic vs
  emitted code, spec placement, `queries.sql`, markers, drift
  regeneration). Trigger: the third hand-written store pair whose SQL
  was mechanical enough that emission would have been cheaper — or an
  owner call.
- **TS/OpenAPI client generation.** Trigger: the first real frontend
  consuming a gopernicus host's OpenAPI document.
- **`new domain`** (the app-local anatomy). Trigger: the second app
  needing it.
- **`doctor` / sqlguard.** Trigger: first CI need.
- **Integration-test harness generation** (capability-map YOUR CALL #9).

Known seam: G5 (`guard-pocket-dependencies`) keeps a hardcoded pocket
list — a pocket scaffolded INTO this monorepo must be added there by
hand; the CLI prints that checklist when it detects a `go.work` target.
