package cacher

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

type coreOnlyStore struct{ Storer }

type controlledStore struct {
	Storer
	getErr, setErr     error
	getCalls, setCalls int
	onGet, onSet       func()
}

func (s *controlledStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	s.getCalls++
	if s.onGet != nil {
		s.onGet()
	}
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	return s.Storer.Get(ctx, key)
}
func (s *controlledStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	s.setCalls++
	if s.onSet != nil {
		s.onSet()
	}
	if s.setErr != nil {
		return s.setErr
	}
	return s.Storer.Set(ctx, key, value, ttl)
}

func TestCacheNamespacesAndInvalidation(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	a := New(store, WithNamespace("team"))
	b := New(store, WithNamespace("team:archive"))
	empty := New(store)
	for cache, values := range map[*Cache]map[string]string{
		a: {"archive:x": "a", "other": "a-other"}, b: {"x": "b"}, empty: {"team:archive:x": "empty"},
	} {
		for key, value := range values {
			if err := cache.Set(ctx, key, []byte(value), 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	values, err := a.GetMany(ctx, []string{"archive:x", "other", "missing", "archive:x"})
	if err != nil || len(values) != 2 || string(values["archive:x"]) != "a" || string(values["other"]) != "a-other" {
		t.Fatalf("logical bulk = %v, %v", values, err)
	}
	if err := a.InvalidatePrefix(ctx, "archive:"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.Get(ctx, "archive:x"); err != nil || found {
		t.Fatalf("invalidated entry still present: %v, %v", found, err)
	}
	if err := a.InvalidatePrefix(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.Get(ctx, "other"); err != nil || found {
		t.Fatalf("empty prefix failed: %v, %v", found, err)
	}
	if value, found, err := b.Get(ctx, "x"); err != nil || !found || string(value) != "b" {
		t.Fatalf("sibling namespace changed: %q, %v, %v", value, found, err)
	}
	if err := empty.InvalidatePrefix(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if value, found, err := b.Get(ctx, "x"); err != nil || !found || string(value) != "b" {
		t.Fatalf("empty namespace escaped: %q, %v, %v", value, found, err)
	}
	if _, ok := any(a).(PrefixDeleter); ok {
		t.Fatal("Cache must not advertise optional raw-store prefix support")
	}
}

func TestCacheUnsupportedPrefixIsReported(t *testing.T) {
	var ops []string
	c := New(coreOnlyStore{NewMemory()}, WithOnError(func(_ context.Context, op string, err error) {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("reported error = %v", err)
		}
		ops = append(ops, op)
	}))
	if err := c.InvalidatePrefix(context.Background(), ""); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("unsupported invalidation = %v", err)
	}
	if !reflect.DeepEqual(ops, []string{"invalidate_prefix"}) {
		t.Fatalf("reports = %v", ops)
	}
}

func TestJSONPreservesZeroAndNullValues(t *testing.T) {
	ctx := context.Background()
	c := New(NewMemory())
	if err := SetJSON(ctx, c, "false", false, 0); err != nil {
		t.Fatal(err)
	}
	if got, found, err := GetJSON[bool](ctx, c, "false"); err != nil || !found || got {
		t.Fatalf("false = %v, %v, %v", got, found, err)
	}
	if err := SetJSON(ctx, c, "zero", 0, 0); err != nil {
		t.Fatal(err)
	}
	if got, found, err := GetJSON[int](ctx, c, "zero"); err != nil || !found || got != 0 {
		t.Fatalf("zero = %v, %v, %v", got, found, err)
	}
	if err := SetJSON[*int](ctx, c, "null", nil, 0); err != nil {
		t.Fatal(err)
	}
	if got, found, err := GetJSON[*int](ctx, c, "null"); err != nil || !found || got != nil {
		t.Fatalf("null = %v, %v, %v", got, found, err)
	}
	if got, found, err := GetJSON[int](ctx, c, "absent"); err != nil || found || got != 0 {
		t.Fatalf("miss = %v, %v, %v", got, found, err)
	}
	loads := 0
	if got, err := GetOrLoadJSON(ctx, c, "false", 0, func(context.Context) (bool, error) { loads++; return true, nil }); err != nil || got || loads != 0 {
		t.Fatalf("false hit loaded: %v, %v, %d", got, err, loads)
	}
}

func TestGetOrLoadJSONMissHitAndExpiry(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	now := time.Now()
	store.now = func() time.Time { return now }
	c := New(store, WithNamespace("catalog"))
	loads := 0
	load := func(gotCtx context.Context) ([]string, error) {
		if gotCtx != ctx {
			t.Fatal("loader lost caller context")
		}
		loads++
		return []string{"fresh"}, nil
	}
	first, err := GetOrLoadJSON(ctx, c, "key", time.Minute, load)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = "caller mutation"
	second, err := GetOrLoadJSON(ctx, c, "key", time.Minute, load)
	if err != nil || loads != 1 || second[0] != "fresh" {
		t.Fatalf("hit = %v, %v, loads=%d", second, err, loads)
	}
	now = now.Add(time.Minute)
	if _, err := GetOrLoadJSON(ctx, c, "key", time.Minute, load); err != nil || loads != 2 {
		t.Fatalf("expired load = %v, loads=%d", err, loads)
	}
	if err := c.Delete(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetOrLoadJSON(ctx, c, "key", time.Minute, load); err != nil || loads != 3 {
		t.Fatalf("deleted load = %v, loads=%d", err, loads)
	}
}

func TestCacheStrictErrorsAndLoaderFallback(t *testing.T) {
	outage := errors.New("cache unavailable")
	ctx := context.Background()
	for _, stage := range []string{"read", "decode", "write", "encode"} {
		t.Run(stage, func(t *testing.T) {
			store := &controlledStore{Storer: NewMemory()}
			var ops []string
			c := New(store, WithOnError(func(_ context.Context, op string, err error) {
				if err == nil {
					t.Fatal("nil report")
				}
				ops = append(ops, op)
			}))
			switch stage {
			case "read":
				store.getErr = outage
			case "decode":
				if err := c.Set(ctx, "key", []byte("broken JSON"), 0); err != nil {
					t.Fatal(err)
				}
			case "write":
				store.setErr = outage
			}
			if stage == "read" || stage == "decode" {
				if _, _, err := GetJSON[int](ctx, c, "key"); err == nil {
					t.Fatal("explicit JSON read suppressed error")
				}
			} else if stage == "write" {
				if err := SetJSON(ctx, c, "key", 1, 0); !errors.Is(err, outage) {
					t.Fatalf("strict write = %v", err)
				}
			} else {
				if err := SetJSON(ctx, c, "key", make(chan int), 0); err == nil {
					t.Fatal("explicit JSON encode suppressed error")
				}
			}
			ops = nil
			loads := 0
			loaded := any(42)
			if stage == "encode" {
				loaded = make(chan int)
			}
			got, err := GetOrLoadJSON(ctx, c, "key", 0, func(context.Context) (any, error) { loads++; return loaded, nil })
			if err != nil || got != loaded || loads != 1 {
				t.Fatalf("fallback = %v, %v, loads=%d", got, err, loads)
			}
			want := map[string]string{"read": "get", "decode": "decode_json", "write": "set", "encode": "encode_json"}[stage]
			if !reflect.DeepEqual(ops, []string{want}) {
				t.Fatalf("reports=%v, want %s once", ops, want)
			}
			if stage == "decode" {
				if got, found, err := GetJSON[int](ctx, c, "key"); err != nil || !found || got != 42 {
					t.Fatalf("successful load did not repair corrupt value: %v, %v, %v", got, found, err)
				}
			}
		})
	}
}

func TestGetOrLoadJSONPreservesLoaderFailures(t *testing.T) {
	failure := errors.New("source unavailable")
	store := &controlledStore{Storer: Noop{}}
	c := New(store, WithOnError(func(context.Context, string, error) { t.Fatal("loader error reported as cache error") }))
	if _, err := GetOrLoadJSON(context.Background(), c, "key", 0, func(context.Context) (int, error) { return 7, failure }); !errors.Is(err, failure) {
		t.Fatalf("loader error = %v", err)
	}
	if store.setCalls != 0 {
		t.Fatal("failed load cached")
	}
}

func TestGetOrLoadJSONCancellationAndNegativeTTL(t *testing.T) {
	for _, stage := range []string{"before", "read", "load", "write", "negative TTL"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &controlledStore{Storer: Noop{}}
			c := New(store)
			ttl := time.Duration(0)
			switch stage {
			case "before":
				cancel()
			case "read":
				store.onGet = cancel
			case "write":
				store.onSet = cancel
			case "negative TTL":
				ttl = -1
			}
			loads := 0
			_, err := GetOrLoadJSON(ctx, c, "key", ttl, func(got context.Context) (int, error) {
				loads++
				if got != ctx {
					t.Fatal("detached loader context")
				}
				if stage == "load" {
					cancel()
				}
				return 1, nil
			})
			want := error(context.Canceled)
			if stage == "negative TTL" {
				want = sdk.ErrInvalidInput
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v, want %v", err, want)
			}
			if (stage == "before" || stage == "read" || stage == "negative TTL") && loads != 0 {
				t.Fatal("invalid/canceled request started loader")
			}
			if stage != "write" && store.setCalls != 0 {
				t.Fatal("invalid/canceled result cached")
			}
		})
	}
}

func TestNoopAndNilStoreDisableCaching(t *testing.T) {
	ctx := context.Background()
	for _, store := range []Storer{nil, Noop{}} {
		c := New(store)
		loads := 0
		for range 2 {
			if got, err := GetOrLoadJSON(ctx, c, "key", 0, func(context.Context) (int, error) { loads++; return loads, nil }); err != nil || got != loads {
				t.Fatalf("disabled load=%d,%v", got, err)
			}
		}
		if loads != 2 {
			t.Fatal("disabled cache retained data")
		}
		if values, err := c.GetMany(ctx, []string{"key"}); err != nil || len(values) != 0 {
			t.Fatalf("disabled bulk=%v,%v", values, err)
		}
		if err := c.InvalidatePrefix(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := (Noop{}).Set(ctx, "key", nil, -1); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	n := Noop{}
	_, _, getErr := n.Get(canceled, "key")
	_, manyErr := n.GetMany(canceled, nil)
	for _, err := range []error{getErr, manyErr, n.Set(canceled, "key", nil, 0), n.Delete(canceled, "key"), n.DeletePrefix(canceled, "")} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Noop cancellation=%v", err)
		}
	}
}

type failingCacheStore struct{ err error }

func (s failingCacheStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, s.err
}
func (s failingCacheStore) GetMany(context.Context, []string) (map[string][]byte, error) {
	return nil, s.err
}
func (s failingCacheStore) Set(context.Context, string, []byte, time.Duration) error { return s.err }
func (s failingCacheStore) Delete(context.Context, string) error                     { return s.err }
func (s failingCacheStore) DeletePrefix(context.Context, string) error               { return s.err }

func TestCacheRawOperationsPreserveAndReportErrors(t *testing.T) {
	outage := errors.New("storage unavailable")
	ctx := context.Background()
	var ops []string
	c := New(failingCacheStore{outage}, WithNamespace("private"), WithOnError(func(got context.Context, op string, err error) {
		if got != ctx || !errors.Is(err, outage) {
			t.Errorf("report context/error=%v/%v", got, err)
		}
		ops = append(ops, op)
	}))
	_, _, getErr := c.Get(ctx, "key")
	_, manyErr := c.GetMany(ctx, []string{"key"})
	for _, err := range []error{getErr, manyErr, c.Set(ctx, "key", []byte("secret value"), 0), c.Delete(ctx, "key"), c.InvalidatePrefix(ctx, "")} {
		if !errors.Is(err, outage) {
			t.Errorf("raw operation lost backend error: %v", err)
		}
	}
	if want := []string{"get", "get_many", "set", "delete", "invalidate_prefix"}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("reports=%v, want %v", ops, want)
	}
}

func TestCacheRawCancellationPreservesStoredValue(t *testing.T) {
	ctx := context.Background()
	c := New(NewMemory())
	if err := c.Set(ctx, "key", []byte("kept"), 0); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, getErr := c.Get(canceled, "key")
	_, manyErr := c.GetMany(canceled, nil)
	for _, err := range []error{getErr, manyErr, c.Set(canceled, "key", []byte("changed"), 0), c.Delete(canceled, "key"), c.InvalidatePrefix(canceled, "")} {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancellation=%v", err)
		}
	}
	if value, found, err := c.Get(ctx, "key"); err != nil || !found || string(value) != "kept" {
		t.Fatalf("canceled operation mutated data: %q,%v,%v", value, found, err)
	}
}

func TestCacheOptionsReplaceNamespaceAndCallback(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	shared := []Option{WithNamespace("first"), WithOnError(func(context.Context, string, error) { t.Fatal("replaced callback invoked") })}
	cache := New(store, append(shared, WithNamespace("final"), WithOnError(nil))...)
	if err := cache.Set(ctx, "key", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	value, found, err := New(store, WithNamespace("final")).Get(ctx, "key")
	if err != nil || !found || string(value) != "value" {
		t.Fatalf("selected namespace = %q, %v, %v", value, found, err)
	}
	if _, found, err := New(store, WithNamespace("first")).Get(ctx, "key"); err != nil || found {
		t.Fatalf("old namespace populated: %v, %v", found, err)
	}
	if err := cache.Set(ctx, "key", nil, -time.Second); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("negative TTL = %v", err)
	}
}

func TestCacheConstructorsRejectNilOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		construct func()
	}{
		{"cacher.New", func() { New(nil, nil) }},
		{"cacher.NewMemory", func() { NewMemory(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.name+": nil option" {
					t.Fatalf("panic = %v", got)
				}
			}()
			tc.construct()
		})
	}
}
