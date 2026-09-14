package firestore_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// testdataFS is the golden corpus: a store fragment, a host manifest carrying
// unrelated indexes and an unrelated field override, and the byte-exact result
// of merging one into the other.
var testdataFS = os.DirFS("testdata")

// ptr is a one-liner for the optional ttl flag.
func ptr[T any](v T) *T { return &v }

// TestParseIndexManifest reads the store fragment and pins what the parser
// produced — field ORDER inside a composite index is semantic, so it is
// asserted position by position.
func TestParseIndexManifest(t *testing.T) {
	m, err := firestore.ParseIndexManifest(testdataFS, "store_manifest.json")
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}

	if len(m.Indexes) != 3 {
		t.Fatalf("indexes = %d, want 3", len(m.Indexes))
	}
	if len(m.FieldOverrides) != 1 {
		t.Fatalf("fieldOverrides = %d, want 1", len(m.FieldOverrides))
	}

	first := m.Indexes[0]
	if first.CollectionGroup != "iam_relationships" || first.QueryScope != firestore.ScopeCollection {
		t.Errorf("indexes[0] = %q/%q, want iam_relationships/%s", first.CollectionGroup, first.QueryScope, firestore.ScopeCollection)
	}
	wantFields := []firestore.IndexField{
		{FieldPath: "resource_key", Order: firestore.OrderAscending},
		{FieldPath: "relationship_id", Order: firestore.OrderDescending},
	}
	if len(first.Fields) != len(wantFields) {
		t.Fatalf("indexes[0].fields = %d, want %d", len(first.Fields), len(wantFields))
	}
	for i, want := range wantFields {
		if first.Fields[i] != want {
			t.Errorf("indexes[0].fields[%d] = %+v, want %+v", i, first.Fields[i], want)
		}
	}

	override := m.FieldOverrides[0]
	if override.CollectionGroup != "iam_relationships" || override.FieldPath != "subject_key" {
		t.Errorf("fieldOverrides[0] = %q/%q", override.CollectionGroup, override.FieldPath)
	}
	if override.TTL != nil {
		t.Errorf("fieldOverrides[0].ttl = %v, want nil (absent is not false)", *override.TTL)
	}
	if len(override.Indexes) != 1 || override.Indexes[0].QueryScope != firestore.ScopeCollectionGroup {
		t.Errorf("fieldOverrides[0].indexes = %+v", override.Indexes)
	}
}

// TestParseIndexManifestReadsTheCLIFieldOverrideShape proves the ttl flag the
// Firebase CLI writes into fieldOverrides parses (strict parsing would
// otherwise reject a real host's file) and is distinguishable from absent.
func TestParseIndexManifestReadsTheCLIFieldOverrideShape(t *testing.T) {
	m, err := firestore.ParseIndexManifest(testdataFS, "host_manifest.json")
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}
	if len(m.FieldOverrides) != 1 {
		t.Fatalf("fieldOverrides = %d, want 1", len(m.FieldOverrides))
	}
	o := m.FieldOverrides[0]
	if o.TTL == nil || *o.TTL {
		t.Errorf("ttl = %v, want a non-nil false", o.TTL)
	}
	if o.Indexes == nil || len(o.Indexes) != 0 {
		t.Errorf("indexes = %v, want a present but empty list (an explicit \"no single-field indexes\")", o.Indexes)
	}
}

// TestParseIndexManifestRejects is the validation table. Every row is a
// manifest a host could plausibly hand-write, and each must fail as invalid
// INPUT — never be silently normalized, and never reach a deploy.
func TestParseIndexManifestRejects(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string // substring the message must name
	}{
		{
			name: "unknown key inside a field",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a","order":"ASCENDING","direction":"ASC"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: "direction",
		},
		{
			name: "unknown top-level key",
			doc:  `{"indexes":[],"indices":[]}`,
			want: "indices",
		},
		{
			name: "renamed queryScope key",
			doc:  `{"indexes":[{"collectionGroup":"c","scope":"COLLECTION","fields":[]}]}`,
			want: "scope",
		},
		{
			name: "not JSON at all",
			doc:  `indexes: []`,
			want: "parsing index manifest",
		},
		{
			name: "trailing content",
			doc:  `{"indexes":[]} {"indexes":[]}`,
			want: "trailing content",
		},
		{
			name: "empty collectionGroup",
			doc:  `{"indexes":[{"collectionGroup":"","queryScope":"COLLECTION","fields":[{"fieldPath":"a","order":"ASCENDING"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: "empty collectionGroup",
		},
		{
			name: "unknown queryScope value",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION_RECURSIVE","fields":[{"fieldPath":"a","order":"ASCENDING"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: "COLLECTION_RECURSIVE",
		},
		{
			name: "one-field composite",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a","order":"ASCENDING"}]}]}`,
			want: "at least 2 fields",
		},
		{
			name: "no fields at all",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[]}]}`,
			want: "at least 2 fields",
		},
		{
			name: "unknown order value",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a","order":"ASC"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: `order "ASC"`,
		},
		{
			name: "unknown arrayConfig value",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a","arrayConfig":"ANY"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: `arrayConfig "ANY"`,
		},
		{
			name: "field sets both modes",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a","order":"ASCENDING","arrayConfig":"CONTAINS"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: "both order and arrayConfig",
		},
		{
			name: "field sets neither mode",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: "neither order nor arrayConfig",
		},
		{
			name: "empty fieldPath",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"","order":"ASCENDING"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: "empty fieldPath",
		},
		{
			name: "fieldPath with a slash",
			doc:  `{"indexes":[{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a/b","order":"ASCENDING"},{"fieldPath":"b","order":"ASCENDING"}]}]}`,
			want: "contains a slash",
		},
		{
			name: "duplicate composite index",
			doc: `{"indexes":[
				{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a","order":"ASCENDING"},{"fieldPath":"b","order":"ASCENDING"}]},
				{"collectionGroup":"c","queryScope":"COLLECTION","fields":[{"fieldPath":"a","order":"ASCENDING"},{"fieldPath":"b","order":"ASCENDING"}]}
			]}`,
			want: "indexes[1] duplicates indexes[0]",
		},
		{
			name: "duplicate field override",
			doc: `{"indexes":[],"fieldOverrides":[
				{"collectionGroup":"c","fieldPath":"a","indexes":[]},
				{"collectionGroup":"c","fieldPath":"a","indexes":[{"order":"ASCENDING","queryScope":"COLLECTION"}]}
			]}`,
			want: "fieldOverrides[1] duplicates fieldOverrides[0]",
		},
		{
			name: "override without a queryScope",
			doc:  `{"indexes":[],"fieldOverrides":[{"collectionGroup":"c","fieldPath":"a","indexes":[{"order":"ASCENDING"}]}]}`,
			want: `queryScope ""`,
		},
		{
			name: "override index sets neither mode",
			doc:  `{"indexes":[],"fieldOverrides":[{"collectionGroup":"c","fieldPath":"a","indexes":[{"queryScope":"COLLECTION"}]}]}`,
			want: "neither order nor arrayConfig",
		},
		{
			name: "override with an empty fieldPath",
			doc:  `{"indexes":[],"fieldOverrides":[{"collectionGroup":"c","fieldPath":"","indexes":[]}]}`,
			want: "empty fieldPath",
		},
		{
			name: "override repeats one single-field index",
			doc: `{"indexes":[],"fieldOverrides":[{"collectionGroup":"c","fieldPath":"a","indexes":[
				{"order":"ASCENDING","queryScope":"COLLECTION"},
				{"order":"ASCENDING","queryScope":"COLLECTION"}
			]}]}`,
			want: "indexes[1] is a duplicate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"firestore.indexes.json": {Data: []byte(tc.doc)}}
			_, err := firestore.ParseIndexManifest(fsys, "firestore.indexes.json")
			if err == nil {
				t.Fatalf("ParseIndexManifest accepted %s", tc.doc)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("error = %v, want sdk.ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// TestParseIndexManifestKeyMatchingIsCaseInsensitive records the one gap in
// strict parsing, so nobody mistakes it for a bug later: encoding/json matches
// object keys case-insensitively, so "queryscope" IS "queryScope" and
// DisallowUnknownFields never sees it. A key that differs by more than case is
// still rejected (the table above), which is the case that matters — the
// Firebase CLI only ever writes the canonical spelling.
func TestParseIndexManifestKeyMatchingIsCaseInsensitive(t *testing.T) {
	doc := `{"indexes":[{"collectiongroup":"c","QUERYSCOPE":"COLLECTION","fields":[{"fieldpath":"a","ORDER":"ASCENDING"},{"fieldPath":"b","order":"ASCENDING"}]}]}`
	m, err := firestore.ParseIndexManifest(fstest.MapFS{"m.json": {Data: []byte(doc)}}, "m.json")
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}
	if got := describe(m.Indexes[0]); got != "c/COLLECTION/a order=ASCENDING,b order=ASCENDING" {
		t.Errorf("parsed = %s", got)
	}
}

// TestParseIndexManifestMissingFile keeps an I/O failure an I/O failure: a
// store whose embed pattern went stale must not read as "invalid manifest".
func TestParseIndexManifestMissingFile(t *testing.T) {
	_, err := firestore.ParseIndexManifest(fstest.MapFS{}, "firestore.indexes.json")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want fs.ErrNotExist", err)
	}
	if errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("a missing file was reported as invalid input: %v", err)
	}
}

// TestMergeUnionsAndCollapses is the merge contract: nothing from either side
// is dropped, an entry both sides declare appears once, and the result is in
// the documented canonical order regardless of input order.
func TestMergeUnionsAndCollapses(t *testing.T) {
	host := manifest(t, "host_manifest.json")
	store := manifest(t, "store_manifest.json")

	merged, err := host.Merge(store)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if len(merged.Indexes) != 4 {
		t.Fatalf("merged indexes = %d, want 4 (2 host + 3 store, one shared)", len(merged.Indexes))
	}
	if len(merged.FieldOverrides) != 2 {
		t.Fatalf("merged fieldOverrides = %d, want 2", len(merged.FieldOverrides))
	}

	wantOrder := []string{
		"iam_relationships/COLLECTION/resource_key order=ASCENDING,relationship_id order=ASCENDING",
		"iam_relationships/COLLECTION/resource_key order=ASCENDING,relationship_id order=DESCENDING",
		"iam_role_assignments/COLLECTION_GROUP/subject_key order=ASCENDING,role_key order=ASCENDING,__name__ order=ASCENDING",
		"posts/COLLECTION/author arrayConfig=CONTAINS,timestamp order=DESCENDING",
	}
	for i, want := range wantOrder {
		if got := describe(merged.Indexes[i]); got != want {
			t.Errorf("merged.Indexes[%d] = %s, want %s", i, got, want)
		}
	}
	if merged.FieldOverrides[0].FieldPath != "subject_key" || merged.FieldOverrides[1].FieldPath != "big_map" {
		t.Errorf("field overrides are not in (collectionGroup, fieldPath) order: %+v", merged.FieldOverrides)
	}

	// The other direction must produce the same document.
	reverse, err := store.Merge(host)
	if err != nil {
		t.Fatalf("Merge (reversed): %v", err)
	}
	for i := range merged.Indexes {
		if describe(reverse.Indexes[i]) != describe(merged.Indexes[i]) {
			t.Fatalf("merge is not commutative at index %d: %s vs %s", i, describe(reverse.Indexes[i]), describe(merged.Indexes[i]))
		}
	}
}

// TestMergeIdentityIsTheWholeIndex proves what "the same index" means: same
// collection group, same scope, same fields in the same order with the same
// directions. Change any one of them and the merge keeps BOTH, because both
// are needed.
func TestMergeIdentityIsTheWholeIndex(t *testing.T) {
	base := firestore.CompositeIndex{
		CollectionGroup: "c",
		QueryScope:      firestore.ScopeCollection,
		Fields: []firestore.IndexField{
			{FieldPath: "a", Order: firestore.OrderAscending},
			{FieldPath: "b", Order: firestore.OrderAscending},
		},
	}

	variants := map[string]func(firestore.CompositeIndex) firestore.CompositeIndex{
		"identical": func(c firestore.CompositeIndex) firestore.CompositeIndex { return c },
		"other collection group": func(c firestore.CompositeIndex) firestore.CompositeIndex {
			c.CollectionGroup = "other"
			return c
		},
		"other scope": func(c firestore.CompositeIndex) firestore.CompositeIndex {
			c.QueryScope = firestore.ScopeCollectionGroup
			return c
		},
		"other direction": func(c firestore.CompositeIndex) firestore.CompositeIndex {
			c.Fields = []firestore.IndexField{
				{FieldPath: "a", Order: firestore.OrderAscending},
				{FieldPath: "b", Order: firestore.OrderDescending},
			}
			return c
		},
		"fields swapped": func(c firestore.CompositeIndex) firestore.CompositeIndex {
			c.Fields = []firestore.IndexField{
				{FieldPath: "b", Order: firestore.OrderAscending},
				{FieldPath: "a", Order: firestore.OrderAscending},
			}
			return c
		},
		"extra field": func(c firestore.CompositeIndex) firestore.CompositeIndex {
			c.Fields = append(append([]firestore.IndexField{}, c.Fields...), firestore.IndexField{FieldPath: "c", Order: firestore.OrderAscending})
			return c
		},
	}

	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			left := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{base}}
			right := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{mutate(base)}}
			merged, err := left.Merge(right)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			want := 2
			if name == "identical" {
				want = 1
			}
			if len(merged.Indexes) != want {
				t.Fatalf("merged indexes = %d, want %d", len(merged.Indexes), want)
			}
		})
	}
}

// TestMergeFieldOverrides covers the three answers a shared override key can
// produce: collapse when the sets agree (even in a different order), adopt a
// ttl flag only one side states, and refuse when the sets differ.
func TestMergeFieldOverrides(t *testing.T) {
	asc := firestore.FieldOverrideIndex{Order: firestore.OrderAscending, QueryScope: firestore.ScopeCollectionGroup}
	desc := firestore.FieldOverrideIndex{Order: firestore.OrderDescending, QueryScope: firestore.ScopeCollectionGroup}

	t.Run("same set in another order collapses", func(t *testing.T) {
		left := override("c", "a", nil, asc, desc)
		right := override("c", "a", nil, desc, asc)
		merged, err := left.Merge(right)
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		if len(merged.FieldOverrides) != 1 {
			t.Fatalf("fieldOverrides = %d, want 1", len(merged.FieldOverrides))
		}
		if len(merged.FieldOverrides[0].Indexes) != 2 {
			t.Fatalf("indexes = %+v, want both", merged.FieldOverrides[0].Indexes)
		}
	})

	t.Run("a ttl flag stated once is adopted", func(t *testing.T) {
		merged, err := override("c", "a", ptr(true), asc).Merge(override("c", "a", nil, asc))
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		if got := merged.FieldOverrides[0].TTL; got == nil || !*got {
			t.Errorf("ttl = %v, want true", got)
		}
	})

	t.Run("conflicting sets are refused and both are named", func(t *testing.T) {
		_, err := override("c", "a", nil, asc).Merge(override("c", "a", nil, desc))
		if !errors.Is(err, firestore.ErrConflictingFieldOverride) {
			t.Fatalf("error = %v, want ErrConflictingFieldOverride", err)
		}
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("error = %v, want sdk.ErrInvalidInput", err)
		}
		for _, want := range []string{"ASCENDING", "DESCENDING", `"a"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to name %q", err, want)
			}
		}
	})

	t.Run("conflicting ttl is refused", func(t *testing.T) {
		_, err := override("c", "a", ptr(true), asc).Merge(override("c", "a", ptr(false), asc))
		if !errors.Is(err, firestore.ErrConflictingFieldOverride) {
			t.Fatalf("error = %v, want ErrConflictingFieldOverride", err)
		}
	})
}

// TestMergeValidatesBothSides keeps Merge from manufacturing a document the
// parser would reject.
func TestMergeValidatesBothSides(t *testing.T) {
	bad := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{{
		CollectionGroup: "c",
		QueryScope:      firestore.ScopeCollection,
		Fields:          []firestore.IndexField{{FieldPath: "a", Order: firestore.OrderAscending}},
	}}}
	good := manifest(t, "store_manifest.json")

	if _, err := good.Merge(bad); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("Merge(good, bad) = %v, want sdk.ErrInvalidInput", err)
	}
	if _, err := bad.Merge(good); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("Merge(bad, good) = %v, want sdk.ErrInvalidInput", err)
	}
}

// TestExportIndexesMergesIntoAHostManifest is the golden test: exporting a
// store fragment into a host manifest that already carries unrelated indexes
// and an unrelated field override produces exactly testdata/merged_manifest.json,
// byte for byte.
func TestExportIndexesMergesIntoAHostManifest(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "firestore.indexes.json")
	host, err := fs.ReadFile(testdataFS, "host_manifest.json")
	if err != nil {
		t.Fatalf("read host manifest: %v", err)
	}
	if err := os.WriteFile(dst, host, 0o644); err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	if err := firestore.ExportIndexes(manifest(t, "store_manifest.json"), dst); err != nil {
		t.Fatalf("ExportIndexes: %v", err)
	}

	want, err := fs.ReadFile(testdataFS, "merged_manifest.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("exported manifest differs from testdata/merged_manifest.json\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestExportIndexesIsIdempotent proves the property a host's diff depends on:
// re-exporting an unchanged fragment rewrites the same bytes, so the merge is
// not a slow drift of the host's file.
func TestExportIndexesIsIdempotent(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "firestore.indexes.json")
	m := manifest(t, "store_manifest.json")

	if err := firestore.ExportIndexes(m, dst); err != nil {
		t.Fatalf("first ExportIndexes: %v", err)
	}
	first, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	for i := range 2 {
		if err := firestore.ExportIndexes(m, dst); err != nil {
			t.Fatalf("ExportIndexes #%d: %v", i+2, err)
		}
		again, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("export #%d changed the file\n--- before ---\n%s\n--- after ---\n%s", i+2, first, again)
		}
	}

	// And the bytes it writes are bytes it can read back.
	if _, err := firestore.ParseIndexManifest(os.DirFS(filepath.Dir(dst)), filepath.Base(dst)); err != nil {
		t.Fatalf("the exported manifest does not parse: %v", err)
	}
}

// TestExportIndexesCreatesTheDestination covers the first-run case, including
// the parent directory a fresh host repository does not have yet, and pins the
// file's shape: sorted, two-space indented, one trailing newline.
func TestExportIndexesCreatesTheDestination(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "config", "firestore", "firestore.indexes.json")

	if err := firestore.ExportIndexes(manifest(t, "store_manifest.json"), dst); err != nil {
		t.Fatalf("ExportIndexes: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	text := string(got)
	if !strings.HasSuffix(text, "}\n") {
		t.Errorf("manifest does not end in a single trailing newline: %q", text[max(0, len(text)-8):])
	}
	if !strings.Contains(text, "\n  \"indexes\": [") {
		t.Errorf("manifest is not two-space indented:\n%s", text)
	}
	first := strings.Index(text, "iam_relationships")
	last := strings.Index(text, "iam_role_assignments")
	if first < 0 || last < 0 || first > last {
		t.Errorf("manifest is not sorted by collection group:\n%s", text)
	}
}

// TestExportIndexesRefusesRatherThanCorrupts covers the three refusals: an
// invalid manifest, an unreadable destination, and a destination that
// contradicts the fragment. In every case the existing file must survive
// untouched — a half-written manifest is a deploy nobody can reason about.
func TestExportIndexesRefusesRatherThanCorrupts(t *testing.T) {
	t.Run("invalid manifest", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "firestore.indexes.json")
		bad := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{{
			CollectionGroup: "c",
			QueryScope:      "SOMEWHERE",
			Fields: []firestore.IndexField{
				{FieldPath: "a", Order: firestore.OrderAscending},
				{FieldPath: "b", Order: firestore.OrderAscending},
			},
		}}}
		if err := firestore.ExportIndexes(bad, dst); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("error = %v, want sdk.ErrInvalidInput", err)
		}
		if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a rejected export created %s", dst)
		}
	})

	t.Run("destination is not a valid manifest", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "firestore.indexes.json")
		original := []byte("{ this is not json\n")
		if err := os.WriteFile(dst, original, 0o644); err != nil {
			t.Fatalf("seed dst: %v", err)
		}
		err := firestore.ExportIndexes(manifest(t, "store_manifest.json"), dst)
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("error = %v, want sdk.ErrInvalidInput", err)
		}
		got, _ := os.ReadFile(dst)
		if string(got) != string(original) {
			t.Errorf("the unreadable destination was overwritten: %q", got)
		}
	})

	t.Run("destination contradicts a field override", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "firestore.indexes.json")
		original := []byte(`{"indexes":[],"fieldOverrides":[{"collectionGroup":"iam_relationships","fieldPath":"subject_key","indexes":[{"order":"DESCENDING","queryScope":"COLLECTION_GROUP"}]}]}`)
		if err := os.WriteFile(dst, original, 0o644); err != nil {
			t.Fatalf("seed dst: %v", err)
		}
		err := firestore.ExportIndexes(manifest(t, "store_manifest.json"), dst)
		if !errors.Is(err, firestore.ErrConflictingFieldOverride) {
			t.Fatalf("error = %v, want ErrConflictingFieldOverride", err)
		}
		got, _ := os.ReadFile(dst)
		if string(got) != string(original) {
			t.Errorf("a refused merge rewrote the host manifest: %q", got)
		}
	})
}

// TestExportIndexesIsAtomicAndLeavesNoDebris is the C8 fold of "never truncate
// the host's manifest". Export writes a temporary beside dst and renames it
// over, so a reader sees the old file or the new one and never a half-written
// one — and, just as importantly, every REFUSED export leaves the directory
// exactly as it found it: no orphan .tmp for someone to find months later and
// wonder whether it is the real manifest.
func TestExportIndexesIsAtomicAndLeavesNoDebris(t *testing.T) {
	assertNoTmp := func(t *testing.T, dir string) {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("export left %s behind", e.Name())
			}
		}
	}

	t.Run("a successful export leaves only the manifest", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "firestore.indexes.json")
		if err := firestore.ExportIndexes(manifest(t, "store_manifest.json"), dst); err != nil {
			t.Fatalf("ExportIndexes: %v", err)
		}
		if _, err := os.Stat(dst); err != nil {
			t.Fatalf("the manifest was not written: %v", err)
		}
		assertNoTmp(t, dir)
	})

	t.Run("a refused export leaves no tmp and no manifest", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "firestore.indexes.json")
		bad := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{{
			CollectionGroup: "c",
			QueryScope:      "SOMEWHERE",
			Fields: []firestore.IndexField{
				{FieldPath: "a", Order: firestore.OrderAscending},
				{FieldPath: "b", Order: firestore.OrderAscending},
			},
		}}}
		if err := firestore.ExportIndexes(bad, dst); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("error = %v, want sdk.ErrInvalidInput", err)
		}
		if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a rejected export created %s", dst)
		}
		assertNoTmp(t, dir)
	})

	t.Run("a refused merge leaves the host manifest and no tmp", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "firestore.indexes.json")
		original := []byte(`{"indexes":[],"fieldOverrides":[{"collectionGroup":"iam_relationships","fieldPath":"subject_key","indexes":[{"order":"DESCENDING","queryScope":"COLLECTION_GROUP"}]}]}`)
		if err := os.WriteFile(dst, original, 0o644); err != nil {
			t.Fatalf("seed dst: %v", err)
		}
		if err := firestore.ExportIndexes(manifest(t, "store_manifest.json"), dst); !errors.Is(err, firestore.ErrConflictingFieldOverride) {
			t.Fatalf("error = %v, want ErrConflictingFieldOverride", err)
		}
		got, _ := os.ReadFile(dst)
		if string(got) != string(original) {
			t.Errorf("a refused merge rewrote the host manifest: %q", got)
		}
		assertNoTmp(t, dir)
	})
}

// TestProbeIndexesNeedsADB pins the wiring-bug guard so a nil DB is an error
// rather than a panic in a store constructor.
func TestProbeIndexesNeedsADB(t *testing.T) {
	err := firestore.ProbeIndexes(context.Background(), nil, firestore.IndexManifest{})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("error = %v, want sdk.ErrInvalidInput", err)
	}
}

// manifest parses one of the testdata documents or fails the test.
func manifest(t *testing.T, name string) firestore.IndexManifest {
	t.Helper()
	m, err := firestore.ParseIndexManifest(testdataFS, name)
	if err != nil {
		t.Fatalf("ParseIndexManifest(%s): %v", name, err)
	}
	return m
}

// override builds a one-entry manifest carrying a single field override.
func override(group, field string, ttl *bool, indexes ...firestore.FieldOverrideIndex) firestore.IndexManifest {
	return firestore.IndexManifest{FieldOverrides: []firestore.FieldOverride{{
		CollectionGroup: group,
		FieldPath:       field,
		TTL:             ttl,
		Indexes:         indexes,
	}}}
}

// describe renders a composite index as one comparable line for assertions.
func describe(c firestore.CompositeIndex) string {
	parts := make([]string, 0, len(c.Fields))
	for _, f := range c.Fields {
		mode := "order=" + f.Order
		if f.ArrayConfig != "" {
			mode = "arrayConfig=" + f.ArrayConfig
		}
		parts = append(parts, f.FieldPath+" "+mode)
	}
	return c.CollectionGroup + "/" + c.QueryScope + "/" + strings.Join(parts, ",")
}
