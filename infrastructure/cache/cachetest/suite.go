// Package cachetest provides compliance tests for cache.Cacher implementations.
// Adapter tests import this and call RunSuite to verify they satisfy the contract.
//
// Example:
//
//	func TestCompliance(t *testing.T) {
//	    store := memorycache.New()
//	    defer store.Close()
//	    cachetest.RunSuite(t, store)
//	}
package cachetest

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache"
)

// RunSuite runs the standard compliance tests against any Cacher implementation.
func RunSuite(t *testing.T, c cache.Cacher) {
	t.Helper()

	t.Run("SetAndGet", func(t *testing.T) {
		ctx := context.Background()
		if err := c.Set(ctx, "test:key1", []byte("value1"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		data, ok, err := c.Get(ctx, "test:key1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !ok {
			t.Fatal("Get: expected key to exist")
		}
		if string(data) != "value1" {
			t.Fatalf("Get: got %q, want %q", data, "value1")
		}
	})

	t.Run("GetMissing", func(t *testing.T) {
		ctx := context.Background()
		_, ok, err := c.Get(ctx, "test:nonexistent")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if ok {
			t.Fatal("Get: expected key to not exist")
		}
	})

	t.Run("GetMany", func(t *testing.T) {
		ctx := context.Background()
		if err := c.Set(ctx, "test:multi1", []byte("a"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := c.Set(ctx, "test:multi2", []byte("b"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}

		result, err := c.GetMany(ctx, []string{"test:multi1", "test:multi2", "test:multi3"})
		if err != nil {
			t.Fatalf("GetMany: %v", err)
		}
		if string(result["test:multi1"]) != "a" {
			t.Fatalf("GetMany: multi1 got %q, want %q", result["test:multi1"], "a")
		}
		if string(result["test:multi2"]) != "b" {
			t.Fatalf("GetMany: multi2 got %q, want %q", result["test:multi2"], "b")
		}
		if _, exists := result["test:multi3"]; exists {
			t.Fatal("GetMany: multi3 should not exist")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		ctx := context.Background()
		if err := c.Set(ctx, "test:del", []byte("x"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := c.Delete(ctx, "test:del"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, ok, err := c.Get(ctx, "test:del")
		if err != nil {
			t.Fatalf("Get after Delete: %v", err)
		}
		if ok {
			t.Fatal("Get: key should not exist after Delete")
		}
	})

	t.Run("DeleteNonexistent", func(t *testing.T) {
		ctx := context.Background()
		if err := c.Delete(ctx, "test:never-set"); err != nil {
			t.Fatalf("Delete nonexistent key should not error: %v", err)
		}
	})

	t.Run("DeletePattern", func(t *testing.T) {
		ctx := context.Background()
		if err := c.Set(ctx, "test:pat:a", []byte("1"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := c.Set(ctx, "test:pat:b", []byte("2"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := c.DeletePattern(ctx, "test:pat:*"); err != nil {
			t.Fatalf("DeletePattern: %v", err)
		}

		_, okA, _ := c.Get(ctx, "test:pat:a")
		_, okB, _ := c.Get(ctx, "test:pat:b")
		if okA || okB {
			t.Fatal("DeletePattern: keys should be deleted")
		}
	})

	t.Run("Overwrite", func(t *testing.T) {
		ctx := context.Background()
		if err := c.Set(ctx, "test:overwrite", []byte("old"), time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := c.Set(ctx, "test:overwrite", []byte("new"), time.Minute); err != nil {
			t.Fatalf("Set overwrite: %v", err)
		}
		data, _, err := c.Get(ctx, "test:overwrite")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if string(data) != "new" {
			t.Fatalf("Get: got %q, want %q", data, "new")
		}
	})
}
