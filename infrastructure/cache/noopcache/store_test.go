package noopcache_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache/noopcache"
)

func TestGet_AlwaysMiss(t *testing.T) {
	s := noopcache.New()
	data, found, err := s.Get(context.Background(), "key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if found {
		t.Error("Get() found = true, want false")
	}
	if data != nil {
		t.Errorf("Get() data = %v, want nil", data)
	}
}

func TestGetMany_AlwaysEmpty(t *testing.T) {
	s := noopcache.New()
	result, err := s.GetMany(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("GetMany() returned %d entries, want 0", len(result))
	}
}

func TestSet_Succeeds(t *testing.T) {
	s := noopcache.New()
	err := s.Set(context.Background(), "key", []byte("value"), time.Minute)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
}

func TestDelete_Succeeds(t *testing.T) {
	s := noopcache.New()
	err := s.Delete(context.Background(), "key")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestDeletePattern_Succeeds(t *testing.T) {
	s := noopcache.New()
	err := s.DeletePattern(context.Background(), "prefix:*")
	if err != nil {
		t.Fatalf("DeletePattern() error = %v", err)
	}
}

func TestClose_Succeeds(t *testing.T) {
	s := noopcache.New()
	err := s.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSetThenGet_StillMiss(t *testing.T) {
	s := noopcache.New()
	ctx := context.Background()

	s.Set(ctx, "key", []byte("value"), 0)

	_, found, _ := s.Get(ctx, "key")
	if found {
		t.Error("noop store should never find a key")
	}
}
