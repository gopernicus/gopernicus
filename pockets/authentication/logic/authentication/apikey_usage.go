package authentication

import (
	"context"
	"sync"
	"time"
)

// DefaultAPIKeyLastUsedInterval is the most often a key's LastUsedAt is
// written when MachineConfig.APIKeyLastUsedInterval is zero.
const DefaultAPIKeyLastUsedInterval = 24 * time.Hour

// lastUsedSweepThreshold is the throttle size at which an admission prunes
// marks older than the interval, so the map holds roughly the active keys.
const lastUsedSweepThreshold = 1024

// lastUsedThrottle admits at most one LastUsedAt write per key per interval.
// It is per process: several instances each write at most once per interval,
// last write wins. A crash loses at most one interval of usage; this is a
// coarse usage signal, not audit. A nil throttle admits nothing (opt-out).
type lastUsedThrottle struct {
	interval time.Duration
	mu       sync.Mutex
	marks    map[string]time.Time
}

// newLastUsedThrottle resolves interval: zero selects the default, negative
// disables usage writes (nil).
func newLastUsedThrottle(interval time.Duration) *lastUsedThrottle {
	if interval < 0 {
		return nil
	}
	if interval == 0 {
		interval = DefaultAPIKeyLastUsedInterval
	}
	return &lastUsedThrottle{interval: interval, marks: map[string]time.Time{}}
}

// admit reports whether keyID's LastUsedAt should be written at now, and if so
// records the mark first, so a failed write is not retried until the next
// interval.
func (t *lastUsedThrottle) admit(keyID string, now time.Time) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.marks[keyID]; ok && now.Sub(last) < t.interval {
		return false
	}
	if len(t.marks) >= lastUsedSweepThreshold {
		for id, last := range t.marks {
			if now.Sub(last) >= t.interval {
				delete(t.marks, id)
			}
		}
	}
	t.marks[keyID] = now
	return true
}

// touchAPIKeyLastUsed writes keyID's LastUsedAt when the throttle admits it.
// A failure never fails authentication; it is logged with coarse fields.
func (s *Service) touchAPIKeyLastUsed(ctx context.Context, keyID string, now time.Time) {
	if !s.lastUsed.admit(keyID, now) {
		return
	}
	if err := s.apiKeys.TouchLastUsed(ctx, keyID, now); err != nil {
		s.logger.Warn("api key touch-last-used failed", "error_kind", ErrorKind(err))
	}
}
