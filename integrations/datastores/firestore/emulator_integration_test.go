//go:build integration && !live

// The emulator leg. It requires a running Firestore emulator and skips LOUDLY
// without one — a silent green here would claim connectivity with nothing
// verified. `make check` never sets the tag, so the hermetic loop stays hermetic.
//
//	docker run --rm -d -p 8080:8080 --name firestore-emulator \
//	  gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators \
//	  gcloud emulators firestore start --host-port=0.0.0.0:8080 --project=gopernicus-test
//	FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 FIRESTORE_PROJECT_ID=gopernicus-test \
//	  go test -tags=integration ./...
//
// The full factory (Reset, the live counterpart, scoped cleanup) lands in C6;
// this file carries only what C1's Open/StatusCheck leg needs.
package firestore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// defaultEmulatorProject matches the project the plan's emulator container runs.
const defaultEmulatorProject = "gopernicus-test"

// TestOpenAgainstEmulator proves the whole C1 lifecycle against a real server:
// eager boot validation under RetryPolicy (which runs StatusCheck for real),
// Emulated() reporting the environment, a standalone StatusCheck, and Close.
func TestOpenAgainstEmulator(t *testing.T) {
	requireEmulator(t)

	ctx := context.Background()
	db, err := firestore.Open(ctx, firestore.Config{
		ProjectID:      emulatorProject(),
		ConnectTimeout: 15 * time.Second,
		Retry:          firestore.RetryPolicy{Attempts: 3, MinBackoff: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("Open against the emulator: %v", err)
	}
	defer db.Close()

	if !db.Emulated() {
		t.Error("Emulated() = false against the emulator")
	}
	if err := firestore.StatusCheck(ctx, db); err != nil {
		t.Errorf("StatusCheck: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestOpenUnreachableFailsEagerly proves Retry.Attempts > 1 is a real boot gate:
// pointed at a dead address, Open FAILS instead of handing back a client whose
// first query dies in a handler.
func TestOpenUnreachableFailsEagerly(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")

	db, err := firestore.Open(context.Background(), firestore.Config{
		ProjectID:      emulatorProject(),
		ConnectTimeout: 2 * time.Second,
		Retry:          firestore.RetryPolicy{Attempts: 2, MinBackoff: 10 * time.Millisecond},
	})
	if err == nil {
		db.Close()
		t.Fatal("Open succeeded against an unreachable emulator; eager boot validation did not run")
	}
}

func requireEmulator(t *testing.T) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set — Firestore emulator conformance NOT verified")
	}
}

func emulatorProject() string {
	if p := os.Getenv("FIRESTORE_PROJECT_ID"); p != "" {
		return p
	}
	return defaultEmulatorProject
}
