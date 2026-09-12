//go:build integration && live

// The LIVE conformance entrypoint (C-D8's second factory, milestone task N6).
// It runs the SAME shared suite as the emulator entrypoint, against a REAL
// Firestore database, and it is the only leg that is release evidence:
//
//   - the emulator enforces NO composite index and keeps no index registry, so
//     an emulator green says nothing about whether the shipped
//     firestore.indexes.json actually covers this store's query matrix. Here ONE
//     probe-enabled construction runs per package (probeLiveOnce), so a gap in
//     the deployed manifest fails the suite at construction, naming the missing
//     index (ruling R5);
//   - the emulator holds transaction locks for up to thirty seconds and "does
//     not implement all transaction behavior", so its results for this pocket's
//     contended families — the deactivate-versus-mint fence, the concurrent
//     grant consume, the concurrent redemption, the purge/Replace handshake —
//     are timing, not serializability.
//
// Configuration is firestoretest's: FIRESTORE_LIVE_PROJECT_ID +
// FIRESTORE_LIVE_DATABASE_ID (a disposable, run-owned database — never
// "(default)") with Application Default Credentials and an EMPTY
// FIRESTORE_EMULATOR_HOST. Unconfigured, every root here SKIPS LOUDLY;
// FIRESTORE_LIVE_REQUIRED=1 turns that skip into the release-gate failure.
//
//	FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_REQUIRED=1 \
//	  FIRESTORE_LIVE_PROJECT_ID='<test-project>' FIRESTORE_LIVE_DATABASE_ID='<run-owned-db>' \
//	  GOOGLE_APPLICATION_CREDENTIALS='<sa.json>' \
//	  go test -json -count=1 -tags='integration,live' -timeout 45m -run 'Live$' ./...
//
// This pocket has NO RunTransactional family, so unlike the authorization
// store's live leg this one has NO allowed skip: both roots below must PASS on
// a required run, and .github/workflows/live-stores.yml carries an EMPTY
// allow-list for this module to say so mechanically.
//
// NOT RUN as of N6: no live GCP project exists for this repository yet. This
// file compiles (go vet -tags='integration,live'), skips loudly unconfigured,
// and fails under FIRESTORE_LIVE_REQUIRED=1 — but nothing in it has executed
// against Firestore, so the index manifest, the probe's live verdict, and the
// suite's live behavior are all UNVERIFIED until the first required dispatch.
package firestore

import (
	"sync"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
)

// liveAllCollections is the COMPLETE set of collections this store owns — the
// thirteen row collections mirroring the SQL tables and the seven CLAIM
// collections that stand in for its unique indexes (SCHEMA.md §5) — and the only
// documents a live reset may delete. A live database is never emptied wholesale
// (that is what the emulator's clear endpoint is for), so the sweep is named
// collection by collection: a collection missing from this list would survive
// between fixtures and leak state across the suite. A LEAKED CLAIM is the worst
// of those leaks, because it does not look like leftover data — it looks like a
// duplicate-detection bug in the store.
var liveAllCollections = []string{
	collectionUsers,
	collectionPasswords,
	collectionIdentifiers,
	collectionSessions,
	collectionOAuthAccounts,
	collectionOAuthStates,
	collectionServiceAccounts,
	collectionAPIKeys,
	collectionSecurityEvents,
	collectionInvitations,
	collectionChallenges,
	collectionContactChanges,
	collectionAuthGrants,

	collectionIdentifierClaims,
	collectionIdentifierPrimaries,
	collectionRefreshHashClaims,
	collectionAPIKeyHashClaims,
	collectionInvitationTokens,
	collectionInvitationPending,
	collectionChallengeDigests,
}

// liveProbeOnce memoizes the ONE probe-enabled construction this package makes,
// and liveProbeErr carries its verdict to every root that asks for it.
var (
	liveProbeOnce sync.Once
	liveProbeErr  error
)

// probeLiveOnce runs the boot index probe against the deployed manifest EXACTLY
// ONCE per package, and fails whichever root asks after a failure.
//
// The probe is never skipped here — R5's claim is that the shipped manifest
// covers this store's queries on a real database, and the only proof of that is
// a probe passing against the deployed indexes. But one construction proves it
// exactly as well as two hundred do, and two hundred cost two hundred Admin API
// ListIndexes plus two hundred GetField per declared field override against a
// shared project's Admin quota. So the proof is hoisted here and the fixtures
// below pass WithoutIndexProbe() EXPLICITLY, which is a stated exemption rather
// than a quiet one; TestIndexProbeAcceptsTheDeployedManifestLive asserts the
// probe's verdict on its own besides.
func probeLiveOnce(t *testing.T, db *firestoredb.DB) {
	t.Helper()
	liveProbeOnce.Do(func() {
		_, liveProbeErr = Repositories(t.Context(), db)
	})
	if liveProbeErr != nil {
		t.Fatalf("Repositories against %s with the index probe ENABLED (ruling R5 — a missing composite or an undeployed field override fails construction, naming it): %v", db.Target(), liveProbeErr)
	}
}

// newLiveRepos returns the per-fixture factory over ONE live client: each call
// clears this store's twenty collections and constructs the repository set
// afresh, which is the "FRESH, empty Repositories per call" storetest requires.
// The client is opened once, at the root, rather than per fixture — the suite
// calls this factory once per leaf and a client per call would be a couple of
// hundred gRPC connections to prove nothing extra. The probe is hoisted for the
// same reason (probeLiveOnce, above); the per-fixture constructions skip it.
func newLiveRepos(t *testing.T, db *firestoredb.DB) func(*testing.T) auth.Repositories {
	t.Helper()
	probeLiveOnce(t, db)
	return func(t *testing.T) auth.Repositories {
		t.Helper()
		firestoretest.ResetLive(t, db, liveAllCollections...)
		repos, err := Repositories(t.Context(), db, WithoutIndexProbe())
		if err != nil {
			t.Fatalf("Repositories against %s: %v", db.Target(), err)
		}
		return repos
	}
}

// TestConformanceLive is the full shared suite — all eighteen ports, the search
// group (R4) and the concurrency family — against a real Firestore database with
// the manifest deployed and the package's one probe-enabled construction behind
// it. NOTHING here is skipped: this pocket has no ambient family, so there is no
// R1 exception to carve out and no allowed skip at all.
//
// The client is opened at the ROOT on purpose. firestoretest.OpenLive skips (or,
// when required, fails) the test it is handed, so opening it inside the factory
// would skip the leaves and let this root report PASS with nothing run — a green
// claiming production evidence it never gathered.
func TestConformanceLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	storetest.Run(t, newLiveRepos(t, db))
}

// TestAmbientTransactionRefusedLive is ruling R1 on a real database: each of the
// fifty-eight public port methods handed a context carrying a connector
// transaction fails loud instead of running on the client beside the host's
// transaction. The emulator proves the same refusal, but the refusal is the
// store's side of the contract a host reads in the README, so the live leg
// asserts it too rather than assuming the emulator's transaction plumbing
// behaves like production's.
func TestAmbientTransactionRefusedLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	repos := newLiveRepos(t, db)(t)
	assertAmbientRefusal(t, db, repos)
}
