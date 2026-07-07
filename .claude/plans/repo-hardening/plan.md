# repo-hardening — git + remote + CI + D8 module-path finalization + first tags

Status: **RATIFIED 2026-07-07 (jrazmi)** — rulings RH1–RH6 recorded in the
YOUR CALLS section below and in NOTES.md ("2026-07-07 — planning wave
RATIFIED" entry). Review gate ran 2026-07-07 (platform-sre /
product-manager): both RATIFY WITH AMENDMENTS, amendments applied.
Execution order per the ratification: **phases 1–3 first — everything into
git before more code lands; hygiene gates before any push.** Phase 5 is
double-gated: events-v1 close (RH5) AND a LICENSE landing (RH6 deferral).

Executor model policy (jrazmi, standing since jobs-v1): implementation tasks
on `model: opus`; design/doc-judgment tasks on `model: fable`. Never sonnet.

## Context

The repo is **not a git repository** — that is the central fact this milestone
fixes. The consequences are already visible in the record: `make check`'s
templ-drift gate runs on a checksum fallback precisely because there is no
`.git` (Makefile comment, `check` target); two milestones (kvstore-
consolidation, fast-follows) closed with "live-store legs not run, no creds";
and RELEASING.md's preconditions to cutting any tag (final module paths,
replaces dropped) sit unmet with no remote to tag against. The 2026-07-06
NOTES.md entry opened this planning wave with repo-hardening as item (2).

One reconnaissance fact reframes D8: **module paths are already
`github.com/gopernicus/gopernicus/*`** (all 26 go.mod files; the Makefile
guards G1–G3 grep that prefix; the bare-`gopernicus/` era is what guard G4
polices). So the "D8 rename" is conditional — if jrazmi's ratified remote is
exactly `github.com/gopernicus/gopernicus`, phase 4 collapses to a
verification pass; any other owner/repo choice makes it the full mechanical
rewrite (189 `.go` files, 26 `go.mod`, 6 `.templ` sources, Makefile guards,
6 live docs — counts as of 2026-07-07; execution re-derives them from the
then-current tree). **RATIFIED: RH1 chose exactly that remote
(`github.com/gopernicus/gopernicus`, public) — phase 4 IS the verification
pass; the rewrite branch is recorded at task-7 as not-taken.**

## Goal

The repo is a pushed GitHub repository whose `main` is protected behind a
green `make check` CI gate, whose module paths match the remote, and whose
importable modules carry `v0.1.0` tags that a consumer outside the workspace
can actually `go get`.

## Definition of Done

- Initial commit pushed to a remote, with a verified-clean secrets sweep
  BEFORE the first `git add` (`.env` files with real Turso tokens exist today
  at `/.env` and `/examples/cms/.env` — neither may enter history).
- The required CI gate (`make check`: templ drift in git mode, every module
  in the then-current Makefile `MODULES` set vet/build/test, four guards)
  observed **green on the remote**, not just YAML written.
- The commit carrying the first tags carries a `LICENSE` file (RH6:
  deferred, gate STANDS) — tags without one are all-rights-reserved and
  legally un-adoptable no matter how cleanly `go get` resolves.
- The live-legs workflow (postgres + redis service containers; secret-gated
  turso) exists, is NOT required for merge, and one manual dispatch has been
  observed green for the creds it has.
- Module paths final per the ratified remote; `make check` green; RELEASING.md
  precondition 1 checked off.
- First `v0.1.0` tags cut per RH5 (the four-module vertical slice — sdk,
  features/cms, integrations/datastores/turso, features/cms/stores/turso;
  timing: after events-v1 close) with relative `replace` directives dropped
  and requires pinned in the tagged modules (RELEASING.md precondition 2); a
  probe module outside the workspace resolves and compiles against the tags.
- Branch protection on `main` requiring the check.

## Out of scope

- **testcontainers integration-test harness** — ratified workshop-v2 scope.
  CI uses GitHub Actions service containers + repo secrets only.
- gcs / s3 / sendgrid live legs in CI — need cloud creds or fake servers;
  their tests keep skipping loudly (sendgrid is hermetic-only by design).
- Automated release/tagging workflow and changelog convention — RELEASING.md
  explicitly defers both; tags stay hand-cut this milestone.
- History backfill or synthetic staged history — history starts at the
  initial commit.
- Any code change beyond what rename/CI/tagging mechanically require
  (surgical-diff rule).
- `v1.0.0` for anything — first tags are `v0.1.0` per RELEASING.md's
  deliberate-v1 note.

## Schema / datastore impact

None. No SQL, no migrations, no store behavior changes. Migration source
names are untouched (the kvstore-consolidation hard constraint is not in
play — module-path renames never touch `MigrationSource.Name` strings, but
task-7's verify greps confirm it anyway).

## Module / API impact

- **No exported-symbol changes anywhere.**
- Phase 4 (resolved by RH1: verification pass, zero file changes). The
  conditional rewrite branch — prefix rewrite across every `go.mod`, all
  import paths, Makefile guards G1–G3, live docs — was not taken. `go.work`
  untouched either way.
- Phase 5 (slice per RH5): relative `replace` directives in the tagged
  modules' `go.mod` files (`features/cms`, `integrations/datastores/turso`,
  `features/cms/stores/turso`) are dropped and replaced with requires pinned
  to tagged versions (RELEASING.md precondition 2). Untagged modules and
  examples keep their replaces — a documented "not yet released" state.
  Local dev is unaffected — `go.work` overrides requires in the workspace.
- Tagging scheme per RELEASING.md: directory-prefixed nested-module tags
  (`sdk/v0.1.0`, `features/cms/stores/turso/v0.1.0`, …). Examples are
  demonstrations, not libraries — never tagged.

## Generated-artifact impact

No generated files change in this milestone — the phase-4 rename branch
that would have touched `*_templ.go` (via `.templ` sources + `make
generate`, never hand-edits) was not taken (RH1 org-match). What remains:
once `.git` exists the Makefile `check` target auto-upgrades templ-drift
from the checksum fallback to `git diff --exit-code -- '*_templ.go'` —
phase 2 verifies that branch actually executes.

## Risks

1. **A secret reaches the initial commit.** `/.env` and `/examples/cms/.env`
   carry real `TURSO_AUTH_TOKEN` values (Turso tokens are JWTs — `eyJ…`).
   Git history is forever once pushed. Mitigation: phase 1 is a hard gate
   before any `git add`; task-3 re-verifies against the actual index
   (`git check-ignore`, token-shape `git grep` of staged content) before the
   first commit, and again before push.
2. **Tag-ordering/proxy failure.** Dropping a `replace` makes `go mod tidy`
   fetch the sibling from the network (tidy ignores `go.work`), so each tag
   layer must be pushed and fetchable before the next layer tidies;
   proxy.golang.org lags AND caches 404s (a mistimed fetch before the tag
   push poisons the version). ~~A private repo needs `GOPRIVATE` + git
   auth~~ — not applicable per RH1 (public). Mitigation: layered tagging
   (tasks 8→9→10), `GOPROXY=direct` for every just-cut tag, and the whole
   dance run `GOWORK=off`.
3. ~~**D8 collides with a code milestone in flight.**~~ **Erased by RH1
   (org-match):** task-7 is a read-only verification pass; no quiet window
   exists and no coordination constraint applies to events-v1 or
   telemetry-closeout (NOTES.md ratification entry). Kept as record — the
   generalized quiet-window rule would have applied only on the not-taken
   rename branch.

## YOUR CALLS — all RATIFIED 2026-07-07 (jrazmi; NOTES.md "planning wave RATIFIED" entry)

1. **RULING RH1: `github.com/gopernicus/gopernicus`, PUBLIC.** Phase 4 is
   definitively the verification pass; RELEASING.md already reads
   correctly. Ruled **jointly with RH2 per the cross-link** — jrazmi
   consciously confirmed that public visibility plus the tracked `.claude/`
   set makes the planning corpus (NOTES.md, every plan under
   `.claude/plans/` and `.claude/past/`, the agent roster, the playground
   URL) world-readable by design. *(Record of the decision as posed: org
   vs personal owner decided rewrite-vs-checkbox for phase 4; public vs
   private decided the proxy/`go get` path and the secret-scanning
   posture. `gh` is authenticated as `jrazmi`, repo + workflow scopes.)*
2. **RULING RH2: track all four** — `NOTES.md`, `.claude/plans/`,
   `.claude/past/` (closed-milestone plans, moved 2026-07-07; mapping at
   `.claude/past/README.md`), `.claude/agents/`. World-readability under
   RH1-public consciously confirmed (see RH1). Sub-decision as
   recommended: commit `go.work`; **ignore `go.work.sum` by exact name**
   (the per-module `go.sum` files are the authoritative consumer record).
3. **RULING RH3: commit the playground Turso URL as-is.** It is a hostname,
   not a credential — access requires the auth token, which lives only in
   the gitignored `.env` files; the token-shape sweep found nothing
   adjacent (URL appears in NOTES.md ×1, `.claude/plans/events-v1/plan.md`,
   and 5 files under `.claude/past/`). The URL is load-bearing in the
   decision log (the truncation authorization is scoped to that exact URL);
   the escape hatch if exposure ever bothers is rotating/retiring the
   playground DB, never redacting history.
4. **RULING RH4: CI bundle as amended.** (a) GitHub Actions on
   `ubuntu-latest`; (b) live-legs workflow on postgres:17 + redis:7
   **service containers** (CI infra, not the testcontainers harness),
   triggered by **manual `workflow_dispatch` only — no schedule** (the
   PM-flipped default stands: the value is the button, pressed before a
   tag or after a store change); (c) playground Turso creds stored as repo
   secrets (`TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN`) so the turso
   conformance leg runs in CI — the secret pins exactly the authorized
   URL, honoring the standing destructive-run rule.
5. **RULING RH5: vertical-slice first tags; tag timing = WAIT for
   events-v1 close.** The slice: `sdk` → `features/cms` +
   `integrations/datastores/turso` → `features/cms/stores/turso` (4
   modules exercising all three tag layers, yielding a consumer-buildable
   app). The untagged remainder of the importable set stays replaces-in,
   documented "not yet released". Timing ruling adopts the PM
   recommendation: no pre-events v0.1.0 — first tags are cut coherent,
   after `features/cms` has its `Mount.Events` emitter. *(Rationale of
   record: the layer-3 dance is learned on the smallest slice; a
   coordinated v0.1.0 across the whole importable set signs a pre-1.0
   framework up for that many release cascades with no consumer waiting.
   Tasks 9–10 keep the full-sweep variant text for a future sweep.)*
6. **RULING RH6: LICENSE DEFERRED.** The repo goes public
   **source-visible but all-rights-reserved** — no license file lands with
   the initial commit. **The hard gate STANDS, unsoftened:** task-8 and
   all of phase 5 remain blocked until a `LICENSE` lands in the tree and
   the tagged commit carries it (DoD line unchanged). The deferral is the
   ruling, not a waiver — cutting tags without a license would strand the
   milestone's headline value (legally un-adoptable modules), so phase 5
   now has two independent gates: events-v1 close (RH5) AND LICENSE (RH6).

## Tasks

### Phase 1 — pre-commit hygiene sweep (no git dependency)

### task-1: secrets sweep + artifact cleanup

- **depends_on:** []
- **model:** opus
- **files:** [deletions only: `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/cms/server`, `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/examples/jobs-minimal/server`]
- **verify:** authoritative pass condition (SRE-corrected — the first draft's expected output was factually wrong): the full sweep regex `eyJ[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN.*PRIVATE KEY|authToken=[A-Za-z0-9]` must return **zero hits outside the `.env` files** across every candidate-tracked file, and the JWT-shape count (`eyJ[A-Za-z0-9_-]{20,}`) must be **0 in every candidate-tracked file** (the definitive index-layer form, `git grep --cached` of the same regex, runs at task-3 once an index exists). Two expected artifacts, named so no executor "fixes" the regex to dodge them: (a) THIS PLAN FILE self-matches once — it quotes the `-----BEGIN.*PRIVATE KEY` pattern in this very block — exclude `.claude/plans/repo-hardening/plan.md` or accept the one known self-match; (b) the `?authToken=` DSN-mechanic doc/code mentions (NOTES.md, `integrations/datastores/turso/turso.go`) are followed by a quote character and are deliberately NOT matched by `authToken=[A-Za-z0-9]` — the pattern targets literal inline tokens; do not widen or narrow it. Also: `find . -name '.env*'` returns exactly `./.env`, `./.env.example`, `./examples/cms/.env`; the two `server` binaries are deleted.
- **description:** Sweep every candidate-tracked file (including `NOTES.md`, `.claude/plans/`, `.claude/past/`, `.claude/agents/`, all READMEs, all `.go`/`.mod`/`.sql`) for token shapes: JWTs (`eyJ…` — Turso auth tokens are JWTs), AWS keys, private-key blocks, inline DSN credentials, SMTP passwords. This is a **filesystem-mode** sweep (there is no history to mine — one initial commit); pay specific attention to the credential-adjacent integrations (`integrations/{email/sendgrid, filestorage/gcs, filestorage/s3, oauth/github, oauth/google, cryptids/golang-jwt}`), `examples/*/workshop`, and any testdata carrying baked `POSTGRES_TEST_DSN`/`TURSO_*`/`REDIS_TEST_ADDR` values. Also sweep for stray local `*.db`/`*.db-*` artifacts the turso local-file legs may have left. Confirm the playground URL occurrences (NOTES.md ×1, `.claude/plans/events-v1/plan.md`, 5 files under `.claude/past/`) have no tokens adjacent — this is the evidence base RH3 was ruled on; re-confirm it holds at execution. Delete the two stale built binaries. Report findings verbatim in the execution log; any literal secret found outside `.env` STOPS the milestone until resolved. (SRE independently re-ran the sweep at review: the only literal JWTs in the tree are in the two gitignored `.env` files.)

### task-2: .gitignore redesign

- **depends_on:** [task-1]
- **model:** opus
- **files:** [`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.gitignore` (no `LICENSE` here — RH6 deferred it; it lands in a later commit, gated before task-8)]
- **verify:** review-only at this phase (no `.git` yet); hard verification is task-3's `git check-ignore` gate. Confirm by inspection: every pattern below present, stale entries gone.
- **description:** Replace the stale single-module-era `.gitignore` (`/server`, `/cmd/server/server` are root-anchored and do NOT match today's `examples/*/server` binaries). New content: `.env` (unanchored — covers root and `examples/cms`), `examples/*/server`, `*.db`, `*.db-*`, `.DS_Store`, `media-data/`, and per RH2 `go.work.sum` **by exact name** — NEVER a `*.sum` glob, which would silently drop every per-module `go.sum`, the exact records consumers rely on to verify these modules (security-relevant mistake, called out by the lead consult). Deliberately tracked per RH2: `.env.example`, `go.work` (the workspace is the documented dev workflow; Makefile and docs assume it), all per-module `go.sum`, `NOTES.md`, `.claude/plans/`, `.claude/past/`, `.claude/agents/`. Per RH6 the `LICENSE` is deferred — it lands in a follow-up commit whenever ruled, but never later than task-8 (the DoD gate stands).

### Phase 2 — git init + initial commit + remote

### task-3: git init + verified-clean initial commit

- **depends_on:** [task-2 — RH1–RH3 are ratified; nothing else blocks]
- **model:** opus
- **files:** [new `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.git/` (repo metadata); one comment line in `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/Makefile` (`check` target's "this repo is not a git repo" note, now false)]
- **verify:** `git check-ignore .env examples/cms/.env` names both; after `git add -A` but BEFORE commit: `git ls-files | grep -E '(^|/)\.env$'` empty, and — index-layer defense-in-depth (SRE amendment): `git grep --cached -I -E 'eyJ[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN.*PRIVATE KEY|authToken=[A-Za-z0-9]'` returns zero hits (modulo task-1's one named plan-file self-match, excluded or accepted explicitly — never by weakening the regex); `git status --ignored` shows `.env` files ignored; then commit and run `make check` — confirm the drift gate takes the **git-diff branch** (the checksum-fallback message must not appear) and every module in the then-current `MODULES` set + 4 guards pass.
- **description:** `git init -b main`, stage per the ratified tracked set, and cut a **single initial commit** (decision: no synthetic staged history — the hygiene gate already ran, and one commit is the honest starting point). Update the Makefile `check` comment that says the repo is not a git repo. The index-level secret checks in verify are the last line of defense before history exists.

### task-4: create remote + push

- **depends_on:** [task-3]
- **model:** opus
- **files:** [none — remote configuration only]
- **verify:** `git ls-remote origin main` returns the initial commit SHA; `gh repo view gopernicus/gopernicus --json visibility,defaultBranchRef` shows PUBLIC + `main`; secret scanning + push protection confirmed enabled (`gh api repos/gopernicus/gopernicus --jq '.security_and_analysis'`); open the repo in a browser and eyeball that `.env` is absent and `NOTES.md`/plans render (real-interaction check).
- **description:** Create the remote per RH1: `gh repo create gopernicus/gopernicus --public --source . --push` (create the `gopernicus` org via the web UI first if needed, then `git remote add` + push). **Ongoing secret posture (SRE amendment — the three-layer gate protects commit #1; the repo is a commit stream): the ACTIVE path per RH1-public is GitHub secret scanning + push protection, enabled at creation** (`gh api` on the repo's security settings; free on public repos) — the NOTES.md ratification entry names this explicitly. The gitleaks CI step remains recorded as the fallback that would have applied on a private repo; it is not taken. Must be active before the second commit lands. No branch protection yet — that lands in task-11 after CI exists.

### Phase 3 — CI (GitHub Actions)

### task-5: required gate — `make check` on every push/PR

- **depends_on:** [task-4]
- **model:** opus
- **files:** [`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.github/workflows/check.yml`; `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/Makefile` (one addition to `check`: a compile-only `-tags=integration` vet loop derived from the turso subset of `STORE_MODULES`)]
- **verify:** `make check` locally first (the new integration-vet step runs with no DB); then push the workflow on a branch, open a PR, and observe the run **green on the remote** (real-interaction check — YAML alone closes nothing): the log must show the templ-drift git-diff step, a `== module ==` block for every entry in the then-current Makefile `MODULES` set, the integration-tag vet step, and the four guard headers. Also confirm on that first run that workspace-mode `make check` regenerates the ignored `go.work.sum` without complaint (SRE amendment — no `-mod=readonly`-style blocker; if it ever refuses, the fallback is tracking `go.work.sum` per RH2's recorded overrule path). Then merge and confirm it runs green on `main`, and record the exact **check-run context name** the workflow reports — task-11 pins branch protection to that string, and a later workflow/job rename silently un-enforces the gate unless the protection rule is updated in the same change (note this in a workflow comment).
- **description:** One job on `ubuntu-latest`, with a least-privilege `permissions: contents: read` block (SRE amendment): `actions/checkout`, `actions/setup-go` with `go-version-file: go.work` (declares `go 1.26.1`; leave `GOTOOLCHAIN` auto so the exact toolchain self-downloads) and module caching, then `make check`. Add a `concurrency` group cancelling superseded runs per ref. templ must come from `go tool templ` (pinned via the `tool` directive in `features/cms/go.mod`) — CI never `go install`s templ separately, or the drift gate is meaningless. Close a hole the lead consult named: the gate never compiles `-tags=integration` files, so turso `*_integration_test.go` files can rot behind a green check — add the vet loop to the Makefile `check` target, **derived from the turso entries of `STORE_MODULES`, never a hardcoded module list** (PM amendment: events-v1 adds a fourth turso store that a hardcoded 3-list would silently exempt). Coordination note: whichever milestone lands this loop first owns it — if events-v1's plan also adds one, reconcile to a single derived loop. ~~Private-repo gitleaks step~~ — not applicable per RH1 (public): GitHub-native scanning is the active posture (task-4). This workflow is the future required status check for task-11. No live creds, no services — `make check` stays hermetic (store suites skip loudly).

### task-6: live-legs workflow — secret/service-gated, NOT required for merge

- **depends_on:** [task-5 — RH4 ratified the bundle as amended]
- **model:** opus
- **files:** [`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/.github/workflows/live-stores.yml`]
- **verify:** trigger one `workflow_dispatch` run and observe it **green on the remote** for the legs it has creds/services for: the three `make test-stores` pgx legs (cms/auth/jobs) against the postgres service, the goredis suites (`-race`) + pgxdb live test, and — if the TURSO_* secrets are configured — the three turso `-tags=integration` legs actually running (not skipping; check the log for the conformance subtest names, not just exit 0).
- **description:** Trigger: **manual `workflow_dispatch` only — no schedule** (RH4 ratified the PM-flipped default; the run is a button pressed before a tag or after a store change, not calendar noise against the playground DB). Least-privilege `permissions: contents: read` block of its own (SRE amendment). Job A: postgres:17 service container → `POSTGRES_TEST_DSN` env + `TURSO_DATABASE_URL`/`TURSO_AUTH_TOKEN` from repo secrets (empty ⇒ turso legs skip loudly, which is correct) → `make test-stores`. Job B: redis:7 service container → `REDIS_TEST_ADDR` explicitly set (spinning the container alone gates nothing — the goredis suites key off the env var, and goredis is NOT in `STORE_MODULES`/`make test-stores`, so this is a separate step by necessity) → `go test -race ./...` in `integrations/kvstores/goredis`, plus `integrations/datastores/pgxdb`'s live test against the postgres service. DSN-host consistency: jobs run on the runner (not in a container), so service ports map to `localhost:<port>` — use that form in both DSNs and say so in a workflow comment. **Failure-signal ownership (SRE amendment):** a non-required job going dark-red is exactly how four milestones closed "no creds, live legs not run" — the explicit alarm reliance is GitHub's workflow-failure email to the workflow's triggering owner (jrazmi); name that in the workflow header comment, and record the Turso token's owner + expiry there too with a rotation note (rotating the playground token means updating the repo secret, nothing else). Explicitly NOT testcontainers (workshop-v2 scope): services are workflow infra, the Go tests stay env-gated exactly as written. gcs/s3/sendgrid have no legs here (out of scope). This workflow is never a merge requirement.

### Phase 4 — D8 module-path verification pass (RH1: org-match; no quiet window)

### task-7: verify module paths match the ratified remote

- **depends_on:** [task-5 — no quiet window exists (RH1 org-match); no coordination constraint on events-v1/telemetry-closeout]
- **model:** opus
- **files:** [none — verification only]
- **verify:** `grep -rn 'github.com/gopernicus/gopernicus' . --exclude-dir=.git` inventory confirms every `go.mod` module line, import path, and Makefile guard (G1–G3) carries exactly the ratified remote prefix — the no-op confirmation; `make check` green (drift gate + every module in the then-current `MODULES` set + `make guard`); migration source names byte-identical (`grep -rn 'Name:' features/*/stores/*/` unchanged); RELEASING.md precondition 1 checked off in the execution log; **CI green on the remote for the verified commit** (real-interaction check).
- **description:** RH1 chose exactly the remote the module paths already carry (`github.com/gopernicus/gopernicus`), so D8 is a verification pass: confirm the prefix inventory, run the gate, record RELEASING.md precondition 1 as satisfied. No guard change is needed — G1–G3 already pin the correct prefix, and G4's org-rename gap (lead consult) only mattered on the rename branch. **Not-taken branch, one-line record:** the full mechanical rewrite (every `go.mod` + ~189 `.go` files incl. build-tagged ones + 6 `.templ` sources with the sed→generate→stage ordering + guard rewrite + live docs) was fully specified in this plan's pre-ratification revisions and is retrievable from the plan history if a future re-homing ever fires; it is not executable scope.

### Phase 5 — first tags (vertical slice per RH5) + branch protection — DOUBLE-GATED: events-v1 close (RH5) AND LICENSE landed (RH6 deferral)

### task-8: tag layer 1 — sdk/v0.1.0

- **depends_on:** [task-7; **GATE A (RH5 timing):** events-v1 CLOSED so the first tags are coherent (cms carries its `Mount.Events` emitter); **GATE B (RH6, deferred but unsoftened):** a `LICENSE` file landed and present in the tagged commit — both gates independent, both must clear]
- **model:** opus
- **files:** [none — tag only (sdk has no `require` block and no replaces to drop)]
- **verify:** `LICENSE` exists at the repo root on the commit being tagged (GATE B); `make check` green on the tagged commit; `git push origin sdk/v0.1.0`; then from a scratch dir outside the workspace: `go mod init probe && GOPROXY=direct go get github.com/gopernicus/gopernicus/sdk@v0.1.0` resolves (direct on purpose — see description; ~~private-repo `GOPRIVATE`~~ not applicable per RH1-public).
- **description:** Cut and push `sdk/v0.1.0` per RELEASING.md's nested-tag scheme. This tag must be remotely fetchable before task-9 can tidy, because `go mod tidy` ignores `go.work` and fetches requires from the network. **Negative-cache landmine (lead consult):** proxy.golang.org caches 404s — a mistimed `go get` before the tag is pushed poisons the proxy for that version and you wait it out. Every post-tag tidy/probe of a just-cut tag in this phase runs `GOPROXY=direct` so resolution goes straight to git, skipping both propagation latency and the negative cache.

### task-9: tag layer 2 — features/cms + integrations/datastores/turso

- **depends_on:** [task-8]
- **model:** opus
- **files:** [`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/cms/go.mod`, `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/integrations/datastores/turso/go.mod` — drop the relative `replace` of sdk, pin `require <prefix>/sdk v0.1.0`]
- **verify:** per module, run the WHOLE dance off-workspace — `GOWORK=off go mod tidy && GOWORK=off go build ./... && GOWORK=off go test ./...` from inside the module dir (tidy included, so it sees the identical module graph a consumer sees); full `make check` from the root (workspace dev path still green); commit, tag `features/cms/v0.1.0` + `integrations/datastores/turso/v0.1.0`, push tags; both resolve from the scratch probe with `GOPROXY=direct`.
- **description:** Layer 2 of the vertical slice: the two modules whose only sibling dependency is `sdk`. One commit for both `go.mod` edits, then two tags on that commit. Expect real `go.sum` churn (lead consult): replace targets are never hashed into `go.sum`, so pinning adds the sibling's zip + `/go.mod` hashes and pulls its transitive requires into MVS — a non-trivial but expected diff, not a regression. Full-sweep variant (not taken per RH5; kept as record for a future sweep): same dance across every integration + feature core in the then-current `MODULES` set.

### task-10: tag layer 3 — features/cms/stores/turso

- **depends_on:** [task-9]
- **model:** opus
- **files:** [`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/features/cms/stores/turso/go.mod` — drop all three relative replaces; pin `sdk`, `features/cms`, and `integrations/datastores/turso` at v0.1.0]
- **verify:** `GOWORK=off go mod tidy && GOWORK=off go build ./... && GOWORK=off go test ./...` from the module dir (hermetic legs; live legs stay env-gated); full `make check`; commit, tag `features/cms/stores/turso/v0.1.0`, push; migration source name re-confirmed byte-identical (`"cms"`).
- **description:** The layer-3 store dance on the smallest possible slice (the point of RH5): three replaces unwound, three pinned requires, tidy off-workspace — both layer-2 tags must already be pushed and directly fetchable. `features/cms@v0.1.0` drags templ/goldmark/bluemonday in as real requires, so this module's `// indirect` block reshuffles — expected. Examples KEEP their replaces under slice mode (they require untagged features; replaces are harmless for untagged demonstration hosts and `go.work` covers dev). Full-sweep variant (not taken per RH5, kept as record): all 6 store modules + example pinning.

### task-11: branch protection + end-to-end resolution probe

- **depends_on:** [task-10]
- **model:** opus
- **files:** [none — remote configuration + a throwaway probe module outside the repo]
- **verify:** `gh api` (or `gh repo edit`) applies protection on `main`: require the task-5 check **pinned to the exact check-run context name recorded at task-5** (a later workflow rename must not silently un-enforce the gate), forbid force-push and deletion (no required reviews — solo maintainer with stacked PRs); confirm by pushing a trivial branch PR and observing the check requirement appear. **Tag protection (SRE amendment):** apply a repository ruleset for `**/v*` tag refs forbidding deletion and non-fast-forward updates — branch protection does NOT cover tag refs, and a force-pushed tag that the proxy already cached is unrecoverable (the proxy serves the old bytes forever; consumers see checksum mismatches). Confirm the ruleset rejects a test `git push --force origin <tag>` attempt. Real-interaction check for tags: in a scratch dir, a probe `main.go` importing `github.com/gopernicus/gopernicus/sdk/errs` AND `.../features/cms` (+ its turso store in `go.mod`) — `go get` at `@v0.1.0`, `go build` compiles. ~~Private repo: document the `GOPRIVATE` + auth line~~ — not applicable per RH1 (public).
- **description:** Protect `main` behind the CI gate, protect the tag namespace, and prove the whole point of the milestone from the outside: a consumer with none of this repo checked out can depend on tagged modules the normal Go way. Standing rule to record in RELEASING.md at task-12: **pushed version tags are immutable — a broken tag is fixed by cutting the next patch version, never by `git tag -f`.**

### task-12: docs + record sync

- **depends_on:** [task-11]
- **model:** fable
- **files:** [`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/RELEASING.md` (the "No tags have been cut yet" paragraph and preconditions section — now history; plus the tag-immutability rule from task-11), `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/README.md` (**REQUIRED, PM amendment**: a "how to depend on gopernicus" section with the literal `go get <prefix>/<module>@v0.1.0` line — this is the single most host-developer-facing outcome of the milestone, not a nice-to-have), `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus/NOTES.md` (milestone entry), `.claude/plans/repo-hardening/plan.md` (execution log)]
- **verify:** `make check` still green (docs-only diff); fresh-eyes read: no doc claims the repo is un-versioned, un-tagged, or CI-less; the README `go get` line copy-pastes and works against the real tags (re-run the task-11 probe command verbatim from the README text).
- **description:** Close the record: RELEASING.md's "no tags yet" framing becomes "first tags cut <date>, procedure below stands" and gains the pushed-tags-are-immutable rule, plus — under slice mode — an explicit released/not-yet-released table (the tagged slice modules vs the untagged remainder of the then-current importable set, which is NOT advertised as consumable until tagged); README gains the required depend-on-gopernicus section; NOTES.md gains the milestone entry (secrets-sweep outcome, RH1–RH6 ruling references, tag list, CI run links). Historical entries stay as written.

## Sequencing

```
task-1 → task-2 → task-3 → task-4 → task-5 → task-6        (phases 1–3)
                                      └────────→ task-7 → task-8 → task-9 → task-10 → task-11 → task-12   (phases 4–5)
```

- **Phases 1–3 execute FIRST and immediately** (ratification execution
  order: everything into git before more code lands; hygiene gates before
  any push). They have zero dependency on feature work; events-v1 and
  telemetry-closeout proceed in parallel. Nothing in them touches Go code
  beyond Makefile comment/vet-loop lines.
- **Phase 4: no quiet window exists (RH1 org-match).** Task-7 is a
  read-only verification pass that conflicts with nothing; events-v1 and
  telemetry-closeout are unconstrained by it. *(Record: the generalized
  quiet-window rule — rename case excludes every code-landing milestone —
  applied only to the not-taken rewrite branch.)*
- **Phase 5 is double-gated — two independent gates, both must clear:**
  **GATE A (RH5):** events-v1 CLOSED, so the first tags are coherent
  (`features/cms` carries its `Mount.Events` emitter; no immediate-stale
  v0.1.0 → v0.1.1 re-cut). **GATE B (RH6 deferral):** a `LICENSE` file has
  landed and the tagged commit carries it — the gate stands unsoftened;
  LICENSE deferred means phase 5 waits, not that tags ship without one.
  Internally the phase stays ordered by tag-fetchability (8 → 9 → 10),
  then 11 → 12.
- All six YOUR CALLs are RATIFIED (RH1–RH6, 2026-07-07; NOTES.md entry) —
  no ruling blocks phases 1–4. The only open inputs are phase 5's two
  gates above.

## Consultation notes

`lead-backend-engineer` reviewed a paragraph sketch of phases 4–5 and
returned four changes, all adopted: (1) **tag a vertical slice, not all 22**
(now YOUR CALL 5's default; tasks 8–10 restructured around
sdk → cms core + turso connector → cms turso store); (2) **G4 cannot catch a
stale hosted prefix after an org rename** (its grep anchors on
`'"gopernicus/'`) — task-7 now rewrites/extends the guard with a
prove-can-fail step; (3) **the required gate never compiles
`-tags=integration` files**, so the turso stores' integration tests could
rot behind a green check — task-5 adds a compile-only vet loop to `make
check`; (4) **`go.work.sum` tracking is a real either-way call** — folded
into YOUR CALL 2 (default: ignore by exact name; never a `*.sum` glob).
Also confirmed/absorbed: tidy-ignores-workspace ordering; proxy
**negative-cache** poisoning (not just latency) → `GOPROXY=direct` for every
just-cut tag; run the whole layer dance `GOWORK=off` including tidy; expect
`go.sum` churn when replaces drop (indirect blocks reshuffle — not a
regression); sed must include build-tagged files; stage regenerated
`*_templ.go` before the drift check; `REDIS_TEST_ADDR` is outside
`make test-stores` and needs its own step; pin the CI toolchain via
`go-version-file` + auto `GOTOOLCHAIN`.

## Open questions

- When to tag the untagged remainder of the importable set under slice
  mode — default: as real demand appears (a consumer, or the next milestone
  that needs a pinned version), not on a calendar.
- Whether to later add a schedule to the live-legs workflow (manual-only is
  the RATIFIED posture, RH4) — revisit if a real consumer starts depending
  on the tagged stores.

## Recommended reviews

- **product-manager** — gate run 2026-07-07, RATIFY WITH AMENDMENTS (all
  applied: LICENSE YOUR CALL, YC1↔YC2 pairing, generalized quiet window,
  de-hardcoded module arithmetic, derived vet loop, manual-only live legs,
  required README `go get` section, tag-timing sub-decision). Re-review
  only if YOUR CALL rulings reshape scope.
- **platform-sre** — gate run 2026-07-07, RATIFY WITH AMENDMENTS (all
  applied: corrected task-1 pass condition, full-regex index check,
  workflow permissions + pinned check context, live-leg alarm ownership +
  token rotation note, tag protection ruleset + immutability rule, ongoing
  secret-scanning posture, go.work.sum regen confirmation). Re-review only
  if YOUR CALL rulings reshape phases 2–3/5.
- **lead-backend-engineer** — consulted pre-ratification (all four of its
  changes adopted; see Consultation notes); re-review only if the
  YOUR CALL rulings change phase 4/5 shape.

## Notes

- Reconnaissance facts the plan relies on (verified 2026-07-07): 26 modules
  in go.work↔Makefile agreement; module prefix already
  `github.com/gopernicus/gopernicus`; real tokens confined to the two
  gitignored `.env` files; no token-shaped strings in candidate-tracked
  files (re-confirmed independently by the SRE review); `gh` authenticated
  as `jrazmi` (repo+workflow scopes); git identity configured; templ pinned
  via `tool` directive in `features/cms/go.mod`; built binaries at
  `examples/{cms,jobs-minimal}/server`; stale root-anchored `.gitignore`
  entries; closed-milestone plans moved to `.claude/past/` 2026-07-07
  (mapping at `.claude/past/README.md`) — treated identically to
  `.claude/plans/` throughout (sweep scope, tracked set).
- **Module-arithmetic convention (PM amendment):** every count in this plan
  (26 modules / 22 importable / 18 untagged remainder) is as-of-2026-07-07;
  verify lines and task scopes bind to the **then-current Makefile
  `MODULES` set**, never these numbers (they become 29/25/21 if events-v1
  lands first).
- The Makefile's `check` target needs **no edit** for the drift-mode
  upgrade — it already branches on `[ -d .git ]`; task-3 merely proves the
  git branch runs. Only its stale comment changes (task-3) and the derived
  integration-vet loop is added (task-5).

## Execution log

### task-1 — 2026-07-07 (secrets sweep + artifact cleanup) — PASS

**Environment confirmed**
- `[ -d .git ]` → NO `.git` (filesystem-mode sweep; no history to mine).
- `find . -name '.env*'` → exactly `./.env`, `./.env.example`,
  `./examples/cms/.env` (matches task-1 verify).

**Sweep commands run** (from repo root, to-be-gitignored set excluded:
`--exclude-dir=.git --exclude-dir=media-data --exclude=go.work.sum
--exclude='*.db' --exclude='*.db-*' --exclude=.DS_Store --exclude=.env
--exclude=server`):
- Full 4-pattern sweep
  `grep -rnIE 'eyJ[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN.*PRIVATE KEY|authToken=[A-Za-z0-9]' .`
- Same, adding `--exclude=plan.md` → **zero hits.**
- JWT-shape only `grep -rnIE 'eyJ[A-Za-z0-9_-]{20,}' .` (real `.env`
  excluded) → **zero hits.**
- JWT-shape count in the two real `.env` files → `./.env:1`,
  `./examples/cms/.env:1` (real Turso-token JWTs confined to the two
  gitignored files).

**Hit counts per pattern (candidate-tracked files):**
- `eyJ[A-Za-z0-9_-]{20,}` (JWT): **0.**
- `AKIA[0-9A-Z]{16}` (AWS key): **0.**
- `-----BEGIN.*PRIVATE KEY`: **2 — both inside
  `.claude/plans/repo-hardening/plan.md` (lines 203, 221)**, the named
  expected self-match artifact. Zero elsewhere.
- `authToken=[A-Za-z0-9]`: **0.**

**Named expected artifacts observed:**
- (a) Plan-file self-match: observed on **two** lines (203 and 221), not
  one. Both quote the full regex string, both match on the literal
  `-----BEGIN.*PRIVATE KEY` substring. Line 221 is task-3's verify block,
  which also quotes the regex — hence a second occurrence of the same
  named artifact. Excluding `plan.md` yields zero hits tree-wide.
  (Divergence from the plan's "self-matches once" wording; benign — same
  artifact class, the plan quoting its own regex.)
- (b) `?authToken=` DSN-mechanic mentions: `integrations/datastores/turso/turso.go:52`
  (`"authToken=" + cfg.AuthToken`), `NOTES.md:29`, `NOTES.md:68` — each
  followed by a quote/backtick character. `grep -rnIE 'authToken=[A-Za-z0-9]'`
  on those files → **no match** (exit 1). Confirmed deliberately un-matched;
  pattern left unchanged.

**`.env.example` (tracked) content:** placeholders only —
`TURSO_DATABASE_URL=libsql://your-db.turso.io`,
`TURSO_AUTH_TOKEN=your-auth-token`, empty `SMTP_PASSWORD=`. No real token.

**Playground Turso URL occurrences** (re-derived; RH3 evidence base) —
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`,
**10 occurrences**, all with only prose adjacent (authorization notes), no
token adjacent (proven conclusively by the tree-wide zero JWT-shape result
— a Turso token is a JWT):
- `NOTES.md:298`
- `.claude/plans/events-v1/plan.md:366`
- `.claude/plans/roadmap/execution-loop-handoff.md:87`  *(not in plan inventory)*
- `.claude/plans/auth-v2/00-overview.md:139`  *(not in plan inventory)*
- `.claude/plans/auth-v2/07a-store-turso.md:74`  *(not in plan inventory)*
- `.claude/past/auth-v1/05-auth-store-turso.md:87`
- `.claude/past/datastore-portability/04-docs-policy-sync.md:121`
- `.claude/past/fast-follows/HANDOFF.md:85`
- `.claude/past/jobs-v1/00-overview.md:81`
- `.claude/past/jobs-v1/05-store-turso.md:38`

  Plan inventory expected NOTES.md ×1 + events-v1 + 5 under `.claude/past/`
  (=7). Observed: the `.claude/past/` count is **exactly 5** as expected;
  **3 additional occurrences under `.claude/plans/` (auth-v2 ×2, roadmap ×1)**
  — a drift-since-2026-07-07 divergence to log, not a failure (RH3: the
  planning corpus is world-readable by design; no token adjacent to any).
  `.claude/plans/restructure/` (on disk, absent from the plan inventory) was
  swept and carries **zero** playground-URL occurrences.

**Baked test-cred sweep** (`POSTGRES_TEST_DSN`/`TURSO_*`/`REDIS_TEST_ADDR`
with assigned values): only the canonical local-dev placeholder DSN
`postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable` (in
Makefile usage text, `*/conformance_test.go` doc comments, store READMEs,
`pgxdb/live_test.go`) and `REDIS_TEST_ADDR=localhost:6379` addresses. No
real credentials; no `TURSO_AUTH_TOKEN=<real>` outside the gitignored
`.env` files.

**Credential-adjacent integration secret-literal sweep** (sendgrid, gcs,
s3, oauth/github, oauth/google, cryptids/golang-jwt): only two obvious test
fixtures — `integrations/oauth/github/github_test.go:13` and
`integrations/oauth/google/google_test.go:20`, both
`testClientSecret = "test-client-secret"`. Benign literal placeholders,
not matched by the authoritative regex.

**Stray artifacts:** `find` for `*.db`/`*.db-shm`/`*.db-wal`/`*.db-journal`
tree-wide → **none.** No `testdata` directories exist.

**Deletions performed** (the only file modifications):
- `examples/cms/server` (stale built binary) — deleted.
- `examples/jobs-minimal/server` (stale built binary) — deleted.

**`make check`:** ended `all checks passed`. No `.git`, so the templ-drift
gate took the **checksum-fallback (`else`) branch** — the git-diff error
string never printed (correct for this phase). Every module in the
`MODULES` set (vet+build+test) and all four guards passed.

**Real-interaction boot check:** `cd examples/minimal && PORT=8081 go run
./cmd/server` (host defaults `PORT` to 8081; seeds a "Widget 3000" product,
slug `widget-3000`):
- `GET http://localhost:8081/` → **200**
- `GET http://localhost:8081/products/widget-3000` → **200**
- Process killed; port 8081 confirmed free (`lsof -iTCP:8081` empty; curl →
  connection refused). Note: `go run`'s compiled child binary (`server`)
  outlives a `pkill -f 'go run'`; killed explicitly by port.

**Divergences logged (none are failures):**
1. Plan file self-matches on two lines (203, 221), not one — task-3's
   verify block also quotes the regex.
2. Playground URL: 10 occurrences vs the plan's 7-line inventory — 3 extra
   under `.claude/plans/` (auth-v2 ×2, roadmap ×1); `.claude/past/` count
   matches exactly at 5. No token adjacent to any (zero JWT-shapes tree-wide).
3. Two benign `test-client-secret` fixtures in oauth github/google tests —
   not real credentials, not matched by the authoritative regex.

**Result: PASS** — the full 4-pattern sweep returns zero hits outside the
gitignored `.env` files (modulo the named plan-file self-match); JWT-shape
count is 0 in every candidate-tracked file (real JWTs only in `./.env` and
`./examples/cms/.env`); `find .env*` returns exactly the three expected;
both stale `server` binaries deleted. No literal secret found outside the
`.env` files — the milestone is not blocked.

### task-2 — 2026-07-07 (.gitignore redesign) — PASS (review-verify)

**Scope note:** review-only phase — no `.git` exists yet, so `git
check-ignore` cannot run. The hard, index-layer ignore gate is task-3's.
Verification here is by inspection only; git-level ignore behavior is
deferred to task-3 by design.

**Final `.gitignore` content** (replaces the stale single-module-era file
whose root-anchored `/server` and `/cmd/server/server` entries never
matched today's `examples/*/server` binaries):

```
# local env (unanchored: matches ./.env and examples/cms/.env; .env.example stays tracked)
.env

# built binaries (host apps build to examples/<app>/server)
examples/*/server

# local sqlite/libsql files
*.db
*.db-*

# macOS
.DS_Store

# local uploaded assets (disk filestore)
media-data/

# workspace sum — exact name only; per-module go.sum files stay tracked (never a *.sum glob)
go.work.sum

# transient local tooling artifact
.claude/scheduled_tasks.lock
```

**Inspection verify — PASS:**
- Every required pattern present: `.env` (unanchored), `examples/*/server`,
  `*.db`, `*.db-*`, `.DS_Store`, `media-data/`, `go.work.sum` (exact name).
- Stale root-anchored entries gone: `grep -nE '^/server|^/cmd/server/server'`
  → no match (exit 1).
- No `*.sum` glob anywhere: `grep -nE '\*\.sum'` matches ONLY the comment on
  line 17 (which literally warns against the glob); the actual sum pattern
  on line 18 is the exact name `go.work.sum`. Per-module `go.sum` records
  consumers rely on are therefore never dropped.
- `.env.example` NOT matched: gitignore `.env` matches the exact basename
  `.env` at any depth, never the distinct filename `.env.example` (no prefix
  match). Deliberately-tracked set (`.env.example`, `go.work`, all per-module
  `go.sum`, `NOTES.md`, `.claude/plans/`, `.claude/past/`, `.claude/agents/`)
  is untouched by every pattern. No `LICENSE` entry (RH6 deferred it).

**Logged divergence — premise drift since planning (NOT a re-decision):**
`.claude/scheduled_tasks.lock` exists on disk (130 bytes, mtime 2026-07-07
12:46) — a transient local tooling artifact absent from RH2's tracked-set
inventory (which named NOTES.md, `.claude/plans/`, `.claude/past/`,
`.claude/agents/`). Left unignored, task-3's `git add -A` would stage it
into a public repo. Added the exact-name line `.claude/scheduled_tasks.lock`
(exact name, not a `.claude/*.lock` glob — surgical, matches only this
artifact). No other speculative patterns added; every line traces to the
plan or this named divergence.

**`make check`:** ended `all checks passed`. No `.git` yet, so the templ-drift
gate took the **checksum-fallback (`else`) branch** as expected for this
phase (the git-diff error string never printed). Every module in the
`MODULES` set (vet+build+test) and all four guards passed.

**Real-interaction boot check** (`cd examples/minimal && PORT=8081 go run
./cmd/server`):
- `GET http://localhost:8081/` → **200**
- `GET http://localhost:8081/products/widget-3000` → **200**
- Killed by port (`lsof -tiTCP:8081 -sTCP:LISTEN` → kill; `go run`'s child
  binary outlives a parent pkill, so killed explicitly by port); port 8081
  confirmed free (`lsof :8081` empty; curl → connection refused).

**Result: PASS** — review-verify satisfied by inspection; every required
pattern present, stale root-anchored entries gone, no `*.sum` glob,
`.env.example` unmatched. Git-level enforcement deferred to task-3's
`git check-ignore` gate by design.

### task-3 — 2026-07-07 (git init + verified-clean initial commit) — PASS

**Environment confirmed (pre-init):** no `.git`; git identity
`user.name=jrazmi`, `user.email=joshua@gpsimpact.com`; `find . -name '.env*'`
→ exactly `./.env`, `./.env.example`, `./examples/cms/.env`.

**Step 1 — Makefile `check` comment fix (comment lines only, recipe
untouched):** the stale note ("this repo is not [a git repo] (as of phase
1), so it falls back to before/after checksums") now reads that the repo IS
a git repo as of phase 2, so the git-diff branch runs, and the checksum
branch remains a fallback for gitless checkouts. Only lines 3–5 of the
`check` comment changed; the `@if [ -d .git ]; then …` recipe is byte-identical.

**Step 2 — `git init -b main`:** initialized; `git symbolic-ref --short
HEAD` → `main`.

**Step 3 — ignore-layer gate — PASS:**
- `git check-ignore .env examples/cms/.env` → names both (exit 0).
- `git check-ignore .claude/scheduled_tasks.lock` → names it (task-2
  divergence; exit 0).
- `git status --ignored --short` → `!! .env`, `!! examples/cms/.env`,
  `!! .claude/scheduled_tasks.lock`; `.env.example` shows `??`
  (untracked → will be staged, correct).

**Step 4 — `git add -A`:** staged 545 files.

**Step 5 — index-layer gates (BEFORE commit) — PASS:**
- `git ls-files | grep -E '(^|/)\.env$'` → empty (grep exit 1). No `.env`
  staged.
- Cached secret grep with the ONE named exclusion:
  `git grep --cached -I -E 'eyJ[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN.*PRIVATE KEY|authToken=[A-Za-z0-9]' -- ':!.claude/plans/repo-hardening/plan.md'`
  → **zero hits** (grep exit 1). Regex not weakened.
- Same grep WITHOUT the exclusion → **5 matching lines, ALL inside
  `.claude/plans/repo-hardening/plan.md`** (the task-1/task-3 verify blocks
  and the execution-log lines quoting the `-----BEGIN.*PRIVATE KEY` /
  full-regex strings). Zero matches in any other tracked file — the named
  self-match artifact, confirmed contained to that one file. (task-1
  observed 2 lines pre-index; the added execution-log quoting brings it to 5
  now, exactly the "may add more — all in that same file" case the plan
  named.)
- Tracked-set sanity: `git ls-files | grep -c 'go.sum$'` → **22** (>0);
  `git ls-files | grep -E '^(NOTES.md|go.work|\.env\.example)$'` → all three
  present; `.claude/scheduled_tasks.lock` NOT in `git ls-files` (grep exit
  1); `.claude/{plans,past,agents}` all tracked (31 / 28 / 10 files).
- No literal secret staged → proceeded to commit (no stop condition fired).

**Step 6 — single initial commit:** message "Initial commit: gopernicus
framework monorepo …" ending in the `Co-Authored-By: Claude Fable 5` trailer.
Pre-amend SHA `44f1bf216d4d6b99e3becf2038bc267119a360a0`. No synthetic staged
history — one honest starting commit.

**Step 7 — post-commit `make check` — PASS, git-diff drift branch — PASS:**
- Branch-selection trace of the exact condition make evaluates
  (`sh -xc 'if [ -d .git ]; then …'`) → `+ '[' -d .git ']'` then the
  git-diff branch echo. With `.git` present the `[ -d .git ]` then-branch
  runs `git diff --exit-code -- '*_templ.go'`; the checksum `else` branch
  does NOT execute.
- `make check` ended `all checks passed`, exit 0. The captured log contains
  **zero** occurrences of `ERROR: templ generation drift` (the
  checksum-fallback string). `make generate` ran once (git-diff branch
  form), and `git status --porcelain` shows no `*_templ.go` changes — the
  `git diff --exit-code` was the operative, clean check.
- Every module in `MODULES` (vet/build/test) + all four guards passed.

**Step 8 — real-interaction boot check (examples/minimal, :8081) — PASS:**
- Port 8081 free before boot.
- `cd examples/minimal && PORT=8081 go run ./cmd/server`.
- `GET http://localhost:8081/` → **200**; `GET
  http://localhost:8081/products/widget-3000` → **200**.
- Killed by port (`lsof -tiTCP:8081 -sTCP:LISTEN | kill`; the `go run` child
  binary outlives a parent pkill, so killed by port). Port confirmed free;
  post-kill curl → `000` / connection refused (exit 7).

**Step 9 — amend:** this log entry `git add`ed and folded into the initial
commit via `git commit --amend --no-edit` (safe — no remote exists yet), so
the single initial commit carries its own up-to-date log. Final commit SHA
and the final cached-grep re-check recorded inline below at amend time.

**Divergences (none are failures):**
1. Named plan-file self-match grew from 2 lines (task-1, pre-index) to 5
   lines post-index — the task-3 verify block plus execution-log lines
   quoting the regex. All contained to `.claude/plans/repo-hardening/plan.md`;
   the named exclusion yields zero hits tree-wide. Expected per the plan.

**Result: PASS** — ignore-layer, index-layer, tracked-set sanity, commit,
git-diff drift branch, and boot check all green. No secret entered the
index; the milestone is not blocked.

### task-4 — 2026-07-07 (create remote + push) — STOPPED, stop-and-flag: remote name already occupied

Precondition checks (protocol step 2, before any GitHub action):

- `gh auth status` → jrazmi authenticated, scopes `repo`, `workflow` (+gist/project/read:org). PASS.
- `gh api /orgs/gopernicus` → org EXISTS; `gh api /user/memberships/orgs/gopernicus` → jrazmi is `admin / active`. PASS.
- `gh repo view gopernicus/gopernicus` → **repo ALREADY EXISTS** — premise
  of this task ("create the remote"; "create the gopernicus org via the web
  UI first if needed") is FALSE.

State of the existing remote (observed 2026-07-07):

- PUBLIC, non-empty: created 2026-03-25, last pushed 2026-06-17,
  ~6.8 MB, default branch `main`, 6 branches (`main`,
  `auth_flows_accounts`, `chore/shed-cli-references`,
  `docs/fix-cli-and-framework-reference-drift`, `httpmid_streamers_flush`,
  `v0.3.1-segovia-migration-fixes`).
- Root contents: `bridge/`, `core/`, `workshop/`, `sdk/`, `telemetry/`,
  `infrastructure/`, `gopernicus.yml`, `CHANGELOG.md`, … — this is the
  **ORIGINAL gopernicus repo** (the salvage source the planning corpus
  calls gopernicus-original).
- Secret scanning + push protection: DISABLED on the existing repo.

Why this stops the leg: every path forward is a destructive owner-level
action against a repo with live history — rename, delete, or force-push —
none of which RH1's ratification contemplated (the plan text assumed the
name was vacant, to the point of allowing for the org not existing). Per
the stop-and-flag rules (contradicted premise + destructive action), the
leg ends here; no push, no repo mutation performed. Tasks 5–7 depend on
task-4; the loop stops with every queue item gated on jrazmi.

Options for the YOUR CALL (loop's recommendation: option 1):

1. **Rename the original** to e.g. `gopernicus/gopernicus-original`
   (`gh repo rename` preserves all history/branches; old-name redirects
   are then intentionally severed when the new repo claims the name), then
   re-run task-4 verbatim (`gh repo create gopernicus/gopernicus --public
   --source . --push`).
2. Push the monorepo into the existing repo (force-push over `main`) —
   mixes two unrelated histories in one repo, leaves 5 stale branches;
   NOT recommended.
3. Delete the original repo and recreate fresh — destroys the original's
   history unless it is fully preserved elsewhere; most destructive.
4. Choose a different remote name — contradicts ratified RH1 and would
   re-arm phase 4's full rename branch; a scope change, not a default.
