package decisions_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

type exactRoleKey struct {
	subjectType, subjectID, roleName, resourceType, resourceID string
}

type countedRoleStore struct {
	roles.Storer
	mu    sync.Mutex
	reads []exactRoleKey
	hook  func(exactRoleKey) error
}

func (s *countedRoleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	key := exactRoleKey{subjectType, subjectID, roleName, resourceType, resourceID}
	s.mu.Lock()
	s.reads = append(s.reads, key)
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(key); err != nil {
			return false, err
		}
	}
	return s.Storer.HasExactRole(ctx, subjectType, subjectID, roleName, resourceType, resourceID)
}

func (s *countedRoleStore) calls() []exactRoleKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]exactRoleKey(nil), s.reads...)
}

func (s *countedRoleStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = nil
}

func roleBatchFixture(t testing.TB, direct int, global bool, duplicate bool) (authorization.Components, *countedRoleStore, []authmodel.CheckRequest) {
	t.Helper()
	ctx := context.Background()
	store := &countedRoleStore{Storer: memory.New().Roles()}
	for i := 0; i < direct; i++ {
		if err := store.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer", ResourceType: "doc", ResourceID: fmt.Sprintf("d%02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if global {
		if err := store.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer"}); err != nil {
			t.Fatal(err)
		}
	}
	components, err := authorization.New(authorization.Repositories{Roles: store}, authorization.WithRoleModel(authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"doc": {Roles: []string{"viewer"}, Permissions: map[string][]string{"view": {"viewer"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	requests := make([]authmodel.CheckRequest, 20)
	for i := range requests {
		id := fmt.Sprintf("d%02d", i)
		if duplicate {
			id = "d00"
		}
		requests[i] = authmodel.CheckRequest{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: authmodel.Resource{Type: "doc", ID: id}}
	}
	return components, store, requests
}

func BenchmarkRoleBatchReads(b *testing.B) {
	for _, tc := range []struct {
		name      string
		direct    int
		global    bool
		duplicate bool
	}{
		{name: "denied"},
		{name: "sparse", direct: 5},
		{name: "direct", direct: 20},
		{name: "global", global: true},
		{name: "duplicate_denied", duplicate: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			service, store, requests := roleBatchFixture(b, tc.direct, tc.global, tc.duplicate)
			b.ResetTimer()
			for range b.N {
				results, err := service.Decisions.CheckBatch(context.Background(), requests)
				if err != nil || len(results) != len(requests) {
					b.Fatalf("CheckBatch: %v (%d results)", err, len(results))
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(store.calls()))/float64(b.N), "exact_reads/op")
		})
	}
}

func TestRoleBatchReadCounts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		direct    int
		global    bool
		duplicate bool
		reads     int
	}{
		{name: "denied", reads: 21},
		{name: "sparse", direct: 5, reads: 21},
		{name: "direct", direct: 20, reads: 20},
		{name: "global", global: true, reads: 21},
		{name: "duplicate denied", duplicate: true, reads: 2},
		{name: "duplicate global", global: true, duplicate: true, reads: 2},
		{name: "duplicate direct", direct: 20, duplicate: true, reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, store, requests := roleBatchFixture(t, tc.direct, tc.global, tc.duplicate)
			results, err := service.Decisions.CheckBatch(context.Background(), requests)
			if err != nil || len(results) != len(requests) {
				t.Fatalf("CheckBatch = %v, %v", results, err)
			}
			var wantIDs []string
			ids := make([]string, len(requests))
			for i, result := range results {
				want := tc.global || i < tc.direct
				if result.Allowed != want {
					t.Fatalf("decision %d = %+v; want allowed=%v", i, result, want)
				}
				ids[i] = requests[i].Resource.ID
				if want {
					wantIDs = append(wantIDs, ids[i])
				}
			}
			if got := len(store.calls()); got != tc.reads {
				t.Fatalf("CheckBatch exact reads = %d; want %d", got, tc.reads)
			}
			store.reset()
			filtered, err := service.Decisions.FilterAuthorized(context.Background(), requests[0].Principal, "view", "doc", ids)
			if err != nil || strings.Join(filtered, ",") != strings.Join(wantIDs, ",") {
				t.Fatalf("FilterAuthorized = %v, %v; want %v", filtered, err, wantIDs)
			}
			if got := len(store.calls()); got != tc.reads {
				t.Fatalf("FilterAuthorized exact reads = %d; want %d", got, tc.reads)
			}
		})
	}
}

func TestRoleBatchSeesMutationOnNextCall(t *testing.T) {
	service, store, requests := roleBatchFixture(t, 0, false, false)
	ctx := context.Background()
	for _, granted := range []bool{false, true, false} {
		if granted {
			if err := store.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "viewer"}); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := store.Unassign(ctx, "user", "u1", "viewer", "", ""); err != nil {
				t.Fatal(err)
			}
		}
		store.reset()
		results, err := service.Decisions.CheckBatch(ctx, requests)
		if err != nil {
			t.Fatal(err)
		}
		for i, result := range results {
			if result.Allowed != granted {
				t.Fatalf("batch retained prior grant at %d: got %v want %v", i, result.Allowed, granted)
			}
		}
		if got := len(store.calls()); got != 21 {
			t.Fatalf("new call did not read current store facts: %d reads", got)
		}
	}
}

func TestRoleBatchCachedGlobalDoesNotHideExactFailure(t *testing.T) {
	service, store, requests := roleBatchFixture(t, 0, true, false)
	boom := errors.New("exact scope unavailable")
	store.hook = func(key exactRoleKey) error {
		if key.resourceID == "d01" {
			return boom
		}
		return nil
	}
	results, err := service.Decisions.CheckBatch(context.Background(), requests[:2])
	if !errors.Is(err, boom) || results != nil {
		t.Fatalf("cached global masked exact error: results=%v err=%v", results, err)
	}
	want := []exactRoleKey{{"user", "u1", "viewer", "doc", "d00"}, {"user", "u1", "viewer", "", ""}, {"user", "u1", "viewer", "doc", "d01"}}
	if got := store.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("read order = %v; want %v", got, want)
	}
}

func TestRoleBatchDoesNotShareFactsAcrossDifferentCoordinates(t *testing.T) {
	_, store, requests := roleBatchFixture(t, 1, false, false)
	components, err := authorization.New(authorization.Repositories{Roles: store}, authorization.WithRoleModel(authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"doc":   {Roles: []string{"viewer", "editor"}, Permissions: map[string][]string{"view": {"viewer"}, "edit": {"editor"}}},
		"other": {Roles: []string{"viewer"}, Permissions: map[string][]string{"view": {"viewer"}}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	base := requests[0] // user:u1 has viewer only on doc:d00.
	requests = []authmodel.CheckRequest{base, base, base, base, base}
	requests[1].Principal.ID = "u2"
	requests[2].Principal.Type = "service_account"
	requests[3].Permission = "edit"
	requests[4].Resource.Type = "other"
	results, err := components.Decisions.CheckBatch(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range results {
		if result.Allowed != (i == 0) {
			t.Fatalf("cached fact leaked across request %d coordinates: %+v", i, result)
		}
	}
}

func TestRoleBatchPreservesExactProvenanceAndDuplicates(t *testing.T) {
	service, store, requests := roleBatchFixture(t, 1, true, false)
	requests = []authmodel.CheckRequest{requests[1], requests[0], requests[1]}
	results, err := service.Decisions.CheckBatch(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	for i, provenance := range []string{roles.ProvenanceGlobal, roles.ProvenanceDirect, roles.ProvenanceGlobal} {
		if !results[i].Allowed || !strings.HasSuffix(results[i].Reason, "@"+provenance) {
			t.Fatalf("decision %d = %+v; want %s", i, results[i], provenance)
		}
	}
	if got := len(store.calls()); got != 3 {
		t.Fatalf("exact reads = %d; want 3", got)
	}
	store.reset()
	result, explanation, err := service.Decisions.CheckExplain(context.Background(), requests[1])
	if err != nil || !result.Allowed || len(explanation.Steps) != 1 || explanation.Steps[0].Scope != roles.ProvenanceDirect {
		t.Fatalf("exact-over-global explanation = %+v %+v %v", result, explanation, err)
	}
	if len(store.calls()) != 1 {
		t.Fatal("exact hit must not read a global fallback")
	}
}

func TestRoleBatchCallsCanRunConcurrently(t *testing.T) {
	service, _, requests := roleBatchFixture(t, 0, true, false)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 10 {
				results, err := service.Decisions.CheckBatch(context.Background(), requests)
				if err != nil || len(results) != len(requests) {
					t.Errorf("CheckBatch = %v %v", results, err)
					return
				}
				for _, result := range results {
					if !result.Allowed {
						t.Error("global assignment denied")
					}
				}
			}
		})
	}
	workers.Wait()
}
