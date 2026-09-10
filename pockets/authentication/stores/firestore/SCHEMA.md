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
| `SetStatus` | users doc, every session of the user (for their hash claims), every grant owned by the user **or bound to those sessions** | status, `status_changed_at`, `updated_at`, `auth_revision + 1`; delete sessions and grants | releases every deleted session's refresh claim |

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

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Create` | token-hash claim, pending-tuple claim (when the stored status is pending) | invitations doc | takes the token claim, and the pending claim while pending |
| `Get` | doc | — | — |
| `GetByTokenHash` | token claim → doc | — | ACCESS PATH |
| `ListByResource` | query `resource_key ==`, order `(created_at, id)` | — | — |
| `ListBySubject` | query `subject_key ==`, order `(created_at, id)` | — | — |
| `UpdateStatus` | doc, plus the OLD and NEW token claims and the pending claim | status, token hash, expiry, accepted_at, resolved subject, updated_at | releases the pending claim when the STORED status leaves pending; re-points the token claim when `TokenHash` changes |

### `challenge.Repository` — 4

| Method | Reads | Writes | Claims |
|---|---|---|---|
| `Replace` | the `(subject_key, purpose)` doc (for the displaced row's digest), the new digest claim | Set the doc | releases the displaced digest claim, takes the new one |
| `ConsumeCode` | the doc | attempt increment / delete on redeem, expiry, or lockout — every outcome COMMITS and is then reported through `ConsumeOutcome` (N-D2) | releases the digest claim with the row |
| `ConsumeToken` | digest claim → doc | delete both; an expired token's deletion COMMITS, then `sdk.ErrExpired` | releases the digest claim |
| `PurgeExpired` | query `expires_at <= before`, ordered and bounded, re-read INSIDE the transaction with each row's digest claim | delete rows + claims | releases every purged row's claim |

### `contactchange.Repository` — 2

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
| `Snapshot` | ONE `ReadSnapshot` over: users doc (`auth_revision`), user_passwords existence, oauth_accounts by user ordered by provider, active identifiers by user | — | — |
| `Apply` | users doc (revision CAS) + the typed mutation's targets: `RemovePassword` → the password doc; `UnlinkOAuth` → the user's link for that provider; `RetireIdentifier` → the retired row, the promoted replacement, their claims, the projection; `ChangeIdentifierUses` → the row, the displaced primary, the auth/primary claims, the projection | exactly the targeted source, `auth_revision + 1` | per mutation — see §6.3 |

### `passwordreset.Repository` — 1

`Redeem`: one transaction — consume the live `(purpose, digest)` challenge
through its digest claim, resolve the user from it, Set the password doc, delete
every session (with its refresh claim), every grant, and every challenge of the
named purposes for that user (with their digest claims). A non-live challenge —
unknown, consumed, or expired — is `sdk.ErrNotFound` with nothing applied.

### `passwordless.Repository` — 1

`Redeem`: N-D2's largest operation, in ONE transaction — consume the token
challenge, decode and validate the versioned binding, re-read the CURRENT active
claim for the bound address, then branch login / adopt / provision. Adoption is
the anti-takeover branch: revoke the pre-proof password, every session and its
refresh claim, every grant, and the named challenge purposes BEFORE inserting the
new session, verify the identifier, maintain claims and the projection, and
advance `auth_revision`. A stable bad outcome is `passwordless.ErrRedemption`
with NOTHING written; an infrastructure error rolls back the token consumption
itself.

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
`created_at`. `details` is a NATIVE map where SQL stores `'{}'` JSON text — the
round-trip contract is uniform (nil or empty in, non-nil empty out) and the
storage shape is each family's choice. Append-only: no update or delete path
exists in the port, so none exists here.

Indexes `idx_security_events_created_at_id`, `_user_id`, `_event_type`,
`_event_status` become the composite matrix of §7.

### 3.9 `invitations` (migrations 0009, 0016)

Columns map one-to-one (`id`, `resource_type`, `resource_id`, `relation`,
`identifier`, `identifier_kind`, `resolved_subject_id`, `invited_by`,
`token_hash`, `auto_accept` bool, `status`, `expires_at`, `accepted_at`
nullable, `created_at`, `updated_at`, `metadata` — a native map where SQL stores
`'{}'`), plus the derived `resource_key` and `subject_key` (§4).

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
| **Never indexed** | `display_name`, `name` (searched in Go under R4), `payload`, the OAuth token ciphertext, `details`, `metadata`, `hash` | no exposure |

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
| 6 | `CredentialMutations.Apply` / `RetireIdentifier` | clear when the retired row was the projected primary email and no replacement is named |
| 7 | `CredentialMutations.Apply` / `RetireIdentifier` with `ReplacementPrimaryID` | the promoted row becomes the projection (its own `verified_at` decides the flag) |
| 8 | `CredentialMutations.Apply` / `ChangeIdentifierUses` with `MakePrimary` | promotion demotes the current primary of that kind → recompute |
| 9 | `CredentialMutations.Apply` / `RemovePassword`, `UnlinkOAuth` | NO projection change (listed so the enumeration is complete rather than silent) |
| 10 | `Passwordless.Redeem` — provision | the new user's verified primary identifier IS the projection |
| 11 | `Passwordless.Redeem` — adopt | the adopted identifier becomes verified; when it is the active primary email, `email_verified` flips true |
| 12 | `Passwordless.Redeem` — login | NO projection change |
| 13 | `PasswordResets.Redeem` | NO projection change (no identifier is touched) |
| 14 | `UserAdmin.SetStatus` | writes `status`, `status_changed_at`, `updated_at`, `auth_revision` — all Summary fields — and MUST NOT disturb the two projection fields (§6.2) |
| 15 | `Users.Update` | writes `display_name`, `updated_at` — same rule |

Rows 1–8 and 10–11 are the ones that WRITE the projection; 9, 12, 13 are audited
no-ops; 14 and 15 are the whole-document-Set hazard. N2c proves the update
semantics AND the absence semantics (no active primary email → empty, verified
false), and that a page costs no per-user identifier read.

## 7. The query matrix (provisional — N5 is the authority)

The manifest's specification is the COMPLETE supported query matrix, derived and
proven live at N5. What the audit already pins:

Rows the implementation has REACHED are marked; the rest are still the audit's
projection of what the unbuilt tasks will issue.

| Collection | Filters | Order | Notes |
|---|---|---|---|
| `users` | — | `(created_at, id)` both directions | `UserAdmin.List` — **BUILT (N2c)**, through the connector `List` helper with `user.OrderFields`/`user.DefaultOrder` and PK `id`; the reverse direction is the `HasPrev` probe's |
| `user_identifiers` | `user_id ==`, `active ==` | `(created_at, id)` ascending | `ListByUser` — **BUILT (N2a)**, exactly one shape: `user_id == AND active == true ORDER BY created_at ASC, id ASC`. It is NOT paged (the port returns a slice), so it needs no reversed direction of its own. Credential `Snapshot` (N2b) reuses it |
| `sessions` | `user_id ==` \| `previous_refresh_token_hash ==` | — | **BUILT (N3a)**, exactly two shapes, both a SINGLE equality with NO order: `user_id ==` (`DeleteByUser`, and the N2b/N4d revocation cascades) and `previous_refresh_token_hash == … LIMIT 1` (`GetByRefreshHash`'s grace half, under a `ReadSnapshot`). Firestore serves a single-field equality from the automatic index, so NEITHER needs a composite. The CURRENT hash issues no query at all — its claim is the access path (§7.1) |
| `oauth_accounts` | `user_id ==` (+ `provider ==` for Delete) | `(linked_at DESC, provider_user_id DESC)` | **BUILT (N3b)**, two shapes: `ListByUser` = `user_id == ORDER BY linked_at DESC, provider_user_id DESC` (a COMPOSITE — already in the manifest — with no reversed direction, because the port returns a slice, not a page) and `Delete` = `user_id == AND provider ==`, equality-only and therefore served without a composite (Firestore merges the two automatic single-field indexes) |
| `service_accounts` | — | `(created_at, id)` both directions | `List` |
| `api_keys` | `service_account_id ==` | `(created_at, id)` both directions | + `PostFilter` search (R4) |
| `security_events` | any subset of `user_id`, `event_type`, `event_status` × `created_at` range | `(created_at, id)` both directions | the widest set: every equality subset × the range × both directions |
| `invitations` | `resource_key ==` \| `subject_key ==` \| `resolved_subject_id ==` | `(created_at, id)` both directions | |
| `challenges` | `expires_at <=` (purge); `user_id ==` + `purpose in` (reset/adoption revocation) | `expires_at`, then `id` | |
| `authentication_grants` | `consume_key ==` + `consumed_at == null`; `session_id ==`; `user_id ==` | `(created_at, id)` ascending | `Consume` — **BUILT (N3b)** — is `consume_key == AND consumed_at == null ORDER BY created_at ASC, id ASC LIMIT 1`; `consumed_at == null` is a Firestore IS_NULL filter and the shape needs the four-field COMPOSITE the manifest already carries. `session_id ==` (`DeleteBySession`) is **BUILT (N3b)** and equality-only, so no composite. `user_id ==` is N2b's half of the lifecycle cascade (`readGrantsForUser`), also equality-only |

Every direction a store serves — PLUS the reversed direction the List helper's
`HasPrev` probe issues, which needs the same index with all directions flipped —
belongs in the manifest. The emulator enforces NONE of it.

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
| `users_doc.go` | `users` | `putUser` (the ONLY whole-document write; takes the projection explicitly), `updateUserProfile`, `advanceUserRevision` |
| `identifiers_doc.go` | `user_identifiers`, `identifier_claims`, `identifier_primaries` | `putIdentifier`, `updateIdentifier` |
| `passwords_doc.go` | `user_passwords` | `putPassword` |
| `projection.go` | the two projection FIELD names, in both spellings | `resolveEmailProjection` and its `apply`/`updates`/`fill` |
| `sessions_doc.go` (N3a) | `sessions`, `session_refresh_hashes` | `putSession`, `updateSession`, `dropSession`, `dropSessionsForUser` |
| `oauth_doc.go` (N3b) | `oauth_accounts`, `oauth_states` | `putOAuthAccount`, `dropOAuthAccounts`, `putOAuthState`, `dropOAuthState` |
| `grants_doc.go` (N3b) | `authentication_grants` | `putAuthGrant`, `spendAuthGrant`, `dropAuthGrants` |

The revocation helpers (`dropSessionsForUser`, `dropAuthGrants`) take
ALREADY-READ documents and read nothing, which is what lets N2b's `SetStatus` and
N4d's adoption finish a multi-collection read phase before they write — the
vendor refuses any read issued after a transaction's first write.

## 8. Index manifest

`firestore.indexes.json` ships PROVISIONAL at N1: 16 composites covering the
shapes above that are already certain. N5 derives the complete set from the
matrix, adds the field overrides this store's single-field dependencies need, and
verifies every row against a real database; `indexes_test.go` will then assert
the correspondence both ways (a query with no index, and an index no query needs,
both fail the build).

Firestore allows 200 composite indexes per database without billing enabled. The
security-events matrix alone is the largest contributor — 8 equality subsets ×
one range × two directions is the upper bound before pruning — and the
`platform-sre` review at N7 checks the total against that cap, shared with
whatever the host and the authorization store deploy.

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
