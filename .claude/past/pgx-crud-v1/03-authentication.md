# Phase P3 — authentication: the pattern-setter feature

Status: **RATIFIED 2026-07-08 (jrazmi — Q1/Q2/Q3 at recommendations; see 00-overview.md)**
Executor model: opus
Depends on: P2

## Context

Authentication has the most paginated ports (service accounts, API keys,
security events, invitations ×2) and owns the repo's only strict HTTP
list-param edge (`internal/inbound/http/machine.go:190-197`, shared by
four JSON list endpoints). This phase sets the pattern every later feature
phase copies: order vocabulary in the domain rim → storetest extension +
memstore in one boundary → pgx rewrite to the full idiom set → turso
minimal migration → HTTP edge wiring. Its memstore twin
(`examples/auth-cms/internal/authmem`) runs hermetically inside
`make check`, so storetest and authmem move together.

## Goal

Every authentication paginated port supports order/prev/offset/count on
pgx (idiomatic pgx v5 throughout the store), provably matched by turso
and authmem via the extended storetest suite, and exposed through the
existing four JSON list endpoints.

## Definition of Done

- Order allow-lists + defaults declared in
  `features/authentication/domain/{serviceaccount,apikey,securityevent,invitation}`
  (minimum vocabulary: `created_at`, default DESC — additions only for
  already-indexed columns, each with a conformance ordering case).
- storetest gains the standard case family per paginated port (see
  below); authmem passes hermetically in `make check`; both dialect
  stores pass live.
- `features/authentication/stores/pgx` fully on the idiom set: NamedArgs
  everywhere, CollectRows/CollectOneRow over db-tagged row structs +
  toDomain, `pgxdb.List` for all five paginated ports, filter builders
  appending to NamedArgs; `oauth_states.go` DELETE…RETURNING consume and
  `invitations.go` UPDATE…RETURNING preserved; MapError semantics
  unchanged.
- turso store migrated to `turso.List` — semantics only, no idiom work.
- The four JSON list endpoints accept `order`/`offset`/`count` and return
  `total`; verified over live HTTP.
- Zero `pgxdb.ListPage`/`turso.ListPage` callers remain in this feature.

## The standard storetest case family (pinned here; P4/P5 copy it)

Per paginated port, named sub-tests:

- `Order` — explicit asc + desc on `created_at` (and any added field),
  paged traversal preserves order + completeness; tiebreak (existing
  collision cases) re-asserted under asc.
- `PrevPage` — page forward to page 2+: `HasPrev` true, `PreviousCursor`
  round-trips to exactly page 1's IDs; first page ⇒ `HasPrev` false;
  partial prior window ⇒ `HasPrev` true with empty `PreviousCursor`.
- `OffsetMode` — offset traversal yields the identical ID sequence as
  cursor traversal; `HasPrev` iff offset > 0; no cursors in offset pages.
- `WithCount` — `Total` equals the filtered row count in both modes and
  is nil when unrequested.
- `StaleCursorOrderChange` — a cursor minted under desc used with asc is
  treated as first page (no error, no skew).
- `CursorOffsetExclusive` — cursor+offset rejected with the invalid-input
  kind.

## Out of scope

- New list endpoints or ports; users has no List and gets none.
- turso idiom parity (follow-up milestone).
- Auth domain behavior changes of any kind.

## Schema / datastore impact

None (no migrations). Order vocabulary limited to indexed spine columns.

## Risks

1. storetest + authmem must land in one task boundary or `make check`
   breaks (authmem's hermetic conformance run).
2. The pgx rewrite touches session/oauth security code — preserve-verbatim
   idioms are named per task; live conformance is the acceptance.

## Tasks

### task-1: order vocabulary in the domain rim

- **depends_on:** []
- **model:** opus
- **files:** [features/authentication/domain/serviceaccount/order.go, features/authentication/domain/apikey/order.go, features/authentication/domain/securityevent/order.go, features/authentication/domain/invitation/order.go]
- **verify:** `cd features/authentication && go build ./... && go test ./... && go vet ./...` then `make guard`
- **description:** Declare each aggregate's exported
  `map[string]crud.OrderField` allow-list + default `crud.Order`
  (minimum: `created_at` DESC; match existing file/naming conventions in
  each package — if the package keeps vars atop an existing file rather
  than a new order.go, follow the repo convention). No entity changes,
  no db tags (ratified). FS1 intact: crud is sdk.

### task-2: storetest extension + authmem, one boundary

- **depends_on:** [task-1]
- **model:** opus
- **files:** [features/authentication/storetest/storetest.go, examples/auth-cms/internal/authmem/ports_v2.go, examples/auth-cms/internal/authmem/authmem.go]
- **verify:** `cd features/authentication && go test ./...` then `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...` then `make check` (both dialect stores skip loudly but must still compile — they keep passing because legacy ListPage semantics only fail the NEW cases, which is why this task must also gate: run the pgx/turso conformance hermetically to confirm skip-not-fail)
- **description:** Add the six-case family to every paginated port's
  sub-runner (ServiceAccounts, APIKeys, SecurityEvents,
  Invitations/ByResource, Invitations/BySubject), reusing the existing
  `pageAll*` traversal helpers. Extend authmem's generic `page[T]`
  helper (ports_v2.go:361) to honor Order (sort by allow-listed key +
  id tiebreak), reverse probes, offset mode, and counts, so authmem
  passes hermetically. NOTE: after this task the dialect stores FAIL the
  new cases when run live — that is expected mid-phase (loud, not
  silent); tasks 3–5 close it. Do not run `make test-stores` between
  task-2 and task-5.

### task-3: pgx paginated ports onto pgxdb.List + filter builders

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/authentication/stores/pgx/service_accounts.go, features/authentication/stores/pgx/api_keys.go, features/authentication/stores/pgx/security_events.go, features/authentication/stores/pgx/invitations.go, features/authentication/stores/pgx/helpers.go]
- **verify:** `cd features/authentication/stores/pgx && go build ./... && go test ./... && go vet ./...` (hermetic skip); live leg (executor-local): `docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17` then `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./...` — full extended suite green; container removed
- **description:** Rewrite the five paginated ports onto
  `pgxdb.List[T]`: db-tagged row structs + toDomain per store file,
  NamedArgs filter builders for the WHERE fragments
  (resource/identifier/service-account filters), `crud.MapPage` for the
  page conversion, order allow-lists from task-1 threaded through
  `ListQuery`. `invitations.go` UPDATE…RETURNING moves to
  CollectOneRow+RowToStructByName in passing (it's in these files).
  Preserve MapError call sites and port error semantics exactly.

### task-4: pgx idiom sweep — remaining store files

- **depends_on:** [task-3]
- **model:** opus
- **files:** [features/authentication/stores/pgx/users.go, features/authentication/stores/pgx/sessions.go, features/authentication/stores/pgx/passwords.go, features/authentication/stores/pgx/oauth_accounts.go, features/authentication/stores/pgx/oauth_states.go, features/authentication/stores/pgx/verification.go, features/authentication/stores/pgx/postgres.go]
- **verify:** hermetic module verify as task-3, then the same live pgx leg — full suite green; then `make check`
- **description:** Convert every remaining query to NamedArgs +
  CollectRows/CollectOneRow over row structs (userspgx/generated.go is
  the shape oracle: NamedArgs at :59/:121/:144, CollectOneRow at :131).
  **Preserve verbatim:** `oauth_states.go:44` DELETE…RETURNING consume
  semantics, session/verification single-use flows, `ExecAffecting`
  zero-rows→ErrNotFound mappings, all InTx boundaries. No behavior
  change is the acceptance — the pre-existing storetest cases are the
  regression net.

### task-5: turso minimal migration to turso.List

- **depends_on:** [task-2]
- **model:** opus
- **files:** [features/authentication/stores/turso/service_accounts.go, features/authentication/stores/turso/api_keys.go, features/authentication/stores/turso/security_events.go, features/authentication/stores/turso/invitations.go, features/authentication/stores/turso/helpers.go]
- **verify:** `cd features/authentication/stores/turso && go build ./... && go test ./... && go vet ./... && go vet -tags=integration ./...` then `make check`; live leg (executor-local, playground discipline — URL must be `libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`): `go test -tags=integration ./...` — extended suite green, recorded
- **description:** Migrate the five paginated call sites from
  `turso.ListPage` to `turso.List` passing order allow-lists and the
  full ListRequest; keep hand-scan callbacks and every other turso idiom
  untouched (ratified turso-minimal scope — semantics only, no
  NamedArgs-equivalent work).

### task-6: HTTP edge — order/offset/count params + total

- **depends_on:** [task-3, task-5]
- **model:** opus
- **files:** [features/authentication/internal/inbound/http/machine.go, features/authentication/internal/inbound/http/invitation.go, features/authentication/internal/inbound/http/responses.go]
- **verify:** `cd features/authentication && go build ./... && go test ./... && go vet ./...` then `make check` and `make guard` (G6/FS9: responses stay on sdk/web); then the real-interaction protocol below
- **description:** Extend the shared `parseListRequest` helper to the new
  five-param `crud.ParseListRequest` + per-aggregate `crud.ParseOrder`
  (each handler passes its aggregate's allow-list + default; bad params
  → 400 via web.ErrBadRequest, the existing pattern). Thread `total`
  through the page response envelope (Page's json tags already carry
  has_prev/previous_cursor/total — confirm the envelope doesn't
  re-marshal them away). Adjust the exact response-helper file names to
  what exists (responses live near newPageResponse in machine.go today).
  Do not add endpoints.

## Acceptance

```sh
cd features/authentication && go build ./... && go vet ./... && go test ./...
cd features/authentication/stores/pgx && go test ./...    # loud skip hermetically
cd examples/auth-cms && go test ./...                     # authmem conformance green
make check && make guard
grep -rn "ListPage" features/authentication/stores/   # → empty
```

Live: the task-3/4 pgx leg and task-5 turso leg recorded (dated) for the
milestone NOTES artifact.

## Real-interaction check

Boot `examples/auth-cms`; run the standing cookie-jar flow (register →
verify code from the console-mailer log → login). Then, authenticated:

- `GET /auth/service-accounts?limit=2&order=created_at:asc&count=true` →
  200; JSON has `total`, items ascending.
- Take `next_cursor`, request page 2 → `has_prev: true`; request
  `previous_cursor` → page 1's IDs exactly.
- `GET /auth/service-accounts?offset=2&limit=2` → 200, rows match cursor
  page 2; `?cursor=X&offset=2` → 400.
- Repeat one leg on `/auth/service-accounts/{id}/keys` and
  `/auth/invitations/mine`. Report exact status codes and field values.

## Execution log

### 2026-07-08 — phase 3 executed (implementer on opus); PHASE COMPLETE

All six tasks landed in order. Order vocabulary (`OrderFields` +
`DefaultOrder`, created_at DESC minimum) in the four domain rims; the
six-case family wired into all five paginated ports via a generic
`runPagedFamily` in storetest, with authmem's `page[T]` extended
(order + id tiebreak, offset, reverse probe, counts) in the same
boundary; pgx stores fully on the idiom set (db-tagged row structs +
toDomain + `crud.MapPage`, `pgxdb.List` on all five paginated ports,
NamedArgs everywhere incl. writes, `queryOne[T]` helper on
CollectExactlyOneRow, UPDATE…RETURNING through the row struct; pgx/v5
promoted to a direct require); turso migrated semantics-only to
`turso.List` (hand-scan kept); HTTP edge parses limit/cursor/offset/
count + per-aggregate order (bad param → 400 web.ErrBadRequest), page
envelope carries has_prev/previous_cursor/total. Preserve-verbatim list
honored (oauth_states consume, single-use flows, ExecAffecting
zero-rows mappings, InTx boundaries).

**Flagged executor decisions, accepted:** (1) `StaleCursorOrderChange`
tests a FIELD change (updated_at-minted cursor → first page), not the
plan's literal desc→asc phrasing — the codec keys staleness on the
order field, and a same-field direction flip is deliberately NOT stale;
the literal test would need a second indexed column (out of scope,
logged as the known trade). (2) `storetest/reference_test.go` rebuilt
(`pageDESC` → full-matrix `pageMem`) though not in task-2's file list —
its own verify forced it. (3) task-6's `responses.go` doesn't exist;
envelope/parser live in machine.go, edited there per the plan's
adjust-to-actual-names note.

**Live legs (dated 2026-07-08, for the milestone NOTES artifact):** pgx
— docker postgres:17, full extended suite **101 subtests PASS, 0 fail**
(30 new family cases = 6 × 5 ports), run at task-3 AND task-4;
container removed, port free. turso — playground URL byte-verified
pre-run (hard gate), `-tags=integration` suite **ok, 371.3s, 0 fail**;
no tokens in logs. Real-interaction protocol (executor, examples/
auth-cms :8082): register 201 → verify 200 → login 200 (cookie jar);
order+count leg `total=5` ascending with next_cursor; prev round-trip
exact (partial-window empty previous_cursor = first page; page-3
previous_cursor round-trips to page 2); offset leg matches cursor page
2 desc, no cursors in offset pages; `cursor+offset` → **400**; keys +
invitations/mine legs green (`total=3`, ascending, cursors present).
Main session re-verified: fresh module + both-store builds/tests,
`make check` (30 modules) + `make guard` green, `grep ListPage
features/authentication/stores/` → empty, auth-cms boot :8082 GET / →
200 with unauthenticated list → 401 (middleware precedes param parse),
killed, port free. Next: P4 (`04-cms.md`).
