//go:build integration && live

// The A5 live leg: the ONLY place this store's index manifest can be proven.
//
// The emulator enforces no composite index, so an emulator-green conformance run
// says nothing about production's FAILED_PRECONDITION. Two assertions live here,
// and together they are ruling R5's "shipped, exported, probed, and proven live":
//
//   - the boot probe accepts the DEPLOYED manifest (the Admin API agrees the
//     indexes exist and are READY), and the constructor therefore succeeds
//     without WithoutIndexProbe;
//   - every row of the query matrix EXECUTES — the manifest is not merely
//     deployed, it is the right set. A missing composite is a
//     *firestoredb.MissingIndexError from the server, on an empty collection,
//     before any document is read.
//
// The CI job deploys this module's manifest before running this leg:
//
//	FIRESTORE_LIVE_INDEXES=pockets/authorization/stores/firestore/firestore.indexes.json
//
// (one `gcloud firestore indexes composite create` per entry, then the READY
// wait — the connector README's deploy recipe; A6 wires the workflow step).
//
// NOT RUN as of A5: no live GCP project existed in the session that wrote it. It
// compiles (go vet -tags='integration,live'), skips loudly without
// configuration, and fails under FIRESTORE_LIVE_REQUIRED=1. Until it runs, the
// manifest is derived-and-reviewed but UNPROVEN:
//
//	FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_REQUIRED=1 \
//	FIRESTORE_LIVE_PROJECT_ID=<project> FIRESTORE_LIVE_DATABASE_ID=<run-owned-db> \
//	GOOGLE_APPLICATION_CREDENTIALS=<sa.json> \
//	go test -tags='integration,live' -run 'Live$' -timeout 30m ./...
package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"google.golang.org/api/iterator"
)

// liveIndexTimeout bounds the whole matrix run. Each row is one Limit(1) query
// against a collection the probe values match nothing in, so the wall clock is
// round trips, not scanning.
const liveIndexTimeout = 5 * time.Minute

// TestIndexProbeAcceptsTheDeployedManifestLive proves the deployment: the Admin
// API reports every composite index this store declares, READY, so a host's boot
// probe passes and the constructor needs no opt-out. A failure here means either
// the manifest was not deployed to this database or an index is still CREATING —
// the error names which.
func TestIndexProbeAcceptsTheDeployedManifestLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveIndexTimeout)
	defer cancel()

	if err := firestoredb.ProbeIndexesFS(ctx, db, IndexesFS, IndexesFile); err != nil {
		t.Fatalf("ProbeIndexesFS on %s: %v", db.Target(), err)
	}

	// The constructor's own path — no WithoutIndexProbe, which is what a real
	// host wires.
	if _, err := Repositories(t.Context(), db); err != nil {
		t.Errorf("Repositories(t.Context(), db) with the probe enabled: %v", err)
	}
	if _, err := RelationshipRepository(t.Context(), db); err != nil {
		t.Errorf("RelationshipRepository(t.Context(), db) with the probe enabled: %v", err)
	}
}

// TestQueryMatrixExecutesAgainstTheDeployedIndexesLive runs EVERY row of the
// query matrix against the live database. This is the assertion the probe cannot
// make: the probe proves the manifest was deployed, and this proves the manifest
// is the right set — a shape whose index is missing (or whose field order or
// direction is wrong) comes back as a *firestoredb.MissingIndexError instead of
// an empty page.
//
// The filter values match no document on purpose. Firestore selects the index
// before it reads anything, so an unindexed shape fails on an empty collection
// exactly as it would on a full one, and the leg needs no fixture and writes
// nothing.
func TestQueryMatrixExecutesAgainstTheDeployedIndexesLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveIndexTimeout)
	defer cancel()

	r := db.ReaderFrom(ctx)
	for _, s := range queryMatrix() {
		t.Run(s.name, func(t *testing.T) {
			if err := drain(ctx, r, s.query(db)); err != nil {
				var missing *firestoredb.MissingIndexError
				if errors.As(err, &missing) {
					t.Fatalf("query shape %q has no usable index: %s\ndeploy: %s\nexpected index: %s",
						s.name, missing.Message, missing.URL, describeRequirement(s))
				}
				t.Fatalf("query shape %q: %v", s.name, err)
			}
		})
	}
}

// query builds one matrix row as a real query: the equality filters, the `in`
// filters (three values, so the disjunction is genuine), the cursor inequality,
// and the sort clauses, bounded to one document.
func (s queryShape) query(db *firestoredb.DB) gcfs.Query {
	q := db.Collection(s.collection).Query
	for _, f := range s.equality {
		q = q.Where(f, "==", probeValue)
	}
	for _, f := range s.in {
		q = q.Where(f, "in", probeValues)
	}
	if s.rangeField != "" {
		q = q.Where(s.rangeField, ">", probeValue)
	}
	for _, o := range s.order {
		q = q.OrderBy(o.field, direction(o.direction))
	}
	return q.Limit(1)
}

// direction converts the manifest's vocabulary to the vendor's.
func direction(order string) gcfs.Direction {
	if order == firestoredb.OrderDescending {
		return gcfs.Desc
	}
	return gcfs.Asc
}

// drain runs a query to completion, mapping the server's error the way every
// store read does — a missing index arrives at Next, not at Documents.
func drain(ctx context.Context, r firestoredb.Reader, q gcfs.Query) error {
	it := r.Documents(ctx, q)
	defer it.Stop()

	for {
		_, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return firestoredb.MapError(err)
		}
	}
}

// describeRequirement renders the index the matrix says a shape needs, so a
// failure hands the operator the entry to deploy rather than a field list to
// reconstruct.
func describeRequirement(s queryShape) string {
	idx, ok := requiredIndex(s)
	if !ok {
		return "none (this shape is served by automatic single-field indexes — the derivation rule in indexes_test.go is what disagrees with the server)"
	}
	return indexKey(idx)
}
