package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/tuplekey"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func runTupleContracts(t *testing.T, factory func(*testing.T) Repositories) {
	t.Run("RoleWrittenFactsParticipateInGraphAndLookup", func(t *testing.T) {
		repos := factory(t)
		policy := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
			"group":  {Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}}},
			"folder": {Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "group", Relation: "member"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Direct("viewer")}},
			"doc":    {Relations: map[string]decisions.RelationDef{"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "folder"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Through("parent", "view")}},
		}}
		components, err := authorization.New(repos.Repositories, authorization.WithModel(policy))
		if err != nil {
			t.Fatal(err)
		}
		for _, assignment := range []roles.Assignment{
			{SubjectType: "user", SubjectID: "u", Role: "member", Scope: tuples.On("group", "g")},
			{SubjectType: "folder", SubjectID: "f", Role: "parent", Scope: tuples.On("doc", "d")},
		} {
			if err := components.RoleWriter.AssignRole(t.Context(), assignment); err != nil {
				t.Fatal(err)
			}
		}
		if err := repos.Relationships.CreateRelationships(t.Context(), []relationships.CreateRelationship{{ResourceType: "folder", ResourceID: "f", Relation: "viewer", SubjectType: "group", SubjectID: "g", SubjectRelation: "member"}}); err != nil {
			t.Fatal(err)
		}
		principal := authmodel.PrincipalRef{Type: "user", ID: "u"}
		result, err := components.Decisions.Check(t.Context(), authmodel.CheckRequest{Principal: principal, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: "d"}})
		if err != nil || !result.Allowed {
			t.Fatalf("role-written membership/Through target not expanded: %+v/%v", result, err)
		}
		found, err := components.Decisions.LookupAllResourceIDs(t.Context(), principal, "view", "doc")
		if err != nil || found.Unrestricted || !slices.Equal(found.IDs, []string{"d"}) {
			t.Fatalf("role-written graph lookup: %+v/%v", found, err)
		}
	})
	t.Run("BulkExactSetsAndCanonicalCursor", func(t *testing.T) {
		s := factory(t).Tuples
		facts := []tuples.Tuple{roleFact("user", "u", "owner", "", "")}
		for i := range 130 {
			facts = append(facts, roleFact("user", "u", "owner", "doc", fmt.Sprintf("d%03d", i)))
		}
		userset := roleFact("group", "g", "owner", "", "")
		userset.Subject.Relation = "member"
		facts = append(facts, userset)
		if err := s.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
			t.Fatal(err)
		}
		if err := s.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
			t.Fatal(err)
		}
		queries := slices.Clone(facts)
		slices.Reverse(queries)
		queries = append(queries, roleFact("user", "absent", "owner", "", ""), facts[0])
		got, err := s.ContainsMany(t.Context(), queries)
		if err != nil || len(got) != len(queries) {
			t.Fatalf("bulk exact: %v/%v", got, err)
		}
		for i, held := range got {
			if held != (i != len(queries)-2) {
				t.Fatalf("bulk result order %d = %v", i, held)
			}
		}
		key := tuples.SetKey{Scope: tuples.Global(), Relation: "owner"}
		sets, err := s.ReadSets(t.Context(), []tuples.SetKey{key, key}, 4)
		if err != nil || len(sets) != 2 || len(sets[0]) != 2 || !slices.Equal(sets[0], sets[1]) {
			t.Fatalf("global full-fact sets: %v/%v", sets, err)
		}
		if got, err := s.ReadSets(t.Context(), []tuples.SetKey{key, key}, 3); got != nil || !errors.Is(err, tuples.ErrReadLimit) {
			t.Fatalf("aggregate bound: %v/%v", got, err)
		}
		var walked []tuples.Tuple
		q := tuples.Query{Subject: &facts[0].Subject, Limit: 11}
		for pages := 0; ; pages++ {
			if pages > 20 {
				t.Fatal("tuple cursor did not terminate")
			}
			page, err := s.Lookup(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			walked = append(walked, page...)
			q.After = &page[len(page)-1]
		}
		if !slices.Equal(walked, facts[:len(facts)-1]) {
			t.Fatalf("full identity ordering or cursor coverage: %v", walked)
		}
		q = tuples.Query{Scope: &facts[0].Scope, ConcreteOnly: true}
		if got, err := s.Lookup(t.Context(), q); err != nil || !slices.Equal(got, facts[:1]) {
			t.Fatalf("global concrete filter: %v/%v", got, err)
		}
	})
	t.Run("CanonicalQueryFiltersAndListingCursors", func(t *testing.T) {
		store := factory(t).Tuples
		facts := []tuples.Tuple{
			roleFact("user", "u", "viewer", "", ""),
			roleFact("group", "u", "viewer", "", ""),
			roleFact("user", "u", "viewer", "doc", "a"),
			roleFact("group", "u", "viewer", "doc", "a"),
			roleFact("group", "g", "viewer", "doc", "b"),
			roleFact("group", "g", "viewer", "doc", "b"),
			roleFact("service", "u", "owner", "folder", "x"),
			roleFact("user", "u", "viewer", "doc", "é"),
		}
		for _, i := range []int{1, 3, 5} {
			facts[i].Subject.Relation = "member"
		}
		if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name    string
			query   tuples.Query
			indexes []int
		}{
			{"resource only", tuples.Query{ResourceOnly: true}, []int{2, 3, 4, 5, 6, 7}},
			{"subject type", tuples.Query{SubjectType: "group"}, []int{1, 3, 4, 5}},
			{"subject id", tuples.Query{SubjectID: "u"}, []int{0, 1, 2, 3, 6, 7}},
			{"subject pair includes usersets", tuples.Query{SubjectType: "group", SubjectID: "g"}, []int{4, 5}},
			{"exact subject", tuples.Query{Subject: &facts[4].Subject}, []int{4}},
			{"combined filters", tuples.Query{ResourceOnly: true, ResourceType: "doc", SubjectID: "u", ConcreteOnly: true}, []int{2, 7}},
		} {
			t.Run(c.name, func(t *testing.T) {
				want := make([]tuples.Tuple, len(c.indexes))
				for i, index := range c.indexes {
					want[i] = facts[index]
				}
				slices.SortFunc(want, tuples.Compare)
				got, err := store.Lookup(t.Context(), c.query)
				if err != nil || !slices.Equal(got, want) {
					t.Fatalf("lookup %+v: %+v/%v, want %+v", c.query, got, err, want)
				}
				page, err := store.ListTuples(t.Context(), c.query, list.Request{Limit: 20, WithCount: true})
				if err != nil || !slices.Equal(page.Items, want) || page.Total == nil || *page.Total != int64(len(want)) {
					t.Fatalf("listing: %+v/%v, want %+v", page, err, want)
				}
			})
		}
		for _, query := range []tuples.Query{
			{ResourceOnly: true, Scope: &facts[0].Scope},
			{Subject: &facts[0].Subject, SubjectType: "group"},
			{Subject: &facts[0].Subject, SubjectID: "different"},
			{SubjectType: "bad\x01type"}, {SubjectID: "bad\x01id"}, {After: &tuples.Tuple{}},
		} {
			if got, err := store.Lookup(t.Context(), query); !errors.Is(err, sdk.ErrInvalidInput) || len(got) != 0 {
				t.Fatalf("invalid query accepted: %+v/%v", got, err)
			}
		}
		slices.SortFunc(facts, tuples.Compare)
		key := tuplekey.Encode(facts[0])
		for _, cursor := range []struct {
			field string
			value any
			pk    string
		}{
			{"role_key", key, key}, {"tuple_key", key, tuplekey.Encode(facts[1])},
			{"tuple_key", 17, key}, {"tuple_key", "1\x01obsolete", "1\x01obsolete"},
		} {
			token, err := list.EncodeCursor(cursor.field, cursor.value, cursor.pk)
			if err != nil {
				t.Fatal(err)
			}
			if page, err := store.ListTuples(t.Context(), tuples.Query{}, list.Request{Cursor: token, Limit: 2}); !errors.Is(err, sdk.ErrInvalidInput) || len(page.Items) != 0 {
				t.Fatalf("invalid tuple cursor accepted: %+v/%v", page, err)
			}
		}
		for _, direction := range []string{list.ASC, list.DESC} {
			t.Run(direction, func(t *testing.T) {
				want := slices.Clone(facts)
				if direction == list.DESC {
					slices.Reverse(want)
				}
				request := list.Request{Order: list.NewOrder("tuple_key", direction), Limit: 2, WithCount: true}
				var walked, previous []tuples.Tuple
				for pageIndex := 0; ; pageIndex++ {
					if pageIndex > len(facts) {
						t.Fatal("tuple listing did not terminate")
					}
					page, err := store.ListTuples(t.Context(), tuples.Query{}, request)
					if err != nil {
						t.Fatal(err)
					}
					if page.Total == nil || *page.Total != int64(len(facts)) {
						t.Fatalf("total = %v", page.Total)
					}
					if pageIndex > 0 {
						if !page.HasPrev {
							t.Fatal("missing previous page")
						}
						backRequest := request
						backRequest.Cursor = page.PreviousCursor
						back, err := store.ListTuples(t.Context(), tuples.Query{}, backRequest)
						if err != nil || !slices.Equal(back.Items, previous) {
							t.Fatalf("previous page: %+v/%v, want %+v", back, err, previous)
						}
					}
					walked = append(walked, page.Items...)
					previous = page.Items
					if !page.HasMore {
						break
					}
					request.Cursor = page.NextCursor
				}
				if !slices.Equal(walked, want) {
					t.Fatalf("listing traversal: %+v, want %+v", walked, want)
				}
			})
		}
	})
	t.Run("AtomicValidationAndScopeDeletion", func(t *testing.T) {
		s := factory(t).Tuples
		fact := roleFact("user", "u", "owner", "doc", "d")
		invalid := fact
		invalid.Scope = tuples.Scope{}
		if err := s.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{fact, invalid}}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid suffix accepted: %v", err)
		}
		if held, err := s.Contains(t.Context(), fact); err != nil || held {
			t.Fatalf("partial publication: %v/%v", held, err)
		}
		global := fact
		global.Scope = tuples.Global()
		if err := s.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{fact, global}}); err != nil {
			t.Fatal(err)
		}
		if err := s.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{fact}, Remove: []tuples.Tuple{fact}}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("overlapping batch accepted: %v", err)
		}
		if err := s.DeleteScope(t.Context(), fact.Scope); err != nil {
			t.Fatal(err)
		}
		got, err := s.ContainsMany(t.Context(), []tuples.Tuple{fact, global})
		if err != nil || !slices.Equal(got, []bool{false, true}) {
			t.Fatalf("scope delete crossed global boundary: %v/%v", got, err)
		}
	})
	for _, exit := range []string{"success", "error", "cancel", "panic"} {
		t.Run("SnapshotLifetime/"+exit, func(t *testing.T) {
			s := factory(t).Tuples
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("snapshot callback failure")
			var reader tuples.Reader
			var got error
			panicked := false
			func() {
				defer func() {
					if p := recover(); p != nil {
						if p != failure {
							panic(p)
						}
						panicked = true
					}
				}()
				got = s.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
					reader = r
					switch exit {
					case "error":
						return failure
					case "cancel":
						cancel()
					case "panic":
						panic(failure)
					}
					return nil
				})
			}()
			want := map[string]error{"error": failure, "cancel": context.Canceled}[exit]
			if !errors.Is(got, want) || panicked != (exit == "panic") {
				t.Fatalf("snapshot exit %s: %v panic=%v", exit, got, panicked)
			}
			if _, err := reader.Contains(t.Context(), roleFact("user", "u", "owner", "", "")); !errors.Is(err, tuples.ErrSnapshotClosed) {
				t.Fatalf("escaped snapshot reader: %v", err)
			}
			if _, err := reader.ReadSets(t.Context(), nil, 0); !errors.Is(err, tuples.ErrSnapshotClosed) {
				t.Fatalf("escaped empty read: %v", err)
			}
		})
	}
	t.Run("CompoundChecksRequireSnapshot", func(t *testing.T) {
		r := &countedTupleReader{Reader: factory(t).Tuples}
		s, err := decisions.NewService(r)
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.Evaluate(t.Context(), authmodel.PrincipalRef{Type: "user", ID: "u"}, decisions.All(decisions.Role("admin"), decisions.Role("editor")))
		if !errors.Is(err, sdk.ErrInvalidInput) || result.Allowed || r.calls != 0 {
			t.Fatalf("missing snapshot degraded to independent reads: %+v/%v reads=%d", result, err, r.calls)
		}
	})
	t.Run("ModelFreeAllUsesOneSnapshot", func(t *testing.T) {
		store := factory(t).Tuples
		global := roleFact("user", "u", "admin", "", "")
		scoped := roleFact("user", "u", "member", "doc", "d")
		if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{global}}); err != nil {
			t.Fatal(err)
		}
		changed := false
		interleaved := interleavedTupleStore{Storer: store, after: func() error {
			if changed {
				return nil
			}
			changed = true
			return store.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{global}, Add: []tuples.Tuple{scoped}})
		}}
		service, err := decisions.NewService(interleaved)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			result, err := service.Evaluate(t.Context(), authmodel.PrincipalRef{Type: "user", ID: "u"}, decisions.All(decisions.Role("admin"), decisions.RoleIn("member", authmodel.Resource{Type: "doc", ID: "d"})))
			if err != nil || result.Allowed || !changed {
				t.Fatalf("mixed authority between exact leaves: %+v/%v changed=%v", result, err, changed)
			}
		}
	})
	t.Run("ModelFreeAnyUsesOneSnapshot", func(t *testing.T) {
		store := factory(t).Tuples
		global := roleFact("user", "u", "admin", "", "")
		scoped := roleFact("user", "u", "member", "doc", "d")
		if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{scoped}}); err != nil {
			t.Fatal(err)
		}
		changed := false
		interleaved := interleavedTupleStore{Storer: store, after: func() error {
			if changed {
				return nil
			}
			changed = true
			return store.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{scoped}, Add: []tuples.Tuple{global}})
		}}
		service, err := decisions.NewService(interleaved)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			result, err := service.Evaluate(t.Context(), authmodel.PrincipalRef{Type: "user", ID: "u"}, decisions.Any(decisions.Role("admin"), decisions.RoleIn("member", authmodel.Resource{Type: "doc", ID: "d"})))
			if err != nil || !result.Allowed || !changed {
				t.Fatalf("mixed authority lost continuous Any grant: %+v/%v changed=%v", result, err, changed)
			}
		}
	})
	t.Run("SnapshotCompletionErrorDiscardsAllow", func(t *testing.T) {
		store := factory(t).Tuples
		if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{roleFact("user", "u", "admin", "", "")}}); err != nil {
			t.Fatal(err)
		}
		failure := errors.New("snapshot completion failed")
		service, err := decisions.NewService(completionErrorStore{Storer: store, failure: failure})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Evaluate(t.Context(), authmodel.PrincipalRef{Type: "user", ID: "u"}, decisions.Role("admin"))
		if !errors.Is(err, failure) || result != (authmodel.CheckResult{}) {
			t.Fatalf("provisional allow escaped failed completion: %+v/%v", result, err)
		}
	})
}

type completionErrorStore struct {
	tuples.Storer
	failure error
}

func (s completionErrorStore) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	if err := s.Storer.ReadTupleSnapshot(ctx, fn); err != nil {
		return err
	}
	return s.failure
}

// Deliberately exposes only Reader, so Snapshotter cannot be discovered.
type countedTupleReader struct {
	tuples.Reader
	calls int
}

func (r *countedTupleReader) Contains(ctx context.Context, fact tuples.Tuple) (bool, error) {
	r.calls++
	return r.Reader.Contains(ctx, fact)
}
