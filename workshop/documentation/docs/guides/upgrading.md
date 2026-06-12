---
sidebar_position: 5
title: Upgrading the Framework
---

# Upgrading the Framework

How to move a gopernicus project to a newer framework version. The flow is
the same for every release; per-release specifics live in the
[CHANGELOG](https://github.com/gopernicus/gopernicus/blob/main/CHANGELOG.md)
as a **Consumer actions** checklist — read that release's entry (and every
entry between your pin and the target) before starting.

The framework pins the generator to the runtime via go.mod's `tool`
directive, so repinning upgrades both together — generated code and
framework can never drift apart.

---

## The repin flow

### 1. Repin

```bash
go get github.com/gopernicus/gopernicus@vX.Y.Z
go mod tidy
go mod vendor   # only if your project vendors dependencies
```

If your go.mod carries a `replace` pointing at a local framework checkout,
drop it first (`go mod edit -dropreplace=github.com/gopernicus/gopernicus`).
Keep a replace only while actively co-developing the framework.

### 2. Regenerate

```bash
go tool gopernicus generate
```

If a release changed schema reflection, run `go tool gopernicus db reflect`
first (the CHANGELOG entry will say so).

### 3. Reconcile the diff, file class by file class

Review `git diff` grouped by what gopernicus owns:

| File class | What to expect |
|---|---|
| `generated*.go` | Regenerated wholesale — review for surprises, don't hand-edit |
| Bootstrap files (`repository.go`, `store.go`, `bridge.go`, `fixtures.go`, `store_test.go`, …) | Untouched except marker blocks (e.g. the Storer interface between `// gopernicus:start` markers) — your custom code above markers survives |
| Satisfiers | Generated when headers present; headerless copies are skipped with a printed note |
| Generation output notes | Read them — skips and "left untouched" notes are deliberate signals, not noise |

### 4. Apply the release's consumer actions

Work through the CHANGELOG checklist for every release between your old pin
and the new one, oldest first. Typical actions: dropping a spec ejection to
re-adopt a fixed shipped spec, deleting a private helper copy a regenerated
file now provides, or adopting a new annotation.

### 5. Verification gate

```bash
go build ./... && go vet ./... && go test ./...
go test -tags=integration ./...   # database up
go test -tags=e2e ./...
go tool gopernicus doctor
```

Then the determinism check — regenerate twice and confirm a clean tree:

```bash
go tool gopernicus generate && go tool gopernicus generate
git status   # generated files must show no further changes
```

Finally, boot the app and smoke one auth flow plus one custom-domain
endpoint. Green tests alone are not a release gate.

---

## Refreshing bootstrap files

Bootstrap files are created once and never overwritten, so a project
carries them at the template vintage of whatever version first generated
them. When a release improves a bootstrap template:

- `go tool gopernicus generate --force-bootstrap` overwrites **all**
  bootstrap files. Never run this blindly on a project with customized
  bootstraps — diff first, or refresh selectively by deleting the specific
  file and regenerating.
- The safest selective refresh: `git rm` the one file, regenerate (it comes
  back at the current template), then re-apply your customizations from
  git history.

## Spec ejection policy

Feature entities (auth, rebac, tenancy, events, jobs) have shipped specs;
a project-local `queries.sql` for one of them is an **ejection** that wins
over the shipped spec — and forfeits upstream spec improvements.

On each upgrade, for every ejected feature entity, diff your file against
the framework's `core/repositories/<domain>/<entity>/queries.sql` at the
new tag:

- **Identical** → delete your file to adopt the shipped spec (automatic
  updates on future bumps).
- **Different** → keep it, and document why at the top of the file.

Re-ejecting is always possible: create the file again and it wins.

## Version skew rules

- Upgrade one consumer project at a time; the framework supports being on
  different pins across projects indefinitely.
- Don't skip reading intermediate CHANGELOG entries — consumer actions
  compound.
- `go tool gopernicus doctor` after every upgrade; `doctor --json` if a
  script or agent is driving.
