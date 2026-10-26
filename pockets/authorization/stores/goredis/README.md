# Authorization TupleCache — go-redis

This module implements `logic/tuplecache.Backend` over a host-owned
`*redis.Client`. The authoritative source can be Turso, PostgreSQL, or another
implementation of `tuplecache.Source`; this module contains no SQL assumptions.

```go
client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
    ContextTimeoutEnabled: true,
})
backend, err := goredis.NewTupleCache(client,
    goredis.WithNamespace("my-app"), // optional
    goredis.WithLimits(goredis.Limits{
        MaxReadBytes: 1 << 20,
        MaxMutationBytes: 4 << 20,
    }),
)
```

Production hosts should also pass `goredis.WithIntegrityKeys` and treat Redis
as part of the authorization boundary; see
[Trust boundary and hardening](#trust-boundary-and-hardening).

The namespace is optional; it only adds a readable key prefix. Mirrors are keyed
on the runtime binding, so the host never types anything that must match it. The
constructor starts no goroutines and performs no I/O. The host retains ownership
of the client, its connection settings, persistence, memory capacity, and shutdown.
Compose the backend with `tuplecache.New(source, backend, ...)` and drive that
runtime's poll function through the host's worker lifecycle.

The client must enable `ContextTimeoutEnabled`; otherwise construction returns
`sdk.ErrInvalidInput`. This ensures the runtime's `ReadTimeout` also bounds Redis
socket I/O. Construction never mutates the borrowed client's options. Host hooks
and custom dialers remain responsible for honoring context deadlines themselves.

## Fit

The adapter is correct but has a narrow performance fit; choose it knowingly.
Owner benchmark (Apple M4 Pro, Unix-socket Redis, one principal holding N direct
grants):

| Set size | one read | one delta | full rebuild |
|---|---|---|---|
| 100 | 0.11 ms | 0.41 ms | 0.29 ms |
| 1,000 | 0.95 ms | 3.7 ms | 2.7 ms |
| 10,000 | 9.4 ms | 38 ms | 25 ms |
| 100,000 | 96 ms | 426 ms | 283 ms |

- Each index set is one JSON field. A read decodes the whole set touched; a delta
  decodes, validates and re-encodes the whole set inside Redis's single thread.
- Good fit: many resources with small forward sets, principals with small reverse
  sets, and PostgreSQL under read load from many replicas.
- Poor fit: a principal or resource with thousands of direct tuples. Graph checks
  are subject-first and read the principal's whole reverse set. Prefer durable
  reads. A per-replica memory backend is not a cheap alternative: each replica's
  mirror shares the source's one delivery receipt (see below), so replicas keep
  rebuilding each other in full.
- Full rebuilds are cheap relative to deltas on large sets.

## Redis key

Each mirror is keyed on its runtime binding: `tuplecache:v3:{<binding>}`, or
`tuplecache:v3:{<namespace>/<binding>}` with `WithNamespace`. With
`WithIntegrityKeys` the key is `tuplecache:v3:ik:{<binding>}` (or
`tuplecache:v3:ik:{<namespace>/<binding>}`), so keyed and unkeyed runtimes never
read each other's mirror. The runtime binding
is `protocol:2/<source binding>/max-staleness:<nanoseconds>`, so processes share
a mirror exactly when their protocol, source identity and `MaxStaleness` match;
any difference lands in a different mirror instead of failing with
`tuplecache.ErrBinding`. `v3` is this adapter's key-layout version; the wire
format is unchanged.

The namespace and the binding may contain ASCII letters, digits and `:._-/`.
An empty or invalid namespace fails construction, and an invalid binding (from a
custom `tuplecache.Source`) fails `State`, `Read` and `Publish`; both return
`sdk.ErrInvalidInput`. Braces are reserved for the surrounding Redis hash tag,
which keeps the mirror and temporary `:build:<nonce>` hashes in the same slot.
This key layout does not add Redis Cluster support. A runtime whose custom
binding is invalid never reaches ready: every poll fails with
`FailureStage: "state"` and decisions stay on the durable source.

`(*TupleCache).Key(binding)` returns the key for a binding. Log the runtime's
binding and key once at INFO on startup, so operators can see which mirror each
process uses:

```go
runtime := components.TupleCache
key, err := backend.Key(runtime.Binding())
logger.Info("authorization tuple cache", "binding", runtime.Binding(), "key", key)
```

For example, inspect the mirrors with:

```sh
redis-cli --scan --pattern 'tuplecache:v3:{my-app/*'
redis-cli HLEN 'tuplecache:v3:{my-app/protocol:2/<source binding>/max-staleness:5000000000}'
```

Protocol 2 uses `!schema=2` and includes the protocol in the runtime binding.
Apply the authoritative adapter's fresh base and optional cache schema before
constructing the source. The first successful poll rebuilds the mirror from
current canonical tuples. See the SQL adapter's [schema setup guide](../UPGRADE.md).

### One binding per authorization database

Every replica on the same authorization database must run the same
`MaxStaleness` (and the same gopernicus protocol), so all of them share one
binding and one mirror. The database keeps a single delivery receipt, so it can
keep exactly one mirror current. Runtimes with different bindings (or separate
mirrors, such as per-replica memory backends) over the same database each keep
their own mirror, but every acknowledgement moves the receipt past the others.
Each of them then rebuilds in full on **every** poll, even with no writes, for
as long as more than one is active: a full SQL scan plus a full rebuild per poll
per replica. Answers stay correct; only the cost changes.

**Rollout note.** When changing `MaxStaleness` (or upgrading from the v2 key
layout), old and new pods rebuild on every poll while they overlap. Either
deploy the change quickly, or turn the cache off, deploy, and turn it back on.

### Mirror expiry and orphans

Every publication (full rebuild, delta, or the no-change renewal each poll)
renews the mirror's Redis expiry inside the same Lua script. A live mirror is
renewed at poll cadence and never lapses; a superseded binding's mirror (after a
`MaxStaleness`, protocol or source identity change) is no longer published to,
so it expires on its own. `WithExpiry(d)` sets the duration: the default is
7 days, and `0` keeps mirrors forever (the previous behaviour; superseded keys
then need manual deletion). Keep it far above the poll interval.

Keys with a TTL are what `volatile-*` `maxmemory` policies evict first. That is
safe: reads fall back to the durable source and the next poll rebuilds the
mirror.

**Upgrading from the v2 key layout.** Old binaries `PERSIST`ed their
`tuplecache:v2:` key, so it never expires. Once the whole fleet runs v3, delete
it once by hand:

```sh
redis-cli --scan --pattern 'tuplecache:v2:*'
redis-cli DEL '<key>'
```

**Rollback.** v2 binaries ignore v3 keys and rebuild their stale v2 key once.
Do not delete the v2 key until the whole fleet is on v3.

## Stored data

One Redis hash contains metadata and raw forward/reverse canonical tuple sets:

- A forward field selects explicit global or resource scope and a relation.
- A reverse field selects the exact subject type, ID, and subject relation and
  includes matching global and resource facts.
- Both indexes contain complete seven-field facts: scope kind, resource type,
  resource ID, relation, subject type, subject ID, and subject relation. The six
  string components use URL-base64 inside JSON arrays. Global resource type and
  ID are empty; zero or unknown scope kinds are invalid.
- A keyed mirror stores eight-field facts: the seven fields plus a 22-character
  URL-base64 tag (see below). A fact's identity is its seven fields, so a delta
  that adds a fact replaces its stored tag and a revoke removes it whatever tag
  is stored.
- Metadata fields start with `!`: `!schema` (`2`), `!binding`, `!receipt`,
  `!until` (the eligibility deadline, Redis ms), `!epoch` and `!epoch_at` (a
  random identifier and the Redis time of the last full publication, which the
  adapter reports as `tuplecache.State.FullAt`), and on a keyed mirror `!mac`.

The mirror stores role and relationship facts together. There is no writer-origin
classification: identical scope, relation and subject form one fact. Independent
relations coexist. Exact role reads need no graph model and never expand usersets.
Global scope is not a wildcard or a graph intermediary. Optional graph evaluation
still applies its compiled model and traverses the facts for each operation.
No decisions or expanded memberships are cached.

`TupleCache.ReadTupleSnapshot` implements `tuples.Snapshotter`, allowing roles and
composed decisions to share the same runtime. All cached reads require the host's
explicit freshness policy. Indexed raw lookups use forward or reverse sets;
queries requiring an unindexed scan retry the whole operation in a durable
snapshot. Ambient transactions always use the source's bound durable view and
must satisfy its snapshot-isolation requirements.

Publications compare the binding and delivery receipt atomically. Ordinary
changes update only the index fields that contain changed tuples; receipts are
never part of index keys and do not cause unrelated data to be invalidated.
Empty sets are omitted from the hash. Missing fields mean known absence only
while the complete mirror has the expected receipt and remains eligible.

## Atomicity, freshness, and recovery

Delta publication checks encoded size, then validates and transforms all affected
sets before writing.
The Lua script applies the whole captured source batch atomically. It removes
the complete marker before modifying fields and restores it last, so an
operational error during mutation leaves an unavailable mirror instead of
partial indexes under a valid receipt.

Full publication builds a temporary hash with a five-minute cleanup TTL and
swaps it into place only if the old receipt still matches. The swapped-in
mirror takes the renewing expiry described under "Mirror expiry and orphans". Full snapshots already contain their pending changes; those changes
are not applied again to the rebuilt tuples.

Eligibility is checked using Redis server time. Time spent contacting Redis or
building a full mirror consumes the source observation's remaining freshness
bound. Expired mirrors retain their receipt for recovery but cannot serve reads.
The runtime checks the same receipt throughout an operation and falls back to
the authoritative store if a publication intervenes.

Keeping data and metadata in one hash makes whole-key eviction or loss detectable;
it cannot remove individual tuple sets while retaining a complete marker. Missing
or malformed metadata is treated as an absent mirror and can be rebuilt from the
source. A key with a different Redis type is rejected. Arbitrary writes, hash
field eviction/expiration, or deletion of individual fields outside this adapter
are unsupported. Normal Redis whole-key eviction is safe but makes the cache
unavailable until the source rebuild succeeds.

Redis persistence is optional host configuration. A restored receipt is compared
with the source's acknowledged receipt by the runtime; an older restored dataset
must be reconstructed from authoritative tuples. The outbox is disposable pending
work, not the rebuild source.

## Trust boundary and hardening

Redis is inside the authorization boundary. Without integrity keys the adapter
trusts every well-formed hash it reads: anyone who can write the mirror key can
grant access until the next full rebuild. Treat the instance like the
authorization database:

- **Dedicated instance**, or at least a logical database that holds nothing
  else. Never share a database index with session stores, queues, rate limiters
  or any lower-trust tenant; a shared index means every client of it can write
  the mirror.
- **TLS with a verified CA** (`rediss://`). Never set `skip_verify`; for a
  private CA, fill `TLSConfig.RootCAs` after parsing the URL.
- **An ACL user** limited to the mirror keys and the commands the adapter's
  scripts run. Redis checks the commands a script calls against the caller's
  ACL, so the user needs those as well as `EVAL`/`EVALSHA`:

  ```
  ACL SETUSER authz-cache on >CHANGE-ME resetkeys resetchannels -@all \
    ~tuplecache:v3:ik:{my-app/* \
    +hello +ping +client|setinfo +eval +evalsha +del \
    +time +type +exists +hget +hmget +hset +hdel +hstrlen +rename +pexpire +persist +pttl
  ```

  Without `WithNamespace` the key pattern is `~tuplecache:v3:ik:*`. Disable the
  `default` user or give it no access to these keys.
- **`maxmemory-policy`**: `noeviction` or a `volatile-*` policy. Whole-key
  eviction is safe (reads fall back, the next poll rebuilds); size the instance
  so it is rare.
- Persistence is optional: a restored older dataset is handled as below.

The client comes from one URL, the shape `AUTH_CACHE_REDIS_URL` already uses
(`rediss://<acl-user>:<password>@<host>:<port>/<db>`):

```go
opts, err := redis.ParseURL(os.Getenv("AUTH_CACHE_REDIS_URL"))
if err != nil {
    return err
}
opts.ContextTimeoutEnabled = true
client := redis.NewClient(opts)
```

### Integrity keys

`WithIntegrityKeys(current, previous...)` makes a Redis compromise cost
availability, not access:

- Every fact carries a tag: `HMAC-SHA256(key, "gopernicus.tuplecache.fact.v1"
  ‖ binding ‖ epoch ‖ the seven fields)`, length-prefixed and truncated to 16
  bytes. Lua never holds the key; Go tags facts before publication and checks
  every tag it reads.
- Every publication writes `!mac = HMAC-SHA256(key,
  "gopernicus.tuplecache.header.v1" ‖ binding ‖ receipt ‖ epoch ‖ epoch_at ‖
  until)`. `State` and `Read` verify it. Every full publication mints a new
  epoch, so it re-tags every fact.
- Any failure returns `goredis.ErrIntegrity`, which matches
  `tuplecache.ErrUnavailable`: the decision is answered from the durable source
  and `(*TupleCache).Stats().IntegrityFailures` counts it. A header that fails
  verification reads as an absent mirror, so the next poll replaces it in full.
  A set holding a bad tag stays unreadable (every read of it falls back) until
  the next full publication: `FullRebuildEvery`, or the runtime's `Rebuild`.
  Alert on a rising `IntegrityFailures`.

Generate each key with `openssl rand -hex 32` (32 bytes minimum; shorter keys
fail construction with `sdk.ErrInvalidInput`). A host might read a
comma-separated list from `AUTHZ_CACHE_INTEGRITY_KEYS`, newest first:

```go
var keys [][]byte
for _, part := range strings.Split(os.Getenv("AUTHZ_CACHE_INTEGRITY_KEYS"), ",") {
    key, err := hex.DecodeString(strings.TrimSpace(part))
    if err != nil {
        return err
    }
    keys = append(keys, key)
}
backend, err := goredis.NewTupleCache(client,
    goredis.WithNamespace("my-app"),
    goredis.WithIntegrityKeys(keys[0], keys[1:]...),
)
```

Keep the keys in the host's secret store beside the database credential, never
in Redis. Every replica sharing a binding must hold the same keys.

**Rotation.** The current key signs new full publications; every key verifies.
An epoch stays signed by the key that opened it, so its deltas keep verifying
across a rotation. Prepend a new key and deploy; once every mirror has had a
full publication under it (one `FullRebuildEvery` period, or call the runtime's
`Rebuild`), remove the old key. Removing it early is safe but not free: the
mirror fails verification once and is rebuilt in full. During a rolling deploy,
pods that do not yet hold the new key rebuild a mirror the new key opened.

**Turning keys on or off** changes the mirror key, so the first poll rebuilds in
full; the old mirror expires after `WithExpiry`. As with any binding change,
keyed and unkeyed replicas on one authorization database rebuild on every poll
while they overlap, so roll the change out quickly.

**What the keys bound, and what they do not.**

- A forged or altered fact is never read; a missing or seven-field fact on a
  keyed mirror fails verification.
- An older mirror restored over the current one keeps its old `!until`, which
  the MAC covers, so it is ineligible within `MaxStaleness` and the next poll
  replaces it.
- A revoked fact copied back with a tag from the current epoch verifies until
  the next full publication mints a new epoch. Pair the keys with
  `tuplecache.Policy.FullRebuildEvery` (one hour is a reasonable start) on
  mirrors that rebuild well within `MaxStaleness`; a rebuild that takes longer
  than `MaxStaleness` cannot publish.
- Deleting facts or keys only denies: there is no exclusion or intersection in
  the model, so a missing fact never grants.
- The key lives in the host process. A compromise of the host is out of scope;
  the threat these keys address is a Redis-only compromise.
- Each stored fact grows by about 25 bytes, and each fact is stored twice
  (forward and reverse), so about 50 bytes per fact. Mirrors near the limits
  fall back more often. Reads also verify one HMAC per fact read.

**FullRebuildEvery and clock skew.** `State.FullAt` is the Redis `TIME` of the
last full publication, read from `!epoch_at`, so replicas sharing a mirror
rebuild once per period between them. The cadence compares it with each host's
local wall clock, so skew between a host and Redis shifts that host's cadence by
the skew: a Redis clock ahead delays the rebuild, one behind brings it forward.
Keep both on NTP.

**Drill.** On a scratch Redis, alter a fact and, separately, a header field of a
keyed mirror; the next decision must come from the durable source
(`Stats().Fallbacks` and `IntegrityFailures` rise, the answer matches the
database) and the next poll must rebuild the mirror. `integrity_test.go` runs
the same cases.

## Limits and verification

The complete mirror occupies one Redis hash and therefore one Redis shard. A
large relation set is encoded as one field value; mutations decode and encode
the touched sets, and a captured transaction is published as one Lua script.

`WithLimits(Limits{...})` replaces the complete record. Zero fields select finite
defaults; negative values and nil options return `sdk.ErrInvalidInput`.

| Limit | Default | Accounting |
|---|---:|---|
| `MaxReadBytes` | 1 MiB | Aggregate raw JSON bytes returned by one `Read`, including repeated keys and two bytes per absent set. |
| `MaxMutationBytes` | 4 MiB | Incoming delta JSON plus each distinct existing touched field value. Full rebuilds bound each field name plus value, and each upload chunk, separately. |

Reads check field lengths inside the same Lua script before fetching values or
decoding JSON. Deltas bound incoming encoding incrementally in Go, then check
existing field lengths before Lua decodes or transforms any tuple set. An
over-limit operation returns `tuplecache.ErrCapacity`, which also matches
`tuplecache.ErrUnavailable`. A permission decision retries from the authoritative
snapshot; a failed publication preserves the old mirror and leaves pending work
unacknowledged. Results are never truncated.

Full rebuilds allow the entire mirror to exceed these per-operation limits. They
reject an oversized individual field before creating a temporary hash, and send
bounded chunks. A rebuild can recover an oversized pending delta batch when its
current individual fields fit: invoke the runtime's `Rebuild(ctx)`. A set near
the mutation limit may be rebuildable but too large to update by delta because
the delta includes both existing data and incoming operations. Raise limits only
after measuring the workload or keep those decisions on durable reads.

Limits bound encoded work, not Redis/Go memory byte for byte, total full-source
snapshot memory, or wall-clock script duration. Full rebuilds still materialize
the complete source and both indexes in Go. JSON decoding has additional allocation
overhead, and Lua runs synchronously on the Redis server. Capacity and script
duration must be measured against the host's tuple-set sizes. This is not a
partitioned or Redis Cluster adapter.

Run `go build ./...`, `go test ./...`, `go test -race ./...`, and `go vet ./...`
from this module, and once more with `GOWORK=off` to prove the published pins
resolve (`GOWORK=off go build ./... && GOWORK=off go test ./...`). CI does not
run the Redis-backed tests, so a local `-race` run is the release gate. Tests launch isolated `redis-server` processes using private
Unix sockets, including an AOF restart proof. They skip explicitly when the
executable is unavailable; they do not connect to a host or production Redis.

Run retained high-cardinality benchmarks with:

```sh
go test -run '^$' -bench '^BenchmarkTupleCache$' -benchmem -benchtime=1s -count=3
```

The matrix uses 100, 1,000, 10,000 and 100,000 documents assigned to one principal,
measuring accepted/rejected reverse-set reads, accepted/rejected single-tuple
changes to that hot set, and full rebuilds. Accepted benchmarks explicitly raise
limits to 16 MiB to expose costs beyond defaults; rejected benchmarks set a limit
below the encoded operation size. Fixture creation and Redis process startup are
excluded. `B/op` and `allocs/op` measure the Go client, not Redis Lua allocations.
