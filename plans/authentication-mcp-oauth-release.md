# Independent MCP OAuth release

Status: AUTHORIZED, IN PROGRESS — 2026-09-18.

Josh explicitly authorized committing the OAuth implementation, merging it to
main and publishing module releases. Three-sixty adoption, migrations and
production enablement remain separate work.

## Scope and versions

| Module | Previous | Release |
| --- | --- | --- |
| pockets/authentication | v0.12.0 | v0.13.0 |
| pockets/authentication/stores/pgx | v0.6.1 | v0.7.0 |
| pockets/authentication/stores/turso | v0.5.1 | v0.6.0 |
| pockets/authentication/views/goth | v0.4.0 | v0.5.0 |
| pockets/authentication/stores/firestore | v0.1.1 | v0.2.0 |

MCP OAuth connections have independent sessions from web logins, with PKCE,
consent, refresh/revoke, session management, authenticated introspection and
restricted MCP-to-API token exchange. SQL stores add migration 0019. Firestore
remains first-party only. Update SQL/view/Firestore core pins and auth-cms;
Turso also needs the already published connector v0.6.0.

## Preconditions and preservation

- Local branch `authentication-mcp-oauth` and remote main start at
  `5a388dd1aae50fdc08ab77a14b125bcb561a2bbd`.
- GitHub reports main unprotected; latest main checks passed. All five proposed
  tags are absent remotely. Recheck refs before publication; use ordinary
  fast-forward pushes and annotated tags in dependency order.
- Preserve and exclude `plans/cacher-design.md`,
  `plans/gps-360-go-audit-upgrade-handoff.md`,
  `plans/segovia-v2-audit-upgrade-handoff.md` and `.github/scripts/__pycache__/`.
- Preserve historical migration bytes. No unrelated module or deployment change.
- Implementation inventory and prior race/live SQL evidence are in
  [the implementation plan](authentication-mcp-oauth.md).

## Tasks and gates

- [ ] R1 Apply verified dependency pins, freeze final candidate archives and
  verify independent modules, external consumer and auth-cms with `GOWORK=off`.
- [ ] R2 Exercise the final running proof host; commit only release-owned files;
  run the complete 42-module `make check` with the committed generated artifacts.
- [ ] R3 Confirm committed source matches candidates, fast-forward main and
  publish the five annotated module tags in dependency order.
- [ ] R4 Verify public archives with ordinary sum.golang.org checks, compare
  source/checksums and Git origins, rerun public module/consumer checks and
  inspect GitHub CI. Commit a publication receipt without moving tags.

Use isolated file-proxy candidates before publication, with exemptions limited to
these unpublished modules. Public verification must use no checksum exemptions,
no replacements and `GOWORK=off`. Keep logs and durable hashes in
[the manifest](authentication-mcp-oauth-release-manifest.json).

## Verification limits

Prior actual HTTP/TLS and running-host tests cover login, independent W/A/B
connections, consent, PKCE, introspection/exchange, refresh, individual revoke,
web logout and revoke-all. Real PostgreSQL (default/named schema) and local SQLite
passed; optional non-C PostgreSQL collation, hosted Turso and Firestore/GCP were
not exercised. Firestore does not support delegated sessions.

Visual browser verification was unavailable because no browser connector or
computer-use permission was granted. Retry practical repository test tooling;
do not change personal browser/security settings. Record any remaining gap.
Actual Claude discovery/connector acceptance and three-sixty MCP transport,
resource metadata and tool authorization remain the consuming host's work.
