package decisions

import (
	"context"
	"errors"
	"testing"
)

func TestRoleMemoChecksCancellationBeforeCacheHit(t *testing.T) {
	reads := 0
	read := memoRoleReads(func(context.Context, string, string, string, string, string) (bool, error) {
		reads++
		return true, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if held, err := read(ctx, "user", "u1", "viewer", "doc", "d1"); !held || err != nil {
		t.Fatalf("first read = %v %v", held, err)
	}
	cancel()
	if held, err := read(ctx, "user", "u1", "viewer", "doc", "d1"); held || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cache hit = %v %v", held, err)
	}
	if reads != 1 {
		t.Fatalf("canceled hit called store: reads=%d", reads)
	}
}
