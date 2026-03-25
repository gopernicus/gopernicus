package memorycache_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache/cachetest"
	"github.com/gopernicus/gopernicus/infrastructure/cache/memorycache"
)

func newTestStore() *memorycache.Store {
	return memorycache.New(memorycache.Config{MaxEntries: 100})
}

func TestCompliance(t *testing.T) {
	store := newTestStore()
	defer store.Close()
	cachetest.RunSuite(t, store)
}

// =============================================================================
// Basic Operations
// =============================================================================

func TestGet_Miss(t *testing.T) {
	s := newTestStore()
	defer s.Close()

	_, found, err := s.Get(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if found {
		t.Error("Get() found = true, want false for missing key")
	}
}

func TestSetAndGet(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	if err := s.Set(ctx, "key", []byte("value"), 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	data, found, err := s.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !found {
		t.Error("Get() found = false, want true")
	}
	if string(data) != "value" {
		t.Errorf("Get() data = %q, want %q", string(data), "value")
	}
}

func TestSet_OverwritesExisting(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "key", []byte("v1"), 0)
	s.Set(ctx, "key", []byte("v2"), 0)

	data, _, _ := s.Get(ctx, "key")
	if string(data) != "v2" {
		t.Errorf("Get() after overwrite = %q, want %q", string(data), "v2")
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "key", []byte("value"), 0)
	s.Delete(ctx, "key")

	_, found, _ := s.Get(ctx, "key")
	if found {
		t.Error("Get() after Delete should not find key")
	}
}

func TestDelete_NonExistent(t *testing.T) {
	s := newTestStore()
	defer s.Close()

	err := s.Delete(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Delete() non-existent key should succeed, got: %v", err)
	}
}

// =============================================================================
// TTL
// =============================================================================

func TestTTL_Expired(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "key", []byte("value"), time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	_, found, err := s.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if found {
		t.Error("Get() should not find expired key")
	}
}

func TestTTL_NotExpired(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "key", []byte("value"), time.Hour)

	data, found, _ := s.Get(ctx, "key")
	if !found {
		t.Error("Get() should find non-expired key")
	}
	if string(data) != "value" {
		t.Errorf("Get() data = %q, want %q", string(data), "value")
	}
}

func TestTTL_ZeroMeansNoExpiration(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "key", []byte("forever"), 0)

	_, found, _ := s.Get(ctx, "key")
	if !found {
		t.Error("Get() with TTL=0 should always find key")
	}
}

// =============================================================================
// GetMany
// =============================================================================

func TestGetMany_MixedResults(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "a", []byte("1"), 0)
	s.Set(ctx, "b", []byte("2"), 0)

	result, err := s.GetMany(ctx, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if len(result) != 2 {
		t.Errorf("GetMany() returned %d entries, want 2", len(result))
	}
	if string(result["a"]) != "1" {
		t.Errorf("result[a] = %q, want %q", string(result["a"]), "1")
	}
	if string(result["b"]) != "2" {
		t.Errorf("result[b] = %q, want %q", string(result["b"]), "2")
	}
}

func TestGetMany_SkipsExpired(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "live", []byte("1"), time.Hour)
	s.Set(ctx, "dead", []byte("2"), time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	result, _ := s.GetMany(ctx, []string{"live", "dead"})
	if len(result) != 1 {
		t.Errorf("GetMany() returned %d entries, want 1", len(result))
	}
	if _, ok := result["dead"]; ok {
		t.Error("GetMany() should not return expired key")
	}
}

// =============================================================================
// LRU Eviction
// =============================================================================

func TestLRU_Eviction(t *testing.T) {
	s := memorycache.New(memorycache.Config{MaxEntries: 3})
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "a", []byte("1"), 0)
	s.Set(ctx, "b", []byte("2"), 0)
	s.Set(ctx, "c", []byte("3"), 0)
	s.Set(ctx, "d", []byte("4"), 0) // Should evict "a"

	_, found, _ := s.Get(ctx, "a")
	if found {
		t.Error("oldest entry 'a' should have been evicted")
	}

	_, found, _ = s.Get(ctx, "d")
	if !found {
		t.Error("newest entry 'd' should exist")
	}
}

func TestLRU_AccessPreventsEviction(t *testing.T) {
	s := memorycache.New(memorycache.Config{MaxEntries: 3})
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "a", []byte("1"), 0)
	s.Set(ctx, "b", []byte("2"), 0)
	s.Set(ctx, "c", []byte("3"), 0)

	// Access "a" to move it to front.
	s.Get(ctx, "a")

	// Adding "d" should evict "b" (least recently used).
	s.Set(ctx, "d", []byte("4"), 0)

	_, found, _ := s.Get(ctx, "a")
	if !found {
		t.Error("recently accessed 'a' should not have been evicted")
	}

	_, found, _ = s.Get(ctx, "b")
	if found {
		t.Error("least recently used 'b' should have been evicted")
	}
}

// =============================================================================
// DeletePattern
// =============================================================================

func TestDeletePattern_PrefixWildcard(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "users:1", []byte("a"), 0)
	s.Set(ctx, "users:2", []byte("b"), 0)
	s.Set(ctx, "posts:1", []byte("c"), 0)

	s.DeletePattern(ctx, "users:*")

	_, found, _ := s.Get(ctx, "users:1")
	if found {
		t.Error("users:1 should be deleted by pattern")
	}
	_, found, _ = s.Get(ctx, "users:2")
	if found {
		t.Error("users:2 should be deleted by pattern")
	}
	_, found, _ = s.Get(ctx, "posts:1")
	if !found {
		t.Error("posts:1 should not be affected by pattern")
	}
}

func TestDeletePattern_ExactMatch(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "exact-key", []byte("value"), 0)
	s.DeletePattern(ctx, "exact-key")

	_, found, _ := s.Get(ctx, "exact-key")
	if found {
		t.Error("exact pattern should delete matching key")
	}
}

func TestDeletePattern_SuffixWildcard(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "data.json", []byte("a"), 0)
	s.Set(ctx, "config.json", []byte("b"), 0)
	s.Set(ctx, "data.xml", []byte("c"), 0)

	s.DeletePattern(ctx, "*.json")

	_, found, _ := s.Get(ctx, "data.json")
	if found {
		t.Error("data.json should be deleted by suffix pattern")
	}
	_, found, _ = s.Get(ctx, "data.xml")
	if !found {
		t.Error("data.xml should not be affected")
	}
}

// =============================================================================
// Data Isolation
// =============================================================================

func TestDataIsolation_SetCopy(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	original := []byte("original")
	s.Set(ctx, "key", original, 0)

	// Mutate the original slice.
	original[0] = 'X'

	data, _, _ := s.Get(ctx, "key")
	if string(data) != "original" {
		t.Errorf("mutating original should not affect cache, got %q", string(data))
	}
}

func TestDataIsolation_GetCopy(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	s.Set(ctx, "key", []byte("original"), 0)

	data, _, _ := s.Get(ctx, "key")
	data[0] = 'X'

	data2, _, _ := s.Get(ctx, "key")
	if string(data2) != "original" {
		t.Errorf("mutating Get result should not affect cache, got %q", string(data2))
	}
}

// =============================================================================
// Len & Close
// =============================================================================

func TestLen(t *testing.T) {
	s := newTestStore()
	defer s.Close()
	ctx := context.Background()

	if s.Len() != 0 {
		t.Errorf("Len() = %d, want 0", s.Len())
	}

	s.Set(ctx, "a", []byte("1"), 0)
	s.Set(ctx, "b", []byte("2"), 0)

	if s.Len() != 2 {
		t.Errorf("Len() = %d, want 2", s.Len())
	}
}

func TestClose_ClearsAll(t *testing.T) {
	s := newTestStore()
	ctx := context.Background()

	s.Set(ctx, "a", []byte("1"), 0)
	s.Set(ctx, "b", []byte("2"), 0)
	s.Close()

	if s.Len() != 0 {
		t.Errorf("Len() after Close = %d, want 0", s.Len())
	}
}
