# Additive delegated audience admission

Status: PUBLISHED AND PUBLICLY VERIFIED — 2026-09-18.

Josh approved implementing and publishing authentication core v0.13.1 so
three-sixty can admit delegated API tokens on existing web/API-key routes using
the framework authenticator. No host credential-dispatch middleware is needed.

## Contract

- Add `DelegatedAudience(resources ...string)` to authentication `inbound/http`.
  It explicitly admits header-only delegated access tokens for an exact listed
  resource while preserving existing first-party and API-key admission policy.
- `Accept`, `Transports`, `FirstParty`, `Optional` and `Live` retain their
  existing meanings. Delegated tokens always require authoritative live checks,
  including nested admissions. Failed header authentication never falls back
  to cookies or anonymous admission.
- Preserve strict `Audience(...)`: it still applies to every credential and
  rejects audience-less web tokens and API keys. When both audience options are
  present, delegated credentials must satisfy both sets, independent of order.
- Repeated `DelegatedAudience` options replace that option's own set; validate
  and snapshot resources like `Audience`. Defaults remain unchanged. An outer
  gate must admit the credential before nested gates can narrow it.
- No token, OAuth endpoint, session, migration or repository contract changes.
  Existing store/view releases remain compatible and need no new tags or pins.
  Patch classification follows RELEASING.md's optional, independently adoptable
  host-configuration rule.

## Preconditions and preservation

Baseline main: `bb1cc5b04ae2e20561ea9a5b4215f514de275e07`. Check remote main and
absence of `pockets/authentication/v0.13.1` before publishing. Preserve and exclude
`plans/cacher-design.md`, `plans/gps-360-go-audit-upgrade-handoff.md`,
`plans/segovia-v2-audit-upgrade-handoff.md` and `.github/scripts/__pycache__/`.
Use an isolated feature branch and ordinary fast-forward/tag pushes.

## Tasks and verification

- [x] A1 Implement the option and update API/host documentation; review boundary
  and composition semantics with the repository architecture steward.
- [x] A2 Regression tests for mixed web/API-key/delegated admission, exact and
  wrong audiences, strict/combined options, kinds/transports/first-party gates,
  invalid credentials and no fallback, nested policies/revocation, and independent
  connection revocation. Exercise actual HTTP requests through the real service.
- [x] A3 Run goimports; core build, full race tests and vet; full `make check`.
  Verify the exact core candidate independently with `GOWORK=off`, and an external
  consumer with the already published store/view releases. No dependency changes.
- [x] A4 Commit only owned files; confirm candidate entries equal committed source;
  fast-forward main and publish only the annotated core v0.13.1 tag. Verify public
  checksums, origins, module/consumer checks and GitHub CI; record the receipt.

Do not request the unpublished version from a public Go proxy: prepare its private
file-proxy candidate first, avoiding prepublication negative caches. Public checks
must use normal sum.golang.org verification without exemptions or replacements.
Use existing published versions for unchanged modules. No production host changes.

## Changed-file scope

- `pockets/authentication/inbound/http/principal_options.go`
- `pockets/authentication/inbound/http/principal.go` (API comments)
- `pockets/authentication/inbound/http/delegated_audience_test.go` (new)
- `pockets/authentication/README.md`
- `workshop/documentation/docs/pockets/authentication.md`
- `AUDIT.md`, `RELEASING.md`, this plan and its release manifest

Evidence directory: `/tmp/gopernicus-authentication-delegated-audience-release`.

## Verification

- Repository architecture-steward review: aligned, no blockers. Existing
  authenticator owns the admission policy; no new dependency boundary.
- Nine focused regression tests passed under the race detector, including
  actual loopback HTTP requests. An initial sandbox listener denial was resolved
  by running the disposable listener tests with execution permission.
- Core `go build ./...`, `go test -race -count=1 ./...` and `go vet ./...` passed.
- Full 42-module `make check` passed in 136.82 seconds on the frozen working tree,
  including generated-artifact gates, tagged vet and all architecture guards.
  Release source is compared with the candidate and committed Git blobs before tagging.
- Documentation `pnpm typecheck` and `pnpm build` passed. The Docusaurus update
  notifier's config-permission warning did not affect either check.
- Exact core candidate: 348 entries, no manifest transformations, unchanged
  dependencies; independent build/test/vet and tagged compile/vet passed.
- External consumer passed build, race tests, vet, module verification and graph
  checks with the existing published adapters/views. Its 21 actual HTTP cases use
  real JWT signing and local SQLite, covering shared/strict admission, header
  authority, nested policies and independent revocation.
- Candidate verification used a private file proxy and locally calculated core
  checksums seeded only into temporary contexts. All checksum exemptions stayed
  empty; published siblings retained normal verification. Public core verification
  must start fresh after publication.

This patch adds no UI or schema behavior. Browser/Claude and hosted-database
acceptance were not rerun; three-sixty's live integration remains separate.
Durable checksums, commands and outcomes are in the release manifest.

## Publication receipt

Published annotated tag `pockets/authentication/v0.13.1` from main commit
`9a9482ddf06512f5f0b5a5beb58d3b60ce5764ba` (tag object
`a2f208f3543a9c3fda3c10786f3b1441dc431231`). All 483 preexisting remote tag
refs are unchanged. Only the authentication core was released.

- Public module checksum: `h1:70KYEQKl36sEC+Iqs9lzygtxRzg4I7C3x6QH163F6b0=`.
- Public go.mod checksum: `h1:V4s8PuUv/BPWtuLigf9aVCYEcSA+tklqVUnMnTkxvg4=`.
- All 348 public archive entries match both the candidate and committed source.
  Public Git origin identifies the exact release commit, tag and module subdirectory.
- Public download passed on its first attempt with normal `sum.golang.org`
  verification, no checksum exemptions, and no preseeded core checksums. The core
  download context/cache started fresh; previously verified external dependency
  cache entries were reused. No transient errors occurred.
- Published core build/test/vet and tagged compile/vet passed independently with
  `GOWORK=off`. The published consumer passed build/race tests/vet/module
  verification and all 21 actual HTTP cases, without replacements.
- GitHub [main check](https://github.com/gopernicus/gopernicus/actions/runs/35386917488),
  [release-tag check](https://github.com/gopernicus/gopernicus/actions/runs/35387470883)
  and [docs deployment](https://github.com/gopernicus/gopernicus/actions/runs/35386917492)
  all passed.
- Owner files remain byte-identical and excluded from the release commits.

Three-sixty can upgrade the core:

```sh
go get github.com/gopernicus/gopernicus/pockets/authentication@v0.13.1
```

Use `authHTTP.RequirePrincipal(authenticationhttp.DelegatedAudience(apiResource))`
on shared API routes, with delegated token verification configured as required by
v0.13.0. Keep strict `Audience(...)` for resource-only routes and `FirstParty()`
where delegated tokens must be denied. No additional migration or store/view pin
change is required for this patch. Three-sixty wiring, deployment and live Claude
acceptance remain host work.
