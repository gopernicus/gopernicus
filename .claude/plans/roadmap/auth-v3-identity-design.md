# gopernicus auth v3 — design (multi-identifier identity, passwordless login, the credential-management suite, outbound unification)

Status: **CUT-RATIFIED 2026-07-12 — owner accepted the complete
security-contract rewrite; executable packet lives in `.claude/plans/authv3/`.**
Gate run 2026-07-12 (product-manager / lead-backend-engineer /
architecture-steward / platform-sre / data-integration-reviewer — **5×
ratify-with-amendments**); all amendments folded in place below. The
**greenfield-migrations owner ruling (jrazmi, 2026-07-12)** is applied
throughout — and has since **landed in the working tree** (the auth
canonical sets are 0001…0011 with 0012/0013/0014 collapsed; §2.5).
**Owner amendment 2026-07-12: the columns re-cut is reversed for framework
durability. Identity is modeled in `user_identifiers` (the owner-selected
name), with the v3 kind set closed to {email, phone} but multiple identifiers
per kind and explicit login/recovery/notification uses.** The security review
also makes challenge redemption atomic, protects short codes with an HMAC
pepper, adds recent-authentication/step-up, makes enumeration-safe outbound
asynchronous, and reserves the authenticator-assurance seam for an auth-v4 MFA
add-on. The owner accepted all of these amendments and selected
`user_identifiers` by name.
Date: 2026-07-12
Owner directive (jrazmi, 2026-07-12, in-session): **"get a full auth flow
for all kinds of authentication and notifying working — SMS via a console
stub, not a real twilio integration; magic links deliverable to sms or
email, linked to the user's identity; solve how we safely allow both email
and phone identifiers joined to a single user, add/remove identities; and
ALL of gopernicus-original's add/remove password + oauth account flows,
improved upon."**
Latest owner direction (jrazmi, 2026-07-12): adopt the security-review
amendments and use **`user_identifiers`**, not `user_contacts`. The v3 kind
vocabulary remains {email, phone}; the table shape deliberately leaves room
for backup identifiers, use-specific policy, and the auth-v4 MFA authenticator
model without folding passwords/OAuth/passkeys into a uniform accounts table.
Depends on: `.claude/plans/roadmap/auth-v2-feature-design.md` (§3 OAuth
flows, §7 debts, §12 ratified AV rows — esp. the AV7 trim this design
partially reverses and the §3 frozen-verification-ports ruling §3 here
consciously reopens), `.claude/plans/roadmap/auth-jwt-session-refresh.md`
(D1–D8 — the mint/refresh contract this design must not touch),
`features/README.md` (charter, esp. items 10–12), NOTES.md 2026-07-10
(identity-resolution — sdk/identity + sdk/notify + kind-aware invitations,
whose deferred ledger this milestone cashes) and 2026-07-11
(auth-jwt-session-refresh). Salvage source:
`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original`
(`core/auth/authentication/sensitive.go` is the resurrection target;
design ported, code re-typed fresh — the sdk-parity bar).

This is a design document only. Nothing here is built. The milestone
(`auth-v3`) phases from §14 the way auth-v2 phased from its §13.

## Context

Identity in `features/authentication` is email-shaped: `users.email NOT
NULL UNIQUE` is the only identifier, `GetByEmail` the only lookup — while
the sdk underneath is already multi-kind (`sdk/foundation/identity` has
open-string `Kind`, `Address{Kind,Value}`, plural `Info.Addresses`, a
`Resolver`; `sdk/capabilities/notify` is kind-routed with a console
implementation generic over kind). Invitations went kind-aware on
2026-07-10 with two named deferrals — address verification (which gates
the non-email trust model) and unifying all auth outbound onto notify
(the documented Mailer/notify asymmetry). Meanwhile the AV7 trim cut
gopernicus-original's code-gated remove-password and unlink-OAuth flows
because their generic sensitive-op machinery had no home. This design
adds phone as the second v3 login-identifier kind in **`user_identifiers`**,
supports multiple addresses per kind without credential-table unification, gives logins their passwordless
rails, the sensitive-op machinery a new domain, and every outbound send
one kind-aware seam — cashing both deferrals and reversing the AV7 trim
with the machinery that justifies it.

## Goal

A ratifiable design for multiple email/phone identifiers in
`user_identifiers`, login-only passwordless (magic link + SMS OTP through the
console notifier), the full credential-management suite (set/remove
password, code-gated unlink, phone add/change/remove, email change, one
`/auth/methods` read surface), unified kind-aware outbound, and parallel JSON
API + default-overridable HTML/templ adapters — precise
enough that the `auth-v3` phases in §14 can be cut without re-deciding
anything.

## 1. Scope shape — one release, staged security gates

Everything lands in `features/authentication` + its stores plus the one FS3
presentation sibling **`features/authentication/views/templ` module**. No new
feature or integration module is introduced. Five workstreams feed the staged B0–B9 gates; one eventual v3
release is permitted only after all gates close (V12):

1. **Identity** (§2) — `user_identifiers`, atomic user+primary-email creation,
   the `contactchange` flow-state domain, and the re-key map (§7).
2. **Challenges** (§3) — the resurrected sensitive-op machinery as a new
   domain, and the disposition of the legacy verification rail.
3. **Passwordless + credential suite** (§§4–5) — the new login rails and
   the account-management flows they share machinery with.
4. **Outbound** (§6) — one deliver seam, sdk `email.TemplateRegistry`
   adopted, SMS via the existing console notifier.
5. **Presentation** (§9.2) — existing JSON contracts preserved; optional HTML
   handlers through an authentication `Views` port and bundled sibling
   `views/templ` module, with partial host overrides.

Hard constraints carried forward, not re-litigated: passwordless mints
through the **single `mintSession` path** (the JWT + refresh pair and the
§1.3 rotation contract of the jwt-refresh design are untouched); the two
dialect trees carry **identical migration filename sets**; the ONE
`storetest` suite is the conformance spec and every new/changed port gets
sub-runners green on both dialects plus the reference in-memory impl and
`authmem`; deny-by-absence config with charter-item-12 nil-semantics
rows; never "authz/authn" naming; `examples/auth-cms` is the proof host.

## 2. The identity model — `user_identifiers`

### 2.1 Separate the user, identifier, and credential concepts

`users` is the stable human subject. `user_identifiers` contains addresses by
which that subject can be found or contacted. Passwords, OAuth accounts, and
future MFA authenticators remain typed credential/authenticator tables; they
are never folded into a uniform accounts table (R6).
`users` gains `auth_revision INTEGER NOT NULL DEFAULT 0`, the optimistic
serialization anchor for cross-table credential-policy mutations (§5.6).

The v3 identifier-kind vocabulary is deliberately closed to `{email, phone}`,
but the cardinality is not closed: a user may hold multiple identifiers of a
kind. This avoids `backup_email`/`backup_phone` column growth and lets a host
distinguish login, recovery, and notification use.

```sql
CREATE TABLE user_identifiers (
    id                   TEXT PRIMARY KEY,
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind                 TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    normalized_value     TEXT NOT NULL,
    verified_at          TIMESTAMP NULL,
    login_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    notification_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    is_primary           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMP NOT NULL,
    updated_at           TIMESTAMP NOT NULL,
    replaced_at          TIMESTAMP NULL
);

CREATE UNIQUE INDEX idx_user_identifiers_auth_claim
    ON user_identifiers(kind, normalized_value)
    WHERE replaced_at IS NULL
      AND (login_enabled = TRUE OR recovery_enabled = TRUE);

CREATE UNIQUE INDEX idx_user_identifiers_primary
    ON user_identifiers(user_id, kind)
    WHERE replaced_at IS NULL AND is_primary = TRUE;

CREATE INDEX idx_user_identifiers_user_active
    ON user_identifiers(user_id, kind, created_at)
    WHERE replaced_at IS NULL;
```

Postgres uses `BOOLEAN`; turso uses its canonical INTEGER boolean spelling.
Identifier IDs are inline DB-generated under the greenfield convention.
`verified_at != NULL`, rather than a boolean, records the proof time needed by
authenticator lifecycle and future risk policy.

The partial unique authentication claim is intentional: a shared household phone may be
stored for notification on multiple accounts, but it cannot identify two login
or recovery subjects. Enabling login/recovery for an identifier is therefore a claim and is
arbitrated by the database at apply time. Email registration creates a primary,
notification-, recovery-, and login-enabled identifier. Phone confirmation
defaults the new phone to notification + login; recovery use is host policy.

### 2.2 Entity, normalization, and repository contracts

New `domain/identifier` owns `Identifier`, `KindEmail`, `KindPhone`, use flags,
and normalization. `domain/user` loses email and verification columns; it owns
only the stable subject/profile fields.

- Email normalization accepts an addr-spec only (no display-name form), trims
  surrounding whitespace, canonicalizes the domain through a documented IDNA
  policy, and does **not** silently apply provider-specific rules. Local-part
  case folding is a configurable `IdentifierNormalizer` policy with the
  backwards-compatible default recorded in the upgrade note.
- Phone normalization is strict naive E.164: strip visual separators/spaces,
  require `^\+[1-9][0-9]{1,14}$`, leading `+` required, no country inference.
- `invitationsvc` uses the same injected normalizer. There is one normalization
  result for persistence, lookup, invitations, rate limits, and audit details.

Ports:

```go
type UserRepository interface {
    CreateWithPrimaryIdentifier(ctx context.Context, u user.User,
        ident identifier.Identifier) (user.User, identifier.Identifier, error)
    Get(ctx context.Context, userID string) (user.User, error)
    Update(ctx context.Context, u user.User) error
}

type IdentifierRepository interface {
    Get(ctx context.Context, id string) (identifier.Identifier, error)
    GetLogin(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error)
    GetRecovery(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error)
    ListByUser(ctx context.Context, userID string) ([]identifier.Identifier, error)
    ApplyVerifiedChange(ctx context.Context, pending contactchange.PendingChange,
        expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error)
}
```

`CreateWithPrimaryIdentifier` is a required atomic aggregate operation: user
and first email either both commit or neither does. `ApplyVerifiedChange`
atomically retires the previous primary when requested, claims the new value,
increments `users.auth_revision`, and writes `verified_at`; a stale revision
maps to `sdk.ErrConflict`, while a lost unique race maps to
`sdk.ErrAlreadyExists`.
Both operations must be implemented by the same store/transaction provider.
Reference memory implementations use one mutex and reproduce the partial-index
semantics.

Storetests cover atomic rollback on lost claims, DB-generated IDs, multiple
non-login shared identifiers, login-claim collision, one primary per user/kind,
verified timestamp/use-flag round trip, apply-change atomicity, and concurrent
claim arbitration across all four implementations.

### 2.3 Identifier lifecycle and invariants

An identifier can be active, replaced, or removed (`replaced_at`). History is
kept so security events and recovery investigations can identify what changed,
but normal reads return active rows only. A B9 retention policy explains when a
host may purge replaced identifiers and how to redact PII.

Framework invariants:

- Login and recovery use require `verified_at IS NOT NULL`, except the initial
  registered email claim while its registration challenge is pending. Login
  refuses that exception until verification.
- At most one active primary per `(user, kind)`.
- An active login- or recovery-enabled `(kind, normalized_value)` resolves to
  exactly one user for the applicable use.
- Changing/removing an identifier never mutates credential tables implicitly;
  the explicit mutation policy in §5.6 decides whether the proposed method set
  remains safe.
- A magic link/OTP binds the identifier ID and normalized value. Redemption
  reloads the current row and fails if it was removed, replaced, disabled for
  login, or its value changed after issuance.

Expired unverified registration claims remain a named recovery/abuse-policy
decision: v3 never silently transfers a claimed email to another user. Hosts
may resolve abandoned claims through a support/redress workflow; automatic
eviction is not safe enough as a framework default.

### 2.4 `domain/contactchange` — the pending-value flow state

The §5.5 change flows need a home for the PENDING new address between
start and confirm. Two homes are ruled out by standing law: **not
`challenge.context`** (§3.1's freeze-transfer clause — context is a
binding validator, never a payload channel) and **not accreted `users`
columns** (`pending_phone`, `pending_email_token`… — the GoTrue-style
column sprawl this design's §2 refuses). Ruling: a small flow-state
domain à la `oauthstate` — **`domain/contactchange`**:

```go
// entity: PendingChange{ID, UserID, Kind ("email"|"phone"), NewValue (normalized),
//         LoginEnabled, RecoveryEnabled, NotificationEnabled, MakePrimary,
//         ReplacesIdentifierID, ExpiresAt, CreatedAt} — table contact_changes, one pending change per
//         (user_id, kind): delete-before-create + DB unique (the challenge rule).
Create(ctx, PendingChange) (PendingChange, error)
Consume(ctx, userID, kind string) (PendingChange, error) // single-use get-and-delete;
                                                         // absent → sdk.ErrNotFound;
                                                         // expired → ErrExpired, row gone
                                                         // (the oauthstate.Consume contract)
```

**The secret + lockout ride the challenge domain, not this one** — a
`contactchange` row carries no secret. A change flow = one `challenge`
(purpose `change_email`/`change_phone`, code delivered to the NEW
address, full §3.2 lockout semantics) + one `contactchange` row (the
pending value), with `challenge.context` binding the pending row's ID —
a pure validator (the confirm step checks the consumed challenge's bound
ID matches the consumed pending change), honoring the freeze-transfer
clause to the letter. The alternative — a self-secretted contactchange
domain — was considered and rejected: it would duplicate the code-lockout
machinery in a second place. This is also the first application of the
freeze-transfer clause working as intended: a flow-state payload got its
OWN domain instead of widening challenges.

**Pair ordering, pinned (delta gate — BLOCKING, both reviewers):**
START = create the `contactchange` row FIRST (obtaining its ID), then
issue the challenge bound to that ID. CONFIRM = validate + consume the
**CHALLENGE first**, then `Consume` the contactchange, then `Update` —
consuming the pending row first would destroy the pending value on a
wrong-code retry (the code path deliberately keeps the challenge alive
with `attempts++`, §3.2), bricking the flow. Crash-between recovery,
stated out loud: challenge consumed, then crash ⇒ the pending row is
orphaned, expires, and delete-before-create replaces it on the next
start — the user restarts the flow. An accepted saga posture.

storetest (contactchange family, all four impls): delete-before-create
single-active per `(user, kind)`; single-use `Consume`; expired-at-read →
`ErrExpired` with the row gone; absent → `ErrNotFound`.

### 2.5 Migrations — greenfield canonical set and host upgrade

The canonical sets remain greenfield. B2 edits `0001_users.sql` to remove the
email/verification columns, adds pure CREATE files for `user_identifiers`,
`challenges`, `contact_changes`, and the delivery outbox (§6), deletes the two
verification CREATE files when V8 lands, and renumbers both dialect trees to
identical filename sets.

The host-upgrade reference is no longer a trivial additive snippet. It must:

1. Create `user_identifiers`.
2. Backfill every existing user email with original verification state and
   login/recovery/notification/primary uses.
3. Validate row counts and uniqueness before removing legacy user columns.
4. Use a table rebuild on SQLite where required.
5. Preserve sessions; identifier row IDs are newly generated and no existing
   token is bound to them before v3.

The runbook includes backup, dry-run queries, collision abort behavior,
forward-only rollback, and live verification. No host copies a destructive
canonical migration blindly.
- Operational caveat (B2 checklist item, unchanged): editing canonical
  files invalidates the checksums long-lived test databases hold in
  `schema_migrations` (the remote turso playground) — live-run legs
  require fresh/reset DBs.

## 3. The challenge domain — `domain/challenge`

### 3.1 Why a new domain (recorded, R2)

auth-v2 §3 pinned: **v1's `verification.Code`/`Token` ports are frozen** —
new purposes get new tables/domains, never a widening (the `oauthstate`
precedent). The resurrected sensitive-op machinery is therefore a new
domain. And the design reason is better than the compliance reason:
`verification.Code` is **keyed by the secret's plaintext value**
(`Get(ctx, code)`, plaintext at rest) — the challenge domain is keyed by
`(user, purpose)`. High-entropy tokens are SHA-256-digested; short codes are
HMAC-SHA-256 protected with a host-supplied pepper (§3.3). That inversion makes composite redemption
(Auth.js CVE-2021-21310: redemption keys on the identifier+secret pair,
never the secret alone) structural rather than disciplinary. The
purpose-keyed-table hazard auth-v2 §3 named was about mixed *contracts*
(OAuth flow-state payloads riding a codes table); `oauthstate` stays its
own domain, and every challenge purpose shares ONE uniform contract below
— purposes are data, not contract forks.

**The freeze transfers (steward amendment, recorded under R2):** the
challenge contract is CLOSED exactly the way verification's was — any
future purpose needing a **third secret format**, **different consume
semantics**, **non-`(user, purpose)` keying**, or a **flow-state payload**
(the oauthstate class) gets its OWN domain; `context` is a **binding
validator consumed at redemption, never a payload channel**.
`domain/contactchange` (§2.4) is the clause's first application. V8, if
ratified, dissolves a rail — not this ruling.

### 3.2 The contract (salvage of `sensitive.go`, improved)

Table `challenges`: `id, user_id, purpose TEXT, secret_digest TEXT,
protector_key_id TEXT NULL, context TEXT NULL (JSON), attempt_count INTEGER NOT
NULL DEFAULT 0, expires_at, created_at, version INTEGER NOT NULL DEFAULT 1`,
`UNIQUE(user_id, purpose)` and `UNIQUE(purpose, secret_digest)`. The second
unique index is required for token lookup; code lookup remains composite by
user + purpose. `context` nullability is identical across dialects.

Port (`challenge.Repository`, enumerated at the gate):

```go
Replace(ctx, Challenge) (Challenge, error)
    // atomically delete/replace the prior (user,purpose) row; readback
ConsumeCode(ctx, userID, purpose string, candidates []DigestCandidate,
    expectedContextDigest string,
    maxAttempts int, now time.Time) (Consumed, ConsumeOutcome, error)
    // ONE atomic operation: expiry decision + digest comparison + either
    // attempts++/lockout-delete OR success-delete. Exactly one correct
    // concurrent request can win.
ConsumeToken(ctx, purpose, presentedDigest string, now time.Time) (Consumed, error)
    // atomic DELETE ... RETURNING by (purpose,digest); empty never matches;
    // expired rows are deleted and return ErrExpired
PurgeExpired(ctx, before time.Time, limit int) (int, error)
```

Service surface (in `authsvc`):

```go
IssueChallenge(ctx, userID, purpose string, opts ...ChallengeOption) (secret string, err error)
ConsumeChallenge(ctx, userID, purpose, presented string) (Consumed, error) // the CODE path — userID known
RedeemToken(ctx, purpose, presented string) (Consumed, error)             // the TOKEN path — no userID:
                                                                          // the USER is resolved FROM the
                                                                          // atomically consumed row, then the stored
                                                                          // identity binding is validated
```

`WithStoredContext(v any)` JSON-binds context into the row. Code-flow context
validation is supplied to atomic `ConsumeCode` as an expected digest. Token
flows cannot know the user/context until the atomically deleted row is returned;
the caller validates the returned binding against the current identifier before
performing any business action. A mismatch is `ErrChallengeInvalid`; the row is
already consumed (anti-probing: a valid secret never survives wrong-context
replay).

**Two secret formats, two consume semantics** — pinned here because they
are mutually exclusive and conflating them breaks OTP (consultation
finding):

| format | space | on wrong secret | on correct secret | lockout |
|---|---|---|---|---|
| **code** (6-digit, OTP/sensitive-op) | 10^6 — retries expected | atomic `ConsumeCode`: attempts++; at `MaxChallengeAttempts = 5` the row is DELETED → `ErrTooManyAttempts` (+ event) | atomically consumed — exactly one concurrent success; context mismatch consumes | yes |
| **token** (256-bit URL secret, magic link) | unguessable | `ErrNotFound`-class generic failure, nothing counted; the empty-hash guard applies | consumed on redemption (single-use get-and-delete) | none (space is the defense) |

Codes are always resolved by `(user_id, purpose)` then HMAC-compared inside the
atomic repository operation — the composite rule. Magic-link tokens are resolved by digest
(purpose-scoped) and the row's stored context binds the identifier
`(kind, value)` the link was issued for; consumption validates that
binding. Secrets are generated by the package-private unconditional
random generator (the D9/D10 opaque-secret rule — never `Config.IDs`).

Store conformance includes two simultaneous correct code submissions (exactly
one success), two simultaneous token redemptions (exactly one success), races
at attempts 4→5, atomic replace, expired-delete, empty-digest rejection, and
purge batching on pgx, turso, reference memory, and `authmem`.

TTLs (constants this milestone, no Config knobs — deferral trigger: first
host demand): sensitive-op codes **15m**, OTP login codes **5m**, magic
link **15m**, reset token **1h** (all inside the survivor-systems 5m–1h
band; every surviving system hashes these at rest — GoTrue, Auth.js,
Kratos; better-auth's plaintext default is the outlier we do not follow).

Purposes (constants): `login_magic_link`, `login_otp`, `change_email`,
`change_phone`, `remove_password`, `unlink_oauth` — plus
`verify_registration` and `password_reset` if V8 ratifies the migration
below.

### 3.3 The OTP pepper — in-process HMAC, not a service

A six-digit code has only one million possibilities, so plain SHA-256 does not
protect it from a database reader. A **pepper** is a random server secret kept
outside the database. Gopernicus implements the protection in ordinary Go with
the standard library's `crypto/hmac` + `sha256`; there is no external pepper
service and no network call.

```go
type ChallengeProtector interface {
    ActiveKeyID() string
    DigestCode(keyID, userID, purpose, code string) (string, error)
    CandidateCodeDigests(userID, purpose, code string) ([]DigestCandidate, error)
    DigestToken(token string) string
}
```

The bundled implementation is constructed from a key ring:

```go
protector, err := auth.NewHMACChallengeProtector(auth.HMACKeyRing{
    Active: "2026-01",
    Keys: map[string][]byte{"2026-01": challengePepper}, // >= 32 random bytes
})
```

The host loads `challengePepper` from `AUTH_CHALLENGE_PEPPER` in development or
from its normal deployment secret store in production, just like the JWT
signing and AES-GCM keys. `Config.ChallengeProtector` is REQUIRED; nil or a key
under 32 bytes is a loud construction error. `protector_key_id` allows rotation:
new issues use the active key while `CandidateCodeDigests` supplies one candidate
per accepted key ID; the atomic store selects the candidate matching the row's
`protector_key_id`. Unexpired rows therefore remain verifiable with an old key
until their short TTL passes. HMAC input is domain-separated and binds user ID +
purpose + code. The pepper is never persisted, logged, placed
in security-event details, or reused as the JWT/encryption key. Random 256-bit
URL tokens remain SHA-256-digested because their entropy, not a pepper, protects
them.

### 3.4 Disposition of the legacy verification rail (open, V8)

Shipping challenges NEXT TO the frozen verification domain leaves the
feature with a split secret posture: reset tokens and registration codes
**plaintext at rest, keyed by value**, beside magic links and OTPs hashed
— permanent, if the freeze holds (nothing in the repo is tagged; the
freeze is a design ruling, not a release constraint). Recommended:
**migrate the two legacy flows (register-verify, forgot/reset) onto
`domain/challenge` and RETIRE `domain/verification`** — delete the
domain, remove the two ports from `Repositories`, and simply **delete the
two verification CREATE files from the canonical set** in B2's renumber
(no drop migration; the greenfield ruling removed the old cost). This is
**replacement, not widening** — the frozen ports are never touched, they
are removed whole — but it consciously reopens the auth-v2 §3 ruling and
is presented as an open row, not a silent choice. The payoff is one
secret model and attempt-lockout on flows that today have none.
**Retirement hygiene (delta gate):** the removal enumerates the
`VerificationCodes`/`VerificationTokens` storetest sub-runners AND the
`domain/verification` import in `storetest/storetest.go` (currently
~lines 39/89/95) — otherwise they orphan on domain deletion.
Alternative: two-rail coexistence with the plaintext posture explicitly owned
in the README. Recommended V12 staging gives this migration its own B3 gate.

## 4. Passwordless login — magic link + SMS OTP

### 4.1 Posture (open, V3): LOGIN-ONLY against existing VERIFIED identifiers

A passwordless start for an identifier that is unknown, inactive, not
login-enabled, or unverified reveals nothing. Resolution is
`Identifiers.GetLogin(kind, normalizedValue)` followed by current-row validation. No auto-provision (the Kratos
posture; GoTrue/better-auth auto-signup was considered — a future
`Config` knob is the named deferral). Signup remains register/invitation.
OAuth branch-2's pending-link gate remains the only "adopt an account
from a foreign proof" path, untouched. **Password login stays
EMAIL-ONLY** in v1 — phone is a passwordless-only identifier
(magic link / OTP); phone+password login is a named deferral (V10). This
keeps `Login`/`IssueToken` signatures untouched.

**Enumeration contract, pinned:** same status/body is necessary but not
sufficient. Start endpoints enqueue an opaque delivery command through §6 and
return the same 202/200 response after the same bounded local path whether the
identifier exists or not. Account resolution, challenge issuance, and provider
latency occur in the worker, never on the response path. A delivery failure is
logged/audited but never surfaced to the unauthenticated caller. Verify/redeem
failures are a single generic 401. Passwordless success mints through
**`mintSession`**.

### 4.2 Enablement seam (open, V4): `Config.Passwordless []string`

Kinds, not a bool — v3 validates against **{email, phone}** (any other string
→ loud construction error). Empty (default) → the passwordless routes are
**not registered** (deny-by-absence — there is no natural nil
collaborator for email magic link because `Mailer` is required, so the
knob is explicit). Each listed kind must have a delivery channel — email
via the required Mailer (or an email-kind Notifier), phone via a wired
phone-kind `notify.Notifier` — else **loud construction error**
(`ErrPasswordlessKindUnsupported`, the partial-wiring precedent). Listing a
kind permits active verified login-enabled identifiers of that kind as direct
methods under §5.6 policy. Production startup validates unsafe transitions (§8).

### 4.3 Routes and flows

- `POST /auth/passwordless/start` — `{identifier_kind, identifier,
  method?}`; `method ∈ {link, code}`, defaulting per kind (email → link,
  phone → code) but caller-selectable — the directive's "magic links
  deliverable to sms or email" is this knob (a link in an SMS body is
  legal). Resolves the VERIFIED identifier, issues the challenge
  (`login_magic_link` / `login_otp`, context = identifier ID + kind + normalized
  value), enqueues through §6. Always the same accepted response.
- `POST /auth/passwordless/verify` — `{identifier_kind, identifier,
  code}` → composite consume against the resolved user → `mintSession` →
  200 + cookies/pair (the OTP path).
- `POST /auth/passwordless/redeem` — `{token}` → single-use
  `RedeemToken`, the bound identifier is the login identity →
  `mintSession` → 200 + pair (the magic-link path). The optional HTML surface
  ships a bundled default landing page through `Views`; API-only hosts provide
  their own client. Hosts may override the default page without changing the
  POST redemption contract (§9.2).

Security events (pairs, the original's discipline): `passwordless_start`
(success/blocked) + `passwordless_login` (success/failure), details carry
kind + challenge purpose, **never the secret**.

### 4.4 Rate limits (open, V7)

Password login behavior is otherwise untouched, but all limiter keys stop
carrying raw PII. The service derives a stable keyed limiter digest from
`kind || normalized-value`; the passwordless surface uses kind-scoped keys:
`passwordless:<kind>:<identifier-digest>|<trusted-ip>` on verify, and the start
endpoints get BOTH a per-identifier budget (SMS/email flood protection
for the victim) and a per-IP budget (farming protection), through the
existing `Config.RateLimiter` port. Limits run before account resolution and
apply equally to unknown identifiers. Untrusted `X-Forwarded-For` is never used;
without configured trusted proxies the key uses `RemoteAddr`. The jwt-refresh
multi-instance caveat (a per-process memory limiter is N× budget) carries over.
`Config.IdentifierKeyer` supplies this HMAC under a distinct >=32-byte key
(`AUTH_IDENTIFIER_KEY`); it is required in production and must be shared across
instances. It is deliberately separate from the challenge pepper, JWT key, and
encryption keys. Development may use a per-boot key with a WARN.

## 5. The credential-management suite

The gopernicus-original salvage, improved. Server-side guards stay
authoritative on every mutation (the read surface is advisory — TOCTOU).

### 5.0 Live session versus recent authentication / step-up

`RequireLiveSession` proves revocation state; it does not prove that the human
recently presented an existing authenticator. Every authenticator/identifier
binding or removal below additionally requires a **recent authentication grant**
bound to the live session, user, intended operation, and relevant context
(provider or identifier-change ID). Default maximum age: 5 minutes.

```go
type AuthenticationGrant struct {
    SessionID, UserID, Purpose, ContextDigest string
    AuthenticatedAt, ExpiresAt time.Time
    Methods []AuthenticationMethod
    Assurance AssuranceLevel
}

RequireRecentAuthentication(purpose, context, maxAge)
BeginStepUp(purpose, context)
CompleteStepUpWithPassword(...)
CompleteStepUpWithIdentifierCode(...)
CompleteStepUpWithOAuth(...)
```

`authentication_grants` persists the grant ID, session/user/purpose/context
digest, methods/assurance, issued/expiry times, and consumed time. Its repository
offers atomic `Consume(sessionID, purpose, contextDigest, now)`; expiry,
single-use, session binding, and context mismatch are decided in that one
operation. Session rows also record `authenticated_at`, method descriptors, and
assurance for the recent-primary-login shortcut. Revoking a session cascades or
invalidates its grants.

Grants are server-side, single-purpose, short-lived, and consumed by the
mutation. The user reauthenticates with an **existing** enrolled method; proving
the proposed new email/phone is a separate binding proof and cannot satisfy the
step-up by itself. Successful primary login records its method/time/assurance on
the session so a sufficiently recent login can satisfy the grant without an
extra prompt. Password reset/recovery never silently counts as high assurance.

This abstraction is intentionally broader than v3. Auth-v4 MFA adds passkey,
TOTP, recovery-code, and multi-factor grant producers without changing the v3
mutation contracts (§12.1).

### 5.1 `GET /auth/methods` — one read surface

Live-session-gated (`RequireLiveSession`) because it returns sensitive contact
and credential inventory. Replaces the original's two round trips
(`/auth/me` has_password + `/auth/oauth/linked`) with one:

```json
{
  "has_password": true,
  "oauth": [{"provider": "google", "linked_at": "…", "assurance": "aal1", "removable": true}],
  "identifiers": [
    {"id": "…", "kind": "email", "value": "masked-by-default", "verified_at": "…",
     "uses": ["login", "recovery", "notification"], "primary": true, "removable": false}
  ]
}
```

Hosts may explicitly request/display the full value after live-session policy;
the default JSON projection masks it. Each entry carries use/assurance /
`removable` hints computed from §5.6 policy so hosts
can pre-disable removal UI — advisory only; the server-side guard stays
authoritative. `GET /auth/oauth/linked` is subsumed and removed (pre-tag
route break, named in the upgrade note).

### 5.2 Set initial password — the original's named gap, closed

`POST /auth/password/set` — **`RequireLiveSession` + recent-authentication
grant (`set_password`)**,
`{new_password}`, 409 `password_already_set` when one exists. OAuth-only
users no longer abuse the reset flow to gain a password. Mints a fresh
pair (a new credential class appeared; same posture as change-password).

### 5.3 Code-gated remove-password (AV7 reversal, part 1)

`POST /auth/password/remove/start` (RequireLiveSession; begins/fulfills a
recent-authentication grant and issues a
`remove_password` code, delivered to a verified recovery identifier selected by
policy — §6; a delivery
failure here SURFACES, §6.1) → `POST /auth/password/remove` `{code}`
(RequireLiveSession; consumes, guarded by §5.6, deletes the password,
**invalidates any pending reset token** — the original's rule — and
revokes all sessions + remints for the caller, the ChangePassword
posture). Security-event pair: `password_remove_code_sent` +
`password_removed`.

### 5.4 Code-gated unlink-OAuth (AV7 reversal, part 2)

`POST /auth/oauth/{provider}/unlink/start` →
`POST /auth/oauth/{provider}/unlink` `{code}` — both RequireLiveSession.
The code's **StoredContext binds the provider**: a code issued for Google
cannot unlink GitHub — context mismatch consumes the code and fails
(§3.2). The mutation consumes a provider-bound `unlink_oauth` recent-auth grant.
Replaces the plain session-gated `DELETE /auth/oauth/{provider}/link`
(route break, upgrade note). Guarded by §5.6. Event pair:
`oauth_unlink_code_sent` + `oauth_unlinked`.

### 5.5 Identifier management

All require `RequireLiveSession` plus an operation-bound recent-authentication
grant using an existing method (§5.0). Every add/change flow is the §2.4 pair:
a `contactchange` row holding the pending normalized value and requested uses,
plus a challenge delivered to the new address. Start order is pending row →
challenge; confirm order is atomic challenge consumption → pending consumption
→ atomic `ApplyVerifiedChange`.

- **Add/change phone** — `POST /auth/identifiers/phone` `{phone, uses,
  make_primary}`; no phone notifier → `ErrKindNotSupported`. Confirm through
  `POST /auth/identifiers/phone/confirm` `{code}`. Claim-at-apply lets the
  partial unique authentication-claim index arbitrate a lost race; collision is the generic
  `ErrAlreadyExists`. Multiple non-primary notification identifiers may exist.
- **Add/change email** — the corresponding email routes deliver proof to the
  new address, apply only at confirmation, and perform no start-time existence
  lookup. Collision timing therefore reveals nothing. A notice is enqueued to
  each previously verified independent recovery/notification channel.
- **Remove/change uses** — `DELETE /auth/identifiers/{id}` and
  `PATCH /auth/identifiers/{id}` are evaluated by §5.6. Removing a primary
  requires choosing a replacement in the same atomic operation. The row is
  retired (`replaced_at`), not hard-deleted on the request path.

Every successful binding sends an independent notification through a previously
verified channel that was not used to prove the new identifier. The notice
contains time, IP/device context, and a host-owned redress URL. High-risk hosts
may configure an activation delay during which the old primary remains a
recovery path and can cancel the change.

**Rate budgets (delta gate):** these start endpoints deliver secrets to
CALLER-SUPPLIED addresses — authenticated, but a victim-address flood
amplifier otherwise, and the §4.4 passwordless budgets do not cover
them. Each start gets a per-target-address AND a per-user budget through
the existing `Config.RateLimiter`.

Events: `phone_change_code_sent` / `phone_changed` / `phone_removed`,
`email_change_code_sent` / `email_changed`.

### 5.6 Credential-mutation policy, not method counting

Passwords, OAuth, magic links, SMS, and future passkeys do not have equivalent
assurance. A scalar `authenticationMethodCount` is rejected. ONE policy
evaluator receives the current and proposed sets:

```go
type CredentialPolicy interface {
    EvaluateMutation(ctx context.Context, current, proposed MethodSet) error
}
```

The bundled safe default requires at least one direct login method and one
verified recovery method, rejects a recovery set consisting only of the
identifier being removed, and marks PSTN/SMS restricted rather than
phishing-resistant. Hosts may require two independent recovery methods, a
non-PSTN method, or a minimum assurance. Remove-password, unlink-OAuth, and
identifier remove/use-change all call this evaluator immediately before their
atomic mutation; `/auth/methods` hints remain advisory.

`users.auth_revision` supplies optimistic serialization. Reading the current
`MethodSet` returns its revision; the same store adapter applies the typed
credential/identifier mutation and increments the revision in one transaction
only if the expected revision still matches. A conflict reloads/re-evaluates;
it never proceeds on a stale safe-looking set. This closes concurrent
self-removal without requiring the policy implementation to run inside SQL.

```go
type CredentialMutationRepository interface {
    Snapshot(ctx context.Context, userID string) (MethodSet, error)
    Apply(ctx context.Context, userID string, expectedAuthRevision int64,
        mutation CredentialMutation) error
}
```

`CredentialMutation` is a closed v3 sum type (remove password, unlink OAuth,
retire/change identifier uses). It coordinates typed tables without turning
them into a uniform storage model. `Apply` returns `sdk.ErrConflict` on revision
mismatch; the service reloads, re-runs policy, and retries a bounded number of
times.

Email password reset is explicit policy, not a hidden permanent rail.
`Config.PasswordRecovery` names enabled verified identifier kinds; the v3 zero
value means email for compatibility, while an explicit disabled mode turns
self-serve password recovery off. Strict production validation (§8) compares
configuration transitions with a repository method summary and rejects a
rollout that would strand users.

### 5.7 Adoption-revocation (open, V5)

The linking rule distilled from better-auth's 2026 CVEs: when a
proof-of-address event **adopts** an account whose matched identifier was
UNVERIFIED at flow start, revoke the account's pre-existing passwords AND
sessions first — a squatter who pre-registered the victim's address with
a password must not retain access after the true address owner completes
a pending link. Application in this design: the OAuth `verify-link`
completion path, when the branch-2 identifier match on normalized provider
email hit a login claim whose `verified_at` was NULL. **The
unverified-at-flow-start fact is
CAPTURED at branch-2 time into the persisted pending-link payload (the
oauthstate row) and read back verbatim at verify-link — never re-derived
at completion** (lead-backend amendment: verification state can change
between start and completion; re-deriving is a TOCTOU). Explicitly
EXEMPT: self-serve register→verify (the user proving their own address)
and §5.5's confirm of a self-initiated change. The mailed pending-link
secret goes to the ADDRESS, so completion is address-possession proof
regardless — this revocation rule is what makes the unverified-match arm
safe.

OAuth matching/adoption is permitted only when the provider explicitly asserts
the email claim as verified and the integration maps that assertion; a provider
email string without verified provenance never auto-matches, registers, or
adopts an existing identifier.

### 5.8 Error-code discipline (recorded, R11)

The original's stable machine codes, kept: `challenge_expired` 410,
`challenge_invalid` 400, `too_many_attempts` 403,
`cannot_remove_last_method` 409, `password_not_set` 404,
`password_already_set` 409. Login and passwordless verify/redeem stay a
single generic 401; the start endpoints stay silent-success (enumeration
protection, both directions).

### 5.9 Password and recovery hardening

V3 updates the existing password contract rather than leaving it at the current
eight-byte minimum: single-factor passwords require at least 15 Unicode code
points, the maximum accepted length is at least 64, arbitrary composition and
periodic-rotation rules are forbidden, and a host-injected compromised-password
blocklist/checker is supported. Password hashing remains behind
`PasswordHasher`; inputs are length-bounded before expensive hashing.

Successful password reset uses a narrow `domain/passwordreset.Repository`
composition operation that, in one same-adapter transaction, redeems/deletes the
`password_reset` challenge, sets the typed password row, and revokes **all**
sessions and outstanding password/reset grants. This is the freeze-transfer
rule applied correctly: reset has cross-aggregate consume semantics, so the
generic challenge port is not widened with a callback. It never automatically
logs the caller in. An independent notification is enqueued, and the next
sensitive action requires fresh step-up. Transaction rollback and injected
failure tests prove there is no changed-password/live-old-session partial state.

## 6. Outbound unification

### 6.1 One deliver seam (recorded, R4)

`invitationsvc.deliver` (kind fork: email → Mailer or email-kind Notifier;
other kinds → their Notifier; unsupported → loud error) is today the only
kind-aware send path; `authsvc` still hand-builds `email.Message` in three
places (`sendVerificationEmail`, `sendResetEmail`, `sendPendingLinkEmail`)
— the documented asymmetry. Ruling: the fork moves to a small shared
internal package — **`internal/logic/delivery`** — consumed by BOTH
services (they cannot share by importing each other; two copies would
drift — consultation finding).

**Dependency direction, pinned (steward amendment):** services →
`delivery`, one-way; `delivery` imports sdk ports only (notify, email,
logging) and is constructor-injected into both services — never a
registry. It lives under `internal/logic/` because it **owns the
deny-by-absence kind policy** — a business rule, not transport plumbing.
`Config.Notifiers`' documented scope widens from "invitation delivery
only" to ALL auth outbound; the kind predicate (email always-on via the
required Mailer) is unchanged and becomes the single definition §4.2 and
invitations both consume.

### 6.1.1 Durable asynchronous delivery and enumeration resistance

Auth v3 adds a small `delivery_jobs` outbox and `delivery.Repository`. Public
unauthenticated starts perform only normalization, rate limiting, and enqueueing
of an opaque request; the worker performs account resolution, challenge
replacement, rendering, and provider delivery. Known and unknown identifiers
therefore have the same bounded request path and provider latency cannot become
an enumeration signal.

Jobs contain the minimum necessary PII and their destination/rendered-secret
payload is always encrypted through a required `DeliveryEncrypter`; they have
bounded retry/backoff, idempotency keys,
`available_at`, attempt count, and terminal status. Raw secrets never appear in
job/audit error strings. Terminal rows are purged under a documented retention
policy. The worker issues/renders a challenge once when the job first becomes
deliverable; retries resend the same protected message idempotently rather than
minting a chain of invalid links. A terminal send failure deletes that
challenge. A new user-requested resend replaces the prior challenge/job and
observes a cooldown and per-identifier send budget.

Session-gated starts also enqueue, but may return a job receipt and expose a
session-gated status endpoint so the authenticated caller can learn that
delivery failed without holding the request open. A bounded provider context is
still required, and notifier implementations contractually honor cancellation.

### 6.2 Email content — `email.TemplateRegistry` adopted

The sdk `email` capability (TemplateRegistry + Emailer; layouts
transactional/marketing/minimal; layers Infra < Core < App) is fully built
with ZERO consumers. This milestone adopts it for **all auth email
content**: the feature registers its default templates
(verification code, reset, pending-link, magic link, sensitive-op codes,
email-change code + old-address notice, invitation + member-added) at
`LayerCore`; hosts override at `LayerApp` (one override is demonstrated
live in B8). The three hand-built `email.Message` sites are deleted in
the move.

### 6.3 SMS content and the console stub (recorded, R1)

SMS is **console-stub only** this milestone: `notify.NewConsole(
identity.KindPhone, log)` already exists and is the wired channel — no
twilio, no `integrations/notify/<tech>`. SMS content is **body-only plain
text** (short templates in-core, `fmt`-level): `notify.Message` carries
Subject+Body but SMS has no subject, and rendering email-layout HTML into
an SMS body is a category error — the TemplateRegistry is email's, not
SMS's (consultation finding). A magic link over SMS is the URL in the
body.

**Console delivery is a DEV transport (SRE amendment):** serving either email
or another kind through a console sender/notifier in production **leaks OTPs and
magic links to logs**. Both delivery port families expose optional capability
metadata (`TransportSecurity`, `DevelopmentOnly`) implemented by their bundled
console transports rather than relying on concrete-type checks.
`Config.RuntimeMode="production"` rejects a development-only transport or one
that does not declare metadata; development mode emits a startup WARN. The first production host
MUST wire a real transport — that is
exactly where the deferred `integrations/notify/<tech>` trigger (first
host wiring real SMS; segovia likely) fires.

### 6.4 Magic-link construction and browser handling

Links are built only from required `Config.PublicAuthBaseURL`, validated as an
absolute HTTPS URL in production; request `Host`/forwarded headers never
participate. Redirect targets are exact-match allowlisted. The host landing page
receives the token in the URL fragment where supported, immediately removes it
from browser history, and POSTs it to redeem. It sends
`Referrer-Policy: no-referrer`, `Cache-Control: no-store`, and a restrictive CSP.
No GET consumes a token, so link scanners cannot authenticate a user merely by
fetching the URL. Mobile/deep-link variants must preserve the same POST and
allowlist contract.

## 7. The re-key map

What changes (the B5 checklist):

| site | today | becomes |
|---|---|---|
| `users` schema | email/verification columns | stable subject/profile only; addresses move to `user_identifiers` (§2.1) |
| repositories | `UserRepository.GetByEmail/Create` | atomic `CreateWithPrimaryIdentifier` + `IdentifierRepository` (§2.2) |
| Register / Login / IssueToken / ForgotPassword | email lookup on users | normalize then identifier lookup; password login remains email-only in v3 |
| limiter keys | raw normalized email + spoofable-IP fallback | keyed identifier digest + trusted-proxy/RemoteAddr IP (§4.4) |
| `sendVerificationEmail` / `sendResetEmail` / `sendPendingLinkEmail` | hand-built `email.Message`, Mailer-direct | deleted; `internal/logic/delivery` + TemplateRegistry (§6) |
| OAuth branch-2 (`oauth.go:192–205`) + verify-link | `GetByEmail` match; no captured state | identifier match + captured identifier ID/unverified-at-start flag (§5.7/V5) |
| `resolver.go` | one hardcoded KindEmail address | all active verified identifiers permitted for identity projection |
| authsvc→invitationsvc identity port | single email | kind-aware active verified-identifier accessor |
| invitation accept (non-email kinds) | session + token only ("until address verification lands") | **iff V11**: phone-kind invitations gain ACCEPT-TIME account-match against the caller's VERIFIED phone — nothing else changes; `Mine`, auto-accept, and login-time resolution stay email-only. **Dependency pinned (delta gate):** the match silently never fires unless phone-kind invitation identifiers are normalized strict-E.164 AT CREATE — the `normalizeIdentifier` convergence (§2.2) supplies exactly that, via a new (legal, ordinary service→domain) `invitationsvc → domain/user` import edge |
| `ListBySubject` stores (pgx `invitations.go:161–178` + turso twin) | `WHERE identifier = ?` — **no kind filter** (latent cross-kind collision) | port gains the kind parameter; both stores filter `(identifier_kind, identifier)`; storetest case added; rides B5. A plain `(identifier_kind, identifier)` index is added to canonical 0011 (planner call, stated: the fixed query filters both columns and no covering index exists — the pending-tuple unique index leads with resource columns and cannot serve it) |
| `Config.RequireVerifiedEmail` | email-column-specific | compatibility alias over email identifier policy; deprecate after v3 |
| register-verify / reset flows | `verification.Code`/`Token` (plaintext at rest) | `domain/challenge` if V8 ratifies; otherwise unchanged, posture documented |
| sensitive mutations | live session | live session + consumed recent-authentication grant (§5.0) |
| password reset | sets password only | consumes atomically, revokes sessions/grants, no automatic login (§5.9) |
| outbound | synchronous direct send | durable enumeration-safe outbox worker (§6.1.1) |

The invitation `ListBySubject` kind-filter bug and its composite lookup index
remain part of B5. `Mine` and accept-time matching now use the same normalized
identifier repository instead of email-only helper ports.

## 8. Config + Repositories additions (charter item 12)

`Repositories` adds required `Identifiers`, `CredentialMutations`, `Challenges`,
`PasswordResets`, `ContactChanges`, `AuthenticationGrants`, and `DeliveryJobs`; it removes verification-code/token
ports when V8 lands. User + identifier atomic operations and credential-policy
mutations must be backed by a common transaction-capable adapter. Loud
construction errors apply to required ports.

| Config field | nil/zero means |
|---|---|
| `Passwordless []string` | **empty → passwordless OFF**. Kinds are {email, phone}; each requires a delivery channel. The credential policy evaluates only active, verified, login-enabled identifiers whose kind is enabled. Strict production validation rejects a transition that would strand users (§5.6). |
| `PasswordRecovery` | zero → email recovery compatibility default; explicit disabled or kind policy supported (§5.6). |
| `ChallengeProtector` | **REQUIRED**; HMAC key ring supplied by host; nil/short key → construction error (§3.3). |
| `IdentifierNormalizer` | nil → documented strict defaults (§2.2); one injected policy used everywhere. |
| `IdentifierKeyer` | production-required stable HMAC keyer for PII-free limiter/idempotency keys; separate key from challenge/JWT/encryption. |
| `CredentialPolicy` | nil → bundled safe default (§5.6). |
| `PublicAuthBaseURL` | required when any link flow is enabled; production requires HTTPS (§6.4). |
| `DeliveryEncrypter` | **REQUIRED** because retryable jobs temporarily carry rendered secrets/PII; bundled AES-GCM satisfies it with a distinct key. |
| `RuntimeMode string` | **required enum** `development`/`production`; empty/unknown is an error so a host cannot accidentally inherit dev posture. Production rejects console transports, insecure public URLs/cookies, non-durable limiter, untrusted proxy ambiguity, and missing audit repo. |
| `RequireVerifiedEmail bool` | compatibility alias; false default; deprecated after v3 in favor of identifier policy. |
| `Notifiers []notify.Notifier` | widened to all auth outbound; development-only capability rejected in production (§6.3). |
| `Views Views` | nil → HTML surface absent and shared POST routes accept JSON only; non-nil → HTML GET pages + form handling mounted alongside the unchanged JSON API (§9.2). The core never imports templ. |

No challenge TTL knobs this milestone. The feature core stays sdk-only.
Startup validation may query a repository summary in strict production mode to
detect configuration changes that would strand existing accounts.

## 9. Route surface additions (inside the claimed `/auth/*` namespace)

New: `POST /auth/passwordless/{start,verify,redeem}` (§4.3; registered
only when `Passwordless` non-empty) · `GET /auth/methods` ·
`POST /auth/password/set` · `POST /auth/password/remove/start` +
`POST /auth/password/remove` · `POST /auth/oauth/{provider}/unlink/start`
+ `POST /auth/oauth/{provider}/unlink` · identifier add/confirm/PATCH/DELETE
under `/auth/identifiers/*` (§5.5) · step-up start/complete routes (§5.0) ·
session-gated delivery-job status.
Removed (pre-tag breaks, upgrade note): `GET /auth/oauth/linked`
(subsumed by `/auth/methods`), `DELETE /auth/oauth/{provider}/link`
(replaced by the code-gated pair). Gating tiers per route as specified in
§5; sensitive mutations require live session + operation-bound recent-auth
grant.

### 9.1 HTTP security contract

Every cookie-authenticated mutation validates an allowlisted `Origin` (and
`Sec-Fetch-Site` where present) and uses the framework CSRF token middleware;
SameSite cookies are defense-in-depth, not the sole control. JSON routes require
`Content-Type: application/json`, reject unknown/trailing fields, and apply a
small `http.MaxBytesReader` limit before decoding. CORS never combines wildcard
origins with credentials. Trusted-proxy configuration is the only source of
forwarded client IPs. All auth responses containing secrets or method inventory
set `Cache-Control: no-store`.

### 9.2 Dual JSON + HTML transport and the default view module

Authentication supports two parallel inbound adapters over the same service
methods:

- the existing JSON API contracts remain at `/auth/*`; and
- when `Config.Views != nil`, normal HTML GET pages and form submissions are
  mounted for registration, verification, login, forgot/reset, passwordless,
  step-up, method/identifier management, password management, OAuth unlink, job
  status, and errors.

The core defines a technology-neutral `Views` port whose methods accept exported
view models and return `web.Renderer`. The bundled default lives in the sibling
module `features/authentication/views/templ`, exactly like CMS FS3: the auth
feature remains sdk-only, hosts wire `authtempl.New()`, and the blessed override
path is embedding `templ.Views` and overriding individual methods. A host may
instead implement the port with `html/template` via `web.Template`.

There are no duplicate POST route registrations. Canonical mutation endpoints
use one transport dispatcher: `application/json` keeps the existing JSON DTO /
response contract; `application/x-www-form-urlencoded` or multipart form input
is accepted only when Views is wired and follows HTML render/redirect behavior.
Unsupported content types return 415. HTML successes use 303 Post/Redirect/Get;
validation failures re-render a form with safe field values and generic auth
errors. Passwords, OTPs, tokens, and peppers are never echoed. `Accept` does not
silently reinterpret a JSON request body.

Default HTML coverage includes login, register, registration verification,
forgot/reset password, passwordless start/code verify/magic-link landing,
check-delivery, step-up, account-security/method inventory, identifier add/edit,
set/change/remove password, OAuth unlink confirmation, and generic status/error
pages. View models carry CSRF tokens, CSP nonce where needed, masked identifiers,
return-to values already validated by the redirect allowlist, and accessible
field errors. The magic-link page reads a fragment token, scrubs browser history,
and POSTs redemption; a visible manual action remains for no-auto-submit/CSP
failure.

HTML defaults are secure and deliberately plain: semantic labels, appropriate
autocomplete values, keyboard/focus behavior, no third-party assets/analytics,
no-store/no-referrer headers, restrictive CSP, generic enumeration-resistant
copy, and no credential values in URLs. Host overrides change presentation, not
route security, decoding, service policy, or error classification.

## 10. Schema / store impact summary

**Canonical sets (§2.5):** B2 reshapes users, adds `user_identifiers`,
`challenges`, `contact_changes`, authentication grants, and delivery jobs,
deletes verification tables under V8, adds the invitation composite index, and
renumbers. The host upgrade reference includes backfill/validation and the
SQLite rebuild path; it is not described as purely additive.

Four implementations change (the jwt-refresh §2.4 lesson — there are
four, not two): `stores/turso`, `stores/pgx`, the storetest reference
impl, and `examples/auth-cms/internal/authmem`. `storetest` grows/updates
sub-runners: **user+identifier** (atomic create/rollback, use flags,
multiple/shared notification identifiers, login uniqueness, primary uniqueness,
concurrent claim and apply-change), **challenge** (atomic replace/consume,
concurrent one-winner code/token redemption, lockout race, HMAC key ID,
empty-digest guard, purge), **contactchange**
(delete-before-create per (user, kind); single-use `Consume`; expired →
ErrExpired with the row gone; absent → ErrNotFound), **invitations**
(`ListBySubject` kind filter), **authentication grants** (purpose/context/session
binding, expiry, single use), and **delivery jobs** (claim/retry/idempotency,
terminal purge). Iff V8 ratifies, the
`VerificationCodes`/`VerificationTokens` sub-runners and the
`domain/verification` import in `storetest.go` are removed with the
domain (§3.4 hygiene). Milestone close gates on
recorded live runs per dialect (charter item 11) — **against fresh/reset
databases** (the stale-checksum caveat, §2.5). Breaking version bump for
the feature, both nested store modules, AND the new sibling templ-view module
(RELEASING.md precedent).

## 11. Host carry

`examples/auth-cms` is the proof host (its phase is B8, XL with four
named sub-gates — PM amendment): **(a) identifier legs** — add primary and
secondary phone/email identifiers, prove the new address, observe independent
old-channel notice, exercise recent-auth grants, policy-refused removal, shared
notification-only phone, and concurrent login-claim collision;
**(b) passwordless legs** — email magic link end-to-end through the bundled
default/host-overridable landing page, SMS OTP login via
the console notifier, the console-dev startup WARN and production rejection
observed; prove same-timing enqueue shape for known/unknown starts; **(c)
credential legs** — code-gated remove-password, provider-bound unlink (a
wrong-provider code fails AND is consumed), set-password, register→login
intact; **(d) dual-transport/view legs** — every public/account journey through
JSON and normal HTML where applicable, API-only nil-Views proof, bundled
`views/templ`, 303 PRG/form security, fragment landing, and a real partial page
override by embedding default Views — plus a separate email `LayerApp` template
override. README curl/browser transcripts per leg — green tests alone do not
close it. **segovia is OUT
of scope** (the jwt-refresh §8 pattern): its carry — the additive upgrade
reference applied as its own migrations (the 0018 pattern), `.env`,
authpages against the changed routes — is a named follow-on, cut after
upstream lands. The proof host also demonstrates `AUTH_CHALLENGE_PEPPER`, key-ID
rotation across one unexpired challenge, secure public-base URL construction,
CSRF/origin denial, reset-driven session revocation, delivery retry/purge, and
the masked `/auth/methods` response.

## 12. Non-goals

- No real SMS/provider integrations (`integrations/notify/<tech>` —
  trigger: the first host wiring real SMS; segovia likely). Console only,
  and console is a DEV transport — production use leaks secrets to logs
  (§6.3; production construction rejects it).
- No backup columns: multiple identifiers per kind are native to
  `user_identifiers` (§2.1).
- No phone+password login (phone is passwordless-only in v3; V10). Additional
  identifier kinds require an explicit schema/vocabulary revision; the table
  shape avoids another users-table rewrite.
- No MFA method implementation in v3. Passkeys/WebAuthn, TOTP, recovery codes,
  factor replacement, and AAL2 policy are the named **auth-v4 add-on** (§12.1).
  V3 does build the method/assurance, step-up-grant, lifecycle-event, and
  credential-policy seams v4 consumes. Account lockout remains risk-policy work;
  per-secret attempt limits and distributed rate limiting ship now.
- No microsoft/apple OAuth providers — the `oauth.Provider` port is
  already extensible; new providers are integrations on demand.
- No passwordless auto-provision. Identifier removal is policy-controlled.
- No libphonenumber-grade phone validation in core (§2.2; the override
  port is the named deferral). No TTL Config knobs (§3.2).
- No upgrade migrations in the canonical set, ever (§2.5's ruling).
- No uniform accounts table, ever (R6). No tenancy. No segovia carry
  (§11). The sdk changes are narrow delivery transport-security metadata and
  existing `web.Renderer` consumption; no new `Mount` fields. Templ stays in the
  sibling authentication view module, never the feature core.

### 12.1 Auth-v4 MFA reserved design space

Auth-v4 adds a typed `authenticators` domain/table, not rows in
`user_identifiers` and not a uniform credential table. Initial types are
WebAuthn/passkeys, TOTP, and one-time recovery codes. Each authenticator records
type, assurance properties (factor category, phishing resistance, replay
resistance), bound/last-used/revoked timestamps, display metadata, and type-
specific protected material in typed storage.

V3 reserves the following stable seams for it:

- `AuthenticationMethod` + `AssuranceLevel` on sessions and recent-auth grants.
- `CredentialPolicy` over current/proposed method sets, not scalar counts.
- Authenticator lifecycle security events and independent binding notices.
- Operation/context-bound step-up grants with pluggable completion methods.
- `user_identifiers` as discovery/delivery/recovery addresses only; proving an
  email/SMS address never masquerades as a passkey/TOTP record.

V4 must add recovery-code issuance/rotation, factor reset/redress, multiple
authenticators, AAL2 reauthentication, passkey RP-ID/origin policy, TOTP secret
encryption, and downgrade-resistant factor replacement. No v3 route or port may
assume that one method equals one assurance unit.

## 13. Decision table

### Recorded — derivations, directives, and consultation/gate outcomes (not open for re-decision)

| # | decision | recorded position | source |
|---|---|---|---|
| R1 | SMS delivery | console stub only; development warns, production rejects it (§6.3) | owner directive + security rewrite |
| R2 | challenge placement + closure | `domain/challenge`; repository-atomic replace/code-consume/token-consume; third format/different semantics/payload → own domain | auth-v2 ruling + concurrency review |
| R3 | session minting | passwordless mints through the single `mintSession` path; JWT + refresh contract untouched | jwt-refresh D1–D8 govern |
| R4 | outbound seam | shared `delivery` package + durable outbox worker; unauthenticated starts never synchronously resolve/send; bounded cancellable providers | security review + prior delivery ruling |
| R5 | secret posture | 256-bit tokens → SHA-256; short codes → HMAC-SHA-256 with required external-to-DB pepper/key ring; atomic single use | offline-guessing + concurrency review |
| R6 | credential tables | typed `user_passwords`/`oauth_accounts` stay; uniform accounts table rejected | better-auth CVE-2026-53516 + GHSA-qq9h-g4jm-xgf3 lived in uniform linking |
| R7 | user+identifier atomicity | `CreateWithPrimaryIdentifier` and `ApplyVerifiedChange` are same-adapter transactional operations; memory implementations use one mutex | owner `user_identifiers` amendment |
| R8 | mutation safety | one `CredentialPolicy` evaluates typed current/proposed methods; no scalar count; sensitive binding/removal also consumes recent-auth grant | assurance review |
| R9 | normalization home | `domain/identifier` + injected policy used by auth/invitations/rate limits; strict E.164 default and explicit email/IDNA behavior | framework review |
| R10 | invitation plumbing | kind-filter bug/index fixed; invitation matching uses active verified `user_identifiers` | identity rewrite |
| R11 | error codes | stable machine codes; login/verify generic; starts return indistinguishable accepted response after enqueue | enumeration review |
| R12 | dual transport/views | JSON API remains stable; optional HTML uses one content-type dispatcher on shared POST routes, a core `Views`/`web.Renderer` port, and bundled sibling `views/templ`; nil Views = API-only; embedding default Views is the partial-override path | owner clarification + feature FS3 precedent |

### Ratified implementation decisions

| # | decision | recommended default | notes / alternative |
|---|---|---|---|
| V1 | identity model | **`user_identifiers`**; v3 kinds {email, phone}, multiple per kind, explicit login/recovery/notification uses; typed credentials stay separate | owner amendment 2026-07-12 |
| V2 | uniqueness / shared addresses | unique for active login- or recovery-enabled claims; shared notification-only addresses allowed; no automatic abandoned-claim eviction | §2.1–2.3 |
| V3 | passwordless posture | LOGIN-ONLY against existing VERIFIED identifiers; no auto-provision; starts enqueue indistinguishably; **password login stays email-only** | Kratos posture; phone+password deferred. TTLs: link 15m, OTP 5m |
| V4 | passwordless enablement | kind-granular deny-by-absence + startup transition validation under production profile | §4.2/§8 |
| V5 | adoption-revocation | ADOPT using captured identifier ID + unverified-at-start flag; revoke passwords/sessions before adoption | takeover defense |
| V6 | verified-login knob | keep `RequireVerifiedEmail` as a deprecated compatibility alias for email-identifier policy; passwordless always requires verified identifiers | avoids an unnecessary simultaneous API rename while removing column coupling |
| V7 | rate-limit keys | keyed identifier digest + trusted IP; per-identifier and per-IP; before resolution; shared durable limiter required by production profile | §4.4 |
| V8 | legacy verification rail | MIGRATE register-verify + forgot/reset onto atomic `domain/challenge`; RETIRE plaintext verification ports/tables | replacement, not widening; B3 owns the larger migration gate |
| V9 | registration shape | **email-only public signature**; internally atomically creates user + primary email identifier | optional phone registration duplicates the verified-change flow; phone-first needs deferred policy |
| V10 | deferral bundle | MFA implementation becomes named **auth-v4**; v3 reserves assurance/step-up/policy seams. Defer phone+password, providers, and real SMS integrations | §12/§12.1 |
| V11 | invitation trust-model extension | ADOPT via kind-aware verified identifier accessor and shared normalization | §7 |
| V12 | milestone severance | **SPLIT INTERNAL GATES**: security foundations first, then identity/credential flows, then passwordless/proof host. One eventual v3 release is allowed only after every gate closes | security rewrite enlarged invariants |
| V13 | recent authentication | ADOPT operation/context-bound, single-use grants for all authenticator/identifier binding and removal | §5.0 |
| V14 | outbound timing | ADOPT durable asynchronous outbox for unauthenticated starts; no synchronous provider call on response path | §6.1.1 |
| V15 | production profile | ADOPT fail-closed validation for console transports, HTTPS/cookies, trusted IP, durable limiter/audit, and outbox encryption | §8 |
| V16 | HTML defaults | ADOPT full default auth HTML surface in sibling templ module, wired explicitly by host and overridable per view; HTML and JSON call the same services/security controls | §9.2 |

## 14. Rough phase breakdown

**Executor model policy (standing, from auth-v1):** implementation phases
run on `model: opus`; design/doc-judgment phases on `model: fable`; never
sonnet. Verification gate for every phase: `make check` (per-module
build/vet/test + layering guards) + dual-dialect storetest green;
milestone close additionally requires recorded live runs per dialect
(charter item 11, **fresh/reset DBs** per §2.5) and the B8 run-and-look
transcripts.

| phase | what | size | depends on | model |
|---|---|---|---|---|
| B0 | Security contracts first: atomic challenge API/storetests, HMAC key ring, recent-auth grants, credential-policy types, CSRF/origin/body-limit middleware, production-profile validation | L | — | opus |
| B1 | `domain/identifier` + `user_identifiers`; atomic user+primary creation and verified apply-change; normalizer; contactchange; spec-first conformance across four implementations | L | B0 | opus |
| B2 | Canonical migrations for reshaped users, identifiers, challenges, grants, contact changes, delivery jobs; dual dialects; validated host backfill/rebuild runbook | L | B1 | opus |
| B3 | Challenge service on repository-atomic consume + protector rotation; migrate/retire verification rail; password-reset revocation and password-policy hardening | L | B0, B2 | opus |
| B4 | Durable delivery outbox/worker, templates, notifier capability metadata, production console rejection, purge/retry/idempotency tests | L | B0, B2 | opus |
| B5 | Re-key register/login/OAuth/resolver/invitations to identifiers; adoption flag; limiter privacy/trusted IP; magic-link URL contract | L | B1–B4 | opus |
| B6 | Credential suite: masked methods, recent-auth step-up, policy-evaluated password/OAuth/identifier mutations, independent notices/redress | L | B3–B5 | opus |
| B7 | Passwordless routes on asynchronous starts, atomic verify/redeem, identifier revalidation, distributed budgets, mintSession | M | B3–B5 | opus |
| B8 | HTML + proof host: public `Views` port/models; sibling `features/authentication/views/templ` module; dual JSON/form dispatch + GET/PRG handlers; default accessible/no-store/no-referrer/CSP pages; partial override proof; then §11 pepper/concurrency/timing/CSRF/production/reset run-and-look gates | XL | B6, B7 | opus |
| B9 | Docs: JSON + HTML route/config/override guide, complete port/break inventory, destructive-backfill upgrade runbook, production checklist, PII retention/purge, auth-v4 MFA seam, RELEASING/NOTES evidence | M | all | fable |

Sequencing follows the explicit gates above. B0 cannot be bypassed: identity or
passwordless work does not begin against the old non-atomic challenge/recent-
auth assumptions. B3/B4 may parallelize after B2; B6/B7 after B5. One v3 tag may
still ship the complete result, but no single implementation gate owns the
whole security surface. V11 remains the clean pressure valve; atomic challenge,
pepper, reset revocation, recent-auth, and enumeration-safe delivery are not.

## 15. Risks

1. **Channel compromise:** mailbox/SIM control becomes an AAL1 login method.
   Mitigations: explicit uses, restricted-SMS classification, recent-auth for
   binding, independent notices, atomic short-lived challenges, and future v4
   phishing-resistant methods.
2. **Pepper loss or mismatch:** losing the active key invalidates outstanding
   codes; inconsistent keys break multi-instance verification. Mitigations:
   external secret backup, shared key ring, key IDs, startup self-test, and
   rotation overlap longer than the maximum code TTL. It does not affect stored
   passwords or 256-bit token digests.
3. **Outbox complexity:** asynchronous delivery introduces retries, duplicate
   sends, and PII retention. Mitigations: idempotency keys, atomic job claims,
   issue-near-send, replacement semantics, encryption, terminal purge, and
   conformance/live-worker tests.
4. **Cross-table mutation:** users, identifiers, methods, and policy span ports.
   Mitigation: required same-adapter transactional aggregate operations for
   create/apply/remove; no accepted last-method TOCTOU.
5. **Host migration is destructive:** moving email off `users` requires
   backfill and SQLite rebuild. Mitigation: validated runbook, backup, collision
   abort, row-count checks, fresh-db canonical tests, and host-owned migrations.
6. **MFA is not v3:** v3 remains AAL1 for password/email/SMS/OAuth as actually
   provided. The assurance types must not overclaim. Auth-v4 is the named
   passkey/TOTP/recovery-code milestone (§12.1).

## Consultation notes

The earlier five-reviewer gate and columns delta gate are historical inputs, not
approval of this rewrite. Preserved findings include typed credential tables,
challenge/contactchange separation, adoption revocation, shared delivery,
normalization consistency, invitation kind filtering, console-as-development,
and dual-dialect conformance. Superseded findings are the columns model,
non-atomic challenge port, scalar last-method count, synchronous silent-success
send, and accepted self-removal TOCTOU.

The 2026-07-12 security-contract review adds the binding decisions now recorded
in R5/R7/R8 and V13–V15: HMAC pepper, repository-atomic consumption,
`user_identifiers`, step-up grants, method-set policy, reset revocation,
asynchronous delivery, HTTP protections, production fail-closed wiring, and
auth-v4 MFA space.

## Open questions

None. Exact internal names and purpose constants remain plan-cut freedom so long
as the executable tasks preserve the recorded contracts.

## Recommended reviews

Do not run reviewer/consultation agents during implementation. The implementer
executes the cut tasks and their test/live gates straight through AV3-9.6. After
the implementation is complete, AV3-9.7 runs one consolidated pre-PR
security/backend/data/architecture/SRE/HTML-accessibility/release reviewer wave;
AV3-9.8 applies accepted findings and repeats affected gates. No auth-v3 PR opens
before that review/remediation closes. Earlier historical gates do not close
the final implementation review.
