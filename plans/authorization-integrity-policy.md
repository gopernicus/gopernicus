# Principal admission and tuple integrity

Status: COMPLETED — implemented, verified, uncommitted and unreleased. Starting main 22c712e2100ff58e0b8a3a53528a9b43ae69c942;
prior inbound-policy work remains uncommitted and must be preserved.

## Owner decision

All application principal permission checks belong at inbound access points.
The owner's acceptance of IntegrityPolicy supersedes I3's retained atomic
principal-guard exception in plans/authorization-inbound-policy.md. Authentication
proof (credentials, token validity and redemption identity) remains authentication
logic; invitation management by issuer is an inbound access policy.

## Design

- Keep reusable decision evaluation in logic, invoked by inbound. Remove the
  principal guard, actor parameter, transactional permission view and separate
  system-mutator path from tuple writes. The mutations package describes data
  commands, not authorization policy. Expose one principal-free Service.
- Rename GuardianPolicy/GuardianRule/MinAnchors to IntegrityPolicy/IntegrityRule/
  MinSubjects. Preserve Rules, wildcard matching, concrete-subject counting,
  default-one minima, explicit resource teardown and conflict outcomes. Keep
  model shape validation; writes must not depend on a decision service.
- Logic owns pure integrity planning; adapters own serialization, transaction,
  current facts, atomic delta and audit. Remove ApplyGuarded. Keep Apply and
  SemanticValidator. Reject ambient transactions on this atomic command path.
- Permission revocation after inbound admission does not revoke an admitted
  operation. Integrity is checked against the current serialized state at write.
  Two concurrent removals cannot violate a minimum. There is no second permission
  decision in the transaction. Audit attribution is data supplied by inbound.
- Bundled HTTP role routes must require an inbound policy over the validated
  exact assign/unassign command; neither a missing callback nor a coarse
  authenticated gate becomes an implicit allow. Preserve gate requirements,
  input bounds, audit attribution and response mapping. Migrate the example's
  host principal policy to host inbound, including its tests.
- Preserve low-level tuple storage APIs, their ambient joining contract, and
  ordinary read snapshots. Review policy enforcement on these writers explicitly;
  configured integrity must not silently be bypassed by a normal write facade.
- Invitation cancel/resend issuer authorization moves inbound. Retain state,
  expiry, recipient-proof, credential and token consistency validations.
- No schema or dependency changes; no compatibility aliases. No commit/release.

## Tasks

- [x] J1 Principal-free core write service, IntegrityPolicy and inbound role policy.
- [x] J2 Memory/PostgreSQL/Turso adapters and shared conformance: retain atomic
      integrity/audit, remove transactional principal policy machinery.
- [x] J3 Authentication lifecycle principal-check audit and inbound move.
- [x] J4 Host migration, doctrine, guard and error/audit terminology review.
- [x] J5 Verification, architecture/backend review and durable execution record.

## Verification

Goimports; changed-module build/test/vet and race; real HTTP admission/denial;
owned disposable PostgreSQL integrity races/audit, SQLite local tests where
available; complete workspace make check and git diff --check. Replace obsolete
atomic-permission conformance with deterministic admission and integrity tests;
retain current-model shape, cancellation, transaction ownership, rollback,
no-op, last-owner, userset, teardown and audit coverage. Report skips exactly.

## Preserved files and workflow

Do not edit plans/cacher-design.md, plans/gps-360-go-audit-upgrade-handoff.md,
plans/segovia-v2-audit-upgrade-handoff.md or .github/scripts/__pycache__/.
Prior evidence remains plans/authorization-inbound-policy-verification.json and
/tmp/gopernicus-inbound-authorization. New evidence uses
/tmp/gopernicus-integrity-policy. Named implementers own disjoint scopes;
architecture/backend reviewers are read-only. SQL destructive tests require an
owned scratch fixture; never use existing host containers or remote databases.

## Design review refinements

Named architecture/backend review requires configured integrity on every raw
writer too. All addressed scopes (including no-op targets) receive post-state
validation; only explicit teardown bypasses minima. Raw writes retain ambient
joining/savepoints. PostgreSQL post-state rows are locked with FOR UPDATE so
a stale repeatable-read snapshot cannot count concurrently deleted owners.
SQLite reads explicitly name main.iam_tuples to resist temporary shadow tables.
Policy validates at store construction and propagates through relationship views.

## Execution and verification

Implemented J1–J5. Principal access policy now runs at inbound; tuple commands
are principal-free. The existing `logic/mutations` package continues to describe
data changes. `IntegrityPolicy` owns pure rule definitions; every ordinary store
writer enforces them atomically. Explicit teardown supplies its reason and is
the only minimum-subject exception. HTTP role assignment and removal require
inbound WritePolicy; invitation cancel/resend use immutable prepared targets.
The example places principal policies in its host pocket's inbound package.

All 42 modules passed the final sanitized make check (build, test, vet, generated
artifact verification, tagged compile/vet and architecture guards). The final
1917-source/build-input snapshot stayed unchanged for that gate. Authorization,
authentication and auth-cms full race suites passed; real HTTP tests cover deny,
exact target, cancellation, actor attribution and revocation after admission.
Memory, local SQLite and disposable PostgreSQL full adapter race suites passed.
Memory's ambient family skips because it has no transactions. PostgreSQL's
optional non-C test skipped in the full leg and then passed separately against
the same owned en_US.utf8 database. SQLite had no skips. The restored PostgreSQL
raw reconciliation concurrency test remains covered.

Architecture and backend reviews have no outstanding findings. The review caught
and fixed SQLite TEMP-table shadowing, lost raw concurrency coverage, optional
conformance-port handling and callback cancellation before custom writers. The
new shadow-table regression initially reproduced the defect; its corrected run
passes. Nil mutation repositories now fail direct service construction. Eight
G29 synthetic cases passed. Goimports and git diff --check pass. Documentation's
production build passed through its installed CLI (the pnpm wrapper attempted a
blocked bootstrap; no dependency install or package changes were made).

The initial complete workspace gate passed while two final test-only cleanups
landed; their targeted race check also passed. A later final gate caught a schema
test referencing an unused helper removed during cleanup. That assertion now
checks the retained schema directly, and the final frozen-source gate passes.
These intermediate failures and their resolutions are retained in the JSON record.

Twenty PostgreSQL/SQLite tuple-writer benchmark samples passed (five 500ms samples
per store/worker case). They measure principal-free writes with an empty policy
and audit off; concurrent verification workloads make them smoke baselines, not
isolated comparisons to the retired guarded-writer measurements. BENCHMARKS.md
records medians and bounds; the JSON record retains every sample.

The labeled, loopback-only PostgreSQL 17 container had no host mounts and was
removed after testing. No remote Turso, dedicated Redis end-to-end run, or live
authentication datastore suite was rerun. The complete workspace gate covers
their hermetic tests and adapter compilation. No schema, protocol, dependency,
commit, push or tag changes. The three owner plan files retain their original
hashes. Prior inbound-policy work is preserved.

See [verification record](authorization-integrity-policy-verification.json) for
commands, results, source hashes, changed-file inventory and benchmark samples.
