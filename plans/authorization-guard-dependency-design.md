# Authorization guard dependency ownership

Current mutation contract: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md)
(AUDIT-026). The approved change removes request receipts, scope revisions and the
best-effort audit sink, adds optional atomic change history, and protects guarded
reads against supported raw writers. Earlier protocol descriptions below are
historical evidence, not the current API.

Status: design decision recorded for AZ-C5, 2026-09-11. This phase preserves
baseline and guarded capabilities; it does not implement revision-aware baseline
writes or change consumer code. Companion implementation:
[authorization-audit-implementation.md](authorization-audit-implementation.md).

## Contract

A guard's dependency set includes every fact consulted to decide the mutation:
direct authority, userset membership, containment edges on the target and its
ancestors, and exact/global role assignments. It is not limited to the relation
being changed, nor to one resource type. A currently absent fact can be a
dependency too: a later grant must not escape the same validation rule.

Guarded/trusted mutation commands update the relevant scope revisions. Baseline
relationship and role writes intentionally do not. Recording a revision before
reading a baseline-owned fact cannot detect that fact changing without a revision
bump. Different relation names, disallowing actor topology edits, and an ambient
transaction shared by business rows and baseline tuples do not solve this gap.
The ambient transaction prevents partial business/projection commits; it does not
make a separate guarded transaction participate in its concurrency protocol.

The immutable compiled RelationshipModel/RoleModel is construction policy. Mixed
models across processes are a separate deployment consistency issue; model-aware
reads do not turn baseline writes into revision-aware writes.

## Actual consumer inventory

These are read-only source observations of current local consumers, not a claim
that their deployed workloads exhibit a reproduced race. No host source or data
was modified.

| Consumer | Guard dependency | Writer and implication |
| --- | --- | --- |
| Segovia v2 | `pockets/auth/logic/guard.go:130` checks platform admin; `:171` checks the target management permission through its model. The walk can consult ancestor space/tenant authority and usersets. | Standing grants use actor commands/SystemMutator. These participate in revisions. |
| Segovia v2 | `guard.go:209` reads the space's tenant tuple to bound a proposed tenant-member userset. Its model also traverses `space#parent`, `space#tenant`, dashboard/timeline `#space`. | `internal/outbound/domains/tenancy/authorization.go:72,80` replaces tenant/parent through baseline SetRelationTargets. Dashboard and timeline adapters do the same for `#space` at their respective `authorization.go:35`. Every one is in the guard's possible dependency closure. |
| Segovia v2 | Parent topology is mutable, not merely boot data. | `internal/logic/domains/tenancy/service.go:234,292` implements MoveSpace and updates containment. `cmd/server/tenancy.go:42` wires one ambient SQL transaction for row/tuple parity. This does not establish guarded-write serializability. |
| Coordination Hub | `pockets/auth/logic/model.go:282` checks platform admin and target admin through transaction-bound CheckRelation. Userset expansion extends that dependency set. | Production coordination membership/vendor adapters and bootstrap use SystemMutator. No baseline writer call was found in inspected non-vendored `internal`, `pockets`, or `cmd` source. This is source inventory, not proof that imports/migrations/operators never write facts directly. |
| GPS 360 Go | `pockets/auth/logic/assignment.go:131` GateOnlyGuard deliberately performs no view reads: route gate authorization is the host's contract. | Role writes use a policy decorator over SystemMutator. The framework cannot claim that an earlier HTTP gate is atomically rechecked here. The new role-aware CheckPermission makes a transaction-bound guard possible; converting this host is a separate adoption task. |

Local roots: `/Users/jrazmi/code/segovia/segovia/v2`,
`/Users/jrazmi/code/gps/coordination-hub`, and
`/Users/jrazmi/code/gps/three-sixty/gps-360-go`. Vendored framework snapshots were
excluded from the consumer inventory.

## Supported ownership choices

1. **Immutable or externally serialized topology.** Baseline-owned dependencies
   are established before guarded work becomes available and cannot change while
   it runs, or all relevant writers/readers obey a host coordination protocol.
   This must include every dependency and every process. Segovia's current move
   feature means immutability cannot simply be assumed for that host.
2. **Explicitly accepted weaker consistency.** The host permits a guard to commit
   using topology/authority observed before a concurrent baseline change. Record
   which operations accept this and why; do not advertise those operations as
   atomically protected from dependency revocation. Framework baseline semantics
   remain useful for ordinary desired-state projections under this choice.
3. **One compatible command ownership protocol.** Route security-sensitive
   mutable dependencies through the revision-aware command path and coordinate
   related business transactions at the host. Merely placing a guarded call in
   an ambient SQL transaction is not supported: stores explicitly refuse that
   composition. Receipts, occurrence identity, rollback and retry policy need a
   deliberate host design rather than an undocumented swap of writers.

The framework supports the first two as explicit baseline contracts and the
third where its existing command boundary fits. It does not silently select a
weaker host security policy. Each consumer must choose its contract during
adoption; this framework phase does not claim that choice has been made for
Segovia or GPS 360 Go.

## Separate future feature: revision-aware baseline writes

If required, design a receipt-free baseline writer that participates in the same
scope protocol. That needs all baseline relationship/role operations, including
multi-resource deletes and desired-state replacement, in memory/PG/libSQL/
Firestore; atomic row+revision changes; missing-anchor/phantom handling; canonical
lock ordering; ambient SQL transaction behavior; and retries. Affected scope
discovery must itself be protected. Firestore's read-before-write transaction
shape and its refusal of ambient transactions remain material constraints.

Acceptance would require deterministic baseline-revoke/guard races for direct,
userset, Through, exact and global role dependencies, no-op rules, bulk writes,
rollback and existing ambient conformance. It may change cross-path consistency
and adapter APIs. It is explicitly not included in AZ-C3 or AZ-C6, and no schema
or baseline mutation behavior changes in this implementation phase.
