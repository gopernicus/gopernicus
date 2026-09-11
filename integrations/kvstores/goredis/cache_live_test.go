package goredis_test

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/kvstores/goredis"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

func TestCacherLiveNamespaceIsolation(t *testing.T) {
	rdb := dialLive(t, requireAddr(t))
	ctx := context.Background()
	base := fmt.Sprintf("cacheisolation:%d:", time.Now().UnixNano())
	own := goredis.NewCacher(rdb, goredis.WithCacheKeyPrefix(base+"tenant*:"))
	neighbor := goredis.NewCacher(rdb, goredis.WithCacheKeyPrefix(base+"tenant-other:"))
	t.Cleanup(func() { _ = own.DeletePrefix(ctx, ""); _ = neighbor.DeletePrefix(ctx, "") })
	for _, store := range []*goredis.Cacher{own, neighbor} {
		if err := store.Set(ctx, "page:a", []byte("value"), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := own.DeletePrefix(ctx, "page:"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := own.Get(ctx, "page:a"); err != nil || found {
		t.Fatalf("own deletion: %v %v", found, err)
	}
	if _, found, err := neighbor.Get(ctx, "page:a"); err != nil || !found {
		t.Fatalf("neighbor lost: %v %v", found, err)
	}

	a := cacher.New(own, cacher.WithNamespace("catalog"))
	b := cacher.New(own, cacher.WithNamespace("catalog:next"))
	if err := a.Set(ctx, "next:key", []byte("a"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(ctx, "key", []byte("b"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := a.InvalidatePrefix(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if data, found, err := b.Get(ctx, "key"); err != nil || !found || string(data) != "b" {
		t.Fatalf("service namespace crossed: %q %v %v", data, found, err)
	}
}

func TestCacherLiveTTLBounds(t *testing.T) {
	rdb := dialLive(t, requireAddr(t))
	ctx := context.Background()
	prefix := fmt.Sprintf("cachettl:%d:", time.Now().UnixNano())
	store := goredis.NewCacher(rdb, goredis.WithCacheKeyPrefix(prefix))
	t.Cleanup(func() { _ = store.DeletePrefix(ctx, "") })
	if err := store.Set(ctx, "maximum", []byte("v"), time.Duration(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if data, found, err := store.Get(ctx, "maximum"); err != nil || !found || string(data) != "v" {
		t.Fatalf("maximum TTL overflow: %q %v %v", data, found, err)
	}
	if err := store.Set(ctx, "tiny", []byte("v"), time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	// Do not require seeing a 1ms hit: the request can outlive that TTL. It must
	// expire instead of accidentally becoming immortal through truncation.
	time.Sleep(10 * time.Millisecond)
	if _, found, err := store.Get(ctx, "tiny"); err != nil || found {
		t.Fatalf("tiny TTL did not expire: %v %v", found, err)
	}
}
