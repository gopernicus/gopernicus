# Security

This file is the written security posture of the gopernicus framework repository: how to
report a vulnerability, which versions are supported, and the controls an auditor can sample
for change management, vulnerability management and CI credentials. It describes the
repository as it is on 2026-10-26; host applications built on the framework document their
own posture, starting from the docs site's *Production posture* guide.

A host that turns on the authorization TupleCache brings its Redis into scope: the cache
sits inside the authorization trust boundary, so that instance is an in-scope system with
the database's controls (dedicated instance or logical database, TLS with a verified CA, an
ACL user limited to the mirror keys). Since `pockets/authorization/stores/goredis` v0.6.0,
`WithIntegrityKeys` (one key per environment, paired with `FullRebuildEvery`) makes a
Redis-only compromise cost availability rather than access; the store README's "Trust
boundary and hardening" section is the recipe.

## Reporting a vulnerability

Report privately through GitHub's security advisories for this repository
(**Security → Report a vulnerability**). Do not open a public issue for an unfixed
vulnerability. There is no bug bounty. Reports are acknowledged as soon as practical and
fixes ship as patch tags on the affected module(s), recorded in `RELEASING.md`.

## Supported versions

Each module is tagged independently (`RELEASING.md`, "Tagging scheme"). Only the latest tag of
each module receives fixes. Latest tags at the time of writing:

| Module | Latest |
|---|---|
| `sdk` | v0.20.0 |
| `pockets/authentication` | v0.31.0 |
| `pockets/authentication/stores/pgx` | v0.15.0 |
| `pockets/authentication/stores/turso` | v0.14.0 |
| `pockets/authentication/views/goth` | v0.9.0 |
| `pockets/authorization` | v0.31.0 |
| `pockets/authorization/stores/pgx` | v0.21.0 |
| `pockets/authorization/stores/turso` | v0.20.0 |
| `pockets/authorization/stores/goredis` | v0.6.0 |
| `pockets/cms` | v0.6.0 |
| `pockets/cms/stores/pgx` | v0.5.0 |
| `pockets/cms/stores/turso` | v0.4.0 |
| `pockets/events` | v0.3.0 |
| `pockets/jobs` | v0.7.0 |
| `pockets/jobs/stores/pgx` | v0.7.0 |
| `pockets/jobs/stores/turso` | v0.6.0 |
| `pockets/secrets` | v0.2.0 |
| `integrations/authentication/firebase` | v0.2.0 |
| `integrations/cryptids/go-oidc` | v0.2.0 |
| `integrations/cryptids/go-webauthn` | v0.1.0 |
| `integrations/cryptids/golang-jwt` | v0.3.0 |
| `integrations/datastores/pgxdb` | v0.14.0 |
| `integrations/datastores/turso` | v0.9.0 |
| `integrations/email/sendgrid` | v0.5.0 |
| `integrations/encoding/rsc-qr` | v0.1.0 |
| `integrations/filestorage/gcs` | v0.3.0 |
| `integrations/filestorage/s3` | v0.3.0 |
| `integrations/kvstores/goredis` | v0.4.0 |
| `integrations/oauth/github` | v0.3.0 |
| `integrations/oauth/google` | v0.3.0 |
| `ui/goth` | v0.2.1 |
| `workshop/gopernicus` | v0.9.0 |

Every module declares `go 1.26.10`; a host that honors the pin builds with a standard
library carrying the fixes listed in `plans/soc2-s8-toolchain-pgx-repin-handoff.md`.

## Change management

The repository has one maintainer. The compensating control for the missing second
reviewer is written here and enforced by configuration, not by habit:

1. **Plan-file ratification.** Significant work is designed in a plan file under
   `.claude/plans/` (released copies in `plans/`) and ratified by the owner before any code
   is written; the plan names the verification each slice must pass.
2. **Required status checks on `main`.** A repository ruleset on `main` requires a pull
   request, the `check` status (the hermetic `make check` gate: per-module vet, build, test,
   templ and asset drift, and every layering guard) and the `vuln` status (see below), blocks
   force-pushes and deletion, and limits the admin bypass to pull requests only, so a bypass
   leaves a reviewable trail.
3. **Cold-verified releases.** Every tag is verified from a scratch consumer with
   `GOWORK=off`, an empty module cache, `proxy.golang.org` and `sum.golang.org`, and the
   result is recorded with per-tag commit hashes and checksum manifests in `RELEASING.md`.
   Tags are annotated (`git tag -a`) and unsigned; tamper evidence comes from the checksum
   database's transparency log and the manifests. Signed tags are planned once a dedicated
   release-signing key exists (SOC 2 readiness follow-up, FHC-9).
4. **`CODEOWNERS`** lists the maintainer; the day a second reviewer is listed, the ruleset's
   required-approval count moves from zero to one.

## Vulnerability management

- **Every push and pull request** runs `vuln` (`.github/workflows/check.yml`): `make vuln`,
  which runs `govulncheck` with symbol-level reachability in every module against the live
  Go vulnerability database. A newly published advisory that the code reaches fails the next
  run. The tool version is pinned in the `Makefile`.
- **Dependency currency** is a deliberate, periodic coordinated bump across the modules (the
  2026-10-22 train is the precedent), reviewed and gated like any change; Dependabot version
  updates are not configured (under evaluation). Sibling framework modules move only through
  release trains. The `vuln` job above is what catches a reachable advisory between bumps.
- **CodeQL** default setup is configured for this repository.
- **GitHub Actions** are pinned to full commit SHAs and the repository setting requires it;
  only GitHub-authored actions plus an explicit allowlist may run.
- The toolchain is pinned per module (`go 1.26.10`); `GOTOOLCHAIN=auto` fetches it.

## CI credentials

CI runs on GitHub-hosted runners with a read-only default workflow token. The hermetic
`check` and `vuln` jobs use no secrets. The live-store workflow (`.github/workflows/live-stores.yml`)
uses repository secrets for the Turso playground only; no cloud service-account credential
exists in CI.

## Where the evidence lives

`RELEASING.md` (release records, cold verification, checksum manifests), `.github/workflows/`
(the gates), `.github/dependabot.yml`, `.github/CODEOWNERS`, the ruleset on `main` (repository
settings), and the docs site's operations guides for host-side controls.
