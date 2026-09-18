# Login page respects disabled password flows

Status: VERIFIED, PUBLICATION APPROVED — 2026-09-18.

Three-sixty is a Google-only host. `PasswordFlowsDisabled` already removes the
password endpoints, but the bundled login page still offers a password form,
registration and password recovery. Fix the framework model and renderer so the
host can consume the bundled view without overriding `Login`.

## Contract and scope

- Add `LoginPage.PasswordFlowsDisabled`, derived from
  `!authService.PasswordFlowsEnabled()` for both GET and failed-form rendering.
  False preserves existing behavior for zero-value and custom page models.
- Hide the complete password form plus registration and password-recovery links
  when disabled. Keep notices, OAuth providers and configured passwordless login.
- Existing routes, authentication, consent, session separation/revocation and
  validated return-to handling retain their behavior. No datastore/schema work.
- Keep the core technology-neutral; templ remains in the Goth sibling module.
  Regenerate templ output with the pinned generator; never edit it manually.
- Paired patch candidates: authentication v0.13.2 and Goth views v0.5.1. The
  renderer must pin the fixed core. No store release or host override is needed.

## Preconditions and preservation

Start from main `305864e9a8e7715f9fe1c461e7d20206da7c9317` on an isolated branch.
Preserve unrelated edits in `plans/cacher-design.md`,
`plans/gps-360-go-audit-upgrade-handoff.md`,
`plans/segovia-v2-audit-upgrade-handoff.md` and `.github/scripts/__pycache__/`.
Do not change any host repository, credentials, production data or existing tags.

## Tasks

- [x] L1 Core model and both rendering paths; focused HTTP regression coverage
  proves the model follows configuration and disabled endpoints remain absent.
- [x] L2 Goth rendering and regression coverage for enabled/default, OAuth-only
  and passwordless-only pages, with notices and provider links preserved.
- [x] L3 Document the contract and paired adoption; run goimports, pinned templ
  generation, affected-module build/test/vet, full repository checks and a local
  browser check of the rendered page. Record architectural review.
- [ ] L4 Prepare independently verified module candidates and a consumer with
  `GOWORK=off`; publish the paired patches using the established release process
  and verify public checksums, archive source, module checks and CI.

## Verification and evidence

Evidence directory: `/tmp/gopernicus-authentication-login-posture`.
Do not request unpublished candidate versions from a public Go proxy. Use the
private file proxy for candidate checks; public verification starts after tags
exist and uses normal sum.golang.org validation without exemptions.

Changed-file scope: this plan/release receipt; core inbound view model, login GET
and failed-form handlers and focused tests; Goth credential template/generated
output and rendering tests; Goth minimum core dependency; relevant authentication
documentation and release/audit notes.

Live Claude/Google acceptance and three-sixty's `/account/keys` boundary remain
downstream checks; this presentation fix does not prove those host behaviors.

## Verification record

- Architecture-steward review: aligned, no blockers. Core is safely adoptable
  with existing views; the corrected Goth renderer carries the ordinary minimum
  core dependency. Both qualify as presentation patches under RELEASING.md.
- Core and Goth `go build ./...`, full `go test -race -count=1 ./...` and
  `go vet ./...` passed. goimports and whitespace checks passed.
- Full 42-module `make check` passed in 129.13 seconds, including pinned templ
  generation, generated-artifact checks, tagged checks and architecture guards.
- Documentation `pnpm typecheck` and `pnpm build` passed.
- Exact candidates (349 core entries, 36 view entries) passed independent
  `GOWORK=off` build/test/vet and tagged checks with no source transformations.
  The only dependency change is Goth's minimum core pin and corresponding sums.
- External consumer passed module verification, build, race tests and vet. Real
  mounted TLS requests cover default/password-enabled, OAuth-only,
  passwordless-only and OAuth/passwordless configurations, failed password login,
  sign-out notices, providers, CSRF/return-to data and absent password endpoints.
- Chrome inspection of actual consumer-captured HTML with bundled CSS confirmed
  Google-only and enabled-password layouts, passwordless-only controls and failed
  login notices. Temporary servers and browser tab were closed. External Google
  sign-in and live Claude were not exercised.
- Early Goth tests were blocked by missing unpublished module metadata; a
  private candidate proxy resolved this without public version probes or checksum
  exemptions. Temporary test-fixture corrections (OAuth start path and 405 for
  disabled POST /auth/login beside its mounted GET) needed no production change.
  An initial temporary preview asset path was corrected before visual verification.

Detailed commands, checksums and receipts are in
`plans/authentication-login-password-posture-release-manifest.json`.

The initial publication command was rejected by automatic approval review and
did not execute. Josh subsequently explicitly approved releasing both patches
("ok lets release it"). Source and candidates remain unchanged and verified.
