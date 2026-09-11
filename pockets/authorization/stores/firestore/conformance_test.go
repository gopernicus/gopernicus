//go:build integration && !live

// Integration tests hit a Firestore EMULATOR. Run with:
//
//	FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 go test -tags=integration -timeout 30m ./...
//
// Absent FIRESTORE_EMULATOR_HOST they skip loudly — a silent green here would
// claim a datastore-family conformance nothing verified. The suite opens the
// emulator's "authorization" database (firestoretest's isolation contract: every
// pocket store train owns its own database, because Reset clears a WHOLE
// database), and passes WithoutIndexProbe because the emulator keeps no index
// registry (R5).
//
// What an emulator green does NOT prove: composite index coverage (nothing here
// enforces one) and exact transaction/lock timing. Task A5's live query matrix
// and the live-project leg are the truth for both.
package firestore

import (
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
)

// emulatorDatabase is this store train's own emulator database. It is NOT the
// default database and NOT the connector's: firestoretest.Reset clears the whole
// selected database, so two suites sharing one would delete each other's rows
// and the failure would arrive as a flake.
const emulatorDatabase = "authorization"

// newRepos opens the emulator database, clears it, and constructs the full
// repository set — a FRESH, empty store per call, which is what every storetest
// family assumes.
func newRepos(t *testing.T) authorization.Repositories {
	return newReposWithPolicy(t, mutations.GuardianPolicy{})
}

func newReposWithPolicy(t *testing.T, policy mutations.GuardianPolicy) authorization.Repositories {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	repos, err := Repositories(t.Context(), db, WithoutIndexProbe(), WithGuardianPolicy(policy))
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	return repos
}

// TestConformance runs the shared authorization conformance suite (both kinds,
// the adversarial/budget/keyset/set-read families, and the mutation families)
// against the emulator. It is the executable form of all three ports' contracts.
func TestConformance(t *testing.T) {
	storetest.Run(t, newReposWithPolicy)
}

// TestRunTransactional is ruling R1's loud skip. The Firestore store hands the
// harness NO transaction.Transactor, because a Firestore transaction requires every
// read to precede every write and never observes its own pending writes — the
// exact property the ambient family proves from both sides. The harness's
// existing nil-transactor path (the memstore takes it too) skips the family
// loudly rather than reporting a green it did not earn.
func TestRunTransactional(t *testing.T) {
	// The harness owns the SKIP line ("no transaction.Transactor supplied — ambient-
	// transaction family NOT verified"); this log is what names the family, the
	// store, and the ruling right above it, so a log reader never has to guess
	// whose skip it is or whether it is a defect.
	t.Log("firestore: this store supplies NO transaction.Transactor — the ambient-transaction family is a KNOWN FAMILY DIFFERENCE (firestore-stores ruling R1), not a defect and not a gap to be fixed by a wrapper. See README.md, first section.")
	storetest.RunTransactional(t, func(t *testing.T) (authorization.Repositories, transaction.Transactor) {
		return newRepos(t), nil
	})
}

// TestAmbientTransactionRefused is R1's other half: a store method handed a
// context that carries a connector transaction FAILS LOUD rather than running on
// the client beside the host's transaction, which would silently split an atomic
// unit. Every public port method is driven, in both kinds of ambient
// transaction: a read-write Transact and a read-only ReadSnapshot.
func TestAmbientTransactionRefused(t *testing.T) {
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	repos, err := Repositories(t.Context(), db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	assertAmbientRefusal(t, db, repos)
}

func TestAuditConformance(t *testing.T) {
	storetest.RunAudit(t, func(t *testing.T, enabled bool) authorization.Repositories {
		db := firestoretest.OpenDatabase(t, emulatorDatabase)
		firestoretest.Reset(t, db)
		opts := []Option{WithoutIndexProbe()}
		if enabled {
			opts = append(opts, WithAudit())
		}
		repos, err := Repositories(t.Context(), db, opts...)
		if err != nil {
			t.Fatal(err)
		}
		return repos
	})
}
