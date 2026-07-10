package turso

import (
	"database/sql"
	"testing"
	"time"
)

// TestFormatTimeFixedWidth pins the storage layout: always 30 characters, always
// 9 fractional digits, always the "Z" zone — the lexicographic-ordering
// guarantee that TEXT keyset pagination and range predicates depend on. A
// timestamp with a sub-second component that RFC3339Nano would trim must keep its
// trailing zeros here.
func TestFormatTimeFixedWidth(t *testing.T) {
	// 100ms → ".100000000"; RFC3339Nano would render ".1".
	in := time.Date(2026, 7, 7, 12, 34, 56, 100000000, time.UTC)
	got := FormatTime(in)
	want := "2026-07-07T12:34:56.100000000Z"
	if got != want {
		t.Fatalf("FormatTime = %q, want %q", got, want)
	}
	if len(got) != len(want) {
		t.Fatalf("FormatTime width = %d, want fixed %d", len(got), len(want))
	}
}

// TestFormatTimeNormalizesToUTC proves a non-UTC input is rendered in UTC so
// stored values are zone-stable regardless of the caller's location.
func TestFormatTimeNormalizesToUTC(t *testing.T) {
	loc := time.FixedZone("UTC+5", 5*3600)
	in := time.Date(2026, 7, 7, 17, 34, 56, 100000000, loc) // == 12:34:56Z
	if got, want := FormatTime(in), "2026-07-07T12:34:56.100000000Z"; got != want {
		t.Fatalf("FormatTime(non-UTC) = %q, want %q", got, want)
	}
}

// TestFormatParseRoundTrip is the fixed-width round-trip identity: any instant
// formatted then parsed returns the same instant.
func TestFormatParseRoundTrip(t *testing.T) {
	cases := []time.Time{
		time.Date(2026, 7, 7, 12, 34, 56, 123456789, time.UTC),
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(1999, 12, 31, 23, 59, 59, 999999999, time.UTC),
	}
	for _, in := range cases {
		got, err := ParseTime(FormatTime(in))
		if err != nil {
			t.Fatalf("ParseTime(FormatTime(%v)): %v", in, err)
		}
		if !got.Equal(in) {
			t.Errorf("round-trip = %v, want %v", got, in)
		}
	}
}

// TestParseTimeRFC3339Tolerance proves ParseTime accepts RFC3339 variants (e.g.
// values written by another writer that trimmed trailing zeros) and normalizes
// them to UTC.
func TestParseTimeRFC3339Tolerance(t *testing.T) {
	cases := map[string]time.Time{
		"2026-07-07T12:34:56.1Z":      time.Date(2026, 7, 7, 12, 34, 56, 100000000, time.UTC),
		"2026-07-07T12:34:56Z":        time.Date(2026, 7, 7, 12, 34, 56, 0, time.UTC),
		"2026-07-07T17:34:56+05:00":   time.Date(2026, 7, 7, 12, 34, 56, 0, time.UTC),
		"2026-07-07T12:34:56.500000Z": time.Date(2026, 7, 7, 12, 34, 56, 500000000, time.UTC),
	}
	for in, want := range cases {
		got, err := ParseTime(in)
		if err != nil {
			t.Fatalf("ParseTime(%q): %v", in, err)
		}
		if !got.Equal(want) {
			t.Errorf("ParseTime(%q) = %v, want %v", in, got, want)
		}
		if got.Location() != time.UTC {
			t.Errorf("ParseTime(%q) location = %v, want UTC", in, got.Location())
		}
	}
}

// TestParseTimeInvalid rejects a value that is neither the fixed-width layout nor
// a recognized RFC3339 variant.
func TestParseTimeInvalid(t *testing.T) {
	if _, err := ParseTime("not-a-timestamp"); err == nil {
		t.Fatal("ParseTime(garbage) = nil error, want error")
	}
}

// TestNullTime is the value-typed absent-model (auth's contract): a zero time
// encodes as NULL, any other as the fixed-width string.
func TestNullTime(t *testing.T) {
	if v := FormatNullTime(time.Time{}); v != nil {
		t.Errorf("FormatNullTime(zero) = %#v, want nil (NULL)", v)
	}
	set := time.Date(2026, 7, 7, 12, 34, 56, 100000000, time.UTC)
	if v := FormatNullTime(set); v != "2026-07-07T12:34:56.100000000Z" {
		t.Errorf("FormatNullTime(set) = %#v, want fixed-width string", v)
	}
}

// TestNullTimePtr is the pointer-typed absent-model (cms's contract): a nil
// pointer encodes as NULL, any set value as the fixed-width string. This is the
// divergence the D1 audit flagged — NullTime and NullTimePtr must NOT collapse.
func TestNullTimePtr(t *testing.T) {
	if v := FormatNullTimePtr(nil); v != nil {
		t.Errorf("FormatNullTimePtr(nil) = %#v, want nil (NULL)", v)
	}
	set := time.Date(2026, 7, 7, 12, 34, 56, 100000000, time.UTC)
	if v := FormatNullTimePtr(&set); v != "2026-07-07T12:34:56.100000000Z" {
		t.Errorf("FormatNullTimePtr(set) = %#v, want fixed-width string", v)
	}
}

// TestNullTimeZeroDivergence pins the trap directly: the SAME instant absent
// through NullTime (zero value.Time) and through NullTimePtr (nil pointer) both
// yield NULL, and a non-absent value formats identically through both — proving
// they share one wire encoding while keeping distinct absent-models.
func TestNullTimeVsNullTimePtrParity(t *testing.T) {
	if FormatNullTime(time.Time{}) != nil || FormatNullTimePtr(nil) != nil {
		t.Fatal("both absent-models must encode absence as NULL")
	}
	set := time.Date(2026, 7, 7, 12, 34, 56, 100000000, time.UTC)
	if FormatNullTime(set) != FormatNullTimePtr(&set) {
		t.Errorf("FormatNullTime(set) = %#v, FormatNullTimePtr(&set) = %#v, want identical wire value",
			FormatNullTime(set), FormatNullTimePtr(&set))
	}
}

// TestParseNullTime covers both directions the connector defines: an invalid or
// empty NullString reads back as the zero time, a valid one parses through
// ParseTime.
func TestParseNullTime(t *testing.T) {
	got, err := ParseNullTime(sql.NullString{Valid: false})
	if err != nil {
		t.Fatalf("ParseNullTime(NULL): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("ParseNullTime(NULL) = %v, want zero time", got)
	}

	got, err = ParseNullTime(sql.NullString{Valid: true, String: ""})
	if err != nil {
		t.Fatalf("ParseNullTime(empty): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("ParseNullTime(empty) = %v, want zero time", got)
	}

	want := time.Date(2026, 7, 7, 12, 34, 56, 100000000, time.UTC)
	got, err = ParseNullTime(sql.NullString{Valid: true, String: FormatTime(want)})
	if err != nil {
		t.Fatalf("ParseNullTime(set): %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("ParseNullTime(set) = %v, want %v", got, want)
	}
}

// TestParseNullTimeRoundTripFromNullTime closes the loop: a value written through
// NullTime reads back through ParseNullTime, and an absent value (NULL) reads
// back as the zero sentinel NullTime encoded from.
func TestParseNullTimeRoundTripFromNullTime(t *testing.T) {
	set := time.Date(2026, 7, 7, 12, 34, 56, 123456789, time.UTC)

	enc, ok := FormatNullTime(set).(string)
	if !ok {
		t.Fatalf("FormatNullTime(set) did not encode to string: %#v", FormatNullTime(set))
	}
	got, err := ParseNullTime(sql.NullString{Valid: true, String: enc})
	if err != nil {
		t.Fatalf("ParseNullTime: %v", err)
	}
	if !got.Equal(set) {
		t.Errorf("round-trip = %v, want %v", got, set)
	}

	// FormatNullTime(zero) → NULL → ParseNullTime → zero.
	if FormatNullTime(time.Time{}) != nil {
		t.Fatal("FormatNullTime(zero) must be NULL")
	}
	back, err := ParseNullTime(sql.NullString{Valid: false})
	if err != nil {
		t.Fatalf("ParseNullTime(NULL): %v", err)
	}
	if !back.IsZero() {
		t.Errorf("absent round-trip = %v, want zero time", back)
	}
}

// TestBoolToInt pins the 0/1 INTEGER mapping.
func TestBoolToInt(t *testing.T) {
	if BoolToInt(true) != 1 {
		t.Errorf("BoolToInt(true) = %d, want 1", BoolToInt(true))
	}
	if BoolToInt(false) != 0 {
		t.Errorf("BoolToInt(false) = %d, want 0", BoolToInt(false))
	}
}
