package firestore

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
const indexManifestCount = 32

// compositeBudget is the no-billing composite-index cap of one database.
const compositeBudget = 200

// singleFieldConfigBudget is the per-database cap on FIELD CONFIGURATIONS — the
// entries a fieldOverride creates. It is a separate 200 from compositeBudget and
// is shared with every other store the host mounts, exactly as the composite
// budget is, which is why the manifest states its share rather than discovering
// it when a deployment starts failing.
const singleFieldConfigBudget = 200

// indexFieldOverrideCount is the number of field overrides
// firestore.indexes.json declares: the single-field DEPENDENCIES this store's
// queries have (indexFieldDependencyCount) plus the EXEMPTIONS that turn
// SCHEMA.md §4.2's "never indexed" row from a claim into a fact
// (indexFieldExemptionCount). See derivedFieldOverrides.
const indexFieldOverrideCount = indexFieldDependencyCount + indexFieldExemptionCount

// indexFieldDependencyCount is the queried half: every field any matrix row
// filters or orders on, on the collection that queries it.
const indexFieldDependencyCount = 36

// indexFieldExemptionCount is the exempted half: every never-queried field whose
// automatic single-field indexes this store DISABLES (exemptFields).
const indexFieldExemptionCount = 17

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
//
// Every collection a matrix row touches has an entry, including the ones whose
// rows carry a single filter: the map is also the list of collections this store
// QUERIES at all, and the ten that answer only point reads (§7.1) are absent
// from it on purpose.
var equalityPrecedence = map[string][]string{
	collectionUsers:           {"created_at", "id"},
	collectionIdentifiers:     {"user_id", "active", "created_at", "id"},
	collectionSessions:        {"user_id", "previous_refresh_token_hash"},
	collectionOAuthAccounts:   {"user_id", "provider", "linked_at", "provider_user_id"},
	collectionServiceAccounts: {"created_at", "id"},
	collectionAPIKeys:         {"service_account_id", "created_at", "id"},
	collectionSecurityEvents:  {"user_id", "event_type", "event_status", "created_at", "id"},
	collectionInvitations:     {"resource_key", "subject_key", "created_at", "id"},
	collectionChallenges:      {"user_id", "purpose", "expires_at", "id"},
	collectionAuthGrants:      {"consume_key", "consumed_at", "session_id", "user_id", "created_at", "id"},
}

// exemptFields are the fields whose AUTOMATIC single-field indexes this store
// DISABLES, with a fieldOverride carrying an empty index set.
//
// SCHEMA.md §4.2 has always had a "never indexed" row. Until now it described an
// intention: Firestore indexes EVERY scalar field of every document by default,
// ascending and descending, so "never indexed" was true of the store's queries
// and false of the database. That gap is not cosmetic on this schema — the
// fields below are the store's secrets and its largest values:
//
//   - Index entries on a secret are a second copy of it, in a structure with its
//     own retention and its own export path, ordered so that a range scan by
//     anyone who can read indexes is a prefix walk over hashes and addresses.
//   - Every write pays for every index entry. security_events is append-only and
//     the highest-volume collection here; api_keys, sessions and challenges are
//     written on every authentication.
//   - Index entries over 1500 bytes TRUNCATE (§4.2), so on the unbounded fields
//     they are not even a correct copy.
//
// The rule for adding one: the field must appear in NO row of queryMatrix, on
// any collection — TestExemptedFieldsAreNeverQueried proves it, and the
// partition test below proves the remaining fields were left indexed
// DELIBERATELY rather than by omission. A field that is ever queried must never
// appear here: an exemption is not a performance hint, it makes the query fail
// with FAILED_PRECONDITION in production while every emulator run stays green.
//
// Two of them earned their place at N7 by CHANGING TYPE: security_events.details
// and invitations.metadata are JSON text now (documents.go), so they are one
// large string each rather than a map whose every subfield was separately
// indexed.
var exemptFields = map[string][]string{
	collectionUsers:           {"display_name"},
	collectionPasswords:       {"hash"},
	collectionIdentifiers:     {"normalized_value"},
	collectionSessions:        {"refresh_token_hash", "authentication_methods"},
	collectionOAuthAccounts:   {"access_token", "refresh_token"},
	collectionOAuthStates:     {"payload"},
	collectionServiceAccounts: {"name"},
	collectionAPIKeys:         {"name", "key_prefix", "key_hash"},
	collectionSecurityEvents:  {"details"},
	collectionInvitations:     {"token_hash", "metadata"},
	collectionChallenges:      {"secret_digest"},
	collectionAuthGrants:      {"methods"},
}

// defaultIndexedFields is the REMAINDER, pinned: every field of a row document
// that this manifest neither declares as a query dependency nor exempts, and
// which therefore keeps Firestore's automatic ascending + descending +
// array-contains single-field indexes.
//
// It is pinned so that adding a field to a document is a decision about its
// indexing rather than a default nobody looked at. A new field lands here, in
// exemptFields, or in a query shape, and TestEveryStoredFieldIsClassified says
// which by name.
//
// Most of these are small, low-cardinality, and plausibly worth a filter a host
// might one day add (status, kinds, flags, the actor columns); a few are
// timestamps that no query orders by alone. None is a secret and none is
// unbounded except by a host's own input.
var defaultIndexedFields = map[string][]string{
	collectionUsers:           {"auth_revision", "email_verified", "primary_email", "status", "status_changed_at", "updated_at"},
	collectionPasswords:       {"user_id"},
	collectionIdentifiers:     {"is_primary", "kind", "login_enabled", "notification_enabled", "recovery_enabled", "replaced_at", "updated_at", "verified_at"},
	collectionSessions:        {"assurance_level", "authenticated_at", "created_at", "expires_at", "id", "previous_used", "rotation_count"},
	collectionOAuthAccounts:   {"account_verified", "provider_email", "provider_email_verified", "scope", "token_expires_at", "token_type"},
	collectionOAuthStates:     {"expires_at", "provider", "purpose", "token"},
	collectionServiceAccounts: {"act_as_user", "created_by", "description", "owner_user_id", "updated_at"},
	collectionAPIKeys:         {"expires_at", "last_used_at", "revoked_at"},
	collectionSecurityEvents:  {"actor_id", "actor_type", "ip_address", "user_agent"},
	collectionInvitations:     {"accepted_at", "auto_accept", "expires_at", "identifier", "identifier_kind", "invited_by", "relation", "resolved_subject_id", "resolved_subject_type", "resource_id", "resource_type", "status", "updated_at"},
	collectionChallenges:      {"attempt_count", "context", "created_at", "protector_key_id", "subject_key", "version"},
	collectionContactChanges:  {"created_at", "expires_at", "id", "kind", "login_enabled", "make_primary", "new_value", "notification_enabled", "recovery_enabled", "replaces_identifier_id", "user_id"},
	collectionAuthGrants:      {"assurance", "authenticated_at", "context_digest", "expires_at", "purpose"},
}

// rowDocuments maps each row collection to the document shape it stores, so the
// classification test can enumerate a collection's stored fields from the struct
// tags rather than from a list that would rot.
var rowDocuments = map[string]any{
	collectionUsers:           userDoc{},
	collectionPasswords:       passwordDoc{},
	collectionIdentifiers:     identifierDoc{},
	collectionSessions:        sessionDoc{},
	collectionOAuthAccounts:   oauthAccountDoc{},
	collectionOAuthStates:     oauthStateDoc{},
	collectionServiceAccounts: serviceAccountDoc{},
	collectionAPIKeys:         apiKeyDoc{},
	collectionSecurityEvents:  securityEventDoc{},
	collectionInvitations:     invitationDoc{},
	collectionChallenges:      challengeDoc{},
	collectionContactChanges:  contactChangeDoc{},
	collectionAuthGrants:      authGrantDoc{},
}

// orderSpec is one ORDER BY clause of a query shape: the field and the direction
// (firestoredb.OrderAscending / OrderDescending).
type orderSpec struct {
	field     string
	direction string
}

// queryShape is one row of this store's COMPLETE query matrix — every query the
// eighteen ports issue, in the vocabulary Firestore's index rules are written
// in. The matrix is the manifest's specification (ruling R5):
// firestore.indexes.json is DERIVED from it by requiredIndex, and
// TestIndexManifestMatchesTheQueryMatrix asserts the derivation both ways, so §7
// of SCHEMA.md is executable rather than prose and a new query cannot ship
// without its index.
//
// Document-addressed reads are NOT rows: they use no index at all, and this
// store answers most of its surface with them — every claim resolution, every
// Get on a derived id, every CAS read (SCHEMA.md §7.1 lists all of them). That
// is why 64 port methods need 32 indexes rather than a matrix per method.
type queryShape struct {
	// name identifies the shape in a failure message and in SCHEMA.md §8.3.
	name string

	// collection is the collection the query runs against.
	collection string

	// equality are the "==" filter fields, in the order the query applies them.
	equality []string

	// in are the "in" filter fields, in query order. Firestore selects the same
	// index for `in` as for "==".
	in []string

	// rangeField is the inequality filter field, which Firestore requires to be
	// the FIRST order field; TestQueryMatrixRangeFieldLeadsTheOrder pins that.
	rangeField string

	// order are the ORDER BY clauses in query order.
	order []orderSpec
}

// queryMatrix is every query shape the store issues. The security-events family
// is generated over its optional-filter subsets and both directions rather than
// transcribed, because an omitted subset is exactly the production
// FAILED_PRECONDITION the manifest exists to prevent: a composite index serves a
// query only when its equality prefix is the query's WHOLE equality set, so each
// optional filter combination is its own index.
//
// Every paged list appears in BOTH directions. The connector's List helper emits
// the requested direction AND, whenever a cursor is present, a reverse probe
// with every clause flipped (list.go markPrev) — Firestore has no automatic
// reverse index, so the flipped spelling is a SECOND index, not a free read of
// the first. A list whose port returns a slice rather than a page has no cursor
// and therefore only one direction; SCHEMA.md §8.2 states which is which.
func queryMatrix() []queryShape {
	both := []string{firestoredb.OrderAscending, firestoredb.OrderDescending}

	shapes := []queryShape{
		// identifiers_doc.go activeIdentifiersQuery — Identifiers.ListByUser and
		// the credential Snapshot that reuses it verbatim. Not paged (the port
		// returns a slice), so it has no reversed direction.
		{
			name:       "active identifiers by user",
			collection: collectionIdentifiers,
			equality:   []string{"user_id", "active"},
			order: []orderSpec{
				{field: "created_at", direction: firestoredb.OrderAscending},
				{field: "id", direction: firestoredb.OrderAscending},
			},
		},
		// sessions_doc.go sessionsForUserQuery — the revocation cascade's
		// population (Sessions.DeleteByUser, the lifecycle transition, the
		// passwordless adoption). One equality, no order.
		{
			name:       "sessions by user",
			collection: collectionSessions,
			equality:   []string{"user_id"},
		},
		// sessions_doc.go sessionByPreviousHashQuery — GetByRefreshHash's GRACE
		// slot, the one refresh lookup with no claim to point-read.
		{
			name:       "session by grace refresh hash",
			collection: collectionSessions,
			equality:   []string{"previous_refresh_token_hash"},
		},
		// oauth_doc.go oauthAccountsForUserQuery — OAuthAccounts.ListByUser and
		// the credential Snapshot's link inventory, in the SQL adapters'
		// `ORDER BY linked_at DESC, provider_user_id DESC`. A slice port: one
		// direction.
		{
			name:       "oauth links by user",
			collection: collectionOAuthAccounts,
			equality:   []string{"user_id"},
			order: []orderSpec{
				{field: "linked_at", direction: firestoredb.OrderDescending},
				{field: "provider_user_id", direction: firestoredb.OrderDescending},
			},
		},
		// oauth_doc.go oauthAccountsForUserProviderQuery — OAuthAccounts.Delete
		// and CredentialMutations.UnlinkOAuth. Two equalities, no order.
		{
			name:       "oauth links by user and provider",
			collection: collectionOAuthAccounts,
			equality:   []string{"user_id", "provider"},
		},
		// challenges_doc.go expiredChallengesQuery — Challenges.PurgeExpired,
		// which runs INSIDE the purge transaction. The port returns a count
		// rather than a page, so one direction.
		{
			name:       "expired challenge purge",
			collection: collectionChallenges,
			rangeField: "expires_at",
			order: []orderSpec{
				{field: "expires_at", direction: firestoredb.OrderAscending},
				{field: "id", direction: firestoredb.OrderAscending},
			},
		},
		// challenges_doc.go challengesForPurposesQuery — the reset/adoption
		// revocation cascade, chunked at the 30-disjunction cap.
		{
			name:       "challenges by user and purposes",
			collection: collectionChallenges,
			equality:   []string{"user_id"},
			in:         []string{"purpose"},
		},
		// grants_doc.go unspentGrantQuery — AuthenticationGrants.Consume. The
		// `consumed_at == null` conjunct is a Firestore IS_NULL filter, which is
		// an equality for index selection.
		{
			name:       "oldest unspent grant",
			collection: collectionAuthGrants,
			equality:   []string{"consume_key", "consumed_at"},
			order: []orderSpec{
				{field: "created_at", direction: firestoredb.OrderAscending},
				{field: "id", direction: firestoredb.OrderAscending},
			},
		},
		// grants_doc.go grantsForSessionQuery — DeleteBySession, which revokes
		// ONE session.
		{
			name:       "grants by session",
			collection: collectionAuthGrants,
			equality:   []string{"session_id"},
		},
		// grants_doc.go grantsForSessionsQuery — the session half of the
		// LIFECYCLE cascade, chunked at the 30-disjunction cap so a subject with
		// many live sessions costs a bounded number of queries inside the
		// revoking transaction rather than one per session. A single-field `in`
		// selects the same index as a single-field `==` ("in and == clauses use
		// the same index"), so it derives no composite — which is a claim this
		// row exists to keep tested rather than assumed.
		{
			name:       "grants by sessions",
			collection: collectionAuthGrants,
			in:         []string{"session_id"},
		},
		// grants_doc.go grantsForUserQuery — the user half of the lifecycle
		// cascade and the passwordless adoption's revocation.
		{
			name:       "grants by user",
			collection: collectionAuthGrants,
			equality:   []string{"user_id"},
		},
	}

	// users_doc.go listUsers — UserAdmin.List, the operator directory. No
	// filters (the port has none), the connector List's (order field, PK) sort,
	// and the PK is the domain `id` FIELD rather than __name__, so both clauses
	// are real index fields.
	for _, dir := range both {
		shapes = append(shapes, queryShape{
			name:       "user directory page " + dir,
			collection: collectionUsers,
			order: []orderSpec{
				{field: "created_at", direction: dir},
				{field: "id", direction: dir},
			},
		})
	}

	// serviceaccounts_doc.go listServiceAccounts — ServiceAccounts.List, the
	// same unfiltered shape over its own collection.
	for _, dir := range both {
		shapes = append(shapes, queryShape{
			name:       "service account page " + dir,
			collection: collectionServiceAccounts,
			order: []orderSpec{
				{field: "created_at", direction: dir},
				{field: "id", direction: dir},
			},
		})
	}

	// apikeys_doc.go listAPIKeys — APIKeys.ListByServiceAccount. req.Search adds
	// NO shape: it is a client-side PostFilter over this same parent-scoped
	// query while page-filling (ruling R4), and the page-fill's resume is a
	// StartAfter on the same ordering.
	for _, dir := range both {
		shapes = append(shapes, queryShape{
			name:       "api keys by service account " + dir,
			collection: collectionAPIKeys,
			equality:   []string{"service_account_id"},
			order: []orderSpec{
				{field: "created_at", direction: dir},
				{field: "id", direction: dir},
			},
		})
	}

	// invitations_doc.go invitationListQuery over its two base queries —
	// ListByResource and ListBySubject, each keyed by one derived equality.
	for _, key := range []string{"resource_key", "subject_key"} {
		for _, dir := range both {
			shapes = append(shapes, queryShape{
				name:       "invitations by " + strings.TrimSuffix(key, "_key") + " " + dir,
				collection: collectionInvitations,
				equality:   []string{key},
				order: []orderSpec{
					{field: "created_at", direction: dir},
					{field: "id", direction: dir},
				},
			})
		}
	}

	// securityevents_doc.go securityEventsQuery — the widest family in the
	// store. Its three equality filters are INDEPENDENTLY optional, so every
	// subset is a legal call and therefore its own index; the Since/Until window
	// constrains created_at, which the order already leads with, so it adds no
	// field and no row (SCHEMA.md §7.3).
	for _, filters := range subsets([]string{"user_id", "event_type", "event_status"}) {
		for _, dir := range both {
			shapes = append(shapes, queryShape{
				name:       "security events" + filterSuffix(filters) + " " + dir,
				collection: collectionSecurityEvents,
				equality:   filters,
				rangeField: "created_at",
				order: []orderSpec{
					{field: "created_at", direction: dir},
					{field: "id", direction: dir},
				},
			})
		}
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
//     default direction therefore contributes no index field. NO row does today
//     — every paged list in this store pins the domain `id` field as its PK — and
//     the rule is kept because the connector's List orders by __name__ for any
//     ListQuery that leaves PK empty (list.go pk), so a future list must not
//     silently acquire an index field it does not need.
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
		// get a composite, `in` or not.
		return true
	default:
		return false
	}
}

// subsets returns every subset of the optional filter fields, in a stable order,
// starting with the empty one. Each subset preserves the argument order, which
// is what keeps the derived equality prefixes agreeing with equalityPrecedence.
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
// Several query shapes derive no composite at all — the two session reads, the
// two grant cascades — and every one of them is served by Firestore's AUTOMATIC
// single-field indexing. Automatic is not the same as guaranteed: a host (or a
// later manifest of this store's own) can disable a field's single-field indexes
// with a fieldOverride, and the query would then fail with FAILED_PRECONDITION
// in production while every emulator run stayed green. The manifest is the
// specification, and ProbeIndexes checks only what the manifest DECLARES
// (connector C5), so an undeclared dependency is an unchecked one.
//
// The rule is uniform rather than minimal: every field any matrix row filters or
// orders on is declared, on the collection that queries it. A field used only
// inside a composite costs nothing to declare and keeps the rule stateable in
// one sentence.
//
// Each dependency entry asks for ONE index — ASCENDING at COLLECTION scope.
// Declaring an override REPLACES the default set (ascending + descending +
// array-contains), which is deliberate here and justified per field class in
// SCHEMA.md §8.4: no document in any queried collection carries an array field,
// and no query sorts a single field descending without an equality prefix
// (every descending order in the matrix belongs to a composite).
//
// The second half is exemptFields: the never-queried secrets and large values
// whose automatic indexes are switched OFF, with an empty index set. The two
// halves are the same mechanism pointed in opposite directions, which is why
// they are derived together and why an overlap between them is a test failure
// rather than a silent winner.
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
	// The EXEMPTIONS. An empty (but present) index set is Firestore's "no
	// single-field indexes for this field", and it is the only way to say it —
	// a field with no override gets ascending, descending and array-contains
	// automatically. ProbeIndexes skips an empty set (there is nothing to
	// verify), so an exemption costs nothing at boot.
	for collection, exempt := range exemptFields {
		for _, field := range exempt {
			out = append(out, firestoredb.FieldOverride{
				CollectionGroup: collection,
				FieldPath:       field,
				Indexes:         []firestoredb.FieldOverrideIndex{},
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
// collection-scoped and belongs to a collection this store queries, and the
// counts are the stated ones.
func TestIndexManifestParses(t *testing.T) {
	m, err := firestoredb.ParseIndexManifest(IndexesFS, IndexesFile)
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}
	if len(m.Indexes) != indexManifestCount {
		t.Errorf("manifest declares %d composite indexes, want %d — update indexManifestCount and SCHEMA.md §8 deliberately", len(m.Indexes), indexManifestCount)
	}
	if len(m.Indexes) > compositeBudget {
		t.Errorf("manifest declares %d composite indexes, over the %d a database allows without billing — and that budget is shared with every other store the host mounts", len(m.Indexes), compositeBudget)
	}
	if len(m.FieldOverrides) != indexFieldOverrideCount {
		t.Errorf("manifest declares %d field overrides, want %d — the single-field indexes this store's queries depend on (SCHEMA.md §8.4)", len(m.FieldOverrides), indexFieldOverrideCount)
	}
	for _, idx := range m.Indexes {
		if idx.QueryScope != firestoredb.ScopeCollection {
			t.Errorf("%s: query scope %s, want %s — every collection here is a top-level one", indexKey(idx), idx.QueryScope, firestoredb.ScopeCollection)
		}
		if _, queried := equalityPrecedence[idx.CollectionGroup]; !queried {
			t.Errorf("%s: %s is not a collection this store queries — the other ten answer point reads only (SCHEMA.md §7.1)", indexKey(idx), idx.CollectionGroup)
		}
	}
	if len(m.FieldOverrides) > singleFieldConfigBudget {
		t.Errorf("manifest declares %d field overrides, over the %d field configurations a database allows — and that budget is shared with every other store the host mounts", len(m.FieldOverrides), singleFieldConfigBudget)
	}
	for _, o := range m.FieldOverrides {
		_, queried := equalityPrecedence[o.CollectionGroup]
		_, exempting := exemptFields[o.CollectionGroup]
		if !queried && !exempting {
			t.Errorf("field override on %s.%s: %s is neither a collection this store queries nor one it exempts a field on", o.CollectionGroup, o.FieldPath, o.CollectionGroup)
		}
		if len(o.Indexes) == 0 && !slices.Contains(exemptFields[o.CollectionGroup], o.FieldPath) {
			t.Errorf("field override on %s.%s declares NO single-field index but is not in exemptFields — an accidental exemption is a production FAILED_PRECONDITION the emulator cannot show", o.CollectionGroup, o.FieldPath)
		}
	}
}

// TestExemptedFieldsAreNeverQueried is the safety rule for exemptFields: an
// exemption switches a field's automatic single-field indexes OFF, so a field
// that any query filters or orders on would start failing with
// FAILED_PRECONDITION in production while every emulator run stayed green (the
// emulator enforces no index at all). The two sets must be disjoint, and the
// check is deliberately GLOBAL as well as per-collection: `previous_refresh_token_hash`
// looks exactly like the other session hash and is the one the grace lookup
// queries.
func TestExemptedFieldsAreNeverQueried(t *testing.T) {
	queried := map[string]bool{}
	queriedAnywhere := map[string]bool{}
	for _, shape := range queryMatrix() {
		fields := slices.Concat(shape.equality, shape.in)
		if shape.rangeField != "" {
			fields = append(fields, shape.rangeField)
		}
		for _, o := range shape.order {
			fields = append(fields, o.field)
		}
		for _, f := range fields {
			queried[shape.collection+"."+f] = true
			queriedAnywhere[f] = true
		}
	}

	total := 0
	for collection, exempt := range exemptFields {
		for _, field := range exempt {
			total++
			if queried[collection+"."+field] {
				t.Errorf("%s.%s is exempted from single-field indexing AND queried by a matrix row — the query would fail with FAILED_PRECONDITION on a real database", collection, field)
			}
			if queriedAnywhere[field] {
				t.Errorf("%s.%s is exempted, and a field named %q is queried on another collection — re-read the exemption before trusting the name", collection, field, field)
			}
		}
	}
	if total != indexFieldExemptionCount {
		t.Errorf("exemptFields declares %d fields, want %d — move indexFieldExemptionCount deliberately", total, indexFieldExemptionCount)
	}
}

// TestEveryStoredFieldIsClassified pins the OTHER half of the exemption
// decision: the fields that keep Firestore's automatic single-field indexes.
//
// Every field of every row document is exactly one of three things — a query
// dependency (declared ASCENDING/COLLECTION), an exemption (declared empty), or
// the default-indexed remainder (declared nowhere). Without this test the third
// bucket is invisible: a new field would silently acquire three index entries on
// every write, and a field DELETED from a document would leave an override
// behind for a field that no longer exists. The remainder is therefore written
// down, and a change to a document shape has to move a name here on purpose.
func TestEveryStoredFieldIsClassified(t *testing.T) {
	dependencies := map[string]bool{}
	for _, o := range derivedFieldOverrides() {
		if len(o.Indexes) > 0 {
			dependencies[o.CollectionGroup+"."+o.FieldPath] = true
		}
	}

	for collection, doc := range rowDocuments {
		stored := firestoreFields(doc)
		var remainder []string
		for _, field := range stored {
			key := collection + "." + field
			exempt := slices.Contains(exemptFields[collection], field)
			switch {
			case dependencies[key] && exempt:
				t.Errorf("%s is both a declared query dependency and an exemption", key)
			case dependencies[key], exempt:
			default:
				remainder = append(remainder, field)
			}
		}
		slices.Sort(remainder)
		want := slices.Clone(defaultIndexedFields[collection])
		slices.Sort(want)
		if !slices.Equal(remainder, want) {
			t.Errorf("%s keeps Firestore's automatic single-field indexes on %v, pinned as %v — classify the difference: query dependency (add the query), exemption (exemptFields), or default (defaultIndexedFields)", collection, remainder, want)
		}
		for _, field := range exemptFields[collection] {
			if !slices.Contains(stored, field) {
				t.Errorf("%s.%s is exempted but is not a field of the stored document — an override for a field that does not exist", collection, field)
			}
		}
	}
	for collection := range exemptFields {
		if _, ok := rowDocuments[collection]; !ok {
			t.Errorf("exemptFields names %s, which is not a row collection", collection)
		}
	}
}

// firestoreFields lists a document shape's stored field names, taken from the
// struct tags so the test reads the SAME names Firestore does.
func firestoreFields(doc any) []string {
	typ := reflect.TypeOf(doc)
	out := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		if tag := typ.Field(i).Tag.Get("firestore"); tag != "" {
			out = append(out, strings.Split(tag, ",")[0])
		}
	}
	return out
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
// half of the scaffold step: a host that already keeps its own
// firestore.indexes.json gets every index this store's queries require, by full
// field identity, after the merge — and keeps its own.
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

	overrides := map[string]bool{}
	for _, o := range merged.FieldOverrides {
		overrides[o.CollectionGroup+"."+o.FieldPath] = true
	}
	for _, o := range derivedFieldOverrides() {
		if !overrides[o.CollectionGroup+"."+o.FieldPath] {
			t.Errorf("the single-field dependency %s.%s did not reach the host's manifest", o.CollectionGroup, o.FieldPath)
		}
	}
}

// TestCompositeFreeShapesDeclareTheirSingleFieldIndexes is the other half of the
// manifest. A query shape that derives NO composite index is served by
// Firestore's automatic single-field indexing — an assumption nothing else in
// this package checks and the probe cannot check, because ProbeIndexes validates
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

	for key, o := range declared {
		if len(o.Indexes) == 0 {
			// An EXEMPTION, whose whole point is that no query uses the field.
			// TestExemptedFieldsAreNeverQueried holds it to the opposite rule.
			continue
		}
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
