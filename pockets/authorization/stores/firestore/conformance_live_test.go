//go:build integration && live

// The LIVE conformance entrypoint (C-D8's second factory, milestone task A6).
// It runs the SAME shared suite as the emulator entrypoint, against a REAL
// Firestore database, and it is the only leg that is release evidence:
//
//   - the emulator enforces NO composite index and keeps no index registry, so
//     an emulator green says nothing about whether the shipped
//     firestore.indexes.json actually covers this store's query matrix. Here the
//     repositories are constructed WITHOUT WithoutIndexProbe, so every one of the
//     ~130 fixtures re-runs the boot probe against the deployed manifest and a
//     gap fails the suite at construction, naming the missing index (ruling R5);
//   - the emulator holds transaction locks for up to thirty seconds and "does
//     not implement all transaction behavior", so its contention results are
//     timing, not serializability.
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
//	  go test -json -count=1 -tags='integration,live' -timeout 30m -run 'Live$' ./...
//
// NOT RUN as of A6: no live GCP project exists for this repository yet. This
// file compiles (go vet -tags='integration,live'), skips loudly unconfigured,
// and fails under FIRESTORE_LIVE_REQUIRED=1 — but nothing in it has executed
// against Firestore, so the index manifest, the probe's live verdict, and the
// suite's live behavior are all UNVERIFIED until the first required dispatch.
package firestore

import (
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/storetest"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// liveAllCollections is the COMPLETE set of collections this store owns, and the
// only documents a live reset may delete. A live database is never emptied
// wholesale (that is what the emulator's clear endpoint is for), so the sweep is
// named collection by collection: a collection missing from this list would
// survive between fixtures and leak state across the suite.
var liveAllCollections = []string{
	collectionRelationships,
	collectionSubjectClaims,
	collectionIDClaims,
	collectionRoles,
	collectionScopes,
	collectionMutations,
}

// newLiveRepos returns the per-fixture factory over ONE live client: each call
// clears this store's six collections and constructs the repository set afresh,
// which is the "FRESH, empty Repositories per call" storetest requires. The
// client is opened once, at the root, rather than per fixture — the suite calls
// this factory well over a hundred times and a client per call would be a
// hundred gRPC connections to prove nothing extra.
//
// The probe is deliberately NOT skipped: R5's claim is that the shipped manifest
// covers this store's queries on a real database, and the only proof of that is
// the probe passing against the deployed indexes. It costs one Admin API
// ListIndexes per fixture; if a live run ever shows Admin quota pressure, hoist
// the probe to a single root-level construction and say so HERE — do not quietly
// pass WithoutIndexProbe, which would delete the proof.
func newLiveRepos(t *testing.T, db *firestoredb.DB) func(*testing.T) authorization.Repositories {
	t.Helper()
	return func(t *testing.T) authorization.Repositories {
		t.Helper()
		firestoretest.ResetLive(t, db, liveAllCollections...)
		repos, err := Repositories(db)
		if err != nil {
			t.Fatalf("Repositories against %s (the index probe runs here — a missing composite fails construction, naming it): %v", db.Target(), err)
		}
		return repos
	}
}

// TestConformanceLive is the full shared suite — every family, including the
// v0.12.0 set reads and the mutation families — against a real Firestore
// database with the manifest deployed. NOTHING here is skipped: R1's ambient
// family (TestRunTransactionalLive) is the one allowed skip of this leg, and it
// is a separate root so this one's verdict is unambiguous.
//
// The client is opened at the ROOT on purpose. firestoretest.OpenLive skips (or,
// when required, fails) the test it is handed, so opening it inside the factory
// would skip the leaves and let this root report PASS with nothing run — a green
// claiming production evidence it never gathered.
func TestConformanceLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	storetest.Run(t, newLiveRepos(t, db))
}

// TestRunTransactionalLive is the ONE allowed skip of the live leg (ruling R1),
// and the audit step in .github/workflows/live-stores.yml allows it BY NAME. The
// store hands the harness no crud.Transactor, because a Firestore transaction
// requires every read to precede every write and never observes its own pending
// writes — the exact property the ambient family proves from both sides. Real
// Firestore does not change that; it is a family difference, so this skip is the
// same on the emulator and live.
func TestRunTransactionalLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	t.Log("firestore: this store supplies NO crud.Transactor — the ambient-transaction family is a KNOWN FAMILY DIFFERENCE (firestore-stores ruling R1), not a defect. It is the only test root this leg is allowed to skip, and live-stores.yml allows it by name.")
	newRepos := newLiveRepos(t, db)
	storetest.RunTransactional(t, func(t *testing.T) (authorization.Repositories, crud.Transactor) {
		return newRepos(t), nil
	})
}

// TestAmbientTransactionRefusedLive is R1's other half on a real database: every
// public port method handed a context carrying a connector transaction fails
// loud instead of running on the client beside the host's transaction. The
// emulator proves the same refusal, but the refusal is the store's side of the
// contract a host reads in the README, so the live leg asserts it too rather
// than assuming the emulator's transaction plumbing behaves like production's.
func TestAmbientTransactionRefusedLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	repos := newLiveRepos(t, db)(t)
	assertAmbientRefusal(t, db, repos)
}
