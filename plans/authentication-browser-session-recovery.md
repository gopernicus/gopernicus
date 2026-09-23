# Browser session recovery after access-token expiry

Status: COMPLETE — 2026-09-22. Published, public-verified and Linux CI passed.

## Finding and scope

The browser gate redirects an expired or missing access cookie to login even
while the fixed seven-day refresh session remains valid. The refresh cookie is
normally scoped to `/auth`; rotating at an arbitrary protected page would not
receive it. Rotation permits one concurrent grace use, so independently
refreshing several tabs can revoke an honest session.

Implement recovery at the existing login landing, using the existing HTTP
refresh endpoint. No poller, store/schema changes or session-lifetime changes
are needed. The user subsequently authorized exposing the custom-page helper
and committing, pushing, merging, tagging and releasing the paired modules.
Downstream repository edits and application deployment remain outside scope.

## Contract

- On an outermost credential-resolution failure, `Browser()` may mark its
  existing GET/HEAD login redirect with `recover=1`. Only a posture admitting
  first-party access cookies qualifies, and an authoritative bearer excludes
  recovery. Nested, liveness, kind/audience denials and unsafe methods keep
  their existing denial behavior. No submitted mutation is replayed.
- The login handler supplies an additive `LoginPage.SessionRecovery` model
  only for that marker with a nonempty first-party refresh cookie. Credentials
  never enter the page model. Ordinary sign-in and failed login forms keep
  their current presentation. Custom views can ignore the additive field.
- The bundled Goth login renders a nonce-authorized script and an accessible
  recovery status while retaining the sign-in form. Under one origin-wide
  Web Lock, check `/auth/me`, refresh only on 401, then recheck before returning
  to the validated destination. Read no token response body; retain HttpOnly
  cookies. Use same-origin fetch credentials, no cache and reject redirects.
- Preserve the registered login route's prefix in recovery endpoint URLs;
  never infer URLs from the refresh cookie path. Keep the existing origin
  check and refresh rate limits. Permit only same-origin fetches in Goth CSP.
- A per-tab, per-destination short cooldown bounds redirect loops (including
  mis-scoped access cookies). Failure, unavailable Web Locks/storage, disabled
  JS, 429, or infrastructure errors leave normal sign-in available. Failed
  recovery never clears shared cookies. All browser refresh clients sharing
  these cookies must use the same lock and recheck protocol.

## Preconditions and preservation

Initial branch: `main`; release branch: `authentication-browser-session-recovery`.
Preserve pre-existing edits to
`plans/cacher-design.md`, `plans/gps-360-go-audit-upgrade-handoff.md`,
`plans/segovia-v2-audit-upgrade-handoff.md`, and `.github/scripts/__pycache__/`.
Go 1.26.1 and goimports are available. Generate templ output with the pinned
tool; never hand-edit generated Go. Goth will pin the new core release.

## Tasks

- [x] R1 Core redirect eligibility, additive page model, login population and
  focused HTTP regression tests, including prefix and bearer isolation.
- [x] R2 Goth recovery script/rendering and CSP, with deterministic behavior
  tests and regenerated templ output.
- [x] R3 Real mounted-handler/browser proof for expiry, multiple tabs, invalid
  refresh, bounded failure, and ordinary login fallback. Run affected-module
  build/test/vet, race checks, formatting and repository gates.
- [x] R4 Document adoption and limitations; record exact verification and
  architectural review.
- [x] R5 Export `authgoth.SessionRecovery(model, nonce)` as a reusable templ
  component containing its status markup and script. It must need no Goth
  container or custom DOM hooks; install its interaction listener on the
  document. A nil model renders nothing. Bundled and custom login views use
  the same component after their sign-in controls. Add custom-page coverage,
  regenerate, run affected checks and re-exercise custom-page browser recovery.
- [x] R6 Prepare additive releases: authentication `v0.14.0`, Goth `v0.6.0`.
  Verify remote tag availability, pin Goth to the new core, and use exact
  nearest-module archives/private file proxy for unpublished candidate checks
  with `GOWORK=off`. Never request unpublished versions from the public proxy.
  Preserve unrelated owner changes and existing tags. Validate full repository,
  exact candidates and an external custom-page consumer before publication.
- [x] R7 Commit only this change on a release branch; push and merge into main
  using the established repository workflow, then publish annotated module
  tags and release notes. Verify CI, remote provenance, public Go checksums and
  fresh consumer adoption; record the release receipt and any propagation delay.

## Verification record

Passed:

- `goimports -w` on changed Go source; scoped `git diff --check`.
- Goth `go tool templ generate`, `go build ./...`, `go test ./...`,
  `go vet ./...`, and `go test -race ./...`. Node 24.0.1 executed the rendered
  JavaScript behavior tests; they were not skipped.
- Core `go test ./inbound/http ./`, targeted recovery regressions, and full
  `go test -race ./...`. Full core build/test/vet also passed in the full gate.
- `GOCACHE=/tmp/gopernicus-auth-recovery-gocache make check` in an exact source
  copy at `/tmp/gopernicus-browser-session-recovery/workspace`: all 42 modules
  passed vet/build/test, 28 guards passed, eight integration-tag and two
  integration/live-tag vet checks passed, and scaffold cache warming passed.
  The copy excludes `.git` so the existing checksum branch checks templ drift
  without staging generated files. All 92 generated files match the source
  checkout. Source `make guard-no-legacy-features-path` closes the gitless G20
  limitation; source UI asset diff passed separately.
- Chromium, Firefox and WebKit ran the actual mounted authentication handlers,
  actual JWT signer, Goth HTML/CSP and host memory repositories on localhost.
  Each engine passed: valid access; three expired-access tabs with exactly one
  rotation; missing access cookie after sleep with a prefixed mount; revoked
  refresh preserving shared cookies and usable sign-in; missing Web Locks;
  disabled JavaScript; mis-scoped access-cookie loop suppression; direct login.
  No page errors or recovery CSP violations occurred. The sign-in fallback
  screenshot was visually inspected. The temporary server was stopped and its
  test fixture removed from the repository.

Named `lead-backend-engineer` review confirmed the placement and admission
contract. Both requested corrections landed: escaped derived endpoint paths
(including an encoded-prefix regression), and a refreshed cooldown receipt
immediately before navigation to cover a slow lock/network round trip.

Early verification encountered a sandbox cache/socket restriction (resolved
with the temporary cache and authorized local test execution), a corrected
test-helper name, and a browser fixture that exhausted the normal login rate
limit when reused across engines. Fresh fixtures per engine passed without
changing production rate limits. No unresolved implementation/test failures.

Live PostgreSQL/remote Turso/Firestore suites were not run; env-gated store
tests skipped in the hermetic gate. No persistence code changed. Production and
downstream host adoption remain unverified. Both modules are published and fresh public-proxy verification passed.

The public `authgoth.SessionRecovery` helper is implemented and bundled Login
uses it. Its nil-model, external-package custom rendering and document-level
interaction tests pass. Final source generation, affected-module race tests
and independent architecture/SRE review passed. Exact private module archives
for core `v0.14.0` (350 entries) and Goth `v0.6.0` (41 entries, pinning the new
core) passed `go mod verify`, `go build ./...`, `go test -race -count=1 ./...`
and `go vet ./...` with `GOWORK=off`, no replacements and normal checksum
verification for published dependencies. The unpublished candidate checksums
were independently computed and seeded only into private candidate contexts.

The final source snapshot at `/tmp/gopernicus-session-recovery-release/workspace`
passed the full 42-module `make check`, all 28 guards, eight integration-tag
vets and two integration/live-tag vets. All 92 regenerated files match source;
source G20, asset diff and scoped whitespace checks passed. Candidate source
inventories and all five unrelated owner-file hashes remain unchanged.

Evidence: `/tmp/gopernicus-browser-session-recovery/` contains the full gate
log, browser runner and server fixture, per-engine JSON receipts and screenshots.

Changed files: this plan, its release manifest and `RELEASING.md`; authentication `README.md`,
`browser_login_path_test.go`; inbound `principal.go`, `principal_options.go`,
`html.go`, `views.go`, `browser_middleware_test.go`,
`principal_posture_test.go`, `session_recovery_test.go`; Goth
`credential.templ`, `credential_templ.go`, `session_recovery.templ`,
`session_recovery_templ.go`, `policy.go`, `policy_test.go`,
`session_recovery_test.go`, `session_recovery_external_test.go`,
`testdata/session_recovery.mjs`, `go.mod`, `go.sum`.

Next adoption step: upgrade both released modules in the affected host. Hosts
with a custom Login override include the exported recovery helper with the
provided model/nonce or delegate to the updated bundled Login view. Downstream
adoption and deployment were not requested in this release task.

Final external consumer proof passed with exact private candidate modules, no
replacements and `GOWORK=off`: module verify/build/uncached race tests/vet plus
Chromium, Firefox and WebKit. The fixture uses published SQLite/Turso storage,
real migrations, repositories, JWT and authentication services/HTTP handlers.
Its independent custom Login renders only the public helper after its own form,
without Goth layout/assets. Every engine passed three-tab expiry recovery with
one rotation, missing access with prefix, revoked refresh preserving cookies and
a successful manual sign-in, no JS/locks, bounded mis-scoped-cookie behavior and
ordinary nil-helper login. No page errors or CSP violations. The fallback
screenshot was visually inspected. Fixture servers and browsers were stopped.
Evidence and reusable public-proxy runner: `/tmp/gopernicus-session-recovery-release/`.

## Publication receipt

Source commit: `7d358447428dd46a0af527cff689ba82ec549955` on
`authentication-browser-session-recovery`, fast-forward merged into main from
`b7a4c99d336b0cb38fc1bf3a79cbf5ba67c8ca32`. Main, the release branch and both
annotated tags were pushed atomically. Remote main/branch and peeled tags match
the source commit; all 491 pre-existing remote tag refs are unchanged. Committed
nearest-module inventories (including inherited root LICENSE) match the tested
350 core and 41 Goth archive entries byte for byte.

Published tags: `pockets/authentication/v0.14.0` and
`pockets/authentication/views/goth/v0.6.0`. Fresh public Go module caches with
`GOWORK=off`, `GOPROXY=https://proxy.golang.org,direct` and `GOSUMDB=sum.golang.org`
verified both source origins and checksums without preseeded candidate hashes,
replacements or exemptions. Both modules passed verify/build/uncached race
tests/vet again. The separate public consumer began without a go.sum, fetched
both published versions and repeated its Go checks and all seven browser cases
in all three engines successfully. No propagation retry was needed. All
fixture processes exited cleanly. Full receipts, sums and command logs are
listed in the companion release manifest. All four GitHub Linux CI runs passed:
[main](https://github.com/gopernicus/gopernicus/actions/runs/35804533873),
[core tag](https://github.com/gopernicus/gopernicus/actions/runs/35804533734),
[Goth tag](https://github.com/gopernicus/gopernicus/actions/runs/35804533906), and
[release branch](https://github.com/gopernicus/gopernicus/actions/runs/35804533943).
The final verification receipt changes documentation only; published tags remain
on the verified source commit. All unrelated owner changes remain preserved.
