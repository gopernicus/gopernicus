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

func TestMigrationsRejectNilOptionBeforeDatabaseAccess(t *testing.T) {
	err := RunMigrations(context.Background(), nil, fstest.MapFS{}, ".", nil)
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option error = %v", err)
	}
}
