package contactchange

import (
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
)

var base = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func TestNewPopulatesFieldsAndExpiry(t *testing.T) {
	uses := identifier.Uses{Login: true, Recovery: false, Notification: true}
	p := New("u1", identifier.KindEmail, "new@example.com", uses, true, "old-id", time.Hour, base)

	if p.ID != "" {
		t.Errorf("ID = %q, want empty (store assigns the DB-generated key)", p.ID)
	}
	if p.UserID != "u1" || p.Kind != identifier.KindEmail || p.NewValue != "new@example.com" {
		t.Errorf("subject/kind/value not set: %+v", p)
	}
	if !p.LoginEnabled || p.RecoveryEnabled || !p.NotificationEnabled {
		t.Errorf("use flags not copied: %+v", p)
	}
	if !p.MakePrimary || p.ReplacesIdentifierID != "old-id" {
		t.Errorf("primary/replacement intent not set: %+v", p)
	}
	if !p.CreatedAt.Equal(base) {
		t.Errorf("CreatedAt = %v, want %v", p.CreatedAt, base)
	}
	if !p.ExpiresAt.Equal(base.Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want %v", p.ExpiresAt, base.Add(time.Hour))
	}
}

func TestUsesRoundTrips(t *testing.T) {
	uses := identifier.Uses{Login: false, Recovery: true, Notification: true}
	p := New("u1", identifier.KindPhone, "+15551230000", uses, false, "", time.Minute, base)
	if got := p.Uses(); got != uses {
		t.Errorf("Uses() = %+v, want %+v", got, uses)
	}
}

func TestExpired(t *testing.T) {
	p := New("u1", identifier.KindEmail, "x@example.com", identifier.Uses{Notification: true}, false, "", time.Hour, base)
	if p.Expired(base.Add(59 * time.Minute)) {
		t.Errorf("Expired before ExpiresAt = true, want false")
	}
	if !p.Expired(base.Add(time.Hour)) {
		t.Errorf("Expired at ExpiresAt = false, want true (boundary is inclusive)")
	}
	if !p.Expired(base.Add(2 * time.Hour)) {
		t.Errorf("Expired past ExpiresAt = false, want true")
	}
}
