//go:build integration && !live

// The emulator's half of the index story, which is a small half ON PURPOSE: the
// emulator keeps no index registry and enforces no composite index, so nothing
// here can prove an index is right. What it CAN prove is that the constructor
// refuses to pretend otherwise — the probe fails loudly against an emulator
// instead of answering a false green, and the opt-out every conformance run
// passes is the only way through.
//
// The manifest's correctness is proven by indexes_test.go (matrix ↔ manifest,
// hermetic) and by indexes_live_test.go (every matrix row executed against a
// real database with the manifest deployed).
package firestore

import (
	"errors"
	"reflect"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestConstructorProbesTheIndexManifest is ruling R5's wiring: a construction
// that does NOT opt out probes, and against the emulator that probe is
// ErrProbeUnavailableOnEmulator rather than a silent skip.
func TestConstructorProbesTheIndexManifest(t *testing.T) {
	db := firestoretest.OpenDatabase(t, emulatorDatabase)

	if _, err := Repositories(t.Context(), db); !errors.Is(err, firestoredb.ErrProbeUnavailableOnEmulator) {
		t.Errorf("Repositories(t.Context(), db) = %v, want ErrProbeUnavailableOnEmulator — a store that skipped the probe here would report a green nothing verified", err)
	}

	// The refusal is a wiring error, not a retryable outage: it names the option
	// that fixes it, and no amount of waiting deploys an index registry.
	if _, err := Repositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("Repositories(t.Context(), db) = %v, want it to wrap sdk.ErrInvalidInput", err)
	}
}

// TestConstructorSkipsTheProbeWhenAskedTo is the other side: WithoutIndexProbe
// is what every conformance run passes, and it must construct ALL EIGHTEEN
// slots against a database with no index registry at all.
func TestConstructorSkipsTheProbeWhenAskedTo(t *testing.T) {
	db := firestoretest.OpenDatabase(t, emulatorDatabase)

	repos, err := Repositories(t.Context(), db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories(t.Context(), db, WithoutIndexProbe()): %v", err)
	}
	assertEveryRepositorySlotWired(t, repos)
}

// assertEveryRepositorySlotWired fails on any nil slot of auth.Repositories. It
// reflects over the struct rather than listing eighteen names, so a slot ADDED
// to the pocket's set is caught here instead of arriving as a nil-interface
// panic in a host's handler.
func assertEveryRepositorySlotWired(t *testing.T, repos auth.Repositories) {
	t.Helper()

	v := reflect.ValueOf(repos)
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if !f.IsExported() || v.Field(i).Kind() != reflect.Interface {
			continue
		}
		if v.Field(i).IsNil() {
			t.Errorf("auth.Repositories.%s is nil — this store wires all eighteen slots unconditionally (N-D4)", f.Name)
		}
	}
}
