# Phase 8 — release, documentation, and coordination-hub adoption

## Outcome

Turn selected completed phases into immutable module releases with a cold-cache
resolution proof and an exact coordination-hub adoption manifest. No tag/push or
downstream edit is authorized by this plan alone.

## CHAU-8.1 — inventory and semver

Before proposing versions:

1. record `git status --short` and preserve unrelated/user changes;
2. diff each changed module from its latest tag;
3. inventory exported API, behavior, migration, template-output, go.mod, and
   documentation changes;
4. classify source compatibility separately from operator/config compatibility;
5. confirm pgx/Turso migration filename parity; and
6. update one keyed next-tag entry in `RELEASING.md` per module—never add
   contradictory parallel notes.

Likely changed modules:

- `sdk` for canonical runtime posture and transactional layout output;
- `features/authentication` for admin/status, resend, reset links, compatibility
  aliases, and optionally provisioning;
- `features/authentication/stores/pgx` and `/turso` for status migrations and
  atomic repository capabilities; and
- examples/docs, which are not tagged independently.

Expected version direction only:

- sdk minor from v0.3.x for new canonical public APIs; a logo-only release could
  be a patch, but do not split if runtime work rides the same commit;
- authentication minor from v0.2.x for the new lifecycle/admin surface and new
  production reset configuration;
- store-module minor from v0.1.0 because new migrations/ports are adopter-visible;
  and
- a later authentication/store minor for provision-on-consumption if the two
  release trains remain separate.

Freeze exact tags only after the final diff. Authentication go.mod must require
the sdk tag that supplies canonical runtime APIs; store modules must require the
authentication tag whose ports they implement. Push dependency tags first.

## CHAU-8.2 — verification matrix

Minimum hermetic gate:

```sh
(cd sdk && go build ./... && go test -race -count=1 ./... && go vet ./...)
(cd features/authentication && go build ./... && go test -race -count=1 ./... && go vet ./...)
(cd features/authentication/stores/pgx && go test -race -count=1 ./...)
(cd features/authentication/stores/turso && go test -race -count=1 ./... && go vet -tags=integration ./...)
(cd examples/auth-cms && go test -race -count=1 ./...)
make check && make guard
```

Required live gates for lifecycle/provisioning:

- pgx and Turso shared conformance;
- repeated deactivate-versus-session-mint race;
- repeated registration-versus-provision/redeem race;
- rollback/retry at transaction failure boundaries;
- jobs-mode resend/replacement/stale-claim proof;
- jobs-mode reset link restart/checkpoint proof; and
- a real-browser admin/resend/reset/link/provision flow for the phases selected.

Record loud skips verbatim. A live-store workflow failure in untouched code is an
owner disposition with evidence, not something silently labeled green.

Documentation gate:

- exported comments compile and match behavior;
- README route/config/security/migration sections are internally consistent;
- store migration counts and source lists match files;
- every new production-required field appears in examples and upgrade notes;
- compatibility aliases have a removal/deprecation posture; and
- downstream snippets use only exported APIs and tagged module paths.

## CHAU-8.3 — owner-cut manifest and cold resolution

Prepare a manifest containing:

- commit SHA and clean/known-dirty status;
- module → tag → bump rationale;
- dependency/tag push order;
- exact owner commands clearly marked **DO NOT RUN BY THE PLANNING TASK**;
- pre-tag gate evidence;
- migration export/copy/apply order;
- configuration changes and compatibility aliases; and
- post-tag cold resolution commands.

After the owner cuts/pushes tags, verify each outside the workspace:

```sh
cd "$(mktemp -d)"
go mod init scratch
GOWORK=off GOFLAGS=-mod=mod go get github.com/gopernicus/gopernicus/<module>@<tag>
GOWORK=off go build ./...
```

Authentication cold resolution must select the intended sdk version; each store
must select the intended authentication version. Do not rely on `go.work` or
local replaces as release evidence.

## CHAU-8.4 — coordination-hub adoption record

Write an exact downstream checklist for only the phases released:

1. copy/apply new authentication store migrations before deploying binaries;
2. update module versions, tidy, vendor, and rerun dependency/license guards;
3. switch app-wide mail code from auth runtime vocabulary to sdk foundation/
   email helpers;
4. configure `PasswordResetURL` and validate production HTTPS;
5. wire `UserAdminCheck` to the existing `platform:main#admin` decision without
   importing authorization into auth core;
6. mount/use admin list, deactivate/reactivate, and admin resend routes;
7. add the public verification-resend UI path;
8. remove no OAuth-link workaround—document/use the already-shipped link/start
   route and `/auth/methods` inventory;
9. enable provision-on-consumption only after the second train is adopted and its
   threat-model checklist is accepted; and
10. update coordination-hub's upstream-flags rows with tag/evidence and exact
    interim-removal conditions.

The record must call out the logo nuance: coordination-hub's app layout override
may make the sdk transactional fix a no-op locally, but the upstream defect is
still closed for default-layout adopters.

## Final handoff

The final report names:

- files/modules changed;
- tests/live gates and skips;
- migrations/config/operator actions;
- tags cut by the owner (or still pending);
- coordination-hub rows resolved, corrected, or deferred; and
- any remaining security/race unknowns.

## Execution log

Append only. Record CHAU-8.x module diffs, exact gates/skips, owner-cut tags,
cold-resolution output, downstream adoption, and final flag dispositions.

### 2026-08-16 — CHAU-8.1 … CHAU-8.4 complete; NO TAG CUT, NO PUSH, NO DOWNSTREAM EDIT

**CHAU-8.1 — inventory and semver.** `git status --short` recorded: **108
entries** (50 modified, 56 untracked, 2 deleted). The two deletions
(`plan.md`, `tag-manifest.md`) are the OWNER's — they were removed when the
ten-file packet replaced the single plan file — and were preserved, except that
`tag-manifest.md` was restored with this batch's content because `RELEASING.md`
links to that exact path. The prior batch's evidence remains recoverable at
`git show 9b73785:.claude/plans/coordination-hub-auth-upstream/tag-manifest.md`,
and the manifest says so.

Per-module diff from each latest tag:

| module | from | tracked diff | uncommitted entries | proposed |
|---|---|---|---|---|
| `sdk` | `v0.3.1` | 6 files, +127/−15 | 15 | **v0.4.0** |
| `features/authentication` | `v0.2.2` | 33 files, +2524/−182 | 58 | **v0.3.0** |
| `features/authentication/stores/pgx` | `v0.1.0` | 8 files, +185/−37 | 11 | **v0.2.0** |
| `features/authentication/stores/turso` | `v0.1.0` | 6 files, +138/−34 | 9 | **v0.2.0** |
| `integrations/datastores/pgxdb` | `v0.3.0` | 2 files, +45/−2 | 3 | **v0.4.0** |
| `integrations/datastores/turso` | `v0.2.0` | 2 files, +43/−2 | 3 | **v0.3.0** |
| `integrations/email/sendgrid` | `v0.2.0` | — | 1 (a TEST) | **no tag** |

sendgrid's only change is `posture_test.go`; nothing importable moved, so it does
not retag. `examples/*` are never tagged.

Source compatibility and operator compatibility were classified SEPARATELY, as
the plan requires. Everything is source-compatible (no removal, no rename; the
`RuntimeMode` alias is assignable both ways; two struct-field additions would
break only an exhaustive positional literal, of which there are none in-repo; two
error MESSAGES changed while `errors.Is` did not). Two things are
operator-incompatible without action: migrations `0014`/`0015` must precede the
new binaries, and `PasswordResetURL` becomes production-required.

**Migration filename parity verified by `diff`: 15 files per dialect, identical
sets.**

`RELEASING.md` carries ONE keyed entry per module per change, with no
contradictory parallel notes: the sdk minor (runtime posture + capability checks +
crud search), the sdk patch-floor logo entry marked not-to-be-split, the
authentication entries for lifecycle / resend / reset links / the RuntimeMode
alias, the store entries, and a separately-headed **SECOND TRAIN** entry for
provisioning.

**CHAU-8.2 — verification matrix. Full hermetic gate: ALL PASS.**

```
sdk / authentication / stores{pgx,turso} / pgxdb / turso / sendgrid / examples/auth-cms
   build + go test -race -count=1 + vet   → PASS
(stores/turso additionally: go vet -tags=integration ./...) → PASS
make check (nineteen guards + per-module vet) → all checks passed
```

Every other workspace module also builds clean
(`features/{authorization,cms,events,jobs}`, `ui/goth`,
`examples/{cms,minimal,jobs-minimal}`, `workshop/gopernicus`).

**Required live gates — BOTH DIALECTS CLOSED.** Postgres ran in throwaway
containers created and removed per run; the user's existing
`coordination-hub-postgres` and `venona-*` containers were deliberately NOT used
because the harness truncates tables. Turso ran against the authorized playground
URL, verified to match the recorded safe-to-wipe value before any destructive run.

| required gate | pgx | turso |
|---|---|---|
| shared conformance | PASS (55.1s full suite) | PASS |
| repeated deactivate-vs-mint race | PASS (`-count=5` × 12 rounds) | PASS |
| repeated registration-vs-provision/redeem race | PASS (`-count=5` × 8 rounds) | PASS |
| rollback/retry at transaction boundaries | PASS (rejection cases assert nothing written) | PASS |
| migrations apply | PASS | PASS (`c80035ba`, `b7789bfc`) |
| list-search cross-dialect oracle | PASS | PASS |
| collation catalog (`users.id` = `C`) | PASS | n/a (SQLite BINARY) |

Jobs-mode resend/replacement and reset-link checkpoint proofs ran as part of the
existing `jobs_delivery_*` suites in the same package as the new tests, all green.

**Gates deliberately NOT run, recorded as open rather than papered over:**

1. `POSTGRES_NON_C_TEST_DSN` was unset, so `TestCollationControlsOrdering_NonC`
   **SKIPPED**. The catalog check proving `users.id` reports collation `C` DID
   run and passed, so the new pin is verified; the unverified part is the
   belt-and-braces ordering demonstration on a locale database.
2. Live concurrency repetition is `-count=5` (pgx) / `-count=1` (Turso) rather
   than the suggested `-count=10`; each Turso round costs ~8s of network
   round-trips. Executed totals are 60/40 races on pgx and 12/8 on Turso.
3. No headless browser was driven. Every user-facing flow IS proven over real
   HTTP through exported host seams with a real cookie jar — admin lifecycle,
   resend, reset-link redemption, OAuth link/unlink, provisioning — but a
   browser-level check is an owner call.

**One environment failure recorded verbatim:** the first Turso conformance run
failed on `checksum mismatch: migration default:0001_users.sql was modified after
being applied` — a stale ledger row from an older auth cut on that shared
playground database. The integration schema probe's own drop-and-remigrate reset
cleared it. No canonical migration file was edited to make a test pass.

**Documentation gate:** exported comments reviewed against behavior; five new
README sections internally consistent with the route table, config matrix,
repositories table, and migration section; store migration counts and file lists
match (15/15, byte-identical names); the new production-required field appears in
the example host and the upgrade notes; every compatibility alias carries a
removal posture; downstream snippets use exported APIs and tagged paths only.

**CHAU-8.3 — owner-cut manifest.** `tag-manifest.md`: commit state, the
module→tag→rationale table, the source-vs-operator compatibility split, the
two-train decision with the exact file set to separate if the owner splits them,
the dependency-ordered tag commands marked **DO NOT RUN BY THE PLANNING TASK**,
the go.mod pin table (every module still pins v0.1.0-era siblings that `go.work`
masks locally), the full gate evidence including the skips above, migration
export/apply ordering, and the post-tag `GOWORK=off` cold-resolution protocol.

**No tag was created. Nothing was pushed. No commit was made.**

**CHAU-8.4 — coordination-hub adoption record.** `adoption-checklist.md`: the
migrations-before-binaries ordering with the pgx ACCESS EXCLUSIVE warning, the
`go get`/tidy/vendor/guard step, the app-wide mail migration table that lets the
hub delete its `features/authentication` import, `PasswordResetURL` configuration
plus the SPA landing-page responsibilities, the `platform:main#admin`
`UserAdminCheck` wiring with the last-admin decision called out, the admin route
table with the two UI notes (bounded stateless-JWT revocation; `changed:false` is
success), the public resend path with copy guidance and the explicit warning not
to swap the public and admin surfaces, the OAuth flag closed as documentation
with the two client notes, the search adoption, the default-off provisioning
posture, the full flag-disposition table, and the interim removals.

**The logo nuance is called out explicitly**, as the plan requires: the hub
overrides the transactional layout at `LayerApp`, so the sdk fix is a **local
no-op** for them — the upstream defect is still closed for default-layout
adopters, and the fix starts mattering the day they drop the override.

**Two rows are marked OUT OF SCOPE rather than silently dispatched:** the
authorization resolver/role-middleware flags, and the filestorage row — which the
overview explicitly warns must not be dispatched verbatim because this checkout
now contains `sdk/capabilities/filestorage` and the row needs a fresh version
audit.
