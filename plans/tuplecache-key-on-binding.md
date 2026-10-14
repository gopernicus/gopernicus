# tuplecache: key the mirror on its binding, retire the namespace latch

**Status:** RELEASED 2026-10-14. PR #77 merged at fabc6261 and #76 is closed; authorization
v0.25.0 and stores/goredis v0.5.0 are tagged (see Build below). Previously RATIFIED 2026-10-14 (owner, in-session: "i agree with the fix we do need to do
that"; R1–R5 as recommended unless the owner says otherwise at build start; D7 added after
ratification from the same session's benchmark discussion). Answers
[gopernicus/gopernicus#76](https://github.com/gopernicus/gopernicus/issues/76) (owner-filed,
2026-10-14, from gps-360-go/Segovia production experience). Handoff prompt:
[tuplecache-key-on-binding-handoff.md](tuplecache-key-on-binding-handoff.md).

**Build (2026-10-14):** BUILT — PR [#77](https://github.com/gopernicus/gopernicus/pull/77)
(one PR, core + goredis commits; go.work CI forbids a core-only stacked PR). Tags pending owner:
authorization v0.25.0 at merge, then a pin commit, then goredis v0.5.0 at the pin commit. Approved
amendments F3, F4, F6, F7 (docs and verify leg; the repin itself follows the authorization tag)
and F8 were built at 1418fe94; F1/F2 option A docs at 1464c18d; F5, F9, F10 and F11 at b4645aa8; F12 at a054618e. All findings built. **RELEASED 2026-10-14:** #77 merged @ fabc6261 → `pockets/authorization/v0.25.0`;
pin PR #79 merged @ 63af377b → `pockets/authorization/stores/goredis/v0.5.0` (pgx/turso repinned,
no tag). Both cold-verified on the proxy first poll. #78 fixed the auth-cms resend-test race.

**Posture decided in-session (2026-10-14):** this is a framework fix, not a host-driven
release. Both hosts have a principal-heavy tuple shape (one principal, thousands of direct
grants) for which the current Redis encoding is a net loss (benchmarks: reads ~1 µs per tuple
in the set touched, deltas re-encode the whole set inside Redis; see D7). The owner's direction
is to turn the cache off in gps-360-go and Segovia and serve authorization from Postgres until
production numbers say otherwise. So: build and tag this, but no downstream repin is urgent,
and the "Downstream" section below is advisory. The successor work is
[per-route-lookup-strategy.md](per-route-lookup-strategy.md).

## Context

`tuplecache.New` derives a binding from the source identity and the freshness policy
(`pockets/authorization/logic/tuplecache/runtime.go:90`):

```
protocol:2/<source.Binding()>/max-staleness:<nanoseconds>
```

The goredis backend stores the mirror under a key the host types by hand
(`tuplecache:v2:{<namespace>}`, `stores/goredis/tuple_cache.go:91`). The runtime refuses a
mirror whose stored binding differs from its own (`runtime.go:193` → `ErrBinding`) and never
overwrites it. That is the right refusal, but it makes the hand-typed namespace a correctness
dependency: change `MaxStaleness`, the schema or the source identity row and every decision
evaluates durably until an operator picks a new namespace. gps-360-go latched on its first boot
after a staleness change; Segovia carries a `workflow_dispatch` job whose only purpose is
rotating the namespace.

Two facts the issue gets slightly wrong, verified in-session:

- **Orphans do not expire.** The permanent mirror is `PERSIST`ed on swap (`scripts.go:71`); only
  the temporary `:build:<nonce>` hash has a TTL. After a binding change the old key stays
  forever. The plan has to say what happens to it (R3).
- **Binding characters are only "already allowed" for the bundled SQL sources.** Custom
  `Source.Binding()` values (test sources return `test-store`, `redis-test`) can contain
  anything, including the braces that would escape the Redis hash tag. The backend must validate
  or reject (D2).

## Problem

Make it impossible for the host to type something that must equal the runtime's binding. Two
processes share a mirror exactly when their bindings are identical (same source identity, same
schema, same staleness policy); any difference lands in a different mirror. `ErrBinding` stays
as a defensive check. No change to the wire format, the Lua delta/rebuild logic, receipts, or the
SQL sources.

## Design

### D1 — `Backend` is keyed per call on the binding (authorization core)

The runtime already carries the binding in `State.Binding`, which `Read` and `Publish` receive
through `expected` / `next`. Only `State` lacks it. Change one signature:

```go
type Backend interface {
	// State returns the mirror published under binding, or the zero State when
	// none exists. ...
	State(ctx context.Context, binding string) (State, error)
	Read(ctx context.Context, expected State, keys []SetKey) ([][]tuples.Tuple, error)
	Publish(ctx context.Context, expected, next State, snapshot Snapshot, validFor time.Duration) error
}
```

Contract, written into the `Backend` doc comment: a backend holds one independent mirror per
binding. `State(ctx, b)` addresses mirror `b`; `Read` addresses `expected.Binding`; `Publish`
addresses `next.Binding` (`expected.Binding` is either empty, meaning "no mirror yet", or equal
to `next.Binding`, otherwise `ErrBinding`). The old sentence "The host supplies a dedicated
namespace" goes.

Runtime edits (`runtime.go`): `poll` calls `State(ctx, c.binding)`; `attempt` likewise. The
`before.Binding != "" && before.Binding != c.Binding()` → `ErrBinding` check stays, now meaning
"the backend returned another binding's mirror for my key", which is corruption or a
misbehaving backend. The `decisions` constructor check (`constructor.go:69`, reader binding vs
source binding) is unrelated and unchanged.

Why not `Bind(binding) (Backend, error)` or `New(source, func(binding) (Backend, error))`:
both leave a `*goredis.TupleCache` that is constructed but not yet usable, or change `New`,
`authorization.WithTupleCache`, `decisions.WithTupleCache` and `Config.TupleBackend` for
every host. One method signature is the smallest change that meets the requirement.

### D2 — goredis: optional namespace prefix, key derived from the binding

- `NewTupleCache(client *redis.Client, opts ...Option)`. The positional namespace goes;
  `WithNamespace(string) Option` adds an optional readable prefix. Per the owner's standing rule,
  an optional parameter is never positional.
- Key: `tuplecache:v3:{<namespace>/<binding>}`, or `tuplecache:v3:{<binding>}` when no namespace
  is given. Same character allow-list as today (`[A-Za-z0-9:._/-]`) applied to **both** the
  namespace (at construction) and the binding (per call, since a custom Source can return
  anything); a violation returns `sdk.ErrInvalidInput`. Braces stay reserved for the hash tag so
  the mirror and its `:build:<nonce>` hashes keep one slot.
- `key(binding string) (string, error)` replaces the stored `key` field. It is a short
  concatenation plus a byte scan on ~100 bytes per Redis round trip; no memoisation.
- `!schema` stays `2`, `tuplecache.Protocol` stays `2`: the wire format and the binding format
  are unchanged. The `v3` in the key is the adapter's key-layout version (R2).
- Lua scripts: unchanged. They only see `KEYS[1]`.

### D3 — memory backend mirrors the contract

`stores/memory/tuple_cache.go` keys its state and sets by binding (`map[string]*mirror`). This
keeps the reference backend honest, so `logic/tuplecache` tests prove the two-policies-one-backend
behaviour without Redis, and hosts that use `memory.NewTupleCache()` in tests (Segovia,
gps-360-go) see the same semantics as production.

### D4 — tests

`logic/tuplecache`:
- `TestSharedMirrorRejectsDifferentFreshnessPolicies` → `TestDifferentFreshnessPoliciesUseSeparateMirrors`:
  `short` and `long` on one backend both reach ready; `long.Stats().Rebuilds == 1`; after a
  revoke and `poll(short)`, `short` denies, `long` still allows from its own mirror (its
  accepted lag), then `poll(long)` denies. No `ErrBinding`.
- `TestPolicyBindingStartupAndWake`: the "publish under `other-store`, expect `ErrBinding`" step
  can no longer trigger through a correct backend. Replace it with a fake whose `State` returns a
  foreign binding; `Poll` → `ErrBinding`, reads stay durable. Binding string assertion unchanged.
- New: restarting a runtime with an unchanged binding against a populated backend reports
  `Rebuilds == 0` (delta resume). Covers acceptance bullet 3 at the runtime level.
- Fakes (`hookedBackend`, `capacityBackend`, `filterValidationBackend`) embed `Backend`, so the
  signature change is absorbed; only direct `backend.State(ctx)` calls in tests gain the
  binding argument.

`stores/goredis`:
- New `TestTupleCacheKeyLayout`: with and without namespace, the published key is exactly
  `tuplecache:v3:{ns/<binding>}` / `tuplecache:v3:{<binding>}` (assert via `--scan`-equivalent
  `KEYS`/`TYPE` on the test server); a binding containing `{` → `ErrInvalidInput` from `State`,
  `Read` and `Publish`; constructor rejects a bad namespace as today.
- New `TestTupleCacheBindingsAreIndependent`: two runtimes, one Redis, one source, staleness 1s
  and 5m, both ready, two keys, no `ErrBinding`; two runtimes with identical bindings share one
  key and acknowledge each delta exactly once (reuse the receipt CAS assertions from
  `TestTupleCacheCASAndAtomicIndexes`).
- `TestTupleCacheRestartPersistence` (AOF restart) adds the `Rebuilds == 0` assertion after
  restart.
- `TestCanonicalProtocolDoesNotReadOrMutatePreviousMirror` extends to a leftover `v2` key under
  the same namespace: untouched, unread.
- Existing constructor calls move to `NewTupleCache(client, WithNamespace("..."))`.

`stores/pgx` / `stores/turso` (memory backend, no Redis): changing the source identity row moves
the runtime to a new binding, which reads an empty mirror and rebuilds once; the old mirror is
left as is. Covers acceptance bullet 4. If `TestPostgresTupleCacheBatchAndRevocation` already
drives the identity change, extend it rather than add a test.

### D5 — docs

- `stores/goredis/README.md`: constructor example, "Redis key" section rewritten around
  `v3:{<namespace>/<binding>}`, namespace optional, the operator `--scan` recipe uses the
  namespace prefix, and a short "Orphaned mirrors" paragraph per R3.
- `pockets/authorization/README.md` ~596–614: `NewTupleCache(client, namespace)` →
  `NewTupleCache(client)` with the optional namespace mentioned; the sentence "source/policy
  mismatches fail with `tuplecache.ErrBinding`" becomes "different bindings use different
  mirrors; `ErrBinding` now only reports a backend returning a foreign mirror".
- `workshop/documentation/docs/pockets/authorization.md:274`: example updated.
- `stores/memory/tuple_cache.go` doc comment: one mirror per binding.
- `AUDIT.md` is a historical record; untouched.

### D6 — release

| Module | Bump | Why |
|---|---|---|
| `pockets/authorization` | v0.24.0 → **v0.25.0** | `Backend.State` signature (breaking for implementers, pre-1.0 MINOR) |
| `pockets/authorization/stores/goredis` | v0.4.0 → **v0.5.0** | constructor signature, key layout; repins authorization v0.25.0 |

`stores/pgx` and `stores/turso` implement `Source`, not `Backend`; no tag unless their test
changes require a repin (they use the memory backend from the authorization module they already
pin, so expected: none). Two stacked PRs (core, then goredis) or one PR with two tags; owner's
call at release time, as usual.

**Rollback (F9):** rolling back to v2 binaries reads the old `tuplecache:v2:{<namespace>}`
key, finds a stale receipt, and rebuilds once. Do not `DEL` the v2 key until the whole fleet
runs v3. Once it does, the v2 key is safe to delete manually. Old binaries `PERSIST`ed it, so
F5's expiry never reaches it.

### D7 — goredis README "Fit" section (added after ratification, 2026-10-14)

The adapter is correct but has a narrow performance fit. Adopters must choose knowingly. Add a
"Fit" section near the top of `stores/goredis/README.md` carrying the owner's benchmark run
(Apple M4 Pro, Unix-socket Redis, one principal holding N direct grants):

| Set size | one read | one delta | full rebuild |
|---|---|---|---|
| 100 | 0.11 ms | 0.41 ms | 0.29 ms |
| 1,000 | 0.95 ms | 3.7 ms | 2.7 ms |
| 10,000 | 9.4 ms | 38 ms | 25 ms |
| 100,000 | 96 ms | 426 ms | 283 ms |

State plainly:

- Each index set is one JSON field. A read decodes the whole set touched; a delta decodes,
  validates and re-encodes the whole set inside Redis's single thread.
- Good fit: many resources with small forward sets, principals with small reverse sets, and
  Postgres under read load from many replicas.
- Poor fit: a principal or resource with thousands of direct tuples. Graph checks are
  subject-first and read the principal's whole reverse set (see
  [per-route-lookup-strategy.md](per-route-lookup-strategy.md)). Prefer durable reads or the
  per-replica memory backend.
- Full rebuilds are cheap relative to deltas on large sets.

Docs only; no code change. Ships in the same goredis tag.

## Rulings

- **R1 — API shape:** `State(ctx, binding)` per-call keying (D1). Recommended. Alternatives
  (`Bind`, factory func) rejected above.
- **R2 — key prefix:** `tuplecache:v3:` (recommended) vs keep `v2`. `v3` makes the cut explicit,
  guarantees no overlap with any hand-typed namespace, and lets the README describe one layout.
  `Protocol`/`!schema` stay `2`.
- **R3 — orphaned mirrors:** the old `v2` key and any later superseded binding key persist.
  Recommended: **document, do not auto-expire.** The README gives
  `redis-cli --scan --pattern 'tuplecache:*:{<namespace>/*'` (and `'tuplecache:v2:*'` for the
  one-time cut) plus `DEL`. Alternative: a long renewing `PEXPIRE` (e.g. 30 days) on every
  publication so orphans lapse; rejected because a host down longer than the TTL loses delta
  resume and must rebuild, and it adds a Lua change to a script set this plan otherwise leaves
  alone. Owner may prefer the TTL; it is a one-line Lua addition if so.
- **R4 — namespace option name:** `WithNamespace` (recommended, keeps the host vocabulary
  `AUTH_CACHE_NAMESPACE` and the README term) vs `WithPrefix` (the issue's word).
- **R5 — binding validation in goredis:** reject disallowed bytes with `ErrInvalidInput`
  (recommended, surfaces a bad custom Source at the first poll with `FailureStage: "state"`) vs
  hash the binding (hides the SHA already in it and makes keys unreadable).

## Acceptance (from the issue, mapped)

1. Two runtimes, different `MaxStaleness`, one Redis, one source: both ready, different keys, no
   `ErrBinding` → `TestTupleCacheBindingsAreIndependent`.
2. Identical bindings share one key; each delta acknowledged once → same test, CAS assertions.
3. Restart with unchanged binding → `Rebuilds == 0` → `TestTupleCacheRestartPersistence` +
   runtime test.
4. Source identity change → new key, one rebuild → pgx/turso behaviour test.
5. README and constructor docs describe the namespace as optional → D5.

## Downstream (owner, after tags)

- **gps-360-go:** repin authorization v0.25.0 + goredis v0.5.0; `NewTupleCache(client,
  goredis.WithNamespace(cfg.Namespace))` with `AUTH_CACHE_NAMESPACE` optional; delete the
  `latched` state and the `ErrBinding` branch in `cmd/server/authorization_cache.go:100,253`
  (keep logging `ErrBinding` as an ERROR if it ever appears; it now means corruption); update
  `.claude/context/identity-and-authorization.md` and the two workshop READMEs; one-time
  `DEL` of the old `v2` key.
- **Segovia:** same repin; delete `.github/workflows/prod-segovia-authorization-cache.yml` and
  `v2/workshop/docker/rotate_authorization_cache.py`; `v2/cmd/server/authorization_cache.go:187`
  and `v2/workshop/authbench/build.go:90` take the option form.

## Verify

```sh
cd pockets/authorization && go build ./... && go vet ./... && go test ./...
cd pockets/authorization/stores/goredis && go build ./... && go vet ./... && go test ./... && go test -race ./...
cd pockets/authorization/stores/pgx && go test ./...     # needs the usual Postgres fixture
cd pockets/authorization/stores/turso && go test ./...   # memory-backed cache tests only
make guards   # layering guards; no new guard expected
```

goredis tests launch their own `redis-server`; they skip if the binary is absent, which must
be stated in the report rather than counted as a pass.

## Review findings (2026-10-14) — ALL APPROVED (F1/F2 as option A, F5 as renewing expiry, F11 added)

**Owner ruling, 2026-10-14, in the planning session:** F3, F4, F6, F7 and F8 are approved for
the build (owner's words: "4) ok", answering the list of key accessor, full repins, manual
Redis gate and shared backend suite, plus the F3 restart test). F1/F2 and F5 remain open
pending the owner's choice. F9 and F10 are not yet ruled. See "Owner rulings on review
findings" at the end of this section.

Reviewed after ratification by platform-sre and data-integration-reviewer. Both returned
ship-with-edits. Nothing above has been edited, because the plan is ratified. The items below
are proposed amendments, and each needs the owner's word before the build adopts it.

- **F1 (HIGH, both reviewers): two bindings on one source rebuild each other forever.** Pgx
  and turso keep a single receipt row (`stores/pgx/tuple_cache.go:231-255`, CAS at
  `tuple_source.go:148`; `stores/turso/tuple_cache.go:269`). Runtime A acks R0→Ra. B sees
  Rb≠Ra (`tuple_source.go:29`), rebuilds in full and acks Rb. A then rebuilds in full, and the
  cycle repeats every poll, even with no writes. Answers stay correct, but delta resume is lost
  for both runtimes and each poll costs a full SQL scan plus a Redis rebuild. This hits
  acceptance 1, a mixed v2/v3 rollout, and any live `MaxStaleness` change.
  **Proposed:** one source supports one steady-state binding. Different bindings may overlap
  only briefly and keep rebuilding until one leaves. Rewrite the Context sentence that says
  bindings "share exactly when identical", and the "no change to receipts" claim. Expose the
  state in stats, for example a count of Full snapshots taken when `before.Receipt != ""`. Add
  a rollout note: drain old binaries fast, or deploy with the cache off and then turn it on.
  File per-binding receipts and a per-consumer outbox as a separate issue, since they need a
  SQL migration.
- **F2 (HIGH): the D4 tests cannot see F1.** The runtime test fake also keeps a single receipt
  (`logic/tuplecache/runtime_test.go:39,46-58`). In the in-progress
  `TestDifferentFreshnessPoliciesUseSeparateMirrors`, `Rebuilds==1` holds only because the
  first poll is always full. **Proposed:** assert `long.Stats().Rebuilds == 2` and
  `LastPoll.Full` after the second `poll(long)`, which documents the ping-pong. Apply the same
  change in `TestTupleCacheBindingsAreIndependent`.
- **F3 (MEDIUM): `TestTupleCacheRestartPersistence` cannot assert `Rebuilds == 0`.** It
  exercises only the backend, with no runtime and no `Stats`. **Proposed:** move goredis
  acceptance 3 into `end_to_end_test.go`, which uses a real SQL source. Restart the runtime and
  assert `Rebuilds==0` and `LastPoll.Full==false`.
- **F4 (MEDIUM): there is no shared Backend conformance suite, and parity already drifts.** On
  a delta with empty `expected`, memory returns `ErrConflict` and goredis returns
  `ErrUnavailable`. Memory also accepts `{` in a binding. **Proposed:** add
  `storetest.TupleCacheBackend(t, factory)` and run it on both backends. It covers: an unknown
  binding gives a zero State; an expired mirror still reports its receipt; A's binding with
  B's receipt is unavailable; an empty `expected` over an existing mirror conflicts; a
  cross-binding `expected` returns `ErrBinding`; an invalid binding fails the same way in both.
- **F5 (MEDIUM, SRE): R3 "document, don't TTL".** Every `MaxStaleness` tune leaves a full
  mirror behind permanently. Under `noeviction`, that ends in Redis out-of-memory errors.
  Per D7, a rebuild after a long outage is cheap. **Proposed:** flip R3 to a renewing
  `PEXPIRE` of 7–30 days, set on replace and on delta. The minimum alternative is to keep R3,
  document the eviction-policy prerequisite, and make the `DEL` step mandatory downstream.
  The data reviewer would keep R3. **Owner choice.**
- **F6 (MEDIUM, SRE): an operator cannot see which key a process uses.** **Proposed:** add an
  exported `(*goredis.TupleCache).Key(binding) (string, error)`, and tell hosts to log the
  binding and key once at INFO on startup.
- **F7 (MEDIUM, SRE): pins are undercounted.** The goredis go.mod pins authorization v0.18.0,
  stores/pgx v0.13.0 and stores/turso v0.12.0. The latest are v0.24.0, v0.18.0 and v0.17.0.
  **Proposed:** list all three repins in D6. Add a goredis verify leg run as
  `GOWORK=off go build ./... && go test ./...`. Release order: tag authorization, poll the
  proxy `.info` until it returns 200, repin and tidy goredis, then tag goredis.
- **F8 (MEDIUM, SRE): CI never runs the goredis tests.** `check.yml` installs no
  `redis-server`. **Proposed:** treat the local Redis-backed run (`-race`) as a required manual
  gate, record the result in the PR, and file a follow-up to add redis to `live-stores.yml`.
- **F9 (LOW): rollback.** Rolling back to v2 binaries reads the stale v2 key and rebuilds once.
  Do not `DEL` the v2 key until the whole fleet is on v3.
- **F10 (LOW): custom bindings.** A bad custom binding never reaches ready
  (`FailureStage:"state"` on every poll); state this in the README. Optionally, have memory
  validate bindings the same way goredis does.

Verified safe, with no change needed: forged or empty `expected` (keys derive from the
binding, and the Lua re-checks `!binding`); the allow-list (bundled sources emit 64 hex
characters); orphans persist (`scripts.go:71`).

### Owner rulings on review findings (2026-10-14)

- **F3, F4, F6, F7, F8: APPROVED** (owner: "ok"). Relayed to the build session the same day.
- **F1/F2: APPROVED as option A** (owner: "I think i'm good with option A"). One steady-state
  binding per authorization database; in practice every replica on the same authorization
  database runs the same MaxStaleness. Overlap during a MaxStaleness change costs a full
  rebuild per poll until the old pods stop. Tests assert that cost honestly, and the README
  carries the rollout note (deploy the change quickly, or cache off, deploy, cache on). Per-
  binding receipts are not built; record them as a follow-up issue only.
- **F11: APPROVED** (owner: "this all sounds good"). `tuplecache.Policy.MaxStaleness` zero
  selects a 5s default instead of failing; negative stays ErrInvalidInput. Docs state the 5s
  revocation window plainly, and say that a host setting it explicitly must use the same value
  on every replica sharing an authorization database. This reverses the earlier "host must
  choose" design deliberately.
- **F12: APPROVED** (owner: "1 second"). The default `PollInterval` becomes
  `min(1s, MaxStaleness/4)` instead of `min(250ms, MaxStaleness/4)`. With the 5s default that
  gives 1s, which is about 5 renewals per eligibility window and a quarter of the idle poll load.
  `Notify()` still delivers local writes immediately. `PollInterval` is not part of the binding,
  so replicas may differ. Update the `runtime.go` default, the Policy doc, any test asserting
  the 250ms default, and the README knob table.
- Per-binding receipts follow-up: FILED as a known limitation with no current plans to
  support it (owner: "File it as a known issue with no current plans to support"), as
  [#80](https://github.com/gopernicus/gopernicus/issues/80).
- **F5: APPROVED as renewing expiry** (owner: "this all sounds good"). Supersedes R3. Every
  publication (replace, delta, and the no-change renewal) re-sets a Redis PEXPIRE on the mirror
  inside the same Lua script. A goredis option sets the duration: default 7 days, 0 = never
  expire. No background job. The README notes that old v2 keys were PERSISTed by old binaries
  and need a one-time manual DEL, and that under volatile-* maxmemory policies, keys with a TTL
  are evicted first (safe: reads fall back and the mirror rebuilds).
- **F9, F10: APPROVED** (owner: "this all sounds good"). Rollback note in D6/README (v2
  binaries rebuild the stale v2 key once; do not DEL v2 until the fleet is on v3). README line:
  a bad custom binding never reaches ready (FailureStage "state").
