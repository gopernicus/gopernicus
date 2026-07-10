package cacher

import (
	"context"
	"testing"
	"time"
)

func TestMemory_SetGetExpireDelete(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	now := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	// miss
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Fatal("expected miss")
	}
	// set + hit
	s.Set(ctx, "k", []byte("v"), time.Minute)
	if b, ok, _ := s.Get(ctx, "k"); !ok || string(b) != "v" {
		t.Fatalf("expected hit, got %q %v", b, ok)
	}
	// expiry
	now = now.Add(2 * time.Minute)
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Fatal("expected expired miss")
	}

	// pattern delete
	s.Set(ctx, "page:/a", []byte("1"), 0)
	s.Set(ctx, "page:/b", []byte("2"), 0)
	s.Set(ctx, "other", []byte("3"), 0)
	if err := s.DeletePattern(ctx, "page:*"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(ctx, "page:/a"); ok {
		t.Error("page:/a should be deleted")
	}
	if _, ok, _ := s.Get(ctx, "other"); !ok {
		t.Error("other should survive pattern delete")
	}
}
