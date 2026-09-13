package pgxdb

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestLimiterOptionsReuseAndNil(t *testing.T) {
	opt := WithLimiterKeyPrefix("")
	for range 2 {
		limiter := NewLimiter(nil, WithLimiterKeyPrefix("ignored:"), opt)
		if limiter.keyPrefix != "v2:" {
			t.Fatalf("prefix = %q", limiter.keyPrefix)
		}
	}
	defer func() {
		if got := recover(); got != "pgxdb: nil LimiterOption" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewLimiter(nil, nil)
}

func TestLimiterSchemaOptionsReuseAndZero(t *testing.T) {
	schema, err := NewSchema("Auth")
	if err != nil {
		t.Fatal(err)
	}
	opt := WithLimiterSchema(schema)
	for range 2 {
		limiter := NewLimiter(nil, WithLimiterSchema(Schema{}), opt)
		if limiter.table != `"Auth".ratelimit_windows` {
			t.Fatalf("table = %q", limiter.table)
		}
	}
	if limiter := NewLimiter(nil, opt, WithLimiterSchema(Schema{})); limiter.table != limiterTable {
		t.Fatalf("zero schema table = %q", limiter.table)
	}
}

func TestMigrationsRejectNilOptionBeforeDatabaseAccess(t *testing.T) {
	err := RunMigrations(context.Background(), nil, fstest.MapFS{}, ".", nil)
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option error = %v", err)
	}
}
