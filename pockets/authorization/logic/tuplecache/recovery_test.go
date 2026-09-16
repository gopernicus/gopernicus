package tuplecache_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	tuplefacts "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type capacityBackend struct {
	tuplecache.Backend
	readError, publishError error
	maxChanges              int
}

func (b *capacityBackend) Read(ctx context.Context, state tuplecache.State, keys []tuplecache.SetKey) ([][]tuplefacts.Tuple, error) {
	if len(keys) > 0 && b.readError != nil {
		return nil, b.readError
	}
	return b.Backend.Read(ctx, state, keys)
}

func (b *capacityBackend) Publish(ctx context.Context, before, next tuplecache.State, snapshot tuplecache.Snapshot, validFor time.Duration) error {
	if b.publishError != nil {
		return b.publishError
	}
	if !snapshot.Full && len(snapshot.Changes) > b.maxChanges {
		return tuplecache.ErrCapacity
	}
	return b.Backend.Publish(ctx, before, next, snapshot, validFor)
}

func TestRebuildRecoversRejectedDelta(t *testing.T) {
	backend := &capacityBackend{Backend: memory.NewTupleCache(), maxChanges: 0}
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	poll(t, c)
	before, err := backend.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	revoke(t, s)
	if err := c.Poll(t.Context()); !errors.Is(err, tuplecache.ErrCapacity) {
		t.Fatalf("oversized delta: %v", err)
	}
	after, err := backend.State(t.Context())
	if err != nil || before != after || len(s.pending) != 1 || s.acks != 1 {
		t.Fatalf("failed publication changed receipt or acknowledged: %v/%v pending=%d acks=%d", after, err, len(s.pending), s.acks)
	}
	failed := c.Stats()
	if failed.CapacityFailures != 1 || failed.LastPoll.FailureStage != "publication" || failed.LastPoll.Changes != 1 || failed.LastPoll.Full {
		t.Fatalf("missing capacity diagnostics: %+v", failed)
	}
	// Current tuples are empty; the obsolete delete payload need not be replayed.
	s.pending[0].Before = nil
	if err := c.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	allowed(t, c, false)
	stats := c.Stats()
	if len(s.pending) != 0 || stats.Rebuilds != 2 || stats.Publications != 2 || stats.PollFailures != 1 || stats.LastPoll.FailureStage != "" || !stats.LastPoll.Full || stats.LastPoll.Tuples != 0 || stats.LastPoll.Changes != 1 || stats.LastSuccessfulPoll.IsZero() {
		t.Fatalf("rebuild did not recover: pending=%v stats=%+v", s.pending, stats)
	}
}

func TestCapacityReadFallsBackWithoutGrantingStaleData(t *testing.T) {
	backend := &capacityBackend{Backend: memory.NewTupleCache()}
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	poll(t, c)
	revoke(t, s)
	backend.readError = tuplecache.ErrCapacity
	allowed(t, c, false)
	if stats := c.Stats(); stats.CapacityFallbacks != 1 || stats.Fallbacks != 1 || stats.Hits != 0 || s.durable != 1 {
		t.Fatalf("capacity fallback: %+v durable=%d", stats, s.durable)
	}
	// Even a callback that ignores a backend error cannot certify its result.
	if err := c.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		_, _ = graph(reads, directModel).GetRelationTargets(ctx, "space", "s", "viewer")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stats := c.Stats(); stats.CapacityFallbacks != 2 || s.durable != 2 {
		t.Fatalf("ignored capacity error was not retained: %+v durable=%d", stats, s.durable)
	}
}

func TestRebuildPreservesFailureAndAcknowledgementProtocol(t *testing.T) {
	backend := &capacityBackend{Backend: memory.NewTupleCache()}
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	poll(t, c)
	revoke(t, s)
	backend.publishError = tuplecache.ErrConflict
	if err := c.Rebuild(t.Context()); !errors.Is(err, tuplecache.ErrConflict) || len(s.pending) != 1 || s.acks != 1 {
		t.Fatalf("conflicting rebuild: %v pending=%v acks=%d", err, s.pending, s.acks)
	}
	if stats := c.Stats(); stats.PollConflicts != 1 || stats.LastPoll.FailureStage != "publication" {
		t.Fatalf("conflict diagnostics: %+v", stats)
	}
	backend.publishError = nil
	s.ackErr = errors.New("ack failed")
	if err := c.Rebuild(t.Context()); !errors.Is(err, s.ackErr) || len(s.pending) != 1 {
		t.Fatalf("acknowledgement failure discarded work: %v/%v", err, s.pending)
	}
	allowed(t, c, false)
	if stats := c.Stats(); stats.LastPoll.FailureStage != "acknowledgement" {
		t.Fatalf("acknowledgement diagnostics: %+v", stats)
	}
	s.ackErr = nil
	poll(t, c)
	allowed(t, c, false)
	if len(s.pending) != 0 {
		t.Fatal("ordinary poll did not reconcile failed rebuild acknowledgement")
	}
}

func TestRebuildSharesDeliveryGateAndFreshness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := fixture(t, []tuplefacts.Tuple{grant}, memory.NewTupleCache())
		poll(t, c)
		entered, release := make(chan struct{}), make(chan struct{})
		s.beforeSnapshot = func() { close(entered); <-release }
		done := make(chan error, 1)
		go func() { done <- c.Rebuild(t.Context()) }()
		<-entered
		if err := c.Poll(t.Context()); !errors.Is(err, workers.ErrNoWork) {
			t.Fatalf("poll passed rebuild gate: %v", err)
		}
		if err := c.Rebuild(t.Context()); !errors.Is(err, workers.ErrNoWork) {
			t.Fatalf("rebuild passed rebuild gate: %v", err)
		}
		time.Sleep(2 * time.Second)
		close(release)
		if err := <-done; !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("slow rebuild renewed eligibility: %v", err)
		}
		if stats := c.Stats(); stats.LastPoll.FailureStage != "freshness" || stats.LastPoll.SnapshotDuration != 2*time.Second || stats.Rebuilds != 1 {
			t.Fatalf("slow snapshot diagnostics: %+v", stats)
		}
		allowed(t, c, true)
		if s.durable != 1 {
			t.Fatal("expired data was used after failed rebuild")
		}
	})
}

func TestRebuildPreconditions(t *testing.T) {
	c, s := fixture(t, nil, memory.NewTupleCache())
	s.ambient = true
	if err := c.Rebuild(t.Context()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ambient rebuild: %v", err)
	}
	s.ambient = false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Rebuild(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled rebuild: %v", err)
	}
	_ = c.Close()
	if err := c.Rebuild(t.Context()); !errors.Is(err, tuplecache.ErrUnavailable) {
		t.Fatalf("closed rebuild: %v", err)
	}
	if s.snapshots != 0 {
		t.Fatal("invalid rebuild touched source")
	}
}

func TestPollPanicDoesNotRecordSuccessOrRetainGate(t *testing.T) {
	c, s := fixture(t, nil, memory.NewTupleCache())
	poll(t, c)
	before := c.Stats().LastSuccessfulPoll
	s.beforeSnapshot = func() { panic("source failed") }
	func() {
		defer func() {
			if got := recover(); got != "source failed" {
				t.Fatalf("poll changed panic: %v", got)
			}
		}()
		_ = c.Poll(t.Context())
	}()
	stats := c.Stats()
	if !stats.LastSuccessfulPoll.Equal(before) || stats.PollFailures != 1 || stats.LastPoll.FailureStage != "snapshot" {
		t.Fatalf("panic recorded successful delivery: %+v", stats)
	}
	s.beforeSnapshot = nil
	poll(t, c)
	if stats := c.Stats(); stats.LastPoll.FailureStage != "" || stats.PollFailures != 1 {
		t.Fatalf("poll did not recover after panic: %+v", stats)
	}
}
