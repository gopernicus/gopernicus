package memory_test

import (
	"testing"

	job "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/storetest"
)

// TestConformanceQueue runs the shared queue conformance suite against the
// in-core memstore reference from the memstore package itself — the hermetic leg
// of make check for this pocket's paginated semantics (the dialect stores prove
// the same suite live). Each newRepo call returns a fresh, empty queue built with
// the exported Lease so the lease-expiry case is honored identically here and in
// the dialect stores.
func TestConformanceQueue(t *testing.T) {
	storetest.RunQueue(t, func(t *testing.T) job.QueueRepository {
		return memory.NewQueue(memory.WithLease(storetest.Lease))
	})
}

// TestConformanceSchedules runs the shared schedule conformance suite against a
// fresh in-core memstore per call.
func TestConformanceSchedules(t *testing.T) {
	storetest.RunSchedules(t, func(t *testing.T) schedule.Repository {
		return memory.NewSchedules()
	})
}
