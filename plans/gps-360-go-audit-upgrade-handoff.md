# gps-360-go: adopt the coordinated Gopernicus audit releases

Paste this document into a new context opened in
`/Users/jrazmi/code/gps/three-sixty/gps-360-go`.

## Task and authorization

Build the Gopernicus dependency upgrade in this repository. Plan the significant
work first using this repository's established numbered `plans/NN-*.md` format,
then implement and verify it. This is an instruction to complete the local
upgrade, including required host migration files and vendoring, rather than stop
after proposing a plan. Preserve all existing user work and repository rulings.

Update **every Gopernicus package this application actually uses**, including
tests, tools, workers, CLI contracts, and newly split dependencies needed to keep
existing functionality. Read and account for **AUDIT-001 through AUDIT-034**, not
just the latest entries. Do not add every optional module, change datastores,
enable disabled authentication features, or reintroduce retired integrations.

No production/shared database migration, cloud mutation, real provider send,
deployment, push, or PR is authorized by this prompt. Complete and test the local
result and write an explicit rollout handoff. Use disposable local fixtures for
runtime and migration verification. Do not read or print credentials to do the
inventory. Ordinary local code edits, dependency downloads, regeneration and
disposable verification are part of the requested upgrade.

## Sources and release authority

Framework checkout: `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus`.
Read these from that checkout:

- `AUDIT.md`: the consumer migration guide, **all 34 entries**.
- `plans/audit-release-manifest.json`: exact versions, source commit and public
  checksums for the 34-module audit release, published from
  `c3f8b4ad453021ba1e40c91572d8e4618c2d8382` on 2026-09-11.
- `plans/firestore-release-manifest.json` and `plans/firestore-release.md`:
  authoritative separate Firestore publication/verification state.
- `plans/pocket-api-migrations.json`: detailed import/symbol map, interpreted
  together with the later constructor changes in AUDIT-030/031/032.
- `RELEASING.md`, `ARCHITECTURE.md`, `examples/README.md`, the relevant module
  READMEs, canonical SQL migrations and final public constructor/port sources.

**The audit alone is not the whole upgrade.** Several application pins precede
the audit's starting point (notably authorization v0.7.0 versus the pre-audit
v0.12.0). For every current-to-target version span, also read the intervening
published notes in `RELEASING.md` and relevant store upgrade guides. Record
pre-audit applicability beside the 34-entry matrix. Do not assume a change was
already adopted merely because its tag is older than the audit release.

The audit entries are progressive implementation history. Their old
"unreleased", "no tags", temporary import paths and retained-Config examples
are not the current release state. The manifests and current release overview
override those historical status statements. Final tagged source is authoritative
for APIs. In particular, AUDIT-017 updates older `sdk/foundation` examples to
`sdk/pkg`; AUDIT-026 supersedes authorization receipts/revisions;
AUDIT-028 supersedes the universal pocket root services; AUDIT-031 supersedes
the retained non-CMS root Config records in AUDIT-030. Migrate directly to the
final APIs rather than implementing and undoing each intermediate design.

Firestore's three separate **v0.1.0** tags were published on 2026-09-13 from
`10e5f97b32c043f8fdd71cbd733fbb09592c42f7`, with the real-GCP gate explicitly
waived by the owner. Fresh public downloads matched all ZIP/go.mod checksums and
176 source entries; independent build/test/vet and live-suite compile/vet passed
with `GOWORK=off`, no replacements and normal public checksum verification.
**The real GCP suite remains untested.** Emulator success does not verify
composite-index deployment/readiness, Admin API index probing, production
contention/snapshots or backend transaction/size limits. The 34 broader audit
versions above are unchanged by this Firestore promotion. Neither a missing
merge to framework `main` nor old audit status text is a reason to use old pins
or a local replacement. Recheck the manifests when starting the upgrade.

## Reconcile the application before editing

Read the applicable ancestor instructions and this repo's current `CLAUDE.md`
whole, plus any `AGENTS.md` and closer instructions. Its later dated rulings
supersede earlier narrative; actual existing user work must be preserved.
Named project agents are suggested, not automatically dispatched: follow the
current repo's agent policy and use a named role only when actually authorized.

The read-only preparation on 2026-09-13 observed:

- A clean local `main` at `e1ab3f0e8f1c2b6ffdbf3081933c22b032c63da9`, **27 commits
  behind** cached `origin/main` at `589a6b24528d4e96de902178a732f695e947bd9c`.
  Both refs had the same Gopernicus `go.mod`, `go.sum` and vendor tree. This was a
  local-ref observation, not a fresh network check or permission to reset work.
- Those 27 commits add real IO/commission/media workflows, historic clients,
  resource posts/review/queue, matching CLI commands, `workshop/parity`, an
  `adspend` migration, and a fix making devstack apply new upstream ledger files.
  They are part of the application to preserve and migrate.
- One Go module, Go **1.26.1**, checked-in `vendor/`, no local replacements or
  repo `go.work`. No first-party `package.json`, JavaScript lockfile or `.github`
  workflow was observed. Vendor dependencies' own CI/config files are unrelated.
- Local `plans/` is gitignored; present top-level numbered plans ended at 42,
  while code/instructions also refer to 43/44 and moved `plans/old` records.
  Do not assume 43 is available for reuse. Inventory the current plan record,
  preserve in-progress plans, and select the next appropriate unused number.

Start with fresh `git status`, branch/upstream/worktree inventory and refs;
inspect current module/vendor state and any unfinished upgrade before choosing
the working branch. Fetch/reconcile non-destructively as appropriate to the
current state; do not develop this upgrade on stale local main while dropping
the 27 newer commits. Use an isolated branch/worktree if useful. Do not overwrite,
reset, stash away or rebase somebody else's work without authorization.

Read current Makefile/scripts before running them. `make run` depends on
`make migrate`: it can mutate whatever `DB_URL` points at. Pin a disposable local
target explicitly before either command; never inherit a shared/production DSN
from a developer's environment. `workshop/testdb` uses Docker/PostgreSQL and the
sibling `../gps-360` migration ledger, or `GPS360_MIGRATIONS_DIR`. Missing fixtures
must be reported as failures/blockers, not converted to silent skips. Newer
`workshop/parity` additionally needs the sibling's Git history (optionally
`GPS360_UPSTREAM_DIR`).

## Coordinated version set

Every module below is prefixed with `github.com/gopernicus/gopernicus/`.
The previous versions are the observed baseline on both application refs above;
re-inventory if the repo has advanced. Use exact versions from the manifests,
not `@latest`, pseudo-versions, broad `go get -u`, or local source replacements.

| Module | Observed version | Target |
|---|---|---|
| `sdk` | v0.7.1 | **v0.9.0** |
| `integrations/cryptids/bcrypt` | v0.1.0 | **v0.2.0** |
| `integrations/datastores/pgxdb` | v0.6.1 | **v0.7.0** |
| `integrations/email/sendgrid` | v0.2.0 | **v0.3.0** |
| `integrations/filestorage/gcs` | v0.1.0 | **v0.2.0** |
| `integrations/filestorage/s3` | v0.1.0 | **v0.2.0** |
| `integrations/oauth/google` | v0.1.0 | **v0.2.0** |
| `integrations/scheduling/robfig-cron` | v0.1.0 | **v0.2.0** |
| `pockets/authentication` | v0.9.0 | **v0.11.0** |
| `pockets/authentication/stores/pgx` | v0.5.0 | **v0.6.0** |
| `pockets/authorization` | v0.7.0 | **v0.13.0** |
| `pockets/authorization/stores/pgx` | v0.3.0 | **v0.8.0** |
| `pockets/jobs` | v0.5.0 | **v0.6.0** |
| `pockets/jobs/stores/pgx` | v0.5.0 | **v0.6.0** |
| `integrations/cryptids/golang-jwt` | New required split for existing signer | **v0.2.0** |
| `pockets` | New required split for existing mount contract | **v0.1.0** |

The host presently uses PostgreSQL for authentication, authorization and jobs.
Firestore, Turso, Redis, events, CMS, bundled Goth views/UI and OTel are not reasons
to add new features. Mark unused modules/entries not applicable with evidence;
if the fresh inventory finds additional existing Gopernicus dependencies, add
their manifest pins to the coordinated update. Keep unrelated dependency changes
to the minimum required by the selected module graph.

Important intervening published changes already identified:

| Version span | Required account before applying final audit APIs |
|---|---|
| SDK v0.7.1 → v0.8.0 | Dotenv whitespace/comment correction, nested env tags and empty-value precedence; optional Secret helpers decode **hex**, unlike this host's raw JWT key. Preserve host-specific defaults, modes, proxy/origin policy and JOBS_HANDLER_TIMEOUT. |
| Authentication v0.9.0 → v0.9.1 | New env tags can become live when moving from hand-built config to parsed feature records; do not accidentally enable delivery/passwordless or override host mode/origin/cookie posture. |
| Authentication v0.9.x → v0.10.0 | Optional principal middleware is additive; absent credentials may pass only on deliberately optional surfaces, while invalid presented credentials still fail. Do not convert this protected API to anonymous admission. |
| Authorization v0.7.0 → v0.8.0 | Guard/store decision-view port changes, in-transaction permission evaluation; final AUDIT-024/026/028 shapes replace the intermediate CheckPermission/revision APIs. |
| Authorization v0.8.x → v0.9.0 and pgx store v0.4.x → v0.5.0 | Raw baseline SQL reads/writes join the host callback transaction on the same DB; guarded/trusted mutation boundary still refuses ambient use. Final audit concurrency behavior supersedes historical lock/revision details. |
| Authorization v0.9.x → v0.10.0 | Candidate-source page-filling and bounded scan semantics; final FilteredPage/ScanLimitReached replaces the original plain Page result. |
| Authorization v0.10.x → v0.11.0 and pgx store → v0.6.0 | Required **0005 keyset index migration**, byte-order ID paging, query/model-bound cursors and HasMore/NextCursor; the host's current cap-50 roster needs explicit consideration. |
| Authorization v0.11.x → v0.12.0 and pgx store → v0.7.0 | Set-read/batch port and evaluation changes; final AUDIT-023/024 model/read contracts supersede historical required methods and budget claims. |
| Jobs core/store v0.5.0 | Already the observed baseline: preserve its per-kind queue/reclaim/scheduler isolation while adopting the final runtime and durable occurrence protocol. |

SDK v0.8.0 also documents a concrete old dotenv defect: a line shaped
`AUTH_JWT_SECRET=   # long public example comment` could become the comment as
the effective signing key. Record the operator's need to check whether any
environment ever booted that way and rotate affected keys/tokens if so; do not
claim every host is affected, expose live secret values, or perform a production
key rotation during this local upgrade. Ordinary valid keys retain exact bytes.

## Migration ledger: account for every audit entry

Create a row per entry in the application plan with status
`applicable / already satisfied / not applicable / blocked`, affected paths,
required action, and verification evidence. The following is a starting triage,
not permission to skip the full entry or fresh source search:

| Audit | Required review for this host |
|---|---|
| 001 Validation | Replace `web.FieldErrors` (including echo DTO), retain field names/messages/codes; exercise pointer validation and top-level null rejection. |
| 002 Removed conversions | Search source/tests/generator inputs; use root `sdk.Deref`/`DerefOr` only where needed; preserve host date/JSON/ID behavior. |
| 003 Environment/logging | Check ignored LoadEnv errors in server, every worker and workshop command; keep configured logging and request IDs, safe startup diagnostics. |
| 004 Identity/crypto | Replace SDK HS256 with exact-pinned JWT integration, ID generator/root identity and SHA256 helper; preserve effective key bytes and token/digest compatibility. |
| 005 Listing/transactions | Widespread crud-to-list migration; audit SQL projected columns, FixedOrder, cursor/reverse probes and host transaction participation. |
| 006 Web | Remove WithLogging, explicitly compose logger/panics/request ID; preserve unknown-field DTO policy, no-store, actual HTTP errors and shutdown. |
| 007 Workers | Preserve handler timeouts, per-kind ownership, retry/dead-letter and drain behavior; review comps cancellation follow-up. |
| 008 Cryptids spelling | Use final `sdk/pkg/cryptids`; no temporary cryptography package. |
| 009 SDK root primitives | Migrate every principal producer/reader/resolver, ID/digest helper and mock coherently. |
| 010 Cache | Inventory direct/transitive usage; no new cache required; apply final constructor options if used. |
| 011 Rate limiter | Audit authentication's limiter/defaults and production policy; PostgreSQL window_ms migration only if that adapter is actually selected. |
| 012 Event delivery | Currently no events pocket/Redis wiring; verify absence, preserve any newly found checked handoff rather than using async Emit. |
| 013 File storage | Migrate server + comps + host GCS/S3 wrappers, direct Storer, concrete cleanup, stable archive overwrite, key/range behavior. |
| 014 Email/notify | Move email import under notify, update posture/render APIs and constructor errors; delivery stays off where already off. |
| 015 OAuth/tracing | Google config, explicit TrustOAuthEmail, bound browser flow/cookie and callback policy; tracing only if inventory finds it. |
| 016 Shared pockets | Replace sdk/pocket with the independent pockets module for mounts and tests. |
| 017 sdk/pkg | Remove remaining sdk/foundation imports, including guard/CLI census patterns and tools. |
| 018 Handler snapshots | Finish handler registry before final runtime construction; retain per-worker kind isolation, especially foreign schedules. |
| 019 SQL job generation | Upgrade pgx adapter in every binary; fenced capability currently unwired, no historical job rewrite. |
| 020 Durable schedules | Append jobs0004, wire occurrence-aware scheduling only where already enabled, test crash/restart handoff and MaxAttempts. |
| 021 Events visibility/outbox | Currently absent; no event_outbox migration or stream feature unless fresh inventory proves existing use. |
| 022 Authentication proofs | Append authentication0018 and migrate repository/harness contracts; preserve Google-only, machine keys, disabled password/invitation/delivery posture. |
| 023 Authorization models/lists | Preserve hand-typed RoleModel; migrate complete-set vs ID-page semantics and hub roster truncation honestly. |
| 024 Guards/policy | Migrate guard types and role-route policy; preserve steward gate and trusted PolicyMutator, do not invent relationship guardians for a roles-only model. |
| 025 Tuple identity | Append authorization0006 after missing prerequisites; remove raw role timestamps, restart old cursors and update role CLI ordering. |
| 026 Mutation/history | Append authorization0007; remove receipts/revision/mutation IDs from role APIs and CLI; preserve current outcomes, error handling, raw-vs-guarded ownership. |
| 027 Store support | Update existing memory/conformance test import paths if present; do not introduce replacement in-memory host stores. |
| 028 Public pocket components | Migrate auth, authorization, jobs services/HTTP/runtime/logic paths and test helpers to narrow named components. |
| 029 Integrations | Pass startup context to probing SQL stores, preserve schema/connection ownership; inspect UTC cron and S3 region behavior. |
| 030 Constructor options | Apply final SDK/integration options; no options applied to live objects or nil option placeholders. |
| 031 Pocket options | Final constructors require explicit signer/modes/handler map and typed options; entire feature records replace earlier records. |
| 032 Startup errors | Context-first pgxdb.Open throughout tools/tests/binaries; check SendGrid/S3 construction errors before wiring consumers. |
| 033 Turso finalization | Not applicable to current pgx host; verify no newly added Turso usage. |
| 034 Firestore | Not applicable to current pgx host; no datastore switch or Firestore dependency required by this release. |

## Host-specific behavior that must survive

**Authentication and authorization.** Read `cmd/server/authentication.go`, the
auth construction/gate closures in `cmd/server/main.go`, `pockets/auth/{logic,
inbound,outbound}`, `workshop/{testauth,testweb,devcred,steward}`, and all affected
CLI adapters. Keep Google-only login, exact `user:<auth-user-id>` and
`service_account:<id>` principals, act-as-user keys acting as their owner, and
the existing machine/human gate distinctions. Do not resurrect stale `staff`
principal naming from an older paragraph. Keep the closed section registry and
hand-typed role model; service accounts cannot gain steward/developer through
assignment. Directory reads remain open to admitted principals, hub resources
retain their organization scope, and route gates remain explicit.

Choose explicit OAuth email trust that preserves the intended Google Workspace
posture and verified authoritative email requirements. Nil trust denies new
email-based adoption; simply adding the callback is not an independent staff
admission redesign. Inspect the current later rulings and actual admission path,
and surface material ambiguity rather than silently broadening who can log in.
Verify allowed origins, browser proof cookies, refresh/logout and secure cookie
configuration. Preserve development/production mode separation and the new
production limiter requirement; do not make a production boot green by changing
its mode, weakening cookie checks, or silently substituting a local limiter.

`AUTH_JWT_SECRET` currently enters the signer as literal string bytes in server
and devcred. Use `golangjwt.New(secret)` for that same effective key; **do not
hex-decode it merely because the dev fallback looks hex-encoded**. Other
challenge/identifier keys currently do decode hex and retain their own meaning.
Use synthetic compatibility fixtures, never real tokens or secrets in reports.

The universal root `authentication.Service`, `authorization.Service` and
`jobs.Service` no longer exist. Follow public `logic/...` and `inbound/http`
components; avoid recreating a forwarding facade just to retain old signatures.
Keep trusted `SystemMutator` and raw writers out of ordinary handler dependencies.
The current role gate is a deliberate authenticate-then-steward closure, while
`auth.PolicyMutator` covers trusted assignment paths; preserve both protections.
An allow-only guard is safe here only behind its intended host authority boundary.

The terminal client carries its **own** wire DTOs. Its role command bodies and
`MutationReceiptDTO`/`RoleReceiptWriteDTO`/`ReceiptOutcome` logic still describe
the removed revision/receipt protocol. Update the client, renderers, error/exit
handling and tests with the real new bundled role response. Do not remove domain
CAS (`expected_updated_at`) or unrelated business receipts. Only authorization
mutation revisions/receipts were retired. Preserve the CLI transport, dry-run,
confirmation, no-datastore/no-Origin fences and route/gate parity mechanisms.

`internal/inbound/compositions/clienthub/roster.go` currently bounds a permission
ID lookup at 50 and exposes `truncated`. Distinguish a bounded ID page from a
complete allowed-ID filter; do not claim a globally name-sorted complete roster
by hydrating only a first ID page. Preserve or deliberately update the host's
bounded-response contract and its CLI together, including unrestricted and
restricted-empty cases and explicit continuation/scan-limit facts where used.

**SQL and existing data.** The host uses one unpinned pgx pool and explicitly
qualified schemas: authentication/authorization in `auth`, jobs in `jobs`, domain
data in `gps`/existing `adspend`. Maintain narrow domain ports, per-domain SQL,
strict row scans, projections and host transactions. SQL list helpers now wrap
the authored SELECT: search/order/PK/FixedOrder columns must be projected with
unqualified output names. Review all lists, including newer origin/main slices,
not just the reference directory files. Preserve unknown JSON keys, omitted/null
patch semantics, wire field names, audit rows and optimistic business concurrency.

Compare canonical migration **contents and prerequisites** with the current
host ledger; do not just copy the last audit's file names. At the observed
baseline the likely missing source migrations are:

| Canonical source | Host stream |
|---|---|
| authentication/pgx `0018_invitation_acceptance.sql` | `workshop/migrations/primary`, `auth` |
| authorization/pgx `0005_iam_lookup_keyset.sql` | `workshop/migrations/primary`, `auth` |
| authorization/pgx `0006_iam_tuple_identity.sql` | same, after 0005 |
| authorization/pgx `0007_iam_audit.sql` | same, after 0006 |
| jobs/pgx `0004_job_schedule_occurrences.sql` | `workshop/migrations/jobs`, `jobs` |

The existing files use **epoch prefixes**, despite a stale primary README's
four-digit advice. Append properly ordered new host files and qualify all
persistent relations/index references before first apply. Do not edit/renumber
applied files, recopy whole greenfield schemas, invent missing source0017, or
write `gps.*` DDL here. Authorization0006 has a TEMP preflight table: preserve
its temporary semantics and separator validation while adapting the host ledger;
do not mechanically qualify it into `auth` or broadly weaken schema guards.

Even disabled invitations or unwired relationship/fenced features can have
required schema probes; verify final selected stores' complete requirements.
The audit reader expects `iam_audit` even with recording off. Update stale
testauth cleanup of `auth.iam_mutations`, schema probes and ledger tests. Leave
optional `WithAudit()` policy explicit; if enabled, all trusted/raw write sites
need attribution and atomic rollback tests. Do not manufacture past history.

Prove both fresh installation and **upgrade from the existing host ledger with
seeded accounts, sessions, role facts, jobs and schedules**. Preserve facts and
payloads; account explicitly for removed authorization timestamps/receipt tables.
Document archive/backup, quiescing old authentication/authorization writers and
old scheduler workers, migration order, proof-flow restarts and rollback limits
for a later operator. The host runner applies auth, jobs, adspend as separate
transactions; failure of a later stream does not roll back earlier streams.
Workers never migrate themselves. Do not assume wrapping authentication service
calls in host `Transact` makes them atomic; guarded authorization rejects ambient
transactions, while participating SQL raw writers require the same connector and
callback context. Keep external effects outside transactions or on an already
established durable handoff.

**Workers, files and mail.** Migrate all three composition roots
`cmd/workers/{echo,delivery,comps}` and server enqueue adapters. The server stays
enqueue-only, echo stays queue-only, and only the two existing ingest workers
enable their schedules in production. Keep development schedules disabled,
per-kind claim isolation, exact payload versions, handler/store deadlines,
MaxAttempts, dead-letter retry-as-new-execution and signal drain. Use final
`queue.NewRuntime(queueService, handlers, options...)` and optional scheduler
components; do not keep tests expecting removed ErrSchedulesNotConfigured.
Exercise durable pending-occurrence recovery with the host's PostgreSQL schema.

AUDIT-007 records an existing comps cancellation issue: cancellation in the
last race can become a summary string and a nil overall job result. Inspect the
current `internal/logic/domains/comps/import.go`/RunImport path. If still present,
preserve intentional per-race partial success while propagating cancellation at
the workflow boundary; cover the actual regression without expanding ingest scope.

Remove `filestorage.FileStore` and pass Storer/narrow ports directly through
`cmd/server/filestorage.go`, `cmd/workers/comps/main.go` and host
`integrations/filestorage/{gcs,s3}`. Disk now needs real Close after users stop;
returned download readers still need Close. Comps intentionally replaces a stable
archive key on replay: preserve overwrite and exact key identity. Test failed
replacement retains previous bytes, ranges/not-found and local shutdown. Review
GCS prefix/signing, S3 region, permissions and optional upload contracts only to
the extent the host uses them. Owned sinks/fake providers are sufficient for
local verification; report actual cloud signing/CORS as unverified if not run.
Migrate `integrations/emailer` posture and email API imports; do not enable sending
merely because the prior construction-only mailer is being updated.

## Known separate upstream follow-up

`plans/firestore-release.md` records a **core/SQL identifier-replacement
credential-ownership validation gap** discovered during Firestore review. The
Firestore mutation path was fixed separately; the published authentication core
and SQL releases were not changed for that follow-up. The original detailed
report `/tmp/firestore-credential-ownership-followup.md` was absent during this
handoff preparation. Do not claim the gap fixed by these pins or invent its
missing call-path details. Reconstruct the finding from current core/SQL source,
identify whether this host exposes it, and record the concrete risk/test or
upstream prerequisite. Keep it visible in the final handoff even if the package
upgrade succeeds. Any required upstream framework fix is a separate source fix
and published patch, never a fork or local replacement hidden in this host.

## Execution and verification

Organize the application plan into explicit dependent batches: current-state
inventory and audit matrix; coordinated pins/imports/public component wiring;
host ledger and behavior changes; vendoring and real verification. Preserve the
active plan path, changed-file list, commands, baseline failures, current failures
and unverified external checks across context boundaries. Keep surgical diffs;
remove only imports/helpers orphaned by this upgrade. Do not hand-edit vendor or
generated outputs. Refresh docs/guard/census inputs to describe the final APIs.

Before edits, establish the available baseline checks and fixture state. After
pinning and migrating imports, run `GOWORK=off go mod tidy` and
`GOWORK=off go mod vendor`; verify the resolved Gopernicus module versions and
absence of replacements with `GOWORK=off go list -mod=mod -m all`, and run
`GOWORK=off go mod verify`. Keep normal public checksums enabled. If more than one
module has arrived, repeat appropriate checks per module; a root test does not
cover nested modules. Format changed Go source with the documented **goimports**.

Required application checks, using current scripts when they supersede these:

```sh
GOWORK=off go build ./...
GOWORK=off go test ./...
GOWORK=off go vet ./...
make guard
make guard-cli-selftest
make parity
```

`make parity` is present on the observed newer origin/main; use the current
equivalent if it moved. Verify public-module build resolution once with vendor
bypassed (`GOWORK=off go build -mod=mod ./...`) so a stale vendor tree cannot hide
bad pins. Run relevant race/real-PostgreSQL regressions for changed auth, role,
worker and transaction behavior. Keep `workshop/clicontract`'s real credentials,
route/gate/response-shape/documentation censuses and new `workshop/parity` intact;
do not loosen assertions solely to make the upgrade green. Do not change the
separate TS upstream-parity record to pretend it tracks this framework audit.

Use the current safe devstack workflow with explicitly local settings and
synthetic credentials. Boot the real server, drive the real CLI/HTTP surface,
and inspect actual responses and persisted rows. At minimum verify health,
human and machine whoami/permissions, forbidden and cross-organization reads,
role grant/revoke/regrant and global fallback, key lifecycle gates, list/search/
count/forward/backward boundaries, an ordinary domain CAS write, echo enqueue →
worker completion, dead-letter retry and drain. Include representative newer IO,
commission, historic-client and resource-review paths from the reconciled base.
Exercise malformed dotenv/config and canceled startup without leaking input.

Browser OAuth behavior needs a real browser/owned provider fixture when practical:
start and callback in the same cookie jar, missing/wrong proof, allowed-origin
login/refresh/logout, and legitimate completion after a rejected callback. Never
represent fake-provider or local HTTP success as a live Google/GCP check. This
repo is currently a Go API/CLI: run TypeScript/typecheck/frontend commands only if
fresh inventory actually finds an owned frontend and its lockfile/scripts.

Finish with exact versions, audit001–034 disposition counts, changed files,
appended migration mapping, what passed, what failed/skipped/was unavailable,
runtime evidence, the separate core/SQL follow-up and a concise operator rollout
checklist. A green compile alone is not completion. Do all available local work
before reporting any genuine external blocker; do not push, deploy or apply
migrations to shared/production data.
