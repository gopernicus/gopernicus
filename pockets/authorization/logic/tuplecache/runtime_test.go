package tuplecache_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	tuplefacts "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

type source struct {
	store                    *memory.Store
	receipt                  string
	tuples                   []tuplefacts.Tuple
	pending                  []tuplecache.Change
	snapshots, durable, acks int
	ackErr, snapshotErr      error
	beforeSnapshot           func()
	ambient                  bool
}

func (s *source) Binding() string                       { return "test-store" }
func (s *source) CacheableContext(context.Context) bool { return !s.ambient }
func (s *source) Snapshot(_ context.Context, receipt string) (tuplecache.Snapshot, error) {
	s.snapshots++
	if s.beforeSnapshot != nil {
		s.beforeSnapshot()
	}
	if s.snapshotErr != nil {
		return tuplecache.Snapshot{}, s.snapshotErr
	}
	return tuplecache.Snapshot{Receipt: s.receipt, Full: receipt == "" || receipt != s.receipt, Tuples: s.tuples, Changes: s.pending}, nil
}
func (s *source) Acknowledge(_ context.Context, before, next string, ids []string) error {
	s.acks++
	if s.ackErr != nil {
		return s.ackErr
	}
	if before != s.receipt {
		return tuplecache.ErrConflict
	}
	if len(ids) != len(s.pending) {
		return fmt.Errorf("wrong acknowledgement: %v", ids)
	}
	for i, id := range ids {
		if id != s.pending[i].ID {
			return fmt.Errorf("wrong ID: %q", id)
		}
	}
	s.receipt, s.pending = next, nil
	return nil
}
func (s *source) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	s.durable++
	return s.store.Tuples().ReadTupleSnapshot(ctx, fn)
}
func fixture(t testing.TB, tuples []tuplefacts.Tuple, backend tuplecache.Backend, opts ...tuplecache.Option) (*tuplecache.TupleCache, *source) {
	t.Helper()
	s := &source{store: memory.New(), tuples: tuples}
	if err := s.store.Tuples().ApplyTuples(t.Context(), tuplefacts.Changes{Add: tuples}); err != nil {
		t.Fatal(err)
	}
	opts = append([]tuplecache.Option{tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second})}, opts...)
	c, err := tuplecache.New(s, backend, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, s
}

var directModel = relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "space", Relation: "viewer", SubjectType: "user"}})
var grant = tupleFact("space", "s", "viewer", "user", "alice", "")

func allowed(t *testing.T, c *tuplecache.TupleCache, want bool) {
	t.Helper()
	var got bool
	err := c.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		var err error
		got, err = graph(reads, directModel).CheckRelationWithGroupExpansion(ctx, "space", "s", "viewer", "user", "alice", 100)
		return err
	})
	if err != nil || got != want {
		t.Fatalf("allowed=%v want=%v err=%v", got, want, err)
	}
}
func poll(t *testing.T, c *tuplecache.TupleCache) {
	t.Helper()
	if err := c.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func revoke(t *testing.T, s *source) {
	t.Helper()
	if err := s.store.Tuples().ApplyTuples(t.Context(), tuplefacts.Changes{Remove: []tuplefacts.Tuple{grant}}); err != nil {
		t.Fatal(err)
	}
	s.tuples = nil
	s.pending = []tuplecache.Change{{ID: "revoke", Before: &grant}}
}

func TestBootstrapDeliveryAndRecovery(t *testing.T) {
	backend := memory.NewTupleCache()
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	allowed(t, c, true)
	if s.durable != 1 {
		t.Fatal("uninitialized cache served data")
	}
	poll(t, c)
	allowed(t, c, true)
	if s.durable != 1 || c.Stats().Hits != 1 {
		t.Fatalf("warm read: %+v", c.Stats())
	}
	before, _ := backend.State(t.Context(), c.Binding())
	poll(t, c)
	after, _ := backend.State(t.Context(), c.Binding())
	if before != after || s.acks != 1 {
		t.Fatal("idle observation changed receipt or acknowledged work")
	}
	revoke(t, s)
	allowed(t, c, true) // Explicit accepted delivery lag, before relay.
	poll(t, c)
	allowed(t, c, false)
	if len(s.pending) != 0 {
		t.Fatal("delivered work retained")
	}
	fresh, err := tuplecache.New(s, memory.NewTupleCache(), tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	poll(t, fresh)
	allowed(t, fresh, false)
	if fresh.Stats().Rebuilds != 1 {
		t.Fatal("fresh backend did not rebuild from source")
	}
}

func TestAcknowledgementFailureAndRedisRestore(t *testing.T) {
	backend := memory.NewTupleCache()
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	poll(t, c)
	old, _ := backend.State(t.Context(), c.Binding())
	revoke(t, s)
	s.ackErr = errors.New("SQL acknowledgement unavailable")
	if err := c.Poll(t.Context()); !errors.Is(err, s.ackErr) {
		t.Fatal(err)
	}
	if len(s.pending) != 1 {
		t.Fatal("failed acknowledgement lost pending event")
	}
	allowed(t, c, false)
	s.ackErr = nil
	poll(t, c)
	if c.Stats().Rebuilds != 2 || len(s.pending) != 0 {
		t.Fatal("mismatching receipt did not rebuild")
	}
	// Restore an old complete Redis snapshot while the SQL outbox is empty.
	state, _ := backend.State(t.Context(), c.Binding())
	if err := backend.Publish(t.Context(), state, old, tuplecache.Snapshot{Full: true, Tuples: []tuplefacts.Tuple{grant}}, time.Second); err != nil {
		t.Fatal(err)
	}
	poll(t, c)
	allowed(t, c, false)
	if c.Stats().Rebuilds != 3 {
		t.Fatal("acknowledged lost mutation was not recovered from source")
	}
}

type hookedBackend struct {
	tuplecache.Backend
	afterRead func()
	fail      bool
}

func (b *hookedBackend) Read(ctx context.Context, state tuplecache.State, keys []tuplecache.SetKey) ([][]tuplefacts.Tuple, error) {
	if b.fail {
		return nil, tuplecache.ErrUnavailable
	}
	values, err := b.Backend.Read(ctx, state, keys)
	if b.afterRead != nil {
		hook := b.afterRead
		b.afterRead = nil
		hook()
	}
	return values, err
}
func TestPublicationDuringCheckRetriesWholeSnapshot(t *testing.T) {
	backend := &hookedBackend{Backend: memory.NewTupleCache()}
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	poll(t, c)
	backend.afterRead = func() { revoke(t, s); poll(t, c) }
	allowed(t, c, false)
	if s.durable != 1 || c.Stats().Hits != 0 {
		t.Fatal("mixed publication returned a cached result")
	}
	backend.fail = true
	allowed(t, c, false)
	if s.durable != 2 {
		t.Fatal("Redis error did not fall back")
	}
}

func TestExpiryAndSlowObservationDoNotExtendStaleAuthority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, s := fixture(t, []tuplefacts.Tuple{grant}, memory.NewTupleCache())
		poll(t, c)
		revoke(t, s)
		s.snapshotErr = errors.New("source disconnected")
		if err := c.Poll(t.Context()); err == nil {
			t.Fatal("expected relay failure")
		}
		time.Sleep(time.Second)
		allowed(t, c, false)
		if s.durable != 1 {
			t.Fatal("expired mirror served stale authority")
		}
		s.snapshotErr = nil
		s.beforeSnapshot = func() { time.Sleep(2 * time.Second) }
		if err := c.Poll(t.Context()); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatal(err)
		}
		allowed(t, c, false)
		if s.durable != 2 {
			t.Fatal("slow observation renewed old authority")
		}
	})
}

func TestCallbackLifetimePanicCancellationAndGlobalRole(t *testing.T) {
	c, s := fixture(t, []tuplefacts.Tuple{grant}, memory.NewTupleCache())
	poll(t, c)
	var escaped relationships.CheckReader
	func() {
		defer func() {
			if recover() != "callback panic" {
				t.Fatal("panic swallowed")
			}
		}()
		_ = c.Run(t.Context(), func(_ context.Context, reads tuplecache.CheckReads) error {
			escaped = graph(reads, directModel)
			panic("callback panic")
		})
	}()
	if _, err := escaped.CheckBatchDirect(t.Context(), "space", nil, "viewer", "user", "alice", 100); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
		t.Fatalf("escaped empty batch: %v", err)
	}
	if _, err := escaped.GetRelationTargets(t.Context(), "space", "s", "viewer"); !errors.Is(err, tuplecache.ErrSnapshotClosed) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	err := c.Run(ctx, func(_ context.Context, reads tuplecache.CheckReads) error {
		cancel()
		_, err := graph(reads, directModel).GetRelationTargets(context.Background(), "space", "s", "viewer")
		return err
	})
	if !errors.Is(err, context.Canceled) || s.durable != 0 {
		t.Fatalf("cancel: %v durable=%d", err, s.durable)
	}
	globalRole := tuplefacts.Tuple{Scope: tuplefacts.Global(), Relation: "admin", Subject: tuplefacts.SubjectRef{Type: "user", ID: "alice"}}
	if err := s.store.Tuples().ApplyTuples(t.Context(), tuplefacts.Changes{Add: []tuplefacts.Tuple{globalRole}}); err != nil {
		t.Fatal(err)
	}
	s.tuples = append(s.tuples, globalRole)
	s.pending = []tuplecache.Change{{ID: "global-role", After: &globalRole}}
	poll(t, c)
	var hasRole bool
	if err := c.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		var err error
		hasRole, err = reads.Contains(ctx, globalRole)
		return err
	}); err != nil || !hasRole || s.durable != 0 {
		t.Fatalf("cached global role: %v/%v/%d", hasRole, err, s.durable)
	}
	s.ambient = true
	if err := c.ReadTupleSnapshot(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		if reads == nil {
			t.Fatal("ambient callback did not receive a bound reader")
		}
		held, err := reads.Contains(ctx, globalRole)
		if err != nil || !held {
			t.Fatalf("ambient authoritative role: %v/%v", held, err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if s.durable != 1 {
		t.Fatal("ambient operation did not borrow durable snapshot")
	}
}

func TestRawReaderParityModelsCyclesBudgetsAndBatches(t *testing.T) {
	tuples := []tuplefacts.Tuple{
		grant,
		tupleFact("group", "g", "member", "user", "alice", ""),
		tupleFact("group", "h", "member", "group", "g", "member"),
		tupleFact("group", "g", "member", "group", "h", "member"),
		tupleFact("space", "shared", "viewer", "group", "h", "member"),
		tupleFact("space", "admin-only", "viewer", "group", "g", "admin"),
		tupleFact("project", "p", "owner", "group", "h", "member"),
	}
	c, s := fixture(t, tuples, memory.NewTupleCache())
	poll(t, c)
	wide := relationships.NewReadModel([]relationships.SubjectRule{
		{ResourceType: "space", Relation: "viewer", SubjectType: "user"},
		{ResourceType: "group", Relation: "member", SubjectType: "user"},
		{ResourceType: "group", Relation: "member", SubjectType: "group", SubjectRelation: "member"},
		{ResourceType: "space", Relation: "viewer", SubjectType: "group", SubjectRelation: "member"},
		{ResourceType: "space", Relation: "viewer", SubjectType: "group", SubjectRelation: "admin"},
		{ResourceType: "project", Relation: "owner", SubjectType: "group", SubjectRelation: "member"},
	})
	for mi, model := range []relationships.ReadModel{wide, directModel, {}} {
		for _, limit := range []int{0, 1, 2, 5, 6, 100} {
			t.Run(fmt.Sprintf("model%d/limit%d", mi, limit), func(t *testing.T) {
				ids := []string{"s", "shared", "admin-only", "absent", "shared"}
				want, wantErr := s.store.Relationships().ForModel(model).CheckBatchDirect(t.Context(), "space", ids, "viewer", "user", "alice", limit)
				var got map[string]bool
				err := c.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
					var err error
					got, err = graph(reads, model).CheckBatchDirect(ctx, "space", ids, "viewer", "user", "alice", limit)
					return err
				})
				if !errors.Is(err, wantErr) || !reflect.DeepEqual(got, want) {
					t.Fatalf("cached %v/%v durable %v/%v", got, err, want, wantErr)
				}
			})
		}
	}
	if s.durable != 0 {
		t.Fatal("parity tests silently fell back")
	}
	if err := c.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		r := graph(reads, wide).(relationships.RelationSetReader)
		got, err := r.RelationTargetsFor(ctx, "space", []string{"shared", "shared", "absent"}, "viewer")
		if err != nil || len(got["shared"]) != 1 || len(got["absent"]) != 0 {
			t.Fatalf("duplicate/empty forward sets: %v/%v", got, err)
		}
		got["shared"][0].ID = "caller mutation"
		again, err := r.RelationTargetsFor(ctx, "space", []string{"shared"}, "viewer")
		if err != nil || again["shared"][0].ID != "h" {
			t.Fatal("caller corrupted operation memo")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyBindingStartupAndWake(t *testing.T) {
	s := &source{store: memory.New()}
	b := memory.NewTupleCache()
	for _, policy := range []tuplecache.Policy{{MaxStaleness: -1}, {MaxStaleness: time.Second, PollInterval: time.Second}, {MaxStaleness: time.Second, ReadTimeout: -1}} {
		if _, err := tuplecache.New(s, b, tuplecache.WithPolicy(policy)); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	c, s := fixture(t, nil, b)
	if c.Binding() != "protocol:2/test-store/max-staleness:1000000000" {
		t.Fatalf("protocol is absent from shared binding: %q", c.Binding())
	}
	c.Notify()
	c.Notify()
	select {
	case <-c.WakeChannel():
	default:
		t.Fatal("missing wake")
	}
	select {
	case <-c.WakeChannel():
		t.Fatal("wake not coalesced")
	default:
	}
	_ = c.Close()
	// A correct backend never returns another binding's mirror; one that does is
	// corrupt, so the runtime refuses it and stays durable.
	foreign, s := fixture(t, nil, foreignBackend{Backend: b})
	if err := foreign.Poll(t.Context()); !errors.Is(err, tuplecache.ErrBinding) {
		t.Fatal(err)
	}
	allowed(t, foreign, false)
	if s.durable != 1 {
		t.Fatal("foreign mirror read")
	}
	c.Notify()
	select {
	case <-c.WakeChannel():
		t.Fatal("closed runtime emitted wake")
	default:
	}
}

// foreignBackend reports another binding's mirror for every key.
type foreignBackend struct{ tuplecache.Backend }

func (foreignBackend) State(context.Context, string) (tuplecache.State, error) {
	return tuplecache.State{Binding: "other-store", Receipt: "x"}, nil
}

func TestDifferentFreshnessPoliciesUseSeparateMirrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := memory.NewTupleCache()
		short, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
		poll(t, short)
		long, err := tuplecache.New(s, backend, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: 5 * time.Minute}))
		if err != nil {
			t.Fatal(err)
		}
		defer long.Close()
		poll(t, long)
		if long.Stats().Rebuilds != 1 {
			t.Fatalf("long policy did not build its own mirror: %+v", long.Stats())
		}
		allowed(t, short, true)
		allowed(t, long, true)
		revoke(t, s)
		poll(t, short)
		allowed(t, short, false)
		allowed(t, long, true) // long's own mirror, within its accepted lag.
		poll(t, long)
		allowed(t, long, false)
		if s.durable != 0 {
			t.Fatalf("ready runtimes fell back to durable reads: %d", s.durable)
		}
	})
}

// noFullAt is a backend that does not report State.FullAt.
type noFullAt struct{ tuplecache.Backend }

func (b noFullAt) State(ctx context.Context, binding string) (tuplecache.State, error) {
	state, err := b.Backend.State(ctx, binding)
	state.FullAt = time.Time{}
	return state, err
}

func TestFullRebuildEveryValidation(t *testing.T) {
	s := &source{store: memory.New()}
	for _, policy := range []tuplecache.Policy{
		{MaxStaleness: time.Second, FullRebuildEvery: -1},
		{MaxStaleness: time.Second, FullRebuildEvery: 100 * time.Millisecond}, // below the defaulted 250ms PollInterval
		{MaxStaleness: time.Second, PollInterval: 500 * time.Millisecond, FullRebuildEvery: 400 * time.Millisecond},
	} {
		if _, err := tuplecache.New(s, memory.NewTupleCache(), tuplecache.WithPolicy(policy)); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("%+v: %v", policy, err)
		}
	}
	for _, every := range []time.Duration{0, 250 * time.Millisecond, time.Hour} {
		c, err := tuplecache.New(s, memory.NewTupleCache(), tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second, FullRebuildEvery: every}))
		if err != nil {
			t.Fatalf("FullRebuildEvery %v: %v", every, err)
		}
		_ = c.Close()
	}
	// FullRebuildEvery is not part of the binding.
	a, _ := tuplecache.New(s, memory.NewTupleCache(), tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second}))
	b, _ := tuplecache.New(s, memory.NewTupleCache(), tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second, FullRebuildEvery: time.Hour}))
	if a.Binding() != b.Binding() {
		t.Fatalf("binding includes FullRebuildEvery: %q vs %q", a.Binding(), b.Binding())
	}
}

func TestFullRebuildEveryFollowsTheMirrorClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second, FullRebuildEvery: 2 * time.Second})
		backend := memory.NewTupleCache()
		first, s := fixture(t, []tuplefacts.Tuple{grant}, backend, policy)
		poll(t, first)
		if st := first.Stats(); st.Rebuilds != 1 || st.ScheduledRebuilds != 0 || st.LastFullRebuild.IsZero() {
			t.Fatalf("bootstrap: %+v", st)
		}
		state, _ := backend.State(t.Context(), first.Binding())
		if state.FullAt.IsZero() {
			t.Fatal("memory backend did not report FullAt")
		}
		second, err := tuplecache.New(s, backend, policy)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		time.Sleep(time.Second)
		poll(t, first)
		poll(t, second)
		if first.Stats().Rebuilds != 1 || second.Stats().Rebuilds != 0 {
			t.Fatalf("rebuilt before the period: %+v / %+v", first.Stats(), second.Stats())
		}
		time.Sleep(time.Second)
		poll(t, first)
		poll(t, second)
		if st := first.Stats(); st.Rebuilds != 2 || st.ScheduledRebuilds != 1 {
			t.Fatalf("period elapsed without a scheduled rebuild: %+v", st)
		}
		if second.Stats().Rebuilds != 0 {
			t.Fatalf("second replica rebuilt the same period: %+v", second.Stats())
		}
		if next, _ := backend.State(t.Context(), first.Binding()); !next.FullAt.After(state.FullAt) {
			t.Fatalf("FullAt did not advance: %v -> %v", state.FullAt, next.FullAt)
		}
		allowed(t, second, true)
		// Deltas between rebuilds still publish without promotion.
		revoke(t, s)
		poll(t, second)
		allowed(t, second, false)
		if second.Stats().Rebuilds != 0 {
			t.Fatalf("delta promoted to a rebuild: %+v", second.Stats())
		}
	})
}

func TestFullRebuildEveryWithoutFullAtUsesTheProcessClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second, FullRebuildEvery: 2 * time.Second})
		backend := noFullAt{memory.NewTupleCache()}
		c, s := fixture(t, []tuplefacts.Tuple{grant}, backend, policy)
		poll(t, c)
		time.Sleep(time.Second)
		poll(t, c)
		if st := c.Stats(); st.Rebuilds != 1 {
			t.Fatalf("rebuilt before the period: %+v", st)
		}
		// A replica started now has no history: it waits a period, not boot.
		restarted, err := tuplecache.New(s, backend, policy)
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		poll(t, restarted)
		if restarted.Stats().Rebuilds != 0 {
			t.Fatalf("restart rebuilt at boot: %+v", restarted.Stats())
		}
		time.Sleep(time.Second)
		poll(t, c)
		if st := c.Stats(); st.Rebuilds != 2 || st.ScheduledRebuilds != 1 {
			t.Fatalf("period elapsed without a scheduled rebuild: %+v", st)
		}
		time.Sleep(time.Second)
		poll(t, restarted)
		if st := restarted.Stats(); st.ScheduledRebuilds != 1 {
			t.Fatalf("restarted replica never rebuilt on its own clock: %+v", st)
		}
	})
}

// slowFull is a source whose full snapshot outlasts MaxStaleness while slow.
type slowFull struct {
	*source
	slow bool
}

func (s *slowFull) Snapshot(ctx context.Context, receipt string) (tuplecache.Snapshot, error) {
	if receipt == "" && s.slow {
		time.Sleep(1500 * time.Millisecond)
	}
	return s.source.Snapshot(ctx, receipt)
}

func TestFailedScheduledRebuildBacksOffSoDeltasKeepPublishing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &source{store: memory.New(), tuples: []tuplefacts.Tuple{grant}}
		if err := base.store.Tuples().ApplyTuples(t.Context(), tuplefacts.Changes{Add: base.tuples}); err != nil {
			t.Fatal(err)
		}
		s := &slowFull{source: base}
		// PollInterval defaults to 250ms, so the backoff is min(2s, 8×250ms) = 2s.
		c, err := tuplecache.New(s, memory.NewTupleCache(), tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second, FullRebuildEvery: 2 * time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		poll(t, c)
		s.slow = true
		time.Sleep(2 * time.Second)
		revoke(t, base)
		if err := c.Poll(t.Context()); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("promoted rebuild over MaxStaleness: %v", err)
		}
		if st := c.Stats(); st.ScheduledRebuildFailures != 1 || st.ScheduledRebuilds != 0 {
			t.Fatalf("failure not counted: %+v", st)
		}
		time.Sleep(250 * time.Millisecond)
		poll(t, c) // inside the backoff: a delta
		if len(base.pending) != 0 {
			t.Fatal("revocation delta was not published during the backoff")
		}
		durable := base.durable
		allowed(t, c, false)
		if base.durable != durable {
			t.Fatal("revocation was not served from the mirror")
		}
		time.Sleep(time.Second)
		poll(t, c) // still inside the backoff
		if st := c.Stats(); st.ScheduledRebuildFailures != 1 {
			t.Fatalf("promoted again inside the backoff: %+v", st)
		}
		time.Sleep(time.Second)
		if err := c.Poll(t.Context()); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("promotion after the backoff: %v", err)
		}
		if st := c.Stats(); st.ScheduledRebuildFailures != 2 {
			t.Fatalf("second failure not counted: %+v", st)
		}
		s.slow = false
		time.Sleep(2 * time.Second)
		poll(t, c)
		if st := c.Stats(); st.ScheduledRebuilds != 1 || st.ScheduledRebuildFailures != 2 {
			t.Fatalf("recovered rebuild: %+v", st)
		}
	})
}

// renamedSource is a source whose identity row changed, e.g. after a restore.
type renamedSource struct{ *source }

func (renamedSource) Binding() string { return "restored-store" }

func TestSourceIdentityChangeUsesNewMirror(t *testing.T) {
	backend := memory.NewTupleCache()
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	poll(t, c)
	old, err := backend.State(t.Context(), c.Binding())
	if err != nil {
		t.Fatal(err)
	}
	moved, err := tuplecache.New(renamedSource{s}, backend, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	poll(t, moved)
	allowed(t, moved, true)
	if moved.Stats().Rebuilds != 1 || moved.Binding() == c.Binding() {
		t.Fatalf("new identity did not rebuild its own mirror: %+v", moved.Stats())
	}
	if left, err := backend.State(t.Context(), c.Binding()); err != nil || left != old {
		t.Fatalf("old mirror changed: %+v/%v", left, err)
	}
}

func TestSourceIdentityChangeDetachesTheRelay(t *testing.T) {
	identity := fmt.Errorf("store: %w", tuplecache.ErrSourceIdentity)
	terminal := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, tuplecache.ErrSourceIdentity) || !errors.Is(err, workers.ErrPoolShutdown) {
			t.Fatalf("poll after identity change: %v", err)
		}
	}
	t.Run("snapshot", func(t *testing.T) {
		c, s := fixture(t, []tuplefacts.Tuple{grant}, memory.NewTupleCache())
		poll(t, c)
		allowed(t, c, true)
		s.snapshotErr = identity
		terminal(t, c.Poll(t.Context()))
		first := c.Stats()
		if !first.Detached || first.PollFailures != 1 || first.LastPoll.FailureStage != "snapshot" {
			t.Fatalf("identity change did not detach: %+v", first)
		}
		snapshots := s.snapshots
		terminal(t, c.Poll(t.Context()))
		if again := c.Stats(); again.PollFailures != 1 || again.LastPoll != first.LastPoll || s.snapshots != snapshots {
			t.Fatalf("detached poll was counted or reached the source: %+v", again)
		}
		durable, hits := s.durable, first.Hits
		allowed(t, c, true)
		if got := c.Stats(); s.durable != durable+1 || got.Fallbacks != first.Fallbacks+1 || got.Hits != hits {
			t.Fatalf("detached check did not fall back to the source: %+v", got)
		}
		if err := c.Rebuild(t.Context()); !errors.Is(err, tuplecache.ErrSourceIdentity) || errors.Is(err, workers.ErrPoolShutdown) {
			t.Fatalf("detached rebuild: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if err := c.Poll(t.Context()); !errors.Is(err, tuplecache.ErrUnavailable) {
			t.Fatalf("closed poll: %v", err)
		}
	})
	t.Run("acknowledgement", func(t *testing.T) {
		c, s := fixture(t, []tuplefacts.Tuple{grant}, memory.NewTupleCache())
		poll(t, c)
		revoke(t, s)
		s.ackErr = identity
		terminal(t, c.Poll(t.Context()))
		if stats := c.Stats(); !stats.Detached || stats.LastPoll.FailureStage != "acknowledgement" {
			t.Fatalf("acknowledgement identity change did not detach: %+v", stats)
		}
		allowed(t, c, false)
	})
	t.Run("rebuild", func(t *testing.T) {
		c, s := fixture(t, []tuplefacts.Tuple{grant}, memory.NewTupleCache())
		s.snapshotErr = identity
		if err := c.Rebuild(t.Context()); !errors.Is(err, tuplecache.ErrSourceIdentity) || errors.Is(err, workers.ErrPoolShutdown) {
			t.Fatalf("rebuild after identity change: %v", err)
		}
		if !c.Stats().Detached {
			t.Fatal("rebuild did not detach")
		}
		terminal(t, c.Poll(t.Context()))
	})
	t.Run("pool", func(t *testing.T) {
		c, s := fixture(t, []tuplefacts.Tuple{grant}, memory.NewTupleCache())
		poll(t, c)
		s.snapshotErr = identity
		logs := &recordingHandler{}
		pool := workers.NewPool(c.Poll, workers.WithWorkerCount(1), workers.WithPollInterval(time.Millisecond), workers.WithLogger(slog.New(logs)))
		done := make(chan error, 1)
		go func() { done <- pool.Run(t.Context()) }()
		select {
		case err := <-done:
			if !errors.Is(err, tuplecache.ErrSourceIdentity) {
				t.Fatalf("Run returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("pool did not stop after the identity change")
		}
		errorsLogged := logs.matching(slog.LevelError, "")
		if len(errorsLogged) != 1 || errorsLogged[0].Message != "worker requesting pool shutdown" {
			t.Fatalf("want one shutdown ERROR, got %d: %+v", len(errorsLogged), errorsLogged)
		}
		var cause error
		errorsLogged[0].Attrs(func(a slog.Attr) bool {
			if a.Key == "error" {
				cause, _ = a.Value.Any().(error)
			}
			return true
		})
		if !errors.Is(cause, tuplecache.ErrSourceIdentity) {
			t.Fatalf("shutdown ERROR error attribute: %v", cause)
		}
		if stopped := logs.matching(slog.LevelInfo, "worker pool stopped"); len(stopped) != 1 {
			t.Fatalf("want one pool-stopped INFO, got %d", len(stopped))
		}
		if got := pool.Stats().Errors; got != 1 {
			t.Fatalf("pool errors = %d, want 1", got)
		}
	})
}

type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }
func (h *recordingHandler) matching(level slog.Level, message string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Level == level && (message == "" || r.Message == message) {
			out = append(out, r)
		}
	}
	return out
}

func TestZeroMaxStalenessSelectsDefault(t *testing.T) {
	c, err := tuplecache.New(&source{store: memory.New()}, memory.NewTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if want := fmt.Sprintf("protocol:2/test-store/max-staleness:%d", tuplecache.DefaultMaxStaleness); c.Binding() != want || tuplecache.DefaultMaxStaleness != 5*time.Second {
		t.Fatalf("binding %q, want %q", c.Binding(), want)
	}
	if c.PollInterval() != time.Second {
		t.Fatalf("default poll interval %v, want 1s", c.PollInterval())
	}
	short, err := tuplecache.New(&source{store: memory.New()}, memory.NewTupleCache(), tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: 2 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer short.Close()
	if short.PollInterval() != 500*time.Millisecond {
		t.Fatalf("poll interval %v, want MaxStaleness/4", short.PollInterval())
	}
}

func TestRestartWithUnchangedBindingResumesDeltas(t *testing.T) {
	backend := memory.NewTupleCache()
	c, s := fixture(t, []tuplefacts.Tuple{grant}, backend)
	poll(t, c)
	_ = c.Close()
	restarted, err := tuplecache.New(s, backend, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	revoke(t, s)
	poll(t, restarted)
	allowed(t, restarted, false)
	if restarted.Stats().Rebuilds != 0 {
		t.Fatalf("restart with unchanged binding rebuilt: %+v", restarted.Stats())
	}
}

func tupleFact(rt, rid, rel, st, sid, sr string) tuplefacts.Tuple {
	return tuplefacts.Tuple{Scope: tuplefacts.On(rt, rid), Relation: rel, Subject: tuplefacts.SubjectRef{Type: st, ID: sid, Relation: sr}}
}
func graph(reader tuplecache.CheckReads, model relationships.ReadModel) relationships.CheckReader {
	return reader.(relationships.CheckReadSource).ForChecks(model)
}
