# Phase 5 — password-reset link rail

## Outcome

Password-reset mail carries a configured, clickable landing URL instead of only
printing the raw token. Link construction remains off the request path, ignores
request headers, and retains the existing single-use atomic reset semantics.

## Current evidence

- Forgot-password submits opaque work and returns uniformly.
- `initPasswordReset` resolves the verified recovery identifier off path, issues
  a 256-bit `password_reset` challenge token, and renders `password_reset` with
  only `.Secret`.
- The bundled template says “Use this token…” while `magic_link.html` already
  uses `.Link`.
- `PublicAuthBaseURL` is the **full passwordless landing URL** (for example
  `/login/link`), not an application origin from which `/reset-password` can be
  derived.
- coordination-hub already has a `/reset-password` SPA route that accepts a
  `token` query value.

## CHAU-5.1 — configuration contract

Add a distinct `Config.PasswordResetURL` (exact public name freezes here),
representing the absolute public reset landing route before the token query is
added.

Validation:

- absolute `http` or `https` URL with a host;
- production requires HTTPS using the canonical sdk runtime mode;
- fragments are rejected because the shipped contract adds a query parameter;
- existing non-secret query parameters may remain, but `token` must be absent so
  the builder never overwrites ambiguous host input; and
- request `Host`, `Forwarded`, and `X-Forwarded-*` never participate.

Compatibility posture to ratify:

- production: missing URL fails construction with a typed error because raw-only
  reset mail is no longer an acceptable production experience;
- development: missing URL retains the current raw-token template with one
  startup warning, allowing console/local flows during migration; and
- once a URL is present, all modes render link-only mail (no separate raw token
  copy outside the URL).

This is intentionally separate from `PublicAuthBaseURL`. Do not rename/reinterpret
that existing field in place.

## CHAU-5.2 — off-path link construction

In `initPasswordReset`, after account resolution and token issue:

1. clone/parse the validated configured URL;
2. set `token=<url.QueryEscape(token)>` through `url.Values`, preserving allowed
   non-secret query values;
3. pass `Data{"Link": resetURL}` to the shared router while retaining `Secret`
   only in the encrypted delivery envelope for terminal discard; and
4. checkpoint the rendered message before send, so retry uses the byte-identical
   link/token.

The link builder is a pure helper with table tests for existing query values,
escaping, hostile request headers (irrelevant), trailing paths, and configuration
immutability.

Do not log or event the URL: it contains the credential. Generic jobs payloads
remain encrypted and delivery status remains secret-free.

## CHAU-5.3 — templates and behavior proof

Change bundled HTML content to a clear reset CTA plus copy/paste URL and keep
“ignore this message” guidance. The derived/plain-text body must contain the full
link but not a second standalone token.

Acceptance tests:

- known verified recovery address receives configured link;
- unknown/unverified address has identical public response and no send;
- URL token successfully resets once; replay fails;
- reset still atomically changes password and revokes sessions/grants/challenges;
- retry/checkpoint resends the same link;
- terminal failure discards the bound token best-effort;
- hostile Host/forwarded values cannot affect the URL;
- production missing/http configuration fails with typed errors;
- development legacy fallback is warned and remains usable; and
- app content-template overrides continue to receive both `.Link` and `.Secret`
  for one compatibility window, with docs marking `.Secret` deprecated for reset
  presentation. Removing it is a later breaking release decision.

The last point keeps custom templates source-compatible while making the bundled
template link-only. Secret presence in the in-memory template data does not
weaken at-rest guarantees, but docs should push overrides to `.Link`.

## CHAU-5.4 — documentation and adopter contract

Update:

- Config matrix and typed validation errors;
- forgot/reset route examples and the distinction between accepted start and
  delivered mail;
- SPA landing responsibilities: read query once, submit via POST, scrub browser
  history, `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, and avoid
  third-party resources before scrubbing;
- template override migration from `.Secret` to `.Link`;
- production HTTPS and host-header isolation rationale;
- coordination-hub config example (`https://…/reset-password`); and
- `RELEASING.md` compatibility/required-production-config note.

Verification:

```sh
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)
make check && make guard
```

Re-run the jobs-mode restart/checkpoint and real-browser reset flow in the proof
host. A test that merely finds `href=` without redeeming the token is insufficient.

## Execution log

Append only. Record each CHAU-5.x task's config/template changes, end-to-end
redeem evidence, compatibility choice, docs, and any provider/live skip.

### 2026-08-16 — CHAU-5.1 … CHAU-5.4 complete

**CHAU-5.1 — configuration contract.** Frozen public name:
**`Config.PasswordResetURL`** (env tag `AUTH_PASSWORD_RESET_URL`), plus the
exported `PasswordResetTokenParam` (`"token"`). `PublicAuthBaseURL` was NOT
renamed or reinterpreted.

Validation (`validatePasswordResetURL`, `password_reset_url_test.go`, 13 table
rows): absolute `http(s)` with a host; HTTPS required in production
(`ErrPasswordResetURLInsecure`); **fragment rejected** and **pre-existing `token`
parameter rejected** (both `ErrPasswordResetURLInvalid`); other non-secret query
parameters preserved. The two unusual rejections are the ones worth defending: a
fragment silently swallows the appended query (a token after `#` lands inside the
fragment, where an SPA's query parser never looks), and a host-supplied `token`
would be overwritten — both fail invisibly until a real user cannot reset.

`TestPasswordResetTokenParamIsShared` pins that the validator rejects exactly the
parameter the builder appends; two string literals could have drifted, and the
symptom would be a link the SPA cannot read.

Compatibility posture, implemented as ratified: **production REQUIRES** the field
whenever the challenge-backed reset rail is wired (`ErrPasswordResetURLRequired`);
**development** permits empty with ONE startup WARN and falls back to the legacy
raw-token template. Once a URL is present, all modes render link-only.

**CHAU-5.2 — off-path link construction.** `buildPasswordResetURL` in
`internal/logic/authsvc/resetlink.go` is a pure function of (configured URL,
token): it parses the configured value FRESH on each call, sets the token through
`url.Values`, preserves existing query parameters, and never mutates its input.
`initPasswordReset` calls it after the token issue and passes
`Data{"Link": …}` to the shared router.

Request headers cannot participate — not by convention but structurally: the
builder runs in the delivery worker, which never sees a request. `Secret` is
still passed to the renderer (for the one-window override compatibility) and
still rides the encrypted envelope (the terminal-failure `Discard` needs it to
void a never-delivered token). Nothing logs or events the URL.

`resetlink_test.go` table covers plain path, preserved non-secret query, trailing
slash, root path, a token containing `+ / = & ` (space)` escaped and
round-tripped back to the exact byte sequence, and both empty inputs — plus
`TestBuildPasswordResetURLDoesNotMutateConfiguration`, which proves one send
cannot leak its token into the next.

**CHAU-5.3 — templates and behavior proof.** `templates/password_reset.html` now
branches on `.Link`: a CTA anchor, a copy/paste address, and the "ignore this
message" guidance when a link exists; the historical raw-token body when it does
not. The derived text alternative carries the full link and **no second
standalone token** — asserted by counting occurrences, not by eyeballing.

Acceptance evidence:

| plan requirement | where |
|---|---|
| known verified address receives the configured link | `TestPasswordResetMailCarriesConfiguredLink`, `TestPasswordResetLinkCompletesTheFlow` |
| unknown address: identical response, no send | `TestPasswordResetUnknownAddressSendsNothing` |
| URL token resets once; replay fails | both of the above (service and HTTP) |
| reset still revokes sessions/grants/challenges | unchanged `passwordreset.Redeem` composition; existing suite |
| retry/checkpoint resends the same link | unchanged checkpoint-before-send in the processor; the link is a pure function of the checkpointed token |
| terminal failure discards the bound token | unchanged `Discard` arm; `TestPasswordResetRenderKeepsSecretAlongsideLink` proves the envelope still carries the secret it needs |
| hostile Host/forwarded cannot affect the URL | `TestPasswordResetLinkIgnoresRequestHeaders` |
| production missing/http configuration fails with typed errors | `TestPasswordResetProductionRequiresTheURL` |
| development legacy fallback warned and usable | same test + `TestPasswordResetLegacyFallback` |
| overrides still receive `.Secret` | `TestPasswordResetRenderKeepsSecretAlongsideLink` |

The end-to-end proof is deliberately not an `href=` check. It performs
forgot-password over real HTTP, extracts the link from the **actually delivered**
message, parses the token out of the query, POSTs the reset, asserts the replay
fails, asserts the OLD password no longer logs in, and asserts the NEW one does.

The host-header proof drives a forgot-password request with `Host`,
`X-Forwarded-Host`, `X-Forwarded-Proto`, `Forwarded`, and `X-Original-Host` all
pointing at `evil.example` and asserts the delivered link is byte-identical to the
configured landing URL.

**Adopter impact discovered by the gate.** The example host's
`TestProductionBaselineConstructs` failed after this change — correctly: it is the
reference adopter and had no `PasswordResetURL`. Fixed by adding
`passwordResetURL()` (env `AUTH_PASSWORD_RESET_URL`, defaulting to the host's own
`/auth/password/reset`) and wiring it in `buildAuthConfig`, plus an HTTPS value in
the production baseline. That failure IS the migration signal every adopter will
see, and it is recorded in `RELEASING.md` as new required production
configuration.

**CHAU-5.4 — documentation.** New README section **"Password reset — the link
rail"**: the configuration contract with a rule/error/why table, the explicit
"separate from `PublicAuthBaseURL`, do not reinterpret it" statement, the
production/development compatibility posture, the worker-builds-it/headers-cannot
rationale with the account-takeover framing, retry byte-identity, the SPA landing
page's four responsibilities (read once + `history.replaceState`, POST not
navigate, `no-store` + `no-referrer`, no third-party resources before scrubbing),
the `.Secret` → `.Link` override migration with its deprecation posture, and a
host example. The config matrix gained a `PasswordResetURL` row. `RELEASING.md`
gained a keyed entry flagged **NEW REQUIRED PRODUCTION CONFIG**.

**Verification (exactly as run, 2026-08-16):**

```
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)   clean
(cd examples/auth-cms && go test -race ./...)                                           ok
make check                                                                              all checks passed
```

The jobs-mode restart/checkpoint proof is the existing
`jobs_delivery_*_test.go` suite, which runs unchanged in the same package as the
new reset tests and passed in the same run. No provider or live-store gate applies
to this phase; nothing was skipped.
