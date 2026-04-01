package goredislimiter_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/goredislimiter"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/ratelimitertest"
	"github.com/gopernicus/gopernicus/infrastructure/database/kvstore/goredisdb"
)

func TestCompliance(t *testing.T) {
	rdb, err := goredisdb.New(goredisdb.Options{Addr: "localhost:6379"})
	if err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	store := goredislimiter.New(rdb)
	defer store.Close()

	ratelimitertest.RunSuite(t, store)
}
