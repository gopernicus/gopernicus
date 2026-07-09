package memstore_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/features/jobs/memstore"
	"github.com/gopernicus/gopernicus/features/jobs/storetest"
)

// TestConformanceQueue runs the shared queue conformance suite against the
// in-core memstore reference from the memstore package itself — the hermetic leg
// of make check for this feature's paginated semantics (the dialect stores prove
// the same suite live). Each newRepo call returns a fresh, empty queue built with
// the exported Lease so the lease-expiry case is honored identically here and in
// the dialect stores.
func TestConformanceQueue(t *testing.T) {
	storetest.RunQueue(t, func(t *testing.T) job.QueueRepository {
		return memstore.NewQueue(memstore.WithLease(storetest.Lease))
	})
}

// TestConformanceSchedules runs the shared schedule conformance suite against a
// fresh in-core memstore per call.
func TestConformanceSchedules(t *testing.T) {
	storetest.RunSchedules(t, func(t *testing.T) schedule.Repository {
		return memstore.NewSchedules()
	})
}
