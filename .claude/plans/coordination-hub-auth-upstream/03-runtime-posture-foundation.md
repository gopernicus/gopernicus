# Phase 3 — app-wide runtime posture and transport safety

## Outcome

Move the generic development/production vocabulary out of authentication so an
application mailer, notifier composition, or future capability can enforce its
production posture without importing a feature. Keep authentication source
compatible while making the sdk foundation/capabilities the canonical import.

## Current evidence

- `features/authentication/security.go` defines `RuntimeMode`, its two values,
  required/invalid errors, and `ErrInsecureDeliveryTransport`.
- Authentication uses the mode for app-wide concerns as well as auth concerns:
  transport metadata, rate-limiter durability, public-link HTTPS, and delivery
  runtime acknowledgments.
- `sdk/capabilities/email` and `notify` already own `Capabilities`,
  `CapabilityReporter`, `DevelopmentOnly`, and transport-security metadata, but
  not the enforcement helper.
- coordination-hub's general `internal/integrations/mailer` therefore imports
  `features/authentication` only to name mode and the insecure-transport error.
- `sdk/foundation/environment` is stdlib-only and already owns application
  environment loading/tag parsing. It is the least surprising home for the
  canonical mode; putting it in email would be wrong for limiter/URL checks.

## CHAU-3.1 — foundation vocabulary

Add to `sdk/foundation/environment`:

- closed `Mode` values `development` and `production`;
- stable required/invalid sentinels and a validation function;
- `ParseMode(string)` (or equivalent) that does not read a hard-coded env var;
  the host remains responsible for choosing `AUTH_RUNTIME_MODE`, `APP_MODE`, or
  another key; and
- Go/package docs that distinguish deployment posture from build tags and from
  auth's separate `DeliveryMode`.

The empty value stays invalid. Do not add test/staging/preview modes: hosts map
those environments to the security posture they want, normally production.

Acceptance:

- empty/unknown rejected deterministically;
- exact development/production strings round-trip;
- no environment variable is read implicitly;
- package has no dependency outside sdk foundation; and
- docs show parsing through `environment.GetEnvOrDefault` without blessing one
  global variable name.

## CHAU-3.2 — capability-owned checks

Add small validators in `sdk/capabilities/email` and
`sdk/capabilities/notify` which consume `environment.Mode` plus the capability
port and enforce the existing policy:

- production rejects a development-only transport;
- production rejects a transport that declares no `CapabilityReporter`;
- development accepts both, returning enough classification for the caller to
  issue its own context-appropriate warning; and
- errors are stable and capability-owned, with no authentication wording.

Freeze exact names only after checking collision with integration packages.
Avoid a logger parameter in the sdk validator: choosing message text/logger is a
composition concern. Do not duplicate `Capabilities` in the foundation.

Test bundled Console, SMTP, SendGrid (at integration compatibility time), a
metadata-less fake, and notify Console. The sdk helpers must accept structural
third-party implementations without concrete-type switches.

## CHAU-3.3 — authentication compatibility bridge

Authentication adopts the canonical mode without forcing every existing host to
rewrite in the same release:

- `authentication.RuntimeMode` remains a type alias to `environment.Mode`;
- `RuntimeModeDevelopment`/`RuntimeModeProduction` remain aliases;
- existing authentication sentinels remain matchable with `errors.Is` (document
  whether they alias or wrap the new canonical errors); and
- `Config.RuntimeMode` remains source-compatible for struct literals and env-tag
  parsing.

Refactor transport validation to call the capability-owned checks. Keep auth's
specific logger warnings and its non-transport production checks (durable
limiter, identifier key, HTTPS link, delivery acknowledgment) in auth.

Add compile tests for an old-style host using `auth.RuntimeModeProduction` and a
new app-wide mailer using `environment.ModeProduction` with no auth import.
coordination-hub's migration proof must delete the `features/authentication`
import from its generic mailer/notifymail packages; auth composition may still
import the feature for `auth.Config`.

## CHAU-3.4 — documentation

Documentation is a deliverable, not just alias comments:

- sdk environment package docs: mode selection, staging mapping, no implicit env;
- email/notify docs: metadata contract, production validator, examples for
  custom transports;
- auth README: canonical type, compatibility aliases, which fail-closed checks
  remain feature-specific;
- migration table: old import/name → canonical import/name;
- `RELEASING.md`: additive sdk API, alias compatibility, and whether error text
  changes while `errors.Is` remains stable; and
- coordination-hub snippet showing `mailer.RequireProductionCapable` without an
  authentication import.

Verification:

```sh
(cd sdk && go build ./... && go test -race ./... && go vet ./...)
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)
make check && make guard
```

## Stop conditions

Stop if compatibility requires two non-assignable mode types, if a new sdk
package must import authentication, or if capability validation starts reading a
specific host env var by itself.

## Execution log

Append only. Record each CHAU-3.x task's compatibility proof, module gates,
canonical-import migration, docs, and accepted API-name adaptation.

### 2026-08-16 — CHAU-3.1, CHAU-3.2, CHAU-3.3, CHAU-3.4 complete

**CHAU-3.1 — foundation vocabulary.** New
`sdk/foundation/environment/mode.go` (+ `mode_test.go`). Frozen names:

| symbol | shape |
|---|---|
| `Mode` | `type Mode string` |
| `ModeDevelopment` / `ModeProduction` | `"development"` / `"production"` |
| `ErrModeRequired` / `ErrModeInvalid` | sentinels; invalid wraps the offending value |
| `ValidateMode(Mode) error` | the required-enum rule |
| `ParseMode(string) (Mode, error)` | already-read string → Mode |
| `Mode.IsProduction()` / `Mode.String()` | readability helpers |

Acceptance evidence:

- empty → `ErrModeRequired`, unknown → `ErrModeInvalid`, deterministic
  (`TestValidateMode`, nine cases including `"Production"`, `" production "`,
  `"prod"`, `"dev"`, `"test"` — parsing is exact, with no case folding, trimming,
  or aliasing, so a typo fails loudly).
- exact strings round-trip (`TestParseModeRoundTrip`).
- **no env var read implicitly** (`TestParseModeReadsNoEnvironment`): the test
  sets `APP_MODE`, `AUTH_RUNTIME_MODE`, `ENV`, `MODE`, `GO_ENV`, and
  `ENVIRONMENT` all to `production`, then asserts `ParseMode("")` still returns
  `ErrModeRequired` and `ParseMode("development")` still returns development.
  `TestParseModeFromHostVariable` shows the documented
  `ParseMode(GetEnvOrDefault(<host key>, ""))` wiring without blessing a name.
- dependencies: stdlib only (`errors`, `fmt`). The package doc's "zero external
  dependencies" claim still holds, and guard **G12b** (foundation imports the
  root only) is unaffected.
- docs distinguish Mode from build tags, from a full environment name (and say
  why staging/preview/CI map onto the two postures rather than getting a third
  value), and from auth's `DeliveryMode`.

**CHAU-3.2 — capability-owned checks.** New
`sdk/capabilities/email/posture.go` and `sdk/capabilities/notify/posture.go`
(+ tests). Frozen names, checked for collision against both packages' existing
surface and against `integrations/email/sendgrid` and `integrations/notify/mailer`
(no clash):

- `email.CheckSender(environment.Mode, Sender) (TransportPosture, error)`,
  `email.InspectSender(Sender) TransportPosture`,
  `email.TransportPosture{Declared bool; Capabilities Capabilities}` with
  `ProductionCapable() bool`, and `email.ErrInsecureTransport`.
- `notify.CheckNotifier` / `InspectNotifier` / `TransportPosture` /
  `ErrInsecureTransport` — the same shape.

Policy proven by `TestCheckSender` / `TestCheckNotifier`: production rejects a
development-only transport and a metadata-less one (each with a distinct reason
in the message); development accepts both and returns a posture whose
`ProductionCapable()` is the caller's warn signal. **No logger parameter** — the
sdk returns classification, the composition chooses wording.
`TestCheckSenderErrorIsCapabilityOwned` /
`TestCheckNotifierErrorIsCapabilityOwned` assert the messages carry the owning
package prefix and contain no `auth`, `RuntimeMode`, or `Config.` wording.

An invalid or empty mode returns `environment.ErrModeRequired`/`ErrModeInvalid`
rather than defaulting to a posture the host never chose — a deliberate
fail-closed decision, pinned by two table cases per package.

Structural acceptance is pinned rather than assumed: `structuralSender` /
`structuralNotifier` declare capabilities without being any bundled type, and
`metadatalessSender` / `metadatalessNotifier` implement only the port. Nil
transports are covered too (they declare nothing → production rejects).
Coverage: bundled `email.Console` (dev-only), `email.SMTP` (StartTLS,
production-capable), `notify.Console` (dev-only), third-party structural senders
in both postures, metadata-less, and nil.

**SendGrid at integration compatibility time** — new
`integrations/email/sendgrid/posture_test.go`. It drives the *real* `Sender`
through `email.CheckSender` for five cases: default host and explicit
`https://api.eu.sendgrid.com` are production-capable; a plain-`http` host is
rejected in production (its `Capabilities` self-reports development-only) and
accepted-but-not-production-capable in development. It also asserts
`posture.Declared`, which is the structural-detection claim proven against a
module the sdk cannot import.

**CHAU-3.3 — authentication compatibility bridge.** `features/authentication/security.go`:

- `type RuntimeMode = environment.Mode` (alias) and
  `RuntimeModeDevelopment`/`RuntimeModeProduction` = the canonical constants.
- `ErrRuntimeModeRequired`/`ErrRuntimeModeInvalid` now **wrap** the canonical
  sentinels (`fmt.Errorf("…: %w", environment.ErrModeX)`), so `errors.Is` matches
  either target. Ruling recorded: wrapping rather than aliasing, because
  aliasing outright would have replaced auth's `Config.`-oriented message with a
  generic one. **The message text changed** (a canonical suffix was appended);
  nothing in the repo asserted on the text, only on `errors.Is`.
- `validateRuntimeMode` delegates the rule to `environment.ValidateMode` and only
  re-labels the verdict.
- `validateDeliveryTransports` delegates the production verdict to
  `email.CheckSender`/`notify.CheckNotifier`. `asInsecureTransport` re-labels a
  capability rejection in auth's vocabulary and wraps **both** sentinels; a
  non-verdict error (e.g. an invalid mode reaching the check) passes through
  unchanged instead of being mislabelled as a transport problem.
- `warnDevelopmentOnlyTransport` preserves the *exact* prior WARN condition —
  `declared && DevelopmentOnly` in development. Using `!ProductionCapable()`
  would have started warning about metadata-less transports too; that is a
  behavior change and was deliberately **not** made here.
- `enforceTransport`, `emailCapabilities`, and `notifyCapabilities` were removed
  as orphaned by this change (all unexported, no other callers).

Auth's own non-transport production checks stayed in auth, as required: durable
limiter, identifier keyer, HTTPS public link, delivery acknowledgments.

Compatibility proof: new `features/authentication/runtime_mode_compat_test.go`,
an **external** `authentication_test` package so it reaches only exported API.

- `TestOldStyleHostStillCompiles` — a host struct with an
  `env:"AUTH_RUNTIME_MODE"` field typed `auth.RuntimeMode`, plus the literal wire
  values `"production"`/`"development"`.
- `TestAliasIsAssignableBothDirections` — `var x environment.Mode = auth.RuntimeModeProduction`
  and `var y auth.RuntimeMode = environment.ModeProduction` both compile; a
  `Config` literal accepts the sdk constant with no conversion; and an
  `appWideMailer` typed on `environment.Mode` (naming only sdk symbols) accepts a
  value the host read as auth's type.
- `TestRuntimeModeSentinelsMatchBothVocabularies` — both targets match, and the
  required/invalid pair stays mutually distinguishable.
- `TestInsecureTransportMatchesBothVocabularies` — a real `NewService` call with
  a console mailer in production; the error matches both
  `auth.ErrInsecureDeliveryTransport` and `email.ErrInsecureTransport`, and
  does **not** match `notify.ErrInsecureTransport` (the wrong-transport
  false-positive check).
- `TestParseModeFeedsAuthConfig` — the documented migration wiring end to end.

The "app-wide mailer with no auth import" half is proven where it is
structurally enforceable: `sdk/capabilities/{email,notify}`'s own posture tests.
Those packages *cannot* import a feature (guard G12c), so the tests passing is
the proof coordination-hub needs before deleting the import from its generic
mailer package. Doing that deletion is downstream work in the hub repo and is
recorded in `08-release-and-adoption.md`, not performed here.

**CHAU-3.4 — documentation.**

- `sdk/foundation/environment/mode.go` — package-level docs on mode selection,
  staging mapping, and the no-implicit-env rule, with the
  `ParseMode(GetEnvOrDefault(...))` snippet.
- `sdk/README.md` — the `config`/environment row now carries the canonical mode
  vocabulary and its rules; the `email` and `notify` rows carry the new
  enforcement helpers and the "no logger parameter" rationale.
- `email.CheckSender` / `notify.CheckNotifier` godoc carries the
  posture-plus-warn example for a custom transport.
- `features/authentication/README.md` — the **Security posture** section gains
  the canonical-type explanation, the full old→canonical **migration table**
  (types, constants, both error pairs, and the new check functions), the
  `ParseMode` snippet, a complete `mailer.RequireProductionCapable` example with
  **no auth import**, and an explicit "what stays feature-specific" paragraph
  (limiter durability, identifier keyer, HTTPS link, delivery acknowledgments,
  and auth's own WARN wording). The `RuntimeMode` config-matrix row cross-links it.
- `RELEASING.md` — two new keyed entries: `### sdk — next tag: canonical runtime
  posture + capability-owned transport checks (MINOR floor)` and
  `### features/authentication — next tag: RuntimeMode is now an alias of
  environment.Mode (minor floor; SOURCE-COMPATIBLE)`. Both record the
  `errors.Is`-stable / text-changed disposition explicitly, the preserved WARN
  behavior, the sdk dependency direction, and the coordination-hub import
  deletion. The sdk entry instructs not to split the phase-4 patch out of this
  minor.

**Verification (exactly as run, 2026-08-16):**

```
(cd sdk && go build ./... && go test -race ./... && go vet ./...)                    clean, all ok
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...) clean, all ok
(cd integrations/email/sendgrid && go test -race ./...)                              ok
(cd examples/auth-cms && go build ./... && go test -race ./...)                      ok
make check   (runs make guard)                                                       all checks passed
```

Guards of note that stayed green: **G12b** (foundation imports the root only —
`environment` is still stdlib-only) and **G12c** (a capability imports no other
capability and no `sdk/feature` — `email`/`notify` importing
`foundation/environment` is the legal downward direction, matching the existing
`notify` → `foundation/identity` edge).

**Stop conditions: none triggered.** Compatibility needed exactly one type
(alias, not two non-assignable types); no new sdk package imports
authentication; and neither capability validator reads a host env var — the mode
arrives as a parameter.
