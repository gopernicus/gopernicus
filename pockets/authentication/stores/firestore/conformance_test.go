//go:build integration && !live

// Integration tests hit a Firestore EMULATOR. Run with:
//
//	FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 go test -tags=integration -timeout 45m ./...
//
// Absent FIRESTORE_EMULATOR_HOST they skip loudly — a silent green here would
// claim a datastore-family conformance nothing verified. The suite opens the
// emulator's "authentication" database (firestoretest's isolation contract:
// every pocket store train owns its own database, because Reset clears a WHOLE
// database), and passes WithoutIndexProbe because the emulator keeps no index
// registry (R5).
//
// What an emulator green does NOT prove: composite index coverage (nothing here
// enforces one) and exact transaction/lock timing. Task N5's live query matrix
// and the live-project leg (N6) are the truth for both.
//
// There is NO RunTransactional family in this pocket, so ruling R1 appears here
// only as TestAmbientTransactionRefused.
package firestore

import (
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/storetest"
)

// emulatorDatabase is this store train's own emulator database. It is NOT the
// default database, NOT the connector's, and NOT the authorization store's:
// firestoretest.Reset clears the whole selected database, so two suites sharing
// one would delete each other's rows and the failure would arrive as a flake.
const emulatorDatabase = "authentication"

// newRepos opens the emulator database, clears it, and constructs the full
// repository set — a FRESH, empty store per call, which is what every storetest
// family assumes (the suite calls it once per leaf).
func newRepos(t *testing.T) auth.Repositories {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	repos, err := Repositories(db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	return repos
}

// TestConformance runs the shared authentication conformance suite — every port
// group plus the search and concurrency families — against the emulator. It is
// the executable form of all eighteen ports' contracts.
//
// At task N1 it is EXPECTED TO FAIL: every port method answers
// errNotImplemented, and the N1 execution record archives the leaf-failure count
// as the baseline N2–N4 drive to zero. It is wired now, rather than at the end,
// so no slice can be declared complete without the suite's judgment.
func TestConformance(t *testing.T) {
	storetest.Run(t, newRepos)
}

// TestAmbientTransactionRefused is ruling R1: a store method handed a context
// that carries a connector transaction FAILS LOUD rather than running on the
// client beside the host's transaction, which would silently split an atomic
// unit. Every public port method is driven, in both kinds of ambient
// transaction: a read-write Transact and a read-only ReadSnapshot.
func TestAmbientTransactionRefused(t *testing.T) {
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	repos, err := Repositories(db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	assertAmbientRefusal(t, db, repos)
}
