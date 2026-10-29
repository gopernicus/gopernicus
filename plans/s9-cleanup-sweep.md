# S9 — Cleanup sweep: the escaped-redaction fix and the sprint's open follow-ups (SOC 2 readiness, closing slice)

**Status:** EXECUTED 2026-10-29 (tags: sdk v0.25.0 @ 950e175e, #290; pgxdb v0.18.0 + turso v0.11.0 @ 8bc9404e, #292; workshop/gopernicus v0.10.3 @ 236141c6, #293; hygiene #291 @ d4981c02, no tag; record PR-5). SPRINT CLOSED. See **As built** at the end; the tag table, MVS sets, upgrade notes and the inventory with issue numbers are in `RELEASING.md` "Cleanup sweep". Was: RATIFIED 2026-10-29 (owner: "good with your recs": R-1..R-8 all at the recommendations; I5 / #273 REMOVED — an outside contributor's PR #276 is open for it; the owner discusses it separately, so S9 touches no file #276 touches — `examples/auth-cms/cmd/server/` and `examples/auth-cms/README.md`; I7 (the auth-cms README TTL line) moves from PR-3 to PR-5, rebased after #276 is settled). Was: DRAFT 2026-10-29 (owner: "the fix and sweep now"). The owner ratifies; the rulings under "Open rulings —
YOUR CALL" each carry a recommendation and the plan executes at the recommendations unless overruled.

Anchors read on `origin/main` @ efd0c3d7 (S4c-2 PR-4 #287 merged; the S4c-2 record PR-5 is being written in parallel
and owns `RELEASING.md` / `SECURITY.md` / `AUDIT.md` / `control-map.md` / `roadmap.md` until it merges — this plan's
record PR rebases onto it). Current tags, verified local = remote: sdk v0.24.0; pgxdb v0.17.0; datastores/turso
v0.10.0; workshop/gopernicus v0.10.2; authentication v0.38.0; goredis (kvstore) v0.4.0; ui/goth v0.2.1. Parent:
`roadmap.md` (every slice S0–S8 plus S4c-2 is RELEASED; the S6 paragraph names the escaped-form residual),
`control-map.md:25` (CC6.1 "Secrets never in logs/errors": "URL-escaped `%5Bredacted%5D` not detected (follow-up)"),
`RELEASING.md` "Secrets and logging" Known ("such a value fails at the wire on authentication instead"). Sibling plans
for shape: `s6-secrets-logging.md`, `s4d3-keys-passwords.md`. The ~500-line cap applies.

## Goal

A credential composed from a rendered `sdk.Secret` is refused before any I/O in EVERY form a Go program produces
by accident — the literal `[redacted]` and its percent-encoded form `%5Bredacted%5D` (any hex case, as
`url.UserPassword`, `url.Values.Encode`, `url.PathEscape` emit it) — on every site that applies the S6 loud-failure
check (pgxdb `Open`, turso `Open`, SMTP `Probe`/`Send`); and every SOC 2-sprint follow-up still open is either closed in
this sweep, corrected in the records, filed as an issue, or dropped with a reason — so the sprint ends with one
honest inventory instead of twenty "Known" paragraphs.

## Definition of Done

- `sdk.ContainsRedacted` exists, is unit-tested for the literal, `%5B…%5D`, `%5b…%5d`, mixed case, the
  `url.UserPassword` and `url.Values.Encode` compositions, and the negatives; the three check sites call it.
- A real binary proves it: a scratch program that composes a pgxdb DSN with `url.UserPassword("u",
  fmt.Sprint(secret))` and a turso URL with `url.Values{"authToken": {secret.String()}}.Encode()` fails `Open` with
  `errors.Is(err, sdk.ErrRedactedSecret)` naming the field, and `NewSMTP(...).Probe` fails the same way — before any
  dial (no listener on the target port).
- Tags cut per RELEASING discipline (annotated, at the squash-merge commit, after a green `live-stores` on `main`,
  cold-verified with `GOWORK=off` + empty `GOMODCACHE`): `sdk/v0.25.0`, `integrations/datastores/pgxdb/v0.18.0`,
  `integrations/datastores/turso/v0.11.0`, `workshop/gopernicus/v0.10.3`.
- The IN items below are merged; the DOC corrections are in `control-map.md` / `roadmap.md` / `RELEASING.md` via the
  record PR; every LATER item without an issue has one (owner-approved list); every DROP has its reason recorded.
- The control map's header no longer says "RESEARCH, NOT RATIFIED": it records the sprint as closed with the
  residual list.

## Out of scope

- Any authentication, authorization, cms, jobs, secrets or store tag (R-3 keeps the hatch removal out; nothing else
  in IN touches a pocket).
- Extending the loud-failure check to goredis or the other eight integrations (R-2 defers it; the records say
  precisely which sites check).
- Double percent-encoding (`%255Bredacted%255D`), base64/hex/JSON-`\u` transformed values: not detectable by
  inspection; the S6 "fails at the wire" residual stays for those and the records say so.
- The post-sprint host hand-offs (gps-three-sixty, battleboardhq, from scratch — owner 2026-10-28): the next planning
  item after this record merges, not a task here.
- Pre-sprint issues not raised by the sprint (#123, #98, #95, #80, #57, #54, #39, #31, #28, #27, #26, #227, #230,
  #231): untouched, except #35 and #109 which the inventory classifies.

## Module / API impact

- `sdk`: one new exported function `ContainsRedacted(s string) bool` (+ an unexported constant); the SMTP sender's
  refusal widens. MINOR → `sdk/v0.25.0`. No signature changes.
- `integrations/datastores/pgxdb`: `refuseRedactedSecrets` widens; `hasSessionTimeZone` exact-name fix (R-4);
  `go.mod` sdk → v0.25.0 (explicit `GOWORK=off go get`, then tidy). MINOR → v0.18.0 (R-1).
- `integrations/datastores/turso`: `refuseRedactedSecrets` widens; `go.mod` sdk → v0.25.0. MINOR → v0.11.0.
- `workshop/gopernicus`: template pins only (init `go.mod.tmpl` sdk, `init.go` connector versions, pocket store
  templates, `pocket.go` `pgxVersion`, README + `commands.md` `@v…`). PATCH → v0.10.3 (the v0.6.1 "template pin is a
  PATCH" precedent).
- No store repin PR: no store code changes; MVS lifts pgxdb/turso at a host's next repin. Cold-verify proves the
  MVS set (every released pgx store beside pgxdb v0.18.0 selects sdk v0.25.0 and builds; same for turso).
- Examples: auth-cms and production-baseline repin sdk v0.25.0 + pgxdb v0.18.0 (auth-cms also turso v0.11.0); cms
  repins turso v0.11.0 + sdk v0.25.0; external-identity sdk v0.25.0; goth-showcase / jobs-minimal / minimal are on sdk
  v0.22.0 and move to v0.25.0 in the same PR (one window, one repin). Done in the record PR, explicit `GOWORK=off go
  get`.

## Schema / datastore impact

None. No migration, no ledger copy, no store SQL.

## Generated-artifact impact

None (`gofmt -w` on three `ui/goth` primitives touches hand-written Go, not `*_templ.go`).

## Design

### 1. `sdk.ContainsRedacted` (the fix)

Today each site runs `strings.Contains(value.Reveal(), sdk.Redacted)` (`integrations/datastores/pgxdb/postgres.go:258`,
`integrations/datastores/turso/turso.go:162`, `sdk/capabilities/notify/email/smtp.go:91` via the `redactedPassword`
field set in `NewSMTP`). A Secret rendered INTO a URL component is percent-encoded by `net/url`: `url.UserPassword("u",
fmt.Sprint(secret)).String()` → `u:%5Bredacted%5D`, `url.Values{"authToken": {secret.String()}}.Encode()` →
`authToken=%5Bredacted%5D`, `url.PathEscape(Redacted)` → `%5Bredacted%5D`. The literal check misses all three, and the
value reaches the wire (S6 Known). Add to `sdk/secret.go`:

```go
// redactedEscaped is Redacted percent-encoded, lower-cased so the match is
// hex-case-insensitive (net/url emits %5B/%5D; other producers may emit %5b).
const redactedEscaped = "%5bredacted%5d"

// ContainsRedacted reports whether s carries Redacted, literally or
// percent-encoded (%5Bredacted%5D in any hex case): the value was composed from
// a Secret's rendered form through fmt, +, a template, url.UserPassword or
// url.Values instead of its Reveal. Consumers refuse such a value with
// ErrRedactedSecret before any I/O. A value transformed further (double
// encoding, base64, JSON escapes) is not detectable and is out of scope.
func ContainsRedacted(s string) bool {
	return strings.Contains(s, Redacted) || strings.Contains(strings.ToLower(s), redactedEscaped)
}
```

A pattern check, not decode-then-check: `url.QueryUnescape` fails on a raw `%` inside a real password (legal in a
DSN), which would need a fallback branch for no gain. Package function, not a `Secret` method: the composed value is a
plain `string` by the time a host hands it over (the pgxdb `DSN` field is a Secret, but the helper also serves a host
that builds a DSN into a `string` first). Tests in `sdk/secret_test.go`: a table over literal / `%5B` / `%5b` /
`%5B…%5d` mixed / `url.UserPassword` composition / `url.Values.Encode` composition / `url.PathEscape(Redacted)`; a pin
that `strings.EqualFold(url.QueryEscape(sdk.Redacted), redactedEscaped)` and the same for `PathEscape` (so a future
`net/url` change fails the test, not the control); negatives: the bare word `redacted`, `%5B` alone, `[redact]`, the
empty string. The three sites change one expression each; each site's existing test gains the escaped cases
(`pgxdb/postgres_test.go:150-157`, `turso/posture_test.go:275-281`, `email/smtp_protocol_test.go:255-263`). Docs: the
type doc on `Secret` ("in literal or percent-encoded form"), `ErrRedactedSecret`'s doc, the `Config` docs and README
"refuses" sentences in pgxdb (`README.md:44-52,368`) and turso, `sdk/README.md`'s `notify/email` row, and the two
docs-site tables (`guides/production-posture.md:118-119`, `guides/database-posture.md:178`) — the guide edits ride the
record PR with its `pnpm build`.

### 2. Bundled pgxdb fix (R-4): `hasSessionTimeZone` exact-name parse

`postgres.go:320-336` is a substring test that also matches `-c log_timezone=…` inside `options` (S5 Known).
`namesRuntimeParam(params, "timezone")` (`postgres.go:338-352`, S5's exact-name parser) already does the right thing.
Replace the body of `hasSessionTimeZone` with a call to it (or make the call sites use `namesRuntimeParam` and delete
the helper); add the `log_timezone` negative to `postgres_test.go:425-436`. ~10 lines, rides the pgxdb tag that ships
anyway; drop it from PR-2 if the owner wants the fix PR pure.

### 3. The sweep inventory

Every open follow-up found in `roadmap.md` (slice paragraphs), `control-map.md` (PARTIAL / "later" / stale targets),
`RELEASING.md` (every "Known and deferred"), `NEXT.md`, and `gh issue list --state open`. Buckets: **IN** = done in
this sweep; **DOC** = records/control-map correction in the record PR; **LATER** = keep or file as an issue; **DROP** =
moot, with the reason. Verified against `origin/main` code, not against the paragraph that named it.

**IN (9)** — each either closes a SOC 2-cited residual or is trivially small and rides a PR that exists anyway.

| # | Item | Source | Size | PR |
|---|---|---|---|---|
| I1 | Escaped `%5Bredacted%5D` not detected by the loud-failure check (sdk + SMTP) | roadmap S6; control-map:25; RELEASING S6 Known | S | PR-1 |
| I2 | Same, the pgxdb and turso `Open` sites | same | S | PR-2 |
| I3 | `hasSessionTimeZone` matches `-c log_timezone=` (R-4) | RELEASING S5 Known | S | PR-2 |
| I4 | #226 production-baseline README "Two roles" + `Makefile bootstrap` say the OWNER runs `cmd/bootstrap`; the authorization pgx store refuses the owner in production — bootstrap runs as the runtime role, only `make migrate` holds the owner credential | issue #226 (CC6.1 two-role posture) | S | PR-3 |
| ~~I5~~ (REMOVED: contributor PR #276) | #273 auth-cms `authzSchema` / `authzIntegrityPolicy` → `authorizationSchema` / `authorizationIntegrityPolicy` (7 files under `examples/auth-cms/cmd/server/`; the naming rule) | issue #273; RELEASING host-asks Known | S | PR-3 |
| I6 | `gofmt` drift in `ui/goth/primitives/{breadcrumb.go,slider.go,selection_test.go}` | RELEASING S4c Cosmetic | S | PR-3 |
| I7 | auth-cms `README.md:543-549` "1-second access TTL" leg is stale (golang-jwt applies a one-minute leeway): rewrite the sentence to say the token is accepted for up to a minute past `exp`, or raise the wait | RELEASING S6 Known | S | PR-3 |
| I8 | README / `ARCHITECTURE.md` module rows missing for auth0, firebase, hibp, external-identity, ui/goth, goth-showcase, views/goth (verify each; README has two of the names today, ARCHITECTURE six) | RELEASING S4c Housekeeping | S | PR-3 |
| I9 | Scaffold pins stale: init `go.mod.tmpl` sdk v0.22.0, `init.go:100,105` connectors v0.10.0 / v0.17.0, pocket templates sdk v0.22.0 + pgxdb v0.17.0 + turso v0.10.0, `pocket.go:26` `pgxVersion = "v5.8.0"` (pgx is v5.9.2), `README.md:12` `@v0.6.1`, init `readme.md.tmpl:153` + `commands.md:187` `@v0.10.2`/`@v0.6.0` → the S9 versions and `@v0.10.3` | RELEASING S6/S5/S3 Known; roadmap S2 | S | PR-4 |

**DOC (14)** — corrections only, folded into the record PR (after the S4c-2 record merges).

| # | Correction | Where |
|---|---|---|
| D1 | Header "RESEARCH, NOT RATIFIED" → "SPRINT CLOSED 2026-10-xx (S9); residuals under LATER in `s9-cleanup-sweep.md`" | control-map:3 |
| D2 | Row 20 API keys: target "max TTL default; `DisableServiceAccount` [S4]" is STALE — both shipped S4d part 1 (authentication v0.31.0: `APIKeyMaxTTL` 365 d production default, service-account disable/enable, API-key filters); Findings FAC-9 → "scopes still none" | control-map:20 |
| D3 | Row 38 Machine identity deprovisioning: PARTIAL → DEFAULT; "no disable" and "disable [S4]" → `DisableServiceAccount`/enable shipped S4d part 1 (verified: RELEASING "Session and machine governance") | control-map:38 |
| D4 | Row 25: append "closed S9 (`sdk.ContainsRedacted`, sdk v0.25.0, pgxdb v0.18.0, turso v0.11.0); checked at pgxdb/turso `Open` and SMTP `Probe`/`Send` only — goredis and the other integrations render but do not refuse (R-2)" | control-map:25 |
| D5 | Row 58 Crypto-shredding: target "correct docs; ring-retirement runbook [S0]" → done [S0] (the docs-site keys guide + runbook exist) | control-map:58 |
| D6 | Row 64 Brute-force: target "optional escalating backoff [S5]" → ruled out (S4d part 3 R-4 (b)); point at row 120 | control-map:64 |
| D7 | Rows 93 + 131 "sign tags [S8]" → deferred by owner until a release-signing key exists (2026-10-22); row 94 "go 1.26.1" → 1.26.10 | control-map:93,94,131 |
| D8 | Row 127 "47 modules + 31 guards" → 50 modules (`find -name go.mod`), 38 guard targets | control-map:127 |
| D9 | Row 130 Schema change control: target "advisory lock; `-status` exit code [S8]" → the scaffold's `migrations -status` exits non-zero on error (done); the `pg_advisory_xact_lock` in the migration runner was NOT built (only named in a `prepareSchema` comment) → LATER L-21 | control-map:130 |
| D10 | Row 21 Secrets at rest: "correct docs [S0]" → done [S0] | control-map:21 |
| D11 | Row 153 "Redaction: GAP (FSK-2)" — fixed by the S4c-2 record PR; verify after rebase, do not double-edit | control-map:153 |
| D12 | Roadmap:5 "Nothing is built." and :296-298 (S2 follow-ups listed as open: `DeleteOwner` lock — shipped secrets pgx v0.2.1 (`FOR UPDATE` at `pgx.go:212,329`); denial paths — authentication v0.22.0; bridged IP/UA — `ClientInfoMiddleware`; scaffold pin — v0.6.1; firestore retirement — #153/#154) → mark closed; add the S9 paragraph | roadmap |
| D13 | RELEASING S3 Known "`make test-stores` needs `AUTHORIZATION_TURSO_DISPOSABLE_URL`" — the variable no longer exists in any Makefile, workflow or Go file (only RELEASING/plans cite it) → the S9 section says the note is stale | RELEASING (new section only; history untouched) |
| D14 | SECURITY.md rows sdk / pgxdb / turso; `AUDIT.md` AUDIT-063 (the escaped-form refusal; AUDIT-062 is the S4c-2 record, #289) | SECURITY, AUDIT |

**LATER (26 groups)** — kept or filed as issues (R-8 lists which need filing). One line each; sizes S/M/L.

- L1 #271 auth-cms passkey-test log-buffer race under `-race` (M, test infra).
- L2 #272 pgx bare-mode upgrade tests "oauth_clients table missing" — did not reproduce on a fresh DB (S4d4); PR-3 rechecks once (R-7) and closes with the result or leaves open.
- L3 #274 `BeforeErase` hook audit rows without `RequestID`; `securityevent.ErrorKind` lacks `forbidden`/`unavailable` (S, authentication core tag).
- L4 #275 turso `-tags=integration` legs skip in CI (no secrets; Turso token work waived by owner 2026-10-26); authorization turso under `-race` > 10 min locally (M, owner/CI).
- L5 #236 revoke-all outlives `WriteTimeout` (M, authentication core).
- L6 #229 jobs-mode delivery: enqueue-only instances need a Mailer/TokenSigner; `MaxAttempts` fixed; `MailFrom` unvalidated (M).
- L7 #286 magic-link claimless pair on factor-read failure; #288 enrollment-floor redirect 410 + `return_to` — owner low priority; bundle with the next authentication minor (R-3).
- L8 #232 kept open for its deferred items (JSON session arm, RLS/role posture probe, `init --tenancy=rls`, the atomic outbox — each its own plan).
- L9 #109 live PostgreSQL store legs on pull requests (CC8.1; `live-stores` is dispatch-only) (M, CI).
- L10 `InvitationTokenInQuery` one-release hatch still present at authentication v0.38.0 (shipped v0.30.0; WARN text says "the next minor removes") → remove in the next authentication minor (R-3).
- L11 `examples/auth-cms/cmd/server/jobs_delivery_live_test.go` has not compiled under `-tags livedelivery` since #90 (`RegisterUser undefined`, thirteen stale calls) → issue (R-6).
- L12 goredis `TestOpenHonorsDeadlineAfterConnection` flakes under load (P9 Known, S4b, S3) → issue; not fixed.
- L13 FHS-6 fail-open counter; IPv6 `/64` bucketing on `TrustProxiesConfig`; cms `svc.Upload` raw error text at 400 (S5 Known; each S, sdk/cms tags).
- L14 S4d part 1: API-key pre-expiry notification; bounded deadlines + `ErrNotFound`-only mapping for the 0025/0026 probes; `requireJSON` on `revokeAPIKey`; cross-account key listing; the #216 storetest additions.
- L15 S4d part 2: `/auth/sessions` idle marker; a distinct idle refresh code; a pre-expiry idle signal; the battleboard RLS question (at adoption).
- L16 S4d part 3: `ContextEncrypter` (AAD); HS384/HS512 rings; asymmetric signer + JWKS; `WithLegacyNotAfter`; a `guard --posture` ring line; a pocket re-seal port for `oauth_accounts`.
- L17 S4d part 4: `OAuth2Config.DelegatedAssuranceMaxAge`; `FreshAuth` asserted mode; a `guard --posture` `DelegatedAssurance` line; re-consent tooling; go-oidc/auth0 README `max_age` sentences (next tag of each).
- L18 S7: merged `mine` listing (`ListBySubjects`); `CompleteAcceptance` transition reporting; goredis bad-tag self-heal; fenced-queue enqueue wake (#95); account merge; reverse/effective-access listings; actor filter on `ListFilter`; a goth admin page for both rails; retiring auth-cms `/debug/security-events` (dev-only, platform-admin gated; the S4b browser legs still use it).
- L19 S4c: SHA-256 TOTP; retention job for abandoned pending enrollments; committed Playwright spec; dark-theme page background; Apple Passwords / other authenticator apps on devices.
- L20 S4b: username-first passkey sign-in; attestation/AAGUID allow-lists; cross-origin SPA cookie sessions; autofill + Related Origin Requests; iOS/Android/Windows Hello legs; `passkey.js` at its 200-line budget; a "subpackage never imports its parent" guard.
- L21 Migration-runner advisory lock (`pg_advisory_xact_lock`, roadmap S8 item never built; D9) (M, pgxdb + turso migrators).
- L22 S3: `outbox.PurgePublished` not a runner step in any example + a bounded variant; `Disk.Upload` fsync (sdk PATCH; `disk.go` has no `Sync()`); an `sdk/capabilities/retention` protocol; a pgxdb limiter `Prune`; the cms admin form-CSRF seam; `cms-inquiries-admin-paging.md` (DRAFT rev 2, five owner questions open); user-directory status filter; `INVITATION_TTL`.
- L23 S4a: `acr` mapping (`ClaimMapping.ACR`, `Identity.ACR`) once one vendor value is verified; dated vendor facts in the IdP table.
- L24 Default ids: a `gopernicus db restamp` verb / live-host upgrade protocol; a G22 `sdk.IDGenerator{}` guard; QuerierFrom R-3 (b) `storetest.RunForeignInstance` family (next authorization core tag).
- L25 NEXT.md owner calls still open: CodeQL alerts #15–17 and #4 (name-heuristic false positives) if not yet dismissed; the docs-deploy Actions allowlist blocking `actions/upload-artifact@v4` (control-map:92); stale S4b/S4c/S7/S4d worktrees on the owner's machine.
- L26 Post-sprint: new gps-three-sixty + battleboardhq hand-offs from scratch (owner 2026-10-28; both repos moved) — the next planning item after this record merges.

**DROP (9)** — moot, with the reason.

- X1 Secrets `DeleteOwner` race — shipped secrets stores/pgx v0.2.1 (S2 follow-ups); roadmap text stale (D12).
- X2 Unrecorded authentication denial paths; bridged denial rows' IP/UA — authentication v0.22.0 + `ClientInfoMiddleware` (S2 follow-ups); `external_login` per-cache dedupe is a documented design property.
- X3 pgx `Rotate`/`RotateAt`/`ConsumeGrace` `session_profile` filter (S4d2 LOW-2) — present at `stores/pgx/sessions.go` (rode #246); DI9 `QuerierFrom` comments — fixed (`s.db.QuerierFrom`); pgxdb `live_test.go` ledger count — narrowed to `0001_init.sql` (#238); cms turso `localfile` import — present (#257); storetest reference-store purge tie — #257; local paths in AUDIT.md — zero, guarded (`guard-no-local-paths`); `POSTGRES_NON_C_TEST_DSN` in CI — #257.
- X4 H10 skip for `*slog.Logger` methods — not feasible with AST-only guards, documented (RELEASING multi-tenant Known).
- X5 `AUDIT.md` historical snippets showing `string` credential fields — on purpose (records).
- X6 pgx store package doc "20-port bundle" — no such text remains.
- X7 `examples/README.md` §3b "exits 2" clause — the `returns 1` clarification is at `:372` (S1 Known closed).
- X8 #35 (examples adopt `environment.Secret` with hex keys) — superseded by S1 `NewKeys` (hex, fail-closed) and S6 `sdk.Secret`; owner closes with that comment. Also WIF (no GCP key remains), Turso token rotation (waived), tag signing (deferred): the S8 residual line already records all three.
- X9 S3 G35 residual (dynamic `DROP TRIGGER ` + name) and the orphan-api-key-after-cascade residual — documented inert residuals, not work.

## Risks

- **Rebase collision with the S4c-2 record PR.** PR-5 edits the same five record files. Mitigation: PR-5 starts only
  after the S4c-2 record merges; D11 verifies the C1 line rather than re-editing it.
- **A host whose real password contains the text `%5bredacted%5d`** is refused at boot. Accepted: the odds are nil
  and the error names the field; the records say the check is a substring match.
- **The scaffold pin PR (PR-4) depends on three tags**; if any tag slips, PR-4 waits — the fix itself (PR-1/PR-2)
  does not.

## Tasks as PRs

Worktrees `../gopernicus-s9-<n>` from `origin/main`; the owner merges; Claude cuts each tag after a green
`live-stores` dispatch on `main`, confirms it on origin, cold-verifies (`GOWORK=off`, empty `GOMODCACHE`, `go mod
verify`, `go build ./...`, Go 1.26.10; poll `proxy.golang.org/<mod>/@v/<ver>.info` first; never tidy before a tag
resolves; never `go work sync`). Model **opus** on every PR. `make guard && make check` in every verify line.
Verify the next free versions on origin before tagging (`git ls-remote --tags origin | grep -E 'sdk/v0.25|pgxdb/v0.18|datastores/turso/v0.11|gopernicus/v0.10.3'` → empty).

### PR-1 — sdk: `ContainsRedacted` + the SMTP site (`sdk/redacted-escaped`, `../gopernicus-s9-1`)
- **depends_on:** none
- **model:** opus
- **files:** `sdk/secret.go`, `sdk/secret_test.go`, `sdk/errors.go` (doc line), `sdk/capabilities/notify/email/smtp.go`
  (`NewSMTP:91` → `sdk.ContainsRedacted(cfg.Password.Reveal())`; the three doc comments), `sdk/capabilities/notify/
  email/smtp_protocol_test.go` (escaped cases at `:255-263`), `sdk/README.md` (root + `notify/email` rows).
- **verify:** `cd sdk && go build ./... && go test -race ./... && go vet ./...`; `make guard`; `make check`;
  Verification §1 (sdk + SMTP lines).
- **tag:** `sdk/v0.25.0` (MINOR: new exported function; the message names the widened SMTP refusal).
- **description:** Add the helper and its tests per Design 1; switch the SMTP site; document the percent-encoded form
  everywhere the sdk names the literal.

### PR-2 — datastores: pgxdb + turso sites, `hasSessionTimeZone` (`datastores/redacted-escaped`, `../gopernicus-s9-2`)
- **depends_on:** PR-1 tagged (`sdk/v0.25.0` resolves on the proxy)
- **model:** opus
- **files:** `integrations/datastores/pgxdb/{postgres.go (:258; :320-336 per Design 2; Config doc :93-97; Open doc
  :487-489), postgres_test.go (:150-157 escaped cases; :425-436 log_timezone negative), README.md (:44-52, :368),
  go.mod, go.sum}`; `integrations/datastores/turso/{turso.go (:162; Config doc :58-62; Open doc :190-193),
  posture_test.go (:275-281), README.md, go.mod, go.sum}`. Repins: `GOWORK=off go get github.com/gopernicus/
  gopernicus/sdk@v0.25.0 && go mod tidy` in each module.
- **verify:** `cd integrations/datastores/pgxdb && GOWORK=off go build ./... && GOWORK=off go test -race ./... &&
  GOWORK=off go vet ./...` (and with `POSTGRES_TEST_DSN` set, the live suite on a fresh database); `cd integrations/
  datastores/turso && GOWORK=off go build ./... && GOWORK=off go test -race ./... && GOWORK=off go vet ./...` plus
  `go test -tags=integration ./...` on the local-file driver (the libSQL-server leg is waived, S4d precedent);
  `make guard`; `make check`; Verification §1 (connector lines). Before tagging: `gh workflow run live-stores.yml
  --ref main` green on the merge commit.
- **tags:** `integrations/datastores/pgxdb/v0.18.0`, `integrations/datastores/turso/v0.11.0` (R-1). Cold-verify the
  MVS sets: pgxdb v0.18.0 beside authentication stores/pgx v0.19.0, authorization v0.24.0, cms v0.6.0, events v0.4.0,
  jobs v0.9.0, secrets v0.2.1 → selects sdk v0.25.0, `go mod verify`, builds; turso v0.11.0 beside authentication
  stores/turso v0.18.0, authorization v0.23.0, cms v0.4.0, events v0.3.0, jobs v0.8.0 → same. Plus the gps-three-sixty
  shape (authentication core + stores/pgx + authorization goredis v0.6.0 + pgxdb v0.18.0).
- **description:** Switch both `refuseRedactedSecrets` to the helper, add the escaped test cases, fold the
  `hasSessionTimeZone` fix (R-4), repin sdk, update the two READMEs.

### PR-3 — hygiene: #226, gofmt, README fixes, #272 recheck (`chore/s9-hygiene`, `../gopernicus-s9-3`)
- **depends_on:** none (parallel with PR-1; no tag)
- **model:** opus
- **files:** `examples/production-baseline/{README.md ("Two roles" :275-290 and the bootstrap lines :224-240, :548),
  Makefile (:26-30: the bootstrap target runs as the runtime role's credential; document which env var carries it)}`;
  `ui/goth/primitives/{breadcrumb.go, slider.go, selection_test.go}` (`gofmt -w` only); `README.md` + `ARCHITECTURE.md` (I8 rows, one line each, matching the existing table shape).
- **verify:** `gofmt -l ui/goth` → empty (auth-cms `cmd/server` is untouched: #276); `cd examples/auth-cms && go build ./... && go test ./...
  && go vet ./...`; `cd examples/production-baseline && GOWORK=off go build ./... && go test ./... && go vet ./...`;
  `make guard`; `make check`. R-7: `cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN=<fresh db> go test
  -run 'TestInvitationAcceptanceUpgrade|TestOAuth2SessionUpgradePreservesBrowserSession' ./...` without
  `POSTGRES_TEST_SCHEMA`; paste the result on #272. Run-and-look for I4: `make migrate` as the owner then `make
  bootstrap` as the runtime role against a local PG18 → the grant lands and the authorization store's boot probe
  prints no `owner=table` refusal.
- **description:** The four trivially small corrections plus the #226 posture fix; nothing tagged.

### PR-4 — workshop: scaffold pins → S9 versions (`workshop/s9-scaffold-pins`, `../gopernicus-s9-4`)
- **depends_on:** PR-1 + PR-2 tagged (sdk v0.25.0, pgxdb v0.18.0, turso v0.11.0 resolve)
- **model:** opus
- **files:** `workshop/gopernicus/internal/commands/{init.go (:100 "v0.11.0", :105 "v0.18.0"), pocket.go (:26
  pgxVersion "v5.9.2"), scaffold_test.go (:49-52 expectations), templates/init/go.mod.tmpl (:5 sdk v0.25.0),
  templates/init/readme.md.tmpl (:153 @v0.10.3), templates/pocket/core.go.mod.tmpl (:9), templates/pocket/stores/
  pgx/go.mod.tmpl (:7-8), templates/pocket/stores/turso/go.mod.tmpl (:7-8)}`; `workshop/gopernicus/README.md` (:12
  @v0.10.3); `workshop/documentation/docs/workshop/commands.md` (:187 @v0.10.3).
- **verify:** `cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...` (the scaffold-compile tests
  resolve the new pins — run with network); `make guard`; `make check`. After the tag: cold `init --db pgx` and
  `--db turso` from `@v0.10.3` with `GOWORK=off` and an empty cache → each verifies, builds, vets and passes `guard
  --posture` clean.
- **tag:** `workshop/gopernicus/v0.10.3` (PATCH, template pins).
- **description:** Move every scaffold pin to the S9 versions so a fresh `init` carries the fix; refresh the two
  `@v…` command lines.

### PR-5 — the record (`docs/s9-release-record`, `../gopernicus-s9-5`)
- **depends_on:** PR-1..PR-4 merged and tagged; the S4c-2 record PR merged (rebase onto it)
- **model:** opus
- **files:** `RELEASING.md` (ONE section "Cleanup sweep: escaped-redaction refusal and the sprint's residual
  inventory (SOC 2 slice S9 — published 2026-10-xx)": the tag table, Upgrade notes by host shape — "no host action
  beyond the repin; a DSN composed from a printed Secret now fails at `Open` in its percent-encoded form too" —
  the sweep inventory's IN/DOC/LATER/DROP counts with the LATER issue numbers, D13, the review + verification record);
  `SECURITY.md` rows (D14); `AUDIT.md` AUDIT-063; `workshop/documentation/docs/guides/{production-posture.md
  (:118-119), database-posture.md (:178)}` + `pnpm build`; example repins (Module impact; explicit `GOWORK=off go get`
  + tidy per example); `.claude/plans/soc2-readiness/{control-map.md (D1–D11), roadmap.md (D12 + the S9 paragraph),
  NEXT.md (S9 closed; next = L26)}`; `plans/s9-cleanup-sweep.md` (executed copy). R-8: file the LATER issues the
  owner approves (`gh issue create`, one per group needing one: L10, L11, L12, L13, L21, L22; comment on #35 (X8) and
  #272 (R-7)) and cite the numbers in the section.
- **verify:** `make guard`; `make check`; per example `GOWORK=off go build ./... && go test ./... && go vet ./...`;
  Verification §2 from the tags.
- **description:** One RELEASING section, the DOC corrections, repins, the executed copy, the issues.

## Sequencing

```
PR-1 sdk ──[tag sdk v0.25.0]── PR-2 pgxdb+turso ──[live-stores]──[tags pgxdb v0.18.0, turso v0.11.0]── PR-4 workshop ──[tag v0.10.3]──┐
PR-3 hygiene (parallel, no tag) ─────────────────────────────────────────────────────────────────────────────────────────┼── PR-5 record
S4c-2 record PR-5 (other agent) merges ──────────────────────────────────────────────────────────────────────────────────┘
```
Four tags, three trains (sdk → connectors → workshop). No store repin, no pocket tag.

## Release

| Step | Tag | After | Cold verification |
|---|---|---|---|
| 1 | `sdk/v0.25.0` | PR-1 merge | sdk alone; a scratch consumer on authentication v0.38.0 + sdk v0.25.0 builds (MVS lift safe) |
| 2 | `integrations/datastores/pgxdb/v0.18.0`, `integrations/datastores/turso/v0.11.0` | PR-2 merge + green `live-stores` on `main` | the two MVS sets + the gps-three-sixty shape (PR-2 task); the §1 NEGATIVE recorded from the tags |
| 3 | `workshop/gopernicus/v0.10.3` | PR-4 merge | cold `init` pgx + turso from the tag (PR-4 task) |

Annotated, unsigned, one tag per command, at the squash commit, confirmed on origin, then polled on the proxy.
**Not tagged:** every pocket, every store, ui/goth (gofmt only), the examples, `pockets`.

## Verification

1. **Scratch program (a real binary, in the scratchpad, `go.work` resolving the branch):** build `secret :=
   sdk.Secret("hunter2")`; compose `dsn := (&url.URL{Scheme: "postgres", Host: "127.0.0.1:1", Path: "/db", User:
   url.UserPassword("u", fmt.Sprint(secret))}).String()` → print it (shows `%5Bredacted%5D`); `pgxdb.Open(ctx,
   pgxdb.Config{DSN: sdk.Secret(dsn)})` → `errors.Is(err, sdk.ErrRedactedSecret)` true, error names `Config.DSN`, no
   connection attempted (port 1); `tursodb.Open(ctx, tursodb.Config{URL: sdk.Secret("libsql://h.example?" +
   url.Values{"authToken": {secret.String()}}.Encode())})` → same, names `Config.URL`; a lowercase `%5bredacted%5d`
   variant of each → same; `email.NewSMTP(email.SMTPConfig{Host: "127.0.0.1", Port: "1", Username: "u", Password:
   sdk.Secret("x" + url.PathEscape(sdk.Redacted))}).Probe(ctx)` → `ErrRedactedSecret` before dialing. NEGATIVE
   control: the same program against the released pins (`GOWORK=off`, sdk v0.24.0 + pgxdb v0.17.0 + turso v0.10.0)
   reaches the dial (`connection refused`) — recorded verbatim in the section as the before/after.
2. **From the tags (PR-5):** `examples/production-baseline` and `examples/auth-cms` build `GOWORK=off` on the repins;
   auth-cms boots in development on a free port (`lsof -i :8096` first; never 8082 or 55432) and serves `/auth/me`
   401 — the sweep changed no behaviour there, this is the repin smoke. I4 run-and-look per PR-3.

## Open rulings — YOUR CALL

- **R-1 Connector version bumps.** (a) MINOR: pgxdb v0.18.0, turso v0.11.0. (b) PATCH: v0.17.1 / v0.10.1.
  **Recommend (a):** the brief's "MINOR under v0" rule, and each connector raises its sdk requirement a minor.
- **R-2 Extend the refusal to goredis** (`client.go:188,202` reveals URL/Password with no check). (a) Add it (helper
  makes it four lines) → goredis v0.5.0 + a Redis live leg. (b) Defer; D4 states the three checked sites exactly.
  **Recommend (b):** S6 scoped loud failure to the DSN/URL connectors + SMTP on purpose; one more tag and leg for a
  site whose misuse already fails authentication at the wire buys little this week. Flip to (a) if the auditor story
  needs "every connector".
- **R-3 `InvitationTokenInQuery` hatch** ("one release", shipped v0.30.0, eight minors ago). (a) Remove now →
  authentication v0.39.0, bundle #288 (+#286 if small). (b) LATER (L10): remove with the next authentication minor a
  feature needs. **Recommend (b):** no host has adopted v0.30.0 yet (hand-offs apply at the roadmap's end), so nobody
  is on the hatch; an authentication train is the sweep's biggest cost for no SOC 2 gain.
- **R-4 Bundle the `hasSessionTimeZone` fix in PR-2.** (a) Yes. (b) Keep PR-2 pure; LATER. **Recommend (a):** ten
  lines, its parser exists, the tag ships anyway.
- **R-5 PR-4 scaffold pins + workshop v0.10.3.** (a) In. (b) Out (a fresh `init` keeps pgxdb v0.17.0 until the host
  repins). **Recommend (a):** a scaffolded host should carry the fix; the README's `@v0.6.1` is six minors stale.
- **R-6 `jobs_delivery_live_test.go`** (broken under `livedelivery` since #90). (a) Fix (M). (b) Delete the file (S).
  (c) File an issue (L11). **Recommend (c);** (b) if the owner no longer runs the live-delivery leg.
- **R-7 #272 recheck in PR-3** on a fresh database without `POSTGRES_TEST_SCHEMA`; close if green. **Recommend yes.**
- **R-8 File issues for L10, L11, L12, L13, L21, L22** (the LATER groups with no issue today), one per group, and
  comment-close #35. **Recommend yes**, filed by PR-5 after the owner approves the list; the rest of LATER stays
  recorded in the plan and RELEASING only.

## Consultation notes

No lead consulted: the fix is a one-function helper with an obvious shape (Design 1) and no module-boundary
question; the sweep is inventory. Reviewers below.

## Open questions

_None._ (#272's reproduction is R-7's job, not a question.)

## Recommended reviews

**product-manager** (the IN/LATER line: is anything in IN padding, is anything in LATER a SOC 2 residual an auditor
would expect closed); **platform-sre** (the tag train, R-1, R-5, the cold-verify sets); **lead-backend-engineer** on
PR-2's diff only (the `hasSessionTimeZone` swap and the two call sites).

## Notes

- **Naming:** authentication/authorization throughout; I5 (#273) is OUT — contributor PR #276, owner handles it.
- **Not relitigated:** host-owned migrations; HIBP not wired in examples; tag signing deferred; Turso token rotation
  waived; no lockout; the S6 R-5 "no redaction off switch".
- **Cut for the cap:** per-case test lists; the RELEASING section prose; the exact README row text for I8.

## As built

| PR | Merged | Tag | `live-stores` |
|---|---|---|---|
| PR-1 | #290 (`950e175e`) | `sdk/v0.25.0` (annotated `684f7d47`) | 40930336355 @ `d4981c02` (contains `950e175e`) |
| PR-2 | #292 (`8bc9404e`) | `integrations/datastores/pgxdb/v0.18.0` (`48a34aa4`), `integrations/datastores/turso/v0.11.0` (`7a17d63e`) | 40934968698 |
| PR-3 | #291 (`d4981c02`), closes #226 | none | — |
| PR-4 | #293 (`236141c6`) | `workshop/gopernicus/v0.10.3` (`6bdca30b`) | 40939306973 |
| PR-5 | the record PR | none | — |

Cold sets: sdk alone; the pgx and turso store sets (each selects sdk v0.25.0); the gps-three-sixty shape; cold `go
install …@v0.10.3` + `init --db pgx|turso` (verify, build, vet, `guard --posture` clean, 25 rules). PR-5 repinned every
example and ran Verification §2 from the tags (every example `GOWORK=off`; auth-cms `/auth/me` 401; production-baseline
two-role bootstrap on PG 18.6; an escaped `DB_URL` refused with no server-side authentication attempt).

Deviations from the task text:

- **Ratification:** I5 (#273) removed — contributor PR #276. **At the record:** I7 moved to a follow-up tied to #276
  (it edits the same README); PR-5 touches only `examples/auth-cms/go.mod`/`go.sum` there.
- **PR-2:** turso `Open` does not dial a hosted URL (the first statement does), so the fix refuses at `Open`; the
  `hasSessionTimeZone` fix's live-visible case is a custom `-c app.timezone=` setting (the server refuses
  `log_timezone` at connect, 55P02; `log_timezone` is proven by unit test only). Both connectors repinned sdk
  v0.18.0 → v0.25.0.
- **PR-3:** the production-baseline Makefile stays comment-only (owner). #272 rechecked green on fresh databases and
  closed by the lead (R-7).
- **PR-5:** **D13 was wrong** — `AUTHORIZATION_TURSO_DISPOSABLE_URL` is live (the authorization turso fixture guard,
  its README and `live-stores.yml`); only the Makefile `test-stores` comment lacked it (added in PR-5 on the owner's call), so the S3 note
  held and the record says so. D11's C1 line got only its follow-up clause flipped to "closed S9". R-8 filed #294 (L10), #295
  (L11), #296 (L12), #297 (L13), #298 (L21), #299 (L22); #35 closed as superseded. Extra: #300 — `gopernicus version`
  prints `0.0.0-dev` for a `go install …@<tag>` binary (a hard-coded placeholder since the CLI's first commit; no
  LATER group fit).
