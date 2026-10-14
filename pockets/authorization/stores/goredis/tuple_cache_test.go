package goredis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	tuplefacts "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/redis/go-redis/v9"
)

func TestTupleCacheRawSetsAndTargetedChanges(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	a := tuple("space", "a", "viewer", "user", "alice", "")
	b := tuple("space", "b", "viewer", "user", "bob", "")
	if got, err := c.State(ctx, "store"); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("absent State: %v %v", got, err)
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("absent Read: %v", err)
	}
	// Full tuples already represent these changes. A reset event with no payload
	// is deliberately allowed here and must not be re-applied.
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{a, a, b}, Changes: []tuplecache.Change{{}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(a), reverse(a), forward(tuple("space", "missing", "viewer", "user", "x", ""))}, [][]tuplefacts.Tuple{{a}, {a}, {}})
	untouched := client.HGet(ctx, keyFor(t, c, "store"), setField(forward(b))).Val()
	updated := tuple("space", "a", "viewer", "team", "engineering", "member")
	membership := tuple("team", "engineering", "member", "user", "alice", "")
	next := tuplecache.State{Binding: "store", Receipt: "updated"}
	changes := []tuplecache.Change{{Before: &a, After: &updated}, {After: &membership}, {After: &membership}}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: changes}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(updated), reverse(a), reverse(updated), reverse(membership)}, [][]tuplefacts.Tuple{{updated}, {membership}, {updated}, {membership}})
	if got := client.HGet(ctx, keyFor(t, c, "store"), setField(forward(b))).Val(); got != untouched {
		t.Fatal("unrelated set changed")
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("old receipt: %v", err)
	}
	if ttl := client.PTTL(ctx, keyFor(t, c, "store")).Val(); ttl <= defaultExpiry-time.Minute || ttl > defaultExpiry {
		t.Fatalf("mirror retention not renewed to the default: %v", ttl)
	}
	last := tuplecache.State{Binding: "store", Receipt: "removed"}
	if err := c.Publish(ctx, next, last, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &updated}, {Before: &updated}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, last, []tuplecache.SetKey{forward(updated), reverse(updated)}, [][]tuplefacts.Tuple{{}, {}})
	if client.HExists(ctx, keyFor(t, c, "store"), setField(forward(updated))).Val() {
		t.Fatal("empty index field retained")
	}
}

func TestTupleCacheExactOpaqueReferences(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	tuples := []tuplefacts.Tuple{
		tuple("sp:ace", "a#b@\"\\é", "view#er", "team", "a:b", "member"),
		tuple("sp", "ace:a#b", "view#er", "team:a", "b", "member"),
		tuple("space", "東京", "viewer", "user", "éé", ""),
	}
	state := tuplecache.State{Binding: "store", Receipt: "opaque"}
	if err := c.Publish(context.Background(), tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: tuples}, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, row := range tuples {
		assertSets(t, c, state, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{row}, {row}})
	}
	next := tuplecache.State{Binding: "store", Receipt: "opaque-delta"}
	if err := c.Publish(context.Background(), state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &tuples[2]}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(tuples[2]), reverse(tuples[2])}, [][]tuplefacts.Tuple{{}, {}})
}

func TestTupleCacheCASAndAtomicIndexes(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := range 2 {
		go func() {
			results <- c.Publish(ctx, state, tuplecache.State{Binding: "store", Receipt: fmt.Sprint(i)}, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}}}, time.Minute)
		}()
	}
	var successes, conflicts int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, tuplecache.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("publication winners %d conflicts %d", successes, conflicts)
	}
	state, _ = c.State(ctx, "store")
	assertSets(t, c, state, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{}, {}})
	var wg sync.WaitGroup
	done := make(chan struct{})
	readErrors := make(chan error, 1)
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			observed, err := c.State(ctx, "store")
			if err != nil {
				readErrors <- err
				return
			}
			sets, err := c.Read(ctx, observed, []tuplecache.SetKey{forward(row), reverse(row)})
			if errors.Is(err, tuplecache.ErrUnavailable) {
				continue
			}
			if err != nil {
				readErrors <- err
				return
			}
			if len(sets[0]) != len(sets[1]) {
				readErrors <- fmt.Errorf("partial index publication: %v", sets)
				return
			}
		}
	})
	for i := range 40 {
		next := tuplecache.State{Binding: "store", Receipt: fmt.Sprintf("flip-%d", i)}
		change := tuplecache.Change{After: &row}
		if i%2 != 0 {
			change = tuplecache.Change{Before: &row}
		}
		if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{change}}, time.Minute); err != nil {
			t.Fatal(err)
		}
		state = next
	}
	close(done)
	wg.Wait()
	select {
	case err := <-readErrors:
		t.Fatal(err)
	default:
	}
}

func TestTupleCacheReadinessAndRebuild(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, keyFor(t, c, "store"), "!until", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := c.State(ctx, "store"); err != nil || got != state {
		t.Fatalf("expired receipt lost: %v %v", got, err)
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("expired validation: %v", err)
	}
	if err := c.Publish(ctx, state, state, tuplecache.Snapshot{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(row)}, [][]tuplefacts.Tuple{{row}})
	if err := client.Del(ctx, keyFor(t, c, "store")).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := c.State(ctx, "store"); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("lost mirror: %v %v", got, err)
	}
	if _, err := c.Read(ctx, state, []tuplecache.SetKey{forward(row)}); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("lost mirror became absence: %v", err)
	}
	if err := c.Publish(ctx, state, state, tuplecache.Snapshot{}, time.Minute); !errors.Is(err, tuplecache.ErrConflict) {
		t.Fatalf("lost mirror renewal: %v", err)
	}
	rebuilt := tuplecache.State{Binding: "store", Receipt: "rebuilt"}
	if err := c.Publish(ctx, tuplecache.State{}, rebuilt, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, rebuilt, []tuplecache.SetKey{forward(row)}, [][]tuplefacts.Tuple{{}})
	if err := client.HSet(ctx, keyFor(t, c, "store"), "!receipt", "").Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := c.State(ctx, "store"); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("corrupt mirror: %v %v", got, err)
	}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(row)}, [][]tuplefacts.Tuple{{row}})
}

func TestTupleCacheRejectsInvalidDataBeforeMutation(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	other := tuple("space", "b", "viewer", "user", "bob", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	next := tuplecache.State{Binding: "store", Receipt: "next"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row, other}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name           string
		expected, next tuplecache.State
		snapshot       tuplecache.Snapshot
		want           error
	}{
		{"empty change", state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{}}}, sdk.ErrInvalidInput},
		{"invalid tuple", state, next, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{{}}}, sdk.ErrInvalidInput},
		{"receipt reused", state, state, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}}}, sdk.ErrInvalidInput},
		{"wrong binding", state, tuplecache.State{Binding: "other", Receipt: "next"}, tuplecache.Snapshot{}, tuplecache.ErrBinding},
		{"stale writer", tuplecache.State{Binding: "store", Receipt: "old"}, next, tuplecache.Snapshot{Full: true}, tuplecache.ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := client.HGetAll(ctx, keyFor(t, c, "store")).Val()
			if err := c.Publish(ctx, test.expected, test.next, test.snapshot, time.Minute); !errors.Is(err, test.want) {
				t.Fatalf("error %v, want %v", err, test.want)
			}
			if got := client.HGetAll(ctx, keyFor(t, c, "store")).Val(); !reflect.DeepEqual(before, got) {
				t.Fatal("invalid publication mutated hash")
			}
		})
	}
	field := setField(forward(other))
	if err := client.HSet(ctx, keyFor(t, c, "store"), field, `[["bad"]]`).Err(); err != nil {
		t.Fatal(err)
	}
	before := client.HGetAll(ctx, keyFor(t, c, "store")).Val()
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}, {Before: &other}}}, time.Minute); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("malformed existing set: %v", err)
	}
	if got := client.HGetAll(ctx, keyFor(t, c, "store")).Val(); !reflect.DeepEqual(before, got) {
		t.Fatal("script partially mutated before validation error")
	}
	if _, err := c.Read(ctx, state, []tuplecache.SetKey{forward(other)}); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("malformed read: %v", err)
	}
}

func TestTupleCacheRestartPersistence(t *testing.T) {
	client, server := startRedis(t, "", true)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "persisted"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	server.stop()
	restored, _ := startRedis(t, server.dir, true)
	c = newCache(t, restored)
	if got, err := c.State(ctx, "store"); err != nil || got != state {
		t.Fatalf("restored state: %v %v", got, err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{row}, {row}})
}

func TestTupleCacheRuntimeRestartResumesDeltas(t *testing.T) {
	client, server := startRedis(t, "", true)
	row := tuple("space", "a", "viewer", "user", "alice", "")
	source := newDeltaSource(t, "redis-delta", row)
	first := newRuntime(t, source, newCache(t, client), time.Minute)
	poll(t, first)
	_ = first.Close()
	server.stop()
	restored, _ := startRedis(t, server.dir, true)
	source.revoke(t, row)
	resumed := newRuntime(t, source, newCache(t, restored), time.Minute)
	poll(t, resumed)
	assertAllowed(t, resumed, row, false)
	if stats := resumed.Stats(); stats.Rebuilds != 0 || stats.Publications != 1 || source.reads != 0 {
		t.Fatalf("restart with unchanged binding did not resume deltas: %+v reads=%d", stats, source.reads)
	}
}

// Acceptance for #76: bindings, not a hand-typed namespace, decide which
// processes share a mirror.
func TestTupleCacheBindingsAreIndependent(t *testing.T) {
	client, _ := startRedis(t, "", false)
	row := tuple("space", "a", "viewer", "user", "alice", "")
	source := newDeltaSource(t, "redis-delta", row)
	short := newRuntime(t, source, newCache(t, client), time.Second)
	poll(t, short)

	// An identical binding joins the existing mirror and resumes it.
	twin := newRuntime(t, source, newCache(t, client), time.Second)
	poll(t, twin)
	if twin.Stats().Rebuilds != 0 {
		t.Fatalf("identical binding rebuilt: %+v", twin.Stats())
	}
	source.revoke(t, row)
	results := make(chan error, 2)
	for _, runtime := range []*tuplecache.TupleCache{short, twin} {
		go func() { results <- runtime.Poll(t.Context()) }()
	}
	for range 2 {
		if err := <-results; err != nil && !errors.Is(err, tuplecache.ErrConflict) {
			t.Fatal(err)
		}
	}
	poll(t, short)
	poll(t, twin)
	if got := source.acknowledged["revoke"]; got != 1 {
		t.Fatalf("shared mirror acknowledged the delta %d times", got)
	}
	assertAllowed(t, short, row, false)
	assertAllowed(t, twin, row, false)

	// A different freshness policy gets its own mirror; neither refuses the other.
	long := newRuntime(t, source, newCache(t, client), 5*time.Minute)
	poll(t, long)
	assertAllowed(t, long, row, false)
	keys, err := client.Keys(t.Context(), "tuplecache:v3:*").Result()
	want := []string{"tuplecache:v3:{test:raw:sets/" + short.Binding() + "}", "tuplecache:v3:{test:raw:sets/" + long.Binding() + "}"}
	slices.Sort(keys)
	slices.Sort(want)
	if err != nil || !slices.Equal(keys, want) {
		t.Fatalf("different policies: keys %v/%v, want %v", keys, err, want)
	}
	// One source has one delivery receipt. long's rebuild acknowledged it, so
	// short's mirror is behind the source and its next poll rebuilds too.
	// It never settles, even with no writes: each poll rebuilds in full.
	for range 3 {
		for _, runtime := range []*tuplecache.TupleCache{short, long} {
			rebuilds := runtime.Stats().Rebuilds
			poll(t, runtime)
			if got := runtime.Stats().Rebuilds; got != rebuilds+1 {
				t.Fatalf("cross-binding receipt: rebuilds %d, want %d", got, rebuilds+1)
			}
		}
	}
	assertAllowed(t, short, row, false)
	if source.reads != 0 {
		t.Fatalf("ready runtimes used durable reads: %d", source.reads)
	}
}

// deltaSource is a SQL-like Source: one delivery receipt and an outbox of
// pending changes, acknowledged by exact ID.
type deltaSource struct {
	mu           sync.Mutex
	binding      string
	store        *memory.Store
	tuples       []tuplefacts.Tuple
	receipt      string
	pending      []tuplecache.Change
	acknowledged map[string]int
	reads        int
}

func newDeltaSource(t testing.TB, binding string, rows ...tuplefacts.Tuple) *deltaSource {
	t.Helper()
	s := &deltaSource{binding: binding, store: memory.New(), tuples: rows, acknowledged: make(map[string]int)}
	if err := s.store.Tuples().ApplyTuples(t.Context(), tuplefacts.Changes{Add: rows}); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *deltaSource) revoke(t testing.TB, row tuplefacts.Tuple) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.Tuples().ApplyTuples(t.Context(), tuplefacts.Changes{Remove: []tuplefacts.Tuple{row}}); err != nil {
		t.Fatal(err)
	}
	s.tuples = slices.DeleteFunc(slices.Clone(s.tuples), func(r tuplefacts.Tuple) bool { return r == row })
	s.pending = append(s.pending, tuplecache.Change{ID: "revoke", Before: &row})
}

func (s *deltaSource) Binding() string                       { return s.binding }
func (s *deltaSource) CacheableContext(context.Context) bool { return true }
func (s *deltaSource) Snapshot(_ context.Context, mirrored string) (tuplecache.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	full := mirrored == "" || mirrored != s.receipt
	return tuplecache.Snapshot{Receipt: s.receipt, Full: full, Tuples: slices.Clone(s.tuples), Changes: slices.Clone(s.pending)}, nil
}
func (s *deltaSource) Acknowledge(_ context.Context, expected, next string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.receipt || len(ids) > len(s.pending) {
		return tuplecache.ErrConflict
	}
	for i, id := range ids {
		if id != s.pending[i].ID {
			return fmt.Errorf("wrong acknowledgement %q", id)
		}
		s.acknowledged[id]++
	}
	s.receipt, s.pending = next, s.pending[len(ids):]
	return nil
}
func (s *deltaSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return s.store.Tuples().ReadTupleSnapshot(ctx, fn)
}

func newRuntime(t testing.TB, source tuplecache.Source, backend tuplecache.Backend, maxStaleness time.Duration) *tuplecache.TupleCache {
	t.Helper()
	runtime, err := tuplecache.New(source, backend, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: maxStaleness}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func poll(t testing.TB, runtime *tuplecache.TupleCache) {
	t.Helper()
	if err := runtime.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func assertAllowed(t testing.TB, runtime *tuplecache.TupleCache, row tuplefacts.Tuple, want bool) {
	t.Helper()
	var got bool
	err := runtime.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		var err error
		got, err = reads.Contains(ctx, row)
		return err
	})
	if err != nil || got != want {
		t.Fatalf("allowed=%v want=%v err=%v", got, want, err)
	}
}

func TestTupleCachePublicationDoesNotRefreshLongBuild(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	client.AddHook(delayHook{command: "evalsha", script: buildScript.Hash(), duration: 80 * time.Millisecond})
	row := tuple("space", "a", "viewer", "user", "alice", "")
	next := tuplecache.State{Binding: "store", Receipt: "late"}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, 40*time.Millisecond); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("late full publication: %v", err)
	}
	if got, err := c.State(ctx, "store"); err != nil || got != state {
		t.Fatalf("late build replaced mirror: %v %v", got, err)
	}
	keys := client.Keys(ctx, keyFor(t, c, "store")+":build:*").Val()
	if len(keys) != 0 {
		t.Fatalf("temporary hashes leaked: %v", keys)
	}
}

func TestTupleCacheBatchesBeyondRebuildChunk(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	tuples := make([]tuplefacts.Tuple, 600)
	keys := make([]tuplecache.SetKey, len(tuples))
	want := make([][]tuplefacts.Tuple, len(tuples))
	for i := range tuples {
		tuples[i] = tuple("space", fmt.Sprint(i), "viewer", "user", "alice", "")
		keys[i] = forward(tuples[i])
		want[i] = []tuplefacts.Tuple{tuples[i]}
	}
	state := tuplecache.State{Binding: "store", Receipt: "full"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: tuples}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, keys, want)
	changes := make([]tuplecache.Change, len(tuples))
	for i := range tuples {
		changes[i].Before = &tuples[i]
		want[i] = []tuplefacts.Tuple{}
	}
	next := tuplecache.State{Binding: "store", Receipt: "deleted"}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: changes}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, keys, want)
	if fields := client.HLen(ctx, keyFor(t, c, "store")).Val(); fields != 4 {
		t.Fatalf("remaining fields %d, want metadata only", fields)
	}
}

func TestTupleCacheInterruptedScriptLeavesUnavailableMirror(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	// Fail a Redis command after the script has removed the complete marker.
	// Redis does not undo that earlier write when the later HSET is rejected.
	if err := client.Do(ctx, "ACL", "SETUSER", "no-hset", "on", ">test-password", "~*", "+@all", "-hset").Err(); err != nil {
		t.Fatal(err)
	}
	options := *client.Options()
	options.Username, options.Password = "no-hset", "test-password"
	restricted := redis.NewClient(&options)
	t.Cleanup(func() { _ = restricted.Close() })
	failing := newCache(t, restricted)
	next := tuplecache.State{Binding: "store", Receipt: "failed"}
	if err := failing.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}}}, time.Minute); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("script error: %v", err)
	}
	if got, err := c.State(ctx, "store"); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("partially applied script still ready: %v %v", got, err)
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("partially applied script read: %v", err)
	}
	if err := c.Publish(ctx, tuplecache.State{}, next, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{}, {}})
}

func TestTupleCacheLostTemporaryHashIsNotRecreated(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	temporary := keyFor(t, c, "store") + ":build:lost"
	if err := prepareScript.Run(ctx, client, []string{temporary}, temporaryTTL.Milliseconds()).Err(); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(ctx, temporary).Val(); ttl <= 0 {
		t.Fatalf("temporary key has no expiry: %v", ttl)
	}
	if err := client.Del(ctx, temporary).Err(); err != nil {
		t.Fatal(err)
	}
	if err := buildScript.Run(ctx, client, []string{temporary}, "field", "[]").Err(); err == nil {
		t.Fatal("lost temporary build accepted a chunk")
	}
	if exists := client.Exists(ctx, temporary).Val(); exists != 0 {
		t.Fatal("lost temporary key recreated without expiration")
	}
}

func TestTupleCacheDelayedReadCannotOutliveEligibility(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(ctx, state, nil); err != nil {
		t.Fatal(err)
	}
	serverTime, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, keyFor(t, c, "store"), "!until", serverTime.UnixMilli()+40).Err(); err != nil {
		t.Fatal(err)
	}
	client.AddHook(delayHook{command: "evalsha", script: readScript.Hash(), duration: 80 * time.Millisecond, after: true})
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("delayed eligible read: %v", err)
	}
}

func TestTupleCacheConstructor(t *testing.T) {
	if _, err := NewTupleCache(nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil client: %v", err)
	}
	client := redis.NewClient(&redis.Options{ContextTimeoutEnabled: true, Dialer: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("constructor performed I/O")
		return nil, errors.New("unexpected dial")
	}})
	t.Cleanup(func() { _ = client.Close() })
	invalid := []string{"", "test:{raw}:sets", "app}", "{app", "app name", "app\n", "app\x00", "app*", "app?", "app[1]", "app\\name", "caf\u00e9", "app\xff"}
	for _, namespace := range invalid {
		if cache, err := NewTupleCache(client, WithNamespace(namespace)); cache != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid namespace %q: %v/%v", namespace, cache, err)
		}
	}
	const binding = "protocol:2/pgx:schema.v1/max-staleness:1000000000"
	for _, namespace := range []string{"segovia-v2:dev:authorization", "App_1.2/relationships", "app:build:123"} {
		cache, err := NewTupleCache(client, WithNamespace(namespace))
		if err != nil {
			t.Fatalf("readable namespace %q: %v", namespace, err)
		}
		if key, err := cache.Key(binding); err != nil || key != "tuplecache:v3:{"+namespace+"/"+binding+"}" {
			t.Fatalf("namespaced key %q: %q/%v", namespace, key, err)
		}
	}
	cache, err := NewTupleCache(client)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := cache.Key(binding); err != nil || key != "tuplecache:v3:{"+binding+"}" {
		t.Fatalf("unprefixed key: %q/%v", key, err)
	}
	// Custom sources choose their binding, so the adapter checks it per call.
	for _, binding := range invalid {
		if key, err := cache.Key(binding); key != "" || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid binding %q: %q/%v", binding, key, err)
		}
	}
}

// F5: every publication renews the mirror's retention, so only a superseded
// binding's mirror lapses.
func TestTupleCacheExpiryRenewsOnEveryPublication(t *testing.T) {
	client, _ := startRedis(t, "", false)
	ctx := t.Context()
	c, err := NewTupleCache(client, WithNamespace("expiry"), WithExpiry(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	key := keyFor(t, c, "store")
	renewed := func(stage string) {
		t.Helper()
		if ttl := client.PTTL(ctx, key).Val(); ttl <= 59*time.Minute || ttl > time.Hour {
			t.Fatalf("%s did not renew retention: %v", stage, ttl)
		}
		if err := client.PExpire(ctx, key, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "full"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	renewed("full rebuild")
	next := tuplecache.State{Binding: "store", Receipt: "delta"}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	renewed("delta")
	if err := c.Publish(ctx, next, next, tuplecache.Snapshot{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	renewed("no-change renewal")

	forever, err := NewTupleCache(client, WithNamespace("expiry"), WithExpiry(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := forever.Publish(ctx, next, next, tuplecache.Snapshot{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ttl := client.TTL(ctx, key).Val(); ttl != -1 {
		t.Fatalf("WithExpiry(0) kept a TTL: %v", ttl)
	}

	orphan, err := NewTupleCache(client, WithNamespace("orphan"), WithExpiry(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := orphan.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got, err := orphan.State(ctx, "store"); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("unrenewed mirror did not lapse: %+v/%v", got, err)
	}
	for _, expiry := range []time.Duration{-time.Second, time.Microsecond} {
		if cache, err := NewTupleCache(client, WithExpiry(expiry)); cache != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid expiry %v: %v/%v", expiry, cache, err)
		}
	}
}

func TestTupleCacheKeyLayout(t *testing.T) {
	client, _ := startRedis(t, "", false)
	ctx := t.Context()
	plain, err := NewTupleCache(client)
	if err != nil {
		t.Fatal(err)
	}
	prefixed := newCache(t, client)
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	for _, c := range []*TupleCache{plain, prefixed} {
		if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := client.Keys(ctx, "tuplecache:*").Result()
	slices.Sort(keys)
	if want := []string{"tuplecache:v3:{store}", "tuplecache:v3:{test:raw:sets/store}"}; err != nil || !slices.Equal(keys, want) {
		t.Fatalf("keys %v/%v, want %v", keys, err, want)
	}
	for _, key := range keys {
		if kind := client.Type(ctx, key).Val(); kind != "hash" {
			t.Fatalf("%s is a %s", key, kind)
		}
	}
	escaping := tuplecache.State{Binding: "store}{other", Receipt: "x"}
	if _, err := plain.State(ctx, escaping.Binding); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("State accepted escaping binding: %v", err)
	}
	if _, err := plain.Read(ctx, escaping, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Read accepted escaping binding: %v", err)
	}
	if err := plain.Publish(ctx, tuplecache.State{}, escaping, tuplecache.Snapshot{Full: true}, time.Minute); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Publish accepted escaping binding: %v", err)
	}
	if n := client.DBSize(ctx).Val(); n != 2 {
		t.Fatalf("rejected binding wrote keys: %d", n)
	}
}

func TestTupleCacheReadableNamespaces(t *testing.T) {
	client, _ := startRedis(t, "", false)
	ctx := t.Context()
	// The previous adapter's hash for namespace "segovia" is a different key.
	const legacy = "gopernicus:tuplecache:{c2Vnb3ZpYQ}:mirror"
	if err := client.HSet(ctx, legacy, "sentinel", "old").Err(); err != nil {
		t.Fatal(err)
	}
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	var caches []*TupleCache
	var wantKeys []string
	namespaces := []string{"segovia", "segovia-v2:dev:authorization", "segovia-v2.dev.authorization", "my-app/relationships"}
	for _, namespace := range namespaces {
		cache, err := NewTupleCache(client, WithNamespace(namespace))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := cache.State(ctx, state.Binding); err != nil || got != (tuplecache.State{}) {
			t.Fatalf("fresh namespace inherited a mirror: %+v/%v", got, err)
		}
		row := tuple("space", "a", "viewer", "user", namespace, "")
		if err := cache.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
			t.Fatal(err)
		}
		caches = append(caches, cache)
		wantKeys = append(wantKeys, "tuplecache:v3:{"+namespace+"/store}")
	}
	for i, cache := range caches {
		row := tuple("space", "a", "viewer", "user", namespaces[i], "")
		assertSets(t, cache, state, []tuplecache.SetKey{forward(row)}, [][]tuplefacts.Tuple{{row}})
	}
	keys, err := client.Keys(ctx, "tuplecache:*").Result()
	slices.Sort(keys)
	slices.Sort(wantKeys)
	if err != nil || !slices.Equal(keys, wantKeys) {
		t.Fatalf("readable mirrors or build cleanup: %v/%v, want %v", keys, err, wantKeys)
	}
	if got, err := client.HGet(ctx, legacy, "sentinel").Result(); err != nil || got != "old" {
		t.Fatalf("legacy hash changed: %q/%v", got, err)
	}
}

func newCache(t testing.TB, client *redis.Client) *TupleCache {
	t.Helper()
	c, err := NewTupleCache(client, WithNamespace("test:raw:sets"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// keyFor returns c's mirror key for binding.
func keyFor(t testing.TB, c *TupleCache, binding string) string {
	t.Helper()
	key, err := c.Key(binding)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func tuple(rt, rid, relation, st, sid, sr string) tuplefacts.Tuple {
	return tuplefacts.Tuple{Scope: tuplefacts.On(rt, rid), Relation: relation, Subject: tuplefacts.SubjectRef{Type: st, ID: sid, Relation: sr}}
}
func forward(t tuplefacts.Tuple) tuplecache.SetKey {
	return tuplecache.SetKey{Scope: t.Scope, Relation: t.Relation}
}
func reverse(t tuplefacts.Tuple) tuplecache.SetKey {
	return tuplecache.SetKey{Reverse: true, Subject: t.Subject}
}
func assertSets(t *testing.T, c *TupleCache, state tuplecache.State, keys []tuplecache.SetKey, want [][]tuplefacts.Tuple) {
	t.Helper()
	got, err := c.Read(context.Background(), state, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("sets %v want %v", got, want)
	}
	for i := range got {
		sortRefs(got[i])
		sortRefs(want[i])
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("set %d = %v want %v", i, got[i], want[i])
		}
	}
}
func sortRefs(refs []tuplefacts.Tuple) {
	slices.SortFunc(refs, func(a, b tuplefacts.Tuple) int {
		return bytes.Compare([]byte(fmt.Sprintf("%q", a)), []byte(fmt.Sprintf("%q", b)))
	})
}

type testRedis struct {
	dir  string
	stop func()
}

func startRedis(t testing.TB, dir string, persistence bool) (*redis.Client, testRedis) {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		if _, err := os.Stat("/opt/homebrew/bin/redis-server"); err != nil {
			t.Skip("redis-server is not installed")
		}
		binary = "/opt/homebrew/bin/redis-server"
	}
	if dir == "" {
		dir, err = os.MkdirTemp("/tmp", "tuplecache-redis-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	socket := filepath.Join(dir, "redis.sock")
	args := []string{"--port", "0", "--unixsocket", socket, "--unixsocketperm", "700", "--dir", dir, "--save", "", "--loglevel", "warning"}
	if persistence {
		args = append(args, "--appendonly", "yes", "--appendfsync", "always")
	}
	cmd := exec.Command(binary, args...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() { once.Do(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) }
	t.Cleanup(stop)
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := client.Ping(context.Background()).Err(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("redis did not start: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return client, testRedis{dir: dir, stop: stop}
}

type delayHook struct {
	command  string
	script   string
	duration time.Duration
	after    bool
}

func (h delayHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h delayHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		matches := cmd.Name() == h.command && (h.script == "" || cmd.Args()[1] == h.script)
		if matches && !h.after {
			time.Sleep(h.duration)
		}
		err := next(ctx, cmd)
		if matches && h.after {
			time.Sleep(h.duration)
		}
		return err
	}
}
func (h delayHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestTupleCacheBackendConformance(t *testing.T) {
	storetest.RunTupleCacheBackend(t, func(t *testing.T) tuplecache.Backend {
		client, _ := startRedis(t, "", false)
		return newCache(t, client)
	})
}
