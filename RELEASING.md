# Releasing gopernicus modules

This repo is a multi-module workspace (`go.work`, dev-only) with twenty-seven
modules today: `sdk`; `integrations/{cryptids/bcrypt, cryptids/golang-jwt,
datastores/pgxdb, datastores/turso, email/sendgrid, filestorage/gcs,
filestorage/s3, kvstores/goredis, oauth/github, oauth/google,
scheduling/robfig-cron, tracing/otel}`; `features/auth`, `features/cms`
(+ `views/templ`, its bundled default views module — feature-standard B2,
2026-07-07), `features/jobs` (each + `stores/{turso,pgx}`); `examples/{cms,
minimal, auth-cms, jobs-minimal}`. Each importable module (everything except the four
`examples/*` hosts, which are demonstrations, not libraries) is tagged and
versioned **independently** — there is no single repo-wide version.

No tags have been cut yet. This document is the procedure for when they are;
cutting a tag is out of scope for the milestone that introduced this file.

## Tagging scheme

Nested Go modules in a single repo are tagged with the module's directory as a
prefix, per the standard Go module convention for multi-module repos:

```
sdk/v0.1.0
integrations/datastores/turso/v0.1.0
features/cms/v0.1.0
features/cms/stores/turso/v0.1.0
```

Each module's own `go.mod` `require` versions (e.g. `features/cms/stores/turso`
requiring `sdk`) are bumped and tagged independently — a patch release of
`sdk` does not force a release of every module that depends on it, only the
ones whose `go.mod` is updated to require the new version.

## Preconditions before the first tag

1. **Module paths are final.** Every `go.mod` module line and internal import
   is rooted at `github.com/gopernicus/gopernicus/...`.
2. **`replace` directives are dropped or pinned.** `go.work` itself is
   dev-only and is never part of what a downstream consumer sees. The nested
   modules that reference sibling modules by relative path in their own
   `go.mod` (e.g. `features/cms/stores/turso`'s `replace` of `sdk` and
   `features/cms` to `../../../../sdk` and `../../..`) must have those
   `replace` lines removed and replaced with ordinary `require` entries
   pinned to the sibling module's tagged version, so `go build` works for a
   consumer who does not have this repo checked out as a workspace.
3. **Guards + tests green** (`make check`) on the commit being tagged.

## Cutting a tag

For each module being released, from the repo root:

```sh
git tag features/cms/v0.1.0 -m "features/cms v0.1.0"
git push origin features/cms/v0.1.0
```

A consumer depends on it the normal Go way:

```sh
go get github.com/gopernicus/gopernicus/features/cms@v0.1.0
```

## Version bumps

Standard Go module semver rules apply per-module:

- **Patch** — bugfix, no exported API change.
- **Minor** — additive, backward-compatible exported API change (e.g. a new
  `Config` field with a working zero value, a new optional `Mount` field per
  C3's evolution policy in `features/README.md`).
- **Major** — breaking exported API change (removed/renamed exported type or
  field, changed method signature). Pre-`v1`, breaking changes are expected
  and do not require a major bump by Go's own pre-release semantics; each
  module should still move to `v1.0.0` deliberately once its contract is
  considered stable, not accidentally on the first tag.

## Upgrade notes (keyed to each module's next tag)

### features/auth — next tag: session hashing invalidates all live sessions

auth-v2 (2026-07-07) moved session-token storage to service-side SHA-256
hashing (design §7.3): the service hashes the cookie token before every
repository call, and stores keep persisting an opaque string — no DDL. Any
host upgrading `features/auth` across this change must know:

- **Every live session is invalidated at deploy — a forced logout for all
  users, remember-me/long-TTL sessions included** (a v1 plaintext row never
  matches a hashed lookup again). Users just log in again; no data is lost.
- The orphaned plaintext rows are unreachable and dead past their natural
  `expires_at` TTL. No purge ships; hosts may vacuum them at leisure.
- **Deploy in a single cutover or drain traffic first — do not roll.** On a
  rolling deploy, mixed plaintext/hashed pods make the SAME session cookie
  flap 401/200 depending on which pod answers, for the whole rollout window.
- **A rollback forces a SECOND mass logout**: sessions minted by the new
  binary are hashed rows the old binary cannot read.

The same note lives in `features/auth/README.md` (the upgrade note section).

## What this repo is not doing (yet)

- No CI-driven automated tagging — tags are cut by hand until a release
  workflow is built.
- No changelog convention is mandated yet; the tag message plus commit log is
  the record until one is adopted.
