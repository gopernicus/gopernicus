# `features/auth` — design sketch (no code)

Status: **IMPLEMENTED 2026-07-02** — auth-v1 milestone executed (all 7
phases; logs in `.claude/plans/auth-v1/*`); the real code is
`features/auth` (+ `stores/{turso,postgres}`, `integrations/cryptids/
bcrypt`, `examples/auth-cms`). §2's module-shape tree predates the
same-day trio re-layout (`roadmap/feature-trio-relayout.md`): domain
packages now live at `logic/{user,session,verification}`, services at
`internal/logic/authsvc`, HTTP at `internal/inbound/http`. Design content
otherwise implemented as written.
(original status: executed 2026-07-02 (phase 4, W2 + W3))
Depends on: `features/README.md` (the charter this design is held to),
`capability-map.md` (the classification this design implements),
`00-overview.md` (the constitution — every design call below traces to a rule).

This is a design document only. Nothing here is built. The next milestone
implements it; this phase proves the feature contract holds for a *second*
real feature and a *cross-feature* composition, not just the illustrative
`CurrentUser` example in `features/README.md` §5.

## 1. Scope v1

**v1 ships:** registration, email verification, login, logout, password
change, password reset, and a `RequireUser` identity middleware other routes
(cms admin, in the proof host) can gate on. All server-side session state
(opaque token, cookie-delivered), no JWT.

**v1 explicitly excludes**, per the phase brief's own list, plus what the walk
added:

| excluded | why v1 skips it | where it lands |
|---|---|---|
| OAuth / OIDC (Google, GitHub) | Needs its own port (`Provider`) + two integration adapters + redirect/PKCE flow — real scope, not core session identity | `features/auth` v2 subdomain; `integrations/oauth/{google,github}` (`capability-map.md`, Authentication section) |
| API keys | Needs a distinct principal model (a key acts *as* a user, not *is* one) and JWT-based bearer auth | `features/auth` v2, paired with JWT signer |
| Invitations | Needs authorization (ReBAC tuple creation on accept) and the event bus — both themselves deferred | `features/auth` v2, gated behind authorization |
| Authorization / ReBAC | `capability-map.md`'s YOUR CALL: defer past v1 entirely; v1's only access decision is "does a valid session exist" | future `features/auth` v2+ subdomain or standalone feature, built when a real fine-grained-permission need appears |
| Tenancy | `capability-map.md`'s YOUR CALL: fold into auth as a v2+ subdomain; the original never gave it a dedicated layer either | `features/auth` v2+ |
| Service accounts / security-event logging | Optional extensions even in the original `Authenticator` (`With*` options, not the required `Repositories`) | `features/auth` v2 |
| JWT / bearer-token identity | v1 targets browser session identity only; JWT is a v2 concern paired with API keys (machine clients need stateless verification; browsers don't) | `features/auth` v2, `integrations/cryptids/golangjwt` |

**Why this exact cut is principled, not arbitrary.** The original's own
`authentication.NewRepositories` constructor took exactly five required
parameters — `users`, `passwords`, `sessions`, `tokens` (verification), `codes`
(verification) — with OAuth, API keys, service accounts, and security events
all wired in later as optional `With*` calls on the `Authenticator`
(`core/auth/authentication/authenticator.go`). v1 mirrors the original's own
required/optional boundary; it is not a new, weaker cut invented for this
milestone.

## 2. Module shape

Mirrors `features/cms`'s anatomy (`features/README.md` §2), generalized:

```
features/auth/
  auth.go              Repositories, Config, PasswordHasher, Service, NewService(repos, cfg)
                        (*Service, error), Register(mount, repos, cfg) error — see §3 for why
                        NewService lives here rather than a separate file: features/README.md
                        §2's anatomy table calls <name>.go "the feature's entire host-facing
                        surface," so this design keeps that literally true rather than adding
                        a second public entry point elsewhere
  user/                User entity + UserRepository port
  session/             Session entity + SessionRepository port
  verification/        VerificationCode/VerificationToken entities + two repository ports
  internal/authsvc/     domain services: registration, login, logout, password change/reset,
                        session validation — business rules over the ports, no HTTP/SQL
  internal/http/        route table (Mount), JSON handlers — no templ views (v1 is JSON-API-only,
                        see rationale below)
  stores/turso/         a separate module: SQL + canonical migrations for the 5 v1 entities,
                        ExportMigrations, migration source Name = "auth"
  stores/postgres/      same, once integrations/datastores/postgres exists (capability-map.md)
```

**No `theme/` in v1.** Unlike cms, auth v1 has no server-rendered pages —
`POST /auth/register`, `/auth/login`, `/auth/logout`, `/auth/verify`,
`/auth/password/forgot`, `/auth/password/reset` are JSON endpoints, mirroring
the original's own `bridge/auth/authentication` request/response DTOs. This
is a deliberate scope choice (not an oversight): D7 accepts view dependencies
in a feature's core only "where the feature legitimately owns them" (the cms
precedent — real HTML pages a visitor browses). Auth v1 has no such surface;
a host wanting a login *page* renders its own form and calls the JSON API,
exactly as a SPA or mobile client would. This keeps `features/auth`'s
`go.mod` stdlib + sdk only, with zero view deps — leaner than cms, and it
sidesteps C1's absolute-link limitation entirely (JSON bodies have no
in-page navigation to break under a prefix).

**Repositories** (five ports, matching §1's table exactly):

```go
type Repositories struct {
    Users              user.UserRepository
    Passwords          user.PasswordRepository        // separate from Users — see rationale
    Sessions           session.SessionRepository
    VerificationCodes  verification.CodeRepository     // email verification (short-lived)
    VerificationTokens verification.TokenRepository    // password reset (longer-lived)
}
```

`PasswordRepository` stays split from `UserRepository`, mirroring
`core/repositories/auth/{users,userpasswords}` — a real security-hygiene
property worth preserving, not cargo-culted: credential material is
queryable/rotatable independently of general user reads, and a store adapter
can apply tighter access control to the passwords table without touching the
users table's shape.

**Ports, and where they live** (constitution rule 3: *ports live with their
consumer, never their implementor*):

- `user.UserRepository`, `user.PasswordRepository`, `session.SessionRepository`,
  `verification.CodeRepository`, `verification.TokenRepository` — declared in
  their respective domain packages, exactly like `content.EntryRepository`
  lives in `features/cms/content`.
- `PasswordHasher` (`HashPassword`, `VerifyPassword` — porting
  `core/auth/authentication/authenticator.go`'s interface) — declared in the
  **top-level `auth` package**, not a domain subpackage. Rationale: it isn't
  a domain entity's port (no natural home in `user/` or `session/`); it's a
  dependency of the feature's own service, the same role `cacher.Storer` or
  `email.Sender` play in `cms.Config` — except, unlike those, it is **not**
  an sdk type. The phase brief calls this out explicitly as "the good pattern
  to generalize": `core/auth/authentication` already declares `PasswordHasher`
  and `JWTSigner` itself, satisfied *structurally* by `infrastructure/
  cryptids/{bcrypt,golangjwt}` with zero import in either direction. v1
  ports exactly that pattern for `PasswordHasher`; `JWTSigner` is v2 (§1).

**Why `PasswordHasher` is feature-owned, not an sdk facility.** `sdk/README.md`'s
admission policy requires plurality ("two+ real implementations exist or are
genuinely foreseen"). Cache, email, and file storage all have that — many
features could plausibly want them. Password hashing has exactly one
consumer today and none genuinely foreseen elsewhere; promoting it to sdk
would violate the policy's first test on a hope, not evidence. This is also
`capability-map.md`'s explicit call in the Authentication section.

**bcrypt placement:** `integrations/cryptids/bcrypt` (wraps
`golang.org/x/crypto/bcrypt`, satisfies `auth.PasswordHasher` structurally),
mirroring the original's own naming (`infrastructure/cryptids/bcrypt` →
`integrations/cryptids/<tech>`) per W2's instruction and constitution rule 1
(third-party lib implementing a port → integration).

**Config:**

```go
type Config struct {
    Hasher       PasswordHasher       // REQUIRED — no default; see below
    Mailer       email.Sender         // sdk/email — verification/reset emails; nil is invalid in v1
    MailFrom     string
    RateLimiter  ratelimiter.Limiter  // sdk/ratelimiter — nil → ratelimiter.NewMemory() (D6 default)
    SessionCookie CookieConfig        // name/secure/domain/max-age, mirrors the original bridge's cookie handling
}
```

**Deliberate asymmetry with cms's `Config`**: cms's `nil Cache disables public
caching`, `nil Views uses bundled chrome` — safe, silent defaults, because a
missing cache or missing custom chrome degrades gracefully. `Hasher` and
`Mailer` get **no** silent default in v1: `Register`/`NewService` return an
error if either is nil. A password feature with no hasher, or that silently
drops verification/reset emails, is a security foot-gun, not a convenience —
unlike `RateLimiter`, where "unbounded" (no limiter or a permissive Memory
default) is a safe-by-default direction rather than an unsafe one.

**Migrations:** `features/auth/stores/turso` (and later `stores/postgres`)
registers `feature.MigrationSource{Name: "auth", FS: ..., Dir: ...}` via
`mount.Migrations.Register` — a sibling entry alongside cms's `"cms"` source
in the same host's shared ledger (D4). Distinct `Name` guarantees no
collision; checklist item 5 covers this directly.

## 3. The contract stress points

### Middleware: how does a feature export HTTP middleware for OTHER routes to use?

**Answer: a new public `Service` type, declared in `auth.go` itself,
alongside `Register` rather than replacing it.**

`cms.Register(mount, repos, cfg) error` never needs to hand anything back to
the host — cms is self-contained. Auth is not: cms's admin routes need to
*consume* auth's identity check, and the host is the only party allowed to
wire two features together (constitution rule 6). `Register`'s signature
(`Register(mount feature.Mount, repos Repositories, cfg Config) error`) has
no room for a return value beyond `error` — and per `features/README.md`
checklist item 4, it shouldn't gain one; that signature is the contract's
uniform entry point.

**On file placement**: `features/README.md` §2's anatomy table states
`<name>.go` is "public — the feature's entire host-facing surface." An
earlier draft of this design put `Service`/`NewService` in a separate
`service.go`, which technically satisfies checklist item 4 (`Register`'s
signature is untouched) but quietly contradicts that stronger, unqualified
anatomy-table sentence by adding a *second* host-facing entry point outside
`<name>.go`. Fixed by declaring `Service`/`NewService` directly in `auth.go`
— the file-level claim holds literally, not just by convention. (Flagged by
the W3 adversarial pass; see the Appendix.)

The design adds one new exported constructor, used *alongside* `Register`,
not instead of it:

```go
// NewService builds the auth feature's identity capability without mounting
// HTTP routes — the surface other features/hosts consume directly.
func NewService(repos Repositories, cfg Config) (*Service, error)

// RequireUser is HTTP middleware gating a route on a valid session. It
// satisfies sdk/web.Middleware via a method value: authSvc.RequireUser.
func (s *Service) RequireUser(next http.Handler) http.Handler

// CurrentUser matches features/README.md §5's illustrative cms.CurrentUser
// port exactly — structural satisfaction, zero import either direction.
func (s *Service) CurrentUser(ctx context.Context) (userID string, ok bool)
```

A host wanting both auth's own routes *and* cross-feature identity does:

```go
authSvc, err := auth.NewService(authRepos, authCfg)   // for cross-feature wiring
// ...
err = auth.Register(mount, authRepos, authCfg)         // for auth's own HTTP routes
// ...
cms.Register(cmsMount, cmsRepos, cms.Config{
    // ... a future cms.Config field, e.g. AdminMiddleware: []web.Middleware{authSvc.RequireUser}
})
```

**Known wrinkle, flagged not hidden:** this builds `Service` twice (once
inside `Register`, once for the host). `Service` holds no mutable state of
its own beyond the already-shared `Repositories`/`Config` values — no
connection pooling, no in-process cache — so the duplication is two struct
allocations pointing at the same dependencies, not a correctness or resource
issue. It is, however, a real seam an implementer should notice; recorded
here rather than smoothed over.

**Real cost this design surfaces, out of this phase's scope:** exercising
this for real requires a small `features/cms` change — `cms.Config` has no
middleware hook today (`Views`/`Types`/`Templates`/`Cache`/`Blobs`/`Mailer`/
`MailFrom`/`ContactTo`, no `AdminMiddleware`). **Today, `examples/cms` and
`examples/minimal`'s admin routes are unauthenticated** — this design
exercise is what surfaces that gap concretely. Adding the hook is real,
small, in-scope-for-the-*next*-milestone code, not something this
analysis-only phase builds. Flagged for jrazmi below.

**Demands of `sdk/Mount`: none.** `Service`/`RequireUser`/`CurrentUser` are
ordinary exported Go values the host wires by hand — exactly rule 5's
"explicit calls in a host's main," not a `Mount` field.

### Identity-in-context: which package owns the context key + accessor?

**Answer, revised after the W3 adversarial pass: `features/auth` owns it,
unexported, in v1 — it does not graduate to `sdk` yet.**

An earlier draft of this design proposed a new `sdk/identity` package
(`WithUserID`/`UserIDFromContext`), reasoning it satisfied `sdk/README.md`'s
plurality test because both auth and cms "need" the convention. The W3 pass
caught that this doesn't survive tracing the actual call graph: **cms never
touches the context key at all.** `cms`'s only contact with identity is
calling `auth.Service.CurrentUser(ctx) (userID string, ok bool)` — an
ordinary exported method on the port `features/README.md` §5 already
illustrates. Whatever `CurrentUser` does *internally* (read a context key,
query a session store, anything) is `auth`'s implementation detail, invisible
to cms. Only two places in the whole design touch the context key directly:
`auth.Service.RequireUser` (sets it) and `auth.Service.CurrentUser` (reads
it) — both inside `features/auth` itself. That's **one** real consumer
package, not two, and it fails the same plurality test the design already
(correctly) applies to keep `PasswordHasher` feature-owned rather than
sdk-owned — applying a looser standard to `identity` than to `PasswordHasher`
in the same document would be exactly the inconsistency the constitution's
admission policy exists to prevent.

**v1 design:** an unexported `contextKey` type + `withUserID`/
`userIDFromContext` functions live inside `features/auth` (e.g.
`internal/authsvc`, alongside `RequireUser`'s and `CurrentUser`'s
implementations) — same shape as the rejected `sdk/identity` proposal, same
inspiration (`sdk/logging`'s `contextKey` + `WithRequestID`/`FromContext`
pattern, `sdk/logging/context.go`), just unexported and feature-local because
that's what the evidence supports today.

**When this would actually graduate**: the day a *second* feature needs to
read or write the same identity-in-context value **without** going through
`auth.Service`'s exported API — e.g. a test harness injecting identity
directly for handler unit tests without a real login flow, or a future
feature whose own middleware needs to set identity ahead of auth being
mounted at all. That is a genuinely different, stronger scenario than v1
has, and the promotion should wait for it, exactly as `PasswordHasher`
waits for a second real consumer before sdk is even considered.

**Demands of `sdk`: none.** No new sdk package in v1.

### Rate limiting: wiring `sdk/ratelimiter` into login attempts

`Config.RateLimiter ratelimiter.Limiter` (nil → `ratelimiter.NewMemory()`,
the D6/phase-2-W5 default). `internal/authsvc`'s `Login` method calls
`limiter.Allow(ctx, key, ratelimiter.PerMinute(5))` (or a configurable limit)
**before** touching `PasswordRepository`, keyed on `email+client-IP` —
mirroring the original's `httpmid.RateLimit` key-derivation intent, but
applied as business logic inside the login flow rather than as generic
route-level middleware, because login-attempt limiting is semantically
specific to one endpoint's failure mode (credential stuffing), not a
general per-route concern. `capability-map.md`'s Rate limiting section
separately recommends a **generic** `sdk/web` rate-limit middleware
(backlog, not v1) for the broader case (arbitrary route throttling); v1 auth
does not need it and uses the service directly instead.

**Demands of `sdk`: none new.** `sdk/ratelimiter` already exists (D6, phase 2
W5); v1 is simply its first real consumer.

### Migrations: auth's namespace in the shared ledger

Covered in §2: `MigrationSource.Name = "auth"`, registered by
`features/auth/stores/<dialect>` via `mount.Migrations.Register`, alongside
`"cms"` in the same host ledger keyed `(source, version)` (D4). No collision
by construction — checklist item 5.

## 4. Proof plan: a zero-infra two-feature host

An `examples/minimal`-style host (`examples/auth-cms-minimal` or a variant
folded into `examples/minimal` itself — implementer's call) that mounts
**both** `auth` and `cms` with in-memory stores, proving constitution rule 6
("features never import other features") holds under a real second feature,
not just the illustrative C2 example.

**What "zero-infra" means here** (clarifying a possible misreading):
`examples/minimal`'s existing bar is "zero libsql/network datastore in its
module graph" — it is *not* "zero third-party Go modules." `bcrypt` is a
CPU-bound library with no external service dependency; importing
`integrations/cryptids/bcrypt` into a zero-infra host is exactly as
appropriate as the host importing `sdk/email.Console`. The proof host:

- **Stores**: an auth-specific, example-local, in-memory implementation of
  the five v1 repository ports — new code, sibling to
  `examples/minimal/internal/memstore`, not a reuse of cms's memstore (the
  entities don't overlap). **If this mirrors memstore's pattern, it must
  mirror memstore's honesty, not just its shape**: phase 2 W7 found that
  `examples/minimal/internal/memstore`'s `TermRepository.Create` and
  `MenuRepository.CreateMenu` don't enforce the uniqueness their own doc
  comments promise (`(kind,slug)` and slug collisions silently succeeded
  instead of returning `errs.ErrAlreadyExists`) — **RESOLVED 2026-07-02:
  memstore now enforces both and its tests assert it.** The auth
  proof store's `UserRepository.Create` **must** either enforce email
  uniqueness and return `errs.ErrAlreadyExists` on collision, or its doc
  comment must not promise that it does — and its test suite (mirroring
  `memstore_test.go`'s per-repository pattern) must assert or explicitly
  flag whichever is true, not silently pass either way.
- **Hasher**: `integrations/cryptids/bcrypt` — real, no infra dependency.
- **Mailer**: `email.Console` (sdk default, dev logger) — phase 2 found `email.NewConsole(nil)` panicked on `Send` despite
  its docs promising a nil-discard — **RESOLVED 2026-07-02: `NewConsole(nil)`
  now discards via `io.Discard`; the gotcha no longer applies.**
- **RateLimiter**: `ratelimiter.NewMemory()` (the config default; no
  override needed).
- **Cross-wiring**: the host builds `authSvc, _ := auth.NewService(...)`,
  then a (new, out-of-phase-scope) `cms.Config.AdminMiddleware` field
  receives `authSvc.RequireUser`, exercising §3's design end to end.

**What this proves**: `features/cms` never imports `features/auth`;
`features/auth` never imports `features/cms`; only the host's `main` imports
both — the exact shape of constitution rule 6 and the C2 worked example, now
demonstrated with two real, independently-versioned feature modules instead
of one real feature and one illustrative sketch.

## 5. Checklist trace (`features/README.md` §8, item by item)

1. **Module compiles standalone** — `cd features/auth && go build ./...`
   with its own `go.mod`. v1 has zero view deps (no templ/goldmark/
   bluemonday, unlike cms/D7) — leaner than the precedent, not a deviation
   from it.
2. **`go.mod` has no datastore driver** — only stdlib + sdk. `PasswordHasher`
   is a feature-declared port (§2); its bcrypt *implementation* lives in
   `integrations/cryptids/bcrypt`, a module `features/auth` itself never
   imports. Same for datastore drivers: they live in `stores/<dialect>`.
3. **Never imports `integrations/`, `examples/`, or its own `stores/*`** —
   true by construction: `auth.go` references only `user`, `session`,
   `verification` (same module) and `sdk/{web,errs,email,ratelimiter,id}` —
   no `sdk/identity` in v1 (§3 revises that call; the context-key convention
   stays unexported inside `features/auth`). Guard G2's module list needs
   `features/auth` added the day this is built — flagged so the implementer
   doesn't forget.
4. **`Register(mount feature.Mount, repos Repositories, cfg Config) error`
   exists, reaches only `mount.Router`/`mount.Migrations`/`mount.Logger`** —
   yes; `NewService` (§3) is *additional* surface, not a replacement, and
   touches no `Mount` field at all.
5. **Each `stores/<dialect>`'s migration source has a unique
   `MigrationSource.Name`** — `"auth"`, distinct from `"cms"` (§2).
6. **A minimal-host proof exists** — §4, in-memory repositories + real
   `integrations/cryptids/bcrypt` (no network infra) + `email.Console`.
7. **The feature's own README documents route surface, `Config` fields, and
   `Repositories` ports** — route surface: `/auth/{register,login,logout,
   verify,password/forgot,password/reset}`, claimed namespace `/auth/*`,
   prefixable via C1's `feature.PrefixRegistrar` with **less exposure to
   C1's known limitation than cms**: v1's JSON responses carry no in-page
   HTML links to break under a non-root prefix (the limitation is
   specifically about rendered `href`s; a JSON API has none). `Config`
   fields and `Repositories` ports: §2, in full. (To be written as
   `features/auth/README.md` when the feature is actually built — this
   design doc is the source content for it.)
8. **No `init()` registration, no package-level mutable registry** — `Service`
   is an explicit value the host constructs and threads by hand (§3); no
   global state anywhere in the design.
9. **No feature → feature imports** — `cms.CurrentUser`'s illustrative port
   (`features/README.md` §5) is satisfied structurally by
   `auth.Service.CurrentUser`; the host is the only party that imports both
   modules. §4's proof host is exactly this, exercised for real.

## Appendix — W3 adversarial review

A fresh subagent, given only this document, `00-overview.md`, and
`features/README.md` (no other context), was instructed to attack the design
for: hidden feature→feature imports, ports owned by implementors, `Mount`
bloat, `sdk` admission-policy violations, and `init()`/service-locator
temptations. Two passes were run: the first attack found two real issues; the
sketch was revised; a second pass re-attacked the whole document (not just
the fixed sections) and came back clean.

### Pass 1 — findings

1. **`sdk/identity` plurality test not actually satisfied (real violation).**
   The first draft proposed graduating the identity-in-context convention
   into a new `sdk/identity` package, reasoning that both auth and cms
   "need" it. Tracing the actual call graph the draft itself described:
   cms never touches the context key — it only calls
   `auth.Service.CurrentUser(ctx)`, an ordinary exported method `auth`
   implements however it likes. That leaves exactly **one** real consumer
   package (`features/auth`), failing `sdk/README.md`'s plurality test —
   and applying a looser standard to `identity` than the same document
   (correctly) applied to `PasswordHasher`, whose sdk-promotion the draft
   rejected on identical grounds ("hope, not evidence"). **Resolution**
   (§3, "Identity-in-context"): the context-key convention stays unexported
   inside `features/auth` in v1; no new `sdk` package. A concrete graduation
   trigger is named (a second feature needing the raw context value without
   going through `auth.Service`'s API) rather than left open-ended.
2. **`NewService` contradicted the anatomy table's "entire host-facing
   surface" claim (real violation).** `features/README.md` §2 states
   `<name>.go` is "the feature's entire host-facing surface." The first
   draft put `Service`/`NewService` in a separate `service.go` — technically
   satisfying checklist item 4 (`Register`'s signature untouched) but adding
   a second public host-facing entry point the anatomy table's stronger
   sentence doesn't account for. **Resolution** (§2 module-shape tree, §3
   "Middleware"): `Service`/`NewService` moved into `auth.go` itself, so the
   anatomy table's claim holds literally, not just by convention.

**Categories checked with no findings (pass 1):** hidden feature→feature
imports (none — the only shared touchpoint is `cms.CurrentUser`'s structural
satisfaction, both modules imported only by the host); ports owned by
implementors (none — `PasswordHasher` and all five repository ports are
declared by their consumers, matching `content.EntryRepository`'s
precedent); `Mount` bloat (none — the design touches no `Mount` field);
`init()`/service-locator temptation (none — the "`Service` built twice"
wrinkle is duplication, not global state, and is disclosed rather than
hidden); the `PasswordHasher`-in-top-level-package placement (justified —
constitution rule 3 requires the *consumer* to own the port, not a specific
subpackage shape, and `PasswordHasher` has no natural entity home); the
`Config` nil-handling asymmetry (justified — an explicit, reasoned exception,
not a silent contradiction of any documented convention).

### Pass 2 — re-verification + fresh full-document sweep

Both fixes confirmed sound against the actual call graph and file layout,
not just reworded. One new, non-blocking item surfaced from the fresh sweep:

3. **Anatomy table's literal three-item list vs. `auth.go`'s actual contents
   (documentation nit, not a design flaw).** `features/README.md` §2 still
   *enumerates* `<name>.go`'s contents as exactly "Repositories, Config,
   Register" — auth's `auth.go` legitimately adds `PasswordHasher`,
   `Service`, `NewService` under the same "entire host-facing surface"
   umbrella, but the table's literal wording predates a second feature
   stress-testing it. **Resolution:** not fixed in this design doc (it isn't
   this document's job to rewrite the charter) — flagged for jrazmi below as
   a small follow-up: generalize the anatomy table's `<name>.go` row from a
   closed three-item list to "the feature's host-facing exported surface
   (Repositories, Config, Register — plus whatever additional exported
   constructors/types a given feature's cross-feature or host-facing needs
   require)" the day `features/auth` is actually built.

All other constitution rules (1–8) and charter checklist items (1–9)
re-checked against the full revised document in pass 2: clean. No hidden
imports, no new `Mount` demands, no init()/registry pattern, dependency
direction intact (proof host → integrations/cryptids/bcrypt is
examples→integrations, the correct direction), `PasswordHasher` vs.
`identity` now treated consistently (both held to the same plurality
standard, both fail it today, both stay feature-owned). **Verdict: zero
unresolved violations** — the design closes the adversarial-review loop
clean.
