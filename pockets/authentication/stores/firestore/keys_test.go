package firestore

import (
	"regexp"
	"strings"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// hexID is the shape every document id in this store must have: 64 lowercase hex
// characters. It is what makes the ids SAFE — no slash, no reserved name, well
// under 1500 bytes, valid UTF-8 — for port input this pocket never bounds.
var hexID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// hostileInputs are port-legal values that would break a raw document id: a
// slash (illegal in a Firestore document id), the two reserved relative names,
// the reserved __.*__ shape, multi-byte and combining Unicode, and a value far
// past the 1500-byte id limit. Every one of them is legal input HERE — an email
// address, a host-minted user id, an opaque token — because the authentication
// ports declare no length or character bound at all.
var hostileInputs = []struct {
	name  string
	value string
}{
	{"slash", "tenant/acme/user-1"},
	{"dot", "."},
	{"dotdot", ".."},
	{"reserved", "__name__"},
	{"unicode", "私書箱@例え.jp"},
	{"combining", "émile@example.com"},
	{"empty", ""},
	{"long", strings.Repeat("a", 4000) + "@example.com"},
	{"newline", "a\nb"},
	{"nul", "a\x00b"},
}

// TestDocumentIDsAreSafeForEveryPortLegalInput drives every id builder with
// every hostile value. The property is not "the hash is right" (the connector
// proves that); it is that NO builder in this store can produce an id Firestore
// would reject or truncate, for input the ports accept.
func TestDocumentIDsAreSafeForEveryPortLegalInput(t *testing.T) {
	for _, in := range hostileInputs {
		t.Run(in.name, func(t *testing.T) {
			v := in.value
			ids := map[string]string{
				"userDocID":                   userDocID(v),
				"passwordDocID":               passwordDocID(v),
				"identifierDocID":             identifierDocID(v),
				"sessionDocID":                sessionDocID(v),
				"oauthAccountDocID":           oauthAccountDocID(v, v),
				"oauthStateDocID":             oauthStateDocID(v),
				"serviceAccountDocID":         serviceAccountDocID(v),
				"apiKeyDocID":                 apiKeyDocID(v),
				"securityEventDocID":          securityEventDocID(v),
				"invitationDocID":             invitationDocID(v),
				"challengeDocID":              challengeDocID(v, v),
				"contactChangeDocID":          contactChangeDocID(v, v),
				"authGrantDocID":              authGrantDocID(v),
				"identifierClaimDocID":        identifierClaimDocID(v, v),
				"identifierPrimaryDocID":      identifierPrimaryDocID(v, v),
				"refreshHashClaimDocID":       refreshHashClaimDocID(v),
				"apiKeyHashClaimDocID":        apiKeyHashClaimDocID(v),
				"invitationTokenClaimDocID":   invitationTokenClaimDocID(v),
				"invitationPendingClaimDocID": invitationPendingClaimDocID(v, v, v, v, v),
				"challengeDigestClaimDocID":   challengeDigestClaimDocID(v, v),
				"invitationResourceKey":       invitationResourceKey(v, v),
				"invitationSubjectKey":        invitationSubjectKey(v, v),
				"grantConsumeKey":             grantConsumeKey(v, v, v),
			}
			for name, id := range ids {
				if !hexID.MatchString(id) {
					t.Errorf("%s(%q) = %q, want 64 lowercase hex characters", name, v, id)
				}
			}
		})
	}
}

// TestMultiPartKeysAreUnambiguous is the length-prefixing property the connector
// guarantees, asserted where this store DEPENDS on it: every multi-part id here
// is a natural key whose components are arbitrary text, so a naive
// concatenation would let ("ab","c") and ("a","bc") collide — two different
// identifiers, one claim document, a uniqueness rule silently gone.
func TestMultiPartKeysAreUnambiguous(t *testing.T) {
	pairs := []struct {
		name string
		a, b string
	}{
		{"identifierClaimDocID", identifierClaimDocID("ab", "c"), identifierClaimDocID("a", "bc")},
		{"identifierPrimaryDocID", identifierPrimaryDocID("ab", "c"), identifierPrimaryDocID("a", "bc")},
		{"challengeDocID", challengeDocID("ab", "c"), challengeDocID("a", "bc")},
		{"contactChangeDocID", contactChangeDocID("ab", "c"), contactChangeDocID("a", "bc")},
		{"oauthAccountDocID", oauthAccountDocID("ab", "c"), oauthAccountDocID("a", "bc")},
		{"challengeDigestClaimDocID", challengeDigestClaimDocID("ab", "c"), challengeDigestClaimDocID("a", "bc")},
		{"invitationResourceKey", invitationResourceKey("ab", "c"), invitationResourceKey("a", "bc")},
		{"invitationSubjectKey", invitationSubjectKey("ab", "c"), invitationSubjectKey("a", "bc")},
		{"grantConsumeKey", grantConsumeKey("ab", "c", ""), grantConsumeKey("a", "bc", "")},
		{"invitationPendingClaimDocID",
			invitationPendingClaimDocID("ab", "c", "d", "e", "f"),
			invitationPendingClaimDocID("a", "bc", "d", "e", "f")},
	}
	for _, p := range pairs {
		if p.a == p.b {
			t.Errorf("%s: two different component tuples produced the same id %q", p.name, p.a)
		}
	}
}

// TestDocumentIDsAreTheConnectorKeyHash pins the construction: each id is
// KeyHash over the SQL key's components IN THE SQL'S OWN COLUMN ORDER. A
// reordering would be invisible in every other test — the ids would still be
// well-formed and still unique — and would silently make this store's claim
// documents unreadable by any future tool that recomputes them.
func TestDocumentIDsAreTheConnectorKeyHash(t *testing.T) {
	cases := []struct {
		name  string
		got   string
		parts []string
	}{
		{"userDocID", userDocID("u1"), []string{"u1"}},
		{"oauthAccountDocID", oauthAccountDocID("google", "p1"), []string{"google", "p1"}},
		{"challengeDocID", challengeDocID("subj", "login"), []string{"subj", "login"}},
		{"contactChangeDocID", contactChangeDocID("u1", "email"), []string{"u1", "email"}},
		{"identifierClaimDocID", identifierClaimDocID("email", "a@b.example"), []string{"email", "a@b.example"}},
		{"identifierPrimaryDocID", identifierPrimaryDocID("u1", "email"), []string{"u1", "email"}},
		{"challengeDigestClaimDocID", challengeDigestClaimDocID("login", "d1"), []string{"login", "d1"}},
		{"invitationPendingClaimDocID",
			invitationPendingClaimDocID("project", "p1", "email", "a@b.example", "editor"),
			[]string{"project", "p1", "email", "a@b.example", "editor"}},
		{"grantConsumeKey", grantConsumeKey("s1", "reauth", "ctx"), []string{"s1", "reauth", "ctx"}},
	}
	for _, c := range cases {
		if want := firestoredb.KeyHash(c.parts...); c.got != want {
			t.Errorf("%s = %q, want KeyHash(%q) = %q", c.name, c.got, c.parts, want)
		}
	}
}

// TestCollectionNamesMirrorTheSQLTables keeps the milestone's naming convention
// honest for the thirteen collections that HAVE a SQL table, and states the
// seven that deliberately do not: a claim collection reproduces an INDEX, so it
// is named for the constraint it enforces.
func TestCollectionNamesMirrorTheSQLTables(t *testing.T) {
	tables := []string{
		collectionUsers, collectionPasswords, collectionIdentifiers, collectionSessions,
		collectionOAuthAccounts, collectionOAuthStates, collectionServiceAccounts,
		collectionAPIKeys, collectionSecurityEvents, collectionInvitations,
		collectionChallenges, collectionContactChanges, collectionAuthGrants,
	}
	want := map[string]bool{
		"users": true, "user_passwords": true, "user_identifiers": true, "sessions": true,
		"oauth_accounts": true, "oauth_states": true, "service_accounts": true,
		"api_keys": true, "security_events": true, "invitations": true,
		"challenges": true, "contact_changes": true, "authentication_grants": true,
	}
	if len(tables) != len(want) {
		t.Fatalf("the store declares %d table-backed collections, the turso migrations create %d", len(tables), len(want))
	}
	for _, name := range tables {
		if !want[name] {
			t.Errorf("collection %q has no matching table in stores/turso/migrations", name)
		}
	}

	claims := []string{
		collectionIdentifierClaims, collectionIdentifierPrimaries, collectionRefreshHashClaims,
		collectionAPIKeyHashClaims, collectionInvitationTokens, collectionInvitationPending,
		collectionChallengeDigests,
	}
	seen := map[string]bool{}
	for _, name := range append(append([]string{}, tables...), claims...) {
		if seen[name] {
			t.Errorf("collection %q is declared twice — two collections sharing a name share their documents", name)
		}
		seen[name] = true
	}
	if len(claims) != 7 {
		t.Errorf("the store declares %d claim collections, SCHEMA.md §5 pins 7", len(claims))
	}
}
