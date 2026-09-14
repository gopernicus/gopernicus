//go:build integration && live

// The N5 live leg: the ONLY place this store's index manifest can be proven.
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
//	FIRESTORE_LIVE_INDEXES=pockets/authentication/stores/firestore/firestore.indexes.json
//
// (one `gcloud firestore indexes composite create` per entry, then the READY
// wait — the connector README's deploy recipe; N6 wires the workflow step).
//
// NOT RUN as of N5: no live GCP project existed in the session that wrote it. It
// compiles (go vet -tags='integration,live'), skips loudly without
// configuration, and fails under FIRESTORE_LIVE_REQUIRED=1. Until it runs, the
// manifest is derived-and-reviewed but UNPROVEN:
//
//	FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_REQUIRED=1 \
//	FIRESTORE_LIVE_PROJECT_ID=<project> FIRESTORE_LIVE_DATABASE_ID=<run-owned-db> \
//	GOOGLE_APPLICATION_CREDENTIALS=<sa.json> \
//	go test -tags='integration,live' -run 'Index|Matrix' -timeout 30m ./...
package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
)

// liveIndexTimeout bounds the whole matrix run. Each row is one Limit(1) query
// against values that match nothing, so the wall clock is round trips (32
// composites plus the composite-free rows), not scanning.
const liveIndexTimeout = 5 * time.Minute

// probeTime is the timestamp-typed probe value. Firestore compares ACROSS types
// by type order, so a string probe against created_at would still be a legal
// query — but it would compare in the wrong type's range and read like a
// mistake. Each field is probed with a value of its own type instead; none of
// them matches a real document.
var probeTime = time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC)

// TestIndexProbeAcceptsTheDeployedManifestLive proves the deployment: the Admin
// API reports every composite index and every single-field override this store
// declares, READY, so a host's boot probe passes and the constructor needs no
// opt-out. A failure here means either the manifest was not deployed to this
// database or an index is still CREATING — the error names which.
func TestIndexProbeAcceptsTheDeployedManifestLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveIndexTimeout)
	defer cancel()

	if err := firestoredb.ProbeIndexesFS(ctx, db, IndexesFS, IndexesFile); err != nil {
		t.Fatalf("ProbeIndexesFS on %s: %v", db.Target(), err)
	}

	// The constructor's own path — no WithoutIndexProbe, which is what a real
	// host wires.
	if _, err := Repositories(db); err != nil {
		t.Errorf("Repositories(db) with the probe enabled: %v", err)
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
			if err := drainMatrixQuery(ctx, r, s.query(db)); err != nil {
				var missing *firestoredb.MissingIndexError
				if errors.As(err, &missing) {
					t.Fatalf("query shape %q has no usable index: %s\ndeploy: %s\nexpected index: %s",
						s.name, missing.Message, missing.URL, describeIndexRequirement(s))
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
		q = q.Where(f, "==", matrixProbeValue(f))
	}
	for _, f := range s.in {
		q = q.Where(f, "in", probeValues)
	}
	if s.rangeField != "" {
		q = q.Where(s.rangeField, ">", matrixProbeValue(s.rangeField))
	}
	for _, o := range s.order {
		q = q.OrderBy(o.field, indexDirection(o.direction))
	}
	return q.Limit(1)
}

// matrixProbeValue is the value one field is filtered with — typed as the field
// is stored, and matching nothing. consumed_at is the exception that carries
// meaning: the grant query's conjunct is literally `consumed_at == null`
// (SQL's `IS NULL`), so nil IS the shape, not a placeholder.
func matrixProbeValue(field string) any {
	switch field {
	case "created_at", "linked_at", "expires_at":
		return probeTime
	case "consumed_at":
		return nil
	case "active":
		return true
	default:
		return probeValue
	}
}

// indexDirection converts the manifest's vocabulary to the vendor's.
func indexDirection(order string) gcfs.Direction {
	if order == firestoredb.OrderDescending {
		return gcfs.Desc
	}
	return gcfs.Asc
}

// drainMatrixQuery runs a query to completion, mapping the server's error the
// way every store read does — a missing index arrives at Next, not at Documents.
func drainMatrixQuery(ctx context.Context, r firestoredb.Reader, q gcfs.Query) error {
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

// describeIndexRequirement renders the index the matrix says a shape needs, so a
// failure hands the operator the entry to deploy rather than a field list to
// reconstruct.
func describeIndexRequirement(s queryShape) string {
	idx, ok := requiredIndex(s)
	if !ok {
		return "none (this shape is served by automatic single-field indexes — the derivation rule in indexes_test.go is what disagrees with the server)"
	}
	return indexKey(idx)
}
