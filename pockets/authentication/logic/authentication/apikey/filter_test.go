package apikey

import (
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestKeyStatusAndFilter(t *testing.T) {
	now := base.Add(time.Hour)
	live := APIKey{}
	boundary := APIKey{ExpiresAt: now}
	revokedExpired := APIKey{ExpiresAt: base, RevokedAt: base}
	for _, c := range []struct {
		key  APIKey
		want Status
	}{{live, StatusActive}, {boundary, StatusExpired}, {revokedExpired, StatusRevoked}} {
		if got := c.key.Status(now); got != c.want {
			t.Errorf("Status(%+v) = %q, want %q (expiry at-or-before, revoked first)", c.key, got, c.want)
		}
	}
	if !(Filter{NeverExpires: true, Now: now}).Matches(live) || (Filter{NeverExpires: true, Now: now}).Matches(boundary) {
		t.Error("NeverExpires must admit exactly the keys without an expiry")
	}
	if (Filter{Status: StatusExpired, Now: now}).Matches(revokedExpired) {
		t.Error("a revoked key matched the expired filter")
	}
	if err := (Filter{Status: "bogus"}).Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("unknown status = %v", err)
	}
	if err := (Filter{Status: StatusExpired, NeverExpires: true}).Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("contradiction = %v", err)
	}
	if err := (Filter{Status: StatusRevoked, NeverExpires: true}).Validate(); err != nil {
		t.Errorf("revoked and never-expiring = %v, want valid", err)
	}
}
