# Authentication and authorization: audit and planning

Status: AUDIT/PLAN COMPLETE — 2026-09-10. Explicit owner request after jobs/events:
in-depth audit and planning for both pockets, **no implementation**.
Parent: [framework-audit.md](framework-audit.md). Prior execution:
[jobs-events-audit-implementation.md](jobs-events-audit-implementation.md).

Owner subsequently authorized all authentication recommendations. Completed execution:
[authentication-audit-implementation.md](authentication-audit-implementation.md).
This completed audit's read-only restrictions describe its original phase;
authorization implementation remains deferred.

## Context and goal

Review correctness, host usability and unnecessary complexity across both
pockets and their actual adapters/consumers. Produce separate evidence-based
findings and sequenced implementation plans, ready for a later owner decision.
Distinguish bugs, architectural choices and unfinished features. Preserve the
host-owned password, identity, OAuth, policy and lifecycle decisions already made.

## Preconditions and scope

- Branch firestore-authentication / 6807ed06; preserve all existing dirty work.
  /tmp/gopernicus-auth-audit-readonly-baseline.json records both pocket trees.
  Only audit/planning documents may change in this phase. Do not append proposed
  changes as implemented entries in AUDIT.md.
- SDK stays stdlib-only; pocket cores stay datastore-free and independent.
  Examine boundaries/names against actual callers; do not invent abstractions
  because a skill or old plan used one. Current architecture overrides obsolete
  paths/assumptions in historical agent role files.
- Use named lead-backend-engineer read-only reviews for independent pocket
  traces, and the planner role to assemble reviewable tasks in the current
  established plans/ location. Configured opus/fable models are unavailable;
  preserve role behavior using inherited models.
- Existing tests and /tmp overlays/probes are allowed. Live checks use newly
  created isolated fixtures only. External consumer repositories are read-only;
  no production accounts, emails, deployments, provider mutations or auth fixes.
- CMS remains deferred. Existing jobs/events changes are tracked separately.

## Audit sequence

1. [x] Authentication: trace public configuration/setup, passwords and recovery,
   verification, sessions/token rotation, OAuth browser/native callbacks,
   optional principal resolution, machine/API-key routes, middleware and failure
   handling. Compare memory/PG/Turso/Firestore semantics, transactions/indexes,
   lifecycle and current host integrations.
2. [x] Authorization: trace model compilation, relationship graph traversal,
   usersets/inheritance, role assignment, checks/batches/cache, budgets/cycles,
   HTTP gating and write authorization. Compare memory/SQL/Firestore semantics,
   transaction consistency and host ownership.
3. [x] Authorization listing: execute the full
   [three-scenario brief](authorization-listing-audit.md). Compare separate stores,
   same SQL without joins and same SQL with joins; specify minimal host code,
   ordering/pagination/counts, failure behavior, read cost and consistency.
   Do not confuse authorization-ID pages with sorted business-result pages.
4. [x] Reproduce important suspected defects with bounded probes; record exact
   evidence and what remains source-only. Run appropriate existing tests and
   retain logs. No application/library/schema implementation.
5. [x] Write distinct authentication/authorization reports with priorities,
   keep/simplify/complete/defer decisions and dependent implementation tasks;
   record proposed breaking/schema changes separately from implemented AUDIT.
6. [x] Reconcile master handoff, verify documentation links and no source changes
   against the auth baseline. Report tested/unverified areas explicitly.

## Findings, plans and verification

Completed reports:

- [Authentication](framework-audit-authentication.md): ten ranked findings,
  intended architecture versus incomplete features, real consumer configuration,
  proposed port/schema impacts and seven implementation packets. First priorities
  are credential proof/session-admission fencing, verified invitation ownership
  and authenticated logout/browser establishment.
- [Authorization](framework-audit-authorization.md): evaluator/model/batch parity,
  measured graph amplification, guardian configuration ownership, memory mutation
  parity, boot/logger cleanup and optional guarded-write completion. Includes the
  confirmed root-relative depth discrepancy and dependent task packets.
- [Authorization listing design](authorization-listing-design.md): all three
  deployment choices, minimum host port/adapter placement, query/cursor/count
  semantics, consumer call patterns and measured strategy tradeoffs. Detailed
  framework/SQL evidence is in [listing findings](authorization-listing-findings.md).

Each report distinguishes reproduced defects, source-confirmed behavior,
intentional tradeoffs and unfinished features. Proposed behavior/port/schema
changes stay in these plans. Root AUDIT.md contains only the implemented
jobs/events entries from the preceding phase (AUDIT-020/021).

## Verification and limits

- Authentication core race tests, core/three-adapter build and vet, default
  Firestore unit race tests, and fresh-fixture PostgreSQL/libSQL race conformance
  passed. PostgreSQL non-C collation was explicitly skipped. Browser-policy
  router probes passed by reproducing the flaw; no actual browser exploitation
  or fix verification was performed.
- Authorization core build/test/race/vet, three-adapter build/vet, default
  Firestore hermetic race tests, and fresh-fixture PostgreSQL/libSQL integration
  race suites passed. Non-C PostgreSQL fixture was not enabled. New public API
  probes deliberately assert current faulty behavior; passing them is evidence
  of the findings, not a claim those defects are fixed.
- Listing's memory boundary/race probes and actual PostgreSQL query-plan probes
  passed. The separate 50,000-row direct-grant SQL experiment asserted equal
  business ordering across all three strategies and demonstrated the partial-ID
  sorting counterexample. It is not a production benchmark, full ReBAC SQL
  adapter or remote-store latency simulation.
- Firestore live/emulator semantics and indexes, live OAuth providers, real
  email/SMS delivery, browser behavior and consumer deployments remain unverified.
  External consumers were source-inspected only; no pulls, changes or runs.
- Setup-only sandbox localhost denials were resolved by allowed local-fixture
  escalation. No unresolved test failure. Temporary tests/scripts live in /tmp;
  reports preserve the fixture and expected invariant for maintained regressions
  during implementation.
- All four audit-only PostgreSQL/libSQL containers were stopped and disappearance
  verified. `/tmp/gopernicus-authentication-audit-stores.json` and
  `/tmp/gopernicus-authorization-audit-stores.json` record cleaned_up=true.
  Recreate isolated services before replaying live probes; recorded endpoints are
  historical. Existing containers/databases were untouched.
- Final source integrity checks compare all 609 authentication/authorization
  files, plus added/deleted paths, to the read-only phase baseline. No source or
  schema changes. Documentation link and diff-whitespace checks recorded below.

Final integrity verification passed: SHA-256 comparison of all 609 auth files
found zero changed, added or deleted paths; all 105 local links in the eight
handoff documents resolve; `git diff --check` is clean. Earlier AUDIT entries are
byte-preserved. The full task inventory matches 79 jobs/events paths plus six
additional auth-planning paths (85 unique paths; the master audit is shared).
No auth implementation, commit, release or external consumer change occurred.
An independent final read-only review of both pocket plans and the listing
design/findings found no material contradiction, unsafe recommendation or
verification overclaim requiring correction.

## Handoff and changed files

Next action is owner review of the proposed implementation packets. **Do not
start either pocket's implementation from this completed audit alone.** Current
authorization is to audit and plan; the user explicitly withheld implementation.
Do not turn proposed credential/model/list API migrations into implemented AUDIT
entries until an implementation is authorized and verified. CMS stays deferred.

For a new context, read this central plan, the relevant pocket report and the
listing design when applicable, then the named source/tests. Start by rechecking
branch/diff: pre-existing work spans many prior slices and must be preserved.
Baseline remains firestore-authentication / 6807ed06; no commits or releases made.

Audit/planning phase changed only these seven documents:

- plans/authentication-authorization-audit.md
- plans/framework-audit-authentication.md
- plans/framework-audit-authorization.md
- plans/authorization-listing-audit.md
- plans/authorization-listing-design.md
- plans/authorization-listing-findings.md
- plans/framework-audit.md

The jobs/events execution plan's completion status and file inventory were also
reconciled; all implementation paths remain inventoried in that separate plan.
