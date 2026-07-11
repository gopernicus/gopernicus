# Releasing gopernicus modules

This repo is a multi-module workspace (`go.work`, dev-only) with thirty-six
modules today: `sdk`; `integrations/{cryptids/bcrypt, cryptids/golang-jwt, cryptids/google-uuid,
datastores/pgxdb, datastores/turso, email/sendgrid, filestorage/gcs,
filestorage/s3, kvstores/goredis, oauth/github, oauth/google,
notify/mailer, scheduling/robfig-cron, tracing/otel}`; `features/authentication`,
`features/authorization` (authorization-v1, 2026-07-09), `features/cms`
(+ `views/templ`, its bundled default views module — feature-standard B2,
2026-07-07), `features/events` (events-v1, 2026-07-08), `features/jobs`
(each feature + `stores/{turso,pgx}`); `examples/{cms,
minimal, auth-cms, jobs-minimal}`; `workshop/gopernicus` (the scaffolding
CLI — a `go install`-able tool, tagged like any importable module). Each importable module (everything except the four
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

### features/authentication — next tag: session hashing invalidates all live sessions

auth-v2 (2026-07-07) moved session-token storage to service-side SHA-256
hashing (design §7.3): the service hashes the cookie token before every
repository call, and stores keep persisting an opaque string — no DDL. Any
host upgrading `features/authentication` across this change must know:

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

The same note lives in `features/authentication/README.md` (the upgrade note section).

### features/authentication stores — next tag: migration 0013 (invitation identifier kinds)

identity-resolution (2026-07-10) added `0013_invitation_identifier_kind.sql`
to BOTH dialect stores: `ADD COLUMN identifier_kind TEXT NOT NULL DEFAULT
'email'` plus a drop/recreate of the pending-tuple unique index to include
the kind. Hosts re-export (`ExportMigrations`) and apply before deploying
the new feature core; existing rows backfill as `email`, and hosts that
only ever create email invitations see zero behavior change.

### sdk — next tag: the layering split moved every sdk subpackage import path

sdk-layering (2026-07-10) re-homed `sdk/errs` into the root package
(`sdk.ErrNotFound`) and moved every other sdk package under
`sdk/foundation/` or `sdk/capabilities/` (package names unchanged, paths
only). Pre-tag there is no version obligation, but any consumer pinned
to a git SHA must re-path its imports wholesale; the workshop CLI's
emitted scaffolds already use the new paths.

### sdk — next tag: additive middleware symbols (minor floor)

middleware-consolidation (2026-07-11) added exported symbols to two sdk
packages, all backward-compatible — they floor sdk's next tag at a **minor**
bump, never a major:

- `sdk/capabilities/ratelimiter`: `Middleware` + the `Allower` port (the generic
  IP/key rate-limit middleware, relocated out of `features/authentication`'s
  internals).
- `sdk/foundation/web`: `TrustProxies` + `ClientIP` (the rightmost-minus-N
  client-IP resolver ported from the original gopernicus `httpmid`).

No existing symbol changed; a consumer that ignores the new surface is
unaffected.

### features/authorization — next tag: additive gate symbols (minor floor)

middleware-consolidation (2026-07-11) added `RequirePermission`,
`ResourceResolver`, and `FixedResource` to the root package (the exported HTTP
middleware gate; implementation in `internal/logic/authorizersvc`). Additive —
floors the next tag at a **minor** bump. Adopter note: replacing a hand-rolled
gate closure with `RequirePermission` changes the 401/403 response *body* to the
FS9 `web.Error` shape (status codes unchanged) — a client contract detail, not a
Go-API break.

### features/authentication — next tag: patch-only (internal delegation)

middleware-consolidation (2026-07-11) rewrote `Service.RateLimitByIP`'s body to
delegate to `ratelimiter.Middleware` with its exact prior signature and
semantics; no exported surface changed. This is a **patch**, not a minor — the
proof is auth's existing rate-limit tests passing unmodified.

## What this repo is not doing (yet)

- No CI-driven automated tagging — tags are cut by hand until a release
  workflow is built.
- No changelog convention is mandated yet; the tag message plus commit log is
  the record until one is adopted.
