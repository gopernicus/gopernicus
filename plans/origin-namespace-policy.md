# Namespace origin policy (sdk/pkg/web + pockets/authentication) — issue #53

Status: RELEASED AND VERIFIED 2026-09-28. Both PRs merged and both module tags published. Owner requested the review fix, merging PRs #55/#56,
and publishing sdk v0.10.0 then authentication v0.16.0. Work runs in isolated clones;
the original checkout has unrelated user edits that must remain untouched.

## Review fix and release execution (2026-09-28)

1. SDK: require bracketed origin hosts to be valid IPv6 literals in both parsing paths.
   Add request and construction rejection tests, valid IPv6 preservation tests, and CORS
   actual/preflight regressions including non-default ports. Keep legacy exact-list semantics.
2. Authentication: replace the synthetic form table arm with mounted HTML form cases;
   add malformed bracketed origins to the mounted constructor gate test. Extend the existing
   TLS/cookie-jar CSRF flow to run with namespace policy admission as well as exact origins.
3. Format changed Go files with goimports. Run focused regressions, module build/test/vet,
   and make check. Obtain independent verification using the named project reviewer/verifier.
4. Commit/push each PR's changes without force-push. Bring the SDK fix into the stacked
   authentication branch. Retarget #56 to main before merging #55; preserve both branches.
5. Require CI green on the new PR heads; merge #55. Tag sdk/v0.10.0 from its main merge
   commit and push that single annotated tag. Cold-verify via public proxy/checksum DB.
6. Repin authentication to sdk v0.10.0 with GOWORK=off and tidy only that module.
   Verify standalone build/test/vet, final make check and CI, then merge #56.
7. Tag pockets/authentication/v0.16.0 from its main merge commit; cold-verify a consumer
   of both public modules. Record RELEASING.md entries and checksums/provenance, archive
   this plan under plans/, close #53 once published and verified. No deployment or downstream
   dependency upgrades are part of this work.

Verification log and final changed-file list will be recorded below as execution proceeds.


## Review folds (2026-09-28)

- CORS order: exact explicit entries / policy BEFORE a `*` entry — a policy match is credentialed
  even when `*` is configured. Tested, including preflight match/miss.
- `Ports` is exhaustive; the default port may be listed to admit portless origins beside others.
- Policy `Exact` entries are validated + case-normalized (legacy `AllowedOrigins` stays
  byte-exact). The policy rejects a repeated Origin for all its entries; legacy lists keep
  first-value semantics — exact-only configs unchanged.
- Parsing: host limited to LDH labels on the namespace path; comma/whitespace/percent refused;
  suffix labels LDH ASCII, numeric final label refused.
- `OriginPolicy.HasHTTPNamespace()`; authentication construction refuses an http namespace
  when `RuntimeMode == production` (mirrors the Secure-cookie check).
- Docs: threat model (every label = exact origin power; subdomain takeover; `Reserved` is a
  deny-list; no public-suffix check; keep session cookies host-only / no AUTH_COOKIE_DOMAIN).
- Auth threading: constructorConfig, WithBrowser, inbound options/adapterConfig,
  MutationSecurity, csrfConfig. Clone/snapshot funcs need no change (value is immutable). One
  end-to-end test through the root constructor. ParseEnvTags skips the unexported fields.
- Release: tag from the main merge commit; poll proxy `.info` before tidy; merge/tag/push as
  separate steps; repin with GOWORK=off; retarget PR 2 to main before merging PR 1, never
  --delete-branch the stacked base; RELEASING.md entry per tag. examples/* not bumped.
- Deferred: debug-level denial reasons (observability) — not in this release.

## Problem

A host serves one SPA build on `{client}.flight.deck.gpsimpact.com` and calls one API with the
host-only session cookie. Every browser-origin gate takes an exact list, so each new client is a
config edit + redeploy:

- `sdk/pkg/web` `CORSConfig.AllowedOrigins` → `matchOrigin` (`middleware.go:264`). Only exact
  entries get `Access-Control-Allow-Credentials`; `*` never does.
- `pockets/authentication` `BrowserConfig.AllowedOrigins` (`config.go:645`) → inbound
  `MutationSecurity.AllowedOrigins` → `browserOriginAllowed`/`originAllowed`
  (`inbound/http/security.go:161,252`), behind three call sites: the browser-safe-mutation (CSRF)
  gate (`security.go:97`), the credential-establishment gate (`security.go:141`), and HTML form
  posts (`forms.go:87`).

## Design

### D1. Home: `sdk/pkg/web`, not a new `pkg/origins`

`pkg/` is flat (a pkg imports the root only), so `web` cannot import a sibling `origins` package.
The policy lives in `web` as `web.OriginPolicy`. The auth pocket already imports `sdk/pkg/web`, so
no new edge.

### D2. API

```go
// OriginNamespace admits exactly ONE DNS label directly under Suffix.
type OriginNamespace struct {
	Scheme   string   // "https" or "http"
	Suffix   string   // "flight.deck.gpsimpact.com" (≥ 2 labels, no wildcard, no trailing dot)
	Ports    []int    // nil → scheme default only (443/80)
	Reserved []string // labels that are NOT namespace matches ("accounts", "api")
}

type OriginPolicyConfig struct {
	Exact      []string          // exact origins, byte-exact match (today's semantics)
	Namespaces []OriginNamespace
}

// OriginPolicy is immutable after construction. Zero value admits nothing.
type OriginPolicy struct{ /* unexported */ }

func NewOriginPolicy(cfg OriginPolicyConfig) (OriginPolicy, error)
func ParseOriginNamespace(pattern string) (OriginNamespace, error) // "https://*.flight.deck.gpsimpact.com[:port]"
func (p OriginPolicy) AllowsRequest(r *http.Request) bool
func (p OriginPolicy) IsZero() bool
```

Construction rejects: `*` in `Exact` (the policy is credentialed admission only; CORS `*` stays on
`AllowedOrigins`), malformed exact origins, unknown scheme, single-label or wildcard suffixes,
out-of-range ports, invalid reserved labels. Inputs are lowercased and cloned.

### D3. Request matching (strict)

1. Exact entries: first `Origin` value compared byte-exact — unchanged from today.
2. Namespace entries, only if (1) missed. Deny when: more than one `Origin` header value; `null`;
   not `scheme://host[:port]` (userinfo, path, query, fragment all rejected); trailing dot; host
   not ending in `"." + suffix` on a label boundary (`evilflight…`, `x.flight….evil.com`); label
   count ≠ 1 before the suffix; label not LDH 1–63 without leading/trailing hyphen; label in
   `Reserved`; explicit port not in `Ports`, or an explicit default port (browsers never serialize
   it). Host is case-normalized before comparison. No forwarded-header input.

Repeated-Origin rejection happens only on the namespace path, so an exact-only config is
byte-for-byte unchanged (acceptance #1).

### D4. `sdk/pkg/web` CORS

`CORSConfig.OriginPolicy web.OriginPolicy` (additive). Order: existing `AllowedOrigins` loop, then
the policy. A policy match is **credentialed**: `Access-Control-Allow-Origin: <exact request
Origin>` + `Allow-Credentials: true`, never `*`. `Vary: Origin` already written on every request.
Preflight path unchanged (no cookie, no lookup).

### D5. `pockets/authentication`

- Root `BrowserConfig.OriginPolicy web.OriginPolicy` and inbound `BrowserConfig.OriginPolicy`,
  threaded through `constructor.go:550` → `adapter.go:108` `MutationSecurity.OriginPolicy` →
  `csrfConfig`. No env tag (hosts build it in code).
- `browserOriginAllowed(r, cfg)` admits when the exact list OR the policy admits. All three call
  sites switch to it. Sec-Fetch-Site handling is unchanged (`same-origin` passes; `same-site`
  sibling still needs an admitted Origin — namespace admission is exactly that).
- Union semantics when both `AllowedOrigins` and `OriginPolicy` are set (keeps
  `AUTH_ALLOWED_ORIGINS` working alongside a code-built policy). Admission only: grants nothing.

### D6. No-drift story

Host builds one `web.OriginPolicy` at boot and passes the same value to `web.CORSConfig` and
`authentication.BrowserConfig`. README snippet + a compiled example test in `sdk/pkg/web`.

## Tasks

1. `sdk/pkg/web/origin_policy.go` + `origin_policy_test.go`: D2/D3, table tests (valid label,
   uppercase, lookalike prefix, lookalike suffix, extra labels, zero labels, ports allowed/denied,
   explicit default port, null, trailing dot, userinfo/path/query/fragment, repeated Origin,
   reserved label, reserved label also listed exact → admitted, construction errors).
2. `sdk/pkg/web/middleware.go`: D4 + CORS table tests (credentialed echo, no `*`, Vary, preflight
   204, exact-only unchanged). Example test for D6.
3. Release sdk **v0.10.0** (minor, additive). Cold-verify.
4. `pockets/authentication`: repin sdk v0.10.0; D5 across `config.go`, `constructor.go`,
   `inbound/http/{options,adapter,security,forms}.go`; table tests across CSRF gate,
   credential-establishment gate, and form posts; exact-only regression tests stay green.
5. Docs: auth README (BrowserConfig table + SPA/CORS section at ~l.868), ARCHITECTURE.md
   `sdk/pkg/web` row, sdk README if it lists web surface.
6. Release `pockets/authentication` **v0.16.0** (minor, additive). Cold-verify; copy plan to
   `plans/`; comment + close #53.

Verify per module: `go build ./... && go test ./... && go vet ./...` in `sdk/` and
`pockets/authentication/`, plus `make` guards.

## Owner calls

- **YOUR CALL** D5 union vs. error when both `AllowedOrigins` and `OriginPolicy` are set.
  Recommendation: union.
- **YOUR CALL** D1 name/home: `web.OriginPolicy` (recommended) vs. the issue's `origins.Policy`
  (would need a pkg-layering exception).
- **YOUR CALL** one PR (sdk + auth, released in two tag steps) vs. two stacked PRs.
  Recommendation: two stacked PRs, since auth must pin the published sdk tag.
- Out of scope: env-var syntax for namespaces (`AUTH_ALLOWED_ORIGINS=https://*.x`). Host parses
  its own env with `ParseOriginNamespace` if it wants that.

## Execution evidence

- SDK fix commit: f6b9cba1bec355b644dfe89ce7f19812f1f0f79b; 14 new regressions failed
  before the fix and passed after. SDK build/test/vet and full make check passed.
- Authentication regressions: 42a302f5, integrated SDK at 98131d386ad825d8e7569421c9d32ee940ae948c.
  Uncached module tests/build/vet passed; mounted HTML login cases and exact/namespace TLS
  CSRF password-change flows passed. Independent verifier and SRE review found no blocker.
- Original real-HTTP malformed-origin reproduction now passes: both bracketed DNS cases
  receive 403 with no credentialed CORS headers.
- PR #56 retargeted to main; both updated PR heads pushed. No branch deletion or force push.

### SDK published

- PR #55 merged as fb2e8cd0e3b8b8dc4ecc28ae8c2acb53d603d60d. Both PR CI runs passed.
- make check passed on that exact main merge commit; annotated sdk/v0.10.0 published.
- Public proxy .info resolved immediately to the merge commit. Fresh-module-cache consumer
  build, runtime tests, all published SDK package tests, and go mod verify passed with
  GOWORK=off, GOPROXY=https://proxy.golang.org and GOSUMDB=sum.golang.org.
- SDK module checksum: h1:2Olg4g71pOAij0mz+mBGe+meXWreQW268bTPXp+wkSs=.
- Authentication now pins sdk v0.10.0; standalone build, uncached tests and vet passed with GOWORK=off; final PR CI and make check on the merge commit passed.

### Authentication published; release complete

- PR #56 merged as d5c962d6e67ef53fbd7f471d7914a5fffb634815 after both final CI checks passed.
- make check passed on that exact merge commit; annotated pockets/authentication/v0.16.0
  published. Public proxy .info immediately resolved the expected merge commit.
- Fresh-module-cache consumer built and tested both browser-policy APIs, all published
  authentication package tests passed, and go mod verify passed. No workspace, replacements,
  private proxy, or checksum exemptions were used.
- Authentication module checksum: h1:yYvP1F+a4tjPj/7vsM2HG/Kh3hh3g5FCTL4a+hC3vJ8=.
- Final changed-file list, public checksums and CI links are in the adjacent release manifest.
- Unresolved failures: none. Browser automation and live datastore services were not exercised;
  real HTTP/TLS handlers, in-memory fixtures and cookie jars were exercised.
- Downstream adoption remains separate: use sdk v0.10.0 and pockets/authentication v0.16.0.
