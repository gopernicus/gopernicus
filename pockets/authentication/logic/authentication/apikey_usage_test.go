package authentication

import (
	"context"
	"errors"
	"testing"
	"time"
)

// usageHarness mints one key on a machine harness whose clock the test moves.
func usageHarness(t *testing.T, interval time.Duration) (*machineHarness, string, *time.Time) {
	t.Helper()
	h := newMachineHarness(t)
	clock := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	h.svc.now = func() time.Time { return clock }
	h.svc.lastUsed = newLastUsedThrottle(interval)
	ctx := context.Background()
	sa, err := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, raw, err := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	return h, raw, &clock
}

func authN(t *testing.T, h *machineHarness, raw string, n int) {
	t.Helper()
	for range n {
		if _, _, ok, _ := h.svc.verifyAPIKeyCredential(context.Background(), raw); !ok {
			t.Fatal("valid key denied")
		}
	}
}

func touches(h *machineHarness) int {
	h.keys.mu.Lock()
	defer h.keys.mu.Unlock()
	return h.keys.touched
}

// TestAPIKeyLastUsedThrottled: many requests inside one interval write
// LastUsedAt once; the first request past the interval writes it again.
func TestAPIKeyLastUsedThrottled(t *testing.T) {
	h, raw, clock := usageHarness(t, time.Hour)
	authN(t, h, raw, 100)
	if got := touches(h); got != 1 {
		t.Fatalf("TouchLastUsed calls after 100 requests in one interval = %d, want 1", got)
	}
	*clock = clock.Add(59 * time.Minute)
	authN(t, h, raw, 10)
	if got := touches(h); got != 1 {
		t.Fatalf("TouchLastUsed calls inside the interval = %d, want 1", got)
	}
	*clock = clock.Add(time.Minute)
	authN(t, h, raw, 10)
	if got := touches(h); got != 2 {
		t.Fatalf("TouchLastUsed calls after the interval = %d, want 2", got)
	}
}

// TestAPIKeyLastUsedPerKey: two keys are throttled independently.
func TestAPIKeyLastUsedPerKey(t *testing.T) {
	h, raw, _ := usageHarness(t, time.Hour)
	sa, _ := h.svc.CreateServiceAccount(context.Background(), "admin", "bot2", "", false, "")
	_, raw2, _ := h.svc.MintAPIKey(context.Background(), sa.ID, "k2", time.Time{})
	authN(t, h, raw, 5)
	authN(t, h, raw2, 5)
	if got := touches(h); got != 2 {
		t.Fatalf("TouchLastUsed calls for two keys = %d, want 2", got)
	}
}

// TestAPIKeyLastUsedOptOut: a negative interval never writes LastUsedAt.
func TestAPIKeyLastUsedOptOut(t *testing.T) {
	h, raw, clock := usageHarness(t, -1)
	authN(t, h, raw, 20)
	*clock = clock.Add(48 * time.Hour)
	authN(t, h, raw, 20)
	if got := touches(h); got != 0 {
		t.Fatalf("TouchLastUsed calls with usage writes off = %d, want 0", got)
	}
}

// TestAPIKeyLastUsedDefaultInterval: zero selects the 24-hour default.
func TestAPIKeyLastUsedDefaultInterval(t *testing.T) {
	if got := newLastUsedThrottle(0).interval; got != 24*time.Hour {
		t.Fatalf("default interval = %s, want 24h", got)
	}
	h, raw, clock := usageHarness(t, 0)
	authN(t, h, raw, 3)
	*clock = clock.Add(23 * time.Hour)
	authN(t, h, raw, 3)
	if got := touches(h); got != 1 {
		t.Fatalf("TouchLastUsed calls within 24h = %d, want 1", got)
	}
	*clock = clock.Add(time.Hour)
	authN(t, h, raw, 1)
	if got := touches(h); got != 2 {
		t.Fatalf("TouchLastUsed calls at 24h = %d, want 2", got)
	}
}

// TestAPIKeyLastUsedFailureNotRetried: a failing touch keeps authentication
// working and is not retried on every request inside the interval.
func TestAPIKeyLastUsedFailureNotRetried(t *testing.T) {
	h, raw, _ := usageHarness(t, time.Hour)
	var attempts int
	h.keys.touchErr = errors.New("store down")
	h.keys.onTouch = func() { attempts++ }
	authN(t, h, raw, 50)
	if attempts != 1 {
		t.Fatalf("TouchLastUsed attempts during an outage = %d, want 1", attempts)
	}
}

// TestLastUsedThrottleSweeps: past the size threshold, an admission prunes
// marks older than the interval.
func TestLastUsedThrottleSweeps(t *testing.T) {
	th := newLastUsedThrottle(time.Hour)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := range lastUsedSweepThreshold {
		th.admit(string(rune('a'+i%26))+time.Duration(i).String(), start)
	}
	if len(th.marks) != lastUsedSweepThreshold {
		t.Fatalf("marks = %d, want %d", len(th.marks), lastUsedSweepThreshold)
	}
	if !th.admit("fresh", start.Add(2*time.Hour)) {
		t.Fatal("a new key was not admitted")
	}
	if len(th.marks) != 1 {
		t.Fatalf("marks after the sweep = %d, want 1 (only the fresh key)", len(th.marks))
	}
}
