// Package goredis is a multi-port Redis integration: it wraps exactly one
// external library — github.com/redis/go-redis/v9 — and implements three sdk
// facility ports over a single caller-supplied *redis.Client. The module unit
// is the library, not the port (ruling R-KV1): one go-redis dependency, one
// client, three facilities. It depends only on sdk facility ports and go-redis;
// it imports no pockets and no other integration.
//
//   - events.Bus + events.Broadcaster — Bus: notification fanout over Redis
//     pub/sub, with explicit SubscribeWork for reliable competing consumers
//     over Redis Streams.
//   - cacher.Storer — Cacher (cacher.go): a TTL-aware distributed cache over
//     GET/MGET/SET/DEL/SCAN.
//   - ratelimiter.Limiter — Limiter (limiter.go): a distributed sliding-window
//     rate limiter driven by an atomic Lua script keyed off Redis server time.
//
// Each facility takes the caller's *redis.Client and never closes it — the
// caller owns the client lifecycle, and one client can feed all three.
//
// # Notification and work delivery
//
// Emit admits a bounded asynchronous publication. Publish waits for XADD
// acceptance and best-effort pub/sub fanout; it never forces local callbacks.
// Subscribe and SubscribeBroadcast both receive exact-topic or wildcard
// notifications after acknowledged pub/sub setup. Notifications have no replay.
// Setup uses a five-second context; underlying I/O interruption depends on the
// caller-owned client's timeout/context configuration, which the bus preserves.
//
// SubscribeWork accepts an exact topic and verifies the group before returning.
// Group members must deploy identical handler responsibilities. Compose required
// steps into one handler because the first registration starts readers. Each
// worker reads or reclaims one entry at a time, preserving XAUTOCLAIM cursors and
// alternating recovery with new work. Redis 6.2 or newer is required for reclaim.
//
// XACK follows successful completion of every selected handler. Errors, panics,
// cancellation, malformed records and missing handlers leave entries pending.
// HandlerTimeout covers the whole selected-handler attempt; RetryAfter must
// exceed it. A callback ignoring cancellation can overlap reclaim, so handlers
// must be idempotent. Permanent poison requires host-owned repair or explicit
// terminal disposition. There is no automatic discard, DLQ policy or trimming.
//
// Unsubscribe removes inactive topics from reads. NOGROUP recreates the group
// at 0, so retained entries can be delivered again. There is no exactly-once
// guarantee. Close refuses new admissions and waits for admitted publications
// and callbacks; repeated callers wait for shared completion with their own
// contexts. Callbacks must not call Close themselves.
//
// # Record format and rollout
//
// Both paths serialize the canonical events.Record, preserving stable IDs,
// metadata and opaque payload bytes. Publish record.Event() for stable-ID replay.
// New appends an internal v2: suffix to all stream and broadcast prefixes. Hosts
// must coordinate old writers, disjoint physical namespaces, old pending work
// and duplicate-safe replay; no old messages are automatically migrated/deleted.
// Options.BatchSize and MaxLen are removed. Hosts own stream retention.
//
// # Construction options
//
// New takes a required client followed by BusOption values such as WithLogger,
// WithStreamPrefix and WithConsumerGroup. Hosts map their own environment-loaded
// policy into options. Without options New uses the documented defaults.
// Connection Config retains its environment tags for Open.
package goredis
