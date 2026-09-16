package goredis

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestCanonicalScopesAndIndependentRelations(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	owner := tuple("space", "a", "owner", "user", "alice", "")
	member := owner
	member.Relation = "member"
	global := owner
	global.Scope = tuples.Global()
	userset := global
	userset.Subject = tuples.SubjectRef{Type: "team", ID: "engineering", Relation: "member"}
	state := tuplecache.State{Binding: "canonical", Receipt: "initial"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuples.Tuple{owner, member, global, userset, owner}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, state, []tuplecache.SetKey{forward(owner), forward(member), forward(global), reverse(owner), reverse(userset)},
		[][]tuples.Tuple{{owner}, {member}, {global, userset}, {owner, member, global}, {userset}})
	next := tuplecache.State{Binding: state.Binding, Receipt: "revoked-scoped-owner"}
	if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &owner}, {Before: &userset}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, next, []tuplecache.SetKey{forward(owner), forward(member), forward(global), reverse(owner), reverse(userset)},
		[][]tuples.Tuple{{}, {member}, {global}, {member, global}, {}})
	last := tuplecache.State{Binding: state.Binding, Receipt: "swapped-global"}
	if err := c.Publish(ctx, next, last, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &global, After: &userset}, {After: &owner}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertSets(t, c, last, []tuplecache.SetKey{forward(owner), forward(global), reverse(global), reverse(userset)},
		[][]tuples.Tuple{{owner}, {userset}, {owner, member}, {userset}})
}

func TestCanonicalProtocolDoesNotReadOrMutatePreviousMirror(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	legacy := "tuplecache:{test:raw:sets}"
	if err := client.HSet(ctx, legacy, "!schema", "1", "!binding", "store", "!receipt", "old", "sentinel", "unchanged").Err(); err != nil {
		t.Fatal(err)
	}
	before := client.HGetAll(ctx, legacy).Val()
	if state, err := c.State(ctx); err != nil || state != (tuplecache.State{}) {
		t.Fatalf("old mirror reused: %+v/%v", state, err)
	}
	state := tuplecache.State{Binding: "protocol:2/store", Receipt: "new"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := client.HGet(ctx, c.key, "!schema").Val(); got != "2" {
		t.Fatalf("protocol marker = %q", got)
	}
	if got := client.HGetAll(ctx, legacy).Val(); !reflect.DeepEqual(before, got) {
		t.Fatal("protocol 2 modified the old mirror")
	}
	if err := client.HSet(ctx, c.key, "!schema", "1").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(ctx, state, nil); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("wrong protocol accepted: %v", err)
	}
	if err := c.Publish(ctx, state, state, tuplecache.Snapshot{}, time.Minute); !errors.Is(err, tuplecache.ErrConflict) {
		t.Fatalf("wrong protocol renewed: %v", err)
	}
}

func TestCanonicalMalformedAndMisplacedFactsFailClosed(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	row := tuple("space", "a", "viewer", "user", "alice", "")
	other := row
	other.Scope = tuples.Global()
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	next := tuplecache.State{Binding: "store", Receipt: "next"}
	for _, test := range []struct {
		name string
		data any
	}{
		{"wrong scope", []wireTuple{toWire(other)}},
		{"duplicate", []wireTuple{toWire(row), toWire(row)}},
		{"legacy triple", [][]string{{"dXNlcg", "YWxpY2U", ""}}},
		{"unknown scope", [][]string{{"3", "", "", "dmlld2Vy", "dXNlcg", "YWxpY2U", ""}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := client.Del(ctx, c.key).Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuples.Tuple{row}}, time.Minute); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(test.data)
			if err != nil {
				t.Fatal(err)
			}
			if err := client.HSet(ctx, c.key, setField(forward(row)), string(encoded)).Err(); err != nil {
				t.Fatal(err)
			}
			before := client.HGetAll(ctx, c.key).Val()
			if sets, err := c.Read(ctx, state, []tuplecache.SetKey{forward(row)}); sets != nil || !errors.Is(err, tuplecache.ErrUnavailable) {
				t.Fatalf("bad fact read: %v/%v", sets, err)
			}
			if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &row}}}, time.Minute); !errors.Is(err, tuplecache.ErrUnavailable) {
				t.Fatalf("bad fact publication: %v", err)
			}
			if got := client.HGetAll(ctx, c.key).Val(); !reflect.DeepEqual(before, got) {
				t.Fatal("malformed delta partially published")
			}
		})
	}
}

func TestCanonicalInvalidFactsAndSetKeys(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	state := tuplecache.State{Binding: "store", Receipt: "initial"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, fact := range []tuples.Tuple{
		{Scope: tuples.Scope{}, Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}},
		{Scope: tuples.Scope{Kind: tuples.GlobalScope, Type: "space", ID: "a"}, Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}},
		tuple("space", "bad\x00id", "owner", "user", "alice", ""),
		tuple("space", "a", "owner", "user", "bad\xffid", ""),
	} {
		if err := c.Publish(ctx, state, tuplecache.State{Binding: state.Binding, Receipt: "next"}, tuplecache.Snapshot{Changes: []tuplecache.Change{{After: &fact}}}, time.Minute); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid fact accepted: %+v/%v", fact, err)
		}
	}
	for _, key := range []tuplecache.SetKey{{}, {Scope: tuples.Global(), Relation: "owner", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}, {Reverse: true, Scope: tuples.Global(), Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}} {
		if _, err := c.Read(ctx, state, []tuplecache.SetKey{key}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid set accepted: %+v/%v", key, err)
		}
	}
}

func TestCanonicalGlobalAndResourcePublicationIsAtomic(t *testing.T) {
	client, _ := startRedis(t, "", false)
	c := newCache(t, client)
	ctx := t.Context()
	resource := tuple("space", "a", "viewer", "user", "alice", "")
	global := resource
	global.Scope, global.Relation = tuples.Global(), "admin"
	state := tuplecache.State{Binding: "store", Receipt: "global"}
	if err := c.Publish(ctx, tuplecache.State{}, state, tuplecache.Snapshot{Full: true, Tuples: []tuples.Tuple{global}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	failures := make(chan error, 1)
	var readers sync.WaitGroup
	readers.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			observed, err := c.State(ctx)
			if err != nil {
				failures <- err
				return
			}
			sets, err := c.Read(ctx, observed, []tuplecache.SetKey{forward(global), forward(resource), reverse(resource)})
			if errors.Is(err, tuplecache.ErrUnavailable) {
				continue
			}
			if err != nil {
				failures <- err
				return
			}
			if len(sets[0])+len(sets[1]) != 1 || len(sets[2]) != 1 {
				failures <- errors.New("global/resource publication exposed partial authority")
				return
			}
		}
	})
	for i := range 40 {
		remove, add := global, resource
		if i%2 != 0 {
			remove, add = resource, global
		}
		next := tuplecache.State{Binding: state.Binding, Receipt: state.Receipt + "x"}
		if err := c.Publish(ctx, state, next, tuplecache.Snapshot{Changes: []tuplecache.Change{{Before: &remove}, {After: &add}}}, time.Minute); err != nil {
			close(done)
			readers.Wait()
			t.Fatal(err)
		}
		state = next
	}
	close(done)
	readers.Wait()
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
}
