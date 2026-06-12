// gopernicus:bootstrap kind=cache/cache.go template=ee33d743aa6c
// This file is created once by gopernicus and will NOT be overwritten.
// CacheStore is defined in generated_cache.go. Add custom cache methods here.
//
// Example — custom invalidation on a domain event:
//
//	func (s *CacheStore) InvalidateForTenant(ctx context.Context, tenantID string) {
//		_ = s.cache.DeletePattern(ctx, s.config.KeyPrefix+":*")
//	}

package jobschedules
