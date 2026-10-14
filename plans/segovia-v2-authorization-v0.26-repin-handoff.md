# Segovia v2: repin authorization to v0.26.0

Paste everything below the line into a session working in
`/Users/jrazmi/code/segovia/segovia/v2`. This was prepared on 2026-10-14 from a read-only look
at that checkout, which was on branch `chore/v2-dev-binary-serves-ui` at `0a1339c9`.

---

Repin Segovia v2's Gopernicus authorization dependency from `v0.24.0` to `v0.26.0`. Bump the
Redis tuple-cache adapter to match. This is a dependency repin with one small code edit, not a
feature. Plan it briefly, implement it, and verify the real host behaviour.

## Before you edit

- Check the branch, worktrees, working tree and `go.mod` pins. The snapshot below may be out of
  date. Never downgrade a pin that is newer than expected without first reading why it moved.
- Branch from the repository's main branch, for example `chore/authorization-v0.26`. Never commit
  to main. Do not push, open a PR, deploy or touch a shared database unless the owner says so.
- Framework checkout for reading docs: `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus`.
  Read `pockets/authorization/stores/UPGRADE.md` (the v0.26.0 section),
  `pockets/authorization/stores/goredis/README.md` ("Redis key", "One binding per authorization
  database", "Mirror expiry and orphans") and `RELEASING.md` (the v0.26.0 entry).

## Target pins

| Module | Current | Target |
|---|---|---|
| `pockets/authorization` | v0.24.0 | **v0.26.0** |
| `pockets/authorization/stores/goredis` | v0.4.0 | **v0.5.0** (required) |
| `pockets/authorization/stores/pgx` | v0.18.0 | v0.18.0 (already latest) |
| `pockets/authorization/stores/turso` (indirect) | v0.14.0 | whatever `go mod tidy` selects |

Leave every other Gopernicus module alone unless the build forces a change. If it does, stop and
report it.

## What spans v0.24 → v0.26

**v0.25.0: the tuple-cache mirror is keyed on its binding.**
- `goredis.NewTupleCache(client, namespace, opts...)` becomes
  `NewTupleCache(client, opts...)`. The namespace is now the optional
  `goredis.WithNamespace(ns)`.
- The edit is in `cmd/server/authorization_cache.go`, at about line 187:
  `tuplecacheredis.NewTupleCache(client, cfg.Namespace)` becomes
  `tuplecacheredis.NewTupleCache(client, tuplecacheredis.WithNamespace(cfg.Namespace))`.
- goredis v0.4.0 cannot satisfy the v0.25+ `tuplecache.Backend` interface, so the goredis bump
  is required.
- Keep `AUTH_CACHE_NAMESPACE` as it is. Do not make it optional in this change; note it as a
  possible follow-up.
- Redis keys move from `tuplecache:v2:` to `tuplecache:v3:{<namespace>/<binding>}`.
- Rollout: old and new pods each rebuild on every poll while they overlap. Deploy quickly, or
  turn the cache off, deploy, and turn it back on.
- After the whole fleet runs v3, delete the persisted `tuplecache:v2:*` key once by hand.
- Every replica on one authorization database must run the same `MaxStaleness`.
- Mirrors now expire after 7 days by default (`WithExpiry`).

**v0.26.0: direct-hit short circuit.** No code or migration change is needed.
- `Direct` checks probe the principal's own tuple before expanding. Principals with many grants
  on small resources get much faster.
- A miss on a relation that admits usersets pays one extra round trip.
- With the tuple cache on, a resource with very many direct grantees costs about 1 ms per check.
  That is relevant to whether Segovia keeps the cache on.
- A principal over `MaxGraphStates` with a direct grant now gets a grant instead of a 503.
- Explain steps for a hit carry `Outcome: direct_hit`. Grep for any code that compares explain
  step outcomes; none was found at prep time.
- After any bulk load of `iam_tuples`, run `VACUUM ANALYZE iam_tuples`.

**No new authorization migrations.** Segovia already has `0022_iam_operations.sql` and
`0023_iam_operations_fenced.sql` under `workshop/migrations/auth/`. Confirm that, and do not
add anything.

## Steps

1. Poll `https://proxy.golang.org/github.com/gopernicus/gopernicus/pockets/authorization/@v/v0.26.0.info`
   and `.../pockets/authorization/stores/goredis/@v/v0.5.0.info` first. Both are already
   published.
2. `go get github.com/gopernicus/gopernicus/pockets/authorization@v0.26.0 github.com/gopernicus/gopernicus/pockets/authorization/stores/goredis@v0.5.0`,
   then `go mod tidy`. Do not use the gopernicus `go.work`; Segovia resolves published tags.
3. Make the `NewTupleCache` edit, and update `authorization_cache_test.go` and
   `workshop/authbench` if they call the old signature.
4. Verify with `go build ./...`, `go vet ./...` and `go test ./...`, including any live or
   integration tests the repo runs against a disposable local PostgreSQL and Redis.
5. Real-behaviour check, which is required:
   - boot the server locally with the cache enabled against a disposable Redis and Postgres;
   - drive a few authorized requests, at least one allowed and one denied;
   - confirm `redis-cli --scan --pattern 'tuplecache:v3:*'` shows exactly one mirror;
   - confirm the logs show no `ErrBinding` and no fallback storm.
   Repeat with the cache disabled.
6. Report the pins changed, the files edited, each verify leg and whether it ran or skipped, the
   real-behaviour results, and the rollout notes above for the deploy owner.

Close with the owner's standard block: a status board, numbered **Next steps** with
**YOUR CALL** items, **Check manually**, and **Run next**.
