# Docs pass 2026-09-16: authorization first, then the whole site, plus the CI break

Status: EXECUTED 2026-09-16, uncommitted (owner asked for the pass directly in-session; docs-only
edits plus one test fix, all reversible). Not ratified before execution. Verified: sdk build/vet/test
green on macOS and in a linux/arm64 golang:1.26 container (RangeEdges fails before the fix, passes
after); `pnpm typecheck && pnpm build` green with onBrokenLinks: throw; authorization page
screenshot-checked in headless Chrome.

## Why

Since the 2026-09-09 framework audit and the authorization train (v0.13.0 → v0.21.0,
stores pgx v0.15.0 / turso v0.14.0 / goredis v0.4.0), the Docusaurus site and the
repo docs drifted. The authorization page (`workshop/documentation/docs/pockets/authorization.md`)
reads as a compressed README, not an explanation. CI on `main` has been red since
the audit release because of a Linux-only filestorage test, not a flake.

## Scope

1. **CI break** — `sdk/capabilities/filestorage`: `TestDisk_Conformance/RangeEdges`
   seeks to `MaxInt64`; Linux `lseek` returns `EINVAL` past the filesystem's max file
   size, macOS does not. Fix in `Disk.DownloadRange`: clamp the seek offset to the
   current file size (contract already says at/beyond EOF reads as empty). Verify in a
   Linux container before and after.
2. **Authorization docs** — rewrite the site page as a plain-language explanation of
   the decisions: tuples in `iam_tuples` as the single authority; authorization =
   "can this principal perform this action" (inbound, `Require`) vs IntegrityPolicy =
   "will the data be left acceptable" (store, every ordinary write); principal-free
   writers; audit rows in the same commit; TupleCache = triggers → `iam_tuple_outbox`
   → relay → Redis with `MaxStaleness` bounding staleness and durable SQL fallback;
   host-owned denial responses; decision logging; bundled role routes. Keep the README
   as the exhaustive reference; the site page explains and links.
3. **Whole-site drift** — three delegated audits (orientation/architecture; sdk/
   integrations/ui/workshop/guides; other pockets) report stale facts, broken links
   and clarity issues; apply factual fixes and the clarity items that are cheap.
4. Update cross-page authorization sentences (pockets overview, pocket contract,
   persistence guide) to the same vocabulary.

## Out of scope

- Rewriting the pocket README or AUDIT.md prose (reference material stays).
- Any Go API change. The only code change is the filestorage seek clamp.
- The owner's uncommitted `plans/*handoff*` edits.

## Verify

- `cd sdk && go build ./... && go vet ./... && go test ./capabilities/filestorage/`
- Linux: `docker run --rm -v "$PWD":/src -w /src/sdk -e GOWORK=off golang:1.26 go test ./capabilities/filestorage/`
- `cd workshop/documentation && pnpm typecheck && pnpm build` (onBrokenLinks: throw)
- Serve the built site and read the authorization page in a browser.

## Release (owner-directed 2026-09-16)

| Module | Previous | Release | Why |
| --- | --- | --- | --- |
| sdk | v0.9.0 | v0.9.1 | Linux `DownloadRange` seek clamp (patch) |
| pockets/authorization | v0.21.0 | v0.22.0 | removed `OutcomeInvariantBlocked`, `Outcome.Rejection` |
| pockets/authorization/stores/pgx | v0.15.0 | v0.16.0 | dropped dead `Rejection` calls |
| pockets/authorization/stores/turso | v0.14.0 | v0.15.0 | dropped dead `Rejection` calls |

Pins in stores and examples/auth-cms stay at core v0.21.0 (compatible minimum).
Steps: one commit on main; push; wait for `check` and the docs deploy; annotated
tags in dependency order, each pushed alone; poll the proxy `.info` URL before any
`go mod` command names a new version; cold-verify with `GOWORK=off` from a scratch
consumer module; record results below.

### Verification log
