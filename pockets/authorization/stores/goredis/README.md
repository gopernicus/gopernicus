# Authorization TupleCache — go-redis

This module implements `logic/tuplecache.Backend` over a host-owned
`*redis.Client`. The authoritative source can be Turso, PostgreSQL, or another
implementation of `tuplecache.Source`; this module contains no SQL assumptions.

```go
client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
    ContextTimeoutEnabled: true,
})
backend, err := goredis.NewTupleCache(client, "my-relationship-store",
    goredis.WithLimits(goredis.Limits{
        MaxReadBytes: 1 << 20,
        MaxMutationBytes: 4 << 20,
    }),
)
```

The namespace is required and must belong exclusively to one tuple store. The
constructor starts no goroutines and performs no I/O. The host retains ownership
of the client, its connection settings, persistence, memory capacity, and shutdown.
Compose the backend with `tuplecache.New(source, backend, ...)` and drive that
runtime's poll function through the host's worker lifecycle.

The client must enable `ContextTimeoutEnabled`; otherwise construction returns
`sdk.ErrInvalidInput`. This ensures the runtime's `ReadTimeout` also bounds Redis
socket I/O. Construction never mutates the borrowed client's options. Host hooks
and custom dialers remain responsible for honoring context deadlines themselves.

## Redis key

The mirror key is `tuplecache:{<namespace>}`. The namespace is stored verbatim:
`segovia-v2:dev:authorization` produces
`tuplecache:{segovia-v2:dev:authorization}`. It may contain ASCII letters, digits
and `:._-/`; empty namespaces or other characters return `sdk.ErrInvalidInput`.
Colons are allowed. Braces are reserved for the surrounding Redis hash tag, which
keeps the mirror and temporary `:build:<nonce>` hashes in the same slot.
This key layout does not add Redis Cluster support.

For example, inspect the mirror with:

```sh
redis-cli --scan --pattern 'tuplecache:*'
redis-cli HLEN 'tuplecache:{segovia-v2:dev:authorization}'
```

Earlier versions used `gopernicus:tuplecache:{<base64url(namespace)>}:mirror`.
The new name addresses a fresh hash; the next successful relay poll rebuilds it
automatically from authoritative tuples. For a single dev server, restart with
the updated adapter. No SQL migration or manual Redis conversion is required.
If several readers/relays share a source, stop the old versions before starting
the new ones: two mirrors would compete over the same source delivery receipt.
Old hashes are left untouched and have no TTL; remove them separately once no
old process uses them.

## Stored data

One Redis hash contains both metadata and raw forward/reverse relationship sets:

- A forward field selects resource type, ID, and relation and contains exact
  subject references.
- A reverse field selects the exact subject type, ID, and subject relation and
  contains resource type, ID, and relation references.
- References are JSON arrays of three URL-base64 components, preserving opaque
  string bytes and avoiding ambiguity from tuple punctuation.

No permission decisions, expanded memberships, role facts, or model-specific
answers are stored. The authorization evaluator still applies its compiled model
and traverses the graph on each operation.

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
swaps it into place only if the old receipt still matches. The permanent hash
has no TTL. Full snapshots already contain their pending changes; those changes
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
duration must be measured against the host's relationship sizes. This is not a
partitioned or Redis Cluster adapter.

Run `go build ./...`, `go test ./...`, `go test -race ./...`, and `go vet ./...`
from this module. Tests launch isolated `redis-server` processes using private
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
