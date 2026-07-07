// Package goredis is a multi-port Redis integration: it wraps exactly one
// external library — github.com/redis/go-redis/v9 — and implements three sdk
// facility ports over a single caller-supplied *redis.Client. The module unit
// is the library, not the port (ruling R-KV1): one go-redis dependency, one
// client, three facilities. It depends only on sdk facility ports and go-redis;
// it imports no features and no other integration.
//
//   - events.Bus + events.Broadcaster — Bus (bus.go, broadcast.go): a durable
//     competing-consumer rail on Redis Streams plus a best-effort fan-out rail
//     on Redis pub/sub.
//   - cacher.Storer — Cacher (cacher.go): a TTL-aware distributed cache over
//     GET/MGET/SET/DEL/SCAN.
//   - ratelimiter.Limiter — Limiter (limiter.go): a distributed sliding-window
//     rate limiter driven by an atomic Lua script keyed off Redis server time.
//
// Each facility takes the caller's *redis.Client and never closes it — the
// caller owns the client lifecycle, and one client can feed all three.
//
// # Two delivery rails, two guarantees (Bus)
//
// Streams (the events.Bus path — Emit/Subscribe/Close):
//
//   - Emit writes an event to a per-type stream with XADD.
//   - Subscribe lazily creates a consumer group on that stream (XGROUPCREATE
//     MKSTREAM) and starts XReadGroup worker goroutines.
//   - Delivery is at-least-once with competing-consumer semantics: N processes
//     sharing one ConsumerGroup split the load, each stream message going to
//     exactly one consumer across the group, and a crashed consumer's unacked
//     messages are re-delivered. Handlers MUST be idempotent.
//   - XACK-always poison-pill policy: a message is acknowledged even when it
//     fails to parse or a handler errors or panics, so one bad message can never
//     block the group's pending list. There is no in-bus retry — durable retry
//     is the outbox/jobs rail's job, not this bus's.
//
// Broadcast (the events.Broadcaster path — SubscribeBroadcast; see broadcast.go):
//
//   - Every Emit also PUBLISHes onto a pub/sub channel, and SubscribeBroadcast
//     delivers each event to EVERY process that has a broadcast subscriber.
//   - Delivery is best-effort fan-out with no durability and no replay: an event
//     published while a subscriber is disconnected is simply gone (SSE clients
//     reconnect and re-fetch). This is the right shape for ephemeral consumers.
//
// # Wildcard note
//
// A "*" subscription on the streams path receives events on topics THIS process
// also emits (the consumer reads a stream once a local wildcard subscriber makes
// it relevant). Cross-process wildcard fan-out is the broadcast path's job, not
// the competing-consumer streams path's.
//
// # Options from the environment
//
// Options carries `env:` struct tags so a host can populate it with
// sdk/environment.ParseEnvTags; that is a convenience, not an import edge — the zero
// value is filled with the defaults documented on Options by New.
package goredis
