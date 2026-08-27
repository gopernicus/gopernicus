# `features/` → `pockets/` — the domain-hexagon tier gets its own word

**Status: RELEASED 2026-08-27 — PR #7 squash-merged to `main` @ `aaaf5c8`; all
19 tags pushed at that SHA in D3 order and cold-resolved (`GOWORK=off
GOFLAGS=-mod=mod`); see §As executed at the bottom. Previously REVIEWED +
AMENDED 2026-08-27 (Q1–Q4 ruled; Codex review folded into D1–D7/T1–T10).**
**Owner rulings:** the word is `pockets` (pocket gophers; "a self-contained
pocket of code"); **`features` is removed completely** — no shim, no alias, no
surviving path or verb anywhere outside history.

## Problem (confirmed against the code)

The repo has four tiers — `sdk` (kernel), `integrations/*` (connectors),
`features/*` (five self-contained hexagons: authentication, authorization, cms,
events, jobs), `examples/*` (hosts). The word "feature" is the wrong word for
the third tier: a host app's *feature* ("invite a teammate") is built by
composing two or three of these units, so every conversation about a host
collides with the framework's own vocabulary. ARCHITECTURE.md already reaches
for a different word in prose ("session-auth hexagon", "IAM hexagon", "the CMS
hexagon") — the directory name is the only place the wrong word is load-bearing.

It IS load-bearing, though. Measured 2026-08-27:

| Where | Count |
|---|---|
| Modules under `features/` (each its own `go.mod`, each tagged) | 17 |
| Existing tags on `features/*` paths | 52 |
| `gopernicus/features/` import-path references in-repo | ~1,078 (511 `.go`, 19 generated `_templ.go`, 22 `go.mod`, 13 `go.sum`, 76 `.md`, docs-site `.ts`) |
| `sdk/feature` — the ONE sanctioned composer (`feature.Mount`, `feature.Group`, `feature.PrefixRegistrar`, `feature.RouteRegistrar`) | imported by every pocket core + inbound pkg + 4 example hosts |
| Makefile: `MODULES`, `STORE_MODULES`, 7 guards named `guard-feature-*` / `guard-store-no-foreign-feature`, templ + pgx legs | — |
| Workshop CLI verb `gopernicus new feature` + `templates/feature/` tree + `featureParams`/`emitFeature`/… identifiers; independently tagged today at `workshop/gopernicus/v0.1.0` | — |
| Docs site (`workshop/documentation`): `docs/features/*`, sidebar "Feature modules", `guides/create-feature`, `architecture/feature-contract`, navbar "Features" | — |
| `.claude/agents/*.md` — every agent charter speaks "feature core / store adapter" | 10 files |
| Downstream consumers importing `gopernicus/features/` | 5 repos (table in §Adoption) |

This is a Go **module-path rename**, i.e. an identity change for 17 modules —
the largest break since sdk-layering (2026-07-10). It is allowed pre-1.0, all
five consumers are the owner's own repos, and the cost only grows with every
tag; so it ships as ONE train, now.

## Proposal in one paragraph

`git mv features pockets`; every module path `github.com/gopernicus/gopernicus/features/X`
becomes `…/pockets/X`; the composer package `sdk/feature` becomes `sdk/pocket`
(sdk minor bump, no alias shim); the CLI verb becomes `gopernicus new pocket`;
the guards, workspace, docs, agent charters and the contract document
(`pockets/README.md`) follow; historical text (`plans/`, `.claude/plans/`,
RELEASING upgrade history, git tags) is **not** rewritten; one new guard
forbids the legacy path forever; the 17 pocket modules + sdk + the behavior-
changing Workshop CLI are tagged in one 19-tag train, each continuing its own
version lineage with a minor bump; the five
consumers repin via a mechanical mapping this plan provides.

## Decisions

### D1 — Vocabulary

- **Noun:** a *pocket*. `pockets/authentication` is "the authentication
  pocket". Sub-parts keep their names: *pocket core* (the datastore-free
  hexagon), *store adapter* (`stores/<pkg>`), *views* (`views/<pkg>`).
- **Contract:** "the pocket contract" (`pockets/README.md`, formerly "the
  feature contract").
- **Composer:** `sdk/pocket` — `pocket.Mount`, `pocket.Group`,
  `pocket.PrefixRegistrar`, `pocket.RouteRegistrar`. Type/func names inside
  are unchanged; only the package moves.
- **Rule IDs stay:** `FS1`, `FS2`, `FS3`, `FS4`, `FS9`, `G5`–`G17` are
  historical identifiers cross-referenced from ratified plans; they are NOT
  renamed (one sentence in `pockets/README.md` §1 says "FS = the former
  'feature standard' series"). Guard *targets* in the Makefile DO rename
  (they are commands, not history) — see D4.
- **Leave alone:** the CMS `Featured` field and the word "feature" where it
  genuinely means an application feature in prose. All SQL *comments* that use
  feature vocabulary for this tier are living text and DO change: both
  `0001_job_queue.sql` files, both authentication
  `0016_invitation_metadata.sql` files, and the
  `features/jobs/stores/pgx` precedent path in events'
  `0001_event_outbox.sql`. SQL statements and migration behavior stay
  byte-identical.

### D2 — `sdk/feature` → `sdk/pocket`, hard rename, no alias shim

Renaming the directory but leaving the composer called `feature` would keep
the exact split-vocabulary this plan exists to end, in the one API every host
`main.go` types. So it moves in the same train.

No deprecated `sdk/feature` alias package — **ruled 2026-08-27 (Q1: no
shim; clean removal)**. Rationale: a host cannot consume the new pocket tags
without touching every import line anyway, so a shim buys one fewer sed
pattern and costs a release of dead code plus a guard exception. Pre-1.0; all
consumers are owner-controlled.

sdk bumps **v0.4.2 → v0.5.0** (minor: a package moved; nothing else changes).
The implementation also renames the composer source files
`feature.go`/`feature_test.go` to `pocket.go`/`pocket_test.go`; leaving tier-
derived filenames under `sdk/pocket/` would contradict the clean-removal ruling.

### D3 — Module paths and the tag train

Directory move preserves history (`git mv`). Every module keeps its own
version lineage (**ruled 2026-08-27, Q2: continue**) and takes a **minor** bump on the new path so `git tag -l`
reads as one continuous story per module (a `pockets/authentication/v0.1.0`
that is newer than `features/authentication/v0.6.0` would mislead forever).

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

`ui/goth` and all `integrations/*`: **not retagged** — their module paths and
runtime behavior do not change (verify no `go.mod` mentions `features/` in T2;
if any integration turns out to require a pocket, it joins the train).
`workshop/gopernicus` DOES join: although its module path is stable, its shipped
CLI removes one command, adds another, and embeds renamed templates. Without a
new tag, `go install .../workshop/gopernicus@latest` would keep distributing
`new feature` forever.

Tag order (requires must resolve cold): `sdk/v0.5.0` → the five pocket cores +
`workshop/gopernicus/v0.2.0` → the stores/views. Same discipline as the
2026-08-16 six-tag train.
`sdk/v0.5.0` rides THIS train, not a later one (**ruled 2026-08-27, Q3**): with
no shim, every pocket core imports `sdk/pocket`, so the sdk tag is a
prerequisite of every other tag in the table.

**Release-pin invariant (Codex review).** Path substitution alone is invalid:
it would leave requirements such as `pockets/authentication v0.4.0`, a version
that will never exist on the new module path, and `go.work` would hide the
mistake. Before merge:

- every pocket core requires `sdk v0.5.0` (the first tag containing
  `sdk/pocket`);
- every store/views module requires its corresponding core at that core's
  first new-path version in the table;
- direct sdk requirements in adapters advance to `v0.5.0` when their source or
  emitted code uses `sdk/pocket`; otherwise MVS still selects `v0.5.0` through
  the core, but pins are normalized to `v0.5.0` in this train for clarity;
- example hosts' requires use the new table versions even though their local
  replaces make versions operationally irrelevant; and
- the Workshop pocket-core template requires `sdk v0.5.0`, replacing its stale
  pre-tag `v0.0.0` instruction. Its nested store templates keep the emitted
  core's sibling `v0.0.0` + local replace, but pin published dependencies to
  real tags: `sdk v0.5.0`, `integrations/datastores/pgxdb v0.5.0`, and
  `integrations/datastores/turso v0.3.0`.

T9 cold-resolves **every one of the 19 tags** with `GOWORK=off`; a sample is not
enough because workspace-local resolution is deliberately masking until then.

Old tags stay. They are immutable history and still resolve at the old path
(the Go proxy serves `features/cms@v0.1.0` from the commit that tag points at).
No consumer is force-moved by this train — they move when they repin.

### D4 — Makefile and guards

- `MODULES`, `STORE_MODULES`, the templ and pgx legs: path substitution.
- Guard targets rename: `guard-feature-isolation` → `guard-pocket-isolation`,
  `guard-feature-core-sdk-only` → `guard-pocket-core-sdk-only`,
  `guard-feature-transport-sdk-web` → `guard-pocket-transport-sdk-web`,
  `guard-feature-no-cross-feature` → `guard-pocket-no-cross-pocket`,
  `guard-store-no-foreign-feature` → `guard-store-no-foreign-pocket`. Their
  regexes move from `features/` to `pockets/`; their echo/error text follows.
  Comment headers keep the historical `(FS1, feature-standard 2026-07-07)`
  provenance lines verbatim.
- **New guard `guard-no-legacy-features-path`** (sibling of
  `guard-no-legacy-path`), the enforcement of "removed completely": fails on
  (a) any `gopernicus/features/`, `sdk/feature`, `features/` path, or phrase
  `new feature` in **any tracked living file**, regardless of extension
  (`.tmpl`, `.tsx`, `.yml`, `.sql`, and `go.sum` are known necessary coverage,
  not exceptions); (b) the existence of top-level `features/`, `sdk/feature/`,
  Workshop's legacy `feature.go`/`feature_scaffold_test.go`/`templates/feature/`,
  or the old docs filenames/directories; and (c) tier-derived identifiers known
  today (`eventsfeature`, composer `feature.` call sites). Use `git grep` so
  ignored/untracked dependency trees are not scanned. Exclude only ratified
  history (`plans/`, `.claude/plans/`, `.claude/past/`) and all of
  `RELEASING.md`, whose release chronicle intentionally names immutable old
  tags; T7 separately audits RELEASING's living inventory/tagging-scheme
  sections. Wired into `guard` (and therefore `check`).
- There are **nineteen** guard targets before this change; the new guard makes
  **twenty**. Update the live counts in README and Makefile as well as any
  ARCHITECTURE count found in T5. Historical counts in NOTES stay historical
  only when the surrounding entry is clearly dated/as-executed.

### D5 — Workshop CLI and templates

- Verb: `gopernicus new pocket <name> --module …`. `new feature` is **removed**,
  not aliased (**ruled 2026-08-27, Q4: no alias**; the independently tagged
  command module is stdlib-only and its only current users are the owner and
  the docs). `commands_test.go`'s `{"new feature stub", …}` case becomes the
  `new pocket` case; a new case asserts `new feature` exits non-zero with the
  usage text (the verb must be gone, not merely undocumented).
- `templates/feature/` → `templates/pocket/`; `feature.go` → `pocket.go`,
  `feature_scaffold_test.go` → `pocket_scaffold_test.go`; identifiers
  `featureParams`, `scaffoldFeatureParams`, `buildFeatureParams`,
  `emitFeature`, `runNewFeature`, `newFeatureUsage`, `featureTemplates`,
  `TestScaffoldFeature*`, `assertFeatureGuardShapes`, the `Feature` field
  → `pocket*` / `Pocket`. The emitted scaffold's own import paths reference
  `sdk/pocket`. Usage text and `commands.go:58` ("Scaffold a new feature")
  follow.
- The CLI's `templates/init/**` tree follows too: its emitted host README,
  `main.go`, migration-ledger prose, composer snippets, headings, and
  placeholders say pocket/`pocket.Mount`. The rename is not complete if
  `gopernicus init` immediately emits the retired vocabulary.
- The emitted pocket core's `go.mod` pins `sdk v0.5.0` (the first tag that owns
  `sdk/pocket`) and drops the obsolete "no version tags yet" instruction. T6
  uses a temporary local sdk replace because the tag does not exist before
  merge; T9 repeats the emission cold from the tagged CLI with no replace.
- The emitted host-side tree's directory name is whatever `--module`/name the
  host chose today; this plan does not change the scaffold's shape.

### D6 — Documentation

- `features/README.md` → `pockets/README.md`; title "Pockets — the gopernicus
  pocket contract"; body vocabulary per D1; FS ids kept with the one-line note.
- `ARCHITECTURE.md`, `README.md`, `NOTES.md`: paths + vocabulary. The
  sdk-layering law line becomes "pocket/ = the one sanctioned composer".
- `RELEASING.md`: (a) the tagging-scheme examples move to `pockets/…`;
  (b) ONE new upgrade note at the top of "Upgrade notes" — "2026-08-27: the
  `features/` tier is `pockets/` (module-path rename; ONE train of 19 tags)" —
  carrying the D3 table, the sed mapping from §Adoption, and the
  `sdk/feature`→`sdk/pocket` line; (c) **every existing upgrade-note heading
  and body stays verbatim** — they describe tags that exist at those paths.
- Docs site: `docs/features/` → `docs/pockets/`, sidebar category "Pockets",
  `guides/create-feature` → `guides/create-pocket`,
  `architecture/feature-contract` → `architecture/pocket-contract`, navbar
  label/`activeBaseRegex`. **The site is published** (GitHub Pages workflow +
  live configured URL, confirmed in Codex review), so add
  `@docusaurus/plugin-client-redirects` plus lockfile changes and redirects for
  all eight retired ids: `features/overview`, the five named pocket pages,
  `guides/create-feature`, and `architecture/feature-contract`.
- `.claude/agents/*.md` (10 charters): vocabulary only; no behavior change.
- `plans/`, `.claude/plans/` (including this file's siblings), `.claude/past/`:
  **untouched** — ratified/executed text is history (memory:
  ratification-contract).

### D7 — Generated files

19 `_templ.go` files carry the old import path. Per the repo law (never edit
generated files) they are **regenerated** (`make generate` → `templ generate`
in the two views modules) after the `.templ` sources are updated, not sed-ed.
The templ-drift check in `make check` is the proof.

## Compatibility

- **Source-breaking for every consumer** at the moment they repin — import
  paths and the `sdk/pocket` package. No behavior, schema, config, route, or
  wire change anywhere. No migration.
- **Consumers on local `replace` directives break the instant `features/`
  moves on disk** — gps-360-go, loremanac, venona-platform all `replace` to
  absolute/relative `…/gopernicus/features/…` paths. Their `go build` fails
  until their `go.mod` is edited, regardless of tags. This is the one
  non-optional downstream action and it is listed first in §Adoption.
- Consumers on tags (segovia v2, coordination-hub) are unaffected until they
  choose to repin.

## Tasks

Executor: `implementer` agent (Opus per subagent-model-policy). Every task
ends with its verify line green before the next starts. One branch,
`rename-features-to-pockets`; one implementation PR through T8; squash is fine
— `git mv` history survives a squash because the rename is detected on the tree
diff. T9 is the post-merge tag operation. T10 is necessarily a second, small
post-release bookkeeping PR because it records T9's actual tag results.

| # | Task | Verify |
|---|---|---|
| T1 | `git mv features pockets`; `git mv sdk/feature sdk/pocket`; package clause `package feature` → `package pocket` in the 4 files; rename composer `feature.go`/`feature_test.go` → `pocket.go`/`pocket_test.go`; `git mv features/README.md` is implicit | `git status --short` + `git diff --summary` show detected renames; no `sdk/pocket/feature*.go` survives |
| T2 | Module identities **and release pins** per D3 invariant: every `module`, `require`, and local `replace` under `pockets/**`, `examples/**`, `go.work`; Workshop emitted-template pins; confirm no `integrations/*` or `ui/goth` go.mod mentions `features/` | `git grep -n 'gopernicus/features/' -- '**/go.mod' go.work` → empty; script/assert every core→sdk and adapter→new-core pin equals D3; document that workspace builds are not cold proof |
| T3 | Import paths in `*.go` (non-generated), `*.templ`, and tracked templates: `gopernicus/features/` → `gopernicus/pockets/`; `sdk/feature` → `sdk/pocket`; composer identifier `feature.` → `pocket.`; `eventsfeature` → `eventspocket`; all tier-derived code identifiers follow (compile alone will not catch valid-but-retired aliases) | per-module `go build ./...` through `go.work`; structural `git grep` zero for old imports/aliases outside history |
| T4 | Regenerate templ (D7); `go work sync`; `go mod tidy` in every module (go.sum entries for old paths drop out) | `make generate`; inspect `git diff -- '*_templ.go'` (not only `--stat`) and prove generated hunks are import/package-path substitutions; templ-drift leg green |
| T5 | Makefile per D4 incl. the all-tracked-file legacy guard; exact guard count becomes 20 in Makefile/README/ARCHITECTURE living text; `.github/workflows/check.yml` follows | `make guard`; deliberately plant each of: old import in scratch `.go`, old docs path in tracked scratch `.tsx`, old path in tracked scratch `.sql`, and a legacy directory/artifact → guard fails each → remove; then `make check` green |
| T6 | Workshop CLI per D5, including `templates/pocket/**`, `templates/init/**`, real published dependency pins, and removal-asserting command test | `cd workshop/gopernicus && go test ./...`; emit with `go run . new pocket demo --module example.com/x` outside the repo, add a temporary absolute sdk replace because `sdk/v0.5.0` is not tagged pre-merge, then `GOWORK=off go build ./...`; separately build/test both nested emitted store modules using the scaffold test's explicit local replaces; `new feature` exits non-zero |
| T7 | Docs per D6: README, ARCHITECTURE, NOTES, pockets/README, RELEASING living sections + new note, published docs site + eight redirects, 10 agent charters, SQL comments | `make docs-build`; `git grep` (tracked files only) proves structural legacy strings zero outside named history; a separate case-insensitive vocabulary audit lists survivors by file/category (FS provenance, CMS `Featured`, genuine app-feature sense) in the PR body for owner veto |
| T8 | Full verify: `make check`; then **real behavior**: run `examples/auth-cms` and `examples/jobs-minimal`, hit a mounted route on each (login page renders; jobs enqueue → runs) | curl transcripts in the PR body |
| T9 | **Tag (never “retag”)** after merge to `main`, pushing in D3 dependency order: sdk → five cores + Workshop → every store/view. After each tier is pushed, cold-resolve **all 19 tags** outside the checkout/workspace with `GOFLAGS=-mod=mod GOWORK=off`; install `workshop/gopernicus@v0.2.0`, emit a pocket, and build it with no replaces | manifest loop proves every tag resolves; cold host mounts `pocket.Mount` and imports every core plus at least one adapter/view per available family; tagged CLI emits a cold-buildable core; immutable old tags still resolve at old paths |
| T10 | In a **separate post-release bookkeeping commit/PR** (T9 necessarily occurs after the implementation PR merges), copy this plan to `plans/` as executed (repo convention, cf. commit 0e96642) and update memory index | executed copy records merge SHA, all 19 pushed tags, and cold-resolution transcript; this exception replaces the earlier “one PR” claim for T10 only |

## Adoption (downstream; owner-driven, after T9)

Owner states three live consumers (2026-08-27). Five `go.mod` files reference
`gopernicus/features/`; the two not named live — loremanac, venona-platform —
are treated as **dormant** here: their `replace` directives will dangle after
T1, which is harmless until someone builds them, at which point the complete
path + alias + version sequence below is required. Owner to say if either is
actually live.

Mechanical mapping for every consumer (paths first, then versions):

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
# 4. versions per D3; inspect BOTH sides of every replace, then tidy/resolve.
go mod edit -json
go mod tidy
go list -m -json all
```

For a consumer with more than one `go.mod`, run the sequence in each module.
The owner must inspect both JSON outputs: the acceptance condition for local-
replace consumers is that neither a replacement `Path`/RHS nor any resolved
module `Dir` contains `/features/`.

| Consumer | Mode | Current old-path pins | Action |
|---|---|---|---|
| gps-360-go | **local replace (abs paths)** | authentication v0.6.0, …/stores/pgx v0.4.0, authorization v0.5.0, …/stores/pgx v0.2.0 | **Immediate** path edit on the day T1 lands, or its build breaks; repin to v0.7.0 / v0.5.0 / v0.6.0 / v0.3.0 when convenient |
| loremanac (dormant?) | **local replace (relative)** | authentication, …/stores/pgx, authorization, …/stores/pgx, jobs, …/stores/pgx (all v0.0.0) | Path edit whenever next built |
| venona-platform (dormant?) | **local replace (abs) + tags** | authentication v0.3.0, authorization v0.1.0 (+ stores via replace) | Path edit whenever next built |
| segovia v2 | tags | authentication v0.5.2, …/stores/pgx v0.3.0, …/views/goth v0.2.2, authorization v0.1.0, …/stores/pgx v0.1.0 | Repin as part of the pending Segovia sync leg (`.claude/plans/segovia-lessons/gopernicus-sync-handoff.md` gets one more checklist line) |
| coordination-hub | tags | authentication v0.4.2, …/stores/pgx v0.3.0, authorization v0.1.0, …/stores/pgx v0.1.0, events v0.1.0, …/stores/pgx v0.1.0, jobs v0.2.0, …/stores/pgx v0.2.0 | Repin at leisure; bundle with the open Model→RelationshipModel adoption |

## Non-goals

- Any behavior, API-shape, schema, route, config, or wire change. If a diff
  hunk isn't a path/word substitution or a regeneration, it doesn't belong.
- Renaming the FS/G rule ids, rewriting ratified plans, executed-plan copies,
  RELEASING history, or git tags.
- A compatibility shim of any kind — ruled out (Q1).
- Renaming the top-level `examples/`, `integrations/`, `workshop/`, or `ui/`
  directories — this plan renames the third tier plus the Workshop command and
  template artifacts that explicitly expose that tier.
- Doing the downstream repins from this repo. §Adoption is a handoff.

## Open questions for the owner — RULED 2026-08-27

- **Q1 — alias shim for `sdk/feature`?** **No.** "Make sure we cleanly remove
  features completely." → D2 hard rename; the new guard (D4) also polices
  living docs and the `sdk/feature/` directory itself.
- **Q2 — version numbering on the new paths?** **Continue lineage** (D3 table
  stands).
- **Q3 — `sdk` composer rename in the same train?** Owner asked what this
  meant; answered: `sdk/feature` lives in the separately-tagged `sdk` module,
  so renaming it means cutting `sdk/v0.5.0`. Q1's "no shim" forces it into the
  same train (every pocket core imports `sdk/pocket`). **Same train.**
- **Q4 — keep `new feature` as a CLI alias?** **No.** D5 adds the
  removal-asserting test.
- **Review gate:** Codex review completed and findings were folded into this
  revision on 2026-08-27. Execution starts on the owner's word, not merely on
  the existence of this amended text.

## As executed — 2026-08-27

- **Implementation PR:** gopernicus/gopernicus#7, squash-merged to `main` as
  `aaaf5c8` (two branch commits `f2ab5f3` T1–T4, `b4ef070` T5–T7; 926 files).
  `make check` green (templ drift zero, per-module vet/build/test, all 20 guards
  incl. G20 `guard-no-legacy-features-path`); CI green on both runs.
- **T8 real behavior:** `examples/auth-cms` — `/auth/login` 200 renders
  "Sign in — Gopernicus CMS", `/articles` 401-gated, log `registered
  authorization pocket` / `registered events pocket`; `examples/jobs-minimal` —
  `POST /enqueue demo.print` 200 → worker log `job completed`.
- **T9 tags** (all annotated, at `aaaf5c8`, pushed sdk → cores + workshop →
  adapters):

```
OK   github.com/gopernicus/gopernicus/sdk v0.5.0
OK   github.com/gopernicus/gopernicus/pockets/authentication v0.7.0
OK   github.com/gopernicus/gopernicus/pockets/authorization v0.6.0
OK   github.com/gopernicus/gopernicus/pockets/cms v0.2.0
OK   github.com/gopernicus/gopernicus/pockets/events v0.2.0
OK   github.com/gopernicus/gopernicus/pockets/jobs v0.3.0
OK   github.com/gopernicus/gopernicus/workshop/gopernicus v0.2.0
OK   github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx v0.5.0
OK   github.com/gopernicus/gopernicus/pockets/authentication/stores/turso v0.4.0
OK   github.com/gopernicus/gopernicus/pockets/authentication/views/goth v0.3.0
OK   github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx v0.3.0
OK   github.com/gopernicus/gopernicus/pockets/authorization/stores/turso v0.2.0
OK   github.com/gopernicus/gopernicus/pockets/cms/stores/pgx v0.3.0
OK   github.com/gopernicus/gopernicus/pockets/cms/stores/turso v0.2.0
OK   github.com/gopernicus/gopernicus/pockets/cms/views/goth v0.2.0
OK   github.com/gopernicus/gopernicus/pockets/events/stores/pgx v0.3.0
OK   github.com/gopernicus/gopernicus/pockets/events/stores/turso v0.2.0
OK   github.com/gopernicus/gopernicus/pockets/jobs/stores/pgx v0.4.0
OK   github.com/gopernicus/gopernicus/pockets/jobs/stores/turso v0.3.0
```

- **Cold resolution transcript** (outside the checkout, `GOWORK=off
  GOFLAGS=-mod=mod`, proxy.golang.org):

```
--- old tags still resolve at old paths (immutable history):
--- cold host: imports every core + one adapter/view per family, mounts pocket.Mount
OK   cold host builds
--- resolved dirs must not contain /features/:
--- pins:
--- tagged CLI: install, emit, build with NO replaces
OK   emitted core builds cold (no replace)
OK   emitted stores/pgx builds cold
OK   emitted stores/turso builds cold
OK   new feature rejected by tagged CLI
RESULT fail=0
```

  The cold host imported every core, every store adapter, both views modules and
  `sdk/pocket`, built, and `go list -m -json all` showed zero resolved `Dir`
  containing `/features/`. Old tags (`features/cms@v0.1.0`,
  `features/authentication@v0.6.0`, `sdk@v0.4.2`) still resolve at their old
  paths. `go install …/workshop/gopernicus@v0.2.0` emitted a pocket whose core
  and both store modules built with no replace directives; `new feature` exits
  non-zero. (The installed binary prints `gopernicus 0.0.0-dev` — the version
  string is ldflags-only and pre-existing, not a rename defect.)

### Deviations from the ratified text (owner-visible in the PR body)

1. **D1's SQL-comment edits were NOT shipped.** Both migration runners
   (`integrations/datastores/pgxdb/migrate.go`, `…/turso/migrate.go`) SHA-256
   the whole migration file, comments included, and hard-fail a migrated host
   on mismatch with no repair path; the plan's "migration behavior stays
   byte-identical" premise did not hold. Shipped migrations are immutable
   bytes. G20 excludes `pockets/*/stores/*/migrations/*.sql` with the reason
   recorded in the Makefile.
2. **`NOTES.md` and `examples/cms/NOTES.md` are G20-excluded** — every hit is
   inside a dated EXECUTED/CLOSED entry (same class as `RELEASING.md`).
3. **D6's docs-site redirects were dropped** by owner ruling (no
   `@docusaurus/plugin-client-redirects`; lockfile untouched).
4. `.claude/agents/*.md` charters were updated on disk; `.claude/` is
   gitignored so they are not in the merged diff.
5. gofmt import-order drift from the rename (120 files) was repaired inside the
   train; the Makefile has no gofmt leg (residual unformatted count 18 = the
   pre-rename baseline).
6. Small living-text extras: "not a new feature route" → "pocket route"
   (`deliveryhealth.go`); `registered X feature` log strings → `pocket`;
   stale "four layering guards" counts in `.github/workflows/check.yml` and
   `examples/cms/README.md` → twenty.

### Left as found (not this train's)

`examples/goth-showcase/go.mod` v0.0.0 placeholder pins; Workshop
`templates/init/go.mod.tmpl` connector pin `v0.1.0` (builds; stale) and
"pre-tag" prose in `init.go`; ARCHITECTURE "thirty-six" vs RELEASING
"thirty-seven" modules; uneven turso-connector pins across adapters.

### Downstream (owner-driven, per §Adoption)

gps-360-go's absolute `replace` paths to `…/gopernicus/features/…` dangle as
of `aaaf5c8` on `main`; segovia v2 and coordination-hub repin at leisure with
the sed mapping above.
