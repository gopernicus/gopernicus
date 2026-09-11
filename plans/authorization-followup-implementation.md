# Authorization second-review implementation

Current mutation contract: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md)
(AUDIT-026). The approved change removes request receipts, scope revisions and the
best-effort audit sink, adds optional atomic change history, and protects guarded
reads against supported raw writers. Earlier protocol descriptions below are
historical evidence, not the current API.

Status: COMPLETE — 2026-09-11. The owner authorized implementing the updates in
[authorization-deep-review.md](authorization-deep-review.md). That review's
implementation restriction is superseded. Preserve the first implementation,
previous audit work and user changes.

## Preconditions and scope

- Branch `firestore-authentication`, HEAD `6807ed06`; 42 workspace modules.
  Baseline: `/tmp/gopernicus-authorization-followup-baseline.json` (2,139 files).
- Core and all four authorization stores, affected repository examples/tests/docs,
  consumer migration guidance. No external consumer edits, publishing or CMS work.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache` on every Go/make command.
  Formatter `/Users/jrazmi/go/bin/goimports`; preserve generated artifacts.
- Use named implementer and read-only backend/data-review roles for independent
  bounded packets. Their configured opus model is unavailable; inherited models
  retain the roles' behavior. Current ARCHITECTURE overrides stale role paths.
- Previous fixtures were removed. Create new disposable local fixtures as needed,
  record exact identities and remove them after verification.

## Work and acceptance

- [x] F1 mutation guarantees: protect negative membership predicates (AR2-01),
  record actual global-role subjects and reject malformed scope (AR2-02), clone
  each guard proposal (AR2-03), atomic PG set reconciliation conflict/rollback
  (AR2-04), distinguish Firestore contention from policy refusal (AR2-09).
  Preserve budgets, current-model filtering, replay authorization and context.
- [x] F2 evaluator: one linear graph cycle pass (AR2-05); root plus visited role
  grantor step accounting (AR2-06), shared by ordinary and guarded evaluation;
  checked lookahead configuration (AR2-07), cancellation boundaries (AR2-08).
- [x] F3 public contracts: explicit host guardian policy, validated against the
  model at construction; stable errors for rejected writes and receipts only for
  committed outcomes; host-facing DecisionView.Check(CheckRequest), explicit
  global/resource fact checks, with revision/scoped-reader internals retained on
  the adapter port. Keep one evaluator. Snapshot guard callback input per retry.
- [x] F4 clarity: remove unused FilterRelation/RelationTargetsFor from mandatory
  reader/store ports after caller inventory; default FilterPage pulls to fixed
  requested page size; name the existing route-only assignment policy explicitly;
  shorten historical comments in touched public code. Keep the three listing
  recipes and distinct complete-set/page types.
- [x] F5 adoption and proof: update examples, README/API docs, AUDIT and release
  pointers; meaningful regression/conformance tests; live PG (default and named
  schema), libSQL and Firestore emulator coverage of changed paths; relevant HTTP
  host examples; full documented build/test/vet/guards gate, final source inventory
  and fixture cleanup.

## Resolved implementation choices

- Domain rejections become stable SDK-kind errors; successful `no_change` and
  `not_found` remain replayable outcomes. No fabricated unpersisted Receipt.
- RoleRouteAssignmentPolicy is a transport-only policy. Rename the existing hook
  rather than add another domain validator without a demonstrated need. The model
  already governs assignment legality; guards own state-dependent authority.
- A fixed page-sized candidate pull replaces shrinking remaining capacity.
- Receipt-free revision-aware baseline writing (review D6) stays a separate design
  feature. Existing ordinary/guarded ownership limits remain explicit. No silent
  change to baseline receipt/revision semantics.
- Negative predicate protection and one-source guardian plumbing receive a short
  source-level design review before edits; record the selected mechanism below.

## Verification log and progress

Implementation beginning. Append concrete changes, commands, failures, decisions,
changed-file inventory and fixture identities as packets finish. Do not mark
complete on partial testing or carry proposed changes into AUDIT prematurely.


### Implemented design decisions

- PostgreSQL uses SERIALIZABLE for all mutation-owned transactions, retaining
  canonical anchor locks and revision comparisons. Negative membership predicates
  are protected by SSI, not by unbounded reverse graph enumeration. Trusted
  commands retry only vendor serialization/deadlock conflicts; guarded conflicts
  and callback refusals are terminal. Set reconciliation no longer swallows a
  competing exclusive-subject relation conflict; creation timestamps use stored
  microsecond precision.
- Stores keep the single guardian policy source. Defaults are empty; host
  WithGuardianPolicy options and GuardianPolicy getters snapshot their slices.
  NewService validates the actual policy when the relationship kind is wired.
  No role-minimum policy or duplicate Config policy was introduced.
- Refusals return ErrSemanticConflict/ErrInvariantBlocked and nil receipts.
  Applied/no_change/not_found remain durable and replayable. Root refusal-outcome
  aliases were removed; adapter evaluation constants remain. Existing
  ErrInvariantConflict aliases the new invariant sentinel; audit reasons and HTTP
  409 handling remain stable. No receipt/schema migration.
- Public DecisionView owns Check(CheckRequest) and explicit raw fact methods,
  without promoted adapter methods. Per-callback proposal copies prevent guard
  rewrites. Raw fact limits resolve even with opaque roles; successful primitive
  reads recheck cancellation. Store adapters track the actual global-role subject.
- Linear deterministic cycle traversal, role root/grantor work accounting,
  overflow checks and lookup/filter cancellation boundaries are implemented.
  Enumeration charges every compiled grantor before reads; ordinary checks can
  stop after an early allow. FilterPage uses fixed page-sized default pulls.
- Required Reader/Storer ports shed two unused bulk methods. Optional
  RelationSetReader retains bundled behavior; no production consumer caller was
  found in Gopernicus, Segovia v2, Coordination Hub or GPS360. Conformance wrappers
  prove custom stores may omit it without skipping required checks.
- Route hook renamed RoleRouteAssignmentPolicy; no new core legality abstraction.
  Baseline revision-aware writing stays the explicit future D6 design decision.

### Verification so far

- Core build/test/race and targeted vet pass. Maintained tests cover proposal
  isolation, retry freshness, public adapter-method isolation, guardian/model
  compatibility, reader-only default, raw fact budgets, and refusal audit/ID reuse.
- PG full live race: default 99.849s; named authorization_followup 99.038s. Both
  new shared contract deltas pass with race. Only optional non-C collation DSN
  test skipped. Report /tmp/gopernicus-authorization-followup-PG.md.
- Turso full integration/race: 32.449s; new contract delta 1.468s. Build/vet and
  integration compilation pass. Log /tmp/gopernicus-authorization-followup-turso.log.
- Firestore full emulator integration/race: 224.736s, including conformance;
  adapter final 17.590s, shared delta 1.350s, validator delta 1.661s. Build/test/vet,
  integration/live compile-only pass; real dependency-bump contention repeated 3/3.
  Only expected ambient transaction-family skip. No live GCP/index claims.
  Report /tmp/gopernicus-authorization-followup-FS.md.
- F2 evaluator and F4 optional-port/cancellation reports:
  /tmp/gopernicus-authorization-followup-F2.md and
  /tmp/gopernicus-authorization-F4-set-report.md.
- Host full race found four small proof models carrying the unrelated project
  guardian rule. Corrected fixture policy selection; all twelve proof sections
  now pass -race. Other host tests, including real HTTP and PG listing tests,
  passed in that full run. Initial sandbox-denied local listeners were rerun
  with approved local-test access. No approval rejection.
- Existing outcome assertions and an integrity fixture that ignored a previously
  refused purge were corrected; refusal invariants/no-partial-state coverage was
  retained. A misleading Firestore validator-as-read retry test now injects an
  actual failed datastore Reader. No failures waived.
- Root make guard passes. Full make check is running in a byte-recorded snapshot
  /tmp/gopernicus-authorization-followup-check-dv2ejem1, metadata
  /tmp/gopernicus-authorization-followup-check-snapshot.json and log
  /tmp/gopernicus-authorization-followup-make-check.log. All Go source edits were
  formatted with goimports before snapshotting. Real .git guard execution
  complements snapshot before/after generation hashing.

### Owned fixtures (removed)

Metadata: /tmp/gopernicus-authorization-followup-fixtures.json. Only these new
containers were used; existing application containers were not touched:

- PG: 3323db8b2053a6356a17b0a1720b13bf3bd53d7c849af15dacbfff21e511ea73,
  localhost63866.
- libSQL: 9bcaaac365713fc04f7c662b8b91994a95b4c88ac1cf58f7e778e6724db420a5,
  localhost63868.
- Firestore: 9b1c83d4bc424f4660bd1ebe4b4faa9f752f8ce220ccd0848815bada90870bea,
  localhost63872, project gopernicus-authorization-followup.

Migration is AUDIT-024. Current changed-file inventory is
/tmp/gopernicus-authorization-followup-inventory.json. Finish full gate, compare
source hashes, confirm prior AUDIT prefix/generated artifacts preservation, then
remove these exact fixtures and mark F5 complete.


### Final gate and cleanup

Full `make check` PASS: all 42 workspace modules built, tested and vetted;
integration/live sources compile-vetted, generation hashes stable, architecture
guards passed. The no-.git snapshot logs the expected git-only guard limitation;
separate real-worktree `make guard` PASS covers that guard. Tested Go/module bytes
match the final working tree; later differences are Markdown audit records only.
No TypeScript or rendered UI changes required a browser or documentation-site build.
Host behavior was exercised through real HTTP and PostgreSQL listing tests.

All three recorded containers were identity-checked and removed by
/tmp/gopernicus-authorization-followup-cleanup.py. Metadata is marked cleaned_up.
No consumer, application service, production data, release tag or publication was
changed. Existing AUDIT content matches the baseline byte-for-byte as a prefix.
Generated templates and UI assets match the starting baseline. A final review
caught an overbroad Markdown index replacement; its historical brief/context
sections were restored from the prior hash-recorded verification snapshot and
checked independently. The current phase and handoff now link to this completed
implementation rather than the old review-only restriction.

Verification limits: production Firestore/index validation was not run; the
optional PostgreSQL non-C-collation DSN test was not configured. Firestore's
unsupported ambient transaction join family skips explicitly. Existing HTTP and
store semantics, the new concurrency regressions and no-state-on-refusal behavior
were exercised locally. No required implementation or verification remains open.

Next work is a separately scoped review or consumer adoption using AUDIT-024.
The accepted D6 baseline consistency limitation remains explicit; no new
revision-aware baseline writer was added speculatively.

Final inventory: 88 changed paths (15 new, no deleted files), including 76 Go files
that pass goimports. The master index preserves unrelated historical sections;
its stale task-2 auth planning restriction and retired lookup names were also
updated after independent review. No source changes followed the passing snapshot.
