# Phase 1 — identifier domain and memory contracts

Status: READY after phase 0.
Depends on: all phase-0 tasks.
Design: §§2.1–2.4, 5.5–5.6, 7.

## Goal

Move identity discovery/contact state out of `users` conceptually, define
`user_identifiers`, and prove aggregate behavior in the reference and proof-host
memory implementations before writing SQL.

Implementation cut: to avoid a sibling-domain import cycle,
`IdentifierRepository.ApplyVerifiedChange` accepts an identifier-owned
`ApplyVerifiedChangeInput`; `authsvc` later translates a consumed
`contactchange.PendingChange` into it. This preserves the design's atomic apply
contract without coupling repository packages.

## Task AV3-1.1 — identifier entity, kinds, uses, and normalization

Touch: new `features/authentication/domain/identifier`, current user entity
tests, normalization consumers only where needed to compile.

Implement:

- Identifier fields: ID, UserID, kind, normalized value, `VerifiedAt`, login /
  recovery / notification use flags, primary flag, created/updated/replaced
  timestamps.
- Closed v3 kinds `email` and `phone`; do not use an open arbitrary string at
  persistence boundaries.
- Constructors and transitions that reject authentication use without
  verification except the explicit initial-registration-email state.
- Email addr-spec-only validation, documented domain/IDNA behavior, configurable
  local-part folding with the current lowercase behavior as compatibility
  default. Reject display-name forms.
- Strict E.164 phone normalization with required `+`, no country inference.
- `IdentifierNormalizer` default and custom seam used by both kinds.
- Unit/property-style table tests for whitespace, display forms, Unicode/domain
  cases, invalid E.164, stable idempotent normalization, and no provider-specific
  Gmail-style rewriting.

Verify:

```sh
cd features/authentication && go test ./domain/identifier ./domain/user
make guard
```

## Task AV3-1.2 — reshape user and define atomic repository contracts

Depends on: AV3-1.1.
Touch: user domain/repository, identifier repository, public `Repositories`,
storetest suite/reference, and compile-complete implementations in both bundled
stores and authmem.

Implement:

- Add `AuthRevision` and the new contracts. To keep every task buildable before
  phase 5 re-keys existing services, retain the old email fields and
  `Create/GetByEmail` methods behind conspicuous `Deprecated: auth-v3
  transition` comments. They remain authoritative only for legacy call sites;
  no new v3 code may call them. AV3-5.5 removes them and the phase-5 gate proves
  zero references. This is temporary branch scaffolding, never a release state.
- Add atomic `CreateWithPrimaryIdentifier`, `Get`, and profile `Update`
  contracts alongside that temporary compatibility surface.
- Add `IdentifierRepository.Get`, `GetLogin`, `GetRecovery`, `ListByUser`, and
  revision-CAS `ApplyVerifiedChange` using the identifier-owned input type.
- Write storetest sub-runners first for atomic create rollback, DB-generated IDs,
  active-only reads, login/recovery lookup, multiple identifiers, shared
  notification-only addresses, authentication-claim collision, one active
  primary per user/kind, use/timestamp round trip, replacement history,
  revision conflict, apply-change rollback, and concurrent claim arbitration.
- Implement the reference-memory store with one mutex across user + identifier
  atomic operations and partial-index-equivalent semantics.
- Add correct method implementations to pgx, turso, and authmem in the same
  task so enlarging the existing interface never leaves `make check` red. SQL
  methods may target the phase-2 tables before live execution, but must be real
  implementations—not panics/stubs. Phase 2 adds migrations and proves them
  live.

Do not attempt bidirectional background synchronization. Transitional tests
seed whichever contract they exercise; the phase-5 cut migrates all production
callers and removes the legacy source in one task.

Verify:

```sh
cd features/authentication && go test -race ./storetest ./domain/user ./domain/identifier
make check
```

## Task AV3-1.3 — contact-change flow state

Depends on: AV3-1.1, AV3-0.3.
Touch: new `domain/contactchange`, storetest reference, service fakes.

Implement:

- Pending change fields from design §2.4, including requested use flags,
  primary/replacement intent, expiry, and normalized new value.
- `Create` as atomic replace for `(user, kind)` and atomic single-use `Consume`.
- Expired consume deletes and returns `sdk.ErrExpired`; missing/already consumed
  returns `sdk.ErrNotFound`.
- Storetest cases for replace, single-use concurrency, expiry deletion, missing,
  and byte-exact value/use round trip.
- Reference-memory implementation with one mutex.

Do not put secrets in contact changes and do not turn challenge context into the
pending-value payload.

Verify:

```sh
cd features/authentication && go test -race ./storetest ./domain/contactchange
make check
```

## Task AV3-1.4 — update proof-host memory repositories

Depends on: AV3-1.2, AV3-1.3, AV3-0.3, AV3-0.4.
Touch: `examples/auth-cms/internal/authmem` and its tests.

Finish any contact-change/grant/credential/delivery repository additions not
owned by AV3-1.2, then have authmem run the exported storetest suite instead of
duplicating behavioral tests. Keep it pedagogical: one shared data object/mutex
is preferable to fake cross-repository transactions.

Verify:

```sh
cd examples/auth-cms && go test -race ./internal/authmem/...
cd features/authentication && go test ./storetest
make check
```

## Phase acceptance

- Reference memory and authmem pass identical identifier/contact-change,
  challenge/grant, and credential-mutation conformance.
- All new v3 code uses identifier contracts. Legacy email fields/methods are
  explicitly marked transitional and listed for mandatory AV3-5.5 removal.
- `make check && make guard` green.

## Stop conditions

- Removing user email cannot compile because a prior unfinished milestone still
  owns it: stop and finish/rebase that dependency instead of duplicating state.
- Any memory operation needs two unlocked repository calls to simulate an atomic
  promise: redesign it around the shared data mutex before proceeding.

## Execution log

Append dated entries per completed task.

### 2026-07-12 — AV3-1.1 (identifier entity, kinds, uses, and normalization)

Dependencies: all six phase-0 tasks confirmed complete (checked off in `TASKS.md`
with execution-log entries in `01-security-foundations.md`; phase-0 close gate
green). Worktree changes preserved; no resets. Scope held to the identifier
DOMAIN only — repository contracts are AV3-1.2, contactchange is AV3-1.3.

Files changed:

- `features/authentication/domain/identifier/identifier.go` (new) — the identity-
  discovery domain: closed `Kind` vocabulary (`KindEmail`/`KindPhone`, pinned to
  `identity.KindEmail`/`identity.KindPhone` — no third kind) with `Kind.Valid` and
  a boundary-guarding `ParseKind`; the `Identifier` entity (ID, UserID, Kind,
  NormalizedValue, `VerifiedAt` zero=unverified, login/recovery/notification use
  flags, `IsPrimary`, created/updated/`ReplacedAt` zero=active); a `Uses`
  role-flag value with `requiresVerification` (login OR recovery); the `New`
  constructor (normalizes, rejects login/recovery use on an unverified identifier)
  and `NewRegistrationEmail` (the single §2.3 exception: unverified email carrying
  login+recovery+notification+primary while its registration challenge is
  pending); transitions `Verify`, `Retire` (history-preserving `replaced_at`, not
  a hard delete), `SetUses` (re-enforces the verification invariant), `MakePrimary`;
  predicates `Verified`/`Active`/`CurrentUses`. Stable errors `ErrUnknownKind`
  (wraps `sdk.ErrInvalidInput`) and `ErrVerificationRequired` (wraps
  `sdk.ErrConflict`).
- `features/authentication/domain/identifier/normalize.go` (new) — the `Normalizer`
  seam (method set `Normalize(kind, value string) (string, error)`, structurally
  identical to the public `authentication.IdentifierNormalizer` so one value
  satisfies both) and the bundled strict `DefaultNormalizer`. Email: addr-spec
  only (display-name AND empty-name angle-addr forms rejected), whitespace
  trimmed, domain lowercased, local part folded by default with
  `PreserveLocalPartCase` opt-out (zero value = v1-compatible whole-address
  lowercasing), NO provider-specific (Gmail dot/`+tag`) rewriting. Phone: strict
  naive E.164 (`^\+[1-9][0-9]{1,14}$`), visual separators (space, NBSP, `-`, `(`,
  `)`, `.`) stripped, leading `+` required, no country inference.
- `features/authentication/domain/identifier/identifier_test.go` (new) — table
  tests: kind validity/closed-vocabulary rejection and identity-name pinning;
  email whitespace/display-form/angle-addr/unicode-local/unicode-domain-lowercase/
  no-gmail-rewrite/missing-parts; local-part folding config (default folds,
  preserve keeps case); phone canonical/separator-strip/no-inference/leading-zero/
  letters/length/plus-only; cross-kind idempotence; unknown-kind normalization;
  `New` field/normalization wiring; unverified login/recovery rejection +
  notification-only-unverified permitted; the registration-email exception
  (guarded that plain `New` refuses the same unverified login/recovery request);
  `SetUses` verify-then-enable; `Retire` marks replaced not deleted.

Commands / results:

- `cd features/authentication && go test ./domain/identifier ./domain/user` —
  **PASS** (identifier suite green; user has no test files — unchanged).
- `make guard` — **PASS** (exit 0; all guards, sdk stays stdlib-only, feature
  core still requires sdk only).
- Hygiene (not a task verify command): `go vet ./domain/identifier` and
  `go build ./...` in the feature — clean.

Premise adaptations:

- **Documented IDNA policy is stdlib-only Unicode case folding, not ToASCII.**
  §2.2 asks the email domain be "canonicalized through a documented IDNA policy."
  Go's stdlib has no IDNA implementation (it lives in `golang.org/x/net/idna`),
  and the feature core requires sdk only (FS1) — it may not import x/net. The
  bundled `DefaultNormalizer` therefore lowercases the domain (Unicode-aware
  `strings.ToLower`) and does NOT perform punycode/IDNA mapping; the policy and
  its limit are documented on `DefaultNormalizer`, and a host needing full IDNA
  ToASCII injects a custom `Normalizer` (the design's stated seam). No guard or
  boundary is weakened.
- **`ErrVerificationRequired` wraps `sdk.ErrConflict`, not `ErrInvalidInput`.** A
  malformed VALUE (normalizer failure, unknown kind) is `sdk.ErrInvalidInput`; an
  attempt to enable login/recovery on an unverified identifier is a STATE
  conflict, so it wraps `sdk.ErrConflict` to let a service map the two distinctly.
- **`domain/user` left untouched.** The task's touch list allows the user entity
  tests "where needed to compile"; adding a sibling domain does not break user, so
  the surgical diff adds only the new package. Reshaping user (removing email,
  the atomic repository contracts) is explicitly AV3-1.2.
- **Two constructors instead of one god-constructor.** `New` (general, enforces
  the invariant) plus `NewRegistrationEmail` (the exception) honestly encode the
  §2.3 rule at the type boundary; the identifier domain deliberately does NOT
  import `domain/credential` (which would create a cycle when AV3-1.2 has
  credential project real identifiers), so it defines its own plain-bool use flags
  rather than reusing `credential.IdentifierUses`.

### 2026-07-12 — AV3-1.2 (reshape user and define atomic repository contracts)

Dependencies: AV3-1.1 complete and checked off (identifier domain public);
phase-0 fully closed. Worktree changes preserved; no resets.

Files changed:

- `features/authentication/domain/user/user.go` — added `AuthRevision int64`
  (the design §2.1/§5.6 optimistic anchor) to `User`; marked `Email`/`EmailVerified`
  as `Deprecated: auth-v3 transition` (removed in AV3-5.5).
- `features/authentication/domain/user/repository.go` — extended
  `UserRepository` with the atomic `CreateWithPrimaryIdentifier(ctx, User,
  identifier.Identifier) (User, identifier.Identifier, error)`; retained
  `Get`/`Update` and marked `Create`/`GetByEmail` `Deprecated: auth-v3 transition`.
  New `user → identifier` import edge (acyclic: identifier imports only sdk).
- `features/authentication/domain/identifier/repository.go` (new) —
  `IdentifierRepository` (`Get`, `GetLogin`, `GetRecovery`, `ListByUser`,
  revision-CAS `ApplyVerifiedChange`) and the identifier-owned
  `ApplyVerifiedChangeInput` (the phase-1 cut that keeps the repo from importing
  contactchange).
- `features/authentication/authentication.go` — added the `Identifiers`
  slot to `Repositories` (frozen, nil-tolerated until phase 5).
- `features/authentication/storetest/storetest.go` — new `UserIdentifiers`
  group (skip-loud when nil) with the 13 sub-runners the task enumerates
  (atomic create + DB-generated IDs, create rollback on lost claim, active-only
  reads, login/recovery lookup, multiple identifiers, shared notification-only
  address, authentication-claim collision, one active primary per user/kind,
  apply use/timestamp round trip, replacement history, revision conflict,
  apply-change rollback, concurrent claim arbitration) plus seed helpers.
- `features/authentication/storetest/reference_test.go` — `refIdentifiers`
  over a new `userIdentifiers` map (real identifier rows) + `refUsers.CreateWithPrimaryIdentifier`,
  all under the single shared reference mutex; the identifier `ApplyVerifiedChange`
  shares the phase-0 `authRevisions` anchor (one auth_revision per user).
- `features/authentication/stores/pgx/{users.go,identifiers.go,postgres.go}`,
  `features/authentication/stores/turso/{users.go,identifiers.go,turso.go}` —
  real transactional implementations (InTx: user+identifier atomic create; CAS
  on `users.auth_revision` FOR UPDATE, retire replaced/displaced-primary rows,
  claim insert, revision bump) targeting the phase-2 `user_identifiers` table and
  `users.auth_revision` column; `Identifiers` wired into both `Repositories`.
- `examples/auth-cms/internal/authmem/authmem.go` — `identifierRepo` +
  `userRepo.CreateWithPrimaryIdentifier` over a new `identifiers` map, sharing
  the store mutex; auth_revision rides the user row; `Identifiers` wired.
- Test doubles extended to keep the enlarged interface buildable:
  `internal/logic/authsvc/service_test.go` (`*fakeUsers`) and
  `internal/inbound/authentication/helpers_test.go` (`*memUsers`).

Commands / results:

- `cd features/authentication && go test -race -count=1 ./storetest ./domain/user
  ./domain/identifier` — **PASS** (storetest reference green incl. the new
  `UserIdentifiers` group; domain/identifier green; domain/user has no test files).
- `make check` — **PASS** (`all checks passed`; per-module build/vet/test across
  every module, including `examples/auth-cms/internal/authmem` running the
  exported storetest suite — so authmem is the second hermetic conformance target
  for the new group; pgx/turso live conformance legs skip without a DSN, recorded).
- `make guard` — **PASS** (exit 0; feature core still requires sdk only, stores
  import no foreign feature/examples).

Premise adaptations:

- **`ApplyVerifiedChange` takes the identifier-owned `ApplyVerifiedChangeInput`,
  not `contactchange.PendingChange`** — exactly the phase-file implementation cut
  (avoids the sibling-domain import cycle). authsvc translates a consumed
  `contactchange.PendingChange` into it at confirm time (AV3-1.3/phase 5).
- **Single auth_revision anchor, credential projection untouched.** The reference
  shares the phase-0 `authRevisions` map between credential mutations and the new
  identifier `ApplyVerifiedChange` (design §5.6 "users.auth_revision supplies
  optimistic serialization" — one anchor). The phase-0 credential-projection
  stand-in (`identifiers map[string][]credential.IdentifierMethod`, used by
  `refCredentialMutations` and the crown-jewel self-removal test) is left as-is;
  wiring the credential `Snapshot` to project from the real identifier rows is the
  phase-6 credential-suite re-key, not a drive-by refactor here. The new real rows
  live in a separate `userIdentifiers`/authmem `identifiers` map.
- **SQL targets phase-2 schema.** pgx/turso `CreateWithPrimaryIdentifier` and
  `IdentifierStore` are real transactional implementations against the phase-2
  `user_identifiers` table and `users.auth_revision` column (no migration yet;
  AV3-2.1 adds it and AV3-2.4 proves both dialects live). The deprecated
  `Create`/`Get`/`GetByEmail`/`Update` SQL is left untouched (phase-5 removal).
- **`MakePrimary` retires the displaced same-kind primary** (design §2.2 "atomically
  retires the previous primary when requested"), scoped per (user, kind) so a new
  primary phone never retires a primary email. `Get(id)` returns replaced rows
  (history/redemption reload per §2.3); `GetLogin`/`GetRecovery`/`ListByUser` are
  active-only.

For AV3-1.3 (contact-change flow state): `ApplyVerifiedChangeInput` is the
translation target for a consumed `contactchange.PendingChange` — field names
line up (`UserID`, `Kind`, `NormalizedValue`, the three use flags, `MakePrimary`,
`ReplacesIdentifierID`), so `contactchange.PendingChange.NewValue` maps to
`NormalizedValue` at the confirm step. The confirm order the design pins
(challenge consume → contactchange consume → `ApplyVerifiedChange`) is unblocked:
the atomic apply contract now exists and is storetest-proven. The identifier
repo deliberately does NOT import contactchange — keep that edge one-way
(authsvc → both) when AV3-1.3 lands the contactchange domain.

### 2026-07-12 — AV3-1.3 (contact-change flow state)

Dependencies: AV3-1.1 and AV3-1.2 complete and checked off; AV3-0.3 complete
(the challenge purposes `change_email`/`change_phone` and the atomic-secret rail
this flow pairs with exist). Phase 0 fully closed. Worktree changes preserved; no
resets. Scope held to the contactchange DOMAIN + its storetest conformance +
reference impl + the `Repositories.ContactChanges` slot — authmem wiring is
explicitly AV3-1.4.

Files changed:

- `features/authentication/domain/contactchange/contactchange.go` (new) — the
  flow-state domain: the `PendingChange` entity (design §2.4 fields — `ID`,
  `UserID`, `Kind`, `NewValue` (normalized), the three use flags, `MakePrimary`,
  `ReplacesIdentifierID`, `ExpiresAt`, `CreatedAt`), whose field names line up
  one-to-one with `identifier.ApplyVerifiedChangeInput` so the confirm-step
  translation is a plain field copy (`NewValue` → `NormalizedValue`); a `New`
  constructor (leaves `ID` empty for the store to assign inline — greenfield
  DB-generated — sets `CreatedAt`/`ExpiresAt` from `ttl`, takes an
  already-normalized value); `Uses()` and `Expired(now)` helpers. Carries NO
  secret and NO lockout state — those ride the challenge domain (§2.4).
- `features/authentication/domain/contactchange/repository.go` (new) —
  `Repository` (`Create` atomic replace-per-`(UserID, Kind)`; `Consume` single-use
  get-and-delete by `(userID, kind)`: live → the row, expired → `sdk.ErrExpired`
  (row deleted), missing/consumed → `sdk.ErrNotFound`). Imports only
  `domain/identifier` (for `Kind`) — the one-way edge (identifier never imports
  contactchange; authsvc imports both) is preserved.
- `features/authentication/domain/contactchange/contactchange_test.go` (new) —
  unit tests for `New` field/expiry population, `Uses()` round-trip, and the
  inclusive-boundary `Expired`.
- `features/authentication/authentication.go` — added the `ContactChanges` slot
  to `Repositories` (frozen, nil-tolerated until phase 6) + the import.
- `features/authentication/storetest/storetest.go` — new `ContactChanges` group
  (skip-loud when nil) with the six enumerated sub-runners
  (`CreateReplacesPriorPerUserKind` incl. cross-kind coexistence,
  `ValueAndUseRoundTrip`, `ConsumeIsSingleUse`, `ConsumeExpiredDeletes`,
  `ConsumeMissingNotFound`, `ConcurrentConsumeSingleWinner`) + a `newEmailChange`
  seed helper.
- `features/authentication/storetest/reference_test.go` — `refContactChanges`
  over a new `contactChanges` map keyed by `(user, kind)` (composite key enforces
  atomic replace-per-pair; get-and-delete regardless of expiry), under the single
  shared reference mutex; wired into `repositories()`.

Commands / results:

- `cd features/authentication && go test -race ./storetest ./domain/contactchange`
  — **PASS** (storetest reference green incl. the new `ContactChanges` group, all
  six sub-runners executed under `-race`, verified not skipped via
  `-run TestReference/ContactChanges -v`; `domain/contactchange` unit suite green).
- `make check` — **PASS** (`all checks passed`; per-module build/vet/test across
  every module, including `examples/auth-cms/internal/authmem` — its
  `ContactChanges` is nil so the group skips LOUDLY there, which AV3-1.4 wires;
  pgx/turso live conformance legs skip without a DSN).
- `make guard` — **PASS** (run as part of `make check`; feature core still requires
  sdk only, no foreign-feature/examples imports).

Premise adaptations:

- **`PendingChange.Kind` and `Repository.Consume`'s kind parameter are typed
  `identifier.Kind`, not the illustrative `string` in the design §2.4 port
  sketch.** The AV3-1.2 handoff pins the field as "identical" to
  `identifier.ApplyVerifiedChangeInput.Kind` (which is `identifier.Kind`), so the
  confirm-step translation is a plain field copy and the closed
  `{email, phone}` vocabulary is enforced at the type boundary rather than as a
  raw string. This is a strengthening of the illustrated type, not a
  product/architecture change; the one-way `contactchange → identifier` import
  edge stays acyclic (identifier imports only sdk).
- **Expiry uses `sdk.ErrExpired` / `sdk.ErrNotFound` directly, no
  contactchange-specific error vars.** The phase file pins exactly these two
  sentinels ("Expired consume deletes and returns `sdk.ErrExpired`;
  missing/already consumed returns `sdk.ErrNotFound`"), matching the
  oauthstate.Consume precedent, so no domain error vars were introduced.
- **`ContactChanges` slot added in this task (AV3-1.3), not deferred.** Design §8
  lists `ContactChanges` among the required `Repositories` additions; the storetest
  group needs the field to key its skip-loud gate and the reference wires it, so
  the slot lands with the domain it backs (frozen/nil-tolerated until phase 6).

For AV3-1.4 (update proof-host memory repositories): `Repositories.ContactChanges`
now exists (frozen, nil-tolerated) and the exported storetest `ContactChanges`
group runs any wired implementation. authmem currently leaves `ContactChanges` nil,
so the group skips LOUDLY there — AV3-1.4 should add a `contactChanges` map keyed by
`(user, kind)` sharing authmem's single store mutex (one shared data object, no fake
cross-repo transaction), wire it into authmem's `Repositories`, and let the exported
suite prove it instead of duplicating behavioral tests. The reference impl in
`storetest/reference_test.go` (`refContactChanges`, composite `(user, kind)` key,
get-and-delete regardless of expiry) is the pattern to mirror. Field names on
`contactchange.PendingChange` line up with `identifier.ApplyVerifiedChangeInput`
for the phase-5 authsvc confirm-step translation; the confirm order the design
pins (challenge consume → contactchange `Consume` → `ApplyVerifiedChange`) is now
fully unblocked at the repository level.

### 2026-07-12 — AV3-1.4 (update proof-host memory repositories)

Dependencies: AV3-1.2, AV3-1.3, AV3-0.3, AV3-0.4 all complete and checked off;
phase 0 fully closed; AV3-1.1–AV3-1.3 checked off. Worktree changes preserved;
no resets. This is the LAST task of phase 1, so the phase-close gate ran after
the task verification. Scope held to `examples/auth-cms/internal/authmem` — the
proof host — plus its stale-count doc touch; no feature-core, reference, or SQL
changes.

Files changed:

- `examples/auth-cms/internal/authmem/ports_v3.go` (new) — wires the five v3
  atomic-rail ports that authmem left nil (so the exported storetest groups were
  skipping LOUDLY there): `challengeRepo` (single-active per (user, purpose),
  unique (purpose, secret_digest), atomic ConsumeCode/ConsumeToken with constant-
  time digest compare via `auth.ConstantTimeDigestEqual`, attempt/lockout, purge);
  `contactChangeRepo` (keyed by `(user, kind)` → atomic replace-per-pair, single-
  use get-and-delete Consume: live→row, expired→`sdk.ErrExpired` row-gone,
  missing→`sdk.ErrNotFound`); `authGrantRepo` (single-use session-bound Consume,
  context/expiry decided in one critical section, DeleteBySession cascade);
  `credentialMutationRepo` (Snapshot projects HasPassword/OAuth + AuthRevision,
  revision-CAS Apply with the closed typed-mutation switch); `deliveryJobRepo`
  (idempotent Enqueue, superseding Replace, oldest-due Claim lease, lease-checked
  Succeed/Fail/Retry/Cancel, bounded PurgeTerminal). Compile-time `_ Repository`
  assertions for all five. Behavior mirrors `storetest/reference_test.go`.
- `examples/auth-cms/internal/authmem/authmem.go` — added the five backing maps
  to the shared `data` holder (`challenges`, `authGrants`, `deliveryJobs`,
  `contactChanges` keyed by `(user, kind)`, and the design §5.6 credential-
  projection stand-in `credentialIdentifiers`), initialized them in `New`, wired
  the five repos into `Repositories()`, added the five domain imports, and
  refreshed the stale "twelve ports" doc comment.

Commands / results:

- `cd examples/auth-cms && go test -race ./internal/authmem/...` — **PASS**
  (0.165s under `-race`).
- Verified not-skipped: `go test -race -run
  'TestConformance/(Challenges|ContactChanges|AuthenticationGrants|CredentialMutations|DeliveryJobs)'
  -v ./internal/authmem/` — all five groups **RUN + PASS** (46 leaf sub-runners,
  no SKIP) against authmem, so authmem is now the second full conformance target
  for the whole v3 atomic-rail suite alongside the reference.
- `cd features/authentication && go test ./storetest` — **PASS** (reference
  unchanged).
- Phase-close gate: `make check` — **PASS** (`all checks passed`; per-module
  build/vet/test across every module incl. authmem, integration-tag vet, and all
  guards; pgx/turso live legs skip without a DSN, recorded). `make guard` —
  **PASS** (exit 0).

Premise adaptations:

- **auth_revision anchors on the user row in authmem, not a separate revisions
  map.** The reference keeps a standalone `authRevisions` map;
  `credentialMutationRepo` instead reads/writes `users[userID].AuthRevision`,
  matching authmem's existing single-anchor model (its `identifierRepo.Apply`
  VerifiedChange` and `userRepo.CreateWithPrimaryIdentifier` already ride the user
  row). Same observable contract; consistent single source of truth. The exported
  `CredentialMutations` suite always seeds via `Users.Create`, so the row-anchored
  revision is present for every case.
- **Credential-projection identifiers are a stand-in mirroring the reference, not
  the real identifier rows.** authmem stores real `identifier.Identifier` rows in
  `data.identifiers`, but `credentialMutationRepo` projects `MethodSet.Identifiers`
  from a separate `credentialIdentifiers` stand-in (empty in the exported suite,
  which only exercises password/OAuth/revision) exactly as the reference does.
  Wiring the credential Snapshot to project from the real rows is the phase-6
  credential-suite re-key, not a drive-by refactor here; the `RetireIdentifier`/
  `ChangeIdentifierUses` branches are real mutations of the stand-in, not stubs.

Phase-1 acceptance (end of this file):

- Reference memory and authmem now pass identical identifier/contact-change,
  challenge/grant, and credential-mutation conformance (both drive the exported
  `storetest.Run`; authmem's five v3 groups execute, no skips). ✓
- All new v3 code uses identifier contracts; legacy email fields/methods stay
  marked `Deprecated: auth-v3 transition` and are listed for mandatory AV3-5.5
  removal (unchanged this task). ✓
- `make check && make guard` green. ✓

For phase 2 (AV3-2.1 transitional canonical migrations + parity test, and
AV3-2.2/2.3 pgx/turso implementations): the memory/reference contract is now
frozen and proven against BOTH the reference and authmem for every v3 port
(identifiers, contact changes, challenges, grants, credential mutations, delivery
jobs). The SQL stores must reproduce these exact semantics live. Note the memory
peculiarities the SQL schema must back: contact changes are unique-per-`(user,
kind)` (a partial/greenfield unique index or upsert-replace); the challenge
`(purpose, secret_digest)` claim and single-active `(user, purpose)` row are
unique indexes; `credentialMutations` serialize on `users.auth_revision` (the
single anchor authmem/pgx/turso all share — no separate revisions table); delivery
Claim leases the oldest due job by `(available_at, created_at, id)`. pgx/turso
`Identifiers`/`CreateWithPrimaryIdentifier` already target the phase-2
`user_identifiers` table and `users.auth_revision` column (AV3-1.2, real
implementations, not stubs) but are not yet migrated/live — AV3-2.1 adds the
byte-identical migration trees, AV3-2.4 proves both dialects live. No SQL adapter
work for the five phase-0/1 atomic-rail ports (challenges, grants, credential
mutations, delivery jobs, contact changes) exists yet — those land as phase-2
store tasks; authmem/reference are their only conformance targets today.
