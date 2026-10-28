# Host-contract reconciliation — adopt the reference shape, conform the examples, ship `gopernicus guard` (issue #90)

**Status:** RELEASED 2026-10-20 — `workshop/gopernicus/v0.4.0` @ fef8f47b (PRs #91, #93, #96),
cold-verified; release record in `RELEASING.md`. Ratified 2026-10-20 — owner accepted every YOUR CALL recommendation as written
(1 split H11; 2 sanction `ui/` = H14, move showcase, guard all five examples; 3 guard auth-cms;
4 whole driver list; 5 record reference roots as findings; 6 client binaries may declare
interfaces; 7 init skeleton deferred to v0.4.1; 9 deny `internal/**` → host `workshop/`).
No further reviewers. Execution worktree: `../gopernicus-hc`.
**Progress (2026-10-20):** PR-1 #91 open (CI green). PR-2 BUILT: #92 @ 9c26659f, stacked on
#91; `make check` green; auth-cms + minimal driven live. cms live run unverified (no local-file
driver, so task-7's "works offline" claim is wrong). Pre-existing on main: auth-cms jobs-mode
delivery does not deliver within 20s. #91 MERGED @ bdfa265a. #92 merged into the stale base
(`docs/host-contract-rewrite` @ 56756fee), NOT main; #93 carried 9c26659f to main — MERGED
@ ea5d78fd (verified: main tree identical to the stale branch). Lesson:
retarget a stacked PR with `gh pr edit --base main` BEFORE merging it. NEXT: PR-3 (tasks
10–14, `workshop/guard-verb` from main once #93 lands). 2026-10-20: PR-3 IN PROGRESS —
worktree `../gopernicus-hc` on `workshop/guard-verb` @ 954b1c17 (main after #94 plans prune);
tasks 10–14 BUILT @ 0715e26e → PR #96 (make check green; five hosts clean; negative
check exit 1 re-verified by the lead session); NEXT: owner merges #96, then task-15 tag; G30 loop = FIVE hosts incl. goth-showcase (ruling 2 +
README §8). Jobs fenced-wake bug filed as #95 (pre-existing, not this plan).
Audit baseline `origin/main @ c9cfb9cf`. Execution branches fresh from `origin/main`; the
local `secrets-pocket` checkout and its dirty `plans/` files belong to other work and are not
touched. Owner rulings 1–4 (below) are ratified and are not relitigated here.

## Context

`examples/README.md` is the host contract (H0–H10, ratified 2026-08-27 as PR-A, #10). Its
executable floor — `gopernicus guard` — was "next PR" (PR-B, `NOTES.md` :2768–2773) and was
never built: `workshop/gopernicus/internal/commands/commands.go` :31–45 dispatches `version`,
`init`, `new`, `db` only, and `workshop/gopernicus/v0.3.0` (tagged 2026-09-11 for the
framework audit) does not carry the verb though `examples/README.md` :412 and
`RELEASING.md` :1753–1757 say it does. Meanwhile the reference host (gps-360-go) retired
`internal/integrations` (its `plans/old/31-domain-owned-table-ports.md`, "`internal/integrations`
GONE Aug 28 2026") and put host tech adapters at top-level `integrations/<capability>/<vendor>`
(`plans/old/32-root-integrations-emailer-s3-gcs.md`, Makefile `guard-integrations` :570–575 and
`guard-no-vendor-libs` :603–626). Issue #90 lists the resulting disagreements, the example hosts
that break the contract they are labelled as conforming to, and the stale doc claims.

Owner rulings (ratified, in the issue thread and the dispatch for this plan):

1. **Adopt the reference shape.** Host tech adapters live at top-level
   `integrations/<capability>/<vendor>`; they import stdlib + framework `sdk` + their one
   connector; they return sdk/stdlib types, never domain types; **only `cmd` names them**;
   nothing under `internal/` or `pockets/` imports them; a consumer declares its own narrow
   port that the integration satisfies structurally. Add a sanctioned host-local top-level
   `sdk/` (stdlib + framework `sdk` only). Add the vendor-libs fence as a rule. Rewrite
   H0/H4/H5/H7/H9, §2, §6, `ARCHITECTURE.md` :541–545, and the docs-site page.
2. **Ship `gopernicus guard` in this milestone**, against the rewritten contract (contract
   text first, then the guard), with `--list`, a Makefile/CI hook over the example hosts, and
   a new `workshop/gopernicus` tag (owner-executed, RELEASING.md discipline). Fix the tense
   drift (:44, :412, :474) to match reality at each PR boundary.
3. **Sanction `cmd/<bin>` client binaries** (H10 carve-out): a client binary may own its
   commands, API client, and wire types under `cmd/<bin>`; still no SQL literals.
4. **Turso carve-out** in the "exactly one external dependency" rule text (`ARCHITECTURE.md`
   :327, :411) naming libsql + `modernc.org/sqlite` with the :19 reason. Docs only.

## Goal

After the train lands, every sentence in `examples/README.md` is true of the tree: the rules
match the reference host, `examples/{cms,minimal,jobs-minimal,auth-cms}` pass
`gopernicus guard` with zero findings under `make guard` in CI, and
`go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.4.0 guard --list` prints the
contract's canonical sentences from a cold module cache.

## Definition of Done

- `examples/README.md`, `ARCHITECTURE.md` (:13–60 tree + count, :327/:411 carve-out,
  :541–560), `README.md` (:11, :121), `RELEASING.md` (:1753–1767) and
  `workshop/documentation/docs/architecture/host-contract.md` agree with each other and with
  the reference on every rule; no production doc still says `internal/integrations`.
- The eight out-of-shape example packages (issue §3) are at their contract homes;
  `supervisor.go:19` declares no interface; every example server still boots and serves the
  pages/endpoints named in the run-and-look notes.
- `go run ./workshop/gopernicus guard <host>` exists, exits 0/1/2 as :426–429 says, prints a
  status line per rule, has a negative fixture per rule, and `--list` matches the §3 table
  by test.
- `make guard` runs the verb over the guarded examples (new G30) and `make check` is green on
  CI for each PR of the train.
- `workshop/gopernicus/v0.4.0` tagged at the PR-3 merge commit, proxy `.info` polled, then
  cold-verified (`GOWORK=off`, empty `GOMODCACHE`, `proxy.golang.org`, `sum.golang.org`) —
  owner-executed; release record appended to `RELEASING.md` and the plan copied to `plans/`.

## Out of scope

- Any change to framework module APIs (sdk, pockets, integrations, ui). No tag other than
  `workshop/gopernicus/v0.4.0`.
- Splitting `integrations/datastores/turso/localfile` into its own module (ruling 4 is the
  carve-out, not the split).
- `gopernicus init` emitting the `tool` pin + `guard:` target skeleton (PR-B's second half).
  Deferred to a follow-up after v0.4.0 is on the proxy — see YOUR CALL 7.
- Re-architecting `examples/auth-cms`'s composition root beyond the H10 floor (its
  `purge.go`, `membership.go`, `product.go` provider behavior is the reviewer-enforced clause,
  :396–400; it stays "a harness, not a model", :477–489).
- Changing the reference host, venona-project, or Segovia. They get a handoff section.
- `make guard` running the host guard over a host *outside* this repo (hosts pin the tool;
  RELEASING.md :1753–1767).

## Schema / datastore impact

None. No SQL, no migrations, no store adapter changes. The moved packages are in-memory
stores and view overrides; their storetest conformance suites move with them unchanged.

## Module / API impact

- No module is added or removed; `go.work` and the Makefile `MODULES` list (44) are unchanged.
- Example modules (`examples/auth-cms`, `examples/cms`, `examples/minimal`, and
  `examples/goth-showcase` if YOUR CALL 2 says move) change *internal* import paths only. They
  are hosts, never tagged (RELEASING.md "the four `examples/*` hosts … never tagged").
- `workshop/gopernicus` gains a verb: **MINOR**, `v0.4.0`. Its `go.mod` keeps zero `require`
  lines (`go/parser`, `go/ast`, `go/token` are stdlib). G11 (`guard-workshop-boundary`) holds:
  the verb imports no pocket/example; the Makefile hook `go run`s it, nothing imports it.
- `--list` output becomes a versioned surface: a wording change to a canonical sentence is a
  change to `examples/README.md` and to the CLI in the same tag (RELEASING.md :1762–1764).

## Generated-artifact impact

None. No `.templ` source changes; `examples/goth-showcase/internal/showcase` contains hand-written
Go that *calls* templ components from `ui/goth` and has no `*_templ.go` of its own
(`git ls-tree origin/main examples/goth-showcase/internal/showcase` shows only `.go` files).
`make check`'s templ-drift gate is unaffected.

## The rewritten contract — what PR-1 writes

This section is the design the implementer transcribes into `examples/README.md`. Canonical
sentences are final wording; prose around them may be edited for flow.

### Roots and shape (H0, §2)

```
<host>/
  cmd/<binary>/                 composition root — wiring, behavior-free (H10); a CLIENT BINARY may own more (H10 carve-out)
  sdk/                          OPTIONAL host-local vocabulary: stdlib + framework sdk only (H12)
  integrations/<capability>/<vendor>/   OPTIONAL host tech adapters, named only by cmd (H5, H11)
  internal/
    logic/     domains/<d>  compositions/<c>
    inbound/   domains/<d>  compositions/<c>  middleware/  wire/  views/
    outbound/  domains/<d>
  pockets/<name>/{logic,inbound,outbound}   host pockets (§5) — any non-empty subset
  ui/                           OPTIONAL host presentation root — YOUR CALL 2
  workshop/                     developer-time tools (shape-exempt)
```

- `internal/` holds **only** `logic`, `inbound`, `outbound`. The `internal/integrations` slot
  is retired; the reference deleted it (plans/old/31) and the contract follows (ruling 1).
- `integrations/` is keyed `<capability>/<vendor>` exactly like the framework's
  (`ARCHITECTURE.md` :13–27). Always write the prefix in prose: bare `integrations/…` in a
  host document means the host's; `github.com/gopernicus/gopernicus/integrations/…` means the
  framework's. (The current :379–380 says the opposite for `internal/integrations`; that
  sentence goes.)
- `sdk/` is the home for repo-local vocabulary shared by integrations and outbound adapters
  (venona-project's need, issue §1). Its layering inside (`pkg/`, `capabilities/`) is a host
  choice; the contract fixes only its imports (H12).

### Canonical sentences (the `--list` output; §3 table)

| id | the rule, in one line |
|---|---|
| **H0** | A host's production Go packages live only under `cmd/`, `sdk/`, `integrations/`, `internal/{logic,inbound,outbound}`, `pockets/<name>/{logic,inbound,outbound}`, `ui/`¹, or `workshop/`, and at least one package sits under `cmd/<binary>/`. |
| **H1** | `internal/logic/**` imports only the standard library, framework `sdk/...`, the host's own `sdk/...`, and its own `internal/logic/...` — an allow-list, with no escape valve. |
| **H2** | unchanged (:106) |
| **H3** | unchanged (:107) |
| **H4** | `internal/inbound/**` may reach `internal/logic`, framework and host `sdk`, framework pocket cores and their `views/<pkg>`, `ui/*`, and a host pocket's `logic` and `inbound` — never `internal/outbound`, the host's `integrations/`, or a host pocket's `outbound`. |
| **H5** | `internal/outbound/domains/<d>` imports, among host packages, only `internal/logic/domains/<d>`, the host's `sdk/`, and host pockets — never inbound, never another logic domain, never the host's `integrations/` (it declares the narrow port; `cmd` injects the integration). |
| **H6** | as :110, with one addition: a host pocket's `logic` may also import the host's `sdk/...`. |
| **H7** | Only `cmd/**`, `internal/outbound/**`, the host's `integrations/**`, a host pocket's `outbound/**`, and `workshop/**` may import a framework pocket's `stores/*`, a framework integration, or a recognized database driver. |
| **H8** | unchanged (:112) |
| **H9** | `.Underlying()` is called nowhere outside the host's `integrations/` and `workshop/`, and `RowToStructByNameLax` appears nowhere. |
| **H10** | In production files, `cmd/**` contains no SQL statement literal, and a composition root under `cmd/<binary>/` declares no interface type; a client binary — a `cmd/<binary>/` tree that imports nothing under `internal/`, `pockets/`, or the host's `integrations/`, and no framework pocket, store, integration, or database driver — may own its commands, API client, wire types², and interfaces. |
| **H11** | A host integration `integrations/<capability>[/<vendor>]` holds no domain: it imports only the standard library, framework `sdk/...`, the host's `sdk/...`, and exactly one connector or vendor client library plus the companion modules its API forces; it never imports `internal/*` or a pocket of either scope, and only `cmd/**` and `workshop/**` import it — tests included. |
| **H12** | The outer fence, which H1, H4, H6 and H7 tighten and never loosen: `internal/**` and `pockets/**`, tests included, import only the standard library, the host module, and the framework's `sdk`, `pockets`, `integrations` and `ui` modules; recognized database drivers are additionally allowed under `internal/outbound/**` and a host pocket's `outbound/**`, recognized view technology under `internal/inbound/**` and a host pocket's `inbound/**`; production files never import the host's `workshop/`. |
| **H13** | The host's `sdk/**` imports only the standard library, framework `sdk/...`, and its own `sdk/...`, and holds no domain entity, repository port, or `Service` — a type two domains both need is still each domain's own. |
| **H14**³ | The host's `ui/**` imports only the standard library, framework `sdk/...`, framework `ui/*`, recognized view technology, the host's `sdk/...`, and its own `ui/...` — never `internal/*`, a pocket of either scope, or an integration of either scope. |

¹ `ui/` appears only if YOUR CALL 2 sanctions it. ² "wire types" = the client's request/response
structs; the server's HTTP+JSON conventions stay in `internal/inbound/wire` (:60, :231).
³ H14 exists only if YOUR CALL 2 sanctions `ui/`; the series is then H0–H14, **fifteen**
sentences (fourteen without it). Task-1 sets the count once the call is made.

Prefix discipline in every sentence and paragraph: bare `sdk` always means the framework's
module; the host's is always written "the host's `sdk/`" (the steward's note — the README's
existing rule for `integrations/`, :379–380, extended). ARCHITECTURE.md's five-point sdk
admission test governs *framework* `sdk` only; a host `sdk/` is H1's "small util outside logic"
(:125), named.

Recognized lists (rule text and implementation list the same names, the :132 precedent). There
is **one** driver list shared by H3 item 2, H7 and H12 — today H3 (:156–157) names
`github.com/jackc/pgx`, `github.com/tursodatabase/`, `database/sql` while the reference fence
names only pgx; the consultation caught that a Turso-backed host's `internal/outbound` would
fail one rule or the other:

- **database drivers:** `github.com/jackc/pgx/v5`, `github.com/tursodatabase/libsql-client-go`,
  `modernc.org/sqlite`, `github.com/lib/pq`. `database/sql` is stdlib and is dropped from H3's
  list (a composition importing it is still caught by "no storage import" through the driver it
  would need). YOUR CALL 4 is whether the H12 allowance under the outbound slots is this whole
  list (recommended) or pgx alone (the reference's literal allow-list).
- **view technology:** `github.com/a-h/templ` (the one view library `ui/goth` pins).
- **Not stdlib:** `golang.org/x/...` is third-party under the "first path element has no dot"
  rule, so `x/sync/errgroup`, `go-cmp`, testcontainers under `internal/**` are H12 findings,
  tests included — deliberately (the reference's fence behaves the same, :616–618). The rule
  text says so, and names the homes: a test that needs a real integration lives in the
  integration package's own tests, in `cmd/**` `_test.go`, or under `workshop/`.

The current §"The import law" table (:123–133) is rewritten row by row to the above; the
`internal/integrations` row (:130) becomes the `integrations/<capability>/<vendor>` row (H11);
H3 item 2's storage-import list (:154–157) swaps `internal/integrations/` for the host's
`integrations/`; H9 prose (:190–195) follows.

### §6 — `cmd`, `workshop`, `integrations`, `sdk`

- **`integrations/<capability>[/<vendor>]/`** replaces the :376–384 paragraph. The optional
  `<vendor>` segment follows the reference, whose tree is not uniformly two-level
  (`integrations/emailer`, `fec`, `quickbooks` sit at depth one beside
  `filestorage/{s3,gcs}`). Content: what a host integration is (a GPS-mail seam over the
  SendGrid connector; an S3 store with the host's prefix policy; a vendor client with no
  upstream module); its imports (H11); where its real-backend tests live (its own package,
  `cmd/**` tests, or `workshop/`, never `internal/outbound` tests — H12); that it
  **returns sdk/stdlib/host-sdk types and never a domain type** — reviewer-enforced, with the
  structural floor being "it imports no `internal/logic`"; that the **consumer declares its own
  narrow port** (an outbound adapter or a logic domain) and `cmd` injects the integration,
  which satisfies it structurally — the reference's `cmd/server/{email,filestorage}.go`
  (gps-360-go README :264); and the §4 "`internal/outbound` vs `integrations`" paragraph
  (:286–291) is cross-referenced, not restated. The "an integration that imports two logic
  domains" sentence (:381–384) keeps its point with the new path.
- **`sdk/`** — one paragraph: repo-local vocabulary shared by integrations and outbound
  adapters; H13; a host that has nothing to share has no `sdk/`; "it may later move up into the
  framework or stay local" is a host decision.
- **`cmd/`** (:386–400) gains the client-binary carve-out: the machine-checkable
  classification (no inward import of any kind → client binary), what it may own
  (commands, client, wire types, interfaces — the reference's `cmd/gps360/{cli,client,exit,read,write}`),
  what it may not (SQL literals; any import of `internal/`, `pockets/`, framework stores/
  integrations/datastores or a driver — the reference's `guard-cli-no-inward-imports`
  :799–808). A composition root keeps the no-interface clause.
- **`workshop/`** (:402–407): exempt from H5, H7, H11's "only cmd" clause (a `workshop/mailcheck`
  legitimately names an integration), and H0's internal shape; H9 and H12 still apply.

### §7, §8 and the tense

- §7 :411–413: "The `gopernicus guard` verb ships in `workshop/gopernicus` **v0.4.0**" —
  PR-1 says "is planned for v0.4.0 (plan of record: this file)"; PR-3 makes it present tense;
  :422 `require … v0.3.0` → `v0.4.0`; :452 `@v0.3.1` → `@<next>`.
- :44–45 "`gopernicus guard --list` prints these eleven sentences" — PR-1: "will print these
  fifteen sentences" (fourteen if YOUR CALL 2 declines); PR-3: "prints". Every "H0–H10" and
  "eleven" in prose (:42, :45, :98, docs page :3/:23) moves with it — task-1's verify greps
  for both tokens because the `--list` drift test cannot see prose.
- :474–475 — PR-1: "The first four run under this repo's own `make guard` once the guard verb
  ships"; PR-3: "run under … `make guard` (G30)". §8 table (:456–462): `auth-cms` row becomes
  "conforming to the H floor since PR-2; still the authentication conformance harness, not a
  layout reference (its `cmd/` carries provider behavior the reviewer clause covers)" if
  YOUR CALL 3 says guard it; `goth-showcase` per YOUR CALL 2.

### `ARCHITECTURE.md`

- :13–60 tree: add `secrets/` and `secrets/stores/pgx` under `pockets/` (module lines
  `github.com/gopernicus/gopernicus/pockets/secrets` — "owner-scoped encrypted secrets, rotation,
  audit; datastore-free" — and `…/pockets/secrets/stores/pgx`), and :60 "**Forty-two modules
  today.**" → "**Forty-four**". `README.md` :11 and :121 likewise.
- :51–55 example trees: PR-2 rewrites to the post-move layout (`cmd/  pockets/cms/inbound/theme
  workshop/migrations`; `cmd/  internal/{logic,inbound,outbound}/domains/catalog  pockets/cms/outbound/memstore`;
  `cmd/  internal/…/documents  pockets/{access,authentication,cms,events}`).
- :327 (taxonomy row) and :411–413 (prose): after "exactly one external dependency" add the
  named carve-out: "`integrations/datastores/turso` additionally requires `modernc.org/sqlite`
  for its opt-in `localfile` package, because libsql's `file:` mode delegates to a registered
  SQLite driver (:19); it is a companion the vendor's API forces on the caller, not a second
  backend — the same isolation-unit clause that admits `google.golang.org/api/option` beside
  the Firestore client". Docs only (ruling 4).
- :541–545 (H4/H5/H5/H7 summary rows) → the new H4/H5/H7/H11 rows; :556–560 pointer text:
  "`cmd`/`workshop`/`integrations`/`sdk`, and the retrofit recipe … `gopernicus guard` is its
  executable floor" (true after PR-3; PR-1 writes "will be").

### Docs site

`workshop/documentation/docs/architecture/host-contract.md` :3 ("the eleven H-rules"), :10
(`cmd`/`workshop`/`internal/integrations`), :25 ("planned but has not shipped" → PR-3: shipped
in v0.4.0), :27–39 table → the fifteen (or fourteen) sentences. `docs/examples.md` :20 wording per §8.
`docs/workshop/commands.md` gains `## guard` (PR-3); `docs/workshop/overview.md` :8 gains the
verb in its one-line summary.

## Package homes — what PR-2 moves

Each move is an import-path rename plus the doc references listed; package names are kept so
`cmd/` code changes are import lines only. Evidence is the package header on `origin/main`.

| today | home | why (contract clause) |
|---|---|---|
| `examples/auth-cms/internal/authjobs` | `examples/auth-cms/pockets/authentication/outbound/authjobs` | Imports `pockets/authentication/logic/delivery` + `pockets/jobs/logic/queue` ("the ONE place that imports BOTH pockets", `authjobs.go` :1–26). An adapter satisfying one pocket's port using another pocket is a host pocket's **outbound** (:180–183, :357–360 "the canonical case"). Sibling of the existing `pockets/authentication/outbound/oauth2fixture`. Issue §4. |
| `examples/auth-cms/internal/authmem` | `examples/auth-cms/pockets/authentication/outbound/authmem` | In-memory implementation of the auth pocket's repository ports (`authmem.go` :1–9) → host pocket outbound (:332–335). |
| `examples/auth-cms/internal/authpages` | `examples/auth-cms/pockets/authentication/inbound/authpages` | Embeds `pockets/authentication/views/goth` and overrides `Login` + an email template (`authpages.go` :1–23) → "pure presentation over a pocket … is the host pocket's **inbound**" (:329–331). It sits beside, not under, the existing `inbound/http/oauth2demo`: `http/` mirrors the pocket's own `inbound/http` transport tree, and a views override is not transport. |
| `examples/auth-cms/internal/deliveryhealth` | `examples/auth-cms/pockets/authentication/outbound/deliveryhealth` | Decorates the pocket's `delivery.Dispatcher` and the sdk `events.Emitter` the pocket calls (`deliveryhealth.go`: `Dispatcher(next)`, `Emitter(next)`, `countingDispatcher`, `countingEmitter`) — driven-side adapters of pocket ports; its `Handler()` is a JSON projection `cmd` mounts at `/healthz/delivery` (main.go :584). Outbound, with the handler as the noted impurity; splitting it out would create an inbound→outbound edge inside the pocket (both consultants agreed). Its tests import `jobs/stores/memory` / `authentication/stores/storetest` — legal only under an `outbound` slot (H7), another reason neither this nor `authjobs`/`authmem` may land under `inbound`. |
| `examples/auth-cms/internal/memstore` | `examples/auth-cms/pockets/cms/outbound/memstore` | In-memory cms repositories (`memstore.go` :1–11); the contract names this exact path (:334). Keep the "verbatim copy of minimal's" note, with the new source path. |
| `examples/auth-cms/internal/outboxmem` | `examples/auth-cms/pockets/events/outbound/outboxmem` | In-memory `outbox.EntryRepository` for `pockets/events` (`outboxmem.go` :1–15) → host pocket outbound. |
| `examples/cms/internal/theme` | `examples/cms/pockets/cms/inbound/theme` | Embeds `pockets/cms/views/goth`, overrides four chrome methods (`theme.go` :1–13); the contract names this exact path (:331) and :283 calls it the live override. |
| `examples/minimal/internal/memstore` | `examples/minimal/pockets/cms/outbound/memstore` | Same as auth-cms's copy; :334 names the path. |
| `examples/goth-showcase/internal/showcase` | `examples/goth-showcase/ui/showcase` **or** stays a named exemption | Imports `github.com/a-h/templ`, `ui/goth`, `ui/goth/assets`, `sdk/pkg/web`; no pocket, no logic (`showcase.go` :1–27). It fits no `internal/` slot (H8 would demand a logic counterpart; `inbound/views/` is the shared kit, not a route-registering harness). YOUR CALL 2. |
| `examples/auth-cms/cmd/server/supervisor.go` (`deliveryHealthMarker` :19, `deliverySupervisor`, `superviseDelivery`) + `supervisor_test.go` | `examples/auth-cms/pockets/authentication/outbound/deliveryhealth/supervisor.go` + `supervisor_test.go` | H10 (:114, :386–390) and its normative clause (:391–394: lifecycle supervision is provider behavior, not wiring). Moving the supervisor into the package it brackets lets the marker interface become an unexported seam there and the `markerSpy` test (`supervisor_test.go` :13) survive byte-for-byte; `cmd` keeps one call, `deliveryhealth.Supervise(deliveryCtx, cancelHost, run, health, log)`, and the `wait(within)` call at shutdown. Exporting the interface from `deliveryhealth` instead was rejected: the producer would be declaring the consumer's port. |

After these moves, `examples/auth-cms` passes the H **floor** (checked by hand against
`origin/main` imports, and re-checked by the lead-backend pass): `internal/{logic,inbound,outbound}/domains/documents`
only (H0); logic imports stdlib + `sdk` (H1); inbound imports logic, `sdk`,
`pockets/authorization/logic/*` — framework cores (H4); outbound imports logic,
`integrations/datastores/pgxdb`, `pgx/v5` (H5, H7, H12); its outbound *tests* import
`internal/inbound/domains/documents` and `workshop/migrations/documents` — legal only through
H5's `_test.go` exemption (:129), which the new text keeps; all four host pockets carry
framework rim imports and import no `internal/*` (H6); no `.Underlying()`/`RowToStructByNameLax`
anywhere under `examples/` (H9); production `cmd/` has one interface (moved) and no SQL literal
(H10 — `jobs_delivery_live_{pgx,turso}_test.go` :36/:37 carry `TRUNCATE`/`DELETE FROM` behind
`//go:build livedelivery`, which is why H10 is production-files-only). The floor is not the
clause: `cmd/server/{demo,purge,membership,product,oauthfake}.go` are provider behavior, and
the PR text records a green G30 there as named debt, not H10 compliance. `examples/minimal`
and `examples/cms` conform in substance. `examples/jobs-minimal` is `cmd/` only and already
conforms.

## Guard design — what PR-3 builds

**Placement.** `workshop/gopernicus/internal/commands/guard.go` (dispatch + flags + output),
`guard_rules.go` (the H table: id, canonical sentence, evaluator), `guard_walk.go` (module
discovery, package classification, parsing), `guard_test.go` (exit codes, `--list`, drift,
fixtures). "Each command owns its own file" (`commands.go` :1–4) and the existing boundary
checks already live in this package (`integration_boundaries_test.go`,
`pocket_boundaries_test.go`), so no new directory is invented.

**Invocation.** `gopernicus guard [host-root]` (default `.`), `gopernicus guard --list`.
No `--skip`, no allow file, no config (:431–434). Stdlib only (NOTES.md :2769: "`go/ast`, no
grep, no shell"): `go/parser` + `go/ast` + `go/token`; the module path is the first
`module ` line of `<root>/go.mod` read with `bufio` (no `go mod edit -json` exec — the
test-only `integration_boundaries_test.go` shells out, the verb does not). No `go/packages`:
it is `golang.org/x/tools`, and the module's `go.mod` must keep zero `require` lines.

**Module path.** The first `module ` line of `<root>/go.mod`: strip a trailing `//` comment,
unquote if quoted (`module "x"` is legal), tolerate CRLF. Missing or malformed → exit 2.

**Walk.** `filepath.WalkDir` from the root (symlinks are not followed — documented); skip
`.git`, `vendor`, `node_modules`, `testdata` **at any depth** (as the Go tool does, so
`cmd/<bin>/testdata/*.go` is never a package), names starting with `.` or `_`, and any
directory other than the root that contains a `go.mod` (a nested module is a separate host run
— reported once as an informational line, not a finding). A *package* is a directory with ≥1
`.go` file **not** carrying `//go:build ignore`; a directory holding only `_test.go` files is a
package for every rule that says "tests included" and out of scope for the rest; a *production
file* is one not ending `_test.go`. Build constraints are otherwise ignored on purpose
(`//go:build integration`, `livedelivery` files are parsed — imports are imports regardless of
tag; `go/parser` does not evaluate constraints and the guard does not pretend to).
`import "C"` is stdlib. Files are parsed with `parser.ImportsOnly` for the import rules;
`cmd/**`, `internal/logic/compositions/**`, and every package for H9 get a full parse
(`parser.SkipObjectResolution`).

**Classification — host first, then an enumerated framework list, never the bare prefix.**
This repo's hosts are `github.com/gopernicus/gopernicus/examples/<h>` — *under* the framework
prefix — so an import is classified by (1) the host module path (longest match) into the
families below, then (2) the enumerated framework modules `…/sdk`, `…/integrations/<cat>/<tech>`,
`…/pockets` (the root contract package — a framework pocket core for H4/H6 purposes;
`examples/minimal/internal/inbound/domains/catalog/routes.go` and `oauth2demo` import it),
`…/pockets/<p>` core vs `…/pockets/<p>/stores/*` vs `…/pockets/<p>/views/*`, `…/ui/*`; then
(3) `…/workshop/*` and `…/examples/*` are **third-party for every rule** (an example importing
another example or the CLI is a finding, never silently framework); then (4) stdlib = first
path element has no dot (the reference awk :616, Go's own convention); (5) everything else is
third-party, matched against the two recognized lists. Directory → family: `cmd/<bin>[/…]`,
`sdk/…`, `integrations/<cap>[/<vendor>][/…]`, `internal/logic/domains/<d>`,
`internal/logic/compositions/<c>`, `internal/inbound/{domains/<d>,compositions/<c>,middleware,wire,views}[/…]`,
`internal/outbound/domains/<d>[/…]`, `pockets/<n>/{logic,inbound,outbound}[/…]`, `ui/…`,
`workshop/…`, or *out of shape* (an H0 finding carrying the suggested home: "`internal/<x>`
that imports a framework pocket rim → `pockets/<p>/{inbound,outbound}`; otherwise
`internal/{logic,inbound,outbound}/domains/<x>`", :87–88).

**G11 trap.** `guard-workshop-boundary` (Makefile :279) greps `workshop/` for the literal
`"github.com/gopernicus/gopernicus/(pockets|examples)` *including the opening quote*. The
guard's own classification constants must therefore be assembled (`frameworkModule + "/pockets"`
from `scaffold.go`'s `baseModule` :15), never written as one quoted literal — the same rule the
fixtures already follow.

**Rule evaluators (one per H id; each returns `checked int, findings []finding`).**
H0 shape + the positive `cmd/<binary>` check; H1/H2 allow-lists; H3 the five AST checks
(:150–173: `Repository`-suffixed type names through pointers/slices/maps/funcs/variadics/
channels/generics/selectors; storage imports from the shared driver list; the `Dependencies`
struct whose fields are same-package `*Service` interfaces, non-empty, not aliases; ≥1 domain
import; no `Transactor`/`Tx`); H4–H7 import rules per family with the `_test.go` exemptions the
table names (H4's integrations clause, H5's whole row, :128–129 — kept verbatim so auth-cms's
outbound tests stay legal); H8 directory mirror; H9 `CallExpr` selector `Underlying` +
identifier `RowToStructByNameLax` (:192–195); H10 — production files only — `*ast.InterfaceType`
in a `TypeSpec` of a composition root (a `cmd/<bin>` tree with any inward import), and SQL
literals = `*ast.BasicLit` STRING whose trimmed upper-cased prefix is one of the seven verbs
(:389–390) in every `cmd/**` production file; H11 integration imports + "who imports it";
H12 the fence incl. the no-`workshop/` clause; H13 host sdk imports (its "no domain entity/port/
Service" clause is reviewer-enforced, no AST); H14 host ui (if sanctioned). A rule with nothing
in scope reports `nothing to check` and counts as green (:426–428).

**Output.** One status line per rule in id order — `H5   ok        (6 packages)`,
`H3   no scope  (no internal/logic/compositions)`, `H0   2 findings` — then one line per
finding `H0  internal/theme: package outside the contract's roots — home: pockets/cms/inbound/theme`
or `H5  internal/outbound/domains/x/y.go:14: imports <path> (host integrations are named only by cmd)`,
then `guard: N findings across M rules` or `guard: clean (15 rules, K packages)`. Exit 0 clean,
1 findings, 2 for a missing/unreadable `go.mod`, an unparsable file, or bad flags (:429).
`--list` prints `H0  <sentence>` … `H13 <sentence>`, exit 0.

**Tests.**
- `TestGuardListMatchesContract`: reads `examples/README.md` from `repoRoot(t)`
  (`scaffold_test.go` :220), extracts the `| **Hn** | … |` rows of the §3 table, and asserts
  byte equality with the embedded sentences — this is what makes :45 ("cannot drift") true.
- `TestGuardRuleFixtures`: table-driven; for each rule a clean fixture and ≥1 violating
  fixture, written to `t.TempDir()` as `go.mod` + files; forbidden tokens assembled by
  concatenation (`scaffold_test.go` :34–36 precedent) so repo-wide greps G9/G10 never see
  `.Underlying()` or `RowToStructByNameLax` in source. Negative fixtures: an `internal/foo`
  package (H0); `internal/logic` importing `internal/outbound` (H1); domain→domain (H2); a
  composition with a `Repository` field, with a `Transactor` param, and with an aliased
  `Dependencies` field (H3 ×3); inbound importing outbound and importing host `integrations/`
  (H4); outbound importing another domain and importing host `integrations/` (H5); a host
  pocket importing `internal/logic` and one with no framework rim import (H6); `internal/inbound`
  importing a framework `stores/pgx` (H7); `internal/inbound/domains/x` with no logic twin (H8);
  `.Underlying()` in outbound and `RowToStructByNameLax` in workshop (H9); an interface in a
  composition root, an interface in a client binary (clean), `"SELECT 1"` in a client binary
  (H10); an integration importing `internal/logic`, and `internal/outbound` importing the
  integration (H11); `golang.org/x/crypto` in outbound, `a-h/templ` in outbound (finding) vs in
  `internal/inbound/views` (clean), `pgx/v5` in `internal/inbound` (finding) vs in
  `internal/outbound/domains/x` (clean), `internal/inbound` importing the host's `workshop/`
  (finding) (H12); host `sdk/` importing `internal/logic` (H13); host `ui/` importing
  `internal/inbound` (finding) vs importing `…/ui/goth` + templ (clean) (H14, if sanctioned);
  and two classifier cases: an example importing another `…/examples/<h>` package (finding
  under H12, not silently framework) and `internal/inbound` importing the root `…/pockets`
  package (clean under H4).
- `TestGuardExitCodes`: no `go.mod` → 2; clean → 0; one finding → 1; `--list` → 0; unknown flag → 2.
- `TestGuardNothingToCheckIsReported`: a `cmd/`-only host prints `no scope` for H1–H9 and
  still passes H0's positive check.

**Makefile hook (G30, `guard-host-contract`).** Added to the `guard:` prerequisite list and the
`.PHONY` line; comment block in the Makefile's house style naming the plan; it follows the
existing `cd workshop/gopernicus && go …` pattern (G13, :190) rather than a root `go run`, so
it does not lean on `go.work` resolution:

```make
# G30 (host-contract-reconciliation, 2026-10-20): the in-repo hosts pass the host
# contract's executable floor. examples/* never pin the tool (G11); the verb is
# run from this checkout. goth-showcase: see the plan's YOUR CALL 2.
guard-host-contract:
	@echo "== guard: example hosts pass gopernicus guard (H0–H14) (G30) =="
	@cd workshop/gopernicus && for h in cms minimal jobs-minimal auth-cms; do \
		echo "-- examples/$$h"; go run . guard ../../examples/$$h || exit 1; done
```

`make check` already ends with `$(MAKE) guard` and CI is `make check` (`check.yml` :60), so
no workflow change. `check.yml`'s header comment ":2 the twenty-three layering guards" is
stale already (Makefile `guard:` lists twenty-eight); PR-3 corrects it to the real count rather
than adding one to a wrong number.

## PR train

Three stacked PRs, then an owner-executed tag. Cut rationale: PR-1 is pure text and can merge
on its own day; PR-2 is Go moves inside example modules and must land before any guard can
run green over them; PR-3 is the verb plus the hook that *depends on PR-2's tree*. The
`go.work` CI rule ("never a core-only stacked PR" — CI runs `make check` over the whole
workspace, so a PR that changes a module's API without the dependents it forces is red) does
not bite here: no module API changes. It does fix the order — PR-3 opened against `main`
without PR-2 fails G30 on three hosts, so PR-3 is stacked on PR-2. PR-2 may be opened against
PR-1's branch (review order) or `main` (it is technically independent); recommend stacking so
the contract text is merged before the moves that cite it. Never `--delete-branch` a stacked
base (recorded lesson).

| PR | branch | contents | tags |
|---|---|---|---|
| PR-1 | `docs/host-contract-rewrite` ← `main` | tasks 1–4 | none |
| PR-2 | `examples/host-contract-conformance` ← PR-1 | tasks 5–9 | none |
| PR-3 | `workshop/guard-verb` ← PR-2 | tasks 10–14 | none (tag after merge) |
| release | owner, on `main` | task 15 | `workshop/gopernicus/v0.4.0` |
| PR-4 | `docs/guard-verb-release` ← `main` | task 16 | none |

## Tasks

### task-1: Rewrite the host contract to the reference shape

- **depends_on:** []
- **model:** fable
- **files:** [`examples/README.md`]
- **verify:** `grep -n "internal/integrations\|H0–H10\|eleven" examples/README.md` → no hits; `grep -c '^| \*\*H[0-9]*\*\* |' examples/README.md` → 15 (or 14) rows in §3 (the import-law table rows carry the id in bold too — assert on the §3 block); `grep -n "database/sql" examples/README.md` → only in a "not a driver" sentence; `make guard` (no code change; proves nothing else moved)
- **description:** Transcribe "The rewritten contract" above into `examples/README.md`: §2 tree and shape bullets (:49–94), the §3 canonical table (:102–114) and import-law table (:123–133), H3 item 2 (:154–157), H9 (:188–195), §6 (:374–407, retitled "`cmd`, `workshop`, `integrations`, and `sdk`"), §7 tense/version (:411–413, :422, :452), §8 (:456–462, :474–475). Keep §1, §4 (except the :283 path, which PR-2 changes), §5 verbatim. Record the reference findings from YOUR CALL 2/5 (`ui/`, `manual/`, `harness/`, `pockets/auth/views`) in a short "Reference findings" list at the end of §8 so "where this document and the reference disagree, the disagreement is a finding" (:14–16) has a ledger.

### task-2: ARCHITECTURE.md — tree, count, turso carve-out, H summary

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`ARCHITECTURE.md`, `README.md`]
- **verify:** `grep -n "Forty-two\|forty-two" ARCHITECTURE.md README.md` → none; `grep -n "pockets/secrets" ARCHITECTURE.md` → two tree lines; `grep -n "internal/integrations" ARCHITECTURE.md` → none; `make guard`
- **description:** Add `secrets/` + `secrets/stores/pgx` to the :13–60 tree; :60 and `README.md` :11/:121 → forty-four; add the named turso carve-out at :327 and :411–413 (ruling 4 wording above); replace :541–545 with the new H4/H5/H7/H11 rows and :556–560's pointer text. Leave :51–55 (example trees) for task-9.

### task-3: Docs site + RELEASING LIVING paragraph

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`workshop/documentation/docs/architecture/host-contract.md`, `workshop/documentation/docs/examples.md`, `RELEASING.md`]
- **verify:** `grep -rn "internal/integrations\|eleven" workshop/documentation/docs/architecture/host-contract.md` → none; `grep -n "from \*\*v0.3.0\*\* it is also" RELEASING.md` → none; if pnpm is installed, `make docs-build` (Node-gated; otherwise note "docs build unverified locally, CI deploy-docs builds on merge to main")
- **description:** Bring the docs-site page to fifteen (or fourteen) rules with the new wording and "planned for v0.4.0"; `examples.md` :20 per §8; `RELEASING.md` :1753–1757 "from **v0.3.0** it carries the `guard` verb" → "from **v0.4.0** (planned; host-contract-reconciliation)". No release entry yet — that is task-16.

### task-4: NOTES.md ratification entry

- **depends_on:** [task-1, task-2, task-3]
- **model:** opus
- **files:** [`NOTES.md`]
- **verify:** `grep -n "^## 2026-10-20 — host-contract-reconciliation" NOTES.md`
- **description:** Append a dated entry in the style of :2725–2773: the four rulings, the rule-id changes (H5/H11 split, H12 fence, H13 host sdk, H10 carve-out), the package homes, the guard-ships-in-v0.4.0 correction to :2768–2773 ("Executable floor, next PR" → this plan; G21 in that text was taken by `guard-list-no-nethttp`, the host guard is G30), and that PR-B's init skeleton half is deferred (YOUR CALL 7).

### task-5: Move auth-cms's six packages to their host pockets

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`examples/auth-cms/pockets/authentication/outbound/authjobs/*`, `examples/auth-cms/pockets/authentication/outbound/authmem/*`, `examples/auth-cms/pockets/authentication/outbound/deliveryhealth/*`, `examples/auth-cms/pockets/authentication/inbound/authpages/*` (incl. `templates/verification.html`), `examples/auth-cms/pockets/cms/outbound/memstore/*`, `examples/auth-cms/pockets/events/outbound/outboxmem/*`, `examples/auth-cms/cmd/server/*.go` (import lines + the comments at main.go :35, :181, :195, :567), `examples/auth-cms/README.md` (:15, :157–158, :249, :274, :299, :556, :655)]
- **verify:** `cd examples/auth-cms && go build ./... && go test ./... && go vet ./...`; `git ls-files examples/auth-cms/internal | grep -v "/domains/"` → empty; `make guard`. Run-and-look: `cd examples/auth-cms && go run ./cmd/server` (port 8082, `.env.example`), then `curl -s localhost:8082/healthz/delivery` → JSON with `"runtime":"running"` (jobs mode) and `GET /auth/login` renders the Gopernicus-CMS-branded login (the `authpages` override, not the stock ui/goth page); `POST /outbox-demo` per README still 2xx.
- **description:** `git mv` each package to the home in the table; update every import line and the in-code comments that cite the old paths; keep package names. The two existing host pockets (`pockets/access/inbound`, `pockets/authentication/{inbound/http/oauth2demo,outbound/oauth2fixture}`) are untouched. Known flake: `TestAdminResendReportsRealTargetState` (fixed in #78; if it recurs, re-run once and say so).

### task-6: Move the delivery supervisor out of auth-cms's composition root

- **depends_on:** [task-5]
- **model:** opus
- **files:** [`examples/auth-cms/pockets/authentication/outbound/deliveryhealth/supervisor.go` (from `cmd/server/supervisor.go`), `…/deliveryhealth/supervisor_test.go` (from `cmd/server/supervisor_test.go`), `examples/auth-cms/cmd/server/main.go` (:615 call site + the shutdown `wait`)]
- **verify:** `grep -n "interface {" examples/auth-cms/cmd/server/*.go | grep -v _test.go` → empty; `cd examples/auth-cms && go test ./pockets/authentication/outbound/deliveryhealth ./cmd/server -run 'Supervis|Delivery' -count=1`; `go vet ./...`
- **description:** `git mv` both files; export `Supervise(deliveryCtx, cancelHost, run, health marker, log) *Supervisor` and `(*Supervisor).Wait(within)`; the marker interface stays unexported inside `deliveryhealth` (H10 governs `cmd/` only) and `markerSpy` is unchanged; `cmd` keeps exactly one call and one `Wait`. The `errDeliveryExitedClean` sentinel moves with the supervisor.

### task-7: Move cms's theme to `pockets/cms/inbound/theme`

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`examples/cms/pockets/cms/inbound/theme/*`, `examples/cms/cmd/server/main.go` (:18), `examples/cms/README.md` (:12, :32)]
- **verify:** `cd examples/cms && go build ./... && go test ./... && go vet ./...`; `make guard`. Run-and-look: `make migrate && make run` (Turso env from `examples/cms/.env.example`; the local-file profile works offline), open `http://localhost:8080/` → the ACME-branded chrome (header/footer) renders and an archive/single page shows the theme, admin pages still the bundled default.
- **description:** `git mv examples/cms/internal/theme examples/cms/pockets/cms/inbound/theme`; one import line; README paths; delete the now-empty `internal/`.

### task-8: Move minimal's memstore to `pockets/cms/outbound/memstore`

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`examples/minimal/pockets/cms/outbound/memstore/*`, `examples/minimal/cmd/server/main.go` (:20), `examples/minimal/cmd/server/catalog_cache_test.go` (:15), `examples/minimal/cmd/server/goth_htmx_proof_test.go` (:12)]
- **verify:** `cd examples/minimal && go build ./... && go test ./... && go vet ./...`; `go list -deps ./cmd/server | grep -c libsql` → 0 (the zero-libsql proof, ARCHITECTURE.md :65–66); `make guard`. Run-and-look: `cd examples/minimal && go run ./cmd/server`, `curl -s localhost:8081/catalog.json` returns the product projection (:464–468) and `/` renders.
- **description:** `git mv`, three import lines, `internal/memstore` gone. `internal/{logic,inbound,outbound}/domains/catalog` stay.

### task-9: Repoint every remaining doc reference to the moved packages

- **depends_on:** [task-5, task-6, task-7, task-8]
- **model:** opus
- **files:** [`ARCHITECTURE.md` (:51–55), `examples/README.md` (:283), `pockets/authentication/README.md` (:1848, :2999), `ui/goth/README.md` (:1202–1203), `workshop/documentation/docs/pockets/cms.md` (:95), `examples/goth-showcase/e2e/README.md` (:12, only if YOUR CALL 2 moves showcase)]
- **verify:** `git grep -nE "internal/(memstore|theme|authmem|authjobs|authpages|outboxmem|deliveryhealth)" -- '*.md' '*.go' '*.tmpl' ':!plans/' ':!NOTES.md' ':!RELEASING.md' ':!.claude/'` → empty; `make check`
- **description:** Path updates only; `plans/`, `NOTES.md`, `RELEASING.md` keep their historical paths (the G20 exclusion precedent).

### task-10: Implement `gopernicus guard`

- **depends_on:** [task-9]
- **model:** opus
- **files:** [`workshop/gopernicus/internal/commands/guard.go`, `workshop/gopernicus/internal/commands/guard_rules.go`, `workshop/gopernicus/internal/commands/guard_walk.go`, `workshop/gopernicus/internal/commands/commands.go` (dispatch case + usage line)]
- **verify:** `cd workshop/gopernicus && go build ./... && go vet ./... && test -z "$(go list -m all | grep -v '^github.com/gopernicus/gopernicus/workshop/gopernicus$')"` (zero requires); `go run . guard --list | wc -l` → 15 (or 14); `make guard-workshop-boundary` (G11 — the framework-pocket string is assembled, never literal); `go run . guard ../../examples/minimal; echo $?` → 0; `go run . guard ../../examples/cms; echo $?` → 0; `go run . guard ../../examples/jobs-minimal; echo $?` → 0; `go run . guard ../../examples/auth-cms; echo $?` → 0; `go run . guard /tmp; echo $?` → 2 (no go.mod); `make guard` (G11 still green). Real-behavior negative check: `cp -r examples/minimal "$SCRATCH/broken"`, add `$SCRATCH/broken/internal/leak/leak.go` importing `…/internal/outbound/domains/catalog` from a `package leak`, and add an import of `…/internal/outbound/domains/catalog` to `internal/logic/domains/catalog/catalog.go`; `go run . guard "$SCRATCH/broken"; echo $?` → 1 with an H0 line naming `internal/leak` and its suggested home and an H1 line naming `catalog.go:<line>`; every other rule still prints a status line.
- **description:** Build the verb per "Guard design": module path from go.mod, the walk/classifier, fifteen evaluators (fourteen without H14), status/finding/summary output, exit codes, `--list`. Canonical sentences are `const`s in `guard_rules.go` and are the single source for `--list` and for each finding's trailer. Keep the output deterministic (sorted paths) so CI logs diff cleanly.

### task-11: Guard tests — fixtures per rule, drift test, exit codes

- **depends_on:** [task-10]
- **model:** opus
- **files:** [`workshop/gopernicus/internal/commands/guard_test.go`, `workshop/gopernicus/internal/commands/commands_test.go` (add `guard` rows to `TestRunExitCodes`)]
- **verify:** `cd workshop/gopernicus && go test ./internal/commands -run 'Guard' -count=1 -v` → every H id appears in at least one clean and one violating case; mutate one canonical sentence in `examples/README.md` locally → `TestGuardListMatchesContract` fails, then revert; `make guard` (no fixture token leaks into G9/G10 — they grep the whole tree)
- **description:** The four tests in "Guard design → Tests". Fixtures are generated into `t.TempDir()`; forbidden identifiers are concatenated at runtime.

### task-12: Makefile G30 hook and CI comment

- **depends_on:** [task-10]
- **model:** opus
- **files:** [`Makefile` (`.PHONY`, `guard:` list, the new target + comment), `.github/workflows/check.yml` (:2 count comment)]
- **verify:** `make guard-host-contract` prints four `-- examples/…` blocks each ending `guard: clean`; `make guard`; `make check` (full; this is the CI gate)
- **description:** Add `guard-host-contract` as above; keep the loop list identical to §8's "guarded" rows. The target `cd`s into `workshop/gopernicus` and runs `go run . guard ../../examples/<h>` (the G13 pattern); `examples/*` carry no `tool` directive (RELEASING.md :1765–1767, G11). `go run` compiles the stdlib-only CLI once and caches it; four to five invocations cost seconds.

### task-13: Flip the contract and docs to present tense; CLI README + docs reference

- **depends_on:** [task-12]
- **model:** opus
- **files:** [`examples/README.md` (:44–45, :411–413, :474–475, §8 table), `workshop/documentation/docs/architecture/host-contract.md` (:25), `workshop/documentation/docs/workshop/commands.md` (new `## guard`), `workshop/documentation/docs/workshop/overview.md` (:8), `workshop/gopernicus/README.md` (install line `@v0.3.0` → `@v0.4.0`, commands table row), `ARCHITECTURE.md` (:48 "init / new pocket / db verbs" → add guard; :560 "will be" → "is")]
- **verify:** `grep -n "planned\|will print\|once the guard verb ships" examples/README.md workshop/documentation/docs/architecture/host-contract.md` → none; `diff <(cd workshop/gopernicus && go run . guard --list | sed 's/^H[0-9]*  *//') <(awk -F'|' '/^\| \*\*H[0-9]+\*\* \|/ {gsub(/^ +| +$/,"",$3); print $3}' examples/README.md | head -15)` → identical (the test does this too; the command is the human check)
- **description:** Make every "ships in"/"will print"/"once it ships" sentence present tense now that the verb exists in the tree; document `guard` (flags, output shape, exit codes, the no-skip rule) in `commands.md` in the page's existing format; add the `guard` row to the CLI README table.

### task-14: Sanity pass over the emitted-host guard shapes (no template change)

- **depends_on:** [task-10]
- **model:** sonnet
- **files:** [none expected; read `workshop/gopernicus/internal/commands/templates/init/Makefile.tmpl` and `scaffold_test.go` :266]
- **verify:** `cd workshop/gopernicus && go test ./internal/commands -run 'Scaffold' -count=1`
- **description:** Confirm the init templates still emit the three grep guards and that `assertGuardShapes` is unchanged — this PR deliberately does not touch the skeleton (YOUR CALL 7). Report the exact lines a follow-up would change (`Makefile.tmpl` `guard:` → `go tool gopernicus guard`; `go.mod.tmpl` `tool` directive + `require` at `v0.4.0`; `readme.md.tmpl` "Local framework development" replace line).

### task-15: Release `workshop/gopernicus/v0.4.0` (owner-executed)

- **depends_on:** [task-13]
- **model:** opus
- **files:** [none in-tree; the annotated tag]
- **verify:** Owner, one step per command (the classifier blocks merge+tag+push in one line): `git tag workshop/gopernicus/v0.4.0 <PR-3 merge sha> -m "workshop/gopernicus v0.4.0: the guard verb (host contract H0–H14)"`; `git push origin workshop/gopernicus/v0.4.0`; poll `curl -s https://proxy.golang.org/github.com/gopernicus/gopernicus/workshop/gopernicus/@v/v0.4.0.info` until it resolves — **never** curl `.info` before the tag is pushed (a miss is negative-cached ~30 min); then cold-verify from an empty scratch dir: `GOWORK=off GOMODCACHE=$(mktemp -d) GOFLAGS=-mod=mod GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.4.0 guard --list` → 15 (or 14) lines; `… @v0.4.0 version` → prints the module path; and `… @v0.4.0 guard <a scratch copy of examples/minimal>` → `guard: clean`.
- **description:** MINOR (new verb). `make check` must be green on the merge commit (RELEASING.md "Preconditions" item 3). No other module is tagged; the `version` const stays the pre-tag placeholder as documented (`commands.md` "version").

### task-16: Release record + plan copy

- **depends_on:** [task-15]
- **model:** opus
- **files:** [`RELEASING.md` (new top entry "Host contract reconciliation (workshop/gopernicus v0.4.0 — published <date>)" + the LIVING paragraph :1753 made definite), `plans/host-contract-reconciliation.md` (copy of this file with the status line updated), `NOTES.md` (one line under task-4's entry: RELEASED)]
- **verify:** `make guard` (G20 scans `plans/` for legacy paths — the copy must not introduce `features/`); `git grep -n "v0.3.0" -- RELEASING.md examples/README.md workshop/gopernicus/README.md` → only historical entries
- **description:** Record sha, tag, cold-verification evidence, and the downstream handoffs below in the RELEASING.md house format (newest entry first, "What changed / Tags and verification / Known").

## Sequencing

```
task-1 ─┬─ task-2 ─┐
        ├─ task-3 ─┼─ task-4                       (PR-1)
        │          │
        ├─ task-5 ─ task-6 ─┐
        ├─ task-7 ──────────┼─ task-9              (PR-2, stacked on PR-1)
        └─ task-8 ──────────┘
                              task-10 ─┬─ task-11
                                       ├─ task-12 ─ task-13      (PR-3, stacked on PR-2)
                                       └─ task-14
                                                    task-15 (owner tag) ─ task-16 (PR-4)
```

PR-1 can merge before PR-2 is opened; PR-3 cannot be CI-green until PR-2's tree is under it.

## Risks

- **Rule-set churn after the tag.** `--list` is versioned with the binary; if a YOUR CALL
  below is reversed after v0.4.0, the fix is v0.4.1 + a contract edit in the same PR, not an
  edit to the page alone (RELEASING.md :1762–1764). Mitigation: settle every YOUR CALL before
  PR-1 merges.
- **Fence false positives on real hosts.** H12 is strict (tests included) and the examples have
  only `pgx/v5` and `a-h/templ` as third-party imports under `internal/`+`pockets/`
  (`git grep` on `origin/main`); venona's blake3/zstd and gps-360-go's `x/net/html` test import
  are the known downstream hits — recorded in the handoff, not softened in the rule.
- **AST floor vs. normative clause.** H10 and H11's "returns no domain type" are reviewer
  clauses with structural floors; the plan keeps the :396–400 language so a green guard is never
  read as "the root is wiring in substance".

## YOUR CALL items (owner decisions, each with a recommendation)

1. **Fold vs split the integrations rules.** Proposed: H5 keeps the outbound row and the
   integration row moves to a new **H11** so a finding cites one sentence and `--list` reads
   cleanly. *Recommend: split (as written).* Alternative: keep two H5 rows (the current :129–130
   shape) and number the fence H11, host sdk H12 — thirteen rules.
2. **Sanction a host `ui/` root?** Evidence: gps-360-go `ui/{spa,ts}` (Go + TypeScript; guarded
   `guard-spa-stdlib-and-web` :979–990 to stdlib + `sdk/pkg/web`), Segovia v2 `ui/ts`, venona
   `ui/`. *Recommend: sanction* `ui/` as an optional Go-bearing root for host presentation
   packages with no domain — imports stdlib, framework `sdk/...`, framework `ui/*`, recognized
   view technology; never `internal/`, `pockets/`, or `integrations/` of either scope — and
   move `examples/goth-showcase/internal/showcase` → `examples/goth-showcase/ui/showcase`, then
   add goth-showcase to G30 so all five examples are guarded. The rule rides in H0 (root list)
   plus one import-law row; no new id. If declined: goth-showcase stays a named exemption and
   the §8 wording is kept.
3. **Guard `examples/auth-cms` under G30?** After PR-2 it passes the floor (analysis above),
   and only the floor. *Recommend: yes* — guard it, and in the same PR change its §8 row and
   README to "passes the H floor (G30); its composition root still carries provider behavior
   (`demo.go`, `purge.go`, `membership.go`, `product.go`, `oauthfake.go`) that the H10 reviewer
   clause covers — named debt, not a layout reference". The alternative (exempt it) leaves the
   most-tested host unguarded for no structural reason; the risk to manage is the PR text
   reading a green guard as conformance.
4. **Driver allowance under H12's outbound slots.** Reference: exactly `github.com/jackc/pgx/v5`.
   *Recommend: the whole shared recognized-driver list* {pgx/v5, libsql-client-go,
   modernc.org/sqlite, lib/pq}, allowed only under `internal/outbound/**` and host-pocket
   `outbound/**` — one list for H3, H7 and H12, so a Turso-backed host's app-local repositories
   are not caught by one rule and freed by another. Alternative: pgx alone (the reference's
   literal), and the list grows in a minor on the first lesson.
5. **Reference findings — record or sanction.** gps-360-go also has `manual/` and `harness/`
   (stdlib-only leaves consumed by `cmd/gps360` and `cmd/mcp`; README :296–304), `server/` (a
   committed 125 MB built binary, not a package root — H0 is about Go packages, so out of scope;
   a `.gitignore` matter for the reference), and `pockets/auth/views` (templ; H0's host-pocket
   shape :89–91 says never `views/` — the blessed slot is `pockets/auth/inbound/views`, :329–331).
   *Recommend: record all four as reference findings in §8 (task-1); do not sanction `manual/`/
   `harness/` — under this contract a stdlib-only leaf shared by two binaries is the host `sdk/`
   root's job (`sdk/manual`, `sdk/harness`), and that is the handoff note.*
6. **Client binaries may declare interfaces.** The reference's `cmd/gps360/write/*.go` declares
   ten (`SkillRegistry`, `ClipsDesk`, …) — the command packages' narrow ports over the client,
   exactly the consumer-declares-the-port pattern the rest of the contract teaches. Forbidding
   them would make the reference fail H10 on day one. *Recommend: allow* (as written in H10),
   classification by imports (the reference's `guard-cli-no-inward-imports` :799–808), SQL
   literals still forbidden. The tension to accept: a composition root cannot declare an
   interface but a client binary can; the discriminator is structural, so the guard never
   guesses.
7. **`gopernicus init` skeleton (PR-B's second half) — this milestone or follow-up?**
   *Recommend: follow-up (v0.4.1)* after the tag is on the proxy: the init templates pin
   published versions (`go.mod.tmpl` :5 pins `sdk v0.9.0`; `TestReleaseScaffoldsResolvePinnedModules`
   resolves them from the proxy), so a `tool …/workshop/gopernicus v0.4.0` directive cannot be
   emitted before v0.4.0 exists — the same reason `v0.2.1` bumped scaffold pins after the fact
   (RELEASING.md :1643). Task-14 records the exact lines.
8. **(Decided, recorded for visibility) H10 is production-files-only, both clauses.**
   `examples/auth-cms/cmd/server/jobs_delivery_live_pgx_test.go` :36 (`TRUNCATE`) and
   `jobs_delivery_live_turso_test.go` :37 (`DELETE FROM`) are SQL literals in `cmd/` tests behind
   `//go:build livedelivery`; `go/parser` ignores constraints, so a tests-included H10 would
   fail the harness on day one for a live-leg fixture reset. The contract's general rule is
   production files unless a row says otherwise (:120–121); H10's sentence now says so.
9. **Sanction or deny `internal/**` → host `workshop/` imports.** The reference's
   `internal/inbound/domains/manual` imports `workshop/testweb` in production code; no current
   rule covers it. *Recommend: deny* (the clause is in H12 above) — `workshop/` is the
   developer-time tree and nothing under `internal/` or `pockets/` should link it; the
   reference gets a finding in the handoff.

## Downstream handoff

- **gps-360-go (the reference).** Conforming to the new H0/H4/H5/H7/H11/H12 by construction
  (its `guard-integrations` and `guard-no-vendor-libs` are the source). Residual findings to
  settle on its side: (a) `pockets/auth/views` → `pockets/auth/inbound/views` (host-pocket shape);
  (b) `manual/`, `harness/` — host `sdk/` root or keep as named host exemptions; (c)
  `pockets/auth/views/views_test.go` imports `golang.org/x/net/html` — outside the fence
  (its own guard prunes the dir); (d) `server/` is a committed build artifact; (d′)
  `internal/inbound/domains/manual` imports `workshop/testweb` in production code (H12's
  no-`workshop/` clause, YOUR CALL 9); (d″) `integrations/{emailer,fec,quickbooks}` are
  depth-one — legal under `integrations/<capability>[/<vendor>]`; (e) after
  v0.4.0, pin the tool (`go get -tool github.com/gopernicus/gopernicus/workshop/gopernicus@v0.4.0`)
  and collapse the mirrored greps per §7 — its 25 `guard-*` targets with no H-equivalent stay.
  The `cmd/gps360` interfaces are sanctioned under H10's client-binary carve-out.
- **venona-project.** Its S2.R plan (`venona: plans/hosting/02-management-app.md`
  "S2.R plan (2026-10-20)") already matches rulings 1–2: top-level `integrations/`, repo-local
  `sdk/`, `internal/outbound/domains/<d>`, `guard-no-vendor-libs`. Alignment notes: (a) put
  authjobs at `pockets/authentication/outbound/authjobs` (a sub-package, matching auth-cms and
  leaving room for siblings), not as the `outbound` package itself; (b) its planned
  `guard-layout` root list includes `ui/spa` — align with YOUR CALL 2's `ui/` wording; (c) its
  layered `sdk/` (`pkg/`, `capabilities/`) is a superset of H13 — fine; (d) today's
  `internal/{inbound/{cli,httpapi},integrations/*,outbound/<flat>}` are H0/H5 findings its own
  slices R1–R6 remove; (e) pin the tool after v0.4.0 and run `go tool gopernicus guard` red
  until R6 lands (:431–434).
- **Segovia v2** (`segovia/v2`). Already has top-level
  `integrations/ratelimit` named only by `cmd/server`, and `internal/outbound/domains`. Findings
  under the new text: `internal/outbound/identifiers` (not under `domains/` — H0 shape);
  `cmd/server/{notice_delivery,discussions,mention_labels}.go` declare five interfaces (H10 —
  `cmd/server` imports `internal/`, so it is a composition root, not a client binary); its
  `github.com/google/uuid` import under `internal/` fails the fence (H12 — the framework
  connector is `integrations/cryptids/google-uuid`, named from `cmd`); `integrations/ratelimit`
  is `<capability>` with no `<vendor>` segment (it wraps pgxdb — `integrations/ratelimit/pgx`
  or similar). Segovia pins the tool after v0.4.0 and runs red until its own slice.

## Consultation notes

Two read-only passes, single hop, on a paragraph sketch of the rule set, the moves and the guard
design. Both verdicts: direction sound, edits required before it is a task list. Folded in:

- **lead-backend-engineer.** (1) Hosts in this repo sit under the framework module prefix →
  classifier is host-first then an enumerated framework list; `…/examples/*` and `…/workshop/*`
  are third-party for every rule; the root `…/pockets` package is a framework core. (2) G11
  greps `workshop/` for the quoted framework-pocket string → guard constants are assembled.
  (3) One shared driver list for H3/H7/H12; `golang.org/x/...` is third-party and the rule says
  so; real-integration tests have named homes. (4) H10 production-only (live-leg SQL in
  `cmd/` tests). (5) auth-cms passes the floor only — recorded as named debt. (6) Supervisor
  moves into `deliveryhealth` rather than exporting the consumer's port from the producer.
  (7) No example edge breaks under the new rules; H5's `_test.go` exemption must survive;
  the moved packages' tests force the `outbound` slot. (8) deliveryhealth is outbound.
  (9) go.mod parsing edge cases, `import "C"`, `testdata` at any depth, test-only and
  `//go:build ignore` dirs, symlinks, and the `cd workshop/gopernicus && go …` hook pattern.
  (10) Prose drift the `--list` test cannot see → task-1/3 greps.
- **architecture-steward.** (1) The fence as drafted contradicted H1 → H12 is now stated as
  the outer fence that H1/H4/H6/H7 tighten, with drivers confined to the outbound slots.
  (2) H4/H5/H6-logic name the host's `sdk/`; a deny on `internal/**` → `workshop/` (YOUR CALL 9).
  (3) Host `sdk/` is doctrine-consistent only with a content fence (no entity/port/Service) and
  the "framework-sdk-only admission test" caveat → in H13 and §6. (4) Integrations rules get
  their own id; the reference's `integrations/` tree is not uniformly two-level → `[/<vendor>]`.
  (5) Turso carve-out reuses the :327 forced-companion framing — no second exception.
  (6) `pockets/auth/views`: record, do not widen. (7) `ui/` gets its own import-only rule
  (H14) if sanctioned; "registers no route" stays a reviewer note because `goth-showcase`'s
  package legitimately exposes a `Mount` that `cmd` calls. (8) Client-binary definition names
  the host's `integrations/` too. (9) ARCHITECTURE.md :541–560 re-sync is task-2.

Not adopted: the steward's suggested id numbering (H11 fence / H13 integrations) — the plan
keeps integrations at H11, fence H12, host sdk H13, ui H14 so ids read in the order a host grows
them. The planner's call.

## Open questions

- Whether `examples/goth-showcase`'s `e2e/` Playwright suite references the Go package path
  anywhere besides `e2e/README.md` :12 (task-9 greps; if a spec imports a path, it moves too).

## Recommended reviews

- **product-manager** — scope and sequencing: three PRs vs two; whether YOUR CALL 7's deferral
  leaves a host-developer with a confusing `init` output between v0.4.0 and v0.4.1.
- **platform-sre** — the tag/cold-verify step, the G30 hook's CI cost (`go run` compiles the
  CLI once per host — four to five times; acceptable, or build once to `bin/`?), and the
  `check.yml` comment correction.
- **architecture-steward** — post-hoc on the final rule wording in PR-1 (the pre-write pass is
  in the consultation notes).
- **lead-frontend-engineer** — PR-2's theme/authpages/showcase moves are path-only, but the
  run-and-look notes (ACME chrome, branded login, showcase specimens) deserve a second pair of
  eyes before "done".

## Notes

- The planner found no copy of the original `.claude/plans/host-layout-contract.md` on
  `origin/main` or in `plans/`; `NOTES.md` :2725–2773 is the surviving record of PR-A/PR-B and
  is cited instead.
- G-number: the Makefile's highest in-use id is G29 (`guard-tuple-write-integrity`); PR-B's
  original "G21" and the later "G22" were both taken (`guard-list-no-nethttp`, and the 2026-09
  additions). The host guard is **G30**.
- `workshop/gopernicus/v0.3.0` (2026-09-11, "coordinated framework audit") is the tag the
  contract currently names for the verb; it predates this work and carries no `guard`. v0.4.0
  is the first tag that does.
