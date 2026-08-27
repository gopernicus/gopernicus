---
title: Testing & architecture checks
description: Test modules, conformance, browser behavior, generated source, and dependency rules.
---

# Testing & architecture checks

Gopernicus tests behavior at several boundaries: ordinary package units, reusable conformance suites, live adapters, browser interaction, scaffolding output, and repository architecture.

## The main repository gate

```bash
make check
```

It:

- regenerates templ Go and checks committed output for drift;
- checks committed GOTH asset output for drift without requiring Node;
- warms the module cache needed by offline scaffold proofs;
- vets, builds, and tests every Go module;
- compile-vets Turso store code with integration tags;
- runs architecture guards.

The loop is intentionally hermetic. Live database suites skip loudly rather than making every contributor provision infrastructure.

## Run a focused module

Each directory with a `go.mod` is independently testable:

```bash
cd sdk
go test ./...

cd pockets/jobs
go test -race ./...
```

Use `GOWORK=off` when validating that a module's declared requirements—not workspace convenience—are sufficient.

## Pocket store conformance

Pocket cores export test suites:

```go
func TestStore(t *testing.T) {
    storetest.Run(t, func(t *testing.T) cms.Repositories {
        return newTestRepositories(t)
    })
}
```

Jobs splits ordinary queue, schedule, and fenced queue families. Capability packages expose similar suites for cache, event bus, file storage, rate limiting, and work protocol implementations.

Conformance tests are contracts. Every backend must run them; a backend-specific test cannot redefine shared semantics.

## Live stores

```bash
POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
  make test-stores
```

Turso legs need `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN` and compile with `-tags=integration`. Use disposable databases. Some suites exercise concurrency, claims, and migration state destructively within their own test data.

## Architecture guards

`make guard` enforces boundaries including:

- SDK standard-library-only and internal tiering;
- pocket core SDK-only requirements;
- no pocket-to-pocket imports;
- no pocket core imports of stores, views, UI, integrations, or examples;
- no integration imports inward to pockets/hosts/Workshop;
- transport responders use SDK web primitives;
- store modules do not reach foreign pockets;
- Workshop isolation;
- UI dependency whitelist;
- security-specific negative boundaries for authentication/authorization delivery.

If a guard fails, fix the dependency direction. Do not weaken a regex to make an architecture bug disappear.

## Scaffold compile proofs

Workshop templates are not Go files and therefore evade many whole-tree scans. Dedicated tests emit a host and pocket into temporary directories, rewrite local pre-tag replacements, disable workspace resolution, build, and run memory conformance.

Update these proofs whenever a template changes its module requirements or emitted anatomy.

## UI verification

Templ render tests and Go tests run in the normal module loop. The full browser gate is separate:

```bash
make test-ui-browser
```

It installs Playwright browsers and runs the GOTH showcase against Chromium, Firefox, and WebKit with accessibility coverage. Use it for UI milestone/release work, not for every SDK-only edit.

Rebuild committed CSS/JS assets with:

```bash
make generate-ui-assets
```

Normal `make check` verifies the committed outputs did not drift without invoking Node.

## Documentation verification

From `workshop/documentation`:

```bash
pnpm install
pnpm typecheck
pnpm build
```

The production build treats broken links as errors. Keep snippets abbreviated where full construction would obscure the contract, and point to an executable example for exact current wiring.
