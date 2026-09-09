//go:build integration && live

// The C5 live leg. ProbeIndexes talks to the Firestore ADMIN API, which has no
// emulator at all — the emulator keeps no index registry and the admin client
// does not honor FIRESTORE_EMULATOR_HOST — so this file is the ONLY place the
// probe's real behavior can be observed. Everything the emulator leg can prove
// about it is that it refuses to run there.
//
// What this leg needs from the database it is pointed at:
//
//   - the four composite indexes the C4 live leg already requires on
//     firestore_c4_list_live, READY:
//
//     group ASC, created_at ASC,  id ASC
//     group ASC, created_at DESC, id DESC
//     group ASC, n ASC,           id ASC
//     group ASC, n DESC,          id DESC
//
//   - NO composite index on liveProbeAbsentGroup, which is a collection group
//     nothing else in this module touches. The missing-index case depends on
//     its absence, so a database that has one makes this test fail correctly.
//
//   - a credential with datastore.indexes.list / datastore.indexes.get
//     (roles/datastore.indexAdmin, or roles/datastore.owner as the CI live
//     database provisioning already grants — see the plan's N7).
//
//     FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_PROJECT_ID=<project> \
//     FIRESTORE_LIVE_DATABASE_ID=<run-owned-db> \
//     GOOGLE_APPLICATION_CREDENTIALS=<sa.json> \
//     go test -tags='integration,live' -run 'Live$' -timeout 30m ./...
//
// NOT RUN as of C5: no live GCP project existed in the session that wrote it.
// It compiles (go vet -tags='integration,live'), skips loudly without
// configuration, and fails under FIRESTORE_LIVE_REQUIRED=1. Everything about
// the Admin API calls themselves — the ListIndexes parent path, the GetField
// resource name, the NotFound-means-default branch, the PermissionDenied
// message — is UNVERIFIED until this runs.
package firestore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/sdk"
)

// liveProbeCollection is the collection whose indexes the C4 live leg deploys,
// reused here as the "everything is deployed" case.
const liveProbeCollection = "firestore_c4_list_live"

// liveProbeAbsentGroup names a collection group the live database deliberately
// has no composite index for.
const liveProbeAbsentGroup = "firestore_c5_probe_absent"

// liveProbeTimeout bounds one probe. ListIndexes is a single paginated call, so
// this is generous by an order of magnitude.
const liveProbeTimeout = 2 * time.Minute

// TestProbeIndexesReportsAMissingCompositeLive is the failure the whole probe
// exists to produce: a manifest entry nobody deployed comes back as a
// *MissingIndexError naming the collection group and the exact field tuple, at
// WIRING time, instead of as a FAILED_PRECONDITION on a host's first request.
func TestProbeIndexesReportsAMissingCompositeLive(t *testing.T) {
	ctx, db := liveProbeFixture(t)

	manifest := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{{
		CollectionGroup: liveProbeAbsentGroup,
		QueryScope:      firestore.ScopeCollection,
		Fields: []firestore.IndexField{
			{FieldPath: "never_deployed_a", Order: firestore.OrderAscending},
			{FieldPath: "never_deployed_b", Order: firestore.OrderDescending},
		},
	}}}

	err := firestore.ProbeIndexes(ctx, db, manifest)
	if err == nil {
		t.Fatalf("ProbeIndexes accepted a manifest whose index is not deployed on %s", db.Target())
	}

	var missing *firestore.MissingIndexError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v (%T), want *firestore.MissingIndexError", err, err)
	}
	if !errors.Is(err, firestore.ErrMissingIndex) || !errors.Is(err, sdk.ErrUnavailable) {
		t.Errorf("error = %v, want ErrMissingIndex → sdk.ErrUnavailable", err)
	}
	for _, want := range []string{liveProbeAbsentGroup, "never_deployed_a", "never_deployed_b"} {
		if !strings.Contains(missing.Message, want) {
			t.Errorf("message %q does not name %q", missing.Message, want)
		}
	}
	if missing.URL == "" {
		t.Errorf("no console URL on the gap; an operator has to find the indexes page by hand")
	}
}

// TestProbeIndexesReportsEveryMissingCompositeLive proves the aggregate: two
// undeployed indexes produce one error carrying both, so an operator gets one
// deploy list instead of a sequence of boot failures.
func TestProbeIndexesReportsEveryMissingCompositeLive(t *testing.T) {
	ctx, db := liveProbeFixture(t)

	manifest := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{
		{
			CollectionGroup: liveProbeAbsentGroup,
			QueryScope:      firestore.ScopeCollection,
			Fields: []firestore.IndexField{
				{FieldPath: "first_a", Order: firestore.OrderAscending},
				{FieldPath: "first_b", Order: firestore.OrderAscending},
			},
		},
		{
			CollectionGroup: liveProbeAbsentGroup,
			QueryScope:      firestore.ScopeCollectionGroup,
			Fields: []firestore.IndexField{
				{FieldPath: "second_a", Order: firestore.OrderDescending},
				{FieldPath: "second_b", Order: firestore.OrderDescending},
			},
		},
	}}

	err := firestore.ProbeIndexes(ctx, db, manifest)
	if err == nil {
		t.Fatalf("ProbeIndexes accepted two undeployed indexes on %s", db.Target())
	}
	for _, want := range []string{"first_a", "first_b", "second_a", "second_b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not name %q — only the first gap was reported", err, want)
		}
	}
}

// TestProbeIndexesAcceptsTheDeployedManifestLive is the other half: the four
// composite indexes the C4 live leg requires are deployed and READY, so the
// probe answers nil. If this fails, either the database's indexes are not READY
// (and the C4 live leg is about to fail too) or the probe's identity comparison
// disagrees with what the server reports — the exact drift no emulator can
// catch, since the manifest's implicit __name__ tiebreak is added server-side.
func TestProbeIndexesAcceptsTheDeployedManifestLive(t *testing.T) {
	ctx, db := liveProbeFixture(t)

	ordered := func(field, direction string) firestore.CompositeIndex {
		return firestore.CompositeIndex{
			CollectionGroup: liveProbeCollection,
			QueryScope:      firestore.ScopeCollection,
			Fields: []firestore.IndexField{
				{FieldPath: "group", Order: firestore.OrderAscending},
				{FieldPath: field, Order: direction},
				{FieldPath: "id", Order: direction},
			},
		}
	}
	manifest := firestore.IndexManifest{Indexes: []firestore.CompositeIndex{
		ordered("created_at", firestore.OrderAscending),
		ordered("created_at", firestore.OrderDescending),
		ordered("n", firestore.OrderAscending),
		ordered("n", firestore.OrderDescending),
	}}

	if err := firestore.ProbeIndexes(ctx, db, manifest); err != nil {
		t.Fatalf("ProbeIndexes on the deployed manifest: %v", err)
	}
}

// TestProbeIndexesChecksFieldOverridesLive exercises the single-field half,
// whose branch table (explicit override, inherited configuration, absent Field
// resource) exists only on a real database. A field with no override provides
// the DEFAULT configuration — ascending, descending, and array-contains at
// collection scope — and nothing else, so a collection-group scoped
// requirement on that same field must be reported missing.
func TestProbeIndexesChecksFieldOverridesLive(t *testing.T) {
	ctx, db := liveProbeFixture(t)

	satisfied := firestore.IndexManifest{FieldOverrides: []firestore.FieldOverride{{
		CollectionGroup: liveProbeCollection,
		FieldPath:       "created_at",
		Indexes: []firestore.FieldOverrideIndex{
			{Order: firestore.OrderAscending, QueryScope: firestore.ScopeCollection},
			{Order: firestore.OrderDescending, QueryScope: firestore.ScopeCollection},
		},
	}}}
	if err := firestore.ProbeIndexes(ctx, db, satisfied); err != nil {
		t.Errorf("ProbeIndexes on the default single-field configuration: %v", err)
	}

	demanding := firestore.IndexManifest{FieldOverrides: []firestore.FieldOverride{{
		CollectionGroup: liveProbeAbsentGroup,
		FieldPath:       "never_overridden",
		Indexes: []firestore.FieldOverrideIndex{
			{Order: firestore.OrderAscending, QueryScope: firestore.ScopeCollectionGroup},
		},
	}}}
	err := firestore.ProbeIndexes(ctx, db, demanding)
	if !errors.Is(err, firestore.ErrMissingIndex) {
		t.Fatalf("error = %v, want ErrMissingIndex for a collection-group scoped single-field index nobody configured", err)
	}
	if !strings.Contains(err.Error(), "never_overridden") {
		t.Errorf("error %v does not name the field", err)
	}
}

// TestProbeIndexesShortCircuitsAnEmptyManifestLive pins the documented
// shortcut: a manifest that requires nothing issues no Admin RPC and answers
// nil, so a store with no composite indexes does not need the permission.
func TestProbeIndexesShortCircuitsAnEmptyManifestLive(t *testing.T) {
	ctx, db := liveProbeFixture(t)

	if err := firestore.ProbeIndexes(ctx, db, firestore.IndexManifest{}); err != nil {
		t.Fatalf("ProbeIndexes on an empty manifest: %v", err)
	}
}

// liveProbeFixture opens the live database (skipping loudly when it is not
// configured, failing under FIRESTORE_LIVE_REQUIRED=1) and bounds the probe. It
// writes and deletes NOTHING: reading the index registry needs no fixture data,
// which is why this leg has no ResetLive call.
func liveProbeFixture(t *testing.T) (context.Context, *firestore.DB) {
	t.Helper()

	db := firestoretest.OpenLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveProbeTimeout)
	t.Cleanup(cancel)
	return ctx, db
}
