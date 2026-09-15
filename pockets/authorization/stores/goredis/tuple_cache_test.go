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

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
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
	if got, err := c.State(ctx); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("absent State: %v %v", got, err)
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("absent Read: %v", err)
	}
	// Full tuples already represent these changes. A reset event with no payload
	// is deliberately allowed here and must not be re-applied.
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{a, a, b}, Changes: []tuplecache.Change{{}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(a), reverse(a), forward(tuple("space", "missing", "viewer", "user", "x", ""))}, [][]relationships.SubjectRef{{subject(a)}, {resource(a)}, {}})
	untouched := client.HGet(ctx, c.key, setField(false, toWire(resource(b)))).Val()
	updated := tuple("space", "a", "viewer", "team", "engineering", "member")
	membership := tuple("team", "engineering", "member", "user", "alice", "")
	next := tuplecache.State{Binding: "store", Receipt: "updated"}
	changes := []tuplecache.Change{{Before: &a, After: &updated}, {After: &membership}, {After: &membership}}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: changes}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(updated), reverse(a), reverse(updated), reverse(membership)}, [][]relationships.SubjectRef{{subject(updated)}, {resource(membership)}, {resource(updated)}, {resource(membership)}})
	if got := client.HGet(ctx, c.key, setField(false, toWire(resource(b)))).Val(); got != untouched {
		t.Fatal("unrelated set changed")
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("old receipt: %v", err)
	}
	if ttl := client.TTL(ctx, c.key).Val(); ttl != -1 {
		t.Fatalf("ordinary mirror has TTL: %v", ttl)
	}
	last := tuplecache.State{Binding: "store", Receipt: "removed"}
	if err := c.Publish(ctx, next, last, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &updated}, {Before: &updated}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, last, []tuplecache.SetKey{forward(updated), reverse(updated)}, [][]relationships.SubjectRef{{}, {}})
	if client.HExists(ctx, c.key, setField(false, toWire(resource(updated)))).Val() {
		t.Fatal("empty index field retained")
	}
}

func TestTupleCacheExactOpaqueReferences(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	tuples := []relationships.CreateRelationship{
		tuple("sp:ace", "a#b@\"\\\x00\xff", "view#er", "team", "a:b", "member"),
		tuple("sp", "ace:a#b", "view#er", "team:a", "b", "member"),
		tuple("space", "\xfb\xff", "viewer", "user", "\xff\xff", ""),
	}
	state := tuplecache.State{Binding: "store", Receipt: "opaque"}
	if err := c.Publish(context.Background(), tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: tuples}, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, row := range tuples {
		assertSets(t, c, state, []tuplecache.SetKey{forward(row), reverse(row)}, [][]relationships.SubjectRef{{subject(row)}, {resource(row)}})
	}
	next := tuplecache.State{Binding: "store", Receipt: "opaque-delta"}
	if err := c.Publish(context.Background(), state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &tuples[2]}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(tuples[2]), reverse(tuples[2])}, [][]relationships.SubjectRef{{}, {}})
}

func TestTupleCacheCASAndAtomicIndexes(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{row}}, time.Minute); err != nil {
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
	state, _ = c.State(ctx)
	assertSets(t, c, state, []tuplecache.SetKey{forward(row), reverse(row)}, [][]relationships.SubjectRef{{}, {}})
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
			observed, err := c.State(ctx)
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
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, c.key, "!until", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := c.State(ctx); err != nil || got != state {
		t.Fatalf("expired receipt lost: %v %v", got, err)
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("expired validation: %v", err)
	}
	if err := c.Publish(ctx, state, state, tuplecache.Snapshot{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(row)}, [][]relationships.SubjectRef{{subject(row)}})
	if err := client.Del(ctx, c.key).Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := c.State(ctx); err != nil || got != (tuplecache.State{}) {
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
	assertSets(t, c, rebuilt, []tuplecache.SetKey{forward(row)}, [][]relationships.SubjectRef{{}})
	if err := client.HSet(ctx, c.key, "!receipt", "").Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := c.State(ctx); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("corrupt mirror: %v %v", got, err)
	}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(row)}, [][]relationships.SubjectRef{{subject(row)}})
}

func TestTupleCacheRejectsInvalidDataBeforeMutation(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	other := tuple("space", "b", "viewer", "user", "bob", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	next := tuplecache.State{Binding: "store", Receipt: "next"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{row, other}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name           string
		expected, next tuplecache.State
		snapshot       tuplecache.Snapshot
		want           error
	}{
		{"empty change", state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{}}}, sdk.ErrInvalidInput},
		{"invalid tuple", state, next, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{{}}}, sdk.ErrInvalidInput},
		{"receipt reused", state, state, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}}}, sdk.ErrInvalidInput},
		{"wrong binding", state, tuplecache.State{Binding: "other", Receipt: "next"}, tuplecache.Snapshot{}, tuplecache.ErrBinding},
		{"stale writer", tuplecache.State{Binding: "store", Receipt: "old"}, next, tuplecache.Snapshot{Full: true}, tuplecache.ErrConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := client.HGetAll(ctx, c.key).Val()
			if err := c.Publish(ctx, test.expected, test.next, test.snapshot, time.Minute); !errors.Is(err, test.want) {
				t.Fatalf("error %v, want %v", err, test.want)
			}
			if got := client.HGetAll(ctx, c.key).Val(); !reflect.DeepEqual(before, got) {
				t.Fatal("invalid publication mutated hash")
			}
		})
	}
	field := setField(false, toWire(resource(other)))
	if err := client.HSet(ctx, c.key, field, `[["bad"]]`).Err(); err != nil {
		t.Fatal(err)
	}
	before := client.HGetAll(ctx, c.key).Val()
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}, {Before: &other}}}, time.Minute); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("malformed existing set: %v", err)
	}
	if got := client.HGetAll(ctx, c.key).Val(); !reflect.DeepEqual(before, got) {
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
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	server.stop()
	restored, _ := startRedis(t, server.dir, true)
	c = newCache(t, restored)
	if got, err := c.State(ctx); err != nil || got != state {
		t.Fatalf("restored state: %v %v", got, err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(row), reverse(row)}, [][]relationships.SubjectRef{{subject(row)}, {resource(row)}})
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
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{row}}, 40*time.Millisecond); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("late full publication: %v", err)
	}
	if got, err := c.State(ctx); err != nil || got != state {
		t.Fatalf("late build replaced mirror: %v %v", got, err)
	}
	keys := client.Keys(ctx, c.key+":build:*").Val()
	if len(keys) != 0 {
		t.Fatalf("temporary hashes leaked: %v", keys)
	}
}

func TestTupleCacheBatchesBeyondRebuildChunk(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	tuples := make([]relationships.CreateRelationship, 600)
	keys := make([]tuplecache.SetKey, len(tuples))
	want := make([][]relationships.SubjectRef, len(tuples))
	for i := range tuples {
		tuples[i] = tuple("space", fmt.Sprint(i), "viewer", "user", "alice", "")
		keys[i] = forward(tuples[i])
		want[i] = []relationships.SubjectRef{subject(tuples[i])}
	}
	state := tuplecache.State{Binding: "store", Receipt: "full"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: tuples}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, keys, want)
	changes := make([]tuplecache.Change, len(tuples))
	for i := range tuples {
		changes[i].Before = &tuples[i]
		want[i] = []relationships.SubjectRef{}
	}
	next := tuplecache.State{Binding: "store", Receipt: "deleted"}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: changes}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, keys, want)
	if fields := client.HLen(ctx, c.key).Val(); fields != 4 {
		t.Fatalf("remaining fields %d, want metadata only", fields)
	}
}

func TestTupleCacheInterruptedScriptLeavesUnavailableMirror(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{row}}, time.Minute); err != nil {
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
	if got, err := c.State(ctx); err != nil || got != (tuplecache.State{}) {
		t.Fatalf("partially applied script still ready: %v %v", got, err)
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("partially applied script read: %v", err)
	}
	if err := c.Publish(ctx, tuplecache.State{}, next, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(row), reverse(row)}, [][]relationships.SubjectRef{{}, {}})
}

func TestTupleCacheLostTemporaryHashIsNotRecreated(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := context.Background()
	temporary := c.key + ":build:lost"
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
	if err := client.HSet(ctx, c.key, "!until", serverTime.UnixMilli()+40).Err(); err != nil {
		t.Fatal(err)
	}
	client.AddHook(delayHook{command: "evalsha", script: readScript.Hash(), duration: 80 * time.Millisecond, after: true})
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("delayed eligible read: %v", err)
	}
}

func TestTupleCacheConstructor(t *testing.T) {
	if _, err := NewTupleCache(nil, "namespace"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil client: %v", err)
	}
	if _, err := NewTupleCache(redis.NewClient(&redis.Options{}), ""); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty namespace: %v", err)
	}
}

func newCache(t *testing.T, client *redis.Client) *TupleCache {
	t.Helper()
	c, err := NewTupleCache(client, "test:{raw}:sets")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tuple(rt, rid, relation, st, sid, sr string) relationships.CreateRelationship {
	return relationships.CreateRelationship{ResourceType: rt, ResourceID: rid, Relation: relation, SubjectType: st, SubjectID: sid, SubjectRelation: sr}
}
func resource(t relationships.CreateRelationship) relationships.SubjectRef {
	return relationships.SubjectRef{Type: t.ResourceType, ID: t.ResourceID, Relation: t.Relation}
}
func subject(t relationships.CreateRelationship) relationships.SubjectRef {
	return relationships.SubjectRef{Type: t.SubjectType, ID: t.SubjectID, Relation: t.SubjectRelation}
}
func forward(t relationships.CreateRelationship) tuplecache.SetKey {
	return tuplecache.SetKey{Ref: resource(t)}
}
func reverse(t relationships.CreateRelationship) tuplecache.SetKey {
	return tuplecache.SetKey{Reverse: true, Ref: subject(t)}
}
func assertSets(t *testing.T, c *TupleCache, state tuplecache.State, keys []tuplecache.SetKey, want [][]relationships.SubjectRef) {
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
func sortRefs(refs []relationships.SubjectRef) {
	slices.SortFunc(refs, func(a, b relationships.SubjectRef) int {
		return bytes.Compare([]byte(fmt.Sprintf("%q", a)), []byte(fmt.Sprintf("%q", b)))
	})
}

type testRedis struct {
	dir  string
	stop func()
}

func startRedis(t *testing.T, dir string, persistence bool) (*redis.Client, testRedis) {
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
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, MaxRetries: -1})
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
