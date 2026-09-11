// Package cacher provides byte storage, application-data caching and optional
// public-page caching. Hosts own cache keys, freshness and invalidation policy.
package cacher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Storer stores opaque bytes. Implementations are concurrency safe, copy writes,
// and return independently owned reads. Empty values are hits; absent/expired
// keys are misses. Capacity eviction may remove entries before their TTL.
// Canceled operations return the context error; negative TTL is invalid input.
// Resource lifecycle belongs to the adapter's owner, not this port.
type Storer interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// GetMany returns only present keys, in one API call. Physical round trips
	// depend on the adapter. Duplicate keys appear once in the returned map.
	GetMany(ctx context.Context, keys []string) (map[string][]byte, error)
	// Set replaces the value and its TTL. Zero TTL means no expiration.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete succeeds even when the key is absent.
	Delete(ctx context.Context, key string) error
}

// PrefixDeleter optionally deletes literal key prefixes. Metacharacters have no
// special meaning. An empty prefix deletes all keys in the adapter's namespace.
// Deletion is not atomic with concurrent writers or in-flight loads.
type PrefixDeleter interface {
	DeletePrefix(ctx context.Context, prefix string) error
}

// Option configures an application-data cache at construction.
type Option func(*config)

type config struct {
	namespace string
	onError   func(context.Context, string, error)
}

// WithNamespace separates logical keys sharing a store, including the empty
// namespace. It is length-framed to prevent ambiguous namespace/key pairs.
// Repeated options replace the namespace.
func WithNamespace(namespace string) Option {
	return func(c *config) { c.namespace = namespace }
}

// WithOnError reports cache/codec failures with an operation and cause, without
// adding keys or payloads. It runs synchronously and must be concurrency safe.
// Operations: get, get_many, set, delete, invalidate_prefix, decode_json,
// encode_json. Loader errors are authoritative and are not reported here.
// Repeated options replace the callback; nil disables reporting.
func WithOnError(report func(context.Context, string, error)) Option {
	return func(c *config) { c.onError = report }
}

// Cache namespaces raw storage and reports failures. Raw and explicit JSON
// operations return errors; only GetOrLoadJSON tolerates cache/codec outages.
// Construct it with New.
type Cache struct {
	storer  Storer
	prefix  string
	onError func(context.Context, string, error)
}

// New constructs a Cache. A nil store selects Noop. Typed-nil adapters are not
// supported. The host retains ownership of the store and its resources.
// A nil option panics.
func New(storer Storer, opts ...Option) *Cache {
	var cfg config
	for _, opt := range opts {
		if opt == nil {
			panic("cacher.New: nil option")
		}
		opt(&cfg)
	}
	if storer == nil {
		storer = Noop{}
	}
	return &Cache{storer: storer, prefix: strconv.Itoa(len(cfg.namespace)) + ":" + cfg.namespace + ":", onError: cfg.onError}
}

// Get retrieves a logical key without suppressing storage errors.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, c.report(ctx, "get", err)
	}
	value, found, err := c.storer.Get(ctx, c.prefix+key)
	if err = c.finish(ctx, "get", err); err != nil {
		return nil, false, err
	}
	return value, found, nil
}

// GetMany returns present logical keys without exposing the physical namespace.
func (c *Cache) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, c.report(ctx, "get_many", err)
	}
	physical := make([]string, len(keys))
	for i, key := range keys {
		physical[i] = c.prefix + key
	}
	values, err := c.storer.GetMany(ctx, physical)
	if err = c.finish(ctx, "get_many", err); err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(values))
	for i, key := range keys {
		if value, found := values[physical[i]]; found {
			result[key] = value
		}
	}
	return result, nil
}

// Set replaces a logical key's bytes and TTL. Zero TTL means no expiration.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := validateSet(ctx, ttl); err != nil {
		return c.report(ctx, "set", err)
	}
	return c.finish(ctx, "set", c.storer.Set(ctx, c.prefix+key, value, ttl))
}

// Delete removes a logical key, succeeding when it is absent.
func (c *Cache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return c.report(ctx, "delete", err)
	}
	return c.finish(ctx, "delete", c.storer.Delete(ctx, c.prefix+key))
}

// InvalidatePrefix deletes a literal logical prefix within this namespace.
// It returns errors.ErrUnsupported when the adapter lacks PrefixDeleter.
// Cache itself deliberately does not implement the optional PrefixDeleter port.
func (c *Cache) InvalidatePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return c.report(ctx, "invalidate_prefix", err)
	}
	store, ok := c.storer.(PrefixDeleter)
	if !ok {
		return c.report(ctx, "invalidate_prefix", errors.ErrUnsupported)
	}
	return c.finish(ctx, "invalidate_prefix", store.DeletePrefix(ctx, c.prefix+prefix))
}

func (c *Cache) report(ctx context.Context, operation string, err error) error {
	if err != nil && c.onError != nil {
		c.onError(ctx, operation, err)
	}
	return err
}

func (c *Cache) finish(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return c.report(ctx, operation, err)
}

func validateSet(ctx context.Context, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl < 0 {
		return fmt.Errorf("cacher: TTL must not be negative: %w", sdk.ErrInvalidInput)
	}
	return nil
}
