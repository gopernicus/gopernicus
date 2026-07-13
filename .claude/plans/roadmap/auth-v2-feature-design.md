# gopernicus auth v2 — design (authorization + the identity remainder)

Status: **RATIFIED 2026-07-07 (jrazmi) — all recommended defaults**
(AV3, AV6, AV7, AV8, AV9 as recommended; **AV10 = DEFER** the durable
security-event rail; **AV11 = DECOUPLE** — events-v1 gates only the
deferred rail). The NOTES.md 2026-07-07 ratification entry is the record
of decision. **AV10/AV11 were applied structurally at ratification**: A8
is struck from the auth-v2 phase table into the named deferred
disposition (§13), and the milestone carries no events-v1 dependency.
Review gate (the events design's tier-review-gate precedent, §11 there):
run 2026-07-07 (architecture-steward / lead-backend-engineer /
product-manager), all three ratify-with-amendments, amendments applied in
place before ratification. The tier-review question re-runs at each plan
cut, per the events precedent. **Plan-cut gate run 2026-07-07**
(architecture-steward / lead-backend-engineer / platform-sre /
data-integration-reviewer, 4× ratify-with-amendments): one design
amendment applied in place — **§7.2's ChangePassword session policy is
now delete-ALL-sessions + remint for the caller**, consciously
superseding the "other sessions" wording (**CONFIRMED by jrazmi at cut
ratification, 2026-07-07**); all other plan-cut amendments were
operational and landed in `.claude/plans/auth-v2/`. The cut itself was
**cut-ratified 2026-07-07 (jrazmi)**.
**AMENDED 2026-07-08 (jrazmi, in-session owner direction):** the §13
Z1–Z5 section is being executed via `.claude/plans/authorization-v1/`
**as amended by the 2026-07-08 multi-kind owner direction** — the feature
is the IAM/authorization domain with independently-wireable kinds; tables
take the `iam_` prefix (`iam_relationships`, NEW `iam_roles`); a roles
kind is added to v1 scope; the policy kind is a designed, named seam
deferred with a demand trigger. The plan's 00-overview carries the
verbatim direction + both Q&A rulings.
**EXECUTED 2026-07-09 (authorization-v1 Z1–Z5 complete):** ratified Q1–Q7
all at recommendations (Q1 groups TRIM, Q2 Option A two-commit protocol,
Q3 store-glue guard ADD, Q4 metadata TRIM, Q5 global fallback, Q6
`Config.IDs` mint seam + inline DDL DEFAULTs, Q7 second-relation silent
no-op). Shipped: `features/authorization` + `stores/{turso,pgx}` (modules
32–34); memstore + both dialect stores pass the ONE storetest suite live
(all five named adversarial sub-runners + the `Roles/*` family); the
three postures demonstrated on `examples/auth-cms` (commit-1 `2e1e5eb`
middle posture with the clean-graph capture, commit-2 `65fcb49` flagship,
both kinds driven live). Staleness findings recorded at plan cut (read
the design accordingly): §2.5's `Storer` enumeration is illustrative —
the real salvaged port is 14 methods; §2.2's "events' seam is
user-shaped" worry was overtaken by the shipped pair-shaped
`identity.Principal` seam; §10's module arithmetic ("+3 → 29") predates
later landings — the tree closed at 34.
Date: 2026-07-07
Governing directive: NOTES.md 2026-07-06 (final entry) — **auth-v2 ships
authorization, but as a port-shaped capability: a first-party ReBAC
authorizer is the flagship implementation, and hosts must be able to run
gopernicus with no authorization at all, or with other authorizer types
(simple role/ownership checks; future policy engines).** That ruling
consciously amends capability-map YOUR CALLs #1/#2 (which deferred ReBAC
pending a concrete need); it is the design's spine and is not re-litigated
here.
Depends on: `.claude/plans/restructure/00-overview.md` (constitution rules
1–8), `.claude/plans/restructure/capability-map.md` (Authentication &
identity + Tenancy sections; ratified YOUR CALLs 1–9),
`.claude/plans/roadmap/00-intersections.md` (taxonomy §1, degraded-mode
matrix §2, seam map §3, R1–R10), `.claude/plans/roadmap/events-feature-design.md`
(the outbox contract §5 and the `Config.Authorize` deny-by-absence precedent
§6), `features/README.md` (charter, esp. checklist items 1–12),
`.claude/past/auth-v1/` (what v1 shipped and how it phased).
Sequencing (ratified AV11): **auth-v2 is decoupled from events-v1 and
schedules on its own merits** — only the AV10-deferred durable rail
carries an events-v1 dependency; phase files are cut from §13 when the
milestone's execution window opens. Salvage source:
`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original` (design
ported, code re-typed fresh — the sdk-parity bar).

This is a design document only. Nothing here is built. Future milestones
phase from §13, the way auth-v1 phased from `auth-feature-design.md`.

## Context

auth v1 shipped password + session identity (`features/auth`, five ports,
both dialect stores, the rule-6 proof host). The original repo's remaining
auth surface — the optional `With*` extensions (API keys, OAuth accounts,
service accounts, security events), the ReBAC authorization engine,
invitations, and JWT signing — was classified feature-v2 by the capability
map and deferred. The 2026-07-06 ruling un-defers authorization with a
specific posture: **supported, never required**. This doc designs that
posture plus the whole v2 remainder, against the code that now exists:
`sdk/oauth` (port + PKCE) and `integrations/oauth/{google,github}` from
sdk-parity, `sdk/cryptids` (`Encrypter`+`AESGCM`, `SHA256Hasher`,
`JWTSigner` port) with `integrations/cryptids/golang-jwt`, and the ratified
events outbox design auth's security events will be the first durable
emitter through.

## Goal

A ratifiable design for (1) authorization as a three-posture capability
with `features/authorization` (first-party ReBAC) as its flagship, and
(2) the `features/auth` identity remainder — OAuth flows, API keys +
service accounts, JWT bearer mode, security events on the outbox,
invitations, and the v1 product debts — precise enough that the milestones
in §13 can be cut without re-deciding anything.

## 1. Scope shape — two milestones, one design

The work splits on a natural seam: everything in `features/auth` (identity:
who is calling) versus the new `features/authorization` module (permission:
what may they do). The cut (ratified AV3):

- **Milestone `auth-v2`** — the identity remainder, all inside
  `features/auth` + its stores: v1 debts, OAuth, API keys + service
  accounts, JWT bearer mode, security events (audit table; the durable
  outbox rail is DEFERRED per ratified AV10), invitations
  (ReBAC-decoupled, §6).
- **Milestone `authorization-v1`** — `features/authorization`: the ReBAC
  engine, model DSL, stores, storetest, and the consumer-seam proof host.

One design doc (this one) covers both so the seams (invitations' Granter,
security-event subjects, the consumer-port pattern) are decided once.
Neither milestone depends on the other's code — that independence is
itself the ruling's proof (§2.1) — but auth-v2 executes first: it is
smaller-risk, pays the v1 debts, and gives authorization-v1's proof host
richer material (invitations + service accounts as subjects).

## 2. Authorization — the ruling, cashed

### 2.1 Three postures, all first-class

| posture | what the host does | modules pulled | migrations |
|---|---|---|---|
| **none** | wires nothing; consuming features' authorize ports stay nil → **deny-by-absence** (gated routes not registered) or the feature's session-only default (cms `AdminMiddleware` = `RequireUser`, today's status quo) | — | — |
| **host-authored** | satisfies a feature's narrow authorize port with its own closure/type — an ownership column check, a role map, a future policy-engine client | — | host's own, if any |
| **flagship ReBAC** | mounts `features/authorization`, declares a model, wires `Authorizer` methods into consumer ports via closures in `main` | `features/authorization` + one store module | source `"authorization"` |

The load-bearing structural fact (this is why AV1 recommends a separate
module): **schema-optionality is only achievable at the feature/source
boundary.** A nil port disables behavior; it never un-migrates a table.
There is no "adopt this feature but skip a subset of its migrations"
mechanism in the charter — so "supported, never required," applied to
tuple tables, *forces* the ReBAC engine out of `features/auth`. A host
that wants sessions but no ReBAC must never see `rebac_relationships` in
its scaffolded tree. Independently, the taxonomy litmus (own migrations →
feature) lands it in a feature of its own.

**The bounding rule (review-gate amendment — so this section cannot be
cited as precedent for splitting every optional capability into its own
feature):** intra-auth capabilities are optional at the **port/behavior
level only** — a host adopting auth-v2 scaffolds `oauth_accounts`,
`api_keys`, and the other §10 tables inert-but-present regardless of what
it wires. Source-level schema optionality is reserved for authorization
because a ruling demands it **and** it carries independent
capability/engine weight and its own release cadence. Nobody gets to
demand `features/oauth` by citing §2.1.

### 2.2 The consumer seam — narrow ports, declared by consumers, adapted in `main`

No `sdk/authorization` port ships in v2. The pattern is C2 + the events
precedent: **each consuming feature declares its own narrow authorize
shape in its own public package, with documented deny-by-absence nil
semantics** (charter item 12), and the host adapts whatever authorizer it
runs — or none. Two shapes already exist or are named:

```go
// features/events (shipped design): coarse, stream-scoped
type AuthorizeStream func(ctx context.Context, userID, resourceType, resourceID string) (bool, error)

// features/auth v2 (this design, §6): grant-on-accept for invitations
type Granter interface {
    Grant(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error
}
```

They are deliberately different granularities — which is exactly the
evidence *against* premature sdk graduation (the C2 corollary's plurality
test wants a second **identical** shape, and there isn't one). A host
running events + authorization writes, in `main`:

```go
eventsCfg.Authorize = func(ctx context.Context, userID, rt, rid string) (bool, error) {
    res, err := authorizer.Check(ctx, authorization.CheckRequest{
        Subject:    authorization.Subject{Type: "user", ID: userID},
        Permission: "view",
        Resource:   authorization.Resource{Type: rt, ID: rid},
    })
    return res.Allowed, err
}
```

No import edge anywhere; pure rule-5 wiring. Graduation trigger, recorded:
the day two features need the *identical* authorize vocabulary, revisit
under `sdk/README.md`'s admission policy — not before. Shape note for
future seams (review-gate amendment): prefer **pair-shaped subjects**
(`subjectType, subjectID`) — events' shipped seam is user-shaped, and
machine principals cannot flow through it; whatever eventually graduates
to sdk must not accidentally be the user-only shape.

### 2.3 `features/authorization` — anatomy

Module `github.com/gopernicus/gopernicus/features/authorization`, package
`authorization`. Migration source `"authorization"`. Trio layout:

| path | contents |
|---|---|
| `authorization.go` | socket: `Repositories{Relationships relationship.Storer}`, `Config{Model Schema, PlatformAdmin…}`, `New(repos, cfg) (*Authorizer, error)`, `Register(mount, repos, cfg) error` (v1 registers no routes — the jobs precedent; logger only; the `/authorization/*` namespace is claimed for a future admin surface) |
| `logic/relationship/` | public rim: the tuple entity, `CreateRelationship`, filters, and the `Storer` port (salvaged shape below) |
| `internal/logic/authorizersvc/` | the engine: check evaluation, through-traversal, cycle guards, batch, lookup, membership rules (last-owner protection), schema validation |
| `memstore/` | public in-core reference implementation (R3: substantial + host-needed — group expansion re-implemented in Go) |
| `storetest/` | conformance suite; **must assert group-expansion / through-traversal / userset (`group#member`) behavior** so the memstore and the recursive-CTE stores provably authorize identically (the events EventID-uniqueness lesson) |
| `stores/turso/`, `stores/pgx/` | sibling modules: tuple + metadata SQL, recursive-CTE group expansion, canonical migrations, `ExportMigrations` |

The salvaged engine (verified against the original: ~2,000 LOC non-test
across `authorizer.go`/`model.go`/`builder.go`/`schema_validator.go`/
`membership.go`/`explain.go`/`cache_store.go`, stdlib-only except
pagination typing) exposes on the concrete `Authorizer`:

```go
func New(repos Repositories, cfg Config) (*Authorizer, error) // schema-validated at construction; invalid model = loud error

func (a *Authorizer) Check(ctx context.Context, req CheckRequest) (CheckResult, error)         // CheckResult{Allowed, Reason}
func (a *Authorizer) CheckBatch(ctx context.Context, reqs []CheckRequest) ([]CheckResult, error)
func (a *Authorizer) FilterAuthorized(ctx context.Context, subject Subject, permission, resourceType string, resourceIDs []string) ([]string, error)
func (a *Authorizer) LookupResources(ctx context.Context, subject Subject, permission, resourceType string) (LookupResult, error) // {Unrestricted, IDs}
func (a *Authorizer) CreateRelationships(ctx context.Context, rels []CreateRelationship) error
func (a *Authorizer) DeleteRelationship / DeleteResourceRelationships / DeleteByResourceAndSubject(...)
func (a *Authorizer) RemoveMember(...) error // last-owner protection
func (a *Authorizer) ValidateRelation / ValidateRelationships / GetSchema / GetPermissionsForRelation(...)
func (a *Authorizer) ListRelationshipsBySubject / ListRelationshipsByResource(...) // crud-typed listing (§9)
```

`Subject{Type, ID, Relation}` keeps the optional userset relation
(`group#member`-style subjects). Subject types are **string conventions**
("user", "service_account"), never imports — `features/authorization`
needs nothing from `features/auth`'s entities, and rule 6 holds in both
directions with zero adaptation.

**The model DSL is registered data** (the cms `Config.Types` seam
pattern): the host declares resource types, relations, and permission
rules (`AnyOf` unions of direct relations and `Through` traversals) in Go
at construction via `NewSchema(...)` + `ResourceSchema` values; the schema
validator rejects unknown relations, bad through-targets, and cycles at
`New` time. Adding a resource type is a code change with zero migration —
the tuple table never changes shape (the EAV-spine philosophy applied to
permissions).

### 2.4 Port-surface split (AV2 — recorded derivation of the ruling, §12)

- **The generic seam is Check-only** — a single boolean decision per
  consumer-declared shape (§2.2). A `func` or one-method interface; an
  ownership closure can always satisfy it.
- **Everything else — CheckBatch, FilterAuthorized, LookupResources,
  relationship CRUD, listing — is ReBAC-Authorizer-specific exported API**,
  never part of a consumer seam. A simple role check cannot honestly
  enumerate resources; putting lookup on the seam would make the flagship
  the floor and quietly violate the ruling. (An earlier sketch had
  CheckBatch as an "optional capability" on the seam — rejected in
  consultation: you cannot type-assert a capability off a `func`; batch
  belongs with the engine.)

### 2.5 Storage

Salvage shape (`0002_rebac.sql`): `rebac_relationships` (resource_type,
resource_id, relation, subject_type, subject_id, subject_relation;
unique-tuple index; resource/subject/type-relation indexes) +
`rebac_relationship_metadata` (JSON metadata keyed to a tuple; GIN index
on pgx — turso carries a plain JSON TEXT column, a documented
index-capability divergence, same migration filenames). The engine's
`Storer` port pushes recursion to the store: `CheckRelationWithGroupExpansion`
is a recursive CTE in both SQL stores and a Go graph walk in the memstore
— which is exactly why `storetest` must assert expansion behavior, not
just tuple CRUD. `GetRelationTargets`, `CheckBatchDirect`,
`CountByResourceAndRelation` (last-owner), and the two listing methods
complete the port.

**Pinned (design decision, review-gate amendment):**
`CountByResourceAndRelation` counts **direct tuples only**, never expanded
group membership. It feeds last-owner protection, which asks how many
direct owner anchors exist on the resource — the original's implementation
counts tuples, and expanded counting would let a group's transient
membership mask the loss of the last direct owner. A count divergence
between implementations is a **security divergence**, which is why the
diamond-dedup storetest case (§13 Z1) carries an explicit Count assertion.
Not genuinely contested — flag only if an implementer finds the original
behaves otherwise.

The original's `groups` aggregate (name/slug + membership sugar over
tuples) is **a trim candidate**: the engine needs no groups *table* —
group expansion is pure tuples (`group:{id}#member@user:{x}`). §13 carries
it as an explicitly droppable phase; recommend building it only if the
proof host's demo wants named groups.

The original's `cache_store.go` (a caching `Storer` decorator) and
`explain.go` (decision tracing) are salvage-if-free: not v1 acceptance
criteria, noted in the phase table.

### 2.6 The parked `fop` gap (capability-map ratified call #8)

The authorization-aware overfetch loop (`PostfilterLoop`) and the
prefilter pattern (`LookupResources` → `Unrestricted`-or-`IDs` → SQL `IN`)
were parked with the authorization decision. Disposition: **the prefilter
contract ships with `LookupResources`** (the `LookupResult.Unrestricted`
semantics above are that contract); **`PostfilterLoop` stays unbuilt**
until a real list surface consumes it — it is a consumer-side pagination
helper, and no consuming list surface exists in either milestone. Recorded
as the demand gate, not silently dropped. One connecting constraint
(review-gate amendment): a future *enumeration-shaped consumer seam*
(Lookup-shaped, not Check-shaped) stays ruling-honest **only** when paired
with the deferred `PostfilterLoop` as its deny-by-absence fallback path —
an unpaired prefilter seam is a de-facto ReBAC requirement, the exact
anti-pattern §2.4 warns against. Whoever cashes the demand gate builds
both halves together.

## 3. OAuth — wiring the shipped ports into `features/auth`

What exists: `sdk/oauth.Provider` (+PKCE S256), `integrations/oauth/google`
(go-oidc), `integrations/oauth/github` (zero-require). What's missing is
the *feature flow*. Salvage (original `oauth_flow.go`/`oauth_linking.go`,
~1,100 LOC with satisfiers) with deliberate trims:

**New domains (public rim):**

- `logic/oauthaccount` — `OAuthAccount{UserID, Provider, ProviderUserID,
  ProviderEmail, ProviderEmailVerified, AccountVerified, LinkedAt,
  AccessToken, RefreshToken, TokenExpiresAt, TokenType, Scope}` (token
  fields hold **ciphertext** when an encrypter is wired, empty otherwise)
  + `OAuthAccountRepository` (Create, GetByProvider(provider,
  providerUserID) — unique pair, ListByUser, Delete(userID, provider);
  sentinel contract mirrors v1's ports).
- `logic/oauthstate` — the short-lived flow-state secret: `State{Token,
  Provider, Purpose, Payload []byte, ExpiresAt}` + `StateRepository`
  (Create, Consume — get-and-delete, expired → `errs.ErrExpired`). This is
  a **new table, not a widening of v1's verification ports** — the
  original hung OAuth state, pending links, and sensitive-op codes off one
  purpose-keyed verification-codes table (salvage hazard: every redesign
  of that table ripples through three flows); v1's `Code`/`Token` ports
  are frozen and stay frozen.

**Flow (the anti-takeover shape, kept intact):** start → PKCE
verifier+challenge, state token, OIDC nonce when `SupportsOIDC()`, state
persisted server-side → callback → state consumed, code exchanged,
identity read (ID-token claims for OIDC, `GetUserInfo` otherwise) → then
the three-way branch: existing link → login (session minted); no link but
an existing user with that email → **pending link**: a single-use,
expiring pending-link secret is mailed and the link completes only via
`verify-link`. Pinned (review-gate amendment): the pending-link secret is
an **`oauthstate` row** (purpose pending-link, payload = the pending
account, `Consume` = single-use get-and-delete, `ExpiresAt` enforced) —
**never** `verification.Code`, whose port has no single-use-with-payload
contract; the anti-takeover gate needs both properties. (This is the
account-takeover gate — an attacker with a provider account matching a
victim's email cannot silently attach it.) No user → register + link
(+ `TrustEmailVerification()` gating whether the provider's email-verified
claim marks the new user verified). Unlinking enforces
**last-authentication-method protection**: refuse to unlink the only
credential when no password is set.

**Config additions** (all on the existing `auth.Config`):

| field | nil/empty means |
|---|---|
| `Providers []oauth.Provider` | OAuth subsystem off — no routes registered (deny-by-absence); `Repositories.OAuthAccounts`/`OAuthStates` may be nil |
| `TokenEncrypter cryptids.Encrypter` | provider tokens are **not persisted** (login/linking work; no offline API access) — safe silent degradation, documented; wire `cryptids.AESGCM` to store them |
| `OAuthCallbackBase string` + `RedirectAllowlist []string` | callback URL construction + the exact-match open-redirect guard (feature-internal matcher per the capability map's allowlist row — promoted to `sdk/web` only on a second consumer) |

Partial wiring is a **loud construction error** (the Hasher/Mailer
precedent): `Providers` set but `OAuthAccounts` or `OAuthStates` nil →
`ErrOAuthReposRequired`.

**Route surface** (inside the claimed `/auth/*` namespace):
`GET /auth/oauth/{provider}/start`, `GET /auth/oauth/{provider}/callback`,
`POST /auth/oauth/verify-link`; session-gated:
`GET /auth/oauth/linked`, `GET /auth/oauth/{provider}/link/start`,
`DELETE /auth/oauth/{provider}/link`.

**Trims (ratified AV7):** the mobile flow (flow-secret hashing,
`/oauth/initiate`, mobile callback/redirect endpoints) and the
code-gated sensitive-op pair (`unlink/send-code` + code-verified unlink)
are **not** salvaged — no mobile host exists, and session-gated unlink
with last-method protection covers the web threat model. Both return on
demand; the original's design is the reference.

## 4. Machine identity — API keys, service accounts, principals, JWT

### 4.1 API keys + service accounts (new domains in `features/auth`)

- `logic/serviceaccount` — `ServiceAccount{ID, Name, Description,
  CreatedBy, ActAsUser, OwnerUserID, CreatedAt, UpdatedAt}` (+the original's
  invariant: `ActAsUser → OwnerUserID != ""`) + repository (Create, Get,
  ListPage, Update, Delete).
- `logic/apikey` — `APIKey{ID, ServiceAccountID, Name, KeyPrefix, KeyHash,
  ExpiresAt, RevokedAt, LastUsedAt, CreatedAt}` + repository (Create,
  **GetByHash** — active+unexpired scoping, ListByServiceAccount, Revoke,
  TouchLastUsed). Keys are high-entropy random secrets: **SHA-256 at rest**
  (`cryptids.SHA256Hasher` — its documented purpose), plaintext shown once
  at mint; `KeyPrefix` stored plain for display. Pinned (review-gate
  amendment): minted keys use a **dotless encoding** (`sdk/id`'s base32
  alphabet; prefix joined with `_`, never `.`) so no API key can ever
  contain two dots and collide with §4.3's JWT-detection heuristic. No
  scopes column —
  authorization is "authenticate as the owning service account; the host's
  authorizer governs the rest" (faithful to the original; per-key scopes
  are new design, explicitly out).
- Salvage-hazard fixes, named: the original **required** `GetByHash` but
  shipped no implementation or example query (a compile-time trap for
  adopters) — v2 ships it in both stores under `storetest`; and
  `last_used_at`/`last_used_ip` were schema-only dead columns — v2 wires
  `TouchLastUsed` best-effort post-auth (errors logged, never fail auth)
  and drops the IP column unless the audit row (§5) wants it.

**Service accounts authenticate exclusively via API keys** (the original's
model — no service-account JWTs). `Service.AuthenticateAPIKey(ctx, rawKey)
(Principal, error)` hashes, looks up, checks revocation/expiry, records
the security event, and resolves the **effective principal**: an
`ActAsUser` account resolves to `Principal{Type: "user", ID: OwnerUserID}`
(personal API keys); otherwise `Principal{Type: "service_account", ID:
sa.ID}`.

### 4.2 Principals (AV5 — recorded consultation outcome; divergence from the original, flagged for visibility)

The original's `principals` table is a bare identity registry whose sole
job was polymorphic-FK integrity across `users`/`service_accounts` — no
engine option ever read it, and the new repo's stores deliberately carry
no enforced FKs. **Recommended: do not salvage the registry table.**
Actor references are `(subject_type, subject_id)` string pairs everywhere
(the ReBAC `Subject` shape); per-kind tables stay authoritative; the
exported value type `auth.Principal{Type, ID string}` is the effective
caller (also resolving the original's `authentication.Principal` vs
`principals.Principal` naming collision by having exactly one type).
Revisit trigger: a concrete need for a unified principal read-model or
FK-integrity requirement string pairs can't satisfy.

### 4.3 Middleware surface

`RequireUser` keeps its exact v1 semantics (session cookie → user
identity; the cms `AdminMiddleware` contract is untouched), gaining one
addition: when `Config.TokenSigner` is wired, a `Authorization: Bearer`
JWT resolves to the same user identity. Two new middleware join it:
`RequireServiceAccount` (API-key bearer only) and `RequirePrincipal`
(either credential class; sets `Principal` in context;
`Service.CurrentPrincipal(ctx)` alongside the existing `CurrentUser`).
Bearer-token classing uses the original's detection (JWTs have exactly
two dots), applied only for the credential classes actually configured —
no `TokenSigner` → bearer JWTs are never parsed; no API-key repos → keys
never looked up.

### 4.4 JWT bearer mode (ratified AV6 — the honest salvage)

> **[SUPERSEDED 2026-07-11 by `.claude/plans/roadmap/auth-jwt-session-refresh.md`
> (ratified same day, decisions D1–D8).]** The refresh change reverses AV6's "no
> refresh" arm and re-frames the JWT: the access JWT becomes the **primary**
> access credential (not a side convenience) and the session row becomes the
> revocation + refresh anchor. Concretely, past this date: `Config.TokenSigner`
> is **required** (no signer-off mode); `/auth/token` returns
> `{access_token, expires_at, refresh_token}` (not `{token, expires_at}`);
> `TokenTTL` is removed in favour of `AccessTokenTTL` (15m) + `RefreshTTL` (7d);
> `POST /auth/refresh` rotates an opaque refresh token with single-generation
> reuse detection; and a new `RequireLiveSession` tier gives immediate revocation
> where the stateless-JWT revocation asymmetry below is unacceptable. The
> original AV6 framing is retained verbatim below for decision history only.

**Finding (corrects this design's own task framing):** the original had
no machine-client JWT mode — machine clients used API keys; JWTs were
*user* tokens (claims `{user_id, exp, iat}`) as a stateless alternative to
cookie sessions. Recommended: salvage that faithfully. `Config.TokenSigner
cryptids.JWTSigner` (nil → mode off; `integrations/cryptids/golang-jwt`
satisfies it); `POST /auth/token` accepts login-shaped credentials
(rate-limited exactly like `/auth/login`, verification-gating per §7
applies) and returns `{token, expires_at}` with a configurable short TTL
(default 1h); `RequireUser`/`RequirePrincipal` verify bearer JWTs when the
signer is wired. **No refresh tokens in v2** (non-goal §11): sessions
remain the revocable long-lived identity; JWTs are short-lived API
conveniences. The revocation asymmetry (a JWT outlives a password change
until expiry) is documented on the Config field — short TTL is the
mitigation, mirroring the events design's `MaxConnAge` posture.

## 5. Security events — the audit log, and the first durable outbox emitter

Two distinct rails, deliberately not conflated (the outbox is a
publish-then-purge **delivery rail**; an audit log is a queryable,
retained **table** — events design §9 says so explicitly):

**5.1 The audit table (always-on when wired).** New domain
`logic/securityevent`: append-only `SecurityEvent{ID, UserID (optional),
Actor Principal (optional), EventType, EventStatus, Details map[string]any,
IPAddress, UserAgent, CreatedAt}` + repository (Create, List — crud-typed,
filterable by user/type/status/time). The salvaged event vocabulary
(register / login / logout / password_change / password_reset /
email_verified / oauth_login / oauth_register / oauth_link_verified /
oauth_linked / oauth_unlinked / apikey_auth / invitation events; statuses
success / failure / blocked) is recorded **synchronously** from every
sensitive `authsvc` operation, with the original's non-negotiable
property kept: **audit-write failures are logged and never fail the auth
flow.** `Repositories.SecurityEvents` nil → no audit trail (documented;
ratified AV9 — optional, loud README row); when wired, writes are
unconditional — auditing is never deny-by-absence-gated on anything else.
No HTTP read surface in v2 (an `/auth/security-events` admin surface is
workshop-v2-shaped, deferred).

**5.2 The durable emission rail (DEFERRED at ratification — AV10).**
This section is retained in full because it **governs the deferred phase
when it returns** (trigger: the first real durable consumer —
webhooks/alerting); nothing below is auth-v2 milestone scope. For hosts
that want reactions (SSE, alerting, webhooks), security events ride
the **events outbox** — at-least-once, per the ratified events design §3.
auth is the first feature to actually pay §5's appender pattern, so the
full blast radius is budgeted, not hand-waved:

- The security-event **create input** gains an optional
  `Events []events.Record` field (input struct, never a widened method
  signature; records built via `events.NewRecord` so serialization stays
  sdk-owned) with the **MAY-drop contract** — the memstore and any host
  without an outbox legitimately drop them; `storetest` asserts the port
  tolerates both.
- Each auth **store adapter** declares its own dialect-typed appender port
  (consumer-declares): `type OutboxAppender interface {
  AppendTx(ctx context.Context, tx *turso.Tx, recs ...events.Record) error }`
  (pgx twin over the pgx integration's Tx). Constructor-injected, nil =
  drop. The audit insert and the outbox append share **one transaction** —
  and because the audit row *is* the domain write for most security events
  (a failed login writes nothing else), the appender rides the
  security-event create, giving true outbox atomicity with no other port
  changes.
- Host prerequisites, documented: the `"events"` migration source applied
  before an appender-wired store boots (the events stores' boot-time probe
  covers the runtime check); wiring order in `main` mirrors the events
  design §7 host row.

**The combined guarantee, stated plainly (review-gate amendment — this
paragraph is repeated verbatim on the Config field and port docs):**
because §5.1's audit-writes-never-fail-the-flow rule governs the shared
transaction, a transient store failure drops **both** the audit row and
the outbox record. The durable rail is therefore **at-least-once
if-committed** — best-effort commit, at-least-once delivery after commit.
It is a monitoring aid, **not a guaranteed-delivery security control**. A
host that needs guaranteed emission for a specific event type cannot get
it from this rail as designed: that requirement means *not* swallowing the
write error for that type — a deliberate future amendment (a fail-closed
event-type seam), named here, not a wiring option today.

**The events-contract re-check gate (hard requirement, travels with the
deferred phase):** events-v1 resumes at its phase 3 and may amend the
as-designed outbox contract during execution. Before the deferred
durable-emission phase is **cut** (not merely before it runs), re-read
the as-shipped `sdk/events.Record`, `outbox.EntryRepository`, appender
pattern, and poller semantics against this section; divergences amend
this doc's §5.2 in place, logged in the status header. The audit table
(§5.1) has no events dependency and sits on the identity critical path.
**Ratified AV10 (2026-07-07): the durable-emission phase is deferred out
of auth-v2 entirely** — this section's contract, this gate, the
combined-guarantee statement above, and the no-`features/events`-import
acceptance grep all remain in force and govern the phase when its trigger
fires.

## 6. Invitations — decoupled from ReBAC (AV4 — recorded derivation of the ruling, §12)

The original's `Inviter` is the sharpest rule-6 hazard in the salvage: it
constructor-holds `*authorization.Authorizer`, writes bookkeeping tuples
for the invitation-as-a-resource (`invitation:{id}#owner@user:{inviter}`),
and leans on those tuples for cancel/resend authorization. Under the
ruling, invitations must work with **no ReBAC anywhere**. The decoupled
shape:

- **New domain `logic/invitation`** in `features/auth` (placement note:
  the original stored invitations under `repositories/rebac/` as
  "ReBAC-adjacent"; the new placement is auth because every hard
  dependency is identity-shaped — users, sessions, Mailer — and the
  ReBAC edge is reduced to one port). Entity salvaged minus tuple
  bookkeeping: `Invitation{ID, ResourceType, ResourceID, Relation,
  Identifier (email), ResolvedSubjectID, InvitedBy, TokenHash, AutoAccept,
  Status (pending|accepted|declined|cancelled|expired), ExpiresAt,
  AcceptedAt, CreatedAt, UpdatedAt}`. `Relation` is an **opaque string
  the Granter interprets** — a ReBAC host maps it to a relation, a
  role-column host to a role. Token: 32-char generated secret, **SHA-256
  at rest**, plaintext only in the invitation email. Uniqueness: one
  pending invitation per (resource, identifier, relation).
- **`Granter` — the one seam, grant-on-accept only** (§2.2's shape).
  Called on accept, on direct-add (known user + `AutoAccept`), and on
  resolve-on-registration. It does **nothing else**: invitation
  *visibility* ("my invitations", list-by-resource) rides the table's own
  `ResolvedSubjectID`/`InvitedBy` columns, never tuples — a host wiring
  its own membership logic has no "invitation" resource type, and the
  design must not pretend otherwise. Cancel/resend authorization is a
  plain `InvitedBy == current user` ownership check.
- **`MemberCheck` — a separate optional port** (duplicate-membership
  detection before creating an invitation): nil → no dup check, an
  accepted duplicate grant must be idempotent in the Granter's world.
- **Nil semantics:** no `Granter` wired → the invitations route surface is
  **not registered** (deny-by-absence — an invitation that can grant
  nothing is a misconfiguration trap, not a degraded mode). Wiring a
  Granter with `Repositories.Invitations` nil → loud construction error.
- **Email**: direct `Config.Mailer` calls through the sdk/email template
  registry (invite-sent, member-added) — no bus subscribers (v1's ratified
  direct-mailer decision extends; the original's bus-driven subscriber
  layer is not salvaged). Destination paths guarded by the §3 redirect
  allowlist.
- **Resolve-on-registration**: pending auto-accept invitations for a
  just-verified email are granted inside auth's own register/verify flow
  (auth owns both sides — no cross-feature event needed), best-effort per
  invitation (one failed grant doesn't abort registration; each failure
  audit-logged). Internal wiring, pinned for the plan cut (review-gate
  amendment): the `Granter` is injected into `invitationsvc` **only** —
  `authsvc` never holds it. The register/verify flow calls one narrow
  internal port (`resolveInvitations(ctx, email, subjectType, subjectID)
  (int, error)`), satisfied by `invitationsvc` and injected into `authsvc`
  as an optional collaborator (nil when invitations are off). The
  authsvc↔invitationsvc coupling is that single interface; A6 budgets it.
- **Routes** (session-gated except decline):
  `POST /auth/invitations/{resource_type}/{resource_id}`,
  `GET /auth/invitations/{resource_type}/{resource_id}`,
  `GET /auth/invitations/mine`, `POST /auth/invitations/accept`,
  `POST /auth/invitations/{id}/cancel`, `POST /auth/invitations/{id}/resend`,
  `POST /auth/invitations/{id}/decline` (public, rate-limited).
- **Milestone stranding, resolved** (consultation finding): invitations
  land in auth-v2 but their flagship Granter (ReBAC) lands in
  authorization-v1. The auth-v2 proof host therefore wires a **toy
  membership Granter** (an in-memory membership map) — which is not a
  workaround but the *demonstration of the ruling itself*: invitations
  provably work with no ReBAC in the module graph. authorization-v1's
  proof host later swaps in `authorizer.CreateRelationships` via closure.

## 7. v1 product debts (all three, closed in auth-v2)

1. **Login gating on email verification.** New `Config.RequireVerifiedEmail
   bool`; when true, `/auth/login` and `/auth/token` return 403
   (`ErrEmailNotVerified`) for unverified users. **Default false**
   (ratified AV8): flipping the default breaks the standing five-step
   acid-test flow and every existing host silently; the flag ships off,
   with a recorded recommendation to revisit the default at first tag.
2. **`ChangePassword` routed.** `POST /auth/password/change`
   (session-gated, current password verified, strict decode). Ships with a
   **`SessionRepository.DeleteByUser(ctx, userID)` port addition** — a
   password change revokes **ALL** the user's sessions and mints a fresh
   one for the caller (new cookie in the response). **Amended in place
   2026-07-07 at the plan-cut gate** (steward finding): this supersedes
   the earlier "revokes the user's *other* sessions" wording — a security
   superset, and simpler than delete-all-but-current. `DeleteByUser` is
   also the primitive logout-everywhere and future `session.revoked`
   reactions need. Port change → both stores + memstores + `storetest`
   gain the method/case (§10).
3. **Session-token hashing — service-side, stores untouched**
   (consultation-confirmed shape): `authsvc` SHA-256-hashes the cookie
   token before every `Create`/`Get`/`Delete`; stores keep persisting an
   opaque string under the existing `token` column — **no DDL, no store
   or storetest changes**. Pinned (review-gate amendment): **one private
   mint/lookup hash helper in `authsvc` is the ONLY hashing site** —
   Login's create, `ValidateSession`'s get, Logout's delete, and any
   OAuth-/token-minted session (§§3–4) all route through it, so no second
   call site can drift. Existing plaintext rows simply never match a
   hashed lookup → all live sessions invalidated at deploy; orphaned
   plaintext rows are immediately unreachable and dead past their natural
   `ExpiresAt` TTL (hosts may vacuum; no purge ships). The forced logout
   is a **host-facing upgrade note in README/RELEASING — an explicit A10
   deliverable**, not only a NOTES artifact. The one entity wrinkle,
   resolved: `Session.Token` becomes "the stored value" (the hash); the
   service returns the plaintext cookie value separately at mint — doc
   comments on `session.Session` and the store SQL headers updated to stop
   saying "no hashing". The anti-pattern — hashing inside each store — is
   rejected (crypto duplicated across four implementations, storetest
   rewrite, guard gap).

## 8. Tenancy — stance reaffirmed

The ratified default stands unamended: tenancy folds into `features/auth`
as a **v2+ subdomain, demand-gated on a real multi-tenant host existing**
— it is *not* built by either milestone here. Nothing in this design
blocks it: subjects/actors are string pairs (a `tenant` subject or
resource type slots into the ReBAC model as data), `events.Record` already
carries the optional `TenantID` vocabulary, and the original's tenancy was
one entity + middleware, exactly subdomain-sized. Revisit trigger
unchanged.

## 9. Salvage reconciliation (cross-cutting, named so it can't balloon silently)

- **`fop` → `sdk/crud` re-typing.** The original types every listing port
  in `fop.Order`/`fop.PageStringCursor`/`fop.Pagination`; this repo's
  vocabulary is `sdk/crud` (`ListRequest`, `Page[T]`, cursor codec).
  Touches: authorization's `Storer` listing methods, security-event
  `List`, invitations' `ListByResource`/`ListBySubject`/`Mine` — and every
  store + storetest case behind them. Mechanical but pervasive; an
  explicit task in every store phase.
- **The satisfier layer disappears.** The original's `satisfiers/`
  packages existed to bridge hand-owned types to generated-repository
  types; the new repo hand-writes stores against the domain ports
  directly. Nothing to port — but the *invariants* satisfiers encoded
  (e.g. `Details` map → JSON marshaling) move into the store adapters.
- **Fixes shipped as part of salvage** (behavior intentionally diverges,
  the sdk-parity precedent): `GetByHash` implemented + conformance-tested
  (was a required port with zero implementation); `last_used_at` wired or
  dropped (was a dead column); the `Principal` naming collision resolved
  by AV5; OAuth state decoupled from the verification-codes table
  (was three flows on one purpose-keyed table).

## 10. Schema / store impact summary

**`features/auth` stores (turso + pgx, identical version filename sets,
source `"auth"`, continuing after v1's 0005):** new tables
`oauth_accounts`, `oauth_states`, `service_accounts`, `api_keys`,
`security_events` (append-only), `invitations`; **no change** to
`sessions` (§7.3 is service-side); `storetest` grows sub-runners per new
port plus the `DeleteByUser` case. The AV10-deferred durable-emission
phase, when its trigger fires, adds the per-dialect `OutboxAppender`
(+ its per-store tests against the events store, not in the neutral
suite) — no auth-v2 store work. `examples/auth-cms/internal/authmem`
grows the new ports it needs for the proof flows.

**`features/authorization` stores (new modules, source `"authorization"`,
0001+):** `rebac_relationships` + `rebac_relationship_metadata`
(+ `groups` only if its phase survives the trim). Recursive-CTE group
expansion per dialect; pgx-only GIN metadata index divergence documented.

**Module count:** +3 (`features/authorization` + its two stores) → 29.
The real registration artifacts (review-gate correction — there is no
"G2 list"): `go.work`, Makefile `MODULES`, `STORE_MODULES`, and the
`test-stores` enumeration, plus the RELEASING.md module list — all updated
in the respective docs phases.

**Guard mechanics, corrected and extended (review-gate amendment):** G2
(`guard-feature-isolation`) is a **blanket pattern grep with no
per-feature list** — it auto-covers `features/authorization`'s
integrations/examples/own-stores edge with zero edits. What does **not**
exist today is any guard on the feature→feature edge this design's whole
"supported, never required" posture rests on. Therefore: **Z5 ships G5**,
a new Makefile guard target forbidding any `features/<a>` →
`features/<b>` import (a≠b), prove-can-fail like the others; until it
lands, A9/Z4 keep the manual rule-6 acceptance greps. Its store-module
sibling — "store modules never import another feature's modules" (the
AV10-deferred appender seam is the named case) — is a candidate guard to
be **added or consciously deferred at Z5**, and the deferred
durable-rail phase's acceptance carries the explicit grep regardless:
auth store modules contain no `features/events` import.

## 11. Non-goals (both milestones)

- No `sdk/authorization` port (§2.2's graduation trigger recorded).
- No tenancy (§8). No per-key API scopes (§4.1). ~~No refresh tokens (§4.4).~~
  **[SUPERSEDED 2026-07-11 by `auth-jwt-session-refresh.md`: refresh tokens with
  rotation + single-generation reuse detection are now in scope; see the §4.4
  banner.]**
- No OAuth mobile flow, no code-gated sensitive-op unlink (AV7).
- No security-event HTTP read surface, alerting, or webhook reactions
  (the outbox rail is the extension point; consumers are host-side).
- No generated CRUD admin bridges over the new entities (workshop v2's
  question, per the capability map).
- No `PostfilterLoop` (§2.6 demand gate). No ReBAC caching decorator as
  acceptance criteria (salvage-if-free). No groups admin UI.
- No new `Mount` fields — nothing here needs one (C3 discipline).

## 12. Decision table — FULLY RATIFIED 2026-07-07 (jrazmi); nothing stays open

### Recorded — not open for re-decision (AV numbering retained for citation stability)

AV1/AV2/AV4 are **derivations of the ratified 2026-07-06 ruling** —
re-deciding any of them means reopening the ruling itself, a jrazmi-only
move, not a plan-review option (PM finding: presenting consequences as
open calls invites re-litigation). AV5 is a **recorded consultation
outcome**, kept visible because it deliberately diverges from the
original's schema.

| # | decision | recorded position | notes |
|---|---|---|---|
| AV1 | **(a) authorization placement** | **own module `features/authorization`** | schema-optionality exists only at the feature/source boundary (§2.1, incl. the bounding rule) — a subdomain drags tuple tables into every auth host's tree; plus independent engine weight/release cadence. Cost: one more module trio (29 total) |
| AV2 | **(b) port-surface split** | **consumer seams are Check-only; everything else is concrete-Authorizer API** (§2.4) | a role-check closure can satisfy Check; only ReBAC-class engines can enumerate — lookup on the seam would make ReBAC required in practice |
| AV4 | **(d) invitations' ReBAC coupling** | **decoupled: grant-on-accept `Granter` port, deny-by-absence routes; ships in auth-v2 with a toy-membership Granter in the proof host** (§6) | gating invitations behind authorization-v1 would make the flagship required for a flow the ruling says must work without it |
| AV5 | principals registry table | **not salvaged — actor references are `(subject_type, subject_id)` string pairs; `auth.Principal` is a value type; registry demand-gated** (§4.2) | consultation outcome; the new stores carry no enforced FKs, which was the registry's whole job |

### Ratified 2026-07-07 (jrazmi) — every row to its recommended default; the NOTES.md 2026-07-07 entry is the record of decision

| # | decision | ratified outcome | notes |
|---|---|---|---|
| AV3 | **(c) milestone split**: authorization inside auth-v2 vs follow-on milestone | **two milestones: auth-v2 (identity) then authorization-v1** | keeps each close-gate (store parity, live runs) tractable; auth-v2 pays debts first; the ruling's "never required" is proven by auth-v2 shipping with zero authorization imports |
| AV6 | JWT bearer mode shape | **stateless *user* tokens (short TTL, no refresh); machine clients authenticate via API keys** (§4.4) | faithful to the original (which had no machine-JWT mode, correcting this design's own task framing); service-account JWTs would be new design. **[AMENDED 2026-07-11 by `auth-jwt-session-refresh.md` (D1–D8): the "no refresh" arm is reversed — the access JWT becomes the primary credential with an opaque rotating refresh token + `RequireLiveSession`; see the §4.4 supersession banner.]** |
| AV7 | OAuth scope trims | **mobile flow + code-gated unlink OUT; browser login/register/pending-link/linking/unlink IN** (§3) | no mobile host exists; the anti-takeover pending-link gate — the part with real security content — is kept intact |
| AV8 | `RequireVerifiedEmail` default | **ship the knob defaulting to false; revisit the default at first tag** (§7.1) | flipping now silently breaks the standing acid-test flow and existing hosts; the knob's existence closes the product debt |
| AV9 | `Repositories.SecurityEvents` optionality | **optional (nil → no audit trail, loud README row)** (§5.1) | requiring it would force six ports on zero-infra hosts; but if jrazmi wants audit-by-default posture, "required like Hasher" is defensible — genuine product call |
| AV10 | A8 (durable emission rail): build in auth-v2 vs **defer** | **DEFER**, trigger = the first real durable consumer (webhooks/alerting) | PM case: nothing in either milestone consumes the rail (A9 proves only the synchronous audit table); it was this design's highest-risk phase (§15 risk 1); and it was the sole reason auth-v2 gated on events-v1. **Applied structurally at ratification**: A8 struck from §13 into the named deferred disposition; §5.2 retained in full as the governing contract (re-check gate, combined-guarantee statement, acceptance grep) |
| AV11 | events-v1 coupling: gate the whole milestone vs only A8 | **decouple** — only the AV10-deferred rail gates on events-v1; A1–A7/A9/A10 have zero events dependency | **applied structurally at ratification**: header sequencing line + §13 updated; auth-v2 schedules on its own merits |

## 13. Rough phase breakdown (the jobs §10 / events §11 pattern)

**Executor model policy (standing, from auth-v1):** implementation phases
run on `model: opus`; design/doc-judgment phases on `model: fable`; never
sonnet.

**Plan-cut requirements** (inherited from the events design's §11
amendments, applied here): (1) the design-level review gate **ran
2026-07-07** (status header); the tier-review question re-runs at each
plan cut per the events precedent; (2) the **events-contract re-check
gate** (§5.2) runs before the AV10-deferred durable-rail phase is cut —
not an auth-v2 concern; (3) each milestone's docs phase ships the
per-capability **wiring page** (one diagram + one complete `main.go`)
with the proof host as its executable twin — the authorization wiring
tour (model declaration → `New` → closures into consumer ports) is a
five-stop comprehension cost paid with docs, like events'.

**AV10/AV11 applied (ratified 2026-07-07):** A8 is struck below (row kept
for numbering stability, the jobs-v1 struck-phase-6 precedent) and its
scope moved to the named deferred disposition after the table; auth-v2
carries **no events-v1 dependency**.

### Milestone `auth-v2` (decoupled from events-v1 — ratified AV11)

| phase | what | size | depends on | model |
|---|---|---|---|---|
| A1 | v1 debts: service-side session hashing, `POST /auth/password/change` + `SessionRepository.DeleteByUser` (port + both stores + memstores + storetest), `RequireVerifiedEmail` knob | M | — | opus |
| A2 | OAuth: `logic/oauthaccount` + `logic/oauthstate`, flow orchestration in `authsvc` (login/register/pending-link/link/unlink, PKCE/state/nonce), Config additions + loud partial-wiring errors, routes, allowlist matcher, fake-Provider unit tests, storetest sub-runners | L | A1 | opus |
| A3 | Machine identity: `logic/serviceaccount` + `logic/apikey`, `AuthenticateAPIKey` + effective-principal resolution, mint/hash/prefix + `GetByHash` + `TouchLastUsed`, `RequireServiceAccount`/`RequirePrincipal`, minimal session-gated JSON lifecycle routes, storetest sub-runners | L | A1 | opus |
| A4 | JWT bearer mode: `Config.TokenSigner`, `POST /auth/token`, bearer verification in the middleware trio (golang-jwt integration consumed, not modified) | S | A3 | opus |
| A5 | Security events (audit rail): `logic/securityevent`, synchronous never-fail writes across all sensitive ops (v1 ops + A2–A4's), storetest sub-runner | M | A2, A3 | opus |
| A6 | Invitations: `logic/invitation`, Inviter service, `Granter`/`MemberCheck` ports, resolve-on-registration, email templates, routes, storetest sub-runner | L | A1, A5 | opus |
| A7a | Stores, turso: extend `stores/turso` — migrations 0006+ (the canonical version set), all new repositories, crud re-typing (§9), env-gated live conformance leg | L | A2–A6 | opus |
| A7b | Stores, pgx: extend `stores/pgx` — identical version filename set to A7a's, all new repositories, env-gated live conformance leg | M | A7a | opus |
| ~~A8~~ | **STRUCK (ratified AV10, 2026-07-07)** — the durable security-event rail is deferred out of this milestone; see the deferred disposition below | — | — | — |
| A9 | Proof host: extend `examples/auth-cms` — fake OAuth provider end-to-end; API-key mint→authenticated machine call; **JWT-bearer leg**: `POST /auth/token` with credentials → `Authorization: Bearer` against a `RequirePrincipal` route → 200, plus an expired-token 401 and the absent-signer path (bearer JWTs never parsed); invitation invite→accept via **toy membership Granter**; verified-email gate on; audit rows visible; run-and-look protocol per flow (curl transcripts; green tests alone do not close it) | M | A2–A6 | opus |
| A10 | Docs sync: auth README (route surface, Config/Repositories nil-semantics tables per charter item 12), the **host-facing upgrade note for the A1 session invalidation (README + RELEASING, §7.3)**, wiring page, ARCHITECTURE/README/RELEASING counts, capability-map v2 rows marked BUILT, NOTES artifacts (live store runs per dialect) | S | all | fable |

Sequencing: A1 first (everything touches `authsvc`); A2/A3 parallelizable
after A1; A5 wants A2/A3's ops to exist; A7a/A7b gate milestone close (DP1
parity + recorded live runs; split per the auth-v1 store-phase precedent —
one L phase carrying six domains' SQL for both dialects was a single point
of milestone-close failure); A9 before A10.

**Deferred — the durable security-event rail (ratified AV10).**
Trigger: **the first real durable consumer** (webhooks, alerting, or any
host that must react to security events rather than query them). Scope
when it returns (the struck A8's, unchanged): the `Events
[]events.Record` input field with the MAY-drop contract, the per-dialect
`OutboxAppender` + per-store appender tests, host wiring docs + the
migration-ordering prerequisite. Governing law travels with it, all
retained in this doc: §5.2's contract, the **events-contract re-check
gate**, the **combined-guarantee statement** (at-least-once
if-committed), and the acceptance grep (auth store modules contain no
`features/events` import). Dependencies when cut: A5's audit rail, both
A7 store phases, and events-v1 shipped. It is the only auth-v2-designed
work with an events-v1 dependency (ratified AV11).

### Milestone `authorization-v1` (follow-on)

| phase | what | size | depends on | model |
|---|---|---|---|---|
| Z1 | `features/authorization` core: engine salvage (check/through/cycle guards/batch/lookup/membership/schema validator; `explain`/cache decorator only if free), model DSL, socket (`New`/`Register`), crud re-typing, `memstore/` + `storetest` with **named adversarial sub-runners** (not categories): membership cycle (A∈B, B∈A), ≥3-level nesting, diamond/multi-path membership dedup **with a `CountByResourceAndRelation` assertion** (§2.5 — a count divergence is a security divergence), nested userset (`group#member@group#member`), and `LookupResult.Unrestricted` wildcard semantics | L | — | opus |
| Z2 | `stores/turso` + `stores/pgx`: tuple + metadata SQL, recursive-CTE expansion, migrations source `"authorization"`, env-gated live legs — **all Z1 named sub-runners green per dialect** (split into Z2a/Z2b at plan cut per the A7 precedent if preferred) | L | Z1 | opus |
| Z3 | `groups` aggregate (`logic/group` + store tables) — **trim candidate at plan cut**: build only if Z4's demo wants named groups; tuples alone already give group semantics | M | Z1 | opus |
| Z4 | Consumer seams + proof host: extend the auth-cms proof host (or a new example) — declare a model, wire `Authorizer.Check` closures into a consuming surface (events' `AuthorizeStream` if mounted, or a host-gated route), swap A9's toy Granter for `CreateRelationships`; **plus two demonstrations (review-gate amendment): (a) the middle posture made real — a host satisfying a Check seam with a plain ownership closure and NO ReBAC in its module graph (`go list -m all` clean; the ruling's point, demonstrated not asserted); (b) a `LookupResources`-backed "list what this subject may view" call so the enumeration API doesn't ship unexercised**; run-and-look: invite → accept → tuple exists → Check allows → gated surface 200s, and denies without the tuple | M–L | Z1, Z2, auth-v2 | opus |
| Z5 | Docs sync: feature README (**opens with §2.1's three-posture decision table before any model-DSL/engine content**, and states the cms-admin-gating-stays-coarse boundary explicitly — a documented boundary, not a gap), the authorization wiring page, **the G5 feature→feature guard (new Makefile target, prove-can-fail) + the add-or-consciously-defer decision on its store-module sibling (§10)**, registration artifacts (`go.work`, Makefile `MODULES`/`STORE_MODULES`/`test-stores`), ARCHITECTURE/RELEASING, capability-map ReBAC rows updated, NOTES live-run artifacts | S–M | all | fable |

## 14. Checklist trace (charter §8, for the new/changed modules)

1. Standalone compile — both feature cores own `go.mod`s;
   `features/authorization` requires exactly `sdk`.
2. No datastore drivers in either core — new domains are ports + entities;
   drivers stay in `stores/*` (auth keeps its v1 leanness: sdk only).
3. No imports of integrations/examples/own stores — G2 is a blanket
   pattern grep (no per-feature list) and auto-covers
   `features/authorization`; the artifacts that DO need edits are
   `go.work` + Makefile `MODULES`/`STORE_MODULES`/`test-stores` (Z5). The
   feature→feature edge gains its own guard, G5 (Z5, §10).
4. `Register(mount, repos, cfg) error` conforms in both; authorization v1
   touches `mount.Logger` only (jobs precedent); `New`/`NewService`
   exported constructors follow the auth/jobs socket precedent.
5. Store adapters expose `Repositories(db)` + `ExportMigrations(dst)`;
   sources `"auth"` (0006+) and `"authorization"` (0001+) stay
   collision-free in the shared ledger.
6. Zero-infra proof — A9 (authmem + toy Granter + fake Provider) and Z4
   (memstore) both `go run` with no driver in the graph.
7. READMEs document route surfaces (`/auth/*` grown; `/authorization/*`
   claimed-unregistered), Config fields, nil semantics (item 12's tables
   are A10/Z5 deliverables).
8. No `init()`, no service locator — model schemas, providers, granters
   are all explicit `Config` data.
9. No feature→feature imports — `Granter`, `AuthorizeStream`-class ports,
   and subject strings are the seams; greps in both directions stay empty
   (the A9/Z4 acceptance repeats auth-v1's rule-6 check).
10–11. `storetest` grows per new port; {turso, pgx} parity gates each
   milestone's close with dated NOTES live-run artifacts per dialect.
12. Every optional port's nil row is in this doc (§§3–7) and lands in the
   READMEs verbatim.

## 15. Risks

1. **The appender pattern gets real in the AV10-deferred durable-rail
   phase** — the events design priced it on paper (§5 costs); auth pays it
   across two dialect stores + memstore + suite when the trigger fires.
   The ratified deferral (AV10) removes it from auth-v2 entirely;
   remaining mitigations when it returns: the re-check gate and the
   MAY-drop contract keeping the neutral suite honest. If events-v1
   execution reshapes the contract, only §5.2 and the deferred
   disposition move.
2. **Scope mass in auth-v2** — six domains land in one feature. Mitigation:
   the phase seams are real (each domain = port + service + routes +
   storetest sub-runner, independently green); A2/A3 parallelize; AV7's
   trims are the pressure valve, and the PM review gate exists to cut
   deeper if needed.
3. **Silent divergence between the memstore's Go group expansion and the
   stores' recursive CTEs** — the one place the flagship could authorize
   differently per backend. Mitigation: the NAMED adversarial sub-runners
   (Z1: membership cycle, ≥3-level nesting, diamond dedup with the Count
   assertion, nested userset, Unrestricted wildcard) are acceptance
   criteria of Z1/Z2 per dialect, not nice-to-haves — and §2.5 pins the
   Count semantics they assert against.
4. **Session invalidation at A1 deploy** (hashing) surprises a live host.
   Mitigation: the host-facing upgrade note is an explicit A10 deliverable
   in README/RELEASING (§7.3), not only a NOTES artifact; pre-tag, the
   blast radius is the example hosts.

## Consultation notes

`lead-backend-engineer` reviewed the pre-write sketch (single hop).
Verdict: ship-with-edits; both load-bearing structural calls (own module;
Check-only seam) confirmed. Material findings adopted: the own-module
rationale now **leads with schema-optionality-only-at-the-source-boundary**
(release cadence demoted to supporting); "CheckBatch as optional seam
capability" deleted as shape-contradictory — batch lives on the concrete
Authorizer; the **`fop` → `sdk/crud` re-typing** named as explicit
pervasive work (§9); session hashing pinned **service-side** with stores/
storetest untouched and the `Session.Token` field asymmetry resolved
(§7.3); invitations' Granter narrowed to **grant-only** with visibility on
table columns and the invitation-as-resource tuples dropped, MemberCheck
split out (§6); the **milestone-stranding** problem (invitations' flagship
Granter arrives a milestone later) resolved via the toy-membership Granter
in A9; memstore **group-expansion conformance honesty** made a named
acceptance criterion (Z1/Z2); the audit-write's always-on-when-wired
property confirmed and A8 explicitly gated off the critical path. The
lead's principals pushback (registry table buys nothing without enforced
FKs) is adopted as AV5's recommendation. Salvage-analysis passes over the
original repo (authorization engine; the v2 `With*`/OAuth/invitations
surface) ground §§2–6's signatures and the §9 hazards — notably the
unimplemented-`GetByHash` trap, the dead `last_used_*` columns, the
no-machine-JWT finding (AV6), and the three-flows-on-one-codes-table
coupling that §3's dedicated `oauthstate` domain unwinds.

## Open questions

None — §12 is fully ratified (2026-07-07, jrazmi; NOTES.md entry). The
events-contract re-check (§5.2) is a gate on the AV10-deferred phase with
an owner and a trigger, not an open question.

## Recommended reviews

Both gates are closed: the review gate ran 2026-07-07 (three
ratify-with-amendments, applied), and **jrazmi ratified the design
2026-07-07** — all recommended defaults, AV10/AV11 applied structurally
(status header; NOTES.md entry is the record). What remains, at each plan
cut (the events precedent): the tier-review question re-runs; plus
**platform-sre** (migration phasing 0006+/new source, the
session-invalidation upgrade note, audit-log retention posture, live-leg
gating) and **data-integration-reviewer** (recursive-CTE parity,
metadata-index divergence, the named storetest sub-runners' coverage).

## Notes

- Reference-only salvage sources (design ported, code re-typed fresh):
  `gopernicus-original/core/auth/authorization/{authorizer,model,builder,schema_validator,membership,explain,cache_store}.go`
  (+ its 2,650-line test suite as behavioral reference);
  `core/repositories/rebac/{rebacrelationships,rebacrelationshipmetadata,groups,invitations}/`;
  `core/auth/authentication/{oauth_flow,oauth_linking,apikey,security_events,sensitive,crypto,model}.go`
  + `satisfiers/`; `core/auth/invitations/{inviter,model,events}.go`;
  `bridge/auth/authentication/{oauth,oauth_model,http}.go`;
  `bridge/auth/invitations/`; `bridge/transit/allowlist/`;
  `bridge/transit/httpmid/authenticate.go`;
  `workshop/migrations/primary/{0001_auth,0002_rebac}.sql`.
  Deliberately not salvaged: the satisfier layer, the principals registry
  (AV5), mobile OAuth + code-gated unlink (AV7), bus-driven invitation
  subscribers, generated CRUD bridges (workshop v2), `PostfilterLoop`
  (§2.6 demand gate).
- This doc is the fourth sibling in the roadmap set; like events/jobs it
  states requirements one-directionally: it consumes the events outbox
  contract (§5.2, gated) and the shipped sdk-parity surfaces
  (`sdk/oauth`, `sdk/cryptids`, the two oauth integrations, golang-jwt)
  without amending any of them.
