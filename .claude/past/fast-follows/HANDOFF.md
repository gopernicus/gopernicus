# HANDOFF — fast-follows milestone (session switch: personal → company account)

Written 2026-07-06 by the session that executed sdk-parity and
kvstore-consolidation. The previous session hit a spend limit mid-dispatch;
**zero fast-follows work landed** (all agents died during their read phase —
verified: no new dirs under integrations/, no StatusCheck/Caser code,
go.work/Makefile untouched at 21 modules).

## Paste-in starter for the new session

> Read .claude/plans/fast-follows/HANDOFF.md and .claude/plans/fast-follows/plan.md,
> then execute the fast-follows milestone exactly as planned. The plan is
> ratified; do not re-litigate scope.

## Repo state (all verified green at handoff)

- **sdk-parity milestone:** EXECUTED + gated (NOTES.md 2026-07-06 entry #1).
  8 phases/27 tasks; sdk gained validation/async/conversion/tracing/cryptids/
  oauth/events; web gained JSON kit/SSE/static/OpenAPI; repository→crud
  rename done. Execution log: `.claude/plans/sdk-parity/execution-log.md`.
- **kvstore-consolidation:** EXECUTED + gated (NOTES.md entry #2; plan file
  has the status stamp). Rulings R-KV1–R-KV3 live in ARCHITECTURE.md:
  multi-port `integrations/kvstores/goredis` (bus + cacher + limiter +
  Open/hooks); `datastores/pgx` + `features/*/stores/pgx` renames;
  migration source names proven unchanged ("auth"/"cms"/"jobs").
- **21 modules**, `make check` green at last gate, repo is NOT a git repo
  (templ drift uses the checksum fallback), no tags cut.
- **fast-follows:** plan RATIFIED at `.claude/plans/fast-follows/plan.md`,
  nothing executed.

## What to execute (from the plan; deltas from the in-flight dispatch prompts)

Dispatch task-0 and tasks 1–5 as PARALLEL `implementer` agents (project agent,
use as-is — jrazmi's model policy: never default Sonnet for gopernicus
executors; `verifier` for gates). The parallelization key: **module-creating
agents must NOT touch go.work/Makefile** — each verifies standalone with
`GOWORK=off go mod tidy/build/test/vet` from inside its module dir (the
bcrypt-pattern `replace gopernicus/sdk => ../../../sdk` resolves without the
workspace). Main session registers all five modules at task-6.

Per-task specifics beyond the plan text:

- **task-0:** StatusCheck mirrors `integrations/datastores/pgx`'s shape/name
  exactly (that parity is the requested feature). Caser: package funcs become
  the default Caser — existing conversion tests must pass UNMODIFIED.
- **task-1 otel:** ONE module for the otel family; exporter selected by
  Config (stdout | otlp-grpc | caller-supplied TracerProvider). Hermetic
  tests via otel's tracetest SpanRecorder. Old `telemetry/` is otel-aliased —
  model shapes only, port nothing verbatim.
- **task-2 gcs / task-3 s3:** the old fat storage interface splits into the
  NEW core `Storer` + optional `ResumableUploader`/`SignedURLer` capability
  interfaces (read sdk/filestorage first). Live legs: fake-gcs-server /
  MinIO docker, env-gated LOUD skips. s3 must keep S3-compatible endpoint +
  path-style options.
- **task-4 sendgrid:** hermetic ONLY — httptest via sendgrid-go's overridable
  request host; NEVER a live SendGrid leg (sends real email).
- **task-5 golang-jwt:** keep the alg-confusion guard (assert signing
  method) + test it; ONE permitted sdk edit: the `sdk/cryptids/jwt.go` doc
  sentence saying the integration is "deliberately not built" now points at
  the module (jrazmi committed to golang-jwt this session).
- **task-6 (main session):** add 5 go.work `use` entries + Makefile MODULES
  (alphabetical within existing ordering) → 26 modules; docs sync
  (ARCHITECTURE tree + "Twenty-one"→"Twenty-six", README module list,
  RELEASING enumeration, sdk/README rows gain external backends, NOTES
  dated entry); update the memory dir's feature-roadmap-plans.
- **task-7:** verifier final gate — fresh `go clean -testcache && make check`
  (26 modules), go.work↔Makefile agreement, grep hygiene, loud skips.

Network caveat: aws-sdk-go-v2 / cloud.google.com/go/storage / otel /
sendgrid-go may not be in the local module cache — agents were told to
report BLOCKED rather than substitute libraries if fetches fail.

## Standing conventions (jrazmi, non-negotiable)

- Plan-first; surgical diffs; every task completion ends with a status-board
  closing block (+ YOUR CALL items, Check manually, Run next).
- Never claim success from green tests alone for user-facing behavior.
- sdk/go.mod stays require-free; one library-family per integration; never
  authz/authn — always authentication/authorization; connectors/stores named
  for the package they wrap (R-KV2/R-KV3).
- Salvage from /Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original:
  read + re-type fresh, never copy import paths (guard G4 catches the legacy
  module path).
- Turso: destructive/live runs ONLY against the playground URL
  (`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`);
  verify .env matches before any migrate/run.

## After this milestone (already scoped in the plan's remainder section)

throttler (needs an sdk port decision — flag jrazmi), sqlitelimiter
(recommend skip), events-v1 resumes at its phase 3 (Mount.Events + outbox;
design at .claude/plans/roadmap/events-feature-design.md, status header
current), auth v2+ (authorization/invitations/oauth wiring). httpc and the
crud SQL DSL are ruled dead.

## Verify commands

    make check            # full gate; hermetic
    make guard            # the four layering guards
    docker run --rm -d -p 6379:6379 redis:7 && REDIS_TEST_ADDR=localhost:6379 \
      go test -race ./... # in integrations/kvstores/goredis — all three suites live
