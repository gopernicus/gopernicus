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
