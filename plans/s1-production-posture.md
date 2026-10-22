# S1 — Production posture floor (SOC 2 readiness, slice 1) — rev 2

**Status:** RELEASED 2026-10-22 — all ten tags published ANNOTATED at their merge commits and
COLD-VERIFIED on the first poll (record: `RELEASING.md` "Production posture floor"; host handoff
`plans/soc2-s1-production-posture-handoff.md`; manifest `plans/s1-production-posture-release-manifest.json`;
this file copied verbatim to `plans/s1-production-posture.md` by PR-8 = **#137** `docs/s1-release-record`, worktree
`../gopernicus-s1-rec`); PR-7 `examples/production-baseline` = #136 open, untagged; control-map S1 rows
flipped to DEFAULT. History: RATIFIED 2026-10-22 (owner: "merged. ratified. let's go"; O-1…O-6 taken at the
recommendations; O-4 is filled in by the owner in the PR-8 handoff). Review gate RUN the same day
(five × RATIFY WITH AMENDMENTS, folded in rev 2). EXECUTING 2026-10-22 from `main` @ a4597fb2 (after #127), four implementers in parallel:
PR-5 `../gopernicus-s1-cms` (`cms/admin-deny-by-absence`), PR-1 `../gopernicus-s1-sdk`
(`sdk/posture-floor`), PR-2 `../gopernicus-s1-conn` (`integrations/posture-floor`), PR-3
`../gopernicus-s1-authz` (`authorization/posture-floor`) — PR-2/PR-3 need no new sdk symbol, so
they run beside PR-1; PR-4 waits for PR-1 + PR-3; PR-6 waits for tags 1–2.
**BUILT 2026-10-22, lead-reviewed, CI green:** PR-5 = **#129** (ready), PR-1 = **#130** (ready;
`FrameOptions` zero → DENY + `DisableFrameOptions` added after review), PR-2 = **#131** (ready),
PR-3 = **#128** (one `check` run still in progress at last look). Execution amendment: the mode
is never assigned in `.env` (strict `LoadEnvForMode`); scaffold/P7/verification text corrected.
Known CI flake seen twice today: `integrations/kvstores/goredis` `TestOpenHonorsDeadlineAfterConnection`
(not an S1 change; rerun passes) — worth its own issue.
**RELEASE PROGRESS:** #129 MERGED @ 2f789281 → `pockets/cms/v0.4.0` (row 0) and #130 MERGED @
bfaf233a → `sdk/v0.16.0` (row 1), both ANNOTATED (owner 2026-10-22: no tag signing until a
dedicated release-signing key exists; docs corrected in PR #132, which also removes
`.github/dependabot.yml` — owner wants to research Dependabot first; its first run opened #125/#126,
closed). Both tags COLD-VERIFIED 2026-10-22 on the first `.info` poll (scratch consumer, `GOWORK=off`, empty
module cache, `go mod verify`, `go build`); the sdk consumer auto-switched to go1.26.10 (the S8 floor).
PR-4 BUILT: **#134** (retargeted to `main` after #128 merged; 60 files; `make check` green;
lead-reviewed: `NewKeys`, `checkPosture`, `RequireBrowserSafeMutation`). Deviations accepted:
`SingleFactorAcknowledged` lives on `IdentityConfig` (the pocket has no exported `Config`);
`ErrCredentialPolicyRequired` deleted (no references outside the pocket). Follow-up: the OAuth
development-only check runs at the root only (a host calling `authlogic.New` directly bypasses it).
PR-6 BUILT: **#135** (stacked on `authentication/posture-floor`; 40 files; `make check`,
`make guard` with G31–G33, `make test-ui-browser` 441 pass, cold scaffold boot shows HSTS and the
production refusals). Evaluator interpretations accepted: `--list` H-only unless `--posture`; P4
host-level; P7 requires the commented mode mention; a wrapped served handler is a P2 finding. #133 MERGED @ 10135055 → tag 3b cut ANNOTATED (`stores/pgx/v0.19.0`, `stores/turso/v0.18.0`),
both COLD-VERIFIED on the first poll;
#134 MERGED @ b8036dd0 → authentication live legs PASSED on `main` (pgx bare + schema on the
scratch Postgres; turso on the local libSQL server; core) → tag 4 `pockets/authentication/v0.20.0`
cut ANNOTATED @ b8036dd0, COLD-VERIFIED on the first poll; #135 MERGED @ 2de17b50 → tag 5 `workshop/gopernicus/v0.5.0` cut ANNOTATED @ 2de17b50
— COLD-VERIFIED on the first poll; cold `@v0.5.0 init --db pgx` builds, `.env.example` is
P7-clean, `guard --posture` clean; `@v0.4.1 guard --posture` prints `exit status 2` (the binary
exits 2; `go run` returns 1 — non-zero either way). ALL TEN TAGS PUBLISHED AND COLD-VERIFIED; #136
retargeted to `main` with `main` merged in (two six-vs-seven-hosts conflicts resolved to the
branch side), CI green, READY (one CodeQL `go/clear-text-logging` alert on `main.go:98` dismissed as
a name-heuristic false positive — `Passwordless` kinds and `PasswordResetURL` are not credentials; the
dismissal reason is recorded on alert #12). PR-8 (release record) in progress.
PR-7 BUILT: **#136** (stacked on `workshop/guard-posture`; 38 files; `guard --posture` clean with
zero `ack`/`rand` lines; `make check` green; live run-and-look matched Verification steps 1–2 incl.
zero connection attempts on the TLS refusal). Deviations accepted: only two keys needed (challenges,
contact changes, machine identity unwired); authenticator = `RequirePrincipal(Accept(
CredentialExternalToken), Live())`; authorization migrations renumbered 0021–0023 in the host
ledger. Follow-up DONE on #136: `cmd/bootstrap` grants the first global `admin` through
`Mutations.AssignRole` under `audit.Source{System: "bootstrap"}`, idempotent, verified live (403 →
bootstrap → 200, two attributed `iam_audit` rows). Module count corrected to 49.
#128 MERGED @ ddcb1468 (PR-3), #131 MERGED @ 31d57904 (PR-2), #132 MERGED @ 95f1ac08 (docs cleanup).
Live store legs on merged `main` gate tags 2 and 3a: turso connector + authorization turso store
`-tags=integration` PASSED against a local libSQL server (the hosted playground's group is gone on
Turso, `404 group not found`, so the hosted leg cannot run — owner item); pgx legs (`make test-stores`
against scratch Postgres 17 on 127.0.0.1:55433) PASSED (18 legs, 0 failures). Tags 2 and 3a cut
ANNOTATED at their merge commits: `integrations/datastores/pgxdb/v0.10.0`, `…/turso/v0.7.0`,
`integrations/tracing/otel/v0.3.0` @ 31d57904; `pockets/authorization/v0.28.0` @ ddcb1468. All four COLD-VERIFIED on the first `.info` poll. 3b (stores repin to core v0.28.0 → `stores/pgx/v0.19.0`, `stores/turso/v0.18.0`) in progress as its own PR. Then the stores repin PR → 3b. PR-6
waits for PR-4 to land (both touch auth-cms `main.go`). Rev 1 is kept beside this file; the five reviews are in the session
scratchpad (`review-s1/`). Anchors re-read on `main` @ 8ad6d911 (S8 merged; PR #127 S0 docs open).
Parent: `roadmap.md` (S1 slice; rulings 1–4 RULED, defaults 5–8 in effect). Evidence: `review.md`
§1/§4 and `passes/`.

PR numbering keeps rev 1's labels so the reviews stay citable; the ORDER changed (PR-5 ships first).

## Context

The only fail-closed production matrix in the framework is the authentication pocket's
(`pockets/authentication/constructor.go:66` takes `environment.Mode` positionally; sixteen checks
in `passes/01-auth-core.md`). Everything else is mode-blind: `pockets/authorization/constructor.go:39-72`
validates typed nils and pairings only; `pockets/cms/cms.go:80-87` says "Nil disables gating";
`pgxdb/postgres.go:104-105` and `turso/turso.go:76-78` guard `LogQueries` with a comment;
`sdk/pkg/web/run.go:23` serves plain HTTP and no file in the repo writes HSTS;
`environment.LoadEnv` (`config.go:16-18`) reads `.env` in every mode; `otel/config.go:47-48`
defaults the exporter to stdout through its env tag. The scaffold (`templates/init/env.example.tmpl:30-38`,
`main.go.tmpl:80`) emits no mode, no `TrustProxies`, no `PUBLIC_BASE_URL`, and `DB_SSLMODE=disable`.
The harness mints per-boot keys for four of five auth secrets in every mode
(`examples/auth-cms/cmd/server/demo.go:210-217,245,295,319`), ships a fake OAuth provider that
production constructs (`main.go:961`; `production_test.go:60-95`), and dumps the audit rail to any
session under `AUTH_DEBUG=1` (`demo.go:140-145`).

S1 closes, by ID: **B1/FOP-1**; **H1** (FAC-1, FAZ-1, FAZ-13); **H4** (FAC-3, FHC-5, FHC-6,
FHC-7, FHS-2, FAZ-5); **H5** (FHS-1, FHS-5, FAS-1, FHC-2, FHC-13, FSK-3); the mode-aware half of
**M11** (FAS-5, FSK-4, FSK-5, FSK-8); and the ruling-1 posture check for **H3/FAC-2**.

Left to later slices: denial recording, the decision observer, tamper evidence (S2); retention and
erasure (S3); native MFA, idle timeout, `APIKeyMaxTTL`, breach checker (S4); `no-store` on `/auth`,
per-route IP budgets, `netip`, cookie `Domain`, the cms admin cookie-mutation origin layer (the
admin forms carry no CSRF token, so only an origin-only gate can sit in front of them) (S5); the
kernel `Secret` type, `RedactHandler`, arity-only query logs (S6); Redis HMAC, access review (S7).
A mode for events, jobs, secrets and goredis is deferred: none has a development-only option
beyond in-memory stores, which P1 surfaces. The cms mail STARTTLS refusal is deferred to the cms
minor that next repins sdk (cms mail carries contact-form notifications, no secrets).

**Dropped from rev 1 after review:** `pgxdb.Config.RequireTLS`/`DB_REQUIRE_TLS` (redundant with
the mode); `LogQueriesAcknowledged` (production refuses query-argument logging outright; S6
revisits with arity-only logs); `email.WithRuntimeMode` (no S1 consumer; the two-line
`notify.CheckTransport` recipe goes in the `email` package doc); `notify.AllowOpportunisticTLS`
(replaced by the opt-in refusal below); `KeysFromEnv` reading the process environment (replaced by
`KeysInput` + `NewKeys`).

## Design

### The one principle

**One production posture, fail-closed, visible.** A component that owns a development-only or
unprovable option refuses it when the host says `production` unless the composition root writes
an explicit acknowledgement; in `development` it WARNs. The posture is asserted three times in one
vocabulary: at construction (pocket/connector), before boot in CI (`gopernicus guard --posture`),
and in the artifact an auditor samples (the scaffold and `examples/production-baseline`).

### Mode semantics (uniform across every module S1 touches)

- **Zero mode is mode-blind everywhere**: no production refusal, no posture WARN, and no call into
  anything that validates a mode (`notify.CheckTransport` rejects an empty mode,
  `notify/posture.go:76-78`; cms calls it only when a non-zero mode was supplied). The one
  exception is the owner-approved BREAKING cms default, which carries its own WARN.
- **A non-zero mode is validated** with `environment.ValidateMode` at construction
  (`ErrModeRequired`/`ErrModeInvalid`, `mode.go:31-37`); `Mode.IsProduction()` is false for
  `Production`/`prod`, so invalid spellings fail loudly instead of silently running development.
- **Mode threading is additive.** Authentication already requires a mode; every other module gains
  an optional mode whose zero value is today's behavior. Every tag is minor except the cms admin
  flip.
- **Every `RuntimeMode` and `…Acknowledged` field is programmatic**: no `env:` tag, asserted by a
  test that runs `ParseEnvTags` with a same-named variable set and finds the field zero
  (`ParseEnvTags` fills any tagged field, `tags.go:63-100`; hosts run it over `cms.Config` and
  `pgxdb.Config`). The mode has ONE source: the process environment, read by the composition root.
- **Production logs one WARN per honored acknowledgement** at construction
  (`posture_waiver=<Flag>`), so the runtime log evidences each waiver, not only `guard --posture`.

### The acknowledgement-flag pattern (exact names)

An acknowledgement is a wiring assertion — a boolean literal in the composition root, never an env
knob (precedent `DeliveryJobsAcknowledged`, `pockets/authentication/config.go:263-272`). Two
classes:

- **Wiring-fixed waivers** (a nil audit port, an absent admin gate): an acknowledgement beside the
  thing it waives is a construction error in every mode, zero included, in the `ErrXWithY` shape
  of `ErrHTMLPolicyWithoutViews` (`configuration_errors.go:117-123`).
- **Environment-varying waivers** (TLS class of the DSN, STARTTLS relay, single-factor login): the
  acknowledgement is inert when nothing is waived — one binary runs in several environments —
  exactly like `DeliveryJobsAcknowledged` outside jobs mode (`constructor.go:197-218`). No
  contradiction error.

| module | mode input | refused in production | acknowledgement | sentinel (`errors.Is`) |
|---|---|---|---|---|
| `pockets/authorization` | `WithRuntimeMode(environment.Mode) Option` (new; zero = mode-blind) | `Repositories.Tuples` (and `Mutations` when wired) not reporting `audit.RecordingReporter` true; nil `Repositories.Audit` (O-1) | `WithAuditDisabledAcknowledged() Option` | `authorization.ErrAuditRequired` (text distinguishes "absent: store pgx < v0.19.0 / turso < v0.18.0 or a wrapper hides it" from "false: construct `WithAudit()`"); `ErrAuditAcknowledgedWithRecording` |
| `pockets/authentication` | positional (exists) | nil `Repositories.SecurityEvents` | `WithAuditDisabledAcknowledged() Option` | `authentication.ErrAuditRequired`; `ErrAuditAcknowledgedWithSecurityEvents` |
| `pockets/authentication` | positional | password flows on (`!PasswordFlowsDisabled`) OR any `Passwordless` kind configured (decided by ruling 1; a magic link or OTP is AAL1 too) | **top-level** `Config.SingleFactorAcknowledged bool` beside `DeliveryJobsAcknowledged` (`config.go:58`; `Passwordless` is its own group, `config.go:478-531`, so the flag cannot live on `PasswordConfig`) | `ErrSingleFactorUnacknowledged` |
| `pockets/authentication` | positional | a delivery transport declaring `TransportSecurityStartTLS`, at BOTH check sites (`security.go:224-225` and `logic/delivery/router.go:534-544` via `logic/authentication/constructor.go:161-172`) | `DeliveryConfig.OpportunisticTLSAcknowledged bool`, threaded into the logic constructor as `delivery.WithOpportunisticTLSAcknowledged()` | `ErrInsecureDeliveryTransport` wrapping `notify.ErrOpportunisticTLS` (which wraps `notify.ErrInsecureTransport`, so `asInsecureTransport` at `security.go:248-253` keeps matching) |
| `pockets/authentication` | positional | an `oauth.Provider` or `ExternalProvider.Provider` declaring `DevelopmentOnly` (undeclared providers are ACCEPTED — the asymmetry with notify, where undeclared is refused, is deliberate: providers predate the hook) | none — no production use exists | `authlogic.ErrDevelopmentOnlyProvider`, re-exported by the root as `authentication.ErrDevelopmentOnlyProvider` (import direction: root → logic only) |
| `pockets/cms` | `Config.RuntimeMode environment.Mode` (new; zero = mode-blind) | the acknowledgement itself; a Mailer failing `notify.CheckTransport(mode, mailer)` (console senders) — checked only when the mode is non-zero | `Config.AdminUngatedAcknowledged bool` (outside production: mounts ungated + WARN) | `cms.ErrAdminUngatedRefused`; `ErrAdminAcknowledgedWithMiddleware`; `ErrInsecureMailTransport` wrapping `notify.ErrInsecureTransport` |
| `integrations/datastores/pgxdb` | `Config.RuntimeMode` (new, programmatic) | any connection target classified *unverified* or *plaintext* (below); `LogQueries`, or a `Tracer` that is or wraps `*LoggingQueryTracer` (`tracers.go:23,61`) — refused with no acknowledgement | `Config.TLSUnverifiedAcknowledged` (admits *unverified*); `Config.TLSDisabledAcknowledged` (admits all) | `pgxdb.ErrTLSRequired`; `ErrQueryLoggingRefused` |
| `integrations/datastores/turso` | `Config.RuntimeMode` (new, programmatic) | a non-`file:` URL whose scheme is not `libsql`/`https`/`wss`, or a `libsql` URL with `tls=0` (libsql-client-go rewrites it to `http`); checked in `Open` after `dsn()` so the error keeps the redacted target | `Config.TLSDisabledAcknowledged` | `tursodb.ErrTLSRequired` |
| `integrations/tracing/otel` | `Config.RuntimeMode` (new, programmatic) | empty `Exporter` — reachable only once `default:"stdout"` is removed from the env tag (`config.go:47-48`; `Open`'s fill at `:89` keeps development unchanged); OTLP `Insecure` to a non-loopback endpoint (O-3) | none (set `TRACING_EXPORTER`) | `otel.ErrExporterRequired`; `ErrInsecureExporter` |
| `sdk/capabilities/notify` | `CheckTransport(mode, n, opts ...CheckOption)` | with `notify.RequireMandatoryTLS()`: `TransportSecurityStartTLS` — **the default verdict is unchanged** (a changed default would reach hosts on authentication ≤ v0.19.1 through MVS with no waiver available) | callers omit the option | `notify.ErrOpportunisticTLS` (wraps `ErrInsecureTransport`) |
| `sdk/capabilities/oauth`, `sdk/capabilities/authentication` | `oauth.CheckProvider(mode, p)` / `sdkauth.CheckProvider(mode, p)` mirroring `CheckTransport` | a declared `DevelopmentOnly` provider | none | `oauth.ErrDevelopmentOnlyProvider` / `sdkauth.ErrDevelopmentOnlyProvider` |
| `sdk/pkg/web` | none — `pkg` imports the root only (G12b); the host calls it under `mode.IsProduction()` | `ServerConfig.ValidateProduction()`: `PublicBaseURL` unset, not `https`, carrying userinfo, query or fragment, or a path other than `""`/`"/"` (`Origin()` trims a trailing slash, `server.go:59-61`) | none | `web.ErrPublicBaseURLRequired`, `ErrPublicBaseURLInsecure`, `ErrPublicBaseURLMalformed` (never echo userinfo) |
| `pockets/authentication` keys | `NewKeys(mode, in KeysInput, log, need ...KeyName) (Keys, error)` — the pocket never reads the process environment | any NEEDED key unset or malformed, reported together (`errors.Join`, one `ErrKeyRequired` per variable name) | none; development mints an ephemeral key + WARN per unset needed key | `authentication.ErrKeyRequired` (names the variable, never the value; a malformed value wraps `environment.ErrSecretEncoding`/`ErrSecretUnderMinimum` WITHOUT the decoder's text, which names one byte) |

**pgxdb TLS classification** (pgx v5.9.2 `pgconn/config.go:793-838,913-914,400-402`): after
`pgxpool.ParseConfig` (`poolConfig`, `postgres.go:275`) every target — `ConnConfig` and each
`ConnConfig.Fallbacks[i]` — is *verified* (`TLSConfig != nil && (!InsecureSkipVerify ||
VerifyPeerCertificate != nil)`: `verify-full`, `verify-ca`, `require`+`sslrootcert`,
`sslrootcert=system`), *unverified* (`TLSConfig != nil`, skip-verify, no callback: bare `require`),
*plaintext* (`TLSConfig == nil` on a TCP host: `disable`, and the plain fallback `prefer`/`allow`
leave in `Fallbacks`), or *local* (a unix-socket host; pgx never wraps it — the Cloud SQL Auth
Proxy/connector shape; `Open` logs `tls=local_socket`). Production admits a config only when every
target is verified or local. The error names the class, `cfg.Redacted()` and the fix
(`sslmode=verify-full` with `sslrootcert=system` or the provider CA; `verify-ca` + `sslrootcert`
where the certificate does not name the host) and never echoes the DSN. README table: Neon
`verify-full&sslrootcert=system`; Supabase/RDS/Azure `verify-full` + provider CA; Cloud SQL
socket = local, direct IP `verify-ca` + `server-ca.pem`; Heroku-style self-signed `require` +
`TLSUnverifiedAcknowledged`.

**Capability hooks** mirror `notify.Capabilities{DevelopmentOnly}` (`notify/capabilities.go:25-40`):
`oauth.Capabilities{DevelopmentOnly bool}` + `oauth.CapabilityReporter` in `sdk/capabilities/oauth`
and the same pair in `sdk/capabilities/authentication` (duplicated per port — capabilities never
import each other and a per-port record diverges; root admission fails the five-point test).
Detection is a type assertion; the refusal POLICY lives beside each hook (`CheckProvider`), and
the pocket wraps the sentinel at `security.go:442` and `logic/authentication/external_config.go:168`.
`authenticationtest.RunProvider` pins the round-trip when a provider declares one.

**Audit recording capability:** `audit.RecordingReporter interface{ AuditRecording() bool }` is
declared in `pockets/authorization/logic/audit` beside `audit.Reader`; implemented, with
compile-time assertions, by memory `*Tuples`/`*Mutations` and pgx/turso `*tupleStore`/
`*mutationStore` from their audit config (pgx/turso have no `Tuples()` method — the authority is
the `Tuples` FIELD of the `Repositories` bundle they return, `stores/pgx/postgres.go:108`,
`stores/turso/turso.go:90`; only memory has `Store.Tuples()`). `authorization.New` requires
`Repositories.Tuples` — and `Mutations` when wired — to report true. Wrappers and delegating stores
forward the method (README, as with `LoweredPolicy`). Because the stores gain the assertion, they
repin core v0.28.0 (tag 3b after 3a). The storetest case lives inside `RunAudit`, whose factory
already takes the audit flag (`stores/storetest/audit.go:23`).

### `gopernicus guard --posture` — the P rule set

`--posture` adds P0–P8 to the H0–H14 run: same exit codes and output shape (`guard.go:52-99`),
`--list` prints H then P (`contractRow`, `guard_test.go:18`, gains a `P[0-9]+` row regex,
order-checked within each series). The rules are static over the H7 adapter-naming trees —
`cmd/**`, `internal/outbound/**`, the host's `integrations/**`, host `pockets/*/outbound/**` —
plus `workshop/**` main packages, and `.env.example`; runtime refusal is the pocket's job, the
guard proves the composition asks for it. Canonical sentences live in a new `examples/README.md`
§3b ("P0–P8 — production posture, opt-in via `--posture`"); P1, P5 and P6 name a **Recognized
posture surfaces** list beneath the table (mode-accepting constructors and `RuntimeMode` records;
acknowledgement field names; the key constructor), maintained like §3's driver list (`:185-201`):
the sentence names the list, the list names the identifiers, a new surface is a list edit shipped
in a workshop minor (so the authorization-port dependency note below adds surfaces without
rewriting frozen sentences). §1 gains the P-series sentence beside H; the ARCHITECTURE pointer
(`:561-569`) adds "and P0–P8 under `--posture`". `--posture` requires workshop/gopernicus ≥ v0.5.0;
an older pin exits 2 (unknown flag) and CI must treat 2 as a broken invocation, never a pass.
P0/P1/P5/P6/P8 are "no scope" unless the host imports a surface on the list.

| id | the rule, in one line |
|---|---|
| **P0** | The host resolves its runtime mode from the process environment only: through `environment.LoadEnvForMode`, `environment.ParseMode`, or an `environment.Mode`-typed env field tagged `required:"true"` with no `default:`; scanned production files call `environment.LoadEnv(`/`LoadPath(` nowhere; `ModeDevelopment`/`ModeProduction` appear only as an operand of `==`/`!=` or a `case`. |
| **P1** | For every recognized `Config{` literal or `var x <pkg>.Config` in a scanned function, that function names `RuntimeMode:` or `x.RuntimeMode =`; every `authentication.New` call passes a mode argument and every `authorization.New` call passes `WithRuntimeMode(`. (The guard proves the mode is NAMED; the pocket proves it is honored.) |
| **P2** | `web.TrustProxies(` is the first argument of the first `.Use(` call on the handler the composition root passes to `web.Run`/`HTTPServer`. |
| **P3** | `web.SecurityHeaders(` or `web.SecurityHeadersWithConfig(` appears in a `.Use(` call on that handler. |
| **P4** | A host that builds a `web.ServerConfig` calls its `ValidateProduction()`. |
| **P5** | Every recognized acknowledgement in scanned files is a boolean literal (`true` or `false`), never another expression; an option-form acknowledgement is a direct argument of the `New(` call; `--posture` prints one `ack` line (file:line, flag) per `true`, and `ack (conditional)` when it sits under `if`/`switch` — informational, exit code unchanged. |
| **P6** | A host that mounts authentication builds its keys through `authentication.NewKeys(` with a `KeysInput` filled by `environment.ParseEnvTags` in a scanned file; `--posture` prints one informational `rand` line per `crypto/rand` use in scanned files so a review sees any other key minting (no ban: non-auth keys such as auth-cms's cursor key, `documents.go:43`, are legitimate). |
| **P7** | `.env.example`, when present, carries no **uncommented** `sslmode=disable`/`DB_SSLMODE=disable` line; mentions the host's mode key ONLY in a comment (`# APP_RUNTIME_MODE=…` — an uncommented assignment, even empty, is a finding, because `LoadEnvForMode` refuses it); and names `TRUSTED_PROXY_COUNT` and `PUBLIC_BASE_URL`. |
| **P8** | Every `cms.Config` literal in scanned files names `AdminMiddleware` or `AdminUngatedAcknowledged` (the flip is not a boot refusal, so downstream hosts need the pre-deploy signal; G31 covers only this repo). |

Framework guards continue after G30 (`Makefile:659-665`): **G31** `guard-cms-admin-gate` (every
file under `examples/`, `_test.go` included, containing `cms.Config{` names `AdminMiddleware` or
`AdminUngatedAcknowledged`); **G32** `guard-host-posture` (`go run . guard --posture` over every
G30 host plus `production-baseline`); **G33** `guard-pocket-no-env` (O-5: `os.Getenv|os.LookupEnv|
environment.ParseEnvTags|environment.LoadEnv|environment.Secret(` over `pockets/*` production code
excluding `stores/`, making `config.go:835-837`'s posture structural). All join `guard:`
(`Makefile:204-211`), hence `make check`; `vuln` stays outside `check` by design. The Makefile
and `check.yml` "twenty-nine guards" prose is corrected to the real count.

### Scaffold deltas (`workshop/gopernicus/internal/commands/templates/init/`)

- `env.example.tmpl`: first block a COMMENTED `# APP_RUNTIME_MODE=development|production — read
  from the process environment only, never from this file (LoadEnvForMode refuses any assignment
  of the key in .env); development: APP_RUNTIME_MODE=development make run; production: systemd
  Environment=/EnvironmentFile=, docker --env-file, or platform secrets`, `TRUSTED_PROXY_COUNT=0`,
  `PUBLIC_BASE_URL=` (`https://…` in production); pgx block `DB_SSLMODE=verify-full`,
  `# DB_SSLMODE=disable  # local docker only — refused in production` (commented; P7 skips
  comments), and the `?sslmode=disable` example URL rewritten.
- `main.go.tmpl`: `mode, err := environment.LoadEnvForMode("APP_RUNTIME_MODE")` replaces
  `LoadEnv()` (:29); `run` takes `mode`; `srv` is built before the stack;
  `router.Use(web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), web.RequestID(),
  web.Logger(log), web.Panics(log))` replaces :80; `if mode.IsProduction() { if err :=
  srv.ValidateProduction(); err != nil { return err } }`; the connector `Config` gets
  `RuntimeMode: mode`; the mount comment shows passing `mode` to a pocket.
- `migrations.main.go.tmpl`: `LoadEnvForMode("APP_RUNTIME_MODE")` replaces `LoadEnv()` (:43) and
  the connector `Config` gets `RuntimeMode: mode` (:83) — the runner holds the most privileged
  connection and must not read `.env` or skip the TLS check in production.
- `Makefile.tmpl`: `run` and `migrate` export `APP_RUNTIME_MODE=development`. `readme.md.tmpl`: a
  "First run" section (copy `.env.example` to `.env`; the mode is NEVER written there — `make run`
  exports `APP_RUNTIME_MODE=development`, and a `.env` that assigns the key is refused with
  `ErrModeFromFile`; uncomment `DB_SSLMODE=disable` for a plain docker Postgres) and a "Production
  posture" section listing the
  refusals and the CI command `go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.5.0
  guard --posture .` (the scaffold Makefile has no `tool` pin; this is its only route to the check).
- `go.mod.tmpl`: pins `sdk v0.16.0`, `pgxdb v0.10.0`, `turso v0.7.0`.

### `examples/production-baseline` — composition (decided: ruling 1 + default 7)

Module `github.com/gopernicus/gopernicus/examples/production-baseline`: a thin host (`cmd/server`,
`workshop/migrations`, no `internal/`, no host pockets) plus one host integration
`integrations/authentication/devissuer` (a third copy of `examples/external-identity/integrations/
authentication/devissuer`, declaring `sdkauth.Capabilities{DevelopmentOnly: true}`; recorded debt —
a fourth host gets a `devissuer` package inside `integrations/cryptids/go-oidc`, not a fourth copy).
Composition root, in order: `LoadEnvForMode("APP_RUNTIME_MODE")` → `environment.ParseEnvTags` into
`authentication.KeysInput` → `authentication.NewKeys(mode, in, log, KeyJWT, KeyIdentifier)` →
`web.ServerConfig` + `ValidateProduction` under production → `pgxdb.Open` with `RuntimeMode` →
`limiter.StatusCheck(ctx)` (the `ratelimit_windows` DDL ships as a host migration) →
`authentication/stores/pgx` and `authorization/stores/pgx` (`WithAudit()`, `WithIntegrityPolicy`) →
authentication with `SecurityEvents` wired, `PasswordFlowsDisabled: true`, delivery `off`, the pgxdb
durable rate limiter, the identifier keyer from `Keys`, `Secure` cookies, and one `ExternalProvider`
chosen by whether `IDP_ISSUER` is set (unset → the dev issuer in any mode, which production refuses
by capability; set → `sdkauth.NewOIDC` over go-oidc with `IDP_ISSUER`/`IDP_AUDIENCE`/`IDP_JWKS_URL`)
→ `authSvc.HTTP.Register(mount)` (the JSON `/auth/*` surface, including `/auth/csrf`, which is open
to provider-cookie users, `route_policy.go:46-48`) → authorization with `WithRuntimeMode(mode)` and
`Repositories.Audit` → role routes behind `authenticate ∘ RequireBrowserSafeMutation ∘ authorize`,
where `authenticate` is a `PrincipalStrategy` admitting the external-provider credential (header
bearer, and the provider cookie when configured) — NOT `RequireAccessTokenLive()`, which refuses
external tokens (`principal_options.go:432-436` vs `:449-461`) → `router.Use(TrustProxies,
SecurityHeaders, RequestID, Logger, Panics)` → `/healthz`. No console transport, fake provider,
debug route, or acknowledgement anywhere (`guard --posture` prints no `ack` line). Migrations are
exported from both pgx stores into `workshop/migrations/primary` and applied by the host runner
pre-boot (D4).

### Multi-authority hosts (rewritten after review; see roadmap.md "Cross-plan dependencies")

A host with several tuple authorities (one per tenant database plus a control plane) satisfies the
PR-3 check in one of two shapes: **(a)** a registry of per-tenant `Components`, each built with
`WithRuntimeMode(mode)` and its own recording store + `Repositories.Audit`; **(b)** one
`authorization.New` over a host-owned delegating `tuples.Storer` (plus a delegating
`mutations.MutationRepository` and `audit.Reader` when wired) that resolves the tenant from context
and fails closed — an error, never an empty read — when none is present. Its `AuditRecording()`
reports the registry's ADMISSION POLICY: `true` only if registration refuses, in production, any
tenant store whose own `AuditRecording()` is false, and `false` while the registry is empty
(`New` samples it once, so a conjunction over the stores present at boot would be vacuous). A host
that builds `decisions.NewService` directly bypasses `authorization.New` and the S1 check; that is
the level-2 shape and inherits the posture in port slices B–D. A per-tenant connection factory that
runs after boot is provider behavior, not wiring (H10, `examples/README.md:470-481`): it lives in a
host pocket's `outbound/` or the host's `integrations/**` (H7, `:139`; it takes the mode as a
constructor parameter and sets `RuntimeMode` from it), and P0/P1 scan those trees. The baseline
stays single-tenant; a multi-tenant worked example is a second host.

### Dependency note — the authorization port (`.claude/plans/authorization-provider-port.md`)

`WithRuntimeMode` lands on level 3 (`authorization.New`). Slices B–D carry the posture into the
other buy-in levels (plan :41-55) without reopening S1: **level 1** (`integrations/authorization/
authzed`, later `openfga`) gains `Config.RuntimeMode` with production refusing an insecure gRPC
channel and an unset preshared key — the PR-2 connector pattern; **level 2** (the guard over the
bare port, `decisions.NewLoweredEvaluator` + `Require(...)`, no `Components`) gains a
`WithRuntimeMode` option whose production branch requires the port implementation to report
`AuditRecording()` or the host to pass `WithAuditDisabledAcknowledged()`. Both reuse
`ErrAuditRequired`, the Recognized posture surfaces list, and P1/P5.

## Tasks — the PR train (rev 1 labels; new order)

Order: **PR-5 first** (the BLOCKER; needs nothing new from sdk — `notify.CheckTransport` and
`environment.Mode` exist at the sdk v0.9.0 cms pins, verified at `git show sdk/v0.9.0`), then
PR-1 sdk, PR-2 connectors and PR-3 authorization in parallel on PR-1, PR-4 authentication, PR-6
workshop + examples (after tags 1–2 resolve), PR-7 baseline, PR-8 release record. Model `opus`
unless marked. Retarget a stacked child to `main` before merging it; never `--delete-branch` a
stacked base.

### PR-5 — cms: the admin flip (`cms/admin-deny-by-absence`) — BREAKING, ships first

- **files:** `pockets/cms/cms.go` (`Config.RuntimeMode`, `Config.AdminUngatedAcknowledged` — no
  env tags; three sentinels; the Mailer check via `notify.CheckTransport(mode, mailer)` only when
  `RuntimeMode != ""`; the unmount WARN on `m.Logger`, nil-guarded as in `pockets/events/events.go:62`,
  and only when `Views != nil` — with nil Views no admin surface exists, `routes.go:66-69`);
  `pockets/cms/internal/inbound/cms/routes.go:54-148` (`Mount` takes `adminEnabled bool`; every
  admin route registers through one local `handleAdmin(method, path, h)` helper that no-ops when
  disabled, so no admin route can be added outside it; public registrations, exactly: `GET /{$}`,
  `GET /{publicBase}/{slug}` or `GET /{slug}` per routable type, `GET /category/{slug}`,
  `GET /tag/{slug}`, `GET /menu/{slug}`, `GET /media/{id}/file`, `GET /contact`, `POST /contact`);
  `internal/inbound/cms/menus.go:144-157` and `contact.go:36-58` (public error paths render through
  `h.views.Error` — the `PublicHandlers.renderError` precedent, `public.go:162-168` — instead of
  `views.AdminError`, whose admin nav would otherwise leak onto public pages; handler-only, no
  templ edits); tests: `pockets/cms/cms_test.go:61-95` (route-set test split gated/acknowledged/
  neither; nil `Mount.Logger` allowed), `internal/inbound/cms/admin_middleware_test.go:128-138`
  (`TestAdminMiddleware_NilPreservesBehavior` → `TestAdminMiddleware_NilUnmountsAdmin`),
  `helpers_test.go:43-53` (`BuildRouter` gains test-only `WithAdminUngated()`);
  `examples/cms/cmd/server/main.go:157-169` and `examples/minimal/cmd/server/main.go:101-111`
  (`AdminUngatedAcknowledged: true` with a comment naming the demo posture — mode threading comes
  in PR-6); `examples/minimal/cmd/server/goth_htmx_proof_test.go:50` (the acknowledgement);
  `examples/auth-cms/cmd/server/main.go:468` (unchanged gate; `RuntimeMode` threaded in PR-4);
  `examples/cms/README.md` ("admin ungated by acknowledgement — demo only, refused in production");
  `Makefile` (G31); `AUDIT.md` (`AUDIT-053: cms admin routes are deny-by-absence` after :5538: find
  `cms.Config{` literals, choose gate or acknowledgement, verify no admin handler is reached:
  `GET /articles/x/edit` → 404, `POST /media` → 405, `GET /articles` → the public 404 page);
  `workshop/documentation/docs/pockets/cms.md:33,61,105`, `docs/getting-started/quickstart.md:62`,
  `docs/architecture/pocket-contract.md:106-108`, `docs/pockets/overview.md:64-66` (the gated
  recipe beside `AdminUngatedAcknowledged: true // development only`, and "a gate must authorize,
  not only authenticate — compose `Require(Can(...))`"; auth-cms keeps authentication-only behind a
  harness comment because README Leg 0 depends on it); `pockets/README.md` §3 (cms now
  deny-by-absence).
- **behavior:** nil `AdminMiddleware`, no acknowledgement → admin routes NOT mounted, one WARN at
  `Register`; acknowledgement outside production → mounted ungated + WARN; production +
  acknowledgement → `ErrAdminUngatedRefused`; middleware + acknowledgement →
  `ErrAdminAcknowledgedWithMiddleware` (every mode); production + a Mailer failing `CheckTransport`
  → `ErrInsecureMailTransport`. Zero `RuntimeMode` is mode-blind: no Mailer check, no production
  refusal. With admin unmounted, `ServeMux` falls through: `GET /articles` etc. reach the public
  `GET /{slug}` handler (404, or 200 if a published page carries that slug), and `POST` to an admin
  path answers 405 — nothing admin is reachable, and the tests assert the exact public set.
- **BREAKING:** yes (ruling 3; pre-1.0 minor per RELEASING.md "Version bumps"). A host constructing
  `cms.Config` without `AdminMiddleware` loses its admin surface at the bump — loudly (WARN +
  404/405), never silently open. Not a boot refusal: a mixed fleet flaps admin routes, so hosts run
  `guard --posture` P8 before deploying.
- **escape hatch:** `AdminUngatedAcknowledged: true` (demos; refused in production).
- **tests:** route table via a recording registrar: with neither gate nor acknowledgement the
  registered set EQUALS the public list (exact set, so a new route fails until classified); over
  HTTP an admin path answers 404 or 405 and its body lacks `cms-admin-nav`; `GET /menu/nope` →
  404 whose body lacks `cms-admin-nav`; the three sentinels; production + console Mailer refused;
  no-env-tag test for both fields; both examples' `go test ./...`; run-and-look:
  `cd examples/minimal && APP_RUNTIME_MODE=development go run ./cmd/server` → `/articles` 200 with
  the acknowledgement; remove it → `GET /articles/x/edit` 404, `POST /media` 405, WARN logged;
  `/` still renders.
- **verify:** `cd pockets/cms && go build ./... && go test ./... && go vet ./...`; `make guard`
  (G31 red when an example literal names neither field); `make check`.
- **closes:** B1/FOP-1; FSK-8 (cms half).
- **tag:** `pockets/cms/v0.4.0` (release row 0; sdk pin unchanged at v0.9.0). `views/goth` and
  both stores untouched.

### PR-1 — sdk: transport, environment, notify, capability hooks (`sdk/posture-floor`)

- **files:** `sdk/pkg/web/security_headers.go` (new) + test; `sdk/pkg/web/server.go`
  (`ValidateProduction`, three sentinels) + test; `sdk/pkg/web/response.go:38-52,72-77`
  (`RespondJSON`/`RespondJSONError` set `X-Content-Type-Options: nosniff` as their FIRST statement,
  so the 500 on a marshal failure carries it too); `sdk/pkg/environment/config.go`
  (`LoadEnvForMode`) + test; `sdk/capabilities/notify/posture.go` (`CheckOption`,
  `RequireMandatoryTLS`, `ErrOpportunisticTLS`) + test; `sdk/capabilities/notify/email/smtp.go`
  (`SMTPConfig.RequireTLS`; `MinVersion: tls.VersionTLS12`; `Capabilities()` reports
  `TransportSecurityTLS` when set; send-time `email.ErrTLSRequired` on a relay without STARTTLS;
  `(*SMTP).Probe(ctx) error` — dial, EHLO, require STARTTLS when `RequireTLS`, QUIT; no AUTH, no
  message — for production hosts to call at boot) + test; `email` package doc (the two-line
  `notify.CheckTransport` recipe); `sdk/capabilities/oauth/oauth.go` and
  `sdk/capabilities/authentication/authentication.go` (`Capabilities`, `CapabilityReporter`,
  `CheckProvider`, sentinels); `sdk/README.md` package rows.
- **behavior:** `web.SecurityHeaders()` is a preset over `DefaultHeadersMiddleware` exactly like
  `NoStore()` (`middleware.go:263`) — one header-writing mechanism; ARCHITECTURE's "Where middleware
  lives" pkg row lists it beside `NoStore` — writing `Strict-Transport-Security: max-age=31536000;
  includeSubDomains`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`,
  `X-Frame-Options: DENY`, `Permissions-Policy: camera=(), microphone=(), geolocation=()` before
  `next`; it writes NO `Content-Security-Policy` (pages own CSP: auth `security.go:404-412`,
  goth-showcase); a handler may `Set` or `Del` any of the five.
  `SecurityHeadersWithConfig(SecurityHeadersConfig{HSTSMaxAge, HSTSPreload, HSTSExcludeSubDomains,
  DisableHSTS, FrameOptions})`: `HSTSMaxAge < 0` emits `max-age=0` (the documented un-pin recovery —
  `DisableHSTS` stops sending but does not un-pin browsers); `FrameOptions: ""` drops the header for
  hosts that embed (Segovia dashboards set `frame-ancestors`). `ServerConfig.ValidateProduction()`
  per the pattern table. `LoadEnvForMode(key) (Mode, error)`: reads `key` from the PROCESS
  environment only; unset/empty → `ErrModeRequired`, invalid → `ErrModeInvalid` (the file is never
  read); `production` → never opens `.env` (one WARN "`.env` present, ignored in production");
  `development` → `LoadPath(".env")`; a `.env` that names `key` at all → `ErrModeFromFile` (the only
  new sentinel). Error texts name the key and the remedy (set the key in the PROCESS environment — development by
  exporting it or prefixing the command, the rest of `.env` still loads; production through systemd
  `Environment=`/`EnvironmentFile=`, `docker --env-file`, platform secrets).
  *Execution amendment 2026-10-22:* rev 2's scaffold/P7/verification text still had the mode
  assigned in `.env`, contradicting M-1; the loader stays strict and those sections were corrected. `CheckTransport`'s default verdict is UNCHANGED; with `RequireMandatoryTLS()`
  production rejects `TransportSecurityStartTLS` with `ErrOpportunisticTLS` wrapping
  `ErrInsecureTransport`. `CheckProvider` per the pattern table (undeclared accepted).
- **BREAKING:** no — no existing caller's verdict changes; `LoadEnv` stays.
- **escape hatch:** omit `RequireMandatoryTLS()`; `SecurityHeadersWithConfig`.
- **tests:** header matrix incl. handler `Set`/`Del` override; `SecurityHeaders` outside
  `cacher.Pages`: second GET is `X-Cache: HIT` and carries all five headers (`cacher` skips
  non-allowlisted inherited headers, `middleware_policy.go:95-96`); `ValidateProduction` table
  (unset, http, userinfo, path, `/`, query, fragment, valid); `LoadEnvForMode` (production skips the
  file, development loads, file names the key → `ErrModeFromFile`, unset → `ErrModeRequired`,
  invalid → `ErrModeInvalid`); `CheckTransport` default unchanged / `RequireMandatoryTLS` refused /
  development accepted; SMTP `RequireTLS` + `Probe` against a test relay without STARTTLS;
  `CheckProvider` declared/undeclared/development.
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`; `make guard` (G12b:
  `web` must not import `environment`); `make check`.
- **closes:** FHS-1, FHS-5, FSK-4, FSK-3 (sdk halves), FHC-13 (sdk half), FHC-7 (hook half).
- **tag:** `sdk/v0.16.0` (minor; additive). Cut at PR-1's merge commit and pushed BEFORE the `.info`
  poll; no dependent branch runs `make check`/`go mod tidy` against the proxy until it resolves.

### PR-2 — connectors: pgxdb, turso, otel (`integrations/posture-floor`)

- **files:** `integrations/datastores/pgxdb/postgres.go` (`Config.RuntimeMode`,
  `TLSUnverifiedAcknowledged`, `TLSDisabledAcknowledged`; the classification in `poolConfig` after
  `pgxpool.ParseConfig` at :275; production refuses `LogQueries` and a `Tracer` that is or wraps
  `*LoggingQueryTracer` with `ErrQueryLoggingRefused`, no acknowledgement) + tests + README
  (managed-provider table); `integrations/datastores/turso/turso.go` (`Config.RuntimeMode`,
  `TLSDisabledAcknowledged`; scheme + `tls=0` check in `Open` after `dsn()` at :150, not inside
  `dsn()` which `Redacted()` also calls at :120) + tests + README; `integrations/tracing/otel/
  config.go` (`Config.RuntimeMode`; remove `default:"stdout"` from the `Exporter` tag at :47-48;
  `Open` returns `ErrExporterRequired` when production and `cfg.Exporter == ""`, checked before the
  fill at :89; O-3 `ErrInsecureExporter`) + tests + README.
- **behavior:** zero `RuntimeMode` is today's behavior; a non-zero mode is validated. Production:
  per the pattern table; development logs one WARN per condition; each honored acknowledgement
  logs `posture_waiver=`.
- **BREAKING:** no. **escape hatch:** the TLS acknowledgements; `TRACING_EXPORTER=stdout` set
  explicitly (now distinguishable from the removed default).
- **tests:** no server needed: production + `sslmode=disable` → `ErrTLSRequired`; `verify-ca` +
  a testdata CA admitted; `require`+`sslrootcert` admitted; `sslrootcert=system` admitted; bare
  `require` refused and admitted under `TLSUnverifiedAcknowledged`; `prefer` refused via its
  fallback; `host=/tmp` admitted with the `tls=local_socket` line; a two-host DSN with one
  `sslmode=disable` target refused; `LogQueries` and a wrapped `LoggingQueryTracer` refused;
  turso `http://`, `ws://`, `libsql://127.0.0.1:8080?tls=0` → `ErrTLSRequired`, `file:` exempt;
  otel: `ParseEnvTags` with `TRACING_EXPORTER` unset + production → `ErrExporterRequired`.
- **verify:** per module `go build ./... && go test ./... && go vet ./...`; `make check`; live:
  `make test-stores` pgxdb leg and turso `-tags=integration` on merged `main` before tag 2
  (`poolConfig` and the turso open path are rewritten).
- **closes:** FAS-1 (connector half), FAS-5, FSK-5.
- **tag:** `integrations/datastores/pgxdb/v0.10.0`, `integrations/datastores/turso/v0.7.0`,
  `integrations/tracing/otel/v0.3.0` (minor; no sdk repin — `environment.Mode` exists at v0.9.0).

### PR-3 — authorization: mode, audit requirement, recording capability (`authorization/posture-floor`)

- **files:** `pockets/authorization/options.go` (`WithRuntimeMode`, `WithAuditDisabledAcknowledged`);
  `config.go` (two config fields); `constructor.go` (the check after the pairing checks at :52-60;
  the registration log at :140 gains `audit_recording`, `audit_reader`, `runtime_mode`); the
  sentinel file beside `ErrNoKindConfigured` (`ErrAuditRequired` with the two-case text,
  `ErrAuditAcknowledgedWithRecording`); `logic/audit/audit.go` (`RecordingReporter`);
  `stores/memory/mutations.go` (`AuditRecording()` on `*Tuples` and `*Mutations`, compile-time
  assertions); `stores/pgx/{tuples,mutations}.go` and `stores/turso/{tuples,mutations}.go` (same,
  from `c.audit`); `stores/storetest/audit.go` (the capability tracks the factory's audit flag);
  `pockets/authorization/README.md` (production section; "wrappers forward `AuditRecording()`";
  the multi-authority shapes (a)/(b) so they live outside a plan file).
- **behavior:** `WithRuntimeMode(production)`: `New` returns `ErrAuditRequired` when
  `repos.Audit == nil` (O-1), or `Repositories.Tuples`/`Mutations` lack the method or report false,
  unless `WithAuditDisabledAcknowledged()`; the acknowledgement beside a recording authority is
  `ErrAuditAcknowledgedWithRecording` (every mode). Development WARNs. Zero mode: unchanged.
- **BREAKING:** no. **escape hatch:** `WithAuditDisabledAcknowledged()`.
- **tests:** production matrix over the memory store in `constructor_test.go`: recording + reader
  constructs; no reader → `ErrAuditRequired` (reader case); store without `WithAudit()` →
  `ErrAuditRequired` (false case); a store lacking the method → `ErrAuditRequired` (absent case
  text); acknowledged + non-recording constructs with a `posture_waiver` WARN; acknowledged +
  recording → contradictory; development WARN captured; registration log fields asserted.
- **verify:** `cd pockets/authorization && go build ./... && go test ./... && go vet ./...`; both
  store modules the same; `make check`; live store legs on merged `main` before tags 3a/3b.
- **closes:** FAZ-1, FAZ-13, H1 (authorization half).
- **tag:** 3a `pockets/authorization/v0.28.0`; 3b `…/stores/pgx/v0.19.0`, `…/stores/turso/v0.18.0`
  after 3a resolves and a `GOWORK=off` tidy (the stores carry the compile-time assertion, so they
  repin the core).

### PR-4 — authentication + the auth-cms harness (`authentication/posture-floor`)

- **files (pocket):** `pockets/authentication/keys.go` (new: `KeysInput` — five `string` fields
  tagged `env:"AUTH_JWT_SECRET"`, `env:"AUTH_CHALLENGE_PEPPER"`, `env:"AUTH_IDENTIFIER_KEY"`,
  `env:"AUTH_DELIVERY_ENCRYPTER_KEY"`, `env:"AUTH_TOKEN_ENCRYPTER_KEY"`, all hex; `KeyName`
  constants; `Keys` (redacting `String`/`GoString`/`LogValue`, so S6's `Secret` type lands
  additively); `NewKeys(mode, in, log, need ...KeyName)`: decodes via `environment.DecodeSecret`
  (`JWTSecret`, `ChallengePepper`, `IdentifierKey` ≥ 32 bytes; `DeliveryEncrypterKey`,
  `TokenEncrypterKey` exactly 32); `need` names the keys the host wires (the constructor already
  fails closed on a missing protector/encrypter); production → one `ErrKeyRequired` per unset or
  malformed needed variable, joined; development → ephemeral + WARN per unset needed key;
  `TokenEncrypterKey` unset → `nil` in BOTH modes with one INFO line (provider tokens not persisted,
  today's documented degradation; O-2) — never ephemeral; `config.go:835-837` cites `NewKeys` as the
  sanctioned shape: the host fills `KeysInput` with `environment.ParseEnvTags` or any other source)
  + test; `config.go` (`WithAuditDisabledAcknowledged`; top-level `SingleFactorAcknowledged`;
  `DeliveryConfig.OpportunisticTLSAcknowledged`; the `WithDelivery` copy at :303; no env tags);
  `security.go:65-78` (production-refusal sentinels beside `ErrDeliveryJobsUnacknowledged`:
  `ErrAuditRequired`, `ErrSingleFactorUnacknowledged`, `ErrInsecureDeliveryTransport`) and
  `configuration_errors.go` (the contradiction `ErrAuditAcknowledgedWithSecurityEvents`);
  `constructor.go` (audit check after the keyer check at :264; development WARN when
  `repos.SecurityEvents == nil`; single-factor check; production joins every posture violation
  with `errors.Join`, each naming its acknowledgement, so a repinning host sees its whole list in
  one boot — a test asserts the text names the field); `security.go:224` (`validateDeliveryTransports`
  passes `notify.RequireMandatoryTLS()` unless acknowledged and labels `ErrOpportunisticTLS`) and
  `:442` (`validateOAuthConfig` via `oauth.CheckProvider`); `logic/delivery/router.go:534-544`
  (`delivery.WithOpportunisticTLSAcknowledged()` router option applied in `ValidateTransports`) and
  `logic/authentication/constructor.go:161-172` (threaded from `DeliveryConfig`);
  `logic/authentication/external_config.go:168` (`sdkauth.CheckProvider`; declares
  `authlogic.ErrDevelopmentOnlyProvider`, the root aliases it); `inbound/http/routes.go:432`
  (`clientInfoMiddleware`: one `sync.Once` production WARN when `web.ClientIP` is absent, OR when
  the request carries `X-Forwarded-For` and the resolved client IP equals the peer address —
  `TrustProxies(0)` behind a proxy, the real FHS-2 failure); `inbound/http/security.go:85-165`
  (`Adapter.RequireBrowserSafeMutation() web.Middleware` — GET/HEAD/OPTIONS pass; on unsafe methods
  it is `requireBrowserSafeMutation(cfg)`: bearer-only skips, else allowlisted Origin/Sec-Fetch-Site
  AND the `X-CSRF-Token` double-submit — under the adapter's csrf config; on an adapter built by
  `NewAuthenticator` with no routes it fails closed with 403 on unsafe methods, never panics —
  decided: the origin-only gate at `:144-165` is for credential-establishment endpoints and the
  role routes also wrap two GETs, `authorization/inbound/http/routes.go:158-161`); `README.md`
  constructor table (+5 production rows, the key table with ONE encoding, the `NewKeys` recipe,
  the revocation note that JWT rotation does not end sessions); `go.mod` → sdk v0.16.0, tidied
  `GOWORK=off` against the published tag.
- **files (harness):** `examples/auth-cms/cmd/server/main.go:117` (`LoadEnvForMode("AUTH_RUNTIME_MODE")`
  replaces `LoadEnv()`; the mode is parsed FIRST — today `newAuthorization` runs at :250 before
  `buildAuthConfig` parses the mode at :277/:1039 — and `newAuthorization(…, mode, …)`,
  `buildAuthConfig(…, mode, …)` take it; `authenticationConfig.RuntimeMode` drops its env tag;
  `main.go:310-311` keeps its `= false`/`= true` literals, which P5 accepts); `demo.go:195-325`
  (five builders → `ParseEnvTags` into `KeysInput` + one `NewKeys` call; the AUTH_DEBUG refusal
  becomes the FIRST statement after mode resolution in `run()` so its sentinel is observable, gated
  on `RequireAccessTokenLive()` + `Require(Can("admin", platform…))`); `documents.go:43` unchanged
  (P6 prints an informational `rand` line); `oauthfake.go` (`Capabilities()` → `DevelopmentOnly:
  true`); both `integrations/authentication/devissuer` copies (auth-cms, external-identity) declare
  `DevelopmentOnly`; `authorization.go:86-95` (`authzmem.WithAudit()`, `Audit: store.Audit()`,
  `WithRuntimeMode(mode)` — safe: the adapter and the mutation service stamp `audit.Source`,
  `inbound/http/roles.go:322`, `logic/mutations/mutation_service.go:52`) and `:105-125`
  (`roleAdministrationGate` composes `authSvc.HTTP.RequireBrowserSafeMutation()` between
  authenticate and authorize; the ⚠ OPEN QUESTION paragraph at :113-119 is deleted);
  `production_test.go` (`productionBaseline` clears `Providers`, sets `SingleFactorAcknowledged`;
  negatives: each needed key unset → `ErrKeyRequired` naming it, all joined; `fake_oauth_provider` →
  `ErrDevelopmentOnlyProvider`; `nil_security_events` → `ErrAuditRequired`; `single_factor` →
  `ErrSingleFactorUnacknowledged`; `starttls_mailer` → `ErrInsecureDeliveryTransport`;
  `starttls_mailer_acknowledged` constructs end to end through `authentication.New`;
  `auth_debug` → its own sentinel); `.env.example:23-65` (all five keys hex; `AUTH_RUNTIME_MODE` mentioned
  only in a comment (never assigned — `LoadEnvForMode` refuses it); `TRUSTED_PROXY_COUNT=0`; `PUBLIC_BASE_URL=`);
  `README.md:349,361,492,914` ("boots with none of them set" becomes development-only; the A9
  protocol exports `AUTH_JWT_SECRET=$(openssl rand -hex 32)`; every run line is prefixed
  `AUTH_RUNTIME_MODE=development`).
- **behavior:** the pattern table. Migration note (RELEASING + handoff): **continuity** — set each
  hex variable to the hex of the OLD bytes (`printf %s "$OLD" | xxd -p | tr -d '\n'`) for
  `AUTH_JWT_SECRET` (today the hex TEXT is the key material, `demo.go:219`),
  `AUTH_DELIVERY_ENCRYPTER_KEY` and `AUTH_TOKEN_ENCRYPTER_KEY` (raw 32-character strings today,
  `demo.go:272,297`); minting new values instead invalidates outstanding access tokens, strands
  every in-flight outbox job sealed under the old key, makes every stored provider token
  undecryptable, and makes a mixed fleet reject each other's output for the whole rollout.
- **BREAKING:** no exported removal. Production hosts on native passwords or passwordless, STARTTLS
  mail, or without `SecurityEvents` refuse to boot after repinning until they acknowledge — the
  control; the joined error lists every acknowledgement at once.
- **escape hatch:** the three acknowledgements; `PasswordFlowsDisabled`; `SMTPConfig.RequireTLS`.
- **tests:** a positive control plus one negative per sentinel, development WARN captured; `NewKeys`
  table (hex ok, odd length, short, unset × mode × needed/not-needed; token key nil; joined error
  names every variable; no decoder text); no-env-tag test; `RequireBrowserSafeMutation`: cross-site
  cookie POST → 403; same-origin cookie POST without `X-CSRF-Token` → 403; same-origin cookie POST
  with the token from `GET /auth/csrf` → passes; bearer-only cross-origin POST → passes; cookie
  `GET /authorization/roles/by-subject` with no token → passes; the auth-cms matrix; run-and-look:
  `AUTH_RUNTIME_MODE=development make run`, `GET /debug/security-events` as a non-admin → 403, as
  the seeded admin → 200; `AUTH_RUNTIME_MODE=production AUTH_DEBUG=1 go run ./cmd/server` → the
  refusal text names `AUTH_DEBUG`.
- **verify:** `cd pockets/authentication && go build ./... && go test ./... && go vet ./...`;
  `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...`; `make guard`;
  `make check`; live legs on merged `main` before tag 4 (`make test-stores` authentication pgx/turso).
- **closes:** FAC-1, FAC-3, FHC-6, FHC-7 (harness half), FHS-2 (WARN half), FSK-3 (ack half),
  H3/FAC-2 (ruling-1 check), FAZ-5 (origin-export half), H1 (authentication half).
- **tag:** `pockets/authentication/v0.20.0` (minor; repins sdk v0.16.0). Stores and views untouched.

### PR-6 — workshop: `guard --posture`, scaffold, examples posture-clean (`workshop/guard-posture`)

Opens after tags 1–2 are PUSHED and resolve (`TestWarmScaffoldModuleCache`, `Makefile:598-610`,
runs with the proxy on; do not run `make check` on this branch before). Model `fable` for the P
evaluators' design, `opus` for the code.

- **files:** `workshop/gopernicus/internal/commands/guard.go` (`--posture`, `ack`/`rand` lines,
  usage); `guard_rules.go` + new `posture_rules.go` (P0–P8 sentences, the Recognized posture
  surfaces list, evaluators over `guardHost`, `guard_walk.go:80-101`); `guard_test.go` (P cases in
  `TestGuardRuleFixtures`; `--list` drift over §3 and §3b with the `P[0-9]+` row regex; exit codes
  with `--posture`); `templates/init/{env.example,main.go,migrations.main.go,go.mod,Makefile,
  readme.md}.tmpl` (the deltas above); `scaffold_test.go:53-183` (emitted hosts compile on the new
  pins, boot in development, and pass `guard --posture`); `examples/README.md` §1 (the P-series
  sentence), §3b (P0–P8 + the list + the "older pin exits 2" note), §6 (the production
  composition-root recipe; TLS at the edge, HSTS by the app), §8 ("all six run under G30 and G32";
  PR-7 adds the seventh); `examples/{cms,minimal,jobs-minimal,goth-showcase,external-identity,
  auth-cms}/cmd/server/main.go` (`TrustProxies` outermost + `SecurityHeaders` second;
  `ValidateProduction` under production; mode via `LoadEnvForMode` where not already;
  external-identity drops `default:"development"` at `main.go:68`; examples/cms `RuntimeMode: mode`
  on the `:71` `tursodb.Config` and `cfg.RuntimeMode = mode` after the `:204` `ParseEnvTags`;
  cms and minimal `RuntimeMode: mode` on `cms.Config`); `examples/*/.env.example` (P7: mode key
  commented only, `TRUSTED_PROXY_COUNT`, `PUBLIC_BASE_URL`; external-identity `:17` loses its
  value); every example README run line, the root `Makefile` `run` target (:188-189), and
  `examples/goth-showcase/e2e/playwright.config.ts:37-40` (webServer env gains
  `APP_RUNTIME_MODE: "development"`) so first runs do not fail with `ErrModeRequired`;
  `Makefile` (G32, G33, G30/G32 host lists, guard-count prose) and `check.yml:2` (same prose);
  `workshop/documentation/docs/architecture/host-contract.md:25` and `docs/workshop/commands.md`;
  `ARCHITECTURE.md` (the host-contract pointer; the pkg middleware row); `RELEASING.md` "Tagging
  scheme" (P rules ship in a workshop minor like H rules).
- **behavior:** the P table. `guard` without `--posture` is byte-for-byte today's output.
- **BREAKING:** no (new flag; a host on v0.4.1 is unaffected until it bumps). **escape hatch:**
  none — no skip flag (`guard.go:12-15`); a rule a host cannot satisfy is a conversation, and the
  informational `ack`/`rand` lines exist so the rules stay satisfiable.
- **tests:** one clean and one violating fixture per P rule (P5 fixtures: `true`, `false`,
  conditional, option-form, expression); `TestGuardListMatchesContract` over §3 + §3b;
  `TestGuardExitCodes` with `--posture`; `go run . guard --posture ../../examples/<each>` exits 0
  (auth-cms prints `ack` and `rand` lines, minimal and cms print one `ack` each); a cold
  `init --db pgx` host passes `--posture`; auth-cms under `SecurityHeaders`: `GET /auth/login`
  carries `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, CSP with `frame-ancestors 'none'`
  (pocket wins) plus `Strict-Transport-Security` and `Permissions-Policy` (preset); `GET /` (cms)
  carries `strict-origin-when-cross-origin`.
- **verify:** `cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...`;
  `make guard-host-contract`; `make guard` (G11: the verb imports no pocket; G33); `make check`;
  `make test-ui-browser`.
- **closes:** FHC-5, FHC-2, FHS-2 / FAS-1 / FHS-5 / FAZ-5 (scaffold and example halves), ruling
  2's "surfaced by guard".
- **tag:** `workshop/gopernicus/v0.5.0` (minor: new rules, new pins).

### PR-7 — `examples/production-baseline` (`examples/production-baseline`)

- **files:** `examples/production-baseline/{go.mod,README.md,.env.example,Makefile}`,
  `cmd/server/{main.go,production_test.go}`, `integrations/authentication/devissuer/` (copy),
  `workshop/migrations/{main.go,primary/*.sql}` (exported from both pgx stores plus the pgxdb
  `ratelimit_windows` reference DDL; the runner shape of `migrations.main.go.tmpl`, mode-aware);
  `go.work`; `Makefile` `MODULES` (+1; G30/G32 lists); `ARCHITECTURE.md:13-60` tree + the module
  count; `README.md` count; `examples/README.md` §8 (the seventh row).
- **behavior:** the composition in Design. `production_test.go` builds the same wiring over
  `authmem`/`authzmem` (the auth-cms `productionBaseline` shape) and proves: constructs in
  production with every input present; each single omission — `SecurityEvents`, `WithAudit()`,
  `Repositories.Audit`, a needed key, `PublicBaseURL`, cookie `Secure`, the dev issuer built in
  development and handed to a production pocket (`devissuer.New` itself refuses non-development
  modes, `devissuer.go:69-71`, so the live binary shows `devissuer.ErrNotDevelopment`; the pocket's
  `ErrDevelopmentOnlyProvider` is provable only here and for `oauthfake`) — is refused with its
  sentinel.
- **BREAKING:** no (examples are never tagged). **escape hatch:** none — zero acknowledgements.
- **tests:** the matrix; `guard --posture` clean with no `ack` line; run-and-look (Verification).
- **verify:** `cd examples/production-baseline && go build ./... && go test ./... && go vet ./...`;
  `cd workshop/gopernicus && go run . guard --posture ../../examples/production-baseline`;
  `make guard-host-contract`; `make check`.
- **closes:** FAZ-5 (reference-host half; the scaffold mounts no pockets), H4 (reference host),
  `passes/08` checklist rows 17–22. **tag:** none.

### PR-8 — release record, handoff, manifest (`docs/s1-release-record`)

- **files:** `RELEASING.md` ("Production posture floor" section: the ten tags, cold verification,
  migration notes — cms admin, key continuity, the production refusals a repinning host meets, and
  "these are the first published tags carrying S8's `go 1.26.10` and pgx v5.9.2: a consumer on
  `GOTOOLCHAIN=local` with an older toolchain fails; bump authorization core v0.28.0 together with
  stores pgx ≥ v0.19.0 / turso ≥ v0.18.0 before passing `WithRuntimeMode(production)`");
  `plans/soc2-s1-production-posture-handoff.md` (one section per host — gps-360-go, Segovia,
  coordination-hub — listing the refusals the host will hit on repin, the acknowledgements it is
  expected to choose (e.g. a Cloud SQL socket is *local*, not `TLSDisabledAcknowledged`; O-4 names
  gps-360-go's connection path), a read-only `guard --posture` dry-run of its `cmd/` — Segovia today:
  P0 `main.go:718` pre-seeds development, P1 `openDatabase` `:731-738` has no `RuntimeMode`, P3 no
  `SecurityHeaders`, and its dashboards set `frame-ancestors` for embedding (`dashboards/content.go:71`)
  so it uses `SecurityHeadersWithConfig{FrameOptions: ""}` — the key continuity recipe, and the
  move of EVERY variable out of a shipped `.env`); `plans/s1-production-posture-release-manifest.json`;
  `plans/s1-production-posture.md` (executed copy); this file's status line; `roadmap.md`
  (S1 rows → done; principle 1's module list narrowed to what S1 threads) and `control-map.md`.
- **verify:** `make guard` (G20 over the new text). **tag:** none.

### Sequencing

```
PR-5 cms ─ [tag 0: cms v0.4.0]
PR-1 sdk ─ [tag 1 sdk, pushed, .info polled] ─┬─ PR-2 connectors ─ [live legs] ─ [tag 2 ×3]
                                              └─ PR-3 authorization ─ [live legs] ─ [tag 3a core] ─ [tag 3b stores ×2]
PR-4 authentication(+auth-cms) ─ [live legs] ─ [tag 4]          (needs tags 1 and 3a resolved)
PR-6 workshop(+examples) ─ [tag 5 workshop]                      (needs tags 1–2 resolved)
PR-7 baseline                                                     (needs tags 1–4 and PR-6 for --posture)
PR-8 record
```

Ten module tags in seven steps, every one cut at its PR's merge commit, pushed, THEN polled on
`.info` (never before the push — negative cache), and cold-verified (scratch consumer, `GOWORK=off`,
empty `GOMODCACHE`, standalone archive build, `go mod verify`). Dependents tidy `GOWORK=off`
against the published tag so every `go.sum` is complete. Never `go work sync`.

## Release — the tag train

| step | tag(s) | after | repins |
|---|---|---|---|
| 0 | `pockets/cms/v0.4.0` | PR-5 | none (sdk stays v0.9.0) |
| 1 | `sdk/v0.16.0` | PR-1 | — |
| 2 | `integrations/datastores/pgxdb/v0.10.0`, `…/turso/v0.7.0`, `integrations/tracing/otel/v0.3.0` | PR-2 + live legs | none |
| 3a | `pockets/authorization/v0.28.0` | PR-3 + live legs | none |
| 3b | `pockets/authorization/stores/pgx/v0.19.0`, `…/stores/turso/v0.18.0` | 3a resolved + tidy | core v0.28.0 |
| 4 | `pockets/authentication/v0.20.0` | PR-4 + live legs | sdk v0.16.0 |
| 5 | `workshop/gopernicus/v0.5.0` | PR-6 | scaffold pins sdk v0.16.0, pgxdb v0.10.0, turso v0.7.0 |

## Risks

1. **Production hosts refuse to boot after a repin** (highest, intended). A downstream host running
   `production` on native passwords or passwordless (`ErrSingleFactorUnacknowledged`), STARTTLS
   mail (`ErrInsecureDeliveryTransport`), without `SecurityEvents` (`ErrAuditRequired`), or — once
   it passes a mode to pgxdb — on `sslmode=prefer` or a bare `require` (`ErrTLSRequired`) stops at
   construction with ONE joined error naming every acknowledgement. gps-360-go, Segovia and
   coordination-hub repin on their own schedule with the PR-8 handoff. Hosts on current tags or in
   `development` see no change.
2. **The cms admin flip.** A host constructing `cms.Config` without the field keeps compiling and
   loses `/articles`, `/media`, `/inquiries` admin at the bump — WARN + 404/405, AUDIT-053, G31/P8;
   the public site is untouched. A published page whose slug equals an admin base (`articles`,
   `pages`, `terms`, `menus`, `media`, `inquiries`) becomes publicly reachable at that path once
   admin is unmounted.
3. **Not every S1 change is a boot refusal.** Construction-time (a bad config fails the new
   instance; old instances keep serving IF the orchestrator gates traffic on `/healthz`
   readiness): every sentinel in the pattern table, `NewKeys`, `LoadEnvForMode`,
   `ValidateProduction`. Runtime, not protected by a rolling deploy: (a) `SMTPConfig.RequireTLS`
   against a relay without STARTTLS fails each send — production hosts call `(*SMTP).Probe(ctx)`
   at boot; (b) the cms flip deploys green and 404s admin routes, flapping in a mixed fleet —
   P8/AUDIT-053 before deploying; (c) HSTS is sticky and rollback does not clear it — the
   `HSTSMaxAge < 0` recovery, `HSTSExcludeSubDomains` for an apex with HTTP-only siblings,
   `HSTSPreload` off; (d) changed key bytes (Risk 4) reject the other half of a mixed fleet.
4. **Key continuity.** Hex-encode the OLD bytes (PR-4 migration note); never mint at the upgrade.
   auth-cms itself follows the same recipe, so its sessions survive.
5. **Scaffold tests couple to the proxy.** PR-6 is red until tags 1–2 resolve; the train order is
   the mitigation.
6. **A `.env` shipped beside a production binary.** Production never reads it after this slice;
   every variable it carried must move to the process environment (handoff), not only the mode.

## Verification — how the owner proves it end to end

Framework: `make check` green on every PR; `make guard` lists G31–G33; ten module tags
cold-verified.

Baseline, from `examples/production-baseline` with a local Postgres (docker on a free port, never
55432) and `.env` copied from `.env.example` with `DB_SSLMODE=disable` uncommented (local docker has
no TLS; `.env` is not `.env.example`, so P7 is unaffected) and NO mode line — `make run` exports
`APP_RUNTIME_MODE=development`:

1. `make run` boots; the log shows the dev-issuer WARN, `audit_recording=true`,
   `runtime_mode=development`; `curl -i localhost:8080/healthz` → 200 with
   `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`.
   With a dev-issuer bearer token (README recipe), `POST /authorization/roles` carrying
   `Origin: https://evil.example` and a cookie → 403 from `RequireBrowserSafeMutation`; without a
   credential → 401; `GET /authorization/roles/by-subject?subject_type=user&subject_id=x` without
   a credential → 401.
2. `APP_RUNTIME_MODE=production go run ./cmd/server` with `.env` still present → one joined error
   naming `AUTH_JWT_SECRET` and `AUTH_IDENTIFIER_KEY` (production never read `.env`; the WARN says
   it was ignored). Export the two needed keys (`openssl rand -hex 32`) and repeat:
   `web.ErrPublicBaseURLRequired` → `PUBLIC_BASE_URL=https://baseline.example` → export
   `DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME` and `DB_SSLMODE=disable` with `DB_URL` unset (a DSN
   wins over `DB_SSLMODE`, `postgres.go:129-131`) → `pgxdb.ErrTLSRequired` naming class
   `plaintext`, no connection attempted → optionally `DB_SSLMODE=verify-full` against a TLS-enabled,
   migrated Postgres → `devissuer.ErrNotDevelopment` (the issuer refuses the mode before the pocket
   sees it; the pocket's refusal is proven in `production_test.go`) → set `IDP_ISSUER`,
   `IDP_AUDIENCE`, `IDP_JWKS_URL` (any https values; go-oidc does no network I/O when given a JWKS
   URL, `gooidc.go:116-119`) → boots; `/healthz` 200.
3. `cd workshop/gopernicus && go run . guard --posture ../../examples/production-baseline` → clean,
   zero `ack` lines; over `examples/minimal` → clean with one `ack` line
   (`AdminUngatedAcknowledged`); over `examples/auth-cms` → clean with `ack` lines and one `rand`
   line; a scratch copy of minimal with `AdminUngatedAcknowledged: os.Getenv("X") != ""` → P5
   finding, exit 1; `go run …/workshop/gopernicus@v0.4.1 guard --posture .` → exit 2.
4. `cd examples/auth-cms`: `AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 make run`;
   `GET /debug/security-events` as a non-admin → 403, as the seeded admin → 200;
   `AUTH_RUNTIME_MODE=production AUTH_DEBUG=1 go run ./cmd/server` → the refusal names `AUTH_DEBUG`
   (it runs before `NewKeys`). Browser-load `/auth/login` and `/articles` with `SecurityHeaders`
   installed: ui/goth stylesheet and HTMX load under `nosniff` (served `text/css`/`text/javascript`,
   `sdk/pkg/web/static.go:231-239`); devtools shows the pocket's `no-referrer` on `/auth/login`
   and the preset's `strict-origin-when-cross-origin` on `/`.
5. Cold scaffold: `go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.5.0 init --db pgx
   --module example.com/x` in an empty directory → `go mod tidy && go build ./...` green;
   `.env.example` mentions `APP_RUNTIME_MODE` only in a comment and carries `DB_SSLMODE=verify-full`;
   `go run …@v0.5.0 guard --posture .` clean; `APP_RUNTIME_MODE=development make run` boots and
   `curl -i :<port>/healthz` → 200 with `Strict-Transport-Security`.

## Decided by the review gate (no ruling needed)

- Rev 1 YOUR CALL 1 (passwordless under the single-factor flag): yes, by ruling 1; placement
  corrected to a top-level `Config` field.
- Rev 1 YOUR CALL 2 (export vs slice): export, as `RequireBrowserSafeMutation()` (method-aware,
  origin + double-submit), composed into auth-cms.
- Rev 1 YOUR CALL 3 (baseline identity): `PasswordFlowsDisabled` + one OIDC provider, by ruling 1
  and default 7; provider chosen by `IDP_ISSUER` presence.

## Open rulings — YOUR CALL

- **O-1 `Repositories.Audit` in the production check.** The pocket never consumes it
  (`constructor.go:50` only typed-nil-checks it) and pgx/turso always fill it. *Recommend: keep it
  required* — S7's access-review route and the operations runbook consume it, and a host that
  records but cannot read has half a control; `Components` exposes it. Alternative: an
  `audit_reader=false` WARN.
- **O-2 `AUTH_TOKEN_ENCRYPTER_KEY` optional in production.** *Recommend: optional*, as today
  (unset ⇒ provider tokens not persisted, INFO line); requiring it makes the baseline mint a key it
  never uses. Alternative: `ErrKeyRequired` when OAuth providers are wired.
- **O-3 OTel `TRACING_OTLP_INSECURE=true` to a non-loopback endpoint in production.** *Recommend:
  refuse* (`ErrInsecureExporter`), following go-oidc's loopback-only precedent; S.
- **O-4 gps-360-go's Postgres path.** Cloud SQL socket (class *local*) or direct IP (`verify-ca` +
  `server-ca.pem`)? The handoff names the class; owner knows.
- **O-5 G33 `guard-pocket-no-env`.** *Recommend: yes*, S, in PR-6 — it makes the "pocket never reads
  the process environment" posture structural.
- **O-6 pgxdb v0.9.1 (S8 pgx fix only) ahead of S1.** *Recommend: no* unless the per-tenant plan's
  `QuerierFrom` fix lands first, in which case that patch carries both; otherwise pgxdb v0.10.0 is
  the first tag with GO-2026-5004 fixed and the handoff already tells hosts to require pgx v5.9.2
  themselves.

## Recommended reviews

Gate RUN 2026-10-22; all five RATIFY WITH AMENDMENTS, folded. A second pass is not required unless
the owner changes O-1…O-6 against the recommendations.

## Notes

- `ErrCredentialPolicyRequired` (`security.go:147-152`, never returned): PR-4 deletes it (S0
  corrected the docs). `web.Logger` still logs `RemoteAddr` (`middleware.go:91`); the audit-grade
  access log is S6/M2.
- The `ack`/`rand` lines extend `guard`'s "status, findings, summary" shape; informational, exit
  code unchanged.
- Rev-1 reviewer line anchors that drifted after S8: G30 `Makefile:659-665`; `warm-scaffold-cache`
  `:598-610`; authorization registration log `constructor.go:140`; cms routes `routes.go:54-148`;
  `RespondJSONError` `response.go:72-77`; otel fill `config.go:89`.
