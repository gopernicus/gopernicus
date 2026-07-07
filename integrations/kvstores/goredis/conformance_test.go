// Conformance and broadcast tests hit a live Redis. Run with:
//
//	docker run --rm -d -p 6379:6379 redis:7
//	REDIS_TEST_ADDR=localhost:6379 go test ./...
//
// They require REDIS_TEST_ADDR in the environment. Absent it, the tests skip
// LOUDLY — a silent green here would claim events.Bus / cacher.Storer /
// ratelimiter.Limiter conformance with nothing verified — so `make check` stays
// hermetic while `REDIS_TEST_ADDR=... go test` proves the three live contracts.
// The live clients are built through goredis.Open, so this leg also proves Open
// against real Redis, not just the raw redis.NewClient path.
package goredis_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/integrations/kvstores/goredis"
	"github.com/gopernicus/gopernicus/sdk/cacher"
	"github.com/gopernicus/gopernicus/sdk/cacher/cachertest"
	"github.com/gopernicus/gopernicus/sdk/events"
	"github.com/gopernicus/gopernicus/sdk/events/eventstest"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter/ratelimitertest"
	"github.com/gopernicus/gopernicus/sdk/tracing"
)

// requireAddr returns the live Redis address or skips loudly.
func requireAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set — goredis conformance NOT verified (make check stays hermetic)")
	}
	return addr
}

// dialLive builds a client through goredis.Open (which performs its own
// fail-fast ping), failing loudly (never skipping) once REDIS_TEST_ADDR is set.
// Routing every live client through Open proves Open against real Redis and that
// the raw *redis.Client it returns feeds all three facilities unchanged.
func dialLive(t *testing.T, addr string) *redis.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb, err := goredis.Open(ctx, goredis.Config{Addr: addr})
	if err != nil {
		t.Fatalf("goredis.Open(%s): %v", addr, err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// TestOpenLive proves Open returns a working client end-to-end with the logging
// and tracing hooks installed: a hooked client still round-trips a SET/GET.
func TestOpenLive(t *testing.T) {
	addr := requireAddr(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rdb, err := goredis.Open(ctx, goredis.Config{Addr: addr},
		goredis.WithLogging(slog.New(slog.DiscardHandler)),
		goredis.WithTracing(tracing.Noop{}),
	)
	if err != nil {
		t.Fatalf("goredis.Open with hooks: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	key := fmt.Sprintf("opentest:%d", time.Now().UnixNano())
	if err := rdb.Set(ctx, key, "v", time.Minute).Err(); err != nil {
		t.Fatalf("Set through hooked client: %v", err)
	}
	got, err := rdb.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("Get through hooked client: %v", err)
	}
	if got != "v" {
		t.Errorf("Get = %q, want v", got)
	}
}

// TestStatusCheckLive proves StatusCheck returns nil against a reachable Redis.
// It skips loudly without REDIS_TEST_ADDR, matching the file's other live legs;
// the client is dialed through goredis.Open so the check runs over a real server.
func TestStatusCheckLive(t *testing.T) {
	addr := requireAddr(t)

	rdb := dialLive(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := goredis.StatusCheck(ctx, rdb); err != nil {
		t.Fatalf("StatusCheck() error = %v, want nil against reachable Redis", err)
	}
}

// TestConformance_Bus runs the shared events.Bus conformance suite against a
// live Redis. Each newBus call uses a unique stream prefix so leftover streams
// from a prior subtest never cross-talk, and a short BlockTimeout keeps the
// workers responsive to newly-relevant streams within the suite's delivery
// window.
func TestConformance_Bus(t *testing.T) {
	addr := requireAddr(t)

	eventstest.Run(t, func(t *testing.T) events.Bus {
		rdb := dialLive(t, addr)
		prefix := fmt.Sprintf("evtest:%d:", time.Now().UnixNano())
		return goredis.New(rdb, slog.New(slog.DiscardHandler), goredis.Options{
			StreamPrefix:  prefix,
			ConsumerGroup: "conformance",
			Workers:       2,
			BlockTimeout:  150 * time.Millisecond,
			BatchSize:     10,
		})
	})
}

// TestConformance_Cacher runs the shared cacher.Storer conformance suite against
// a live Redis. Each fresh Cacher gets a unique key prefix so keys from a prior
// subtest never collide on the shared instance.
func TestConformance_Cacher(t *testing.T) {
	addr := requireAddr(t)

	cachertest.Run(t, func(t *testing.T) cacher.Storer {
		rdb := dialLive(t, addr)
		prefix := fmt.Sprintf("cachetest:%d:", time.Now().UnixNano())
		return goredis.NewCacher(rdb, goredis.WithCacheKeyPrefix(prefix))
	})
}

// TestConformance_Limiter runs the shared ratelimiter.Limiter conformance suite
// against a live Redis. Each fresh Limiter gets a unique key prefix so window
// state from a prior subtest never bleeds across the shared instance.
func TestConformance_Limiter(t *testing.T) {
	addr := requireAddr(t)

	ratelimitertest.Run(t, func(t *testing.T) ratelimiter.Limiter {
		rdb := dialLive(t, addr)
		prefix := fmt.Sprintf("rltest:%d:", time.Now().UnixNano())
		return goredis.NewLimiter(rdb, goredis.WithLimiterKeyPrefix(prefix))
	})
}

// TestBroadcastFansOutAcrossInstances proves the broadcast rail's defining
// property the streams path does not have: a subscriber on bus A receives an
// event emitted on bus B (two clients, one Redis, one broadcast channel).
func TestBroadcastFansOutAcrossInstances(t *testing.T) {
	addr := requireAddr(t)

	prefix := fmt.Sprintf("bcast:%d:", time.Now().UnixNano())
	log := slog.New(slog.DiscardHandler)
	busA := goredis.New(dialLive(t, addr), log, goredis.Options{StreamPrefix: prefix})
	busB := goredis.New(dialLive(t, addr), log, goredis.Options{StreamPrefix: prefix})
	t.Cleanup(func() {
		_ = busA.Close(context.Background())
		_ = busB.Close(context.Background())
	})

	received := make(chan events.Event, 1)
	if _, err := busA.SubscribeBroadcast("note.created", func(_ context.Context, e events.Event) error {
		select {
		case received <- e:
		default:
		}
		return nil
	}); err != nil {
		t.Fatalf("SubscribeBroadcast() error = %v", err)
	}

	// Give the SUBSCRIBE a moment to be live before publishing.
	time.Sleep(300 * time.Millisecond)

	event := events.NewBaseEvent("note.created").WithTenant("t1").WithAggregate("note", "n1")
	if err := busB.Emit(context.Background(), event, events.WithSync()); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}

	select {
	case e := <-received:
		remote, ok := e.(events.RemoteEvent)
		if !ok {
			t.Fatalf("delivered event type = %T, want events.RemoteEvent", e)
		}
		if remote.Type() != "note.created" {
			t.Errorf("Type() = %q, want note.created", remote.Type())
		}
		if remote.TenantID() == nil || *remote.TenantID() != "t1" {
			t.Errorf("TenantID() = %v, want t1", remote.TenantID())
		}
		if remote.AggregateID() == nil || *remote.AggregateID() != "n1" {
			t.Errorf("AggregateID() = %v, want n1", remote.AggregateID())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast event never arrived across instances")
	}

	// Topic filtering: an unmatched type must not arrive.
	if err := busB.Emit(context.Background(), events.NewBaseEvent("other.event"), events.WithSync()); err != nil {
		t.Fatalf("Emit(other) error = %v", err)
	}
	select {
	case e := <-received:
		t.Fatalf("unexpected event %q delivered for a filtered topic", e.Type())
	case <-time.After(500 * time.Millisecond):
	}
}
