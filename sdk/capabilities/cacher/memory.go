package cacher

import (
	"bytes"
	"container/list"
	"context"
	"strings"
	"sync"
	"time"
)

const defaultMaxEntries = 10_000

var (
	_ Storer        = (*Memory)(nil)
	_ PrefixDeleter = (*Memory)(nil)
)

// MemoryOption configures an in-process cache at construction.
type MemoryOption func(*memoryConfig)

type memoryConfig struct {
	maxEntries int
}

// WithMaxEntries bounds the cache's key count, not payload bytes. Nonpositive
// values select the default of 10,000. Repeated options replace the limit.
func WithMaxEntries(n int) MemoryOption {
	return func(c *memoryConfig) { c.maxEntries = n }
}

// Memory is a bounded, concurrency-safe cache with TTL and least-recently-used
// eviction. Construct it with NewMemory; its zero value is not initialized.
// Expired values are reclaimed on access and before capacity evicts live values.
// It starts no janitor and owns no resources requiring shutdown.
type Memory struct {
	mu         sync.Mutex
	data       map[string]*list.Element
	lru        list.List
	maxEntries int
	now        func() time.Time
}

type memEntry struct {
	key     string
	value   []byte
	expires time.Time
}

// NewMemory returns an empty bounded cache. A nil option panics.
func NewMemory(opts ...MemoryOption) *Memory {
	var cfg memoryConfig
	for _, opt := range opts {
		if opt == nil {
			panic("cacher.NewMemory: nil option")
		}
		opt(&cfg)
	}
	if cfg.maxEntries <= 0 {
		cfg.maxEntries = defaultMaxEntries
	}
	return &Memory{data: make(map[string]*list.Element), maxEntries: cfg.maxEntries, now: time.Now}
}

// Get returns independently owned bytes and refreshes a present entry's recency.
func (s *Memory) Get(ctx context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	value, found := s.get(key, s.now())
	return value, found, nil
}

// GetMany reads present values and refreshes recency in requested-key order.
func (s *Memory) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(keys))
	now := s.now()
	for _, key := range keys {
		if value, found := s.get(key, now); found {
			out[key] = value
		}
	}
	return out, nil
}

func (s *Memory) get(key string, now time.Time) ([]byte, bool) {
	element, found := s.data[key]
	if !found {
		return nil, false
	}
	entry := element.Value.(*memEntry)
	if !entry.expires.IsZero() && !now.Before(entry.expires) {
		s.remove(element)
		return nil, false
	}
	s.lru.MoveToFront(element)
	return bytes.Clone(entry.value), true
}

// Set copies value and replaces the prior TTL. Zero TTL means no expiration.
func (s *Memory) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateSet(ctx, ttl); err != nil {
		return err
	}
	now := s.now()
	entry := &memEntry{key: key, value: bytes.Clone(value)}
	if ttl > 0 {
		entry.expires = now.Add(ttl)
	}
	if element, found := s.data[key]; found {
		element.Value = entry
		s.lru.MoveToFront(element)
		return nil
	}
	if len(s.data) >= s.maxEntries {
		for _, element := range s.data {
			expires := element.Value.(*memEntry).expires
			if !expires.IsZero() && !now.Before(expires) {
				s.remove(element)
			}
		}
		if len(s.data) >= s.maxEntries {
			s.remove(s.lru.Back())
		}
	}
	s.data[key] = s.lru.PushFront(entry)
	return nil
}

// Delete removes a key if present.
func (s *Memory) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if element, found := s.data[key]; found {
		s.remove(element)
	}
	return nil
}

// DeletePrefix removes literal prefixes. An empty prefix clears this Memory.
func (s *Memory) DeletePrefix(ctx context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for key, element := range s.data {
		if strings.HasPrefix(key, prefix) {
			s.remove(element)
		}
	}
	return nil
}

func (s *Memory) remove(element *list.Element) {
	delete(s.data, element.Value.(*memEntry).key)
	s.lru.Remove(element)
}
