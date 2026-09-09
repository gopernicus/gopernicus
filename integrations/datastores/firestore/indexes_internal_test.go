package firestore

import (
	"strings"
	"testing"
)

// asc / desc build a directional composite index field.
func asc(path string) IndexField  { return IndexField{FieldPath: path, Order: OrderAscending} }
func desc(path string) IndexField { return IndexField{FieldPath: path, Order: OrderDescending} }

// composite is the manifest entry every case below asks for: an ordinary
// two-field collection-scoped index.
func composite(fields ...IndexField) CompositeIndex {
	return CompositeIndex{CollectionGroup: "iam_relationships", QueryScope: ScopeCollection, Fields: fields}
}

// ready builds a live index in the state that serves queries.
func ready(c CompositeIndex, fields ...IndexField) liveIndex {
	return liveIndex{CollectionGroup: c.CollectionGroup, QueryScope: c.QueryScope, Fields: fields, State: stateReady}
}

// TestMissingComposites is the probe's comparison in full, as a pure function —
// which is the only way to test it at all this session, since the Admin API has
// no emulator and no live project was available.
//
// Each row states what the manifest wants and what the database has; want is
// the number of gaps and, when there is one, the state it must report.
func TestMissingComposites(t *testing.T) {
	want := composite(asc("resource_key"), asc("relationship_id"))

	cases := []struct {
		name      string
		live      []liveIndex
		wantGaps  int
		wantState string
	}{
		{
			name: "exact match is satisfied",
			live: []liveIndex{ready(want, asc("resource_key"), asc("relationship_id"))},
		},
		{
			name: "the server's implicit __name__ tiebreak still matches",
			live: []liveIndex{ready(want, asc("resource_key"), asc("relationship_id"), asc(documentIDField))},
		},
		{
			name:     "nothing deployed",
			live:     nil,
			wantGaps: 1,
		},
		{
			name:      "deployed but still building",
			live:      []liveIndex{{CollectionGroup: want.CollectionGroup, QueryScope: want.QueryScope, Fields: []IndexField{asc("resource_key"), asc("relationship_id")}, State: "CREATING"}},
			wantGaps:  1,
			wantState: "CREATING",
		},
		{
			name:      "deployed but needs repair",
			live:      []liveIndex{{CollectionGroup: want.CollectionGroup, QueryScope: want.QueryScope, Fields: []IndexField{asc("resource_key"), asc("relationship_id")}, State: "NEEDS_REPAIR"}},
			wantGaps:  1,
			wantState: "NEEDS_REPAIR",
		},
		{
			name: "a READY twin outweighs a CREATING one",
			live: []liveIndex{
				{CollectionGroup: want.CollectionGroup, QueryScope: want.QueryScope, Fields: []IndexField{asc("resource_key"), asc("relationship_id")}, State: "CREATING"},
				ready(want, asc("resource_key"), asc("relationship_id")),
			},
		},
		{
			name: "unrelated live indexes are ignored",
			live: []liveIndex{
				ready(composite(), asc("other"), asc("thing")),
				{CollectionGroup: "posts", QueryScope: ScopeCollection, Fields: []IndexField{asc("resource_key"), asc("relationship_id")}, State: stateReady},
				ready(want, asc("resource_key"), asc("relationship_id")),
			},
		},
		{
			name:     "field order matters",
			live:     []liveIndex{ready(want, asc("relationship_id"), asc("resource_key"))},
			wantGaps: 1,
		},
		{
			name:     "direction matters",
			live:     []liveIndex{ready(want, asc("resource_key"), desc("relationship_id"))},
			wantGaps: 1,
		},
		{
			name:     "query scope matters",
			live:     []liveIndex{{CollectionGroup: want.CollectionGroup, QueryScope: ScopeCollectionGroup, Fields: []IndexField{asc("resource_key"), asc("relationship_id")}, State: stateReady}},
			wantGaps: 1,
		},
		{
			name:     "collection group matters",
			live:     []liveIndex{{CollectionGroup: "iam_role_assignments", QueryScope: want.QueryScope, Fields: []IndexField{asc("resource_key"), asc("relationship_id")}, State: stateReady}},
			wantGaps: 1,
		},
		{
			name:     "an extra field is a different index",
			live:     []liveIndex{ready(want, asc("resource_key"), asc("relationship_id"), asc("subject_key"))},
			wantGaps: 1,
		},
		{
			name:     "an array index does not answer a directional one",
			live:     []liveIndex{ready(want, asc("resource_key"), IndexField{FieldPath: "relationship_id", ArrayConfig: ArrayContains})},
			wantGaps: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gaps := missingComposites([]CompositeIndex{want}, tc.live)
			if len(gaps) != tc.wantGaps {
				t.Fatalf("gaps = %d (%v), want %d", len(gaps), gaps, tc.wantGaps)
			}
			if tc.wantGaps == 0 {
				return
			}
			if gaps[0].State != tc.wantState {
				t.Errorf("state = %q, want %q", gaps[0].State, tc.wantState)
			}
			msg := gaps[0].message()
			for _, name := range []string{"iam_relationships", "resource_key", "relationship_id", ScopeCollection} {
				if !strings.Contains(msg, name) {
					t.Errorf("message %q does not name %q", msg, name)
				}
			}
			if tc.wantState != "" && !strings.Contains(msg, tc.wantState) {
				t.Errorf("message %q does not report the observed state %q", msg, tc.wantState)
			}
		})
	}
}

// TestMissingCompositesExplicitDocumentID covers the other half of the __name__
// rule: a manifest that STATES the tiebreak is compared including its
// direction, so a store that needs `__name__ DESCENDING` is not satisfied by
// the server's default ascending one.
func TestMissingCompositesExplicitDocumentID(t *testing.T) {
	want := composite(asc("resource_key"), desc("relationship_id"), desc(documentIDField))

	satisfied := []liveIndex{ready(want, asc("resource_key"), desc("relationship_id"), desc(documentIDField))}
	if gaps := missingComposites([]CompositeIndex{want}, satisfied); len(gaps) != 0 {
		t.Errorf("gaps = %v, want none", gaps)
	}

	wrongDirection := []liveIndex{ready(want, asc("resource_key"), desc("relationship_id"), asc(documentIDField))}
	if gaps := missingComposites([]CompositeIndex{want}, wrongDirection); len(gaps) != 1 {
		t.Errorf("gaps = %v, want one (the document-id tiebreak runs the other way)", gaps)
	}
}

// TestMissingCompositesReportsEveryGap proves the probe does not stop at the
// first missing index: an operator gets one deploy list, not a sequence of
// boot failures.
func TestMissingCompositesReportsEveryGap(t *testing.T) {
	want := []CompositeIndex{
		composite(asc("a"), asc("b")),
		composite(asc("c"), asc("d")),
		composite(asc("e"), asc("f")),
	}
	live := []liveIndex{ready(want[1], asc("c"), asc("d"))}

	gaps := missingComposites(want, live)
	if len(gaps) != 2 {
		t.Fatalf("gaps = %d, want 2", len(gaps))
	}
	if gaps[0].Index.Fields[0].FieldPath != "a" || gaps[1].Index.Fields[0].FieldPath != "e" {
		t.Errorf("gaps are not in manifest order: %+v", gaps)
	}
}

// TestMissingFieldIndexes covers the single-field half of the probe, including
// the case that makes it worth doing at all: a collection-group scoped
// single-field index is NOT part of the default configuration, so a field with
// no explicit override does not provide it.
func TestMissingFieldIndexes(t *testing.T) {
	ascCollection := FieldOverrideIndex{Order: OrderAscending, QueryScope: ScopeCollection}
	ascGroup := FieldOverrideIndex{Order: OrderAscending, QueryScope: ScopeCollectionGroup}
	contains := FieldOverrideIndex{ArrayConfig: ArrayContains, QueryScope: ScopeCollection}

	cases := []struct {
		name string
		want []FieldOverrideIndex
		have []FieldOverrideIndex
		miss int
	}{
		{name: "present", want: []FieldOverrideIndex{ascGroup}, have: []FieldOverrideIndex{ascGroup}, miss: 0},
		{name: "absent", want: []FieldOverrideIndex{ascGroup}, have: nil, miss: 1},
		{name: "scope differs", want: []FieldOverrideIndex{ascGroup}, have: []FieldOverrideIndex{ascCollection}, miss: 1},
		{name: "mode differs", want: []FieldOverrideIndex{ascCollection}, have: []FieldOverrideIndex{contains}, miss: 1},
		{name: "extra live entries are fine", want: []FieldOverrideIndex{ascCollection}, have: []FieldOverrideIndex{contains, ascCollection, ascGroup}, miss: 0},
		{name: "the default configuration covers collection scope", want: []FieldOverrideIndex{ascCollection, contains}, have: defaultFieldIndexes, miss: 0},
		{name: "the default configuration does NOT cover group scope", want: []FieldOverrideIndex{ascGroup}, have: defaultFieldIndexes, miss: 1},
		{name: "several missing at once", want: []FieldOverrideIndex{ascGroup, contains}, have: nil, miss: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := missingFieldIndexes(tc.want, tc.have); len(got) != tc.miss {
				t.Errorf("missing = %+v, want %d entries", got, tc.miss)
			}
		})
	}
}

// TestCollectionGroupOf pins the parse of an Admin API index resource name,
// which is the only place a live index's collection group comes from.
func TestCollectionGroupOf(t *testing.T) {
	cases := map[string]string{
		"projects/p/databases/(default)/collectionGroups/iam_relationships/indexes/CICAgOjs": "iam_relationships",
		"projects/p/databases/ci-42/collectionGroups/users/indexes/abc":                      "users",
		"projects/p/databases/ci-42/collectionGroups/users":                                  "users",
		"":                                       "",
		"projects/p/databases/ci-42/indexes/abc": "",
	}
	for name, want := range cases {
		if got := collectionGroupOf(name); got != want {
			t.Errorf("collectionGroupOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestIndexesConsoleURL keeps the operator link buildable for a named database
// and for the parenthesized default one.
func TestIndexesConsoleURL(t *testing.T) {
	db := &DB{project: "my project", database: "(default)"}
	got := db.indexesConsoleURL()
	if !strings.Contains(got, "my+project") && !strings.Contains(got, "my%20project") {
		t.Errorf("URL %q does not escape the project", got)
	}
	if !strings.Contains(got, "/firestore/databases/") || !strings.HasSuffix(got, "indexes?project=my+project") && !strings.HasSuffix(got, "indexes?project=my%20project") {
		t.Errorf("URL = %q", got)
	}
}
