# Phase 5 — migration, removal, proof hosts, and closeout handoff

Depends on phases 3 and 4.

## Outcome

Delete the auth-specific durable queue only after both replacement modes satisfy
the shared characterization suite, migrate hosts and docs, and return the complete
auth-v3 system to its existing reviewer/remediation wave.

### AV3D-5.1 — remove bespoke delivery persistence and worker

Delete:

- `features/authentication/domain/deliveryjob`;
- auth pgx/turso delivery-job stores and reference/storetest implementations;
- auth delivery-job migrations once the upgrade path is documented;
- the private claim/poll/purge worker and durability reporter; and
- `Repositories.DeliveryJobs`, `DeliveryWorkerAcknowledged`, and obsolete errors.

Retain and reshape the renderer/router, encrypted envelope, initializer, processor,
observer, and public status behavior. Add guards that fail if auth-specific delivery
tables/repositories or request-time provider calls return.

### AV3D-5.2 — canonical migrations and adopter upgrade

Keep pgx/turso filename sets identical in both jobs and authentication modules.
Write an explicit upgrade procedure for deployments with pending auth delivery
rows:

1. stop old auth delivery workers and quiesce admission;
2. drain or export/re-enqueue pending encrypted commands without decrypting them
   in tooling;
3. apply generic jobs schema changes and new host wiring;
4. verify no active auth delivery rows remain;
5. remove the obsolete auth table in the allowed migration strategy; and
6. start the generic jobs runtime or selected bounded runtime.

If existing released tags require append-only migrations, stop before rewriting a
canonical migration.

### AV3D-5.3 — proof-host variants and operational health

Make auth-cms demonstrate the recommended durable jobs composition. Provide a
small/development bounded-mode variant without hiding its ephemeral posture.

Expose bounded, secret-free health/status sufficient to distinguish:

- runtime not started;
- queue saturated/backlogged;
- provider retry/dead-letter activity; and
- observer failure.

No health endpoint may expose recipient identifiers or payloads.

### AV3D-5.4 — public docs and compatibility inventory

Update authentication, jobs, events, example, migration, and release docs with:

- mode selection and guarantees;
- composition snippets and lifecycle ownership;
- events-as-observation decision;
- encryption/key rotation expectations;
- at-least-once duplicate semantics and resend race;
- bounded-mode crash/multi-instance limitations;
- status retention and purge; and
- public removals/renames plus adopter steps.

Search the repository for every removed symbol/table and record the zero-result
inventory or intentional historical-plan references.

### AV3D-5.5 — final implementation-complete gate and auth-v3 handoff

Run:

- sdk/workers, jobs, authentication, integration, and proof-host tests under
  `-race` where supported;
- live pgx/turso jobs + auth suites on fresh/reset databases;
- restart, stale-claim, resend, saturation, shutdown, and real-provider protocols;
- migration parity and upgrade rehearsal;
- `make check` and `make guard`; and
- adversarial searches for plaintext secrets, direct provider sends, bespoke auth
  job persistence, unbounded goroutines/queues/maps, and event-driven dispatch.

Append the milestone evidence to `00-overview.md`, then begin the existing
`AV3-9.7` reviewer wave. That review covers the whole auth-v3 implementation plus
this follow-up. `AV3-9.8` owns accepted remediation and final reverification; do
not create a competing reviewer track here.

### Phase 5 gate

All AV3D tasks are logged, no bespoke auth queue remains, both modes have their
claimed evidence, hosts/docs/migrations are current, and `AV3-9.7` is unblocked.

## Execution log

Append dated task evidence, the compatibility inventory, upgrade rehearsal, and
the final handoff to `AV3-9.7`.
