package firestore

import (
	"bytes"
	"go/ast"
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
// sdk. The three columns Consume selects on are collapsed into ONE indexed
// equality, and a writer that stored a row without it — or with a key built from
// different parts — would create a grant that can never be spent and never be
// found again.
func TestGrantCollectionIsOwnedByGrantsDoc(t *testing.T) {
	assertOwnership(t, grantOwners, grantSymbols,
		"step-up grants are owned by grants_doc.go (putAuthGrant/spendAuthGrant/dropAuthGrants) — route the change through them instead")
}

// invitationOwners are the only non-test files allowed to name the invitation
// row's collection or either of its two claim collections.
var invitationOwners = map[string]bool{
	"keys.go":            true,
	"documents.go":       true,
	"invitations_doc.go": true,
}

var invitationSymbols = []string{
	"collectionInvitations",
	"collectionInvitationTokens",
	"collectionInvitationPending",
	"invitations",
	"invitation_token_hashes",
	"invitation_pending",
}

// TestInvitationCollectionsAreOwnedByInvitationsDoc is the R3 discipline for the
// pending-tuple PARTIAL index (SCHEMA.md §5.6), which is the one claim in this
// store whose predicate a reader is most likely to get wrong: it is the STORED
// status, never the clock. A write path that released the tuple because it
// noticed the invitation had expired would let a second pending invite exist for
// a tuple the SQL adapters still consider taken, and no read would notice.
// Keeping putInvitation/updateInvitation the only way in is what makes the
// predicate a property of the code rather than of each call site's memory.
func TestInvitationCollectionsAreOwnedByInvitationsDoc(t *testing.T) {
	assertOwnership(t, invitationOwners, invitationSymbols,
		"invitation rows and their token/pending claims are owned by invitations_doc.go (putInvitation/updateInvitation) — route the change through them instead")
}

// challengeOwners are the only non-test files allowed to name the challenge row's
// collection or its digest claim collection.
var challengeOwners = map[string]bool{
	"keys.go":           true,
	"documents.go":      true,
	"challenges_doc.go": true,
}

var challengeSymbols = []string{
	"collectionChallenges",
	"collectionChallengeDigests",
	"challenges",
	"challenge_digests",
}

// TestChallengeCollectionsAreOwnedByChallengesDoc guards the store's most
// fragile claim relationship (SCHEMA.md §5.7). The challenge document is keyed by
// (subject_key, purpose), so a Replace DISPLACES the previous row instead of
// deleting it — and the displaced row's digest claim therefore has no other
// chance to be released. A write path that Set a challenge document without
// going through putChallenge would strand the old digest as claimed forever, and
// the only symptom would be an ErrAlreadyExists months later for a secret no
// live row explains. The purge and the reset composition write this collection
// too, which is exactly why the rule is enforced rather than documented.
func TestChallengeCollectionsAreOwnedByChallengesDoc(t *testing.T) {
	assertOwnership(t, challengeOwners, challengeSymbols,
		"challenge rows and their digest claim are owned by challenges_doc.go (putChallenge/updateChallengeAttempts/dropChallenge) — route the change through them instead")
}

// contactChangeOwners are the only non-test files allowed to name the pending
// contact-change collection. It carries no claim: its uniqueness IS the document
// id, which is precisely why every reference must go through the file that
// derives it.
var contactChangeOwners = map[string]bool{
	"keys.go":               true,
	"documents.go":          true,
	"contactchanges_doc.go": true,
}

var contactChangeSymbols = []string{
	"collectionContactChanges",
	"contact_changes",
}

// TestContactChangeCollectionIsOwnedByContactChangesDoc keeps the id-derived
// replacement in one place. A write path that reached this collection with any
// other document id — the row's surrogate id, say — would silently drop the "one
// pending change per user and kind" rule that makes Create an atomic replace,
// and the confirm step would then have two pending values to choose from.
func TestContactChangeCollectionIsOwnedByContactChangesDoc(t *testing.T) {
	assertOwnership(t, contactChangeOwners, contactChangeSymbols,
		"pending contact changes are owned by contactchanges_doc.go — the document id IS the replacement key, so route the change through it")
}

// serviceAccountOwners are the only non-test files allowed to name the
// machine-identity collection. It carries no claim — `name` is deliberately not
// unique (SCHEMA.md §5.9) — so the rule protects the opposite property from the
// claim rules above: that no future write path INVENTS a constraint the SQL
// siblings do not have.
var serviceAccountOwners = map[string]bool{
	"keys.go":                true,
	"documents.go":           true,
	"serviceaccounts_doc.go": true,
}

var serviceAccountSymbols = []string{
	"collectionServiceAccounts",
	"service_accounts",
}

// TestServiceAccountCollectionIsOwnedByServiceAccountsDoc keeps the audited
// NON-constraint auditable. A second write path is where a name claim, or a
// whole-document Set that rewrote created_at and silently reordered the
// directory, would arrive.
func TestServiceAccountCollectionIsOwnedByServiceAccountsDoc(t *testing.T) {
	assertOwnership(t, serviceAccountOwners, serviceAccountSymbols,
		"service accounts are owned by serviceaccounts_doc.go (putServiceAccount/updateServiceAccountProfile/dropServiceAccount) — route the change through them instead")
}

// apiKeyOwners are the only non-test files allowed to name the API-key row's
// collection or its key-hash claim collection.
var apiKeyOwners = map[string]bool{
	"keys.go":        true,
	"documents.go":   true,
	"apikeys_doc.go": true,
}

var apiKeySymbols = []string{
	"collectionAPIKeys",
	"collectionAPIKeyHashClaims",
	"api_keys",
	"api_key_hashes",
}

// TestAPIKeyCollectionsAreOwnedByAPIKeysDoc is the R3 discipline for a claim
// whose predicate is UNCONDITIONAL (SCHEMA.md §5.4) — the asymmetry that makes it
// worth enforcing. Every other claim in this store is released when its row
// leaves a predicate; this one is never released at all, because
// `idx_api_keys_key_hash` has no `WHERE revoked_at IS NULL`. A well-meaning
// write path that "cleaned up" the claim on revocation would make a revoked
// credential's hash mintable again, and no port case would notice.
func TestAPIKeyCollectionsAreOwnedByAPIKeysDoc(t *testing.T) {
	assertOwnership(t, apiKeyOwners, apiKeySymbols,
		"API keys and their key-hash claim are owned by apikeys_doc.go (putAPIKey/revokeAPIKey/touchAPIKey) — the claim is never released, so route the change through them instead")
}

// securityEventOwners are the only non-test files allowed to name the audit
// rail.
var securityEventOwners = map[string]bool{
	"keys.go":               true,
	"documents.go":          true,
	"securityevents_doc.go": true,
}

var securityEventSymbols = []string{
	"collectionSecurityEvents",
	"security_events",
}

// TestSecurityEventCollectionIsOwnedBySecurityEventsDoc keeps the rail
// APPEND-ONLY in the store as well as in the port. The port offers no Update and
// no Delete, and this rule is what keeps that structural: the only writer in the
// package is putSecurityEvent, so a rewrite path cannot be added quietly beside
// it. An audit trail a store can rewrite is not an audit trail.
func TestSecurityEventCollectionIsOwnedBySecurityEventsDoc(t *testing.T) {
	assertOwnership(t, securityEventOwners, securityEventSymbols,
		"audit events are owned by securityevents_doc.go (putSecurityEvent is its ONLY writer — the rail is append-only) — route the change through it")
}

// TestOwnerFilesExist keeps every allow-list honest: a renamed or deleted owner
// must fail here rather than silently widening the rule it appears in.
func TestOwnerFilesExist(t *testing.T) {
	for _, owners := range []map[string]bool{identifierOwners, userOwners, passwordOwners, projectionOwners, sessionOwners, oauthOwners, grantOwners,
		invitationOwners, challengeOwners, contactChangeOwners,
		serviceAccountOwners, apiKeyOwners, securityEventOwners} {
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
	// Import paths name public contracts, not datastore collections.
	declarations := file.Decls[:0]
	for _, declaration := range file.Decls {
		if gen, ok := declaration.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}
		declarations = append(declarations, declaration)
	}
	file.Decls = declarations
	var out bytes.Buffer
	if err := printer.Fprint(&out, fset, file); err != nil {
		return "", err
	}
	return out.String(), nil
}
