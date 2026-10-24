# S6 — Secrets and logging (SOC 2 readiness, slice 6)

**Status:** EXECUTED 2026-10-24 — RELEASED. Five code PRs + the store repin squash-merged on `main`: #182 PR-1 sdk @ f347b66a · #183 PR-2 datastores @ 59c318d8 · #184 PR-3 integrations @ d54de133 · #185 PR-2b store repin @ 1b0643bb (no tags) · #186 PR-4 workshop @ 5143422f; PR-5 = the record (`docs/s6-release-record`). TWELVE tags, no store tags (R-3): sdk v0.18.0 @ f347b66a; pgxdb v0.14.0 + turso v0.9.0 @ 59c318d8 (each cold-built with the released store set on sdk v0.18.0); goredis v0.4.0, sendgrid v0.5.0, s3 v0.3.0, gcs v0.3.0, oauth/google v0.3.0, oauth/github v0.3.0, firebase v0.2.0, golang-jwt v0.3.0 @ d54de133; workshop/gopernicus v0.9.0 @ 5143422f. Live legs on merged main before the connector tags (eleven store legs @ 59c318d8) and the goredis tag (158/0/0 @ d54de133). CodeQL alert 14 (the gated turso args flow, formerly alert 13) is an OWNER dismissal. Build-time deviations under "Build-time deviations" below. Former status: RATIFIED 2026-10-24 (owner: "ratified"; rev 2 as reviewed; R-1 migrate DSN/URL; R-2 keep the production refusal, turso same rule, arity/types the development default; R-3 PR-2b = the one store-repin PR, twelve modules, no tags; R-4 firebase CredentialsJSON → Secret; R-5 no off switch; the NewSMTP adaptation (signature kept, Probe/Send return ErrRedactedSecret) accepted). Build started from `main` @ 00d12053: PR-1 sdk first; PR-2 + PR-3 after the sdk tag (PR-2 merges first); PR-2b after the pgxdb/turso tags; PR-4 after PR-2's tags; PR-5 last.

Anchors read on `main` @ 00d12053 (S1, S2, S3, S5 RELEASED — `RELEASING.md:3-230`). Current tags, verified local = remote
(`git tag --list '<module>/*' | sort -V | tail -1` and `git ls-remote --tags origin`): sdk v0.17.0; pgxdb v0.13.0; turso
v0.8.0; `integrations/kvstores/goredis` v0.3.0 (the v0.5.0 in older notes is the authorization STORE
`pockets/authorization/stores/goredis`, a different module); sendgrid v0.4.0; s3 v0.2.0; gcs v0.2.0; oauth/google v0.2.0;
oauth/github v0.2.0; firebase v0.1.0; golang-jwt v0.2.0; workshop v0.8.0; authentication v0.24.0; authorization v0.30.0;
cms v0.6.0; jobs v0.7.0; secrets v0.2.0; events v0.3.0. Parent: `roadmap.md:210-217` (the S6 slice, verbatim scope),
`:239-249` (S5–S7 independent), `:250-290` (standing rulings), `:300-312` (the 2026-10-23 process ruling: one store-repin
PR per slice if any, two-reviewer gate, 500-line cap, record in RELEASING). Evidence: `review.md:310-317` (M11),
`:253-260` (M2, the `web.Logger` half); `passes/06-sdk-kernel.md:89-95` (FSK-1), `:97-103` (FSK-2), `:184` (the v0.8.0
"Secret" is a decode FUNCTION); `passes/03-http-surface.md:346-365` (FHS-7); `passes/04-auth-stores.md:101-109` (FAS-5).
Control-map rows closed: `control-map.md:24` (CC6.1 "Secrets never in logs/errors" — FSK-1, FSK-2, FAS-5) and `:107`
(CC7.2/CC7.3 "Access log quality" — FHS-7); CC6.7 has no row of its own and is claimed through `:24`. Rev 1 is kept
beside this file as `s6-secrets-logging.rev1.md`.

## Goal

A host that composes the framework the documented way (`logging.New`, `web.Logger`, `ParseEnvTags` over a connector
`Config`) cannot print a credential by accident: every integration secret is an `sdk.Secret` that renders as
`[redacted]` through `fmt`, `slog`, `encoding/json` and `%+v`; a secret composed INTO a DSN or URL fails boot loudly
instead of reaching the wire; the default logger redacts a bundled key list plus `LOG_REDACT_KEYS`; the access line
carries the resolved client IP, the principal, a bounded user agent and one `request_id`; and a query log never carries
bind values unless a development host asks for them with `LogQueryArgs` — production refuses query logging on both
connectors.

## Scope and non-goals

Every roadmap item, mapped:

| Roadmap item (`roadmap.md:211-216`) | Where | Finding |
|---|---|---|
| Kernel `Secret` type, redacting `String/GoString/Format/LogValue/MarshalJSON`, `Reveal()` | PR-1 (`sdk/secret.go`) | FSK-1 |
| `ParseEnvTags` support | PR-1 — NO code: `tags.go:104-124` sets any `reflect.String` kind; a pinning test + doc line | FSK-1 |
| Migrate integration `Config` secret fields (minor BREAKING) | PR-1 (sdk SMTP), PR-2 (pgxdb, turso), PR-3 (seven integrations + golang-jwt) | FSK-1 |
| Default-on `logging.RedactHandler`, bundled deny-list, `LOG_REDACT_KEYS` | PR-1 (`sdk/pkg/logging/redact.go`) | FSK-2 |
| `web.Logger` uses `web.ClientIP`, adds principal, bounded user agent, `request_id` | PR-1 (`middleware.go`, plus the root principal capture) | FHS-7, M2 |
| Connectors log arity/types unless `LogQueryArgs` | PR-2 (pgxdb + turso tracers, posture) | FAS-5 |

**Already exists (built on, not rebuilt):** `environment.Secret(key, minBytes)` — a hex DECODE function
(`sdk/pkg/environment/secret.go:53`), kept as is; `ParseEnvTags` named-string support by kind; `web.ClientIP`
(`trustproxies.go:68`) and `web.RequestID` validation (S5); `logging.ContextHandler` adding `request_id` from context
(`handler.go:28-41`); pgxdb's production refusal of `LogQueries` (`posture.go:29-32,85-93`, S1 — which marked the
arity-only revisit for S6, `plans/s1-production-posture.md:99-100`); `pgxdb.Config.Redacted()`/`turso.Config.Redacted()`
for operator display; `authentication.Credential.String/LogValue` as the one redacting value today.

**Non-goals (deferred with why):** FSK-4 (`.env` in production — closed by S1's `LoadEnvForMode`, P0), FSK-8 (console
transports — `notify.CheckTransport` is S1/S5 territory) and FSK-5 (OTel sampling) are M11 siblings the roadmap line
does not assign to S6; `pockets/secrets` (its `vault.Secret` entity is an unrelated name); a `fmt`-level `%v`
scrubber for arbitrary structs (impossible — Go calls no method through an unexported field, see Design 1 and
Risks); redacting inside `slog.Any` maps/structs or free-text `error` values (documented limit of the handler, incl. the
access line's `error` attribute — Design 4); `oauth.Tokens` fields (`sdk/capabilities/oauth/oauth.go:93-96` — runtime
values with their own lifetime, not config; G37 excludes the file by path); renaming `environment.Secret` (S1's hex keys
ride on it); an off switch for redaction (R-5); log RETENTION (host infrastructure; the policy paragraph says so).

## Decided by gate

Reviews: lead-backend-engineer (B1–B11, one question) and product-manager (P1–P11, one question), 2026-10-24.

- B1 (HIGH, R-3 premise false; 54 literals not ~30) → PR-2b, the slice's one store-repin PR; counts corrected everywhere.
- B2 (goredis is v0.3.0; empty MVS check) → tags v0.4.0; "lags" sentence and the goredis-beside-store check deleted.
- B3 (`LogValue` leaves `fmt` leaking; `golang-jwt.Signer` missed) → `Format` + `LogValue` on five adapters; golang-jwt
  joins PR-3 at v0.3.0; §1 prints each with `%+v`/`%#v`.
- B4 (capture semantics wrong; reuse bleeds across scopes) → Design 4 rewritten: fresh capture per Logger, chained
  write-through, "last principal authenticated, including one the surface then refused", observability only, fallback.
- B5 (turso refusal would ship inert; nothing boots it) → checked before the TLS early return, `errors.Join`ed; proven
  by the §1 scratch program (P2). Declined the examples/cms production boot: cms never reads `DB_LOG_QUERIES` (P2).
- B6 (R-2 text inconsistent) → "query logging" wording; `wrapsLoggingTracer` ignores `LogArgs`; `error_type` end line.
- B7 (G37 exact names only) → the prefixed regex over `integrations/` + `sdk/`, `oauth.go` excluded by path.
- B8 (store `go.mod.tmpl:8` sdk pin; pre-v0.9.0 turso hosts) → Design 9 and the handoff note.
- B9 (nested `RequestID` mints a new id) → PR-1 reuses `sdk.RequestIDFromContext`; middleware order documented.
- B10 (access-line `error` text residual) → named in the policy paragraph; §2 uses a 23505 failure.
- B11 (match rule ambiguous) → matching key replaces whatever its kind; `ContextHandler` `WithAttrs` limit stated.
- B question (`LogQueryArgs` without `LogQueries`) → inert in development; production refuses it alone (Design 5).
- P1 (§3 query-log leg has no database) → the leg runs on production-baseline in development.
- P2 (row 10 never booted) → §1 `tursodb.Open` through `ParseEnvTags`; the error names `Config.LogQueries (DB_LOG_QUERIES)`.
- P3 (auth-cms demo `api_key`/`token` collide) → PR-1 renames `seeded_api_key`/`token_path`, README, regex, `logging.New`.
- P4 ("Read first" box; row 7; optional loud failure) → box with two greps; loud failure RULED YES (`sdk.ErrRedactedSecret`;
  `NewSMTP` keeps its signature — the refusal surfaces from `Probe`/`Send`, which production hosts call at boot).
- P5 (host-facing snippets stop compiling) → PR-2 fixes the two docs guides; PR-3 wraps every README `Config` snippet.
- P6 (row 4 hatch exists) → `LOG_REDACT_KEYS=client_ip,user_agent` named; `# LOG_REDACT_KEYS=` in production-baseline.
- P7 (no "who did what, from where" line) → §2 authenticated call in development, pasted into PR-5 as CC7 evidence.
- P8 (§1 gaps) → `ParseEnvTags` for `LOG_REDACT_KEYS`, `probe_only` logged; every PR-3 Config and adapter printed.
- P9 (PR-4 waits on PR-3 only for G37) → G37 moves into PR-3; PR-4 depends on PR-2's tags; `DSN` in, `URL` out.
- P10 (examples literals, `livedelivery`) → three example literals carried; `-tags livedelivery` in PR-2's verify.
- P11 (ceremony) → "Recommended reviews" and the consultation log cut; PR-5 runs §4 plus a §2 boot-from-tags smoke.
- P R-3 note ("each store closes at its next repin") → declined: superseded by the lead's R-3 — PR-2b closes all twelve now.
- P question (eleven tags?) → twelve: golang-jwt v0.3.0 added (B3).

## Design

Package legality follows `sdk/README.md:13-50` (root = kernel, stdlib only, no subpackage import — G12a greps only
subpackage imports; `pkg/*` import the root only; capabilities import root + pkg).

**1. `sdk.Secret` — root `package sdk`, new file `sdk/secret.go`.** `type Secret string`; `const Redacted =
"[redacted]"`; methods `String()`, `GoString()`, `Format(fmt.State, rune)` (every verb writes `Redacted`),
`LogValue() slog.Value`, `MarshalJSON()`, `MarshalText()` — all emit `Redacted`; `Reveal() string` is the one read.
`var ErrRedactedSecret` (root, beside `ErrNotFound`): the sentinel a consumer returns when a revealed value CONTAINS
`Redacted` — the marker of a secret composed through `fmt`/`+` (loud failure, Design 5/6). Unmarshal is the string
kind's own (a JSON/env/text source populates it; a JSON ROUND-TRIP stores `"[redacted]"` — documented). Root admission
(`sdk/README.md:21-25`): a small primitive, common vocabulary, no lifecycle; new root imports `fmt`, `log/slog`,
`encoding/json` are stdlib. A string kind, not a struct, because an untyped constant or literal still assigns
(`Config{Password: "x"}` compiles unchanged); a typed `string` variable needs `sdk.Secret(v)` — that conversion is the
whole host migration (Upgrade notes). Closes FSK-1's type half. Hatch: none; `Reveal()` is the read. Limits stated in
the doc comment: `fmt` calls no method through an UNEXPORTED field (`%+v` of a struct holding `config Config` prints
the value; slog's TextHandler formats structs with `%+v`, JSONHandler skips unexported fields), so an adapter holding a
secret in an unexported field implements `fmt.Formatter` AND `slog.LogValuer` itself (Design 7).

**2. `ParseEnvTags` — `sdk/pkg/environment` (pkg → root import, legal).** No code change: `supportedFieldType`
(`tags.go:104-112`) and `setFieldValue` (`:123-125`) act on `reflect.String`. New: a test in `tags_test.go` that an
`sdk.Secret` field honors `env`, `default` and `required` (errors stay value-free), and one doc sentence at
`tags.go:33` ("including named string types such as `sdk.Secret`"). `secret.go:46` and `sdk/secret.go` cross-reference.

**3. `logging.RedactHandler` — `sdk/pkg/logging/redact.go` (imports root only).** `NewRedactHandler(inner
slog.Handler, keys []string) *RedactHandler`; `var DefaultRedactKeys = []string{password, passwd, secret, token,
access_token, refresh_token, api_key, apikey, authorization, cookie, set-cookie, private_key, client_secret,
credentials_json, auth_token, dsn}`; `Options.RedactKeys []string env:"LOG_REDACT_KEYS"` is ADDITIVE to the defaults
(not a `default:` tag — an env value would REPLACE it). Match: key, case-insensitive, `-`/`_` normalized (`x-api-key`
≡ `x_api_key`), exact (no substring — `refresh_token_hash` is not `token`). A MATCHING key replaces its value whatever
the kind (`slog.Any("token", groupValuer)` becomes one redacted string); recursion happens only into groups whose key
does NOT match (B11; test). Mechanics: `Resolve()` every value BEFORE the group test, treat an empty-key group as
inline, redact `WithAttrs` pre-bound attrs on a COPIED slice, pass `WithGroup` through. Replacement:
`slog.StringValue(sdk.Redacted)`. `logging.New` composes `ContextHandler(RedactHandler(format handler))` in every mode
(default-on). No off switch (R-5): a host that must log such a key renames the attribute or composes
`NewContextHandler` over its own handler. `ContextHandler.Handle` (`handler.go:28-41`) gains one rule: skip an id the
record already carries, so Design 4 adds `request_id` without a duplicate key; limit (documented): it sees record attrs
only, not a `request_id` pre-bound through `WithAttrs` (`logger.With(...)`). ONE framework key collides with the list:
auth-cms's demo WARN logs `"api_key", key` and `"token", devissuer.TokenPath` (`examples/auth-cms/cmd/server/
demo.go:358`) — PR-1 renames them `seeded_api_key`/`token_path` (the row-3 hatch, worked; P3); no other does
(lead-verified). The dev console senders log under `text`/`body` (`notify/email/console.go:45-50`), so magic links
still show. Closes FSK-2. The log-content policy paragraph lands in `sdk/README.md` (Packages → `logging`): what is
redacted, what the handler cannot see (`slog.Any` maps/structs, error text — including the access line's `error`
attribute, which keeps `MapError`'s 22P02 text, `pgxdb/postgres.go:539-540`), that access lines carry `client_ip` and
`user_agent` (personal data — the hatch is `LOG_REDACT_KEYS=client_ip,user_agent`; retention is the host's log
pipeline, not S3's rails).

**4. `web.Logger` — `sdk/pkg/web/middleware.go:77-128` (imports root already).** Attributes: keep `method`, `path`,
`status`, `elapsed`, `remote_addr` (raw peer), `error`, `aborted`, `hijacked`; ADD `client_ip` from `ClientIP(ctx)`
when `TrustProxies` ran, `request_id` from `sdk.RequestIDFromContext` when present, `user_agent` truncated to 256 bytes
at a UTF-8 boundary, `principal_type` + `principal_id`. Query strings stay out. Order rule (doc): `web.Logger` sits
INSIDE `TrustProxies` and `RequestID` — it reads both from its own `r.Context()`. `RequestID()` (`:142-144`) reuses an
id already on the context (`sdk.RequestIDFromContext`) instead of minting a second one, so cms's nested
`web.RequestID()` (`pockets/cms/internal/inbound/cms/routes.go:177`) keeps the host's id and header (B9). The principal
problem: the pocket attaches it on a DERIVED request via `sdk.WithPrincipal` (`sdk/identity.go:54`,
`pockets/authentication/logic/authentication/authenticate.go:50`), which the Logger's outer `r.Context()` never sees.
Fix in the root, transport-neutral: `sdk.WithPrincipalCapture(ctx) (context.Context, *PrincipalCapture)` ALWAYS
creates a fresh capture (`atomic.Pointer[Principal]`) LINKED to any capture already on the context;
`(*PrincipalCapture).Principal() (Principal, bool)`; `WithPrincipal` writes through the whole chain (child → parent →
…), LAST write wins INCLUDING a clear (a liveness failure clears before a 401, `machine.go:384`). `web.Logger` installs
one before `next.ServeHTTP` and never reuses a long-lived one (a CLI/cron-installed capture on a base context would
otherwise show other requests' principals); at the end it reads the capture, falling back to
`sdk.PrincipalFromContext(r.Context())` when an outer middleware attached the principal first. The attribute is
defined (doc + README) as "the last principal authenticated during the request, including one the surface then
refused" — `RequirePrincipal` denies on `!set.admits(cred)`/`set.assure` without clearing
(`pockets/authentication/inbound/http/principal.go:76-83`), so those 401/403 lines DO carry `principal_*`;
observability only, never an authorization input. Tests: last-write incl. clear; refused-kind logged; nested Logger
agrees with the outer; two requests on one base context do not bleed; outer-attached fallback; `-race` write after the
handler returned. Zero pocket code changes; two DOC lines change (`identity.go:53`,
`pockets/authentication/logic/authentication/context.go:123-124`). Closes FHS-7 and M2's access-log half.

**5. Connector query logs — `integrations/datastores/pgxdb`, `integrations/datastores/turso`.** `LogQueries` is the
switch; `LogQueryArgs bool env:"DB_LOG_QUERY_ARGS" default:"false"` only WIDENS it — alone, in development, it is INERT
(no tracer installed). Default tracer line: `sql`, `arg_count`, `arg_types` (`%T` per arg), never values; with
`LogQueryArgs` the verbatim `args`. pgxdb: `LoggingQueryTracerConfig{Logger *slog.Logger; LogArgs bool}` +
`NewLoggingQueryTracerWithConfig(cfg)` (struct-input rule); `NewLoggingQueryTracer(logger)` keeps its signature with
the safe default. Both tracers take the context (`InfoContext`/`DebugContext`) so query lines carry `request_id`;
turso's `db.go:64-90` passes its ctx into `traceQuery`. The END line (`pgxdb/tracers.go:40-41`) stops logging
`data.Err.Error()` when `!LogArgs`: pgx encode errors embed the argument via `%#v` and a 22P02 server message repeats
the input — a `*pgconn.PgError` logs `sqlstate`, `constraint`, `command_tag`; anything else logs `error_type` (`%T`)
only; the full text returns only with `LogArgs`. Production posture (R-2): KEEP S1's outright refusal of ANY query
logging — `LogQueries`, `LogQueryArgs` (a stale variable is a signal), or a `Tracer` that is or wraps a
`*LoggingQueryTracer` whatever `LogArgs` says (`wrapsLoggingTracer` no longer reads it); the sentinel text becomes
"production refuses query logging" and the message names field AND variable: `Config.LogQueries (DB_LOG_QUERIES)`.
turso gets the same sentinel and rule (it refuses nothing today): checked BEFORE the TLS early return
(`turso/posture.go:33-35`) and `errors.Join`ed with the TLS refusal the way pgxdb does (`pgxdb/posture.go:85-93`) — a
production turso host with `DB_LOG_QUERIES=true` now fails boot (row 10; no waiver). Development logs the posture WARN.
Loud failure (P4, ruled): `Open` on both connectors returns `sdk.ErrRedactedSecret` (wrapped, naming the field) when a
revealed `DSN`/`URL`/`Password`/`AuthToken` contains `sdk.Redacted`, before any dial. Closes FAS-5.

**6. Config secret fields → `sdk.Secret` (compile-time break for `string`-variable builders; literals unchanged).**
sdk module: `email.SMTPConfig.Password` (`smtp.go:37`; `:74` becomes `cfg.Password.Reveal()`); `NewSMTP` KEEPS its
`*SMTP` signature — it records a password containing `sdk.Redacted`, and `Probe`/`Send` return `sdk.ErrRedactedSecret`
(production hosts probe at boot, `smtp.go:81-85`, so the boot fails there). pgxdb: `DSN`, `Password`
(`postgres.go:91,95`; readers `:179-180,205-206,224`). turso: `URL`, `AuthToken` (`turso.go:63,66`; a URL can embed the
token). goredis: `URL`, `Password` (`client.go:62,66`; `:187-201`). sendgrid: `APIKey` (`sendgrid.go:25`; `:58-77`). s3:
`SecretAccessKey` only (`s3.go:66`; `AccessKeyID` is an identifier; `:114-126`). gcs: `CredentialsJSON` (`gcs.go:39`;
`:126-140`). google/github: `ClientSecret` (`google.go:80`, `github.go:79`; form values at `google.go:166,277`,
`github.go:131,238`). firebase: `CredentialsJSON []byte` → `sdk.Secret` (`firebase.go:102`; `:113` `len(...)` still
compiles, `:114` becomes `[]byte(cfg.CredentialsJSON.Reveal())`) — R-4, breaks EVERY firebase builder. `DSN`/`URL`
migrate too (R-1): FSK-1 cites goredis `URL`; `Redacted()` stays the operator's view. Not touched: otel, go-oidc, auth0
(no secret fields), golang-jwt's `New(secret string, …)` (a parameter, not config — Design 7 covers its field),
`AccessKeyID`, `Username`, every pocket (no pocket production code builds or reads an integration `Config`).

**7. Adapters holding a secret in an unexported field implement `fmt.Formatter` AND `slog.LogValuer`** —
`Format` writes a redacted summary for EVERY verb (`%v`, `%+v`, `%#v`, `%s`), `LogValue` returns a group WITHOUT the
secret (structure for JSON handlers): `google.Provider`/`github.Provider` (`config Config`, `google.go:70`,
`github.go:70`), `sendgrid.Sender` (`apiKey`, `sendgrid.go:41`), `gcs.Store` (`privateKey []byte`, `gcs.go:77`),
`golangjwt.Signer` (`secret []byte`, `golangjwt.go:50-53`; pinned by auth-cms and production-baseline). Without
`Format`, `fmt.Printf("%+v", p)` prints the key through the unexported field (lead-probed); without `LogValue`, so does
slog's TextHandler. PR-3; two methods each, one test each (`%+v`, `%#v`, slog text, slog json contain no secret bytes).

**8. Guard G37 (Makefile, PR-3) — "credential-named fields are `sdk.Secret`".** Pattern
`^\s+([A-Z][A-Za-z]*)?(Password|Secret|SecretAccessKey|APIKey|AuthToken|ClientSecret|CredentialsJSON|PrivateKey|DSN|Token)\s+(string|\[\]byte)`
over `--include='*.go' integrations sdk`, excluding `_test.go`, lines containing `json:"` (inbound DTOs), and
`sdk/capabilities/oauth/oauth.go` by path (`Tokens`, runtime values); must be empty. On `main` today it hits exactly
the eleven migration targets plus `oauth.go:93-96` (lead-run), so it passes once PR-1, PR-2 and PR-3 are merged — PR-3
carries it (P9). `URL` stays out (too generic; the type, not the guard, protects it). Why machine enforcement: FSK-1 is
a shape a NEW integration reintroduces silently (G31 precedent). Self-check: a fixture line the pattern must match
(G35 style). Pockets are out by path (DTOs carry `Password string json:"password"` legitimately).

**9. Workshop (PR-4): P7 gains a clause** — `.env.example` carries no uncommented `DB_LOG_QUERIES=true` or
`DB_LOG_QUERY_ARGS=true` (the turso refusal is new; the pre-deploy signal belongs with `sslmode=disable`,
`posture_rules.go:33`). No P5 name (no acknowledgement exists). Scaffold, under
`workshop/gopernicus/internal/commands/templates/`: `init/main.go.tmpl:74-75` and `init/migrations.main.go.tmpl:114-115`
wrap `os.Getenv(...)` in `sdk.Secret(...)`; `pocket/stores/pgx/conformance_test.go.tmpl:52` and
`pocket/stores/turso/conformance_test.go.tmpl:54` wrap `dsn`/`url`/`token` (templates are invisible to `go build` —
they change in the PR that moves the pins); `init/env.example.tmpl` gains commented `# LOG_REDACT_KEYS=` and
`# DB_LOG_QUERY_ARGS=` lines; pins: `init/go.mod.tmpl:5` sdk v0.17.0 → v0.18.0, `pocket/stores/{pgx,turso}/go.mod.tmpl:7`
pgxdb v0.9.0 → v0.14.0 / turso v0.6.0 → v0.9.0 and `:8` sdk v0.15.0 → v0.18.0 (a stale direct pin fails
`-mod=readonly`, B8).

## Upgrade notes

> **Read first.** (a) Every migrated field (rows 2, 7, 9, 12–17 — `DSN` and `URL` included) renders `[redacted]`
> through `fmt`, `+`, templates and `%s`; a DSN/URL COMPOSED from one would carry the marker, so pgxdb/turso `Open` and
> SMTP `Probe`/`Send` now refuse it with `sdk.ErrRedactedSecret` (loud at boot, not at the wire). Grep every read site
> and add `.Reveal()`: `grep -rnE '\.(DSN|URL|Password|AuthToken|APIKey|SecretAccessKey|CredentialsJSON|ClientSecret)\b'
> --include='*.go' . | grep -v 'Reveal()'`. (b) A production turso host with `DB_LOG_QUERIES=true` (prefixed forms
> included) fails boot, as pgx has since S1: `grep -rnE '^[^#]*[A-Z_]*DB_LOG_QUERY(IES|_ARGS)=true' .env* deploy/`.

Compile-time breaks are rows 2, 7, 9, 12–17: `sdk.Secret(v)` at the literal (an untyped constant needs nothing) and
`.Reveal()` where the host READS the field. Row 10 fails boot. Rows 3 and 8 change log content without a compile error.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `sdk.Secret`, `sdk.Redacted`, `sdk.ErrRedactedSecret`, `PrincipalCapture` | additive | none | sdk@v0.18.0 |
| 2 | `email.SMTPConfig.Password` is `sdk.Secret`; `Probe`/`Send` refuse a composed one | compile error for a `string` variable; `%s` prints `[redacted]` | `sdk.Secret(v)`; `.Reveal()` to read | sdk@v0.18.0 |
| 3 | `logging.New` redacts by default | attrs keyed password/token/api_key/… (Design 3 list) print `[redacted]` | rename the attribute (auth-cms's `seeded_api_key` is the worked example); `LOG_REDACT_KEYS` adds; compose `NewContextHandler` over your own handler | sdk@v0.18.0 |
| 4 | `web.Logger` adds `client_ip`, `request_id`, `user_agent`, `principal_type`, `principal_id` | additive; `remote_addr` kept; must sit inside `TrustProxies`/`RequestID` | `LOG_REDACT_KEYS=client_ip,user_agent` | sdk@v0.18.0 |
| 5 | `sdk.WithPrincipal` writes through to a capture chain | none unless the host reads `PrincipalCapture`; doc only | none | sdk@v0.18.0 |
| 6 | `ContextHandler` skips an id the record carries; nested `web.RequestID` reuses an existing id | one `request_id` per line; nested routers keep the host's id | none | sdk@v0.18.0 |
| 7 | pgxdb `Config.DSN`/`Password` are `sdk.Secret`; `Open` refuses a composed one | compile error for `string` variables; `ParseEnvTags` hosts unaffected unless they READ the field | `sdk.Secret(v)`; `.Reveal()` | pgxdb@v0.14.0 |
| 8 | query log = `arg_count`/`arg_types` + sqlstate/constraint (or `error_type`) on errors | no bind values or error text in dev logs | `DB_LOG_QUERY_ARGS=true` / `Config.LogQueryArgs` (development only; inert without `LogQueries`) | pgxdb@v0.14.0 |
| 9 | turso `Config.URL`/`AuthToken` are `sdk.Secret`; `Open` refuses a composed one | compile error for `string` variables | `sdk.Secret(v)`; `.Reveal()` | turso@v0.9.0 |
| 10 | turso production refuses `LogQueries`/`LogQueryArgs` | `Open` fails `ErrQueryLoggingRefused` naming `Config.LogQueries (DB_LOG_QUERIES)`, joined with any TLS refusal | none — unset it (matches pgxdb since S1; P7 warns pre-deploy) | turso@v0.9.0 |
| 11 | turso query log = arity/types | as row 8 | as row 8 | turso@v0.9.0 |
| 12 | goredis `Config.URL`/`Password` are `sdk.Secret` | compile error | `sdk.Secret(v)` | goredis@v0.4.0 |
| 13 | sendgrid `Config.APIKey` | compile error | `sdk.Secret(v)` | sendgrid@v0.5.0 |
| 14 | s3 `Config.SecretAccessKey` | compile error | `sdk.Secret(v)` | s3@v0.3.0 |
| 15 | gcs `Config.CredentialsJSON` | compile error | `sdk.Secret(v)` | gcs@v0.3.0 |
| 16 | google/github `Config.ClientSecret` | compile error | `sdk.Secret(v)` | oauth/google@v0.3.0, oauth/github@v0.3.0 |
| 17 | firebase `Config.CredentialsJSON` (`[]byte` → `sdk.Secret`) | compile error for EVERY builder, literal or not | `sdk.Secret(string(b))` | firebase@v0.2.0 |
| 18 | provider/sender/store/signer structs implement `Format` + `LogValue` | `%+v`/`%#v`/`slog.Any` of one prints a redacted summary | none | the PR-3 modules incl. golang-jwt@v0.3.0 |
| 19 | P7 flags uncommented `DB_LOG_QUERIES=true`/`DB_LOG_QUERY_ARGS=true`; scaffold pins + `sdk.Secret` wraps | `guard --posture` finding on a stale `.env.example` | comment the line | workshop@v0.9.0 |

Handoff notes (written in the RELEASING section, applied at the roadmap's end): the two "Read first" greps; grep for
`slog.Any(` over a connector/provider struct; for any attribute key in the Design 3 list the host relies on reading;
every turso host scaffolded by `gopernicus init` at workshop ≤ v0.8.0 has `URL: os.Getenv(...)`/`AuthToken:
os.Getenv(...)` in `cmd/server/main.go` and `cmd/migrations/main.go` and must wrap both (B8).

## Risks

- `String()` is the silent footgun: `fmt.Sprintf("%s", cfg.Password)` compiles and yields `[redacted]`; in-repo readers
  are protected by compile errors, hosts are not. `ErrRedactedSecret` turns the DSN/URL/SMTP cases into a boot failure
  that explains itself; a key composed into a sendgrid/s3/gcs/oauth call still fails at first use (loud, not silent).
- `%+v`/TextHandler of a struct holding a secret in an UNEXPORTED field still leaks unless the struct implements
  `Format` (Design 7 covers the five in-repo adapters; a host's own wrapper is its own). `%+v` is never claimed safe.
- Between PR-2's merge and PR-2b's merge, `GOWORK=off go vet` in the twelve store modules fails (their old sdk pins have
  no `sdk.Secret`); go.work CI stays green. PR-2b closes it the same train (R-3).

## Tasks as PRs

### PR-1 — sdk: `Secret`, principal capture, `RedactHandler`, audit-grade `web.Logger`, SMTP field (`sdk/secrets-logging`)
- **files:** `sdk/secret.go` + `secret_test.go` (every verb, `%+v` nested exported field, slog text+json, JSON
  marshal/round-trip, `MarshalText`, `Reveal`, `ErrRedactedSecret`); `sdk/identity.go` + `identity_test.go`
  (`PrincipalCapture`: fresh-per-call, chain write-through, last-write-wins incl. clear, no bleed, `-race`);
  `sdk/pkg/environment/tags.go` (doc) + `tags_test.go` (Design 2); `sdk/pkg/logging/redact.go` + `redact_test.go`
  (Design 3 mechanics incl. matching-key-replaces-group), `logging.go` (`Options.RedactKeys`, composition),
  `logging_env_test.go` (`LOG_REDACT_KEYS`), `handler.go` + `handler_test.go` (skip-if-present); `sdk/pkg/web/
  middleware.go` + `middleware_test.go` (five attributes; UTF-8 truncation; principal set by an inner handler is
  logged; refused-kind logged; cleared principal is not; nested Logger agrees; outer-attached fallback; `RequestID`
  reuse); `sdk/capabilities/notify/email/smtp.go` + tests (`Probe`/`Send` refusal); `sdk/README.md` (Packages rows for
  root/`logging`/`web`/`notify/email`; the policy paragraph; the middleware-order rule). Carried for the `go.work` CI
  gate (S5 PR-1 precedent): `examples/cms/cmd/server/main.go:144` (`sdk.Secret(os.Getenv("SMTP_PASSWORD"))`);
  `examples/auth-cms/cmd/server/demo.go:358` (`seeded_api_key`, `token_path`), `examples/auth-cms/README.md:946,957`,
  `examples/auth-cms/cmd/server/external_demo_test.go:26` (regex) and `:71` (`logging.New`, so the test runs through the
  redacting handler); `examples/production-baseline/.env.example` (commented `# LOG_REDACT_KEYS=` beside `LOG_*`, `:17-19`);
  `pockets/cms/internal/logic/messagingsvc/email_boundary_test.go` if it passes a variable; the two doc lines
  (`identity.go:53`, `pockets/authentication/logic/authentication/context.go:123-124` — doc only, no pocket tag).
- **BREAKING for hosts:** rows 2–3 (compile / log content). Escape hatch: rows 2–3.
- **verify:** `cd sdk && go build ./... && go test -race ./... && go vet ./...`; `cd examples/auth-cms && go test -run
  TestExternalDemo ./cmd/server/`; `make guard` (G12a); `make check`. Run-and-look: Verification §1 (the sdk lines),
  §2(a) and §3 (a real binary each).
- **tag:** `sdk/v0.18.0` (MINOR: new symbols; row 2 is a compile-time break under the v0 convention, named in the tag
  message).

### PR-2 — datastores: `Secret` DSN/URL/token fields, arity/types tracers, `LogQueryArgs`, turso refusal, loud `Open` (`integrations/datastores-secret-args`)
- **depends_on:** PR-1 tagged (`sdk.Secret`).
- **files:** `integrations/datastores/pgxdb/postgres.go` (fields, `LogQueryArgs`, readers `:179-224`,
  `ErrRedactedSecret` check), `tracers.go` (config struct, ctx-carrying calls, arity/types, sqlstate/constraint/
  `error_type` end line), `posture.go` (sources incl. `LogQueryArgs`; `wrapsLoggingTracer` ignores `LogArgs`; sentinel
  and message reworded, env var named), `README.md`, `go.mod` (sdk v0.18.0), tests beside each; `integrations/datastores/
  turso/turso.go` (fields, `LogQueryArgs`, `:130` reader, `ErrRedactedSecret`), `tracers.go`, `db.go` (ctx into the
  tracer), `posture.go` (`ErrQueryLoggingRefused` before the TLS early return, `errors.Join`), `README.md`, `go.mod`,
  tests; `workshop/documentation/docs/guides/persistence.md:18` (`sdk.Secret(os.Getenv("DATABASE_URL"))`) and
  `database-posture.md:165-168` (arity by default, both connectors refuse, `LogQueryArgs`). Carried, TEST-ONLY or
  example, for the `go.work` gate — 54 literals: 51 in `pockets/*/stores/{pgx,turso,goredis}/*_test.go` (the grep
  `pgxdb\.Config\{|tursodb\.Config\{` over `pockets/*/stores`) and 3 under `examples/auth-cms`
  (`cmd/server/jobs_delivery_live_turso_test.go:27`, `cmd/server/jobs_delivery_live_pgx_test.go:27` — behind
  `//go:build livedelivery` — and `internal/outbound/domains/documents/postgres_test.go:34`), plus the two cms example
  literals `examples/cms/cmd/server/main.go:80-81`, `examples/cms/workshop/migrations/main.go:58-59`. No store module's
  production code changes; NO store tag here (the pins move in PR-2b).
- **BREAKING for hosts:** rows 7–11; row 10 fails boot. Escape hatch: rows 7–11.
- **verify:** `cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...`; same in `turso`;
  `cd examples/auth-cms && go vet -tags livedelivery ./...`; `make guard`; `make check`. Then on merged `main` BEFORE
  each tag (the standing rule): the SIX pgx store legs (`POSTGRES_TEST_DSN=…`, each `go test -json -count=1 ./...`)
  before `pgxdb/v0.14.0`; the FIVE turso store legs (authentication, authorization, cms, jobs, events; the truncate-safe
  playground URL only) before `turso/v0.9.0`. Run-and-look: Verification §1 (the connector lines) and §2(b).
- **tag:** `integrations/datastores/pgxdb/v0.14.0`, `integrations/datastores/turso/v0.9.0` (MINOR; compile-time break
  named in both tag messages; turso's boot refusal named).

### PR-2b — stores: repin the twelve store go.mods, no tags (`stores/s6-repin`)
- **depends_on:** Release steps 2 and 3 (pgxdb v0.14.0 and turso v0.9.0 on the proxy).
- **files:** `pockets/{authentication,authorization,cms,events,jobs,secrets}/stores/pgx/go.mod` (+ `go.sum`) → sdk
  v0.18.0 + pgxdb v0.14.0; `pockets/{authentication,authorization,cms,events,jobs}/stores/turso/go.mod` → sdk v0.18.0 +
  turso v0.9.0; `pockets/authorization/stores/goredis/go.mod` (pins sdk v0.15.0, pgxdb v0.9.0, turso v0.6.0 today) → all
  three. Nothing else — the slice's single allowed store-repin PR, spent on exactly the `RELEASING.md:1419-1426`
  precedent (test-only store changes; pins move; no tags).
- **verify:** in each of the twelve: `GOWORK=off GOFLAGS=-mod=readonly go build ./... && go vet ./...` (all twelve exit
  0 on `main` today — B1 — and must again); `make check`; `make guard`.
- **tag:** none (MVS lifts nothing a store's production code needs; hosts are unaffected).

### PR-3 — integrations: `Secret` config fields, `Format` + `LogValue` on secret-holding adapters, G37 (`integrations/secret-config-fields`)
- **depends_on:** PR-1 tagged; PR-2 MERGED (G37 scans pgxdb/turso too).
- **files:** `integrations/kvstores/goredis/client.go` (+ `conformance_test.go`), `integrations/email/sendgrid/
  sendgrid.go` (field, `:58-77` readers, `Sender.Format`/`LogValue`), `integrations/filestorage/s3/s3.go` (`:114-126`),
  `integrations/filestorage/gcs/gcs.go` (`:126-140`, `Store.Format`/`LogValue`; `gcs_test.go` literals),
  `integrations/oauth/google/google.go` (`:166,277`, `Provider.Format`/`LogValue`), `integrations/oauth/github/github.go`
  (`:131,238`, same), `integrations/authentication/firebase/firebase.go` (`:102,114`), `integrations/cryptids/golang-jwt/
  golangjwt.go` (`Signer.Format`/`LogValue`); each module's `README.md` (a "Secrets" sentence AND every `Config` snippet
  wrapped — `sendgrid/README.md:12`, `oauth/google/README.md:10`, `oauth/github/README.md:10`) and `go.mod` (sdk
  v0.18.0); a `Format`+`LogValue` test per adapter; `Makefile` (G37 target + fixture, wired into `guard`).
- **BREAKING for hosts:** rows 12–18. Escape hatch: `sdk.Secret(v)`.
- **verify:** per module `go build ./... && go test ./... && go vet ./...`; `make guard` (G37 green on the branch rebased
  on merged PR-2); `make check`. Live leg on merged `main` before the goredis tag: `.github/workflows/live-stores.yml:
  124-128` (`REDIS_TEST_ADDR` conformance, `go test -race`). Run-and-look: Verification §1 (the adapter lines).
- **tag:** eight, cut at the squash commit: `integrations/kvstores/goredis/v0.4.0`, `integrations/email/sendgrid/
  v0.5.0`, `integrations/filestorage/s3/v0.3.0`, `integrations/filestorage/gcs/v0.3.0`, `integrations/oauth/google/
  v0.3.0`, `integrations/oauth/github/v0.3.0`, `integrations/authentication/firebase/v0.2.0` (message: breaks every
  builder), `integrations/cryptids/golang-jwt/v0.3.0` (all MINOR, break named).

### PR-4 — workshop: P7 clause, scaffold wraps and pins, env comments, posture guide (`workshop/s6-posture`)
- **depends_on:** PR-2 tagged (scaffold pins). Not PR-3 (G37 lives there).
- **files:** `workshop/gopernicus/internal/commands/posture_rules.go` (`sentenceP7`, `evalP7`) + tests; the templates
  named in Design 9 (four `.go.tmpl`, `init/env.example.tmpl`, the three `go.mod.tmpl` pins); `workshop/gopernicus/
  internal/commands/guard_test.go` (its two `Config{` fixtures); `workshop/documentation/docs/guides/
  production-posture.md` ("Added by S6": the turso refusal, `LogQueryArgs`, `LOG_REDACT_KEYS`, `ErrRedactedSecret`, the
  P7 clause).
- **BREAKING for hosts:** row 19 (a posture finding, not a boot change).
- **verify:** `cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...`; `make guard` (G30/G32 still
  green over the examples); a cold `gopernicus init --db pgx` and `--db turso` from the branch build with `GOWORK=off`
  and pass `guard --posture`; `new pocket` store scaffolds compile their conformance tests with `GOWORK=off`; `make
  check`. Run-and-look: Verification §2(d).
- **tag:** `workshop/gopernicus/v0.9.0` (MINOR).

### PR-5 — record: RELEASING section, SECURITY rows, examples repinned, control-map, executed copy (`docs/s6-release-record`)
- **depends_on:** every tag above cold-verified; PR-2b merged.
- **files:** `RELEASING.md` (ONE new top section "Secrets and logging (SOC 2 slice S6)": the twelve-tag table, the
  Upgrade notes above incl. the "Read first" box, the handoff notes — no handoff/manifest files), `SECURITY.md` (rows:
  secrets in logs, access log quality), `examples/*/go.mod`: all seven → sdk v0.18.0; auth-cms → pgxdb v0.14.0, turso
  v0.9.0, golang-jwt v0.3.0; cms → turso v0.9.0; production-baseline → pgxdb v0.14.0, golang-jwt v0.3.0 (no example pins
  the other PR-3 modules — §1 is their only real-binary proof); `.claude/plans/soc2-readiness/control-map.md:24,107`
  (→ DEFAULT, "shipped S6"; `:107` quotes the §2(c) line), `roadmap.md` (an "S6 RELEASED" paragraph),
  `plans/s6-secrets-logging.md` (executed copy with the status line and any build-time deviations).
- **verify:** `make check`; `make guard`; each example builds from tags once with `GOWORK=off` then normally; §4 in full
  and a boot-from-tags smoke of §2(a) only (the rest ran on their PRs; evidence pasted into the section).
- **tag:** none.

## Sequencing

PR-1 first (everything needs `sdk.Secret`) → `sdk/v0.18.0`. PR-2 and PR-3 are developed in parallel (independent
modules; PR-2 carries the store/example test literals, PR-3 none) but PR-2 MERGES first: G37 rides PR-3 and scans the
pgxdb/turso fields. Steps 2–3 after the store legs → PR-2b (the repin) → step 4 after the goredis leg → PR-4 (needs only
steps 2–3) → step 5 → PR-5 last. Three trains for the integrations was the floor: sdk must be tagged before any
consumer, and the datastore pair needs legs the other eight do not.

## Release

| Step | Tag | After | Cold verification |
|---|---|---|---|
| 1 | `sdk/v0.18.0` | PR-1 merge | scratch consumer builds `sdk/...`; the §1 sdk lines print `[redacted]` |
| 2 | `integrations/datastores/pgxdb/v0.14.0` | PR-2 merge + six pgx store legs on merged `main` | builds; the MVS set — pgxdb v0.14.0 beside authentication `stores/pgx` v0.11.0, authorization v0.21.0, cms v0.5.0, events v0.4.0, jobs v0.7.0, secrets v0.2.1 — `go.mod` selects v0.14.0, `go mod verify`, `go build` (S5 step 2 shape) |
| 3 | `integrations/datastores/turso/v0.9.0` | PR-2 merge + five turso store legs | builds; MVS set beside authentication `stores/turso` v0.10.0, authorization v0.20.0, cms v0.4.0, jobs v0.6.0, events (current) |
| 4 | goredis v0.4.0, sendgrid v0.5.0, s3 v0.3.0, gcs v0.3.0, oauth/google v0.3.0, oauth/github v0.3.0, firebase v0.2.0, golang-jwt v0.3.0 | PR-3 merge + goredis live leg | each builds cold; golang-jwt v0.3.0 beside sdk v0.18.0 in a scratch consumer (auth-cms and production-baseline pin it) |
| 5 | `workshop/gopernicus/v0.9.0` | PR-4 merge | cold `init` from the tag requires sdk v0.18.0 / pgxdb v0.14.0 (or turso v0.9.0), builds, `guard` + `guard --posture` clean |

Twelve tags. Steps 2–4 after 1 (2–3 before 4 — PR-3 merges after PR-2); PR-2b after 2–3; 5 after 2–3. **Not tagged:**
every store module (PR-2b moves pins only), every pocket (no code; two doc lines), `pockets`, otel, go-oidc, auth0,
every `views/goth`. No migration. Nothing retracts. Tags annotated, not signed; one push / tag / merge per command
(classifier rule). Live legs mandatory before steps 2, 3 (store legs) and 4 (goredis), on merged `main`, fresh scratch
Postgres 17 / the truncate-safe turso playground / local Redis, counted with `go test -json`, pass/fail/skip recorded
in PR-5. Cold-verify = `GOWORK=off`, empty module cache, `proxy.golang.org` + `sum.golang.org`, `go mod verify`,
`go build <module>/...`; poll the `.info` URL before any `go mod tidy` (negative-cache lesson).

## Verification

`$B` = the example's origin. Examples build from tags after PR-5's repins (once with `GOWORK=off`, then normally).
Every real-binary leg below runs in DEVELOPMENT mode: a production boot needs TLS Postgres and an https issuer (the S5
lesson); the one production-only behaviour (the refusal) is proven by a joined boot error and by the §1 program.

1. **Scratch program (a real binary; the scratchpad, never the repo; `go.work` resolves the branch):**
   (a) builds `pgxdb.Config{DSN: sdk.Secret("postgres://u:hunter2@h/db"), Password: "hunter2"}`,
   `tursodb.Config{URL: "file:probe.db", AuthToken: "hunter2"}`, `goredis.Config{Password: "hunter2"}`,
   `email.SMTPConfig{Password: "hunter2"}`, `sendgrid.Config{APIKey: "hunter2"}`, `s3.Config{SecretAccessKey:
   "hunter2"}`, `gcs.Config{CredentialsJSON: "hunter2"}`, `google.Config`/`github.Config{ClientSecret: "hunter2"}`,
   `firebase.ClientConfig{CredentialsJSON: "hunter2"}`; prints each with `fmt.Printf("%+v %s %q %#v\n", …)`, logs each
   with `slog.NewTextHandler` and `slog.NewJSONHandler`, `json.Marshal`s one — every line reads `[redacted]`;
   `Reveal()` prints `hunter2` once, on purpose. (b) `LOG_REDACT_KEYS=probe_only` parsed by `ParseEnvTags` into
   `logging.Options`, then `log.Info("probe", "password", "hunter2", "x-api-key", "k", slog.Group("g", "token", "t"),
   "probe_only", "k2")` through `logging.New` — none of `hunter2`/`k`/`t`/`k2` appear. (c) After PR-3: each adapter
   (`google.New(...)`, `github.New(...)`, `sendgrid.New(...)`, `gcs.New(...)`, `golangjwt.New("hunter2")`) printed with
   `%+v` and `%#v` and logged with `slog.Any("adapter", a)` on text and json handlers — no `hunter2`. (d) turso refusal:
   `tursodb.Config` through `ParseEnvTags` with `DB_URL=file:probe.db DB_LOG_QUERIES=true`; `RuntimeMode` production →
   `Open` fails with `ErrQueryLoggingRefused` naming `Config.LogQueries (DB_LOG_QUERIES)`; development → one `Exec`
   logs `sql`/`arg_count`/`arg_types` (no values) plus the posture WARN; `DB_LOG_QUERY_ARGS=true` ALONE in development →
   no tracer line (inert). (e) loud failure: `pgxdb.Open` with `DSN: sdk.Secret(fmt.Sprintf("postgres://u:%s@h/db",
   cfg.Password))` fails `ErrRedactedSecret` before any dial; `email.NewSMTP(...)` with a composed password → `Probe`
   returns `ErrRedactedSecret`.
2. **production-baseline (`examples/production-baseline`, `make run`, development, `TRUSTED_PROXY_COUNT=1`):**
   (a) `curl -si $B/healthz -H 'X-Forwarded-For: 203.0.113.9' -H 'X-Request-ID: s6probe' -H "User-Agent: $(python3 -c
   'print("U"*300)')"` → ONE request line with `client_ip=203.0.113.9`, `remote_addr=127.0.0.1:…`, `request_id=s6probe`
   exactly once, `user_agent` of 256 bytes, no `principal_*`, no query string. (b) `DB_LOG_QUERIES=true`: register a
   user → query lines carry `sql`, `arg_count`, `arg_types`, `request_id`; the email and the password hash do NOT appear;
   register the same email again → the failing INSERT logs `sqlstate=23505` and `constraint`, no argument text; add
   `DB_LOG_QUERY_ARGS=true` → `args` reappear and the posture WARN prints. (c) the Bearer flow (`README.md:163`), then
   one authenticated call with `X-Forwarded-For: 203.0.113.9` and `X-Request-ID: s6who` → ONE line carrying
   `client_ip=203.0.113.9`, `principal_type`, `principal_id`, `request_id=s6who` — pasted into PR-5 as the evidence for
   `control-map.md:107`. (d) boot in production with a plaintext DSN and `DB_LOG_QUERIES=true` → one joined error naming
   `ErrQueryLoggingRefused` (`Config.LogQueries (DB_LOG_QUERIES)`) and `ErrTLSRequired`; `gopernicus guard --posture` on
   a copy whose `.env.example` has an uncommented `DB_LOG_QUERIES=true` → one P7 finding (PR-4).
3. **auth-cms (`examples/auth-cms`, development, memstore — no database):** log in through the browser, load `/admin` →
   that request line carries `principal_type=user`, `principal_id=<id>`; the cms pocket's nested Logger line and the
   outer line both show the principal and the SAME `request_id`. A request with an expired access token (401) carries
   NO `principal_*` (the cleared principal). `AUTH_EXTERNAL_DEMO=1` boot → the WARN prints `seeded_api_key=<KEY>` and
   `token_path=/dev/token` (not `[redacted]`); `curl -i -H "Authorization: Bearer <KEY>" $B/demo/external/hybrid` → 200;
   the same key on `/demo/external/only` → refused, and that line carries `principal_type=service_account` (the
   refused-kind definition).
4. **examples/cms (turso):** boots from the tag with its two wrapped literals; SMTP configured via env still
   authenticates (`Reveal()` on the PLAIN auth path, `smtp.go:74`); `make run` + one admin page load → access lines
   carry one `request_id` and `principal_*`.

What must NOT appear in any log of §2–§4: a DSN, a Redis password, an SMTP password, a SendGrid key, a service-account
private key, a bind value, a password hash, or an attribute value under a Design 3 key (the access line's `error` for a
22P02 is the documented residual — §2(b) uses 23505 for that reason). Green tests alone do not close PR-1, PR-2 or
PR-3; §1–§3 are the closing evidence (pasted into PR-5).

### Build-time deviations (lead rulings during execution; PRs #182–#186)

1. PR-1 sdk: `x_api_key` ADDED to `DefaultRedactKeys` (17 keys; lead ruling — the plan's Design 3 list had 16; the list stays exact per key, with the ratified `-`/`_` equivalence, so `x-api-key` and `X-API-Key` match it too; no prefix matching: `x_api_key_id` is kept). §3's refused-kind route is `/demo/external/mfa` (authenticated then refused), not `/demo/external/only` (an API key is refused before authentication, so no `principal_*`). auth-cms has no nested cms Logger in its real wiring (only `BuildRouter` has one) — proven by `TestLogger_NestedAgreesWithOuter` + a scratch binary. The outer-principal fallback lives in the capture (`WithPrincipalCapture` starts from the principal the context carries; the Logger reads only the capture) to keep last-write-wins. `ContextHandler` skips `trace_id`/`span_id`/`request_id` when already on the record. Documented limit: never key a map by `Secret` (encoding/json writes string-kind keys as-is). Non-sdk files carried for the workspace gate: `examples/cms/cmd/server/main.go` (SMTP password wrap — the only example line the field change broke), auth-cms demo rename + README + test logger, production-baseline `# LOG_REDACT_KEYS=`, one doc comment in `pockets/authentication/logic/authentication/context.go` (no pocket tag). README Leg 3's "1s TTL" is stale (golang-jwt one-minute leeway) — follow-up.

2. PR-3 integrations (built @ fef8d3be, held until PR-2 merges — G37 flags PR-2's `pgxdb DSN/Password` + `turso AuthToken` lines on main): goredis wraps landed in `client_live_test.go:58` + `client_address_test.go:128`, not `conformance_test.go` (plan :340); the gcs constructor is `Open(ctx, cfg, …)` not `New` (plan :427) and `golangjwt.New` needs ≥ 32 bytes (`ErrSecretTooShort`); G37 regex uses `[[:space:]]` (G35 style, BSD/GNU-safe) and its self-check compares the exact matched set (a count-only check let an over-broad pattern through); `Format`/`LogValue` have POINTER receivers (every constructor returns a pointer; printing a dereferenced value bypasses them — documented limit); `Format` prints non-secret identifiers + `Secret:[redacted]`, `LogValue` groups omit the secret entirely; three wire assertions added (google/github exchange + refresh send the real `client_secret`). LEFT FOR PR-5: `README.md:92,121` + `examples/cms/README.md:91,108` "thirty-five" → thirty-six; `AUDIT.md` historical snippets untouched on purpose.

3. PR-2 datastores (#183): four scaffold `.go.tmpl` wraps RIDE PR-2 (Design 9's "templates are invisible to go build" was wrong — the workshop scaffold tests replace sdk/connectors with the checkout; pins/P7/env/guide stay in PR-4 — until PR-4 moves the pins a turso scaffold from main HEAD at GOWORK=off will not compile); 56 literal sites wrapped (plan said 54 + two cms; `jobs_delivery_live_turso_test.go` was missing from the list); PRE-EXISTING ROT not fixed: `examples/auth-cms/cmd/server/jobs_delivery_live_test.go` has not compiled under `-tags livedelivery` since #90 (13 stale API calls) — owner follow-up; `LogQueryArgs` alone in development logs one "inert without LogQueries" WARN and installs no tracer; `arg_count`/`arg_types` stay on the line when LogArgs adds `args`; `constraint` always present (empty) on a PgError end line; `request_id` on query lines needs `logging.NewContextHandler` (the tracer does not add it); NamedArgs queries log `arg_count=1 arg_types=[pgx.NamedArgs]`; the redaction check matches a literal `[redacted]` only, not URL-escaped `%5Bredacted%5D` (fails at the wire on auth — follow-up); the §2 23505 came from racing eight first logins of one subject (a second subject with the same email provisions a separate user). `production-posture.md` + `init/readme.md.tmpl` still describe the pgx-only refusal — PR-4's.

4. CodeQL on PR-2 (#183): alert 14 `go/clear-text-logging` at `integrations/datastores/turso/tracers.go:47` (the opt-in `args` attribute) — it is the SAME flow as pre-existing open alert 13 on main (`tracers.go:32`, previously ungated); the PR narrows it (development-only, `LogQueryArgs` opt-in with a WARN, production refuses at Open — ruling R-2). Dismissing the alert via the API was DENIED by the session's auto-mode classifier (CI bypass); #183 was merged with the REQUIRED checks (`check`, `vuln`) green — CodeQL is not a required check. OWNER ACTION: dismiss alert 14 (won't fix) with the reason above, which also retires alert 13; record the dismissal in RELEASING (PR-5). Two other pre-existing open clear-text-logging alerts on main are unrelated to S6 (`sdk/capabilities/notify/console.go:36` dev console sender, `examples/auth-cms/cmd/server/main.go:139`).

5. PR-4 workshop (#186): P7 matches prefixed keys (`AUTH_DB_LOG_QUERIES`), any `strconv.ParseBool` true (`1`, `TRUE`) and quoted values (the plan was silent on spelling); the `# DB_LOG_*` comment lines are pgx-only (the turso scaffold builds its config from `TURSO_*`, no ParseEnvTags — its README names `Config.LogQueries`/`LogQueryArgs`); the `=true` form per the ruling, not Design 9's bare `# DB_LOG_QUERY_ARGS=`; `guard_test.go`'s two `Config{` fixtures needed no change (untyped literals assign to `sdk.Secret`); `host-contract.md` untouched (it does not state the P sentences); the guide names PR-3's Secret fields and G37 ahead of their tags and points to RELEASING. LEFT: scaffold README guard line `@v0.8.0` → `@v0.9.0` in PR-5; FOLLOW-UPS (pre-existing, not S6): `pocket/core.go.mod.tmpl` pins sdk v0.15.0 (the core uses no Secret), `pocket.go` `pgxVersion = "v5.8.0"` is stale against v5.9.2 (tidy lifts it; an untidied pgx store under -mod=readonly would fail).

## Open rulings — YOUR CALL

Both reviewers agreed with every recommendation below; the lead's call is shown. The owner ratifies or overrules.

- **R-1 Migrate `DSN`/`URL` fields too, or pure credentials only?** Lead: MIGRATE — a DSN with an embedded password IS
  the FSK-1 finding (`goredis.URL` is its cited evidence; a turso URL can carry the token); `Redacted()` remains the
  display path; the "Read first" box names DSN/URL composition and `ErrRedactedSecret` makes the composed case loud.
  Cost: 54 test-only literal wraps (PR-2) + PR-2b, which the turso `AuthToken: token` literals need regardless.
  Alternative: `Password`/`AuthToken`/keys only — `%+v cfg` keeps printing the DSN.
- **R-2 Production query logging: keep S1's outright refusal, or narrow it to `LogQueryArgs`?** Lead: KEEP the refusal
  of any query logging, make arity/types the development default, give turso the same refusal (before the TLS early
  return, joined; the error names `Config.LogQueries (DB_LOG_QUERIES)`). Reason: even an arity-only line can leak through
  pgx's encode-error text and 22P02 messages, so production-legal query logging would need a second redaction layer.
  Alternative: narrow (production logs `sql` + arity/types) — the end-line strip and the `wrapsLoggingTracer` change
  happen regardless.
- **R-3 Stale store `go.mod` pins after the test wraps.** Lead: PR-2b — the slice's one store-repin PR, after the
  pgxdb/turso tags: all twelve store go.mods → sdk v0.18.0 + pgxdb v0.14.0 / turso v0.9.0, NO store tags, gated on
  `GOWORK=off go build ./... && go vet ./...` per module (`RELEASING.md:1419-1426` precedent). Rev 1's "record the gap"
  rested on a false premise: every store passes `GOWORK=off` today, and S7 repins authorization stores only.
- **R-4 firebase `CredentialsJSON []byte` → `sdk.Secret` (an eighth PR-3 module/tag)?** Lead: YES — a full
  service-account key held as config, the same shape as gcs; `[]byte(cfg.CredentialsJSON.Reveal())` at `:114`; the tag
  message says it breaks every firebase builder. With it, G37's `[]byte` arm has a real target. Alternative: leave
  `[]byte` and list firebase as a G37 exclusion.
- **R-5 `RedactHandler` with no off switch?** Lead: NONE — the key list is exact, additive and documented; a host that
  must log one of those keys renames the attribute (auth-cms's `seeded_api_key` is the in-repo case, fixed in PR-1) or
  composes its own handler. Alternative: `LOG_REDACT_DISABLED=true` with a production WARN — a new posture knob, hence
  a P5 name.

## Notes

- **Cut for the 500-line cap (detail, not scope):** per-test-case lists for `secret_test.go`/`redact_test.go`; the
  `RedactHandler` algorithm as pseudocode; the exact `sentenceP7`/`evalP7` wording; the SECURITY.md row text; the
  docs-site guide prose; the cold-verify shell; the full 54-file literal list (it is the grep in PR-2).
- **Gate record:** the two-reviewer gate is standing; the consultation record (lead-backend-engineer one hop at rev 1,
  then the two reviews at rev 2) goes into the executed copy's Notes as one line.
- **Versioning posture:** every `string`→`Secret` field is a compile-time break shipped under MINOR, per the v0
  convention and the S1 rule's spirit (a compile error is louder than any acknowledgement); each tag message names it.
- **Naming:** no `authz`/`authn`; attributes are `principal_type`/`principal_id`; the capture is `PrincipalCapture`.
- **Not relitigated:** migrations host-owned; the `environment.Secret` function name; S1's pgxdb refusal (R-2 keeps
  it); the one-store-repin-PR rule (spent on PR-2b, the only store PR); pgxdb/turso's `redactedDSN = "REDACTED"`
  display string stays (tests pin it; a display string, not the type).
