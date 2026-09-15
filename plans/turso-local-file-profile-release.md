# Turso connector v0.6.0 release — local file profile — 2026-09-15

Companion to [the feature plan](turso-local-file-profile.md) and
[AUDIT-036](../AUDIT.md#audit-036-turso-local-file-profile-and-env-tagged-config).
Manifest: [turso-local-file-profile-release-manifest.json](turso-local-file-profile-release-manifest.json).

## Scope

One module, `integrations/datastores/turso`, minor version `v0.6.0`: additive `Config`
env tags and `BusyTimeout`, the local file profile in `Open`, the new `turso/localfile`
driver package. No other module changes. No host adoption or deployment is part of
this release; adopting hosts bump the connector afterwards.

## Sequence

1. PR #51 merged into main as `943b8bf989aa1d63e61e8092f7ed12b669e452a5`; the merge
   commit's tree equals the reviewed branch head `50013c9d`.
2. Candidate verification at that commit with the workspace off (published
   dependency graph, no replace): connector build, vet and tests (three packages);
   every Turso-backed store suite with the integration tag; `make guard` (exit 0,
   including `TestIntegrationBoundaries`); `go mod tidy` leaves `go.mod` unchanged.
3. Source inventory at the release commit: 48 entries,
   sha256 `76f8572e5bc85787e2e7b7b6c8bba641405408b85ae4f3093ffa861d7d86ba6b`.
4. Annotated tag `integrations/datastores/turso/v0.6.0` (object `7b87bd5f`) created at
   the release commit and pushed; the remote tag resolves to the same object and
   commit; tags `v0.4.0`, `v0.5.0`, `v0.5.1` verified unchanged on the remote.
5. Public verification through the standard proxy and checksum database, no
   bypass: `go list -m -json` and `go mod download -json` for the version, then an
   isolated consumer module (no workspace, no replace) that imports the connector and
   `turso/localfile`, reads `AUTH_DB_*` through the `AUTH` namespace, opens a `file:`
   database and asserts the profile on a pooled connection: build, vet, test.

## Public verification

Passed 2026-09-15. The proxy served the version on the first attempt
(`Time` 2026-09-15T05:01:21Z). `go list -m -json` and `go mod download -json` report
`Origin.Hash` = the release commit `943b8bf9…`, `Origin.Ref` = the tag, `Subdir` =
the module directory; `Sum` `h1:XsGlUV/dCr8et4Cc3xPi28mU30IzgoU75WC4R+Z567E=`,
`GoModSum` `h1:83vKGYZCHOoTnCSWM/ClCLOvBlXOgkvGh3SPfn+dOOE=` (identical to v0.5.1:
the module file did not change). No checksum bypass, no replace, workspace off. The
isolated consumer (`example.com/turso-consumer`, importing the connector,
`turso/localfile` and `sdk/pkg/environment`) tidied, built, vetted and passed its
test, which reads `AUTH_DB_URL`/`AUTH_DB_MAX_CONNS` through the `AUTH` namespace,
opens a `file:` database under a not-yet-existing directory and observes
`journal_mode=wal`, `foreign_keys=1`, `busy_timeout=5000` on a pooled connection.

The release is complete. Hosted `libsql://` behavior is unchanged by this release
and was not re-exercised. Host adoption is a separate step per application.
