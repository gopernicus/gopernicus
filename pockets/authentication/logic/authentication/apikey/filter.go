package apikey

import (
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// The derived key statuses a listing filters on. Revoked takes precedence over
// expired.
const (
	StatusActive  Status = "active"
	StatusExpired Status = "expired"
	StatusRevoked Status = "revoked"
)

// Status is a key's derived status.
type Status string

// Filter narrows ListByServiceAccountFiltered. A zero Status admits every
// status; NeverExpires admits only keys without an expiry. Now is the instant
// expiry is judged at, exactly as APIKey.Expired judges it (at or before).
type Filter struct {
	Status       Status
	NeverExpires bool
	Now          time.Time
}

// Status derives the key's status at now: revoked, else expired, else active.
func (k APIKey) Status(now time.Time) Status {
	switch {
	case k.Revoked():
		return StatusRevoked
	case k.Expired(now):
		return StatusExpired
	}
	return StatusActive
}

// Validate refuses an unknown status and the contradiction of an expired key
// that never expires; both wrap sdk.ErrInvalidInput.
func (f Filter) Validate() error {
	switch f.Status {
	case "", StatusActive, StatusExpired, StatusRevoked:
	default:
		return fmt.Errorf("apikey: unknown status filter %q: %w", f.Status, sdk.ErrInvalidInput)
	}
	if f.Status == StatusExpired && f.NeverExpires {
		return fmt.Errorf("apikey: an expired key cannot be one that never expires: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// Matches is the filter's predicate, the oracle every store's SQL is held to.
func (f Filter) Matches(k APIKey) bool {
	if f.NeverExpires && !k.ExpiresAt.IsZero() {
		return false
	}
	return f.Status == "" || k.Status(f.Now) == f.Status
}
