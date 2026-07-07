# sdk-parity — execution log

Working notes per phase; feeds task-27's NOTES.md milestone entry. Plan:
`.claude/plans/sdk-parity/plan.md` (RATIFIED 2026-07-06).

## Phase 1 — pure stdlib additions (tasks 1–6) — CLOSED (gate green 2026-07-06)

Verifier gate: full `make check` (18 modules) pass; guards ×4 pass; templ
drift none (checksum fallback — repo not git-managed); sdk/go.mod
require-free; gofmt clean; async race-run clean (15 tests).

All six tasks executed in parallel by implementer agents; each reported
build/test/vet + `make guard` green, sdk/go.mod zero-require held.

Deviations / details to carry into NOTES (task-27):

- **task-6 (cryptids):** input-validation guards wrap `errs.ErrInvalidInput`
  (old repo used bare fmt.Errorf) — matches sdk/email sibling style; crypto-op
  failures keep descriptive wraps. Constructor is in-package `NewAESGCM`
  (no aesgcm subpackage), per D-3.
- **task-5 (slug):** old accent table folds `ß`→single `s` (`Straße`→`strase`);
  matched exactly per plan. Full `make check` was green including cms slug
  call sites. Idempotency invariant holds for accented inputs.
- **task-2 (validation):** reciprocal cross-reference from sdk/web's docs to
  the validation composition recipe deferred to phase 6 (web files out of
  task-2 scope). errors.go doc example softened (no web.ErrBadRequest ref).
- **task-1 (config):** round-trip tests are external `_test` packages
  (web_test, logging_test) making the no-production-import-edge structural.
  logging gets its first external test file (idiomatic, harmless).
- **task-4 (conversion):** old TestAddAcronym dropped with the AddAcronym trim
  (D-2); package doc lives on cases.go (sibling convention).
- **task-3 (async):** two tests added beyond old suite (idempotent Close,
  non-positive-concurrency fallback).
- Post-implementation: one consolidated gopls-modernization sweep over the
  new phase-1 files (atomic.Int64, TypeFor, range-over-int, b.Loop,
  infertypeargs) — newexpr hints on conversion.Ptr call sites deliberately
  NOT applied where Ptr is the subject under test.

## Phase 2 — tracing spine (tasks 7–9) — CLOSED (gate green 2026-07-06)

Verifier gate: full `make check` pass; workers+async race-clean (-count=1);
gofmt clean; sdk zero-require; features/jobs (workers consumer) explicitly
green. Deviations/details for NOTES:

- **task-9 (workers):** J6/J7 supersession implemented per D-7.
  `WithRetryWithinClaim` enforces maxElapsed via context.WithTimeout over the
  whole retry loop (processing + backoff ≤ maxElapsed — the strict reading).
  Wire-time panic on maxElapsed <= 0 (option closures can't return errors —
  sanctioned by D-7); attempts < 1 clamps to 1 per package idiom. Tracer
  default is tracing.Noop{} (unconditional span calls, no nil checks);
  WithTracer(nil) coerced to Noop. Span names use new vocabulary (job.claim,
  not job.checkout). Lease-overrun regression test present (leaseStore fake).
- **task-8 (logging):** purely additive; trace_id/span_id injected only when
  present; existing tests byte-unchanged.
- **task-7 (tracing):** Noop is a zero-value struct (tracing.Noop{}), no
  constructor.
- Tolerated lint hints: infertypeargs/rangeint flags on pre-existing lines in
  workers test files left alone (surgical-diff rule; vet passes).
## Phase 3 — events (tasks 10–12) — CLOSED (gate green 2026-07-06, 19 modules)

Gate: full make check (19 modules); events race-clean fresh; conformance
skips loudly without REDIS_TEST_ADDR; go-redis v9.18.0 is the only direct
external require. task-12 notes for NOTES:
- **Live redis leg RAN and passed race-clean** (docker redis:7 on :6390; all
  8 eventstest subtests + cross-instance broadcast fan-out).
- **Streams-path wildcard was a real design addition**: old goredisbus never
  delivered async emits to "*" subscribers (skipped at consume). New `groups`
  set: local wildcard subscriber lazily ensures emitted topics' groups.
  Documented limitation: streams "*" covers topics the process also emits;
  cross-process wildcard fan-out is the broadcast path's job.
- Close hardened vs old code (idempotent, cancelable XReadGroup, wg.Add under
  closeMu). Old prefix-topic matching dropped (parity with port + design trim).
- gopls showed stale BrokenImport diagnostics after the go.work change; shell
  build/vet authoritative (pass).

task-10 deviations/findings for NOTES:
- **Old-code bug fixed per design:** old RemoteEvent did NOT implement
  Unmarshaler — TypedHandler would silently drop rehydrated events. New
  RemoteEvent.Unmarshal added (design §2 requires it; redis broadcast loop
  depends on it). EncodeEvent kept as superset (re-encode must return original
  payload, not a marshal of the wrapper).
- **Two latent races in old memorybus fixed** during re-type: unsynchronized
  subscription.cancelled field (now map-removal-under-lock); enqueue vs Close
  serialized under closeMu. Post-close guard added to sync Emit path too.
- Old WithWorkerCount was a no-op bug; now applies. Memory uses the repo
  options idiom (NewMemory(opts...), WithLogger/WithWorkerCount/WithQueueSize).
- Metadata accessor order follows design (AggregateType, AggregateID,
  TenantID), not old code. EmitConfig/ApplyOptions exported beyond the D-9
  literal list — out-of-package backends (task-12) need them to honor
  WithSync.
## Phase 4 — oauth (tasks 13–16) — CLOSED (gate green 2026-07-06, 21 modules)

Gate: make check pass (21 modules); google go.mod = exactly one external
require (go-oidc v3.17.0), github go.mod = zero external requires; both
suites hermetic (13 tests each, endpoints injected via unexported seams);
no features/ imports; no authn/authz identifiers. Verifier initially
reported the task-13 amendment "missing" — false negative (it grepped for
oauth-specific terms; the amendment is the rule wording, confirmed present:
"vendor's live API contract", dated). Stale module map/counts in
ARCHITECTURE + Makefile header are task-27 scope (already in plan). Notes:
- google: New returns (*Provider, error) — fail-fast OIDC discovery; type
  named Provider (package carries vendor). x/oauth2 indirect pinned v0.34.0
  (cache-available, MVS-compatible; go-oidc pins v0.28.0 whose zip was absent).
- github: New returns *Provider (no error — no construction network); old
  hardcoded emails endpoint made injectable; no response_type param
  (faithful to old; GitHub defaults to code).

task-13: taxonomy amendment applied surgically to ARCHITECTURE.md (table row,
connectors bullet, decision rule + earn-your-module sentence, dated
2026-07-06) and features/README.md R3 paragraph (refusal stands — memory
store isolates nothing external). make guard green.
## Phase 5 — repository→crud rename (tasks 17–18) — CLOSED (2026-07-06)

Two-step executed clean: sdk/crud landed with D-6 contract (TrimPage placed
in pagination.go per D-6 sketch — the "cursor.go verbatim" instruction
yielded to the sketch to avoid duplicate symbols); 30 importers migrated
(count exact vs enumeration), sdk/repository deleted, make check green
BEFORE and AFTER delete, both grep gates return nothing. Deviation: 2
comment-only fixes in features/cms/stores/{turso,postgres}/assets.go (stale
`repository.ErrNotFound` doc refs — not importers, hence unlisted). gofmt
re-sorted imports in 20/30 files (crud sorts differently) — formatting only.
make test-stores skipped (no creds; hermetic storetest suites green —
recommended-not-gating per plan). Zero semantic changes: clamping kept
everywhere; nothing adopts Reader[T,F]/Order/ParseListRequest yet.
## Phase 6 — web JSON-API merge (tasks 19–24) — CLOSED (2026-07-06)

Gate: fresh make check (testcache cleaned) green; web race-clean; feature
RouteRegistrar assertions hold; zero web→crud imports; SSR kernel files
untouched (mtime evidence — no git). RUN-AND-LOOK executed against
examples/cms on the verified playground turso DB: admin list 200 → create
"sdk-parity gate check" → edit (rename) → public home renders themed with
the entry → public single 200 at the RE-SLUGGED URL (rename recomputes
slug, D-5 write-time semantics confirmed live) → X-Cache MISS→HIT on second
public load. Screenshots at /tmp/gate-*.png. Server stopped after.

Task notes for NOTES (task-27):
- task-19: validation cross-ref landed on FieldErrors doc (package doc lives
  in do-not-touch handler.go). MaxBytesError→413 test authored fresh (old
  repo never had one). Old errorRecorder re-declaration collision resolved
  by reusing existing RecordError seam.
- task-21: write-deadline acceptance criterion proven by mutation (neutered
  extension → test fails). ResponseCapture confirmed unnecessary.
- task-23: old statusCodeStr was lossy (202→"200"); replaced with
  strconv.Itoa + pinning test. Pagination component literal matches
  crud.Page tags exactly; Paginated envelope uses allOf composition.
- task-24: old CORS sent Allow-Credentials on wildcard-matched origins — a
  credential-leak bug; new semantics per D-4 (credentials only for explicit
  allowlist matches). DefaultHeaders keeps before-next() ordering.
- task-20: RouteRegistrar never widened; canonical assertion untouched.
- task-22: SPA index Cache-Control keeps old "no-cache, no-store,
  must-revalidate" verbatim; range requests via ServeContent tested (206).
- carry-in from task-2 (web-side cross-ref): DONE in task-19.
## Phase 7 — email templates (tasks 25–26) — CLOSED (2026-07-06)

task-25: TemplateRegistry/Renderer/Emailer landed over the NEW Sender/Message
(old Client/Email do not return); RenderAndSend folds old Send's
validate/default-From/log behavior; empty req.To left unset so
Message.Validate catches it. task-26: buildMessage branches — empty HTML =
byte-identical plain path (pinned by regression test); HTML set =
multipart/alternative (text first), structure asserted by parsing (random
boundary). buildMessage now returns ([]byte, error) — unexported, Send-only.
Pre-existing SMTP tests passed unmodified. make check green.

## Phase 8 — docs sync (task-27) — CLOSED (2026-07-06, by main session, Fable)

ARCHITECTURE (tree +3 rows, Twenty-one), sdk/README (7 new package rows;
web/email/crud/config/logging/slug rows updated), RELEASING (twenty-one +
enumeration), Makefile header (8→21), events-design status amendment
(phases 1–2 landed, §9 superseded, resume at phase 3), NOTES.md dated
milestone entry (supersessions, taxonomy amendment, salvage bug fixes,
slug caveat, open flags). README.md (top-level) had two stale "eighteen"
spots outside task-27's file list — caught by the DoD grep, fixed (+3
integration rows). Fresh-eyes grep clean across all six docs.
