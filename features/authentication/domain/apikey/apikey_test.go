package apikey

import (
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

var base = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func TestNewValid(t *testing.T) {
	k, err := New(cryptids.IDGenerator{}, "sa-1", "deploy", "prefix12", "hash-abc", time.Time{}, base)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if k.ID == "" {
		t.Error("New did not generate an ID")
	}
	if k.ServiceAccountID != "sa-1" || k.KeyHash != "hash-abc" || k.KeyPrefix != "prefix12" {
		t.Errorf("New round-trip: %+v", k)
	}
	if !k.ExpiresAt.IsZero() {
		t.Errorf("zero expiry must stay zero (never-expires): %v", k.ExpiresAt)
	}
}

func TestNewRequiredFields(t *testing.T) {
	if _, err := New(cryptids.IDGenerator{}, "", "n", "p", "hash", time.Time{}, base); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("blank service account id: err=%v, want ErrInvalidInput", err)
	}
	if _, err := New(cryptids.IDGenerator{}, "sa-1", "n", "p", "", time.Time{}, base); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("blank key hash: err=%v, want ErrInvalidInput", err)
	}
}

func TestExpired(t *testing.T) {
	now := base
	// Never-expires (zero ExpiresAt) is never expired.
	live, _ := New(cryptids.IDGenerator{}, "sa-1", "n", "p", "h1", time.Time{}, base)
	if live.Expired(now) {
		t.Error("never-expiring key reported expired")
	}
	// Future expiry: not expired.
	future, _ := New(cryptids.IDGenerator{}, "sa-1", "n", "p", "h2", base.Add(time.Hour), base)
	if future.Expired(now) {
		t.Error("future-expiry key reported expired")
	}
	// Past expiry: expired.
	past, _ := New(cryptids.IDGenerator{}, "sa-1", "n", "p", "h3", base.Add(-time.Hour), base)
	if !past.Expired(now) {
		t.Error("past-expiry key reported not expired")
	}
}

func TestRevoked(t *testing.T) {
	k, _ := New(cryptids.IDGenerator{}, "sa-1", "n", "p", "h", time.Time{}, base)
	if k.Revoked() {
		t.Error("fresh key reported revoked")
	}
	k.RevokedAt = base
	if !k.Revoked() {
		t.Error("key with RevokedAt set reported not revoked")
	}
}
