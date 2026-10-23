# Retire the Firestore stores (connector + authentication adapter); keep Firebase Auth

**Status:** EXECUTED 2026-10-23 — PR-A = #153 MERGED @ afd3dc9f (deletions, CI, Makefile, G24 tombstone; the four A-4 proofs green); PR-B = #154 (`docs/firestore-retired`, built @ 75256b69: current-support docs, AUDIT-055, the RELEASING "Retired" block; docs build green); archive ref `archive/firestore-stores-2026-10-23` at f73e70a1; `live-stores` dispatched once after PR-A (two jobs, no input prompt; the postgres-turso job failed on the pre-existing `RotationKeepsExpiresAt` precision flake, filed in the S2 follow-ups plan); no tags; this file copied verbatim to `plans/retire-firestore-stores.md` by PR-B. History: RATIFIED 2026-10-23 (owner: "ratified on 1"; no review gate beyond the backend-lead consultation — the owner's call). The seven open rulings take the plan's recommendations: (1b) keep the two dated README records + one Retired paragraph; (2) the connector goes; (3) keep the Firebase Auth emulator references only; (4) tag PR-A's parent `archive/firestore-stores-2026-10-23`; (5) edit the sdk comment, leave the storetest R6 comments; (6) leave `examples/README.md:303`; (7) the R6-probe deletion is filed in the S2 follow-ups plan. Build started from `main` @ f73e70a1 (S2 PR-7 merged) in worktree `../gopernicus-retire-fs`. (Drafted 2026-10-23 from `main` @ 159be98a; backend-lead consultation folded the same day.)

**Owner direction (2026-10-23, verbatim intent):** "We can just retire the firestore adapters
all together. I don't want to support that anymore. I DO STILL WANT TO SUPPORT FIREBASE AUTH —
just not the firestore stores." The owner believed this had already happened; it had not — only
the *authorization* Firestore adapter was retired (AUDIT-040, commit 66d70c51, 2026-09-16). The
connector and the *authentication* Firestore adapter are still in the workspace, the Makefile,
`go.work`, CI and every current-support document.

## Context

Two Firestore modules remain: `integrations/datastores/firestore` (the connector,
`cloud.google.com/go/firestore`) and `pockets/authentication/stores/firestore` (the
authentication pocket's document-store adapter). They were always an **optional third family**
outside the DP1 `{turso, pgx}` parity bar (`pockets/README.md:211-222`,
`ARCHITECTURE.md:444-449`), published once under an owner-accepted "emulator verification only"
exception (RELEASING "Firestore — v0.1.0", line 2366), and never verified against real GCP. The
family carried two standing rulings — R1 (joins no ambient `transaction.Transactor`) and R6
(`errors.ErrUnsupported` stubs for the terminal-settlement methods) — plus a live-GCP CI job that
has never had credentials (`live-stores.yml:52-57`: the `FIRESTORE_LIVE_*` secrets do not exist).

No host mounts them. The only three `go.mod` files naming `firestore` are the two retired modules
and `integrations/authentication/firebase/go.mod`, whose `cloud.google.com/go/firestore v1.22.0 //
indirect` is a transitive dependency of `firebase.google.com/go/v4` — unrelated to the connector
and untouched by this plan. Nothing outside the two directories imports
`…/integrations/datastores/firestore` (grep-verified; the lead also checked every `go.mod` under
`/Users/jrazmi/code` — the only consumers are gopernicus worktrees). Firestore hosts were already
frozen at authentication ≤ v0.17.x because the published store `v0.2.0` does not compile against
v0.18.0 (RELEASING line 630-631; `pockets/authentication/README.md:775-779`).

This plan follows the AUDIT-040 precedent exactly (archived execution record:
`git show archive/plans-2026-10-20:plans/authorization-firestore-retirement.md`): delete the
modules and everything that exists only for them, in one deletion PR; bring every
*current-support* document to the new truth in one docs PR; leave every dated *record* (AUDIT
entries, RELEASING history, executed `plans/`) as written; cut **no tag** — the retired modules
are frozen at their last published versions.

**Kept, untouched:** `integrations/authentication/firebase` (Firebase Auth as an identity
provider — `integrations/authentication/firebase/v0.1.0`), its `-tags=emulator` leg
(`EMULATOR_TAG_MODULES`, `Makefile:32`), `make dev-firebase` / `dev-firebase-down`
(`Makefile:159-164`), and `workshop/dev/firebase/` (Dockerfile, docker-compose.yml,
`firebase.json` — which configures **only** the `auth` emulator on 9099; nothing under
`integrations/authentication/firebase/` names Firestore).

## Goal

`main` builds, tests and guards green with the two Firestore modules gone (47 modules, 33 guards),
every current-support document says Firestore stores are retired, Firebase Auth is provably
unaffected, and no module tag is cut.

## Definition of Done

- `integrations/datastores/firestore/` and `pockets/authentication/stores/firestore/` are deleted;
  `go.work` lists 47 modules; `make check` and `make vuln` are green with `FIRESTORE_*` unset.
- `live-stores.yml` has exactly two jobs (`postgres-turso`, `redis-pgxdb`), no `firestore_live_required`
  input, and the five `.github/scripts/*firestore*` helpers are gone; `python3 -B -m unittest discover
  -s .github/scripts` passes.
- `make dev-firebase` + `go test -tags=emulator ./...` in `integrations/authentication/firebase` still
  passes (the Firebase Auth proof — a real one now; see Design, "workspace versions").
- A repo-wide `grep -ri firestore` hits only the allowlist in task A-4 (records, `go.sum`/`go.work.sum`,
  the Firebase module, the G24 tombstone, the storetest R6 comments, and the retirement sentences this
  plan writes).
- `AUDIT.md` carries AUDIT-055 and `RELEASING.md` carries the "Retired" note naming the frozen tags.

## Out of scope

- Any change to `integrations/authentication/firebase`, `workshop/dev/firebase`, `sdk/capabilities/
  authentication`, or `examples/external-identity` (the Firebase Auth surface).
- A Firestore-to-SQL data migration tool. None shipped for authorization (AUDIT-040); none ships here.
- Retracting, deleting or re-tagging any published module version (`retract` directives included).
- Editing ratified or executed plan text: `.claude/plans/soc2-readiness/s2-audit-completeness.md`
  (its `…/stores/firestore v0.2.0 → v0.3.0` row, line 557, and the PR-2a firestore clauses at lines
  64-68, 352, 707, 718 are **superseded by the owner's ruling**, not rewritten — S2's own release
  record, PR-7, already states it: its RELEASING section says "Not tagged:
  `pockets/authentication/stores/firestore` (retired, below)" and item (l) "The firestore store
  adapters are retired (owner ruling 2026-10-23)"), `plans/external-identity-providers.md`,
  `plans/host-contract-reconciliation.md`, and every dated section of `RELEASING.md`/`AUDIT.md`.
- Deleting the R6 `ErrUnsupported` probes in `pockets/authentication/stores/storetest`
  (`invitation_lifecycle.go:152-170`). That makes `CancelAcceptance`/`ListActiveByResource` conformance
  mandatory — a behavior change in an exported conformance package, i.e. an authentication core MINOR
  with a RELEASING line. It does not fit a no-tag train; it is ruling 7 as a follow-up.
- Fixing the pre-existing, unrelated staleness in `README.md:123` ("twenty-three layering guards" —
  the true count is thirty-four today, thirty-three after this plan) and in
  `.github/scripts/authorization_cache_verify.py:25` (its `MODULES` tuple predates auth0/firebase/
  secrets/production-baseline). Both are noted for the owner; only the Firestore entries are touched.
- The pre-existing emulator failure `TestConstructorSkipsTheProbeWhenAskedTo` (the store never wired the
  `OAuth2`, `SessionManagement`, `ExternalSubjects` slots) — moot once the module is gone.
- `go work sync` (never run it — it rewrites unrelated modules). `go.work.sum` is never hand-edited;
  see Design for what the tooling appends.

## Inventory — what goes, what stays

Verified on `main` @ 159be98a. Every path is repo-root-relative. Line numbers are anchors for the
implementer to re-check, not instructions to cut by number — anchor every edit on the quoted text.

**Retire (delete):**

| Path | Why |
|---|---|
| `integrations/datastores/firestore/` (46 files incl. `firestoretest/`, `testdata/`) | the connector |
| `pockets/authentication/stores/firestore/` (76 files incl. `firestore.indexes.json`, `README.md`, `SCHEMA.md`) | the adapter |
| `.github/scripts/firestore_index_readiness.py`, `firestore_live_audit.py`, `test_firestore_index_readiness.py`, `test_firestore_live_audit.py`, `test_firestore_workflow.py` | exist only for the `firestore-live` / `firestore-emulator` jobs |
| `.github/workflows/live-stores.yml` from the `# Job C — the Firestore emulator suites` comment (line 200) to end of file (859): the `firestore-emulator` and `firestore-live` jobs; the `firestore_live_required` dispatch input (83-89); the Firestore header paragraphs (30-70). **Lines 195-198 belong to `redis-pgxdb`** (its `POSTGRES_TEST_DSN` and `run:`) and stay. | CI legs for the retired family |
| `Makefile` `LIVE_TAG_MODULES` (20-25) and its `check` loop (632); the two `test-stores` legs (148-153); the Firestore emulator comment lines (89-92, 94, 96-102, and the `FIRESTORE_*` half of 104); `guard-firestore-mediation` (G24, 371-**433** — line 433 is `done; exit $$fail` and must go with the block) from the aggregate (215) and `.PHONY` (42) | build/test/guard entries that enumerate the family |
| `.env.example:5-16` (`FIRESTORE_EMULATOR_HOST`, `FIRESTORE_PROJECT_ID` and the emulator comment) | env for the retired legs |

**Edit (current-support statements):** `go.work` (via `go work edit -dropuse`); `go.work.sum` (tool-appended
lines only); `Makefile:3,7,12,15-19,103,599`; `.github/workflows/check.yml:1-6`;
`.github/scripts/authorization_cache_verify.py:25`; `ARCHITECTURE.md:17,32,64,332,334,422,444-449`;
`pockets/README.md:211-222`; `README.md:11,18,33,122-124`; `SECURITY.md:76-91`; `sdk/README.md:112`;
`sdk/capabilities/transaction/transaction.go:22` (ruling 5a); `pockets/authentication/README.md:147-148,
229-231,244,3677-3679` (ruling 1); `pockets/authorization/README.md:28-29`;
`pockets/authorization/BENCHMARKS.md:558`; `workshop/documentation/docs/{project-status.md:16-17,
pockets/authentication.md:109-110,272,276-277,350, pockets/overview.md:14,55, pockets/authorization.md:328,
integrations/catalog.md:16,29, architecture/repository-layout.md:16,26, sdk/capabilities.md:24,302,311,
guides/testing.md:65, guides/production-posture.md:86,122, guides/persistence.md:128}`; `AUDIT.md` (append
AUDIT-055); `RELEASING.md` (the Retired note).

**Keep as records (no edit):** `AUDIT.md` lines 37-66 (coordinated-release table), AUDIT-034,
AUDIT-040 and every dated entry; every dated `RELEASING.md` section (2366-2465 "Firestore — v0.1.0",
630-631, 1142-1145, 2332, …); `plans/*.md`; `.claude/plans/soc2-readiness/*` (gitignored — `.gitignore:32`;
`control-map.md:25` already reads "RETIRED (firestore store, owner ruling 2026-10-23)", written by the
S2 session; nothing for this plan to do there); `pockets/authentication/README.md:164` (v0.11.1 patch
record) and `:771-779` (v0.18.0 cutover record); `go.sum` files; the stale
`cloud.google.com/go/firestore v1.21.0/go.mod` line in `go.work.sum`.

**Keep as live code (no edit):** `integrations/authentication/firebase/**`, `workshop/dev/firebase/**`,
`Makefile:28-32,105-110,155-164,634` (the Firebase Auth emulator comment, `EMULATOR_TAG_MODULES`,
`dev-firebase*`, the `-tags=emulator` vet loop), `examples/external-identity/**`,
`sdk/capabilities/authentication/**`, `pockets/authentication/stores/storetest/{storetest.go:260-261,
invitation_lifecycle.go:152-153}` (the R6 comments stay — ruling 5b), `examples/README.md:303` (ruling 6,
recommended leave), `.github/scripts/test_authorization_cache_verify.py:15,19` (uses
`FIRESTORE_EMULATOR_HOST` only as an arbitrary env name the scrubber must drop).

## Frozen tags (the proxy keeps them forever; nothing here retracts them)

```
integrations/datastores/firestore/v0.1.0-beta.1
integrations/datastores/firestore/v0.1.0            @ 10e5f97b  2026-09-13   (last)
pockets/authentication/stores/firestore/v0.1.0-beta.1
pockets/authentication/stores/firestore/v0.1.0
pockets/authentication/stores/firestore/v0.1.1
pockets/authentication/stores/firestore/v0.2.0     @ 6a399d1e  2026-09-18   (last)
pockets/authorization/stores/firestore/v0.1.0-beta.1 … v0.3.0              (already retired, AUDIT-040)
```

The workspace copy of `pockets/authentication/stores/firestore` is three untagged commits ahead of
`v0.2.0` (57800646 external_subjects, 8ad6d911 S8 toolchain, ce894e2d S2 PR-2a correlation ids); its
`go.mod` still pins `pockets/authentication v0.13.0` / `sdk v0.9.0` and compiles only through
`go.work`. S2 deliberately cut **no v0.3.0**; that untagged work is deleted with the module and
survives in git history at PR-A's parent commit. **Retired modules are frozen at their last tag; a
deletion gets no tag and no new tag will ever ship** (`RELEASING.md` "Retired" note, task B-4).

## Design

- **Two PRs, nothing refactored.** PR-A deletes; PR-B documents. Each is independently green and
  independently revertable. PR-A is independent of S2 PR-7 (no file overlap except append-only
  `go.work.sum`); PR-B branches from `main` **after both PR-A and PR-7 have merged** — not stacked —
  because PR-B prepends to `RELEASING.md` and PR-7 rewrites its top heading.
- **Workspace versions move; `go.work.sum` grows.** Dropping the two `use` lines changes the
  workspace's minimal-version selection for the Google family (lead-verified in a scratch
  workspace): `google.golang.org/api` v0.287.1 → v0.279.0, `cloud.google.com/go/firestore` v1.25.0 →
  v1.22.0 (Firebase's indirect), `longrunning` v1.2.0 → v1.0.0, `gax-go/v2` v2.23.0 → v2.22.0,
  `enterprise-certificate-proxy` v0.3.17 → v0.3.15, `genproto/googleapis/bytestream` to an older
  pseudo-version. `go list -m all` then appends about ten `/go.mod` hash lines to `go.work.sum` —
  **commit what the tool appends; never hand-edit; never `go work sync`**; the first `go list -m all`
  needs the network (`GOPROXY=off` fails on `genproto/googleapis/bytestream`). Upside: Firebase's
  workspace selection now equals what consumers get with `GOWORK=off`, so the A-4 emulator run is a real
  test rather than a formality; `vuln` is a required check and the versions change, so A-4 runs
  `make vuln` too (lead's dry run: firebase vet/test/`-tags=emulator`/`-tags=live`, gcs test, and
  govulncheck all green, same two uncalled advisories before and after).
- **Records are not rewritten.** The repo's convention (AUDIT header; AUDIT-040's "Existing tags and
  historical release/audit records remain") is that dated entries describe the capability as it was.
  A current-support sentence gets the new truth; a dated sentence stays. Where a current sentence
  links into a deleted directory (`pockets/authentication/README.md:231`, `ARCHITECTURE.md:334`), the
  link goes with the sentence.
- **Guard numbers are never reused.** G24 is retired with a tombstone comment where its block stood
  (after G23, before the G13 block), in the style of the G22 note at `Makefile:365-366`: one or two
  comment lines naming the target, its origin (firestore-stores C8), the date and AUDIT-055. Not kept
  as a dormant target — its `[ -d "$$d" ] || continue` would make it pass forever. The aggregate
  shrinks 34 → 33 and the prose counts in `Makefile:599`, `check.yml:2` follow.
- **Doctrine stays doctrine.** `ARCHITECTURE.md` loses the Firestore clauses outright (AUDIT-040 R2:
  "keep architecture doctrine free of progress reports"); the history lives in AUDIT-055 and the
  RELEASING note. The isolation-unit clause (`ARCHITECTURE.md:332,422`) keeps its example by swapping
  the client: `google.golang.org/api/option` beside `cloud.google.com/go/storage` in
  `integrations/filestorage/gcs` (verified: `gcs.go`, `signing.go` import `option`). Deleting
  `pockets/README.md:211-222` removes the only written rule for admitting a store family outside the
  DP1 bar (optional, host-demand, may refuse the ambient transaction loudly — R1); AUDIT-055 says so.
- **Firebase Auth is proven, not assumed.** PR-A's verify runs the `-tags=emulator` leg against
  `make dev-firebase` so the retirement cannot be reported done on `make check` alone.
- **Dependency hygiene:** no `go.mod` outside the deleted directories changes; no module repins; no
  `go mod tidy` anywhere (nothing required the modules).

## Tasks — the PR train

Model `opus` for every task (mechanical breadth across ~20 files, a Makefile rewrite and a 660-line
workflow cut is not a trivial lookup). Branch names in parentheses. Verify commands run from the repo root.

### PR-A — delete the modules and everything that exists only for them (`retire/firestore-stores`)

Branch from `main` now; independent of S2 PR-7.

#### task A-1: delete the two modules and the CI helper scripts; drop the `use` lines

- **depends_on:** []
- **model:** opus
- **files:** `integrations/datastores/firestore/**` (rm), `pockets/authentication/stores/firestore/**` (rm),
  `.github/scripts/firestore_index_readiness.py`, `.github/scripts/firestore_live_audit.py`,
  `.github/scripts/test_firestore_index_readiness.py`, `.github/scripts/test_firestore_live_audit.py`,
  `.github/scripts/test_firestore_workflow.py` (rm), `go.work` (two `use` lines), `go.work.sum`
  (tool-appended lines), `.github/scripts/authorization_cache_verify.py:25` (drop
  `'integrations/datastores/firestore'` and `'pockets/authentication/stores/firestore'` from `MODULES` —
  nothing else in that tuple).
- **verify:**
  ```sh
  test ! -d integrations/datastores/firestore && test ! -d pockets/authentication/stores/firestore
  go work edit -json | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["Use"]))'   # 47
  go list -m all >/dev/null          # network on; appends ~10 /go.mod lines to go.work.sum — commit them
  git diff --stat -- go.work go.work.sum   # go.work: -2 lines; go.work.sum: additions only
  python3 -B -m unittest discover -s .github/scripts
  ```
- **description:** `git rm -r` both directories and the five scripts. Drop the workspace entries with
  `go work edit -dropuse=./integrations/datastores/firestore -dropuse=./pockets/authentication/stores/firestore`
  (never bare `go work use`, which rewrites the `go` line). Trim the tuple. Leave
  `test_authorization_cache_verify.py` alone. Commit the `go.work.sum` lines the tooling appends; do not
  remove the stale `firestore v1.21.0/go.mod` line.

#### task A-2: Makefile — module lists, test-stores legs, emulator comments, retire G24

- **depends_on:** [A-1]
- **model:** opus
- **files:** `Makefile`
- **verify:**
  `sed -n '7p' Makefile | awk '{print NF-2}'` → `47`; the `guard:` aggregate `grep -o 'guard-[a-z-]*' |
  sort -u | wc -l` → `33`; `grep -n -i firestore Makefile` → only the G24 tombstone line(s);
  `make -n check >/dev/null && make guard`
- **description:** Line 3: "49 modules" → "47 modules". Line 7 `MODULES`: drop
  `integrations/datastores/firestore` and `pockets/authentication/stores/firestore`. Line 12
  `STORE_MODULES`: drop the firestore store. Lines 14-25: `INTEGRATION_TAG_MODULES = $(filter
  %/turso,$(STORE_MODULES)) pockets/authorization/stores/goredis`, reword its comment (no Firestore
  suites/connector), delete `LIVE_TAG_MODULES` and its comment entirely, and delete the
  `for m in $(LIVE_TAG_MODULES)` line in `check` (line 632). Keep lines 28-32 (`PROVIDER_LIVE_TAG_MODULES`,
  `EMULATOR_TAG_MODULES` — Firebase) and the `-tags=emulator` vet loop (634) verbatim. Lines 86-110:
  cut the Firestore sentences (89-92) and the Firestore emulator `docker run`/port lines (94, 96-102);
  **keep line 103** (`POSTGRES_TEST_DSN='…' \`) and end that command with `make test-stores` — only the
  `FIRESTORE_*` half of line 104 goes; keep the postgres line (95) and the Firebase Auth emulator lines
  (105-110). Lines 148-153: delete the two firestore `test-stores` legs. Lines 42 and 215: remove
  `guard-firestore-mediation`. Lines 371-**433**: replace the whole G24 block — comment, target, recipe,
  and the closing `done; exit $$fail` — with the tombstone (Design). Line 599: "thirty-four" →
  "thirty-three".

#### task A-3: CI — live-stores.yml loses the two Firestore jobs; check.yml header; .env.example

- **depends_on:** [A-1]
- **model:** opus
- **files:** `.github/workflows/live-stores.yml`, `.github/workflows/check.yml:1-6`, `.env.example:5-16`
- **verify:**
  ```sh
  grep -n -i -E 'firestore|gcloud|FIRESTORE' .github/workflows/live-stores.yml .github/workflows/check.yml .env.example   # no hits
  grep -n 'run: go test -race ./...' .github/workflows/live-stores.yml   # redis-pgxdb's last step still has its run:
  python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/live-stores.yml')); print(sorted(d['jobs']), d['on']['workflow_dispatch'])"
  #   → ['postgres-turso', 'redis-pgxdb'] None     (pyyaml in a scratch venv if absent)
  ```
  Then push the branch and confirm the `check` workflow is green; after merge, dispatch `live-stores`
  once and confirm it lists only the two jobs with no input prompt.
- **description:** In `live-stores.yml` delete from the `# Job C — the Firestore emulator suites`
  comment (line 200) through end of file — that is both the `firestore-emulator` job and the
  `firestore-live` job, including its `concurrency: group: firestore-live`, `always()` cleanup and
  `upload-artifact` steps. **Anchor on the comment text, not the number: lines 195-198 are the tail of
  `redis-pgxdb` (its `POSTGRES_TEST_DSN:` and `run: go test -race ./...`) and must survive.** Delete the
  `inputs:` block under `workflow_dispatch:` (83-89), leaving a bare `workflow_dispatch:`; delete the
  Firestore header paragraphs (30-70) and keep the Turso/postgres/redis prose, the "NOT testcontainers"
  and "DSN-host consistency" paragraphs intact. In `check.yml:1-6` reword to: thirty-three guards; the
  compile-only `-tags=integration` vet of the turso stores and the goredis store — no live-tag clause.
  In `.env.example` delete lines 5-16 (the Firestore emulator comment, `FIRESTORE_EMULATOR_HOST=`,
  `FIRESTORE_PROJECT_ID=gopernicus-test`). `actionlint` is not installed locally; the YAML load above,
  the push-time `check` run and one post-merge dispatch are the parse proof.

#### task A-4: prove the workspace, the vuln gate and Firebase Auth

- **depends_on:** [A-2, A-3]
- **model:** opus
- **files:** none (verification only; its output goes in the PR description)
- **verify:**
  ```sh
  cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus
  env -u POSTGRES_TEST_DSN -u TURSO_DATABASE_URL -u TURSO_AUTH_TOKEN \
      -u FIRESTORE_EMULATOR_HOST -u FIRESTORE_PROJECT_ID make check          # 47 modules, 33 guards, no -tags=integration,live line
  make vuln                                                                   # required check; the Google versions moved
  grep -rn -i firestore --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=build . \
    | grep -v -E '^\./(AUDIT|RELEASING)\.md|^\./plans/|^\./\.claude/|go\.sum|go\.work\.sum' \
    | grep -v -E '^\./integrations/authentication/firebase/' \
    | grep -v -E '^\./Makefile:[0-9]+:# .*G24' \
    | grep -v -E '^\./\.github/scripts/test_authorization_cache_verify\.py:(15|19):' \
    | grep -v -E '^\./pockets/authentication/stores/storetest/(storetest\.go:26[01]|invitation_lifecycle\.go:15[23]):' \
    | grep -v -E '^\./examples/README\.md:303:'
  #   PR-A expectation: ONLY the current-support doc lines PR-B rewrites (list them in the PR description
  #   under "PR-B"); nothing under .github/workflows, .env.example, go.work, or the Makefile recipe lines.
  make dev-firebase
  (cd integrations/authentication/firebase && FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099 FIREBASE_PROJECT_ID=demo-gopernicus go test -tags=emulator -count=1 ./...)
  make dev-firebase-down
  ```
- **description:** Run the hermetic gate with every Firestore and datastore env var unset, the vuln gate,
  the allowlisted grep, then the Firebase Auth emulator leg (Docker-gated) to prove the identity provider
  survives at its new workspace versions. Record all four outputs in the PR.

### PR-B — bring the current-support documents to the new truth (`docs/firestore-retired`)

Branch from `main` **after PR-A and S2 PR-7 have both merged** (PR-7 rewrites the top of `RELEASING.md`
and already carries the retirement sentence; PR-B anchors under it). Do not stack on PR-A.

#### task B-1: ARCHITECTURE.md, pockets/README.md, root README, SECURITY, sdk/README, the sdk port comment

- **depends_on:** [A-4]
- **model:** opus
- **files:** `ARCHITECTURE.md`, `pockets/README.md`, `README.md`, `SECURITY.md`, `sdk/README.md`,
  `sdk/capabilities/transaction/transaction.go:22`, and `examples/README.md:303` only if ruling 6 = edit
- **verify:** `grep -n -i firestore ARCHITECTURE.md pockets/README.md README.md SECURITY.md sdk/README.md
  sdk/capabilities/transaction/transaction.go` → no hits; `cd sdk && go build ./... && go vet ./...`;
  `make guard`; and if ruling 6 = edit: `cd workshop/gopernicus && go test ./internal/commands -run
  TestGuardListMatchesContract` (the README-vs-tool byte check, `guard_test.go:42`; it matches only the
  `| **Pn** | … |` rows, and line 303 is "P9 in full" prose, so the edit is safe).
- **description:** `ARCHITECTURE.md`: delete tree lines 17 and 32; line 64 "**Forty-nine modules
  today.**" → "Forty-seven"; in the taxonomy row 332 drop `datastores/firestore` from the examples and
  swap the isolation-unit parenthetical to "`google.golang.org/api/option` beside
  `cloud.google.com/go/storage`" (same swap in the prose at 422); in row 334 delete the index-manifest
  clause and the `authentication/stores/firestore` example; delete the Firestore/R1 sentences at 444-449
  (the DP1 sentence stays; line 779 already states Firestore is not an authorization store — leave it).
  `pockets/README.md:211-222`: delete the "OPTIONAL third family" bold sentence through the R1 clause,
  and drop the firestore clause from the env-gated list. `README.md`: heading 11 and line 122
  "forty-nine" → "forty-seven"; delete tree lines 18 and 33; line 124 drop `/ FIRESTORE_EMULATOR_HOST`.
  `SECURITY.md:76-91`: the CI-credentials paragraph becomes "uses repository secrets for the Turso
  playground only; no cloud service-account credential exists in CI" and the GCP key / Workload
  Identity Federation bullets go (they described only the retired `firestore-live` job).
  `sdk/README.md:112`: "SQL and Firestore connectors implement it" → "the SQL connectors implement it".
  `transaction.go:22` (ruling 5a): replace the Firestore sentence with the vendor-neutral "A document
  store may require reads before writes and not observe its pending writes." and **keep line 21**
  ("Isolation, read restrictions and cancellation depend on the connector.") — that leeway is the port
  contract for third-party implementations. Comment-only; rides the next sdk tag.

#### task B-2: pocket READMEs and the docs site

- **depends_on:** [A-4]
- **model:** opus
- **files:** `pockets/authentication/README.md`, `pockets/authorization/README.md`,
  `pockets/authorization/BENCHMARKS.md`, `workshop/documentation/docs/project-status.md`,
  `workshop/documentation/docs/pockets/authentication.md`, `workshop/documentation/docs/pockets/overview.md`,
  `workshop/documentation/docs/pockets/authorization.md`, `workshop/documentation/docs/integrations/catalog.md`,
  `workshop/documentation/docs/architecture/repository-layout.md`, `workshop/documentation/docs/sdk/capabilities.md`,
  `workshop/documentation/docs/guides/testing.md`, `workshop/documentation/docs/guides/production-posture.md`,
  `workshop/documentation/docs/guides/persistence.md`
- **verify:** `grep -rn -i firestore workshop/documentation/docs pockets/authorization/README.md
  pockets/authorization/BENCHMARKS.md` → only the two retirement sentences (`pockets/authorization/README.md`,
  docs `pockets/authorization.md:328`); `grep -n -i firestore pockets/authentication/README.md` → exactly the
  retained records (164, the v0.18.0 cutover bullet 771-779) plus the one Retired paragraph;
  `make docs-install && make docs-build` (pnpm typecheck + build; a dangling relative link fails the
  Docusaurus build). Then `make docs` and look at Pockets → Overview (durable-stores column reads
  "pgx, Turso") and Guides → Production posture (the family table has two rows).
- **description:** `pockets/authentication/README.md` — per ruling 1's recommendation: delete the
  current-posture sentences at 147-148, 229-231 (with its `stores/firestore/README.md` link), 244 (row
  becomes `stores/{pgx,turso}`), 3677-3679 ("Firestore has no mirror…"); add one paragraph under "Public
  packages and composition": *"Retired (2026-10-23): the Firestore store. Its last tag,
  `stores/firestore/v0.2.0`, stays on the proxy, compiles only against authentication ≤ v0.17.x, and
  **no further tag will ship**; hosts move to pgx or turso (no data migration tool is shipped)."* Keep
  164 and 771-779 verbatim (dated records; 779's "until a firestore store tag ships" is answered by the
  Retired paragraph). `pockets/authorization/README.md:28-29`: "The shared Firestore connector and
  authentication Firestore adapter remain separate supported modules" → "The Firestore connector and
  the authentication Firestore adapter were retired on 2026-10-23 (RELEASING, AUDIT-055)".
  `BENCHMARKS.md:558`: drop the "Authorization Firestore has…" sentence. Docs site:
  `project-status.md:16-17` (drop "a Firestore store for authentication" and "Firestore" from the
  database list); `pockets/authentication.md:109-110` (delete the sentence), `:272` (delete the "; the
  Firestore store module…" clause), `:276-277` (delete "Firestore has no external_subjects…"), `:350`
  ("PostgreSQL, Turso and the example memory store support it." — drop the Firestore clause);
  `pockets/overview.md:14` ("pgx, Turso"), `:55` ("a shipped `stores/goredis` mirror (authorization)
  where one exists"); `pockets/authorization.md:328` (keep "has been retired" — true; drop any
  "connector remains" clause if present); `integrations/catalog.md:16` (delete the row), `:29` (delete
  the sentence); `architecture/repository-layout.md:16,26` (`{pgxdb,turso}`, `{pgx,turso}`);
  `sdk/capabilities.md:24` ("pgx, Turso connectors"), `:302` (drop Firestore), `:311` (delete the
  Firestore sentence); `guides/testing.md:65` (delete the Firestore legs sentence);
  `guides/production-posture.md:86` (delete the firestore table row), `:122` (drop ", and by the host's
  IAM on firestore"); `guides/persistence.md:128` (drop ", and the Firestore emulator").

#### task B-3: the records — AUDIT-055 and the RELEASING "Retired" note

- **depends_on:** [B-1, B-2]
- **model:** opus
- **files:** `AUDIT.md` (append after AUDIT-054), `RELEASING.md` (new block directly under the title,
  **above** PR-7's "## Audit completeness and integrity (SOC 2 slice S2 — published 2026-10-23)"),
  `.claude/plans/retire-firestore-stores.md` (status line → EXECUTING / BUILT / MERGED with PR numbers
  and commits, as the s1 plan does — gitignored, local bookkeeping)
- **verify:** `grep -n 'AUDIT-055' AUDIT.md`; `sed -n '1,25p' RELEASING.md` shows the note above the S2
  section; the note names every frozen tag listed above, says "no tag", and names G24; then the A-4
  grep (same allowlist) hits only the records and the retirement sentences.
- **description:** AUDIT-055 "Retire the Firestore connector and the authentication Firestore
  adapter", modeled on AUDIT-040: Status implemented, unreleased (no tag ever); Modules
  `integrations/datastores/firestore` and `pockets/authentication/stores/firestore` removed from the
  workspace; Old behavior (authentication shipped pgx, Turso and an emulator-verified Firestore
  adapter; a shared Firestore connector; guard G24 mediated the adapter's vendor I/O; the pockets
  charter admitted an optional third family on host demand — R1); New behavior (authentication ships
  pgx and Turso; **G24 retired, number not reused**; the optional-third-family rule is withdrawn with
  its only member; Firebase Auth remains supported via `integrations/authentication/firebase`);
  Consumer guidance (the last tags stay published and frozen; `stores/firestore/v0.2.0` compiles only
  against authentication ≤ v0.17.x; move to pgx or turso; no data migration tool); Verification (the
  A-4 commands, the 47-module `make check`, `make vuln`, the Firebase emulator leg). RELEASING note,
  headed `## Retired: Firestore stores (no tags — owner ruling 2026-10-23)`: the frozen-tag list
  verbatim, "retired modules are frozen at their last published version; a deletion gets no tag and
  no retraction", a pointer to PR-7's S2 section item (l) for the S2-side statement (do **not** restate
  the supersession — PR-7 owns it), and a pointer to AUDIT-055. Do not touch any dated section.

## Sequencing

1. **PR-A** from `main` now. Independent of PR-7 (PR-7's diff is `RELEASING.md`,
   `examples/{auth-cms,production-baseline}/go.{mod,sum}` and a plans manifest; PR-A touches `go.work`,
   `go.work.sum` (appended lines), `Makefile`, `.github/**`, `.env.example` and the two trees). If both
   append to `go.work.sum`, merge both sides and rerun `go list -m all`.
2. **PR-7** (S2 release record) merges on its own schedule.
3. **PR-B** from `main` after 1 and 2 — not stacked; B-1/B-2 in parallel, B-3 last.
4. Post-merge: one `live-stores` dispatch; the optional archive ref (ruling 4).

## Release — tags: NONE

- **No module tag is cut.** A deletion gets no tag; retracting is not done; the frozen tags above stay
  resolvable on the proxy with their original contracts.
- **`go.work`:** loses `./integrations/datastores/firestore` and `./pockets/authentication/stores/firestore`
  (49 → 47 `use` entries, via `go work edit -dropuse`). **`go.work.sum`:** gains the `/go.mod` hash lines
  the tooling appends for the lowered Google-family selections (Design); committed as-is. No `go.mod`
  outside the deleted directories changes; no module repins; no `go work sync`.
- **Makefile:** `MODULES` 49 → 47; `STORE_MODULES` 12 → 11; `INTEGRATION_TAG_MODULES` loses the connector;
  `LIVE_TAG_MODULES` is deleted; guard aggregate 34 → 33 (G24 retired, number not reused).
- **CI:** `check.yml` runs `make check` with no matrix — nothing to edit beyond its header comment; the
  required checks are only `check` and `vuln` (`check.yml:14-20`), and `live-stores` is dispatch-only,
  so deleting its jobs cannot block a merge. `live-stores.yml` keeps `postgres-turso` and `redis-pgxdb`.
  The repo secrets `FIRESTORE_LIVE_*` never existed (`live-stores.yml:52-55`), so there is nothing to
  revoke; **owner action:** if a GCP service-account key was ever minted for this repo outside CI,
  delete it in IAM.
- **Optional archive ref (ruling 4):** `git tag archive/firestore-stores-2026-10-23 <PR-A parent>` so
  record links into the deleted trees resolve via
  `git show archive/firestore-stores-2026-10-23:pockets/authentication/stores/firestore/README.md`
  (precedent: `archive/plans-2026-10-20`). It is a plain repo ref, not a module tag.
- **Downstream:** no in-repo host changes. External hosts still on `stores/firestore/v0.2.0` (none
  known; the handoffs in `plans/` list it as "untouched / documented nil") keep building and get the
  AUDIT-055 guidance; they were already frozen at authentication ≤ v0.17.x.

## Risks

1. **Workspace version selection drops for the Google family** (Design). Firebase, gcs and the
   firebase `-tags=emulator`/`-tags=live` legs were dry-run green by the lead at the new selections,
   and govulncheck reports the same two uncalled advisories; A-4 re-runs `make vuln` and the emulator
   leg on the real branch. If a selection breaks a consumer, the fix is a repin in that consumer's
   `go.mod`, not a `go work sync`.
2. **Untagged S2 work is deleted.** ce894e2d touched `stores/firestore/securityevents.go` for the
   correlation ids; it was never tagged (S2 cut no v0.3.0) and goes with the module. Intended; recorded
   in AUDIT-055 and PR-7's item (l). Reversible with `git revert` of PR-A.
3. **Prose counts drift.** Four places state the module count (`Makefile:3`, `README.md:11,122`,
   `ARCHITECTURE.md:64`) and two the guard count (`Makefile:599`, `check.yml:2`); A-2/A-3/B-1 change all
   six, and A-4's `awk`/`wc` checks pin the real numbers. `README.md:123` ("twenty-three guards") is
   already wrong and is left for the owner (out of scope).
4. **`live-stores.yml` is cut by ~660 lines by hand with no local `actionlint`,** and the cut starts
   four lines after `redis-pgxdb`'s last `run:`. A-3 anchors on the `# Job C` comment, greps for the
   surviving `run: go test -race ./...`, loads the YAML, and the post-merge dispatch is the proof; the
   five deleted python helpers include the test that parsed this workflow, so no test will miss them.
5. **Makefile recipe boundary.** The G24 block ends with `done; exit $$fail` on line 433; leaving it
   behind makes it a recipe line of G23 and `make guard` fails with a shell syntax error. A-2's `make
   guard` catches it.

## Verification — how the owner proves it end to end

1. On the PR-A branch: `env -u FIRESTORE_EMULATOR_HOST -u FIRESTORE_PROJECT_ID -u POSTGRES_TEST_DSN
   -u TURSO_DATABASE_URL -u TURSO_AUTH_TOKEN make check` ends in "all checks passed" after printing
   `== vet -tags=emulator integrations/authentication/firebase ==` and **no** `-tags=integration,live` line;
   `make vuln` exits 0.
2. `make dev-firebase`, then the `-tags=emulator` leg in `integrations/authentication/firebase` passes
   (prints the emulator token cases, not a skip), then `make dev-firebase-down`.
3. `git ls-files | grep -i firestore` → empty; `git diff main -- go.work.sum` shows additions only.
4. After merge: dispatch `live-stores` once; the run shows two jobs, no input prompt.
5. On the PR-B branch: `make docs-build` green; the A-4 grep hits only the allowlisted records and the
   retirement sentences; `make guard` green.
6. Cold proof for the frozen family's consumers is not needed — no tag moved;
   `go list -m -versions github.com/gopernicus/gopernicus/pockets/authentication/stores/firestore`
   from any scratch module still lists `v0.1.0 v0.1.1 v0.2.0`.

## Decided (no ruling needed)

- Two PRs (deletion, docs), no tags, no retractions, no data-migration tooling — the AUDIT-040 shape.
- PR-A is independent of S2 PR-7; PR-B branches from `main` after both merge (not stacked).
- G24's number is retired, not reused; a tombstone comment marks it; AUDIT-055 names it.
- Dated records (AUDIT, RELEASING history, executed `plans/`, `.claude/plans/soc2-readiness/*`) are not
  edited; current-support sentences are. `control-map.md:25` is already done by the S2 session and is
  gitignored — nothing for this plan to do.
- `go.work` edits go through `go work edit -dropuse`; `go.work.sum` additions are committed as the tool
  writes them; `go work sync` is never run; no `go.mod` outside the deleted trees changes.
- Firebase Auth's emulator leg and `make vuln` are part of PR-A's verify, not assumptions.
- `test_authorization_cache_verify.py`'s use of `FIRESTORE_EMULATOR_HOST` as a sample env name stays.

## Open rulings — YOUR CALL

1. **`pockets/authentication/README.md`'s Firestore notes.** (a) delete every Firestore sentence, or
   (b) delete the current-posture sentences (147-148, 229-231, 244, 3677-3679) and keep the two dated
   records (164: the v0.11.1 patch; 771-779: the v0.18.0 cutover that froze firestore hosts at
   ≤ v0.17.x) plus **one** "Retired" paragraph naming the last tag and saying no further tag ships.
   **Recommend (b)** — the README is also the host migration guide and the ≤ v0.17.x freeze is the
   fact a stranded host needs.
2. **Does `integrations/datastores/firestore` go too?** Its only consumer in the tree was the
   authentication store (grep: no other Go importer; the authorization store was its other consumer
   until AUDIT-040; the lead found no consumer in any `go.mod` under `/Users/jrazmi/code` outside
   gopernicus worktrees). `integrations/authentication/firebase` does **not** use it (its indirect
   `cloud.google.com/go/firestore` comes from the Firebase Admin SDK). **Recommend yes** — a connector
   with no store is dead weight in `make check`, `vuln` and the docs.
3. **Emulator image references.** The Firestore emulator image (`gcr.io/google.com/cloudsdktool/
   google-cloud-cli:emulators`, pinned by digest at `live-stores.yml:241`, floating in the Makefile
   comment and `.env.example`) goes everywhere. The Firebase **Auth** emulator (`workshop/dev/firebase`,
   `gopernicus/firebase-auth-emulator:15.34.0`, `make dev-firebase`) stays verbatim. **Recommend keep
   Auth only** — it is the leg that proves Firebase Auth after this change.
4. **Archive ref.** Tag PR-A's parent as `archive/firestore-stores-2026-10-23` so the record links
   in `RELEASING.md`/`plans/` into the deleted trees stay resolvable by `git show`. **Recommend yes**
   (precedent `archive/plans-2026-10-20`; zero cost; not a module tag).
5. **Comment-only mentions in tagged modules — split.** (a) `sdk/capabilities/transaction/
   transaction.go:22`: edit to vendor-neutral wording, keep line 21 (B-1; comment-only, no tag).
   (b) `pockets/authentication/stores/storetest/{storetest.go:260-261, invitation_lifecycle.go:152-153}`:
   **leave** the "(the firestore stub, ruling R6)" comments. Rewording them to "an adapter answering
   `ErrUnsupported`" would turn a Firestore-only exemption into a general way out of
   `CancelAcceptance`/`ListActiveByResource` conformance, which the core relies on to settle
   invitations; nothing in the repo uses the exemption any more, and the accurate pointer until the
   probes are deleted is R6. Both lines are allowlisted in the A-4 grep. **Recommend as stated.**
6. **`examples/README.md:303`** ("the memory, goredis and firestore stores do not [ship migrations]").
   It is the ratified host contract, and the parenthetical stays true: P9 decides scope from a
   `migrations/` directory at run time, so a host pinned to the frozen `v0.2.0` is still out of scope.
   (a) leave it (smaller diff; allowlisted in the A-4 grep), or (b) drop the word in B-1 with
   `TestGuardListMatchesContract` as the proof. **Recommend (a).**
7. **Follow-up, not this train:** delete the R6 `ErrUnsupported` probes in
   `pockets/authentication/stores/storetest/invitation_lifecycle.go:152-170` so the terminal-settlement
   conformance cases are mandatory for every adapter. Behavior change in an exported conformance
   package → authentication core MINOR + RELEASING line. **Recommend filing it as its own small plan
   after PR-B merges.**

## Consultation notes

`lead-backend-engineer` (2026-10-23, read-only, scratch workspace at the session scratchpad; `git
status` unchanged). Verdict: ship with edits. All folded in above:

- `go.work.sum` is **not** untouched — dropping the two `use` lines lowers the workspace selection of
  the Google family and `go list -m all` appends ~10 `/go.mod` lines; commit them, never hand-edit.
  Firebase's workspace selection now equals `GOWORK=off`, so the emulator leg is a real test; add
  `make vuln` (required check; versions move). Dry run: firebase vet/test/tagged legs, gcs test and
  govulncheck green before and after.
- `live-stores.yml`: the cut is 200-EOF anchored on `# Job C`; 195-198 are `redis-pgxdb`'s tail.
- Makefile: G24 block is 371-**433** (`done; exit $$fail`); the live-tag loop is line **632**; keep
  line 103 and end it with `make test-stores`.
- Former task B-5 (control-map row 25) deleted: `.claude` is gitignored (`.gitignore:32`), and
  `control-map.md:25` already carries "RETIRED (firestore store, owner ruling 2026-10-23)"; the "Tamper
  evidence" row (106) never mentioned firestore.
- B-3 (was B-4) anchors above PR-7's S2 published heading and drops the supersession sentence — PR-7's
  item (l) already owns it; AUDIT-055 names G24 (otherwise the tombstone is its only record) and the
  withdrawn optional-family rule from `pockets/README.md`.
- A-1 uses `go work edit -dropuse` (bare `go work use` rewrites the `go` line and `2>/dev/null` hid it).
- `ARCHITECTURE.md:64` added to the module-count list (four places, not three).
- B-1's README proof is `TestGuardListMatchesContract` (`workshop/gopernicus/internal/commands/
  guard_test.go:42`), not G30; `examples/README.md:303` moved to ruling 6 (recommend leave).
- Ruling 5 split: edit the sdk line (keep line 21), leave the storetest R6 comments, file the
  probe-deletion follow-up (ruling 7).
- PR-A need not wait for PR-7; PR-B must follow PR-7 and should not be stacked on PR-A.
- Low priority: after PR-A, `.github/scripts/` holds only `authorization_cache_verify.py`, its test and
  fixture, and no workflow or Makefile target runs them (the only CI Python step was the deleted
  `test_firestore_*.py` discovery) — orphaned tool, flagged for the owner.

## Recommended reviews

- `product-manager` — scope discipline (two PRs, no refactor), the host-facing wording of the
  RELEASING note and AUDIT-055, rulings 1 and 6.
- `platform-sre` — the `live-stores.yml` cut, the `go.work.sum`/version-selection change and `make
  vuln`, the secrets/IAM statement in `SECURITY.md`, the count drift, the post-merge dispatch proof.
- `architecture-steward` — `ARCHITECTURE.md` edits (taxonomy rows 332/334, the DP1 paragraph, the
  isolation-unit example swap to gcs), G24's retirement, the withdrawn optional-family rule.

## Notes

- Precedent: AUDIT-040 (`AUDIT.md:4982`) and its archived execution record
  (`git show archive/plans-2026-10-20:plans/authorization-firestore-retirement.md`). This plan is that
  record applied to the remaining two modules, with the Firebase Auth proof and the vuln gate added.
- The S2 plan (`.claude/plans/soc2-readiness/s2-audit-completeness.md:557`) scheduled
  `stores/firestore v0.3.0` in the post-tag repin step; the owner's ruling supersedes that row. The plan
  text is ratified and is not edited here — PR-7's RELEASING section item (l) carries the supersession.
- `README.md:123` says "twenty-three layering guards"; the Makefile aggregate has thirty-four today.
  Pre-existing drift, unrelated to Firestore; flagged, not fixed.
- The `authorization_cache_verify.py` `MODULES` tuple is stale beyond Firestore (no auth0, firebase,
  secrets, production-baseline, external-identity entries), and nothing runs the script in CI. Only the
  two Firestore entries are removed; the orphaned tool is the owner's call.
