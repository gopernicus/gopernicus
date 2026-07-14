// Package worktest is a conformance suite for the keyed-work submission
// protocol (sdk/capabilities/work): every backend that satisfies the ports
// should pass Run — and, if it supports replacement, RunReplace — against a
// fresh instance. Modeled on the cachertest / eventstest pattern: a
// Run(t, newHarness) runner so implementations are verified against one shared
// behavioral contract. Imports stdlib + the sdk root + sdk/capabilities/work
// only (sdk stays dependency-free per the constitution), never another
// capability or sdk/feature.
//
// Return values alone cannot prove a queue admitted no hidden duplicate or that
// a replacement retired the prior generation, so the suite drives a TEST-ONLY
// Inspector that reveals the queue's per-key executions. Inspector is inspection
// vocabulary for conformance testing ONLY: it must NEVER be promoted into the
// production work package or used by production consumers, which see lifecycle
// status by key and nothing else. Executor behavior under claim/lease races is
// out of this consumer protocol and stays in features/jobs/storetest.
package worktest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/work"
)

// Queue is the core submit/status protocol. It does not require optional
// replacement.
type Queue interface {
	work.Enqueuer
	work.StatusReader
}

// ReplaceQueue adds the optional atomic replacement capability.
type ReplaceQueue interface {
	Queue
	work.Replacer
}

// Execution is TEST-ONLY inspection vocabulary, not part of the production
// protocol.
type Execution struct {
	ExecutionID string
	Status      work.Status
	Payload     []byte
}

// Inspector lets the conformance suite prove hidden queue effects — execution
// count, per-generation status, and opaque payload bytes — without adding a
// production payload/list API. Implementations provide an adapter in their
// tests; it exists only here.
type Inspector interface {
	ExecutionsByKey(ctx context.Context, logicalKey string) ([]Execution, error)
}

// Run exercises the core Enqueuer + StatusReader contract against a fresh
// instance obtained from newHarness for each subtest.
func Run(t *testing.T, newHarness func(t *testing.T) (Queue, Inspector)) {
	t.Helper()

	t.Run("EnqueueOnceIdempotentWhileActive", func(t *testing.T) {
		q, insp := newHarness(t)
		testEnqueueOnceIdempotentWhileActive(t, q, insp)
	})
	t.Run("LatestByKeyDeterministic", func(t *testing.T) {
		q, insp := newHarness(t)
		testLatestByKeyDeterministic(t, q, insp)
	})
	t.Run("StatusProjectionTotality", func(t *testing.T) {
		q, _ := newHarness(t)
		testStatusProjectionTotality(t, q)
	})
	t.Run("UnknownKeyIsNotFound", func(t *testing.T) {
		q, _ := newHarness(t)
		testUnknownKeyIsNotFound(t, q)
	})
	t.Run("PayloadOpaqueBytePreserved", func(t *testing.T) {
		q, insp := newHarness(t)
		testPayloadOpaqueBytePreserved(t, q, insp)
	})
}

// RunReplace exercises the optional Replacer extension against a fresh instance
// obtained from newHarness for each subtest.
func RunReplace(t *testing.T, newHarness func(t *testing.T) (ReplaceQueue, Inspector)) {
	t.Helper()

	t.Run("ReplaceFreshDistinctExecution", func(t *testing.T) {
		q, insp := newHarness(t)
		testReplaceFreshDistinctExecution(t, q, insp)
	})
	t.Run("ReplaceRepeatedSupersedesToLatest", func(t *testing.T) {
		q, insp := newHarness(t)
		testReplaceRepeatedSupersedesToLatest(t, q, insp)
	})
}

// testEnqueueOnceIdempotentWhileActive proves a second EnqueueOnce under the
// same active key returns the SAME execution ID and admits no duplicate: the
// inspector sees exactly one execution.
func testEnqueueOnceIdempotentWhileActive(t *testing.T, q Queue, insp Inspector) {
	ctx := context.Background()
	const (
		kind = "suite.kind"
		key  = "suite-key-idem"
	)

	first, err := q.EnqueueOnce(ctx, kind, key, []byte("payload"))
	if err != nil {
		t.Fatalf("first EnqueueOnce() error = %v", err)
	}
	if first == "" {
		t.Fatal("first EnqueueOnce() executionID = \"\", want non-empty")
	}

	second, err := q.EnqueueOnce(ctx, kind, key, []byte("payload"))
	if err != nil {
		t.Fatalf("second EnqueueOnce() error = %v", err)
	}
	if second != first {
		t.Errorf("second EnqueueOnce() executionID = %q, want the active %q", second, first)
	}

	execs := executions(t, insp, key)
	if len(execs) != 1 {
		t.Fatalf("inspector saw %d executions for an idempotent key, want 1", len(execs))
	}
	if execs[0].ExecutionID != first {
		t.Errorf("sole execution ID = %q, want %q", execs[0].ExecutionID, first)
	}
}

// testLatestByKeyDeterministic proves repeated LatestStatusByKey reads for an
// unchanged key return the same status.
func testLatestByKeyDeterministic(t *testing.T, q Queue, _ Inspector) {
	ctx := context.Background()
	const (
		kind = "suite.kind"
		key  = "suite-key-latest"
	)

	if _, err := q.EnqueueOnce(ctx, kind, key, []byte("payload")); err != nil {
		t.Fatalf("EnqueueOnce() error = %v", err)
	}

	first, err := q.LatestStatusByKey(ctx, key)
	if err != nil {
		t.Fatalf("first LatestStatusByKey() error = %v", err)
	}
	for i := range 3 {
		got, err := q.LatestStatusByKey(ctx, key)
		if err != nil {
			t.Fatalf("LatestStatusByKey() error = %v", err)
		}
		if got != first {
			t.Errorf("LatestStatusByKey() = %q on read %d, want deterministic %q", got, i+1, first)
		}
	}
}

// testStatusProjectionTotality proves the latest-status projection is total:
// every value it returns is in the canonical vocabulary (Known).
func testStatusProjectionTotality(t *testing.T, q Queue) {
	ctx := context.Background()
	const (
		kind = "suite.kind"
		key  = "suite-key-total"
	)

	if _, err := q.EnqueueOnce(ctx, kind, key, []byte("payload")); err != nil {
		t.Fatalf("EnqueueOnce() error = %v", err)
	}

	status, err := q.LatestStatusByKey(ctx, key)
	if err != nil {
		t.Fatalf("LatestStatusByKey() error = %v", err)
	}
	if !status.Known() {
		t.Errorf("LatestStatusByKey() = %q, which is not a Known() status", status)
	}
}

// testUnknownKeyIsNotFound proves a status read for a never-submitted key
// resolves to the sdk not-found error class.
func testUnknownKeyIsNotFound(t *testing.T, q Queue) {
	ctx := context.Background()

	status, err := q.LatestStatusByKey(ctx, "suite-key-never-submitted")
	if err == nil {
		t.Fatalf("LatestStatusByKey(unknown) status = %q, err = nil, want the not-found class", status)
	}
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("LatestStatusByKey(unknown) err = %v, want errors.Is sdk.ErrNotFound", err)
	}
}

// testPayloadOpaqueBytePreserved proves the payload is opaque: arbitrary
// non-UTF8/non-JSON bytes are accepted, and the queue keeps a deep copy — the
// inspector sees the original bytes even after the caller mutates its slice.
func testPayloadOpaqueBytePreserved(t *testing.T, q Queue, insp Inspector) {
	ctx := context.Background()
	const (
		kind = "suite.kind"
		key  = "suite-key-payload"
	)

	original := []byte{0xff, 0xfe, 0x00, 0x01, 0x80, 0x7f}
	want := make([]byte, len(original))
	copy(want, original)

	if _, err := q.EnqueueOnce(ctx, kind, key, original); err != nil {
		t.Fatalf("EnqueueOnce() error = %v", err)
	}

	// Mutate the caller's slice after enqueue: a queue holding the same backing
	// array would leak the mutation into its stored payload.
	for i := range original {
		original[i] = 0
	}

	execs := executions(t, insp, key)
	if len(execs) != 1 {
		t.Fatalf("inspector saw %d executions, want 1", len(execs))
	}
	if !bytes.Equal(execs[0].Payload, want) {
		t.Errorf("stored payload = %v, want %v (deep copy, unaffected by caller mutation)", execs[0].Payload, want)
	}
}

// testReplaceFreshDistinctExecution proves Replace admits a fresh, distinct
// execution: exactly one pending generation, the prior generation superseded.
func testReplaceFreshDistinctExecution(t *testing.T, q ReplaceQueue, insp Inspector) {
	ctx := context.Background()
	const (
		kind = "suite.kind"
		key  = "suite-key-replace"
	)

	original, err := q.EnqueueOnce(ctx, kind, key, []byte("v1"))
	if err != nil {
		t.Fatalf("EnqueueOnce() error = %v", err)
	}

	replaced, err := q.Replace(ctx, kind, key, []byte("v2"))
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if replaced == "" {
		t.Fatal("Replace() executionID = \"\", want non-empty")
	}
	if replaced == original {
		t.Errorf("Replace() executionID = %q, want distinct from the original %q", replaced, original)
	}

	execs := executions(t, insp, key)
	if got := statusOf(execs, original); got != work.StatusSuperseded {
		t.Errorf("original execution status = %q, want %q", got, work.StatusSuperseded)
	}

	pending := countStatus(execs, work.StatusPending)
	if pending != 1 {
		t.Errorf("pending generations = %d, want exactly 1", pending)
	}
	if got := statusOf(execs, replaced); got != work.StatusPending {
		t.Errorf("fresh execution status = %q, want %q", got, work.StatusPending)
	}

	latest, err := q.LatestStatusByKey(ctx, key)
	if err != nil {
		t.Fatalf("LatestStatusByKey() error = %v", err)
	}
	if latest != work.StatusPending {
		t.Errorf("LatestStatusByKey() = %q, want the fresh generation %q", latest, work.StatusPending)
	}
}

// testReplaceRepeatedSupersedesToLatest proves that across repeated replace
// sequences exactly one pending generation survives and latest status always
// reflects it; every prior generation is superseded.
func testReplaceRepeatedSupersedesToLatest(t *testing.T, q ReplaceQueue, insp Inspector) {
	ctx := context.Background()
	const (
		kind = "suite.kind"
		key  = "suite-key-replace-repeat"
	)

	last, err := q.EnqueueOnce(ctx, kind, key, []byte("gen-0"))
	if err != nil {
		t.Fatalf("EnqueueOnce() error = %v", err)
	}

	for gen := 1; gen <= 3; gen++ {
		fresh, err := q.Replace(ctx, kind, key, []byte("gen"))
		if err != nil {
			t.Fatalf("Replace() gen %d error = %v", gen, err)
		}
		if fresh == last {
			t.Errorf("Replace() gen %d executionID = %q, want distinct from prior %q", gen, fresh, last)
		}

		execs := executions(t, insp, key)
		if pending := countStatus(execs, work.StatusPending); pending != 1 {
			t.Errorf("after gen %d: pending generations = %d, want exactly 1", gen, pending)
		}
		if got := statusOf(execs, last); got != work.StatusSuperseded {
			t.Errorf("after gen %d: prior execution %q status = %q, want %q", gen, last, got, work.StatusSuperseded)
		}

		latest, err := q.LatestStatusByKey(ctx, key)
		if err != nil {
			t.Fatalf("LatestStatusByKey() gen %d error = %v", gen, err)
		}
		if latest != work.StatusPending {
			t.Errorf("after gen %d: LatestStatusByKey() = %q, want %q", gen, latest, work.StatusPending)
		}

		last = fresh
	}
}

// executions fetches the per-key executions from the inspector, failing the test
// on any error.
func executions(t *testing.T, insp Inspector, key string) []Execution {
	t.Helper()
	execs, err := insp.ExecutionsByKey(context.Background(), key)
	if err != nil {
		t.Fatalf("Inspector.ExecutionsByKey(%q) error = %v", key, err)
	}
	return execs
}

// statusOf returns the status of the execution with id, or the empty Status if
// no such execution is present.
func statusOf(execs []Execution, id string) work.Status {
	for _, e := range execs {
		if e.ExecutionID == id {
			return e.Status
		}
	}
	return ""
}

// countStatus counts executions currently in status.
func countStatus(execs []Execution, status work.Status) int {
	n := 0
	for _, e := range execs {
		if e.Status == status {
			n++
		}
	}
	return n
}
