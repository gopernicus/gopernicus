package pgx

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

type tupleSnapshotCounter struct {
	tuplecache.Source
	reads int
}

func (s *tupleSnapshotCounter) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	s.reads++
	return s.Source.ReadSnapshot(ctx, fn)
}

func TestPostgresTupleCacheBatchAndRevocation(t *testing.T) {
	db, cfg := cacheFixture(t, true)
	repos, err := Repositories(t.Context(), db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	source := &tupleSnapshotCounter{Source: repos.TupleSource}
	repos.TupleSource = source
	model := relationships.Schema{ResourceTypes: map[string]relationships.ResourceTypeDef{
		"group":    {Relations: map[string]relationships.RelationDef{"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}}},
		"space":    {Relations: map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "group", Relation: "member"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))}},
		"document": {Relations: map[string]relationships.RelationDef{"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Through("parent", "view"))}},
	}}
	tuples := []relationships.CreateRelationship{
		{ResourceType: "group", ResourceID: "engineering", Relation: "member", SubjectType: "user", SubjectID: "alice"},
		{ResourceType: "space", ResourceID: "main", Relation: "viewer", SubjectType: "group", SubjectID: "engineering", SubjectRelation: "member"},
	}
	requests := make([]authmodel.CheckRequest, 128)
	for i := range requests {
		id := fmt.Sprint(i)
		tuples = append(tuples, relationships.CreateRelationship{ResourceType: "document", ResourceID: id, Relation: "parent", SubjectType: "space", SubjectID: "main"})
		requests[i] = authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "alice"}, Resource: authmodel.Resource{Type: "document", ID: id}, Permission: "view"}
	}
	if err := repos.Relationships.CreateRelationships(t.Context(), tuples); err != nil {
		t.Fatal(err)
	}
	backend := memory.NewTupleCache()
	newComponents := func(backend tuplecache.Backend) authorization.Components {
		t.Helper()
		components, err := authorization.New(repos, authorization.WithRelationshipModel(model), authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: time.Minute}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = components.TupleCache.Close() })
		return components
	}
	components := newComponents(backend)
	check := func(components authorization.Components, want bool) {
		t.Helper()
		results, err := components.Decisions.CheckBatch(t.Context(), requests)
		if err != nil || len(results) != len(requests) {
			t.Fatalf("batch: %v/%v", results, err)
		}
		for _, result := range results {
			if result.Allowed != want {
				t.Fatalf("want %v: %+v", want, result)
			}
		}
	}
	check(components, true)
	if source.reads != 1 {
		t.Fatalf("cold fallback snapshots=%d", source.reads)
	}
	if err := components.TupleCache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(components, true)
	if source.reads != 1 || components.TupleCache.Stats().Hits != 1 {
		t.Fatalf("warm read fell back: %d/%+v", source.reads, components.TupleCache.Stats())
	}
	state, err := backend.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unchangedKey := tuplecache.SetKey{Ref: relationships.SubjectRef{Type: "space", ID: "main", Relation: "viewer"}}
	before, err := backend.Read(t.Context(), state, []tuplecache.SetKey{unchangedKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.Relationships.DeleteRelationshipTarget(t.Context(), "group", "engineering", "member", relationships.SubjectRef{Type: "user", ID: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := components.TupleCache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(components, false)
	if source.reads != 1 {
		t.Fatal("revocation check required source snapshot")
	}
	state, err = backend.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	after, err := backend.Read(t.Context(), state, []tuplecache.SetKey{unchangedKey})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("unrelated set changed: %v -> %v/%v", before, after, err)
	}
	pending := tupleSnapshot(t, source, state.Receipt)
	if pending.Full || len(pending.Changes) != 0 {
		t.Fatalf("processed events retained: %+v", pending)
	}
	recovered := newComponents(memory.NewTupleCache())
	if err := recovered.TupleCache.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(recovered, false)
	if source.reads != 1 || recovered.TupleCache.Stats().Rebuilds != 1 {
		t.Fatal("rebuild failed to restore facts independently of old events")
	}
}
