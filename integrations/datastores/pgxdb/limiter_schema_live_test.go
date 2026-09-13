package pgxdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

func createLimiterSchema(t *testing.T, db *DB, name string, legacy bool) Schema {
	t.Helper()
	schema := disposableSchema(t, db, name)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE SCHEMA "`+schema.String()+`"`); err != nil {
		t.Fatal(err)
	}
	ddl := strings.Replace(limiterTableDDL, limiterTable, schema.Table(limiterTable), 1)
	if legacy {
		ddl = strings.Replace(ddl, ",\n    window_ms     BIGINT      NOT NULL DEFAULT 0", "", 1)
	}
	if _, err := db.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	index := strings.Replace(limiterIndexDDL, "ON "+limiterTable, "ON "+schema.Table(limiterTable), 1)
	if _, err := db.Exec(ctx, index); err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestLive_LimiterSchemaIsolationSharedPool(t *testing.T) {
	db := openLimiterDB(t)
	if searchPath := db.Underlying().Config().ConnConfig.RuntimeParams["search_path"]; searchPath != "" {
		t.Fatalf("test requires an unpinned pool; search_path = %q", searchPath)
	}
	ctx := context.Background()
	var searchPathBefore string
	if err := db.QueryRow(ctx, "SHOW search_path").Scan(&searchPathBefore); err != nil {
		t.Fatal(err)
	}
	// Case-sensitive names also prove that schema selection uses quoted names.
	a := createLimiterSchema(t, db, "pgxdb_Limiter_A", false)
	b := createLimiterSchema(t, db, "pgxdb_limiter_b", false)
	prefix := livePrefix(t)
	key := "shared-key"
	fullKey := prefix + limiterKeyVersion + key
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "DELETE FROM ratelimit_windows WHERE key = $1", fullKey)
	})
	limit := ratelimiter.Limit{Requests: 1, Window: time.Minute}
	limiters := []*Limiter{
		NewLimiter(db, WithLimiterKeyPrefix(prefix), WithLimiterSchema(a)),
		NewLimiter(db, WithLimiterKeyPrefix(prefix), WithLimiterSchema(b)),
		NewLimiter(db, WithLimiterKeyPrefix(prefix)),
	}
	tables := []string{a.Table(limiterTable), b.Table(limiterTable), limiterTable}
	for i, limiter := range limiters {
		if err := limiter.StatusCheck(ctx); err != nil {
			t.Fatalf("%s probe: %v", tables[i], err)
		}
		result, err := limiter.Allow(ctx, key, limit)
		if err != nil || !result.Allowed {
			t.Fatalf("%s first admission = %+v, %v", tables[i], result, err)
		}
	}
	for i, limiter := range limiters {
		result, err := limiter.Allow(ctx, key, limit)
		if err != nil || result.Allowed {
			t.Fatalf("%s second admission = %+v, %v", tables[i], result, err)
		}
	}
	for resetIndex, limiter := range limiters {
		if err := limiter.Reset(ctx, key); err != nil {
			t.Fatalf("%s reset: %v", tables[resetIndex], err)
		}
		for i, table := range tables {
			want := 1
			if i == resetIndex {
				want = 0
			}
			if got := countRows(t, db, "SELECT count(*) FROM "+table+" WHERE key = $1", fullKey); got != want {
				t.Fatalf("reset %s: %s has %d rows, want %d", tables[resetIndex], table, got, want)
			}
			result, err := limiters[i].Allow(ctx, key, limit)
			if err != nil || result.Allowed != (i == resetIndex) {
				t.Fatalf("reset %s: %s admission = %+v, %v", tables[resetIndex], table, result, err)
			}
		}
	}
	var searchPathAfter string
	if err := db.QueryRow(ctx, "SHOW search_path").Scan(&searchPathAfter); err != nil {
		t.Fatal(err)
	}
	if searchPathAfter != searchPathBefore {
		t.Fatalf("search_path changed from %q to %q", searchPathBefore, searchPathAfter)
	}
}

func TestLive_LimiterSchemaDoesNotFallBack(t *testing.T) {
	db := openLimiterDB(t)
	ctx := context.Background()
	prefix := livePrefix(t)
	key := "no-fallback"
	fullKey := prefix + limiterKeyVersion + key
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "DELETE FROM ratelimit_windows WHERE key = $1", fullKey)
	})
	healthy := createLimiterSchema(t, db, "pgxdb_limiter_healthy", false)
	limit := ratelimiter.Limit{Requests: 1, Window: time.Minute}
	for _, limiter := range []*Limiter{
		NewLimiter(db, WithLimiterKeyPrefix(prefix)),
		NewLimiter(db, WithLimiterKeyPrefix(prefix), WithLimiterSchema(healthy)),
	} {
		if err := limiter.StatusCheck(ctx); err != nil {
			t.Fatal(err)
		}
		if result, err := limiter.Allow(ctx, key, limit); err != nil || !result.Allowed {
			t.Fatalf("healthy admission = %+v, %v", result, err)
		}
	}
	before := readWindow(t, db, fullKey)
	for _, name := range []string{"missing_schema", "missing_table", "missing_column"} {
		t.Run(name, func(t *testing.T) {
			var schema Schema
			if name == "missing_column" {
				schema = createLimiterSchema(t, db, "pgxdb_limiter_"+name, true)
			} else {
				schema = disposableSchema(t, db, "pgxdb_limiter_"+name)
				if name == "missing_table" {
					if _, err := db.Exec(ctx, `CREATE SCHEMA "`+schema.String()+`"`); err != nil {
						t.Fatal(err)
					}
				}
			}
			limiter := NewLimiter(db, WithLimiterKeyPrefix(prefix), WithLimiterSchema(schema))
			if err := limiter.StatusCheck(ctx); !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), schema.String()) {
				t.Fatalf("probe error = %v, want missing selected schema/table/column", err)
			}
			if _, err := limiter.Allow(ctx, key, limit); err == nil {
				t.Fatal("admission succeeded against an unavailable selected table")
			}
			if err := limiter.Reset(ctx, key); err == nil {
				t.Fatal("reset succeeded against an unavailable selected table")
			}
			if after := readWindow(t, db, fullKey); after != before {
				t.Fatalf("failed scoped operations changed default window: before %+v, after %+v", before, after)
			}
			if got := countRows(t, db, "SELECT count(*) FROM "+healthy.Table(limiterTable)+" WHERE key = $1 AND request_count = 1", fullKey); got != 1 {
				t.Fatalf("healthy schema budget changed: %d rows", got)
			}
		})
	}
}

func TestLive_LimiterSchemaConcurrentAdmissionSharedPool(t *testing.T) {
	db := openLimiterDB(t)
	prefix := livePrefix(t)
	schemas := []Schema{
		createLimiterSchema(t, db, "pgxdb_limiter_concurrent_a", false),
		createLimiterSchema(t, db, "pgxdb_limiter_concurrent_b", false),
	}
	ceilings := []int{3, 5}
	limiters := make([]*Limiter, len(schemas))
	for i, schema := range schemas {
		limiters[i] = NewLimiter(db, WithLimiterKeyPrefix(prefix), WithLimiterSchema(schema))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var allowed [2]atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 32)
	for n := range 32 {
		index := n % len(limiters)
		wg.Go(func() {
			<-start
			result, err := limiters[index].Allow(ctx, "concurrent-key", ratelimiter.Limit{Requests: ceilings[index], Window: time.Minute})
			if err != nil {
				errs <- fmt.Errorf("schema %s: %w", schemas[index], err)
				return
			}
			if result.Allowed {
				allowed[index].Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i, schema := range schemas {
		if got := allowed[i].Load(); got != int64(ceilings[i]) {
			t.Errorf("schema %s admitted %d requests, want %d", schema, got, ceilings[i])
		}
		if got := countRows(t, db, "SELECT count(*) FROM "+schema.Table(limiterTable)+" WHERE key = $1 AND request_count = $2",
			prefix+limiterKeyVersion+"concurrent-key", ceilings[i]); got != 1 {
			t.Errorf("schema %s persisted budget rows = %d, want 1", schema, got)
		}
	}
}
