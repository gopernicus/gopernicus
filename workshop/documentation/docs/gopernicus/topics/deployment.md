---
title: Deployment
---

# Deploying gopernicus apps

`gopernicus new deploy <target>` emits a deploy profile: a runbook under
`workshop/deploy/<target>/` plus the pipeline files for one hosting
target (CI workflows land in `.github/workflows/` so they fire).
Profiles generalize real production pipelines; after emission the files
are yours — everything is created-once with a drift marker, and
re-running never overwrites.

| Target | What it is |
|---|---|
| `do-app` | DigitalOcean App Platform: tag-triggered GitHub workflow — ghcr image build, migrations as a release step, app refresh — plus a reference app spec |
| `cloud-run` | Google Cloud Run: make targets (`cloud-bootstrap`, `cloud-build`, `cloud-migrate`, `cloud-deploy`, `cloud-ship`) included from the root Makefile |

Each target's README is the runbook: one-time setup, deploy procedure,
rollback notes.

## The probe contract

Every scaffolded server exposes two endpoints with strictly separated
meanings:

- **`/healthz` — liveness.** Dependency-free: answers 200 as long as the
  process serves HTTP. Point platform *restart* checks here; a database
  outage must never make the platform kill healthy processes.
- **`/readyz` — readiness.** Answers 200 only when downstream
  dependencies are reachable (pings the database with a 2s timeout; 503
  otherwise). Point *traffic-routing* checks here so load balancers
  drain an instance during an outage instead of restarting it.

## Version stamping

The scaffolded dockerfile passes `BUILD_REF` into the binary
(`-X main.build`); deploy profiles set it to the commit SHA (do-app) or
`git describe` (cloud-run). The running build is visible in the startup
log line and in `/healthz`:

```json
{"status": "ok", "build": "3f2c1a9..."}
```

That makes "which commit is actually serving?" a curl, not an archaeology
session.

## Production migration policy: release step, never at boot

Migrations run as an explicit deploy step (`go tool gopernicus db
migrate` with the production database URL), **before** the new version
starts:

- A bad migration fails the *deploy*, not the running fleet — old
  instances keep serving on the old schema.
- Instances never race each other to migrate at startup.
- Boot stays fast and dependency-light, which the probe contract relies
  on.

Both shipped profiles embody this: the do-app workflow migrates between
image push and app refresh; cloud-run's `cloud-ship` runs
`cloud-migrate` between build and deploy.

Migrations are forward-only. For schema rollback, write down-safe
migrations or restore from backup; for code rollback, redeploy a
previous image (both runbooks cover the mechanics).

## Drift markers in non-Go files

Profile files carry the same `gopernicus:bootstrap` marker as Go
bootstraps, in the comment style their format allows: `#` for
yaml/makefiles/shell (after the shebang, when present), `<!-- -->` for
markdown. `gopernicus doctor` tracks them under `workshop/deploy/` and
`.github/workflows/` and warns when a file was created from an older
template — a hint to diff against a fresh emission, never an error.
