# crud-search-upstream — restore the search half of the list vocabulary

**Target repo: `gopernicus` (`~/code/gopernicus-ecosystem/gopernicus`).**

Status: **REVISED DRAFT — not ratified.** The original open decisions are resolved
below; the plan still needs owner ratification before execution.

Blocks: `.claude/plans/list-surfaces.md` in coordination-hub, whose D4 and R1
depend on this landing. That plan's Milestone A can start on the paging half
alone; every screen that searches waits on this tag.

## The gap

v3's `sdk/foundation/crud` is the list vocabulary with **half of it missing**. It
owns the page request, the cursor codec, the order allow-list and the page
envelope. It has no word for search at all — `crud.go`, `order.go`, `cursor.go`
and `pagination.go` contain no search vocabulary, and `pgxdb.ListQuery` has no
place to declare a searchable column beside `OrderFields`.

**v1 had all of it, generated.** Verified in `gopernicus-original`:

| Piece                                                           | v1 location                                            | v3            |
| --------------------------------------------------------------- | ------------------------------------------------------ | ------------- |
| `@search:` annotation → fields + mode                           | `workshop/codegen/generators/resolve.go:154-166`       | —             |
| three modes: `ilike`, `web_search`, `tsvector`; default `ilike` | `generators/types.go:266`                              | —             |
| `SearchTerm *string` on the generated filter                    | `generators/repository_tmpl.go:107-108`                | —             |
| store builds the predicate, OR'd across fields                  | `generators/pgxstore_tmpl.go:523-553`                  | —             |
| transport reads the term                                        | `generators/bridge_tmpl.go:383,397,408` (`q.Get("s")`) | —             |
| page request, cursor, order, envelope                           | —                                                      | ✓ all present |

So this is a **regression from the de-generation**, not a feature request. Every
host that needs a searchable list is currently inventing its own word for it —
coordination-hub was about to, and segovia v1 already has one (`s`). Two dialects
and counting.

## What v1 got wrong, and should not be restored as-is

`pgxstore_tmpl.go:545` builds the pattern as:

```go
searchPattern := "%" + *filter.SearchTerm + "%"
```

No escaping. A term containing `%` or `_` becomes a wildcard, so a person typing
`100%` into a search box matches every row and a person typing `a_c` matches
`abc`. A search term is something a human typed, and it must be a **literal**.
The restoration fixes this rather than carrying it forward.

## Design

The shape mirrors what crud already does for ordering, deliberately — the point
is that a host declares searchable columns exactly the way it declares sortable
ones, and neither the transport edge nor the store learns a second idiom.

```
                    ORDER (exists)                  SEARCH (this plan)
declaration    map[string]crud.OrderField      []crud.SearchField
transport      crud.ParseOrder(fields, …)      crud.ParseListRequest(… Search …)
request        ListRequest.Order               ListRequest.Search
store          ListQuery.OrderFields           ListQuery.SearchFields
applied by     AddOrderByClause                AddSearchClause
```

### sdk/foundation/crud

```go
// SearchField describes a searchable text column — the twin of OrderField.
type SearchField struct {
	Column string
}

// ListParams gains one field, parsed like the rest:
type ListParams struct {
	Limit, Cursor, Offset, Count string
	Search                       string   // NEW
	Limits                       Limits
	DefaultStrategy              Strategy
}

// ListRequest gains one field:
type ListRequest struct {
	Limit     int
	Cursor    string
	Offset    int
	WithCount bool
	Strategy  Strategy
	Order     Order
	Search    string   // NEW — stores normalize it; blank means no search
}
```

`ParseListRequest` trims the term and treats blank as absent. Store helpers trim
again because a programmatic `ListRequest` can bypass the transport parser. Unlike
a bad limit, **the contents of a search term are never invalid** — `%`, `_`, `\`
and non-ASCII text are all legal input. A nonblank term sent to a list whose
`SearchFields` is empty is rejected with `sdk.ErrInvalidInput`, rather than
silently returning an unfiltered page.

Also add the one pure predicate, so a non-SQL backend, a test, and an in-memory
store cannot disagree with Postgres about what a term means:

```go
// MatchesSearch reports whether haystack matches term: a case-insensitive
// LITERAL substring under the search case-folding contract below. It trims term;
// a blank term matches everything.
func MatchesSearch(haystack, term string) bool
```

The v1 contract is deliberately narrow and dialect-reproducible: ASCII letters
compare case-insensitively; non-ASCII code points compare exactly. PostgreSQL
applies `ILIKE` under `COLLATE "C"`; SQLite/libSQL uses its default `LIKE`
semantics. `MatchesSearch` performs the same ASCII-only fold rather than Go's
Unicode-wide `strings.ToLower`. The oracle table includes accented and non-Latin
cases so a later move to full Unicode folding must be an explicit, cross-dialect
contract change rather than an accidental backend difference.

Document `q` in the package doc's existing "Query-param vocabulary" section
alongside `limit`, `cursor`, `offset`, `count` and `order`. `q` is the canonical
v3 transport key. The SDK carries only the already-extracted value, so legacy
edges migrating v1 clients may accept `s` as a temporary alias: use `q` when it
is present, otherwise fall back to `s`, and document removal of the alias in the
host. New endpoints accept `q` only.

### integrations/datastores/pgxdb

```go
type ListQuery[T any] struct {
	BaseSQL      string
	Args         pgx.NamedArgs
	OrderFields  map[string]crud.OrderField
	DefaultOrder crud.Order
	SearchFields []crud.SearchField   // NEW — empty accepts only an empty search
	PK           string
	Limits       crud.Limits
	OrderValueOf func(row T, field string) any
	PKOf         func(row T) string
}
```

New in `listquery.go`, beside `AddOrderByClause` and `ApplyCursorPagination`:

```go
// AddSearchClause appends a case-insensitive literal-substring predicate over
// fields and binds @list_search. Writes WHERE when buf holds none and AND otherwise.
// Columns are quoted via QuoteIdentifier; the term is escaped so %, _ and the
// escape character are literal. It trims term, rejects a nonblank term with no
// fields, and rejects an existing @list_search arg instead of overwriting it.
func AddSearchClause(buf *strings.Builder, args pgx.NamedArgs, fields []crud.SearchField, term string) error
```

producing `(("col_a" COLLATE "C") ILIKE @list_search ESCAPE '\' OR ("col_b"
COLLATE "C") ILIKE @list_search ESCAPE '\')` with `@list_search` bound to
`"%" + escape(term) + "%"`, where `escape` replaces `\` → `\\`, `%` → `\%`,
`_` → `\_` **in that order**. `list_search` is a connector-reserved argument
name.

`List` must clone `q.Args` before binding search. `ListQuery` is passed by value,
but its `pgx.NamedArgs` map is not: mutating it directly would leak the reserved
argument into the caller and future queries.

### The query fan-out trap

`ListQuery.count` (list.go:230) builds `SELECT COUNT(*) FROM (BaseSQL)`. It sees
`BaseSQL` and nothing `List` appended. So if search is appended to a local buffer
the way the cursor predicate is, **`WithCount` returns the unfiltered total** —
a page of 3 rows reporting 412 results.

There are **three consumers and four query-construction paths** to cover:

1. the cursor or offset page query;
2. the cursor strategy's reverse probe (`markPrev`), when a cursor is present;
3. the count query; and
4. the other pagination strategy's page query.

The predicate must be built once and reach all of them. Cleanest: before the
strategy switch, derive a searched copy of `ListQuery` whose `BaseSQL` already
contains the predicate and whose args are a cloned-and-extended copy. The
existing cursor, offset, `markPrev` and `count` paths then all consume the same
searched base. Do not append search only to a local forward-page buffer: that
would make `WithCount` report the unfiltered total and let the reverse probe
derive `HasPrev` / `PreviousCursor` from nonmatching rows.

## Decisions

### D1 — substring only ships

v1 had three (`ilike`, `web_search` via `websearch_to_tsquery`, `tsvector` against
a precomputed column).

Ship one mode, no `SearchMode` type and no config. It is what every current
consumer needs, and it is the only mode whose meaning can be stated in the SDK
and tested identically across both stores. Full-text can be added later without
breaking this signature.

### D2 — Turso ships in the same pass

`integrations/datastores/turso` has its own `List`. v1 generated both dialects,
and the migration files are explicit about keeping structure and semantics
identical across them. coordination-hub is pgx-only, so nothing is blocked either
way — but a search vocabulary that exists in one dialect and not the other is the
kind of asymmetry that gets discovered by whoever ports a host.

Do both. Turso's escape syntax is checked separately: SQLite `LIKE` is
case-insensitive for ASCII by default and takes `ESCAPE` the same way, but
`ILIKE` does not exist there. Its predicate is `LIKE`, and that dialect
difference belongs in the store. A live Turso/SQLite test runs the same wildcard,
backslash and non-ASCII oracle as PostgreSQL and `crud.MatchesSearch`.

### D3 — `q` is canonical; `s` is a migration alias only

New v3 transports read `q`. A transport with known v1 clients may temporarily
fall back to `s` only when `q` is absent. The alias belongs at that host's
transport edge, not in `crud.ListParams`, and must be documented with a removal
milestone. This restores the capability without silently pretending the v1 wire
key and the v3 wire key are the same contract.

## Tasks

**T1. `crud`: the vocabulary.** `SearchField`, `ListParams.Search`,
`ListRequest.Search`, `MatchesSearch`, and the package-doc entry for `q`. Tests:
transport trimming, programmatic-request trimming at the matcher/store seam,
blank-means-absent, and a `MatchesSearch` table whose rows are the ones a
wildcard-aware or Unicode-wide implementation would get wrong — `100%`, `a_c`,
a literal backslash, ASCII case pairs, accented case pairs and non-Latin text.
That table is the shared oracle for T2 and T3.

**T2. `pgxdb`: apply it.** `ListQuery.SearchFields`, `AddSearchClause`, wiring in
`List` before the strategy switch so **both** strategies, the cursor reverse
probe and count share the searched base. Tests: generated SQL and args; wildcard
escaping; invalid identifiers; an existing `list_search` arg fails rather than
being overwritten; a nonblank term with no fields fails rather than being
ignored; search combined with a forward cursor (`WHERE`/`AND` interleaving); a
reverse probe whose preceding rows include nonmatches; search with `WithCount`;
and a live test asserting PostgreSQL agrees with `crud.MatchesSearch` on the T1
oracle.

**T3. Turso parity.** Add `ListQuery.SearchFields` and the positional-argument
twin of `AddSearchClause`; derive the searched query before the strategy switch,
including reverse probe and count. Mirror T2's behavioral tests and run the same
live oracle against SQLite/libSQL. Preserve BaseSQL arg order, append the search
pattern exactly once to each derived execution, and verify that count receives
the same base args without cursor/limit/offset args.

**T4. Exercise it end to end in authentication.** Use API-key `name` as the first
searchable field in both authentication store dialects. Update
`machine.go:parseListRequest` to pass `Search: q.Get("q")`, update the reference
store so its API-key list applies `crud.MatchesSearch`, and add store-conformance
coverage for both dialects. Add an HTTP test proving
`GET /auth/service-accounts/{id}/keys?q=...` returns only matching names.
Declaring `SearchFields` alone is not an end-to-end proof; the transport and
reference implementation must carry the same contract.

**T5. Release.** Per `RELEASING.md`:

| module                                   | current  | target   | dependency / reason                                                                                                         |
| ---------------------------------------- | -------- | -------- | --------------------------------------------------------------------------------------------------------------------------- |
| `sdk`                                    | `v0.3.1` | `v0.4.0` | new `SearchField`, `ListParams.Search`, `ListRequest.Search`, `MatchesSearch` — additive minor                              |
| `integrations/datastores/pgxdb`          | `v0.3.0` | `v0.4.0` | require `sdk v0.4.0`; new search surface and searched-count/reverse-probe behavior — additive minor                         |
| `integrations/datastores/turso`          | `v0.2.0` | `v0.3.0` | require `sdk v0.4.0`; dialect-parity search surface — additive minor                                                        |
| `features/authentication`                | `v0.2.2` | `v0.3.0` | require `sdk v0.4.0`; the existing API-key list gains the additive `q` capability                                          |
| `features/authentication/stores/pgx`     | `v0.1.0` | `v0.2.0` | require the new authentication, sdk and pgxdb tags; declares API-key `SearchFields`                                        |
| `features/authentication/stores/turso`   | `v0.1.0` | `v0.2.0` | require the new authentication, sdk and turso tags; declares API-key `SearchFields`                                        |

Targets are relative to the tags that exist when this plan was revised. Recompute
them if the dirty precondition work lands and moves a baseline first; do not
reuse a tag. Release in dependency order: sdk, both connector modules,
authentication core, then both authentication store modules. Update every
listed module's `go.mod`; the repository's `go.work` otherwise masks stale
sibling requirements. Before tagging, run each changed module with workspace
resolution disabled (`GOWORK=off go test ./...`) and run a throwaway downstream
consumer build against the proposed tags.

All changes are additive for callers that do not send search. An unset
`SearchFields` with an empty search behaves exactly as today. A nonblank search
against a list that declares no searchable fields now fails loud rather than
claiming an unfiltered page is a search result. The count/reverse-probe fixes are
new-path behavior because no shared search path exists today.

**T6. Adopt in coordination-hub.** `go get` the new tags, `go mod tidy && go mod
vendor`, then rewrite `list-surfaces.md` D4 to "SQL-side always" and replace its
R1 with a pointer to this plan's tags. `internal/inbound/http/list.go` already
parses `q` into a search string and should ride `crud.ListParams.Search` instead
once the tag lands.

## Execution log

### 2026-08-16 — T1, T2, T3, T4 complete; T5/T6 pending

**Ratification/stacking.** The owner dispatched this plan mid-session with "we've
got some crud plans that we can stack on this pr/branch/release as well". That
resolves the **Precondition** below: the working tree's `sdk` changes (the
coordination-hub auth upstream packet) and this plan's `sdk` changes ship on the
SAME tag by decision, rather than one being landed first.

**T1 — `crud`: the vocabulary.** New `sdk/foundation/crud/search.go`:
`SearchField{Column}`, `MatchesSearch`, and the `foldASCII` helper;
`ListParams.Search` and `ListRequest.Search` added to their existing structs;
`ParseListRequest` trims the term and treats blank as absent; the package doc's
query-param vocabulary gained `q` with the `s`-alias posture (host edge only,
documented removal, new endpoints accept `q` only).

`MatchesSearch`'s contract is the whole point, so it is stated in its doc and
pinned by a 30-row table (`search_test.go`) that is the SHARED oracle T2/T3/T4
re-run: plain substring behavior; ASCII case folding; the wildcard rows a
naive `"%"+term+"%"` gets wrong (`100%`, `a_c`, lone `%`, lone `_`, literal
backslash, `\%`); and the Unicode rows a `strings.ToLower` implementation gets
wrong (`CAFÉ` vs `café` must NOT match, `Straße` vs `STRASSE` must NOT match,
Cyrillic case must NOT fold, non-Latin substrings must match exactly).

`TestParseListRequestSearch` pins that a term's CONTENTS are never rejected —
`%`, `_`, `\`, `'`, and non-ASCII are all legal things a human types.

**T2 — `pgxdb`: apply it.** New `integrations/datastores/pgxdb/search.go`:
`AddSearchClause`, `EscapeSearchTerm`, and the reserved `list_search` argument.
`ListQuery.SearchFields` added; `List` derives a searched query BEFORE the
strategy switch via `withSearch`.

Predicate: `(("col" COLLATE "C") ILIKE @list_search ESCAPE '\' OR …)`.
`COLLATE "C"` is not decoration — `ILIKE` under a non-deterministic collation is
an error, and under a locale collation its folding would diverge from SQLite and
from `MatchesSearch`.

Tests (`search_test.go`): generated SQL and bound args; WHERE-vs-AND interleaving;
wildcard escaping into the pattern; term trimming; blank-is-a-no-op; **non-blank
term with no fields fails** with `sdk.ErrInvalidInput` and appends nothing; an
invalid identifier is rejected and never reaches the SQL; **an existing
`list_search` arg fails rather than being overwritten**, with the caller's value
verified untouched. `TestWithSearchDoesNotMutateCaller` pins the args-map
aliasing hazard; `TestWithSearchReachesEveryQueryPath` pins the COUNT wrap and the
single-WHERE composition with the cursor predicate.

**T3 — Turso parity.** New `integrations/datastores/turso/search.go`, the
positional twin. The dialect difference is real and lives in the store: **SQLite
has no `ILIKE`**, and its `LIKE` is already ASCII-case-insensitive, so the
predicate is `("col" LIKE ? ESCAPE '\' OR …)` with **one bound argument per
field** (a positional placeholder cannot be reused the way `@list_search` can).
`withSearch` copies the args slice — a slice header shares its backing array, so
an in-place append could scribble into a caller's array with spare capacity, and
the test builds exactly that capacity to prove it does not.

`TestSearchDialectsAgreeOnEscaping` restates the pgx escaping rule locally (the
two connector modules cannot import each other) and fails if either side drifts.
`TestAddSearchClauseSQL/uses_LIKE,_never_ILIKE` guards the dialect difference from
being "fixed" by copy-paste. Argument ORDER is asserted: the caller's args, then
search, then cursor/limit/offset.

**T4 — end to end in authentication.** `apikey.SearchFields` declares **`name`**
and nothing else; the doc states why the key hash and prefix are excluded (a
searchable prefix is a probe primitive). Both dialect stores declare it;
`parseListRequest` passes `Search: q.Get("q")`; the storetest reference and the
example host's `authmem` apply `crud.MatchesSearch`, so a memory store cannot
disagree with SQL.

New shared conformance group `storetest/search.go` (`ListSearch`, 5 cases):
the literal-substring oracle re-run against a real store for 13 terms with
**exact set equality** against `MatchesSearch` (not just "the expected row came
back"); blank-is-unfiltered; parent scoping with an identically-named foreign row;
**`CountReflectsTheSearch`** (the fan-out trap); and `SearchWithCursorPaging`
(the predicate must reach the second page AND the reverse probe).

One test-design correction worth recording: `SearchWithCursorPaging` first used
`Limit: 1` and asserted `HasPrev` on page 2. That failed — correctly. `HasPrev` is
derived from rows STRICTLY BEFORE the cursor, and with a limit of 1 the cursor IS
the entire first page, so page 2 legitimately has nothing before it. The case now
seeds a third matching name and pages two at a time, so the assertion tests the
search rather than the pagination semantic.

HTTP proof: `examples/auth-cms/cmd/server/apikey_search_test.go` mints keys
through the real endpoint and searches with `?q=`, comparing each result set to
the oracle; asserts `count=true` reports the SEARCHED total; and asserts that
searching by a key's own prefix matches nothing.

**Verification (exactly as run, 2026-08-16):**

```
(cd sdk && go test -race ./...)                                              ok
(cd integrations/datastores/pgxdb && go test -race ./...)                    ok
(cd integrations/datastores/turso && go test -race ./...)                    ok
(cd features/authentication && go test -race ./...)                          ok
(cd features/authentication/stores/{pgx,turso} && go test -race ./...)       ok
(cd examples/auth-cms && go test -race ./...)                                ok
make check                                                                   all checks passed
```

**LIVE CROSS-DIALECT ORACLE — CLOSED on both dialects.** The full 13-term table,
including `100%`, `a_c`, a lone `%`, a lone `_`, a literal backslash, ASCII case
pairs, and non-Latin text, plus `WithCount` and cursor paging:

```
POSTGRES_TEST_DSN=… go test -run 'TestConformance_Postgres/ListSearch' -v ./...
    → PASS, all 13 term subtests + BlankTermIsUnfiltered + SearchIsScopedToTheParent
      + CountReflectsTheSearch + SearchWithCursorPaging   (PostgreSQL 17, throwaway container)
go test -tags=integration -run 'TestConformance_Turso/ListSearch' -v ./...
    → PASS, identical set                                  (live Turso playground, 21.7s)
```

PostgreSQL's `ILIKE … COLLATE "C"`, SQLite's `LIKE`, and `crud.MatchesSearch` all
agree on every row. That is the D2 parity claim, closed rather than asserted.

**T5 (release) is folded into the coordination-hub-auth-upstream phase 8 version
freeze**, since both programs ship on one train by owner direction. `RELEASING.md`
carries a keyed entry for this work. **T6 (coordination-hub adoption) is
downstream** and is not performed from this repo.

## Precondition

`gopernicus/main` currently has an **uncommitted working tree** spanning
`features/authentication`, `sdk`, integrations and plan files. The exact counts
are intentionally not pinned here because they changed while this plan was being
reviewed. At the last review the branch was level with `origin/main`, so none of
that work was pushed.

`sdk` is one of the modules this plan changes. Land or stash that work before
starting T1, or the sdk tag carries it.

## Not in this plan: the reverse lookup

Separate ask, recorded here so it is not lost. There is no group-expanding
`resource → subjects` lookup in the authorization feature:

- `Service.LookupResources` (authorization.go:438) **does** expand usersets, with
  a documented Check/Lookup parity guarantee and `ErrEvaluationLimit` rather than
  a partial list. That is `subject → resources`.
- The reverse direction has no expanding equivalent. `GetRelationTargets`
  forwards straight to the store (`authorizersvc/service.go:608-610`);
  `ListRelationshipsByResource` is direct-only; `CheckRelationExists` says so in
  its own doc.

Today that is harmless — coordination-hub writes no userset tuples, so direct
tuples are the whole truth. It stops being harmless the day groups land: a
campaign roster would silently omit anyone whose membership arrives through a
group.

This is **not** a v1 regression — v1's authorizer only had `GetRelationTargets`
too. Proposal when groups get scheduled: `LookupSubjects(ctx, resourceType,
resourceID, permission) ([]SubjectRef, error)`, the twin of `LookupResources`,
same expansion and the same fail-loud limit posture.

Consumers should be built so the id-resolution step is one narrow seam, so
swapping direct tuples for the expanded lookup touches no SQL, no filter and no
screen. coordination-hub's `list-surfaces` plan is written that way.
