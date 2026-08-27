# The list contract reaches the request: `crud.ParseListQuery`, sdk-wrapped rejections, `crud.Items`/`MapItems`, `web.NoStore`, and UTC-located `timestamptz` on every pgxdb connection

**Modules:** `sdk` (`foundation/crud`, `foundation/web`); `integrations/datastores/pgxdb`; `pockets/authentication` core (`internal/inbound/authentication` only). `examples/auth-cms` is the proof host. No store modules, no schema, no migrations.
**Issue:** gopernicus/gopernicus#11 (related: #9, the open pgxdb helpers issue — §Open questions Q1).
**Status: RELEASED 2026-08-27 — PR #12 (precondition: G12 tests-included) squash-merged @ `8363c29`, PR #13 squash-merged to `main` @ `1d88986`; `sdk/v0.6.0` + `integrations/datastores/pgxdb/v0.6.0` tagged at `1d88986`, `pockets/authentication/v0.8.0` tagged at `7960db6` (the pin-move commit); cold-resolved `GOWORK=off GOFLAGS=-mod=mod`; issues #11 and #9 closed. See §As executed at the bottom.** Ratified same day (owner: "go"). Rulings: Q1 IN (T9a–c ride in `pgxdb/v0.6.0`); Q2/Q4/Q6 confirmed; Q3 YES (sentence on the wire); Q5 sdk minor; Q7 YES; Q8 YES — guard takes **G21** (PR-B's guard takes G22); Q9 YES (D6b in).
**Traced against:** `main` @ `ca3d1db` (2026-08-27; third tier `pockets/`, composer `sdk/pocket`, host contract `examples/README.md` H0–H10). Latest tags: `sdk/v0.5.0`, `integrations/datastores/pgxdb/v0.5.0` (pins `sdk v0.4.0`), `pockets/authentication/v0.7.0` (pins `sdk v0.5.0`), `pockets/authentication/stores/pgx/v0.5.0`, `pockets/authentication/stores/turso/v0.4.0` (both pin `authentication v0.7.0`, `sdk v0.5.0`).
**Target tags (one train):** `sdk/v0.6.0` (minor), `integrations/datastores/pgxdb/v0.6.0` (minor — connect-time behaviour change), `pockets/authentication/v0.8.0` (minor — D7). Stores NOT retagged.
**Reference implementation:** `/Users/jrazmi/code/gps/three-sixty/gps-360-go/internal/inbound/wire/wire.go` @ `3cfa2c1` — `ListRequest`, `Items`/`RespondList`, `NoStore`, `Time`/`TimePtr` are lifted in shape; `Vocabulary`/`VocabularyPtr`, `Date`, `JSON` stay in the host (§Adoption).
**Consulted (2026-08-27):** lead-backend-engineer (ship-with-edits), architecture-steward (aligned-with-edits) — §Consultation; every accepted edit is folded in below and attributed there.

## Problem (as filed, confirmed against the code)

- **The parser stops one step short, and its rejections are unclassified.** `sdk/foundation/crud/pagination.go:136` `ParseListRequest(ListParams)` takes already-extracted strings; every caller re-derives the same five `q.Get(...)` lines. Its seven rejections are plain `fmt.Errorf`, none wrapped in `sdk.ErrInvalidInput` (`pagination.go:154,159,163,167,186,189,198` — three of them `%w`-wrap the `strconv` cause, none the sentinel); `ParseOrder`'s two are the same (`order.go:52,58`). `web.ErrFromDomain` (`sdk/foundation/web/errors.go:252`) classifies only through `errors.Is` on the kernel sentinels, so an unwrapped parse error falls to `ErrInternal` — `?limit=zero` is a 500 unless the caller special-cases it. For the record: once wrapped, `ErrFromDomain` answers the **generic** `400 "invalid input"` by design; the sentence reaches the wire through `web.ErrValidation(err)` (`errors.go:98-116`, "otherwise the error message is used directly"), the path `DecodeJSON` failures already take. The issue's "answers 400 with the message" is two things — the status (`ErrFromDomain`) and the message (`ErrValidation`) — D2.
- **Every caller special-cases it.** The authentication pocket's `parseListRequest` (`pockets/authentication/internal/inbound/authentication/machine.go:258-281`) builds `ListParams` by hand — with `DefaultStrategy: h.listStrategy`, the host-configured field the issue's snippet omits — then answers a fixed `web.ErrBadRequest("invalid page parameters")` / `"invalid order parameter"`, discarding the sentence. Five list routes go through it (`useradmin.go:120`, `machine.go:191,229`, `invitation.go:225,248`). It is the ONLY `crud.ParseListRequest` caller in `pockets/` and `examples/` (grep, non-test). gps-360-go's `wire.ListRequest` (`wire.go:73-85`) is the same glue with the same special case, called from **10** sites.
- **The issue's signature is illegal.** `web.ListRequest(r *http.Request, limits crud.Limits)` needs `web → crud`. Both are `sdk/foundation/*`; ARCHITECTURE.md §"The sdk layering law" says foundation "may import the ROOT only — FLAT, no foundation→foundation edges", and Makefile G12b (`Makefile:273-278`) fails any such edge in production or test code. Evidence that the law holds today: every `github.com/gopernicus/gopernicus/sdk/...` import in `sdk/foundation/web/*.go`, including `_test.go`, is either the root package or `web` itself for external-package tests. And a second, weightier reason (lead-backend-engineer): `integrations/datastores/pgxdb/list.go` and the turso connector import `crud`, so whatever `crud` imports, every store adapter carries. The parser lives in `crud` over `url.Values` — D1.
- **No name for the bounded page, and empty pages can say `null`.** `crud.Page[T]` (`crud.go:277-284`) has every field but `Items` omitempty, so a parent-bounded, uncursored list already serializes as `{"items":[…]}`; `MapPage`/`MapPageErr` bridge pages, nothing constructs one from a bare slice. gps-360-go carries `wire.Items` (1 direct site) + `wire.RespondList` (14 sites) for it. Adjacent fact (lead-backend-engineer, confirmed): `Items` is NOT omitempty, `TrimPage` (`pagination.go:15`) stores the records slice as given, and `MapPage`/`MapPageErr` keep nil as nil (`:62,:85`); pgx's `CollectRows` starts from `[]T{}` (`pgx/v5@v5.8.0/rows.go:459`) so pgx pages never carry nil, but the turso connector's `List` builds `var items []T` (`integrations/datastores/turso/list.go:280`) — an empty turso page marshals `"items":null` today. D4 closes both.
- **No-store is a map literal in each host.** `web.DefaultHeadersMiddleware` (`middleware.go:195`) is the primitive; gps-360-go's `wire.NoStore` wraps it and is wired on the `/api/v1` group (`cmd/server/main.go:355`) plus two test routers. Nothing named `RequirePrincipal` exists in sdk (grep: zero hits under `sdk/`; it is gps-360-go's `authenticationSvc.RequirePrincipal`, a pocket method) — the godoc must not invent it (D5).
- **Scanned `timestamptz` values come back in `time.Local`, and the cause is NOT the session zone.** `pgxdb/timestamps.go` normalizes what it writes and what `FromNullTime*` read to UTC, but a plain `time.Time` scan never passes through those helpers. In pgx v5.8.0 (the pinned version) the default extended protocol decodes `timestamptz` in **binary** — microseconds since epoch, no zone — via `time.Unix(...)`, which yields a `time.Local`-located value (`pgtype/timestamptz.go:272-278`); the session `TimeZone` influences only the **text** decoder's input (`:305-331`). So the owner's `2026-08-27T11:58:03-07:00` is the Mac's local zone, and `SET TIME ZONE 'UTC'` alone would not have changed it. pgx exposes `pgtype.TimestamptzCodec{ScanLocation *time.Location}` ("does not change the instant", `timestamptz.go:133-137`), and BOTH decoders apply it (`:276-278`, `:326-328`). That is the seam that fixes the symptom; the session zone is a separate, server-side matter — D6. gps-360-go carries `wire.Time`/`wire.TimePtr` for this: **41 + 14 = 55** call sites (the issue's "~60"), plus `wire.Date` 15 and `wire.Vocabulary` 18 that stay.
- **turso has no equivalent concern.** SQLite has no session zone; the turso connector stores fixed-width UTC TEXT (`FormatTime`, `timestamps.go:16`) and `ParseTime` normalizes to UTC on read (`timestamps.go:22-24`). Nothing to do there.

## Proposal in one paragraph

Put the transport-edge parser where the law allows it: **`crud.ParseListQuery(q url.Values, opts crud.ListQueryOptions) (crud.ListRequest, error)`** reads the canonical keys `limit`/`cursor`/`offset`/`count`/`q` (`order` stays with `crud.ParseOrder`, one line beside it — the cms admin list needs a fall-back posture a combined parser cannot express), and every rejection in `ParseListRequest` and `ParseOrder` wraps `sdk.ErrInvalidInput` with its sentence preserved, so `web.ErrFromDomain` answers 400 and `web.ErrValidation` carries the sentence — `web` is untouched. Add **`crud.Items[T]`** and **`crud.MapItems[T,U]`**, the bounded-page constructors, and make `TrimPage`/`MapPage`/`MapPageErr` normalize nil `Items` to `[]`; direct `Page[T]{}` construction remains caller-owned. Add **`web.NoStore() web.Middleware`**, a named preset of `DefaultHeadersMiddleware` (`Cache-Control: no-store`, nothing else). In **`pgxdb.Open`**, register a `TimestamptzCodec{ScanLocation: time.UTC}` (and a matching `timestamptz[]` array codec) on every connection so a scanned `timestamptz` is a UTC-located `time.Time` and Go's default JSON marshalling emits `Z`; and — owner-ruled, recommended — default the session `timezone` to `UTC` in the startup packet when the DSN/`PGTZ`/`options=` name none, so zone-dependent SQL evaluates the same on a laptop and in a container. The authentication pocket's `parseListRequest` collapses to the two framework calls. Three modules tag; stores do not.

## Decisions

### D1 — The parser lives in `crud`, takes `url.Values`, parses the five page keys, and options are a struct

```go
// sdk/foundation/crud/listquery.go
const (
	QueryKeyLimit  = "limit"
	QueryKeyCursor = "cursor"
	QueryKeyOffset = "offset"
	QueryKeyCount  = "count"
	QueryKeySearch = "q"
	QueryKeyOrder  = "order" // read by ParseOrder callers, never by ParseListQuery
)

// ListQueryOptions is the resource-side policy ParseListQuery resolves an
// untrusted query against. The zero value is sdk's defaults (DefaultLimit /
// MaxLimit, StrategyCursor).
type ListQueryOptions struct {
	Limits          Limits
	DefaultStrategy Strategy
}

// ParseListQuery is ParseListRequest over the canonical query keys
// (limit/cursor/offset/count/q). Every rejection wraps sdk.ErrInvalidInput —
// web.ErrFromDomain answers 400; web.ErrValidation carries the sentence. Order
// is a separate concern with a per-aggregate allow-list: parse it beside this
// call with ParseOrder (reject) or fall back to the default order (SSR).
func ParseListQuery(q url.Values, opts ListQueryOptions) (ListRequest, error)
```

- **Why `crud`, not `web`:** the layering law and G12b (§Problem). `net/url` is stdlib, so G1 (`guard-sdk-stdlib`) stays green and `crud` stays "pure mechanism/vocabulary" — a parser over a string map is vocabulary, and `crud.go:85-88` already declares crud the owner of these keys. `url.Values` rather than `*http.Request` keeps `crud` transport-agnostic (forms, CLIs, tests) and keeps `net/http` out of every store adapter's import graph. A capabilities package (no behavioral port — fails the admission test) and a G12 exception (the law is seven weeks old and the whole point of it) were rejected; both leads concur (§Consultation).
- **`order` is NOT folded in** (lead-backend-engineer R4, accepted): `pockets/cms/internal/inbound/cms/entries.go:118-130` deliberately falls back to `content.DefaultOrder` on a bad `?order` (its "Per Q3" SSR ruling) — a combined parser has only skip/reject modes and cannot express fall-back, and the allow-list is per-aggregate by design (`crud.go:87-92`). `ParseOrder` stays the one order parser; its rejections are still wrapped (D2) so JSON edges that reject get a 400 too. The pocket's helper becomes two calls, not one (D3) — still the de-duplication the issue asks for.
- **Struct options, not positional** (`optional-params-struct-input`, ruled 2026-08-14): both fields are optional; a future field (a legacy `s` search alias with a removal milestone, say) lands without signature churn. `ListParams`/`ParseListRequest` stay exactly as they are — `ParseListQuery` builds a `ListParams` and delegates, so there is one parser, not two.
- The key constants are exported so a host's OpenAPI or docs can name them without literals. `web/openapi.go:161-183` documents only `limit`/`cursor`/`order` — pre-existing drift, left as found (§Non-goals).
- **Optional guard (Q8):** a one-line Makefile guard, `guard-crud-no-nethttp` (`! grep -l '"net/http"' sdk/foundation/crud/*.go` over production files), pins "crud may import `net/url`, never `net/http`" — G12b's gopernicus-only grep cannot see it (lead-backend-engineer R8). Recommended: it is the repo's one-grep-per-boundary habit and the reason is a dependency-weight fact, not taste.

### D2 — Every rejection wraps `sdk.ErrInvalidInput`; the sentence is preserved as the prefix

Wrap each of the nine rejections in the repo's existing sentence-first shape (`crud.go:221` `fmt.Errorf("offset must not be negative: %w", sdk.ErrInvalidInput)`):

| site | today | after |
|---|---|---|
| `pagination.go:154` | `"page limit conversion: %w"` (strconv) | `"page limit conversion: %w: %w"` (strconv cause, then `sdk.ErrInvalidInput`) |
| `:159` | `"rows value too small, must be larger than 0"` | `… + ": %w"` |
| `:163` | `"rows value too large, must be at most %d"` | `… + ": %w"` |
| `:167` | `"cursor and offset are mutually exclusive"` | `… + ": %w"` |
| `:186` | `"page offset conversion: %w"` (strconv) | `"…: %w: %w"` |
| `:189` | `"offset value too small, must not be negative"` | `… + ": %w"` |
| `:198` | `"page count conversion: %w"` (strconv) | `"…: %w: %w"` |
| `order.go:52` | `"unknown direction: %s"` | `… + ": %w"` |
| `order.go:58` | `"unknown order field: %s"` | `… + ": %w"` |

- Every existing sentence survives verbatim as the prefix; `pagination_test.go:116-131` asserts with `strings.Contains`, so the table stays green, and a new column asserts `errors.Is(err, sdk.ErrInvalidInput)` per row (precedent `crud_test.go:98`). The strconv cause stays in the chain (`errors.As(*strconv.NumError)` keeps working; multi-`%w` needs Go ≥1.20, the module is `go 1.26.1`).
- **Cursor-decode errors are NOT swept in** (architecture-steward): a stale/bad cursor token is a first page by rule (`crud.go:80`, `cursor.go`), a store-edge concern, never a 400.
- **Layered wire proof — no `foundation` sibling import, even in tests:** `crud` tests prove every parser rejection wraps the root `sdk.ErrInvalidInput` sentinel and preserves its sentence. Existing `web/errors_test.go` proves `ErrFromDomain` and `ErrValidation` from a synthetic error such as `fmt.Errorf("rows value too large: %w", sdk.ErrInvalidInput)`; it imports only the root `sdk` package, which is legal for `foundation/web`. The authentication handler test (T4), where the pocket may compose both foundations, proves the real parser error reaches the wire as 400. `ErrFromDomain` is NOT changed to surface messages — its generic mapping is a deliberate posture (`SafeDomainError` godoc, `errors.go:180-200`); these sentences are framework-authored, so `ErrValidation` is the legal path for them, exactly as for `DecodeJSON`.

### D3 — The authentication pocket switches to the two framework calls and puts the sentence on the wire

`machine.go:258-281` becomes:

```go
func (h *handlers) parseListRequest(w http.ResponseWriter, r *http.Request, orderFields map[string]crud.OrderField, defaultOrder crud.Order) (crud.ListRequest, bool) {
	q := r.URL.Query()
	req, err := crud.ParseListQuery(q, crud.ListQueryOptions{DefaultStrategy: h.listStrategy}) // Config.ListStrategy; `q` only, no legacy `s`
	if err == nil {
		req.Order, err = crud.ParseOrder(orderFields, q.Get(crud.QueryKeyOrder), defaultOrder)
	}
	if err != nil {
		web.RespondJSONError(w, web.ErrValidation(err))
		return crud.ListRequest{}, false
	}
	return req, true
}
```

- `Limits` stays zero (today's behaviour — the pocket declares no per-aggregate limits). The existing `q`-only comment (no legacy `s` fallback) moves onto the call.
- **Wire-body change:** the five list routes answer the parser's sentence instead of the fixed `"invalid page parameters"` / `"invalid order parameter"` (status and `code` unchanged: 400 `bad_request`). No test in the pocket pins those strings (grep). This is the issue's point; keeping the fixed strings would leave the pocket as the one caller that discards the sentence. Both leads note the pocket already answered 400 here — the change is de-duplication plus a message change, and the RELEASING note says so. **Owner call (Q3)** — the alternative (keep the fixed strings) makes the pocket change invisible on the wire and lets the core tag as a pin-move-only patch if Q5 is also patch.
- `pockets/authentication/README.md:243` (the `/auth/admin/users` row) gains the 400-body sentence.

### D4 — `crud.Items[T]` AND `crud.MapItems[T,U]`; SDK constructors and mapping bridges normalize nil `Items`

```go
// Items is the bounded-page constructor: a parent-scoped, uncursored list is a
// Page holding only Items — the omitempty tags make {"items":[…]} the wire
// shape. A nil slice becomes an empty one so the wire says [] and never null.
func Items[T any](items []T) Page[T]

// MapItems is MapPage(Items(items), fn) — the row/domain→DTO bridge for the
// bounded case. Defined that way so nil-normalization has one site.
func MapItems[T, U any](items []T, fn func(T) U) Page[U]
```

- Both, because both call shapes are real: `Items` for an already-DTO slice, `MapItems` for the domain→DTO edge (gps-360-go's `RespondList` is `MapItems` + `RespondJSON` + the error branch). `MapItems` is DEFINED as `MapPage(Items(items), fn)` (lead-backend-engineer), so there is exactly one normalization site.
- **The rule extends to the paginated constructors** (lead-backend-engineer R3, accepted — the turso `null` case in §Problem is real): `TrimPage` normalizes a nil `records` to `[]T{}`, and `MapPage`/`MapPageErr` drop their `if p.Items != nil` guards and always allocate `make([]U, len(p.Items))`. One-line changes each; every `Page` produced by these SDK constructors or bridges marshals `"items":[]` when empty. No `MarshalJSON` on `Page` — a generic-type marshaller is a magic seam for a rule three constructors can state plainly. A directly constructed `Page[T]{}` still marshals `null`, so this plan makes no global `Page` wire-shape claim; the godoc on `Page.Items` says "use Items/TrimPage". **Owner call (Q7)** — this is a wire change for turso-backed hosts (`null` → `[]` on empty pages), bug-fix class, flagged in the sdk upgrade note.
- Wire-shape proof: tests marshal `Items[string](nil)` → exactly `{"items":[]}`, `MapItems([]int{1,2}, strconv.Itoa)` → `{"items":["1","2"]}`, and `TrimPage[string](nil, 10, enc)` → `{"items":[]}` — no `has_more`, no `next_cursor`, no `total`.

### D5 — `web.NoStore()`: one header, a named preset, godoc that claims no guarantee and names no host symbol

```go
// NoStore is a preset of DefaultHeadersMiddleware that writes
// Cache-Control: no-store before the handler runs (a handler may still override
// on its own writer). Mount it on route groups whose responses are derived from
// a per-request grant — an authenticated API surface, where every answer must
// reflect a revocation on the very next request and nothing may be retained by
// a browser or a shared cache:
//
//	v1 := router.Group("/api/v1", requirePrincipal, web.NoStore())
//
// It is a header policy the host applies, not an identity gate and not a
// guarantee: whatever gate the host mounts beside it is what makes the group
// authenticated. Only Cache-Control is written — Pragma and Expires are HTTP/1.0
// relics no-store supersedes, and this is the exact header the authentication
// pocket's own no-store surfaces write. (The SPA index served by web's static
// handler says "no-cache, no-store, must-revalidate" instead: that is the
// index-document posture for caches that revalidate; API answers are simply
// never stored.)
func NoStore() Middleware {
	return DefaultHeadersMiddleware(map[string]string{"Cache-Control": "no-store"})
}
```

- **Tier:** `sdk/foundation/web` — "pure HTTP mechanism, no capability port behind it", the same row as `DefaultHeadersMiddleware` in ARCHITECTURE.md's middleware table (`:150`); that row gains `NoStore`. The steward confirms the tier and that ARCHITECTURE.md:225 ("no new generic header middleware rides this posture") is scoped to the cross-origin-mutation posture, not a cache preset — which is why the godoc presents it as a preset, not a security posture.
- **Header set:** `Cache-Control: no-store` only. The authentication pocket's own surfaces (`policy.go:21`, `me.go:15`, `stepup.go:15`, `useradmin.go:31`) write exactly this one header and their tests pin it; `Pragma` is a request-side relic and `Expires` adds nothing under HTTP/1.1. The `static.go:148` spelling is reconciled in the godoc, not changed.
- **Not a guarantee** (lead-backend-engineer R5, accepted): an opt-in middleware delivers the policy only when mounted; the wording says so. Having the authentication pocket apply it to its own protected surfaces automatically is an authentication change, out of scope (§Non-goals).
- A function, not a `var` (the `CORSMiddleware(...)` shape; leaves room for options without a signature break). Not applied to the pocket's routes in this train.

### D6 — pgxdb: (a) `ScanLocation: time.UTC` on every connection, unconditional; (b) session `timezone=UTC` by default, owner-ruled; no new `Config` field

In `Open` (`postgres.go:176-207`), between `ParseConfig` and `NewWithConfig`, extracted into `func (cfg Config) poolConfig(dsn string) (*pgxpool.Config, error)` so it is hermetically testable.

**D6a — the scan location (ships regardless of Q9).** `poolConfig.AfterConnect` builds `tz := &pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC}}`, registers it on `conn.TypeMap()`, and registers `_timestamptz` as `&pgtype.Type{Name: "_timestamptz", OID: pgtype.TimestamptzArrayOID, Codec: &pgtype.ArrayCodec{ElementType: tz}}` — a NEW array type over the NEW element, because the default array/range codecs captured a pointer to the default element type at init (`pgtype_default.go:119,127,169,243`) and re-registering the scalar alone leaves `timestamptz[]` scanning in `time.Local` (lead-backend-engineer R1, confirmed). `tstzrange`/`tstzmultirange` are documented out of scope with that citation — no store in-repo scans them (`pockets/events/stores/pgx/outbox.go:170` only encodes a `[]time.Time`, which `ScanLocation` does not touch). `timestamp` (without zone) already decodes to UTC (`pgtype/timestamp.go:214,235`) — untouched. Both pgx decoders honour `ScanLocation` (§Problem), so this fixes the symptom under the extended AND simple protocols — behind a statement-pooling proxy too.

- **`AfterConnect` ownership** (lead-backend-engineer R6): `Config`'s doc comment records that the connector owns `pgxpool.Config.AfterConnect` for its codecs, and that a future `Config.AfterConnect` seam (none today) will CHAIN after the connector's registration, never replace it — decided now, in the comment.

**D6b — the session zone (recommended ON; Q9).** `poolConfig.ConnConfig.RuntimeParams["timezone"] = "UTC"` **unless** the host already named one: a `timezone` key in `RuntimeParams` (pgconn folds every non-connection DSN key there, `pgconn/config.go:351-356`, and maps `PGTZ` onto it, `:468`) or an `options` value containing `timezone=` case-insensitively (`-c timezone=…` / `-c TimeZone=…`, also `PGOPTIONS`). An explicit host choice is respected; absence gets UTC.

- **Why RuntimeParams, not `AfterConnect: SET TIME ZONE 'UTC'`:** a startup parameter costs no extra round-trip; PgBouncer tracks `timezone` in its default startup-parameter set, so it survives server-connection reuse in transaction mode, where a `SET` is scoped to whatever connection the pooler hands you next. Idempotent under pgx's own reconnects. **Why not DSN string mutation:** pgconn already parses both URL and `key=value` forms into the map; appending to the string means re-quoting what pgx has parsed.
- **What it changes, honestly** (lead-backend-engineer R2): nothing about scans (D6a covers those). It changes server-side, zone-dependent SQL for hosts that never set a zone: `now()::text`, `to_char(tstz, …)`, `date_trunc('day', tstz)`, `tstz::date`, `EXTRACT(hour FROM tstz)`, `timestamptz → timestamp` casts, and — the sharp one — a bare `'2026-01-01 00:00'` literal bound to a `timestamptz`, which is interpreted in the session zone. Bound `time.Time` arguments are unaffected (pgx binds an instant).
- **Why still recommended:** it is what the issue asks for by name; it makes those expressions evaluate identically on a developer's laptop and in a `TZ`-less container (today they differ silently — that IS the bug class); and the blast radius is measured: gps-360-go has **zero** `date_trunc`/`::date`/`to_char`/`AT TIME ZONE` hits in Go or SQL (grep @ `3cfa2c1`); coordination-hub is the owner's to check before repinning. A host that wants server-local bucketing says `AT TIME ZONE '…'` or pins `timezone=` in its DSN. If ruled OFF, D6b is dropped entirely (no `Config` toggle for it).

**No `Config.TimeZone` / `Config.ScanLocation` field** (Q4). The DSN is the session-zone escape hatch; the scan location is always UTC because a Go `time.Time`'s location is presentation, never data — a host with a local-zone need calls `.In(loc)` at the edge.

**Behaviour change (flagged in RELEASING):** (a) every scanned `timestamptz` (scalar or array) is UTC-located — `Equal`/`Before`/`Sub` unchanged, `String()`/`Format` output and JSON change from the local offset to `Z`; hosts already on `timestamps.go`'s `From*` helpers see nothing new. (b) D6b's list above, if ruled in.

**Proof.** Hermetic (`postgres_test.go`, on `poolConfig`): clear `PGTZ`, `PGOPTIONS`, `PGSERVICE`, and `PGSERVICEFILE` with `t.Setenv` before default-behaviour cases so ambient libpq configuration cannot change the result; `AfterConnect` is non-nil for any DSN; with D6b, `RuntimeParams["timezone"]=="UTC"` for a bare DSN, and explicit DSN, `PGTZ`, and `PGOPTIONS` choices are left alone. Live (`POSTGRES_TEST_DSN`, skips loudly like `live_test.go:27`), `TestLive_ScanUTC`: `SELECT now(), '2026-01-01T00:00:00+05:00'::timestamptz, ARRAY[now()]::timestamptz[]` scanned into `time.Time`/`[]time.Time` — every `Location() == time.UTC`, the literal equals `2025-12-31T19:00:00Z`, `json.Marshal` ends in `Z`; the same through `QueryOne[T]` (struct scan) and through `pgx.QueryExecModeSimpleProtocol` (text decoder). With D6b: `SHOW TimeZone` = `UTC`; for the override case, parse the DSN with `net/url`, set its `timezone=Europe/Oslo` query parameter, and open that DSN (never append a second `?` to a DSN that already has `sslmode`) — it reports `Europe/Oslo` while scans stay UTC-located.

### D7 — Tags and pins

| module | bump | why |
|---|---|---|
| `sdk` v0.5.0 → **v0.6.0** | minor | additive symbols (`ParseListQuery`, `ListQueryOptions`, `QueryKey*`, `Items`, `MapItems`, `NoStore`) plus two observable changes: `ParseListRequest`/`ParseOrder` errors now satisfy `errors.Is(_, sdk.ErrInvalidInput)` (a host routing them through `ErrFromDomain` answers 400 where it answered 500), and SDK constructor/bridge paths normalize nil page items to `[]` (Q7). The owner's additive-as-patch precedent (`sdk/v0.3.1`, `v0.4.1`) could apply; recommending minor. **Owner call (Q5).** |
| `integrations/datastores/pgxdb` v0.5.0 → **v0.6.0** | minor | connect-time behaviour change (D6). Pin stays `sdk v0.4.0` — no new sdk symbol is used (also true if #9 rides along: `sdk.ErrInvalidInput` exists at v0.4.0). |
| `pockets/authentication` v0.7.0 → **v0.8.0** | minor | pin move to a sibling **minor** (`sdk v0.6.0`; RELEASING's "a pin move to a sibling patch does not floor a minor" implies the converse) plus the D3 wire-body change; lead-backend-engineer concurs. If Q3 keeps the fixed strings and Q5 cuts sdk as a patch, this becomes v0.7.1. |
| `pockets/authentication/stores/{pgx,turso}` | **none** | untouched; their `authentication v0.7.0` / `sdk v0.5.0` pins upgrade at the host via MVS. |
| `integrations/datastores/turso` | **none** | no code change (the D4 `[]` fix lives in `crud.TrimPage`, which turso's `List` already calls). |

Cold resolution per the 2026-08-27 train: a scratch module outside the workspace requiring the three new tags, `GOWORK=off GOFLAGS=-mod=mod go build ./... && go vet ./...`, plus `go list -m all` showing no `replace`.

## Compatibility

- 1–3 additive; no exported symbol changes shape. `ListParams`/`ParseListRequest`/`ParseOrder` signatures unchanged; their error **text** is unchanged as a prefix, their error **identity** now includes `sdk.ErrInvalidInput`. A host matching the raw strings keeps working; a host that special-cased the 500 (gps-360-go `wire.go:79-82`) can delete the special case.
- Pages returned by `TrimPage`/`MapPage`/`MapPageErr` marshal nil items as `"items":[]` instead of `null` (Q7) — visible to turso-backed hosts. Directly constructed `Page[T]{}` remains unchanged and still marshals `null`.
- The authentication pocket's five list routes change their 400 body text (D3, Q3); status and `code` unchanged.
- pgxdb: D6 behaviour changes. `Open` still never reads the process environment itself — pgconn's `PGTZ` handling is upstream's and is honoured as "the host named a zone".
- No schema, no migration, no store-module change. `make guard` stays green; one optional new guard (Q8).

## Tasks

Executor: `implementer` (model: opus). Sequential unless `depends_on` says otherwise. Every `verify` runs from the repo root.

| T# | task | files | verify |
|---|---|---|---|
| **T1** | `crud`: wrap the nine rejections (D2 table) in `sdk.ErrInvalidInput`; add `listquery.go` with `QueryKey*`, `ListQueryOptions`, `ParseListQuery` (delegates to `ParseListRequest`; never reads `order`); extend `pagination_test.go`'s table with an `errors.Is` column; add `listquery_test.go` (each key; `q` trimmed; `DefaultStrategy` honoured; `Limits` resolution matches `ParseListRequest`; `order` present is ignored); wrap `order.go` errors and pin them in `order_test.go`; update the package doc's transport-vocabulary paragraph (`crud.go:85-88`: the constants, "order via ParseOrder") and the sdk README `crud` row (`sdk/README.md:106`). Cursor-decode errors untouched. | `sdk/foundation/crud/pagination.go`, `sdk/foundation/crud/order.go`, `sdk/foundation/crud/listquery.go` (new), `sdk/foundation/crud/listquery_test.go` (new), `sdk/foundation/crud/pagination_test.go`, `sdk/foundation/crud/order_test.go`, `sdk/foundation/crud/crud.go` (doc only), `sdk/README.md` | `cd sdk && go build ./... && go test ./... && go vet ./...` ; `make guard` |
| **T2** | `crud`: `Items[T]` + `MapItems[T,U]` (= `MapPage(Items(x), fn)`) beside `MapPage`; if Q7 rules in, `TrimPage` nil→`[]T{}` and `MapPage`/`MapPageErr` always allocate. Update the existing `crud_test.go` assertion that currently requires `MapPageErr(Page[int]{})` to preserve nil, and pin nil normalization through `Items`, `MapItems`, `TrimPage`, `MapPage`, and `MapPageErr`; wire-shape tests assert `{"items":[]}` and no pagination keys. Add an empty Turso `List` regression asserting `Items` is non-nil and JSON contains `"items":[]`; pgxdb's module run is a compile/regression check, not the empty-page wire proof. Add the `Page.Items` godoc line and name the constructors in the sdk README `crud` row. | `sdk/foundation/crud/pagination.go`, `sdk/foundation/crud/pagination_test.go`, `sdk/foundation/crud/crud_test.go`, `sdk/foundation/crud/crud.go` (doc only), `sdk/README.md`, `integrations/datastores/turso/list_test.go` | `cd sdk && go build ./... && go test ./... && go vet ./...` ; then `cd integrations/datastores/turso && go test ./...` and `cd integrations/datastores/pgxdb && go test ./...` — depends_on T1 |
| **T3** | `web`: `NoStore()` (D5) beside `DefaultHeadersMiddleware` + a test (header present; a handler override wins). Extend existing `errors_test.go` with a synthetic error wrapping only root `sdk.ErrInvalidInput`; prove `ErrFromDomain` returns generic 400/`bad_request`/`invalid input` and `ErrValidation` carries the synthetic sentence. Do **not** import `crud` or any other foundation package from `web`, including in tests. ARCHITECTURE.md middleware table row (`:150`) and sdk README `web` row list `NoStore`. | `sdk/foundation/web/middleware.go`, `sdk/foundation/web/middleware_test.go`, `sdk/foundation/web/errors_test.go`, `ARCHITECTURE.md`, `sdk/README.md` | `cd sdk && go build ./... && go test ./... && go vet ./...` ; `make guard`; `rg` confirms no `sdk/foundation/crud` import anywhere under `sdk/foundation/web`, including `_test.go` — depends_on T1 |
| **T4** | `pockets/authentication`: `parseListRequest` → `ParseListQuery` + `ParseOrder` + `ErrValidation` (D3); handler tests for `?limit=zero`, `?limit=0`, `?cursor=x&offset=1`, `?order=nope:asc` on one list route asserting 400 + `bad_request` + the sentence; README row `:243` note. No `go.mod` change in-tree (go.work resolves `sdk`); the pin moves at T7. | `pockets/authentication/internal/inbound/authentication/machine.go`, the existing test file that drives a list route (`machine_test.go` / `invitation_test.go` / `useradmin_test.go` — implementer picks the one already exercising `parseListRequest`), `pockets/authentication/README.md` | `cd pockets/authentication && go build ./... && go test ./... && go vet ./...` ; `make guard` — depends_on T1 |
| **T5** | `pgxdb`: extract `poolConfig`; D6a `AfterConnect` (UTC `TimestamptzCodec` + explicitly built `_timestamptz` `ArrayCodec`); D6b `RuntimeParams["timezone"]` default with the `timezone`/`options=` detection (if Q9 rules in); `Config` doc comment on `AfterConnect` ownership/chaining; hermetic tests on `poolConfig` that clear `PGTZ`, `PGOPTIONS`, `PGSERVICE`, and `PGSERVICEFILE` for default cases and separately pin explicit env overrides; `TestLive_ScanUTC` (D6 proof, including the `timestamptz[]` case); README: `Open` row + a new "Time zone — UTC-located scans on every connection" section (D6a contract, D6b's list of zone-dependent SQL and the DSN escape hatch, `tstzrange` out of scope with the citation). | `integrations/datastores/pgxdb/postgres.go`, `integrations/datastores/pgxdb/postgres_test.go`, `integrations/datastores/pgxdb/timezone_live_test.go` (new), `integrations/datastores/pgxdb/README.md` | `cd integrations/datastores/pgxdb && go build ./... && go test ./... && go vet ./...` ; live leg uses an isolated temporary Postgres 17 container: publish `5432` on a random loopback port, capture the container ID, install an exit trap that always stops it, wait for `pg_isready`, discover the mapped port, and run `POSTGRES_TEST_DSN=... go test -run 'TestLive_' ./...`. Do not use a fixed host port or race startup; if Docker/DSN is unavailable, report the live leg UNVERIFIED by name; never point this at a host database. No `examples/*/.env.example` carries a Postgres DSN (both carry Turso placeholders) and the Turso playground authorization is SQLite-only, so there is no in-repo DSN to borrow. |
| **T6** | Real-behaviour leg for the request/parser contract (the auth-cms proof host; no code change expected). `cp examples/auth-cms/.env.example examples/auth-cms/.env` if absent; `cd examples/auth-cms && go run ./cmd/server` (port 8082, in-memory stores). Register + login with a cookie jar per `examples/auth-cms/README.md:372-383`, then: `curl -i -b jar 'http://localhost:8082/auth/invitations/mine?limit=zero'` → `400 {"message":"page limit conversion: strconv.Atoi: parsing \"zero\": invalid syntax: invalid input","code":"bad_request"}`; `?limit=0` → `rows value too small, must be larger than 0: invalid input`; `?cursor=x&offset=1` → `cursor and offset are mutually exclusive: invalid input`; `?order=nope:asc` → `unknown order field: nope: invalid input`; `?limit=5` → 200 `{"items":[]}`. This last body is the authentication DTO's existing `newPageResponse` behaviour and is **not** the D4 connector proof; T2's Turso regression is that proof. Paste the bodies into the PR. | — | manual, recorded in the PR body — depends_on T4 |
| **T7** | RELEASING: three "next tag" upgrade notes (sdk: the additions, the classification change, the `ErrValidation`-carries-the-sentence contract, and constructor/bridge nil normalization; pgxdb: D6 in full — the pgx binary-decoder explanation, the array/range scope, D6b's zone-dependent-SQL list with the `AT TIME ZONE`/DSN escape hatches; authentication: the wire-body change + pin move), a tag manifest under this plan (`.claude/plans/web-crud-list-request/tag-manifest.md`: order sdk → pgxdb → authentication; `pockets/authentication/go.mod` `sdk v0.5.0 → v0.6.0` at cut time; `go mod tidy` per module), and the cold-resolution recipe (D7). Owner cuts the tags. `make check` green before the PR. | `RELEASING.md`, `pockets/authentication/go.mod` (at cut), `.claude/plans/web-crud-list-request/tag-manifest.md` (new) | `make check` ; post-tag: cold resolution per D7 — depends_on T1–T6 |
| **T8** (optional, Q8) | Makefile: `guard-crud-no-nethttp` beside G12 — production files under `sdk/foundation/crud/` never import `"net/http"`; wired into `guard`; allocate the next free G-id at implementation time and update the declared guard count plus the guard inventories in `Makefile`, `README.md`, `ARCHITECTURE.md`, and `.github/workflows/check.yml`. If the host-contract PR-B guard lands first this is G22; otherwise coordinate the two plans so one takes G21 and the other G22. Add the guard's standard planted-violation proof. | `Makefile`, `README.md`, `ARCHITECTURE.md`, `.github/workflows/check.yml` | planted violation fails with the new G-id; restored tree passes `make guard` |

### Optional block — bundling #9 into the same pgxdb tag (only if Q1 rules IN)

Additive, small, and gps-360-go adopts both in the one pin move; cost is three short tasks and one more README/RELEASING paragraph. Not planned in detail beyond this.

| T# | task | files | verify |
|---|---|---|---|
| **T9a** | `Collect[T](ctx, q Querier, sql string, args any) ([]T, error)` — the exported twin of `ListQuery.collect` (`list.go:206-217`; strict `RowToStructByName`, `MapError` on both errors, `[]T{}` on no rows). Godoc: parent-bounded, unpaginated; not a paging primitive. Hermetic test + a `TestLive_Collect` leg. | `integrations/datastores/pgxdb/query.go`, `query_test.go`, `live_test.go`, `README.md` | module verify as T5 |
| **T9b** | `ProbeTables(ctx, db Querier, tables ...string) error` — `ProbeTable` in a loop, the error naming the first failing table. | `integrations/datastores/pgxdb/probe.go`, `probe_test.go`, `README.md` | as T5 |
| **T9c** | `MapError`: `invalidTextRepresentation = "22P02"` → `fmt.Errorf("%s: %w", pgErr.Message, sdk.ErrInvalidInput)`. The message never reaches a client (`ErrFromDomain` is generic — lead-backend-engineer), but it is what the host's LOG loses today ("which column was malformed"), so keep the wrap as the issue asks; the existing four cases keep returning bare sentinels (no scope creep). Test in `postgres_test.go:18`. README `MapError` row. | `integrations/datastores/pgxdb/postgres.go`, `postgres_test.go`, `README.md` | as T5 |

## Sequencing

T1 → T2 → {T3, T4} → T5 (independent of sdk; may run in parallel with T2–T4) → T6 → T7. T8 anywhere after T1. T9a–c, if ruled in, slot after T5 and before T7.

## Adoption (downstream, after the tag — gps-360-go `internal/inbound/wire/wire.go` @ `3cfa2c1`)

| host helper | sites | after `sdk/v0.6.0` + `pgxdb/v0.6.0` |
|---|---|---|
| `wire.ListRequest` | 10 | **delete**; each site becomes `crud.ParseListQuery(r.URL.Query(), crud.ListQueryOptions{Limits: limits})` + `web.RespondJSONError(w, web.ErrValidation(err))`. (Or keep a 6-line host wrapper that preserves the `ok=false` shape — the host's call; the special-case comment goes either way.) |
| `wire.Items` / `wire.RespondList` | 1 / 14 | **delete** `Items` (→ `crud.MapItems`); `RespondList` is `crud.MapItems` + `web.RespondJSON` + the `ErrFromDomain` branch — delete or keep as a host convenience over the sdk pieces (the issue says delete). |
| `wire.NoStore` | 3 (`cmd/server/main.go:355` + two test routers) | **delete** → `web.NoStore()`. |
| `wire.Time` / `wire.TimePtr` | 41 / 14 | **delete**; DTO fields become `time.Time` / `*time.Time` — default marshalling is RFC 3339 with nanoseconds and `Z`, byte-identical to today's strings for scanned instants. **Audit first:** an instant minted in-process (`time.Now()` in a domain constructor, computed `Current()`-style fields) still carries `time.Local` unless the container runs `TZ=UTC`; normalize those at the domain edge (`.UTC()`) or they will marshal with an offset. |
| `wire.Date` | 15 | **stays** until an `sdk` civil-date type exists (issue item 5). |
| `wire.Vocabulary` / `VocabularyPtr` | 18 | **stays** — `{value, stored}` is a product rule. |
| `wire.JSON` | — | stays (jsonb empty-substitution is a host convention). |

Also: gps-360-go's `openDatabase` (`cmd/server/database.go`) needs no change — `DB_URL` carries no `timezone`, so the D6b default applies, and the host has no zone-dependent SQL (grep, §D6b). coordination-hub: the owner greps `date_trunc|::date|to_char|AT TIME ZONE` before repinning pgxdb.

## Non-goals

- Issue item 5, an `sdk` civil-date type — noted, not asked; `wire.Date` stays.
- Issue #9 (`Collect`, `ProbeTables`, 22P02) unless Q1 rules it in — the optional block above.
- Changing `web.ErrFromDomain` to surface messages, or touching `SafeDomainError`.
- Folding `order` into `ParseListQuery` (D1 — the cms fall-back posture) or wrapping cursor-decode errors (D2).
- A `Page.MarshalJSON`; unifying the authentication pocket's per-handler `Cache-Control: no-store` writes onto `web.NoStore()`, or having the pocket auto-apply it to protected groups — separate, pocket-scoped.
- `web/openapi.go` list-param drift (`offset`/`count`/`q` undocumented) — left as found; note it in the PR.
- `pgxdb.Config.TimeZone`/`ScanLocation` fields (Q4), `tstzrange`/`tstzmultirange` re-registration, `AfterConnect: SET TIME ZONE`, any turso change.
- Deleting anything in gps-360-go — downstream, owner-driven (§Adoption).

## Consultation (2026-08-27) — what changed from the first draft

**architecture-steward (aligned-with-edits).** Candidate A (`crud.ParseListQuery(url.Values, …)`) is the only legal home: crud already owns the key vocabulary (`crud.go:85-99`), `net/url` keeps G1/G12b green, `web` stays untouched (the `validation` ↔ `web.FieldErrors.AddErr` doc-only precedent), a capability fails admission, a G12 exception breaks a locked decision. `Items`/`MapItems` belong in crud; `NoStore` is foundation-web mechanism and must read as a `DefaultHeadersMiddleware` preset, never a security posture, and never name `RequirePrincipal` (a gps-360-go symbol). Cursor-decode errors must not be swept into the wrap. No Makefile change needed for the placement. → Folded: D1 wording, D2's cursor exclusion, D5's godoc.

**lead-backend-engineer (ship-with-edits).** Accepted: R1 — array/range codecs capture the default element pointer; `_timestamptz` must be built over the new element, `tstzrange`/`tstzmultirange` documented out of scope, and the live test scans a `timestamptz[]` (D6a, T5). R2 — `ScanLocation` fixes both decoders, so the session param is a separate, server-side behaviour change with a sharper blast radius than the issue states → split into D6a (unconditional) and D6b (owner-ruled Q9, still recommended, with the measured downstream check). R3 — `TrimPage`/`MapPage`/`MapPageErr` leave nil as nil → D4 normalizes all three (Q7) — confirmed real for turso (`list.go:280`), not for pgx (`CollectRows` starts non-nil). R4 — cms's fall-back `?order` posture (`entries.go:118-130`) → `order` dropped from `ParseListQuery`, `ParseOrder` stays separate; `ListQueryOptions` kept as a struct per the optional-params rule. R5 — `NoStore` godoc claims no guarantee. R6 — `AfterConnect` ownership/chaining documented in `Config`. R7 — the pocket's 400-message change is called out (D3/Q3, RELEASING). R8 — the `net/http` rule gets a one-line guard (T8/Q8). Item 5 — authentication minor. Item 6 — bundle #9 (Q1). **Declined:** `Page.MarshalJSON` (three plain constructor rules beat a generic marshaller); dropping the 22P02 message (it is log value, which is what the issue's host lost); a `Config.ScanLocation` field (Q4 — presentation, not data).

## Open questions for the owner

1. **Q1 — Bundle #9 into `pgxdb/v0.6.0`?** Recommendation: **yes, one train, one tag** — the three helpers are additive and tiny (`Collect` is `list.go`'s `collect` exported; `ProbeTables` a loop; 22P02 one `case`), gps-360-go adopts both issues in a single pin move, and a second pgxdb minor a week later costs another cold-resolution pass and another host repin for the same adopter. Cost: T9a–c (~1 implementer session) and one more RELEASING paragraph. If ruled OUT, #9 tags alone later as v0.7.0.
2. **Q2 — `ParseListQuery` over `url.Values` in `crud`, `order` left to `ParseOrder` (D1)** — confirm.
3. **Q3 — The pocket's 400 body carries the parser's sentence (D3)** — confirm, or keep the fixed `"invalid page parameters"`/`"invalid order parameter"` strings.
4. **Q4 — No `pgxdb.Config.TimeZone`/`ScanLocation` field; the DSN is the session-zone escape hatch, scans are always UTC-located (D6)** — confirm.
5. **Q5 — `sdk` as minor (v0.6.0)** rather than the additive-as-patch precedent, because error identity and empty-page shape change observably.
6. **Q6 — `NoStore` writes `Cache-Control: no-store` only (D5)** — confirm no `Pragma`/`Expires`.
7. **Q7 — `TrimPage`/`MapPage`/`MapPageErr` normalize nil to `[]` (D4)** — recommendation: **yes**, same tag; it applies the constructor/bridge normalization where hosts actually hit it (turso-backed empty pages say `null` today) without changing direct `Page[T]{}` construction.
8. **Q8 — A one-line `guard-crud-no-nethttp` (T8)** — recommendation: **yes**; the reason is dependency weight (every store adapter imports crud), and the repo's habit is one grep per boundary.
9. **Q9 — D6b, the session `timezone=UTC` default** — recommendation: **yes**, with the RELEASING note listing the zone-dependent SQL it changes and the two escape hatches; the originating host has zero such SQL. If ruled OUT, D6a alone still closes the issue's symptom.

## Recommended reviews

product-manager (scope: three modules, one train, the #9 bundling call, Q7's wire change); platform-sre (D6b pooler behaviour and the zone-dependent-SQL caveat, tag/cold-resolution discipline); data-integration-reviewer (D6a codec/array registration, the live test matrix, the turso `[]` change); architecture-steward (consulted — re-check the final D1/D5 text).

## Notes

- The G20 vocabulary rule applies to this plan and to every doc line it touches: the third tier is `pockets/`; RELEASING's historical headings keep their old paths and are not edited.
- This plan introduces no `foundation`→`foundation` import in production **or tests**. `crud` imports stdlib plus the root `sdk`; `web` imports the root `sdk`; the authentication pocket is the legal composition point. G12 applies equally to `_test.go`, so there is no test exemption to route around the boundary.
- Message shape reminder for the implementer: sentence first, sentinel last (`"…: %w"`), matching `crud.go:221`; never `"%w: …"`.

## As executed (2026-08-27)

- **Precondition** landed first as its own PR: #12 `guard-g12-tests-included` (G12b/G12c now police `_test.go` too) — squash @ `8363c29`.
- **PR #13** `web-crud-list-request`, eight task commits (T8 G21, T1, T4, T2, T5, T3, T9a–c, T7), rebased onto `main` after #12, squash @ `1d88986`. `make check` green; CI green on both PRs.
- **Executor:** `implementer` (Opus) per task, T1‖T5 then T2‖T4, then T3‖T9; T6/T7/T8 by the session directly.
- **Real-behaviour legs:** pgxdb `TestLive_ScanUTC` / `TestLive_Collect` + the whole `TestLive_*` suite against a throwaway `postgres:17` on a random loopback port, under `TZ=America/Los_Angeles` (every scan `+0000 UTC`; `'2026-01-01T00:00:00+05:00'::timestamptz` → `2025-12-31 19:00:00 +0000 UTC`; `SHOW TimeZone` = `UTC`, `Europe/Oslo` with the DSN override). T6 on `examples/auth-cms`: the five `GET /auth/invitations/mine` bodies match this plan's T6 row verbatim; `?limit=5` → `200 {"items":[]}`.
- **Deviations (documented in code + RELEASING):** (1) D6a's "scalar-only leaves `timestamptz[]` in `time.Local`" is not true for a `[]time.Time` destination on pgx v5.8.0 (the array codec re-plans through the map) — it IS true for a `pgtype.Timestamptz` element destination; the explicit `_timestamptz` registration is kept as the strictly-safe form and the rationale corrected. (2) `hasSessionTimeZone` matches the `RuntimeParams` key case-insensitively. (3) `Collect` is `args ...any`; `ListQuery.collect` delegates to it; `ProbeTables` returns the first failure unwrapped (`ProbeTable` already names the relation).
- **Tags, in order:** `sdk/v0.6.0` @ `1d88986` → `integrations/datastores/pgxdb/v0.6.0` @ `1d88986` → pin move (`pockets/authentication/go.mod` `sdk v0.5.0 → v0.6.0`, tidied against the proxy once it served the tag — a too-early `go mod tidy` fails `unknown revision` and the proxy briefly caches the negative lookup) @ `7960db6` → `pockets/authentication/v0.8.0` @ `7960db6`. Stores and turso NOT retagged.
- **Pre-existing flags, not fixed:** `examples/auth-cms` `cp .env.example .env` fails boot (inline `# comments` become values — `AUTH_TOKEN_ENCRYPTER_KEY … got 63 bytes`); jobs-mode in-memory delivery never delivered the verification mail within 8s on that host (`DELIVERY_MODE=in_process` did); `web/openapi.go` list-param drift; pgxdb README lacks `QueryOne`/`ExecAffecting` rows; `sdk/foundation/workers/fenced_test.go` not goimports-clean.
- **Downstream (owner):** gps-360-go repins `sdk v0.6.0` + `pgxdb v0.6.0` and deletes `wire.ListRequest/Items/RespondList/NoStore/Time/TimePtr` per §Adoption (audit in-process `time.Now()` instants for `.UTC()` first); coordination-hub greps `date_trunc|::date|to_char|AT TIME ZONE|EXTRACT` before the pgxdb repin. PR-B's guard takes **G22**.
