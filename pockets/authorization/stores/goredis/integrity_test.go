package goredis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	tuplefacts "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/redis/go-redis/v9"
)

var (
	keyA = bytes.Repeat([]byte{'a'}, MinIntegrityKeyBytes)
	keyB = bytes.Repeat([]byte{'b'}, MinIntegrityKeyBytes)
)

// withTag returns row's eight-field wire fact with an arbitrary tag.
func withTag(row tuplefacts.Tuple, tag string) wireFact {
	w := toWire(row)
	return append(w[:], tag)
}

func keyedCache(t testing.TB, client *redis.Client, current []byte, previous ...[]byte) *TupleCache {
	t.Helper()
	c, err := NewTupleCache(client, WithNamespace("test:raw:sets"), WithIntegrityKeys(current, previous...))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// forgeField overwrites one set field with facts that carry the given tags,
// standing in for a Redis writer that does not hold the key.
func forgeField(t testing.TB, client *redis.Client, c *TupleCache, binding, field string, facts ...wireFact) {
	t.Helper()
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(t.Context(), keyFor(t, c, binding), field, string(encoded)).Err(); err != nil {
		t.Fatal(err)
	}
}

// assertDurable proves an answer came from the durable source after one
// integrity failure.
func assertDurable(t testing.TB, runtime *tuplecache.TupleCache, backend *TupleCache, row tuplefacts.Tuple, want bool) {
	t.Helper()
	before, failures := runtime.Stats(), backend.Stats().IntegrityFailures
	assertAllowed(t, runtime, row, want)
	after := runtime.Stats()
	if after.Fallbacks != before.Fallbacks+1 || after.Hits != before.Hits {
		t.Fatalf("answer did not come from the durable path: %+v -> %+v", before, after)
	}
	if got := backend.Stats().IntegrityFailures; got <= failures {
		t.Fatalf("integrity failures %d, want more than %d", got, failures)
	}
}

func assertHit(t testing.TB, runtime *tuplecache.TupleCache, row tuplefacts.Tuple, want bool) {
	t.Helper()
	hits := runtime.Stats().Hits
	assertAllowed(t, runtime, row, want)
	if got := runtime.Stats().Hits; got != hits+1 {
		t.Fatalf("answer did not come from the mirror: hits %d -> %d", hits, got)
	}
}

func TestIntegrityKeysValidation(t *testing.T) {
	client := redis.NewClient(&redis.Options{ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	for _, opt := range []Option{
		WithIntegrityKeys(nil),
		WithIntegrityKeys(keyA[:MinIntegrityKeyBytes-1]),
		WithIntegrityKeys(keyA, keyB[:1]),
	} {
		if c, err := NewTupleCache(client, opt); c != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("short key accepted: %v/%v", c, err)
		}
	}
	current := slices.Clone(keyA)
	c, err := NewTupleCache(client, WithIntegrityKeys(current))
	if err != nil {
		t.Fatal(err)
	}
	current[0] = 'z'
	if !bytes.Equal(c.ring.keys[0], keyA) {
		t.Fatal("the option retained the caller's key slice")
	}
	if key, err := c.Key("store"); err != nil || key != "tuplecache:v3:ik:{store}" {
		t.Fatalf("keyed key: %q/%v", key, err)
	}
}

func TestIntegrityKeyedBackendConformance(t *testing.T) {
	storetest.RunTupleCacheBackend(t, func(t *testing.T) tuplecache.Backend {
		client, _ := startRedis(t, "", false)
		return keyedCache(t, client, keyA)
	})
}

// The keyed mirror format: 8-field facts with a 22-character tag, and a
// header MAC beside the epoch.
func TestIntegrityKeyedWireFormat(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := keyedCache(t, client, keyA)
	ctx := t.Context()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	var facts [][]string
	if err := json.Unmarshal([]byte(client.HGet(ctx, keyFor(t, c, "store"), setField(forward(row))).Val()), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || len(facts[0]) != 8 || len(facts[0][7]) != factTagChars {
		t.Fatalf("keyed fact shape: %v", facts)
	}
	meta := client.HMGet(ctx, keyFor(t, c, "store"), "!epoch", "!epoch_at", "!mac").Val()
	for i, v := range meta {
		if s, _ := v.(string); s == "" {
			t.Fatalf("header field %d missing: %v", i, meta)
		}
	}
	next := tuplecache.State{Binding: "store", Receipt: "next"}
	other := tuple("space", "a", "viewer", "user", "bob", "")
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{After: &other}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(row)}, [][]tuplefacts.Tuple{{row, other}})
	if got := client.HGet(ctx, keyFor(t, c, "store"), "!mac").Val(); got == meta[2] {
		t.Fatal("delta did not re-MAC the header")
	}
	if got := client.HGet(ctx, keyFor(t, c, "store"), "!epoch").Val(); got != meta[0] {
		t.Fatal("delta changed the epoch")
	}
}

// §2: a forged fact (a wrong tag, or no tag) is never read as a grant.
func TestIntegrityForgedFactsFallBack(t *testing.T) {
	member := tuple("space", "a", "viewer", "user", "alice", "")
	forged := tuple("space", "a", "viewer", "user", "mallory", "")
	for _, test := range []struct {
		name string
		fact func(*factSigner) wireFact
	}{
		{"wrong tag", func(*factSigner) wireFact { return withTag(forged, strings.Repeat("A", factTagChars)) }},
		{"seven fields", func(*factSigner) wireFact { return withTag(forged, "")[:7] }},
		{"another epoch's tag", func(valid *factSigner) wireFact {
			return newFactSigner(keyA, valid.binding, "other-epoch").seal(toWire(forged))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, _ := startRedis(t, "", false)
			backend := keyedCache(t, client, keyA)
			source := newDeltaSource(t, "redis-integrity", member)
			runtime := newRuntime(t, source, backend, time.Minute)
			poll(t, runtime)
			assertHit(t, runtime, member, true)
			valid := newFactSigner(keyA, runtime.Binding(), client.HGet(t.Context(), keyFor(t, backend, runtime.Binding()), "!epoch").Val())
			forgeField(t, client, backend, runtime.Binding(), setField(forward(forged)), valid.seal(toWire(member)), test.fact(valid))
			forgeField(t, client, backend, runtime.Binding(), setField(reverse(forged)), test.fact(valid))
			assertDurable(t, runtime, backend, forged, false)
			if source.reads != 1 {
				t.Fatalf("durable reads %d, want 1", source.reads)
			}
		})
	}
}

// §2: a rewritten header field fails the MAC; the mirror reads as absent and
// the next poll replaces it in full.
func TestIntegrityTamperedHeaderRebuilds(t *testing.T) {
	member := tuple("space", "a", "viewer", "user", "alice", "")
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	for _, test := range []struct {
		field string
		value any
	}{
		{"!until", future},
		{"!epoch_at", future},
		{"!epoch", "00000000000000000000000000000000"},
		{"!mac", strings.Repeat("A", 43)},
	} {
		t.Run(test.field, func(t *testing.T) {
			client, _ := startRedis(t, "", false)
			backend := keyedCache(t, client, keyA)
			source := newDeltaSource(t, "redis-integrity", member)
			runtime := newRuntime(t, source, backend, time.Minute)
			poll(t, runtime)
			key := keyFor(t, backend, runtime.Binding())
			if err := client.HSet(t.Context(), key, test.field, test.value).Err(); err != nil {
				t.Fatal(err)
			}
			if state, err := backend.State(t.Context(), runtime.Binding()); err != nil || state != (tuplecache.State{}) {
				t.Fatalf("tampered header state: %+v/%v", state, err)
			}
			assertDurable(t, runtime, backend, member, true)
			rebuilds := runtime.Stats().Rebuilds
			poll(t, runtime)
			if got := runtime.Stats().Rebuilds; got != rebuilds+1 {
				t.Fatalf("tampered mirror was not rebuilt: %d -> %d", rebuilds, got)
			}
			assertHit(t, runtime, member, true)
		})
	}
}

// §2: an older mirror restored over the current one keeps its old deadline,
// so it is ineligible once MaxStaleness passes, and the next poll replaces it.
func TestIntegrityRestoredMirrorIsIneligible(t *testing.T) {
	client, _ := startRedis(t, "", false)
	backend := keyedCache(t, client, keyA)
	member := tuple("space", "a", "viewer", "user", "alice", "")
	source := newDeltaSource(t, "redis-integrity", member)
	const staleness = 500 * time.Millisecond
	runtime := newRuntime(t, source, backend, staleness)
	poll(t, runtime)
	key := keyFor(t, backend, runtime.Binding())
	dump, err := client.Dump(t.Context(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	source.revoke(t, member)
	poll(t, runtime)
	assertHit(t, runtime, member, false)
	if err := client.RestoreReplace(t.Context(), key, 0, dump).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(staleness + 100*time.Millisecond)
	before := runtime.Stats()
	assertAllowed(t, runtime, member, false)
	if after := runtime.Stats(); after.Fallbacks != before.Fallbacks+1 || after.Hits != before.Hits {
		t.Fatalf("restored mirror served a read past its deadline: %+v -> %+v", before, after)
	}
	poll(t, runtime)
	if stats := runtime.Stats(); !stats.LastPoll.Full {
		t.Fatalf("restored mirror was not rebuilt: %+v", stats)
	}
	assertHit(t, runtime, member, false)
}

// §2: a revoked fact copied back with its current-epoch tag still reads (the
// named residual) until a full publication mints a new epoch.
func TestIntegrityReplayLastsUntilRebuild(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		name := "Rebuild"
		if scheduled {
			name = "FullRebuildEvery"
		}
		t.Run(name, func(t *testing.T) {
			client, _ := startRedis(t, "", false)
			backend := keyedCache(t, client, keyA)
			member := tuple("space", "a", "viewer", "user", "alice", "")
			source := newDeltaSource(t, "redis-integrity", member)
			policy := tuplecache.Policy{MaxStaleness: time.Minute}
			if scheduled {
				policy.PollInterval, policy.FullRebuildEvery = 100*time.Millisecond, 300*time.Millisecond
			}
			runtime, err := tuplecache.New(source, backend, tuplecache.WithPolicy(policy))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			poll(t, runtime)
			key := keyFor(t, backend, runtime.Binding())
			saved := client.HMGet(t.Context(), key, setField(forward(member)), setField(reverse(member))).Val()
			source.revoke(t, member)
			poll(t, runtime)
			assertHit(t, runtime, member, false)
			if err := client.HSet(t.Context(), key, setField(forward(member)), saved[0], setField(reverse(member)), saved[1]).Err(); err != nil {
				t.Fatal(err)
			}
			assertHit(t, runtime, member, true)
			if scheduled {
				time.Sleep(policy.FullRebuildEvery)
				poll(t, runtime)
				if got := runtime.Stats().ScheduledRebuilds; got != 1 {
					t.Fatalf("scheduled rebuilds %d, want 1", got)
				}
			} else if err := runtime.Rebuild(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertHit(t, runtime, member, false)
			if err := client.HSet(t.Context(), key, setField(forward(member)), saved[0], setField(reverse(member)), saved[1]).Err(); err != nil {
				t.Fatal(err)
			}
			assertDurable(t, runtime, backend, member, false)
		})
	}
}

// §2 (BE1): identity excludes the tag, so a revoke removes a stored fact
// whatever tag it carries.
func TestIntegrityRevokeIgnoresStoredTag(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := keyedCache(t, client, keyA)
	ctx := t.Context()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	stale := withTag(row, strings.Repeat("Q", factTagChars-1)+"A")
	forgeField(t, client, c, "store", setField(forward(row)), stale)
	forgeField(t, client, c, "store", setField(reverse(row)), stale)
	next := tuplecache.State{Binding: "store", Receipt: "revoked"}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{setField(forward(row)), setField(reverse(row))} {
		if client.HExists(ctx, keyFor(t, c, "store"), field).Val() {
			t.Fatalf("revoke left the differently tagged fact in %s", field)
		}
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{}, {}})
	// An add replaces a stored tag rather than duplicating the fact.
	forgeField(t, client, c, "store", setField(forward(row)), stale)
	forgeField(t, client, c, "store", setField(reverse(row)), stale)
	last := tuplecache.State{Binding: "store", Receipt: "regranted"}
	if err := c.Publish(ctx, next, last, tuplecache.Snapshot{Changes: []tuplecache.Change{{After: &row}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, last, []tuplecache.SetKey{forward(row), reverse(row)}, [][]tuplefacts.Tuple{{row}, {row}})
}

// §2: rotation. An epoch stays signed by the key that opened it; the next
// full publication moves it to the current key.
func TestIntegrityKeyRotation(t *testing.T) {
	client, _ := startRedis(t, "", false)
	member := tuple("space", "a", "viewer", "user", "alice", "")
	other := tuple("space", "a", "viewer", "user", "bob", "")
	source := newDeltaSource(t, "redis-integrity", member)
	old := newRuntime(t, source, keyedCache(t, client, keyA), time.Minute)
	poll(t, old)

	rotatedBackend := keyedCache(t, client, keyB, keyA)
	rotated := newRuntime(t, source, rotatedBackend, time.Minute)
	poll(t, rotated)
	if stats := rotated.Stats(); stats.Rebuilds != 0 {
		t.Fatalf("adding a key rebuilt the mirror: %+v", stats)
	}
	assertHit(t, rotated, member, true)
	source.mu.Lock()
	source.tuples = append(source.tuples, other)
	source.pending = append(source.pending, tuplecache.Change{ID: "grant", After: &other})
	source.mu.Unlock()
	poll(t, rotated)
	assertHit(t, rotated, other, true)
	assertHit(t, old, other, true)

	if err := rotated.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertHit(t, rotated, member, true)
	onlyNew := newRuntime(t, source, keyedCache(t, client, keyB), time.Minute)
	poll(t, onlyNew)
	if stats := onlyNew.Stats(); stats.Rebuilds != 0 {
		t.Fatalf("the re-tagged mirror needed the old key: %+v", stats)
	}
	assertHit(t, onlyNew, other, true)

	// A runtime holding only the retired key cannot verify the new epoch.
	retiredBackend := keyedCache(t, client, keyA)
	retired := newRuntime(t, source, retiredBackend, time.Minute)
	if state, err := retiredBackend.State(t.Context(), retired.Binding()); err != nil || state != (tuplecache.State{}) {
		t.Fatalf("retired key verified the new epoch: %+v/%v", state, err)
	}
	if retiredBackend.Stats().IntegrityFailures != 1 {
		t.Fatalf("integrity failures: %+v", retiredBackend.Stats())
	}
}

// §2: keyed and unkeyed runtimes never read each other's mirror.
func TestIntegrityKeyedAndUnkeyedAreSeparate(t *testing.T) {
	client, _ := startRedis(t, "", false)
	ctx := t.Context()
	member := tuple("space", "a", "viewer", "user", "alice", "")
	source := newDeltaSource(t, "redis-integrity", member)
	plainBackend := newCache(t, client)
	plain := newRuntime(t, source, plainBackend, time.Minute)
	poll(t, plain)
	keyedBackend := keyedCache(t, client, keyA)
	if state, err := keyedBackend.State(ctx, plain.Binding()); err != nil || state != (tuplecache.State{}) {
		t.Fatalf("keyed runtime saw the unkeyed mirror: %+v/%v", state, err)
	}
	keyed := newRuntime(t, source, keyedBackend, time.Minute)
	poll(t, keyed)
	if !keyed.Stats().LastPoll.Full {
		t.Fatal("turning the key on did not rebuild in full")
	}
	keys, err := client.Keys(ctx, "tuplecache:*").Result()
	slices.Sort(keys)
	want := []string{"tuplecache:v3:ik:{test:raw:sets/" + plain.Binding() + "}", "tuplecache:v3:{test:raw:sets/" + plain.Binding() + "}"}
	if err != nil || !slices.Equal(keys, want) {
		t.Fatalf("keys %v/%v, want %v", keys, err, want)
	}
	// An unkeyed mirror copied under the keyed name carries no MAC.
	if err := client.Copy(ctx, want[1], want[0], 0, true).Err(); err != nil {
		t.Fatal(err)
	}
	assertDurable(t, keyed, keyedBackend, member, true)
	if state, err := keyedBackend.State(ctx, plain.Binding()); err != nil || state != (tuplecache.State{}) {
		t.Fatalf("copied unkeyed mirror verified: %+v/%v", state, err)
	}
	if state, err := plainBackend.State(ctx, plain.Binding()); err != nil || state.Receipt == "" {
		t.Fatalf("unkeyed mirror disturbed: %+v/%v", state, err)
	}
}

// A keyed delta over a tampered header refuses to re-MAC it.
func TestIntegrityDeltaRefusesUnverifiedHeader(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := keyedCache(t, client, keyA)
	ctx := t.Context()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	state := tuplecache.State{Binding: "store", Receipt: "first"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, keyFor(t, c, "store"), "!epoch_at", time.Now().Add(time.Hour).UnixMilli()).Err(); err != nil {
		t.Fatal(err)
	}
	before := client.HGetAll(ctx, keyFor(t, c, "store")).Val()
	next := tuplecache.State{Binding: "store", Receipt: "next"}
	for _, snapshot := range []tuplecache.Snapshot{{}, {Changes: []tuplecache.Change{{Before: &row}}}} {
		expected, target := state, next
		if len(snapshot.Changes) == 0 {
			target = state
		}
		if err := c.Publish(ctx, expected, target, snapshot, time.Minute); !errors.Is(err, ErrIntegrity) || !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("delta over a tampered header: %v", err)
		}
	}
	if got := client.HGetAll(ctx, keyFor(t, c, "store")).Val(); !maps.Equal(before, got) {
		t.Fatal("refused delta changed the mirror")
	}
	// A full publication from an empty expectation replaces it.
	if err := c.Publish(ctx, tuplecache.State{}, next, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(row)}, [][]tuplefacts.Tuple{{row}})
}

// Review HIGH: the header MAC binds the mirror key, so a signed mirror copied
// from another namespace under the same key and source binding never answers.
func TestIntegrityCrossNamespaceCopyFallsBack(t *testing.T) {
	client, _ := startRedis(t, "", false)
	member := tuple("space", "a", "viewer", "user", "alice", "")
	stagingOnly := tuple("space", "a", "viewer", "user", "mallory", "")
	prod, err := NewTupleCache(client, WithNamespace("prod"), WithIntegrityKeys(keyA))
	if err != nil {
		t.Fatal(err)
	}
	staging, err := NewTupleCache(client, WithNamespace("staging"), WithIntegrityKeys(keyA))
	if err != nil {
		t.Fatal(err)
	}
	rp := newRuntime(t, newDeltaSource(t, "same-db", member), prod, time.Minute)
	rs := newRuntime(t, newDeltaSource(t, "same-db", member, stagingOnly), staging, time.Minute)
	poll(t, rp)
	poll(t, rs)
	if err := client.Copy(t.Context(), keyFor(t, staging, rs.Binding()), keyFor(t, prod, rp.Binding()), 0, true).Err(); err != nil {
		t.Fatal(err)
	}
	assertDurable(t, rp, prod, stagingOnly, false)
	poll(t, rp)
	if !rp.Stats().LastPoll.Full {
		t.Fatal("the copied mirror was not replaced")
	}
	assertHit(t, rp, stagingOnly, false)
	assertHit(t, rp, member, true)
}

// Review MEDIUM-2: a header naming another binding is an integrity failure,
// counted and replaced, never a permanent ErrBinding.
func TestIntegrityForeignBindingHeaderIsReplaced(t *testing.T) {
	client, _ := startRedis(t, "", false)
	member := tuple("space", "a", "viewer", "user", "alice", "")
	backend := keyedCache(t, client, keyA)
	ra := newRuntime(t, newDeltaSource(t, "db-a", member), backend, time.Minute)
	rb := newRuntime(t, newDeltaSource(t, "db-b", member), backend, time.Minute)
	poll(t, ra)
	poll(t, rb)
	if err := client.Copy(t.Context(), keyFor(t, backend, rb.Binding()), keyFor(t, backend, ra.Binding()), 0, true).Err(); err != nil {
		t.Fatal(err)
	}
	if state, err := backend.State(t.Context(), ra.Binding()); err != nil || state != (tuplecache.State{}) {
		t.Fatalf("foreign header state: %+v/%v", state, err)
	}
	if got := backend.Stats().IntegrityFailures; got != 1 {
		t.Fatalf("integrity failures %d, want 1", got)
	}
	poll(t, ra)
	if !ra.Stats().LastPoll.Full {
		t.Fatal("the foreign mirror was not replaced")
	}
	assertHit(t, ra, member, true)
	assertHit(t, rb, member, true)
}

// Review NIT (d): the recovery fence refuses to replace a mirror that a
// legitimate publication changed after this process observed it.
func TestIntegrityRecoveryFenceConflicts(t *testing.T) {
	client, _ := startRedis(t, "", false)
	ctx := t.Context()
	c := keyedCache(t, client, keyA)
	rivalOptions := *client.Options()
	rivalClient := redis.NewClient(&rivalOptions)
	t.Cleanup(func() { _ = rivalClient.Close() })
	rival := keyedCache(t, rivalClient, keyA)
	row := tuple("space", "a", "viewer", "user", "alice", "")
	full := tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{row}}
	if err := c.Publish(ctx, tuplecache.State{}, tuplecache.State{Binding: "store", Receipt: "first"}, full, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, keyFor(t, c, "store"), "!until", time.Now().Add(time.Hour).UnixMilli()).Err(); err != nil {
		t.Fatal(err)
	}
	won := tuplecache.State{Binding: "store", Receipt: "rival"}
	var rivalErr error
	fired := false
	client.AddHook(commandHook{observe: func(cmd redis.Cmder) {
		if fired || cmd.Name() != "evalsha" || cmd.Args()[1] != replaceScript.Hash() {
			return
		}
		fired = true
		rivalErr = rival.Publish(ctx, tuplecache.State{}, won, full, time.Minute)
	}})
	err := c.Publish(ctx, tuplecache.State{}, tuplecache.State{Binding: "store", Receipt: "late"}, full, time.Minute)
	if !fired || rivalErr != nil {
		t.Fatalf("rival publication: fired=%v %v", fired, rivalErr)
	}
	if !errors.Is(err, tuplecache.ErrConflict) {
		t.Fatalf("fenced replacement: %v", err)
	}
	if state, err := rival.State(ctx, "store"); err != nil || !sameMirror(state, won) {
		t.Fatalf("the rival's publication was overwritten: %+v/%v", state, err)
	}
}

// Review MEDIUM-1: the README's ACL recipe runs a keyed runtime on database 0
// and on a non-zero database (go-redis SELECTs on connect).
func TestIntegrityREADMEACLRecipe(t *testing.T) {
	admin, _ := startRedis(t, "", false)
	ctx := t.Context()
	recipe := []any{"ACL", "SETUSER", "authz-cache", "on", ">pw", "resetkeys", "resetchannels", "-@all",
		"~tuplecache:v3:ik:{my-app/*",
		"+hello", "+ping", "+client|setinfo", "+select", "+eval", "+evalsha", "+del",
		"+time", "+type", "+exists", "+hget", "+hmget", "+hset", "+hdel", "+hstrlen", "+rename", "+pexpire", "+persist", "+pttl"}
	if err := admin.Do(ctx, recipe...).Err(); err != nil {
		t.Fatal(err)
	}
	for _, db := range []int{0, 1} {
		t.Run(fmt.Sprint("db=", db), func(t *testing.T) {
			options := *admin.Options()
			options.Username, options.Password, options.DB = "authz-cache", "pw", db
			client := redis.NewClient(&options)
			t.Cleanup(func() { _ = client.Close() })
			backend, err := NewTupleCache(client, WithNamespace("my-app"), WithIntegrityKeys(keyA))
			if err != nil {
				t.Fatal(err)
			}
			member := tuple("space", "a", "viewer", "user", "alice", "")
			source := newDeltaSource(t, "acl", member)
			runtime := newRuntime(t, source, backend, time.Minute)
			poll(t, runtime)
			assertHit(t, runtime, member, true)
			source.revoke(t, member)
			poll(t, runtime)
			assertHit(t, runtime, member, false)
			if err := runtime.Rebuild(ctx); err != nil {
				t.Fatal(err)
			}
			if leftover := admin.Do(ctx, "ACL", "LOG").Val(); leftover != nil && len(leftover.([]any)) != 0 {
				t.Fatalf("ACL denials: %v", leftover)
			}
		})
	}
}

// A mirror written before !epoch existed (v0.5.0) keeps taking deltas and
// reports a zero FullAt until its next full publication.
func TestIntegrityUnkeyedPreEpochMirrorResumes(t *testing.T) {
	client, _ := startRedis(t, "", false)
	ctx := t.Context()
	member := tuple("space", "a", "viewer", "user", "alice", "")
	source := newDeltaSource(t, "old", member)
	backend := newCache(t, client)
	runtime := newRuntime(t, source, backend, time.Minute)
	poll(t, runtime)
	if err := client.HDel(ctx, keyFor(t, backend, runtime.Binding()), "!epoch", "!epoch_at").Err(); err != nil {
		t.Fatal(err)
	}
	if state, err := backend.State(ctx, runtime.Binding()); err != nil || state.Receipt == "" || !state.FullAt.IsZero() {
		t.Fatalf("pre-epoch state: %+v/%v", state, err)
	}
	source.revoke(t, member)
	poll(t, runtime)
	if runtime.Stats().LastPoll.Full {
		t.Fatal("a pre-epoch mirror was rebuilt instead of taking the delta")
	}
	assertHit(t, runtime, member, false)
}

// Review NIT (a): two-phase rotation never fails a verification.
func TestIntegrityTwoPhaseRotation(t *testing.T) {
	client, _ := startRedis(t, "", false)
	member := tuple("space", "a", "viewer", "user", "alice", "")
	source := newDeltaSource(t, "db-a", member)
	start := keyedCache(t, client, keyA)
	poll(t, newRuntime(t, source, start, time.Minute))
	phase1 := keyedCache(t, client, keyA, keyB)
	phase2 := keyedCache(t, client, keyB, keyA)
	p1 := newRuntime(t, source, phase1, time.Minute)
	p2 := newRuntime(t, source, phase2, time.Minute)
	poll(t, p1)
	if err := p2.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	poll(t, p1)
	poll(t, p2)
	assertHit(t, p1, member, true)
	assertHit(t, p2, member, true)
	if p1.Stats().Rebuilds != 0 {
		t.Fatalf("a phase-1 pod rebuilt the new key's epoch: %+v", p1.Stats())
	}
	phase3 := keyedCache(t, client, keyB)
	p3 := newRuntime(t, source, phase3, time.Minute)
	poll(t, p3)
	assertHit(t, p3, member, true)
	for _, b := range []*TupleCache{phase1, phase2, phase3} {
		if got := b.Stats().IntegrityFailures; got != 0 {
			t.Fatalf("two-phase rotation failed a verification: %d", got)
		}
	}
}

// The named residual: another Redis type at the mirror key fails every poll
// (a denial), while decisions stay correct on the durable path.
func TestIntegrityWrongTypeDeniesService(t *testing.T) {
	client, _ := startRedis(t, "", false)
	member := tuple("space", "a", "viewer", "user", "alice", "")
	backend := keyedCache(t, client, keyA)
	runtime := newRuntime(t, newDeltaSource(t, "db-a", member), backend, time.Minute)
	poll(t, runtime)
	key := keyFor(t, backend, runtime.Binding())
	if err := client.Del(t.Context(), key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(t.Context(), key, "x", 0).Err(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := runtime.Poll(t.Context()); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("poll over a string key: %v", err)
		}
	}
	before := runtime.Stats()
	assertAllowed(t, runtime, member, true)
	if after := runtime.Stats(); after.Fallbacks != before.Fallbacks+1 {
		t.Fatalf("decision did not fall back: %+v -> %+v", before, after)
	}
}
