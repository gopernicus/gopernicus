# gopernicus — jobs tenant metadata (v0.2.0)

Status: RATIFIED 2026-08-14 (in-session). Rulings: (1) all three tables get the
column (fenced queue is the demand — issue #4 option 2; job_queue/job_schedules
are parity); (2) scoped-sibling shape = STRUCT-INPUT, not positional — TenantID
is an optional vocabulary slot and the feature's existing struct-input variants
(EnqueueJob/EnsureSchedule) are the convention: `EnqueueOnceIn(ctx,
EnqueueOnceInput{Kind, LogicalKey, Payload, TenantID})` / `ReplaceIn(...)`;
(3) index = fenced queue only, partial (`WHERE tenant_id IS NOT NULL`);
(4) a schedule's TenantID IS copied onto each job it fires — vocabulary
carry-through so tenant-scoped ops queries see fired work (ruled after
implementation flagged the ambiguity); (5) playground schema reset authorized
for the turso conformance leg (both live legs PASSED). J1–J5 EXECUTED
2026-08-14. J6 EXECUTED same day — NOT its own PR: a concurrent session had
coordination-hub PR #16 (the repin) open, so the adoption commit rode there
(PR retitled to cover jobs v0.2.0 tenant metadata; Closes #4 + #5); issue #4
answered citing #12's tenant=campaign ruling. #16 MERGED 2026-08-14 (9ee80f1);
issues #4 and #5 closed. MILESTONE CLOSED.
Date: 2026-08-14
Driver: coordination-hub issue gpsimpact/Coordination-Hub#4 — campaign-scoped ops queries against the jobs rails. Events already carries first-class nullable `tenant_id` with store support; jobs lacks the parity, and the fenced queue's encrypted payloads make payload-embedded tenant identity unqueryable there. "Tenant" stays gopernicus-defined: an OPTIONAL, host-defined boundary slot — vocabulary only, never framework semantics (the events posture).

## Design (mirrors the events precedent; everything additive)

- **`job.Job` gains `TenantID string`** (empty = none; follows Job's own empty-zero convention — WorkerName/FailureReason — stores map "" ↔ NULL). `jobs.FencedClaim` gains the same field so handlers see the scope of what they claimed.
- **Input structs grow the field:** `job.Enqueue.TenantID`, `schedule.Ensure.TenantID` — the struct-input variants (`EnqueueJob`, `EnsureSchedule`) pick it up with zero signature changes. The convenience `Enqueue(ctx, kind, payload)` stays tenant-less.
- **Fenced path: sdk `work` protocol untouched.** `EnqueueOnce`/`Replace`/`LatestStatusByKey` keep their frozen protocol signatures. The feature adds tenant-aware struct-input siblings (RULED 2026-08-14): `EnqueueOnceIn(ctx, EnqueueOnceInput{Kind, LogicalKey, Payload, TenantID})` / `ReplaceIn(ctx, ReplaceInput{...})`; the frozen positional methods delegate to the struct forms with empty tenant. The work protocol's vocabulary does NOT gain tenant — that would be an sdk protocol amendment needing all three graduation gates; a feature-level field needs none.
- **No new query APIs this cut.** The column's consumer is operator SQL (the ops gap in #4) plus whatever listing APIs a later demand justifies. `LatestStatusByKey` semantics unchanged.
- **Schema (greenfield rule):** nullable `tenant_id TEXT` folded into the canonical CREATEs of `0001_job_queue.sql`, `0002_job_schedules.sql`, `0003_fenced_job_queue.sql` — BOTH dialects, byte-identical filename sets, plus a partial index on the fenced queue (`WHERE tenant_id IS NOT NULL`) for the ops queries. NO evolution file ships (2026-07-12 rule); an already-migrated host adds its own host-tree ALTER (reference SQL in the upgrade note; coordination-hub is the exemplar). The `0001_job_queue.sql` header comment ("No tenant/aggregate/correlation columns §1") is updated to name the new posture.
- **Stores:** pgx + turso column lists/INSERTs/scans updated; storetest conformance gains tenant round-trip cases (set, unset→NULL, fenced claim carries it, dedup/supersession unaffected by tenant); memstore updated (public, hosts use it).

## Tasks

- **J1** — Domain + service: `Job.TenantID`, `Enqueue`/`Ensure` fields, fenced scoped siblings, `FencedClaim.TenantID`; memstore; unit tests.
- **J2** — Storetest conformance cases (the parity proof both dialects must pass).
- **J3** — Both dialect stores + canonical migration folds; live conformance: pgx against a throwaway postgres:17 container (port 5433 — NOT coordination-hub's), turso per its integration-tag leg.
- **J4** — RELEASING.md upgrade note: jobs + both store modules floor at **minor** (v0.2.0); host ALTER reference SQL for already-applied ledgers; the fenced-payload-encryption rationale.
- **J5** — `make check` green → commit → tags `features/jobs/v0.2.0`, `features/jobs/stores/pgx/v0.2.0`, `features/jobs/stores/turso/v0.2.0` → push.
- **J6** — coordination-hub adoption (its own PR): host migration `0022_jobs_tenant_id.sql` (three ALTERs + index), bump the three jobs requires to v0.2.0, `go mod tidy && go mod vendor`, build/guard/boot green. Auth delivery jobs stay NULL-tenant (user-scoped, correct); ingestion (#3) switches to the scoped enqueue when it lands. Close #4 citing this + PR #12's naming ruling.

## YOUR CALLs

1. Scoped-sibling shape: positional `EnqueueOnceScoped(ctx, tenantID, ...)` vs a struct/option form. Default: positional (two call sites of vocabulary, no struct ceremony).
2. Index: partial index on fenced queue only, or all three tables. Default: fenced only (the encrypted-payload rail where SQL is the ONLY way in); others on demand.
