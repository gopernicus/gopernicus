# SCHEMA — the authentication pocket's Firestore document layout

This file is to this module what `migrations/0001`–`0016` are to
`pockets/authentication/stores/turso`: the tracked, reviewable statement of what
is stored, under which document id, and which SQL constraint each document shape
reproduces. Firestore has no DDL and no unique constraints, so the schema IS
this document plus `firestore.indexes.json` (the access paths) — nothing in the
datastore records it.

Audited at task N1 of the [firestore-stores milestone](../../../../.claude/plans/firestore-stores/authentication.md)
against `pockets/authentication` **v0.10.0** and the turso store's migrations
`0001`–`0016`, which are the authority for every column, key, and predicate
below. Rulings R1–R5 of the milestone README govern.

## 1. Baseline

| Thing | Value |
|---|---|
| Pocket core | `pockets/authentication v0.10.0` (tag `62e69f47`, an ancestor of this branch) |
| Turso store pin | `pockets/authentication v0.7.0` — the SQL stores have NOT been repinned since; their `migrations/` tree is nevertheless current (0001–0016 are all on this branch) and is what this audit read |
| pgx store pin | `pockets/authentication v0.7.0` (same) |
| Connector | `integrations/datastores/firestore v0.1.0` (untagged during this train; workspace-resolved through the `replace` in `go.mod`) |
| sdk | `v0.6.0` (the pocket core's pin; the connector pins `v0.4.0`, so this module takes the higher) |
| SQL tables | 13, over 16 migrations (0014/0015/0016 are ALTERs: user lifecycle, challenge subject keys, invitation metadata) |

## 2. Port inventory — 58 methods across 18 interfaces

`auth.Repositories` has **eighteen fields** filled by **eighteen DISTINCT
interfaces** declaring **58 methods** in total. (The plan text says "eighteen
fields over sixteen port interfaces … about sixty methods"; the audit's count is
the one this store is built against, and `portcalls_test.go` asserts it, so an
upstream port change fails this module rather than silently escaping the R1
refusal.)

The read set of an operation is also its CONTENTION set — a Firestore
transaction locks what it read — which is why every row states it.

### `user.UserRepository` — 3

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `CreateWithPrimaryIdentifier` | users doc (when the caller supplies an id), identifier doc, auth claim (when login/recovery), primary claim (when primary) | users doc **with the directory projection**, identifier doc | takes auth + primary |
| `Get` | users doc | — | — |
| `Update` | users doc | `display_name`, `updated_at` — FIELD updates, never a whole-document Set (§6.2) | — |

### `user.PasswordRepository` — 2

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Set` | — | user_passwords doc (Set IS the upsert; the doc id is the user id) | — |
| `Get` | user_passwords doc | — | — |

### `identifier.IdentifierRepository` — 5

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Get` | identifier doc (active or replaced) | — | — |
| `GetLogin` | auth claim doc → identifier doc; `login_enabled` checked on the row | — | reads the auth claim as its ACCESS PATH |
| `GetRecovery` | same, `recovery_enabled` | — | same |
| `ListByUser` | query `user_id ==`, `active == true`, order `(created_at, id)` | — | — |
| `ApplyVerifiedChange` | users doc (`auth_revision` CAS), replaced identifier doc, displaced primary claim + its doc, the new auth and primary claim docs | retire replaced + displaced rows, create the new identifier, `auth_revision + 1`, `updated_at`, **projection** | releases the retired rows' auth/primary claims, takes the new ones |

### `user.AdminRepository` — 3

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `List` | query users, order `(created_at, id)`; the projection fields answer PrimaryEmail/EmailVerified in the SAME query | — | — |
| `GetSummary` | users doc | — | — |
| `SetStatus` | **BUILT (N2b)** — users doc, every session of the user (for their hash claims), every grant owned by the user **or bound to those sessions** (`readGrantsForRevocation`, deduplicated by grant id) | status, `status_changed_at`, `updated_at`, `auth_revision + 1`; delete sessions and grants. A replay of the stored status writes NOTHING | releases every deleted session's refresh claim |

### `session.SessionRepository` — 7

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | refresh claim doc | session doc | takes the current-hash claim |
| `Get` | session doc | — | — (expired-at-read → `sdk.ErrExpired`) |
| `GetByRefreshHash` | current-hash claim → session doc; on a miss, query `previous_refresh_token_hash ==` | — | reads the claim as its ACCESS PATH |
| `Rotate` | session doc (CAS on the current hash), old and new claim docs | new current hash, previous slot ← expected current, `previous_used = false`, `rotation_count + 1`, **`expires_at` untouched** | releases the old current claim, takes the new one |
| `ConsumeGrace` | session doc (CAS on previous hash AND `previous_used == false`) | `previous_used = true`; the previous hash is RETAINED | — |
| `Delete` | session doc (for its current hash) | delete the doc | releases the claim |
| `DeleteByUser` | query `user_id ==` | delete every doc | releases every claim |

### `session.ActiveUserRepository` — 1

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `CreateForActiveUser` | users doc (status), refresh claim doc | session doc | takes the current-hash claim |

### `oauthaccount.OAuthAccountRepository` — 4

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | — (`Create` on the PK-derived doc id IS the uniqueness check) | oauth_accounts doc | — |
| `GetByProvider` | doc by id | — | — |
| `ListByUser` | query `user_id ==`, order `(linked_at DESC, provider_user_id DESC)` | — | — |
| `Delete` | query `user_id ==`, `provider ==` (the doc id needs `provider_user_id`, which the caller does not supply) | delete the matches; none → `sdk.ErrNotFound` | — |

### `oauthstate.StateRepository` — 2

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | — | oauth_states doc | — |
| `Consume` | doc by id, in the transaction | delete it — the deletion COMMITS even when expired, then `sdk.ErrExpired` is reported (N-D2) | — |

### `serviceaccount.ServiceAccountRepository` — 5

`Create` (doc), `Get` (doc), `List` (query, `(created_at, id)`), `Update`
(read-then-field-update; absent → `sdk.ErrNotFound`), `Delete` (absent →
`sdk.ErrNotFound`). No claims: `name` carries no unique index (§5.9).

### `apikey.APIKeyRepository` — 5

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | key-hash claim doc | api_keys doc | takes the hash claim |
| `GetByHash` | hash claim → doc; returns revoked and expired records VERBATIM | — | reads the claim as its ACCESS PATH |
| `ListByServiceAccount` | query `service_account_id ==`, order `(created_at, id)`, **`PostFilter` for `req.Search`** (R4) | — | — |
| `Revoke` | doc | `revoked_at`; absent → `sdk.ErrNotFound` | — |
| `TouchLastUsed` | doc | `last_used_at`; absent → `sdk.ErrNotFound` | — |

### `securityevent.SecurityEventRepository` — 2

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | — | security_events doc (append-only) | — |
| `List` | query: any subset of `user_id`/`event_type`/`event_status` equalities × the `created_at` range (`Since` inclusive, `Until` exclusive) × both directions | — | — |

### `invitation.InvitationRepository` — 6

**BUILT (N4b).** `Create` performs NO read — every rule it can break is a
document that does not yet exist, and each is written with `Create` so the SERVER
arbitrates (R3). The `Create` row below therefore lists what the operation
CLAIMS, not what it reads.

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | token-hash claim, pending-tuple claim (when the stored status is pending) | invitations doc | takes the token claim, and the pending claim while pending |
| `Get` | doc | — | — |
| `GetByTokenHash` | token claim → doc | — | ACCESS PATH |
| `ListByResource` | query `resource_key ==`, order `(created_at, id)` | — | — |
| `ListBySubject` | query `subject_key ==`, order `(created_at, id)` | — | — |
| `UpdateStatus` | doc, plus the OLD and NEW token claims and the pending claim | status, token hash, expiry, accepted_at, resolved subject, updated_at | releases the pending claim when the STORED status leaves pending; re-points the token claim when `TokenHash` changes |

### `challenge.Repository` — 4

**BUILT (N4c).** `ConsumeCode`'s `Consumed` is projected from the ROW rather than
from the caller's arguments, so it carries both `SubjectKey` and `UserID` — a
superset of what the SQL adapters return, which fill `UserID` from the argument
and leave `SubjectKey` blank. For every code purpose the two are the same value.

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Replace` | the `(subject_key, purpose)` doc (for the displaced row's digest), the new digest claim | Set the doc | releases the displaced digest claim, takes the new one |
| `ConsumeCode` | the doc | attempt increment / delete on redeem, expiry, or lockout — every outcome COMMITS and is then reported through `ConsumeOutcome` (N-D2) | releases the digest claim with the row |
| `ConsumeToken` | digest claim → doc | delete both; an expired token's deletion COMMITS, then `sdk.ErrExpired` | releases the digest claim |
| `PurgeExpired` | query `expires_at <= before`, ordered and bounded, re-read INSIDE the transaction with each row's digest claim | delete rows + claims | releases every purged row's claim |

### `contactchange.Repository` — 2

**BUILT (N4c).** `Create` is ONE `Set` with no read and no transaction: the
document id IS the (user, kind) uniqueness rule, so a single write both stores
the new state and displaces the old one atomically, where the SQL adapters need a
delete-before-insert inside a transaction to say the same thing.

`Create` (Set on the `(user_id, kind)` doc id — replacement is structural),
`Consume` (single-use read-and-delete; expired → the deletion COMMITS, then
`sdk.ErrExpired`; absent → `sdk.ErrNotFound`). No claims.

### `authgrant.Repository` — 3

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | — | authentication_grants doc | — |
| `Consume` | query `consume_key ==`, `consumed_at == null`, order `(created_at, id)`, limit 1 | `consumed_at`; expired → the write COMMITS, then `sdk.ErrExpired` | — |
| `DeleteBySession` | query `session_id ==` | delete every match (bulk, idempotent) | — |

### `credential.MutationRepository` — 2

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Snapshot` | **BUILT (N2b)** — ONE `ReadSnapshot` over: users doc (`auth_revision`), user_passwords existence, oauth_accounts by user (`ListByUser`'s index, re-sorted by provider in Go — §7), active identifiers by user | — | — |
| `Apply` | **BUILT (N2b)** — users doc (revision CAS) + the typed mutation's targets: `RemovePassword` → NOTHING (see below); `UnlinkOAuth` → the user's links for that provider (a query: the doc id needs `provider_user_id`); `RetireIdentifier` → the retired row, the named replacement, the active primary email; `ChangeIdentifierUses` → the row, the displaced primary of its kind, the active primary email | exactly the targeted source, its claims, the recomputed projection, `auth_revision + 1` | per mutation — see §6.3 |

`RemovePassword` reads NOTHING beyond the revision CAS, which reads as an
omission against the row above's original text and is not one: the credential
document's id is derived from the user id and an unconditional `Delete` is
idempotent (the SQL `DELETE … WHERE user_id = ?` affects zero rows without
failing), so reading it first would only widen the transaction's lock set. Same
call, same reason, as N2a's "a free claim is taken, never read".

An ABSENT target is a successful no-op that still advances `auth_revision` in all
three families: the SQL statement behind every kind affects zero rows rather than
erroring, and this store reproduces that rather than inventing a `sdk.ErrNotFound`
the port does not describe.

### `passwordreset.Repository` — 1

**BUILT (N4c).** It writes four collections it does not own and reaches each one
through that collection's owner file (`putPassword`, `readSessionsForUser`/
`dropSessionsForUser`, `readGrantsForUser`/`dropAuthGrants`, and the challenge
helpers) — a composition composes owners, it never bypasses them (§7.2).

`Redeem`: one transaction — consume the live `(purpose, digest)` challenge
through its digest claim, resolve the user from it, Set the password doc, delete
every session (with its refresh claim), every grant, and every challenge of the
named purposes for that user (with their digest claims). A non-live challenge —
unknown, consumed, or expired — is `sdk.ErrNotFound` with nothing applied.

### `passwordless.Repository` — 1

**BUILT (N4d).** N-D2's largest operation, in ONE `retryTransact`, staged as a
read half and a write half (`stage` → `redemptionWrites.write`) so the vendor's
reads-before-writes rule is structural: the staged value holds documents, not a
reader, so the write phase CANNOT read.

READ SET, in order — `challenge_digests/h(purpose, digest)` → the challenge row
it names (the token, with its captured binding); then, once the binding is
decoded and its version accepted, `identifier_claims/h(kind, value)` → the
identifier row it names (the CURRENT owner of the bound address, never the ids
the binding recorded at issue); then, for an existing owner, `users/h(user_id)`.
An ADOPTION widens it to every session of that user (each carrying the refresh
claim its deletion releases), every grant the user owns, and every challenge of
`RevokeChallengePurposes`. Provisioning reads nothing further: every key it takes
is a document that does not yet exist, and `Create`'s precondition is evaluated
by the SERVER at commit (R3). The password is never read — its document id is
derived from the user id and an unconditional delete is idempotent.

WRITE SET — the token row and its digest claim always (through `dropChallenges`
TOGETHER with the adoption's revoked set, because a host may name the link's own
purpose among them and Firestore refuses a document written twice in one
transaction); then, per branch: LOGIN writes only the session and its
current-hash claim; ADOPT drops the password, the grants, every session with its
refresh claim, and the revoked challenges with their digest claims, moves the
identifier through `updateIdentifier` (verified, uses per `AdoptedIdentifierUses`,
claims following the stored predicate), advances `auth_revision` with the
recomputed projection, then writes the session; PROVISION writes the user with
its projection, the verified primary identifier with both claims, then the
session.

OUTCOME POLICY — a stable bad outcome (unknown/expired/replayed token, absent,
malformed or unknown-version binding, blank bound value, login-disabled or
missing identifier, missing or deactivated subject, a would-be provision without
the captured intent) returns `passwordless.ErrRedemption` FROM the callback, so
the transaction aborts and the token is retained. An infrastructure error rolls
the WHOLE transaction back including the token consumption. A commit-time
`sdk.ErrAlreadyExists` — a key this redemption tried to take was taken between
its read phase and its commit — is answered `ErrRedemption` too: Firestore
evaluates every `Create` precondition at commit, so the candidates (the address's
authentication and primary claims, the new subject's id, the proposed session's
credentials) cannot be told apart afterwards, and every one of them is a stable
bad outcome of THIS redemption with nothing written. Two concurrent redemptions
both read the token's digest claim and the row it names, so the loser aborts,
re-runs, finds the claim gone, and answers `ErrRedemption`.

## 3. SQL schema of record → document layout

Every table, column, primary key, unique index, secondary index, and CHECK
constraint from `stores/turso/migrations/0001`–`0016`, and where it goes. The
Firestore field name equals the SQL column name unless stated; every field is
always WRITTEN (an absent field is an absent index entry), and a nullable column
becomes an EXPLICIT null through the connector's `NullTime` helpers.

### 3.1 `users` (migrations 0001, 0014)

| SQL column | Firestore field | Notes |
|---|---|---|
| `id TEXT PK DEFAULT lower(hex(randomblob(16)))` | `id` | store-minted with `firestoredb.NewID()` when the caller sends an empty id (the `cryptids.Database` convention); the doc id is `h(id)` |
| `display_name TEXT NOT NULL DEFAULT ''` | `display_name` | |
| `auth_revision INTEGER NOT NULL DEFAULT 0` | `auth_revision` (int64) | the optimistic-serialization anchor of every credential-policy mutation |
| `status TEXT NOT NULL DEFAULT 'active' CHECK (active, deactivated)` | `status` | CHECK has no Firestore analogue; the domain's `ParseStatus`/`Valid` is the gate, and the store reader normalizes an empty legacy value exactly as the SQL stores do |
| `status_changed_at TEXT` (nullable) | `status_changed_at` (null \| timestamp) | null = never transitioned |
| `created_at`, `updated_at TEXT NOT NULL` | timestamps | |
| — | `primary_email`, `email_verified` | the DIRECTORY PROJECTION (§6). No SQL column: the SQL stores compute it with a LEFT JOIN per page |

| SQL constraint | Firestore enforcement |
|---|---|
| PK `id` | the DOCUMENT ID: `h(id)` |
| `idx_users_created_at_id (created_at DESC, id DESC)` | composite index, both directions (§8) |

### 3.2 `user_passwords` (migration 0002)

`user_id TEXT PRIMARY KEY` → the document id `h(user_id)`; `hash TEXT NOT NULL` →
`hash`. The SQL upsert (`ON CONFLICT (user_id) DO UPDATE`) is a document `Set`.

### 3.3 `sessions` (migration 0003)

| SQL column | Firestore field | Notes |
|---|---|---|
| `id TEXT PK` (app-minted; the access JWT is signed with it BEFORE the insert) | `id` | doc id `h(id)`; never store-minted |
| `user_id TEXT NOT NULL` | `user_id` | |
| `refresh_token_hash TEXT NOT NULL` | `refresh_token_hash` | |
| `previous_refresh_token_hash TEXT` (nullable) | null \| string | **must stay null, never `""`** — every fresh row would otherwise share `""` and a lookup could match an arbitrary session (the cross-session bleed 0003 calls out) |
| `previous_used INTEGER NOT NULL DEFAULT 0` | bool | |
| `rotation_count INTEGER NOT NULL DEFAULT 0` | int | |
| `authenticated_at TEXT` (nullable) | null \| timestamp | null ↔ the zero "not recorded" sentinel |
| `authentication_methods TEXT NOT NULL DEFAULT ''` | string | the SQL adapters' JSON encoding is kept: the descriptors are a domain type this store must not tag, and the string round-trips identically across all three families |
| `assurance_level TEXT NOT NULL DEFAULT ''` | string | |
| `created_at`, `expires_at TEXT NOT NULL` | timestamps | `Get` is expired-at-read |

| SQL constraint | Firestore enforcement |
|---|---|
| PK `id` | the DOCUMENT ID |
| UNIQUE `idx_sessions_refresh_token_hash (refresh_token_hash)` | **claim** `session_refresh_hashes` (§5.3) |
| `idx_sessions_previous_refresh_token_hash … WHERE NOT NULL` (NON-unique) | an equality query on the field; NO claim, because the SQL index is not unique — a rotated-away hash stays resolvable after the grace slot is consumed, which is what lets a reuse still revoke the session |
| `idx_sessions_user_id` | equality query (`DeleteByUser`, `SetStatus`, the revocation paths) |

### 3.4 `oauth_accounts` (migration 0004)

Columns map one-to-one (`provider`, `provider_user_id`, `user_id`,
`provider_email`, `provider_email_verified` bool, `account_verified` bool,
`linked_at`, `access_token`, `refresh_token`, `token_expires_at` nullable,
`token_type`, `scope`).

| SQL constraint | Firestore enforcement |
|---|---|
| PK `(provider, provider_user_id)` | the DOCUMENT ID `h(provider, provider_user_id)` — a duplicate `Create` fails `AlreadyExists` at the server, so provider uniqueness needs NO claim. This table has no surrogate id, so §5.10's id-claim question does not arise |
| `idx_oauth_accounts_user_id` | equality query |

### 3.5 `oauth_states` (migration 0005)

`token TEXT PRIMARY KEY` → doc id `h(token)`; `provider`, `purpose`, `payload`
(opaque text), `expires_at`. Consume is a transactional read-and-delete; the
expiry decision is computed in Go from the returned row, AFTER the delete commits.

### 3.6 `service_accounts` (migration 0006)

`id` (store-minted when empty) → doc id `h(id)`; `name`, `description`,
`created_by`, `act_as_user` bool, `owner_user_id`, `created_at`, `updated_at`.
**Audit finding:** there is NO unique index on `name` — a claim would make this
store stricter than its SQL siblings, so none is written (§5.9).

### 3.7 `api_keys` (migration 0007)

`id` (store-minted when empty) → doc id `h(id)`; `service_account_id`, `name`,
`key_prefix`, `key_hash`, `expires_at`/`revoked_at`/`last_used_at` (nullable),
`created_at`.

| SQL constraint | Firestore enforcement |
|---|---|
| PK `id` | the DOCUMENT ID |
| UNIQUE `idx_api_keys_key_hash` | **claim** `api_key_hashes` (§5.4), which is also `GetByHash`'s access path |
| `idx_api_keys_service_account_id` | equality query + composite with `(created_at, id)` |

### 3.8 `security_events` (migration 0008)

`id` (store-minted when empty) → doc id `h(id)`; `user_id`, `actor_type`,
`actor_id`, `event_type`, `event_status`, `details`, `ip_address`, `user_agent`,
`created_at`. `details` is **JSON TEXT**, the same `'{}'`-for-empty encoding both
SQL adapters write; the round-trip contract is uniform (nil or empty in, non-nil
empty out). Append-only: no update or delete path exists in the port, so none
exists here.

**N4a correction:** the document field carries `map[string]any`, matching
`securityevent.SecurityEvent.Details` exactly. The N1 skeleton typed it
`map[string]string`, which would have narrowed an OPEN bag — a non-string value
the SQL adapters JSON-encode without comment would have been dropped or refused
here.

**N7 correction — JSON TEXT, not a native map.** N4a stored the bag as a native
Firestore map, on the reasoning that the storage shape is each family's choice.
It is not, because Firestore map KEYS are FIELD PATHS: `""` is rejected outright,
`"a.b"` is split into nested fields, `"__x__"` is a reserved name, and `"a/b"` /
`"a[0]"` collide with the path syntax. The bag is open and its keys come from a
HOST, so a rail that recorded a header name, a URL fragment or a form field would
have stored a different bag than it was handed, or failed a write the SQL
families accept. `encodeDetails`/`decodeDetails` (documents.go) are the turso
adapter's `marshalDetails`/`unmarshalDetails` verbatim, so every key round-trips
as itself. The int64/float64 divergence N4a recorded is GONE with it: a number
now comes back as JSON's `float64` in all three families.

Indexes `idx_security_events_created_at_id`, `_user_id`, `_event_type`,
`_event_status` become the composite matrix of §7.

### 3.9 `invitations` (migrations 0009, 0016)

Columns map one-to-one (`id`, `resource_type`, `resource_id`, `relation`,
`identifier`, `identifier_kind`, `resolved_subject_id`, `invited_by`,
`token_hash`, `auto_accept` bool, `status`, `expires_at`, `accepted_at`
nullable, `created_at`, `updated_at`, `metadata` — **JSON TEXT** since N7, the
same `'{}'`-for-empty encoding SQL stores, because the keys are the HOST's and a
native Firestore map's keys are field paths: see §3.8's N7 correction, which
applies here for the same reason), plus the derived `resource_key` and
`subject_key` (§4).

| SQL constraint | Firestore enforcement |
|---|---|
| PK `id` | the DOCUMENT ID |
| UNIQUE `idx_invitations_token_hash` | **claim** `invitation_token_hashes` (§5.5) |
| UNIQUE PARTIAL `idx_invitations_pending_tuple (resource_type, resource_id, identifier_kind, identifier, relation) WHERE status = 'pending'` | **claim** `invitation_pending` (§5.6) — relation INCLUDED, predicate is the STORED status |
| `idx_invitations_resource` | `resource_key ==` |
| `idx_invitations_kind_identifier` | `subject_key ==` |
| `idx_invitations_resolved_subject_id` | equality query (the resolve-on-registration lookup the pocket drives through `ListBySubject` today; kept as a documented access path) |

### 3.10 `user_identifiers` (migration 0010)

| SQL column | Firestore field | Notes |
|---|---|---|
| `id` (store-minted when empty) | `id` | doc id `h(id)` |
| `user_id`, `kind`, `normalized_value` | same | `kind` CHECK (email, phone) has no Firestore analogue; `identifier.Kind.Valid` is the gate |
| `verified_at TEXT` (nullable) | null \| timestamp | null = unverified. A proof TIME, not a boolean |
| `login_enabled`, `recovery_enabled`, `notification_enabled`, `is_primary` | bools | |
| `created_at`, `updated_at` | timestamps | |
| `replaced_at TEXT` (nullable) | null \| timestamp | null = active; retirement is history-preserving |
| — | `active` (bool) | the indexable spelling of `replaced_at IS NULL`, derived and written WITH `replaced_at`, never separately (§4.1) |

| SQL constraint | Firestore enforcement |
|---|---|
| PK `id` | the DOCUMENT ID |
| UNIQUE PARTIAL `idx_user_identifiers_auth_claim (kind, normalized_value) WHERE replaced_at IS NULL AND (login_enabled = 1 OR recovery_enabled = 1)` | **claim** `identifier_claims` (§5.1) |
| UNIQUE PARTIAL `idx_user_identifiers_primary (user_id, kind) WHERE replaced_at IS NULL AND is_primary = 1` | **claim** `identifier_primaries` (§5.2) |
| `idx_user_identifiers_user_active (user_id, kind, created_at) WHERE replaced_at IS NULL` | composite `(user_id, active, created_at, id)` |

### 3.11 `challenges` (migrations 0011, 0015)

| SQL column | Firestore field | Notes |
|---|---|---|
| `id` (store-minted when empty) | `id` | NOT the doc id — see §5.7 and §5.10 |
| `subject_key TEXT NOT NULL DEFAULT ''` (0015; backfilled from `user_id`) | `subject_key` | a PII-free digest; the thing a challenge is unique under |
| `user_id`, `purpose`, `secret_digest` | same | the plaintext secret is NEVER persisted |
| `protector_key_id TEXT` (nullable) | `protector_key_id` (string, `""` = no pepper) | the value is compared, never ordered or filtered; `""` and NULL are the same fact here (tokens use no pepper) |
| `context TEXT` (nullable) | null \| string | the domain distinguishes a nil blob from an empty one, so this one stays nullable |
| `attempt_count`, `version` | ints | |
| `expires_at`, `created_at` | timestamps | |

| SQL constraint | Firestore enforcement |
|---|---|
| PK `id` | NONE — decision in §5.10 |
| UNIQUE `idx_challenges_subject_purpose (subject_key, purpose)` (0015; it REPLACED `idx_challenges_user_purpose`, which 0015 drops) | the DOCUMENT ID `h(subject_key, purpose)` — replacement is structural |
| UNIQUE `idx_challenges_purpose_secret_digest` | **claim** `challenge_digests` (§5.7), also `ConsumeToken`'s access path |

### 3.12 `contact_changes` (migration 0012)

`id` (store-minted when empty), `user_id`, `kind`, `new_value`, the three use
flags, `make_primary`, `replaces_identifier_id`, `expires_at`, `created_at`.

| SQL constraint | Firestore enforcement |
|---|---|
| PK `id` | NONE — decision in §5.10 |
| UNIQUE `idx_contact_changes_user_kind` | the DOCUMENT ID `h(user_id, kind)` — Create IS the replacement |

### 3.13 `authentication_grants` (migration 0013)

`id` (store-minted when empty) → doc id `h(id)`; `session_id`, `user_id`,
`purpose`, `context_digest`, `methods` (JSON string, as sessions), `assurance`,
`authenticated_at`, `expires_at`, `created_at`, `consumed_at` (nullable — null is
the unspent sentinel), plus the derived `consume_key` (§4).

`idx_authentication_grants_session_purpose_context` is NON-unique: it serves the
`Consume` selection (through `consume_key`) and, by its leading column, the
`DeleteBySession` cascade (`session_id ==`). No claim.

## 4. Derived fields and the input-size audit

### 4.1 The derived fields, and why each exists

| Field | Collection | Construction | Why |
|---|---|---|---|
| `active` | `user_identifiers` | `replaced_at == null` | the leading conjunct of BOTH partial unique indexes and of every active-only read, as one indexable boolean |
| `resource_key` | `invitations` | `h(resource_type, resource_id)` | collapses the two-column `ListByResource` filter into one equality |
| `subject_key` | `invitations` | `h(identifier_kind, identifier)` | same for `ListBySubject`, AND it keeps the filter off the raw invitee address (§4.2) |
| `consume_key` | `authentication_grants` | `h(session_id, purpose, context_digest)` | collapses `Consume`'s three-column selection into one equality |
| `primary_email`, `email_verified` | `users` | §6 | the directory projection |

Derived keys are IDENTITIES: never projections, never sort keys. The originals
stay in their own fields and are what the ports return.

**No sort key is derived.** Unlike the authorization store (whose `role_key` and
`grant_key` reproduce a SQL computed column byte-for-byte), every list in this
pocket orders by `created_at` with the row's own `id` as the tiebreak — both
already stored, both already the SQL contract. Firestore orders strings by UTF-8
bytes, which is the same raw byte order SQLite's BINARY collation and pgx's
`COLLATE "C"` give, so keyset parity across the three families needs no work.

### 4.2 Indexed-value length audit (connector C0 record)

Firestore's limits that matter here: a document id must be valid UTF-8, ≤ 1500
bytes, contain no `/`, and not be `.`, `..`, or `__.*__`; an indexed FIELD value
longer than 1500 bytes is silently TRUNCATED in the index.

**FINDING (N1): the authentication ports declare NO length bound at all.**
Unlike authorization (`relationship.MaxRefFieldLen = 256`), nothing in
`pockets/authentication/domain` bounds an identifier value, a user id, a resource
id, a token, or a provider id — `identifier.New` normalizes but does not limit,
and a host may mint ids of any shape. Port-legal input therefore violates every
interesting document-id rule: an email address is arbitrary UTF-8, a host id may
contain `/`, and nothing prevents a 4 KB value.

Consequences, and the posture taken:

| Class | Examples | Posture |
|---|---|---|
| **Document ids** | every collection | `KeyHash` — fixed 64 hex characters, slash-free, never reserved. Mandatory, not stylistic. `keys_test.go` drives every builder with slashes, `.`/`..`, `__name__`, multi-byte and combining Unicode, NUL, newline, and a 4000-byte value |
| **Multi-part or unbounded-text equality keys** | identifier claims `(kind, value)`, `invitation.subject_key`, `invitation.resource_key`, `grant.consume_key`, the challenge doc id | hashed. This is where truncation would actually bite: an equality filter on a raw 2 KB address could match a DIFFERENT address whose first 1500 bytes agree |
| **Single-component id equality kept RAW** | `user_id`, `service_account_id`, `session_id`, `provider`, `purpose`, `kind`, `status`, `event_type`, `event_status`, `previous_refresh_token_hash` | raw, for SQL parity and a readable manifest. Documented ceiling: an id or purpose ≥ 1500 bytes is out of contract |
| **Sortable / range fields** | `created_at`, `updated_at`, `expires_at`, `linked_at`, `authenticated_at` (timestamps) | **safe** — a timestamp has no length |
| **Sortable tiebreak** | each collection's original `id`; `provider_user_id` on `ListByUser` | store-minted ids are 20 characters (`firestoredb.NewID`) or a `cryptids` nanoid; a HOST-supplied id or a provider-issued subject is unbounded at the port. Over 1500 bytes its index entry truncates and a keyset page could repeat or skip a row |
| **Never indexed** | `display_name`, `name` (searched in Go under R4), `key_prefix`, `normalized_value`, `payload`, the OAuth token ciphertext, `details`, `metadata`, `hash`, and every stored SECRET DIGEST (`key_hash`, `token_hash`, `secret_digest`, `refresh_token_hash`) | no exposure — and since N7 that is enforced rather than described: each one carries a `fieldOverride` with an EMPTY index set (§8.4), which is the only way to switch Firestore's automatic single-field indexing off |

**DECISION (N1):** the store does NOT reject an oversized value and does NOT
silently truncate — either would make it stricter than, or divergent from, the
two SQL adapters, and this is a store adapter, not a validator. The ceiling is a
documented FAMILY DIFFERENCE (module README, N6): *keep user, session, service
account, and API-key ids and any value used as a list tiebreak under 1500 bytes.*
Everything the pocket itself mints already is. Revisit only if the port grows a
bound, in which case the raw-equality class can be hashed the way §4.1's keys
already are.

## 5. Uniqueness — which claim enforces what (R3)

Firestore has no unique constraint. Each SQL UNIQUE index becomes either a
DETERMINISTIC DOCUMENT ID (so a duplicate `Create` fails `AlreadyExists` at the
server) or a CLAIM DOCUMENT whose id IS the unique key, read-then-created in the
SAME transaction as its row. A collision is `sdk.ErrAlreadyExists`, matching both
SQL connectors.

Every claim is written and released ONLY by the private helper pair that owns its
row (`putX`/`dropX`) — the discipline the authorization store enforces with a
source-scanning ownership test, and which N2–N4 reproduce here. A claim is
released the moment its row leaves the index's STORED predicate, in the same
transaction: replacement, demotion, retirement, consumption, purge, and bulk
revocation all release.

**THE READ RULE (N7): the claim is a PATH to the answer; the ROW is the answer.**
Six read rails resolve a secret through a claim document — `GetLogin`/
`GetRecovery`, the primary-claim resolution the directory projection is
recomputed from, `GetByRefreshHash`, `APIKeys.GetByHash`,
`Invitations.GetByTokenHash`, and the `(purpose, digest)` rail
`Challenges.ConsumeToken`, `PasswordResets.Redeem` and `Passwordless.Redeem`
share. In SQL the predicate IS the query, so a row that stops matching stops
being returned whatever else went wrong. Here the claim is an index entry this
store maintains BY HAND, so every one of those reads RE-VERIFIES the row against
the claim's own predicate — kind and normalized value and the use conjunct;
`session_id`'s current hash; the key hash; the token hash; the challenge's
purpose AND digest, the digests compared with
`auth.ConstantTimeDigestEqual` — and answers `sdk.ErrNotFound` on a mismatch.
Without it, a single claim-lifecycle slip is not a duplicate row months later, it
is a credential resolving to the WRONG SUBJECT immediately.
`claims_verification_integration_test.go` plants a mis-pointed claim on every
rail and requires exactly that answer.

**THE COMPLETENESS GATE (N7):** `claims_audit_integration_test.go` scripts a full
lifecycle sweep through the ports — create, replace, promote, demote, retire,
rotate, consume-grace, delete, deactivate, reactivate, resend, decline, challenge
replacement, a wrong attempt, a purge, a token consumption, provision, adopt,
revoke — then recomputes all seven predicates below from the RAW documents and
diffs them against the claim collections in both directions: zero missing, zero
stranded.

### 5.1 `identifier_claims` — the authentication claim

| | |
|---|---|
| Document id | `h(kind, normalized_value)` |
| Fields | `doc_id` (the owning identifier document), `identifier_id`, `user_id` |
| Enforces | `idx_user_identifiers_auth_claim` |
| Stored predicate | `replaced_at IS NULL` **AND** (`login_enabled` **OR** `recovery_enabled`) |

A notification-only address takes NO claim — that is what lets a shared household
phone be notification-only on many accounts while identifying at most one
login/recovery subject. The claim is also `GetLogin`/`GetRecovery`'s ACCESS PATH,
which is how those reads avoid an equality filter on unbounded address text
(§4.2); the specific use flag is then checked on the identifier row, because the
claim covers the UNION of the two uses.

### 5.2 `identifier_primaries` — the active-primary claim

| | |
|---|---|
| Document id | `h(user_id, kind)` |
| Fields | `doc_id`, `identifier_id` |
| Enforces | `idx_user_identifiers_primary` |
| Stored predicate | `replaced_at IS NULL` **AND** `is_primary` |

It is also how a primary switch finds the row it must demote without a query, and
how §6 recomputes the directory projection.

### 5.3 `session_refresh_hashes` — the current refresh credential

| | |
|---|---|
| Document id | `h(refresh_token_hash)` |
| Fields | `doc_id`, `session_id` |
| Enforces | `idx_sessions_refresh_token_hash` (the CURRENT hash only) |
| Stored predicate | the session exists and this is its current hash |

The rotated-away (grace) hash takes NO claim: its SQL index is not unique. It
stays queryable on the session document's own field, before and after
`ConsumeGrace`, which is what keeps a reused grace token resolvable for
revocation. `Rotate` releases the old claim and takes the new one in the same
transaction as the CAS.

### 5.4 `api_key_hashes`

`h(key_hash)` → `{doc_id, api_key_id}`, enforcing `idx_api_keys_key_hash` and
serving `GetByHash`. Predicate: the key row exists (revocation and expiry are
service branches, so a revoked key KEEPS its claim — its hash must never be
re-mintable).

**BUILT (N4a).** The predicate above was re-read from the migration rather than
assumed, because it is the one claim in this store that is never released:

```sql
-- 0007_api_keys.sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys (key_hash);
```

It is UNCONDITIONAL — no `WHERE revoked_at IS NULL` — so nothing takes a row out
of it. `Revoke` therefore stamps `revoked_at` and touches NO claim, and there is
deliberately **no `dropAPIKey`**: the port declares no `Delete`, so a release
path would be dead code whose only reachable effect would be to make a revoked
credential's hash mintable again. `apikeys_doc.go` owns the pair
(`putAPIKey` takes the claim; `revokeAPIKey`/`touchAPIKey` are field updates),
and `TestAPIKeyHashClaimSurvivesRevocation` asserts both the retained claim and
its consequence (a re-mint of a revoked hash is `sdk.ErrAlreadyExists`).

### 5.5 `invitation_token_hashes`

`h(token_hash)` → `{doc_id, invitation_id}`, enforcing
`idx_invitations_token_hash` and serving `GetByTokenHash`. Predicate: the
invitation exists with that token hash. `UpdateStatus` may CHANGE the token hash
(a resend), which releases the old claim and takes the new one in one
transaction.

### 5.6 `invitation_pending` — the partial pending-tuple claim

| | |
|---|---|
| Document id | `h(resource_type, resource_id, identifier_kind, identifier, relation)` |
| Fields | `doc_id`, `invitation_id` |
| Enforces | `idx_invitations_pending_tuple` |
| Stored predicate | `status == 'pending'` — the STORED value, never the read clock |

Two consequences the conformance suite asserts: two invitations that differ only
by `relation` coexist (relation is IN the tuple), and an EXPIRED but still
pending invitation KEEPS its claim — wall-clock expiry alone does not change a
stored status, so the tuple frees up only when `UpdateStatus` moves the row off
pending or the row is deleted.

### 5.7 `challenge_digests`

`h(purpose, secret_digest)` → `{doc_id, challenge_id}`, enforcing
`idx_challenges_purpose_secret_digest` and serving `ConsumeToken`. Predicate: the
challenge exists with that digest. Because the challenge document is keyed by
`(subject_key, purpose)`, `Replace` DISPLACES a row by writing over it — so the
displaced row's digest claim must be released in the same transaction, or the old
digest would stay claimed forever and its later reuse would look like a
collision. `PurgeExpired` has the same obligation, which is why it re-reads
candidates and their claims INSIDE the transaction.

### 5.8 Doc-id-enforced uniqueness (no claim needed)

| SQL constraint | Document id |
|---|---|
| `users` PK | `h(id)` |
| `user_passwords` PK `user_id` | `h(user_id)` |
| `sessions` PK | `h(id)` |
| `oauth_accounts` PK `(provider, provider_user_id)` | `h(provider, provider_user_id)` |
| `oauth_states` PK `token` | `h(token)` |
| `service_accounts` PK | `h(id)` |
| `api_keys` PK | `h(id)` |
| `security_events` PK | `h(id)` |
| `invitations` PK | `h(id)` |
| `user_identifiers` PK | `h(id)` |
| `authentication_grants` PK | `h(id)` |
| `challenges` UNIQUE `(subject_key, purpose)` | `h(subject_key, purpose)` |
| `contact_changes` UNIQUE `(user_id, kind)` | `h(user_id, kind)` |

### 5.9 Constraints that do NOT exist (audited, deliberately unenforced)

- `service_accounts.name` — no unique index in either dialect. No claim.
- `user_passwords`, `oauth_accounts`, `sessions`, `security_events` — no unique
  index beyond the ones above.
- Every `CHECK` (`users.status`, `user_identifiers.kind`, `contact_changes.kind`)
  and every "references … by convention (no enforced FK)" note: Firestore has
  neither, and the SQL tree does not rely on the FK either. The domain
  vocabularies (`user.ParseStatus`, `identifier.Kind.Valid`) are the gate, exactly
  as they are for a turso host that has not applied 0014.

### 5.10 The two replacement-keyed PKs — DECISION: no id claim

`challenges` and `contact_changes` are keyed here by their unique REPLACEMENT
tuple, not by their SQL `id` PRIMARY KEY. The authorization train faced the same
question for `iam_relationships` and answered it with an id claim
(`iam_relationship_ids`). **Here the answer is NO**, for reasons that are
specific and worth stating so a later reader does not read it as an omission:

1. **No port method addresses either row by id.** `Replace`, `ConsumeCode`,
   `ConsumeToken`, `PurgeExpired`, `Create`, and `Consume` key on the subject
   tuple or on the digest. Authorization's `relationship_id`, by contrast, is the
   LIST cursor's PK — a duplicate there could make a keyset page repeat or skip a
   row. Neither of these collections has a paged list at all.
2. **Neither id is a claim key or a foreign key.** The digest claim points at the
   owning DOCUMENT, so nothing dereferences the surrogate id.
3. The id IS returned (`Challenge.ID`, `Consumed.ID`, `PendingChange.ID`) and is
   still stored and round-tripped verbatim.

Residual difference, stated rather than hidden: in SQL, a host that supplies the
SAME explicit id for two challenges of different subjects gets a PK violation on
the second; here it gets two rows. Every bundled path either mints the id
(`cryptids`, `firestoredb.NewID`) or supplies a unique one, so the divergence
needs a host deliberately reusing ids. If a future port ever reads a challenge or
a pending change BY ID, this decision must be revisited and an id claim added —
the same reasoning, with the premise changed.

## 6. The directory projection (N-D3)

### 6.1 What it is

`user.Summary` carries `PrimaryEmail` (the normalized value of the user's ACTIVE
PRIMARY **email** identifier) and `EmailVerified` (whether that same identifier
is proven — `verified_at` non-null). Both SQL adapters resolve them with a LEFT
JOIN whose predicate matches `idx_user_identifiers_primary`, in the SAME
statement as the page.

Firestore has no join. Reading one identifier per listed user would make the
operator directory O(page) round trips, so the two values are PERSISTED on the
users document (`primary_email`, `email_verified`) and `UserAdmin.List` /
`GetSummary` answer from the one query they already issue. **`user_identifiers`
remains authoritative**; this is a projection, and its correctness is a
transactional obligation of every writer that can change it.

Empty `primary_email` means "no active primary email on file" — the same fact the
SQL LEFT JOIN reports as NULL — and `email_verified` is false whenever
`primary_email` is empty.

### 6.2 The invariant that keeps it honest

**The users document is never written with a whole-document `Set` built from a
domain `user.User`.** `user.User` has no email fields, so such a write would
silently erase the projection and the directory would start answering blanks. All
non-identifier writers (`Users.Update`, `UserAdmin.SetStatus`, every
`auth_revision` bump) use FIELD updates. Only the paths in §6.3 touch the two
projection fields, and they recompute rather than assume.

### 6.3 Every writer — the complete list N2c implements

| # | Path | Effect on the projection |
|---|---|---|
| 1 | `Users.CreateWithPrimaryIdentifier` | set from the first identifier when it is `kind == email`, primary, and active; otherwise written EMPTY (never absent) |
| 2 | `Identifiers.ApplyVerifiedChange` — pure add | set when the new row is a primary email; else unchanged |
| 3 | `Identifiers.ApplyVerifiedChange` — replacement (`ReplacesIdentifierID`) | recompute: the retired row may have BEEN the projected address |
| 4 | `Identifiers.ApplyVerifiedChange` — primary switch (`MakePrimary`) | the displaced primary is retired; the new row becomes the projection |
| 5 | `Identifiers.ApplyVerifiedChange` — verification | `email_verified` follows the new row's `verified_at` |
| 6 | `CredentialMutations.Apply` / `RetireIdentifier` | **BUILT (N2b)**; clear when the retired row was the projected primary email and no replacement is named |
| 7 | `CredentialMutations.Apply` / `RetireIdentifier` with `ReplacementPrimaryID` | **BUILT (N2b)**; the promoted row becomes the projection (its own `verified_at` decides the flag) |
| 8 | `CredentialMutations.Apply` / `ChangeIdentifierUses` with `MakePrimary` | **BUILT (N2b)**; promotion demotes the current primary of that kind → recompute |
| 9 | `CredentialMutations.Apply` / `RemovePassword`, `UnlinkOAuth` | **BUILT (N2b)**; NO projection change (listed so the enumeration is complete rather than silent) |
| 10 | `Passwordless.Redeem` — provision | **BUILT (N4d)**; the new user's verified primary identifier IS the projection, resolved from the row about to be written and stamped by `putUser` |
| 11 | `Passwordless.Redeem` — adopt | **BUILT (N4d)**; adoption changes neither the row's kind, nor its value, nor its primary flag, so the ONLY reachable change is `email_verified` flipping true on an active primary email — every other shape rewrites the user's STORED pair unchanged |
| 12 | `Passwordless.Redeem` — login | **BUILT (N4d)**; NO projection change, and no revision bump either — a login writes exactly the session and its claim |
| 13 | `PasswordResets.Redeem` | NO projection change (no identifier is touched) |
| 14 | `UserAdmin.SetStatus` | **BUILT (N2b)**; writes `status`, `status_changed_at`, `updated_at`, `auth_revision` — all Summary fields — and MUST NOT disturb the two projection fields (§6.2) |
| 15 | `Users.Update` | writes `display_name`, `updated_at` — same rule |

Rows 1–8 and 10–11 are the ones that WRITE the projection; 9, 12, 13 are audited
no-ops (row 12 is audited, not assumed: the login branch touches no identifier,
so recomputing would be the only way to get it wrong); 14 and 15 are the whole-document-Set hazard. N2c proves the update
semantics AND the absence semantics (no active primary email → empty, verified
false), and that a page costs no per-user identifier read.

## 7. The query matrix

The manifest's specification is the COMPLETE supported query matrix (ruling R5).
This section is the prose half; the EXECUTABLE half is `queryMatrix()` in
`indexes_test.go`, which §8 derives `firestore.indexes.json` from and checks both
ways. A query that is not a row here is a query with no index, and an index no
row requires fails the build.

Every shape below is BUILT and covered by a matrix row as of N5.

| Collection | Filters | Order | Notes |
|---|---|---|---|
| `users` | — | `(created_at, id)` both directions | `UserAdmin.List` — **BUILT (N2c)**, through the connector `List` helper with `user.OrderFields`/`user.DefaultOrder` and PK `id`; the reverse direction is the `HasPrev` probe's. **N4d adds no shape**: a redemption reaches every users document by id (§7.1) |
| `user_identifiers` | `user_id ==`, `active ==` | `(created_at, id)` ascending | `ListByUser` — **BUILT (N2a)**, exactly one shape: `user_id == AND active == true ORDER BY created_at ASC, id ASC`. It is NOT paged (the port returns a slice), so it needs no reversed direction of its own. Credential `Snapshot` **reuses it verbatim (N2b)** — no new shape. **N4d adds none either**: a redemption resolves the bound address through its authentication CLAIM and never queries identifier text (§7.1) |
| `sessions` | `user_id ==` \| `previous_refresh_token_hash ==` | — | **BUILT (N3a)**, exactly two shapes, both a SINGLE equality with NO order: `user_id ==` (`DeleteByUser`, the N2b lifecycle cascade and N4d's passwordless adoption — all three **BUILT**, all three the same shape) and `previous_refresh_token_hash == … LIMIT 1` (`GetByRefreshHash`'s grace half, under a `ReadSnapshot`). Firestore serves a single-field equality from the automatic index, so NEITHER needs a composite. The CURRENT hash issues no query at all — its claim is the access path (§7.1) |
| `oauth_accounts` | `user_id ==` (+ `provider ==` for Delete) | `(linked_at DESC, provider_user_id DESC)` | **BUILT (N3b)**, two shapes: `ListByUser` = `user_id == ORDER BY linked_at DESC, provider_user_id DESC` (a COMPOSITE — already in the manifest — with no reversed direction, because the port returns a slice, not a page) and `Delete` = `user_id == AND provider ==`, two equality fields and therefore a COMPOSITE of its own — N5 CORRECTED this row: the server MAY merge two automatic single-field indexes, but merging is a documented optimization rather than a guarantee, and the manifest declares what it depends on (§8.1). **N2b adds no shape**: `CredentialMutations.UnlinkOAuth` reuses `Delete`'s query, and `Snapshot` reuses `ListByUser`'s and re-sorts the handful of links by provider IN GO rather than asking Firestore for a `(user_id, provider)` ordering — the SQL adapters' `ORDER BY provider` on a per-user inventory is not worth a composite index of its own |
| `service_accounts` | — | `(created_at, id)` both directions | `List` — **BUILT (N4a)**, one shape: the unfiltered collection ordered `(created_at, id)`, through the connector `List` with `serviceaccount.OrderFields`/`DefaultOrder` and PK `id`. The reverse direction is the `HasPrev` probe's. No `PostFilter`, so a non-blank `Search` is `sdk.ErrInvalidInput` |
| `api_keys` | `service_account_id ==` | `(created_at, id)` both directions | `ListByServiceAccount` — **BUILT (N4a)**, ONE query shape in both directions: `service_account_id == … ORDER BY created_at, id`. `req.Search` adds NO query shape — it is a client-side `PostFilter` from `firestoredb.SearchFilter(apikey.SearchFields, …)` applied while page-filling, which is exactly why R4 restricts it to this parent-scoped list. `GetByHash` issues no query (§7.1) |
| `security_events` | any subset of `user_id`, `event_type`, `event_status` × `created_at` range | `(created_at, id)` both directions | `List` — **BUILT (N4a)**; the widest set in the store, enumerated exhaustively in §7.3. The range field IS the leading order field, so a subset costs one composite per direction and not two |
| `invitations` | `resource_key ==` \| `subject_key ==` \| `resolved_subject_id ==` | `(created_at, id)` both directions | **BUILT (N4b)**, exactly two shapes, both through the connector `List` helper with `invitation.OrderFields`/`DefaultOrder` and PK `id`: `resource_key == ORDER BY created_at, id` and `subject_key == ORDER BY created_at, id`, each in BOTH directions (the reverse is the `HasPrev` probe's) — the four composites the manifest already carries. `resolved_subject_id ==` is NOT issued by any port today (the pocket drives resolve-on-registration through `ListBySubject`), and N5 RULED it out of the matrix: it is neither a composite nor a declared single-field dependency, because the manifest declares what the store queries and nothing else. A port that starts filtering on it adds a matrix row, which adds its index. Neither list declares a `PostFilter`, so a non-blank `Search` is `sdk.ErrInvalidInput` (R4) |
| `challenges` | `expires_at <=` (purge); `user_id ==` + `purpose in` (reset/adoption revocation) | `expires_at`, then `id` | **BUILT (N4c)**, exactly two shapes. `PurgeExpired` is `expires_at <= before ORDER BY expires_at ASC, id ASC [LIMIT n]` — the two-field COMPOSITE the manifest already carries, one direction only (the port returns a count, not a page), and the query runs INSIDE the purge's transaction so its candidates are the contention set. The revocation cascade is `user_id == AND purpose in [...]`, two filter fields and therefore a COMPOSITE of its own — N5 CORRECTED this row for the reason the oauth `Delete` row states (§8.1): `in` and `==` select the same index, and index MERGING is an optimization the manifest must not depend on. The `in` list is chunked at 30 (the DNF disjunction cap) even though this pocket's purge sets are two or three purposes. **N4d adds no shape**: passwordless adoption reuses the revocation query |
| `authentication_grants` | `consume_key ==` + `consumed_at == null`; `session_id ==`; `user_id ==` | `(created_at, id)` ascending | `Consume` — **BUILT (N3b)** — is `consume_key == AND consumed_at == null ORDER BY created_at ASC, id ASC LIMIT 1`; `consumed_at == null` is a Firestore IS_NULL filter and the shape needs the four-field COMPOSITE the manifest already carries. `session_id ==` (`DeleteBySession`) is **BUILT (N3b)** and equality-only, so no composite. `user_id ==` (`readGrantsForUser`, the user half of the lifecycle cascade) is **BUILT (N2b)** and equality-only, so no composite; `SetStatus` issues it once plus one `session_id ==` per revoked session. **N4d adds no shape**: passwordless adoption issues `user_id ==` ALONE, because both SQL adapters revoke `WHERE user_id = ?` there — the wider session-linked disjunction is `SetStatus`'s, whose SQL spells it out |

Every direction a store serves — PLUS the reversed direction the List helper's
`HasPrev` probe issues, which needs the same index with all directions flipped —
belongs in the manifest (§8.2). The emulator enforces NONE of it.

### 7.1 The point-read access paths (no index, by construction)

A large part of this store's read surface issues NO QUERY at all, which is a
deliberate design property rather than an omission, and N5 must not go looking
for indexes to cover it. Every one of these is a `Get` on a deterministic
document id:

| Read | Path |
|---|---|
| `Users.Get`, `UserAdmin.GetSummary`, every `auth_revision` CAS | `users/h(user_id)` |
| `Passwords.Get` | `user_passwords/h(user_id)` |
| `Identifiers.Get`, and every retirement's read of the row it retires | `user_identifiers/h(identifier_id)` |
| `Identifiers.GetLogin` / `GetRecovery` | `identifier_claims/h(kind, value)` → the row it names (**two point reads under ONE `ReadSnapshot`**, so the claim and the row agree) |
| the active primary of a `(user, kind)` — the primary switch's demotion target AND the directory projection's only input | `identifier_primaries/h(user_id, kind)` → the row it names |
| `Sessions.Get`, `Rotate`/`ConsumeGrace`'s CAS read, `Delete`'s claim lookup | `sessions/h(session_id)` |
| `Sessions.GetByRefreshHash`, CURRENT slot | `session_refresh_hashes/h(hash)` → the row it names (**two point reads under ONE `ReadSnapshot`**, with the grace QUERY as the fallback in the same snapshot) |
| `OAuthAccounts.GetByProvider` | `oauth_accounts/h(provider, provider_user_id)` |
| `OAuthStates.Consume` | `oauth_states/h(token)` |
| `ActiveSessions.CreateForActiveUser`'s status proof | `users/h(user_id)` — the read that FENCES the mint against a concurrent `SetStatus` |
| `Invitations.Get`, and `UpdateStatus`'s read of the row it transitions | `invitations/h(id)` |
| `Invitations.GetByTokenHash` | `invitation_token_hashes/h(hash)` → the row it names (**two point reads under ONE `ReadSnapshot`**, so a resend cannot report a live invitation as unknown while its token is moving) |
| `Challenges.Replace`'s read of the row it displaces, and `ConsumeCode`'s read | `challenges/h(subject_key, purpose)` |
| `Challenges.ConsumeToken`, and `PasswordResets.Redeem`'s resolution of the reset token | `challenge_digests/h(purpose, digest)` → the row it names (the claim carries the row's DOCUMENT id, because this collection is keyed by its replacement tuple rather than by its surrogate id — §5.10) |
| `Passwordless.Redeem`'s token resolution | `challenge_digests/h(purpose, digest)` → the row it names — the SAME two point reads, and the two documents whose intersection makes two concurrent redemptions serialize (the winner deletes both; the loser aborts, re-runs and finds them gone) |
| `Passwordless.Redeem`'s decision — which subject, if any, owns the bound address | `identifier_claims/h(kind, value)` → the row it names, then `users/h(user_id)`. **No `ReadSnapshot`**: this already runs inside the redemption's read-write transaction, which is one snapshot AND takes read locks — the seam `ReadSnapshot` would provide is already held |
| `ContactChanges.Create` / `Consume` | `contact_changes/h(user_id, kind)` |
| `ServiceAccounts.Get`, and `Delete`'s existence read | `service_accounts/h(id)` |
| `APIKeys.GetByHash` | `api_key_hashes/h(key_hash)` → the row it names (**two point reads, NO snapshot**: the pair is written in one transaction and neither document is ever deleted or re-pointed, so a visible claim proves a committed row — unlike the identifier and session claims, which MOVE and therefore need one) |

This is why the CLAIM documents are described as access paths and not only as
constraints (§5.1, §5.2): resolving an address through its claim keeps the lookup
off an equality filter on unbounded address text, whose index entry truncates
past 1500 bytes (§4.2), AND keeps the composite manifest smaller than the SQL
index list would suggest.

Two shapes deliberately do NOT appear above and must not appear at N5 either:

- **No query resolves the directory projection.** `UserAdmin.List` answers
  `PrimaryEmail`/`EmailVerified` from the users document it already read — the
  port forbids one identifier read per user, and `TestDirectoryPageReadsNoIdentifiers`
  counts the document reads to prove it (one query, zero point reads).
- **No query enforces uniqueness.** A claim is taken with `Create`, whose
  precondition the SERVER evaluates at commit; nothing reads a claim to decide
  whether it is free (ruling R3).

### 7.2 Ownership rules (the executable half of §5)

Every collection whose uniqueness is carried by a claim document OR by a derived
document id has exactly one owner file, and `ownership_test.go` fails the build
if any other non-test file so much as names the collection. The rules are
hermetic (no emulator) and match on identifier boundaries, so a fragment of a
longer name — `oauth_accounts.provider_email_verified` is not the users
document's `email_verified` projection — is not a violation.

| Owner file | Owns | Writers |
|---|---|---|
| `users_doc.go` | `users` | `putUser` (the ONLY whole-document write; takes the projection explicitly), `updateUserProfile`, `advanceUserRevision`, `transitionUserStatus` (N2b — the lifecycle fields, never the projection) |
| `identifiers_doc.go` | `user_identifiers`, `identifier_claims`, `identifier_primaries` | `putIdentifier`, `updateIdentifier` |
| `passwords_doc.go` | `user_passwords` | `putPassword`, `dropPassword` (N2b) |
| `projection.go` | the two projection FIELD names, in both spellings | `resolveEmailProjection` and its `apply`/`updates`/`fill` |
| `sessions_doc.go` (N3a) | `sessions`, `session_refresh_hashes` | `putSession`, `updateSession`, `dropSession`, `dropSessionsForUser` |
| `oauth_doc.go` (N3b) | `oauth_accounts`, `oauth_states` | `putOAuthAccount`, `dropOAuthAccounts`, `putOAuthState`, `dropOAuthState` |
| `grants_doc.go` (N3b) | `authentication_grants` | `putAuthGrant`, `spendAuthGrant`, `dropAuthGrants`; `readGrantsForRevocation` (N2b) is its read half |
| `invitations_doc.go` (N4b) | `invitations`, `invitation_token_hashes`, `invitation_pending` | `putInvitation`, `updateInvitation` |
| `challenges_doc.go` (N4c) | `challenges`, `challenge_digests` | `putChallenge`, `updateChallengeAttempts`, `dropChallenge`/`dropChallenges` |
| `contactchanges_doc.go` (N4c) | `contact_changes` | `putContactChange`, `dropContactChange` |
| `serviceaccounts_doc.go` (N4a) | `service_accounts` | `putServiceAccount`, `updateServiceAccountProfile`, `dropServiceAccount` |
| `apikeys_doc.go` (N4a) | `api_keys`, `api_key_hashes` | `putAPIKey` (the only claim writer), `revokeAPIKey`, `touchAPIKey` — and NO drop helper, because the claim is never released (§5.4) |
| `securityevents_doc.go` (N4a) | `security_events` | `putSecurityEvent` — the ONLY writer; the rail is append-only in the store as well as in the port |

The revocation helpers (`dropSessionsForUser`, `dropAuthGrants`,
`dropChallenges`) take ALREADY-READ documents and read nothing, which is what
lets N2b's `SetStatus`, N4c's `PasswordResets.Redeem` and N4d's passwordless
adoption finish a multi-collection read phase before they write — the vendor
refuses any read issued after a transaction's first write.

`passwordresets.go` and `passwordless.go` own NO collection: they are
COMPOSITIONS, and each reaches every foreign collection through that
collection's owner pair. `Passwordless.Redeem` composes seven of them —
`putUser`/`advanceUserRevision`, `putIdentifier`/`updateIdentifier`,
`dropPassword`, `putSession`/`readSessionsForUser`/`dropSessionsForUser`,
`readGrantsForUser`/`dropAuthGrants`, and the challenge pair — which is the
largest read/write set in the store and exactly why the rule is executable
rather than advisory: a composition composes owners, it never bypasses them.

### 7.3 `security_events` — the complete composite enumeration (N4a)

`SecurityEventRepository.List` takes a `ListFilter` whose three equalities are
each independently optional, plus a half-open `created_at` window, and pages in
either direction. Every combination is a legal call, so every combination is a
query shape N5's manifest must carry — the emulator enforces none of them, and
the shape a host discovers as a production `FAILED_PRECONDITION` is whichever one
its first operator filter happens to use.

The full set is EIGHT equality subsets × ONE range field × TWO directions = **16
composites**, listed here so N5 derives rather than guesses:

| # | Equality prefix (all `==`) | Index fields |
|---|---|---|
| 1 | — | `created_at`, `id` |
| 2 | `user_id` | `user_id`, `created_at`, `id` |
| 3 | `event_type` | `event_type`, `created_at`, `id` |
| 4 | `event_status` | `event_status`, `created_at`, `id` |
| 5 | `user_id`, `event_type` | `user_id`, `event_type`, `created_at`, `id` |
| 6 | `user_id`, `event_status` | `user_id`, `event_status`, `created_at`, `id` |
| 7 | `event_type`, `event_status` | `event_type`, `event_status`, `created_at`, `id` |
| 8 | `user_id`, `event_type`, `event_status` | `user_id`, `event_type`, `event_status`, `created_at`, `id` |

Each row appears TWICE in the manifest: once with `created_at` and `id` both
ASCENDING and once with both DESCENDING. The equality fields are ASCENDING in
both (an equality clause is direction-free); the DESCENDING variant is the
default order `created_at DESC, id DESC`, and the ASCENDING variant serves BOTH
an explicit ascending request AND the `HasPrev` reverse probe of a descending
page.

Three facts keep this from being larger than it looks:

- **The range field is the leading order field.** `Since`/`Until` constrain
  `created_at`, which the order already leads with, so a window adds no field and
  no row to the table above.
- **Firestore's field order within an index is `equalities → range/order`**, so
  the eight prefixes are genuinely eight indexes rather than one per permutation.
- **Nothing else in this collection queries.** There is no `GetByID` query
  (the document id is `h(id)`), no claim, and no search.

`service_accounts` and `api_keys` contribute the two pairs the manifest already
carries — `(created_at, id)` and `(service_account_id, created_at, id)`, each in
both directions — and `req.Search` contributes NONE, because it is evaluated in
Go over the parent-scoped page fill (R4).

## 8. Index manifest (N5)

`firestore.indexes.json` declares **32 composite indexes** — 16 on
`security_events`, 4 on `invitations`, 2 each on `users`, `service_accounts`,
`api_keys` and `oauth_accounts`, 2 on `challenges`, 1 each on `user_identifiers`
and `authentication_grants` — all at `COLLECTION` scope (every collection is
top-level), plus **53 field overrides** (§8.4): 36 single-field DEPENDENCIES and,
since N7, 17 EXEMPTIONS. It is no longer provisional: every entry is derived from
§7 by the rules below, and `indexes_test.go` fails if the two disagree in either
direction.

The other ten of this store's twenty collections carry NO COMPOSITE at all,
because they answer point reads only (§7.1): `user_passwords`, `oauth_states`,
`contact_changes`, and the seven claim collections. (Two of them do appear in the
field overrides — `user_passwords.hash` and `oauth_states.payload` are exempted
from automatic single-field indexing, §8.4 — which is the opposite kind of entry:
an index switched OFF, not one required.) That is the design paying off — a claim
resolves an address by document id, so unbounded address text is never an indexed
filter value (§4.2) and the manifest is far smaller than the SQL adapters' index
list.

**The count against the cap.** A database allows 200 composite indexes without
billing enabled (1,000 with it), shared across every store a host mounts. This
store's 32 is its share; the authorization store's is 28. A host that mounts both
pockets spends 60 and keeps **140** for its own collections.
`TestIndexManifestParses` states both numbers (`indexManifestCount`,
`compositeBudget`) so a change has to move a number a reviewer can see.

**The SECOND cap, added at N7.** Field configurations have their own per-database
limit of 200, and it is spent by field overrides rather than by composites. This
store's 53 is its share, and `singleFieldConfigBudget` in `indexes_test.go`
asserts the manifest stays under it for the same reason `compositeBudget` does:
the limit is shared with every other store the host mounts, and discovering it at
deployment time is discovering it late.

The provisional N1 manifest carried 16 composites and no field overrides. N5
ADDED 16 and REMOVED or RESPELLED none: **14** security-events entries (N1 shipped
only the `user_id` subset in both directions; §7.3 had projected all eight
subsets, and this is where the projection became the file), plus the
`(user_id, provider)` oauth delete and the `(user_id, purpose)` challenge
revocation that rule 2 turned from "no composite" into two. The 36 dependency
field overrides were N5's; the 17 exemptions are N7's. N7 changed NO composite:
the `session_id in [...]` chunk it added to the revocation cascade
(`readGrantsForRevocation`) is a single-field `in`, which selects the same index
as the `==` shape already in the matrix.

### 8.1 The derivation rules, with sources

From [index-overview](https://firebase.google.com/docs/firestore/query-data/index-overview)
and [queries](https://firebase.google.com/docs/firestore/query-data/queries):

1. **A single filter field needs nothing.** "You can combine constraints with a
   logical AND by chaining multiple equality operators (`==` or
   `array-contains`). However, you must create a composite index to combine
   equality operators with the inequality operators, `<`, `<=`, `>`, and `!=`." A
   single `in` likewise: "You can also create `in` and compound equality (`==`)
   queries" appears under *Queries supported by single-field indexes*.
2. **`in` is an equality for index selection** — "`in` and `==` clauses use the
   same index". Rule 1 is therefore narrowed to a SINGLE filter field: this store
   declares a composite for **any query with two or more filter fields**, `in` or
   not. The server may serve multi-equality queries by MERGING automatic
   single-field indexes, but merging is an optimization the docs recommend, not a
   guarantee, and the cost asymmetry is the whole argument (a surplus index costs
   storage; a missing one is a production `FAILED_PRECONDITION` on a request no
   emulator run would have failed). This is the rule that corrected two §7 rows:
   the oauth `Delete` (`user_id ==` + `provider ==`) and the challenge revocation
   cascade (`user_id ==` + `purpose in`). It matches the authorization store's
   post-A7 rule exactly, so the two manifests are derived by one rule.
3. **Filter + sort on another field, or any two-field sort, needs one.** "If you
   need to run a compound query that uses a range comparison … or if you need to
   sort by a different field, you must create a manual index for that query."
   This is why the two UNFILTERED lists (`users`, `service_accounts`) still take
   composites: their sort is `(created_at, id)`, two fields.
4. **Field order.** "The start position is prefixed with the query's equality
   filters and ends with the range and inequality filters on the first `orderBy`
   field" — equality-class fields first, then the range/order fields in the
   query's direction. Firestore accepts any order WITHIN the equality prefix, so
   the manifest pins one (`equalityPrecedence` in `indexes_test.go`, which is the
   order the store's query builders apply their filters in) and every shape obeys
   it, or two spellings of one index would both have to be deployed.
5. **A range on the leading order field adds nothing.** `security_events`'
   `Since`/`Until` and `PurgeExpired`'s `expires_at <= before` constrain the
   field the sort already leads with, which is also what Firestore REQUIRES
   ("your first ordering must be on the same field as the inequality"). So the
   time window costs no index field and no extra entry — the eight equality
   subsets are eight indexes, not sixteen (§7.3).
6. **`__name__` is implicit and unused here.** "By default, the `__name__` field
   is sorted in the same direction of the last sorted field in the index
   definition." No shape in this store orders by `__name__` at all: every paged
   list pins the DOMAIN `id` field as its PK (`ListQuery.PK`), so the cursor a
   caller receives is the domain id rather than the document-name hash, and both
   sort clauses are real index fields. The `__name__` rule stays in
   `requiredIndex` because the connector's `List` orders by `__name__` for any
   `ListQuery` that leaves `PK` empty.
7. **An aggregation uses the index of the query underneath it.** `WithCount`
   counts the ORDERED query (connector `List.count`), and the search count
   iterates that same population, so neither adds an entry.

### 8.2 Both directions are listed — Firestore has no automatic reverse index

Every PAGED list in §7 is served in both directions: the port's order is
caller-supplied (`created_at ASC` or `DESC`), and the connector List's HasPrev
probe re-issues the page query with EVERY direction flipped
(`ListQuery.ordered(…, reverse: true)`). The vendor does not derive one from the
other:

> To run the same queries but with a descending sort order, you need an
> additional index in the descending direction for `population`.
> — [index-overview](https://firebase.google.com/docs/firestore/query-data/index-overview),
> *Queries supported by manual indexes*

So each paged shape contributes an all-ASCENDING entry and an all-DESCENDING one
(the equality prefix stays `ASCENDING` in both — the prefix's mode is free for
equality fields, so pinning it keeps the two entries comparable). **The ruling is
that a reverse index is never automatic and never assumed: both directions are
declared for every paged list, and ONLY for paged lists.**

Four shapes are the exception that states the rule, and each contributes ONE
direction because it has no cursor to page backwards from:

- `Identifiers.ListByUser` (`created_at ASC, id ASC`) — the port returns a slice.
- `OAuthAccounts.ListByUser` (`linked_at DESC, provider_user_id DESC`) — a slice
  again, and the only DESCENDING-led entry in the manifest with no ascending twin.
- `AuthenticationGrants.Consume` (`created_at ASC, id ASC LIMIT 1`) — "oldest
  unspent", one direction by definition.
- `Challenges.PurgeExpired` (`expires_at ASC, id ASC`) — the port returns a
  count.

### 8.3 The entries

| Collection | Fields (all `ASCENDING` unless marked) | Serves |
|---|---|---|
| `users` | `created_at`, `id` — 2 directions | `UserAdmin.List` + its HasPrev probe |
| `service_accounts` | `created_at`, `id` — 2 directions | `ServiceAccounts.List` + probe |
| `api_keys` | `service_account_id`, `created_at`, `id` — 2 directions | `APIKeys.ListByServiceAccount` + probe; the searched page fill scans this same ordering (R4) |
| `invitations` | `resource_key`, `created_at`, `id` — 2 directions | `Invitations.ListByResource` + probe |
| `invitations` | `subject_key`, `created_at`, `id` — 2 directions | `Invitations.ListBySubject` + probe |
| `security_events` | [`user_id`][, `event_type`][, `event_status`], `created_at`, `id` — 8 subsets × 2 directions = **16** | `SecurityEvents.List`, every legal filter combination, with or without the `Since`/`Until` window (§7.3) |
| `user_identifiers` | `user_id`, `active`, `created_at`, `id` | `Identifiers.ListByUser`, and the credential `Snapshot` that reuses it |
| `oauth_accounts` | `user_id`, `linked_at` DESC, `provider_user_id` DESC | `OAuthAccounts.ListByUser`, and `Snapshot`'s link inventory |
| `oauth_accounts` | `user_id`, `provider` | `OAuthAccounts.Delete`, `CredentialMutations.UnlinkOAuth` (rule 2) |
| `challenges` | `expires_at`, `id` | `Challenges.PurgeExpired`, inside the purge's transaction |
| `challenges` | `user_id`, `purpose` | the reset/adoption revocation cascade, chunked at 30 (rule 2) |
| `authentication_grants` | `consume_key`, `consumed_at`, `created_at`, `id` | `AuthenticationGrants.Consume` — `consumed_at == null` is an IS_NULL equality |

FOUR shapes appear in no row at all, because each is a single-field equality that
Firestore's automatic index serves: `sessions` by `user_id` (the revocation
cascade), `sessions` by `previous_refresh_token_hash` (the grace refresh lookup),
`authentication_grants` by `session_id`, and `authentication_grants` by
`user_id`. Their dependencies are declared as field overrides instead (§8.4) —
that is the point of the overrides, not an oversight.

### 8.4 Field overrides — the single-field indexes this store depends on, and the ones it switches OFF

A composite index is not the only index the store needs. The four shapes just
listed derive none at all, and each is served by Firestore's AUTOMATIC
single-field indexing.

Automatic is not the same as guaranteed. A host, or a later manifest of this
store's own, can disable a field's single-field indexes with a `fieldOverride`,
and those queries would then fail with `FAILED_PRECONDITION` in production while
every emulator run stayed green (the emulator enforces no index at all). The
manifest is the specification and `ProbeIndexes` checks only what the manifest
DECLARES (connector C5), so an undeclared dependency is an unchecked one.

So the manifest declares them. The rule is uniform rather than minimal: **every
field any matrix row filters or orders on**, on the collection that queries it —
36 DEPENDENCY entries across the ten queried collections (`authentication_grants` 6,
`security_events` 5, `user_identifiers`/`oauth_accounts`/`invitations`/
`challenges` 4 each, `api_keys` 3, `users`/`service_accounts`/`sessions` 2 each).
`TestCompositeFreeShapesDeclareTheirSingleFieldIndexes` enforces both directions:
a composite-free shape whose field is undeclared fails, and a declared
DEPENDENCY no query uses fails (an EXEMPTION is exempt from that half by
construction — see below — and answers to `TestExemptedFieldsAreNeverQueried`
instead).

Each DEPENDENCY entry asks for ONE index — `ASCENDING` at `COLLECTION` scope.
**Declaring an override REPLACES a field's default set** (ascending + descending +
array-contains). That is deliberate, and the justification is per field class:

| Field class | Fields | Why ASCENDING/COLLECTION alone is enough |
|---|---|---|
| Identity and derived keys | `user_id`, `session_id`, `service_account_id`, `consume_key`, `resource_key`, `subject_key`, `previous_refresh_token_hash`, `provider`, `provider_user_id`, `purpose`, `event_type`, `event_status`, `id` | Only ever `==`, `in`, or a sort TIEBREAK inside a composite. A tiebreak's direction lives in the composite entry, never in a single-field index. None is an array. |
| Booleans | `active` | Only ever `== true`, and only inside the `user_identifiers` composite. |
| Timestamps | `created_at`, `linked_at`, `expires_at` | Every ordering that uses one is a two-field sort and therefore a composite (rule 3); no query sorts a timestamp alone, in either direction. The `>=`/`<`/`<=` windows ride the composite's leading field. |
| Nullable timestamp | `consumed_at` | Only ever `== null`, inside the grant composite. Firestore indexes null as a value, so the IS_NULL filter is an ordinary equality. |
| Arrays | *(none)* | No queried collection carries an array field. `security_events.details` and `invitations.metadata` are JSON TEXT since N7 (§3.8), so they are one string each rather than a map whose every subfield was separately indexed — and both are exempted outright below. |

The effect on a deployed database is fewer single-field indexes, not fewer served
queries — and on an append-only rail like `security_events` that is a write-cost
saving as well. A host that adds its OWN queries against these collections must
extend the manifest rather than rely on the defaults.

#### The 17 exemptions (N7) — "never indexed" made true

Until N7 the "never indexed" row of §4.2 described an INTENTION. Firestore
indexes every scalar field of every document by default — ascending, descending
and array-contains — so the phrase was true of this store's queries and false of
the database underneath it, and on this schema the gap is not cosmetic: the
fields concerned are the store's SECRETS and its largest values. An index entry
on a secret is a second copy of it in a structure with its own retention and its
own export path, ordered so that a scan is a prefix walk over hashes and
addresses; every write pays for every entry, on the highest-volume collections
here; and past 1500 bytes the entry is a TRUNCATED copy (§4.2) rather than a
correct one.

So each of these carries a `fieldOverride` with an EMPTY index set, which is
Firestore's only way to say "no single-field indexes for this field":

| Collection | Exempted |
|---|---|
| `users` | `display_name` |
| `user_passwords` | `hash` |
| `user_identifiers` | `normalized_value` |
| `sessions` | `refresh_token_hash`, `authentication_methods` |
| `oauth_accounts` | `access_token`, `refresh_token` |
| `oauth_states` | `payload` |
| `service_accounts` | `name` |
| `api_keys` | `name`, `key_prefix`, `key_hash` |
| `security_events` | `details` |
| `invitations` | `token_hash`, `metadata` |
| `challenges` | `secret_digest` |
| `authentication_grants` | `methods` |

`previous_refresh_token_hash` is deliberately NOT among them: it is the one
session hash a query filters on (the grace refresh lookup), which is exactly why
`TestExemptedFieldsAreNeverQueried` checks the exempted set against every matrix
row by name as well as by collection. An exemption is not a performance hint — it
makes the query fail with `FAILED_PRECONDITION` in production while every
emulator run stays green.

The REMAINDER — every other stored field, which keeps the automatic indexes — is
pinned by name in `defaultIndexedFields`, and
`TestEveryStoredFieldIsClassified` derives each collection's field list from the
document struct tags and requires the three sets to partition it. Adding a field
to a document is therefore a decision about its indexing rather than a default
nobody looked at.

**Net effect on a deployed database:** the queried fields carry ONE index each
instead of three, the seventeen above carry none, and every remaining field keeps
Firestore's default set. A host that wants `details` or `metadata` indexed after
all has to change this manifest rather than add a conflicting override —
`IndexManifest.Merge` refuses two definitions of one field with different index
sets (`ErrConflictingFieldOverride`) rather than silently picking a winner.

### 8.5 Export, probe, and what live still owes

`ExportIndexes(dst)` merges this fragment into the host's own manifest (the
connector's `ExportIndexes`: union by index identity, byte-stable, atomic
rename), which is why a host's unrelated indexes survive and a re-export is an
empty diff. The checked-in file is byte-identical to that output
(`TestIndexManifestIsSortedAsMergeSorts`), so it is also directly deployable by
the connector README's `gcloud firestore indexes composite create` loop.

`Repositories` probes the manifest at construction
(`firestoredb.ProbeIndexesFS`) unless `WithoutIndexProbe()` is passed. The
constructor takes no `context.Context` — the SQL siblings' table probes do not
either — so the probe runs on `context.Background()` and is bounded by the
connector's `ProbeTimeout` (30 s), which is what an Admin API that never answers
hits instead of hanging a host's boot. Against the emulator the probe is
`ErrProbeUnavailableOnEmulator` rather than a silent skip
(`indexes_integration_test.go` asserts both sides, including that all eighteen
slots wire under the opt-out).

**Owed, and not satisfiable on an emulator:** `indexes_live_test.go` deploys
nothing but requires this manifest deployed and READY on the target database
(CI: `FIRESTORE_LIVE_INDEXES=pockets/authentication/stores/firestore/firestore.indexes.json`,
wired by N6). It then proves (a) the probe accepts the deployment and the
constructor succeeds with the probe ENABLED, and (b) every matrix row executes
without a `*firestoredb.MissingIndexError`. As of N5 it has NOT run — no live GCP
project existed in that session — so the manifest is derived, reviewed, and
hermetically consistent with the code, but the index set itself is UNPROVEN until
that leg runs. Anything rule 2's conservatism over-declared can only be pruned by
that run, and the security-events sixteen are the entries most worth re-examining
against real operator filter usage.

## 9. Known family differences (R1)

- **No ambient transaction.** This store returns `ErrAmbientTransactionUnsupported`
  from every port method whose context carries a connector transaction. The
  authentication pocket has NO `RunTransactional` conformance family, so R1
  appears here only as that refusal — `portcalls_test.go` drives all 58 methods
  inside both a `Transact` and a `ReadSnapshot`.
- **Committed outcomes versus rollback errors.** The consume family
  (`OAuthStates.Consume`, `AuthenticationGrants.Consume`,
  `ContactChanges.Consume`, `Challenges.ConsumeToken`/`ConsumeCode`) COMMITS its
  deletion or counter update and reports the domain outcome afterwards; only
  `Passwordless.Redeem`'s stable rejection rolls back with nothing written. The
  two policies are deliberate and must not be interchanged (N-D2).
- **The transaction callback may run more than once.** Every attempt-local
  outcome is reset at the top of the callback; nothing outside Firestore happens
  inside one.

The rest of this list was added at N7, when the reviews asked for each difference
to be written down rather than left in a comment.

- **Under EXHAUSTED contention, `sdk.ErrConflict` can escape a port whose
  contract does not name it.** `retryContention` re-runs a losing transaction six
  times with jittered backoff and then returns the mapped conflict. For
  `Passwordless.Redeem` and `Challenges.ConsumeCode` that is a THIRD outcome
  beside the port's own two: not the committed result and not the stable domain
  rejection, but "the store could not decide, and nothing was written". The SQL
  adapters can reach the same place (turso's busy retry, pgx's serialization
  retry) and it is fail-closed either way — nothing is committed, and the caller
  may re-run the whole workflow. The emulator's documented thirty-second lock
  release makes it far likelier there than on a real database, which is why the
  LIVE leg has to measure the real rate rather than infer it from an emulator run
  that never hit it.
- **A lost claim reports a NEUTRAL message.** Firestore evaluates every `Create`
  precondition at commit, so a lost claim arrives as the vendor's AlreadyExists
  naming the losing document — `…/identifier_claims/<64 hex>`, which is this
  store's collection layout plus a SHA-256 fingerprint of the address, token or
  digest the operation was about. `retryTransact` maps it to a store-typed error
  that still matches `sdk.ErrAlreadyExists` and carries neither.
- **`Consumed.ConsumedAt` is FULL precision**, not the microsecond truncation the
  stored timestamps take: the row is being deleted, the value only travels back
  to the caller, and turso returns `now.UTC()` there.
- **An empty binding blob stores NULL**, exactly as the SQL adapters' `nullBlob`
  does for `len(b) == 0`, so a `[]byte{}` and a `nil` binding are one stored fact
  in all three families.
- **The challenge revocation cascade SKIPS an empty `user_id`.**
  `readChallengesForPurposes` reads nothing when the user id is blank, where the
  SQL adapters' `WHERE user_id = ? AND purpose IN (…)` would match rows whose
  `user_id` is the empty string — the magic-link rows the subject key exists for.
  No caller in the pocket reaches it with a blank id (a reset and an adoption both
  resolve a subject first), and matching blank-to-blank would let one anonymous
  flow revoke every other anonymous flow's secrets. Recorded as a difference
  rather than fixed, because the SQL behavior is the accident.
- **`RetireIdentifier`'s `ReplacementPrimaryID` is not checked for ownership**, in
  any family: the SQL adapters promote it with an unguarded `UPDATE … WHERE id =
  ?`. This store follows them, and the divergence is what happens AFTERWARDS —
  the promoted row becomes a candidate for the ACTING user's directory
  projection, so a host that passes another user's identifier can publish that
  address as the acting user's `primary_email`, where the SQL summary's join is
  scoped by `user_id` and would show nothing. The projection is a projection, not
  an authority (§6), and `user_identifiers` still says who owns the row.
- **`Challenges.PurgeExpired` with a non-positive limit is ONE request.** The
  whole purge is a single transaction bounded by the 10 MiB request size and the
  270-second ceiling rather than by a write count, so a large backlog should be
  swept with an explicit limit and repeated calls; an oversized purge fails
  atomically, having deleted nothing. The SQL adapters bound the STATEMENT
  instead, so this is a scheduling difference rather than a semantic one.
