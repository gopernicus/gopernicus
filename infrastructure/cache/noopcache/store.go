// Package noopcache provides a no-op cache implementation.
// Use this for testing or when caching is disabled.
package noopcache

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache"
)

// Store is a no-op cache that does nothing.
// All operations succeed but store/retrieve nothing.
type Store struct{}

// New creates a new noop cache store.
func New() *Store {
	return &Store{}
}

// Get always returns not found.
func (s *Store) Get(_ context.Context, _ string) ([]byte, bool, error) {
	return nil, false, nil
}

// GetMany always returns empty map.
func (s *Store) GetMany(_ context.Context, _ []string) (map[string][]byte, error) {
	return make(map[string][]byte), nil
}

// Set does nothing.
func (s *Store) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}

// Delete does nothing.
func (s *Store) Delete(_ context.Context, _ string) error {
	return nil
}

// DeletePattern does nothing.
func (s *Store) DeletePattern(_ context.Context, _ string) error {
	return nil
}

// Close does nothing.
func (s *Store) Close() error {
	return nil
}

// Compile-time interface check.
var _ cache.Cacher = (*Store)(nil)
