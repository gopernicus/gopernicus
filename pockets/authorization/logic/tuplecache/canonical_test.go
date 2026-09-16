package tuplecache_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func TestCanonicalRawReadsAndLookup(t *testing.T) {
	owner := tupleFact("space", "s", "owner", "user", "alice", "")
	member := owner
	member.Relation = "member"
	global := owner
	global.Scope = tuples.Global()
	globalUserset := global
	globalUserset.Relation = "admin"
	globalUserset.Subject = tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}
	rows := []tuples.Tuple{owner, member, global, globalUserset}
	c, s := fixture(t, rows, memory.NewTupleCache())
	poll(t, c)
	var retained tuples.Reader
	if err := c.Run(t.Context(), func(ctx context.Context, reader tuples.Reader) error {
		retained = reader
		absent := owner
		absent.Relation = "viewer"
		got, err := reader.ContainsMany(ctx, []tuples.Tuple{global, absent, member, global, globalUserset})
		if err != nil || !slices.Equal(got, []bool{true, false, true, true, true}) {
			t.Fatalf("exact probes: %v/%v", got, err)
		}
		admin := globalUserset
		admin.Subject = owner.Subject
		if expanded, err := reader.Contains(ctx, admin); err != nil || expanded {
			t.Fatalf("exact membership expanded a userset: %v/%v", expanded, err)
		}
		keys := []tuples.SetKey{{Scope: owner.Scope, Relation: owner.Relation}, {Scope: global.Scope, Relation: global.Relation}}
		sets, err := reader.ReadSets(ctx, keys, 2)
		if err != nil || !reflect.DeepEqual(sets, [][]tuples.Tuple{{owner}, {global}}) {
			t.Fatalf("explicit scopes: %v/%v", sets, err)
		}
		sets[0][0].Relation = "caller mutation"
		again, err := reader.ReadSets(ctx, keys, 2)
		if err != nil || again[0][0] != owner {
			t.Fatalf("caller changed memo: %v/%v", again, err)
		}
		if got, err := reader.ReadSets(ctx, append(keys, keys[0]), 2); got != nil || !errors.Is(err, tuples.ErrReadLimit) {
			t.Fatalf("repeated sets bypassed aggregate bound: %v/%v", got, err)
		}
		want := []tuples.Tuple{owner, member, global}
		slices.SortFunc(want, tuples.Compare)
		var all []tuples.Tuple
		q := tuples.Query{Subject: &owner.Subject, Limit: 1}
		for {
			page, err := reader.Lookup(ctx, q)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			all = append(all, page...)
			q.After = &page[len(page)-1]
		}
		if !slices.Equal(all, want) {
			t.Fatalf("canonical cursor ordering: %+v, want %+v", all, want)
		}
		q = tuples.Query{Scope: &global.Scope, Relation: "owner"}
		page, err := reader.Lookup(ctx, q)
		if err != nil || !slices.Equal(page, []tuples.Tuple{global}) {
			t.Fatalf("global forward lookup: %+v/%v", page, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.durable != 0 || c.Stats().Hits != 1 {
		t.Fatalf("raw reads used durable source: %d/%+v", s.durable, c.Stats())
	}
	if _, err := retained.Contains(t.Context(), global); !errors.Is(err, tuples.ErrSnapshotClosed) {
		t.Fatalf("escaped reader remained usable: %v", err)
	}
}

func TestCanonicalUnindexedLookupFallsBackWholeOperation(t *testing.T) {
	c, s := fixture(t, []tuples.Tuple{grant}, memory.NewTupleCache())
	poll(t, c)
	attempts := 0
	var rows []tuples.Tuple
	if err := c.Run(t.Context(), func(ctx context.Context, reader tuples.Reader) error {
		attempts++
		var err error
		rows, err = reader.Lookup(ctx, tuples.Query{ResourceType: "space", Limit: 10})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || s.durable != 1 || !slices.Equal(rows, []tuples.Tuple{grant}) {
		t.Fatalf("unindexed query: attempts=%d durable=%d rows=%v", attempts, s.durable, rows)
	}
}

func TestCanonicalGlobalAndGraphPublicationCannotMixAuthority(t *testing.T) {
	global := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: grant.Subject}
	backend := &hookedBackend{Backend: memory.NewTupleCache()}
	c, s := fixture(t, []tuples.Tuple{global}, backend)
	poll(t, c)
	backend.afterRead = func() {
		if err := s.store.Tuples().ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{global}, Add: []tuples.Tuple{grant}}); err != nil {
			t.Fatal(err)
		}
		s.tuples = []tuples.Tuple{grant}
		s.pending = []tuplecache.Change{{ID: "global-revoke", Before: &global}, {ID: "graph-grant", After: &grant}}
		poll(t, c)
	}
	attempts := 0
	var allowed bool
	if err := c.Run(t.Context(), func(ctx context.Context, reader tuples.Reader) error {
		attempts++
		role, err := reader.Contains(ctx, global)
		if err != nil {
			return err
		}
		member, err := graph(reader, directModel).CheckRelationWithGroupExpansion(ctx, "space", "s", "viewer", "user", "alice", 100)
		allowed = role && member
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if allowed || attempts != 2 || s.durable != 1 {
		t.Fatalf("mixed global/graph grant: allowed=%v attempts=%d durable=%d", allowed, attempts, s.durable)
	}
}

func TestCanonicalGlobalFactsAreNotGraphIntermediaries(t *testing.T) {
	global := tuples.Tuple{Scope: tuples.Global(), Relation: "member", Subject: grant.Subject}
	member := tupleFact("group", "g", "member", "user", "alice", "")
	userset := tuples.Tuple{Scope: tuples.Global(), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}}
	c, s := fixture(t, []tuples.Tuple{global, member, userset}, memory.NewTupleCache())
	poll(t, c)
	model := relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "group", Relation: "member", SubjectType: "user"}, {ResourceType: "space", Relation: "viewer", SubjectType: "group", SubjectRelation: "member"}})
	if err := c.Run(t.Context(), func(ctx context.Context, reader tuples.Reader) error {
		got, err := graph(reader, model).CheckRelationWithGroupExpansion(ctx, "space", "s", "viewer", "user", "alice", 100)
		if err != nil || got {
			t.Fatalf("global fact entered resource graph: %v/%v", got, err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if s.durable != 0 {
		t.Fatal("global graph test bypassed cache")
	}
}
