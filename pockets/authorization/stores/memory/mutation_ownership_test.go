package memory

import (
	"context"
	"errors"
	"sync"
	"testing"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
)

func TestGuardianOptionSnapshotsPolicy(t *testing.T) {
	policy := mutation.GuardianPolicy{Rules: []mutation.GuardianRule{{ResourceType: "doc", Relation: "owner", MinAnchors: 1}}}
	option := WithGuardianPolicy(policy)
	policy.Rules[0].ResourceType = "changed-before-construction"
	first, second := New(option), New(option)
	for _, store := range []*Store{first, second} {
		snapshot := store.Mutations().GuardianPolicy()
		snapshot.Rules[0].ResourceType = "changed-snapshot"
		ctx := context.Background()
		cmd := grantOwner(t, "d1", "owner")
		if result, err := store.Mutations().Apply(ctx, cmd, nil); err != nil || result.Outcome != mutation.OutcomeApplied {
			t.Fatalf("establish: %+v, %v", result, err)
		}
		cmd.Operation = mutation.OpRevoke
		if result, err := store.Mutations().Apply(ctx, cmd, nil); !errors.Is(err, mutation.ErrInvariantBlocked) || result != nil {
			t.Fatalf("caller changed guardian policy: %+v, %v", result, err)
		}
	}
	first.mut.guardian.Rules[0].ResourceType = "first-store-only"
	if got := second.mut.guardian.MinDirectAnchors("doc", "owner"); got != 1 {
		t.Fatalf("reused option shared policy between stores: %d", got)
	}
}

// The first Err call signals that Apply passed its entry check before it waits
// on the already-held mutex. Returning nil for that call makes the lock-wait
// interleaving deterministic, without a sleep or a production test hook.
type lockWaitContext struct {
	context.Context
	once    sync.Once
	checked chan struct{}
}

func (c *lockWaitContext) Err() error {
	first := false
	c.once.Do(func() { first = true; close(c.checked) })
	if first {
		return nil
	}
	return c.Context.Err()
}

func TestMutationCancellationWhileWaitingForLock(t *testing.T) {
	store := New()
	cmd := grantOwner(t, "d1", "owner")
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &lockWaitContext{Context: base, checked: make(chan struct{})}
	store.mut.st.mu.Lock()
	done := make(chan error, 1)
	go func() { _, err := store.Mutations().Apply(ctx, cmd, nil); done <- err }()
	<-ctx.checked
	cancel()
	store.mut.st.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter committed: %v", err)
	}
	result, err := store.Mutations().Apply(context.Background(), cmd, nil)
	if err != nil || result.Outcome != mutation.OutcomeApplied {
		t.Fatalf("canceled mutation changed state: %+v, %v", result, err)
	}
}
