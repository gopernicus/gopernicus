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
// enforces one) and exact transaction/lock timing. Task N5's query matrix and
// the live leg next door (conformance_live_test.go, integration && live, which
// constructs WITH the probe) are the truth for both.
//
// There is NO RunTransactional family in this pocket, so ruling R1 appears here
// only as TestAmbientTransactionRefused.
package firestore

import (
	"os"
	"path/filepath"
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
// the executable form of all eighteen ports' contracts, and the whole suite is
// green as of task N4d (212 leaves, 0 failures, 0 skips).
//
// It was wired at N1, when every port method still answered errNotImplemented
// and it failed wholesale, rather than at the end — so no slice could be
// declared complete without the suite's judgment.
//
// The 45-minute timeout the Makefile and CI carry is not padding. Two hundred
// leaves each reset the emulator database (a DELETE sweep, not a TRUNCATE), and
// the contended families pay the emulator's documented thirty-second lock
// release: ConcurrentDeactivateVersusMint alone runs 20–29 s.
func TestConformance(t *testing.T) {
	storetest.Run(t, newRepos)
}

// TestExportIndexes is the scaffold half of ruling R5, and it needs no emulator:
// it exports this store's embedded manifest into an empty t.TempDir(), which is
// the FIRST thing a host does with this module, before it has a database at all.
// A broken export would otherwise surface as a missing composite index on a live
// deployment — a FAILED_PRECONDITION at request time, far from its cause.
//
// Two properties, both of which the merge (not copy) semantics require: an
// absent destination is CREATED, and a second export of the same fragment is
// byte-identical, so a host re-running its scaffold step gets an empty diff
// rather than a duplicated index list. The manifest's CONTENT — which composites
// it declares and which query each one serves — is task N5's, asserted in
// SCHEMA.md and this module's index tests.
func TestExportIndexes(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "firestore.indexes.json")

	if err := ExportIndexes(dst); err != nil {
		t.Fatalf("ExportIndexes into a directory with no manifest: %v", err)
	}
	first, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ExportIndexes reported success but wrote no manifest: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("ExportIndexes wrote an EMPTY manifest — a host deploying it would deploy no indexes and every composite query would fail live")
	}

	if err := ExportIndexes(dst); err != nil {
		t.Fatalf("re-exporting into the manifest this store just wrote: %v", err)
	}
	second, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read back the re-exported manifest: %v", err)
	}
	if string(second) != string(first) {
		t.Errorf("ExportIndexes is not idempotent — a host's scaffold step would produce a diff on every run:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
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
