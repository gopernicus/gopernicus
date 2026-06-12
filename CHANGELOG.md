# Changelog

All notable changes to the gopernicus framework, with a **Consumer actions**
checklist per release. The checklists are written as imperative steps so a
human or agent can execute them top to bottom; the generic repin flow they
assume is documented in
[the upgrading guide](workshop/documentation/docs/guides/upgrading.md).

Releases are tag-only: `git tag -a vX.Y.Z && git push origin vX.Y.Z`.

## Unreleased

### Added
- `gopernicus doctor --json` — machine-readable health output with a stable
  field contract (`root`, `framework`, `ok`, `checks[]`). Exit codes and the
  human report are unchanged. (#30)

### Consumer actions
- [ ] None required. Scripts may now gate on
      `go tool gopernicus doctor --json | jq -e '.ok'`.

## v0.3.5 — 2026-06-11

### Fixed
- `infrastructure/cryptids/golangjwt`: `Verify` now uses strict base64
  decoding, rejecting non-canonical token encodings. Previously, textually
  distinct tokens differing only in a final character's padding bits decoded
  to the same MAC and all verified (token malleability). Framework-issued
  tokens are unaffected — the signer always emits canonical encodings. (#28)

### Consumer actions
- [ ] Repin: `go get github.com/gopernicus/gopernicus@v0.3.5 && go mod tidy`
      (+ `go mod vendor` if vendored), regenerate, run the verification gate.
- [ ] Redeploy to pick up the verify-path change. No code changes; existing
      sessions remain valid (their tokens are canonical).
- [ ] If anything keys on raw token strings (blocklists, caches), note that
      malleated variants are now rejected rather than accepted.

## v0.3.4 — 2026-06-11

### Added
- `@fixture-default` accepts the bare token `null` to pin a nullable column
  (any type) to SQL NULL — the answer to CHECK constraints with an
  `IS NULL` branch. Rejected on NOT NULL columns at generation time. (#27)

### Consumer actions
- [ ] Repin to v0.3.4, regenerate, run the verification gate.
- [ ] For entities skipped over CHECK constraints requiring a column's
      absence: replace `-- @skip-integration-test` with
      `-- @fixture-default: <column> null` and regenerate — the generated
      integration probes come back.

## v0.3.3 — 2026-06-11

### Added
- CI gates on the framework repo: build/vet/unit, tagged-compile
  (`integration`/`e2e`/`penetration` vet), clean-regenerate (drift check),
  and the integration suite. (#25)
- `@fixture-default: <column> <value>` — file-level, repeatable queries.sql
  annotation overriding the generated test fixture's value for one column.
  Validated against the reflected schema; PK/FK/time overrides and
  unparseable values are hard generation errors. Positioned before
  `@skip-integration-test` on the escape-hatch ladder. (#26)

### Changed
- go.mod `go` directive aligned to `goversion.MinGoVersion` (1.26). The two
  move in lockstep from here on.

### Consumer actions
- [ ] Ensure a Go ≥ 1.26 toolchain, then repin to v0.3.3, regenerate, run
      the verification gate.
- [ ] Optional: adopt `@fixture-default` for entities whose CHECK
      constraints the generic fixture can't satisfy.

## v0.3.2 — 2026-06-11

### Added
- Invitations bridge: `redirect_url` on the create request with a
  `WithAllowedFrontends` allowlist (strict when configured, pass-through
  when not), and `WithEmailer` / resolver options for email subscribers. (#24)

### Changed
- `@skip-integration-test` now regenerates `generated_test.go` as a
  **setup-only** file (no test functions, `setupTestStore` helper kept)
  instead of removing it. (#24)

### Consumer actions
- [ ] Repin to v0.3.2, regenerate, run the verification gate.
- [ ] If a skipped entity's `store_test.go` carries a private copy of
      `setupTestStore`, delete the copy — the regenerated setup-only file
      reintroduces the helper and the package otherwise fails to compile
      with a duplicate symbol.
- [ ] If using the invitations bridge: configure `WithAllowedFrontends`
      for strict redirect validation and `WithEmailer` if invitation email
      delivery is wanted.

## v0.3.1 — 2026-06-11

Fixes for everything the first real consumer migration (segovia → v0.3.0)
surfaced. (#23)

### Added
- Shipped oauthaccounts spec gains `GetByProvider`, `ListByUser`,
  `DeleteByUserAndProvider`.
- Bridge e2e generation prints a per-bridge skip reason instead of emitting
  nothing silently.
- Scaffolded Makefiles gain gopernicus targets.

### Fixed
- Shipped serviceaccounts `GetPrincipalInfo` wraps
  `COALESCE(owner_user_id, '')` — the bare nullable select failed row scans.
- Integration-test emitter: only a method literally named `Get` drives
  Get-roundtrip probes; unusable probes regenerate setup-only with a printed
  reason; scope args reading fixture-NULL self-referential FK columns
  suppress the probe needing them.
- Principal-inheritance fixtures insert a CHECK-valid `principal_type`.

### Consumer actions
- [ ] Repin to v0.3.1, regenerate, run the verification gate.
- [ ] If oauthaccounts or serviceaccounts specs were ejected to work around
      the above: delete the project-local queries.sql to re-adopt the fixed
      shipped specs, regenerate, diff.

## v0.3.0 — 2026-06-10

The generator ships in-framework. The standalone gopernicus-cli repo is
archived.

### Added
- Bootstrap via `go run github.com/gopernicus/gopernicus/workshop/gopernicus@latest init`;
  projects pin the generator with go.mod's `tool` directive
  (`go tool gopernicus <cmd>`).
- Satisfiers are generated (existing headerless copies are skipped, never
  overwritten).
- Feature entity queries.sql ship with the framework; a project-local file
  is an ejection that wins over the shipped spec.
- Bridge e2e tests emit next to each bridge with `testserver.ServeBridge`
  wiring (the old `workshop/testing/e2e/` layout is retired).

### Changed
- Invitations engine is self-contained: `NewInviter` takes
  `invitations.InvitationRepository` and speaks engine types — breaking for
  consumers that wired an Inviter against repository types.
- Legacy manifest shape and the `@database:` annotation are retired.

### Consumer actions
- [ ] Follow the full migration: this is the large one. The phase sequence
      (unbreak build → adopt pinned tool → regenerate and reconcile by file
      class → satisfier adoption → spec-ejection policy → verification
      gate) is the upgrading guide's long-form example.

## v0.2.0 — 2026-06-10

- The generator becomes a versioned artifact of the framework (codegen
  lifted into `workshop/codegen`, import-engines work, generation v2
  phase 0).

## Earlier (≤ v0.1.8)

Pre-changelog history; see git tags v0.0.x–v0.1.8.
