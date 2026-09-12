//go:build integration && live

// The LIVE conformance entrypoint (C-D8's second factory, milestone task A6).
// It runs the SAME shared suite as the emulator entrypoint, against a REAL
// Firestore database, and it is the only leg that is release evidence:
//
//   - the emulator enforces NO composite index and has no index registry. A
//     probe-enabled construction checks both deployed manifests once per package;
//     per-fixture constructors explicitly skip that already-completed check;
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
	"context"
	"sync"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/capabilities/transaction"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// liveAllCollections is the COMPLETE set of collections this store owns, and the
// only documents a live reset may delete. A live database is never emptied
// wholesale (that is what the emulator's clear endpoint is for), so the sweep is
// named collection by collection: a collection missing from this list would
// survive between fixtures and leak state across the suite.
var liveAllCollections = []string{
	collectionRelationships,
	collectionSubjectClaims,
	collectionRoles,
	collectionAudit,
}

// The live target is fixed for this test process. Probe both manifests once;
// fixture resets clear data only, leaving deployed indexes unchanged.
var (
	liveProbeOnce sync.Once
	liveProbeErr  error
)

func probeLiveOnce(t *testing.T, db *firestoredb.DB) {
	t.Helper()
	liveProbeOnce.Do(func() {
		_, liveProbeErr = Repositories(t.Context(), db, WithAudit())
	})
	if liveProbeErr != nil {
		t.Fatalf("Repositories against %s with baseline and audit index probes enabled: %v", db.Target(), liveProbeErr)
	}
}

// Each fixture owns an empty repository set and its supplied guardian policy.
// Index verification is explicit above, rather than repeated after every reset.
func newLiveRepos(t *testing.T, db *firestoredb.DB) func(*testing.T, mutations.GuardianPolicy) authorization.Repositories {
	t.Helper()
	probeLiveOnce(t, db)
	return func(t *testing.T, policy mutations.GuardianPolicy) authorization.Repositories {
		t.Helper()
		firestoretest.ResetLive(t, db, liveAllCollections...)
		repos, err := Repositories(t.Context(), db, WithoutIndexProbe(), WithGuardianPolicy(policy))
		if err != nil {
			t.Fatalf("Repositories against %s: %v", db.Target(), err)
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
// store hands the harness no transaction.Transactor, because a Firestore transaction
// requires every read to precede every write and never observes its own pending
// writes — the exact property the ambient family proves from both sides. Real
// Firestore does not change that; it is a family difference, so this skip is the
// same on the emulator and live.
func TestRunTransactionalLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	t.Log("firestore: this store supplies NO transaction.Transactor — the ambient-transaction family is a KNOWN FAMILY DIFFERENCE (firestore-stores ruling R1), not a defect. It is the only test root this leg is allowed to skip, and live-stores.yml allows it by name.")
	newRepos := newLiveRepos(t, db)
	storetest.RunTransactional(t, func(t *testing.T) (authorization.Repositories, transaction.Transactor) {
		return newRepos(t, mutations.GuardianPolicy{}), nil
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
	repos := newLiveRepos(t, db)(t, mutations.GuardianPolicy{})
	assertAmbientRefusal(t, db, repos)
}

// The disposable live harness must deploy both exported manifests before this
// test. Without live configuration OpenLive reports the verification gap.
func TestAuditConformanceLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	probeLiveOnce(t, db)
	storetest.RunAudit(t, func(t *testing.T, enabled bool) authorization.Repositories {
		firestoretest.ResetLive(t, db, liveAllCollections...)
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

func TestAuditQueryMatrixLive(t *testing.T) {
	db := firestoretest.OpenLive(t)
	firestoretest.ResetLive(t, db, liveAllCollections...)
	repos, err := Repositories(t.Context(), db, WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithSource(context.Background(), audit.Source{ActorType: "user", ActorID: "actor"})
	tuple := relationships.CreateRelationship{ResourceType: "doc", ResourceID: "matrix", Relation: "viewer", SubjectType: "user", SubjectID: "subject"}
	if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{tuple}); err != nil {
		t.Fatal(err)
	}
	for mask := 0; mask < 8; mask++ {
		filter := audit.Filter{}
		if mask&1 != 0 {
			filter.ActorType, filter.ActorID = "user", "actor"
		}
		if mask&2 != 0 {
			filter.ResourceType, filter.ResourceID = "doc", "matrix"
		}
		if mask&4 != 0 {
			filter.SubjectType, filter.SubjectID = "user", "subject"
		}
		for _, direction := range []string{list.ASC, list.DESC} {
			page, err := repos.Audit.List(ctx, filter, list.Request{Limit: 2, Order: list.NewOrder("occurred_at", direction), WithCount: true})
			if err != nil || len(page.Items) != 1 || page.Total == nil || *page.Total != 1 {
				t.Fatalf("audit filter %d %s: page=%+v err=%v", mask, direction, page, err)
			}
		}
	}
}
