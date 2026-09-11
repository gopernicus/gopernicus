# SDK audit S3: environment and logging

Status: IMPLEMENTED AND VERIFIED — 2026-09-09. Original review baseline:
framework HEAD `51798833`. Implementation:
[environment-logging-cleanup.md](environment-logging-cleanup.md).
Parent: [framework-audit.md](framework-audit.md).

The review below records pre-change evidence and proposals. The owner authorized
implementation; resolution and verification are recorded at the end.

## Scope and questions

Review every source/test file in `sdk/foundation/environment` and
`sdk/foundation/logging`, their documented contracts, and representative host
callers. Check configuration precedence, parsing failures, process state,
secret handling, logger construction, context propagation, and whether each
API and package boundary earns its place. The owner permits justified breaking
changes and challenges to current package names and architecture rules.

## Work plan

1. Trace implementations, tests, docs, framework composition roots, and the
   three reference apps. Use original Gopernicus for relevant comparisons only.
2. Exercise suspicious cases with temporary programs and dummy environment
   keys/files; run fresh scoped Go tests, build, and vet. Do not read real
   `.env` files or credentials, start services, or modify consumer repositories.
3. Use the named `platform-sre` for an independent read-only logging and
   observability critique while the parent audits environment parsing and
   consumer configuration. Agent findings remain recommendations.
4. Record confirmed defects separately from policy choices and simplification
   opportunities, with bounded recommendations and migration implications.
   Update the shared handoff. Keep `AUDIT.md` for implemented breaking changes;
   obtain the owner's decisions before implementing S3 API/policy changes.

## Preconditions and ownership

Branch `firestore-authorization`; HEAD `51798833` at review start. Environment
and logging sources are clean. Previous validation/utility audit changes and
unrelated Firestore work are present; preserve them, especially shared docs.
This slice owns this review record and the shared audit-plan update only.
No generated artifacts, dependency pins, release tags, or publishing are needed.

## Assessment

Keep both packages and their names. Environment loading and typed configuration
are shared host work; logger construction and context-ID enrichment are another
cohesive responsibility. Combining them would obscure that logging reads no
ambient configuration and returns an ordinary `*slog.Logger`. A general config
manager, provider registry, logger interface, or more subpackages is unnecessary.

Fix the demonstrated parser and record-ownership defects. Make context IDs
automatic in `logging.New`, eliminating its second configuration mechanism.
Remove redundant constructors and two unused namespace lookup compositions.
Preserve existing default/empty/group policies; explain their limitations more
directly. These are proposals, not accepted or implemented breaking changes.

## Consumer evidence

All three consumers declare Go 1.26.1. Segovia declares SDK v0.8.0, Coordination
Hub v0.7.0, and GPS 360 v0.7.1 with a local SDK replace. Pins do not prove the
effective vendor/runtime resolution; no consumer was built or upgraded.
Searches covered imports/aliases and excluded vendor, node_modules, and generated
templ files. Paths below are relative to the roots in the parent plan.

| Responsibility | Evidence |
|---|---|
| Tagged config | Segovia `cmd/server/main.go:80,88` pre-seeds logging format/server port and parses config throughout host wiring; `authentication.go:80` parses nested pocket config. Coordination Hub server `main.go:133` and workers `main.go:156` parse datastore config. GPS 360 parses datastore, email, file storage, and worker config. |
| Explicit namespace | Jobs' fenced runtime documents and tests a second `FENCED_JOBS_*` namespace. Real sampled hosts currently pass an empty namespace. Prefixing remains useful without new lookup wrappers. |
| Raw lookups | Coordination Hub and GPS 360 use `GetEnvOrDefault` extensively. Coordination Hub `integrations/objectstore/config.go` uses explicit reads for all-or-none configuration and validates dependent fields; replacing that domain policy with generic env tags would lose clarity. |
| Secrets and mode | Segovia `cmd/server/authentication.go:117,167` reads optional/required hex keys and owns development/production handling. Email and notify capability checks consume `environment.Mode`; authentication aliases it. These shared contracts earn their place. |
| Context logging | Scaffold `templates/init/main.go.tmpl:48`, Segovia server `main.go:83`, Coordination Hub server `main.go:113`, and GPS 360 server `main.go:87` construct logging without `WithTracing`, then wire request-ID/access middleware. Only `examples/cms` opts in among example hosts. |
| Redundant helpers | No callers found for `GetNamespaceEnvValue` or `GetNamespaceEnvOrDefault`; `NewDefault` appears only in its own test. `NewNoop` has eight CMS test callers, plus its SDK test. Direct `NewTracingHandler` callers include two SDK tracing middleware tests. |

The original `sdk/environment` and `sdk/logger` have substantially the same
parsers, helper surface, option machinery, and log-record mutation. They do not
provide a simpler correct replacement. Its older scaffold did check dotenv
loading errors; that is useful precedent to recover.

## Confirmed findings

### E1: Quoted dotenv values and trailing comments interact incorrectly

**Medium priority; data corruption.** `environment/config.go:48–69` recognizes
quotes only when they surround the entire trimmed right-hand side. With a
trailing comment it treats the value as unquoted and may cut at a hash inside
the quoted value.

| Fixture right-hand side | Observed loaded value |
|---|---|
| `"value" # note` | `"value"`, including the quotes |
| `"value # inside" # outside` | `"value`, truncated |
| `'value # inside' # outside` | `'value`, truncated |
| `"value # inside"` | `value # inside`, correct control |

All returned nil errors. These can alter credentials, URLs, and numeric values;
the probe used dummy strings only. Existing tests exercise quotes and comments
separately, not their combination. Fix with explicit single-line parsing that
finds the closing quote before handling a trailing comment. Do not expand into
shell execution, interpolation, escape processing, or multiline values.

### E2: Loading can report success without applying a value

**Medium priority; silent configuration failure.**
`environment/config.go:74` discards `os.Setenv` errors. An empty key returned
nil; a dummy value containing NUL returned nil while the key remained unset.
Malformed lines without `=` and unterminated quotes also pass silently.

Return assignment failures with filename/line/key context and no raw value.
Define the supported single-line syntax and reject malformed assignments and
unterminated quoted values. That stricter syntax is a deliberate behavior change,
separate from the confirmed swallowed-error defect.

Missing files should still return nil and existing process variables, including
empty ones, should remain untouched. Loading is currently incremental: earlier
assignments survive a later error. No atomicity is promised or needed for the
recommended startup usage; document this instead of adding a rollback system.

Framework examples and scaffold currently use `_ = environment.LoadEnv()`.
Correcting loader errors has limited value if callers discard them. The
implementation slice should check errors in these composition roots and their
docs, before parsing configuration. All sampled apps also ignore the result;
carry that into future consumer migration instructions.

### E3: Tagged field parsing is not consistently type-safe

**Medium priority; panic or invalid configuration.**
`environment/tags.go:113–174` has two reproduced problems:

- `[]environment.Mode` passes the string-element-kind check, then panics when
  reflection assigns `[]string` to it. Unsupported input must return an error
  at minimum. The simpler consistent implementation allocates the destination
  slice type and sets its string elements; this supports named string elements
  the same way scalar named strings already work.
- A `float32` field given `1e40` becomes `+Inf` with no error because parsing
  uses 64-bit precision before narrowing. Parse using the destination's bit
  width. The same 64-bit assumption exists for `int` on 32-bit platforms;
  that consequence is source-derived and was not executed on this arm64 host.

Additionally, a tagged unsupported `uint` field returns success when unset and
an error only when populated. That contradicts the public promise that tagged
unsupported kinds are errors and delays finding bad config schemas until
deployment. Validate tagged field support independently of whether an env value
exists. Leave untagged collaborator pointers/interfaces and nested struct
recursion as documented.

No sampled production field uses float32 or a slice of named strings; this is a
reproduced exported-API defect, not evidence of a deployed incident. Enum
validation remains separate: scalar `Mode` parses any string today, and callers
must invoke the corresponding validation/constructor. Do not introduce a
custom-unmarshaler registry to solve the reflection mistakes.

### E4: Parse errors include the complete rejected input

**Medium priority; diagnostic handling.** The wrapped strconv/duration errors
in `tags.go` carry the input. A dummy invalid bool produced:
`error setting field Enabled: cannot parse bool: strconv.ParseBool: parsing "DUMMY_VALUE_NOT_A_SECRET": invalid syntax`.

Startup callers log these errors. Misassigned secret-bearing configuration can
therefore be copied into logs; no actual secret exposure was observed. Include
the full env key, field path, expected type, and a value-free cause instead.
Avoid wrapping the original value-bearing `strconv.NumError` merely behind a
redacted outer message. Preserve useful syntax/range causes where possible.
`Secret` already avoids embedding the entire raw value and should retain that
behavior.

### L1: The context handler modifies shared log-record state

**Medium priority; slog ownership contract.**
`logging/handler.go:31–41` adds attributes without first cloning its record.
A record built with eight attributes and passed twice to the handler yields
Go's `!BUG` attribute on the second output. Supplying `r.Clone()` before each
call eliminates it. The input's spare attribute capacity is shared by a shallow
copy; this is not an allocation-optimization argument.

Add `r = r.Clone()` before enrichment. The installed Go documentation specifies
this in `/usr/local/go/src/log/slog/doc.go:249–261`; `record.go:67` implements
the clone by clipping capacity. Go 1.26's `NewMultiHandler` already clones each
branch, so this review does not claim its ordinary fanout is broken. The
reproduction covers reused records/custom composition, not a deployed incident.

### L2: The default host wiring loses request correlation

**Medium priority; composition gap, not a violation of documented opt-in.**
`web.RequestID` stores an ID in context and returns `X-Request-ID`.
`web.Logger` relies on the logger handler to include that ID. The default
scaffold and all three sampled hosts omit `WithTracing`.

A real in-memory HTTP flow through `RequestID → Logger → handler` returned
204 and the expected response ID, but the JSON access log had no `request_id`.
The identical flow with `WithTracing()` included the matching ID.

Recommend enriching context IDs automatically in `logging.New`. This neither
creates traces nor chooses exporters; absent IDs add nothing, and callers still
use standard slog context methods. Remove `Option`, private `config`, and
`WithTracing`; one constructor configuration path is sufficient. Preserve a
public handler wrapper for hosts using their own slog handler. Rename it
`ContextHandler`/`NewContextHandler` alongside this change to describe its
actual fixed request/trace/span vocabulary.

Alternative: put `ContextIDs bool` in `Options` and enable it in scaffolds.
That preserves opt-in but retains an easy-to-miss setting for a behavior every
sampled host needs. The automatic form is recommended; it changes log output
and requires migration notes.

## Simplifications and policies

| Surface | Recommendation and compatibility |
|---|---|
| `LoadEnv`, `LoadPath`, `ParseEnvTags`, `GetEnvOrDefault`, `GetNamespaceEnvKey` | Keep. They cover explicit default/custom paths, typed population, fallback selection, and key composition. |
| `GetNamespaceEnvValue`, `GetNamespaceEnvOrDefault` | Remove redundant compositions with no observed callers. Use `os.Getenv(GetNamespaceEnvKey(ns, key))` or `GetEnvOrDefault(GetNamespaceEnvKey(ns, key), fallback)`. This is a modest public-surface reduction, not a correctness fix. |
| `Mode`, its parsers/validation, `Secret`/`DecodeSecret` | Keep together in environment. Explicit host-owned posture and reusable hex decoding have real users. No new package or implicit env lookup is justified. |
| `isZeroValue` and setter branches | Simplify to `reflect.Value.IsZero` with the existing empty-slice special case; remove unreachable empty-input branches in the private setter. Preserve semantics; do not invent a config object or cached reflection plan. |
| `NewDefault` | Remove; `New(Options{})` already has identical defaults. No observed caller migration beyond its own test. |
| `NewNoop` | Remove; Go 1.26 provides `slog.New(slog.DiscardHandler)`. Migrate the eight CMS test call sites. Probe: current helper enables INFO and resolves a `LogValuer`; DiscardHandler disables INFO and does not resolve it. Ordinary argument expressions still evaluate in Go. Output remains absent, but Enabled/evaluation behavior changes. |
| Logging's unknown values | Preserve the existing forgiving policy this pass: unknown level → INFO, format → JSON, output → STDERR. These are documented/tested fallbacks. Strict validation would change `New`/parsing signatures and every host's construction flow; consider it separately if the owner wants that operational policy. |
| `WithGroup` | Preserve IDs inside the active group, as `context_test.go:129` explicitly specifies. Forcing top-level IDs needs extra bookkeeping and changes JSON structure without a demonstrated consumer requirement. |
| Precedence | Preserve env > existing nonzero field > default tag; empty env counts as absent. A pre-seeded false/0 cannot override a true/nonzero default tag. Probe confirmed this; it is documented policy, not a parser regression. State the limitation in examples instead of promising all literal values survive. |
| Required values | Preserve `required:"true"` as requiring a nonempty env value, even with a pre-seeded field. A generic parser does not validate application invariants or infer deployment posture. |
| Source documentation | Correct SDK README's obsolete `config` row to `environment`. Shorten historical rationales and absolute guarantees in mode/secret comments while retaining accepted inputs, failure behavior, precedence, and ownership. |

## Composition followups

Keep these concerns in host wiring rather than growing logging configuration:

- Scaffold, examples, and Segovia log a returned `run` failure through
  package-level `slog.Error`, while `run` constructs a local configured logger.
  Once that logger exists, startup failures should use it too. Choose an
  explicit host lifetime/ownership arrangement during implementation; do not
  make SDK construction mutate slog's global default automatically.
- Coordination Hub `cmd/seed/main.go:52` and `cmd/ingest/main.go:54` construct
  their logger before `LoadEnv` runs at `:65`/`:111`. Source ordering shows
  dotenv logging choices arrive too late. Record for that app's migration;
  no consumer app was changed or launched.
- Tracing exporter config has its own zero/default sampling policy and
  validation; env parsing should not absorb it. Review in S9/integrations.

## Recommended implementation boundary

After the owner selects the proposal, create a focused implementation plan:

1. Fix dotenv quotes/assignment diagnostics and tagged field handling. Make
   parse diagnostics value-free; keep present empty/default/required behavior.
2. Clone log records; make context IDs automatic; remove the option machinery
   and redundant helpers; rename the context handler and migrate direct callers.
3. Update affected examples/scaffold, startup error handling, canonical docs,
   and root `AUDIT.md` with a standalone migration entry for implemented
   removals, stricter dotenv/type handling, and changed log output/evaluation.
   `RELEASING.md` should link it without altering parallel Firestore notes.
4. Add behavioral regressions for the confirmed cases, formatter checks, SDK
   build/test/vet and guards, relevant CMS/tracing/scaffold tests, then the
   full workspace/docs gates because callers span modules. Exercise generated
   host startup errors and request/log correlation without external services.

No service startup, consumer edits, dependency changes, release tags, or
publishing belong in that implementation by default. The owner's approval of
the audit itself is not approval of every proposed API or behavior change.

## Verification and handoff

**Passed on Go 1.26.1 darwin/arm64**, from `sdk/`:

```sh
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go build ./foundation/environment ./foundation/logging
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -count=1 ./foundation/environment ./foundation/logging
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go vet ./foundation/environment ./foundation/logging
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go run /tmp/gopernicus-s3-environment-probe.go
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go run /tmp/gopernicus-s3-logging-probe.go
```

The probes exited successfully and reproduced the defects above; that is
evidence of open defects, not a claim the packages are fixed. They used unique
dummy env keys, temporary fixtures, synthetic log values, and
`httptest.NewRecorder` with no network listener. The independent named
`platform-sre` reviewed logging and consumer wiring read-only; the parent
executed all probes/checks and reviewed environment.

**Not run:** consumer builds/apps, live services, browser flows, 32-bit runtime
or concurrency/race probes. Full SDK/workspace/guards/docs gates passed during
S2 and were not repeated for this documentation-only review. No command remains
blocked. Recheck all applicable gates after implementation.

**Changed repository files this slice:** this file and
`plans/framework-audit.md` only. Environment/logging source remains unchanged;
`AUDIT.md` retains only the implemented S2 changes. Temporary probe programs
under `/tmp` are reproducibility aids, not committed tests.

**Next step:** discuss/select the bounded proposal above, then implement and
record migrations. S4 onward remains unaudited; prior S1 and CMS followups stay
in the parent handoff.

## Implemented resolution

The owner authorized the recommended batch. E1/E2 dotenv parsing and diagnostics,
E3 typed parsing/schema checks, E4 value-free errors, L1 record ownership, and L2
automatic request correlation are implemented with behavioral regressions.
Both packages remain; the selected redundant helpers and option machinery are
removed, and the handler is named ContextHandler. Documented defaults, empty/
required behavior, and grouping are preserved.

Examples/scaffold now check loading errors and retain explicit configured logger
ownership through startup. Consumer repositories remain unchanged; their
migration steps are in AUDIT-003. The implementation record lists all changed
files and passing targeted, race, SDK, full workspace, docs, generated-host, and
migration/HTTP checks. No S3 implementation remains pending. Future tracing
sampling/validation review remains in S9/integrations. Next SDK slice is S4.
