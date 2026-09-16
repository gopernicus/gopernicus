package goredis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	tuplefacts "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/redis/go-redis/v9"
)

func TestTupleCacheClientAndLimitsValidation(t *testing.T) {
	options := &redis.Options{Dialer: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("constructor performed I/O")
		return nil, errors.New("unexpected dial")
	}}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	if got, err := NewTupleCache(client, "limits"); got != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("client without context deadlines: %v/%v", got, err)
	}
	if client.Options().ContextTimeoutEnabled {
		t.Fatal("constructor mutated borrowed client")
	}
	validOptions := *options
	validOptions.ContextTimeoutEnabled = true
	valid := redis.NewClient(&validOptions)
	t.Cleanup(func() { _ = valid.Close() })
	for _, opts := range [][]Option{{nil}, {WithLimits(Limits{MaxReadBytes: -1})}, {WithLimits(Limits{MaxMutationBytes: -1})}} {
		if got, err := NewTupleCache(valid, "limits", opts...); got != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid options: %v/%v", got, err)
		}
	}
	option := WithLimits(Limits{MaxReadBytes: 32})
	for range 2 {
		c, err := NewTupleCache(valid, "limits", WithLimits(Limits{MaxMutationBytes: 1}), option)
		if err != nil || c.limits != (Limits{MaxReadBytes: 32, MaxMutationBytes: defaultMaxMutationBytes}) {
			t.Fatalf("replacement/defaults/reused option: %+v/%v", c, err)
		}
	}
	c, err := NewTupleCache(valid, "limits")
	if err != nil || c.limits != (Limits{MaxReadBytes: defaultMaxReadBytes, MaxMutationBytes: defaultMaxMutationBytes}) {
		t.Fatalf("defaults: %+v/%v", c, err)
	}
}

func TestTupleCacheReadCapacityBoundaries(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	f := setField(forward(row))
	r := setField(reverse(row))
	size := len(client.HGet(ctx, c.key, f).Val())
	other := len(client.HGet(ctx, c.key, r).Val())
	for _, test := range []struct {
		name     string
		limit    int
		keys     []tuplecache.SetKey
		capacity bool
	}{
		{"exact field", size, []tuplecache.SetKey{forward(row)}, false},
		{"one byte over", size - 1, []tuplecache.SetKey{forward(row)}, true},
		{"exact aggregate", size + other, []tuplecache.SetKey{forward(row), reverse(row)}, false},
		{"aggregate exceeds", size + other - 1, []tuplecache.SetKey{forward(row), reverse(row)}, true},
		{"duplicate keys count twice", size, []tuplecache.SetKey{forward(row), forward(row)}, true},
		{"empty eligibility read", 1, nil, false},
		{"absent set exact", 2, []tuplecache.SetKey{{Scope: tuplefacts.On("space", "absent"), Relation: "viewer"}}, false},
		{"absent set over", 1, []tuplecache.SetKey{{Scope: tuplefacts.On("space", "absent"), Relation: "viewer"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			limited := cacheWithLimits(t, client, Limits{MaxReadBytes: test.limit})
			sets, err := limited.Read(ctx, state, test.keys)
			if test.capacity {
				if sets != nil || !errors.Is(err, tuplecache.ErrCapacity) || !errors.Is(err, tuplecache.ErrUnavailable) {
					t.Fatalf("capacity result: %v/%v", sets, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	// An oversized invalid payload must hit the byte gate before JSON decoding.
	if err := client.HSet(ctx, c.key, f, strings.Repeat("!", defaultMaxReadBytes+1)).Err(); err != nil {
		t.Fatal(err)
	}
	if sets, err := c.Read(ctx, state, []tuplecache.SetKey{forward(row)}); sets != nil || !errors.Is(err, tuplecache.ErrCapacity) {
		t.Fatalf("oversized malformed field: %v/%v", sets, err)
	}
}

func TestTupleCacheDeltaCapacityIsAtomic(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	next := tuplecache.State{Binding: "store", Receipt: "next"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	changes := []tuplecache.Change{{Before: &row}, {Before: &row}}
	payload, fields, err := changesWire(changes, defaultMaxMutationBytes)
	if err != nil {
		t.Fatal(err)
	}
	total := len(payload)
	for _, field := range fields {
		total += len(client.HGet(ctx, c.key, field).Val())
	}
	before := client.HGetAll(ctx, c.key).Val()
	// The script independently rejects incoming bytes before decoding them,
	// even if the caller bypasses the Go encoder with invalid JSON.
	status, err := deltaScript.Run(ctx, client, []string{c.key}, state.Binding, state.Receipt,
		next.Binding, next.Receipt, time.Now().Add(time.Minute).UnixMilli(), "not-json", 2).Int()
	if err != nil || status != -2 {
		t.Fatalf("incoming JSON preflight: %d/%v", status, err)
	}
	for _, limit := range []int{len(payload) - 1, total - 1} {
		limited := cacheWithLimits(t, client, Limits{MaxMutationBytes: limit})
		if err := limited.Publish(ctx, state, next, tuplecache.Snapshot{Changes: changes}, time.Minute); !errors.Is(err, tuplecache.ErrCapacity) {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if got := client.HGetAll(ctx, c.key).Val(); !reflect.DeepEqual(got, before) {
			t.Fatal("capacity rejection changed data or complete receipt")
		}
	}
	// The existing value is rejected by length, before its malformed JSON is decoded.
	field := fields[0]
	if err := client.HSet(ctx, c.key, field, strings.Repeat("!", total)).Err(); err != nil {
		t.Fatal(err)
	}
	limited := cacheWithLimits(t, client, Limits{MaxMutationBytes: total})
	if err := limited.Publish(ctx, state, next, tuplecache.Snapshot{Changes: changes}, time.Minute); !errors.Is(err, tuplecache.ErrCapacity) {
		t.Fatalf("oversized malformed set: %v", err)
	}
	if got, err := c.State(ctx); err != nil || got != state {
		t.Fatalf("capacity cleared readiness: %+v/%v", got, err)
	}
	if err := client.HSet(ctx, c.key, field, before[field]).Err(); err != nil {
		t.Fatal(err)
	}
	if err := limited.Publish(ctx, state, next, tuplecache.Snapshot{Changes: changes}, time.Minute); err != nil {
		t.Fatalf("exact boundary with deduplicated touched fields: %v", err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{}, {}})
}

func TestTupleCacheFullCapacityAndChunking(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	tuples := make([]tuplefacts.Tuple, 50)
	for i := range tuples {
		tuples[i] = tuple("space", fmt.Sprintf("%03d", i), "viewer", "user", fmt.Sprintf("%03d", i), "")
	}
	sets, err := fullSets(tuples, defaultMaxMutationBytes)
	if err != nil {
		t.Fatal(err)
	}
	maxField := 0
	for field, refs := range sets {
		encoded, err := json.Marshal(refs)
		if err != nil {
			t.Fatal(err)
		}
		maxField = max(maxField, len(field)+len(encoded))
	}
	next := tuplecache.State{Binding: "store", Receipt: "chunked"}
	before := client.HGetAll(ctx, c.key).Val()
	limited := cacheWithLimits(t, client, Limits{MaxMutationBytes: maxField - 1})
	if err := limited.Publish(ctx, state, next, tuplecache.Snapshot{Full: true, Tuples: tuples}, time.Minute); !errors.Is(err, tuplecache.ErrCapacity) {
		t.Fatalf("oversized full field: %v", err)
	}
	if got := client.HGetAll(ctx, c.key).Val(); !reflect.DeepEqual(got, before) {
		t.Fatal("rejected rebuild changed old mirror")
	}
	if keys := client.Keys(ctx, c.key+":build:*").Val(); len(keys) != 0 {
		t.Fatalf("temporary keys: %v", keys)
	}
	limited = cacheWithLimits(t, client, Limits{MaxMutationBytes: maxField})
	chunks := 0
	client.AddHook(commandHook{observe: func(cmd redis.Cmder) {
		if cmd.Name() != "evalsha" || cmd.Args()[1] != buildScript.Hash() {
			return
		}
		chunks++
		bytes := 0
		for _, arg := range cmd.Args()[4:] {
			bytes += len(arg.(string))
		}
		if bytes > maxField {
			t.Errorf("upload chunk %d bytes exceeds limit %d", bytes, maxField)
		}
	}})
	// The total mirror exceeds this limit many times over, but every field and
	// chunk fits. A total-mirror restriction would reject this valid publication.
	if err := limited.Publish(ctx, state, next, tuplecache.Snapshot{Full: true, Tuples: tuples}, time.Minute); err != nil {
		t.Fatalf("bounded chunk rebuild: %v", err)
	}
	if chunks < len(tuples) {
		t.Fatalf("expected many bounded uploads, got %d", chunks)
	}
	for _, row := range tuples {
		assertSets(t, c, next, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{row}, {row}})
	}
}

func TestTupleCacheOversizedInputEncoding(t *testing.T) {
	row := tuple("space", strings.Repeat("x", 100000), "viewer", "user", "alice", "")
	if _, _, err := changesWire([]tuplecache.Change{{After: &row}}, 100); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("huge identifier delta: %v", err)
	}
	if _, err := fullSets([]tuplefacts.Tuple{row}, 100); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("huge identifier full: %v", err)
	}
	row.Scope.ID = "a"
	changes := make([]tuplecache.Change, 100000)
	for i := range changes {
		changes[i].After = &row
	}
	if _, _, err := changesWire(changes, 1000); !errors.Is(err, tuplecache.ErrCapacity) {
		t.Fatalf("huge changes batch: %v", err)
	}
}

func TestTupleCachePausedServerAndCapacityFallBack(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprintf("paused=%v", paused), func(t *testing.T) {
			client, _ := startRedis(t, "", false)
			limits := Limits{}
			if !paused {
				limits.MaxReadBytes = 1
			}
			backend := cacheWithLimits(t, client, limits)
			row := tuple("space", "a", "viewer", "user", "alice", "")
			source := &redisTestSource{store: memory.New(), tuples: []tuplefacts.Tuple{row}}
			if err := source.store.Tuples().ApplyTuples(t.Context(), tuplefacts.Changes{Add: source.tuples}); err != nil {
				t.Fatal(err)
			}
			runtime, err := tuplecache.New(source, backend, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Minute, ReadTimeout: 20 * time.Millisecond}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			if err := runtime.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			if paused {
				// Warm both scripts and the connection before pausing actual Redis.
				state, err := backend.State(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := backend.Read(t.Context(), state, nil); err != nil {
					t.Fatal(err)
				}
				if err := client.Do(t.Context(), "CLIENT", "PAUSE", 1000, "ALL").Err(); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			var allowed bool
			err = runtime.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
				var err error
				allowed, err = reads.Contains(ctx, row)
				return err
			})
			elapsed := time.Since(started)
			if err != nil || !allowed || source.reads != 1 {
				t.Fatalf("durable fallback allowed=%v reads=%d err=%v", allowed, source.reads, err)
			}
			if elapsed > 300*time.Millisecond {
				t.Fatalf("20ms cache deadline delayed fallback for %v", elapsed)
			}
			if !paused && runtime.Stats().CapacityFallbacks != 1 {
				t.Fatalf("capacity fallback stats: %+v", runtime.Stats())
			}
			t.Logf("durable fallback completed in %s", elapsed)
		})
	}
}

func cacheWithLimits(t testing.TB, client *redis.Client, limits Limits) *TupleCache {
	t.Helper()
	c, err := NewTupleCache(client, "test:raw:sets", WithLimits(limits))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type redisTestSource struct {
	store   *memory.Store
	tuples  []tuplefacts.Tuple
	receipt string
	reads   int
}

type commandHook struct{ observe func(redis.Cmder) }

func (h commandHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h commandHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.observe(cmd); return next(ctx, cmd) }
}
func (h commandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (s *redisTestSource) Binding() string                       { return "redis-test" }
func (s *redisTestSource) CacheableContext(context.Context) bool { return true }
func (s *redisTestSource) Snapshot(context.Context, string) (tuplecache.Snapshot, error) {
	return tuplecache.Snapshot{Receipt: s.receipt, Full: true, Tuples: s.tuples}, nil
}
func (s *redisTestSource) Acknowledge(_ context.Context, expected, next string, _ []string) error {
	if s.receipt != expected {
		return tuplecache.ErrConflict
	}
	s.receipt = next
	return nil
}
func (s *redisTestSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	s.reads++
	return s.store.Tuples().ReadTupleSnapshot(ctx, fn)
}
