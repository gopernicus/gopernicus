package memstore

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/features/jobs"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work/worktest"
)

// This conformance test lives INSIDE package memstore (not memstore_test) so the
// test-only Inspector can snapshot the FencedQueue's private jobs map under the
// queue's own mutex. Production memstore and sdk/capabilities/work expose no such
// inspection API — it exists only here, exactly as worktest requires.

// queueInspector is a TEST-ONLY worktest.Inspector over a memstore FencedQueue. It
// reads the private per-key executions while holding the queue's mutex and clones
// each payload under that lock, so its snapshots stay race-safe under -race.
type queueInspector struct {
	q *FencedQueue
}

// ExecutionsByKey returns every execution generation holding logicalKey. It holds
// the queue mutex for the whole scan and deep-copies each payload before releasing
// it, so a concurrent Checkpoint can never mutate the bytes the suite inspects.
func (i queueInspector) ExecutionsByKey(_ context.Context, logicalKey string) ([]worktest.Execution, error) {
	i.q.mu.Lock()
	defer i.q.mu.Unlock()

	var execs []worktest.Execution
	for _, j := range i.q.jobs {
		if j.LogicalKey != logicalKey {
			continue
		}
		payload := make([]byte, len(j.Payload))
		copy(payload, j.Payload)
		execs = append(execs, worktest.Execution{
			ExecutionID: j.JobID,
			Status:      j.JobStatus,
			Payload:     payload,
		})
	}
	return execs, nil
}

// newProtocolHarness builds a jobs.Service over a fresh memstore fenced queue and
// pairs it with a test-only Inspector over that same queue. The Service is the
// implementation of record for sdk/capabilities/work; it satisfies the full
// ReplaceQueue (Enqueuer + Replacer + StatusReader).
func newProtocolHarness(t *testing.T) (worktest.ReplaceQueue, worktest.Inspector) {
	t.Helper()
	fq := NewFencedQueue()
	svc, err := jobs.NewService(jobs.Repositories{FencedQueue: fq}, jobs.Config{})
	if err != nil {
		t.Fatalf("NewService (fenced-only): %v", err)
	}
	return svc, queueInspector{q: fq}
}

// TestWorkProtocolConformance runs the core keyed-work protocol suite
// (Enqueuer + StatusReader) against the jobs.Service over the memstore fenced queue.
func TestWorkProtocolConformance(t *testing.T) {
	worktest.Run(t, func(t *testing.T) (worktest.Queue, worktest.Inspector) {
		return newProtocolHarness(t)
	})
}

// TestWorkProtocolReplaceConformance runs the optional Replacer extension suite
// against the same Service, proving supersession, latest generation, and byte-exact
// opaque payload under -race.
func TestWorkProtocolReplaceConformance(t *testing.T) {
	worktest.RunReplace(t, newProtocolHarness)
}
