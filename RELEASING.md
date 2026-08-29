# Releasing gopernicus modules

This repo is a multi-module workspace (`go.work`, dev-only) with thirty-seven
modules today: `sdk`; `integrations/{cryptids/bcrypt, cryptids/golang-jwt, cryptids/google-uuid,
datastores/pgxdb, datastores/turso, email/sendgrid, filestorage/gcs,
filestorage/s3, kvstores/goredis, oauth/github, oauth/google,
notify/mailer, scheduling/robfig-cron, tracing/otel}`; `pockets/authentication`
(+ `views/goth`, its bundled default views module — auth-v3 AV3-8.2, 2026-07-13;
renamed from `views/templ` and re-implemented on `ui/goth` in ui-goth GOTH-7.2,
2026-07-18), `pockets/authorization` (authorization-v1, 2026-07-09), `pockets/cms`
(+ `views/goth`, its bundled default views module — feature-standard B2, 2026-07-07;
renamed from `views/templ` and re-implemented on `ui/goth` in ui-goth GOTH-7.3,
2026-07-18), `pockets/events` (events-v1, 2026-07-08), `pockets/jobs`
(each pocket + `stores/{turso,pgx}`); `examples/{cms,
minimal, auth-cms, jobs-minimal}`; `workshop/gopernicus` (the scaffolding
CLI — a `go install`-able tool, tagged like any importable module). Each importable module (everything except the four
`examples/*` hosts, which are demonstrations, not libraries) is tagged and
versioned **independently** — there is no single repo-wide version.

**First release cut 2026-08-14: every importable module tagged `<dir>/v0.1.0`**
(33 tags — everything except the four `examples/*` hosts; plan of record
`.claude/plans/release-v0.1.0.md`). The release commit dropped every nested
module's pre-tag relative `replace` and pinned sibling requires at `v0.1.0`
(precondition 2 below, satisfied); `go.work` continues to resolve siblings by
directory for local dev. All upgrade notes below keyed "next tag" describe the
v0.1.0 vintage. Tags are immutable once the module proxy serves them — a
correction is a new patch tag, never a retag.

**2026-08-14 (same day): `sdk/v0.2.0` + `features/jobs/v0.1.1`** — the fenced
dead-letter reason fix (plan of record
`.claude/plans/deadletter-failure-reason.md`; Coordination-Hub issue #5). See
the upgrade notes for both tags below. No other module was bumped: only
`features/jobs` consumes the changed kernel symbol, and its store modules'
`features/jobs v0.1.0` pins upgrade at the host via MVS.

**2026-08-14 (same day): `features/jobs/v0.2.0` +
`features/jobs/stores/{pgx,turso}/v0.2.0`** — jobs tenant metadata (plan of
record `.claude/plans/jobs-tenant-metadata.md`; Coordination-Hub issue #4).
Minor bumps all three; store pins move to `features/jobs v0.2.0` / `sdk
v0.2.0`. See the upgrade notes below — already-migrated hosts need a host-tree
ALTER (reference SQL in the store note).

**2026-08-14 (same day): `sdk/v0.3.0`,
`features/authentication/v0.2.0`, `integrations/datastores/pgxdb/v0.3.0`** — the
coordination-hub authentication upstream batch (plan of record
`.claude/plans/coordination-hub-auth-upstream/`; the owner-cut tag manifest and
post-tag verification steps are in that directory's `tag-manifest.md`). Minor
bumps all three: genuinely global middleware + configurable CORS, the browser
cookie-flow seams (`RefreshCookiePath`, `/auth/csrf`, `/auth/me`,
`origin_rejected`), and the durable pgx rate limiter. `features/authentication`
pins `sdk v0.3.0`; `integrations/oauth/google` stays at **v0.1.0** (byte-identical
to its tag, verified) and the authentication store modules do **not** retag (no
repository contract changed). Read the three upgrade notes below before adopting —
pgxdb v0.3.0 ships **host schema**, and the sdk note carries the batch's one silent
behavior change (`HandleRaw` no longer bypasses global middleware).

**2026-08-15: `integrations/email/sendgrid/v0.2.0`** — truthful capability
metadata (plan of record, in the Coordination-Hub repo,
`.claude/plans/email-and-invitations.md`, task U1). A **minor**: `Sender` now
implements `email.CapabilityReporter`, describing the configured instance —
the default host and explicit HTTPS hosts report TLS/production-capable; a
non-HTTPS custom host (tests, local emulators) reports development-only. No
breaking API change. See the upgrade note below.

**2026-08-16: `features/authentication/v0.2.2`** — the email layout-override
seam (plan of record, in the Coordination-Hub repo,
`.claude/plans/branded-auth-mail-and-pages.md`, task U1). A **patch**, additive
only: `Config.EmailLayouts []EmailLayoutOverride` (aliasing
`delivery.LayoutOverride{FS embed.FS; Dir string}`, `Dir` defaulting to
`"layouts"`) registers host layout files at `email.LayerApp`, so a host-shipped
`transactional` pair re-frames every auth mail while the zero value keeps the
sdk default byte-identically (pinned by test). Content templates
(`EmailContentTemplates`) are untouched and compose with it. No exported API
break, no schema change, no `go.mod` change (still `sdk v0.3.0`), no store
retags.

**2026-08-15: `features/authentication/v0.2.1`** — the
add-or-signup invitation lifecycle fix (plan of record, in the Coordination-Hub
repo, `.claude/plans/email-and-invitations.md`, task U2). A **patch**: a brand-new
OAuth-provisioned account now resolves its pending auto-accept invitations through
the same best-effort resolver `Register`/`Verify` use, and the invitation JSON
response gains the additive `invited_by` owner field. No exported API change, no
schema change, no `go.mod` change (still `sdk v0.3.0`), and no store retags. Read
the upgrade note below before adopting — it pins which OAuth branches do and do
not grant.

**2026-08-15: `sdk/v0.3.1`** — the coordination-hub
`coordination-api-consistency` upstream flags: `web.ErrStateConflict` completes
the 409 vocabulary (code `conflict`, distinct from `ErrConflict`'s
`already_exists`), and `crud.Page`'s absent-`has_more`-means-false end-of-list
contract is now documented (godoc + sdk README). Cut as a **patch by owner
ruling** despite the one additive symbol — see the upgrade note below. No other
module bumps: the sdk tree is otherwise byte-identical to `sdk/v0.3.0`, and no
in-repo module pins move.

**2026-08-16: `sdk/v0.4.0`, `integrations/datastores/pgxdb/v0.4.0`,
`integrations/datastores/turso/v0.3.0`, `features/authentication/v0.3.0`,
`features/authentication/stores/{pgx,turso}/v0.2.0`** — the coordination-hub
authentication upstream batch, phases 1–8 (plan of record
`.claude/plans/coordination-hub-auth-upstream/`), stacked with the crud search
plan (`.claude/plans/crud.md`, T1–T4). Six minor bumps closing every hub flag:
account lifecycle + operator directory, verification resend, canonical
`environment.Mode` with capability-owned transport checks, password-reset links,
provision-on-consumption (**default-off**), the transactional-layout logo fix,
and SQL-side list search in both dialects. Shipped as **one train** by owner
ruling — provisioning is additive and default-off, so it raised no bump.
`integrations/email/sendgrid` did **not** retag (test-only change).

**Two operator-incompatible changes — read before deploying.** Store migrations
`0014` and `0015` must be applied *before* binaries built against the new store
tags (both constructors probe the added columns and refuse to construct
otherwise), and `Config.PasswordResetURL` is now **required in production**. The
as-executed record, per-module upgrade notes, and the adopter checklist are in
the plan directory's `tag-manifest.md` §8 and `adoption-checklist.md`.

**2026-08-18 `features/authentication/v0.3.1` — clickable OAuth pending-link mail.**
Cut from `main` (which was already at `features/authentication/v0.3.0`) as an
ordinary patch on the current line, NOT the earlier-considered maintenance-line
`v0.2.4` off `v0.2.3`: coordination-hub adopts the current line, so no independent
patch line is maintained. The bump is a **patch** despite adding an exported
`Config` field — the field is additive with a safe zero value, following the
owner's `sdk/v0.3.1` precedent that an additive symbol may ship as a patch.

Scope is presentation-only: the anti-takeover OAuth pending-link email becomes a
clickable link (single-use token in the URL **fragment**, mirroring the magic
link) when the new `Config.OAuthLinkBaseURL` (`AUTH_OAUTH_LINK_URL`) is set, and
the pending-link callback redirect carries `auth=link_sent&provider=<name>` so the
host SPA can render a "check your email" state. The anti-takeover model is
unchanged — the emailed secret is still proof of inbox control.

Upgrade note (backward-compatible, no new production requirement): `OAuthLinkBaseURL`
is OPTIONAL. Empty keeps the historical raw-token email line and, when OAuth
providers are wired, emits one startup WARN naming `AUTH_OAUTH_LINK_URL`. A
non-empty value is validated at construction — absolute http(s) with a host, no
fragment (`ErrOAuthLinkURLInvalid`), HTTPS in production (`ErrOAuthLinkURLInsecure`);
existing non-secret query parameters are preserved. No schema or sibling-module
change. The host-side landing route that reads the fragment token and POSTs
`verify-link` is coordination-hub's #162.

**2026-08-25: `sdk` + `integrations/datastores/pgxdb` — next tags, patch by owner
ruling** — `crud.MapPageErr` and `pgxdb.ProbeTable` (plan of record
`plans/pgxdb-probe-table-map-page-err.md`; originating host gps-360-go).
Both additive, zero-value-preserving, no schema, no sibling pin moves. See the
two upgrade notes below.

**2026-08-25: `features/authentication/v0.5.3`** —
`Config.PasswordFlowsDisabled` (plan of record
`plans/authentication-password-flows-toggle.md`; originating host gps-360-go, a
Google-only staff host). Additive, zero value keeps every route; no schema, no
pin moves. See the upgrade note below.

**2026-08-25: `features/authentication/v0.5.4`** — `Config.MachineRoutesDisabled`
(plan of record `plans/authentication-machine-routes-toggle.md`; originating
host gps-360-go). Additive, zero value keeps every route; no schema, no pin
moves. Carries a **security caveat** on the bundled lifecycle routes — read the
upgrade note before exposing them on a multi-user host.

**2026-08-26: `integrations/datastores/pgxdb/v0.5.0` +
`features/authentication/stores/pgx/v0.4.0`,
`features/{authorization,cms,events}/stores/pgx/v0.2.0`,
`features/jobs/stores/pgx/v0.3.0` — ONE train, cut and pushed 2026-08-26 (minor
across all six; cold-resolution verified)** — host-chosen schema for every feature
pgx store (plan of record `plans/pgx-store-schema-option.md`; gopernicus issue
#4; originating host gps-360-go). `pgxdb.Schema` + `RunMigrations(...,
pgxdb.WithSchema(s))`, and `WithSchema(s)` on every store's `Repositories` AND
every exported per-repository constructor. Default (no option) SQL is
byte-for-byte unchanged. **Pin moves: every store pins `pgxdb v0.5.0`, which
drags `sdk` to v0.4.x for four of them.** Read the combined upgrade note below
before adopting — it ratifies a change to the migration runner's stream model
(one stream per schema, per-schema ledgers) and names the jobs `QueueOption`
compatibility consequence.

**2026-08-26: `features/authentication/v0.6.0` — next tag, MINOR** — the bundled
machine-identity lifecycle routes come standard **behind a host gate**
(`Config.MachineRoutesGate`), and the one-day-old `Config.MachineRoutesDisabled`
is **REMOVED** — a source break (plan of record
`plans/authentication-machine-routes-gate.md`; gopernicus issue #6; the host
that had to re-implement these routes is gps-360-go). Ungated routes no longer
mount at all:
a v0.5.x host with the machine repositories wired and no gate answers 404 on all
five and WARNs at boot until it names one. `POST /auth/service-accounts` no
longer takes `owner_user_id` (400 by name, one release) — the caller is the
owner, and `act_as_user_id` is the explicit, validated, audited delegation, and
cookie clients must now double-submit the CSRF token on the three lifecycle
POSTs (`application/json` required there too). No
schema, no store retags, no pin moves. Read the upgrade note below before
adopting.

**2026-08-27: `sdk/v0.6.0`, `integrations/datastores/pgxdb/v0.6.0`,
`pockets/authentication/v0.8.0` — next tags, ONE train, MINOR across all
three** — the list contract reaches the request (plan of record
`plans/web-crud-list-request.md`; gopernicus issues #11 and #9, bundled by
owner ruling; originating host gps-360-go). `crud.ParseListQuery` over
`url.Values` with every `ParseListRequest`/`ParseOrder` rejection wrapping
`sdk.ErrInvalidInput`; `crud.Items`/`MapItems` and nil-page normalization;
`web.NoStore()`; pgxdb registers a UTC `TimestamptzCodec` on every connection
and defaults the session `timezone` to `UTC` when the host names none; plus
the #9 helpers `Collect[T]`, `ProbeTables`, and `MapError` 22P02. **Pin move:
`pockets/authentication` → `sdk v0.6.0`**; pgxdb stays on `sdk v0.4.0`; no
store module retags (their pins upgrade at the host via MVS). Three observable
changes ride this train — read the notes below before adopting: parser errors
now classify as 400 through `ErrFromDomain` (a host that special-cased the 500
can delete the special case), SDK-constructed empty pages marshal `"items":[]`
instead of `null` (visible on turso-backed hosts), and every scanned
`timestamptz` is UTC-located while zone-dependent SQL on a zone-less DSN now
evaluates in UTC.

**2026-08-28: `features/authentication/v0.4.3` — TAGGED (maintenance line off the
immutable `v0.4.2` tag) + `pockets/authentication/v0.8.1` (next tag on main) —
host-owned mail DATA and SUBJECTS, not only bodies** (plan of record
`plans/auth-mail-host-data-and-subjects.md`; consumer ruling 2026-08-28,
originating host coordination-hub, pinned to `features/authentication v0.4.2`
+ `sdk v0.4.1`). `Config.DeliveryData` (a per-render data hook with reserved
`Secret`/`Link`/`Subject`), `Config.EmailSubjects` + `Config.SMSBodies`
(per-purpose overrides, missing-key errors on), the ten `Purpose*` constants
exported, and enriched invitation/member-added data (`ResourceName`,
`RelationLabel`, `InviterName`, `InvitedBy`, `Metadata`, `InvitationID`,
`OperationID`, …). Nil hook + empty maps render byte-for-byte today's output
(pinned by golden). **Patch on both lines by plan ruling** — additive only, no
schema, no store retags, **no pin moves**: the `v0.4.3` maintenance commit keeps
`sdk v0.4.0` in `go.mod` (the hub's `sdk v0.4.1` pin stays), and `v0.8.1`
keeps `sdk v0.6.0`. Same public contract, safety rules, and tests on both;
adapted commits, not one cherry-pick (the module/paths were renamed between
them). Read the upgrade note below.

**2026-08-28: `pockets/authentication/v0.8.2` — TAGGED @ `c016423` (#17
squash) — `identity.Resolver` is principal-exact**
(plan of record `plans/identity-resolver-principal-exact.md`; originating host
gps-360-go plans/32). `Resolve` no longer synthesizes a user's display name
from the primary email local part when the stored `DisplayName` is blank — a
blank name is projected blank, exactly as stored; the port doc's "never
fabricates an Info" now holds for every field. Service accounts unchanged.
Patch by plan ruling: no port change, no schema, no Config, no pin moves. One
observable change for a host that rendered `Info.DisplayName` unguarded: a
name-less user now shows "" where it showed `bob` — choose the fallback at the
render site. See the upgrade note below.

**2026-08-28: `integrations/datastores/pgxdb/v0.6.1` — TAGGED @ `098241f`
(#16 squash), a patch by owner ruling, as with `ProbeTable`** —
`ListQuery.FixedOrder`, a store-fixed composite `ORDER BY` for the offset
strategy (plan of record `plans/pgxdb-list-fixed-order.md`; gopernicus issue
#15; originating host gps-360-go). Additive, zero-value-preserving, no schema,
no pin moves (still `sdk v0.4.0`), no store retags. See the upgrade note below.

**2026-08-29: `pockets/authentication/v0.9.0` — TAGGED @ `ad8b8a8` (#18
squash) — BREAKING, pre-1.0** — one composable authenticator, `RequirePrincipal(opts ...PrincipalOption)`,
named helpers, and the credential on the context (plan of record
`plans/authentication-principal-posture.md`; originating host gps-360-go
`plans/33-principals-api-keys-and-route-proof.md` D2). `RequireUser`,
`RequireServiceAccount`, `RequireLiveSession`, `RequirePrincipalBrowser`,
`RequireLiveSessionBrowser`, and `AuthenticateAPIKey` are REMOVED; every call
site is rewritten against the four options (`Accept`, `Transports`, `Live`,
`Browser`), the six named helpers, and `CurrentCredential`. `Config.BundledRouteAuth`
lets a host replace one bundled route group's authentication posture without
restating the others. No schema, no store retags. See the upgrade note below
before adopting — the owner cuts the tag.

## Tagging scheme

Nested Go modules in a single repo are tagged with the module's directory as a
prefix, per the standard Go module convention for multi-module repos:

```
sdk/v0.1.0
integrations/datastores/turso/v0.1.0
pockets/cms/v0.1.0
pockets/cms/stores/turso/v0.1.0
ui/goth/v0.1.0
```

Each module's own `go.mod` `require` versions (e.g. `pockets/cms/stores/turso`
requiring `sdk`) are bumped and tagged independently — a patch release of
`sdk` does not force a release of every module that depends on it, only the
ones whose `go.mod` is updated to require the new version.

**UI-implementation modules (ui-goth GOTH-0.2, 2026-07-17).** The seventh module
kind — a UI implementation under the top-level `ui/` family — tags the same
nested way and is versioned independently (`ui/goth/v0.1.0`). Unlike the four
`examples/*` hosts it is an **importable** module, so it IS tagged; its `go.mod`
requires its own view/runtime libraries and `sdk`, never a pocket/integration/
example/workshop (guard G17). A pocket's `views/goth` adapter module (when it
lands) tags independently and requires its pocket core + `sdk` + the pinned
`ui/goth` tag. The `ui/goth` module itself is created at GOTH-1.1; no `ui/*` tag
is cut this milestone.

**The `workshop/gopernicus` CLI as a host `tool` pin (host-layout-contract,
2026-08-27) — LIVING.** The scaffolding CLI is tagged like any other importable
module, but from **v0.3.0** it is also something a host depends on: it carries
the `guard` verb that checks a host against the H0–H10 rules of
`examples/README.md`, and a host pins it with a `go.mod` `tool` directive plus
the matching `require` — a developer-time dependency, never a runtime import.
That makes the rule-set versioned with the binary: **new H-rules ship in a minor
version of `workshop/gopernicus`, and a host adopts them by bumping its pin**
(`go get -tool github.com/gopernicus/gopernicus/workshop/gopernicus@<next>`),
never by copying Makefile text. A host that stays on an older pin keeps the
older rule-set and is unaffected until it bumps; a rule whose wording changes
is a change to `examples/README.md` and to the CLI's `--list` output in the same
tag. This repo's own `examples/*` never carry the `tool` directive (it would put
the workshop module in an example's graph, against G11) — they are guarded with
`go run` from the Makefile instead.

## Preconditions before the first tag

1. **Module paths are final.** Every `go.mod` module line and internal import
   is rooted at `github.com/gopernicus/gopernicus/...`.
2. **`replace` directives are dropped or pinned.** `go.work` itself is
   dev-only and is never part of what a downstream consumer sees. The nested
   modules that reference sibling modules by relative path in their own
   `go.mod` (e.g. `pockets/cms/stores/turso`'s `replace` of `sdk` and
   `pockets/cms` to `../../../../sdk` and `../../..`) must have those
   `replace` lines removed and replaced with ordinary `require` entries
   pinned to the sibling module's tagged version, so `go build` works for a
   consumer who does not have this repo checked out as a workspace.
3. **Guards + tests green** (`make check`) on the commit being tagged.

## Cutting a tag

For each module being released, from the repo root:

```sh
git tag pockets/cms/v0.1.0 -m "pockets/cms v0.1.0"
git push origin pockets/cms/v0.1.0
```

A consumer depends on it the normal Go way:

```sh
go get github.com/gopernicus/gopernicus/pockets/cms@v0.1.0
```

## Version bumps

Standard Go module semver rules apply per-module:

- **Patch** — bugfix, internal behavior, or a narrowly scoped additive change
  whose zero value preserves the historical behavior and which requires no
  schema migration, sibling-module bump, or mandatory production configuration.
  This explicitly includes optional host configuration and presentation changes
  when they can be adopted independently on a maintenance line. The new field
  or behavior must still have compatibility tests and an upgrade note.
- **Minor** — additive, backward-compatible exported API change that materially
  expands the host contract, requires coordinated adoption, introduces a
  mandatory production configuration, or otherwise cannot be safely consumed as
  a maintenance-line patch (for example, a new required `Config` field or a
  migration-bearing store change).
- **Major** — breaking exported API change (removed/renamed exported type or
  field, changed method signature). Pre-`v1`, breaking changes are expected
  and do not require a major bump by Go's own pre-release semantics; each
  module should still move to `v1.0.0` deliberately once its contract is
  considered stable, not accidentally on the first tag.

**`ui/goth` `Requirements`-surface convention (ui-goth gate-b, 2026-07-18).** Any
change to a `ui/goth` bundle's browser `Requirements` — a new CSP directive, a new
required source, or a change to what a profile requires — is an **adopter-facing
upgrade note even when it is only a semver patch**. Adopters map `Requirements` into
their own CSP (see `ui/goth/README.md` §11.3), so a widened requirement that ships
silently would break a host whose CSP no longer covers the kit's assets. Record it in
the module's next-tag upgrade note below and tell hosts to re-derive their CSP header.

## Upgrade notes (keyed to each module's next tag)

### sdk — v0.6.0 (next tag, 2026-08-27): the list contract reaches the request (minor)

Plan of record `plans/web-crud-list-request.md` (gopernicus #11). A **minor**
rather than the additive-as-patch precedent (`v0.3.1`, `v0.4.1`) by owner
ruling, because two things change observably alongside the additions.

**Additions.**

- **`crud.ParseListQuery(q url.Values, opts crud.ListQueryOptions) (crud.ListRequest, error)`**
  — `ParseListRequest` over the canonical query keys `limit` / `cursor` /
  `offset` / `count` / `q` (exported as `crud.QueryKeyLimit` … `QueryKeySearch`;
  `crud.QueryKeyOrder` names `order` for callers). `ListQueryOptions{Limits,
  DefaultStrategy}` is the resource-side policy; the zero value is sdk's
  defaults. It builds a `ListParams` and delegates, so there is one parser, not
  two. It lives in `crud`, not `web`: foundation packages import the root only
  (G12b), and every store adapter imports `crud`, so `crud` carries `net/url`
  and never `net/http` (guard **G21**, `guard-crud-no-nethttp`).
- **`order` is deliberately NOT folded in.** Parse it beside the call with
  `crud.ParseOrder(fields, q.Get(crud.QueryKeyOrder), defaultOrder)` — reject
  (JSON edges) or fall back to the default order (SSR, the cms admin posture)
  is a per-aggregate choice a combined parser cannot express.
- **`crud.Items[T](items []T) Page[T]`** and **`crud.MapItems[T,U](items []T,
  fn func(T) U) Page[U]`** — the bounded-page constructors for a
  parent-scoped, uncursored list: a `Page` holding only `Items`, so the wire
  shape is `{"items":[…]}`. `MapItems` is defined as `MapPage(Items(x), fn)`.
- **`web.NoStore() web.Middleware`** — a named preset of
  `DefaultHeadersMiddleware` writing exactly `Cache-Control: no-store` (no
  `Pragma`, no `Expires`) before the handler runs; a handler may still
  override on its own writer. Mount it on route groups whose answers derive
  from a per-request grant. It is a header policy the host applies, not an
  identity gate and not a guarantee — whatever gate is mounted beside it is
  what makes the group authenticated.

**Observable change 1 — parser rejections now classify.** Every rejection
from `ParseListRequest` (seven) and `ParseOrder` (two) wraps
`sdk.ErrInvalidInput`, sentence first, sentinel last
(`"rows value too small, must be larger than 0: invalid input"`); the
`strconv` cause stays in the chain for the three conversion errors. The
existing sentences survive verbatim as the prefix — a host matching the raw
strings keeps working. Consequences: `web.ErrFromDomain(err)` answers the
**generic** `400 bad_request "invalid input"` where it answered 500 (its
generic mapping is deliberate — `SafeDomainError` posture unchanged), and
`web.ErrValidation(err)` puts the sentence on the wire:
`{"message":"page limit conversion: strconv.Atoi: parsing \"zero\": invalid syntax: invalid input","code":"bad_request"}`.
That is the contract: `ErrFromDomain` for the status, `ErrValidation` for the
sentence — the path `DecodeJSON` failures already take. Cursor-decode errors
are NOT wrapped: a stale or bad cursor token is a first page by rule, never a
400. A host that special-cased the old 500 (gps-360-go `wire.ListRequest`)
can delete the special case; the helper collapses to
`crud.ParseListQuery(r.URL.Query(), crud.ListQueryOptions{Limits: limits})` +
`web.RespondJSONError(w, web.ErrValidation(err))`.

**Observable change 2 — SDK-built empty pages say `[]`, never `null`.**
`Items`, `MapItems`, `TrimPage`, `MapPage`, and `MapPageErr` all normalize a
nil item slice to an empty one. pgx pages never carried nil (`CollectRows`
starts non-nil); the turso connector's `List` did, so a **turso-backed host's
empty page changes from `"items":null` to `"items":[]`** on this tag —
bug-fix class, but a wire change. A directly constructed `Page[T]{}` is
caller-owned and still marshals `null`; there is no `Page.MarshalJSON`.

Not on this tag: `web/openapi.go` still documents only `limit`/`cursor`/`order`
(pre-existing drift, left as found); a civil-date type; folding `order` into the
parser.

### integrations/datastores/pgxdb — v0.6.0 (next tag, 2026-08-27): UTC-located `timestamptz` on every connection, session `timezone=UTC` by default, `Collect`/`ProbeTables`/22P02 (minor; connect-time behaviour change)

Plan of record `plans/web-crud-list-request.md` (gopernicus #11 D6, and #9
bundled by owner ruling). Pin stays `sdk v0.4.0` — no new sdk symbol is used.
No `Config` field is added: the scan location is always UTC, and the DSN is
the session-zone escape hatch.

**Why (the cause is NOT the session zone).** In the pinned pgx v5.8.0 the
default extended protocol decodes `timestamptz` in **binary** — microseconds
since epoch, no zone — via `time.Unix(...)`, which yields a `time.Local`
-located value; the session `TimeZone` only shapes the **text** decoder's
input. So a scanned `timestamptz` marshalled as `2026-08-27T11:58:03-07:00`
on a laptop and `Z` in a `TZ`-less container, and `SET TIME ZONE 'UTC'`
alone would not have changed that. `pgtype.TimestamptzCodec{ScanLocation}`
("does not change the instant") is the seam both decoders honour.

**D6a — the scan location (unconditional).** `Open` now sets
`pgxpool.Config.AfterConnect` to register, on every connection's type map, a
`timestamptz` type whose codec is `TimestamptzCodec{ScanLocation: time.UTC}`
and a `_timestamptz` array type built over that new element. The array
registration is the strictly-safe form: pgx's default array codec captured a
pointer to the default element type at init (`pgtype_default.go:169`), and
while a `[]time.Time` destination already re-plans through the map (the
element codec declines a `*time.Time`, so scalar-only registration suffices
there — measured), a `pgtype.Timestamptz` element destination would still
reach the stale codec. `tstzrange`/`tstzmultirange` are out of scope (same
captured-pointer shape; no in-repo store scans them). `timestamp` (without
zone) already decoded to UTC — untouched. **What changes:** every scanned
`timestamptz`, scalar or array, in `Scan`, `QueryOne[T]`, `List`, and under
`QueryExecModeSimpleProtocol` alike, is UTC-located — `Equal`/`Before`/`Sub`
are unchanged; `String()`/`Format` and `encoding/json` output switch from the
local offset to `Z`. Hosts already on `timestamps.go`'s `From*` helpers see
nothing new. Hosts carrying a `wire.Time`/`TimePtr`-style presentation helper
(gps-360-go: 55 sites) can delete it — DTO fields become `time.Time` /
`*time.Time`, byte-identical output for scanned instants. **Audit first:** an
instant minted in-process (`time.Now()` in a domain constructor) still carries
`time.Local` unless the container runs `TZ=UTC` — normalize those at the
domain edge with `.UTC()`. **`AfterConnect` is owned by the connector** for
its codecs; a future `Config.AfterConnect` seam will chain after the
connector's registration, never replace it (recorded on `Config`).

**D6b — the session zone (owner-ruled IN).** `Open` sets the startup
parameter `timezone=UTC` **unless** the host already named one: a `timezone`
key in the DSN (any case; pgconn also maps `PGTZ` onto it) or an `options=`
value containing `timezone=` (e.g. `-c TimeZone=Europe/Oslo`, also
`PGOPTIONS`). A startup parameter costs no round-trip, is idempotent under
pgx's reconnects, and — unlike an `AfterConnect: SET TIME ZONE` — survives
PgBouncer transaction-mode server reuse because `timezone` is in the pooler's
tracked startup set. The DSN string itself is never rewritten. **What it
changes, honestly:** nothing about scans (D6a covers those). It changes
server-side, zone-dependent SQL for hosts that never set a zone: `now()::text`,
`to_char(tstz, …)`, `date_trunc('day', tstz)`, `tstz::date`, `EXTRACT(hour
FROM tstz)`, `timestamptz → timestamp` casts, and — the sharp one — a bare
`'2026-01-01 00:00'` literal bound to a `timestamptz`, which is interpreted in
the session zone. Bound `time.Time` arguments are unaffected (pgx binds an
instant). Today those expressions differ silently between a laptop and a
`TZ`-less container; after this tag they evaluate identically. **Escape
hatches:** `AT TIME ZONE '…'` in the SQL that needs local bucketing, or pin
`timezone=` in the DSN. Adopter check before repinning: grep the host for
`date_trunc|::date|to_char|AT TIME ZONE|EXTRACT` (gps-360-go: zero hits;
coordination-hub: the owner's to run).

**#9 — the small helpers (additive).**

- **`Collect[T any](ctx, db Querier, sql string, args ...any) ([]T, error)`**
  — the exported twin of `List`'s row collection: strict
  `RowToStructByName`, `MapError` on both the query and the collect error,
  empty non-nil `[]T` on no rows. Parent-bounded, unpaginated reads; not a
  paging primitive — that is `List`. Pairs with `crud.MapItems` on the wire.
- **`ProbeTables(ctx, db Querier, tables ...string) error`** — `ProbeTable` in
  a loop, stopping at and returning the first failure (already naming the
  relation when it is an absence; a `MapError`'d infrastructure failure is not
  re-wrapped as "about" a table).
- **`MapError`: SQLSTATE `22P02` (`invalid_text_representation`)** →
  `fmt.Errorf("%s: %w", pgErr.Message, sdk.ErrInvalidInput)` — the one case
  that keeps the server's message (`invalid input syntax for type uuid:
  "not-a-uuid"`), because that sentence is what a host's **log** loses today.
  It never reaches a client: `web.ErrFromDomain` is generic by design. The
  existing four cases keep returning bare sentinels.

Proof: hermetic tests on the extracted `poolConfig` (codec identity, decoding
through `pgtype.NewMap()`, the `timezone`/`PGTZ`/`PGOPTIONS`/`options=`
precedence with the libpq env cleared) and `TestLive_ScanUTC` /
`TestLive_Collect` against a throwaway Postgres 17 run under
`TZ=America/Los_Angeles` — every scan `+0000 UTC`, the `+05:00` literal equal
to `2025-12-31T19:00:00Z`, `SHOW TimeZone` = `UTC` by default and
`Europe/Oslo` when the DSN says so.

### integrations/datastores/pgxdb — v0.6.1 — tagged 2026-08-28: `ListQuery.FixedOrder`, a store-fixed composite ORDER BY for the offset strategy (patch; additive)

Plan of record `plans/pgxdb-list-fixed-order.md` (gopernicus #15; originating
host gps-360-go, which carried an `OffsetPage[R, D]` helper in nine stores
because `pgxdb.List` could not express `closing_date DESC NULLS LAST, name
ASC, id ASC`). Pin stays `sdk v0.4.0`; no schema; no sibling bumps.

**What.** One additive field, `ListQuery.FixedOrder string` — a store-authored
`ORDER BY` expression (without the keyword), trusted store text like
`BaseSQL`, written verbatim by the offset flow. The store includes its own pk
tiebreak; `List` appends nothing. When set:

- `OrderFields`/`DefaultOrder` are not consulted; `OrderFields` also set is a
  programming error → `sdk.ErrInvalidInput` on first call.
- A request carrying an `Order` → `sdk.ErrInvalidInput` (the order is not the
  caller's).
- The list is **offset-only**: the cursor strategy (default or explicit, or a
  cursor token) → `sdk.ErrInvalidInput` — no keyset predicate is derivable
  from an arbitrary expression.
- The offset flow is otherwise unchanged: `LIMIT n+1` → `HasMore`, `HasPrev`
  from the offset, `WithCount` → `Total` via the `COUNT(*)` wrap, the search
  clause folded before the strategy switch, `MapError`.

**Zero value.** A `ListQuery` without `FixedOrder` builds byte-identical SQL
to `v0.6.0` (pinned by test).

**Adopting (gps-360-go).** Replace each local `offsetPage` helper with
`pgxdb.List` + `FixedOrder` carrying the same expression, then delete
`rows.go`'s `offsetPage`. Same SQL shape, same page fields. Hosts serving
these lists over `web.ParseListQuery` should note a client-supplied `order`
now answers 400 through `ErrFromDomain` rather than being ignored.

Proof: hermetic SQL-capture tests (`TestList_FixedOrder*`) and the live
`TestLive_ListBehavior/fixed_order_offset` against a throwaway Postgres 17 —
`NULLS LAST` + name tiebreak traversal over three offset pages with
`HasMore`/`HasPrev`/`Total` asserted and the cursor strategy refused.

### pockets/authentication — v0.9.0 — tagged 2026-08-29: one composable authenticator, named helpers, the credential on the context (BREAKING, pre-1.0)

Plan of record `plans/authentication-principal-posture.md` (originating host
gps-360-go `plans/33-principals-api-keys-and-route-proof.md` D2). `sdk`
untouched; store modules untouched; no schema.

**The one authenticator.** `Service.RequirePrincipal(opts ...PrincipalOption) web.Middleware`
replaces the five fixed-name resolvers. Its options are OR-sets over credential
kinds and transports plus a liveness tier and a browser denial mode:

```go
Accept(kinds ...CredentialKind)  // OR-set of credentials; default: every wired kind
Transports(ts ...Transport)      // OR-set of transports;  default: header + cookie
Live()                            // access_token ⇒ the session row must exist; api_key ⇒ pass
Browser()                         // on denial 303 to Config.BrowserLoginPath instead of a JSON 401
```

Zero options = every wired credential, both transports, header authoritative,
stateless, JSON 401. `Accept()` / `Transports()` with zero arguments PANICS at
construction. A credential arriving on a transport outside the set is IGNORED,
never denied. Nested under an outer `RequirePrincipal`, an inner instance never
re-resolves: it narrows by reading the stashed `Credential` (a `Live()` inner
runs the session lookup once; a second nested `Live()` reads the already-proven
session id). The supported wiring invariant is **one authentication `Service`
per chain**.

**The migration table:**

| removed | replacement | note |
|---|---|---|
| `RequirePrincipal` (method value, `func(http.Handler) http.Handler`) | `RequirePrincipal()` / `RequireAccessTokenOrAPIKey()` | every `web.Middleware` call site gains `()` |
| `RequireUser` | `RequireAccessToken()` | **correction (security tightening):** a non-JWT bearer no longer falls through to the cookie — a key plus a valid cookie is now 401, where it previously passed as the cookie's user |
| `RequireServiceAccount` | `RequireAPIKey()` or `RequirePrincipal(Accept(CredentialAPIKey), Transports(TransportHeader))` | identical behavior with the transport spelled |
| `RequireLiveSession` | `RequireAccessTokenOrAPIKeyLive()` | identical |
| `RequirePrincipalBrowser` | `RequirePrincipal(Browser())` | identical |
| `RequireLiveSessionBrowser` | `RequirePrincipal(Live(), Browser())` | identical |
| `AuthenticateAPIKey(ctx, rawKey)` | no direct replacement | raw credential verification is not an application-service entry point; it moves behind `RequirePrincipal` — read the result with `CurrentPrincipal` / `CurrentCredential` |

**`Config.BundledRouteAuth`** exposes one optional `RoutePrincipalStrategy` per
bundled route group (built with `PrincipalStrategy(opts...)`). A zero field
keeps that group's audited default; a set field replaces only that group,
resolved once at `NewService` into concrete middleware. It configures
AUTHENTICATION only — it cannot unmount the authenticator from a protected
bundled surface, and it never substitutes for the separate host authorization
seams (`MachineRoutesGate`, `UserAdminCheck`, `InviteCheck`). `PrincipalStrategy()`
called with no options is an explicit choice of the primitive's defaults, NOT
"leave the audited default in place" — leave the field zero for that. Override
example (every other bundled route keeps its default):

```go
Config{
	BundledRouteAuth: BundledRouteAuthentication{
		Invitations: PrincipalStrategy(
			Accept(CredentialAccessToken),
			Live(),
		),
	},
}
```

**The bundled-route audit tightens what an API key reaches.** The old
`RequireLiveSession` admitted both access tokens and API keys uniformly; the
audited per-surface defaults do not:

| tightened (API key no longer admitted) | preserved (API key, incl. act-as-user, still admitted) |
|---|---|
| `GET /auth/delivery/status`, `/auth/methods`, `/auth/csrf` (`SessionSecurityReads` → `RequireAccessTokenLive()`) | `GET /auth/me` (`SessionHydration` → `RequireAccessTokenOrAPIKeyLive()`) |
| password/step-up/identifier/OAuth-unlink credential mutations (`CredentialManagement` → `RequireAccessTokenLive()`) | `/auth/admin/users…` (`UserAdministration` → `RequireAccessTokenOrAPIKeyLive()`) |
| service-account/key create/list/mint/revoke (`MachineLifecycle` → single `RequireAccessTokenLive()`, replacing the old `RequireUser` → `RequireLiveSession` two-authenticator stack) | authenticated invitation routes (`Invitations` → `RequireAccessTokenOrAPIKeyLive()`) |

The bundled HTML surface (`BrowserAccount`) reads its cookie only, requires a
live session, and redirects on denial — including the HTML-only
`POST /auth/identifiers/{id}` form route, which now 303s (instead of a JSON
401) on an authentication denial; the HTML denial honors `Config.BrowserLoginPath`
and carries a validated `path?query` `return_to`.

**Host repin recipe:**

```
go mod edit -require github.com/gopernicus/gopernicus/pockets/authentication@v0.9.0
go mod tidy
```

**gps-360-go's one call site:**

```
router.Group("/api/v1", authenticationSvc.RequirePrincipal, …)
```
becomes
```
router.Group("/api/v1", authenticationSvc.RequireAccessTokenOrAPIKey(), …)
```

No tag exists yet — the owner cuts `v0.9.0`.

### pockets/authentication — v0.8.2 — tagged 2026-08-28: identity.Resolver is principal-exact (patch)

Plan of record `plans/identity-resolver-principal-exact.md`. `Resolve` for a
`user` principal projects the stored `DisplayName` exactly — blank stays blank;
the email-local-part fallback and its helpers are gone. Identifier matching (an
email value → a user) never enters `Resolve` (`TestResolveUserIsPrincipalExact`).
No symbol added or removed from the public surface; no schema; no pin moves.

**Upgrade note.** If your host renders `Info.DisplayName` without a guard, a
user with a blank stored name now renders "" instead of the email local part.
Decide at the render site: refuse (a host stamping the name into a durable
record should 403), show an address, or show nothing. Do not re-add a guess
in a shared Resolver decorator.

### pockets/authentication — v0.8.1 + features/authentication — v0.4.3 — tagged 2026-08-28 (maintenance line off v0.4.2): host-owned mail data, subjects, and SMS bodies (patch; additive)

Plan of record `plans/auth-mail-host-data-and-subjects.md`. A **patch on both
lines** by plan ruling: every symbol is additive, the zero value of every new
`Config` field renders today's output byte-for-byte (pinned by golden test
against the pre-release render), no schema, no store retags, and **no pin
moves** — the `v0.4.3` maintenance commit is the `v0.4.2` tree plus this change
(still `sdk v0.4.0` in its `go.mod`, so a host on `sdk v0.4.1` +
`stores/pgx v0.3.0` upgrades exactly one requirement), and `v0.8.1` stays on
`sdk v0.6.0`.

- **`Config.DeliveryData DeliveryDataHook`** — `func(ctx, purpose, data)
  (additions, error)`, run once per render for every purpose on both rails,
  before the subject/body/SMS templates. Input is a fresh, secret-free copy
  (nested `Metadata` copied); the return merges before `Secret`/`Subject` are
  inserted. **Reserved:** returning `Secret`, `Link`, or `Subject` is
  `ErrDeliveryDataReserved` (wraps `sdk.ErrInvalidInput`) and no envelope is
  built. Error surfacing is per flow (README "Mail content"): invitation
  create/resend return it (create with the already-persisted record), the
  member-added notice logs it (grant already committed), opaque starts follow
  the worker's bounded retry → dead-letter and never report it to the caller.
- **`Config.EmailSubjects` / `Config.SMSBodies map[string]string`** — purpose →
  `text/template` source, parsed at construction with `missingkey=error`.
  `ErrDeliveryOverrideInvalid` at `NewService`/`Register` for an unknown
  purpose, empty source, parse failure, or an SMS entry for an email-only
  purpose. `ErrDeliverySubjectInvalid` at render for an empty or CR/LF-carrying
  subject, before anything is queued.
- **`Purpose*` constants exported** from the public package (ten, aliasing the
  internal delivery set) — key the maps and switch in the hook without string
  literals.
- **Invitation / member-added data enriched** — `InvitationID`, `OperationID`,
  `ResourceType`, `ResourceID`, `ResourceName` (`""`), `ResourceKind` (`""`),
  `Relation`, `RelationLabel` (`""`), `InvitedBy`, `InviterName` (`""`),
  `Metadata` (non-nil copy), `Link`. Direct add: `InvitationID` empty,
  `OperationID` = the minted grant operation ID; accept/pending: both = the row
  ID. The bundled bodies and SMS bodies now render
  `{{or .ResourceName .ResourceID}}` — identical output while the name is
  empty, so a hook that sets only `ResourceName` is immediately useful.
- **One internal render-order change, no observable output change:** the
  subject template now executes on the email rail only (an SMS render never
  ran one to any effect), and a caller-supplied `Data["Secret"]` no longer
  shadows `Request.Secret` (no caller ever did).

**Adopting (coordination-hub):** bump `features/authentication v0.4.2 →
v0.4.3` only; set `cfg.DeliveryData` to look up the campaign name and return
`{"ResourceName": name}` for `auth.PurposeInvitation` / `auth.PurposeMemberAdded`;
optionally `cfg.EmailSubjects[auth.PurposeInvitation]`. No template override is
needed for the name to appear.

### pockets/authentication — v0.8.0 (next tag, 2026-08-27): list routes answer the parser's sentence; pin → `sdk v0.6.0` (minor)

Plan of record `plans/web-crud-list-request.md` (D3). A **minor**: the pin
moves to a sibling minor (`sdk v0.5.0 → v0.6.0`), and one wire body changes.
No schema, no store retags (`stores/{pgx,turso}` keep their `authentication
v0.7.0` / `sdk v0.5.0` pins and upgrade at the host via MVS).

- **Wire-body change on the five list routes** (`GET /auth/admin/users`,
  `/auth/service-accounts`, `/auth/service-accounts/{id}/keys`,
  `/auth/invitations/mine`, and the resource-scoped invitation list): a
  malformed `limit` / `cursor` / `offset` / `count` / `order` still answers
  **400 `bad_request`**, but the `message` is now the parser's own sentence —
  `"rows value too small, must be larger than 0: invalid input"`,
  `"cursor and offset are mutually exclusive: invalid input"`,
  `"unknown order field: nope: invalid input"` — instead of the fixed
  `"invalid page parameters"` / `"invalid order parameter"`. Status and
  `code` are unchanged; a client matching those two fixed strings must stop.
- Internally the pocket's `parseListRequest` is now the two framework calls
  (`crud.ParseListQuery` with `Config.ListStrategy` as `DefaultStrategy`, then
  `crud.ParseOrder`) + `web.ErrValidation` — de-duplication, and the pocket
  stops being the one caller that discarded the sentence. Only `q` is read for
  search (no legacy `s`), as before.

### 2026-08-27: the `features/` tier is `pockets/` (module-path rename; ONE train of 19 tags)

Plan of record `plans/rename-features-to-pockets.md`. The third tier is renamed
so a host app's own *feature* ("invite a teammate") stops colliding with the
framework's word for the unit it composes. This is a **module-path rename** —
an identity change for seventeen modules — plus the composer package move. No
behavior, schema, route, config, or wire change anywhere; no migration.

**The composer moved:** `github.com/gopernicus/gopernicus/sdk/feature` is now
`github.com/gopernicus/gopernicus/sdk/pocket`. Type and function names are
unchanged (`pocket.Mount`, `pocket.Group`, `pocket.PrefixRegistrar`,
`pocket.RouteRegistrar`). There is **no alias shim** — `sdk/feature` is gone.

Every module keeps its own version lineage and takes a minor bump on the new
path, so `git tag -l` reads as one continuous story per module:

| Module (new path) | Last tag on old path | First tag on new path |
|---|---|---|
| `sdk` | v0.4.2 | **v0.5.0** |
| `pockets/authentication` | v0.6.0 | **v0.7.0** |
| `pockets/authentication/stores/pgx` | v0.4.0 | **v0.5.0** |
| `pockets/authentication/stores/turso` | v0.3.0 | **v0.4.0** |
| `pockets/authentication/views/goth` | v0.2.2 | **v0.3.0** |
| `pockets/authorization` | v0.5.0 | **v0.6.0** |
| `pockets/authorization/stores/pgx` | v0.2.0 | **v0.3.0** |
| `pockets/authorization/stores/turso` | v0.1.0 | **v0.2.0** |
| `pockets/cms` | v0.1.0 | **v0.2.0** |
| `pockets/cms/stores/pgx` | v0.2.0 | **v0.3.0** |
| `pockets/cms/stores/turso` | v0.1.0 | **v0.2.0** |
| `pockets/cms/views/goth` | v0.1.0 | **v0.2.0** |
| `pockets/events` | v0.1.0 | **v0.2.0** |
| `pockets/events/stores/pgx` | v0.2.0 | **v0.3.0** |
| `pockets/events/stores/turso` | v0.1.0 | **v0.2.0** |
| `pockets/jobs` | v0.2.0 | **v0.3.0** |
| `pockets/jobs/stores/pgx` | v0.3.0 | **v0.4.0** |
| `pockets/jobs/stores/turso` | v0.2.0 | **v0.3.0** |
| `workshop/gopernicus` | v0.1.0 | **v0.2.0** |

`ui/goth` and every `integrations/*` module are **not** retagged: their module
paths and runtime behavior do not change. `workshop/gopernicus` does join the
train — its CLI removes `gopernicus new feature`, adds `gopernicus new pocket`,
and embeds renamed templates, so `go install …/workshop/gopernicus@latest` would
otherwise keep distributing the retired verb.

Old tags stay. They are immutable history and still resolve at the old path
(the proxy serves `features/cms@v0.1.0` from the commit its tag points at). No
consumer is force-moved by this train — a consumer moves when it repins.

**Adopter action (source-breaking at the moment you repin).** Consumers that
use a local `replace` to this checkout break the instant `features/` moves on
disk, regardless of tags — the path edit below is not optional for them. Run
the sequence in every `go.mod` of the consumer:

```sh
# 1. module/import paths (tracked Go + templ sources)
sed -i '' -e 's#gopernicus/gopernicus/features/#gopernicus/gopernicus/pockets/#g' \
          -e 's#gopernicus/gopernicus/sdk/feature"#gopernicus/gopernicus/sdk/pocket"#g' \
          go.mod $(git ls-files '*.go' '*.templ')

# 2. local replace RHS paths — step 1 changes the module/LHS but deliberately
# does NOT match /abs/.../gopernicus/features/... or ../gopernicus/features/...
sed -i '' -e 's#/features/#/pockets/#g' go.mod

# 3. `feature.` → `pocket.` at composer call sites; rename tier-derived aliases
# such as eventsfeature too (compile does not catch a still-valid old alias).
# 4. versions per the table above, then tidy/resolve.
go mod edit -json
go mod tidy
go list -m -json all
```

Acceptance for a local-replace consumer: neither a replacement `Path`/RHS nor
any resolved module `Dir` contains `/features/`.

### features/authorization — v0.5.0 (next tag): `Config.Model` removed (breaking, pre-1.0)

Owner ruling 2026-08-26, minutes after v0.4.0: "drop config.model now — let's just
make it correct now." The one-release pass-through is withdrawn: `Config.Model` is
GONE, `Config.RelationshipModel` is the only name, and `ErrConfigConflict` (which
existed solely to referee the two) is removed with it. Construction order is back to
zero kinds (`ErrNoKindConfigured`) → `ErrModelRequired`. Nothing else changes —
`RelationshipModel` alias, `ErrNoDecisionKind` at 500, and every v0.4.0 behaviour
stand. The `stores/pgx` upgrade-runbook test literal moved to `RelationshipModel`
(in-workspace only; the store's own pin is unchanged and adopters never compile a
store's tests).

**Adopter action (breaking):** rename `Model:` → `RelationshipModel:` in every
`authorization.Config` literal; remove any `errors.Is(err, authorization.ErrConfigConflict)`
branch. The compiler finds both. Known hosts: `examples/auth-cms` (done in-repo),
coordination-hub and gps-360-go (owner-updated downstream). No store change; stores
not retagged.

### features/authorization — v0.4.0 — tagged 2026-08-26: `Config.RelationshipModel` + `ErrNoDecisionKind` is a 500 (minor; one-release deprecation)

Plan of record `plans/authorization-roles-model-followups.md` — the two
owner calls left open at the v0.3.0 close, released BEFORE gps-360-go adoption so
the handoff targets the final names.

**1. `Config.Model` → `Config.RelationshipModel`.** The relationship kind's model
now says so, symmetrically with `Config.RoleModel`; `RelationshipModel` is also a
type alias of `Schema` so the two kinds read alike at the wiring site. `Config.Model`
STAYS as a deprecated pass-through and still wires the relationship kind exactly as
before. Setting BOTH (each with a non-empty `ResourceTypes`) is the new
`ErrConfigConflict` (wrapping `sdk.ErrInvalidInput`) — the feature refuses to guess
which policy governs rather than letting one silently win. Construction check order
is pinned: zero kinds (`ErrNoKindConfigured`) → `ErrConfigConflict` →
`ErrModelRequired`. The `RequirePermission*` mount panics keep their shape and name
`Config.RelationshipModel` in the message.

**Deprecation window — as tagged.** `Config.Model` was deprecated as of **v0.4.0**
with a v1.0 removal promised; that window was withdrawn by owner ruling the same
day — v0.5.0 removes it (see the entry above).

**2. `ErrNoDecisionKind` answers 500, not 400.** It no longer wraps
`sdk.ErrInvalidInput`: a decision surface with NO model-bearing kind is a
SERVER-SIDE WIRING FAULT, not something the caller said, so it wraps no sdk
taxonomy kind and lands on `web.ErrFromDomain`'s default — HTTP **500** with the
generic internal body — consistent with the three gates, which panic at mount for
exactly this wiring. It is deliberately UNLIKE `ErrMutationsNotConfigured`, which
stays **400**: that one is a precondition an actor can observe on a correctly
deployed host. Consequences: `ReasonFor(ErrNoDecisionKind)` now returns
`ok=false` (no decision reason — the caller treats it as infrastructure), and
`errors.Is(err, sdk.ErrInvalidInput)` no longer matches it. The error identity and
message are otherwise unchanged (it still does NOT wrap
`ErrRelationshipsNotConfigured`), and it is still never a 403.

**Compatibility (v0.3.0 → v0.4.0).** Additive `Config.RelationshipModel` +
`RelationshipModel` alias + `ErrConfigConflict`; `Config.Model` deprecated (still
honoured); `ErrNoDecisionKind` no longer satisfies `errors.Is(err,
sdk.ErrInvalidInput)` and answers 500 (only roles-only-without-model hosts see it
at all). No store change.

**No store change.** No port, DDL, migration, or store-tag movement:
`features/authorization/stores/{pgx,turso}` are **NOT retagged**. Their pinned
`features/authorization` requirement is untouched, so a store module still builds
against the field name it was tagged with.

### features/authorization — v0.3.0 (next tag): the roles kind's model + one decision surface (minor, predominantly additive)

Plan of record `plans/authorization-roles-model.md` (gopernicus issue #5;
originating host gps-360-go, which hand-wrote the entire roles decision half).
The roles kind gains the model it never had, and the decision surface stops being
relationship-kind-only. **A host that sets no `Config.RoleModel` is behaviourally
unchanged** — same decisions, reasons, traces, and zero-length values.

**The seam.**

- **`Config.RoleModel`** — one new field, `RoleModel{ResourceTypes map[string]RoleTypeDef}`
  with `RoleTypeDef{Roles []string; Permissions map[string][]string}` (permission →
  the roles that grant it). Registered data, no migration, validated at
  `NewService`: `ErrRoleModelWithoutRoles` when set without `Repositories.Roles`,
  `ErrInvalidRoleModel` when structurally invalid, `ErrModelConflict` when a
  `(resource type, permission)` pair is declared by BOTH `Config.Model` and
  `Config.RoleModel`.
- **One decision surface, dispatched by pair ownership.** `Check`, `CheckBatch`,
  `CheckExplain`, `FilterAuthorized`, `LookupResources` and
  `RequirePermission`/`RequirePermissionOn`/`RequirePermissionFixed` now route each
  pair to the model that DECLARES it. `Config.Limits` is that surface's budget: it
  is resolved (and a negative field rejected with `ErrInvalidLimits`) whenever any
  model-bearing kind is wired, `MaxBatchSize`/`MaxLookupResults` are charged on a
  roles+model host too, and `MaxGraphStates` additionally bounds the role-assignment
  walk so an adversarial assignment count is `ErrEvaluationLimit`, never an
  open-ended store walk.
- **New exported symbols:** `RoleModel`, `RoleTypeDef`, `Config.RoleModel`,
  `LookupResult.Unrestricted`, `ExplainStep.Role`/`.Scope`, `ExplainKindRole`,
  `ExplainScopeDirect`, `ExplainScopeGlobal`, `ErrInvalidRoleModel`,
  `ErrModelConflict`, `ErrRoleModelWithoutRoles`, `ErrNoDecisionKind`.
- **`LookupResult.Unrestricted`** reports that a granting role is held GLOBALLY, so
  the principal reaches every resource of the type and `IDs` is empty. Only the
  roles kind ever sets it. It is additive because it is fail-closed under
  ignorance: a caller reading only `IDs` shows an unrestricted principal an EMPTY
  page — restrictive, never permissive. `IDs` stays ALWAYS non-nil.
- **`Register`'s log line** gains `role_model=<bool>` — a bool only, never
  resource-type or role names; policy vocabulary stays out of logs.

**The one error-identity change — `ErrNoDecisionKind`.** On a host where NO kind
bears a model (a roles-only wiring with no `Config.RoleModel` — gps-360-go today),
these five calls return the new `ErrNoDecisionKind` where they previously returned
`ErrRelationshipsNotConfigured`: **`Check`, `CheckBatch`, `CheckExplain`,
`FilterAuthorized`, `LookupResources`.** Nothing else moves — every other
relationship-kind method still returns `ErrRelationshipsNotConfigured`, and the
roles-kind methods still return `ErrRolesNotConfigured`. The new sentinel is a
clean identity (it does NOT wrap `ErrRelationshipsNotConfigured`) and wraps
`sdk.ErrInvalidInput`, so like its `ErrMutationsNotConfigured` precedent it is a
stable precondition refusal that maps to **HTTP 400** through the `web.Error`
seam — never a deny, never 403, never `ErrUnavailable`. *(Changed to 500 in
v0.4.0: the sentinel dropped its `sdk.ErrInvalidInput` wrap and is now classed as
a server-side wiring fault — see that entry above.)* A host branching on
`errors.Is(err, ErrRelationshipsNotConfigured)` for those five calls must add the
new sentinel; no host in `examples/` or gps-360-go did (grep, 2026-08-26). The
three gates still panic at mount on that wiring, with a **new message**:

```
authorization: RequirePermission… requires a decision-capable kind (Config.Model
or Config.RoleModel); a roles-only host without a role model must not mount it
```

*(In v0.4.0 the message names `Config.RelationshipModel` instead of `Config.Model`.)*

**No cross-kind universal bypass — deliberately.** The feature declares no
`Superuser`/`IsSuperuser` primitive and performs no cross-kind union. A globally
assigned role grants exactly the role-owned pairs whose grantor lists EXPLICITLY
name it, and acquires neither relationship-owned permissions nor permissions added
to the model later. An application flag that must bypass every present and future
decision (gps-360-go's `ManageAuthorization`, auth-cms's `isPlatformAdmin`) stays
in host composition, run before the surface, and owns the widening. A host
transcribing an existing steward/admin role must therefore list it on every
permission it should grant.

**Assign-time model validation (new, model-gated).** With a `RoleModel`
configured, assigning a `(resource type, role)` pair the model does not declare is
refused with `ErrInvalidRoleModel` (wrapping `sdk.ErrInvalidInput`) — a scoped
assignment needs the role in that type's `Roles`, a global one needs it declared by
some type. It runs inside the mutation repository's boundary as part of the
receipt-absent semantic validator, so `Service.AssignRole`,
`SystemMutator.AssignRole`, and generic `SystemMutator.Apply(OpRoleAssign)` cannot
disagree or bypass it, while an exact replay still returns its stored receipt after
a later model change dropped the role. **`UnassignRole` and every read path stay
opaque**: existing rows need no migration and remain listable and removable. Hosts
with no model are untouched. The typo that used to be a permanently silent no-grant
is now loud at assign time.

**Source-compatibility caveat.** `Config`, `ExplainStep`, and `LookupResult` gain
exported fields. Keyed literals are unaffected and no unkeyed literal of any of the
three exists in-repo or in gps-360-go (grep 2026-08-26; `go vet` checks composites
in the verify set) — but, as with any exported Go struct field addition, an unknown
downstream **unkeyed** literal of those three types must become keyed.

**No store change.** The engine runs on the EXISTING `role.Storer`
(`HasExactRole` + the paged `ListBySubject` walk): no port, DDL, migration, or
store tag change, so a host adopts the model by bumping ONE module.
`features/authorization/stores/{pgx,turso}` are NOT retagged. **Follow-up (D6,
recorded not built):** store-side `RolesHeld(subject, type, id)` and
`ListBySubjectForResourceType` probes, a `storetest` contract for them, and a
store train. Trigger: `ErrEvaluationLimit` from the assignment walk in a real
host, or measured gate latency.

**`storetest`** gains `Roles/Decision`, a `Parity/Roles` arm, and a `Composed`
family (skipped unless both kinds are wired) — all in the core module, no store
module bump needed to run them. Both live dialect legs ran for this train
(pgx on a throwaway `postgres:17` container; turso on the authorized playground —
the turso suite is now ≈12 min end to end, so pass `-timeout 20m`). The next
`stores/{pgx,turso}` repin onto v0.3.0 runs the three new families against the
live dialects automatically.

### features/authorization — v0.2.0 — tagged 2026-08-26: RequirePermission in coordinates (minor, additive)

`Service.RequirePermissionOn(resourceType, permission, pathParam)` and
`Service.RequirePermissionFixed(resourceType, permission, resourceID)` join
`RequirePermission(permission, resolver)`, which is unchanged. A route line now
reads as its own authorization question:

```go
r.GET("/orgs/{orgID}/people", h.people, svc.RequirePermissionOn("org", "view", "orgID"))
r.POST("/campaigns", h.create, svc.RequirePermissionFixed("platform", "admin", "main"))
```

The coordinates are **load-bearing and checked at registration** against the
compiled model: a `(resourceType, permission)` pair the schema does not declare,
an empty parameter name, or an empty fixed id panics when the route is mounted —
never a gate that quietly checks something the model never grants. Request
semantics are `RequirePermission`'s (401 / 403 / 500 / 503, fail closed);
`PathResource(resourceType, param)` is exported for hosts composing their own
gates, and an empty path value is a resolver error (500 — the honest answer to a
parameter name that does not match the route pattern). Same relationship-kind
precondition as `RequirePermission` (panics at mount on a roles-only host).
Origin: gps-360-go / coordination-hub's per-route authorizer style (owner
ruling, 2026-08-26).

### integrations/datastores/pgxdb v0.5.0 + every feature `stores/pgx` — tagged 2026-08-26: WithSchema — host-chosen schema instead of a search_path pin (minor across all six; ONE train)

Plan of record `plans/pgx-store-schema-option.md` (gopernicus issue #4;
originating host gps-360-go, which shares another app's Postgres and today
pins `search_path` in its DSN). Everything is additive and the default is
byte-for-byte unchanged: a host that passes no option gets exactly the SQL,
probes, and ledger statements it always had. Target tags:
`integrations/datastores/pgxdb/v0.5.0`,
`features/authentication/stores/pgx/v0.4.0`,
`features/{authorization,cms,events}/stores/pgx/v0.2.0`,
`features/jobs/stores/pgx/v0.3.0`.

**pgxdb — the seam.**

- **`pgxdb.Schema`** — a validated value type. `pgxdb.NewSchema(name)
  (Schema, error)` accepts a single bare identifier (letter, then
  letters/digits/underscores; ≤ 63 bytes; not `pg_*`, not
  `information_schema`; `public` is valid) and wraps `sdk.ErrInvalidInput`
  otherwise. `(Schema).Table(t)` renders `"<schema>".t`; the **zero `Schema`
  renders the bare name**, which is what makes the default byte-identical.
  Quoting preserves case: `Auth` and `auth` are different schemas. Build it
  once at the host (from env/config) so a malformed name fails there — no
  store option panics. It is the same seam for a host's own app-local pgx
  repositories: render your table names through `Schema.Table` and they
  follow the same option.
- **`pgxdb.RunMigrations(ctx, db, fs, dir, opts ...MigrateOption)`** gains
  `pgxdb.WithSchema(s)`. Inside the existing migration transaction it probes
  the namespace, runs `CREATE SCHEMA IF NOT EXISTS` only when absent, checks
  the role has `USAGE` and `CREATE` on it, then `SET LOCAL search_path TO
  "<schema>"` — **strict, no `public` fallback** (with `public` on the path an
  `ALTER TABLE users` whose `"auth".users` is missing would silently alter the
  host's `public.users`) — and asserts `current_schema()`. Unqualified DDL in
  the exported stream therefore lands in the schema; `ExportMigrations` is
  untouched. The ledger (`schema_migrations`) is **explicitly qualified** and
  lives in the schema. Grant failures name the missing grant and wrap
  `sdk.ErrForbidden`: schema creation needs `CREATE ON DATABASE` (a
  DBA-precreated schema is a real workaround — creation is skipped when the
  namespace exists); applying needs `USAGE, CREATE ON SCHEMA`.
- **STREAM MODEL CHANGE (ratified, YOUR CALL 4).** The runner's godoc used to
  say "one database, one stream … calls `RunMigrations` once per database".
  It now says **one stream per schema, each with its own ledger in its own
  schema**. The #4 use case — feature tables in `auth`, host tables in
  `public` — is two calls over two directories (`migrations/auth/`,
  `migrations/`). Consequences a host must own: the call order is yours and
  must be deterministic; **one call is one transaction and two streams are
  two transactions — there is NO cross-schema atomicity** (if the first
  commits and the second fails, fix the failing stream and rerun; each
  committed stream is idempotent by its own ledger); cross-schema FKs/views/
  functions must be explicitly qualified and applied after the stream they
  depend on. Filename uniqueness is now per `(schema, source)`.
- **Ledger relocation for hosts coming off a `search_path` pin.** Under
  `search_path=auth,public` your ledger rows may live in
  `public.schema_migrations` while your tables live in `auth`. Adopting
  `WithSchema("auth")` then sees an empty `"auth".schema_migrations` and
  **re-runs the stream**. Most files are `IF NOT EXISTS`-safe but
  authentication `0014_user_status.sql` is a full-table `ALTER COLUMN … TYPE
  … COLLATE "C"` rewrite under an exclusive lock and `0015` repeats a backfill
  `UPDATE`. Run the **ledger-relocation preflight** in the pgxdb README
  (explicit column list, `ON CONFLICT (source, version) DO NOTHING`, a
  count/checksum assertion before `COMMIT`) BEFORE the first schema-scoped
  call; the runner then reports every copied file already applied. Skipping
  it is safe-but-expensive, not recommended.
- Do **not** merge the rate limiter's `ratelimit_windows` DDL into a
  schema-scoped stream — the limiter's SQL is unqualified and it would fail at
  its own probe. A limiter schema option is a separate demand.
- pgx's default statement cache is 512 entries per connection; a
  schema-per-tenant host running three-plus store sets on one pool should
  size `statement_cache_capacity` in the DSN or use one pool per schema.

**Every feature `stores/pgx` — the option.**

- Each package gains `type Option func(*config)` and `WithSchema(s
  pgxdb.Schema) Option` (authorization's existing `Option` gains it beside
  `WithGuardianPolicy`). It is accepted by `Repositories(db, opts...)` AND by
  **every exported per-repository constructor** — authentication's 18
  `NewXStore(db, opts...)`, cms's five, jobs' three, events' `New(db,
  opts...) (*Store, error)`, authorization's `RelationshipRepository(db,
  opts...)` — so a host composing its own repository set from individual
  stores gets the same seam. Every table reference renders through one
  unexported `table(name)` chokepoint per store, guarded by a per-package
  `TestNoBareTableReferences` (go/parser over string literals, table set
  derived from the embedded migrations).
- **Boot probes.** authentication/authorization/events replace their private
  `probeTable` with `pgxdb.ProbeTable` on the qualified name (the adoption the
  v0.4.1 note deferred to "each store's next tag"); wrapping messages still
  name the migration source and `errors.Is(err, sdk.ErrNotFound)` holds.
  authentication's ALTER-column probe filters `information_schema.columns` on
  `table_schema` **only when a schema is set** — the default probe stays
  byte-identical (and, as before, unfiltered by schema).
- **cms and jobs gain an additive `StatusCheck(ctx, db, opts ...Option)
  error`** that probes their tables under the configured schema. Call it at
  boot when you configure a schema: those packages have no probe otherwise,
  and a mismatched schema (migrated into one, constructed with another, or
  migrated with a schema and constructed without) would silently read and
  write your own `public.entries` / `public.terms` / `public.assets` /
  `public.menus` / `public.job_queue`. `Repositories` itself stays
  probe-less and error-less.
- **jobs compatibility consequence (YOUR CALL 2).** `QueueOption` is now a
  type ALIAS of the package `Option` (`func(*config)`, config unexported);
  `WithLease` returns `Option` and keeps its non-positive-ignored rule;
  `NewScheduleStore` / `NewFencedQueueStore` accept options and document that
  lease is ignored. A caller that only passes `WithLease(d)` compiles
  unchanged. **A caller that constructs, converts, invokes, returns, or wraps
  its own `QueueOption` as `func(*Queue)` breaks** — accepted at v0.x. The
  option type is now named three ways across the jobs adapters (memstore
  `Option`, pgx `Option` + alias, turso `QueueOption`); recorded, not fixed
  (a turso rename would cost turso retags this train avoids).
- Advisory locks (`pg_advisory_xact_lock` in authorization and the fenced
  jobs queue) are database-scoped, not schema-scoped: two hosts sharing one
  database AND the same lock-key text contend across schemas. Not a
  regression — true under the `search_path` pin too.
- Per-repository DIFFERENT schemas within one feature are out of scope: the
  constructors mechanically allow it, the one-stream-per-schema migration
  model gives it no story. `stores/turso` is untouched — SQLite has no
  schemas; the package docs that claimed "same exported surface" as the turso
  sibling now say "plus the Postgres-only `WithSchema` option".

**Pin moves and the sdk floor.** Every store pins `pgxdb v0.5.0`
(authentication from v0.4.0; authorization/cms/events/jobs from v0.1.0).
pgxdb requires `sdk v0.4.0`, so MVS drags `sdk` along — authorization/cms/
events move from `sdk v0.1.0`, jobs from `v0.2.0`. **Adopting any of these
four store tags raises your effective `sdk` floor to v0.4.x — read the sdk
v0.3.0 note (global middleware genuinely global; `HandleRaw` no longer
bypasses it) before adopting.** Compile-safety of the drag was cold-verified
(`GOWORK=off` builds of the four feature cores against sdk v0.4.2). No
feature-core, sdk, or turso module is bumped.

**Adoption (gps-360-go shape).** Delete the `search_path` DSN wrapper, then:

```go
authSchema, err := pgxdb.NewSchema(os.Getenv("AUTH_DB_SCHEMA")) // "auth"
if err != nil { log.Fatal(err) }

// Two streams, two calls, host-chosen order; no cross-schema atomicity.
if err := pgxdb.RunMigrations(ctx, db, hostMigrationsFS, "migrations"); err != nil { … }
if err := pgxdb.RunMigrations(ctx, db, featureMigrationsFS, "migrations/auth", pgxdb.WithSchema(authSchema)); err != nil { … }

authRepos, err := authenticationpgx.Repositories(db, authenticationpgx.WithSchema(authSchema))
iamRepos, err := authorizationpgx.Repositories(db, authorizationpgx.WithSchema(authSchema))
jobRepos := jobspgx.Repositories(db, jobspgx.WithSchema(authSchema))
if err := jobspgx.StatusCheck(ctx, db, jobspgx.WithSchema(authSchema)); err != nil { … } // cms likewise
```

If your ledger rows live in `public.schema_migrations`, run the README's
relocation preflight first. Verification for this train: `make check` green
(18 guards); `make test-stores` now runs every pgx leg twice (default and
`POSTGRES_TEST_SCHEMA=gopernicus_schema_test`) plus the pgxdb connector's own
live suite, and the schema legs of authorization and events were re-run
against an EMPTY `public` so any unqualified statement would have raised;
decoy tests in events and authentication prove a `public` twin of the same
table is neither read nor written when a schema is configured.

### features/authentication — v0.6.0 (next tag): the lifecycle routes come standard behind a host gate (minor; MachineRoutesDisabled REMOVED — a source break; ungated routes no longer mount)

authentication-machine-routes-gate (plan of record
`plans/authentication-machine-routes-gate.md`, gopernicus issue #6). A **minor**:
an intended behaviour change — the bundled machine-identity lifecycle routes no
longer mount without a host authorization gate — plus one removed `Config` field.
It supersedes the v0.5.4 caveat below, which stays as ledger history.

- **REMOVED: `Config.MachineRoutesDisabled bool`** (and its
  `authsvc.Deps.MachineRoutesDisabled` / `Service.MachineRoutesEnabled()`
  plumbing). A **compile break** for any keyed literal that sets it; the one
  known caller is gps-360-go `cmd/server/authentication.go:50`, a single line it
  deletes while adopting the gate — nil `MachineRoutesGate` is the same posture.
  Removed one day after it shipped rather than deprecated: "off even if a gate is
  set" is a second way to spell nil, and the pre-1.0 rule the owner applied to
  authorization's `Config.Model` applies here. **`AUTH_MACHINE_ROUTES_DISABLED`
  becomes an inert environment variable** — a deployment that still sets it is
  silently ignored (same off-state), so drop it from your manifests.
- **NEW: `Config.MachineRoutesGate web.Middleware`.** Nil → `/auth/service-accounts`,
  `/auth/service-accounts/{id}/keys` and `/auth/api-keys/{id}/revoke` are **NOT
  mounted** (404, deny-by-absence) even with `Repositories.ServiceAccounts` +
  `APIKeys` wired, and `NewService` logs one WARN naming the field. Key
  AUTHENTICATION is untouched: `AuthenticateAPIKey`, `RequireServiceAccount` and
  the bearer path of `RequirePrincipal` still follow `MachineEnabled`. Non-nil →
  each of the five routes registers as **`RequireUser` → `RequireLiveSession` →
  (on the three POSTs) the browser-safe `Origin`/CSRF gate → the gate**
  (outermost first): the human credential class only — an API-key
  bearer, act-as-user or not, is **401**, so a key can never mint another key —
  then immediate revocation (a logged-out session is 401 within one round-trip,
  the invitation precedent), then — for a cookie caller on a mutation — the CSRF
  rung described below, then the host's policy, which writes its own denial
  (`features/authorization`'s gates write FS9 `permission_denied` 403; the
  bundled handlers write no 403 of their own). Typical value:
  `authorizer.RequirePermissionFixed("platform", "steward", "global")`. Set with
  the machine subsystem unwired (BOTH repositories nil — a half-wired pair fails
  earlier with `ErrMachineReposRequired`) → the new
  **`ErrMachineRoutesGateWithoutRepos`** at construction. Deliberately a
  SINGULAR `web.Middleware`, not the
  `[]web.Middleware` of `cms.Config.AdminMiddleware`: nil must mean "no policy,
  do not mount", where an empty slice would mean "mounted, ungated" — the very
  bug this field closes.
- **Request DTO — `POST /auth/service-accounts`.** `owner_user_id` is **refused
  by name with a 400** ("owner_user_id is no longer accepted; the caller is the
  owner, or name act_as_user_id"); an EMPTY value is ignored. The field is kept
  for **one release** so a v0.5.x client learns the rename instead of a generic
  decode error, then dropped — after which strict decode answers 400 for it
  anyway. Its replacement is **`act_as_user_id`**: empty with `act_as_user` → the
  caller owns the account (act-as-self names no id); non-empty → delegation to
  another user, allowed only because the route sits behind the gate.
  `act_as_user_id` without `act_as_user` → 400; an unknown `act_as_user_id` → 400
  `invalid reference` (the service now verifies the act-as owner EXISTS —
  previously a typo minted a live principal for a subject nobody could
  deactivate; an unwired user rail fails closed with `ErrIdentityUnavailable`).
  Create and mint tighten their request hardening. The unknown-field 400 is NOT
  new (v0.5.x already decoded with `DisallowUnknownFields`); what changes is the
  **1 MiB body cap (413)**, **trailing data after the JSON value → 400**, and a
  **required `Content-Type: application/json` → 415** otherwise. **Response DTOs
  are byte-stable** — `owner_user_id` stays in the response, mint still returns
  `key` (now with `Cache-Control: no-store`, like every other secret-bearing
  response), revoke still answers 200 `{"status":"revoked"}`.
- **CSRF — cookie clients must double-submit on the three POSTs.** Create, mint
  and revoke now carry the same browser-safe `Origin`/CSRF gate as
  `/auth/admin/*`: a cookie-authenticated caller reads `GET /auth/csrf` and
  echoes the `__Host-auth_csrf` value in `X-CSRF-Token`, from an origin in
  `Config.AllowedOrigins`. A missing or mismatched token is **403
  `permission_denied`**; a non-allowlisted origin is **403 `origin_rejected`** —
  both BEFORE the host's gate runs. **Bearer-only clients are unaffected** (the
  gate short-circuits when there is no session cookie), and the two GETs are
  body-less reads that never carry it. A cookie-driven admin UI built against
  v0.5.x must add the bootstrap+echo step or it will see 403s.
- **Adopter audit — ghost owners written under v0.5.x.** The new act-as owner
  existence check covers NEW writes only; `userDeactivated` still reads an
  unknown id as ACTIVE, so any act-as account created with a bogus
  `owner_user_id` before this tag still mints live principals nobody can
  deactivate. Before deploying, list them and revoke their keys:

  ```sql
  SELECT id, owner_user_id FROM service_accounts
   WHERE act_as_user AND owner_user_id NOT IN (SELECT id FROM users);
  ```
- **Three new security-event types** (free strings at rest, no store change):
  `service_account_created` (`UserID` = the act-as owner; Details
  `service_account_id`, `act_as_user`, `delegated`), `api_key_minted` (`UserID` =
  the act-as owner the key authenticates as, else empty; Details `key_prefix`,
  `service_account_id` — never the raw key or its hash) and `api_key_revoked`
  (Details `key_id`), emitted **per successful CALL, not per state transition** —
  a replayed revoke of an already-revoked key writes a second row, because the
  revoke path holds only the key id and `APIKeyRepository` has no Get-by-id with
  which to read the prior state (a state-aware event waits on the deferred
  account-scoped revoke train). `Actor` is the principal resolved from
  the request context, never the caller-supplied `createdBy` string. Readers that
  filter by `event_type` are unaffected.
- **Scope, stated plainly:** behind the gate the list is the GLOBAL one and
  mint/revoke take any id — this train fixes OWNERSHIP, not per-object
  authorization. A creator-scoped list or an account-scoped revoke needs a port
  change (`ServiceAccountRepository.List` has no filter dimension;
  `APIKeyRepository` has no read-by-id) and a two-store train; a host that needs
  either leaves the gate nil and serves its own routes over the unchanged
  `CreateServiceAccount` / `MintAPIKey` / `ListServiceAccounts` / `ListAPIKeys` /
  `RevokeAPIKey`.
- **No store retags.** No port method, no DDL, no migration, no `go.mod` change:
  the new event types are free `TEXT`, and `service_accounts` / `api_keys` keep
  their columns. `features/authentication/stores/{pgx,turso}` stay at v0.4.0 /
  v0.3.0.
- **The standard unkeyed-literal caveat:** `Config` gains one field and loses
  one, so any UNKEYED `auth.Config{…}` literal breaks. None exists in-repo or in
  gps-360-go (grep 2026-08-26); keyed literals are unaffected except for the
  removed field.

**Adoption — gps-360-go.** It builds authentication at `cmd/server/main.go`
BEFORE authorization, so either move the authorization boot above the
authentication config or set `MachineRoutesGate` on the config after the
authorizer exists and before `NewService`. Then replace
`MachineRoutesDisabled: true` (`cmd/server/authentication.go:50`) with
`MachineRoutesGate: authorizationComponents.Service.RequirePermissionFixed(...)`
on its platform/steward coordinate, and delete `features/auth/inbound/machine.go`
+ `machine_test.go` (plus the `machine` field of `inbound.Auth` and the
`MachineService` parameter of its `New`). Deleting those routes is **four
client-visible divergences at once**: the path (`/api/v1/service-accounts*` →
`/auth/service-accounts*`), the mint body (`{"key":{…},"plaintext":…}` → the flat
`{…,"key":…}`), the revoke status (204 → 200 `{"status":"revoked"}`), and
`created_by` (`"user:<id>"` → a bare user id, with existing rows keeping the old
format — one column, two formats, no backfill). Repoint the key-holding clients
or keep a thin host adapter. gps's blanket `act_as_user ⇒ 400` refusal becomes a
gate-protected capability; a host that still wants no delegation keeps its own
create route (own-the-routes) and adopts the bundled ones for the rest.

**Adoption — coordination-hub and any other v0.5.x host.** If the machine
repositories are wired, the five routes answer 404 and boot logs the WARN until
`MachineRoutesGate` is named. Once named, run the ghost-owner audit query above
and repoint any cookie-driven admin UI at the CSRF bootstrap. Nothing else
moves: key authentication,
`RequirePrincipal`, `RequireServiceAccount` and `RequireLiveSession` are
unchanged, and no store or migration is touched.

### features/authentication — v0.5.4 (2026-08-25): MachineRoutesDisabled + a caveat on the bundled lifecycle routes (patch by owner ruling)

authentication-machine-routes-toggle (plan of record
`plans/authentication-machine-routes-toggle.md`). A **patch by owner ruling**:
one additive `Config` bool whose zero value preserves every route; no schema,
no `go.mod` change, no store retags.

- **`Config.MachineRoutesDisabled bool`** (`env:"AUTH_MACHINE_ROUTES_DISABLED"`)
  keeps API-key AUTHENTICATION on (`AuthenticateAPIKey` and the bearer path of
  `RequirePrincipal` follow `MachineEnabled` as before) but mounts NONE of the
  bundled lifecycle routes — `/auth/service-accounts`,
  `/auth/service-accounts/{id}/keys`, `/auth/api-keys/{id}/revoke` — so they
  answer 404 (deny-by-absence, the `PasswordFlowsDisabled` shape). A host then
  serves its own gated routes over `CreateServiceAccount` / `MintAPIKey` /
  `ListServiceAccounts` / `ListAPIKeys` / `RevokeAPIKey`.
  `Service.MachineRoutesEnabled()` is the accessor the inbound layer reads.
- **CAVEAT (unchanged behaviour, now documented):** the bundled lifecycle
  routes are gated on `RequireUser` — ANY authenticated user — and are
  UNSCOPED: `POST /auth/service-accounts` takes `owner_user_id` from the
  request body (any user can create an act-as-user account owned by ANY other
  user and mint its key — impersonation), `GET /auth/service-accounts` lists
  every account, minting and revocation accept any id. That is acceptable for
  a single-admin host and is not for a host with several trust levels. Such a
  host should set `MachineRoutesDisabled` today; binding `owner_user_id` to the
  caller and scoping list/mint/revoke to the creator is a separate, behaviour-
  changing fix that needs an owner ruling and is NOT in this tag.

### features/authentication — v0.5.3 (2026-08-25): PasswordFlowsDisabled — the password credential as a posture (patch by owner ruling)

authentication-password-flows-toggle (plan of record
`plans/authentication-password-flows-toggle.md`). A **patch by owner ruling**:
one additive `Config` bool whose zero value preserves every existing route, no
schema, no `go.mod` change, no store retags.

- **`Config.PasswordFlowsDisabled bool`** (`env:"AUTH_PASSWORD_FLOWS_DISABLED"`)
  turns the password credential OFF for a host whose only way in is OAuth or
  passwordless. `Register` then mounts NONE of the password routes —
  `/auth/register`, `/auth/login`, `/auth/verify`, `/auth/verification/resend`,
  `/auth/password/{forgot,reset,change,set,remove,remove/start}`,
  `/auth/step-up/password`, and the HTML twins of the public ones plus the
  password account pages — the same deny-by-absence shape machine identity and
  the token endpoint already use, so a disabled host answers **404**, not 4xx
  from a live handler. `GET /auth/login` (the HTML page hosting OAuth entry)
  stays mounted.
- **Service half:** `Register`, `Login`, `IssueToken`, `ForgotPassword`,
  `ResetPassword`, `ChangePassword`, `SetPassword`, `RemovePassword` refuse
  first with **`ErrPasswordFlowsDisabled`** (wraps `sdk.ErrNotFound`, matching
  the 404) before touching any store — a host reaching the `Service` directly
  gets the unmounted route's answer. `PasswordFlowsEnabled()` is the accessor
  the inbound layer reads.
- **Unchanged:** `Hasher` stays required (the credential rail is shared);
  OAuth, passwordless, sessions, refresh, logout, step-up-by-code, identifiers,
  machine identity, and admin routes are untouched. Hosts that previously
  answered the password routes with their own 404 middleware can delete it.

### sdk — next tag (2026-08-25): crud.MapPageErr — the fallible row→domain page bridge (patch by owner ruling)

pgxdb-probe-table-map-page-err (plan of record
`plans/pgxdb-probe-table-map-page-err.md`). A **patch by owner ruling**
(one additive generic function; the `sdk/v0.4.1` precedent). No in-repo module
pins move.

- **`crud.MapPageErr(p Page[T], fn func(T) (U, error)) (Page[U], error)`** is
  `MapPage` for a mapper that can fail. A store whose `toDomain` VALIDATES the
  stored row (a vocabulary outside the domain's contract must fail loud, never
  enter the domain) previously had to re-implement the page copy by hand around
  its error loop; now it is `crud.MapPageErr(page, row.toDomain)`. Fail-fast: the
  first error comes back unwrapped with a zero `Page` (no partial items, no
  cursors); on success every pagination field is copied unchanged exactly as
  `MapPage` does, and nil `Items`/`Total` stay nil.
- **`MapPage` is untouched.** Infallible mappers keep using it.

### integrations/datastores/pgxdb — next tag (2026-08-25): ProbeTable — the boot-time table probe (patch by owner ruling)

pgxdb-probe-table-map-page-err (plan of record
`plans/pgxdb-probe-table-map-page-err.md`). A **patch by owner ruling**
(one additive function; no `go.mod` change — still `sdk v0.4.0`).

- **`pgxdb.ProbeTable(ctx, q Querier, table string) error`** is the existence
  probe every pgx store constructor already carried privately
  (`features/{authentication,authorization,events}/stores/pgx`): `SELECT
  to_regclass($1)::text` with the name bound as a parameter (bare, or
  schema-qualified like `gps.organizations`). Absent → wraps `sdk.ErrNotFound`
  naming the relation; a query/infrastructure failure maps through `MapError`
  and is **never** misreported as a missing table. Accepts a `*DB` or a `*Tx`.
  Run it in a store constructor so a host aimed at a database without its
  tables fails at wiring time, naming the table, instead of 500ing on the
  first query.
- **The framework's own store copies do not move on this tag.** They adopt
  (`fmt.Errorf("…: %w", pgxdb.ProbeTable(…))`, keeping their
  migration-source wording) when each store module next retags and pins this
  pgxdb version — deliberately not forced here.

### features/authentication — next tag v0.5.2 (2026-08-25): completed account mutations stop landing silently (patch; REDIRECT TARGETS AND RENDERED COPY CHANGE)

Defect fix (Segovia flag #19). Every auth page model embeds `PageContext.Message` and
every bundled page body renders it, and the feature already populates that slot across
a redirect in four places (`?sent=1` on forgot / password-remove / step-up /
oauth-unlink). The account page was never wired in: all seven completed account
mutations 303'd to a bare `/auth/account`, the verify-link completion did the same, and
the OAuth callback's `ActionLinked` — the successful explicit link — 302'd with no
marker at all, while `accountPage` read zero query params. A host's user changed their
password, confirmed an identifier, or linked Google and the destination said **nothing**.

The `?auth=<code>` vocabulary `pendingLinkRedirect` established (`auth=link_sent`) is
now the one outcome vocabulary for the whole feature, extended rather than duplicated:

| Flow | Destination | Code |
| --- | --- | --- |
| password change / set / remove | `/auth/account` | `password_changed` / `password_set` / `password_removed` |
| identifier confirm / remove / set-uses | `/auth/account` | `identifier_confirmed` / `identifier_removed` / `identifier_updated` |
| OAuth unlink | `/auth/account` | `provider_unlinked` |
| pending-link completion (`POST /auth/oauth/verify-link`, form arm) | `/auth/account` | `provider_linked` |
| OAuth callback, **`ActionLinked` only** | the flow's validated destination | `provider_linked` |
| form logout | `/auth/login` | `signed_out` |
| password reset | `/auth/login` | `password_reset` |

Adopter-facing surface:

- **No exported API change.** No new or changed exported type, field, method,
  constant, or route; no schema; no `go.mod` change (still `sdk v0.4.x`); no store
  retag. A host implementing `Views` by hand compiles unchanged, and the JSON arm of
  every one of these endpoints is byte-stable — this is the HTML/browser lane only.
- **Redirect `Location` values change.** The eight account-destination flows now 303
  to `/auth/account?auth=<code>`, logout and reset to `/auth/login?auth=<code>`, and
  the callback's completed-link branch appends `auth=provider_linked` to the target it
  already redirected to. A host asserting the exact old `Location` string in its own
  tests must update those assertions; nothing else observes them.
- **A new query param can arrive at a HOST url.** Only on the callback's `ActionLinked`
  branch, and only on the destination the link flow already validated — the same shape
  `auth=link_sent` has taken since the pending-link work. It is additive and ignorable:
  existing query values and any fragment are preserved, and a host reading `?auth=`
  through a closed whitelist (the recommended posture) simply does not match the new
  code. `ActionLogin` / `ActionRegister` are deliberately **unmarked** — they land on
  the app, not an auth page.
- **Two pages render new copy.** `accountPage` and `loginPage` now read `?auth=`
  through **route-specific closed maps**: an account code is unknown on login and a
  login code is unknown on account, so no destination can be made to show copy for
  something that could not have happened there. Unknown, empty, malformed and
  wrong-destination codes render **no notice**, and the wire value is never returned by
  the mapper and never reaches markup — the same closed-whitelist posture the four
  `?sent=1` readers already hold. The copy is generic and enumeration-safe and
  deliberately **names no provider**: "That sign-in provider was linked to your
  account.", not "Google". The account page lists the linked provider in its own masked
  inventory two rows below the notice.
- **The notice slot has no tone.** `PageContext.Message` renders success and failure
  identically as a polite `<p role="status">`, and the login page now carries both form
  errors and these outcomes. A host styling `[data-slot="auth-message"]` should keep
  that treatment neutral. A structured `PageContext.Tone` is a separate, unshipped ask.

**Why a patch.** The bugfix arm, with the same reasoning as v0.5.1: a defect fix with
no exported-symbol, signature, schema, `go.mod`, or mandatory-configuration change,
adoptable on a maintenance line with no host action. It is louder than v0.5.1 only in
that the change is visible in `Location` headers as well as rendered bytes, which is
why both are stated plainly above rather than left to be discovered.

**`views/goth` does NOT retag.** It already renders the notice slot on every page body
and its source is untouched by this change; its `features/authentication v0.5.1` pin
upgrades at the host through MVS.

### features/authentication v0.5.1 + views/goth v0.2.2 (2026-08-23): the identifier edit form stops offering a removal the policy refuses (patch pair)

Defect fix one layer deeper than v0.2.1 (Segovia flag #18). The credential policy's
`Removable` hint reached the account page but never reached the identifier
**management form**, so `GET /auth/identifiers/{id}/edit` always rendered a live
"Remove this identifier" button — including for the account's only recovery-capable
address. The owner clicks, and the mutation refuses with the generic
`accountPolicyMsg`. The form now offers removal only where the policy would allow it,
and otherwise renders the same muted explanation v0.2.1 introduced:

> Removing this would leave your account without a way to sign in. Add another
> sign-in method first.

The **copy const is reused, not duplicated** — the account page's suppressed unlink
and the form's suppressed remove describe the same policy, and the sentence names no
method kind, no policy rule, and no contact value.

- **`IdentifierFormPage.Removable bool`** (features/authentication) — an additive
  exported field carrying the identifier's advisory removable hint to the form. The
  handler reads it from the seam it already calls, `populateIdentifierEdit`'s
  `Service.Methods` inventory — the same masked projection the account page renders —
  so **no policy logic is re-derived in the transport layer** and the page and the
  guard cannot disagree about what the account has. The server-side refusal in
  `account_forms.go` is untouched and remains the authoritative backstop; the hint
  stays advisory (the mutation re-runs the policy under revision serialization).
- **The account page's identifier row is unchanged.** It renders only "Manage", an
  edit link — editing an identifier's uses is always allowed — so there is no removal
  affordance there to gate. The only direct remove control for an identifier lives
  inside the edit form, which is what this change gates.

**Zero-value semantics — a deliberate flip, stated plainly.** A zero
`IdentifierFormPage` has `Removable == false`, so the zero model (and any edit model
the handler could not resolve to an identifier the caller owns) now renders the
explanation instead of a live remove form. That **changes the bytes of the zero
model's edit rendering**, and it is the choice we want: never offer a mutation the
policy might refuse. It is also the honest rendering of an unresolved form — a page
that cannot name the identifier has no business offering to delete it. Both shapes
are pinned by test (`TestIdentifierForm_ZeroModelFailsSafe`), as is the **removable**
rendering, which is byte-identical to v0.2.1 (`TestIdentifierForm_NonRemovableExplained`);
the add and confirm modes have no removal region and are untouched.

**Patch for both, cut as a pair.** Bugfix arm of the bump rules. For
`features/authentication`: no port method, no interface, no signature, and no schema
changes — one additive exported struct field with a fail-safe zero value, following
the `Config.OAuthLinkBaseURL` (v0.3.1) / `sdk v0.3.1` precedent that an additive
symbol may ship as a patch. A host that implements the `Views` port **by hand
compiles unchanged** and keeps its current rendering; only an **unkeyed composite
literal** of `IdentifierFormPage` would break, and hosts consume that model rather
than construct it (the `InviteCheckRequest` v0.4.1 precedent for naming this). No
store retags; `go.mod` unchanged (still `sdk v0.4.0`). For `views/goth`: template and
copy only, no exported Go symbol, no new kit class, no new asset, and **no change to
the bundle's browser `Requirements`** — no adopter needs to re-derive a CSP header.
They tag together only because the views module reads the new field, so its pin moves
to `features/authentication v0.5.1` at the release commit (`sdk v0.4.0` and
`ui/goth v0.1.0` unchanged). A pin move to a sibling **patch** does not floor a minor;
v0.2.0 was a minor because the `Views` port gained a method.

Adopter note: a host that byte-pins the identifier edit form's markup, or that
translates the shipped copy, should re-capture it. A host that overrides
`IdentifierForm` renders its own page and is unaffected — though it now has the field
available and should gate its own remove control on it.

### features/authentication/views/goth — v0.2.1 (2026-08-21): a suppressed unlink control now explains itself (patch)

Presentation-only defect fix (Segovia flag #17). When the credential policy reports
a linked OAuth account as non-removable — the common case being an account whose
only sign-in method is that link — the bundled account page rendered the row with
no unlink control **and no text at all**, which reads as a broken affordance. The
row now carries a muted explanation in place of the suppressed control:

> Removing this would leave your account without a way to sign in. Add another
> sign-in method first.

It mirrors the generic copy the feature already returns when a removal is refused
server-side, stated ahead of the attempt and paired with the remedy; it names no
policy rule, method, or contact value. Rendered with the existing
`ui/goth` `Typography` muted recipe — **no new kit class, no new asset, and no
change to the bundle's browser `Requirements`**, so no adopter needs to re-derive
a CSP header.

A **patch** per the bump rules' presentation clause: no exported Go symbol is
added or changed (the copy is an unexported const), the `Views` port and every
view model are untouched, `go.mod` is unchanged (`features/authentication v0.5.0`,
`sdk v0.4.0`, `ui/goth v0.1.0`), and `features/authentication` does **not** retag —
the guardrail, the `Removable` computation, and every handler are unchanged. The
LinkableProviders change in v0.2.0 was a minor only because the port gained a
method; new rendered copy on its own does not floor a minor.

Adopter notes: a **removable** method's row is byte-identical to v0.2.0 (pinned by
test), so only the previously-empty non-removable case changes. A host that byte-pins
the account page's linked-accounts markup, or that translates the shipped copy, should
re-capture it; a host that overrides `AccountSecurity` renders its own page and is
unaffected.

### features/authentication v0.5.0 + views/goth v0.2.0 (2026-08-21): OAuth linking completed in the bundled HTML surface (minor floor)

Closes the two browser-side gaps in the OAuth linking story (Segovia flags
#15/#16, plan of record `.claude/dashboards/04-account-oauth.md` in that repo),
and teaches the browser-lane redirect resolver to honor safe same-origin relative
paths. Additive only —
no schema change, no store retags, no `go.mod` change (`features/authentication`
still pins `sdk v0.4.0`) — but the **`Views` port gains a method**, so it is a
minor floor for both modules and they are tagged together (the goth module
implements the new method). `views/goth v0.2.0` is the module's first retag
since the `v0.1.0` first cut, so its pins move with it per the tagging scheme:
`features/authentication v0.5.0` (the port it implements) + `sdk v0.4.0` (the
feature's own floor); `ui/goth` stays at `v0.1.0` (zero drift since that tag,
no retag).

- **`AccountSecurityPage.LinkableProviders []string`** — the wired OAuth providers
  the caller has NOT linked, in the service's deterministic order. The bundled
  account page renders a "Link an account" list, one anchor per provider, pointing
  at the existing `GET /auth/oauth/{provider}/link/start?redirect=/auth/account`;
  previously the link-start route was unreachable from any shipped page. The field
  is empty when OAuth is off or every wired provider is already linked, and an
  empty value renders **byte-identically** to the pre-affordance section (pinned by
  test). The `/auth/account` destination needs no `Config.RedirectAllowlist` entry
  — see the relative-path resolver change below.
- **Browser-lane redirect resolution honors safe same-origin relative paths.**
  `Service.ResolveRedirect` — shared by the OAuth flow start and the HTML form
  return-to — now honors any safe root-relative path (the existing
  `redirect.SafeRelativePath` rule: single leading slash, no `//`, backslash,
  scheme, or control bytes) WITHOUT an allowlist entry; a relative path is never
  an off-site open-redirect vector. Absolute targets still require an exact
  `Config.RedirectAllowlist` match, and anything else still falls back to `/`.
  Behavior change: a relative `redirect`/`return_to` that previously fell back to
  `/` in the OAuth lane is now honored (the HTML form lane already honored it).
  The **invitation lane is unchanged** — its destination is embedded in a mailed
  link, so it stays exact-match only.
- **`Views.OAuthLinkLanding(m OAuthLinkPage) web.Renderer`** — a NEW port method
  (with the re-exported `authentication.OAuthLinkPage` model). A host that embeds
  the bundled `views/goth` default gets it for free; a host that implements the
  port **by hand must add it** (the `features/cms/views/goth` `EntriesListContent`
  precedent).
- **`GET /auth/oauth/link`** — a PUBLIC HTML landing page completing the
  anti-takeover pending-link branch, registered only when `Config.Views` is wired
  AND a provider is configured. It is public by construction: the caller holds the
  mailed single-use secret and no session (the flow is what mints one). It reuses
  the existing externalized `fragment.js` reader — the token rides the mailed
  link's `#token=` fragment, is scrubbed from history client-side, and never
  reaches the server on the landing GET. Point `Config.OAuthLinkBaseURL`
  (`AUTH_OAUTH_LINK_URL`) at this route (`https://<host>/auth/oauth/link`) and the
  clickable pending-link email from v0.3.1 now lands somewhere real; hosts with an
  SPA landing keep theirs unchanged.
- **`POST /auth/oauth/verify-link` gains the form arm** of the standard
  content-type dispatch. The **JSON contract is unchanged** (same body, same user
  JSON response, same strict decoding); a urlencoded body completes the same
  `VerifyLink` service method, sets the session cookies, and **303s to
  `/auth/account`**, where the new link appears in the masked inventory — the
  destination every completed link/unlink mutation already PRGs to. Like the
  reset/redeem landings it carries the credential-establishment origin policy and
  no double-submit CSRF gate (the caller has no session to bootstrap one from). A
  dead or unknown secret re-renders the landing at the mapped status with generic
  copy and echoes no token (it is single-use and the attempt consumed it). With a
  nil `Views` a form body is still 415 — the API-only posture is untouched.

### features/authentication — v0.4.2 (2026-08-21): authorized invitation operations promoted to the public facade (patch)

Same-day follow-up to v0.4.1 by owner ruling. Purely additive facade surface —
two delegate methods, no behavior change anywhere else, no schema change, no
store retags, `go.mod` unchanged (still `sdk v0.4.0`).

- **`Service.CreateAuthorized(ctx, principal, in)`** and
  **`Service.ListByResourceAuthorized(ctx, principal, resourceType, resourceID, req)`**
  are now public on the facade `authentication.Service` — the policy-carrying
  twins of the trusted `Create` / `ListByResource`, for hosts writing their OWN
  invitation handlers instead of mounting the shipped routes. They guard
  `ErrInvitationsDisabled` like their trusted siblings, prepare the request
  (metadata validation, identifier normalization, invitee lookup), pose
  `Config.InviteCheck` with the complete invitee context, and only then act; a
  denial leaves no pending row and attempts no grant. `principal` is the
  resolved caller (the inviter), never the invitee.
- The trusted `Create` / `ListByResource` remain check-free and unchanged; a
  host driving them directly still owns the authorization decision itself.

### features/authentication — v0.4.1 (2026-08-21): authorized invitation operations with invitee context + owner metadata projection (patch by owner ruling)

invitation-metadata-host-seams proposals 1 and 3 (plan of record
`plans/invitation-metadata-host-seams.md`, amending `plans/invitation-metadata.md`).
Cut as a **patch by owner ruling** despite additive exported symbols — the
`sdk/v0.3.1` / `features/authentication/v0.3.1` precedent: it completes the
v0.4.0 metadata contract rather than opening new surface. No schema change, no
store retags, `go.mod` unchanged (still `sdk v0.4.0` — the feature does not
consume the sdk v0.4.1 wrapper).

- **`InviteCheckRequest` gains the invitee context**: `Identifier` (the
  feature-normalized invitee identifier), `IdentifierKind`, and
  `ResolvedSubjectID` (the existing subject the identifier resolves to, or `""`
  when unknown or the kind is not resolvable). A host can now authorize the
  complete invitation — metadata against invitee state — before any row exists
  or grant is attempted. All three are empty for `InviteList`. `UserLookup` is
  email-kind-only today, so `ResolvedSubjectID == ""` is NEVER proof the invitee
  is new. **Unkeyed composite literals of `InviteCheckRequest` break**; keyed
  literals and func implementations are source-compatible.
- **New authorized service operations**: `CreateAuthorized(ctx, principal, in)`
  and `ListByResourceAuthorized(ctx, principal, resourceType, resourceID, req)`
  prepare metadata, normalize the identifier, perform the lookup, pose
  `InviteCheck`, and only then run the existing side-effect paths. The trusted
  `Create` / `ListByResource` composition methods remain check-free and
  behaviorally unchanged. The shipped HTTP create/resource-list handlers now
  reach the policy through the authorized operations instead of posing the check
  in the adapter; a nil check reached on an authorized path fails closed (500),
  never an allow. Practical consequence for hosts: a metadata-dependent refusal
  now lands on the **inviter at create time** instead of surfacing to the
  invitee at accept — or silently stalling on the best-effort
  resolve-on-registration path.
- **Response projections split.** The resource-owner projection (pending create,
  resource list, resend) now carries `metadata` (`omitempty`) so an owner can
  audit the routing they supplied; the recipient-facing `/mine` uses a separate
  projection with NO metadata field. Invitations without metadata keep
  byte-identical responses everywhere.

### sdk — v0.4.1 (2026-08-21): web.SafeDomainError — explicit host-facing safe wire errors (patch by owner ruling)

invitation-metadata-host-seams proposal 2 (plan of record
`plans/invitation-metadata-host-seams.md`). A **patch by owner ruling** (one
additive type + constructor; the `sdk/v0.3.1` precedent). The sdk tree is
otherwise byte-identical to `sdk/v0.4.0`; no in-repo module pins move.

- **`web.SafeDomainError`** (`NewSafeDomainError(public *Error, cause error)`)
  lets a HOST SEAM (an `InviteCheck` or `Granter` refusal) carry a deliberately
  public sentence to the wire through a vendored feature's handler.
  `ErrFromDomain` recognizes only this explicit wrapper — via `errors.As`,
  before its untouched kind switch — and returns the wrapper's `*Error`.
  `Unwrap` returns the **cause alone**, so `errors.Is`/`errors.As` keep matching
  the domain sentinel while the public `*Error` stays outside the unwrap chain;
  a zero-value wrapper falls through to the generic mapping instead of
  panicking.
- **Nothing else changes.** Bare sentinels, arbitrary wrapped `*web.Error`
  values, and feature-internal errors keep today's generic bodies — this is an
  explicit host-seam affordance, not permission for domain code to place user
  text on the wire. Example construction:
  `web.NewSafeDomainError(web.ErrStateConflict("already attached to another account"), sdk.ErrConflict)`.

### features/authentication v0.4.0 + both store modules v0.3.0 (2026-08-20): invitation metadata (minor; store migration REQUIRED)

invitation-metadata (`plans/invitation-metadata.md`). Adds an opt-in, host-defined
`Metadata map[string]string` that rides an invitation from create to the Granter
seam — a channel for a host to route its own facts (a firm id, a plan tier) from
invite-time to grant-time. The feature never interprets it; empty metadata
preserves today's grant semantics, so this is additive for hosts that do not opt in.

**`features/authentication` — additive public surface:**

- `GrantInput.Metadata`, `InviteCheckRequest.Metadata`, and `CreateInput.Metadata`
  (all `map[string]string`). Adding a field to the exported `InviteCheckRequest` /
  `GrantInput` is source-compatible for keyed struct literals and interface
  implementations; a host using an UNKEYED composite literal of either must switch
  to keyed literals — no such construction exists in-repo.
- `invitation.NewWithMetadata`, `invitation.ValidateMetadata`,
  `invitation.CloneMetadata`, and the `invitation.MetadataMax*` bound constants.

**Contract:**

- Metadata is opaque, host-owned routing data supplied at create, persisted, and
  echoed into `GrantInput` on accept, direct-add, and resolve-on-registration as a
  non-nil defensive copy (empty when none). The feature validates only shape/size:
  32 entries, 64-byte keys, 256-byte values, 4 KiB JSON-encoded total, UTF-8,
  non-empty keys — each violation wraps `ErrInvalidInput`. Nil/empty persists as `{}`.
- It is UNTRUSTED inviter input, never an authorization claim by itself:
  `InviteCheck` receives the same metadata (its OWN defensive clone, so a mutating
  policy cannot alter the persisted/granted value) to authorize the complete
  invitation at issuance, and a `Granter` applying any security-sensitive side
  effect from it MUST revalidate.
- The create route now bounds its body with the standard strict JSON decoder before
  decoding the unbounded metadata object.

**`features/authentication/stores/{pgx,turso}` — this tag ships HOST SCHEMA.**

New canonical migration **`0016_invitation_metadata.sql`** in both trees
(append-only; `0009_invitations.sql` stays byte-identical to its tag):

- `invitations.metadata` — `jsonb` (pgx) / `TEXT` (turso), `NOT NULL DEFAULT '{}'`;
  existing rows read back as an empty map. The store never writes JSON `null` (which
  would bypass the column default). Malformed stored JSON fails the read rather than
  surfacing a silent empty.

Both `Repositories()` constructors gained a **column** probe (`invitations.metadata`)
beside the existing probes, so a host that copied 0001–0015 and skipped 0016 fails
at wiring time naming the column rather than mid-request.

**Adopter order: apply 0016 BEFORE deploying a binary built against these tags.**

Live gates closed at authoring time: full `storetest` invitation conformance
including the new metadata round-trip against the live Turso playground database,
plus a populated-table upgrade + malformed-stored-JSON integration check. pgx unit,
migration-inventory, and cross-dialect parity tests pass; live pgx conformance was
not run this cut (no Postgres DSN available in the release environment).

### sdk — v0.3.0 (2026-08-14): global middleware is genuinely global + configurable CORS (minor)

coordination-hub-auth-upstream U1 (plan of record `.claude/plans/`
`coordination-hub-auth-upstream/`; Coordination-Hub upstream items 1b/3/5). Additive
API, so a **minor** floor — but it carries the batch's one silent behavior change,
which leads this note:

- **`WebHandler.Use` now wraps the ENTIRE mux, and `HandleRaw` no longer bypasses
  it.** Global middleware used to be baked into each registered pattern, so it never
  observed mux-generated 404s, method-mismatch 405s (the symptom that broke
  preflights to method-qualified feature routes), redirects, or `HandleRaw`
  registrations. It now runs once around dispatch, so all of those pass through
  panic recovery, request IDs, logging, and CORS. **This changes behavior for any
  existing sdk consumer that relied on `HandleRaw` as a policy escape hatch** — a raw
  OpenAPI/metrics/streaming handler that previously ran outside the global stack now
  runs inside it. `HandleRaw`'s contract is now "raw `http.Handler` + raw ServeMux
  pattern", never "no middleware". Route middleware order, global outermost-first
  order, and the automatic 405 `Allow` header are unchanged (the mux still routes).
  `Use` remains **boot-time-only**: it rebuilds the dispatch chain unsynchronized, so
  it must not be called once the server is serving.
- **`CORSWithConfig(CORSConfig{AllowedOrigins, AllowedHeaders, ExposedHeaders})`** is
  the new configuration entry point. `CORSMiddleware(origins)` is retained as a
  delegating compatibility constructor with byte-identical defaults. A nil
  `AllowedHeaders` keeps `Accept, Content-Type, Authorization`; a non-nil list
  REPLACES it (this is how a host opts in authentication's `X-CSRF-Token` — the sdk
  never names a feature's header).
- **`Access-Control-Expose-Headers` now defaults to `X-Request-ID`**, so cross-origin
  JavaScript can read the framework's own request id. An explicit empty
  `ExposedHeaders` suppresses the header entirely.
- **`Vary: Origin` is now set on every response** through the middleware, matched
  origin or not, appended without overwriting or duplicating an existing `Vary`
  dimension. A host running the ~6-line `varyOrigin` wrapper as a workaround can drop
  it; keeping it stays correct.
- **Only a genuine preflight short-circuits with 204** — an `OPTIONS` request must
  carry both `Origin` and `Access-Control-Request-Method`. Any other `OPTIONS`
  request now continues to the mux, which matters precisely because `Use` is global:
  a host's own `OPTIONS` route stays reachable.
- Wildcard behavior is unchanged: `*` may echo an origin but never emits
  `Access-Control-Allow-Credentials`.

### features/authentication — v0.2.0 (2026-08-14): browser cookie-flow seams (minor; pins sdk v0.3.0)

coordination-hub-auth-upstream U2–U4. Every change is additive — no existing request
body, response shape, or route was changed or removed — so a **minor** bump. The
module's `go.mod` now requires **`sdk v0.3.0`**: the documented cross-origin browser
flow depends on the CORS seam above, and pinning is what stops a host from adopting
`/auth/csrf` against an sdk that cannot be configured to allow the echo header.

- **`Config.RefreshCookiePath string`** (`AUTH_REFRESH_COOKIE_PATH`). Empty keeps
  today's `/auth`. A host mounting the feature under a prefix (`feature.PrefixRegistrar`)
  MUST set the full prefixed path — `/api/v1/auth` — or the browser never sends the
  refresh cookie and cookie-driven refresh dies silently. A non-empty value must be a
  valid absolute cookie path (leading `/`, no query/fragment/control/header-delimiter
  character, no trailing slash except `/`) or construction fails with the new
  `var ErrRefreshCookiePathInvalid`; `net/http` would otherwise silently drop bad
  bytes. The same resolved path is used for every issue AND the logout deletion, so a
  prefixed host clears exactly what it set. It configures ONLY the refresh cookie —
  the access cookie and the `SameSite=Lax` posture are untouched.
- **`GET /auth/csrf`** — the JSON double-submit bootstrap for a cookie-authenticated
  SPA on a different origin than the API (it cannot read the API-origin
  `__Host-auth_csrf` cookie). Gated by `RequireLiveSession` plus the existing
  origin-only browser gate; returns `{"csrf_token":"…"}` under
  `Cache-Control: no-store`, and the body value is always the value of the
  `__Host-auth_csrf` cookie on the same response. It REUSES a
  well-formed existing token rather than rotating (a second tab may hold it in
  flight) and fails closed — 500, no token, no cookie — if entropy is unavailable.
  The host must list `X-CSRF-Token` in its sdk CORS `AllowedHeaders` for the browser
  to send the echo header.
- **The double-submit CSRF cookie is renamed `auth_csrf` → `__Host-auth_csrf`.**
  The `__Host-` prefix makes a browser refuse the cookie unless it is `Secure`,
  `Path=/`, and carries no `Domain` attribute — which is what stops a sibling host
  under the same registrable domain from planting a well-formed token (the
  fixation residual documented at v0.1.0). The cookie was already
  `Secure`/`Path=/`/no-`Domain`, so **no deployment change is required**; the only
  new failure mode is a plain-HTTP host, where the browser now refuses the cookie
  outright. The JSON lane (`GET /auth/csrf`) is new in this tag, so it has no
  migration concern. **The HTML views lane DID use `auth_csrf` at v0.1.0**: at
  upgrade, a form page rendered before the deploy carries a token whose old-name
  cookie the server no longer reads, so that one in-flight submission gets the
  generic 403 CSRF page; reloading the form re-mints under the new name and
  proceeds. The stale `auth_csrf` cookie is neither read nor deleted — it is
  simply orphaned, keeps riding along until the browser session ends (it was set
  without `Max-Age`/`Expires`), and is harmless.
- **`GET /auth/me`** — session hydration behind `RequireLiveSession` (one revocation
  lookup, the `GET /auth/methods` posture), returning the existing
  `{id,email,display_name,email_verified}` shape under `Cache-Control: no-store`. A
  machine/API-key principal is not a current user and gets 401, never a fabricated
  profile.
- **Login/register/`/auth/me` now render email through one service projection.** The
  compatibility DTO is built at a single site over the account's active email
  identity, so the address reported after registering `New@Example.com` is the
  normalized stored value on all three paths instead of the raw submitted string on
  one. A client that depended on the raw echo sees the normalized address.
- **`origin_rejected` splits out of `permission_denied`.** An origin-gate denial
  (both the mutation gate and the origin-only gate) now returns 403 with
  `code: "origin_rejected"`; a failed double-submit keeps `permission_denied`.
  Statuses and human messages are unchanged, so this is a diagnosability gain, not a
  client break — a host that boot-fails on a CORS/auth allowlist mismatch can now
  read the runtime symptom instead.

### sdk — v0.3.1 (2026-08-15): 409 vocabulary completed + Page end-of-list contract documented (patch by owner ruling)

The two upstream flags from coordination-hub's `coordination-api-consistency`
work. **Owner ruling:** this tag carries one additive exported symbol, which the
bump rules below classify as a minor; the owner cut it as a **patch** because it
is a defect fix in an existing contract, not new surface. Pre-`v1` Go semantics
make this safe for consumers either way.

- **`web.ErrStateConflict(msg)`** — the missing constructor for the
  state-conflict half of the 409 vocabulary (code `conflict`, pairing with
  `sdk.ErrConflict`). `web.ErrConflict` keeps code `already_exists` (pairing
  with `sdk.ErrAlreadyExists`) and both doc comments now cross-reference the
  sibling, so the two 409 kinds are no longer conflated by the only reachable
  constructor. `ErrFromDomain`'s `sdk.ErrConflict` arm now calls the new
  constructor instead of an inline literal; its mapping is behaviorally
  unchanged. A host that worked around this with
  `ErrConflict(msg).WithCode("conflict")` can revert to the plain constructor.
- **`crud.Page` end-of-list contract documented** (godoc + sdk README): every
  field except `Items` is omitempty, so a final page serializes as
  `{"items":[…]}` and clients must read absent `has_more`/`next_cursor` as
  false/empty — the normal end-of-list signal, not an error. Doc-only; no
  serialization change.

### features/authentication — v0.2.1 (2026-08-15): add-or-signup invitation lifecycle finished (patch)

coordination-hub `email-and-invitations` task U2 (plan of record, in the
Coordination-Hub repo, `.claude/plans/email-and-invitations.md`). Both changes are
additive/behavioral with no exported API change and no schema change, so a
**patch**. `go.mod` is unchanged (still `sdk v0.3.0`); no store module retags.

- **A brand-new OAuth-provisioned account now resolves its pending auto-accept
  invitations.** The OAuth register-and-link branch (branch 3 — no account claims
  the provider-verified email, so a password-less user is created and linked) calls
  the SAME best-effort invitation resolver `Register` and `Verify` already call,
  with the account's normalized stored primary email and its new user id. It runs
  after the user + verified primary identifier + provider link exist and BEFORE the
  session is minted, so the grants are effective before the caller ever holds a
  session token. The contract is unchanged: nil resolver (invitations off) is a
  no-op, a resolver error is a WARN line that never fails provisioning or the OAuth
  login, and the invitation service audits each grant/failure itself. **This was
  previously the one lifecycle hole in the add-or-signup product shape**: a host
  that invited an unknown address and had the recipient sign up with Google left
  the invitation pending forever.
  - **No other OAuth branch resolves.** Ordinary login of an already-linked
    identity (branch 1) and pending-link start/completion for an
    already-registered address (branch 2) are not provisioning events, so they
    never re-grant — an address that already belongs to an account is
    direct-added at invitation-create time instead. A host that adopts this tag
    therefore sees at most ONE grant per invitation per account, not one per
    sign-in.
  - Resolution is idempotent: a resolved invitation moves off pending, so a
    second pass is a no-op, and a grant that failed stays pending and is retried
    on the account's next resolve site.
  - Unchanged and deliberate: the invitee lookup that decides direct-add vs
    pending invitation is an ACCOUNT-EXISTENCE test, so an account still awaiting
    registration verification is direct-added like any other registered email.
    `Config.RequireVerifiedEmail` remains the independent password-login gate —
    such a user holds the relation but cannot log in until `Verify` succeeds.
    Non-auto-accept invitations are never resolved this way.
- **`invited_by` is added to the invitation JSON response** (pending create, the
  resource and `mine` lists, and resend all share one mapper). It is the owning
  user id — the same value cancel/resend ownership is already enforced on — so an
  admin-facing list can hide actions on another admin's rows. **Purely additive**:
  no field was removed or renamed, the response still carries no token, and the
  server enforces ownership regardless of what a client renders. A client that
  ignores the field is unaffected.

### integrations/email/sendgrid — v0.2.0 (2026-08-15): truthful capability metadata (minor)

coordination-hub `email-and-invitations` task U1 (plan of record, in the
Coordination-Hub repo, `.claude/plans/email-and-invitations.md`). Additive
(**minor**): `Sender` now implements `email.CapabilityReporter`. The report is
instance-sensitive — it describes the configured `Config.Host`, not merely the
package default:

- empty host (SendGrid's default `https://api.sendgrid.com`) or an explicit
  `https://` host → `{TransportSecurity: TLS, DevelopmentOnly: false}`;
- any non-HTTPS host (an httptest server, a local emulator) →
  `{TransportSecurity: None, DevelopmentOnly: true}` — a test instance can no
  longer claim production capability.

Why it matters to adopters: `features/authentication`'s production runtime mode
fail-closes on senders that declare no metadata or declare `DevelopmentOnly`.
Before this tag a SendGrid sender was indistinguishable from a console mailer to
that gate; after it, `AUTH_RUNTIME_MODE=production` can boot on a real SendGrid
instance while test-pointed instances still fail closed. No exported API
removed or renamed; `go.mod` unchanged.

### integrations/datastores/pgxdb — v0.3.0 (2026-08-14): durable rate limiter — this tag ships HOST SCHEMA (minor)

coordination-hub-auth-upstream U5. The Go surface is additive (**minor**): `Limiter`,
`NewLimiter(db *DB, opts ...LimiterOption)`, `WithLimiterKeyPrefix(prefix)` (default
namespace `ratelimit:`), and `(*Limiter).StatusCheck(ctx)`. `NewLimiter` satisfies
`sdk/capabilities/ratelimiter.Limiter` over the pool the host already owns — a
cross-instance limiter with no Redis, the `kvstores/goredis` multi-port precedent.
Semantics mirror goredis deliberately (`Requests + Burst` ceiling, independent keys,
sliding-window tail, `Reset`, `Remaining`/`ResetAt`/`RetryAfter`), every decision is
one atomic `INSERT … ON CONFLICT DO UPDATE … RETURNING`, all time is Postgres server
time (weights clamped, so a stale `now` can never over-count or exceed the window),
and a non-positive window or a zero ceiling is `sdk.ErrInvalidInput` rather than a
silent deny-everything. `Close` is an idempotent no-op and never closes the caller's
pool. Full reference DDL, pruning statement, and operational notes live in
`integrations/datastores/pgxdb/README.md` — this tag is **not** adoptable from the
Go API alone:

- **This release ships host schema, not just an API.** The limiter requires a
  host-owned `ratelimit_windows` table plus its `expires_at` index. The connector
  creates and migrates NOTHING. **Copy the reference DDL into your own migration
  ledger and apply it BEFORE deploying the binary that constructs a `Limiter`:
  schema first, then binary.** A binary deployed against a missing table fails OPEN
  and SILENT wherever the caller fails open (see the posture note below).
- **The DDL is a versioned contract with no compiler signal.** Nothing in Go tells a
  host that its copied table drifted from the shape this connector's SQL expects. A
  future column/index/semantic change to the reference DDL is therefore a
  **breaking** change for adopters even when the Go API is untouched, and MUST be
  released with an explicit schema upgrade note here.
- **Verify the table at boot.** `limiter.StatusCheck(ctx)` probes for it and reports a
  missing table as `sdk.ErrNotFound` (SQLSTATE `42P01`); refuse to start on error.
  Note it is a method on the concrete `*pgxdb.Limiter`, not on the
  `ratelimiter.Limiter` port — the host must hold the concrete type at boot to call
  it.
- **Pruning scheduling, autovacuum, and pool sizing are launch prerequisites, not
  follow-ups.** Every checked request is one non-HOT write (indexed `expires_at` is
  rewritten each `Allow`), so this becomes one of the highest-churn small tables in
  the database. The host schedules
  `DELETE FROM ratelimit_windows WHERE expires_at < now();` (cron, `features/jobs`, or
  pg_cron — the connector never runs it), tunes the storage parameters commented into
  the DDL, and sizes the pool for a limiter check on every rate-limited request. The
  limiter sets no internal deadline: it inherits the caller's context, so give that
  context a deadline you are willing to serve. `UNLOGGED` is a supported tradeoff
  (halves WAL, truncated on crash recovery); the limiter assumes the stock
  `READ COMMITTED` default and never auto-retries `40001`.
- **The `key` column is a durable PII surface.** It persists whatever the host puts in
  its keys verbatim — with authentication wired that includes client IP addresses and
  user identifiers — and it inherits the retention of your backups, WAL archive, and
  replicas. **Pruning is the retention control**, not just capacity hygiene: keep the
  table inside whatever DSR/erasure process covers request logs, prefer opaque or
  digested key material, and do not enable `Config.LogQueries` on a connection with
  the limiter wired (the tracer logs SQL args verbatim, IPs included, into logs that
  are retained longer and read more widely than this table).
- **Swapping `ratelimiter.NewMemory()` → `pgxdb.NewLimiter(db)` is a security-relevant
  behavior change.** A Memory limiter cannot fail; this one can, and today's callers
  disagree on posture: `sdk/capabilities/ratelimiter.Middleware` fails **OPEN** (the
  error is swallowed and the request proceeds unthrottled) while authentication's
  login and passwordless call sites fail **CLOSED** (the attempt is rejected; its
  refresh path fails open). After the swap a database incident is an availability
  event on the fail-closed paths and an unmetered brute-force window on the fail-open
  ones. Decide per path before the swap, and monitor limiter error rate and latency
  as first-class signals.

### integrations/datastores/{pgxdb,turso} — v0.2.0: crud.Transactor implemented (minor)

transactor-connectors (2026-08-14) implemented the sdk transaction seam in BOTH
datastore connectors — the seam's first consumer arrived (the Coordination-Hub
timeline cascade, gpsimpact/Coordination-Hub#13). Additive only, so a **minor**
floor: `(*DB).Transact` (commit on nil; rollback + fn's error unwrapped;
rollback + re-panic on panic), the connector-typed `TxFromContext(ctx) (*Tx,
bool)`, and `(*DB).QuerierFrom(ctx) Querier` (ambient tx when inside a
Transact, else the pool). `InTx` and every existing symbol are unchanged.
**Nesting is now RULED (was explicitly unpinned):** Transact inside an active
Transact fails loud with the connector's `ErrNestedTransact` — the consumer's
one-transaction-per-workflow invariant makes a silent nested begin an
atomicity-splitting hazard; the error can graduate into defined behavior later
without breaking anyone. The sdk contract comment (`sdk/foundation/crud/tx.go`)
records the ruling; comment-only, no sdk API change.

### sdk — v0.2.0 (2026-08-14): FencedDeadLetterFunc carries the failure reason

`workers.FencedDeadLetterFunc[T]` gained a `reason string` parameter —
`func(ctx, job T, reason string) error` — carrying the terminal failure reason
exactly as `Fail` durably recorded it (the runner cannot mutate an arbitrary
`T`, so the reason rides alongside the as-claimed job value). A breaking
exported signature change, released as a **minor** bump per the pre-`v1`
convention above. Any hook registered via `FencedRunner.SetDeadLetterHook`
adds the parameter; `features/jobs`' frozen host-facing `DeadLetterFunc` is
unchanged (see its v0.1.1 note).

### features/jobs — v0.1.1 (2026-08-14): DeadLetterFunc's job carries FailureReason

Behavior fix, no exported API change (**patch**; pins `sdk v0.2.0`): the
per-kind `DeadLetterFunc`'s `j.FailureReason` — structurally always empty
before this tag — is now populated with the recorded terminal reason before
the hook runs (Coordination-Hub issue #5). The AV3D-1.4 compile seam
asserting `DeadLetterFunc` ≡ `workers.FencedDeadLetterFunc[job.Job]` is
removed; the runtime's dispatch closure is the adapter. Hosts that retained
their own handler-error fallback for the empty field can drop it.

### features/jobs — v0.2.0 (2026-08-14): optional tenant metadata

Additive (**minor**): `job.Job`/`job.Enqueue`/`schedule.Schedule`/
`schedule.Ensure` gain `TenantID string` — an OPTIONAL, host-defined boundary
slot, vocabulary only (the events posture): empty = none, stores map `"" ↔
NULL`, the feature attaches no semantics. `jobs.FencedClaim` carries the
claimed job's `TenantID`. The fenced path gains struct-input siblings
`EnqueueOnceIn(ctx, EnqueueOnceInput{Kind, LogicalKey, Payload, TenantID})` /
`ReplaceIn(ctx, ReplaceInput{...})`; the frozen `work` protocol methods
(`EnqueueOnce`/`Replace`/`LatestStatusByKey`) are unchanged and delegate with
empty tenant — the sdk `work` protocol's vocabulary does NOT gain tenant. A
schedule's tenant is copied onto each job it fires (vocabulary carry-through so
tenant-scoped ops queries see fired work). No new query APIs: the column's
consumer is operator SQL. Pins `sdk v0.2.0`.

### features/jobs stores (pgx + turso) — v0.2.0 (2026-08-14): tenant_id columns

Per the greenfield-migrations rule, nullable `tenant_id TEXT` is folded into
the canonical CREATEs of `0001_job_queue.sql`, `0002_job_schedules.sql`, and
`0003_fenced_job_queue.sql` in BOTH dialects — **no evolution file ships**, so
an already-migrated host's runner will refuse the changed canonical files
(checksum mismatch) until the host applies its own host-tree migration and, if
its ledger keys canonical filenames, reconciles it per its own migration
policy. Reference host ALTER (both dialects; the partial index is the fenced
queue's — the encrypted-payload rail where SQL is the ONLY way to scope rows,
the reason this column exists at all):

```sql
ALTER TABLE job_queue        ADD COLUMN tenant_id TEXT;
ALTER TABLE job_schedules    ADD COLUMN tenant_id TEXT;
ALTER TABLE fenced_job_queue ADD COLUMN tenant_id TEXT;
CREATE INDEX IF NOT EXISTS idx_fenced_job_queue_tenant
    ON fenced_job_queue (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
```

Storetest conformance gains tenant round-trip cases plus a
tenant-does-not-affect-keying proof (enqueue-once dedup and Replace
supersession ignore tenant — it is metadata, never part of the logical key).
Both dialects passed live conformance for this tag (pgx against postgres:17,
turso against a real libSQL server).

### features/authentication — next tag: session hashing invalidates all live sessions

auth-v2 (2026-07-07) moved session-token storage to service-side SHA-256
hashing (design §7.3): the service hashes the cookie token before every
repository call, and stores keep persisting an opaque string — no DDL. Any
host upgrading `features/authentication` across this change must know:

- **Every live session is invalidated at deploy — a forced logout for all
  users, remember-me/long-TTL sessions included** (a v1 plaintext row never
  matches a hashed lookup again). Users just log in again; no data is lost.
- The orphaned plaintext rows are unreachable and dead past their natural
  `expires_at` TTL. No purge ships; hosts may vacuum them at leisure.
- **Deploy in a single cutover or drain traffic first — do not roll.** On a
  rolling deploy, mixed plaintext/hashed pods make the SAME session cookie
  flap 401/200 depending on which pod answers, for the whole rollout window.
- **A rollback forces a SECOND mass logout**: sessions minted by the new
  binary are hashed rows the old binary cannot read.

The same note lives in `features/authentication/README.md` (the upgrade note section).

### features/authentication stores — next tag: invitation identifier kinds

identity-resolution (2026-07-10) gave invitations an `identifier_kind` column
(`TEXT NOT NULL DEFAULT 'email'`) and widened the pending-tuple unique index
to include the kind. Per the greenfield-migrations rule (2026-07-12) the
column lives in `0011_invitations.sql`'s CREATE — no evolution file ships. A
host that scaffolded the pre-kind table adds the column with its own host-tree
migration (`ADD COLUMN` + drop/recreate of the pending-tuple index); rows
default to `email`, and hosts that only ever create email invitations see zero
behavior change.

### sdk — next tag: the layering split moved every sdk subpackage import path

sdk-layering (2026-07-10) re-homed `sdk/errs` into the root package
(`sdk.ErrNotFound`) and moved every other sdk package under
`sdk/foundation/` or `sdk/capabilities/` (package names unchanged, paths
only). Pre-tag there is no version obligation, but any consumer pinned
to a git SHA must re-path its imports wholesale; the workshop CLI's
emitted scaffolds already use the new paths.

### sdk — next tag: additive middleware symbols (minor floor)

middleware-consolidation (2026-07-11) added exported symbols to two sdk
packages, all backward-compatible — they floor sdk's next tag at a **minor**
bump, never a major:

- `sdk/capabilities/ratelimiter`: `Middleware` + the `Allower` port (the generic
  IP/key rate-limit middleware, relocated out of `features/authentication`'s
  internals).
- `sdk/foundation/web`: `TrustProxies` + `ClientIP` (the rightmost-minus-N
  client-IP resolver ported from the original gopernicus `httpmid`).

No existing symbol changed; a consumer that ignores the new surface is
unaffected.

### features/authorization — next tag: additive gate symbols (minor floor)

middleware-consolidation (2026-07-11) added `RequirePermission`,
`ResourceResolver`, and `FixedResource` to the root package (the exported HTTP
middleware gate; implementation in `internal/logic/authorizersvc`). Additive —
floors the next tag at a **minor** bump. Adopter note: replacing a hand-rolled
gate closure with `RequirePermission` changes the 401/403 response *body* to the
FS9 `web.Error` shape (status codes unchanged) — a client contract detail, not a
Go-API break.

### features/authentication — next tag: patch-only (internal delegation)

middleware-consolidation (2026-07-11) rewrote `Service.RateLimitByIP`'s body to
delegate to `ratelimiter.Middleware` with its exact prior signature and
semantics; no exported surface changed. This is a **patch**, not a minor — the
proof is auth's existing rate-limit tests passing unmodified. (Superseded for the
same tag by the refresh change below AND the auth-v3 identity cut, which force a
**breaking** bump; the additive HTML resource-policy seam — ui-goth GOTH-0.4 — folds
into that same breaking cut, see its note below.)

### features/authentication — next tag: JWT sessions + refresh rotation (BREAKING)

The refresh change (auth-jwt-session-refresh, 2026-07-11, amends AV6) makes the
access credential a self-validating JWT and turns the session row into the
revocation + refresh anchor. It re-keys `session.SessionRepository` and the
`Config`/`Service` surface — a **breaking version bump** for the feature. The
runbook (mirrored in `features/authentication/README.md`):

- **All live sessions invalidate at deploy — a forced logout for every user**
  (the sessions table is re-keyed; an upgrading host's own migration drops and
  recreates it). No data lost; users log in again.
- **Do NOT roll-deploy across the sessions re-key.** Old binaries SELECT the
  dropped `token` column and **error outright** (not a 401 flap). Stop old,
  migrate, start new.
- **Rollback = restore the old schema AND force a second logout** (the new
  binary's id-keyed rows are unreadable by the old binary).
- **`AUTH_JWT_SECRET` is now required — and required-SHARED for multi-instance
  hosts.** `Config.TokenSigner` is required (`ErrTokenSignerRequired` on nil);
  per-instance ephemeral keys cannot cross-verify behind a load balancer, so a
  multi-instance host MUST share one secret across every instance. Ephemeral
  keys are a single-instance dev convenience only.
- **`Config.TokenTTL` is removed** (compile-time break): replace with
  `AccessTokenTTL` (default 15m) and `RefreshTTL` (default 7d). No host silently
  inherits the old 1h access window.
- **`POST /auth/token` response changed** from `{token, expires_at}` to
  `{access_token, expires_at, refresh_token}` — a breaking client contract;
  API clients now rotate via `POST /auth/refresh`.
- **The session repository port was re-keyed** (id-keyed; `Get`,
  `GetByRefreshHash`, `Rotate`/`ConsumeGrace` CAS, `Delete`, `DeleteByUser`;
  new `ErrRotationConflict`) — a breaking bump for the feature AND both nested
  store-module tags (see below).

### features/authentication stores — next tag: sessions re-key (BREAKING) + greenfield migration set

auth-jwt-session-refresh (2026-07-11, D6) re-keyed sessions to an id-keyed
anchor with `refresh_token_hash` (UNIQUE index), `previous_refresh_token_hash`
(nullable, partial index), `previous_used`, `rotation_count`, and a `user_id`
index. The store adapters implement the re-keyed `session.SessionRepository`
(CAS `Rotate`/`ConsumeGrace`, `GetByRefreshHash`, `MapError`-routed `Create`),
so both nested store-module tags (`features/authentication/stores/turso`,
`.../pgx`) take a **breaking version bump**.

**Greenfield-migrations rule applied (2026-07-12, jrazmi ruling):** the
canonical set defines the FINAL schema and never carries upgrade/evolution
files. The sessions re-key lives in `0003_sessions.sql`'s CREATE, and the
former evolution files (`0012_id_defaults`, `0013_invitation_identifier_kind`,
`0014_sessions_refresh`; cms's `0022_id_defaults`) were folded into their base
CREATEs — auth's set is `0001…0011`, one CREATE per table. New hosts scaffold
the final shape. A host that scaffolded an earlier shape writes its OWN
migration in its host tree (reference SQL in the feature README's upgrade
note; segovia's `0018_sessions_refresh.sql` is the exemplar). Same
no-rolling-deploy / rollback runbook as the feature entry above.

### features/cms stores — next tag: id defaults folded into base CREATEs

The greenfield-migrations rule also folded cms's `0022_id_defaults.sql` into
the six entity tables' CREATE files (terms, menus, menu_items, assets,
inquiries, entries) in BOTH dialects. Schema-identical for any DB that had
already applied 0022; a host that scaffolded before the id-defaults change
adds the defaults with its own host-tree migration.

### features/authentication + both store modules — next tag: auth v3 identity (BREAKING)

auth-v3 (the identity milestone, 2026-07-13) reshapes the feature off a single
`users.email`/`email_verified` pair onto the `user_identifiers` table (multiple
email/phone identifiers with explicit login/recovery/notification/primary uses),
adds `users.auth_revision` (the optimistic-serialization anchor) and session
authentication-metadata columns, adds the `challenges` / `contact_changes` /
`authentication_grants` flow tables, and retires the legacy
`verification_codes` / `verification_tokens` rail. This is a **breaking** bump
for `features/authentication` and BOTH nested store-module tags
(`features/authentication/stores/{pgx,turso}`): the `Repositories` bundle grows
identifier/challenge/contact-change/grant/credential ports, `user.User` loses its
email field, and routes/entities change.

The **AV3D delivery-runtime refactor** (2026-07-13, same untagged milestone) folds
into this cut: it removed authentication's private durable delivery queue, so the
canonical set is `0001…0013` with **no** delivery table (an earlier v3 cut's
`delivery_jobs` table was removed). Durable delivery is now the generic **jobs**
feature reached through `Config.DeliveryDispatcher`; the bounded ephemeral path is
`in_process`. Public removals: `Repositories.DeliveryJobs`, `domain/deliveryjob`,
`Service.RunDeliveryWorker`, and the delivery-durability errors. Rename:
`Config.DeliveryWorkerAcknowledged` → `DeliveryJobsAcknowledged`. Additions:
`Config.DeliveryMode`/`DeliveryDispatcher`/`InProcessDelivery`/`DeliveryEventsEmitter`/
`DeliveryEphemeralAcknowledged`, `Service.RunDelivery`/`DeliveryJobRuntime`, and the
generic-jobs fenced surface (`Repositories.FencedQueue`, `jobs.FencedRuntime`,
migration `0003_fenced_job_queue`). Behavior: `DeliveryStatus.Attempt` now reads 0.
The host-side upgrade is the **Auth delivery-runtime upgrade runbook** below.

Per the greenfield-migrations rule (2026-07-12) the canonical migration set
ships only the FINAL schema and carries **no** upgrade/evolution file — a live
v2 host owns its own evolution and MUST NOT blind-copy the canonical migrations
(the final `0001_users.sql` no longer carries `email`, so copying it onto a
populated v2 `users` drops email before any backfill). The host-owned,
backfill-first, validated migration procedure — exact pgx and SQLite/libSQL SQL
for both dialects — is the **Auth v3 host upgrade runbook** below. The same note
is mirrored in `features/authentication/README.md`.

### features/authentication + both store modules (+ authorization pgx) — next tag: auth adopter hardening (BREAKING, pre-tag)

auth-adopter-hardening (2026-07-15, task prefix `AAH`) is a **pre-tag breaking**
hardening packet folded into the same untagged auth-v3 cut — preflight re-confirmed
zero `features/authentication*` tags (`git tag -l` empty, 2026-07-15), so the
public-API and canonical-migration edits carry no append-only constraint. It closes
the framework gaps the first authorization-v3/authentication-v3 adopter exposed. The
breaking surface:

- **`auth.Granter` is now structured and fail-loud (D1/D2).** `Grant(ctx,
  resourceType, resourceID, relation, subjectType, subjectID string) error` became
  `Grant(ctx, GrantInput) error`, where `GrantInput{OperationID, ResourceType,
  ResourceID, Relation, SubjectType, SubjectID}` carries an operation-scoped identity
  (persisted invitation row id for accept/resolve-on-registration; a fresh
  high-entropy id from the unconditional secret generator — never `Config.IDs` — for
  direct-add). Success (nil) now means the EXACT requested relation was applied or is
  already exactly present; a different existing relation, an invariant refusal, a
  missing/deleted host resource, or any infrastructure error must fail loud (no
  implicit replace). A host adapter inspects its authorization receipt outcome. Any
  host implementing `Granter` updates to the struct signature and the outcome-aware
  contract (the `examples/auth-cms` `relationshipGranter` is the reference).
- **`Config.InviteCheck` is REQUIRED with a `Granter` (D3).** New `InviteAction`
  (`InviteCreate`/`InviteList`), `InviteCheckRequest{Principal, Action, ResourceType,
  ResourceID, Relation}`, and the `InviteCheck` func. A `Granter` with a nil
  `InviteCheck` is `ErrInviteCheckRequired` at construction; an `InviteCheck` with no
  `Granter` is `ErrInviteCheckWithoutGranter`. The feature's parsed create/list HTTP
  handlers call it after live-session validation and principal resolution;
  host-direct `Service` methods are trusted composition calls that skip HTTP policy.
  All authenticated invitation routes (create, resource-list, mine, accept, cancel,
  resend) moved from `RequireUser` to `RequireLiveSession` (immediate revocation);
  public decline is unchanged. Invitation authority is **issuance-time**: an issued
  invitation is a durable expiring capability, and acceptance does not re-check
  inviter authority.
- **Both store constructors now return `(auth.Repositories, error)` (D4).**
  `features/authentication/stores/{pgx,turso}.Repositories(db)` probe all 13 canonical
  tables before returning and error (`sdk.ErrNotFound`, naming the table + the
  `authentication` migration source) when one is missing — pgx via `to_regclass`,
  turso via `sqlite_master`. Constructors never apply schema. Every call site (the
  proof host, store harnesses) takes the new return signature.
- **Canonical pgx migrations carry per-column `COLLATE "C"` (D5, pre-tag fold).**
  The opaque text keyset/derived-key columns fold `COLLATE "C"` into their canonical
  CREATEs: authentication `service_accounts.id`, `api_keys.id`, `security_events.id`,
  `invitations.id`; authorization `iam_relationships.relationship_id` and all five
  `iam_roles` derived-key columns (`subject_type`, `subject_id`, `role`,
  `resource_type`, `resource_id`). Byte-order pagination parity now holds on any
  database's default collation. Deliberate **EXCLUSION**: the `iam_relationships`
  recursion columns (`resource_type`, `resource_id`, `relation`, `subject_type`,
  `subject_id`, `subject_relation`) are left uncollated — collating them raises
  SQLSTATE 42P21 in the recursive reachable CTE (the anchor seeds default-collation
  parameters), and those queries need only deterministic order/equality. Human
  display/content columns are untouched; Turso migrations are unchanged.

**v1→v3 collation upgrade caveat.** `CREATE ... IF NOT EXISTS` no-ops on a
pre-existing table, so a host upgrading from a pre-v3 schema does **not**
retroactively gain the per-column collation on already-created tables. Per the
standing greenfield-migrations rule the canonical set ships the final schema only and
hosts own their own schema evolution — a host that needs the per-column collation on
an existing table adds it with its own host-tree migration (`ALTER TABLE … ALTER
COLUMN … TYPE TEXT COLLATE "C"`) or runs the database in the `C` locale. No canonical
upgrade/evolution file ships. A `C`-locale database remains a supported
belt-and-suspenders posture either way.

These changes fold into the auth-v3 **major / breaking** floor already recorded for
`features/authentication` and both nested store modules; the authorization pgx
collation fold rides the `features/authorization` first-tag breaking-vintage cut (no
authorization Go-API change). The `examples/auth-cms` proof host (never tagged)
carries the reference composition: the structured `GrantInput` granter, a host
resource-existence check, receipt-outcome mapping, and the relation-aware
`InviteCheck`.

### features/authentication — next tag: optional HTML resource-policy seam (additive; folds into the auth-v3 breaking cut)

ui-goth (2026-07-17, GOTH-0.4; Gate C accepted 2026-07-17) added a
technology-neutral, feature-owned HTML resource-policy seam so a selected HTML view
can declare the external styles, scripts, fonts, and images it needs. The whole
surface is **additive** — every change is a new optional field/type, no existing
symbol changed — so on its own it would floor at a **minor**; it folds into the
already-**breaking** auth-v3 identity cut. Adopter-facing surface:

- **New optional `Config.HTMLPolicy *HTMLResourcePolicy`.** `nil` (the default)
  reproduces the historical asset-free CSP **byte-for-byte** (`default-src 'none';
  base-uri 'none'; form-action 'self'; frame-ancestors 'none'; script-src
  'nonce-…'|'none'`) — an upgrading host that leaves it unset sees no header change.
- **New public types/constructor:** `HTMLResourcePolicy` (opaque, validated,
  immutable), `HTMLResourceDirective`, `HTMLResourceKind`, the seven frozen
  widenable-class constants (`HTMLScriptSrc`/`HTMLStyleSrc`/`HTMLImgSrc`/`HTMLFontSrc`/
  `HTMLConnectSrc`/`HTMLMediaSrc`/`HTMLWorkerSrc`), `NewHTMLResourcePolicy` (validates
  loudly, wrapping `sdk.ErrInvalidInput`), and `var ErrHTMLPolicyWithoutViews`.
- **Behavior contract:** a policy only WIDENS the seven frozen resource classes and
  can NEVER name, relax, or remove a fixed protection (the fixed CSP prefix and the
  `Cache-Control`/`Referrer-Policy`/`X-Frame-Options`/`X-Content-Type-Options`
  headers). A non-nil policy REPLACES the default `script-src` tail entirely, so a
  policy that omits `HTMLScriptSrc` (or supplies it without `Nonce: true`) is
  fail-closed on scripts. Setting `HTMLPolicy` with a nil `Views` is
  `ErrHTMLPolicyWithoutViews` at construction (contradictory wiring, never a silent
  no-op). The seam validates directive STRUCTURE, not source VALUES — source-value
  hardening rests on the view adapter / host.
- **No new dependency.** The feature core imports no templ, Alpine, HTMX, or
  `ui/goth`; `HTMLResourcePolicy` is a plain value. The `ui/goth` authentication view
  adapter that maps `goth.Bundle.Requirements()` into a policy lands in GOTH-7.2.

### features/authentication — next tag: browser HTML seams (additive; folds into the auth-v3 breaking cut)

W2 (2026-07-20, flags #9 + #12) added the browser-facing HTML seams a host mounting
the bundled views needs, all **additive** — no existing symbol changed, every JSON API
byte-stable — so on their own they floor at a **minor**; they fold into the
already-**breaking** auth-v3 identity cut. Adopter-facing surface:

- **New `Service.RequirePrincipalBrowser` / `Service.RequireLiveSessionBrowser`.** The
  browser-facing siblings of `RequirePrincipal` / `RequireLiveSession`: same credential
  resolution and context stash, but on an authentication denial they **303 to
  `Config.BrowserLoginPath`** instead of writing a JSON 401 — a denied GET/HEAD carries
  a validated `return_to` of the original path+query, an unsafe method none. They never
  sniff `Accept` or Fetch Metadata; mount them on HTML routes. The JSON gates keep their
  byte-stable 401.
- **New optional `Config.BrowserLoginPath string`** (`AUTH_BROWSER_LOGIN_PATH`). Empty
  defaults to `/auth/login`; a non-empty value must be a safe root-relative path or
  construction fails with the new `var ErrBrowserLoginPathInvalid`. It configures ONLY
  the browser gates.
- **Form-aware `POST /auth/logout`.** The logout route now content-type dispatches like
  the other shared POSTs: the JSON contract is unchanged, and a form body (Views wired)
  clears both cookies and 303s to `/auth/login`; nil Views → 415. Logout stays
  origin-only (no double-submit token, D2). The bundled `features/authentication/views/goth`
  account page gains a matching sign-out form (that module tagged separately below).
- **No new dependency.** The feature core imports no templ or `ui/goth`; the shared
  safe-relative-path validator (`redirect.SafeRelativePath`) is feature-internal.

### features/authentication/views/goth — next tag: NEW module (first tag; renamed from views/templ)

auth-v3 (2026-07-13, AV3-8.2) added the feature's bundled default HTML view module
(the thirty-seventh workspace module), sibling to `features/cms/views/goth`, as
`features/authentication/views/templ`. ui-goth GOTH-7.2 (2026-07-18) **renamed the
module path to `features/authentication/views/goth`** (Gate A's tag-sensitive rule —
the untagged module is renamed in place, no compatibility shim) and re-implemented it
on `ui/goth`: the default auth pages now render through the `ui/goth` primitives/
components and the fingerprinted asset bundle. It carries a `HTMLPolicy()` that maps
`goth.Bundle.Requirements()` into `authentication.HTMLResourcePolicy` (the host wires
it into `Config.HTMLPolicy`), and it **externalizes the reset/magic-link
fragment-token readers** into a served `fragment.js` (`FragmentScriptHandler`,
`DefaultFragmentScriptPath`) so the pages run under a CSP whose `script-src` is
`'self'` + the per-render nonce, with no inline script. Construction is now
`New(bundle *goth.Bundle) (Views, error)` (was `New()`); a host that renders its own
views never imports it. **Migration for an adopter on the old path:** update the
import/require/replace from `.../views/templ` to `.../views/goth`, pass a
`*goth.Bundle` to `New`, serve the bundle assets + `FragmentScriptHandler()`, and set
`Config.HTMLPolicy = views.HTMLPolicy()`. The feature core stays presentation-free
(`Config.Views == nil` is API-only; the feature core imports no templ or `ui/goth`).
This is a **new, standalone module getting its first tag** (no prior tag existed on
the old path); it depends on `features/authentication` and `ui/goth` and is tagged
independently like every other importable module. W2 (2026-07-20) additionally added a
POST `/auth/logout` sign-out form to the account page (origin-only, no `@csrfField`).

### features/cms/views/goth — next tag: NEW module (first tag; renamed from views/templ)

feature-standard B2 (2026-07-07) added the CMS feature's bundled default HTML view
module `features/cms/views/templ`. ui-goth GOTH-7.3 (2026-07-18) **renamed the module
path to `features/cms/views/goth`** (Gate A's tag-sensitive rule — the untagged module
is renamed in place, no compatibility shim) and re-implemented it on `ui/goth`: the
default CMS pages (public site chrome + admin management) now render through the
`ui/goth` primitives/components and the fingerprinted asset bundle. The admin
entries-list is HTMX-enhanced (the status filter, created_at sort toggle, and
pagination swap the `#cms-entries-content` region via explicit `hx-*`, degrading to
full-document no-JS reloads); the feature core reads the `HX-Request` header as a
presentation hint only and gains no templ/`ui/goth` dependency. Construction is now
`New(bundle *goth.Bundle) (Views, error)` (was `New()`); a host serves the bundle
assets under the path it names. The CMS `Views` port gained one method
(`EntriesListContent`, the HTMX content fragment) and `Pager` gained a `Status` field.
**Migration for an adopter on the old path:** update the import/require/replace from
`.../views/templ` to `.../views/goth`, pass a `*goth.Bundle` to `New` (embedding hosts
build the bundle and serve its assets), and — if the host implements the `Views` port
by hand rather than embedding the default — add `EntriesListContent`. This is a **new,
standalone module getting its first tag** (no prior tag existed on the old path); it
depends on `features/cms` and `ui/goth` and is tagged independently.

### integrations/datastores/turso — next tag: BEGIN IMMEDIATE write-intent transactions (patch, behavior fix)

auth-v3 (2026-07-13, AV3-2.4) changed `integrations/datastores/turso`'s `DB.Begin`
to issue `BEGIN IMMEDIATE` over a pinned `*sql.Conn` instead of the driver's default
`BEGIN` (DEFERRED); see `integrations/datastores/turso/tx.go`. No exported surface
changed, so this floors the next tag at a **patch**, but it is a **behavior change a
host must know**: the v3 step-up credential/identifier CAS rails (`Apply`,
`ApplyVerifiedChange`) need write-intent-up-front so `sqld` serializes contending
transactions and the loser's own CAS returns `sdk.ErrConflict`. A host on the
**pre-fix connector** gets a raw `SQLITE_BUSY` ("database is locked") to the CAS
loser instead of `sdk.ErrConflict` and **fails the concurrent step-up contract**.
Data integrity is never at risk either way (no double-commit) — but a turso host
adopting auth-v3 must run the connector at or past this tag.

### sdk/capabilities/{email,notify} — next tag: additive capability metadata (minor floor)

auth-v3 (2026-07-13, AV3-4.4) added the production-safety capability seam consumed
by the delivery worker's fail-closed transport gate — additive, so it floors sdk's
next tag at a **minor**, never a major:

- `sdk/capabilities/email`: new `Capabilities`, `TransportSecurity`, and the
  `CapabilityReporter` interface (`capabilities.go`); `Console` reports
  `{TransportSecurityNone, DevelopmentOnly: true}` and `SMTP` reports
  `{TransportSecurityStartTLS, DevelopmentOnly: false}`.
- `sdk/capabilities/notify`: the same trio; `Console` reports development-only.

A consumer fail-closes in production on a `DevelopmentOnly` / metadata-less
transport. No existing symbol changed; a consumer that ignores the new surface is
unaffected. (`sdk/foundation/cryptids`'s HS256 default and `sdk/foundation/web`'s
`TrustProxies`/`ClientIP` were **not** touched by auth-v3 — HS256 belongs to the
JWT-refresh cut and `TrustProxies` to middleware-consolidation, each keyed above.)

### features/authentication v0.3.0 + both store modules v0.2.0 (2026-08-16): passwordless provision-on-consumption (minor; store migration REQUIRED)

coordination-hub auth upstream, phase 6 (`.claude/plans/coordination-hub-auth-upstream/06-passwordless-provisioning.md`,
CHAU-6.1…6.7, 2026-08-16). The plan called for a **separate release train** from
the core admin/resend/reset work, since it is the highest-risk change in the
packet. **The owner ruled one train**, so it shipped on the same tags: it is
additive and default-off, and it raised no bump of its own. What adopters take
regardless is store migration `0015` — the code stays dormant until they set the
flag, but the schema is not optional.

**Default behavior is unchanged.** `Config.PasswordlessProvisionOnRedeem` is false
by default, and with it false a magic link to an address with no account still
delivers nothing and creates nothing.

- **`Config.PasswordlessProvisionOnRedeem`** (env
  `AUTH_PASSWORDLESS_PROVISION_ON_REDEEM`) — opt-in account creation from an
  EMAIL magic link, at CONSUME time. Never phone, OTP, OAuth, or another kind.
  Enabling it requires the email passwordless kind, the challenge rail +
  `ChallengeProtector`, an `IdentifierKeyer`, a delivery runtime, a valid
  `PublicAuthBaseURL`, `Repositories.Passwordless`, and
  `Repositories.ActiveSessions`; anything missing is the new
  `ErrPasswordlessProvisionWiring` at construction.
- **`Repositories.Passwordless`** — the new one-transaction redemption port
  (`domain/passwordless`). The bundled pgx/turso stores always supply it, so a
  host that leaves the flag off is unaffected.
- New audit types `passwordless_provisioned` and `passwordless_adopted`, both
  secret-free (kind + purpose + outcome class; never the address or token).

**Challenge domain generalization (source-visible).** `challenge.Challenge` and
`challenge.Consumed` gain `SubjectKey`, and the single-active claim moves from
`(user_id, purpose)` to `(subject_key, purpose)`. `Challenge.ResolvedSubjectKey()`
defaults it to `UserID`, so **every purpose that predates this is unchanged**; the
field exists because an email magic link may be issued for an address with no
account, and overloading `user_id` with a digest would make one column mean two
things. `authsvc.WithSubjectKey` is the issuer-side option.

**This tag ships HOST SCHEMA:** new canonical migration
**`0015_challenge_subject_keys.sql`** in both trees (append-only;
`0011_challenges.sql` stays byte-identical to its tag). It adds
`challenges.subject_key`, backfills `subject_key := user_id`, drops
`idx_challenges_user_purpose`, and creates `idx_challenges_subject_purpose`.
Dropping the old index is required, not cosmetic: leaving it would forbid two
magic-link challenges that share an empty `user_id` for different addresses. Both
store constructors now probe the ALTER-added columns by name.

**Apply 0015 BEFORE deploying a binary built against these tags.**

**Security properties, each proven rather than asserted** (shared conformance
group `PasswordlessRedeem`, 12 adversarial cases, run against the reference, the
example host's memory store, live PostgreSQL 17, and the live Turso playground):
creation at CONSUME never at SEND; POST-only redemption so a link scanner cannot
provision; the provisioning intent captured at ISSUE and never re-derived from
current configuration; an unknown binding version rejected rather than guessed;
the send-then-register race logging in the CURRENT owner with no duplicate
subject; the unverified-claim adoption revoking the squatter's password, sessions,
grants, and outstanding challenges BEFORE the new session is written; a
deactivated subject refused; two concurrent redemptions committing exactly one
session; and every stable failure collapsing to one generic 401.

### features/authentication — v0.3.0 (2026-08-16): password-reset LINKS (minor; NEW REQUIRED PRODUCTION CONFIG)

coordination-hub auth upstream, phase 5 (`.claude/plans/coordination-hub-auth-upstream/05-password-reset-links.md`,
CHAU-5.1…5.4, 2026-08-16). Closes the "password-reset mail exposes only the raw
token" flag. **No schema change.**

- **`Config.PasswordResetURL`** (env tag `AUTH_PASSWORD_RESET_URL`) — the absolute
  public reset landing route before `?token=` is appended. A **separate** field
  from `PublicAuthBaseURL`, which is the full passwordless landing URL and not an
  origin; deriving a reset route from it would have been guesswork.
- **New required-production configuration.** With the challenge-backed
  forgot/reset rail wired, production construction now FAILS without it
  (`ErrPasswordResetURLRequired`). Development permits empty with one startup
  WARN and keeps the legacy raw-token mail, so a mid-migration local flow is not
  broken. **Adopters must set this before deploying a production binary built
  against this tag.**
- New errors: `ErrPasswordResetURLRequired`, `ErrPasswordResetURLInvalid`
  (non-absolute, non-http(s), a FRAGMENT, or a pre-existing `token` parameter),
  `ErrPasswordResetURLInsecure` (plain HTTP in production). New exported constant
  `PasswordResetTokenParam` (`"token"`), shared by the validator and the builder
  so the two cannot drift.
- **Rendered-output change:** the bundled `password_reset` body is now link-only
  when a URL is configured (CTA + copy/paste address + "ignore this message"), and
  keeps the historical raw-token body when it is not. The derived text alternative
  carries the full link and no second standalone token.
- **Template-override compatibility:** the renderer passes BOTH `.Link` and
  `.Secret`, so an app override reading `{{.Secret}}` keeps working for one
  window. `.Secret` is deprecated for reset PRESENTATION; removing it is a later
  breaking decision. It remains in the encrypted envelope regardless — the
  terminal-failure discard needs it.
- The link is built in the delivery **worker** from the configured value alone.
  Request `Host`/`Forwarded`/`X-Forwarded-*` never participate, proven by driving
  a forgot-password request with all of them hostile and asserting the delivered
  link is unchanged.

### features/authentication — v0.3.0 (2026-08-16): registration-verification resend (minor; additive)

coordination-hub auth upstream, phase 2 (`.claude/plans/coordination-hub-auth-upstream/02-verification-resend.md`,
CHAU-2.1…2.4, 2026-08-16). Closes the "no verification resend" flag. **No schema
change**; the primitives (`deliveryQueue.Replace`, `enqueueRenderedReplace`,
`verify_registration` challenges) already existed — what was missing was a
service method, an off-request-path worker initializer, routes, budgets, and any
documentation.

- **`POST /auth/verification/resend`** — public and unconditional. Always
  `202 {"status":"accepted"}`, byte-identical status/body/headers for unknown,
  malformed, verified, deactivated, and active-unverified targets. Origin-gated,
  **no** CSRF token (the caller has no session — that is the population it
  serves). New budgets: **3 per address per minute**, **10 per client IP per
  minute**, both keyed on PII-free digests and charged before any resolution.
  429 on a budget, 503 on a saturated delivery queue, 500 otherwise.
- **`POST /auth/admin/users/{id}/verification/resend`** — mounts with the admin
  surface (`Config.UserAdminCheck`, action `resend-verification`). May report
  real state: 202 + secret-free receipt, 409 `already_verified` /
  `user_deactivated`, 404 unknown / no verifiable email.
- New exported surface: `Service.ResendVerification`,
  `Service.ResendVerificationForUser`, `StepUpReceipt` (newly aliased at the
  feature root), `ErrVerificationResendRateLimited`, `ErrAlreadyVerified`,
  `ErrUserDeactivated`, `ErrNoVerifiableEmail`.
- Two new security-event types: `verification_resend_requested` (public; **no**
  user id) and `verification_resend_issued` (worker or admin; target user id).
- `PurposeRegistrationVerification` joins the delivery initializer registry, so a
  resend resolves the account, issues the replacement challenge, and renders
  entirely in the worker. Its `Discard` arm sits with the login-OTP arm: both are
  HMAC codes with no delete-by-secret path, so a never-delivered code expires
  under its TTL.

**Behavior note for adopters:** a resend REPLACES the pending job and ISSUES A
FRESH CODE, invalidating the previous one. Replacement cannot retract a provider
call already accepted, so a user may receive both the old and the new mail — only
the newest code verifies. Document that in your UI copy.

### features/authentication v0.3.0 + both store modules v0.2.0 (2026-08-16): account lifecycle and the operator directory (minor; store migration REQUIRED)

coordination-hub auth upstream, phase 1 (`.claude/plans/coordination-hub-auth-upstream/01-user-directory-and-lifecycle.md`,
CHAU-1.1…1.7, 2026-08-16). Closes two flags at once — "no administrative user
listing" and "no account status/deactivation" — as one capability, because a
directory you cannot act from and a deactivation you cannot see are each half a
feature.

**`features/authentication` — additive public surface:**

- `UserStatus` (alias of `user.Status`) with `UserStatusActive` /
  `UserStatusDeactivated`, `ErrInvalidUserStatus`, `UserSummary`,
  `UserStatusChange`, `ErrUserAdminUnavailable`, `ErrUserNotActive`.
- `UserAdminCheck` + `UserAdminCheckRequest` + `UserAdminAction` and its five
  constants — the InviteCheck precedent applied to the user directory.
- `Config.UserAdminCheck`; `Repositories.UserAdmin` and
  `Repositories.ActiveSessions`; `ErrUserAdminReposRequired`.
- Trusted `Service` methods `UserAdminEnabled`, `ListUsers`, `GetUserSummary`,
  `DeactivateUser`, `ReactivateUser` — they apply NO authorization by design.
- Two new security-event types: `user_deactivated`, `user_reactivated`.
- `user.User` gains `Status` / `StatusChangedAt` and an `Active()` method. This is
  a struct-field addition: a host constructing a `user.User` by hand still
  compiles (the zero Status normalizes to active), but a host with an exhaustive
  positional literal would not — no such construction exists in-repo.

**Behavior changes to be aware of:**

- With `Repositories.ActiveSessions` wired — the bundled stores always supply it —
  **every** session mint routes through the fenced insert. A store that cannot
  serialize it against a status transition must leave the slot nil.
- An **act-as-user API key now denies a deactivated owner**. A key whose service
  account has `ActAsUser` resolves the owner's status; an unknown owner is
  deliberately NOT treated as deactivated (the vocabulary has no "deleted"), so
  no existence requirement was added.
- Public credential endpoints collapse a lifecycle refusal into their own generic
  failure, so a deactivated login is byte-identical to a wrong password. Nothing
  that previously succeeded now fails differently; this only constrains a NEW
  failure mode.
- `Config.UserAdminCheck` set without both repositories is a LOUD construction
  error. Wiring the repositories without the check is not an error and mounts
  nothing.

**`features/authentication/stores/{pgx,turso}` — this tag ships HOST SCHEMA.**

New canonical migration **`0014_user_status.sql`** in both trees (append-only;
`0001_users.sql` stays byte-identical to its tag):

- `users.status` — `TEXT NOT NULL DEFAULT 'active'` with a closed
  `CHECK (status IN ('active','deactivated'))`; existing rows backfill to active
  from the DEFAULT.
- `users.status_changed_at` — nullable; NULL until the first transition.
- `idx_users_created_at_id` — the directory's contractual keyset.
- **pgx only:** `ALTER TABLE users ALTER COLUMN id TYPE TEXT COLLATE "C"`.
  `users.id` became a keyset tiebreak with this release and joins the contractual
  collation inventory. It is a **rewriting DDL under ACCESS EXCLUSIVE** — call it
  out in the adopter's maintenance window.

Both `Repositories()` constructors gained a **column** probe (`users.status`,
`users.status_changed_at`) beside the existing table probes, so a host that
copied 0001–0013 and skipped 0014 fails at wiring time naming the column rather
than mid-request. Both bundles now always return `UserAdmin` and `ActiveSessions`.

**Adopter order: apply 0014 BEFORE deploying a binary built against these tags.**

One cross-dialect defect was found and fixed by the live gate: the pgx directory
list now normalizes its cursor order value to UTC (`OrderValueOf` → `.UTC()`).
Without it Postgres encodes the session-zone offset and libSQL encodes `Z`, so
byte-identical cursors across dialects would have been false for identical data.
The other pgx paged stores still encode the session offset; that is pre-existing
and untouched here, but worth a look when their contracts next move.

Live gates closed at authoring time: full `storetest` conformance including the
repeated concurrent deactivate-versus-mint race, against a live PostgreSQL 17 and
the live Turso playground database, plus the pgx collation catalog check
confirming `users.id` reports collation `C`.

### sdk v0.4.0 + pgxdb v0.4.0 + turso v0.3.0 + features/authentication v0.3.0 (+ its stores v0.2.0) — 2026-08-16: LIST SEARCH restored (minor)

Plan of record `.claude/plans/crud.md` (crud-search-upstream, T1–T4, 2026-08-16;
stacked onto this release train by owner direction). This is a **regression fix**,
not a feature request: v1 generated a `@search:` annotation, a `SearchTerm` filter
field, a store-side predicate, and a transport key, and the de-generation dropped
all of it. Two downstream repos had already invented their own word for it.

**`sdk/foundation/crud`** — `SearchField`, `ListParams.Search`,
`ListRequest.Search`, and `MatchesSearch`, plus the `q` entry in the package doc's
query-param vocabulary. Additive; a caller that sends no search is unaffected.

`MatchesSearch` is the SHARED oracle: a case-insensitive **LITERAL** substring
under an **ASCII-only** fold. Both halves are deliberate. Literal, because v1
built `"%" + term + "%"` unescaped — someone typing `100%` matched every row and
`a_c` matched `abc`; that defect is not restored. ASCII-only, because the fold
must be reproducible in three places that cannot share code (this function,
PostgreSQL's `ILIKE`, SQLite's `LIKE`), and all three agree on ASCII letters while
leaving non-ASCII code points alone. Go's `strings.ToLower` would have made the Go
matcher disagree with both dialects.

**`integrations/datastores/pgxdb`** — `ListQuery.SearchFields`,
`AddSearchClause`, `EscapeSearchTerm`, and the reserved `@list_search` argument.
The predicate is `(("col" COLLATE "C") ILIKE @list_search ESCAPE '\' OR …)`.
`COLLATE "C"` is required: `ILIKE` under a non-deterministic collation errors, and
under a locale collation its folding would diverge from the other two.

**`integrations/datastores/turso`** — the positional twin. **SQLite has no
`ILIKE`**; its `LIKE` is already ASCII-case-insensitive, so the predicate is
`("col" LIKE ? ESCAPE '\' OR …)` with one bound argument per field (positional
placeholders cannot be reused the way a named argument can). That dialect
difference lives in the store, where it belongs.

**The query fan-out, which is where this would have gone wrong.** The search
predicate is folded into `BaseSQL` **before** the strategy switch, so all FOUR
query paths inherit it: the cursor page, the offset page, the cursor strategy's
reverse probe, and the `COUNT(*)` wrap. Appending it to a per-page buffer — the
way the cursor predicate is appended — would make `WithCount` report the
**unfiltered** total (a page of 2 rows claiming 8 results) and let the reverse
probe derive `HasPrev`/`PreviousCursor` from rows the search excluded. Both
connectors clone their args before binding, because `ListQuery` is passed by value
but its args map/slice is not.

**`features/authentication` + both store modules** — `apikey.SearchFields`
declares `name` as the first searchable column (the key hash and prefix are
credential material and are deliberately NOT searchable — a searchable prefix
would let a caller probe for a key by fragment), both dialect stores declare it,
`GET /auth/service-accounts/{id}/keys?q=…` parses it, and the reference/host
memory stores apply `crud.MatchesSearch` so they cannot disagree with SQL.

**Fail-loud rules:** a non-blank term against a list declaring no `SearchFields`
is `sdk.ErrInvalidInput`, never a silently unfiltered page; an existing
`@list_search` argument fails rather than being overwritten; an invalid column
identifier fails before any SQL runs.

**Live cross-dialect oracle CLOSED at authoring time.** The full term table —
including `100%`, `a_c`, a lone `%`, a lone `_`, a literal backslash, ASCII case
pairs, and non-Latin text — ran against a live PostgreSQL 17 and the live Turso
playground database through the store conformance suite, plus `WithCount` and
cursor paging with the predicate. All three implementations agree.

**Migration for hosts:** none required. `q` is the canonical key; a transport with
v1 clients may fall back to `s` at its OWN edge with a documented removal
milestone — that alias is deliberately not in `crud.ListParams`.

### sdk — v0.4.0 (2026-08-16): canonical runtime posture + capability-owned transport checks (minor)

coordination-hub auth upstream, phase 3 (`.claude/plans/coordination-hub-auth-upstream/03-runtime-posture-foundation.md`,
CHAU-3.1…3.4, 2026-08-16). The upstream flag: coordination-hub's **generic**
`internal/integrations/mailer` imported `features/authentication` for nothing but
the runtime-mode vocabulary and the insecure-transport error. That is a layering
inversion — an app-wide mailer should not depend on an auth feature to name its
own deployment posture. Purely additive; no existing sdk symbol changed.

- **`sdk/foundation/environment`** (new `mode.go`): `Mode` with `ModeDevelopment`
  / `ModeProduction`, `ValidateMode`, `ParseMode`, and the
  `ErrModeRequired` / `ErrModeInvalid` sentinels. A REQUIRED enum — the empty
  value is an error, never a silent development default. It reads **no**
  environment variable: the host names the key and passes the already-read
  string, so nothing about mode selection is implicit. Only two values ship;
  staging/preview/CI map onto the posture the host wants (normally production).
  Stdlib-only, so `environment` keeps its "zero external dependencies" claim and
  the foundation-imports-root-only guard (G12b) holds.
- **`sdk/capabilities/email`**: `CheckSender(environment.Mode, Sender) (TransportPosture, error)`,
  `InspectSender`, `TransportPosture{Declared, Capabilities}` with
  `ProductionCapable()`, and `ErrInsecureTransport`. Production rejects a
  development-only or metadata-less Sender; development accepts both and returns
  the classification so the caller phrases its own warning. **No logger
  parameter** — message text and log routing are composition concerns. An empty
  or unknown mode is rejected rather than defaulted.
- **`sdk/capabilities/notify`**: the same trio —
  `CheckNotifier`/`InspectNotifier`/`TransportPosture`/`ErrInsecureTransport`.
- Detection stays **structural** (a `CapabilityReporter` type assertion), so a
  third-party transport opts in without the sdk knowing its type;
  `integrations/email/sendgrid` now carries a compatibility test proving the real
  Sender resolves through `email.CheckSender` as its `Capabilities` claim.

Capabilities importing `foundation/environment` is legal under the layering
guard (the `notify` → `foundation/identity` precedent); the guard forbids
capability↔capability and capability→feature only.

If the phase-4 transactional-logo change rides the same commit, do **not** split
it into its own patch — this minor covers both.

### features/authentication — v0.3.0 (2026-08-16): RuntimeMode is now an alias of environment.Mode (minor; SOURCE-COMPATIBLE)

The feature half of phase 3 (CHAU-3.3). **No host code change is required**, and
none is expected to break:

- `RuntimeMode` is a **type alias** of `environment.Mode`, and
  `RuntimeModeDevelopment`/`RuntimeModeProduction` are the canonical constants.
  Alias, not a new distinct type: values are assignable in both directions with
  no conversion, `Config{RuntimeMode: …}` literals compile unchanged, the wire
  values are still `"development"`/`"production"`, and the `AUTH_RUNTIME_MODE`
  env tag parses identically.
- `ErrRuntimeModeRequired` / `ErrRuntimeModeInvalid` now **wrap**
  `environment.ErrModeRequired` / `ErrModeInvalid`. `errors.Is` against the auth
  sentinels is unchanged, and sdk-only code can match the canonical sentinel for
  the same failure. **The error TEXT changed**: each message gained a
  `": environment: mode …"` suffix. Nothing in the repo asserted on that text
  (only `errors.Is`), but a host string-matching on the message should switch to
  `errors.Is`.
- `ErrInsecureDeliveryTransport` is unchanged as a sentinel, but the returned
  error now **also** wraps `email.ErrInsecureTransport` or
  `notify.ErrInsecureTransport`, and its message gained the capability error as a
  suffix. The verdict is now made by `email.CheckSender`/`notify.CheckNotifier`;
  auth keeps the per-transport label and its own development WARN wording.
- **Development WARN behavior is byte-identical to before**: only a transport
  that *declared* itself development-only warns. A metadata-less transport is
  still rejected in production and still silent in development — deliberately
  not "improved" as a side effect of this refactor.
- Requires the sdk tag above (`environment.Mode` + the capability checks).
- The two unexported helpers `emailCapabilities`/`notifyCapabilities` were
  removed as orphaned by this change; both were unexported and had no other
  caller.

Downstream action for coordination-hub: delete the `features/authentication`
import from the generic `internal/integrations/mailer` / notifymail packages and
name `environment.Mode` + `email.CheckSender` instead. The auth composition may
keep importing the feature — it needs `auth.Config` regardless.

### sdk/capabilities/email — sdk v0.4.0 (2026-08-16): bundled transactional layout renders Brand.LogoURL (RENDERED-OUTPUT change; rode the minor)

coordination-hub auth upstream, phase 4 (`.claude/plans/coordination-hub-auth-upstream/04-email-layout-branding.md`,
CHAU-4.1…4.3, 2026-08-16). The upstream flag read "sdk layouts ignore
`Brand.LogoURL`"; the audit found it true of exactly one bundled layout.
`marketing.html` already rendered the logo and `minimal.html` is deliberately
unbranded, but `transactional.html` — the layout **every** authentication
delivery purpose uses — dropped it. A host that set `EmailBranding.LogoURL` got
no image and no error.

- `templates/layouts/transactional.html` gains a conditional logo block above the
  brand name: `{{if .Brand.LogoURL}}<img src="{{.Brand.LogoURL}}" alt="…">{{end}}`,
  with conservative email-client inline dimensions (`max-height: 48px`,
  `max-width: 100%`, `height: auto`, `border: 0`). Name and tagline still render
  beside it, so a blocked or broken image never erases brand identity.
- `alt` is `Brand.Name`, falling back to `Your Company` — the same fallback the
  visible header already used, so image and text identity cannot disagree.
- `TemplateRegistry.SetBranding(nil)` now resets to empty branding instead of
  storing nil, making the registry's documented "branding is never nil" invariant
  hold for the public `email.WithBranding` path too.
- Package docs gain the branding/layout field matrix, the escaping boundary
  (`html/template`, never `template.HTML`; no fetching or URL validation), and
  the external-image-blocking rationale. `Branding`'s godoc carries the matrix.

**No exported symbol changed**, so this floors at a **patch**. It is, however, a
**rendered-output change**: HTML differs *only* when `Branding.LogoURL` is
non-empty, and only for hosts on the bundled transactional layout. A host that
overrides the layout at `email.LayerApp` (coordination-hub does) sees **no
change** — the override wins wholesale and owns its own logo markup. The
plain-text alternatives are untouched and remain image-free. Do not split this
into its own tag if the phase-3 runtime-posture work rides the same commit; that
work floors sdk at a **minor**.

### sdk/capabilities/work — next tag: NEW module (first tag)

The SWP promotion (sdk-work-protocol, 2026-07-13) added `sdk/capabilities/work` —
the **keyed-work submission protocol**: a vocabulary + narrow-port contract with
**no default implementation** (the `oauth` posture). It has **no prior tag**, so it
is a **new module, first tag**. It ships:

- `work.Status` — the frozen seven-value lifecycle vocabulary
  (`pending`/`running`/`completed`/`failed`/`dead_letter`/`canceled`/`superseded`;
  `failed` is NON-terminal/retryable), with `Terminal()`/`Known()` predicates,
  pinned byte-for-byte to the persisted job-status strings by the package's own
  literal test;
- segregated consumer ports `Enqueuer` (idempotent keyed admission), `Replacer`
  (optional atomic replace/supersede), and `StatusReader` (deterministic
  latest-by-key, lifecycle-only — never payload/attempt/secret);
- an **opaque `[]byte`** payload (deliberately NOT `json.RawMessage`: some producers
  submit ciphertext, so the protocol must not imply JSON); and
- `worktest`, the conformance suite an implementation runs.

The implementation of record is `features/jobs` (below). **Payload snapshot
ownership (SWP-3 / IX-23).** The implementation of record deep-copies the payload
with a central `bytes.Clone` at the protocol boundary, so a keyed unit's admitted
bytes are a store-independent snapshot: a later caller mutation of its slice cannot
alter admitted work, for every backing store, by construction. `worktest` pins this
under `-race`; a new backend inherits the semantic from the protocol, not from its
own storage layer.

### features/jobs + both store modules — next tag: SWP fenced delivery surface (minor floor)

auth-v3/AV3D (2026-07-13) made `features/jobs` the **implementation of record** for
`sdk/capabilities/work` and added the opt-in lease-fenced delivery surface. All
changes are **additive / source-compatible**, so the floor is a **minor** bump (no
existing exported signature was removed or changed incompatibly), with two adopter
notes:

- **New sdk dependency floor.** `features/jobs` now imports `sdk/capabilities/work`;
  a host pins sdk at or past the `work`-carrying tag.
- **`job.Status` is now a source-compatible alias** `type Status = work.Status` (was
  a distinct `type Status string`). The persisted strings are byte-identical, and an
  alias is assignable both ways, so existing `job.Status` code compiles unchanged;
  `job.StatusCanceled`/`job.StatusSuperseded` are new members produced only by the
  fenced queue.
- **Additive surface:** `Repositories.FencedQueue` (nil = the fenced surface is
  off), the keyed-work primitives (`EnqueueOnce`/`Replace`/`LatestStatusByKey` over
  opaque `[]byte`, `Checkpoint`/`PurgeTerminal`), `jobs.FencedRuntime`, and the
  opt-in migration `0003_fenced_job_queue` (both dialects). The existing
  unfenced `Queue`/cron surface, its migrations, and every current consumer are
  unaffected. A host may now wire `FencedQueue` alone (delivery-only), `Queue` alone
  (existing cron host), or both; `ErrQueueRequired`'s message widened accordingly.

Downstream upgrade example — a consuming feature depends on the sdk `work` ports,
never on `features/jobs` (constitution rule 6):

```go
// BEFORE (pre-SWP): a consumer hand-declared its own narrow enqueuer port.
type enqueuer interface {
    EnqueueOnce(ctx context.Context, kind, logicalKey string, payload []byte) (string, error)
}

// AFTER (SWP): depend on the canonical sdk ports; jobs.Service satisfies them.
import "github.com/gopernicus/gopernicus/sdk/capabilities/work"

type Deps struct {
    Enqueuer work.Enqueuer     // jobs.Service.EnqueueOnce
    Replacer work.Replacer     // jobs.Service.Replace     (optional)
    Status   work.StatusReader // jobs.Service.LatestStatusByKey
}
```

### features/authorization + both store modules — next tag: authorization v3 correctness kernel (BREAKING; FIRST tags)

authorizationv3 (2026-07-14, task prefix `AZ3`) hardens the v1 IAM feature into an
exact-semantics correctness kernel. **Preflight re-confirmed zero
`features/authorization*` tags exist (`git tag -l` empty, 2026-07-14),** so this
cut is the module's **first tag** and — per recommended-default #7 and the packet's
pre-tag breaking policy — the canonical migration set was **rewritten greenfield**
(fold-to-final, not append-only). This note **supersedes** the pending
middleware-consolidation "additive gate symbols (minor floor)" note above: with no
tag ever cut, the v3 breaking surface simply absorbs that pending minor floor into
the first tag.

**Breaking-change taxonomy — this note distinguishes SEMANTIC access changes from
source-only renames** (the AZ3-5.3 acceptance criterion):

- **SEMANTIC (a decision or stored-state meaning changed):**
  - **Userset relations are now load-bearing at runtime** (critical finding #1). A
    stored `group#admin` is no longer silently satisfied by `group#member`; a
    concrete-group grant reaches only the group entity; a tuple missing its
    required userset relation is now rejected. Any adopter whose v1 data relied on
    the decorative-relation bug gets DIFFERENT (correct) decisions. This is the
    single change most likely to alter live access — the AZ3-5.1 upgrade audit
    classifies each v1 row RETAIN / LOSE and stops on ambiguity (see
    `features/authorization/stores/UPGRADE.md`).
  - **Decision requests are concrete-principal-only.** A non-empty relation at a
    decision boundary is now rejected, never ignored; `Check`/`CheckBatch`/
    `FilterAuthorized`/`LookupResources` fail closed on malformed refs.
  - **Evaluation is now bounded and fail-closed.** Limit/graph/fan-out/lookup
    exhaustion returns an indeterminate `ErrEvaluationLimit` (wraps
    `sdk.ErrUnavailable`; middleware maps it to 503), never a silent deny or a
    truncated-complete list. A caller that treated "no error, empty result" as
    "denied" must now fail closed on the error.
  - **Mutations are atomic, revisioned, idempotent, and outcome-explicit.** A
    conflicting create no longer silently succeeds: apply/revoke/replace return an
    explicit `Receipt.Outcome` (applied/no_change/semantic_conflict/
    invariant_blocked/not_found) plus an independent `Replayed` flag; stale
    revision and MutationID payload mismatch are command ERRORS, not outcomes.
    Last-owner protection is now a single-winner repository invariant
    (`OutcomeInvariantBlocked`), replacing the non-atomic exists→count→delete.
  - **`LookupResources` completeness + Check/Lookup parity.** Lookup now returns
    Through-derived descendants it previously omitted (the D1(b)/D1(c) gap), so an
    adopter enumerating resources sees a larger, correct set.
  - **Effective role listing with provenance.** A scoped revoke that leaves a
    global grant now reports `SameRoleGrantRemains=true`; `ListEffectiveRoleGrantsByResource`
    reports direct/global/both provenance (raw `ListByResource` retained,
    documented as raw).
  - **Actor/guard authority is now mandatory for untrusted writes.** A guarded
    write commits only if every authorization scope its guard read has the same
    revision when the repository locks; there is no default-allow.
- **SOURCE-ONLY renames / shape changes (compile break, no access-meaning change):**
  - **Decision vocabulary rename:** `CheckRequest.Subject` (type `Subject`) →
    `CheckRequest.Principal` (type `PrincipalRef{Type, ID}`); the stored-subject
    type is now `SubjectRef{Type, ID, Relation}`. These are intentionally distinct
    types (concrete principal vs. possibly-userset stored subject).
  - **Construction shape:** `NewService(repos, cfg)` now returns
    `(Components{Service, RelationshipWriter, SystemMutator}, error)` — the authorization-specific FS2
    amendment (a feature holding a separately-partitioned trusted capability; see
    `features/README.md` §5 FS2 amendment; NOT a general FS2 replacement).
  - **Relationship state writes moved off `Service`:** `CreateRelationships`,
    `DeleteRelationship`, `DeleteResourceRelationships`, `DeleteByResourceAndSubject`,
    raw `AssignRole`/`UnassignRole`. Actor-facing typed guarded commands
    (`GrantRelationship`/`RevokeRelationship`/`ReplaceRelationship`/`AssignRole`/
    `UnassignRole`) remain guarded on `Service`; normal relationship state writes
    live on the separately held `RelationshipWriter`, while `SystemMutator` is the
    opt-in high-integrity path.
  - **Reader ports gained a `limit int`:** the three `Lookup*` reader methods bound
    their result set (SQL `LIMIT` / memstore cap); a custom store implementation
    must add the parameter.
  - **`GetSchema()` returns a `SchemaSnapshot`** (deep-copy read-only projection)
    instead of the mutable `Schema`; new `SchemaDigest()`.
  - **Role effective-listing port addition:** `role.Storer.ListEffectiveByResource`
    (+ `EffectiveGrant`) — a custom store must implement it.

**Canonical migration set (greenfield rewrite).** Both dialect trees ship the final
v3 schema with byte-identical filename sets: `0001_iam_relationships`,
`0002_iam_roles`, `0003_iam_scopes`, `0004_iam_mutations` (the `iam_*` prefix per
owner ruling R4). The `iam_scopes` (revision anchors) and `iam_mutations` (receipt
ledger) tables are new in v3. A v1 adopter does **not** blind-copy these onto a
populated v1 database — the **AZ3-5.1 data-preserving adopter path** (detection →
blocked-until-repaired → conversion + anchor seeding → v3 boot → access comparison →
rollback boundary) is published in `features/authorization/stores/UPGRADE.md` and
linked from `features/authorization/README.md`. It was **executed live** on
fresh/reset PostgreSQL (C-collation) + libSQL both dialects (AZ3-5.1) and has **not**
been applied to a real host.

**Jobs/work axis: authorization adds NOTHING.** authorizationv3 imports **`sdk`
only** (verified: `features/authorization/go.mod` requires only `sdk`; zero
`sdk/capabilities/work`, `features/jobs`, or `features/events` imports in the core).
The v3 correctness kernel emits **no effects** — no authorization delivery queue, no
post-commit dispatch, no event append, no authorization-specific jobs table. This is
enforced permanently by the sixteenth layering guard,
`guard-authorization-no-delivery-repo` (AZ3-5.3), the `guard-auth-no-delivery-repo`
twin pointed at `features/authorization` migrations + repositories. So the settled
`sdk/capabilities/work` (new first-tag module) and `features/jobs` (MINOR floor)
notes above carry **no authorization rider**; a future authorization effects packet
must consume the shared jobs/events vocabulary, never revive a bespoke queue.

**Per-module tag requirements (semver floors; no tag cut this milestone):**

| Module | Floor | Why |
|---|---|---|
| `features/authorization` | **first tag — breaking-vintage** | userset relations load-bearing, concrete-principal decisions, immutable `SchemaSnapshot`, bounded evaluation, `Components{Service, RelationshipWriter, SystemMutator}` construction, separately held baseline state writes, and optional atomic revisioned receipts. Pre-`v1`, breaking is expected (Go pre-release semantics); move to `v1.0.0` deliberately, not on this first tag |
| `features/authorization/stores/pgx` | **first tag — breaking-vintage** | implements the relation-aware readers (+`limit`), the three atomic mutation repositories (`iam_scopes`/`iam_mutations`), and `ListEffectiveByResource` over the greenfield `0001…0004` set |
| `features/authorization/stores/turso` | **first tag — breaking-vintage** | same, libSQL dialect; requires the turso connector's `BEGIN IMMEDIATE` write-intent transactions (already keyed above) for the concurrent mutation CAS |

`examples/auth-cms` is the v3 proof host — a demonstration, never tagged.

**Consumer changes a host/feature must make:**

- **Auth invitation Granter** (`examples/auth-cms/cmd/server/membership.go`): the
  ordinary project-member adapter uses the separately held `RelationshipWriter`
  and intentionally ignores `OperationID`; exact creation is naturally
  idempotent and later re-grants restore current state. A second
  `guardedRelationshipGranter` demonstrates the opt-in sensitive posture:
  `SystemMutator` + operation-scoped `DeriveMutationID` + receipt inspection.
  Hosts choose by resource type/relation; authentication mandates neither path.
- **Events authorization closure:** `features/events` itself needs **no change** —
  its `AuthorizeStream` config seam is a host-supplied closure and does not import
  authorization. A host whose events `Authorize` closure delegates to
  `authorizer.Check` must update the call to the new `CheckRequest{Principal:
  PrincipalRef{...}}` shape (was `CheckRequest{Subject: Subject{...}}`). This is the
  source-only decision-vocabulary rename; the Check-only gate semantics are
  unchanged.
- **auth-cms (done — cite):** the proof host is fully migrated — `hostMutationGuard`
  (`cmd/server/guard.go`) composes the schema `manage_access` relation and the
  platform-admin recipe over the dependency-tracking `DecisionView`;
  `cmd/server/authorization.go` seeds via `SystemMutator`+`DeriveMutationID`; all
  session-only authorization-mutation HTTP routes were removed (browser
  role-assignment deferred to the AZADM packet). Migration proven by the AZ3-4.1/4.2
  host-composition tests and the checked-in
  `examples/auth-cms/cmd/server/testdata/az3-proof-transcript.md`.
- **External host recipe (README wiring page):** a generic host wires
  `Components{Service, RelationshipWriter, SystemMutator}` from `NewService`, uses
  the baseline writer for application-maintained tuple state, and composes a host
  `MutationGuard` (schema-declared relation + any platform-admin short-circuit) that
  fails closed, holding `SystemMutator` apart for the sensitive operations that
  opt into revisions/invariants/receipts/audit,
  and adapts `identity.Principal → Actor`/`PrincipalRef` at the boundary. The full
  wiring recipe and every API is documented in `features/authorization/README.md`
  (AZ3-5.2) — a host wires safely without reading internal code or the plan.

**sdk graduation decision (RECORDED for owner review — NO code moved).** This
milestone fires the ARCHITECTURE protocol-table trigger ("authorizationv3 settles
its semantics"). Re-running the three conjunctive graduation gates over the
authorization check/decision vocabulary: **RE-DEFER — stays consumer-declared.** The
semantics are now settled (the trigger's condition), but graduation still fails:
- *sdk/README.md admission (plurality):* exactly ONE honest implementation exists
  (`features/authorization`); host "closures" are arbitrary access composition, not
  second implementations of a shared decision contract. FAILS test 1.
- *ARCHITECTURE five-point sdk-vs-logic:* multiple honest adapters do NOT exist
  (point 1), and the conformance suite (`storetest`) is feature-coupled, not an
  sdk-generic suite (point 3). FAILS.
- *features/README.md §5 five criteria:* criterion 1 (real producer + real consumer
  in SEPARATE modules) is not met — the only cross-feature usage is consumer-declared
  Check-only closures per the C2 DEFAULT, not a separate module consuming a graduated
  sdk authorization port; and criterion 2 (canonical-across-gopernicus) is still not
  established. FAILS.
Recommendation to the owner: update the ARCHITECTURE protocol-table row reason from
"trigger: authorizationv3 settles its semantics" to "settled, but re-deferred:
single implementation; consumer-declared Check-only closures remain the only
cross-feature usage — re-evaluate when a second authorization decision
implementation or a feature needing the identical decision vocabulary appears." The
owner ratifies any table edit separately; this task records the decision only.

### Auth v3 tag requirements + production checklist

**Per-module tag requirements for the auth-v3 cut** (semver floors; no tag is cut
until the release workflow authorizes it):

| Module | Floor | Why |
|---|---|---|
| `features/authentication` | **major / breaking** | `Repositories` grows identifier/challenge/contact-change/grant/credential ports (NO delivery port — durable delivery is the generic jobs feature via `Config.DeliveryDispatcher`); `user.User` loses its email field; `Config` and routes/entities change; the legacy `verification_*` rail is retired; the AV3D delivery removals/renames above apply |
| `features/authentication/stores/pgx` | **major / breaking** | implements the re-keyed `Repositories` over the greenfield `0001…0013` set (no `delivery_jobs`) |
| `features/authentication/stores/turso` | **major / breaking** | same, libSQL dialect |
| `sdk/capabilities/work` | **new module — first tag** | the keyed-work submission protocol (vocabulary + ports, no default); `features/jobs` is its implementation of record |
| `features/jobs` + both store modules | **minor** | implements `sdk/capabilities/work` (new sdk dep); additive fenced delivery surface: `Repositories.FencedQueue`, keyed-work primitives over opaque `[]byte`, `jobs.FencedRuntime`, `PurgeTerminal`, migration `0003_fenced_job_queue`; `job.Status` is now a source-compatible alias of `work.Status` (existing consumers unaffected) |
| `features/authentication/views/templ` | **new module — first tag** | bundled default HTML views (additive; opt-in) |
| `integrations/datastores/turso` | **patch (behavior fix)** | `BEGIN IMMEDIATE` write-intent transactions (required by a turso host adopting v3) |
| `sdk/capabilities/{email,notify}` | **minor** | additive production-safety capability metadata |

The four `examples/*` hosts (including `examples/auth-cms`, the auth-v3 proof host)
are demonstrations, not importable modules, and are never tagged.

**Host upgrade order** is the seven-step, backfill-first, host-owned procedure in
the runbook below (single cutover — do not roll; destructive Step 6 only after the
v3 binary is confirmed stable). It has been validated on fresh/reset databases both
dialects (AV3-9.2) and **not** applied to any real host.

**Production readiness checklist** (fail-closed gates a host MUST satisfy before
`RuntimeMode` production — detail in `features/authentication/README.md`):

- **Five distinct host-supplied secrets — never reuse one value for two roles.**
  The real key material the host wires into `Config` (proof-host env names in
  parentheses) and each key's ACTUAL rotation story:
  1. **Access-JWT signer** (`Config.TokenSigner`, `AUTH_JWT_SECRET`). Signs the
     access JWT (required — `ErrTokenSignerRequired`). **Single-key, disruptive:**
     rotating it invalidates every live access JWT, forcing re-authentication; a
     multi-instance deployment MUST share one value (a per-instance key flaps auth).
     Use a rolling deploy and expect existing bearer sessions to re-auth.
  2. **Challenge HMAC pepper** (`Config.ChallengeProtector`, `AUTH_CHALLENGE_PEPPER`).
     Protects OTP short codes AND digests magic-link / password-reset tokens — there
     is NO separate magic-link/reset secret; those ride this pepper. It is the ONE
     key with **continuity-supporting rotation**: the bundled `HMACChallengeProtector`
     takes an `HMACKeyRing` (active key ID + retained older keys), so a rotation
     verified by `TestChallengeProtectorKeyRotation` keeps pending codes/links under a
     retained old key valid; removing an old key from the ring invalidates challenges
     still pending under it (the user restarts the flow).
  3. **Delivery payload AES key** (`Config.DeliveryEncrypter`, `AUTH_DELIVERY_ENCRYPTER_KEY`,
     AES-256-GCM). Seals the delivery-outbox envelope. **Single-key, disruptive:**
     rotating it only affects payloads sealed after the change, so **drain in-flight
     delivery work before retiring the old key** (an in-flight payload sealed under a
     removed key dead-letters and the user restarts the flow).
  4. **Provider-token AES key** (`Config.TokenEncrypter`, `AUTH_TOKEN_ENCRYPTER_KEY`,
     AES-256-GCM; optional — nil = provider OAuth tokens are not persisted). Encrypts
     stored OAuth access/refresh tokens at rest. **Single-key, disruptive:** stored
     tokens sealed under the old key become undecryptable on rotation (**stored-token
     loss** — affected users re-link the OAuth provider).
  5. **Identifier HMAC key** (`Config.IdentifierKeyer`, `AUTH_IDENTIFIER_KEY`; required
     in production — `ErrIdentifierKeyerRequired`). Derives PII-free rate-limit /
     idempotency keys. **Single-key**, but rotation is the least disruptive: derived
     limiter/idempotency keys change, so rate-limit buckets and enqueue-idempotency
     dedup reset once (transient; no session or credential loss). A multi-instance
     deployment MUST share one value so one identifier maps to one bucket.

  There is deliberately **no separate CSRF secret**: the double-submit CSRF token is a
  fresh per-render random value set as the `__Host-auth_csrf` cookie and compared in constant
  time against the `csrf_token` field — no host key material to manage or rotate.
- **Production rejects development transports and unacknowledged/incomplete wiring.**
  In `RuntimeMode` production, `NewService` fails construction on: a `DevelopmentOnly`
  / metadata-less email or notify transport (the `console` senders), a memory rate
  limiter, a missing `IdentifierKeyer`, a delivery runtime whose mode is unacknowledged
  (`DeliveryJobsAcknowledged` / `DeliveryEphemeralAcknowledged`), and — when the
  passwordless/link surface is wired — a missing or non-HTTPS `PublicAuthBaseURL`.
  `console` senders are development-only. **What `NewService` does NOT gate (host
  deployment checklist, not a construction guarantee):** `AllowedOrigins` may be empty
  at construction (an empty allowlist simply rejects every cross-origin browser POST at
  request time — the host must populate it for browser clients), and **trusted-proxy /
  `ClientIP` wiring is router-level** (`sdk/foundation/web` `TrustProxies`, wired by the
  host) and therefore **unobservable by `NewService`** — it cannot and does not reject a
  host that forgot it. Both are deployment-checklist items the host verifies, not
  construction-time failures.
- **The delivery runtime is host-lifecycle-owned, and its mode is explicit.**
  `Config.DeliveryMode` is required with no default (never inferred from a non-nil
  collaborator). The recommended production posture is `"jobs"`: wire
  `Config.DeliveryDispatcher` over the generic **jobs** feature, run the generic
  `jobs.FencedRuntime` (start on boot, stop on shutdown), and set
  `DeliveryJobsAcknowledged: true` (production rejects an unacknowledged jobs
  runtime and a `jobs`-mode config with no dispatcher). `"in_process"` is a bounded
  EPHEMERAL pool the host drives with `Service.RunDelivery(ctx)`; it does NOT
  survive a crash and has no cross-instance coordination, so production requires the
  explicit `DeliveryEphemeralAcknowledged: true`. In either mode the dispatcher is
  the only send path, delivery is **at-least-once** (not exactly-once — consumers
  tolerate duplicates; a resend cannot retract an in-flight provider call), payloads
  are always encrypted at rest, and terminal work is purged under bounded retention.
- **Migrations are host-owned and applied pre-boot** — the greenfield canonical set
  for a new host, or this runbook's backfill-first procedure for a live v2 host
  (never blind-copy the canonical `0001_users.sql` onto a populated v2 `users`).
- **pgx byte-order pagination parity needs a `C`-collation database** (parked
  shared-helper finding, not a v3 defect); **turso hosts need the `BEGIN IMMEDIATE`
  connector** (keyed above).

## Auth v3 host upgrade runbook (v2 → v3 identity)

A **host-owned** migration procedure for a database already running auth-v2
(`features/authentication` before the v3 identity work) that is upgrading to
auth-v3. Per the standing greenfield-migrations rule the canonical migration
trees ship the **final** v3 schema only and never carry upgrade/evolution files;
a v2 host's database does **not** match the canonical `0001_users.sql`, so the
host applies the steps below from its **own** host migration tree, pre-boot,
exactly like every other host-owned migration.

**No blind copy.** Do not apply the canonical `stores/{pgx,turso}/migrations/*`
files to a live v2 database. The canonical `0001_users.sql` describes the FINAL
users shape — `id, display_name, auth_revision, created_at, updated_at`, with no
`email`/`email_verified` — so applying it to a populated v2 `users` table would
drop the legacy email data before any backfill. A v2 host runs *this* additive,
backfill-first procedure instead; the destructive column removal happens only in
Step 6, after the backfill and its validation.

Validated on fresh/reset databases both dialects (see the AV3-9.2 execution
record at the end); it has **not** been applied to a real application host.

### Preconditions

- A confirmed, restorable **backup** taken immediately before Step 1.
- A maintenance window (single cutover; see deploy ordering below — the v3
  binary must not run against the pre-Step-5 schema, and old/new binaries must
  not both serve the same database across the cutover).
- v2 `users.email` is stored normalized (trimmed + lowercased) and is `UNIQUE`.
  If a host wrote un-normalized emails, the Step-1 collision dry-run catches the
  ambiguity **before** any write.

**Deploy ordering (single cutover — do NOT roll).** (1) Take the backup
(Step 1). (2) Stop the v2 binary (or drain traffic). (3) Apply Steps 1–5
(additive; keep the v2 binary stopped to avoid mixed-version reads/writes).
(4) Deploy and start the v3 binary; confirm it is healthy and stable.
(5) **Only after** v3 is confirmed stable, apply Step 6 (the destructive
cutover — the point of no return for a v2-binary rollback). Steps 1–5 are
reversible by restoring the backup or redeploying the v2 binary (the new
tables/columns are inert to it); Step 6 drops the legacy columns and
verification tables, after which the v2 binary can no longer read `users`.

### Step 1 — Backup and dry-run collision detection

Take a full, restorable backup first (`pg_dump` / a libSQL/SQLite file copy or
`.backup`). Then run the collision dry-run. **A non-empty result aborts the
upgrade** — do not choose a winner automatically; report the colliding rows for
a human decision (this mirrors the feature's atomic auth-claim invariant,
`UNIQUE(kind, normalized_value)` over active login/recovery identifiers).

pgx:

```sql
SELECT lower(btrim(email)) AS normalized_value,
       count(*)            AS n,
       array_agg(id)       AS user_ids
FROM users
GROUP BY lower(btrim(email))
HAVING count(*) > 1;
```

SQLite / libSQL:

```sql
SELECT lower(trim(email)) AS normalized_value, count(*) AS n
FROM users
GROUP BY lower(trim(email))
HAVING count(*) > 1;
```

If either returns rows, **stop** and resolve the collisions by hand. (Validated:
skipping this and forcing the Step-3 backfill fails atomically on the
`idx_user_identifiers_auth_claim` unique index with zero rows written — the index
is the structural backstop, but detecting collisions up front lets a human
choose, not the DB.)

### Step 2 — Create `user_identifiers` and its indexes

Required by the Step-3 backfill; purely additive.

pgx:

```sql
CREATE TABLE IF NOT EXISTS user_identifiers (
    id                   TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id              TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    normalized_value     TEXT NOT NULL,
    verified_at          TIMESTAMPTZ,
    login_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    notification_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    is_primary           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    replaced_at          TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_auth_claim
    ON user_identifiers (kind, normalized_value)
    WHERE replaced_at IS NULL AND (login_enabled = TRUE OR recovery_enabled = TRUE);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_primary
    ON user_identifiers (user_id, kind)
    WHERE replaced_at IS NULL AND is_primary = TRUE;
CREATE INDEX IF NOT EXISTS idx_user_identifiers_user_active
    ON user_identifiers (user_id, kind, created_at)
    WHERE replaced_at IS NULL;
```

SQLite / libSQL:

```sql
CREATE TABLE IF NOT EXISTS user_identifiers (
    id                   TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id              TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    normalized_value     TEXT NOT NULL,
    verified_at          TEXT,
    login_enabled        INTEGER NOT NULL DEFAULT 0,
    recovery_enabled     INTEGER NOT NULL DEFAULT 0,
    notification_enabled INTEGER NOT NULL DEFAULT 1,
    is_primary           INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    replaced_at          TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_auth_claim
    ON user_identifiers (kind, normalized_value)
    WHERE replaced_at IS NULL AND (login_enabled = 1 OR recovery_enabled = 1);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_primary
    ON user_identifiers (user_id, kind)
    WHERE replaced_at IS NULL AND is_primary = 1;
CREATE INDEX IF NOT EXISTS idx_user_identifiers_user_active
    ON user_identifiers (user_id, kind, created_at)
    WHERE replaced_at IS NULL;
```

### Step 3 — Backfill one primary email identifier per user

Insert exactly one active, primary email identifier per existing user. The
`NOT EXISTS` guard makes the statement **idempotent** — a re-run inserts nothing.
`login_enabled`, `recovery_enabled`, and `notification_enabled` are all set (in
v2 the single `users.email` was the universal login, recovery, and notification
address; OAuth-only users get a login-enabled email too — it is still their
discovery/recovery address and a passwordless-login claim).

**`verified_at` proxy caveat.** v2 recorded only a boolean `email_verified`, not
a proof timestamp. A verified user's identifier is backfilled with `updated_at`
as the best-available verification time; an unverified user gets `NULL`. This
preserves verification **state** exactly; the verification **timestamp** is an
approximation (acceptable for lifecycle/risk policy). A host that kept a truer
verification time elsewhere may substitute it in the `CASE` expression.

pgx:

```sql
INSERT INTO user_identifiers
    (user_id, kind, normalized_value, verified_at,
     login_enabled, recovery_enabled, notification_enabled, is_primary,
     created_at, updated_at, replaced_at)
SELECT id, 'email', lower(btrim(email)),
       CASE WHEN email_verified THEN updated_at ELSE NULL END,
       TRUE, TRUE, TRUE, TRUE,
       created_at, updated_at, NULL
FROM users
WHERE NOT EXISTS (
    SELECT 1 FROM user_identifiers ui
    WHERE ui.user_id = users.id AND ui.kind = 'email' AND ui.replaced_at IS NULL
);
```

SQLite / libSQL:

```sql
INSERT INTO user_identifiers
    (user_id, kind, normalized_value, verified_at,
     login_enabled, recovery_enabled, notification_enabled, is_primary,
     created_at, updated_at, replaced_at)
SELECT id, 'email', lower(trim(email)),
       CASE WHEN email_verified = 1 THEN updated_at ELSE NULL END,
       1, 1, 1, 1,
       created_at, updated_at, NULL
FROM users
WHERE NOT EXISTS (
    SELECT 1 FROM user_identifiers ui
    WHERE ui.user_id = users.id AND ui.kind = 'email' AND ui.replaced_at IS NULL
);
```

### Step 4 — Validate before proceeding

Every check must pass before Step 5. The count-parity check must be equal; every
other query must return **zero** rows. (Column/table names are the v2 auth
schema; adapt if a host renamed anything. On SQLite/libSQL use `is_primary = 1`
and `(login_enabled = 1 OR recovery_enabled = 1)` in the predicates.)

```sql
-- Parity: users == primary active email identifiers (must be EQUAL).
SELECT (SELECT count(*) FROM users) AS users,
       (SELECT count(*) FROM user_identifiers
         WHERE kind='email' AND is_primary AND replaced_at IS NULL) AS primary_email_ids;

-- Every user has an active primary email identifier (expect 0 rows).
SELECT u.id FROM users u
LEFT JOIN user_identifiers ui
  ON ui.user_id = u.id AND ui.kind='email' AND ui.is_primary AND ui.replaced_at IS NULL
WHERE ui.id IS NULL;

-- No duplicate active auth-claim value (expect 0 rows).
SELECT normalized_value, count(*) FROM user_identifiers
WHERE replaced_at IS NULL AND (login_enabled OR recovery_enabled)
GROUP BY kind, normalized_value HAVING count(*) > 1;

-- No orphan passwords / OAuth accounts / sessions (expect 0 rows each).
SELECT p.user_id FROM user_passwords p LEFT JOIN users u ON u.id=p.user_id WHERE u.id IS NULL;
SELECT o.provider, o.provider_user_id FROM oauth_accounts o LEFT JOIN users u ON u.id=o.user_id WHERE u.id IS NULL;
SELECT s.id FROM sessions s LEFT JOIN users u ON u.id=s.user_id WHERE u.id IS NULL;

-- Informational: accepted invitations whose resolved subject is missing.
SELECT i.id FROM invitations i LEFT JOIN users u ON u.id=i.resolved_subject_id
WHERE i.status='accepted' AND (i.resolved_subject_id='' OR u.id IS NULL);
```

Sessions carry no identifier binding in v2, so no session is invalidated by the
backfill (identifier row IDs are newly generated and nothing is bound to them
before v3).

### Step 5 — Add auth/session metadata and the new flow tables

Additive. `users.auth_revision` is the optimistic-serialization anchor; the
session metadata columns back the recent-primary-login shortcut; the four flow
tables need no backfill (they start empty).

> **`delivery_jobs` is obsolete at/after the AV3D delivery refactor
> (2026-07-13).** The `delivery_jobs` CREATE below reflects the auth-v3 schema as
> shipped through AV3D-5.0; the retained DDL preserves the validation record. Auth
> no longer owns a delivery table — durable delivery is the generic **jobs**
> feature's schema and `in_process` delivery is ephemeral (see "Migrations are
> host-owned" in `features/authentication/README.md`). A host adopting auth at or
> past the delivery refactor **skips the `delivery_jobs` CREATE here** and wires
> delivery per the **Auth delivery-runtime upgrade runbook** below; a host that
> already created `delivery_jobs` under an earlier v3 cut drains and drops it via
> that same runbook.

pgx:

```sql
ALTER TABLE users    ADD COLUMN IF NOT EXISTS auth_revision          BIGINT      NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS authenticated_at       TIMESTAMPTZ;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS authentication_methods TEXT        NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS assurance_level        TEXT        NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS challenges (
    id               TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    secret_digest    TEXT NOT NULL,
    protector_key_id TEXT,
    context          TEXT,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    version          INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_user_purpose ON challenges (user_id, purpose);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_purpose_secret_digest ON challenges (purpose, secret_digest);

CREATE TABLE IF NOT EXISTS contact_changes (
    id                     TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id                TEXT NOT NULL,
    kind                   TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    new_value              TEXT NOT NULL,
    login_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    notification_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    make_primary           BOOLEAN NOT NULL DEFAULT FALSE,
    replaces_identifier_id TEXT NOT NULL DEFAULT '',
    expires_at             TIMESTAMPTZ NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_contact_changes_user_kind ON contact_changes (user_id, kind);

CREATE TABLE IF NOT EXISTS authentication_grants (
    id               TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    session_id       TEXT NOT NULL,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    context_digest   TEXT NOT NULL,
    methods          TEXT NOT NULL DEFAULT '',
    assurance        TEXT NOT NULL DEFAULT '',
    authenticated_at TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_authentication_grants_session_purpose_context
    ON authentication_grants (session_id, purpose, context_digest);

CREATE TABLE IF NOT EXISTS delivery_jobs (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    kind            TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload         BYTEA NOT NULL DEFAULT ''::bytea,
    state           TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    available_at    TIMESTAMPTZ NOT NULL,
    lease_id        TEXT NOT NULL DEFAULT '',
    leased_until    TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    terminal_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_jobs_idempotency
    ON delivery_jobs (idempotency_key) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_due
    ON delivery_jobs (available_at, created_at, id) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_terminal
    ON delivery_jobs (terminal_at) WHERE state <> 'pending';
```

SQLite / libSQL — `ALTER TABLE ADD COLUMN` has no `IF NOT EXISTS`; run each
`ADD COLUMN` once (the host migration runner already tracks applied files):

```sql
ALTER TABLE users    ADD COLUMN auth_revision          INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN authenticated_at       TEXT;
ALTER TABLE sessions ADD COLUMN authentication_methods TEXT    NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN assurance_level        TEXT    NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS challenges (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    secret_digest    TEXT NOT NULL,
    protector_key_id TEXT,
    context          TEXT,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    expires_at       TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    version          INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_user_purpose ON challenges (user_id, purpose);
CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_purpose_secret_digest ON challenges (purpose, secret_digest);

CREATE TABLE IF NOT EXISTS contact_changes (
    id                     TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id                TEXT NOT NULL,
    kind                   TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    new_value              TEXT NOT NULL,
    login_enabled          INTEGER NOT NULL DEFAULT 0,
    recovery_enabled       INTEGER NOT NULL DEFAULT 0,
    notification_enabled   INTEGER NOT NULL DEFAULT 1,
    make_primary           INTEGER NOT NULL DEFAULT 0,
    replaces_identifier_id TEXT NOT NULL DEFAULT '',
    expires_at             TEXT NOT NULL,
    created_at             TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_contact_changes_user_kind ON contact_changes (user_id, kind);

CREATE TABLE IF NOT EXISTS authentication_grants (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    session_id       TEXT NOT NULL,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    context_digest   TEXT NOT NULL,
    methods          TEXT NOT NULL DEFAULT '',
    assurance        TEXT NOT NULL DEFAULT '',
    authenticated_at TEXT NOT NULL,
    expires_at       TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    consumed_at      TEXT
);
CREATE INDEX IF NOT EXISTS idx_authentication_grants_session_purpose_context
    ON authentication_grants (session_id, purpose, context_digest);

CREATE TABLE IF NOT EXISTS delivery_jobs (
    id              TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    kind            TEXT NOT NULL,
    purpose         TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload         BLOB NOT NULL DEFAULT x'',
    state           TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    available_at    TEXT NOT NULL,
    lease_id        TEXT NOT NULL DEFAULT '',
    leased_until    TEXT,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    terminal_at     TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_delivery_jobs_idempotency
    ON delivery_jobs (idempotency_key) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_due
    ON delivery_jobs (available_at, created_at, id) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS idx_delivery_jobs_terminal
    ON delivery_jobs (terminal_at) WHERE state <> 'pending';
```

After Step 5 the schema is v3-complete except for the still-present legacy
`users.email`/`email_verified` columns and the verification tables. **Deploy and
verify the v3 binary now.** The feature reads identifiers, not `users.email`, so
the app is fully functional at this point.

### Step 6 — LATER: cutover / drop (only after v3 is stable)

Run this only after the v3 binary has been confirmed healthy **and** the
recovery flows that replaced the verification rail are verified end to end
(registration email verification and forgot/reset password both complete on the
v3 challenge rail — the `challenges` table, with delivery drained through the
runtime the host wired: `delivery_jobs` for a host still on a pre-refactor cut, or
the generic jobs / `in_process` runtime past it — and the Step-4 parity checks
still hold). The legacy `verification_codes` / `verification_tokens`
tables are inert to the v3 binary; drop them only once that cutover succeeds.
This step is the point of no return for a v2-binary rollback.

pgx (`ALTER TABLE ... DROP COLUMN` is a metadata operation on Postgres):

```sql
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users DROP COLUMN email_verified;
DROP TABLE verification_codes;
DROP TABLE verification_tokens;
```

SQLite / libSQL — dropping columns is the standard 12-step table rebuild (more
portable than relying on a specific `DROP COLUMN`-capable engine version); wrap
it in one transaction with foreign keys off:

```sql
PRAGMA foreign_keys=OFF;
BEGIN;
CREATE TABLE users_new (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    display_name  TEXT NOT NULL DEFAULT '',
    auth_revision INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
INSERT INTO users_new (id, display_name, auth_revision, created_at, updated_at)
    SELECT id, display_name, auth_revision, created_at, updated_at FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
DROP TABLE verification_codes;
DROP TABLE verification_tokens;
COMMIT;
PRAGMA foreign_keys=ON;
```

After Step 6 the host's `users` table matches the final canonical v3 shape
(`id, display_name, auth_revision, created_at, updated_at`) — the same
legacy-column removal, reached additively instead of by a blind canonical copy.

### Step 7 — Forward-only recovery and the no-blind-copy warning

- **Forward-only.** There is no down-migration that deletes backfilled identifier
  rows. If Steps 1–5 must be abandoned, restore the Step-1 backup or redeploy the
  v2 binary (Steps 1–5 are inert to it). If Step 6 has run, recovery requires a
  restore from the Step-1 backup — the v2 binary cannot read the rebuilt `users`.
- **Never blind-copy a canonical migration** onto a live v2 database. The
  canonical trees are greenfield/final-shape; this additive, backfill-first,
  validated procedure is the only supported path from a v2 database.
- **Never auto-resolve a collision.** A non-empty Step-1 dry-run stops the upgrade
  for a human decision.

### Runtime caveats (carried from live conformance)

These do not affect the migration DDL; they are runtime/parity behaviors a host
operator must know.

1. **turso hosts must run the connector with the write-intent transaction fix.**
   The step-up credential/identifier CAS rails (`Apply`, `ApplyVerifiedChange`)
   require the turso connector's `BEGIN IMMEDIATE` write-intent transactions
   (`integrations/datastores/turso/tx.go`). An older connector using default
   `DEFERRED` transactions returns `SQLITE_BUSY` to the CAS loser instead of
   `sdk.ErrConflict` under concurrency. Data integrity is never at risk either
   way (no double-commit), but a host on the pre-fix connector fails the
   concurrent step-up contract.
2. **pgx byte-order pagination parity requires a `C`-collation database.** An
   `en_US.utf8` Postgres host pages same-`created_at` lists in linguistic order,
   which diverges from SQLite/libSQL `BINARY` byte order on the id/subject/resource
   tiebreak. This is a pre-existing, parked finding in the shared
   `integrations/datastores/pgxdb` pagination helper — **not** fixed by this
   runbook. A host that needs cross-dialect byte-order pagination parity should
   run Postgres with `LC_COLLATE 'C'`. It does not affect any v3 rail or the
   migration itself.

### AV3-9.2 execution record

Executed 2026-07-13 against fresh/reset databases in the authorized playground
containers, both dialects, all four fixture paths; every fixture torn down after
the run (the long-lived conformance databases were never touched). Fixtures:
verified+password, unverified+password, OAuth-only (no password row),
password-only, and an un-normalized duplicate-collision pair.

- **pgx clean path** — fresh `C`-collation database (`TEMPLATE template0
  LC_COLLATE 'C' LC_CTYPE 'C'`), v2-shape seed (4 users / 3 passwords / 1 oauth /
  1 session / 1 invitation / 1 verification-code / 1 verification-token). Steps
  1–5: collision dry-run **0 rows**; Step-3 backfill **`INSERT 0 4`**; Step-4
  **parity 4/4**, 0 users missing a primary email, 0 duplicate auth-claims,
  0 orphan passwords/oauth/sessions. Step-3 **re-run `INSERT 0 0`** (idempotent,
  identifier count still 4). Verification state preserved exactly (unverified →
  `verified_at NULL`, all others non-NULL). Step-6 `DROP COLUMN email` /
  `email_verified` + `DROP TABLE verification_codes/verification_tokens` clean;
  `users` left at `id, display_name, created_at, updated_at, auth_revision`.
  AFTER: passwords/oauth/sessions/invitations byte-identical to BEFORE, session
  metadata columns present and defaulted. **PASS.**
- **pgx collision path** — fresh `C`-collation database with an un-normalized
  duplicate (` Verified@Example.com ` vs `verified@example.com`): Step-1 dry-run
  **returned `verified@example.com` with both user ids** (abort signal); forcing
  the Step-3 backfill anyway **failed on `idx_user_identifiers_auth_claim`**
  (`duplicate key value violates unique constraint`) and left `user_identifiers`
  **empty** (0 rows — no partial migration). **PASS (expected failure observed).**
- **SQLite/libSQL clean path** — executed against the live libsql server
  (`http://127.0.0.1:8080`, `POST /v2/pipeline`) in an isolated table namespace so
  the standing conformance schema was untouched; identical runbook SQL. Steps 1–5:
  dry-run **0 rows**, backfill **4 rows**, parity **4/4**, 0 missing-primary,
  0 duplicate auth-claims, 0 orphans; re-run **0 inserted** (idempotent); Step-6
  **12-step table rebuild** left `users` at the final v3 shape with identifiers
  intact and both verification tables dropped; AFTER counts preserved
  (users/passwords/oauth/sessions/invitations/identifiers = 4/3/1/1/1/4),
  verification state exact. **PASS.**
- **SQLite/libSQL collision path** — live libsql server, un-normalized duplicate:
  dry-run detected `verified@example.com` (x2); forced backfill **aborted**
  (`UNIQUE constraint failed: user_identifiers.kind, normalized_value`) with
  **0 rows** written. **PASS (expected failure observed).**

**Do not apply this runbook to segovia or another real host in this milestone.**

## Auth delivery-runtime upgrade runbook (bespoke auth outbox → generic jobs / in_process)

A **host-owned** procedure for a database already running auth-v3 as shipped
through the AV3D-5.0 cut — i.e. one that scaffolded and populated the bespoke
`delivery_jobs` outbox table — that is upgrading to auth at or past the AV3D
delivery refactor (2026-07-13). After the refactor auth owns **no** delivery
table: durable delivery is the generic **jobs** feature's `fenced_job_queue`
(host-owned in the jobs migration tree) and `in_process` delivery is a bounded
ephemeral pool with no table. `Repositories.DeliveryJobs`, the private
claim/poll/purge worker, and `Service.RunDeliveryWorker` are gone; the send path
is now `Config.DeliveryMode` + a host-wired `Config.DeliveryDispatcher` (jobs
mode) or `Service.RunDelivery(ctx)` (`in_process`).

Per the standing greenfield-migrations rule (2026-07-12) this is **not** a
canonical migration — the canonical auth set ships the final schema only
(`0001–0013`, no `delivery_jobs`). This runbook is host-side prose; every DDL
step below runs from the host's **own** migration tree, pre-boot, exactly like
every other host-owned migration.

**A host with an EMPTY or absent `delivery_jobs` table has nothing to drain** —
skip Steps 1–2/4, apply the new host wiring (Step 3), drop the empty table
(Step 5), and start the chosen runtime (Step 6).

**Tooling never decrypts a payload.** Every `delivery_jobs.payload` (and every
`fenced_job_queue.payload`) is an opaque, AES-GCM-sealed `command.Envelope`
(`AUTH_DELIVERY_ENCRYPTER_KEY`) carrying the rendered secret and destination. No
step here — count or drop — opens that ciphertext. Only the running auth delivery
processor, holding the encrypter key, ever unseals it: during this upgrade that is
the OLD binary's worker as it drains.

The drain is the **single supported path**. A prior draft offered an opaque
export/re-enqueue copy of the bespoke rows into the generic queue; that path is
unsafe and has been removed — the legacy ciphertext encodes the removed bespoke
envelope shape (not the generic queue's versioned command), the legacy rail kinds
(`email`/`phone`) are not the registered `authentication.delivery` job kind the
generic runtime dispatches on, the copy never terminalized its source rows (so the
zero-source-count check could never pass honestly), and the libSQL variant minted
`datetime('now')` strings the turso connector's fixed-width `Time.Scan` cannot
parse. An opaque copy cannot work in either dialect. Drain-then-drop is the only
supported procedure off the bespoke outbox.

### Preconditions

- A confirmed, restorable **backup** taken before Step 1.
- The OLD binary retains its existing **`AUTH_DELIVERY_ENCRYPTER_KEY`** through the
  drain: it is the only process that unseals `delivery_jobs` payloads and must hold
  the key that sealed them. No payload crosses queues (the drain is in place), so no
  cross-queue key portability is required; the NEW binary seals its own newly
  admitted work under its own key.
- **Do not rotate the delivery key mid-drain** — the old worker cannot unseal
  in-flight rows that were sealed under the previous key.

### Deploy ordering (single cutover — do NOT roll)

Old and new binaries must not both serve the same database across the cutover:
the old binary claims `delivery_jobs`, the new binary has no code that reads it.
Quiesce the old binary's admission, drain, upgrade, then start the new runtime.

### Step 1 — Stop old delivery workers and quiesce admission

Stop the old binary's delivery worker loop (`Service.RunDeliveryWorker`) — or
stop the old binary outright — and quiesce admission so no NEW `delivery_jobs`
rows are written (drain traffic to the start endpoints, or take the maintenance
window). No new opaque command lands in the bespoke table from this point.

### Step 2 — Drain the pending encrypted commands

Keep the OLD binary's delivery worker running (admission still quiesced from
Step 1) until it processes every non-terminal row: `state = 'pending'` rows are
sent or retried to their terminal state (`succeeded`/`failed`/`canceled`), and
terminally undeliverable rows discard their bound challenge best-effort. A leased,
in-flight row is still `state = 'pending'` (the lease is `lease_id`/`leased_until`,
not a separate state), so it counts as non-terminal until the worker terminalizes
it. When the pending count reaches zero (Step 4), upgrade.

The drain preserves at-least-once semantics and never decrypts a payload in
tooling: only the old worker, holding the encrypter key, unseals a row, and it
does so on the normal send path.

- *Tradeoffs.* Requires the old binary + worker to keep running through the
  drain; the drain is bounded by the old queue's retry/backoff horizon (a row in
  long backoff delays completion; you may let it dead-letter rather than wait).
  No encryption handling, no key coupling, no logical-key bookkeeping, no payload
  movement. A large dead-letter/backoff backlog only means a longer drain window,
  not a different path — there is no supported alternative to the drain.

### Step 3 — Apply the generic jobs schema and new host wiring

- **jobs mode (recommended production posture).** Scaffold the generic jobs
  migration tree into the host (`jobsstore.ExportMigrations` from
  `features/jobs/stores/{pgx,turso}`; canonical set `0001_job_queue`,
  `0002_job_schedules`, `0003_fenced_job_queue` — identical filename set across
  both dialects) and apply it pre-boot. Wire `Config.DeliveryMode = "jobs"`, a
  `Config.DeliveryDispatcher` backed by the generic jobs feature, and set the
  `DeliveryJobsAcknowledged` wiring assertion (production requires it —
  `ErrDeliveryJobsUnacknowledged`). A composition adapter (never a feature core)
  bridges auth's `Service.DeliveryJobRuntime()` onto `jobs.Runtime`.
- **in_process mode.** No jobs schema is needed (the bounded pool owns no table).
  Set `Config.DeliveryMode = "in_process"`, keep `Config.DeliveryEncrypter`, and
  set `DeliveryEphemeralAcknowledged` (production requires the crash-loss
  acknowledgment — `ErrDeliveryEphemeralUnacknowledged`). Accepted work does NOT
  survive a restart, so the bespoke outbox must be fully drained (Step 2) before
  cutover — there is no supported path that moves a durable backlog into an
  ephemeral queue.

`Register` starts no runtime in either mode; the host runs the selected runtime
in Step 6.

### Step 4 — Verify no active auth delivery rows remain

Before dropping the table, confirm the bespoke outbox holds no live work. The
active-work count MUST be zero:

pgx / SQLite / libSQL:

```sql
-- Active (unprocessed) bespoke delivery rows — MUST be 0 before Step 5.
SELECT count(*) AS active_delivery_jobs FROM delivery_jobs WHERE state = 'pending';
```

The Step-2 drain drives this to 0: once every `state = 'pending'` row (including
leased, in-flight rows) has reached a terminal state, the count is exact and no
row is lost or duplicated (terminal rows are retained with `terminal_at` set until
Step 5 drops the table). A non-zero active count **stops the upgrade** — finish the
drain first; never drop a table with live encrypted work in it.

### Step 5 — Drop the obsolete `delivery_jobs` table (host-owned)

Once Step 4 shows zero active rows, remove the bespoke table from the host's own
migration tree. This is a host-owned destructive migration, not a canonical one.

pgx:

```sql
DROP TABLE IF EXISTS delivery_jobs;
```

SQLite / libSQL (`DROP TABLE` also drops the table's indexes):

```sql
DROP TABLE IF EXISTS delivery_jobs;
```

This is the point of no return for reading any residual bespoke row; take it only
after Step 4 is clean (zero active rows).

### Step 6 — Start the generic jobs runtime or the bounded runtime

- **jobs mode:** start the generic jobs runtime the host wired in Step 3
  (`go rt.Run(ctx)` for the composed `jobs.Runtime`); cancel the ctx to drain.
  Newly admitted commands now process on the durable fenced queue.
- **in_process mode:** start the bounded pool with `go authSvc.RunDelivery(ctx)`
  for the process lifetime; cancel the ctx for a bounded shutdown drain.

Confirm end to end that a fresh start endpoint (register verification,
forgot-password, passwordless start) delivers OFF the request path on the new
runtime before reopening admission.

### Forward-only recovery and the no-decrypt / no-blind-copy warnings

- **Forward-only.** There is no down-migration. If the upgrade must be abandoned
  before Step 5, redeploy the old binary (it still reads `delivery_jobs`); after
  Step 5 recovery requires the Step-1 backup.
- **Never decrypt a payload in tooling.** The count and drop steps treat
  `payload` as opaque bytes. Only the running processor with the encrypter key
  unseals it — during this upgrade that is the old worker draining the outbox.
- **Never blind-copy a canonical migration.** The canonical auth set is
  greenfield/final-shape and carries no `delivery_jobs`; this host-owned
  drain-then-drop procedure is the only supported path off the bespoke outbox.
- **`in_process` is ephemeral.** Accepted work does not survive a restart, so
  drain the bespoke outbox before cutover; there is no supported copy of a durable
  backlog into an ephemeral queue.

This delivery-runtime runbook has **not** been applied to a real application host
(no auth module tag has been cut — `git tag -l` is empty, so the greenfield
rewrite that removed `delivery_jobs` from the canonical set is allowed with no
append-only constraint).

### AV3-9.8 drain-path fixture verification (both dialects)

The drain-only path's Step-4 verification query and source-row accounting were
proven on disposable fixtures on both dialects (IX-01 remediation). Each fixture
seeds legacy-shape `delivery_jobs` rows in mixed states, runs the Step-4 count,
terminalizes the remaining non-terminal rows exactly as a drained worker would,
and re-runs the count — proving it reaches 0 with no row lost or duplicated.

- **pgx** — disposable database `dr_drain_fixture` (`TEMPLATE template0
  LC_COLLATE 'C' LC_CTYPE 'C'`), legacy `delivery_jobs` created from the shipped
  `0014` shape. Seed: 5 rows — 2 `pending` (one unleased, one leased/in-flight),
  1 `succeeded`, 1 `failed`, 1 `canceled`. Step-4 count → `active_delivery_jobs =
  2` (the leased in-flight row counts as pending — exact). Drained-worker
  terminalize (`UPDATE … SET state='succeeded', terminal_at=now(), lease_id='',
  leased_until=NULL WHERE state='pending'`) → `UPDATE 2`. Step-4 re-count → `0`.
  Total accounting: `SELECT count(*)` = 5 before and after (no row lost or
  duplicated); terminal breakdown afterward `succeeded=3, failed=1, canceled=1`.
  Fixture database dropped afterward.
- **turso/libSQL** — live server, isolated `dr_`-prefixed fixture table
  `dr_delivery_jobs` created from the shipped turso `0014` shape (fixed-width TEXT
  timestamps). Same 5-row mixed-state seed via `POST /v2/pipeline`. Step-4 count →
  `2`. Terminalize (`… SET state='succeeded', terminal_at=<fixed-width ISO>,
  leased_until=NULL WHERE state='pending'`) → 2 rows affected. Step-4 re-count →
  `0`. Total accounting: 5 rows before and after; terminal breakdown `succeeded=3,
  failed=1, canceled=1`. All `dr_`-prefixed fixture tables dropped afterward; the
  standing conformance schema was untouched.

Both container databases were left running; no canonical/conformance table was
modified.

## Migrating from the monolith repository

The old monolith (`github.com/gopernicus/gopernicus v0.5.4`) and this
multi-module repository share import-path prefixes: the monolith's packages and
the nested modules here both resolve under `github.com/gopernicus/gopernicus/...`.
Go module resolution cannot hold both in one active workspace — a `go.work` (or a
single module graph) that references the monolith and any module from this repo
sees two providers for one import prefix, so a migrating host can never keep the
old and new dependencies live side by side.

Migrate across a split workspace or module boundary instead: keep the
still-on-monolith code in its own module tree with its own workspace file, and
make the migrated tree's workspace reference only this repository's modules. The
first adopter's working pattern is a v2-only root `go.work` with a shadowing
`v1/go.work` for the legacy tree; the two trees converge only when the last
monolith import is gone.

## What this repo is not doing (yet)

- No CI-driven automated tagging — tags are cut by hand until a release
  workflow is built.
- No changelog convention is mandated yet; the tag message plus commit log is
  the record until one is adopted.
