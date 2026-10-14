# gps-360-go: repin authorization to v0.26.0

Paste everything below the line into a session working in
`/Users/jrazmi/code/gps/three-sixty/gps-360-go`. This was prepared on 2026-10-14 from a read-only
look at that checkout, which was on branch `josh/dev-binary-serves-spa` at `430fb138`.

---

Repin gps-360-go's Gopernicus authorization dependencies from `v0.21.0` to `v0.26.0`. That span
crosses five minor releases. Two of them add **required authorization migrations**, and one
changes the Redis tuple-cache constructor. Plan it in this repo's numbered `plans/NN-*.md`
format first, then implement and verify it locally.

## Before you edit

- Check the branch, worktrees, working tree and `go.mod` pins. The snapshot below may be out of
  date. Never downgrade a pin that is newer than expected without first reading why it moved.
- **Overlap check.** A broader "Gopernicus audit upgrade" handoff for this repo exists in the
  framework checkout (`plans/gps-360-go-audit-upgrade-handoff.md`). If that upgrade is already in
  progress on a branch here, do not start a parallel one. Stop and report, so the owner can
  choose to fold this repin into it.
- Branch from the repository's main branch, for example `chore/authorization-v0.26`. Never commit
  to main.
- Not authorized: push, PR, deploy, or running migrations on any shared or production database.
  Use disposable local PostgreSQL and Redis only.
- Framework checkout for reading docs: `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus`.
  Read the following in full before planning:
  - `pockets/authorization/stores/UPGRADE.md`, the sections v0.22.0, v0.23.0, v0.24.0 and v0.26.0;
  - `pockets/authorization/stores/goredis/README.md`, the sections "Redis key", "One binding per
    authorization database" and "Mirror expiry and orphans";
  - `RELEASING.md`, the entries for the invitation grant operation ledger, invitation terminal
    settlement and direct-hit short circuit.

## Target pins

| Module | Current | Target |
|---|---|---|
| `pockets/authorization` | v0.21.0 | **v0.26.0** |
| `pockets/authorization/stores/pgx` | v0.16.0 | **v0.18.0** |
| `pockets/authorization/stores/goredis` | v0.4.0 | **v0.5.0** (required) |
| `pockets/authorization/stores/turso` (indirect) | v0.14.0 | whatever `go mod tidy` selects |

`pockets/authentication` v0.14.0 does not import authorization, so this repin does not force an
authentication bump. Leave every other Gopernicus module alone unless the build forces a change.
If it does, stop and report it. This repo **vendors** dependencies, so run `go mod vendor` after
tidying and commit the vendor tree.

## What spans v0.21 → v0.26

**v0.22.0: outcome cleanup.** `mutations.OutcomeInvariantBlocked` and `Outcome.Rejection` are
removed. Grep for both; integrity refusals arrive as the `ErrInvariantBlocked` error.

**v0.23.0: operation ledger. Migration required.** The host uses
`authorizationpgx.Repositories` (`workshop/devstack/main.go`, `workshop/permissionimport/apply.go`,
`pockets/auth/outbound`), so it **must** apply the additive `0003_iam_operations.sql` from the
pgx store. Copy only that file into `workshop/migrations/auth/` as the next entry after
`0021_iam_tuple_cache.sql`, for example `0022_iam_operations.sql`. Match the host's own numbering
and never edit an applied file. Segovia v2 did exactly this, as its `0022`/`0023`.

**v0.24.0: operation fences. One-way migration, required.**
- Copy `0004_iam_operations_fenced.sql` as the next entry, for example
  `0023_iam_operations_fenced.sql`.
- Its `ALTER TABLE` takes an ACCESS EXCLUSIVE lock. Run it in its own migrate run with a lock
  timeout (`options=-c lock_timeout=5s` in the DSN).
- Both schema probes are exact-match. After 0004, an older binary refuses to construct, and the
  new binary refuses a schema without 0004. The deploy must therefore follow UPGRADE.md's
  hard-cutover order: freeze scale-out, migrate, roll immediately, and never let an old revision
  start again.
- No Fencer is wired here, so the rollout stays reversible (phase 1 in UPGRADE.md).
- Write this deploy order into the rollout handoff; do not perform it.

**v0.25.0: the tuple-cache mirror is keyed on its binding.**
- `goredis.NewTupleCache(client, namespace, opts...)` becomes `NewTupleCache(client, opts...)`.
- The edit is in `cmd/server/authorization_cache.go`, at about line 139:
  `tuplecacheredis.NewTupleCache(client, cfg.Namespace)` becomes
  `tuplecacheredis.NewTupleCache(client, tuplecacheredis.WithNamespace(cfg.Namespace))`.
- Update `authorization_cache_test.go` and `workshop/authbench` if they use the old signature.
- Keep `AUTH_CACHE_NAMESPACE` as it is. Making it optional is a possible follow-up, not part of
  this change.
- Rollout: Redis keys move from `tuplecache:v2:` to `tuplecache:v3:`. Old and new pods rebuild on
  every poll while they overlap, so deploy quickly or toggle the cache off and on around the
  deploy.
- After the fleet is on v3, delete the persisted `tuplecache:v2:*` key once.
- Every replica must run the same `MaxStaleness`.

**v0.26.0: direct-hit short circuit.** No code or migration change is needed.
- Principals with many grants on small resources get much faster: in the framework benchmark,
  about 2.4 ms became about 115 µs on PostgreSQL. This is the shape gps-360-go has.
- A miss on a relation that admits usersets pays one extra round trip, about 30 µs.
- With the tuple cache on, a resource with very many direct grantees costs about 1 ms per check.
- A principal over `MaxGraphStates` with a direct grant now gets a grant instead of a 503.
- Explain steps for a hit carry `Outcome: direct_hit`. Grep for any code that compares explain
  step outcomes.
- After any bulk load of `iam_tuples`, run `VACUUM ANALYZE iam_tuples`.

## Steps

1. Poll the proxy `.info` URLs for `pockets/authorization@v0.26.0`, `stores/pgx@v0.18.0` and
   `stores/goredis@v0.5.0` first. All three are already published.
2. Run `go get` for the three targets, then `go mod tidy` and `go mod vendor`. Do not use the
   gopernicus `go.work`.
3. Add the two host migrations, make the `NewTupleCache` edit, and fix anything that the v0.22
   removals break.
4. Run `go build ./...`, `go vet ./...` and `go test ./...`, including live and integration
   tests against a disposable local PostgreSQL with the full host migration stream applied from
   scratch. Also apply the new migrations on top of a database created at the *old* stream, to
   prove the upgrade path.
5. Real-behaviour check, which is required:
   - boot the server locally with the cache enabled against a disposable Redis and Postgres;
   - drive authorized requests, at least one allowed by a direct grant, one allowed through a
     group or parent if the model has one, and one denied;
   - confirm `redis-cli --scan --pattern 'tuplecache:v3:*'` shows one mirror;
   - confirm the logs show no `ErrBinding` and no fallback storm.
   Repeat with the cache disabled.
6. Write the rollout handoff:
   - the migration order, and 0004's lock and cutover rules;
   - the cache-key rollout and the cleanup of the v2 key;
   - what was verified and what was not.
7. Report:
   - the pins changed;
   - the files edited;
   - the migrations added, with their host numbers;
   - each verify leg and whether it ran or skipped;
   - the real-behaviour results.

Close with the owner's standard block: a status board, numbered **Next steps** with
**YOUR CALL** items, **Check manually**, and **Run next**.
