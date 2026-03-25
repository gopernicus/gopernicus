package authorization

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cache"
)

const defaultCacheTTL = 30 * time.Second
const defaultCacheKeyPrefix = "authz"

// CacheStore wraps a Storer with caching for permission check methods
// and automatic cache invalidation on writes.
//
// Reads are cached with a short TTL (default 30s). Writes invalidate
// affected cache entries using pattern-based deletion.
type CacheStore struct {
	Storer
	cache     *cache.Cache
	ttl       time.Duration
	keyPrefix string
}

// CacheOption configures a CacheStore.
type CacheOption func(*CacheStore)

// WithCacheTTL sets the cache TTL. Default is 30 seconds.
func WithCacheTTL(ttl time.Duration) CacheOption {
	return func(s *CacheStore) { s.ttl = ttl }
}

// WithCacheKeyPrefix sets the cache key prefix. Default is "authz".
func WithCacheKeyPrefix(prefix string) CacheOption {
	return func(s *CacheStore) { s.keyPrefix = prefix }
}

// NewCacheStore creates a new caching authorization store.
// If c is nil, all operations pass through to the inner store (no caching).
func NewCacheStore(inner Storer, c *cache.Cache, opts ...CacheOption) *CacheStore {
	s := &CacheStore{
		Storer:    inner,
		cache:     c,
		ttl:       defaultCacheTTL,
		keyPrefix: defaultCacheKeyPrefix,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// =============================================================================
// Permission Checks (cached)
// =============================================================================

func (s *CacheStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	key := s.checkKey(resourceType, resourceID, relation, subjectType, subjectID)

	if result, found, err := cache.GetJSON[bool](s.cache, ctx, key); err == nil && found {
		return result, nil
	}

	result, err := s.Storer.CheckRelationWithGroupExpansion(ctx, resourceType, resourceID, relation, subjectType, subjectID)
	if err != nil {
		return false, err
	}

	_ = cache.SetJSON(s.cache, ctx, key, result, s.ttl)
	return result, nil
}

func (s *CacheStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	key := s.existsKey(resourceType, resourceID, relation, subjectType, subjectID)

	if result, found, err := cache.GetJSON[bool](s.cache, ctx, key); err == nil && found {
		return result, nil
	}

	result, err := s.Storer.CheckRelationExists(ctx, resourceType, resourceID, relation, subjectType, subjectID)
	if err != nil {
		return false, err
	}

	_ = cache.SetJSON(s.cache, ctx, key, result, s.ttl)
	return result, nil
}

func (s *CacheStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	result := make(map[string]bool, len(resourceIDs))
	var uncached []string

	// Check cache for each resource ID.
	for _, id := range resourceIDs {
		key := s.checkKey(resourceType, id, relation, subjectType, subjectID)
		if val, found, err := cache.GetJSON[bool](s.cache, ctx, key); err == nil && found {
			result[id] = val
		} else {
			uncached = append(uncached, id)
		}
	}

	if len(uncached) == 0 {
		return result, nil
	}

	// Query DB for cache misses.
	dbResults, err := s.Storer.CheckBatchDirect(ctx, resourceType, uncached, relation, subjectType, subjectID)
	if err != nil {
		return nil, err
	}

	// Merge and cache results.
	for _, id := range uncached {
		allowed := dbResults[id]
		result[id] = allowed
		key := s.checkKey(resourceType, id, relation, subjectType, subjectID)
		_ = cache.SetJSON(s.cache, ctx, key, allowed, s.ttl)
	}

	return result, nil
}

func (s *CacheStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	key := s.targetsKey(resourceType, resourceID, relation)

	if result, found, err := cache.GetJSON[[]RelationTarget](s.cache, ctx, key); err == nil && found {
		return result, nil
	}

	result, err := s.Storer.GetRelationTargets(ctx, resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}

	_ = cache.SetJSON(s.cache, ctx, key, result, s.ttl)
	return result, nil
}

// =============================================================================
// Relationship CRUD (pass-through with invalidation)
// =============================================================================

func (s *CacheStore) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	if err := s.Storer.CreateRelationships(ctx, relationships); err != nil {
		return err
	}
	s.invalidateForRelationships(ctx, relationships)
	return nil
}

func (s *CacheStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	if err := s.Storer.DeleteResourceRelationships(ctx, resourceType, resourceID); err != nil {
		return err
	}
	_ = s.cache.DeletePattern(ctx, s.keyPrefix+":*:"+resourceType+":"+resourceID+":*")
	return nil
}

func (s *CacheStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	if err := s.Storer.DeleteRelationship(ctx, resourceType, resourceID, relation, subjectType, subjectID); err != nil {
		return err
	}
	s.invalidateForTuple(ctx, resourceType, resourceID, subjectType, subjectID)
	return nil
}

func (s *CacheStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	if err := s.Storer.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID); err != nil {
		return err
	}
	s.invalidateForTuple(ctx, resourceType, resourceID, subjectType, subjectID)
	return nil
}

// =============================================================================
// Cache Keys
// =============================================================================

func (s *CacheStore) checkKey(resourceType, resourceID, relation, subjectType, subjectID string) string {
	return fmt.Sprintf("%s:check:%s:%s:%s:%s:%s", s.keyPrefix, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *CacheStore) existsKey(resourceType, resourceID, relation, subjectType, subjectID string) string {
	return fmt.Sprintf("%s:exists:%s:%s:%s:%s:%s", s.keyPrefix, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *CacheStore) targetsKey(resourceType, resourceID, relation string) string {
	return fmt.Sprintf("%s:targets:%s:%s:%s", s.keyPrefix, resourceType, resourceID, relation)
}

// =============================================================================
// Cache Invalidation
// =============================================================================

func (s *CacheStore) invalidateForRelationships(ctx context.Context, relationships []CreateRelationship) {
	for _, r := range relationships {
		s.invalidateForTuple(ctx, r.ResourceType, r.ResourceID, r.SubjectType, r.SubjectID)
	}
}

func (s *CacheStore) invalidateForTuple(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) {
	// Invalidate all checks/exists/targets for this resource.
	_ = s.cache.DeletePattern(ctx, fmt.Sprintf("%s:*:%s:%s:*", s.keyPrefix, resourceType, resourceID))
	// Invalidate all checks/exists involving this subject.
	_ = s.cache.DeletePattern(ctx, fmt.Sprintf("%s:*:*:*:*:%s:%s", s.keyPrefix, subjectType, subjectID))
}
