package cacher

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Memory is the in-memory, TTL-aware default Storer that ships with sdk — no
// external dependency, handy for development and single-node deployments.
// (A distributed Storer like redis is a separate integration module.)
type Memory struct {
	mu   sync.RWMutex
	data map[string]memEntry
	now  func() time.Time
}

type memEntry struct {
	value   []byte
	expires time.Time // zero = no expiry
}

var _ Storer = (*Memory)(nil)

// NewMemory returns an empty in-memory Storer.
func NewMemory() *Memory {
	return &Memory{data: map[string]memEntry{}, now: time.Now}
}

func (s *Memory) live(e memEntry) bool {
	return e.expires.IsZero() || e.expires.After(s.now())
}

// Get retrieves a value by key. found is false for missing or expired keys.
func (s *Memory) Get(ctx context.Context, key string) ([]byte, bool, error) {
	s.mu.RLock()
	e, ok := s.data[key]
	s.mu.RUnlock()
	if !ok || !s.live(e) {
		return nil, false, nil
	}
	return e.value, true, nil
}

// GetMany retrieves multiple values in one call.
func (s *Memory) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(keys))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range keys {
		if e, ok := s.data[k]; ok && s.live(e) {
			out[k] = e.value
		}
	}
	return out, nil
}

// Set stores a value with TTL (0 = no expiry).
func (s *Memory) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	var exp time.Time
	if ttl > 0 {
		exp = s.now().Add(ttl)
	}
	s.mu.Lock()
	s.data[key] = memEntry{value: value, expires: exp}
	s.mu.Unlock()
	return nil
}

// Delete removes a single key.
func (s *Memory) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
	return nil
}

// DeletePattern removes keys matching a "prefix*" glob (the common case).
func (s *Memory) DeletePattern(ctx context.Context, pattern string) error {
	prefix := strings.TrimSuffix(pattern, "*")
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			delete(s.data, k)
		}
	}
	return nil
}

// Close releases resources (none for the in-memory store).
func (s *Memory) Close() error { return nil }
