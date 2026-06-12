# gopernicus doctor

Check project health and configuration.

## Overview

`gopernicus doctor` runs a series of diagnostic checks against your project to
verify that it is correctly configured for gopernicus. It is a quick way to
identify missing files, version mismatches, or dependency problems.

The command exits non-zero when any check fails (warnings don't fail), so it
is safe to gate scripts on it.

## Usage

```
gopernicus doctor [--json]
```

Run it from anywhere inside a gopernicus project directory (or a
subdirectory). The command walks up the directory tree to find the project
root by locating `go.mod`.

### `--json`

Emits the run as machine-readable JSON on stdout instead of the human
report. Field names are a stable contract for agents and scripts:

```json
{
  "root": "/home/user/code/myapp",
  "framework": "v0.4.0",
  "ok": false,
  "checks": [
    {"name": "go.mod", "passed": true},
    {"name": "sql: Pred.Raw usage", "passed": true, "warn": true, "detail": "store.go:12 raw predicate"},
    {"name": "gopernicus framework", "passed": false, "detail": "not found in go.mod"}
  ]
}
```

- `ok` is false when any check is a hard failure; warnings (`"warn": true`)
  never fail the run.
- `framework` is the project's pinned framework version from go.mod; omitted
  when no version-shaped pin is found.
- `detail` and `warn` are omitted when empty/false.
- Exit code matches the human mode (non-zero on failure), and error text
  goes to stderr — stdout is always exactly one JSON object:

```bash
go tool gopernicus doctor --json | jq -e '.ok'
```

## Checks Performed

Doctor runs these checks in order:

### 1. go.mod

Verifies that `go.mod` exists at the project root.

- **Pass**: `go.mod` is present.
- **Fail**: "not found" — the current directory (and parents) do not contain a
  Go module. Run `gopernicus init` to create a project.

If `go.mod` is not found anywhere in the directory tree, doctor prints
`✗ project root — no go.mod found in current or parent directories` and exits
without running further checks.

### 2. Go version

Reads the `go` directive from `go.mod` and verifies it meets the minimum
supported version.

- **Pass**: The declared Go version meets or exceeds the minimum.
- **Fail**: "requires go X.Y or later" -- update the `go` directive in `go.mod`
  or upgrade your Go installation.

### 3. gopernicus.yml

Loads and validates the `gopernicus.yml` manifest file at the project root.

- **Pass**: Manifest exists and parses without error.
- **Fail**: "not found" -- run `gopernicus init` to create the manifest.

### 4. workshop/migrations/

Checks that the `workshop/migrations/` directory exists.

- **Pass**: Directory is present.
- **Fail**: "not found" -- run `gopernicus init` to scaffold the directory
  layout.

### 5. gopernicus framework dependency

Scans `go.mod` for a `require` or `replace` line referencing
`github.com/gopernicus/gopernicus`. Displays the version if found.

- **Pass**: "gopernicus framework (v0.X.Y)" -- the framework is in your
  dependencies.
- **Fail**: "not found in go.mod" -- run
  `go get github.com/gopernicus/gopernicus@latest` to add it.

### 6. SQL guards

Scans store packages for SQL-injection hazards via the standing guard.

- **Pass**: "sql: parameterized queries" -- no unsanctioned dynamic
  concatenation into SQL.
- **Fail**: names the first offending position (e.g. a `fmt.Sprintf` built
  into a query) -- use placeholders (`args.Add`) or `QuoteIdent`.
- **Warning**: "sql: Pred.Raw usage" -- `Pred.Raw` is a legitimate escape
  hatch that deserves review on every run; it never fails the run.

### 7. Bridge body limits

Walks `bridge.yml` files and warns when a write route (Create/Update) has no
`max_body_size` middleware -- unbounded request bodies are a
resource-exhaustion vector. A warning, not a failure.

### 8. Bootstrap template drift

Bootstrap files created at v0.4+ carry a first-line marker recording which
framework template created them:

```go
// gopernicus:bootstrap kind=repository/fop.go template=f199aa94a337
```

Doctor compares each marker's hash against the current framework's template
for that kind. The hash covers the *template*, not the file -- your edits to
bootstrap files never count as drift.

- **Pass**: all marked bootstraps match the current templates. The detail
  reports how many files are tracked, and how many pre-v0.4 bootstraps have
  no marker yet (they start tracking when refreshed or newly created).
- **Warning**: a file was created from an older template that has since
  changed -- review the release notes or refresh the file (see the
  [upgrading guide](../guides/upgrading.md)). Never a failure: bootstraps
  are user-owned and refreshing is a deliberate act.

## Output Format

Each check is displayed with a symbol prefix:

| Symbol | Meaning |
|---|---|
| `✓` | Check passed. |
| `✗` | Check failed. |
| `!` | Warning (non-fatal). |

After all checks, a summary line is printed:

- "All checks passed." -- everything looks good.
- "Some checks failed. Run 'gopernicus init' to set up a project." -- at least
  one check reported a failure.

## Example Output

```
  project root: /home/user/code/myapp

✓ go.mod
✓ go version (go 1.23)
✓ gopernicus.yml
✓ workshop/migrations/
✓ gopernicus framework (v0.4.2)

All checks passed.
```

### Example with failures

```
  project root: /home/user/code/myapp

✓ go.mod
✗ go version (go 1.20) — requires go 1.23 or later
✓ gopernicus.yml
✗ workshop/migrations/ — not found — run 'gopernicus init'
✗ gopernicus framework — not found in go.mod — run 'go get github.com/gopernicus/gopernicus@latest'

Some checks failed. Run 'gopernicus init' to set up a project.
```

## Common Issues and Fixes

| Issue | Cause | Fix |
|---|---|---|
| "no go.mod found" | Not inside a Go project | `cd` into your project or run `gopernicus init <name>` |
| Go version too old | `go.mod` declares a version below the minimum | Run `go mod edit -go=1.23` (or the current minimum) |
| gopernicus.yml not found | Project was not initialized with gopernicus | Run `gopernicus init` or create the file manually |
| workshop/migrations/ not found | Directory layout incomplete | Run `mkdir -p workshop/migrations/primary` or re-run `gopernicus init` |
| Framework dependency missing | `go get` was not run or failed | Run `go get github.com/gopernicus/gopernicus@latest && go mod tidy` |

## When to Run Doctor

- After running `gopernicus init` to verify the project was set up correctly.
- When switching branches or pulling changes that may affect project structure.
- Before filing a bug report -- include the doctor output.
- After upgrading Go or the gopernicus framework.

## Related

- [init](init.md) -- create a new project (fixes most doctor failures)
- [generate](generate.md) -- requires a healthy project to run
- [db](db.md) -- requires gopernicus.yml and workshop/migrations/
- [YAML Configuration](../generators/yaml-configuration.md)
