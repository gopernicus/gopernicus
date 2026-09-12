# Authentication Firestore store

This module implements all eighteen authentication repository ports over Google
Cloud Firestore Native mode. The connector owns client access, this adapter owns
its documents and queries, and the host owns database lifecycle and index deployment.
It targets authentication v0.11.0 and SDK v0.9.0.

## Construction

```go
repos, err := authfirestore.Repositories(ctx, db)
if err != nil {
    return err
}
```

`Repositories(ctx context.Context, db *firestoredb.DB, opts ...Option)` returns
`(authentication.Repositories, error)`. It probes the embedded index manifest
before returning. The caller's context controls startup and each probe is capped
by `firestoredb.ProbeTimeout` (30 seconds). A canceled context is rejected even
with `WithoutIndexProbe()`. The adapter borrows the database; the caller closes it.

All repository slots are available, including `UserAdmin`, `ActiveSessions` and
`Passwordless`. Host policies and mounting choices decide which services and routes
are enabled. Construction does not start workers, migrate data or deploy indexes.

Use `WithoutIndexProbe()` for the emulator, which has no index registry, or when
the host verifies deployment separately and deliberately removes the Admin API
from its startup dependencies. That option transfers index verification to the host.

## Atomicity and authentication invariants

Every port method rejects a context carrying a connector transaction with
`ErrAmbientTransactionUnsupported`, wrapping `sdk.ErrInvalidInput`. Firestore
requires reads before writes and does not expose a transaction's pending writes
to subsequent reads, so these repositories cannot join an arbitrary host transaction.
The adapter's own atomic operations remain transactions. SQL authentication
adapters also own their focused transactions; their use does not establish an
arbitrary cross-repository host transaction guarantee.

The current contracts include:

- `Users.Provision` creates the user, first identifier and optional initial
  password/OAuth credentials together. A collision leaves no partial identity.
- Password changes, verified identifier changes, OAuth linking and credential
  mutations fence changes against the user's credential revision and revoke the
  required sessions, grants and reset proofs atomically.
- Active-session and grant creation check the current active user, expected
  revision and, for grants, the matching live session. Grant consumption validates
  its operation, proof age and assurance before spending it. Unsuitable proof is
  left available for a less demanding policy with the same binding.
- Password reset checks a versioned proof bound to the current revision and the
  same active, verified recovery identifier. Reset increments the revision and
  commits password replacement and revocation together.
- Known-owner magic links require the issued user, identifier and revision to
  remain current. A known-owner link never provisions a replacement account.
  Only an ownerless version-2 provisioning proof can resolve a newly acquired
  current owner or provision an absent one. Invalid proof leaves the token intact.
- Contact changes expose `Get` and consume only the specified generation ID.
  An older proof cannot delete a replacement change.
- Invitations reserve their tuple while `pending` or `accepting`.
  `ClaimAcceptance` binds the current unexpired token to a subject before a host
  grant runs; `CompleteAcceptance` finalizes that durable claim. Matching claims
  resume after expiry, repeated completion preserves `AcceptedAt`, and an
  accepting invitation cannot be resent, cancelled or declined.
- Credential mutations reject foreign identifier/replacement targets and
  nil or typed-nil variants. Pointer variants of supported mutations are accepted.

An expired challenge, OAuth state or matching contact-change generation is deleted
before its expiry result is returned. An expired reset or rejected magic link makes
no changes. All attempt-local results are reset on transaction retries; a callback's
result is reported only when its transaction commits. Exhausted infrastructure
contention can return `sdk.ErrConflict` without a committed domain outcome.

## Documents and indexes

[SCHEMA.md](SCHEMA.md) records document layouts, claim predicates, query shapes and
limits. Deterministic document IDs and seven claim collections reproduce uniqueness.
Claim-based credential reads recheck the target row's predicate and compare secret
digests in constant time; the claim alone never authorizes a credential.
The primary-email directory projection is maintained with identifier mutations.
Invitation metadata and security-event details use JSON text for consistent
round trips across adapters.

The adapter reserves its twenty collection names. Hosts must avoid collisions at
any depth: Firestore field overrides apply to every collection with the same group
ID, including host subcollections. Use a separate database or different host names.
There is no collection-prefix option.

The embedded `firestore.indexes.json` contains composite indexes and single-field
configuration. Export it into the host's manifest before deployment:

```go
if err := authfirestore.ExportIndexes("firestore.indexes.json"); err != nil {
    return err
}
```

Export merges the fragment atomically and preserves unrelated entries. The host
must deploy both `indexes` and `fieldOverrides`, wait for readiness, and account
for shared database index/TTL quotas. `IndexesFS` and `IndexesFile` are available
for deployment tooling. A deployment preflight can call
`firestoredb.ProbeIndexesFS(ctx, db, authfirestore.IndexesFS, authfirestore.IndexesFile)`.
The normal constructor uses that same manifest; missing or building indexes fail
startup. Emulator tests cannot validate deployed indexes.

Operational limits:

- Do not apply TTL or direct deletes to claim-owning rows: those deletions strand
  uniqueness claims. TTL is never a replacement for counted purge or revocation.
- Non-positive challenge purge limits select the whole backlog in one transaction.
  Use bounded batches for backlogs; request-size and transaction-duration limits
  still apply. An oversized atomic operation fails without partial writes.
- Parent-scoped search uses the connector's bounded postfilter. Unsupported
  search is rejected; it is not silently ignored. Preserve continuation cursors
  even when a bounded scan returns an empty page.
- Long host-supplied values still face Firestore's indexed-value limits. Hashed
  document/equality keys do not remove limits on raw sort values and IDs.
- Challenge and contact-change document IDs are replacement tuples, rather than
  their surrogate ID fields. Reusing a supplied surrogate ID across different
  subjects does not enforce the SQL primary-key uniqueness. Contact-change
  consumption still requires the current generation ID.
- API-key hash claims remain reserved after revocation. Invitation token claims
  remain reserved after decline/cancel/accept; resend moves the claim.

## Verification and release status

The current shared conformance suite passed against an isolated emulator on
2026-09-11: 217 leaf cases, with no failures or skips. Adapter-specific tests also
exercise claim release, poisoned claims, revision fences, retries and rollback.
Final verification commands and release evidence are recorded in
[the release plan](../../../../plans/firestore-release.md).

From this module:

```sh
go build ./...
go test ./...
go vet ./...
FIRESTORE_EMULATOR_HOST='<owned-emulator-endpoint>' \
  FIRESTORE_PROJECT_ID='<test-project>' \
  go test -race -tags=integration -count=1 -timeout 45m ./...
```

The emulator factory uses a process-specific named database. It clears that
selected database between fixtures; never point it at a shared or existing database.

Real Firestore verification remains required before publication. Run the four
live roots with `FIRESTORE_LIVE_REQUIRED=1`, a disposable non-default database,
configured credentials and no emulator endpoint:

```sh
FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_REQUIRED=1 \
  FIRESTORE_LIVE_PROJECT_ID='<test-project>' \
  FIRESTORE_LIVE_DATABASE_ID='<owned-disposable-db>' \
  go test -json -tags='integration,live' -count=1 -timeout 45m -run 'Live$' ./...
```

The roots are `TestConformanceLive`, `TestAmbientTransactionRefusedLive`,
`TestIndexProbeAcceptsTheDeployedManifestLive` and
`TestQueryMatrixExecutesAgainstTheDeployedIndexesLive`. No live skip is allowed
for this module. A green emulator run or compile-only live test is insufficient
release evidence; archive the required CI run and its live evidence artifact.
