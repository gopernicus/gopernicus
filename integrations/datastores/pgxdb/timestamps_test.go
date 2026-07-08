package pgxdb

import (
	"testing"
	"time"
)

// TestNullTime covers the value-typed absent-model both directions plus
// round-trip identity: zero encodes as NULL (nil), a set value encodes as its
// UTC instant, and FromNullTime restores the same instant (NULL → zero).
func TestNullTime(t *testing.T) {
	// zero → NULL
	if got := NullTime(time.Time{}); got != nil {
		t.Fatalf("NullTime(zero) = %v, want nil", got)
	}
	// NULL → zero
	if got := FromNullTime(nil); !got.IsZero() {
		t.Fatalf("FromNullTime(nil) = %v, want zero", got)
	}
	// set value normalizes to UTC on both directions
	loc := time.FixedZone("EST", -5*3600)
	instant := time.Date(2026, 7, 8, 9, 30, 0, 123456789, loc)
	enc := NullTime(instant)
	if enc == nil {
		t.Fatal("NullTime(set) = nil, want non-nil")
	}
	if enc.Location() != time.UTC {
		t.Fatalf("NullTime(set) location = %v, want UTC", enc.Location())
	}
	if !enc.Equal(instant) {
		t.Fatalf("NullTime(set) = %v, want same instant as %v", enc, instant)
	}
	// round-trip identity
	back := FromNullTime(enc)
	if !back.Equal(instant) {
		t.Fatalf("round-trip = %v, want same instant as %v", back, instant)
	}
	if back.Location() != time.UTC {
		t.Fatalf("round-trip location = %v, want UTC", back.Location())
	}
}

// TestNullTimePtr covers the pointer-typed absent-model both directions plus
// round-trip identity: nil encodes as NULL, a set pointer encodes as its UTC
// instant, and FromNullTimePtr restores a pointer to the same instant (NULL →
// nil). Distinct from NullTime: a set zero-value time is still stored (only nil
// is absent).
func TestNullTimePtr(t *testing.T) {
	// nil → NULL
	if got := NullTimePtr(nil); got != nil {
		t.Fatalf("NullTimePtr(nil) = %v, want nil", got)
	}
	// NULL → nil
	if got := FromNullTimePtr(nil); got != nil {
		t.Fatalf("FromNullTimePtr(nil) = %v, want nil", got)
	}
	// set value normalizes to UTC on both directions
	loc := time.FixedZone("EST", -5*3600)
	instant := time.Date(2026, 7, 8, 9, 30, 0, 123456789, loc)
	enc := NullTimePtr(&instant)
	if enc == nil {
		t.Fatal("NullTimePtr(set) = nil, want non-nil")
	}
	if enc.Location() != time.UTC {
		t.Fatalf("NullTimePtr(set) location = %v, want UTC", enc.Location())
	}
	if !enc.Equal(instant) {
		t.Fatalf("NullTimePtr(set) = %v, want same instant as %v", enc, instant)
	}
	// round-trip identity
	back := FromNullTimePtr(enc)
	if back == nil {
		t.Fatal("round-trip = nil, want non-nil")
	}
	if !back.Equal(instant) {
		t.Fatalf("round-trip = %v, want same instant as %v", back, instant)
	}
	if back.Location() != time.UTC {
		t.Fatalf("round-trip location = %v, want UTC", back.Location())
	}
}

// TestNullTimePtr_ZeroValueIsStored pins the semantic difference from NullTime: a
// pointer to a zero time is a present value, not absence, so it encodes non-NULL.
func TestNullTimePtr_ZeroValueIsStored(t *testing.T) {
	zero := time.Time{}
	if got := NullTimePtr(&zero); got == nil {
		t.Fatal("NullTimePtr(&zero) = nil, want non-nil (only nil pointer is absent)")
	}
}
