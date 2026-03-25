// Package memorycache provides an in-memory LRU cache implementation.
// Suitable for single-instance deployments and testing.
package memorycache

import (
	"container/list"
	"context"
	"strings"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache"
)

// Config holds memory cache configuration.
type Config struct {
	MaxEntries int // Maximum number of entries before LRU eviction.
}

// entry represents a cached item.
type entry struct {
	key       string
	value     []byte
	expiresAt time.Time // Zero means no expiration.
}

// Store is an in-memory LRU cache.
type Store struct {
	mu         sync.RWMutex
	maxEntries int
	items      map[string]*list.Element
	lru        *list.List
}

// New creates a new memory cache store.
func New(cfg Config) *Store {
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 10000
	}

	return &Store{
		maxEntries: maxEntries,
		items:      make(map[string]*list.Element),
		lru:        list.New(),
	}
}

// Get retrieves a value by key.
func (s *Store) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	elem, ok := s.items[key]
	if !ok {
		return nil, false, nil
	}

	e := elem.Value.(*entry)

	// Check expiration.
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		s.removeElement(elem)
		return nil, false, nil
	}

	// Move to front (most recently used).
	s.lru.MoveToFront(elem)

	// Return a copy to prevent mutation.
	result := make([]byte, len(e.value))
	copy(result, e.value)
	return result, true, nil
}

// GetMany retrieves multiple values by keys.
func (s *Store) GetMany(_ context.Context, keys []string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make(map[string][]byte, len(keys))
	now := time.Now()

	for _, key := range keys {
		elem, ok := s.items[key]
		if !ok {
			continue
		}

		e := elem.Value.(*entry)

		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			s.removeElement(elem)
			continue
		}

		s.lru.MoveToFront(elem)

		valueCopy := make([]byte, len(e.value))
		copy(valueCopy, e.value)
		result[key] = valueCopy
	}

	return result, nil
}

// Set stores a value with TTL.
func (s *Store) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// Make a copy of the value.
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	// Update existing entry if present.
	if elem, ok := s.items[key]; ok {
		s.lru.MoveToFront(elem)
		e := elem.Value.(*entry)
		e.value = valueCopy
		e.expiresAt = expiresAt
		return nil
	}

	// Add new entry.
	e := &entry{
		key:       key,
		value:     valueCopy,
		expiresAt: expiresAt,
	}
	elem := s.lru.PushFront(e)
	s.items[key] = elem

	// Evict oldest if over capacity.
	for s.lru.Len() > s.maxEntries {
		oldest := s.lru.Back()
		if oldest != nil {
			s.removeElement(oldest)
		}
	}

	return nil
}

// Delete removes a single key.
func (s *Store) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, ok := s.items[key]; ok {
		s.removeElement(elem)
	}
	return nil
}

// DeletePattern removes all keys matching pattern.
// Supports simple glob patterns with * wildcard.
func (s *Store) DeletePattern(_ context.Context, pattern string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var toDelete []*list.Element

	for key, elem := range s.items {
		if matchPattern(pattern, key) {
			toDelete = append(toDelete, elem)
		}
	}

	for _, elem := range toDelete {
		s.removeElement(elem)
	}

	return nil
}

// Close clears the cache.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = make(map[string]*list.Element)
	s.lru.Init()
	return nil
}

// Len returns the number of entries in the cache.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// removeElement removes an element from the cache (caller must hold lock).
func (s *Store) removeElement(elem *list.Element) {
	e := elem.Value.(*entry)
	delete(s.items, e.key)
	s.lru.Remove(elem)
}

// matchPattern performs simple glob matching with * wildcard.
func matchPattern(pattern, key string) bool {
	if pattern == key {
		return true
	}

	// Single wildcard at end: "prefix*"
	if strings.HasSuffix(pattern, "*") && !strings.Contains(pattern[:len(pattern)-1], "*") {
		return strings.HasPrefix(key, pattern[:len(pattern)-1])
	}

	// Single wildcard at start: "*suffix"
	if strings.HasPrefix(pattern, "*") && !strings.Contains(pattern[1:], "*") {
		return strings.HasSuffix(key, pattern[1:])
	}

	// Wildcard in middle: "prefix*suffix"
	if idx := strings.Index(pattern, "*"); idx > 0 && idx == strings.LastIndex(pattern, "*") {
		prefix := pattern[:idx]
		suffix := pattern[idx+1:]
		return strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix)
	}

	// Multi-wildcard: split and check all parts.
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(key[pos:], part)
		if idx == -1 {
			return false
		}
		if i == 0 && !strings.HasPrefix(pattern, "*") && idx != 0 {
			return false
		}
		if i == len(parts)-1 && !strings.HasSuffix(pattern, "*") && pos+idx+len(part) != len(key) {
			return false
		}
		pos += idx + len(part)
	}
	return true
}

// Compile-time interface check.
var _ cache.Cacher = (*Store)(nil)
