package cache_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache"
)

// =============================================================================
// Mock Cacher
// =============================================================================

type mockCacher struct {
	getData    []byte
	getFound   bool
	getErr     error
	setErr     error
	deleteErr  error
	patternErr error
	closeErr   error

	getManyData map[string][]byte
	getManyErr  error

	lastKey     string
	lastValue   []byte
	lastTTL     time.Duration
	lastPattern string
	lastKeys    []string
}

func (m *mockCacher) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.lastKey = key
	return m.getData, m.getFound, m.getErr
}

func (m *mockCacher) GetMany(_ context.Context, keys []string) (map[string][]byte, error) {
	m.lastKeys = keys
	return m.getManyData, m.getManyErr
}

func (m *mockCacher) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.lastKey = key
	m.lastValue = value
	m.lastTTL = ttl
	return m.setErr
}

func (m *mockCacher) Delete(_ context.Context, key string) error {
	m.lastKey = key
	return m.deleteErr
}

func (m *mockCacher) DeletePattern(_ context.Context, pattern string) error {
	m.lastPattern = pattern
	return m.patternErr
}

func (m *mockCacher) Close() error {
	return m.closeErr
}

// =============================================================================
// Cache Service Tests
// =============================================================================

func TestNew_NilCacher(t *testing.T) {
	c := cache.New(nil)
	if c != nil {
		t.Error("New(nil) should return nil")
	}
}

func TestNew_ValidCacher(t *testing.T) {
	c := cache.New(&mockCacher{})
	if c == nil {
		t.Error("New() with valid cacher should not return nil")
	}
}

func TestGet_DelegatesToCacher(t *testing.T) {
	m := &mockCacher{
		getData:  []byte("hello"),
		getFound: true,
	}
	c := cache.New(m)
	ctx := context.Background()

	data, found, err := c.Get(ctx, "test-key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !found {
		t.Error("Get() found = false, want true")
	}
	if string(data) != "hello" {
		t.Errorf("Get() data = %q, want %q", string(data), "hello")
	}
	if m.lastKey != "test-key" {
		t.Errorf("cacher received key = %q, want %q", m.lastKey, "test-key")
	}
}

func TestGet_PropagatesError(t *testing.T) {
	m := &mockCacher{getErr: errors.New("redis down")}
	c := cache.New(m)

	_, _, err := c.Get(context.Background(), "key")
	if err == nil {
		t.Error("Get() should propagate error")
	}
}

func TestGetMany_DelegatesToCacher(t *testing.T) {
	m := &mockCacher{
		getManyData: map[string][]byte{
			"a": []byte("1"),
			"b": []byte("2"),
		},
	}
	c := cache.New(m)

	result, err := c.GetMany(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("GetMany() error = %v", err)
	}
	if len(result) != 2 {
		t.Errorf("GetMany() returned %d entries, want 2", len(result))
	}
}

func TestSet_DelegatesToCacher(t *testing.T) {
	m := &mockCacher{}
	c := cache.New(m)

	err := c.Set(context.Background(), "key", []byte("value"), 5*time.Minute)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if m.lastKey != "key" {
		t.Errorf("cacher received key = %q, want %q", m.lastKey, "key")
	}
	if string(m.lastValue) != "value" {
		t.Errorf("cacher received value = %q, want %q", string(m.lastValue), "value")
	}
	if m.lastTTL != 5*time.Minute {
		t.Errorf("cacher received ttl = %v, want %v", m.lastTTL, 5*time.Minute)
	}
}

func TestDelete_DelegatesToCacher(t *testing.T) {
	m := &mockCacher{}
	c := cache.New(m)

	err := c.Delete(context.Background(), "delete-key")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if m.lastKey != "delete-key" {
		t.Errorf("cacher received key = %q, want %q", m.lastKey, "delete-key")
	}
}

func TestDeletePattern_DelegatesToCacher(t *testing.T) {
	m := &mockCacher{}
	c := cache.New(m)

	err := c.DeletePattern(context.Background(), "users:*")
	if err != nil {
		t.Fatalf("DeletePattern() error = %v", err)
	}
	if m.lastPattern != "users:*" {
		t.Errorf("cacher received pattern = %q, want %q", m.lastPattern, "users:*")
	}
}

func TestClose_DelegatesToCacher(t *testing.T) {
	m := &mockCacher{closeErr: errors.New("close failed")}
	c := cache.New(m)

	err := c.Close()
	if err == nil {
		t.Error("Close() should propagate error")
	}
}

// =============================================================================
// JSON Convenience Tests
// =============================================================================

type testItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestGetJSON_Success(t *testing.T) {
	item := testItem{Name: "test", Count: 42}
	data, _ := json.Marshal(item)

	m := &mockCacher{getData: data, getFound: true}
	c := cache.New(m)

	result, found, err := cache.GetJSON[testItem](c, context.Background(), "key")
	if err != nil {
		t.Fatalf("GetJSON() error = %v", err)
	}
	if !found {
		t.Error("GetJSON() found = false, want true")
	}
	if result.Name != "test" || result.Count != 42 {
		t.Errorf("GetJSON() result = %+v, want %+v", result, item)
	}
}

func TestGetJSON_NotFound(t *testing.T) {
	m := &mockCacher{getFound: false}
	c := cache.New(m)

	_, found, err := cache.GetJSON[testItem](c, context.Background(), "key")
	if err != nil {
		t.Fatalf("GetJSON() error = %v", err)
	}
	if found {
		t.Error("GetJSON() found = true, want false")
	}
}

func TestGetJSON_NilCache(t *testing.T) {
	_, found, err := cache.GetJSON[testItem](nil, context.Background(), "key")
	if err != nil {
		t.Fatalf("GetJSON(nil) error = %v", err)
	}
	if found {
		t.Error("GetJSON(nil) found = true, want false")
	}
}

func TestGetJSON_InvalidJSON(t *testing.T) {
	m := &mockCacher{getData: []byte("not json"), getFound: true}
	c := cache.New(m)

	_, _, err := cache.GetJSON[testItem](c, context.Background(), "key")
	if err == nil {
		t.Error("GetJSON() with invalid JSON should return error")
	}
}

func TestSetJSON_Success(t *testing.T) {
	m := &mockCacher{}
	c := cache.New(m)

	item := testItem{Name: "test", Count: 42}
	err := cache.SetJSON(c, context.Background(), "key", item, time.Minute)
	if err != nil {
		t.Fatalf("SetJSON() error = %v", err)
	}

	// Verify the stored value is valid JSON.
	var stored testItem
	if err := json.Unmarshal(m.lastValue, &stored); err != nil {
		t.Fatalf("stored value is not valid JSON: %v", err)
	}
	if stored.Name != "test" || stored.Count != 42 {
		t.Errorf("stored value = %+v, want %+v", stored, item)
	}
}

func TestSetJSON_NilCache(t *testing.T) {
	err := cache.SetJSON(nil, context.Background(), "key", testItem{}, time.Minute)
	if err != nil {
		t.Fatalf("SetJSON(nil) error = %v", err)
	}
}

// =============================================================================
// StatusCheck Tests
// =============================================================================

func TestStatusCheck_NilCache(t *testing.T) {
	err := cache.StatusCheck(context.Background(), nil)
	if err != nil {
		t.Fatalf("StatusCheck(nil) error = %v", err)
	}
}

func TestStatusCheck_Success(t *testing.T) {
	m := &mockCacher{getFound: true, getData: []byte("ok")}
	c := cache.New(m)

	err := cache.StatusCheck(context.Background(), c)
	if err != nil {
		t.Fatalf("StatusCheck() error = %v", err)
	}
}

func TestStatusCheck_SetError(t *testing.T) {
	m := &mockCacher{setErr: errors.New("set failed")}
	c := cache.New(m)

	err := cache.StatusCheck(context.Background(), c)
	if err == nil {
		t.Error("StatusCheck() should return error when set fails")
	}
}

func TestStatusCheck_GetError(t *testing.T) {
	m := &mockCacher{getErr: errors.New("get failed")}
	c := cache.New(m)

	err := cache.StatusCheck(context.Background(), c)
	if err == nil {
		t.Error("StatusCheck() should return error when get fails")
	}
}
