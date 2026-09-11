// Package cachertest is a conformance suite for cacher.Storer
// implementations: every backend that satisfies the port should pass Run
// against a fresh instance. Modeled on the net/http/httptest /
// go/analysis/analysistest pattern — a RunXxxTests(t, newImpl) style runner
// so adapters are verified against one shared behavioral contract. Imports
// stdlib + sdk/capabilities/cacher only (sdk stays dependency-free per the constitution).
package cachertest

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

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
	t.Run("TTLExpiry", func(t *testing.T) { testTTLExpiry(t, newStorer(t)) })
	t.Run("ZeroTTLNeverExpires", func(t *testing.T) { testZeroTTLNeverExpires(t, newStorer(t)) })
	t.Run("OverwriteReplacesTTL", func(t *testing.T) { testOverwriteReplacesTTL(t, newStorer(t)) })
	t.Run("OwnedValues", func(t *testing.T) { testOwnedValues(t, newStorer(t)) })
	t.Run("EmptyValues", func(t *testing.T) { testEmptyValues(t, newStorer(t)) })
	t.Run("InvalidTTLAndCancellation", func(t *testing.T) { testInvalidInputs(t, newStorer(t)) })
	t.Run("ConcurrentAccess", func(t *testing.T) { testConcurrentAccess(t, newStorer(t)) })
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
	if _, ok, err := s.Get(ctx, "k"); err != nil || ok {
		t.Errorf("Get() after Delete() found = %v, error = %v; want false, nil", ok, err)
	}
	// Deleting an already-absent key must not error.
	if err := s.Delete(ctx, "never-set"); err != nil {
		t.Errorf("Delete(never-set) error = %v, want nil", err)
	}
}

func testTTLExpiry(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), shortTTL); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, ok, err := s.Get(ctx, "k"); err != nil || !ok {
		t.Fatalf("Get() immediately after Set() found = %v, error = %v; want true, nil", ok, err)
	}
	time.Sleep(shortTTL * 4)
	if _, ok, err := s.Get(ctx, "k"); err != nil || ok {
		t.Errorf("Get() after TTL elapsed found = %v, error = %v; want false, nil", ok, err)
	}
}

func testZeroTTLNeverExpires(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	time.Sleep(shortTTL * 4)
	if _, ok, err := s.Get(ctx, "k"); err != nil || !ok {
		t.Errorf("Get() for TTL=0 after waiting found = %v, error = %v; want true, nil", ok, err)
	}
}

func testOverwriteReplacesTTL(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "becomes-expiring", []byte("old"), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "becomes-expiring", []byte("new"), shortTTL); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "becomes-immortal", []byte("old"), shortTTL); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "becomes-immortal", []byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(shortTTL * 4)
	if _, found, err := s.Get(ctx, "becomes-expiring"); err != nil || found {
		t.Fatalf("positive replacement TTL did not expire: %v, %v", found, err)
	}
	if value, found, err := s.Get(ctx, "becomes-immortal"); err != nil || !found || string(value) != "new" {
		t.Fatalf("zero replacement TTL did not remove expiry: %q, %v, %v", value, found, err)
	}
}

func testOwnedValues(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	input := []byte("original")
	if err := s.Set(ctx, "k", input, 0); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	got, found, err := s.Get(ctx, "k")
	if err != nil || !found || string(got) != "original" {
		t.Fatalf("stored input aliased: %q, %v, %v", got, found, err)
	}
	got[0] = 'Y'
	many, err := s.GetMany(ctx, []string{"k", "k", "missing"})
	if err != nil || len(many) != 1 || string(many["k"]) != "original" {
		t.Fatalf("Get/GetMany ownership: %v, %v", many, err)
	}
	many["k"][0] = 'Z'
	delete(many, "k")
	got, found, err = s.Get(ctx, "k")
	if err != nil || !found || string(got) != "original" {
		t.Fatalf("GetMany aliased storage: %q, %v, %v", got, found, err)
	}
}

func testEmptyValues(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	for _, value := range [][]byte{nil, {}} {
		if err := s.Set(ctx, "empty", value, 0); err != nil {
			t.Fatal(err)
		}
		got, found, err := s.Get(ctx, "empty")
		if err != nil || !found || len(got) != 0 {
			t.Fatalf("empty value = %v, %v, %v", got, found, err)
		}
		many, err := s.GetMany(ctx, []string{"empty"})
		if err != nil {
			t.Fatal(err)
		}
		if value, found := many["empty"]; !found || len(value) != 0 {
			t.Fatal("bulk read lost empty hit")
		}
	}
	if got, err := s.GetMany(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty bulk = %v, %v", got, err)
	}
}

func testInvalidInputs(t *testing.T, s cacher.Storer) {
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("original"), 0); err != nil {
		t.Fatal(err)
	}
	for _, ttl := range []time.Duration{-1, -time.Second} {
		if err := s.Set(ctx, "k", []byte("changed"), ttl); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("negative TTL = %v", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	checks := []func() error{
		func() error { _, _, err := s.Get(canceled, "k"); return err },
		func() error { _, err := s.GetMany(canceled, []string{"k"}); return err },
		func() error { _, err := s.GetMany(canceled, nil); return err },
		func() error { return s.Set(canceled, "k", []byte("changed"), 0) },
		func() error { return s.Delete(canceled, "k") },
	}
	for i, check := range checks {
		if err := check(); !errors.Is(err, context.Canceled) {
			t.Errorf("canceled operation %d = %v", i, err)
		}
	}
	got, found, err := s.Get(ctx, "k")
	if err != nil || !found || string(got) != "original" {
		t.Fatalf("invalid operation mutated value: %q, %v, %v", got, found, err)
	}
}

func testConcurrentAccess(t *testing.T, s cacher.Storer) {
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			ctx := context.Background()
			key := fmt.Sprintf("worker:%d", worker)
			for range 10 {
				if err := s.Set(ctx, key, []byte(key), 0); err != nil {
					t.Error(err)
					return
				}
				got, found, err := s.Get(ctx, key)
				if err != nil || !found || string(got) != key {
					t.Errorf("concurrent read = %q, %v, %v", got, found, err)
					return
				}
				got[0] = 'X'
				if err := s.Delete(ctx, key); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}
