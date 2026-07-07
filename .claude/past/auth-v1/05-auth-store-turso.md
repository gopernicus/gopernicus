# Phase 5 — `features/auth/stores/turso`

Status: DRAFT — pending ratification
Executor model: opus
Depends on: phase 1. Independent of phases 3–4.

## Goal

The auth feature's first real store-adapter module: SQL implementations of the
five v1 ports + canonical migrations under source name `"auth"` +
`ExportMigrations`, mirroring `features/cms/stores/turso` file-for-file in
conventions (read that module thoroughly first — it is the template).

## Work items

1. Module scaffold (`gopernicus/features/auth/stores/turso`; requires
   `gopernicus/{sdk, features/auth, integrations/datastores/turso}`;
   go.work + MODULES).
2. Migrations (`migrations/*.sql`, numbering from 0001): users (email UNIQUE),
   user_passwords (user_id PK/FK), sessions (token or token-hash lookup
   column + expiry — match what phase 1's session entity stores; read it),
   verification_codes, verification_tokens. Fixed-width TEXT timestamps per
   the ecosystem convention (NOTES.md's "single most subtle correctness
   detail" — read that entry; `2006-01-02T15:04:05.000000000Z07:00`).
3. Store implementations per port, using the turso connector's `DB`/`MapError`
   exactly as cms's stores do; UNIQUE violations must surface as
   `errs.ErrAlreadyExists`, absent rows as `errs.ErrNotFound`, and
   expired-at-read semantics per the port doc comments.
4. `Repositories(db) auth.Repositories` convenience constructor +
   `ExportMigrations(dst)` — mirror `cmsturso`'s (`features/cms/stores/turso/
   turso.go`).
5. Tests: unit tests for SQL construction/mapping where cms's stores have
   them; the build-tagged live leg (`-tags=integration`, skips loudly without
   `TURSO_*` env) runs `storetest.Run` (phase 1 W7) as its body — the suite
   replaces the bespoke register-shaped flow (ratified R1 edit 5) — plus any
   turso-specific extras (e.g. libsql error-string `MapError` coverage) the
   suite can't express.

## Acceptance

The store passes `storetest.Run` under the integration gating above; the
milestone-close live run is recorded as a dated NOTES.md artifact
(portability plan §4.3).

```sh
cd features/auth/stores/turso && go build ./... && go vet ./... && go test ./...
make check         # green, module included (guards: this module MAY import the
                   # feature + integrations — it is a store adapter, outside G2's
                   # feature-core scope; confirm the generalized guard excludes
                   # stores/ correctly)
```

If live Turso creds are available in the environment (check for a `.env` /
`TURSO_*` — do not ask), also run the integration test and report; if absent,
state plainly that the live path is unverified.

## Real-interaction check

Standing check (a). (A live Turso-backed auth *host* stays out of v1 scope;
the live `storetest` run above is this store's real verification.)

## Execution log

### 2026-07-02 — phase 5 executed (loop leg 10; implementer on opus) — LIVE-VERIFIED

Shipped `features/auth/stores/turso` (12th module): migrations 0001–0005
(users email-UNIQUE, user_passwords, sessions, verification_codes,
verification_tokens; fixed-width TEXT timestamps per the NOTES.md
convention; source Name "auth"); five stores using the turso connector's
DB/MapError exactly as cms's (ErrAlreadyExists/ErrNotFound/ErrExpired per
port docs; passwords Set = ON CONFLICT upsert); exported trio +
MigrationsFS/Dir mirroring cms turso's. go.work + MODULES + STORE_MODULES
+ test-stores updated.

Divergences (logged, sound): (1) session tokens stored PLAIN — the entity
carries the opaque sdk/id token as the lookup key; hashing would break the
port contract as written (a v2 hardening candidate, not a store decision);
(2) NO enforced FK on child tables — the plan said "user_id PK/FK" but the
conformance suite (the executable spec) exercises child ports with no
users row, and the connector never enables PRAGMA foreign_keys;
relationship documented by convention in migration comments.

Acceptance (firsthand): build/vet/test PASS (hermetic loud skip);
`make check` → "all checks passed" (12 modules, 4 guards — store module
correctly outside G2's scope). **LIVE conformance GREEN TWICE against the
AUTHORIZED playground Turso** (URL match verified against
libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io
before each run): implementer run 16/16 leaf subtests ~30s; independent
loop-leg re-run `-count=1` ok in 29.3s. Real-interaction (a): minimal
:8081 → 200/200; port free.

Unverified: nothing for this phase.
