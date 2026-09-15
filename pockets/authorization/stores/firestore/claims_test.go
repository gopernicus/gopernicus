package firestore

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claimOwners are the only non-test files of this package allowed to name the
// subject claim collection: keys.go DECLARES the constants and the document-id
// builders, documents.go declares the claim shape, and tuples.go is the
// pair of helpers that owns a tuple's two documents. None of the three is a
// write path of its own except tuples.go, which is the point.
var claimOwners = map[string]bool{
	"upgrade.go":   true, // explicit host maintenance, never a runtime write path
	"keys.go":      true,
	"tuples.go":    true,
	"documents.go": true,
}

// claimSymbols are the names a write path would have to use to touch a claim
// collection: the constants, and the raw collection names in case a future
// change reaches past the constants.
var claimSymbols = []string{
	"collectionSubjectClaims",
	"iam_relationship_subjects",
	"iam_relationship_ids",
}

// TestClaimCollectionsAreOwnedByTuplesOnly prevents runtime writers from
// bypassing the helpers that maintain a tuple and its subject claim together.
// The explicit offline upgrade is the only separate maintenance writer.
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
		src, err := sourceWithoutComments(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		checked++
		for _, symbol := range claimSymbols {
			if strings.Contains(src, symbol) {
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

// roleOwners are the only non-test files allowed to name the iam_roles
// collection: keys.go DECLARES the constant and the document-id builder,
// documents.go declares the document SHAPE, and grants.go is the file that owns
// the collection — putRole and dropRole are its only writers, roleRef and
// rolesQuery its only addressing, decodeRole its only decoder.
var roleOwners = map[string]bool{
	"upgrade.go":   true, // removes legacy role timestamps during host maintenance
	"keys.go":      true,
	"documents.go": true,
	"grants.go":    true,
}

// roleSymbols are the names a write path would have to use to reach a role
// document: the constant, and the raw collection name in case a future change
// reaches past it.
var roleSymbols = []string{
	"collectionRoles",
	"iam_roles",
}

// TestRoleCollectionIsOwnedByGrantsOnly is the A3a twin of the claim-ownership
// rule above. A role grant carries no claim beside it — its document id IS the
// unique 5-tuple — but it does carry FOUR derived fields (two equality keys and
// two contractual sort keys), and the failure mode that would break them
// silently is a future write path that stores a row without them or with keys
// that disagree with its own fields. Nothing at read time would notice: the row
// would simply be invisible to the listings and to the lookup.
//
// The mitigation is that putRole computes all four from the row itself; this
// test is what keeps that true, by refusing to let any other file in the package
// so much as NAME the collection. It matters most for A4, whose mutation path
// assigns and unassigns roles inside a transaction: that path must reach the
// collection through grants.go rather than build a second write of its own.
//
// Hermetic on purpose, and test files are exempt, for the same reasons as above.
func TestRoleCollectionIsOwnedByGrantsOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") || roleOwners[name] {
			continue
		}
		src, err := sourceWithoutComments(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		checked++
		for _, symbol := range roleSymbols {
			if strings.Contains(src, symbol) {
				t.Errorf("%s names %s: role documents are owned by putRole/dropRole in grants.go — route the change through them instead", name, symbol)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no package sources were checked — the guard would pass vacuously")
	}
}

// TestRoleOwnersExist keeps the allow-list honest.
func TestRoleOwnersExist(t *testing.T) {
	for name := range roleOwners {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("role owner %s: %v", name, err)
		}
	}
}

// Only the explicit cleanup may address retired protocol collections.
func TestRetiredMutationCollectionsOnlyAppearInMaintenance(t *testing.T) {
	assertCollectionOwnership(t, map[string]bool{"upgrade.go": true}, []string{"iam_mutations", "iam_scopes"}, "legacy ledgers belong only to explicit maintenance")
}

func TestAuditCollectionHasOneWriter(t *testing.T) {
	assertCollectionOwnership(t, map[string]bool{"keys.go": true, "audit.go": true}, []string{"collectionAudit", "iam_audit"}, "audit writes and reads belong in audit.go")
}

// assertCollectionOwnership is the shared body of the ownership rules: no
// non-test file outside owners may NAME any of symbols, comments excluded.
func assertCollectionOwnership(t *testing.T, owners map[string]bool, symbols []string, remedy string) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") || owners[name] {
			continue
		}
		src, err := sourceWithoutComments(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		checked++
		for _, symbol := range symbols {
			if strings.Contains(src, symbol) {
				t.Errorf("%s names %s: %s", name, symbol, remedy)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no package sources were checked — the guard would pass vacuously")
	}
}

// sourceWithoutComments renders one package source with its COMMENTS removed. It
// is what lets the role rule above be about code: doc.go and roles.go both name
// the collection in prose, on purpose — an operator reading the package doc
// should see the SQL table name it mirrors — and a documentation sentence is not
// a write path.
func sourceWithoutComments(name string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := printer.Fprint(&out, fset, file); err != nil {
		return "", err
	}
	return out.String(), nil
}
