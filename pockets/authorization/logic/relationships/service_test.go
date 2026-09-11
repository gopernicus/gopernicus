package relationships

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// fakeStore is a minimal in-package relationship.Storer for engine unit tests:
// a slice of tuples with direct-match checks. Real group expansion and keyset
// listing are the memstore's job (task-7) and storetest's (task-8); this fake
// exercises engine logic only.
type fakeStore struct {
	model     *ReadModel
	source    *fakeStore
	tuples    []CreateRelationship
	lastBatch []CreateRelationship // captured for mint assertions

	// Per-read call counters, keyed by the exact read arguments (see
	// targetsCallKey / directCallKey). They are nil until first use and exist for
	// the batch-reader memo tests; nothing else asserts on them. mu guards ONLY
	// the counters, because one immutability test drives the fake concurrently.
	mu           sync.Mutex
	targetsCalls map[string]int
	directCalls  map[string]int
	lookupCalls  int
	// Set-read counters: ONE per FilterRelation / RelationTargetsFor call,
	// whatever the size of the candidate set — the number the set evaluation's
	// cost claim is about.
	setDirectCalls  int
	setTargetsCalls int
}

func targetsCallKey(resourceType, resourceID, relation string) string {
	return resourceType + ":" + resourceID + "#" + relation
}

func directCallKey(resourceType, resourceID, relation, subjectType, subjectID string) string {
	return resourceType + ":" + resourceID + "#" + relation + "@" + subjectType + ":" + subjectID
}

func (f *fakeStore) count(m *map[string]int, key string) {
	if f.source != nil {
		if m == &f.targetsCalls {
			f.source.count(&f.source.targetsCalls, key)
		} else {
			f.source.count(&f.source.directCalls, key)
		}
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if *m == nil {
		*m = make(map[string]int)
	}
	(*m)[key]++
}

func (f *fakeStore) countSet(n *int) {
	if f.source != nil {
		if n == &f.setTargetsCalls {
			f.source.countSet(&f.source.setTargetsCalls)
		} else {
			f.source.countSet(&f.source.setDirectCalls)
		}
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	*n++
}

func (f *fakeStore) setReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setDirectCalls + f.setTargetsCalls
}

func (f *fakeStore) countLookup() {
	if f.source != nil {
		f.source.countLookup()
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookupCalls++
}

func (f *fakeStore) calls(m map[string]int, key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return m[key]
}

func (f *fakeStore) match(resourceType, resourceID, relation, subjectType, subjectID string) bool {
	for _, t := range f.rows() {
		if t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation &&
			t.SubjectType == subjectType && t.SubjectID == subjectID {
			return true
		}
	}
	return false
}

func (f *fakeStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	f.count(&f.directCalls, directCallKey(resourceType, resourceID, relation, subjectType, subjectID))
	return f.match(resourceType, resourceID, relation, subjectType, subjectID), nil
}

func (f *fakeStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return f.match(resourceType, resourceID, relation, subjectType, subjectID), nil
}

func (f *fakeStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	f.count(&f.targetsCalls, targetsCallKey(resourceType, resourceID, relation))
	var out []RelationTarget
	for _, t := range f.rows() {
		if t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation {
			out = append(out, RelationTarget{Type: t.SubjectType, ID: t.SubjectID, Relation: t.SubjectRelation})
		}
	}
	return out, nil
}

// FilterRelation is the SET form of the direct check: ONE counted read for the
// whole candidate set, answered over the same direct-match scan.
func (f *fakeStore) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	f.countSet(&f.setDirectCalls)
	var out []string
	for _, id := range distinctSorted(resourceIDs) {
		if f.match(resourceType, id, relation, subjectType, subjectID) {
			out = append(out, id)
		}
	}
	return out, nil
}

// RelationTargetsFor is the SET form of the Through-hop read: ONE counted read
// for the whole candidate set.
func (f *fakeStore) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]RelationTarget, error) {
	out := make(map[string][]RelationTarget, len(resourceIDs))
	if len(resourceIDs) == 0 {
		return out, nil
	}
	f.countSet(&f.setTargetsCalls)
	want := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		want[id] = true
	}
	for _, t := range f.rows() {
		if t.ResourceType == resourceType && t.Relation == relation && want[t.ResourceID] {
			out[t.ResourceID] = append(out[t.ResourceID], RelationTarget{Type: t.SubjectType, ID: t.SubjectID, Relation: t.SubjectRelation})
		}
	}
	return out, nil
}

func (f *fakeStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		out[id] = f.match(resourceType, id, relation, subjectType, subjectID)
	}
	return out, nil
}

func (f *fakeStore) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	f.lastBatch = relationships
	f.tuples = append(f.tuples, relationships...)
	return nil
}

func (f *fakeStore) SetRelationTargets(ctx context.Context, resourceType, resourceID, relationName string, targets []CreateRelationship) error {
	f.tuples = filter(f.tuples, func(t CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relationName)
	})
	f.tuples = append(f.tuples, targets...)
	return nil
}

func (f *fakeStore) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relationName string, target SubjectRef) error {
	f.tuples = filter(f.tuples, func(t CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relationName &&
			t.SubjectType == target.Type && t.SubjectID == target.ID && t.SubjectRelation == target.Relation)
	})
	return nil
}

func (f *fakeStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	f.tuples = filter(f.tuples, func(t CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID)
	})
	return nil
}

func (f *fakeStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	f.tuples = filter(f.tuples, func(t CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation &&
			t.SubjectType == subjectType && t.SubjectID == subjectID)
	})
	return nil
}

func (f *fakeStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	f.tuples = filter(f.tuples, func(t CreateRelationship) bool {
		return !(t.ResourceType == resourceType && t.ResourceID == resourceID &&
			t.SubjectType == subjectType && t.SubjectID == subjectID)
	})
	return nil
}

func (f *fakeStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	n := 0
	for _, t := range f.rows() {
		if t.ResourceType == resourceType && t.ResourceID == resourceID && t.Relation == relation {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req list.Request) (list.Page[SubjectRelationship], error) {
	return list.Page[SubjectRelationship]{}, nil
}

func (f *fakeStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req list.Request) (list.Page[ResourceRelationship], error) {
	return list.Page[ResourceRelationship]{}, nil
}

func (f *fakeStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	f.countLookup()
	var out []string
	for _, t := range f.rows() {
		if t.ResourceType != resourceType || t.SubjectType != subjectType || t.SubjectID != subjectID {
			continue
		}
		for _, r := range relations {
			if t.Relation == r {
				out = append(out, t.ResourceID)
			}
		}
	}
	return keysetIDs(out, after, limit), nil
}

func (f *fakeStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	f.countLookup()
	set := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		set[id] = true
	}
	var out []string
	for _, t := range f.rows() {
		if t.ResourceType == resourceType && t.Relation == relation && t.SubjectType == targetType && set[t.SubjectID] {
			out = append(out, t.ResourceID)
		}
	}
	return keysetIDs(out, after, limit), nil
}

// LookupDescendantResourceIDs walks the UNION of relations transitively from
// rootIDs (breadth-first, cycle-safe: a root reappears only when a cycle makes it
// a genuine descendant), then applies the port's keyset contract to the closure.
func (f *fakeStore) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	f.countLookup()
	wanted := make(map[string]bool, len(relations))
	for _, r := range relations {
		wanted[r] = true
	}
	visited := make(map[string]bool, len(rootIDs))
	frontier := append([]string(nil), rootIDs...)
	var closure []string
	for len(frontier) > 0 {
		parents := make(map[string]bool, len(frontier))
		for _, id := range frontier {
			parents[id] = true
		}
		var next []string
		for _, t := range f.rows() {
			if t.ResourceType != resourceType || t.SubjectType != subjectType || !wanted[t.Relation] || !parents[t.SubjectID] {
				continue
			}
			if visited[t.ResourceID] {
				continue
			}
			visited[t.ResourceID] = true
			closure = append(closure, t.ResourceID)
			next = append(next, t.ResourceID)
		}
		frontier = next
	}
	return keysetIDs(closure, after, limit), nil
}

// keysetIDs applies the lookup port's output contract to a raw id list: sorted,
// distinct, strictly after `after`, at most limit.
func keysetIDs(ids []string, after string, limit int) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] && id > after {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func filter(in []CreateRelationship, keep func(CreateRelationship) bool) []CreateRelationship {
	out := in[:0:0]
	for _, t := range in {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}

// testSchema: org.manage=Direct(admin); post.view=AnyOf(Direct(owner),
// Through(org, manage)); post.delete=Direct(owner).
func testSchema() Schema {
	return NewSchema([]ResourceSchema{
		{Name: "org", Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("admin"))},
		}},
		{Name: "post", Def: ResourceTypeDef{
			Relations: map[string]RelationDef{
				"owner": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
				"org":   {AllowedSubjects: []SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]PermissionRule{
				"view":   AnyOf(Direct("owner"), Through("org", "manage")),
				"delete": AnyOf(Direct("owner")),
			},
		}},
	})
}

func newTestService(t *testing.T, store Storer) *Service {
	t.Helper()
	svc, err := newService(store, testSchema(), serviceConfig{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestNewServiceRejectsInvalidSchema(t *testing.T) {
	bad := NewSchema([]ResourceSchema{{
		Name: "org",
		Def: ResourceTypeDef{
			Relations:   map[string]RelationDef{"admin": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]PermissionRule{"manage": AnyOf(Direct("owner"))}, // owner undefined
		},
	}})
	_, err := newService(&fakeStore{}, bad, serviceConfig{})
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("want ErrInvalidSchema, got %v", err)
	}
}

func TestCheckDirectRelation(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	store.tuples = append(store.tuples, CreateRelationship{
		ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1",
	})

	res, err := svc.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("owner should have delete: allowed=%v reason=%q err=%v", res.Allowed, res.Reason, err)
	}

	deny, _ := svc.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u2"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	})
	if deny.Allowed {
		t.Fatalf("non-owner must be denied delete")
	}
}

// budgetExceededStore returns the store-layer group-expansion overflow sentinel
// from both check-path methods; the engine must map it to ErrEvaluationLimit.
type budgetExceededStore struct{ *fakeStore }

func (budgetExceededStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return false, ErrExpansionBudgetExceeded
}

func (budgetExceededStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	return nil, ErrExpansionBudgetExceeded
}

// TestCheckGroupExpansionOverflowMapsToEvaluationLimit proves the F4 boundary: a
// store reporting relationship.ErrExpansionBudgetExceeded surfaces from the engine
// as ErrEvaluationLimit (the indeterminate budget class, wrapping
// sdk.ErrUnavailable) — never a deny, and the engine never leaks the store
// sentinel. Both the direct and the batch-optimized paths map.
func TestCheckGroupExpansionOverflowMapsToEvaluationLimit(t *testing.T) {
	store := budgetExceededStore{&fakeStore{}}
	svc := newTestService(t, store)

	_, err := svc.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	})
	if !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("direct check overflow: want ErrEvaluationLimit, got %v", err)
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("ErrEvaluationLimit must classify as unavailable, got %v", err)
	}
	if errors.Is(err, ErrExpansionBudgetExceeded) {
		t.Fatalf("engine must not leak the store sentinel, got %v", err)
	}

	// The batch-optimized path (a Direct-only permission across shared subject +
	// resource type) maps identically.
	_, err = svc.CheckBatch(context.Background(), []authmodel.CheckRequest{
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p1"}},
		{Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "p2"}},
	})
	if !errors.Is(err, authmodel.ErrEvaluationLimit) {
		t.Fatalf("batch check overflow: want ErrEvaluationLimit, got %v", err)
	}
}

// TestCheckNoImplicitSelfAccess proves the engine grants NO self-access: a
// subject reading its own record with no tuple and no schema rule is DENIED.
// Self-access is host composition, not engine behavior.
func TestCheckNoImplicitSelfAccess(t *testing.T) {
	svc := newTestService(t, &fakeStore{})
	res, err := svc.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "read", Resource: authmodel.Resource{Type: "user", ID: "u1"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Allowed {
		t.Fatalf("engine must not grant implicit self-access: %+v", res)
	}
}

// TestCheckPlatformAdminIsNotMagic proves a platform-admin tuple holder is
// DENIED on an unrelated resource type: the tuple no longer bypasses the
// schema. Platform-admin is host composition — the host runs an `admin`
// permission Check in its own closure before delegating here.
func TestCheckPlatformAdminIsNotMagic(t *testing.T) {
	for _, subjType := range []string{"user", "service_account"} {
		store := &fakeStore{}
		svc := newTestService(t, store)
		store.tuples = append(store.tuples, CreateRelationship{
			ResourceType: "platform", ResourceID: "main", Relation: "admin", SubjectType: subjType, SubjectID: "admin1",
		})
		res, err := svc.Check(context.Background(), authmodel.CheckRequest{
			Principal: authmodel.PrincipalRef{Type: subjType, ID: "admin1"}, Permission: "delete", Resource: authmodel.Resource{Type: "post", ID: "pX"},
		})
		if err != nil {
			t.Fatalf("%s Check: %v", subjType, err)
		}
		if res.Allowed {
			t.Fatalf("%s platform admin must NOT bypass the schema: %+v", subjType, res)
		}
	}
}

func TestCheckThroughTraversal(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	// u1 is admin of org o1; post p1 belongs to org o1 → u1 can view p1.
	store.tuples = append(store.tuples,
		CreateRelationship{ResourceType: "org", ResourceID: "o1", Relation: "admin", SubjectType: "user", SubjectID: "u1"},
		CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "org", SubjectType: "org", SubjectID: "o1"},
	)
	res, err := svc.Check(context.Background(), authmodel.CheckRequest{
		Principal: authmodel.PrincipalRef{Type: "user", ID: "u1"}, Permission: "view", Resource: authmodel.Resource{Type: "post", ID: "p1"},
	})
	if err != nil || !res.Allowed {
		t.Fatalf("through traversal should allow view: %+v err=%v", res, err)
	}
}

func TestCheckBatchAndFilterAuthorized(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	store.tuples = append(store.tuples,
		CreateRelationship{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		CreateRelationship{ResourceType: "post", ResourceID: "p3", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	)
	got, err := svc.FilterAuthorized(context.Background(), authmodel.PrincipalRef{Type: "user", ID: "u1"}, "delete", "post", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("FilterAuthorized: %v", err)
	}
	if len(got) != 2 || got[0] != "p1" || got[1] != "p3" {
		t.Fatalf("want [p1 p3], got %v", got)
	}
}

func TestCreateRelationshipsPreservesExactTuples(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	input := []CreateRelationship{
		{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
		{ResourceType: "post", ResourceID: "p2", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	}
	if err := (&RelationshipWriter{store: svc.store, service: svc}).CreateRelationships(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.lastBatch, input) {
		t.Fatalf("stored tuples = %+v, want %+v", store.lastBatch, input)
	}
}

func TestCreateRelationshipsValidatesAgainstSchema(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	err := (&RelationshipWriter{store: svc.store, service: svc}).CreateRelationships(context.Background(), []CreateRelationship{
		{ResourceType: "post", ResourceID: "p1", Relation: "bogus", SubjectType: "user", SubjectID: "u1"},
	})
	if !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("want ErrInvalidRelation, got %v", err)
	}
	if len(store.tuples) != 0 {
		t.Fatalf("invalid batch must not persist")
	}
}

// CreateRelationships gives the store an owned slice of the validated tuples.
func TestCreateRelationshipsDoesNotMutateInput(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(t, store)
	in := []CreateRelationship{
		{ResourceType: "post", ResourceID: "p1", Relation: "owner", SubjectType: "user", SubjectID: "u1"},
	}
	if err := (&RelationshipWriter{store: svc.store, service: svc}).CreateRelationships(context.Background(), in); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
	store.lastBatch[0].ResourceID = "changed-by-store"
	if in[0].ResourceID != "p1" {
		t.Fatalf("store received caller-owned slice: %+v", in)
	}
}

func (f *fakeStore) ForModel(model ReadModel) Reader {
	return &fakeStore{model: &model, source: f}
}

func (f *fakeStore) rows() []CreateRelationship {
	rows := f.tuples
	if f.source != nil {
		rows = f.source.rows()
	}
	if f.model == nil {
		return rows
	}
	out := make([]CreateRelationship, 0, len(rows))
	for _, row := range rows {
		if f.model.Allows(row.ResourceType, row.Relation, row.SubjectType, row.SubjectRelation) {
			out = append(out, row)
		}
	}
	return out
}

func (s budgetExceededStore) ForModel(ReadModel) Reader { return s }
