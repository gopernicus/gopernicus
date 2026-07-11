# 05 — Authorizer: `Check` is pure schema evaluation

**Milestone:** segovia-lessons (downstream feedback from segovia v2, plan 08 "auth layer-in")
**Status:** ✅ EXECUTED (2026-07-11) — engine cut + tests + host recipes + docs
landed; memstore/pgx/turso conformance green; auth-cms driven live (platform-admin
recipe + `{admin,ids}` enumeration confirmed).
**Feature:** `features/authorization`
**Verify:** `go build ./... && go test ./...` (workspace modules) + storetest conformance (memstore/pgx/turso) + boot auth-cms and re-drive the platform-admin / gated-route curl legs.

---

## The law

> **The engine evaluates the schema. Nothing else.** Policy short-circuits
> (platform-admin bypass, self-access) are HOST composition, expressed as
> ordinary schema-declared checks a host runs first in its own closure.

Both bypasses fail **closed**: a host that forgets the recipe locks admins out
/ grants no self-access — never fails open.

---

## Ratified decisions

- **D1 — Full removal.** Delete `checkPlatformAdmin` + `checkSelf` from the
  engine entirely. No `Config.PlatformBypass` / `Config.SelfAccess` flags. Both
  become documented host closure recipes.
- **D2 — `CheckRelationExists` STAYS on the Storer port.** _(Correction: the
  feedback's "drop if engine is the only caller" is refuted by evidence.)_
  Remaining non-engine callers:
  - `membership.go:18` — `RemoveMember` last-owner owner probe (§2.5 pin).
  - `service.go:391` — public `Service.CheckRelationExists` dedup primitive.
  - `storetest.go` — 7 conformance assertions (lines 72, 82, 110, 113, 129, 132, 139).
  Only the engine's **internal** `checkPlatformAdmin` call site is removed; the
  port method, its pgx/turso/memstore impls, and its storetest cases are untouched.
- **D3 — Remove `LookupResult.Unrestricted`.** Its only producer is
  `checkPlatformAdmin` (lookup.go:24→27, propagated :45, :120). With that gone
  the field is permanently false. Delete it; `LookupResources` becomes pure
  enumeration (always returns a non-nil `[]string`). Ripple: `auth-cms
  demo.go` `/demo/my-projects` JSON and `storetest/adversarial.go` `Unrestricted`
  subtest.
- **D4 — `Reason` vocabulary shrinks.** `"platform:admin"` and `"self"`
  disappear as engine-produced reasons. Informational only; docs + tests sweep,
  no contract break. (Note: `model.go:39` doc-comment lists `"platform_admin"`
  which the code never actually emits — it emits `"platform:admin"`; drop both
  from the comment.)

---

## Task list

### T1 — Engine cut (`internal/logic/authorizersvc/`)

**`service.go`**
- `Check` (76–96): drop the `checkPlatformAdmin` block (79–83) and the
  `checkSelf` block (85–87). Update the `// Order:` doc-comment (76–77) to:
  `Check evaluates schema rules only (direct relations + through-traversal with
  cycle detection).`
- `checkBatchOptimized` (287–362): drop the platform-admin probe (295–302) and
  the `checkSelf` loop (304–311). The remaining body starts from
  `getPermissionRules`; every request in `reqs` now goes through the rule path
  (build `needsDBCheck` as all indices, or simplify to iterate `reqs` directly).
- Delete `checkPlatformAdmin` (237–242) and `checkSelf` (244–260) entirely.
- **Keep** `Service.CheckRelationExists` (388–393) and its store delegation
  (D2) — unchanged.

**`lookup.go`**
- `lookupResourcesWithVisited`: drop the platform-admin block (24–28). Remove
  the `LookupResult{Unrestricted: true}` short-circuits at 44–46 and 119–121;
  those `throughResult.Unrestricted` / `targetResult.Unrestricted` guards go
  with the field.
- Update the `LookupResources` doc-comment (5–11): drop the "A platform admin
  yields `Unrestricted: true`" paragraph; state it always returns a non-nil IDs
  slice (empty = no access).

**`model.go`**
- Delete the `Unrestricted` field from `LookupResult` (59–62) and rewrite the
  type doc-comment (49–62) to the pure-enumeration contract.
- `CheckResult.Reason` doc-comment (38–39): drop `"self"` / `"platform_admin"`
  from the examples.

### T2 — Engine tests (`service_test.go`, `lookup_test.go`)

- **Delete** `TestCheckSelfAccess` (214–230) and `TestCheckPlatformAdminBypass`
  (232–246).
- **Add** two contract-flip tests proving the NEW posture:
  - A subject holding `platform:main#admin` is **DENIED** `delete` on an
    unrelated `post` (no schema rule grants it) — admin is no longer magic.
  - A subject gets **no implicit self-access**: `user:u1` reading `user:u1`
    with no tuple + no schema rule → denied.
- `lookup_test.go`: delete `TestLookupResourcesPlatformAdminUnrestricted`
  (47–61) and the `res.Unrestricted` assertions in the remaining tests
  (22, 36, 40); `TestLookupResourcesEmptyIsNonNil` keeps the non-nil-IDs assert.

### T3 — storetest conformance (`storetest/adversarial.go`)

- Rewrite the `Unrestricted` subtest (149–180). The platform-admin tuple no
  longer makes `LookupResources` unrestricted, and the admin no longer bypasses
  `Check`. Replace with: a platform-admin tuple holder is enumerated ONLY for
  resources it holds real grants on, and is **denied** on `doc/d1` (owned by
  u9). Keep the non-admin non-nil-IDs assertion. Rename the subtest
  (`Unrestricted` → e.g. `PlatformAdminIsNotMagic`).
- `CheckRelationExists` conformance cases (storetest.go) — **untouched** (D2).

### T4 — Host recipes (`examples/auth-cms/`)

**`main.go` (schema, 139–143)** — the `platform` type gains a permission rule so
the closure can `Check` it:
```go
{Name: "platform", Def: authorization.ResourceTypeDef{
    Relations: map[string]authorization.RelationDef{
        "admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
    },
    Permissions: map[string]authorization.PermissionRule{
        "admin": authorization.AnyOf(authorization.Direct("admin")),
    },
}},
```

**`membership.go` `requireMembership` (56–83)** — platform-admin becomes a
host-side check the closure runs FIRST:
```go
// Platform admin recipe (host composition — the engine no longer bypasses):
if res, _ := authorizer.Check(r.Context(), authorization.CheckRequest{
    Subject:    authorization.Subject{Type: p.Type, ID: p.ID},
    Permission: "admin",
    Resource:   authorization.Resource{Type: "platform", ID: "main"},
}); res.Allowed {
    next.ServeHTTP(w, r); return
}
// ...then the existing demoPermission Check on project/demo.
```

**`demo.go` `demoMyProjects` (93–111)** — `Unrestricted` is gone. Do the
host-side admin Check first and surface it as an explicit flag; the JSON
contract shifts from `{"unrestricted",...}` to `{"admin",...}`:
```go
// admin? host decides to skip ID filtering (real apps: don't call LookupResources).
adminRes, _ := authorizer.Check(r.Context(), authorization.CheckRequest{
    Subject: authorization.Subject{Type: p.Type, ID: p.ID},
    Permission: "admin", Resource: authorization.Resource{Type: "platform", ID: "main"},
})
res, err := authorizer.LookupResources(...)   // now returns only real grants
// writeHostJSON: {"admin": adminRes.Allowed, "ids": ids}
```
- The `demoBootstrapAdmin` seed tuple (demo.go:67–69) is **unchanged** — the
  `platform:main#admin` tuple is still the provisioning mechanism.

**`README.md`** — A9 protocol legs:
- The `/demo/my-projects` admin leg (line 363) changes from
  `{"ids":[],"unrestricted":true}` to `{"admin":true,"ids":[]}`.
- The platform-admin narrative (68–78) updated: admin is a host-composed check,
  not an engine bypass. The `platform` type now declares an `admin` **permission**.

### T5 — Docs sweep

Every place stating the old order gets the one-line truth (**"Check evaluates
the schema; policy short-circuits are host composition"**):
- `features/authorization/README.md` — lines 6, 45 (method table blurb), 132–135
  (platform-admin section → reframe as host recipe), 198 (Adversarial subtest
  name), 256, 306 (`result.Unrestricted` code snippet → remove).
- `NOTES.md` — lines 1672, 1715; add a dated ledger entry recording the cut
  (house discipline) + the D2 correction + the demo JSON contract change.
- Add BOTH recipes (platform-admin closure, self-access ID-equality) to the
  feature README as the canonical host pattern, with the fails-closed note.

---

## Verify (real behavior, not just green tests)

1. `go build ./... && go test ./...` across workspace modules.
2. storetest conformance green: memstore + pgx + turso (live where available).
3. Boot auth-cms; re-drive from its README:
   - **platform-admin leg**: bootstrap admin tuple → `/demo/members-only` passes
     for the admin via the NEW closure check (not engine magic); `/demo/my-projects`
     returns `{"admin":true,...}`.
   - **non-admin gated leg**: ungranted user → 403 on `/demo/members-only`;
     granted member → 200 (schema path unchanged).
   - **no-implicit-self leg**: confirm nothing depends on the removed self-access
     (auth-cms never used it — segovia never did either).

---

## Downstream coordination (NOT done here — ledger note)

segovia v2 consumes this via its `access.Checker` adapter
(`segovia/v2/internal/outbound/adapters/authz/`): on landing, segovia adds the
platform-admin closure check atop its `Checker.Check` and the platform `admin`
permission rule to `BuildSchema()`. Its `grantadmin` CLI + tuple are unchanged;
it never used checkSelf. Tracked as segovia flags #4–#7.
