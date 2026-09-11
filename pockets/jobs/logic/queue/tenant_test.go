package queue_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	queueapi "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"

	schedule "github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
)

// TestEnqueueJob_CarriesTenant proves the struct-input unfenced surface threads
// the optional tenant slot through to the stored job with no signature change,
// and that the primitive-typed queueapi.Enqueue stays tenant-less.
func TestEnqueueJob_CarriesTenant(t *testing.T) {
	ctx := context.Background()
	queue := memory.NewQueue()
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: queue})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	scoped, err := svc.Queue.EnqueueJob(ctx, queueapi.Enqueue{Kind: "demo.print", TenantID: "acme"})
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if scoped.TenantID != "acme" {
		t.Errorf("EnqueueJob TenantID = %q, want acme", scoped.TenantID)
	}
	stored, err := queue.Get(ctx, scoped.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.TenantID != "acme" {
		t.Errorf("stored TenantID = %q, want acme", stored.TenantID)
	}

	id, err := svc.Queue.Enqueue(ctx, "demo.print", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	plain, err := queue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if plain.TenantID != "" {
		t.Errorf("primitive queueapi.Enqueue TenantID = %q, want empty", plain.TenantID)
	}
}

// TestEnsureSchedule_CarriesTenant proves the schedule struct input carries the
// tenant slot through EnsureSchedule with no signature change.
func TestEnsureSchedule_CarriesTenant(t *testing.T) {
	ctx := context.Background()
	schedules := memory.NewSchedules()
	svc, err := pocketjobs.New(pocketjobs.Repositories{Queue: memory.NewQueue(), Schedules: schedules})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	sch, err := svc.Schedules.EnsureSchedule(ctx, schedule.Ensure{Name: "nightly", Kind: "demo.print", TenantID: "acme", Spec: schedule.Spec{Every: time.Hour}})
	if err != nil {
		t.Fatalf("EnsureSchedule: %v", err)
	}
	if sch.TenantID != "acme" {
		t.Errorf("EnsureSchedule TenantID = %q, want acme", sch.TenantID)
	}
	got, err := schedules.Get(ctx, sch.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TenantID != "acme" {
		t.Errorf("stored TenantID = %q, want acme", got.TenantID)
	}
}

// TestFencedScopedSiblings proves the struct-input fenced siblings stamp the
// tenant and the FROZEN positional protocol methods delegate to them with an
// empty tenant — the work protocol's vocabulary is untouched.
func TestFencedScopedSiblings(t *testing.T) {
	ctx := context.Background()
	svc, fq := newFencedService(t)

	scopedID, err := svc.Queue.EnqueueOnceIn(ctx, queueapi.EnqueueOnceInput{Kind: "delivery", LogicalKey: "key-scoped", Payload: []byte(`"a"`), TenantID: "acme"})
	if err != nil {
		t.Fatalf("EnqueueOnceIn: %v", err)
	}
	scoped, err := fq.Get(ctx, scopedID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if scoped.TenantID != "acme" {
		t.Errorf("EnqueueOnceIn TenantID = %q, want acme", scoped.TenantID)
	}

	replacedID, err := svc.Queue.ReplaceIn(ctx, queueapi.ReplaceInput{Kind: "delivery", LogicalKey: "key-scoped", Payload: []byte(`"b"`), TenantID: "acme"})
	if err != nil {
		t.Fatalf("ReplaceIn: %v", err)
	}
	if replacedID == scopedID {
		t.Fatalf("ReplaceIn returned the superseded id %q", replacedID)
	}
	replaced, err := fq.Get(ctx, replacedID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if replaced.TenantID != "acme" {
		t.Errorf("ReplaceIn TenantID = %q, want acme", replaced.TenantID)
	}

	// The frozen positional protocol methods delegate with no tenant.
	plainID, err := svc.Queue.EnqueueOnce(ctx, "delivery", "key-plain", []byte(`"c"`))
	if err != nil {
		t.Fatalf("EnqueueOnce: %v", err)
	}
	plain, err := fq.Get(ctx, plainID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if plain.TenantID != "" {
		t.Errorf("EnqueueOnce TenantID = %q, want empty", plain.TenantID)
	}
	plainReplacedID, err := svc.Queue.Replace(ctx, "delivery", "key-plain", []byte(`"d"`))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	plainReplaced, err := fq.Get(ctx, plainReplacedID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if plainReplaced.TenantID != "" {
		t.Errorf("Replace TenantID = %q, want empty", plainReplaced.TenantID)
	}
}

// TestFencedClaimCarriesTenant proves the runtime hands the handler the claimed
// execution's tenant, so a handler sees the scope of what it claimed.
func TestFencedClaimCarriesTenant(t *testing.T) {
	ctx := context.Background()
	svc, _ := newFencedService(t)

	var (
		mu      sync.Mutex
		tenants []string
	)
	rt, err := queueapi.NewFencedRuntime(svc.Queue, map[string]queueapi.FencedHandlerFunc{
		"delivery": func(_ context.Context, claim queueapi.FencedClaim) error {
			mu.Lock()
			tenants = append(tenants, claim.TenantID)
			mu.Unlock()
			return nil
		},
	}, queueapi.WithFencedRuntimePolicy(queueapi.FencedRuntimePolicy{Workers: 1, PollInterval: 5 * time.Millisecond, IdleInterval: 5 * time.Millisecond}))
	if err != nil {
		t.Fatalf("NewFencedRuntime: %v", err)
	}

	if _, err := svc.Queue.EnqueueOnceIn(ctx, queueapi.EnqueueOnceInput{Kind: "delivery", LogicalKey: "key-tenant", Payload: []byte(`"opaque"`), TenantID: "acme"}); err != nil {
		t.Fatalf("EnqueueOnceIn: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(runCtx) }()
	waitFor(t, time.Second, func() bool {
		s, err := svc.Queue.LatestStatusByKey(ctx, "key-tenant")
		return err == nil && s == "completed"
	})
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(tenants) == 0 {
		t.Fatal("handler never ran")
	}
	if tenants[0] != "acme" {
		t.Errorf("queueapi.FencedClaim.TenantID = %q, want acme", tenants[0])
	}
}
