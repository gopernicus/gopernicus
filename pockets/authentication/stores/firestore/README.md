# pockets/authentication/stores/firestore

The authentication pocket's **Google Cloud Firestore** (Native mode) store
adapter — its own module so a host on another datastore never pulls the
Firestore client into its module graph. It owns the collections, the documents,
and the queries; the connector
([`integrations/datastores/firestore`](../../../../integrations/datastores/firestore),
imported here as `firestoredb`) owns how to talk to Firestore; the **host** owns
the database's lifecycle and its index deployment.

It fills **all eighteen** slots of `auth.Repositories` — the identity,
credential, session, machine-identity, audit, invitation, challenge and
atomic-redemption ports, fifty-eight methods across eighteen interfaces — exactly
as the `pgx` and `turso` siblings do, and passes the pocket's shared
`storetest.Run` conformance suite **in full** (212 leaves, 0 failures, 0 skips).

## ⚠️ First: this store does NOT join an ambient transaction (known family difference)

**Read this before choosing the family, not after.** It is ruling R1 of the
firestore-stores milestone, and it is a property of Firestore, not a defect or a
gap awaiting a fix.

A Firestore transaction requires **every read to precede every write** and never
observes its own pending writes. So every port method whose context carries a
connector transaction (`firestoredb.TxFromContext` — that is, a call made inside
`db.Transact` or `db.ReadSnapshot`) **fails loud** with
[`ErrAmbientTransactionUnsupported`](store.go), which wraps `sdk.ErrInvalidInput`.
It never quietly runs on the client beside the host's transaction: a silent split
of the host's atomic unit is the failure mode this refusal exists to prevent. All
fifty-eight methods are driven through both kinds of ambient transaction in
`TestAmbientTransactionRefused`, so the refusal is a table, not a convention.

Unlike the authorization store, that is R1's **whole** appearance here: this
pocket ships **no `RunTransactional` conformance family**, so there is no
loudly-skipped family and — on the live leg — **no allowed skip at all**. The
connector still implements `crud.Transactor` for a host's **own** multi-document
atomicity; that seam is sdk-level and host-facing, independent of whether these
repositories join it.

**If a host needs cross-repository atomicity** — "write my domain row and this
session in one unit, or neither" — it has three options, in the order they should
be considered:

1. **Use the pocket's own atomic write paths.** They are the point of this
   pocket's ports, and each is already ONE Firestore transaction:
   `Users.CreateWithPrimaryIdentifier`, `CredentialMutations.Apply`,
   `UserAdmin.SetStatus`, `Sessions.Rotate`/`ConsumeGrace`,
   `AuthenticationGrants.Consume`, `Invitations`' status transitions,
   `Challenges.Replace`/`ConsumeCode`/`PurgeExpired`, and
   `Passwordless.Redeem` — which does login, adoption or provisioning together
   with its complete revocation set in one commit. Cross-*repository* work is
   what this store refuses; atomic identity work is exactly what it does.
2. **Make the host's own work idempotent and order it after the identity
   write**, retrying on failure — the standard posture for any two systems that
   do not share a transaction.
3. **Stay on `pgx` or `turso`** if the ambient-join guarantee is genuinely
   required. It is a SQL-family property; Redis (`MULTI`/`EXEC` has no reads) and
   DynamoDB (write-only transactions) fail the same spec.

Opening READS to an ambient transaction is a possible later minor change. It is
not the shipped behavior, and nothing here degrades silently into it.

## Surface

| member | shape |
|---|---|
| `Repositories(db *firestoredb.DB, opts ...Option) (auth.Repositories, error)` | all eighteen ports wired; **probes the index manifest** against the live database first |
| `WithoutIndexProbe() Option` | skips the boot probe (below). Two legitimate callers: an emulator run, and a host whose runtime credential cannot be granted `datastore.indexes.list` |
| `ExportIndexes(dst string) error` | MERGES this store's manifest into the host's own `firestore.indexes.json` |
| `IndexesFS` / `IndexesFile` | the embedded manifest, this store's analogue of the SQL siblings' `MigrationsFS`/`MigrationsDir` |
| `ErrAmbientTransactionUnsupported` | ruling R1's sentinel (wraps `sdk.ErrInvalidInput`) |

`UserAdmin`, `ActiveSessions` and `Passwordless` are returned
**unconditionally**, mirroring the turso store: an adapter that can serve a
capability always offers it, and the host's `Config` decides whether anything
mounts. There is no migration side effect and no schema creation — a Firestore
collection springs into existence with its first document.

## Indexes are this store's migrations

Firestore has no DDL, so there is no migration tree. Its analogue is the **index
manifest** (ruling R5): `firestore.indexes.json`, embedded in the module.

**Scaffold.** `ExportIndexes(dst)` MERGES the fragment into the host's own
manifest, creating it when absent. It merges rather than copies because a
manifest is ONE shared document (a host commonly already has one, for its own
collections and for other pockets), where migrations were append-only files. The
write is byte-stable, so re-exporting an unchanged fragment is an empty diff and
a host's unrelated indexes survive.

```go
// in the host's scaffold/generate step, not at boot
if err := authfirestore.ExportIndexes("firestore.indexes.json"); err != nil { ... }
```

**Deploy.** The host deploys its merged manifest, exactly as it applies
migrations — pre-boot, never by the framework. The manifest has **two halves and
both must be deployed**: the `indexes` (composites) and the `fieldOverrides` (the
single-field configuration the composite-free shapes read through). Deploying
only the composites leaves the store's boot probe failing on a field the manifest
declares — which reads like a store bug and is an undeployed manifest.

```sh
firebase deploy --only firestore:indexes          # with a firebase.json, both halves

# or, without the Firebase CLI — one call per composite:
gcloud firestore indexes composite create \
  --collection-group=security_events \
  --database="$DATABASE" --project="$PROJECT" \
  --field-config=field-path=user_id,order=ascending \
  --field-config=field-path=created_at,order=descending \
  --field-config=field-path=id,order=descending

# and one call per field override. `--index` OVERWRITES that field's index set
# and deletes anything it omits, so pass every index the manifest declares for
# it; an override whose "indexes" array is EMPTY is an exemption instead:
gcloud firestore indexes fields update created_at \
  --collection-group=security_events \
  --database="$DATABASE" --project="$PROJECT" \
  --index=order=ascending
gcloud firestore indexes fields update <field> \
  --collection-group=<collection> \
  --database="$DATABASE" --project="$PROJECT" \
  --disable-indexes
```

`.github/workflows/live-stores.yml` does the gcloud form in `jq` loops over this
module's manifest — composites **and** field overrides — and then waits for
**both**: every composite READY (a query against a still-building index fails the
same way a missing one does) and every declared field configuration applied and
not `CREATING`. The two waits are separate because `indexes composite list` never
returns single-field indexes; that is what makes the live leg evidence for the
manifest as a WHOLE rather than for its composites alone. On a **pinned**
(re-used) live database nothing prunes what a manifest no longer declares —
sweep stale entries by hand with `gcloud firestore indexes composite list
--database=<pin>` / `indexes composite delete`, and `gcloud firestore indexes
fields list --database=<pin>` / `indexes fields update <field>
--collection-group=<cg> --clear-exemption`, or they keep consuming the two caps
below.

**Probe.** `Repositories` checks the manifest against the live database through
the Firestore Admin API at construction time and REFUSES to construct when an
index is missing or still building, naming the collection group, the exact field
tuple, and the console page. That is this store's version of the SQL siblings'
table probe: **fail at wiring time, name the missing thing** — because the
alternative is a `FAILED_PRECONDITION` on a production login. The probe is not
retried and not degraded; it needs one IAM permission,
`datastore.indexes.list`.

**Who passes `WithoutIndexProbe()`** — the same three callers `RELEASING.md`
names, and no fourth:

1. **the emulator**, which keeps no index registry, so the probe refuses there
   outright (this store's emulator suite passes it);
2. **a credential that cannot be granted `datastore.indexes.list`** — a runtime
   service account a host will not widen;
3. **a deployment that must boot while the Admin API is degraded**, because the
   probe is a HARD BOOT DEPENDENCY on an API the request path never touches.

All three take ownership of deploying and verifying the manifest themselves.

**What the probe costs at boot.** One Admin `ListIndexes` for the composites plus
one `GetField` per declared field override with a non-empty index list (an
override that declares an empty list issues no RPC). Both pockets mounted in one
host is therefore roughly **2 + N Admin RPCs before the first request**, under
**two** independent `firestoredb.ProbeTimeout` budgets of 30 s — the constructors
take no context, so each store bounds its own probe. For a host that would rather
pay that at DEPLOY time than at boot, the preflight shape is:

```go
// deploy step / preflight job — not the request path
if err := firestoredb.ProbeIndexesFS(ctx, db, authfirestore.IndexesFS, authfirestore.IndexesFile); err != nil { ... }

// boot
repos, err := authfirestore.Repositories(db, authfirestore.WithoutIndexProbe())
```

That keeps the manifest checked against the real database and takes the Admin API
out of the boot path; what it gives up is the guarantee that the database a
process actually connects to is the one that was checked.

**The emulator keeps no index registry and enforces no composite index**, so the
probe refuses outright there and every emulator query runs index-free. An
emulator-green suite is therefore *no evidence at all* about index coverage; that
is why the manifest's proof is a live run (see "Testing").

The entry **counts**, the field overrides, and the per-query derivation are in
[`SCHEMA.md`](SCHEMA.md) §8 ("Index manifest") — the manifest is this store's
contract with hosts the way `migrations/0001`–`0016` are for the SQL siblings,
and the security-events filter matrix is its largest single contributor.

**Two caps, not one**, and this manifest spends both. Firestore allows **200
composite indexes per database** without billing enabled (1000 with) and,
separately, **200 single-field configurations per database** without billing
(1000 with) — the budget every `fieldOverrides` entry spends. That second budget
is **shared with TTL policies**: an indexing exemption and a TTL policy on the
same field count as ONE configuration, so a TTL policy on any *other* field costs
another one. Both caps are per DATABASE, so this module is only part of the bill —
the host's own collections, any other pocket's manifest (the authorization store
ships one too), and every TTL policy count against the same two numbers.
`SCHEMA.md` §8 states the exact numbers this module asks for; they live there and
not here so the count cannot drift in two places.

## Document model

**Twenty top-level collections** (no subcollections), of which thirteen are named
after the SQL tables so the two documentation trees and the operator vocabulary
stay shared, and **seven are CLAIM collections** that have no SQL table at all —
they reproduce a SQL `UNIQUE` index, because Firestore has no unique constraints:

| Row collections (13) | Claim collections (7) | the SQL index each claim reproduces |
|---|---|---|
| `users`, `user_passwords`, `user_identifiers` | `identifier_claims` | the active login/recovery `(kind, value)` uniqueness |
| `sessions`, `oauth_accounts`, `oauth_states` | `identifier_primaries` | the active-primary `(user_id, kind)` partial index |
| `service_accounts`, `api_keys` | `session_refresh_hashes` | `sessions.refresh_token_hash` |
| `security_events`, `invitations` | `api_key_hashes` | `idx_api_keys_key_hash` |
| `challenges`, `contact_changes` | `invitation_token_hashes` | `invitations.token_hash` |
| `authentication_grants` | `invitation_pending` | the pending `(resource, subject, relation)` partial index |
| | `challenge_digests` | `challenges.secret_digest` |

Every SQL `UNIQUE` becomes either a **deterministic document id** (the connector's
`KeyHash` over the row's primary key, so a `Create` collides at commit) or a
**claim document** read-then-created in the SAME transaction as the row (ruling
R3). Document ids are hashed because this pocket declares **no length bound at
any port** and an identifier value or a token may contain `/`, both illegal in a
document id; the original domain values are kept in fields and in everything
returned. A hash is an identity, never a projection and never a sort key.

**The directory projection.** `users` carries `primary_email` and
`email_verified` alongside the row, so `UserAdmin.List`/`GetSummary` resolve the
active primary email in the SAME query instead of one identifier read per user.
The identifiers remain authoritative; every create, primary switch, demotion,
retirement, verification, credential mutation and adoption maintains the
projection transactionally with its row and claims, including CLEARING it when no
active primary email remains. A `users` document is therefore never written from
a whole-document `Set` built out of a domain `user.User` — which has no email
fields and would silently erase it.

**A claim is a PATH to the answer; the row is the answer.** Six read rails resolve
a secret through a claim document — `Identifiers.GetLogin`/`GetRecovery`,
`Sessions.GetByRefreshHash`, `APIKeys.GetByHash`, `Invitations.GetByTokenHash`,
and the `(purpose, digest)` rail behind `Challenges.ConsumeToken`,
`PasswordResets.Redeem` and `Passwordless.Redeem`. Each one RE-VERIFIES the row
it reached against the claim's own predicate (the digests compared in constant
time) and answers `sdk.ErrNotFound` on a mismatch. In SQL that predicate is part
of the query and cannot be skipped; here the claim is an index this store
maintains by hand, and without the re-check a single lifecycle slip would not be
a duplicate row months later — it would be a credential resolving to the wrong
subject immediately.

**The two open bags are JSON TEXT, not Firestore maps.**
`SecurityEvents.Details` and `Invitations.Metadata` are stored with the SQL
adapters' own `'{}'`-for-empty JSON encoding, so every host key round-trips as
itself. Firestore map keys are FIELD PATHS: `""` is rejected, `"a.b"` becomes a
nested field, `"__x__"` is reserved. A native map would have quietly restructured
a header name or a form field, or refused a write the other two families accept.
The side effect is one fewer divergence, not one more: a stored number comes back
as JSON's `float64` everywhere.

Field by field, claim by claim, with every port method's read set and write set:
[`SCHEMA.md`](SCHEMA.md) §3–§7.

### Those twenty collection ids are RESERVED by this store

This store owns the twenty ids above outright. It creates, queries, resets and
sweeps them, and its claim collections encode uniqueness that only its own writes
maintain — so a host document that happens to live under one of those ids is not
"extra data", it is a row this store believes it owns.

It reaches further than the top level, because a **field override is
database-wide for a collection-group id, at any depth**. A `fieldOverrides` entry
naming `users` configures the single-field indexing of `users` *everywhere in the
database*: a host's own `orgs/{id}/users` subcollection inherits this store's
index configuration for `created_at`, `id`, `primary_email` and the rest, and an
entry with an empty `indexes` array would DISABLE indexing on that host
subcollection's field. The manifest cannot scope it more narrowly; that is how
Firestore's field configuration works, not a choice this store made.

**So: this store expects a database that does not share those twenty names** —
neither as a top-level collection nor as a subcollection id anywhere. Give it its
own database (the cheapest answer, and what CI does per run), or rename the
host's colliding collections before wiring it up. There is no prefix option; the
collection ids deliberately mirror the SQL table names so the two documentation
trees and the operator vocabulary stay shared.

## Family differences a SQL host is choosing

Everything here is a deliberate, recorded difference from `pgx`/`turso`, not an
accident. Each cites the `SCHEMA.md` section that derives it.

- **No ambient transaction join** — the first screen, above (R1, §9).
- **No length bound, and hashed ids as the consequence** (§4.2). The ports accept
  unbounded strings, and this store neither rejects nor truncates them, because
  either would diverge from the SQL adapters. `KeyHash` covers every document id
  and every multi-part or unbounded-text equality key. Single-component id
  equalities (`user_id`, `session_id`, `provider`, `purpose`, `kind`, `status`,
  `event_type`) stay RAW for SQL parity, with Firestore's documented 1500-byte
  indexed-value ceiling. **The residual risk, stated rather than hidden:** the
  per-collection `id` tiebreak and `oauth_accounts.provider_user_id` are safe for
  store-minted values (a 20-character generated id) and UNBOUNDED for
  host-supplied ones — past 1500 bytes the index entry truncates and a keyset
  page could repeat or skip.
- **No id claim for `challenges` or `contact_changes`** (§5.10). Their document
  ids are the unique REPLACEMENT tuples, no port method addresses either row by
  id, neither is a cursor PK or a foreign key — so the phantom-write hazard that
  forced the authorization store's id claim does not exist. A host that
  deliberately reuses an explicit id across two different subjects gets a PK
  violation in SQL and two rows here.
- **A single-document write takes no transaction** (§2). `Passwords.Set`,
  `Users.Update`, `ContactChanges.Create`, `SecurityEvents.Create` and the other
  write-one-read-nothing methods write directly: a single-document write is
  already atomic in Firestore, and `refuseAmbient` has already established that no
  host transaction is in play, so wrapping them would add a `BeginTransaction` and
  a `Commit` round trip to the login and password-change paths for no correctness
  gain. Multi-document and read-then-write operations are all one `Transact`.
- **`CreateWithPrimaryIdentifier` reads nothing** (§2). Every uniqueness rule it
  can break is a document that does not yet exist — the users PK, the identifier
  PK, the `(kind, value)` authentication claim, the `(user, kind)` primary claim —
  and each is written with `Create`, whose precondition the SERVER evaluates at
  commit. A check-then-write would be slower AND weaker. R3's "read-then-created"
  describes what a claim HAND-OVER needs, not what taking a free claim needs.
- **The api-key hash claim is NEVER released** (§5.4). `idx_api_keys_key_hash` is
  UNCONDITIONAL in both SQL migrations — a revoked key still occupies its hash in
  Postgres and SQLite — so `Revoke` stamps `revoked_at` and touches no claim, and
  there is no delete path at all. Releasing it would make a revoked credential's
  hash mintable again.
- **`AuthenticationGrants.Consume` MARKS, it does not delete** (§3.13). The row
  survives with `consumed_at` set, which is what makes a replay distinguishable
  from an unknown grant; the committed outcome, not the attempt, is what the
  method reports.
- **An invitation status transition does NOT release the token claim** (§5.6).
  The claim's predicate is "the invitation exists with that hash", which a
  decline, cancel or accept does not change — so a declined invitation's token
  stays resolvable and stays unmintable by anyone else. Only a **resend** moves
  it. "Release everything on transition" is the plausible wrong reading, and it is
  asserted against explicitly.
- **`Passwordless` adoption revokes the grants the user OWNS**, not the wider
  session cascade (§2). Both SQL adapters run
  `DELETE FROM authentication_grants WHERE user_id = ?` on the adoption path,
  while `UserAdmin.SetStatus` spells out `user_id = ? OR session_id IN (…)`. This
  store follows each statement where it is written, so the three families agree.
- **A commit-time `sdk.ErrAlreadyExists` inside `Passwordless.Redeem` is answered
  `passwordless.ErrRedemption`** (§2). Firestore evaluates every `Create`
  precondition at COMMIT, so a lost claim arrives from the transaction after the
  branch is out of scope and the candidates cannot be told apart — the address's
  authentication or primary claim, the new subject's id, the proposed session's
  id or refresh-hash claim. Every one is a key this redemption tried to TAKE, every
  one leaves NOTHING written, and the port says every stable bad outcome is one
  generic sentinel. Letting `sdk.ErrAlreadyExists` escape would also fail the
  port's own concurrency contract.
- **Under EXHAUSTED contention, `sdk.ErrConflict` can escape `Redeem` and
  `ConsumeCode`** (§9). A losing transaction is re-run six times with jittered
  backoff and then reported as a conflict: not the committed result and not the
  stable domain rejection, but "the store could not decide, and nothing was
  written". The SQL adapters can reach the same place through their own busy /
  serialization retries, and it is fail-closed in every family — the caller may
  re-run the workflow. The emulator's documented thirty-second lock release makes
  it far likelier there than on a real database, so the **live leg is what
  measures the real rate**; an emulator run that never hit it proves nothing
  about production.
- **A lost claim reports a NEUTRAL message** (§9). The vendor's AlreadyExists
  names the losing document — this store's collection layout plus a SHA-256
  fingerprint of the address, token or digest the operation was about. The error
  a caller sees still matches `sdk.ErrAlreadyExists` and carries neither.
- **The challenge revocation cascade skips a blank `user_id`** (§9), where the SQL
  adapters' `WHERE user_id = ?` would match rows whose `user_id` is the empty
  string — the anonymous magic-link rows. No caller in the pocket reaches it with
  a blank id, and matching blank-to-blank would let one anonymous flow revoke
  every other anonymous flow's secrets.
- **`RetireIdentifier` does not check that `ReplacementPrimaryID` belongs to the
  user** — no family does; the SQL promotion is an unguarded `UPDATE … WHERE id =
  ?`. What differs is the consequence: the promoted row feeds the ACTING user's
  persisted directory projection, so a host that passes another user's identifier
  can publish that address as this user's `primary_email`, where the SQL
  summary's `user_id`-scoped join would show nothing (§9).
- **`PurgeExpired` with a non-positive limit is ONE request** (§9). The whole
  purge is a single transaction bounded by the 10 MiB request size and the
  270-second ceiling, not by a write count, so sweep a large backlog with an
  explicit limit and repeated calls; an oversized purge fails atomically, having
  deleted nothing.

## TTL is operational only — never a substitute for `PurgeExpired`

Firestore TTL policies are attractive for `sessions`, `oauth_states`,
`challenges` and `invitations`, and they are the wrong tool for most of them
here.

- **Never on a claim-owning row.** A TTL delete is a bare document delete: it
  runs outside this store's transactions and releases NO claim. TTL-deleting a
  `challenges` row would strand its `challenge_digests` claim forever, making
  that digest permanently unmintable; the same is true of `sessions` and
  `session_refresh_hashes`, and of `invitations` and its two claims. The
  collections that own claims are listed in `SCHEMA.md` §5.
- **Never as the port's purge.** `Challenges.PurgeExpired` returns a COUNT the
  conformance suite asserts, at an exact boundary, with claim release inside the
  transaction. TTL deletes on its own schedule (Firestore documents up to 24
  hours after expiry) and reports nothing, so a TTL-only deployment answers a
  count that is not the truth and leaves claims behind.
- **Never as revocation.** Credential revocation is a write this store makes and
  a reader can see immediately. An expiry-driven delete is neither.
- **Where it IS reasonable:** independently disposable rows that own no claim and
  are never counted — `oauth_states` after its short lifetime, and
  `security_events` beyond a retention window if the host wants one. Audit
  ownership against `SCHEMA.md` §5 before adding a policy, and add it to the
  HOST's manifest: this module ships none.
- **A TTL policy is also a single-field configuration**, and those are capped at
  200 per database without billing enabled (1000 with) — the SAME budget this
  module's `fieldOverrides` spend (an exemption and a TTL policy on the *same*
  field count as one; on different fields they cost one each). A host adding TTL
  policies is spending down the index-configuration budget, not a separate one.
  See "Indexes are this store's migrations" above and `SCHEMA.md` §8 for what
  this module already asks for.

**An unbounded `PurgeExpired` is ONE request.** `Challenges.PurgeExpired(ctx,
before, limit)` treats a non-positive `limit` as unbounded (the port's contract),
and here the whole purge — the candidate read, every row deletion and every claim
deletion — is a SINGLE Firestore transaction, bounded by the 10 MiB maximum
request size and the 270-second transaction ceiling rather than by a write count.
An oversized purge is refused by the server ATOMICALLY: nothing is deleted, so it
is safe and makes no progress. **Sweeping a backlog that accumulated while the
job was off, pass a limit and call again** — a few thousand at a time. The SQL
adapters bound their *statement* instead of their transaction, so this is a
scheduling difference between the families, not a semantic one (`SCHEMA.md` §9);
the steady-state scheduled purge is unaffected.

## Search is a parent-scoped postfilter (ruling R4)

Firestore has no substring operator. `crud.ListRequest.Search` demands literal
substring match with ASCII case folding, a count that reflects the search, and
cursor paging under it — so this store reads the parent-scoped rows and applies
`crud.MatchesSearch` in Go while page-filling.

**Exactly one list in this pocket is searchable:**
`APIKeys.ListByServiceAccount`, whose base query is already scoped to ONE service
account, over `apikey.SearchFields`. Its cost is O(documents scanned in that
parent scope), which is the price of the contract. Every other list refuses a
non-blank `Search` with `sdk.ErrInvalidInput` rather than ignoring it — today's
turso posture for lists without `SearchFields`.

**The rule, pinned for the future:** a Firestore list honors `Search` only under
a parent scope. A top-level searchable list — a users-directory search is the
obvious candidate — is a **plan-level decision**, never an accidental full scan
someone adds to satisfy a ticket.

## Measured costs

All emulator measurements, stable across repeated runs; the live leg re-measures
what matters. The units to watch are **contended documents** and **the size of a
revocation set**, not row count.

- **The full emulator package: ~73–93 s.** The 212-leaf conformance suite alone
  is 60–72 s (0 failures, 0 skips); the rest is this module's own document-level
  and manifest roots. Run twice end to end it took ~162 s. That is why the
  Makefile leg and CI carry `-timeout 45m` rather than the authorization store's
  30: the ceiling is for a contended emulator on a shared CI runner, not for the
  median. **Give the suite its own emulator database** — `firestoretest.Reset`
  clears a WHOLE database, so a second suite pointed at `authentication`
  concurrently empties this one's fixtures mid-test and the failures arrive as
  empty list results that read like ordering bugs.
- **Contention on ONE document is the dominant cost.**
  `ConcurrentDeactivateVersusMint` — 12 rounds of a two-way race between a
  deactivation and a session mint on one user document — takes **20–29 s** on the
  emulator (26–29 s under `-race`). That is the emulator's documented lock
  behavior (locks "may take up to 30 seconds to be released") resolving the
  fence, not a retry storm. `ConcurrentRedeemsCommitExactlyOne` (8 rounds × 2
  racers on one magic link) takes ~21 s for the same reason, and the eight-way
  concurrent grant consume ~3 s.
- **Every fixture resets the database with a DELETE sweep**, not a `TRUNCATE`:
  20 collections × ~200 leaves. It is the emulator's price for isolation, and it
  is why this suite is minutes rather than seconds.
- **`Passwordless.Redeem`'s adoption is the largest read set in the pocket**: the
  digest claim, the challenge row it names, the identifier claim, the identifier
  row, the user, **every session of that user** (each carrying the refresh claim
  its deletion releases), **every grant the user owns**, and **every challenge of
  the caller's `RevokeChallengePurposes`** — then the whole revocation plus the
  new session in one commit. A user with thousands of live sessions is the shape
  to watch; the 10 MiB request maximum, not a write count, is what bounds it.
- **A batch is bounded by the request, not by a write count.** Firestore
  publishes NO per-transaction write COUNT limit (the quotas page bounds a commit
  by the 10 MiB maximum API request size and by 500 field transformations *per
  document*). This store therefore enforces no ceiling of its own and never
  splits an operation — a split is a partially applied revocation, the opposite
  of what these methods promise. An oversized request is refused by the SERVER,
  **atomically**: nothing is written.
- **The Admin API is a BOOT cost, not a per-request one.** The index probe issues
  one `ListIndexes` plus one `GetField` per declared field override with a
  non-empty index list, once per `Repositories` call, under a 30 s
  `firestoredb.ProbeTimeout`; nothing on the request path touches the Admin API.
  The live suite therefore constructs with the probe ONCE per package rather than
  once per fixture — see "Indexes are this store's migrations" for the
  deploy-time preflight a host can use instead.
- **`Transact`'s callback may run more than once.** That is the connector's
  contract and it reaches through here: a retried attempt re-runs the whole read
  phase and resets every attempt-local result. Exhaustion is `sdk.ErrConflict` —
  an infrastructure conflict the caller may retry — never a committed outcome.
- **A purge and a concurrent `Challenges.Replace` cannot interleave** on the
  emulator: a Firestore read-write transaction takes read locks, so the
  replacement BLOCKS until the purge settles rather than forcing a retry. The
  retry branch is asserted too, for a deployment that drops the lock instead of
  holding it; the invariant — the live replacement survives and stays redeemable
  — holds under both interleavings. Which branch a real database exercises is a
  live-leg question, and the test reports it either way without changing.

## No conversion runbook — a Firestore host is greenfield

There is no v1 Firestore database to upgrade: this store's first tag is its first
release. Export the manifest, deploy it, boot. A host migrating data from an
existing SQL authentication database writes that one-time move itself, through
the ports — which is also the only way to get the claim documents built, since
they are this store's form of the SQL unique indexes and no bulk document import
creates them.

## Testing

```sh
# hermetic — no emulator, no network, no build tag: derived-key parity, claim and
# collection ownership rules, document encoding
go test ./...

# emulator: the FULL shared conformance suite plus this store's own document cases
docker run --rm -d -p 8080:8080 \
  gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators \
  gcloud emulators firestore start --host-port=0.0.0.0:8080 --project=gopernicus-test
FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 FIRESTORE_PROJECT_ID=gopernicus-test \
  go test -tags=integration -count=1 -timeout 45m ./...

# live: a run-owned disposable database with this store's indexes READY
FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_REQUIRED=1 \
  FIRESTORE_LIVE_PROJECT_ID='<test-project>' FIRESTORE_LIVE_DATABASE_ID='<run-owned-db>' \
  GOOGLE_APPLICATION_CREDENTIALS='<sa.json>' \
  go test -json -count=1 -tags='integration,live' -timeout 45m -run 'Live$' ./...
```

**Emulator (`integration && !live`).** The suite opens the emulator database
named **`authentication`** — `firestoretest.Reset` clears a WHOLE database, so
every store train owns its own and the three Firestore suites cannot clobber one
another — and constructs with `WithoutIndexProbe()` because the emulator keeps no
index registry. `-timeout 45m` is not padding; see "Measured costs". Without
`FIRESTORE_EMULATOR_HOST` every case skips loudly, because a silent green here
would claim a datastore-family conformance nothing verified. **Nothing in this
leg skips by design** — there is no `RunTransactional` family in this pocket.

**What an emulator green does NOT prove**, so no green here may be read as
production evidence: it enforces **no composite index** and keeps no index
registry (the probe refuses there); its transaction behavior is not identical and
its documented locks "may take up to 30 seconds to be released"; it cannot
produce commit-contention exhaustion; it does not enforce all limits.

**Live (`integration && live`).** `firestoretest.OpenLive` +
`ResetLive(t, db, <this store's twenty collections>)`; a live database is never
emptied wholesale, and a collection missing from that list would leak state
between fixtures — a leaked CLAIM most of all, because it reads as a
duplicate-detection bug rather than as leftover data. The live leg makes **one
probe-enabled construction per package** (`probeLiveOnce`) and every per-fixture
construction then passes `WithoutIndexProbe()` explicitly: that the shipped
manifest actually serves this store's queries is precisely what only a live run
can show, and one construction proves it exactly as well as two hundred do —
while two hundred would spend two hundred `ListIndexes` plus two hundred
`GetField` per declared override against a shared project's Admin quota.
`TestIndexProbeAcceptsTheDeployedManifestLive` asserts the probe's verdict on its
own besides. Unconfigured, every root skips loudly;
`FIRESTORE_LIVE_REQUIRED=1` turns those skips into the release-gate failure.

Live roots today, and what each is for:

| root | why it is live |
|---|---|
| `TestConformanceLive` | the FULL shared suite against real Firestore with the manifest deployed and the package's one probe-enabled construction behind it |
| `TestAmbientTransactionRefusedLive` | R1's refusal, all fifty-eight methods, asserted against production's transaction behavior rather than the emulator's |
| `TestIndexProbeAcceptsTheDeployedManifestLive` | the probe's verdict against a real Admin API index registry |
| `TestQueryMatrixExecutesAgainstTheDeployedIndexesLive` | every query shape in [`SCHEMA.md`](SCHEMA.md) §7 executed against the deployed indexes — the manifest's actual coverage proof |

**Zero allowed skips.** The authorization store's live leg allows
`TestRunTransactionalLive` by name; this module has no ambient family and
therefore **no allow-list entry at all** — the audit step in
`.github/workflows/live-stores.yml` carries an EMPTY allow-list on purpose, so
any skipped root fails a required run.

From the repository root, `make test-stores` runs the emulator leg (skipping
loudly without `FIRESTORE_EMULATOR_HOST`) and `make check` vets the `integration`
and `integration,live` files compile-only, so the live leg cannot rot between
dispatches. Both CI legs — `firestore-emulator` and `firestore-live` — live in
`.github/workflows/live-stores.yml`; the live one is dispatch-only, provisions a
disposable database, deploys this module's manifest, waits for READY, and audits
the test/skip counts against the roots derived from the source.
