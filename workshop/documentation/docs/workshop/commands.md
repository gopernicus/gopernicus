---
title: Workshop command reference
description: Current Workshop CLI commands, flags, and output.
---

# Workshop command reference

The binary name below is `gopernicus`. In a checkout, substitute `go run .` from `workshop/gopernicus`.

<img class="docs-illustration" src="/gopernicus/img/clicircle.jpg" alt="Gopernicus character holding a glowing system map" />

## `init`

```bash
gopernicus init \
  --module github.com/acme/myapp \
  --db pgx \
  ./myapp
```

Flags:

| Flag | Values | Default |
|---|---|---|
| `--module` | host Go module path; required | — |
| `--db` | `none`, `turso`, `pgx` | `none` |
| `--dir` | target directory; positional target also accepted | current directory |

The command emits:

- `go.mod`;
- `cmd/server/main.go` with explicit wiring and `/healthz`;
- `Makefile` and `.env.example`;
- host migration ledger `workshop/migrations/primary`;
- a datastore-specific migration runner when `--db` is not `none`;
- a README with pre-tag dependency instructions.

It mounts no feature. Add only the modules your host needs.

## `new feature`

```bash
gopernicus new feature notes \
  --module github.com/acme \
  --aggregate note \
  --dir ./features/notes
```

Flags:

| Flag | Meaning | Default |
|---|---|---|
| `--module` | module-path root; feature name is appended; required | — |
| `--aggregate` | first aggregate/package identifier | `item` |
| `--dir` | target | `./<feature>` |

Names must be lowercase Go identifiers starting with a letter.

The emitted feature contains:

- SDK-only core module and public socket;
- domain entity, ordering allow-list, and repository port;
- sealed create/get/list/delete service;
- public memory store;
- exported store conformance suite;
- pgx and Turso sibling modules;
- migration, boot probe, exports, and live conformance hooks for both dialects.

It registers no HTTP routes. Delivery is a separate adapter you design for the real feature.

When the target is inside a `go.work` monorepo, Workshop prints a manual checklist for workspace, Makefile, live-store tests, and the hardcoded feature-core guard. It does not silently edit those shared files.

## `db create`

```bash
gopernicus db create add_widgets
gopernicus db create add_audit --db secondary
```

Creates `NNNN_<slug>.sql` below `workshop/migrations/<ledger>`. The number is the existing maximum plus one. Scratch files beginning with `_` are ignored.

Migration filenames are ledger identity. Never renumber an applied file.

## `db migrate`

```bash
gopernicus db migrate
```

Runs the host-owned migration package:

```bash
go run ./workshop/migrations
```

The CLI does not know a DSN, database driver, or feature migration source. Missing runners and runner exit failures are reported to the caller.

## `db status`

```bash
gopernicus db status
gopernicus db status --db primary
```

Delegates to the host runner's `-status` mode. If the runner is absent or cannot connect, the CLI prints a file-only view with every discovered migration shown as pending—the only honest status a stdlib-only tool can produce without database access.

## `version`

```bash
gopernicus version
```

Prints the CLI version and module path. The repository's current development version remains a pre-tag placeholder.

## Planned commands

`generate`, `boot`, `doctor`, `new domain`, and client-generation commands are not part of the current command surface. Application, store, and client generation remain roadmap work; this page will be updated when those commands have an implementation and tests.
