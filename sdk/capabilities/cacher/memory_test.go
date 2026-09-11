package cacher

import (
	"context"
	"testing"
	"time"
)

func TestMemoryExpiryAndReplacement(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.Set(ctx, "expires", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, found, err := s.Get(ctx, "expires"); err != nil || found {
		t.Fatalf("expiry boundary: %v, %v", found, err)
	}
	if len(s.data) != 0 || s.lru.Len() != 0 {
		t.Fatal("expired entry not reclaimed")
	}
	if err := s.Set(ctx, "k", []byte("first"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "k", []byte("immortal"), 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if value, found, err := s.Get(ctx, "k"); err != nil || !found || string(value) != "immortal" {
		t.Fatalf("zero TTL replacement: %q, %v, %v", value, found, err)
	}
	if err := s.Set(ctx, "k", []byte("short"), time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if values, err := s.GetMany(ctx, []string{"k"}); err != nil || len(values) != 0 {
		t.Fatalf("bulk expiry = %v, %v", values, err)
	}
	if len(s.data) != 0 || s.lru.Len() != 0 {
		t.Fatal("bulk read retained expired entry")
	}
}

func TestMemoryLRURetainsHotEntries(t *testing.T) {
	for _, access := range []string{"get", "get_many", "overwrite"} {
		t.Run(access, func(t *testing.T) {
			s := NewMemory(WithMaxEntries(3), WithMaxEntries(2))
			ctx := context.Background()
			for _, key := range []string{"hot", "cold"} {
				if err := s.Set(ctx, key, []byte(key), 0); err != nil {
					t.Fatal(err)
				}
			}
			switch access {
			case "get":
				s.Get(ctx, "hot")
			case "get_many":
				s.GetMany(ctx, []string{"hot"})
			case "overwrite":
				s.Set(ctx, "hot", []byte("updated"), 0)
			}
			if err := s.Set(ctx, "new", []byte("new"), 0); err != nil {
				t.Fatal(err)
			}
			if _, found, err := s.Get(ctx, "cold"); err != nil || found {
				t.Fatalf("cold entry retained: %v, %v", found, err)
			}
			for _, key := range []string{"hot", "new"} {
				if _, found, err := s.Get(ctx, key); err != nil || !found {
					t.Fatalf("lost %q: %v", key, err)
				}
			}
			if len(s.data) != 2 || s.lru.Len() != 2 {
				t.Fatal("capacity not bounded")
			}
		})
	}
}

func TestMemoryReclaimsExpiredBeforeEvictingLive(t *testing.T) {
	s := NewMemory(WithMaxEntries(2))
	ctx := context.Background()
	now := time.Now()
	s.now = func() time.Time { return now }
	s.Set(ctx, "live", []byte("v"), 0)
	s.Set(ctx, "expired-but-recent", []byte("v"), time.Second)
	now = now.Add(time.Second)
	s.Set(ctx, "new", []byte("v"), 0)
	if _, found, err := s.Get(ctx, "live"); err != nil || !found {
		t.Fatal("evicted a live entry instead of an expired one")
	}
	if _, exists := s.data["expired-but-recent"]; exists {
		t.Fatal("expired value remains allocated")
	}
}

func TestMemoryCapacityDefaults(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if got := NewMemory(WithMaxEntries(limit)).maxEntries; got != 10_000 {
			t.Fatalf("limit %d = %d", limit, got)
		}
	}
}
