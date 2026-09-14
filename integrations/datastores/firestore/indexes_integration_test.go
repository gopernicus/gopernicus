//go:build integration && !live

// The C5 emulator leg, which exists to prove ONE thing: that the index probe
// REFUSES an emulator instead of quietly passing.
//
// The emulator keeps no index registry and enforces no composite index, so any
// answer it could give about indexes would be a false green — and the Admin API
// it would have to ask does not route to the emulator at all, so a probe that
// tried anyway would either fail obscurely or reach real GCP with whatever
// ambient credentials the machine has. The refusal is therefore the behavior,
// not a limitation: a store constructor running against the emulator passes
// WithoutIndexProbe(), the explicit opt-out ratified for R5.
//
//	FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 FIRESTORE_PROJECT_ID=gopernicus-test \
//	  go test -tags=integration ./...
package firestore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestProbeIndexesRefusesTheEmulator covers every entry point and every
// manifest shape: valid, empty, and by way of the FS convenience wrapper. The
// refusal must not depend on what the manifest contains — an empty manifest
// takes the "nothing to probe" shortcut on a real database, and that shortcut
// must not become a silent emulator pass.
func TestProbeIndexesRefusesTheEmulator(t *testing.T) {
	ctx := context.Background()
	db := firestoretest.Open(t)

	manifest, err := firestore.ParseIndexManifest(testdataFS, "store_manifest.json")
	if err != nil {
		t.Fatalf("ParseIndexManifest: %v", err)
	}

	cases := map[string]func() error{
		"a real manifest": func() error { return firestore.ProbeIndexes(ctx, db, manifest) },
		"an empty manifest": func() error {
			return firestore.ProbeIndexes(ctx, db, firestore.IndexManifest{})
		},
		"through ProbeIndexesFS": func() error {
			return firestore.ProbeIndexesFS(ctx, db, testdataFS, "store_manifest.json")
		},
	}

	for name, probe := range cases {
		t.Run(name, func(t *testing.T) {
			err := probe()
			if !errors.Is(err, firestore.ErrProbeUnavailableOnEmulator) {
				t.Fatalf("error = %v, want ErrProbeUnavailableOnEmulator", err)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("error = %v, want sdk.ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), "WithoutIndexProbe()") {
				t.Errorf("error = %v, want it to name the opt-out a store constructor passes", err)
			}
			if errors.Is(err, firestore.ErrMissingIndex) {
				t.Errorf("the emulator refusal reads as a missing index: %v", err)
			}
		})
	}
}

// TestProbeIndexesChecksTheEmulatorBeforeAnyRPC pins the ORDER the design
// requires. A context that is already dead cannot carry a network call, so an
// emulator refusal returned under one proves the branch runs before the Admin
// client exists — which is what keeps an emulator run from ever reaching real
// GCP with ambient credentials.
func TestProbeIndexesChecksTheEmulatorBeforeAnyRPC(t *testing.T) {
	db := firestoretest.Open(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := firestore.ProbeIndexes(ctx, db, firestore.IndexManifest{})
	if !errors.Is(err, firestore.ErrProbeUnavailableOnEmulator) {
		t.Fatalf("error = %v, want ErrProbeUnavailableOnEmulator (not a context error from an RPC that should never have been attempted)", err)
	}
}

// TestProbeIndexesFSParsesBeforeItProbes documents the other ordering: the FS
// wrapper reads the manifest first, so a store whose embedded fragment is
// broken learns THAT, on the emulator as much as in production.
func TestProbeIndexesFSParsesBeforeItProbes(t *testing.T) {
	db := firestoretest.Open(t)

	err := firestore.ProbeIndexesFS(context.Background(), db, testdataFS, "no_such_manifest.json")
	if errors.Is(err, firestore.ErrProbeUnavailableOnEmulator) {
		t.Fatalf("a missing manifest was reported as an emulator refusal: %v", err)
	}
	if err == nil {
		t.Fatal("ProbeIndexesFS accepted a manifest that does not exist")
	}
}
