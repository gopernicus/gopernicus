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
- a README that pins released framework tags, with local `replace` directives shown as an optional development step.

It mounts no pocket. Add only the modules your host needs.

## `new pocket`

```bash
gopernicus new pocket notes \
  --module github.com/acme \
  --aggregate note \
  --dir ./pockets/notes
```

Flags:

| Flag | Meaning | Default |
|---|---|---|
| `--module` | module-path root; pocket name is appended; required | — |
| `--aggregate` | first aggregate/package identifier | `item` |
| `--dir` | target | `./<pocket>` |

Names must be lowercase Go identifiers starting with a letter.

The emitted pocket contains:

- SDK/shared-contract core module and public socket;
- domain entity, ordering allow-list, and repository port;
- sealed create/get/list/delete service;
- public memory store;
- exported store conformance suite;
- pgx and Turso sibling modules;
- migration, boot probe, exports, and live conformance hooks for both dialects.

It registers no HTTP routes. Delivery is a separate adapter you design for the real pocket.

When the target is inside a `go.work` monorepo, Workshop prints a manual checklist for workspace, Makefile, live-store tests, and the hardcoded pocket-core guard. It does not silently edit those shared files.

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

The CLI does not know a DSN, database driver, or pocket migration source. Missing runners and runner exit failures are reported to the caller. A runner scaffolded by `gopernicus init` v0.5.0 or later reads its runtime mode from the process environment, so run the verb as `APP_RUNTIME_MODE=development gopernicus db migrate` (the scaffold's `make migrate` exports it); the CLI never chooses a mode.

## `db status`

```bash
gopernicus db status
gopernicus db status --db primary
```

Delegates to the host runner's `-status` mode. If the runner is absent or cannot connect, the CLI prints a file-only view with every discovered migration shown as pending—the only honest status a stdlib-only tool can produce without database access.

## `guard`

```bash
gopernicus guard                      # the host in the current directory
gopernicus guard ./myapp
gopernicus guard --posture ./myapp    # H0–H14 plus the production-posture rules P0–P8
gopernicus guard --list
gopernicus guard --list --posture
```

Checks a host against the [host contract](../architecture/host-contract.md), rules H0 through H14, and with `--posture` also P0 through P8 (production posture, the contract's section 3b; `workshop/gopernicus` v0.5.0). The host root is the directory holding its `go.mod`; the module path is read from that file. Nested modules, `vendor/`, `testdata/`, `node_modules/`, and directories starting with `.` or `_` are not walked, and symlinks are not followed.

| Flag | Meaning | Default |
|---|---|---|
| `--list` | print every rule's canonical sentence and exit (with `--posture`, the H sentences then the P sentences) | off |
| `--posture` | add P0–P8: the mode from the process environment, `RuntimeMode` on every recognized surface, `TrustProxies` outermost, `SecurityHeaders`, `ValidateProduction`, literal acknowledgements, `NewKeys`, a posture-clean `.env.example` | off |

Output is one status line per rule in id order, then one line per finding, then a summary:

```text
H0   1 finding
H1   ok        (1 package)
H3   no scope  (no internal/logic/compositions)
…

H0  internal/leak: package outside the contract's shape — home: internal/{logic,inbound,outbound}/domains/leak
    rule: A host's production Go packages live only under …

guard: 1 finding across 1 rule
```

A rule with nothing in scope prints `no scope` with the reason, so an inert rule never reads as a clean one. A clean run ends `guard: clean (15 rules, N packages)`; without `--posture` the output is exactly the H run.

With `--posture` the nine P status lines follow the fifteen H lines, and informational lines follow the status block — `ack` for each acknowledgement written `true` (`ack (conditional)` when it sits under an `if` or `switch`) and `rand` for each `crypto/rand` use in the scanned trees. They are review evidence and never change the exit code:

```text
P5   ok        (3 acknowledgements)
P6   ok        (1 mount)
…

ack (conditional) cmd/server/main.go:323: DeliveryEphemeralAcknowledged
ack  cmd/server/main.go:950: DeliveryJobsAcknowledged
rand cmd/server/documents.go:43: crypto/rand.Read
guard: clean (24 rules, 15 packages)
```

A host without the `tool` pin runs the posture check in CI with `go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.5.0 guard --posture .`. A release older than v0.5.0 refuses `--posture` (unknown flag): the binary exits 2, and a `go run` invocation prints `exit status 2` and returns 1; treat any non-zero status as a broken invocation, never a pass.

Exit codes: `0` clean, `1` findings, `2` for a missing or malformed `go.mod`, an unparsable Go file, or bad flags.

There is no `--skip`, no allow file, and no configuration. A host that does not conform yet runs the guard red until it does, then adds it to `make guard`. The `--list` sentences are versioned with the binary: a wording change is a change to `examples/README.md` and the CLI in the same tag.

## `version`

```bash
gopernicus version
```

Prints the CLI version and module path. The repository's current development version remains a pre-tag placeholder.

## Planned commands

`generate`, `boot`, `doctor`, `new domain`, and client-generation commands are not part of the current command surface. Application, store, and client generation remain roadmap work; this page will be updated when those commands have an implementation and tests.
