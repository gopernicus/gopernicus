# OAuth and tracing implementation

Status: COMPLETE — 2026-09-10. Parent: [framework-audit.md](framework-audit.md).
Implements the approved [S9c audit](framework-audit-oauth-tracing.md), including
the owner's requirement that terminal/mobile clients work without cookies.

## Preconditions and boundaries

Branch/base firestore-authentication / 6807ed06; 723 pre-existing dirty entries.
/tmp/gopernicus-oauth-tracing-implementation-baseline.json records 2005 visible
files. Compare task-relative hashes, preserve prior audits/user work. Go 1.26.1,
41 modules, GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; goimports at
/Users/jrazmi/go/bin/goimports. No root go.mod or task services. Never manually
edit generated *_templ.go/dist. No external consumer writes, real OAuth login,
credentials, actual email, collector export, publishing or module version changes.
Use owned OIDC/HTTP peers, fake transports and in-memory exporters for verification.
Named backend/platform roles review only; their unavailable opus model is replaced
by the inherited available model without changing their duties.

## Intended contracts

- OAuth Provider keeps Name, authorization URL, code exchange and userinfo.
  An explicit AuthorizationRequest replaces positional URL arguments; construction
  errors are returned. IDTokenValidator and TokenRefresher are optional interfaces.
  Remove SupportsOIDC, provider-wide TrustEmailVerification and SDK ProviderConfig.
  Preserve OIDC verification and refuse a token containing IDToken without a
  validator. Provider-specific config owns credentials, HTTP client, scopes and
  Google consent/offline options; SDK stays stdlib-only.
- UserInfo/IDTokenClaims preserve raw EmailVerified and expose per-identity
  EmailAuthoritative separately. Google derives authority from verified Gmail or
  verified Workspace hd; GitHub does not claim authority. Authentication takes
  an explicit host TrustOAuthEmail policy; nil denies new email-based adoption/
  registration, while existing linked-ID login remains available. The existing
  mailed proof for linking to an already claimed email is preserved.
- Start returns a typed flow result: authorization URL, public state, independent
  completion secret and expiry. Start/callback requests carry explicit browser or
  native mode; the stored transaction binds mode, provider redirect URI and proof.
  Browser handlers store the proof in a per-state HttpOnly/SameSite=Lax cookie.
  Native start/completion use JSON and no cookies, with exact host-allowlisted
  native redirect URIs used in both authorization and exchange. Native mode is
  opt-in; no arbitrary callback URL or mode-based bypass.
- Use a domain-separated hash of public state + completion secret + mode/provider
  as the opaque repository lookup token. Wrong proof cannot consume a valid flow;
  the existing atomic Consume contract/schema remains unchanged. State lookup keys
  change, so old in-flight flows must restart at upgrade. Never send the completion
  secret in authorization/callback URLs. Browser completion cannot redeem native
  state. Cookie delivery and JSON credential delivery remain transport choices.
- Reject nil/duplicate/invalid provider wiring and callback URL shapes at pocket
  construction. Validate nonnil tokens and stable identity before account reads/
  writes; provider-specific missing/zero IDs fail in adapters. Keep token encryption,
  state expiry/replay protection and host domain policy.
- Provider HTTP clients copy host client configuration, clone scopes and disable
  redirects. Detect response overflow, validate success payloads and return safe
  inspectable errors preserving causes. Google keeps go-oidc signature/JWKS logic
  and explicitly preserves caller cancellation across verifier errors.
- Tracing keeps its small span interfaces and Noop. Defer HTTP completion, report
  write/flush failures and escaping panics without secret text, finish once and
  re-panic unchanged. Preserve committed status and Logger's original cause.
- OTLP sampling validates finite [0,1], treats literal zero as zero root sampling,
  retains env default 1 and uses ParentBased(TraceIDRatioBased). Stdout's real
  parent-based behavior is explicit; caller-owned provider mode stays host-owned.
- Add a bounded OTel HTTP middleware that reuses SDK completion, starts server
  spans and optionally accepts W3C remote parents by explicit host configuration.
  Keep SDK vendor-free, no global provider/propagator, no automatic baggage. Use
  typed current HTTP metadata in the adapter and document dashboard migration.

## Sequence

1. Review concrete flow/security and tracing contracts with named roles. Implement
   SDK OAuth contracts and providers with focused regression tests.
2. Implement shared bound auth flows, browser/native handlers, trust policy and
   construction validation. Migrate workspace callers/mocks/examples and exercise
   browser-transferred callbacks, native no-cookie completion, wrong proof/mode,
   replay/expiry, parallel flows, linked login and email/invitation policy.
3. Fix tracing completion/sampling and add owned HTTP propagation/server-span
   tests. Preserve streaming behavior and explicit resource shutdown.
4. Update canonical docs, examples, guards if needed, release notes and standalone
   AUDIT-015. Preserve AUDIT-001..014 bytes. Record exact API/migration and limits.
5. goimports; focused build/test/vet/race; meaningful owned HTTP/OIDC/export probes;
   full make check and docs-build; scoped module tidy where imports changed.
6. Resolve final source reviews, compare task-relative hashes/generated artifacts,
   close owned services, record inventory/commands/failures and master handoff.

## Implementation and review outcomes

All planned source changes are implemented. Concrete corrections adopted from
named backend/platform reviews:

- Browser proof security derives from parsed callback scheme (uppercase HTTPS
  still uses Secure and __Host-); cookies never inherit session domain/path.
- OIDC flow token omission fails; separate base/OIDC test fakes expose only real
  capabilities. Linked-ID login remains independent of email/host trust.
- Native JSON pending-link completion returns credentials without cookies.
  Exact host allowlists deliberately require configured loopback ports and
  provider-compatible registrations; device authorization remains separate.
- GitHub missing email permission (fully received 403) leaves stable identity
  usable, while canceled reads/overflow remain failures. Google bounds discovery
  and JWKS with a small transport body wrapper and honors cached-key cancellation.
- SDK HTTPTracer starts request-aware spans with metadata available to sampling;
  HTTPSpan receives native integer status. Completion is installed before metadata
  setup. OTel names unknown methods HTTP, recognizes QUERY, and records a fixed
  error.type. The docs describe a bounded semconv subset and finite ratio validation
  even for configuration modes that ignore the ratio.

Final platform review found no remaining material defect. Final backend review's
403 classification correction and discovery wrapper coverage are implemented.
No module requirements or dependency imports changed beyond stdlib and packages
already provided by existing dependencies; no tidy/version migration was needed.
No generated sources or UI assets were manually edited.

## Verification

All commands use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Formatter:
/Users/jrazmi/go/bin/goimports -w on task-relative non-generated Go files.
Loopback suites ran with approved execution after sandbox bind denial; no real
OAuth login, credentials, provider delivery, collector export or external-store
mutation occurred.

| Command | Result / evidence |
| --- | --- |
| Root: make check | PASS after the final GitHub correction; /tmp/gopernicus-oauth-tracing-final-make-check.log. Includes all 41 modules go build/test/vet, tagged compile checks, generated drift and architecture guards. |
| Root: go test -race -count=1 ./sdk/capabilities/oauth/... ./sdk/capabilities/tracing/... ./sdk/foundation/web/... ./integrations/oauth/google/... ./integrations/oauth/github/... ./integrations/tracing/otel/... ./pockets/authentication/... | PASS; /tmp/gopernicus-oauth-tracing-race.log |
| Root: go test -race -count=1 ./integrations/oauth/google/... ./integrations/oauth/github/... | PASS after the final correction: /tmp/gopernicus-oauth-tracing-final-provider-race.log |
| Root: make docs-build | PASS; pnpm typecheck + build, /tmp/gopernicus-oauth-tracing-docs-build.log. Nonblocking Docusaurus update-notifier permission message only. |
| Root: git diff --check | PASS |

Owned HTTP behavior includes browser callback theft followed by legitimate
completion in examples/auth-cms, independent parallel browser cookies, native
start/callback/pending-link verification with no cookie jar, no-store and replay
rejection. Service regressions cover wrong proof/mode/provider/redirect URI,
expiry, malformed identities before writes, no OIDC downgrade, nil host trust
and linked-ID login without email. Provider regressions cover signed claims,
Google cached cancellation/discovery overflow, redirect refusal, malformed
successes, safe errors and GitHub permission vs read failures. Tracing tests cover
write/flush errors, panics before/after commitment, abort/hijack, original logging
cause, exactly-once completion including metadata panic, typed server spans,
explicit/ignored/malformed remote parents, local parents, no baggage and zero/one
parent-based sampling.

Unverified by design: real provider/client registrations, mobile OS app-link or
loopback callback dispatch, CLI device grant (not implemented), collector export,
actual email receipt and live cloud/SQL datastore legs. Existing store schemas use
opaque text/document state keys; no schema migration. No persistent owned service
or external consumer edit. Consumer rollout must register native URIs, configure
trust/consent and restart pre-upgrade in-flight logins.

## Final inventory and handoff

AUDIT-015 is the consumer guide; preserve the AUDIT-001..014 byte prefix.
Next audit: S10 sdk/pocket host wiring/route composition, then full pockets.
All authorized work is complete; no unresolved check failure or persistent owned
service remains. Docusaurus update notification could not write its user config;
typechecking and static build succeeded. Full final gate and provider races pass.

Task-relative inventory: 52 files; 37 Go source/test files.
Compared SHA-256 values to the implementation baseline. Generated sources/assets,
module/workspace files and the full 141541-byte prior AUDIT prefix are unchanged.

```text
ARCHITECTURE.md
AUDIT.md
RELEASING.md
examples/auth-cms/README.md
examples/auth-cms/cmd/server/main.go
examples/auth-cms/cmd/server/oauth_link_settings_test.go
examples/auth-cms/cmd/server/oauthfake.go
examples/auth-cms/cmd/server/password_reset_link_test.go
examples/auth-cms/cmd/server/production_test.go
examples/cms/cmd/server/main.go
integrations/oauth/github/README.md
integrations/oauth/github/github.go
integrations/oauth/github/github_test.go
integrations/oauth/github/security_test.go
integrations/oauth/google/README.md
integrations/oauth/google/google.go
integrations/oauth/google/google_test.go
integrations/oauth/google/security_test.go
integrations/tracing/otel/README.md
integrations/tracing/otel/config.go
integrations/tracing/otel/http.go
integrations/tracing/otel/http_test.go
plans/framework-audit-oauth-tracing.md
plans/framework-audit.md
plans/oauth-tracing-implementation.md
pockets/authentication/README.md
pockets/authentication/auth_test.go
pockets/authentication/authentication.go
pockets/authentication/internal/inbound/authentication/oauth.go
pockets/authentication/internal/inbound/authentication/oauth_binding_test.go
pockets/authentication/internal/inbound/authentication/oauth_link_page_test.go
pockets/authentication/internal/inbound/authentication/oauth_test.go
pockets/authentication/internal/inbound/authentication/outcome_notice_test.go
pockets/authentication/internal/inbound/authentication/sessions.go
pockets/authentication/internal/logic/authsvc/oauth.go
pockets/authentication/internal/logic/authsvc/oauth_binding_test.go
pockets/authentication/internal/logic/authsvc/oauth_invitation_test.go
pockets/authentication/internal/logic/authsvc/oauth_link_test.go
pockets/authentication/internal/logic/authsvc/oauth_test.go
pockets/authentication/internal/logic/authsvc/securityevent_test.go
pockets/authentication/internal/logic/authsvc/service.go
pockets/authentication/oauth_config_test.go
pockets/authentication/security.go
sdk/README.md
sdk/capabilities/oauth/oauth.go
sdk/capabilities/tracing/completion_test.go
sdk/capabilities/tracing/middleware.go
sdk/capabilities/tracing/middleware_test.go
sdk/foundation/web/middleware.go
workshop/documentation/docs/integrations/catalog.md
workshop/documentation/docs/pockets/authentication.md
workshop/documentation/docs/sdk/capabilities.md
```
