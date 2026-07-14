package storetest

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs/domain/job"
	"github.com/gopernicus/gopernicus/features/jobs/domain/schedule"
	"github.com/gopernicus/gopernicus/features/jobs/memstore"
)

// TestReferenceQueue runs the queue conformance suite against the in-core
// memstore reference. This is what lets features/jobs self-verify under guard G2
// (the core cannot import a driver, so without an in-core implementation the
// suite would compile but never execute). The queue is constructed with the
// exported Lease so the lease-expiry case is honored identically here and in the
// dialect stores. Each call returns a fresh, empty store — the clean-isolation
// contract RunQueue documents.
func TestReferenceQueue(t *testing.T) {
	RunQueue(t, func(t *testing.T) job.QueueRepository {
		return memstore.NewQueue(memstore.WithLease(Lease))
	})
}

// TestReferenceSchedules runs the schedule conformance suite against a fresh
// in-core memstore per call.
func TestReferenceSchedules(t *testing.T) {
	RunSchedules(t, func(t *testing.T) schedule.Repository {
		return memstore.NewSchedules()
	})
}

// TestReferenceFencedQueue runs the fenced/keyed/checkpointed queue suite against
// the in-core memstore reference. As of AV3D-1.4 every case is load-bearing here:
// AV3D-1.1 activated lease-fencing, AV3D-1.2 the logical-key admission/supersession
// and concurrency cases, AV3D-1.3 the claimed-payload checkpoint cases, and AV3D-1.4
// the retry-at, permanent dead-letter, and bounded-purge cases. No case is deferred;
// the pgx/turso stores run the same suite live under -race at AV3D-1.5.
func TestReferenceFencedQueue(t *testing.T) {
	RunFencedQueue(t, func(t *testing.T) job.FencedQueueRepository {
		return memstore.NewFencedQueue()
	})
}
