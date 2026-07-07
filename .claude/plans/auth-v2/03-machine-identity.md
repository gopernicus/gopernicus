# Phase A3 — machine identity: API keys, service accounts, principal resolution, middleware

Status: RATIFIED (cut from design §4.1–§4.3)
Executor model: opus
Depends on: A1 (parallelizable with A2).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §4.1 (both
domains, the pinned dotless key encoding, the GetByHash + TouchLastUsed
salvage fixes, NO scopes), §4.2 (ratified AV5 — NO principals table;
`auth.Principal{Type, ID}` value type), §4.3 (middleware surface,
two-dot bearer classing applied only for configured credential classes),
§9 (crud-typed listing).

## Work items

1. **`logic/serviceaccount`** (public rim): `ServiceAccount{ID, Name,
   Description, CreatedBy, ActAsUser, OwnerUserID, CreatedAt, UpdatedAt}`
   with the invariant `ActAsUser → OwnerUserID != ""` enforced at
   construction (`errs.ErrInvalidInput`); repository — `Create`, `Get`,
   `List` (`sdk/crud.ListRequest`-typed; **port doc pins the ordering:
   `ORDER BY created_at DESC, id DESC` — the id tiebreak is contractual**,
   plan-cut amendment), `Update`, `Delete`; sentinels per the v1 port
   precedent.
2. **`logic/apikey`** (public rim): `APIKey{ID, ServiceAccountID, Name,
   KeyPrefix, KeyHash, ExpiresAt, RevokedAt, LastUsedAt, CreatedAt}` +
   repository — `Create`, `GetByHash`, `ListByServiceAccount` (port doc
   pins `ORDER BY created_at DESC, id DESC`), `Revoke`, `TouchLastUsed`.
   **THE PINNED GetByHash CONTRACT (plan-cut amendment — supersedes cut
   refinement 3; stated identically here, in A5 WI2, and in A7a/A7b
   WI2):** the store selects by `key_hash` ALONE and returns the record
   for ANY present row — revoked and expired rows INCLUDED; a NULL/absent
   `ExpiresAt` means never-expires; `errs.ErrNotFound` is returned ONLY
   for a genuinely-unknown hash; `ErrExpired` is NOT a port sentinel —
   revocation/expiry are SERVICE-layer branches (WI4), because the
   service needs the record to emit the `blocked` audit event with
   service-account attribution (the salvage source's own behavior). NO
   `last_used_ip` field (design §4.1 — the audit row carries IP). NO
   scopes.
3. **Key mint** (design §4.1 pins): secret from `sdk/id` (dotless base32
   alphabet), display prefix joined with `_` — a key can NEVER contain
   two dots (the §4.3 JWT-heuristic guarantee); SHA-256 at rest
   (`cryptids.SHA256Hasher`); plaintext returned exactly once at mint.
4. **`auth.Principal{Type, ID string}`** exported value type (AV5) +
   `Service.AuthenticateAPIKey(ctx, rawKey) (Principal, error)` — hash,
   `GetByHash`, then the SERVICE branches per the pinned contract:
   **revoked → deny (and A5 wires the `blocked` audit event WITH
   service-account attribution); expired (non-NULL `ExpiresAt` in the
   past) → deny (A5 wires the failure audit event); valid → proceed** —
   effective-principal resolution (`ActAsUser` → `Principal{Type:
   "user", ID: OwnerUserID}`, else `Principal{Type: "service_account",
   ID: sa.ID}`) + best-effort `TouchLastUsed` (errors logged, never fail
   auth). Structure the branches now; the audit calls themselves arrive
   in A5.
5. **Middleware** (design §4.3): `RequireServiceAccount` (API-key bearer
   only) and `RequirePrincipal` (either credential class; sets Principal
   in context; `Service.CurrentPrincipal(ctx)` alongside `CurrentUser`).
   `RequireUser` semantics untouched this phase (A4 adds its JWT arm).
   Bearer classing: exactly-two-dots ⇒ JWT path, else API-key path —
   each path active only when its credential class is configured.
6. **Lifecycle routes** (session-gated JSON; paths pinned, cut
   refinement 4): `POST /auth/service-accounts`,
   `GET /auth/service-accounts`, `POST /auth/service-accounts/{id}/keys`
   (mint — response carries the plaintext once),
   `GET /auth/service-accounts/{id}/keys`,
   `POST /auth/api-keys/{id}/revoke`. Strict decode throughout. The
   full generated-CRUD admin surface stays workshop-v2 (design §11).
7. **Nil semantics + validation** (cut refinement 5):
   `Repositories.APIKeys` and `Repositories.ServiceAccounts` are
   **both-or-neither** — one without the other →
   `ErrMachineReposRequired` at construction; both nil → subsystem off,
   routes not registered, bearer API-key path inert.
8. **`storetest` sub-runners** for both ports (+ reference impls).
   GetByHash's FOUR pinned cases (plan-cut amendment): unknown →
   `ErrNotFound`; revoked → record returned; expired → record returned;
   **valid-with-NULL-`ExpiresAt` → record returned** (the case that
   catches both the SQL-side-expiry-filter bug and the NULL-handling
   bug). Plus mint-uniqueness; a `ServiceAccounts.List` ordering +
   cursor-pagination case; and a same-`created_at` collision case for
   every paginated port asserting identical order AND `NextCursor`
   across reference/turso/pgx (the reference impl sorts the full
   population then pages). Note: `storetest.go`'s stale header comment
   ("no port paginates") gets updated here — the first paged ports land
   in this phase.
9. **Tests**: authsvc unit tests for mint/authenticate round-trip,
   act-as-user resolution, the three service branches (revoked / expired
   / valid — asserting deny vs proceed per the pinned contract),
   TouchLastUsed best-effort (failing repo does not fail auth);
   middleware tests for classing + configured-class gating; route tests
   (401 unauthenticated, strict decode).

## Acceptance

```sh
cd features/auth && go build ./... && go vet ./... && go test ./...
make check
```

Rule-6 grep (import-anchored, plan-cut form):
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
→ empty.

## Real-interaction check

Standing check (a); check (b) unchanged. Deny-by-absence proof: boot
`examples/auth-cms` (machine repos unwired) →
`curl -s -o /dev/null -w '%{http_code}' localhost:8082/auth/service-accounts`
→ **404**; a `Bearer not-a-jwt` header against an existing session-gated
route → still 401 (API-key path inert). The wired machine-call
run-and-look is A9's.

## Execution log

(append dated entries here)

### A3 — 2026-07-07 — PASS

Executor: opus. Depends on A1/A2 (main tip `6a0ab0d`). All nine work items
landed; no ratified pin relitigated.

**Work items**

1. `logic/serviceaccount` — `ServiceAccount{ID,Name,Description,CreatedBy,
   ActAsUser,OwnerUserID,CreatedAt,UpdatedAt}` + `New` enforcing the invariant
   `ActAsUser → OwnerUserID != ""` (errs.ErrInvalidInput). `ServiceAccountRepository`
   = Create/Get/List(crud.ListRequest)/Update/Delete; port doc pins
   `ORDER BY created_at DESC, id DESC`.
2. `logic/apikey` — `APIKey{ID,ServiceAccountID,Name,KeyPrefix,KeyHash,ExpiresAt,
   RevokedAt,LastUsedAt,CreatedAt}` (zero ExpiresAt/RevokedAt/LastUsedAt = absent)
   + `Revoked()`/`Expired(now)`. `APIKeyRepository` = Create/GetByHash/
   ListByServiceAccount/Revoke/TouchLastUsed. GetByHash doc states THE PINNED
   CONTRACT verbatim (select by key_hash alone; return ANY present row incl.
   revoked/expired; NULL ExpiresAt = never; ErrNotFound only for unknown; NO
   ErrExpired sentinel). No last_used_ip, no scopes.
3. Key mint (`authsvc.mintAPIKeySecret`): prefix = first 8 chars of `id.New()`
   (dotless base32, stored plain); raw = `prefix + "_" + secret`, secret =
   `id.New()+id.New()` (~256 bits). SHA-256 of the whole raw at rest
   (`s.tokenHasher`, the cryptids SHA256 primitive); plaintext returned once from
   MintAPIKey. Zero dots guaranteed (unit-asserted via isJWTToken).
4. `auth.Principal = authsvc.Principal` (alias → exactly one value type, no import
   cycle). `AuthenticateAPIKey` hashes → GetByHash → SERVICE branches: revoked →
   deny (comment marks the A5 `blocked` audit + sa attribution via
   key.ServiceAccountID); expired → deny (comment marks A5 failure audit); valid
   → effectivePrincipal (ActAsUser → {user,OwnerUserID}, else
   {service_account,sa.ID}) + best-effort TouchLastUsed. Every deny returns a
   generic errs.ErrUnauthorized.
5. Middleware: `RequireServiceAccount` (API-key bearer only), `RequirePrincipal`
   (session or API-key bearer; JWT arm inert until A4), `CurrentPrincipal`.
   Two-dot classing (`isJWTToken`); each path active only when its class is
   configured. RequireUser untouched.
6. Routes (session-gated, `mountMachine`, mounted only when MachineEnabled):
   POST/GET `/auth/service-accounts`, POST/GET
   `/auth/service-accounts/{id}/keys`, POST `/auth/api-keys/{id}/revoke`. Strict
   decode; mint response carries the plaintext once, listing never re-exposes it.
7. Both-or-neither: `ErrMachineReposRequired` in NewService when exactly one of
   Repositories.{ServiceAccounts,APIKeys} is wired; both nil → off (routes not
   registered, bearer API-key path inert).
8. storetest: header "no port paginates" comment replaced (the first paged ports
   land here). ServiceAccounts + APIKeys sub-runners (skip-loud when nil) with
   the FOUR pinned GetByHash cases (unknown→NotFound / valid-NULL-expiry / revoked
   / expired all return the record), mint-uniqueness (+ hash-collision
   ErrAlreadyExists), List ordering+cursor pagination, and same-created_at
   collision (id tiebreak) for BOTH paged ports. Reference impl sorts the full
   population then keyset-pages (`pageDESC`/`afterCursorDESC`).
9. Tests: authsvc unit (round-trip, act-as-user, revoked/expired/valid branches,
   never-expires, unknown, TouchLastUsed best-effort, subsystem-off) + middleware
   (classing, configured-class gating, session vs API-key resolution); http route
   tests (deny-by-absence 404, 401 unauthenticated, strict decode, mint-once,
   unknown-sa 404, bad expires_at 400, revoke 404/200); auth_test both-or-neither
   + Register deny/mount; domain New-invariant tests.

**Acceptance**

- `cd features/auth && go build ./... && go vet ./... && go test ./...` → PASS
  (all packages ok; new sub-runners + tests green).
- `make check` → `all checks passed` (26-module set + 4 guards + integration-tag
  vet).
- Rule-6 grep `grep -rn --include='*.go' -E
  '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)'
  features/auth/` → empty (exit 1).
- `features/auth/go.mod` requires exactly `sdk`.

**Real-interaction checks**

- (a) `make check` green; `examples/minimal` :8081 → `GET /` 200, `GET
  /products/widget-3000` 200; killed; port free (000).
- (b) `examples/auth-cms` :8082 cookie-jar (`/terms` is the admin route):
  GET /terms 401 → register 201 → login 200+cookie → GET /terms 200 → logout 200
  → GET /terms 401. Exact codes: `401,201,200,200,200,401`.
- (c) same booted host (machine repos unwired): `GET /auth/service-accounts` →
  **404**; `Authorization: Bearer not-a-jwt` on `/terms` → **401** (API-key path
  inert; RequireUser ignores the bearer). Killed; port free (000).

**Divergences / notes**

- Naming: the phase file's WI1 says `List` (design §4.1 said `ListPage`); followed
  the phase file (authoritative) — the repo method is `List(ctx, crud.ListRequest)`.
- `Principal` is defined canonically in internal `authsvc` and re-exported as
  `auth.Principal` via a type alias (Go can't have the public pkg import its own
  internal pkg's owner without a cycle; the alias yields exactly one value type
  per AV5). Fields exported, host-constructable.
- TouchLastUsed best-effort: the error is SWALLOWED (`_ =`) rather than logged —
  `authsvc` holds no logger and threading one is outside A3's surgical scope. The
  design's "errors logged" and the `blocked`/failure/success audit calls arrive in
  A5 (the branch sites carry `A5:` comments). WI9's requirement (a failing repo
  does not fail auth) is asserted.
- expires_at on mint accepted as optional RFC3339 (empty → never-expires); a
  malformed value is a 400.
- No store-module code (A7a/A7b) and no audit writes (A5), per scope.
