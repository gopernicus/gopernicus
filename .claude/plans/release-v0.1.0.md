# gopernicus — release v0.1.0: first tags for all importable modules

Status: RATIFIED 2026-08-14 (tag all 33; keep coordination-hub vendoring; plain tag messages)
Date: 2026-08-14
Driver: coordination-hub gains collaborators (Jake, Jim); pinned tags must replace the my-machine-only `replace` → local-checkout wiring.

## Preflight facts (verified 2026-08-14)

- Repo `github.com/gopernicus/gopernicus` is PUBLIC, default branch `main`, local main synced with origin. Zero tags local AND remote (`git ls-remote --tags` empty) — every tag below is a first tag.
- The old monolith (`github.com/gopernicus/gopernicus v0.5.4`) tagged only the repo-ROOT module path; this repo's root has no go.mod and every module is a nested path, so no collision. RELEASING.md's split-workspace migration note covers hosts still on the monolith.
- 33 importable modules (37 minus the four `examples/*`, which are never tagged): `sdk`, 14 integrations, 5 feature cores, 10 feature store modules, 2 feature views modules (`authentication/views/goth`, `cms/views/goth`), `ui/goth`, `workshop/gopernicus`.
- 29 module go.mods carry relative `replace` directives (precondition 2 of RELEASING.md: these must be dropped and siblings pinned). `sdk` and `workshop/gopernicus` have none (stdlib-only); a few integrations may also be clean — the script processes whatever it finds.
- RELEASING.md's semver-floor notes all collapse into first tags: pre-v1, breaking-vintage acknowledged; **v0.1.0 across the board**. `v1.0.0` is a deliberate later act per module.

## Release mechanics (why this order works)

Tags are just git refs: edit every go.mod to `require` siblings at `v0.1.0` (no replace), commit, then cut ALL 33 tags on that one commit and push. Each nested tag then satisfies the requires in every other module. Local dev is unaffected throughout — `go.work` resolves siblings by directory and ignores versions. go.sum entries for siblings are not needed in the framework modules themselves (only a main module's go.sum is consulted; consumers record their own hashes via the proxy).

## Tasks

### R1 — Gate: `make check` green on clean main
Full build/vet/test + seventeen layering guards, before any edit. Abort on red.

### R2 — Drop replaces, pin requires
For each of the 33 module go.mods (examples excluded, untouched):
- `go mod edit -dropreplace=<path>` for every `github.com/gopernicus/gopernicus/*` replace;
- `go mod edit -require=<path>@v0.1.0` for every gopernicus sibling already in `require`.
Scripted (loop over go.mods, parse existing replaces), then `gofmt`-clean check of nothing (go.mod only). Examples keep their replaces — they are demonstrations pinned to the workspace.

### R3 — Gate: `make check` green again
The workspace still resolves siblings locally; the scaffold-compile tests inject their own absolute replaces and stay hermetic. Any failure here is a real regression from R2 — fix or abort.

### R4 — Docs: RELEASING.md + workshop caveat
- RELEASING.md: replace "No tags have been cut yet" with the v0.1.0 release record (date, tag list pointer, this plan).
- workshop/gopernicus README + init templates still emit the PRE-TAG replace caveat; update the README table note and `go.mod.tmpl`/`readme.md.tmpl` guidance to "require @v0.1.0" with the replace instructions demoted to a framework-dev note. (Scaffold-compile tests pin emitted content — run `make check` again after; if template edits ripple beyond a doc-comment swap, split to a follow-up rather than bloat this release.)

### R5 — Commit, tag, push
- One commit: "Release v0.1.0: drop pre-tag replaces, pin sibling requires".
- 33 annotated tags at that commit: `<module-dir>/v0.1.0` (e.g. `sdk/v0.1.0`, `features/jobs/stores/pgx/v0.1.0`, `ui/goth/v0.1.0`, `workshop/gopernicus/v0.1.0`).
- Push main + all tags. NOTE: once the proxy serves a tag it is immutable — never retag; mistakes become v0.1.1.

### R6 — Post-verify from a cold cache
In a scratch module with a fresh GOMODCACHE and GOWORK=off:
- `go get github.com/gopernicus/gopernicus/features/authentication/stores/pgx@v0.1.0` (transitively proves sdk, pgxdb, feature core resolve);
- `go install github.com/gopernicus/gopernicus/workshop/gopernicus@v0.1.0` (the CLI installs the tagged way).

### R7 — Migrate coordination-hub to tags
In /Users/jrazmi/code/gps/coordination-hub:
- drop all 11 `replace` directives; `go mod edit -require=<path>@v0.1.0` each; `go mod tidy && go mod vendor`;
- `make build vet guard` + boot against docker postgres + `/healthz` + login smoke (the M1 verification subset);
- update README.host.md (pre-tag wiring paragraph → tagged requires; vendoring now optional but KEPT for offline builds — YOUR CALL 2);
- commit + push. From then on Jake/Jim need no framework checkout at all.

## YOUR CALLs

1. **All modules at v0.1.0** — including `cms`/`turso`/`oauth`/etc. that coordination-hub doesn't use. Tagging everything at once keeps one release commit and lets any module be required later; the alternative (tag only the 11 coordination-hub needs) leaves the repo half-released. Default: tag all 33.
2. **Keep vendor/ in coordination-hub** — with real tags, vendoring is no longer load-bearing (proxy resolves for everyone); keeping it preserves offline/hermetic builds at the cost of vendor-refresh churn in PRs. Default: keep vendoring.
3. **Tag messages** — plain "\<module\> v0.1.0" or carry each module's RELEASING.md upgrade-note headline. Default: plain (the notes live in RELEASING.md).

## Rollback boundary

Everything before R5's push is freely revertible. After push: tags fetched by the proxy are permanently cached — a bad release is corrected by v0.1.1, never by retagging.
