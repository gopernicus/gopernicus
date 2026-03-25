# gopernicus doctor

Check project health and configuration.

## Overview

`gopernicus doctor` runs a series of diagnostic checks against your project to
verify that it is correctly configured for gopernicus. It is a quick way to
identify missing files, version mismatches, or dependency problems.

The command exits cleanly even when checks fail -- it prints results and
suggestions rather than returning errors.

## Usage

```
gopernicus doctor
```

No flags or arguments. Run it from anywhere inside a gopernicus project
directory (or a subdirectory). The command walks up the directory tree to find
the project root by locating `go.mod`.

## Checks Performed

Doctor runs five checks in order:

### 1. go.mod

Verifies that `go.mod` exists at the project root.

- **Pass**: `go.mod` is present.
- **Fail**: "not found" -- the current directory (and parents) do not contain a
  Go module. Run `gopernicus init` to create a project.

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

## Output Format

Each check is displayed with a symbol prefix:

| Symbol | Meaning |
|---|---|
| `+` | Check passed. |
| `x` | Check failed. |
| `!` | Warning (non-fatal). |

After all checks, a summary line is printed:

- "All checks passed." -- everything looks good.
- "Some checks failed. Run 'gopernicus init' to set up a project." -- at least
  one check reported a failure.

## Example Output

```
  project root: /home/user/code/myapp

+ go.mod
+ go version (go 1.23)
+ gopernicus.yml
+ workshop/migrations/
+ gopernicus framework (v0.4.2)

All checks passed.
```

### Example with failures

```
  project root: /home/user/code/myapp

+ go.mod
x go version (go 1.20) -- requires go 1.23 or later
+ gopernicus.yml
x workshop/migrations/ -- not found -- run 'gopernicus init'
x gopernicus framework -- not found in go.mod -- run 'go get github.com/gopernicus/gopernicus@latest'

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
