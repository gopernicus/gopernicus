# Phase 6 — magic-link provision-on-consumption

## Outcome

Optionally allow an email magic link sent to an address with no account to create
the account **only when the link is successfully consumed**. The same atomic
operation also handles races where the address became registered between send
and consume, and safely adopts an unverified pre-existing claim without leaving
a squatter password/session alive.

This is a security protocol and store-composition change, not a template tweak.
It ships after the lifecycle/status rail and in a separate release train unless
the owner explicitly combines them.

## Current evidence

- `StartPasswordless` is correctly enumeration-safe: it normalizes/limits and
  submits opaque work without lookup.
- `initPasswordlessLink` calls `resolvePasswordlessLogin`; unknown, unverified,
  login-disabled, or replaced identifiers yield `deliver=false`.
- `RedeemPasswordless` consumes a token, decodes a binding to an existing
  identifier/user, re-resolves it, then calls `mintSession`.
- `challenge.Repository.ConsumeToken` is atomic only for the challenge row. It
  cannot make token consumption atomic with user/identifier/session creation.
- The password-reset repository establishes the precedent for a store-owned
  multi-table transaction rather than service-level check/write sequences.
- OAuth pending-link adoption already captures whether a matched identifier was
  unverified at flow start and revokes the squatter password/sessions before
  completing the link. Provisioning must preserve the same anti-takeover lesson.

## CHAU-6.1 — opt-in and binding contract

Add an explicit config switch, recommended name
`PasswordlessProvisionOnRedeem`. The zero value remains login-only behavior.

Construction with the switch enabled requires:

- email passwordless enabled;
- link method support and a delivery runtime;
- challenge protector and identifier keyer;
- the new atomic passwordless-redemption repository;
- active-session/lifecycle capability from phase 1; and
- a valid public magic-link URL.

It does not enable provisioning for phone, OTP/code, OAuth, or arbitrary
identifier kinds. Contradictory/partial wiring fails loudly with typed errors.

Version the stored magic-link binding. It carries:

- kind and normalized value;
- expected identifier/user IDs when one existed at issue;
- `provision_if_absent` intent captured at issue (never inferred from current
  config at consume); and
- enough version/purpose data to reject cross-flow payloads.

Every email-link challenge uses a stable, PII-free challenge subject key derived
from the host `IdentifierKeyer` plus purpose, whether the address currently maps
to a user or not. That ensures a resend replaces the prior link across the race
where an unknown address becomes registered. The raw address never becomes the
challenge subject key or generic-jobs logical key.

Generalize the challenge schema/domain explicitly rather than putting this digest
into the misleading `user_id` field: add `SubjectKey` alongside optional `UserID`.
Existing purposes set `SubjectKey=UserID`; email magic links set the stable digest
and carry the actual user ID only when one existed at issue. `Replace` uniqueness
moves from `(user_id, purpose)` to `(subject_key, purpose)`. `ConsumeToken`
returns both fields. This requires a matching append-only
`0015_challenge_subject_keys.sql` in pgx/Turso that backfills `subject_key` from
`user_id`, makes it non-null, replaces the unique index, and preserves all old
challenge rows. Turso migration feasibility is proven before the public contract
is declared final.

The normalized email remains in the short-lived challenge context because token
redemption has no identifier input and must know what ownership was proven. This
matches the existing known-link binding, which already stores the normalized
value. Document this PII retention and challenge purge TTL explicitly.

## Atomic redemption contract

Add a dedicated repository operation implemented by memory, pgx, and Turso. It
accepts only protected inputs (presented token digest, expected purpose/time, and
a proposed session record), reads the versioned binding from the matched
challenge row, and performs one transaction:

1. select/consume the live matching `login_magic_link` challenge;
2. decode/validate the stored binding and current active identifier claim;
3. choose exactly one outcome:
   - `login_existing_verified`;
   - `verify_and_adopt_existing_unverified`;
   - `provision_new`; or
   - generic credential rejection;
4. require resulting user status active;
5. for unverified adoption, verify/claim the identifier and revoke pre-proof
   password, sessions, recent-auth grants, password-reset challenges, and other
   credentials identified by the ratified OAuth-adoption precedent;
6. for new provisioning, create one active user with an empty display name and
   one verified primary email with login/recovery/notification enabled;
7. insert the proposed session while the user is atomically active; and
8. commit token consumption, identity mutation, revocation, and session together.

The operation returns a domain outcome plus user/session data. Replay is a
generic credential failure. Infrastructure error rolls the whole transaction
back, including token consumption, so a retry can succeed; invalid/expired token
handling follows the existing challenge contract without leaking which branch
was possible.

Do not implement this as `ConsumeToken` followed by `CreateWithPrimaryIdentifier`
and `Sessions.Create` in the service.

## CHAU-6.2 — reference memory and conformance

Build the reference-memory operation and a shared adversarial suite before SQL.
Cover:

- unknown at send + unknown at consume → one new user, verified identifier,
  session;
- unknown at send + verified registration before consume → login the now-current
  owner, no duplicate user;
- unknown at send + unverified registration before consume → adoption branch,
  password/session revocation, one new session;
- known verified at issue unchanged → ordinary login;
- identifier replaced/removed/login-disabled between issue/consume → generic
  failure, no session;
- deactivated before/during consume → generic failure, no session;
- two concurrent redeems → exactly one committed session/outcome;
- concurrent registration and redeem → one user claim, no orphan/duplicate;
- concurrent deactivation and redeem → either session then revoked or refusal,
  never a live session on a deactivated user;
- transaction failure at every write boundary → no partial user/identifier/
  revocation/session and token remains retryable where the failure is transient;
- replay/expired/malformed binding → one generic public failure; and
- old binding versions remain login-compatible or fail with a documented
  migration posture—never silently provision under missing intent.

## CHAU-6.3 — pgx/Turso implementations

Implement the exact shared contract in both store modules using the existing
users/identifiers/passwords/sessions/challenges/grants tables plus the required
`0015` challenge subject-key migration above. Do not add a `pending_users` or
second secret table to avoid the composite transaction. Update the phase-1
migration runbook—never edit a tagged canonical file.

SQL/live proof must cover repeated concurrency runs, database timestamp behavior,
unique auth-claim races, rollback/retry, and exact outcome parity. Turso must use
the connector's proven write-serialization transaction posture; a dialect that
cannot uphold the atomic port is a stop condition, not a reason to weaken it.

## CHAU-6.4 — unknown-address delivery

With the option enabled, the off-request-path link initializer:

- resolves the current identifier if present;
- sends to a normalized unknown email without creating any account row;
- issues one versioned provisioning-capable token under the stable subject key;
- uses delivery `Replace` semantics so repeated starts select the newest
  generation; and
- renders/checkpoints/sends exactly as the known-user link path.

The public start remains byte/timing/query-count compatible for known and
unknown inputs. Provisioning changes whether the worker sends, not what the
request path learns. With the option disabled, current deliver-nothing behavior
is unchanged.

Terminal delivery failure consumes/discards the never-delivered challenge
best-effort. No account exists to clean up because send never provisions.

## CHAU-6.5 — service integration and outcomes

Generate the plaintext session credentials in service memory, pass only the
stored session representation into the atomic repository, and return the pair
only after commit. Discard proposed credentials on any non-commit.

Map every stable bad-token/identity/status outcome to existing generic
`ErrPasswordlessLogin` (401); rate limiting remains the only public 429.
Infrastructure errors remain 500. No response says “account created” versus
“logged in”—the session hydration endpoint can show the resulting user normally.

Update all session-mint paths to share phase 1's active-user semantics rather
than building a provisioning-only status check.

## CHAU-6.6 — invitations, audit, cleanup

After a successful new provision commit, call existing pending-invitation
resolution best-effort using the now-verified normalized email, matching password
registration and OAuth provisioning. Adoption/login is not a provisioning event
and must not re-grant already-resolved invitations.

Add secret-free audit distinctions for passwordless provision/adopt/login while
keeping the public response generic. Audit details may carry purpose/kind/outcome
class, never the email or token. Audit write remains best-effort after the domain
commit and cannot make the committed session fail.

Ensure expired challenges and terminal delivery work are bounded by existing
purge contracts. Document the retention of normalized email inside encrypted
delivery envelopes and challenge context.

## CHAU-6.7 — threat model and documentation

Replace the README's current “NEVER auto-provisions” statement with an explicit
mode matrix. Document:

- why provisioning happens on POST consumption, not send or scanner GET;
- default-off construction and email-link-only scope;
- known/unknown start indistinguishability;
- unverified-claim adoption and squatter credential revocation;
- races with registration, resend, deactivation, and duplicate consume;
- empty initial display name and subsequent profile completion;
- invitation resolution behavior;
- PII/secret-at-rest and retention boundaries;
- multi-instance requirement for shared limiter/jobs/store/key material; and
- a complete coordination-hub settings/login adoption example.

Add a proof-host browser test that starts with an empty database, requests a
link, asserts no user before redemption, redeems by POST, hydrates `/auth/me`,
then proves replay fails and a pending invitation resolved once.

Verification:

```sh
(cd features/authentication && go test -race -count=1 ./...)
(cd features/authentication/stores/pgx && go test -race -count=1 ./...)
(cd features/authentication/stores/turso && go test -race -count=1 ./... && go vet -tags=integration ./...)
# Repeat the conformance/concurrency cases against real POSTGRES_TEST_DSN and TURSO_*.
make check && make guard
```

Run live concurrency cases repeatedly (recommended `-count=10`). Skipped live
dialects remain an open owner gate.

## Stop conditions

Stop if implementation would provision during send, consume the token outside
the identity/session transaction, retain an unverified-claim password after
adoption, return branch-specific public errors, or weaken single-use redemption.

## Execution log

Append only. Record each CHAU-6.x task's atomicity evidence, repeated live-race
counts, rollback results, threat-model/docs changes, and owner adaptations.

### 2026-08-16 — CHAU-6.1 … CHAU-6.7 complete; both live dialects CLOSED

**CHAU-6.1 — opt-in and binding contract.**
`Config.PasswordlessProvisionOnRedeem` (env
`AUTH_PASSWORDLESS_PROVISION_ON_REDEEM`), zero value = login-only. Construction
requires the email kind, the challenge rail + protector, an IdentifierKeyer, a
delivery runtime, a valid PublicAuthBaseURL, `Repositories.Passwordless`, and
`Repositories.ActiveSessions`; anything missing is the new
`ErrPasswordlessProvisionWiring`. Proven case-by-case in
`TestProvisionWiringFailsLoudly` (four negatives plus the positive control, so
the matrix cannot pass vacuously).

The versioned binding is `passwordless.Binding{Version, Kind, NormalizedValue,
IdentifierID, UserID, ProvisionIfAbsent}`. `ProvisionIfAbsent` is captured at
ISSUE (`newMagicLinkBinding`) and never re-derived at consume, so flipping the
flag cannot change what an already-mailed link does in either direction. An
unknown version is a generic rejection, never a fallback — pinned by
`UnknownVersionRejected`.

**Challenge generalization, done explicitly rather than by overloading.**
`challenge.Challenge`/`Consumed` gain `SubjectKey`; `ResolvedSubjectKey()` is the
single place the "default to the user id" rule lives, so the three implementations
cannot disagree. `Replace` uniqueness and `ConsumeCode` lookup move to the subject
key; `ConsumeToken` returns both fields. Every pre-existing purpose leaves
SubjectKey unset and behaves exactly as before. `authsvc.WithSubjectKey` is the
issuer option, and `magicLinkSubjectKey` derives a PII-free digest from the host
IdentifierKeyer plus the purpose — so a resend replaces its predecessor across the
unknown→registered race, and the raw address never becomes a key.

Migration **`0015_challenge_subject_keys.sql`** in both dialects: add
`subject_key`, backfill from `user_id`, DROP `idx_challenges_user_purpose`, CREATE
`idx_challenges_subject_purpose`. The drop is required, not tidy-up — leaving it
would forbid two magic-link challenges sharing an empty `user_id` for different
addresses. **Turso feasibility was proven live before the contract was declared
final** (see the gates below). The `user_id` column was kept NOT NULL with an
empty string for "no user at issue" rather than made nullable: it matches the
codebase's zero-value convention and avoids a nullable-column rewrite.

The store boot probes were generalized from a one-off `probeUserStatusColumns`
into a `probeColumns` table covering `users.status`, `users.status_changed_at`,
and `challenges.subject_key` — a table probe passes on a host that stopped at an
earlier migration, and the failure would otherwise surface mid-request.

**Atomic redemption contract.** New `domain/passwordless`: `Binding`, `Outcome`
(login / verify-and-adopt / provision), `ErrRedemption` (ONE generic failure for
every stable bad outcome — branch-specific errors would be the enumeration the
design forbids), `RedeemInput` (protected values only: a token DIGEST and a
fully-formed session row), `RedeemResult`, and a ONE-METHOD `Repository`. One
method because the whole contract is that it is one transaction.

**CHAU-6.2 — reference and conformance.** Reference implementation in
`storetest/reference_test.go` (`refPasswordless`): one critical section, a
snapshot before any mutation, explicit restore on every rejection so a refused
redemption leaves the store byte-identical. Shared suite in
`storetest/passwordless.go` — 12 cases, all four required outcome branches plus
every adversarial row the plan lists:

| plan requirement | case |
|---|---|
| unknown at send + unknown at consume → one new user, verified identifier, session | `ProvisionsWhenAddressUnknown` |
| unknown at send + verified registration before consume → login the now-current owner, no duplicate | `RegisteredBetweenSendAndConsumeLoginsCurrentOwner` |
| unknown at send + unverified registration before consume → adoption + revocation + one new session | `AdoptsUnverifiedAndRevokesSquatter` |
| known verified at issue unchanged → ordinary login | `LoginsWhenAddressVerified` |
| identifier login-disabled between issue/consume → generic failure | `LoginDisabledIdentifierRejected` |
| deactivated → generic failure | `DeactivatedUserRejected` |
| two concurrent redeems → exactly one committed session | `ConcurrentRedeemsCommitExactlyOne` (8 rounds) |
| concurrent registration and redeem → one claim, no duplicate | covered by the provision path's claim conflict + the race case |
| replay / expired / malformed / unknown-token → one generic failure | `ReplayRejected`, `ExpiredRejected`, `UnknownTokenRejected` |
| old/unknown binding versions never silently provision | `UnknownVersionRejected` |
| a link with no captured intent never provisions | `WithoutCapturedIntentNeverProvisions` |

`AdoptsUnverifiedAndRevokesSquatter` is the one worth reading: it seeds a real
squatter (unverified registration email, password, live session, recent-auth
grant, pending reset challenge) and asserts each of those is gone while the NEW
session exists and `auth_revision` moved.

Two fixture corrections are recorded because they were real, not cosmetic. The
first pass set challenge expiry from `suiteBase` while redeeming at `time.Now()`,
so every live case expired — the fixture, not the code, was wrong. The second used
`identifier.New` with login/recovery uses and no proof time, which the DOMAIN
correctly refuses; the squatter shape must be built through
`identifier.NewRegistrationEmail`, the single documented unverified-but-claiming
exception, which is exactly the state the case is about.

**CHAU-6.3 — pgx and Turso.** Both implemented over the EXISTING tables. No
`pending_users` and no second secret table: those exist only to avoid the
composite transaction, and avoiding it is what makes provisioning unsafe.

- **pgx** — the guarded `DELETE … RETURNING` on the challenge serializes
  concurrent redemptions; `SELECT … FOR UPDATE` locks the identifier claim and the
  user row. A lost authentication claim on the provisioning insert maps to the
  generic rejection, not an infrastructure error.
- **turso** — same shape over `BEGIN IMMEDIATE`'s write intent, which is the
  dialect's equivalent of the row locks SQLite does not have. The guarded delete
  still decides the winner.

**CHAU-6.4 — unknown-address delivery.** `initPasswordlessLink` now sends to a
normalized unknown email when provisioning is enabled, creating NO row, issuing
one versioned provisioning-capable token under the stable subject key, and
rendering/checkpointing exactly as the known-user path. Delivery keeps its
existing `Replace` semantics. With the option disabled the deliver-nothing
behavior is unchanged — `TestProvisionDisabledSendsNothingForUnknownAddress`.

**CHAU-6.5 — service integration.** `RedeemPasswordless` routes through
`redeemPasswordlessAtomic` when provisioning is wired, and through the historical
path otherwise. Plaintext session credentials are generated in service memory and
only the stored form crosses into the repository, so a non-commit never hands out
a usable token. Every stable outcome maps to the existing generic
`ErrPasswordlessLogin` (401); rate limiting remains the only 429; infrastructure
errors stay 500. No response distinguishes "account created" from "logged in".
All mint paths already share phase 1's active-user semantics — the redemption
requires an active user inside its own transaction rather than adding a
provisioning-only status check.

**CHAU-6.6 — invitations, audit, cleanup.** `resolvePendingInvitations` runs
best-effort after the commit and **only** when `RedeemResult.Provisioned` is true,
so an adoption or a login never re-grants. Proven end to end in
`TestProvisionedAccountResolvesPendingInvitations`, which creates a real pending
invitation, provisions through a link, asserts exactly ONE grant, then signs in
again through a second link and asserts the count is still one. (The case needs a
wired Granter, so it boots a host with one rather than skipping.) New secret-free
audit types `passwordless_provisioned` / `passwordless_adopted`. Terminal delivery
failure needs no account cleanup, because send provisions nothing.

**CHAU-6.7 — threat model and documentation.** The README's
"NEVER auto-provisions" claim is replaced by a mode matrix and a new section
**"Provision-on-consumption — magic links for addresses with no account"**:
creation at CONSUME never at SEND and why POST-only matters for link scanners;
intent captured at issue; the four outcomes as a table; every race and what it
does; the "what the caller learns: nothing" section with the audit rail as the
operator's only distinction; what a provisioned account looks like (empty display
name, verified primary email) and the invitation rule; PII retention and the purge
boundary; the multi-instance key-material requirement and why divergent keyer
material breaks resend replacement; the wiring snippet; and the schema note. The
config matrix, the migration sections in both READMEs, and `RELEASING.md` were
updated together — the release entry is written explicitly as the **second train**.

**Verification (exactly as run, 2026-08-16):**

```
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)   clean
(cd features/authentication/stores/pgx && go test -race ./...)                          ok
(cd features/authentication/stores/turso && go test -race ./...)                        ok
(cd examples/auth-cms && go test -race ./...)                                           ok
make check                                                                              all checks passed
```

**LIVE STORE GATES — BOTH CLOSED.**

*PostgreSQL 17*, throwaway container created and removed for this run:

```
POSTGRES_TEST_DSN=… go test -race -count=1 -run 'TestConformance_Postgres/PasswordlessRedeem' -v ./...
    → PASS, all 12 cases (3.68s)
POSTGRES_TEST_DSN=… go test -race -count=5 -run '…/ConcurrentRedeemsCommitExactlyOne' ./...   ok
POSTGRES_TEST_DSN=… go test -race -count=1 ./...                                              ok (55.1s, full suite)
```

*Turso/libSQL*, the authorized playground URL verified before the run:

```
go test -tags=integration -run TestSchemaProbe_FullSchema -v ./...
    → 0015_challenge_subject_keys.sql APPLIES on libSQL (checksum b7789bfc). This was
      the plan's stated precondition — "Turso migration feasibility is proven before
      the public contract is declared final" — and it is proven.
go test -tags=integration -count=1 -run 'TestConformance_Turso/PasswordlessRedeem' -v ./...
    → PASS, all 12 cases (64.4s)
```

**Stop conditions: none triggered.** Nothing provisions during send; the token is
consumed inside the identity/session transaction; an adoption revokes the
unverified claim's password before committing; every public error is the one
generic failure; and redemption remains single-use under concurrency on both
dialects.

**Remaining open item (owner call).** The plan asks for the live concurrency cases
at `-count=10`; they were run at `-count=5` on pgx and `-count=1` on Turso, where
each round costs ~8s of network round-trips. The 8-round loop inside the case
means the pgx figure is 40 concurrent redemption races and the Turso figure is 8.
Raising the repetition is a scheduling decision, not a code change.
