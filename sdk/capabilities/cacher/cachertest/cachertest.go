// Package cachertest is a conformance suite for cacher.Storer
// implementations: every backend that satisfies the port should pass Run
// against a fresh instance. Modeled on the net/http/httptest /
// go/analysis/analysistest pattern — a RunXxxTests(t, newImpl) style runner
// so adapters are verified against one shared behavioral contract. Imports
// stdlib + sdk/capabilities/cacher only (sdk stays dependency-free per the constitution).
package cachertest

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// shortTTL and the sleep margin below are generous (order of tens of
// milliseconds) to avoid flakiness on a loaded CI box while still finishing
// quickly.
const shortTTL = 30 * time.Millisecond

// Run exercises the cacher.Storer contract against a fresh instance obtained
// from newStorer for each subtest.
func Run(t *testing.T, newStorer func(t *testing.T) cacher.Storer) {
	t.Helper()

	t.Run("GetMiss", func(t *testing.T) { testGetMiss(t, newStorer(t)) })
	t.Run("SetGetRoundTrip", func(t *testing.T) { testSetGetRoundTrip(t, newStorer(t)) })
	t.Run("GetManyPartialHits", func(t *testing.T) { testGetManyPartialHits(t, newStorer(t)) })
	t.Run("Delete", func(t *testing.T) { testDelete(t, newStorer(t)) })
	t.Run("DeletePattern", func(t *testing.T) { testDeletePattern(t, newStorer(t)) })
	t.Run("TTLExpiry", func(t *testing.T) { testTTLExpiry(t, newStorer(t)) })
	t.Run("ZeroTTLNeverExpires", func(t *testing.T) { testZeroTTLNeverExpires(t, newStorer(t)) })
	t.Run("CloseIdempotent", func(t *testing.T) { testCloseIdempotent(t, newStorer(t)) })
}

func testGetMiss(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	v, ok, err := s.Get(ctx, "missing-key")
	if err != nil {
		t.Fatalf("Get(missing) error = %v, want nil", err)
	}
	if ok {
		t.Errorf("Get(missing) found = true, want false")
	}
	if v != nil {
		t.Errorf("Get(missing) value = %v, want nil", v)
	}
}

func testSetGetRoundTrip(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	v, ok, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() found = false, want true")
	}
	if string(v) != "v" {
		t.Errorf("Get() value = %q, want %q", v, "v")
	}
}

func testGetManyPartialHits(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "a", []byte("1"), 0); err != nil {
		t.Fatalf("Set(a) error = %v", err)
	}
	if err := s.Set(ctx, "b", []byte("2"), 0); err != nil {
		t.Fatalf("Set(b) error = %v", err)
	}
	// "c" is deliberately never set.
	got, err := s.GetMany(ctx, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetMany() returned %d entries, want 2 (got %v)", len(got), got)
	}
	if string(got["a"]) != "1" || string(got["b"]) != "2" {
		t.Errorf("GetMany() = %v, want a=1 b=2", got)
	}
	if _, ok := got["c"]; ok {
		t.Errorf("GetMany() included unset key %q", "c")
	}
}

func testDelete(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Error("Get() after Delete() found = true, want false")
	}
	// Deleting an already-absent key must not error.
	if err := s.Delete(ctx, "never-set"); err != nil {
		t.Errorf("Delete(never-set) error = %v, want nil", err)
	}
}

func testDeletePattern(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	for _, k := range []string{"page:/a", "page:/b", "other"} {
		if err := s.Set(ctx, k, []byte("v"), 0); err != nil {
			t.Fatalf("Set(%s) error = %v", k, err)
		}
	}
	if err := s.DeletePattern(ctx, "page:*"); err != nil {
		t.Fatalf("DeletePattern() error = %v", err)
	}
	if _, ok, _ := s.Get(ctx, "page:/a"); ok {
		t.Error("page:/a should have been deleted by pattern")
	}
	if _, ok, _ := s.Get(ctx, "page:/b"); ok {
		t.Error("page:/b should have been deleted by pattern")
	}
	if _, ok, _ := s.Get(ctx, "other"); !ok {
		t.Error("other should survive a non-matching pattern delete")
	}
}

func testTTLExpiry(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), shortTTL); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, ok, _ := s.Get(ctx, "k"); !ok {
		t.Fatal("Get() immediately after Set() with a positive TTL found = false, want true")
	}
	time.Sleep(shortTTL * 4)
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Error("Get() after TTL elapsed found = true, want false (expired)")
	}
}

func testZeroTTLNeverExpires(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	time.Sleep(shortTTL * 4)
	if _, ok, _ := s.Get(ctx, "k"); !ok {
		t.Error("Get() for a TTL=0 (no expiry) entry found = false after waiting, want true")
	}
}

func testCloseIdempotent(t *testing.T, s cacher.Storer) {
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil (Close must be idempotent)", err)
	}
}
