# SDK audit S9c: OAuth and tracing

Status: REVIEW COMPLETE — 2026-09-10. Parent: [framework-audit.md](framework-audit.md).

Approved fixes are implemented in [oauth-tracing-implementation.md](oauth-tracing-implementation.md);
this file retains the pre-implementation findings and probes. Consumer migration is AUDIT-015.
Review-only slice following completed email/notify implementation. Record findings
and recommended changes before implementation; AUDIT.md contains implemented
migrations only and remains through AUDIT-014 during this review.

## Preconditions and scope

- Branch/base: firestore-authentication / 6807ed06; 722 prior dirty entries.
  /tmp/gopernicus-oauth-tracing-review-baseline.json records 2004 visible files.
  Preserve user/prior audit work; compare task-relative hashes, not the HEAD diff.
- Go 1.26.1, 41 workspace modules, no root go.mod. GOCACHE is
  /tmp/gopernicus-audit-s1.aMLwCQ/cache. Formatter is /Users/jrazmi/go/bin/goimports.
- Review SDK oauth/tracing, GitHub/Google OAuth adapters, OpenTelemetry adapter,
  narrow authentication/web/logging consumers and representative host wiring.
  Full authentication/pocket workflow audits remain separate.
- No real OAuth login, credentials, external provider/collector calls, consumer
  edits, production mutations, publishing, generated-file edits or source fixes.
  Behavior probes use synthetic data, owned loopback peers or fake transports.
- Named lead-backend-engineer and platform-sre roles are read-only. Their configured
  opus model is unavailable here; use the inherited model without changing duties.
  No task services are required initially; close all owned probe listeners.

## Review sequence

1. Read SDK ports and concrete adapters, tests, docs and host callers. Separate
   platform mechanism from host account/trust/provider/sampling policy.
2. Trace OAuth state/PKCE/nonce and profile identity guarantees, transport ownership,
   cancellation, response limits, redirection, diagnostics and OIDC validation.
   Check which required methods are real common capabilities versus optional ones.
3. Trace span lifecycle, HTTP status/panic behavior, request propagation, logging
   identity, sampling/exporter configuration and concrete resource ownership.
4. Compare original framework and representative consumers before recommending
   removals. Keep useful unfinished extension points with an explicit use case.
5. Verify uncertain protocol/library facts against primary documentation. Reproduce
   important findings with local probes; run scoped build/test/vet and race tests.
6. Record ranked defects, simplifications, preserved behavior, migration costs,
   source-review findings and limits. Update the master handoff; leave proposals
   out of AUDIT.md until implementation.

## Verdict and package boundaries

Keep both capabilities. OAuth has useful shared PKCE/protocol vocabulary and real
provider implementations; tracing already has a small useful span port and Noop.
Neither belongs in root SDK utilities. The larger opportunities are correctness,
removing unnecessary OAuth requirements, and making host policy explicit.

These are findings and recommendations, not implemented fixes. SDK/pocket/provider
source remains at the review baseline. The prior email/notify implementation and
AUDIT-014 remain complete and unchanged.

## Confirmed findings, in fix order

### 1. P1: OAuth flow state is not bound to the initiating browser

Source: authentication/internal/inbound/authentication/oauth.go:52–83 mounts start
and callback without browser binding, redirects start without a flow cookie, and
sets session cookies from callback code/state. internal/logic/authsvc/oauth.go:108
stores verifier, nonce, redirect and optional linking user; :209–226 consumes state
and exchanges the code without proof of the initiating browser. The usual origin
and credential middleware do not cover these two routes.

An owned handler probe started a flow, then presented its callback URL in a separate
cookie-free request: start set zero cookies; callback returned 302 and minted a
session. The fake provider supplied a valid synthetic identity; the actual pocket
handlers/service and in-memory repositories performed the rest. No real browser
or Google login was used. An attacker who transfers their complete unused callback
URL can make a victim browser log into the attacker's account. This is login CSRF,
not proof that an arbitrary victim account can be taken over.

PKCE and nonce remain necessary, but global server-side state binds them to the
transaction, not its initiating browser. [RFC 9700 §2.1.1](https://www.rfc-editor.org/rfc/rfc9700.html#section-2.1.1)
requires the user-agent binding too. Preserve state expiry/atomic consumption and
add a browser-bound flow proof checked before consuming/exchanging. Cover normal,
missing, mismatched, expired and transferred flow proofs, replay and parallel tabs.
Explicit linking needs the same review. Keep non-browser/custom-host use explicit,
as clarified below; a cookie requirement belongs to the browser adapter.
Migration must address flow cookies/mount paths and invalidation of old in-flight
flows; do not silently accept unbound legacy states to preserve compatibility.

#### Owner clarification: terminal and mobile clients

The owner explicitly raised non-browser clients after this review. Generalize the
requirement to binding completion to the initiating client transaction. The
confirmed defect above concerns the bundled browser route that sets session
cookies. Cookies and browser-only redirects are not requirements of the SDK
OAuth port or every authentication use case.

- Browser host: bind its server-managed transaction to a browser flow cookie or
  equivalent session proof; completion can set session cookies.
- Native mobile/desktop client doing provider code exchange: use the system
  authorization agent, a registered app/claimed-HTTPS or desktop loopback callback,
  client-held PKCE verifier and expected state. Provider registration and redirect
  policy remain explicit. See [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252.html).
- Mobile/CLI using Gopernicus as a backend broker: when the server holds the
  provider verifier, it does not alone bind result delivery to the app. Require an
  independent client-held flow proof or a verifier-bound, one-time handoff
  redemption. Keep that proof out of the transferable authorization/callback URL;
  release credentials through the validated completion exchange. The original
  mobile flow secret illustrates this seam but is not an audited design to copy.
- Terminal without a suitable callback: a provider-supported device authorization
  flow can display a URL/user code while the CLI retains the device code and polls
  for completion. This grant has a different lifecycle and no redirect callback
  into the CLI. It is an optional capability, not currently implemented here.
  See [RFC 8628](https://www.rfc-editor.org/rfc/rfc8628.html).

Keep protocol mechanisms in SDK/adapters, single-use transaction and proof
validation in authentication, and cookie/JSON/deep-link/loopback handling at the
appropriate transport boundary. The host explicitly selects a supported flow at
initiation; that mode and its completion proof remain bound to the transaction.
Native support must not become a switch that skips browser validation. Exact APIs
and enabled flows remain implementation decisions; this clarification does not
authorize building every grant type or require implementing device flow now.
Only the audit plans changed; no runtime tests were needed for this design update.

### 2. P1: Google email trust overstates what the identity proves

Source: integrations/oauth/google/google.go:127–129 returns provider-wide trust=true;
:219–244 reads email_verified but discards Google's hd claim. sdk oauth claims have
no representation for the relevant per-identity distinction. authsvc/oauth.go:273
uses trust plus EmailVerified; :473–496 creates a VERIFIED login/recovery/notification
identifier and resolves pending invitations before issuing the session.

A signed local OIDC token with verified=true, a third-party email and no hd was
accepted with provider trust=true. Google distinguishes Gmail/Workspace authority
from third-party email addresses: a verified third-party address can have changed
owners since Google verified it. [Google verification guidance](https://developers.google.com/identity/gsi/web/guides/verify-google-id-token)
recommends another challenge in that case.

Important limit: existing unlinked accounts are not automatically merged. They
require a fresh mailed pending-link confirmation at authsvc/oauth.go:281–295;
already-linked login keys on stable provider ID. The exposed branch is new account
creation and any invitation grants the host associates with that email address.

Separate truthful per-identity assurance from host acceptance policy. Preserve the
raw claim meaning if exposed; add the narrow information needed to distinguish
verified claims from authoritative mailbox control, and have the host explicitly
choose what authentication accepts. Do not merely flip all Google identities to
untrusted, or silently start trusting GitHub because its primary email is verified.
The precise assurance/config shape belongs in the implementation plan. Recheck
registration, linking, recovery and invitation behavior together when implementing.

### 3. P1: OAuth HTTP redirects can replay client credentials

Source: GitHub github.go:90–103/254–275 and Google google.go:96–104/273–294 retain a
client whose default redirect policy follows responses. POST bodies contain the
client secret, authorization code/verifier or refresh token. A fake RoundTripper
returning 307 from the GitHub token endpoint to a different HTTP origin received
the replayed client secret in the second request. No external request was made.
Google has the same HTTP construction/policy by source; its redirect path was not
separately probed. A redirect response is the precondition; this does not claim
Google/GitHub currently redirect valid token exchanges that way.

Use an adapter-owned copy of host HTTP settings and refuse redirects for sensitive
provider exchanges, including discovery/JWKS policy as appropriate. Keep custom
transports/timeouts host-owned, preserve context, and never mutate the caller's
client policy. Validate configured endpoints before use. Audit bearer-token GET
redirects at the same time. A reusable global OAuth HTTP client framework is not
needed for these two adapters.

### 4. P2: malformed successful responses become invalid identities/tokens

Source: Google google.go:175–203 and :237–243; GitHub github.go:151–157 and :164–205.
Both ExchangeCode methods accepted HTTP 200 {} as an empty token response. Google
userinfo {} became an empty subject. A locally signed Google ID token without sub
passed validation; the pinned go-oidc verifies cryptographic/issuer/audience/expiry
properties but does not enforce a nonempty subject. GitHub profile {} plus a primary
email became provider ID "0" because a missing int field defaults to zero.

[OIDC's identity definition](https://openid.net/specs/openid-connect-core-1_0.html#IDToken)
requires a subject; shared identifier strings must not stand in for absent IDs.
Adapters should validate provider-specific success fields and reject missing/null/
invalid identities and tokens. In authentication, readIdentity (:683–695) also
needs nonnil results and a nonempty stable identity before lookup or writes.
oauthaccount.New rejects blank IDs, but registerAndLink first writes the user and
verified primary identifier (:479), then linking fails (:483); that ordering can
leave an orphan account. This consequence is source-reviewed, not separately
executed. The string "0" passes the generic domain check and needs GitHub-specific
rejection. General multi-repository transaction design remains the pocket audit.

### 5. P2: error boundaries expose provider bodies and lose a cancellation cause

GitHub github.go:148/242/273/297 and Google google.go:292/316 place provider response
bodies or descriptions into returned error strings. Synthetic sensitive markers
were reproduced in both adapters' returned errors. This is a diagnostic disclosure
surface; this slice did not establish a public HTTP leak through authentication.
Use safe inspectable status/OAuth error data, stable SDK classifications where
appropriate, and preserved underlying transport causes. Avoid classifying provider
credential failures as an end user's authentication failure.

A canceled cold-JWKS Google validation returned an error for which
errors.Is(err, context.Canceled) was false. The pinned go-oidc Verify path formats
the key-set error using %v; the adapter's outer %w cannot restore that cause.
Preserve caller cancellation explicitly around verification without masking a
separate relevant error. Do not require cancellation of a shared library key
refresh that legitimately serves other callers; the configured HTTP client still
bounds that work. Both adapters' ordinary HTTP requests already carry context.

### 6. P2: tracing misses real HTTP failures

Source: sdk/capabilities/tracing/middleware.go:76–82. Completion runs only after a
normal handler return and checks status alone, never StatusRecorder.Err(). With
Tracing → Logger → Panics, an owned OTel span-recorder probe produced:

| Trigger | Exported observation |
| --- | --- |
| Write or flush error | HTTP 200, span status Unset, zero error events |
| Ordinary panic before headers | HTTP 500/Error, correct |
| Panic after headers | No status attribute, Unset, zero error events |
| http.ErrAbortHandler before headers | No status attribute, Unset, zero error events |

Use deferred completion, report response failures and escaping panics safely,
finish exactly once and re-panic with the original value. Do not copy arbitrary
panic/error text into telemetry. Do not invent an HTTP 500 or 200 on an uncommitted
abort: the recorder may need a small committed-state accessor, or omit unknown
status. Preserve the original response error for Logger and streaming interfaces.

### 7. P2: invalid sampling ratios are accepted; documented behavior is inconsistent

Source: integrations/tracing/otel/config.go:57–60/95–96/166. OTLP construction accepted
-1, 2, NaN and +Inf; negative becomes full sampling. Validate finite [0,1] input
before constructing exporter resources. This is separate from zero→1, which is
explicitly documented today and therefore a migration decision.

Raw TraceIDRatioBased ignores parent decisions by design. The probe sampled a
child of an unsampled parent at .25. Stdout instead uses OTel's parent-based
default and emitted nothing for an unsampled parent, contradicting its always-
samples comment. Prefer explicit ParentBased(TraceIDRatioBased(rate)) for the
convenience OTLP path; custom caller-owned providers retain their sampler.
[OpenTelemetry Go sampling guidance](https://opentelemetry.io/docs/languages/go/sampling/)
recommends this composition. Document actual stdout behavior.

Simplest proposed zero migration: retain float64, remove zero normalization for
OTLP, and keep the environment tag's default 1. Literal 0 means zero root sampling;
OTLP struct literals omitting SampleRate must explicitly add 1 to preserve full
sampling. Zero Config still selects the usable stdout dev exporter. A *float64
would require broadening environment.ParseEnvTags, which currently rejects pointer
fields; that is unnecessary scope for this adapter.

## Smaller findings and intentional gaps

- GitHub and Google retain caller scopes slices. A GitHub probe changed the next
  authorization URL by mutating the original slice after construction. Clone owned
  configuration; state who owns any shared HTTP transport. No scope race was run.
- Both adapters use LimitReader(max) then Unmarshal, without detecting overflow.
  A GitHub body with a valid JSON prefix, padding to the 1 MiB limit and trailing
  invalid bytes was accepted. Memory is bounded, but an oversized/truncated response
  is not rejected explicitly. Read max+1 and reject overflow before decoding.
- Authentication provider wiring at authsvc/service.go:551 silently overwrites
  duplicate names and panics on nil entries; public construction does not reject
  those entries. OAuthCallbackBase is concatenated at oauth.go:710 without portable
  URL-shape checks. Validate at construction, preserving host-selected origin and
  mount prefix and local development policy. Source findings, not runtime probes.
- Google's access_type=offline and prompt=consent are hard-coded even for hosts
  that do not store provider tokens. Give these provider-specific consent choices
  explicit host configuration rather than coupling all login to offline access.
- SDK ProviderConfig is used as internal storage by two adapters, not accepted by
  their public constructors. It also duplicates endpoint structs; Google's stored
  endpoints/JWKSURL do not control the discovery verifier. Prefer concrete adapter
  config/private fields and remove unused exported vocabulary after migration scan.
- The discarded GenerateCodeChallenge call at authsvc/oauth.go:182 does no work
  the caller uses; providers already derive the challenge. Remove it during cleanup.
- Google README/code say there is no x/oauth2 dependency, but go.mod includes it
  transitively through go-oidc. Correct the description; this is not an SDK stdlib
  violation and does not justify replacing the verifier with handwritten crypto.
- Distributed HTTP tracing is incomplete: Middleware ignores traceparent and starts
  a generic internal span. A valid incoming traceparent became an unrelated root.
  This is a missing HTTP integration capability, not a defect in generic StartSpan.
  Preserve the generic port and add a bounded optional HTTP propagation/server-span
  seam only with explicit host policy. Avoid globals and a broad new tracing DSL.
  Current string-only legacy HTTP attributes also differ from current typed OTel
  conventions; handle that migration with the HTTP seam, including dashboard keys.
  [OpenTelemetry HTTP conventions](https://opentelemetry.io/docs/specs/semconv/http/http-spans/)
  describe server spans and integer response status. No automatic baggage or new
  captured personal data should be added as incidental cleanup.

## Proposed simplification and implementation order

1. Fix initiating-client transaction binding, truthful email assurance and redirect
   handling first. Make an implementation plan with testable browser-cookie and
   non-browser completion-proof contracts, plus explicit host policy.
   These correctness issues cross the SDK/auth seam and should not wait for the
   full authentication audit. Preserve the fresh-mail existing-account link proof.
2. Validate identities/tokens before account mutation; tighten response/error and
   constructor ownership behavior. Add permanent regressions from the temporary
   probes, including bad signatures/issuer/expiry, rotation, cancellation and safe
   errors. Keep actual cryptographic verification in go-oidc.
3. Keep core OAuth Name/authorization URL/code exchange/userinfo operations; split
   IDTokenValidator and TokenRefresher into optional interfaces. Remove redundant
   SupportsOIDC and unsupported GitHub ValidateIDToken methods together. Ensure
   wrappers preserve optional capabilities and reject an ID token without a usable
   validator instead of silently downgrading to userinfo. Keep refresh as a real
   supported feature despite current login-only consumers. Separating host email
   trust is part of the same deliberate API change. An explicit authorization
   request struct would clarify the four positional strings; settle that signature
   in the implementation plan instead of adding another generic OAuth service.
4. Fix tracing completion and sampling validation. Decide/document zero and parent
   sampling semantics. Preserve Tracer/SpanFinisher/SpanIdentity, Noop and concrete
   Shutdown/ForceFlush ownership. Renaming SpanFinisher or deleting StringAttribute
   alone does not justify migration work.
5. Complete the bounded HTTP tracing seam with owned propagation/server-span proof,
   or explicitly retain local-span-only middleware until that feature is planned.
   Missing adoption is not a reason to erase a useful framework tracing capability.
6. When implemented, migrate SDK/adapters/auth/examples/docs together, append the
   next AUDIT entry and release notes, run full workspace/guards/docs checks and
   relevant race/owned-behavior probes. No consumer rewrite or module publishing
   belongs to this review. Resume S10 wiring/full pockets after these fixes.

## Consumer and original-framework evidence

Read-only lead-backend-engineer review; no pulls, changes or real provider calls.

| Consumer / main revision | Pinned SDK / auth / Google | Actual use |
| --- | --- | --- |
| /Users/jrazmi/code/segovia/segovia/v2 / 76b3d78 | 0.8.0 / 0.10.0 / 0.1.0 | Google New → []oauth.Provider, default scopes, 15s discovery context/10s client, optional token encryption |
| /Users/jrazmi/code/gps/coordination-hub / 84ff08a | 0.7.0 / 0.9.0 / 0.1.0 | Google → provider list; host validates credential pairs, callback origin+prefix and redirect allowlist; optional encryption |
| /Users/jrazmi/code/gps/three-sixty/gps-360-go / e1ab3f0 | 0.7.1 / 0.9.0 / 0.1.0 | Google-only human login; 15s discovery/10s client; no token encrypter |

No direct provider RefreshToken/ValidateIDToken or Gopernicus tracing adoption
found in these hosts' cmd/internal/pockets. This does not make refresh disposable:
original Gopernicus revision 0f763a9 has real provider refresh implementations.
Its mobile flow secret is useful caller-binding evidence, while its browser flow
is also unbound and its auth callback ignores the available ID-token validator.
Do not restore that implementation wholesale. Original HTTP tracing had propagation
and client/server kinds: useful feature intent, not a reason to restore its global
telemetry API. Current examples/cms wires Tracing → Logger → Panics and owns tracer
shutdown; retain that concrete integration example.

## Verification, limits and changed files

Go 1.26.1, with GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. All five reviewed
package trees are: ./sdk/capabilities/oauth/..., ./sdk/capabilities/tracing/...,
./integrations/oauth/github/..., ./integrations/oauth/google/...,
./integrations/tracing/otel/.... From the repository root:

- go build <the five trees> and go vet <the five trees>: PASS, no diagnostics.
- go test -race -count=1 <the five trees>: PASS;
  /tmp/gopernicus-oauth-tracing-focused-race.log. Approved local listeners.
- go test -overlay=/tmp/gopernicus-oauth-review-overlay.json -run TestAudit -v
  -count=1 ./integrations/oauth/google/... ./integrations/oauth/github/...:
  reproductions PASS; /tmp/gopernicus-oauth-review-probes.log. The overlay adds
  /tmp/gopernicus-{google,github}-audit-probe_test.go without repository source edits.
- go test -overlay=/tmp/gopernicus-auth-browser-review-overlay.json
  -run TestAuditOAuthCallbackWithoutInitiatingBrowser -v -count=1
  ./pockets/authentication/internal/inbound/authentication/...: reproduction PASS;
  /tmp/gopernicus-auth-browser-review-probe.log. Overlay substitutes a synthetic
  identity in the existing provider fixture and adds the temporary handler test.
- go run /tmp/gopernicus-tracing-audit-probe.go: reproductions PASS;
  /tmp/gopernicus-tracing-audit-probe.log. Fake writers/in-memory span recorder;
  OTLP spans finish only after owned provider shutdown so no collector export RPC
  occurs. No persistent server/container was started.

Passing reproduction programs confirm the defects described above; they are not
passing corrected-behavior tests. Source and findings are still unfixed. Existing
focused suites passing does not establish correct OAuth browser binding or safe
provider-response behavior. SDK/helper tests and current Google signature/audience/
nonce checks are useful coverage to preserve, not a complete security audit.

No full workspace/docs regeneration, live provider login, browser automation, real
collector acceptance, dependency vulnerability audit or consumer upgrade ran in
this review. The last full 41-module/23-guard/docs gate belongs to the preceding
email/notify implementation. Owned local listeners closed. Reviewed library source:
go-oidc v3.17.0 and OpenTelemetry v1.44.0, pinned by the integration modules.

Review-owned repository files: plans/framework-audit-oauth-tracing.md and
plans/framework-audit.md. AUDIT.md, RELEASING.md, source/tests, generated artifacts,
module/workspace files and external consumers are unchanged task-relative.
Next concrete step: implement the prioritized OAuth/auth and tracing corrections
from a dedicated plan, then continue S10. Preserve this review baseline and limits
when carrying work into another context window.
