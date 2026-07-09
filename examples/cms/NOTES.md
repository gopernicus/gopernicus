
## 2026-07-08 — pgx-crud-v1 milestone CLOSED (P1–P6 executed same day as ratification)

The pgx v5 idiom sweep + sdk/crud List standards landed end to end.
**Shipped:** sdk/crud `ListRequest{+Offset,+WithCount}`/`Page{+Total}`
with the normative mode/count matrix in the package doc (cursor mode
default with reverse-probe prev pages; `offset>0` selects offset mode;
cursor+offset rejected both edges; `Total` = filter-WHERE count),
`Validate`/`UsesOffset`/`MapPage`, five-string `ParseListRequest`;
pgxdb NamedArgs list toolkit (`QuoteIdentifier`, keyset/order/limit
builders, `List[T]`/`ListQuery[T]` over CollectRows+RowToStructByName)
with a semantics-only turso twin; all four features' pgx stores on the
full idiom set (store-local db-tagged row structs + toDomain +
`crud.MapPage` — domain types stay tag-free; NamedArgs everywhere;
UNNEST bulk writes for cms entry_fields/entry_terms and the events
outbox; preserve-verbatim honored for jobs Claim SKIP LOCKED, ClaimDue
CAS, auth DELETE…RETURNING consume); order allow-lists in the domain
rims (created_at only everywhere — jobs priority excluded: only a
partial claim-path index exists; cms updated_at/published_at excluded:
un-indexed / nullable); the six-case storetest family
(Order/PrevPage/OffsetMode/WithCount/StaleCursorOrderChange/
CursorOffsetExclusive) on every paginated port across memstores + both
dialects; jobs memstore NEWLY under conformance (coverage gap closed);
auth JSON list endpoints take order/offset/count and return
total/has_prev/previous_cursor; cms admin + public archive page both
directions via the **`cms.Pager` view-model** (mid-phase owner ruling:
Views port grew once — `EntriesList`/`Archive` take a Pager struct —
so future pagination growth is additive; en-passant fix: the old
"Older →" href pointed at /…/new). Legacy `ListPage` DELETED from both
connectors, zero callers. Docs synced (pgxdb toolkit + no-SendBatch
README, NEW turso README naming the `turso-crud-parity` follow-up,
sdk/README crud row, features/README authoring-checklist item 13);
authorization-v1 02a/02b carry dated landed-notes.

**Live artifacts (2026-07-08, all post-deletion where marked):**
per-phase — P2 pgx TestLive_ListBehavior 9/9; P3 pgx 101 subtests +
turso 371.3s + the authenticated HTTP protocol over examples/auth-cms;
P4 pgx 40/40 + turso 170.6s + browser click-through (Playwright) of
admin/public bidirectional paging incl. order flip and order=nope
fallback; P5 events+jobs pgx (ConcurrentClaim/ClaimDueCAS/LeaseExpiry
green) + turso 41.5s + jobs-minimal demo jobs executed through the
extended memstore. Milestone close (post-ListPage-deletion): full
`make test-stores` exit 0 — pgx cms 2.669s / auth 4.159s / jobs 5.277s
/ events 0.532s; turso events 10.354s fresh, and the three cached legs
re-run `-count=1` fresh: cms 167.4s / auth 374.2s / jobs 80.1s — all
ok, zero FAIL. Post-deletion SSR spot-check: cms paging round-trips
both directions; seeded rows cleaned (playground at 0 articles);
`make check` + `make guard` green at 30 modules throughout.

**Breaking API (zero tags existed, no consumers broken):**
`ParseListRequest` five-string signature; cms Views port
`EntriesList`/`Archive` → Pager; connector `ListPage` removed.

**Declared follow-ups:** `turso-crud-parity` (named-arg emulation,
struct scanning, builder ergonomics — turso README names it); jobs
storetest `reference_test.go` now duplicates the memstore conformance
run (retire? owner call); no wanted-index log entries accrued (every
order vocabulary stayed on already-indexed created_at).

**Sequencing:** authorization-v1 Z2a/Z2b now execute against these
standards (dated notes on its files); its own Q1–Q5 ratification still
owed. Plans housekeeping: `.claude/plans/pgx-crud-v1/` →
`.claude/past/pgx-crud-v1/` this session, README table updated.
