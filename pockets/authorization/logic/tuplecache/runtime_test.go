package tuplecache_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

type source struct {
	store                    *memory.Store
	receipt                  string
	tuples                   []relationships.CreateRelationship
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
	return s.store.ReadSnapshot(ctx, fn)
}
func fixture(t testing.TB, tuples []relationships.CreateRelationship, backend tuplecache.Backend) (*tuplecache.TupleCache, *source) {
	t.Helper()
	s := &source{store: memory.New(), tuples: tuples}
	if err := s.store.Relationships().CreateRelationships(t.Context(), tuples); err != nil {
		t.Fatal(err)
	}
	c, err := tuplecache.New(s, backend, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, s
}

var directModel = relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "space", Relation: "viewer", SubjectType: "user"}})
var grant = relationships.CreateRelationship{ResourceType: "space", ResourceID: "s", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}

func allowed(t *testing.T, c *tuplecache.TupleCache, want bool) {
	t.Helper()
	var got bool
	err := c.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		var err error
		got, err = reads.ForChecks(directModel).CheckRelationWithGroupExpansion(ctx, "space", "s", "viewer", "user", "alice", 100)
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
	if err := s.store.Relationships().DeleteRelationshipTarget(t.Context(), "space", "s", "viewer", grant.Subject()); err != nil {
		t.Fatal(err)
	}
	s.tuples = nil
	s.pending = []tuplecache.Change{{ID: "revoke", Before: &grant}}
}

func TestBootstrapDeliveryAndRecovery(t *testing.T) {
	backend := memory.NewTupleCache()
	c, s := fixture(t, []relationships.CreateRelationship{grant}, backend)
	allowed(t, c, true)
	if s.durable != 1 {
		t.Fatal("uninitialized cache served data")
	}
	poll(t, c)
	allowed(t, c, true)
	if s.durable != 1 || c.Stats().Hits != 1 {
		t.Fatalf("warm read: %+v", c.Stats())
	}
	before, _ := backend.State(t.Context())
	poll(t, c)
	after, _ := backend.State(t.Context())
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
	c, s := fixture(t, []relationships.CreateRelationship{grant}, backend)
	poll(t, c)
	old, _ := backend.State(t.Context())
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
	state, _ := backend.State(t.Context())
	if err := backend.Publish(t.Context(), state, old, tuplecache.Snapshot{Full: true, Tuples: []relationships.CreateRelationship{grant}}, time.Second); err != nil {
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

func (b *hookedBackend) Read(ctx context.Context, state tuplecache.State, keys []tuplecache.SetKey) ([][]relationships.SubjectRef, error) {
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
	c, s := fixture(t, []relationships.CreateRelationship{grant}, backend)
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
		c, s := fixture(t, []relationships.CreateRelationship{grant}, memory.NewTupleCache())
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

func TestCallbackLifetimePanicCancellationAndRoleFallback(t *testing.T) {
	c, s := fixture(t, []relationships.CreateRelationship{grant}, memory.NewTupleCache())
	poll(t, c)
	var escaped relationships.CheckReader
	func() {
		defer func() {
			if recover() != "callback panic" {
				t.Fatal("panic swallowed")
			}
		}()
		_ = c.Run(t.Context(), func(_ context.Context, reads tuplecache.CheckReads) error {
			escaped = reads.ForChecks(directModel)
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
		_, err := reads.ForChecks(directModel).GetRelationTargets(context.Background(), "space", "s", "viewer")
		return err
	})
	if !errors.Is(err, context.Canceled) || s.durable != 0 {
		t.Fatalf("cancel: %v durable=%d", err, s.durable)
	}
	if err := s.store.Roles().Assign(t.Context(), roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	var hasRole bool
	if err := c.Run(t.Context(), func(ctx context.Context, reads tuplecache.CheckReads) error {
		var err error
		hasRole, err = reads.HasExactRole(ctx, "user", "alice", "admin", "", "")
		return err
	}); err != nil || !hasRole || s.durable != 1 {
		t.Fatalf("durable roles: %v/%v/%d", hasRole, err, s.durable)
	}
	s.ambient = true
	if err := c.Run(t.Context(), func(_ context.Context, reads tuplecache.CheckReads) error {
		if reads != nil {
			t.Fatal("ambient callback did not use caller view")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRawReaderParityModelsCyclesBudgetsAndBatches(t *testing.T) {
	tuples := []relationships.CreateRelationship{
		grant,
		{ResourceType: "group", ResourceID: "g", Relation: "member", SubjectType: "user", SubjectID: "alice"},
		{ResourceType: "group", ResourceID: "h", Relation: "member", SubjectType: "group", SubjectID: "g", SubjectRelation: "member"},
		{ResourceType: "group", ResourceID: "g", Relation: "member", SubjectType: "group", SubjectID: "h", SubjectRelation: "member"},
		{ResourceType: "space", ResourceID: "shared", Relation: "viewer", SubjectType: "group", SubjectID: "h", SubjectRelation: "member"},
		{ResourceType: "space", ResourceID: "admin-only", Relation: "viewer", SubjectType: "group", SubjectID: "g", SubjectRelation: "admin"},
		{ResourceType: "project", ResourceID: "p", Relation: "owner", SubjectType: "group", SubjectID: "h", SubjectRelation: "member"},
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
					got, err = reads.ForChecks(model).CheckBatchDirect(ctx, "space", ids, "viewer", "user", "alice", limit)
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
		r := reads.ForChecks(wide).(relationships.RelationSetReader)
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
	for _, policy := range []tuplecache.Policy{{}, {MaxStaleness: -1}, {MaxStaleness: time.Second, PollInterval: time.Second}, {MaxStaleness: time.Second, ReadTimeout: -1}} {
		if _, err := tuplecache.New(s, b, tuplecache.WithPolicy(policy)); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	c, s := fixture(t, nil, b)
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
	if err := b.Publish(t.Context(), tuplecache.State{}, tuplecache.State{Binding: "other-store", Receipt: "x"}, tuplecache.Snapshot{Full: true}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.Poll(t.Context()); !errors.Is(err, tuplecache.ErrBinding) {
		t.Fatal(err)
	}
	allowed(t, c, false)
	if s.durable != 1 {
		t.Fatal("mismatched store binding read")
	}
	_ = c.Close()
	c.Notify()
	select {
	case <-c.WakeChannel():
		t.Fatal("closed runtime emitted wake")
	default:
	}
}

func TestSharedMirrorRejectsDifferentFreshnessPolicies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := memory.NewTupleCache()
		short, s := fixture(t, []relationships.CreateRelationship{grant}, backend)
		poll(t, short)
		long, err := tuplecache.New(s, backend, tuplecache.WithPolicy(tuplecache.Policy{MaxStaleness: 5 * time.Minute}))
		if err != nil {
			t.Fatal(err)
		}
		defer long.Close()
		if err := long.Poll(t.Context()); !errors.Is(err, tuplecache.ErrBinding) {
			t.Fatalf("peer extended shared eligibility: %v", err)
		}
		revoke(t, s)
		time.Sleep(time.Second)
		allowed(t, short, false)
		allowed(t, long, false)
		if s.durable != 2 {
			t.Fatal("mismatched policy or expired runtime served stale grant")
		}
	})
}
