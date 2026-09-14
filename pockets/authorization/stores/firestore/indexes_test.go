package firestore

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// indexManifestCount is the number of composite indexes firestore.indexes.json
// declares. It is stated here so a change to the matrix has to move a number a
// reviewer can see, and because a Firestore database allows 200 composite
// indexes without billing enabled (1,000 with it) SHARED across every store a
// host mounts — this store's share of that budget is a fact, not an accident.
const indexManifestCount = 28

// compositeBudget is the no-billing composite-index cap of one database.
const compositeBudget = 200

// indexFieldOverrideCount is the number of single-field dependencies
// firestore.indexes.json declares — see derivedFieldOverrides for why they are
// declared at all and why the set is every queried field rather than only the
// composite-free ones.
const indexFieldOverrideCount = 17

// probeValue and probeValues are the literals the live matrix leg filters with.
// They match nothing: the point of executing a matrix row live is that Firestore
// answers FAILED_PRECONDITION for a missing index BEFORE it looks at any
// document, so an empty collection proves the index just as well as a full one.
const probeValue = "index-matrix-probe"

var probeValues = []string{probeValue + "-a", probeValue + "-b", probeValue + "-c"}

// equalityPrecedence pins ONE field order per collection for the equality-class
// prefix of a composite index.
//
// Firestore lets the equality prefix stand in any order — the scan is contiguous
// either way — so the manifest has to CHOOSE one and every query that shares a
// field pair has to agree with it, or two spellings of the same index would both
// have to be deployed. The order below is the order the store's own query
// builders apply their filters in, so the manifest reads like the code;
// TestQueryMatrixEqualityOrderIsPinned proves no row contradicts it.
var equalityPrecedence = map[string][]string{
	collectionRelationships: {
		"resource_key", "subject_type", "subject_id", "resource_type",
		"relation", "subject_key", "resource_id", "created_at", "relationship_id",
	},
	collectionRoles: {
		"subject_key", "resource_key", "resource_type", "role",
		"resource_id", "grant_key", "created_at", "role_key",
	},
}

// orderSpec is one ORDER BY clause of a query shape: the field and the direction
// (firestoredb.OrderAscending / OrderDescending).
type orderSpec struct {
	field     string
	direction string
}

// queryShape is one row of this store's COMPLETE query matrix — every query the
// three ports issue, in the vocabulary Firestore's index rules are written in.
// The matrix is the manifest's specification (ruling R5): firestore.indexes.json
// is DERIVED from it by requiredIndex, and TestIndexManifestMatchesTheQueryMatrix
// asserts the derivation both ways, so §7 of SCHEMA.md is executable rather than
// prose and a new query cannot ship without its index.
//
// Document-addressed reads (Get/GetAll on a deterministic id — HasExactRole, the
// unrestricted probe, the claim and anchor reads, the receipt) are NOT rows: they
// use no index at all. SCHEMA.md §7 still lists them.
type queryShape struct {
	// name identifies the shape in a failure message and in SCHEMA.md §7.
	name string

	// collection is the collection group the query runs against.
	collection string

	// equality are the "==" filter fields, in the order the query applies them.
	equality []string

	// in are the "in" filter fields (a one-value `in` is written as "==" by
	// whereAnyOf, which needs the same index), in query order.
	in []string

	// rangeField is the inequality filter field, which Firestore requires to be
	// the FIRST order field; TestQueryMatrixRangeFieldLeadsTheOrder pins that.
	rangeField string

	// order are the ORDER BY clauses in query order, including an explicit
	// __name__ clause where the query issues one.
	order []orderSpec
}

// queryMatrix is every query shape the store issues. The list/lookup families
// are generated over their optional-filter subsets and their directions rather
// than transcribed, because an omitted subset is exactly the production
// FAILED_PRECONDITION the manifest exists to prevent: a composite index serves a
// query only when its equality prefix is the query's WHOLE equality set, so each
// optional filter combination is its own index.
func queryMatrix() []queryShape {
	both := []string{firestoredb.OrderAscending, firestoredb.OrderDescending}

	shapes := []queryShape{
		// reads.go — expandScoped's hop. One `in` over the frontier, nothing
		// else: an `in` on a single field is served by that field's automatic
		// single-field index.
		{
			name:       "expansion hop",
			collection: collectionRelationships,
			in:         []string{"subject_key"},
		},
		// reads.go — anyTupleWithSubject (the second half of an expanded check).
		{
			name:       "expanded check",
			collection: collectionRelationships,
			equality:   []string{"resource_key", "relation"},
			in:         []string{"subject_key"},
		},
		// reads.go — relationTargets, shared by GetRelationTargets and the
		// mutation path's decision view. Also the shape of the direct count
		// (CountByResourceAndRelation), the reconciliation read
		// (setRelationTargets), and DeleteRelationship's sweep: an aggregation
		// uses the index of the query underneath it.
		{
			name:       "relation targets / direct count / reconciliation read / delete by relation",
			collection: collectionRelationships,
			equality:   []string{"resource_key", "relation"},
		},
		// mutations_eval.go resourceRows, writes.go dropMatching over a whole
		// resource (DeleteResourceRelationships, DeleteByResourceAndSubject).
		{
			name:       "resource rows / delete by resource",
			collection: collectionRelationships,
			equality:   []string{"resource_key"},
		},
		// reads.go — scanCandidates, the one candidate reader CheckBatchDirect,
		// FilterRelation and RelationTargetsFor share.
		{
			name:       "candidate scan",
			collection: collectionRelationships,
			equality:   []string{"resource_type", "relation"},
			in:         []string{"resource_id"},
		},
		// lookups.go — descendantClosure's hop.
		{
			name:       "descendant hop",
			collection: collectionRelationships,
			equality:   []string{"resource_type"},
			in:         []string{"relation", "subject_key"},
		},
		// relationships.go — LookupResourceIDs' chunk streams (idStream).
		{
			name:       "lookup by relations",
			collection: collectionRelationships,
			equality:   []string{"resource_type"},
			in:         []string{"relation", "subject_key"},
			rangeField: "resource_id",
			order: []orderSpec{
				{field: "resource_id", direction: firestoredb.OrderAscending},
				{field: gcfs.DocumentID, direction: firestoredb.OrderAscending},
			},
		},
		// relationships.go — LookupResourceIDsByRelationTarget. One relation is
		// an "==" rather than an `in`, which is the same index as the row above.
		{
			name:       "lookup by relation target",
			collection: collectionRelationships,
			equality:   []string{"resource_type", "relation"},
			in:         []string{"subject_key"},
			rangeField: "resource_id",
			order: []orderSpec{
				{field: "resource_id", direction: firestoredb.OrderAscending},
				{field: gcfs.DocumentID, direction: firestoredb.OrderAscending},
			},
		},
		// roles.go — the teardown sweep (scopedRolesQuery) and ListByResource's
		// unordered base.
		{
			name:       "role teardown sweep",
			collection: collectionRoles,
			equality:   []string{"resource_key"},
		},
		// roles.go — LookupResourceIDsBySubjectAndRoles' chunk streams.
		{
			name:       "role lookup by roles",
			collection: collectionRoles,
			equality:   []string{"subject_key", "resource_type"},
			in:         []string{"role"},
			rangeField: "resource_id",
			order: []orderSpec{
				{field: "resource_id", direction: firestoredb.OrderAscending},
				{field: gcfs.DocumentID, direction: firestoredb.OrderAscending},
			},
		},
	}

	// relationships.go — ListRelationshipsBySubject. Two optional equality
	// filters and the connector List's (order field, PK) sort, which the HasPrev
	// probe re-issues with BOTH directions flipped.
	for _, filters := range subsets([]string{"resource_type", "relation"}) {
		for _, dir := range both {
			shapes = append(shapes, queryShape{
				name:       "list relationships by subject" + filterSuffix(filters) + " " + dir,
				collection: collectionRelationships,
				equality:   append([]string{"subject_type", "subject_id"}, filters...),
				order: []orderSpec{
					{field: "created_at", direction: dir},
					{field: "relationship_id", direction: dir},
				},
			})
		}
	}

	// relationships.go — ListRelationshipsByResource, same contract.
	for _, filters := range subsets([]string{"subject_type", "relation"}) {
		for _, dir := range both {
			shapes = append(shapes, queryShape{
				name:       "list relationships by resource" + filterSuffix(filters) + " " + dir,
				collection: collectionRelationships,
				equality:   append([]string{"resource_key"}, filters...),
				order: []orderSpec{
					{field: "created_at", direction: dir},
					{field: "relationship_id", direction: dir},
				},
			})
		}
	}

	// roles.go — ListBySubject and ListByResource, both directions.
	for _, dir := range both {
		shapes = append(shapes,
			queryShape{
				name:       "list roles by subject " + dir,
				collection: collectionRoles,
				equality:   []string{"subject_key"},
				order: []orderSpec{
					{field: "created_at", direction: dir},
					{field: "role_key", direction: dir},
				},
			},
			queryShape{
				name:       "list roles by resource " + dir,
				collection: collectionRoles,
				equality:   []string{"resource_key"},
				order: []orderSpec{
					{field: "created_at", direction: dir},
					{field: "role_key", direction: dir},
				},
			},
			// effective.go — one grantStream per scope (the requested scope and
			// the global one), ordered by grant_key with the cursor as an
			// inequality that follows the direction.
			queryShape{
				name:       "effective grant stream " + dir,
				collection: collectionRoles,
				equality:   []string{"resource_key"},
				rangeField: "grant_key",
				order:      []orderSpec{{field: "grant_key", direction: dir}},
			},
		)
	}

	return shapes
}

// requiredIndex derives the composite index a query shape needs, or reports that
// Firestore serves it from automatic single-field indexes.
//
// The rules, with their sources
// (https://firebase.google.com/docs/firestore/query-data/index-overview and
// https://firebase.google.com/docs/firestore/query-data/queries):
//
//   - "You can combine constraints with a logical AND by chaining multiple
//     equality operators (== or array-contains). However, you must create a
//     composite index to combine equality operators with the inequality
//     operators, <, <=, >, and !=" — so a pure-equality query, and a query whose
//     only filter is one `in` ("You can also create in and compound equality (==)
//     queries" under "Queries supported by single-field indexes"), need nothing.
//   - "If you need to run a compound query that uses a range comparison … or if
//     you need to sort by a different field, you must create a manual index" —
//     any filter plus an order on another field, and any two-field sort, needs
//     one.
//   - Field order: "The start position is prefixed with the query's equality
//     filters and ends with the range and inequality filters on the first
//     orderBy field" — equality-class fields first, then the range/order fields
//     in the query's direction.
//   - `in` counts as an equality for index selection ("an equality (== or in)",
//     "in and == clauses use the same index"). This store still declares a
//     composite whenever an `in` is combined with another filter field: the
//     server MAY serve those disjunctions by merging single-field indexes, but
//     merging is a documented optimization, not a guarantee, and an
//     under-declared manifest surfaces as a production FAILED_PRECONDITION,
//     which is the failure this manifest exists to prevent.
//   - The trailing __name__ tiebreak is implicit: "By default, the __name__
//     field is sorted in the same direction of the last sorted field in the index
//     definition … To sort results by the non-default __name__ direction, you
//     need to create that index." A matrix row that orders by __name__ in the
//     default direction therefore contributes no index field.
func requiredIndex(s queryShape) (firestoredb.CompositeIndex, bool) {
	order := s.effectiveOrder()

	fields := make([]firestoredb.IndexField, 0, len(s.equality)+len(s.in)+len(order))
	for _, f := range s.equality {
		fields = append(fields, firestoredb.IndexField{FieldPath: f, Order: firestoredb.OrderAscending})
	}
	for _, f := range s.in {
		fields = append(fields, firestoredb.IndexField{FieldPath: f, Order: firestoredb.OrderAscending})
	}
	for _, o := range order {
		fields = append(fields, firestoredb.IndexField{FieldPath: o.field, Order: o.direction})
	}

	if !s.needsComposite() || len(fields) < 2 {
		return firestoredb.CompositeIndex{}, false
	}
	return firestoredb.CompositeIndex{
		CollectionGroup: s.collection,
		QueryScope:      firestoredb.ScopeCollection,
		Fields:          fields,
	}, true
}

// effectiveOrder is the shape's order clauses minus a trailing __name__ clause
// that repeats the previous clause's direction — the tiebreak Firestore applies
// to every composite index by default.
func (s queryShape) effectiveOrder() []orderSpec {
	order := slices.Clone(s.order)
	if n := len(order); n >= 2 && order[n-1].field == gcfs.DocumentID && order[n-1].direction == order[n-2].direction {
		order = order[:n-1]
	}
	return order
}

// needsComposite reports whether the shape is outside what automatic
// single-field indexes serve.
func (s queryShape) needsComposite() bool {
	filters := len(s.equality) + len(s.in)
	order := len(s.effectiveOrder())

	switch {
	case order >= 2:
		// Two sort fields cannot come from single-field indexes.
		return true
	case order == 1 && filters > 0:
		// Filter on one field, sort by another.
		return true
	case order == 0 && filters >= 2:
		// Two or more filter fields. `in` counts as an equality for index
		// selection ("in and == clauses use the same index"), and the server
		// MAY serve a multi-equality query by MERGING automatic single-field
		// indexes — but merging is a documented optimization, not a guarantee,
		// and an under-declared manifest surfaces as a production
		// FAILED_PRECONDITION. The rule is therefore uniform: two filter fields
		// get a composite, `in` or not. It was previously conditional on an
		// `in` being present, which is what dropped (resource_key, relation).
		return true
	default:
		return false
	}
}

// subsets returns every subset of the optional filter fields, in a stable order,
// starting with the empty one.
func subsets(optional []string) [][]string {
	out := [][]string{nil}
	for _, f := range optional {
		grown := make([][]string, 0, len(out))
		for _, s := range out {
			grown = append(grown, append(append([]string{}, s...), f))
		}
		out = append(out, grown...)
	}
	slices.SortStableFunc(out, func(a, b []string) int { return len(a) - len(b) })
	return out
}

// filterSuffix names a subset for a shape's name.
func filterSuffix(filters []string) string {
	if len(filters) == 0 {
		return ""
	}
	return " +" + strings.Join(filters, "+")
}

// derivedFieldOverrides is the SINGLE-FIELD half of the manifest, and it exists
// because a composite index is not the only index this store depends on.
//
// Several query shapes derive no composite at all — the expansion hop (one `in`
// on subject_key), the resource-rows read (one equality on resource_key), the
// role teardown sweep — and every one of them is served by Firestore's
// AUTOMATIC single-field indexing. Automatic is not the same as guaranteed: a
// host (or a later manifest of this store's own) can disable a field's
// single-field indexes with a fieldOverride, and the query would then fail with
// FAILED_PRECONDITION in production while every emulator run stayed green. The
// manifest is the specification, and ProbeIndexes checks only what the manifest
// DECLARES (connector C5), so an undeclared dependency is an unchecked one.
//
// The rule is uniform rather than minimal: every field any matrix row filters or
// orders on is declared, on the collection that queries it. A field used only
// inside a composite costs nothing to declare and keeps the rule stateable in
// one sentence.
//
// Each entry asks for ONE index — ASCENDING at COLLECTION scope. Declaring an
// override REPLACES the default set (ascending + descending + array-contains),
// which is deliberate here: these collections carry no array field, and no
// query sorts a single field descending without an equality prefix (every
// descending order in the matrix belongs to a composite). SCHEMA.md §9 says so.
func derivedFieldOverrides() []firestoredb.FieldOverride {
	fields := map[string]map[string]bool{}
	note := func(collection, field string) {
		if field == "" || field == gcfs.DocumentID {
			return
		}
		if fields[collection] == nil {
			fields[collection] = map[string]bool{}
		}
		fields[collection][field] = true
	}
	for _, s := range queryMatrix() {
		for _, f := range slices.Concat(s.equality, s.in) {
			note(s.collection, f)
		}
		note(s.collection, s.rangeField)
		for _, o := range s.order {
			note(s.collection, o.field)
		}
	}

	var out []firestoredb.FieldOverride
	for collection, set := range fields {
		for field := range set {
			out = append(out, firestoredb.FieldOverride{
				CollectionGroup: collection,
				FieldPath:       field,
				Indexes: []firestoredb.FieldOverrideIndex{
					{Order: firestoredb.OrderAscending, QueryScope: firestoredb.ScopeCollection},
				},
			})
		}
	}
	return out
}

// derivedManifest is the manifest the matrix requires: every required composite
// index plus every declared single-field dependency, de-duplicated by the
// connector's identity and sorted the way Merge sorts.
func derivedManifest(t *testing.T) firestoredb.IndexManifest {
	t.Helper()

	m := firestoredb.IndexManifest{FieldOverrides: derivedFieldOverrides()}
	seen := map[string]bool{}
	for _, s := range queryMatrix() {
		idx, ok := requiredIndex(s)
		if !ok {
			continue
		}
		key := indexKey(idx)
		if seen[key] {
			continue
		}
		seen[key] = true
		m.Indexes = append(m.Indexes, idx)
	}

	sorted, err := firestoredb.IndexManifest{}.Merge(m)
	if err != nil {
		t.Fatalf("sorting the derived manifest: %v", err)
	}
	return sorted
}

// indexKey renders a composite index as one comparable string — the test's own
// spelling of the connector's private identity, used for set comparison and for
// failure messages an operator can paste into a deploy command.
func indexKey(idx firestoredb.CompositeIndex) string {
	var b strings.Builder
	b.WriteString(idx.CollectionGroup)
	b.WriteString(" (")
	b.WriteString(idx.QueryScope)
	b.WriteString(") [")
	for i, f := range idx.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %s", f.FieldPath, f.Order+f.ArrayConfig)
	}
	b.WriteString("]")
	return b.String()
}

// TestIndexManifestParses is the wiring the constructor depends on: the embedded
// fragment is a document the connector's strict parser accepts, every entry is
// collection-scoped, and the count is the stated one.
func TestIndexManifestParses(t *testing.T) {
	m, err := firestoredb.ParseIndexManifest(IndexesFS, IndexesFile)
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}
	if len(m.Indexes) != indexManifestCount {
		t.Errorf("manifest declares %d composite indexes, want %d — update indexManifestCount and SCHEMA.md §9 deliberately", len(m.Indexes), indexManifestCount)
	}
	if len(m.Indexes) > compositeBudget {
		t.Errorf("manifest declares %d composite indexes, over the %d a database allows without billing — and that budget is shared with every other store the host mounts", len(m.Indexes), compositeBudget)
	}
	if len(m.FieldOverrides) != indexFieldOverrideCount {
		t.Errorf("manifest declares %d field overrides, want %d — the single-field indexes the composite-free query shapes depend on (SCHEMA.md §9)", len(m.FieldOverrides), indexFieldOverrideCount)
	}
	for _, idx := range m.Indexes {
		if idx.QueryScope != firestoredb.ScopeCollection {
			t.Errorf("%s: query scope %s, want %s — every collection here is a top-level one", indexKey(idx), idx.QueryScope, firestoredb.ScopeCollection)
		}
		if idx.CollectionGroup != collectionRelationships && idx.CollectionGroup != collectionRoles {
			t.Errorf("%s: unexpected collection group — only the two queried collections need composite indexes", indexKey(idx))
		}
	}
}

// TestIndexManifestMatchesTheQueryMatrix is the point of the matrix: the shipped
// manifest is exactly what the store's queries require. A missing entry is the
// production FAILED_PRECONDITION the probe cannot warn about (it only proves the
// manifest was deployed); a dead entry is an index a host pays for and nothing
// uses.
func TestIndexManifestMatchesTheQueryMatrix(t *testing.T) {
	shipped, err := firestoredb.ParseIndexManifest(IndexesFS, IndexesFile)
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}
	derived := derivedManifest(t)

	have := map[string]bool{}
	for _, idx := range shipped.Indexes {
		have[indexKey(idx)] = true
	}
	want := map[string]bool{}
	for _, idx := range derived.Indexes {
		want[indexKey(idx)] = true
	}

	for _, s := range queryMatrix() {
		idx, ok := requiredIndex(s)
		if !ok {
			continue
		}
		if !have[indexKey(idx)] {
			t.Errorf("query shape %q requires %s, which the manifest does not declare", s.name, indexKey(idx))
		}
	}
	for _, idx := range shipped.Indexes {
		if !want[indexKey(idx)] {
			t.Errorf("manifest declares %s, which no query shape requires — delete it or add the query it serves to queryMatrix", indexKey(idx))
		}
	}
}

// TestIndexManifestIsSortedAsMergeSorts keeps the checked-in file identical to
// what ExportIndexes writes, so a host's first export is an addition rather than
// a reordering diff of this store's own entries.
func TestIndexManifestIsSortedAsMergeSorts(t *testing.T) {
	shipped, err := firestoredb.ParseIndexManifest(IndexesFS, IndexesFile)
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}
	sorted, err := firestoredb.IndexManifest{}.Merge(shipped)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for i := range sorted.Indexes {
		if got, want := indexKey(shipped.Indexes[i]), indexKey(sorted.Indexes[i]); got != want {
			t.Fatalf("entry %d is %s, want %s — the file is not in canonical order", i, got, want)
		}
	}

	raw, err := IndexesFS.ReadFile(IndexesFile)
	if err != nil {
		t.Fatalf("reading the embedded manifest: %v", err)
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, IndexesFile)
	if err := ExportIndexes(dst); err != nil {
		t.Fatalf("ExportIndexes: %v", err)
	}
	exported, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading the export: %v", err)
	}
	if !bytes.Equal(raw, exported) {
		t.Errorf("the checked-in manifest is not byte-identical to what ExportIndexes writes:\n--- file ---\n%s\n--- export ---\n%s", raw, exported)
	}
}

// TestExportIndexesCarriesEveryDerivedIndexIntoAHostManifest is the identity
// half of the scaffold step. manifest_test.go proves the export merges, survives
// a host's own entry, and is byte-stable; what it deliberately does not check is
// WHICH indexes arrive, because A1 had no derived set to check against. This
// does: every index the query matrix requires is present in the host's file, by
// full field identity, after the merge.
func TestExportIndexesCarriesEveryDerivedIndexIntoAHostManifest(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, IndexesFile)

	host := firestoredb.IndexManifest{Indexes: []firestoredb.CompositeIndex{{
		CollectionGroup: "host_orders",
		QueryScope:      firestoredb.ScopeCollection,
		Fields: []firestoredb.IndexField{
			{FieldPath: "tenant_id", Order: firestoredb.OrderAscending},
			{FieldPath: "placed_at", Order: firestoredb.OrderDescending},
		},
	}}}
	if err := firestoredb.ExportIndexes(host, dst); err != nil {
		t.Fatalf("writing the host's own manifest: %v", err)
	}
	if err := ExportIndexes(dst); err != nil {
		t.Fatalf("ExportIndexes: %v", err)
	}

	merged, err := firestoredb.ParseIndexManifest(os.DirFS(dir), IndexesFile)
	if err != nil {
		t.Fatalf("parsing the merged manifest: %v", err)
	}
	keys := map[string]bool{}
	for _, idx := range merged.Indexes {
		keys[indexKey(idx)] = true
	}
	for _, idx := range derivedManifest(t).Indexes {
		if !keys[indexKey(idx)] {
			t.Errorf("the query matrix requires %s, which the host's merged manifest does not carry", indexKey(idx))
		}
	}
	if !keys[indexKey(host.Indexes[0])] {
		t.Errorf("the host's own index %s did not survive the export", indexKey(host.Indexes[0]))
	}
}

// TestCompositeFreeShapesDeclareTheirSingleFieldIndexes is the A7 fold of the
// manifest's other half. A query shape that derives NO composite index is served
// by Firestore's automatic single-field indexing — an assumption nothing in this
// package checked and the probe could not check, because ProbeIndexes validates
// only what the manifest DECLARES. A host (or this store's own manifest) that
// disabled one of those fields would break the query in production with every
// emulator run still green.
//
// So: every filter and order field of every composite-free shape must appear in
// fieldOverrides for its collection, with an ASCENDING/COLLECTION entry. The
// converse is checked too — a declared override must belong to a field some
// query actually uses — so the block cannot rot into a list nobody maintains.
func TestCompositeFreeShapesDeclareTheirSingleFieldIndexes(t *testing.T) {
	shipped, err := firestoredb.ParseIndexManifest(IndexesFS, IndexesFile)
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}

	declared := map[string]firestoredb.FieldOverride{}
	for _, o := range shipped.FieldOverrides {
		declared[o.CollectionGroup+"."+o.FieldPath] = o
	}

	ascendingCollection := func(o firestoredb.FieldOverride) bool {
		for _, idx := range o.Indexes {
			if idx.Order == firestoredb.OrderAscending && idx.QueryScope == firestoredb.ScopeCollection {
				return true
			}
		}
		return false
	}

	used := map[string]bool{}
	for _, s := range queryMatrix() {
		fields := slices.Concat(s.equality, s.in)
		if s.rangeField != "" {
			fields = append(fields, s.rangeField)
		}
		for _, o := range s.order {
			fields = append(fields, o.field)
		}
		_, hasComposite := requiredIndex(s)
		for _, f := range fields {
			if f == gcfs.DocumentID {
				continue
			}
			key := s.collection + "." + f
			used[key] = true
			if hasComposite {
				continue
			}
			o, ok := declared[key]
			if !ok {
				t.Errorf("query shape %q derives no composite and depends on the automatic single-field index of %s — declare it in fieldOverrides", s.name, key)
				continue
			}
			if !ascendingCollection(o) {
				t.Errorf("%s is declared without an ASCENDING/COLLECTION index, which is what shape %q reads through", key, s.name)
			}
		}
	}

	for key := range declared {
		if !used[key] {
			t.Errorf("fieldOverrides declares %s, which no query shape filters or orders on — delete it or add the query", key)
		}
	}
}

// TestQueryMatrixEqualityOrderIsPinned proves the manifest commits to ONE
// equality prefix order: every row's equality-class fields follow
// equalityPrecedence, so two queries sharing a field pair share an index instead
// of demanding two spellings of the same one.
func TestQueryMatrixEqualityOrderIsPinned(t *testing.T) {
	for _, s := range queryMatrix() {
		precedence, ok := equalityPrecedence[s.collection]
		if !ok {
			t.Fatalf("%s: no pinned field precedence for collection %s", s.name, s.collection)
		}
		fields := slices.Concat(s.equality, s.in)
		for _, o := range s.effectiveOrder() {
			fields = append(fields, o.field)
		}
		last := -1
		for _, f := range fields {
			at := slices.Index(precedence, f)
			if at < 0 {
				t.Errorf("%s: field %q is not in the pinned precedence for %s", s.name, f, s.collection)
				continue
			}
			if at <= last {
				t.Errorf("%s: field %q breaks the pinned precedence %v", s.name, f, precedence)
			}
			last = at
		}
	}
}

// TestQueryMatrixRangeFieldLeadsTheOrder pins the vendor rule every shape with an
// inequality obeys: Firestore requires the first orderBy to be the inequality's
// field, which is also why the derived index puts it there.
func TestQueryMatrixRangeFieldLeadsTheOrder(t *testing.T) {
	for _, s := range queryMatrix() {
		if s.rangeField == "" {
			continue
		}
		order := s.effectiveOrder()
		if len(order) == 0 || order[0].field != s.rangeField {
			t.Errorf("%s: range filter on %q but the first order clause is %v", s.name, s.rangeField, order)
		}
	}
}
