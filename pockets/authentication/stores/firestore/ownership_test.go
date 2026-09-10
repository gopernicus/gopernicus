package firestore

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The collection- and field-ownership rules, made executable. They are the
// mitigation SCHEMA.md §5 and the milestone's risk register name for the one
// failure mode that breaks a claim-backed store SILENTLY: a future write path
// that changes a row and forgets one of the claim documents that reproduces its
// unique index, or that writes a users document from a partial view and blanks
// the directory projection. Nothing at READ time notices either; the damage
// surfaces months later as two accounts sharing one login address or a directory
// full of blank emails.
//
// Every rule below is the same shape as the authorization store's
// TestClaimCollectionsAreOwnedByTuplesOnly: no non-test file outside a small
// allow-list may so much as NAME the owned symbol. They are hermetic on purpose
// (no emulator, no build tag) — the property is about this package's SOURCE, so
// it should fail on a plain `go test ./...` in the review that introduces the
// drift, not on a datastore run.
//
// Test files are exempt: proving a claim's lifecycle requires reading those
// documents, and a test cannot corrupt production state. The source is rendered
// with its COMMENTS REMOVED, because a doc comment naming the SQL table it
// mirrors is documentation, not a write path — and this package's comments name
// them constantly, on purpose.

// identifierOwners are the only non-test files allowed to name the identifier
// row's collection or either of its two claim collections: keys.go DECLARES the
// constants and the document-id builders, documents.go declares the row and
// claim SHAPES, and identifiers_doc.go is the file that owns them —
// putIdentifier and updateIdentifier are its only writers.
var identifierOwners = map[string]bool{
	"keys.go":            true,
	"documents.go":       true,
	"identifiers_doc.go": true,
}

// identifierSymbols are the names a write path would have to use to reach an
// identifier row or one of its claims: the constants, and the raw collection
// names in case a future change reaches past them.
var identifierSymbols = []string{
	"collectionIdentifiers",
	"collectionIdentifierClaims",
	"collectionIdentifierPrimaries",
	"user_identifiers",
	"identifier_claims",
	"identifier_primaries",
}

// TestIdentifierCollectionsAreOwnedByIdentifiersDoc is the R3 discipline for
// this pocket's two partial unique indexes (SCHEMA.md §5.1, §5.2). An identifier
// row's uniqueness lives entirely in two claim documents beside it, and the pair
// putIdentifier/updateIdentifier is what keeps a row and its claims moving
// together. This test is what keeps that pair the only way in.
func TestIdentifierCollectionsAreOwnedByIdentifiersDoc(t *testing.T) {
	assertOwnership(t, identifierOwners, identifierSymbols,
		"identifier rows and their two claim documents are owned by identifiers_doc.go (putIdentifier/updateIdentifier) — route the change through them instead")
}

// userOwners are the only non-test files allowed to name the users collection:
// keys.go DECLARES the constant and the document-id builder, documents.go
// declares the SHAPE, and users_doc.go owns every read, write, and decode.
var userOwners = map[string]bool{
	"keys.go":      true,
	"documents.go": true,
	"users_doc.go": true,
}

var userSymbols = []string{
	"collectionUsers",
}

// TestUsersCollectionIsOwnedByUsersDoc guards the invariant behind the directory
// projection (SCHEMA.md §6.2): the users document carries two fields the domain
// user.User has no place for, so a write path that built a users document from a
// domain value would silently erase them. users_doc.go exposes exactly ONE
// whole-document write — putUser, which takes the projection as an argument —
// and field updates for everything else, so the erasure is not reachable.
func TestUsersCollectionIsOwnedByUsersDoc(t *testing.T) {
	assertOwnership(t, userOwners, userSymbols,
		"users documents are owned by users_doc.go — putUser is the only whole-document write and it takes the projection explicitly; everything else is a field update")
}

// passwordOwners are the only non-test files allowed to name the credential
// collection.
var passwordOwners = map[string]bool{
	"keys.go":          true,
	"documents.go":     true,
	"passwords_doc.go": true,
}

var passwordSymbols = []string{
	"collectionPasswords",
}

// TestPasswordCollectionIsOwnedByPasswordsDoc keeps credential material to one
// writer. The pocket separates user_passwords from users precisely so a store
// can guard it independently; a second write path would quietly undo that.
func TestPasswordCollectionIsOwnedByPasswordsDoc(t *testing.T) {
	assertOwnership(t, passwordOwners, passwordSymbols,
		"credential rows are owned by passwords_doc.go (putPassword/readPassword) — route the change through them instead")
}

// projectionOwners are the only non-test files allowed to name either directory
// projection field, in EITHER spelling: documents.go declares the two struct
// fields and their firestore tags, and projection.go is the file that computes,
// stamps, renders, and reads them back.
var projectionOwners = map[string]bool{
	"documents.go":  true,
	"projection.go": true,
}

// projectionSymbols covers the Firestore field paths, the constants that hold
// them, and the Go field names — because setting userDoc.PrimaryEmail directly
// is exactly as damaging as writing the field path, and the whole point of the
// rule is that no writer may decide the projection from a partial view of the
// change.
var projectionSymbols = []string{
	"fieldPrimaryEmail",
	"fieldEmailVerified",
	"primary_email",
	"email_verified",
	"PrimaryEmail",
	"EmailVerified",
}

// TestProjectionFieldsAreOwnedByProjection is N-D3's mitigation. The projection
// is only as good as the recomputation behind it: every writer must derive both
// fields from the identifier rows the transaction holds — resolveEmailProjection
// — rather than set one of them from what it happens to know. Confining the two
// names to projection.go is what makes "recompute, never assume" checkable.
func TestProjectionFieldsAreOwnedByProjection(t *testing.T) {
	assertOwnership(t, projectionOwners, projectionSymbols,
		"the directory projection fields are owned by projection.go — recompute the pair with resolveEmailProjection and hand it to putUser or advanceUserRevision")
}

// sessionOwners are the only non-test files allowed to name the session row's
// collection or its refresh-hash claim collection: keys.go DECLARES the
// constants and the id builders, documents.go declares the row and claim
// SHAPES, and sessions_doc.go is the file that owns them — putSession,
// updateSession and dropSession are its only writers.
var sessionOwners = map[string]bool{
	"keys.go":         true,
	"documents.go":    true,
	"sessions_doc.go": true,
}

var sessionSymbols = []string{
	"collectionSessions",
	"collectionRefreshHashClaims",
	"sessions",
	"session_refresh_hashes",
}

// TestSessionCollectionsAreOwnedBySessionsDoc is the R3 discipline for the
// CURRENT refresh credential (SCHEMA.md §5.3). Uniqueness lives in one claim
// document beside the row, and the rotated-away hash deliberately has none — an
// asymmetry a call site can break in either direction, by claiming the grace
// slot (which makes a rotation collide with itself) or by forgetting to release
// the old current claim (which makes the credential unusable forever). Keeping
// the pair the only way in is what makes that unreachable.
func TestSessionCollectionsAreOwnedBySessionsDoc(t *testing.T) {
	assertOwnership(t, sessionOwners, sessionSymbols,
		"session rows and their refresh-hash claim are owned by sessions_doc.go (putSession/updateSession/dropSession) — route the change through them instead")
}

// oauthOwners are the only non-test files allowed to name either OAuth
// collection. Neither has a claim: both uniqueness rules are carried by the
// DOCUMENT ID, which is precisely why every reference must go through the file
// that derives those ids.
var oauthOwners = map[string]bool{
	"keys.go":      true,
	"documents.go": true,
	"oauth_doc.go": true,
}

var oauthSymbols = []string{
	"collectionOAuthAccounts",
	"collectionOAuthStates",
	"oauth_accounts",
	"oauth_states",
}

// TestOAuthCollectionsAreOwnedByOAuthDoc keeps the id-derived uniqueness in one
// place. A write path that reached the account collection with any other
// document id — a surrogate id, a user-scoped key — would silently drop the
// "one local user per provider identity" rule the whole anti-takeover flow rests
// on, and no read would notice.
func TestOAuthCollectionsAreOwnedByOAuthDoc(t *testing.T) {
	assertOwnership(t, oauthOwners, oauthSymbols,
		"OAuth links and flow secrets are owned by oauth_doc.go — their document ids ARE their uniqueness, so route the change through it")
}

// grantOwners are the only non-test files allowed to name the step-up grant
// collection.
var grantOwners = map[string]bool{
	"keys.go":       true,
	"documents.go":  true,
	"grants_doc.go": true,
}

var grantSymbols = []string{
	"collectionAuthGrants",
	"authentication_grants",
}

// TestGrantCollectionIsOwnedByGrantsDoc keeps the derived consume key a single
// identity. The three columns Consume selects on are collapsed into ONE indexed
// equality, and a writer that stored a row without it — or with a key built from
// different parts — would create a grant that can never be spent and never be
// found again.
func TestGrantCollectionIsOwnedByGrantsDoc(t *testing.T) {
	assertOwnership(t, grantOwners, grantSymbols,
		"step-up grants are owned by grants_doc.go (putAuthGrant/spendAuthGrant/dropAuthGrants) — route the change through them instead")
}

// TestOwnerFilesExist keeps every allow-list honest: a renamed or deleted owner
// must fail here rather than silently widening the rule it appears in.
func TestOwnerFilesExist(t *testing.T) {
	for _, owners := range []map[string]bool{identifierOwners, userOwners, passwordOwners, projectionOwners, sessionOwners, oauthOwners, grantOwners} {
		for name := range owners {
			if _, err := os.Stat(name); err != nil {
				t.Errorf("owner file %s: %v", name, err)
			}
		}
	}
}

// assertOwnership is the shared body of the rules above: no non-test file
// outside owners may NAME any of symbols, comments excluded.
func assertOwnership(t *testing.T, owners map[string]bool, symbols []string, remedy string) {
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
			if namesSymbol(src, symbol) {
				t.Errorf("%s names %s: %s", name, symbol, remedy)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no package sources were checked — the guard would pass vacuously")
	}
}

// namesSymbol reports whether src NAMES symbol — as a whole identifier or a
// whole quoted string, not as a fragment of a longer name. The boundaries
// matter: oauth_accounts carries its provider's OWN verified-email flag
// (ProviderEmailVerified), which has nothing to do with the users document's
// directory projection (EmailVerified) and must not be read as a violation of
// its rule. A plain substring test cannot tell the two apart; this can, and it
// still catches every real use, because a real one is preceded by a dot, a
// space, a brace, or a quote.
func namesSymbol(src, symbol string) bool {
	pattern := regexp.MustCompile(`(^|[^\p{L}\p{N}_])` + regexp.QuoteMeta(symbol) + `([^\p{L}\p{N}_]|$)`)
	return pattern.MatchString(src)
}

// sourceWithoutComments renders one package source with its COMMENTS removed. It
// is what lets these rules be about CODE: this package's doc comments name the
// SQL tables and the claim predicates they mirror on nearly every page, on
// purpose, and a documentation sentence is not a write path.
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
