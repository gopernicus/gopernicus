package firestore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestTruncateTime pins the storage normalization: UTC, microseconds, and a
// zero that stays zero.
func TestTruncateTime(t *testing.T) {
	zone := time.FixedZone("UTC+5", 5*60*60)
	in := time.Date(2026, 9, 9, 18, 23, 53, 567873912, zone)

	got := firestore.TruncateTime(in)

	if got.Location() != time.UTC {
		t.Errorf("TruncateTime location = %v, want UTC", got.Location())
	}
	if got.Nanosecond()%1000 != 0 {
		t.Errorf("TruncateTime kept sub-microsecond digits: %v", got)
	}
	if want := in.UTC().Truncate(time.Microsecond); !got.Equal(want) {
		t.Errorf("TruncateTime = %v, want %v", got, want)
	}
	if !firestore.TruncateTime(time.Time{}).IsZero() {
		t.Error("TruncateTime moved the zero time")
	}
	// Idempotent: a value read back and re-truncated does not drift.
	if again := firestore.TruncateTime(got); !again.Equal(got) {
		t.Errorf("TruncateTime is not idempotent: %v then %v", got, again)
	}
}

// TestNullTimeRoundTrip walks both absent models through write and read: the
// zero/nil value becomes an absent field, a set value survives at microsecond
// resolution.
func TestNullTimeRoundTrip(t *testing.T) {
	set := time.Date(2026, 9, 9, 18, 23, 53, 567873912, time.UTC)
	want := set.Truncate(time.Microsecond)

	if got := firestore.NullTime(time.Time{}); got != nil {
		t.Errorf("NullTime(zero) = %v, want nil", got)
	}
	stored, ok := firestore.NullTime(set).(time.Time)
	if !ok {
		t.Fatalf("NullTime(set) = %T, want time.Time", firestore.NullTime(set))
	}
	if !stored.Equal(want) {
		t.Errorf("NullTime(set) = %v, want %v", stored, want)
	}

	if got := firestore.NullTimePtr(nil); got != nil {
		t.Errorf("NullTimePtr(nil) = %v, want nil", got)
	}
	if got := firestore.NullTimePtr(&set); got.(time.Time) != want {
		t.Errorf("NullTimePtr(&set) = %v, want %v", got, want)
	}
	var zero time.Time
	if got := firestore.NullTimePtr(&zero); got != nil {
		t.Errorf("NullTimePtr(&zero) = %v, want nil — a pointer to the zero time is still 'not set'", got)
	}

	read, err := firestore.ParseNullTime(stored)
	if err != nil {
		t.Fatalf("ParseNullTime: %v", err)
	}
	if !read.Equal(want) {
		t.Errorf("ParseNullTime = %v, want %v", read, want)
	}
	absent, err := firestore.ParseNullTime(nil)
	if err != nil || !absent.IsZero() {
		t.Errorf("ParseNullTime(nil) = %v, %v; want the zero time and no error", absent, err)
	}

	ptr, err := firestore.ParseNullTimePtr(stored)
	if err != nil || ptr == nil || !ptr.Equal(want) {
		t.Errorf("ParseNullTimePtr = %v, %v; want %v", ptr, err, want)
	}
	if ptr, err := firestore.ParseNullTimePtr(nil); err != nil || ptr != nil {
		t.Errorf("ParseNullTimePtr(nil) = %v, %v; want nil, nil", ptr, err)
	}
}

// TestParseTimeRejectsOtherTypes keeps a mistyped field a loud error instead of
// a zero timestamp that reads as "not set".
func TestParseTimeRejectsOtherTypes(t *testing.T) {
	for _, value := range []any{"2026-09-09T00:00:00Z", int64(1757443200), nil} {
		if _, err := firestore.ParseTime(value); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ParseTime(%v) error = %v, want sdk.ErrInvalidInput", value, err)
		}
	}
	if _, err := firestore.ParseNullTime("2026-09-09T00:00:00Z"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Error("ParseNullTime accepted a string timestamp")
	}
}
