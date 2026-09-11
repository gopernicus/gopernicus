// These tests are hermetic: they exercise construction and option handling
// without any Redis connection. The live cacher.Storer contract is verified by
// conformance_test.go (cachertest.Run) under REDIS_TEST_ADDR.
package goredis

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/redis/go-redis/v9"

	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

func TestCacherAppliesDefaultKeyPrefix(t *testing.T) {
	c := NewCacher(dummyClient())
	if c.keyPrefix != defaultCacheKeyPrefix {
		t.Errorf("keyPrefix = %q, want %q", c.keyPrefix, defaultCacheKeyPrefix)
	}
}

func TestCacherKeyPrefixOption(t *testing.T) {
	c := NewCacher(dummyClient(), WithCacheKeyPrefix("tenant:"))
	if c.keyPrefix != "tenant:" {
		t.Errorf("keyPrefix = %q, want %q", c.keyPrefix, "tenant:")
	}
}

func TestCacherPortSatisfaction(t *testing.T) {
	var _ cacher.Storer = (*Cacher)(nil)
	var _ cacher.PrefixDeleter = (*Cacher)(nil)
}

type cacheCommandHook struct{ run func(redis.Cmder) error }

func (h cacheCommandHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h cacheCommandHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error { return h.run(cmd) }
}
func (h cacheCommandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestCacherTTLCommands(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ttl    time.Duration
		millis int64
	}{
		{"immortal", 0, 0}, {"nanosecond", 1, 1}, {"below millisecond", time.Millisecond - 1, 1},
		{"millisecond", time.Millisecond, 1}, {"fractional millisecond", time.Millisecond + 1, 2},
		{"second", time.Second, 1000}, {"maximum duration", time.Duration(math.MaxInt64), 9223372036855},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rdb := dummyClient()
			t.Cleanup(func() { rdb.Close() })
			calls := 0
			rdb.AddHook(cacheCommandHook{run: func(cmd redis.Cmder) error {
				calls++
				want := []any{"set", "cache:key", []byte("value")}
				if tc.ttl > 0 {
					want = append(want, "px", tc.millis)
				}
				if !reflect.DeepEqual(cmd.Args(), want) {
					t.Fatalf("command=%#v, want %#v", cmd.Args(), want)
				}
				return nil
			}})
			if err := NewCacher(rdb).Set(context.Background(), "key", []byte("value"), tc.ttl); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("commands=%d", calls)
			}
		})
	}
}

func TestCacherInvalidInputsNeverReachRedis(t *testing.T) {
	c := NewCacher(nil)
	ctx := context.Background()
	for _, ttl := range []time.Duration{-1, -time.Second} {
		if err := c.Set(ctx, "key", nil, ttl); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("negative TTL=%v", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, getErr := c.Get(canceled, "key")
	_, manyErr := c.GetMany(canceled, []string{"key"})
	_, emptyErr := c.GetMany(canceled, nil)
	for _, err := range []error{getErr, manyErr, emptyErr, c.Set(canceled, "key", nil, 0), c.Delete(canceled, "key"), c.DeletePrefix(canceled, "")} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled operation=%v", err)
		}
	}
}

func TestCacherDeletePrefixEscapesEntirePhysicalPrefix(t *testing.T) {
	rdb := dummyClient()
	t.Cleanup(func() { rdb.Close() })
	scans, deletes := 0, 0
	firstKey := `tenant[*?]\:literal?[x]*\one`
	secondKey := `tenant[*?]\:literal?[x]*\two`
	rdb.AddHook(cacheCommandHook{run: func(cmd redis.Cmder) error {
		switch value := cmd.(type) {
		case *redis.ScanCmd:
			scans++
			args := cmd.Args()
			if len(args) != 6 || args[2] != "match" || args[3] != `tenant\[\*\?\]\\:literal\?\[x\]\*\\*` {
				t.Fatalf("unsafe SCAN: %#v", args)
			}
			if scans == 1 {
				value.SetVal([]string{firstKey}, 17)
			} else {
				if args[1] != uint64(17) {
					t.Fatalf("cursor=%v", args[1])
				}
				value.SetVal([]string{secondKey}, 0)
			}
		case *redis.IntCmd:
			deletes++
			key := firstKey
			if deletes == 2 {
				key = secondKey
			}
			if !reflect.DeepEqual(cmd.Args(), []any{"del", key}) {
				t.Fatalf("delete=%#v", cmd.Args())
			}
			value.SetVal(1)
		default:
			t.Fatalf("unexpected command %T", cmd)
		}
		return nil
	}})
	c := NewCacher(rdb, WithCacheKeyPrefix(`tenant[*?]\:`))
	if err := c.DeletePrefix(context.Background(), `literal?[x]*\`); err != nil {
		t.Fatal(err)
	}
	if scans != 2 || deletes != 2 {
		t.Fatalf("scans=%d deletes=%d", scans, deletes)
	}
}

func TestCacherGetManyOwnsByteResults(t *testing.T) {
	rdb := dummyClient()
	t.Cleanup(func() { rdb.Close() })
	shared := []byte("value")
	rdb.AddHook(cacheCommandHook{run: func(cmd redis.Cmder) error {
		cmd.(*redis.SliceCmd).SetVal([]any{shared, "", nil})
		return nil
	}})
	c := NewCacher(rdb)
	got, err := c.GetMany(context.Background(), []string{"a", "empty", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	got["a"][0] = 'X'
	if string(shared) != "value" {
		t.Fatal("GetMany aliased driver's byte storage")
	}
	if value, found := got["empty"]; !found || len(value) != 0 {
		t.Fatal("empty hit missing")
	}
	if _, found := got["missing"]; found {
		t.Fatal("missing key became a hit")
	}
}

func TestCacherCancellationDuringSuccessfulCommand(t *testing.T) {
	for _, stage := range []string{"get", "mget", "set immortal", "set expiring", "del", "scan", "prefix del"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rdb := dummyClient()
			t.Cleanup(func() { rdb.Close() })
			commands := 0
			rdb.AddHook(cacheCommandHook{run: func(cmd redis.Cmder) error {
				commands++
				switch value := cmd.(type) {
				case *redis.StringCmd:
					value.SetVal("value")
				case *redis.SliceCmd:
					value.SetVal([]any{"value"})
				case *redis.StatusCmd:
					value.SetVal("OK")
				case *redis.Cmd:
					value.SetVal("OK")
				case *redis.IntCmd:
					value.SetVal(1)
				case *redis.ScanCmd:
					value.SetVal([]string{"cache:key"}, 0)
				default:
					t.Fatalf("unexpected command %T", cmd)
				}
				if stage != "prefix del" || cmd.Name() == "del" {
					cancel()
				}
				return nil
			}})
			cache := NewCacher(rdb)
			var err error
			switch stage {
			case "get":
				var found bool
				var value []byte
				value, found, err = cache.Get(ctx, "key")
				if found || value != nil {
					t.Fatal("canceled Get returned a hit")
				}
			case "mget":
				var values map[string][]byte
				values, err = cache.GetMany(ctx, []string{"key"})
				if values != nil {
					t.Fatal("canceled GetMany returned hits")
				}
			case "set immortal":
				err = cache.Set(ctx, "key", []byte("v"), 0)
			case "set expiring":
				err = cache.Set(ctx, "key", []byte("v"), time.Minute)
			case "del":
				err = cache.Delete(ctx, "key")
			case "scan", "prefix del":
				err = cache.DeletePrefix(ctx, "")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled command result=%v", err)
			}
			wantCommands := 1
			if stage == "prefix del" {
				wantCommands = 2
			}
			if commands != wantCommands {
				t.Fatalf("commands=%d, want %d; canceled SCAN must not delete", commands, wantCommands)
			}
		})
	}
}
