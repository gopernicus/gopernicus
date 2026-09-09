package firestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claimOwners are the only non-test files of this package allowed to name the
// two claim collections: keys.go DECLARES the constants and the document-id
// builders, documents.go declares the two claim SHAPES, and tuples.go is the
// pair of helpers that owns a tuple's three documents. None of the three is a
// write path of its own except tuples.go, which is the point.
var claimOwners = map[string]bool{
	"keys.go":      true,
	"tuples.go":    true,
	"documents.go": true,
}

// claimSymbols are the names a write path would have to use to touch a claim
// collection: the constants, and the raw collection names in case a future
// change reaches past the constants.
var claimSymbols = []string{
	"collectionSubjectClaims",
	"collectionIDClaims",
	"iam_relationship_subjects",
	"iam_relationship_ids",
}

// TestClaimCollectionsAreOwnedByTuplesOnly is the A-D3 risk mitigation, made
// executable. A relationship tuple's uniqueness lives in two CLAIM documents
// beside the row (SCHEMA.md §5.2, §5.3), and the failure mode that would break
// it silently is a future write path that changes the row and forgets a claim —
// leaving the one-relation-per-subject and primary-key invariants enforced by
// nothing at all. The mitigation is that putTuple and dropTuple own all three
// documents; this test is what keeps that true, by refusing to let any other
// file in the package so much as NAME a claim collection.
//
// It is hermetic on purpose (no emulator, no build tag): the property is about
// this package's source, so it should fail on a plain `go test ./...` in the
// review that introduces the drift, not on a datastore run.
//
// Test files are exempt: proving the claim lifecycle requires reading those
// documents, and a test cannot corrupt production state.
func TestClaimCollectionsAreOwnedByTuplesOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") || claimOwners[name] {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		checked++
		for _, symbol := range claimSymbols {
			if strings.Contains(string(src), symbol) {
				t.Errorf("%s names %s: a tuple's claim documents are owned by putTuple/dropTuple in tuples.go — route the change through them instead", name, symbol)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no package sources were checked — the guard would pass vacuously")
	}
}

// TestClaimOwnersExist keeps the allow-list honest: a renamed or deleted owner
// file must fail here rather than silently widening the rule above.
func TestClaimOwnersExist(t *testing.T) {
	for name := range claimOwners {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("claim owner %s: %v", name, err)
		}
	}
}
