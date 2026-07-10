package turso

import (
	"database/sql"
	"time"
)

// TimeLayout is a fixed-width UTC timestamp layout. Fixed width (always 9
// fractional digits, always "Z") keeps stored timestamps lexicographically
// ordered as TEXT — the ecosystem timestamp-storage convention that range
// predicates and keyset pagination rely on. time.RFC3339Nano trims trailing
// fractional zeros and would break ordering.
const TimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// FormatTime renders t in the fixed-width UTC layout used for storage.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

// ParseTime parses a stored timestamp, tolerating both the fixed-width layout
// and RFC3339 variants. The result is normalized to UTC.
func ParseTime(s string) (time.Time, error) {
	if t, err := time.Parse(TimeLayout, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// ParseNullTime parses a nullable stored timestamp: a NULL or empty column reads
// back as the zero time (the domain's "not set" sentinel).
func ParseNullTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return time.Time{}, nil
	}
	return ParseTime(ns.String)
}

// FormatNullTime renders a possibly-zero timestamp for storage: a zero time stores
// as NULL (never expires / not set), any other value as a fixed-width TEXT
// timestamp. It is the value-typed absent-model — the caller's zero time.Time is
// the "not set" sentinel. Use FormatNullTimePtr when absence is modeled as a nil
// pointer instead. (The read twin is the NullTime Scanner type.)
func FormatNullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return FormatTime(t)
}

// FormatNullTimePtr renders a possibly-nil timestamp for storage: nil stores as
// NULL, any set value as a fixed-width TEXT timestamp. It is the pointer-typed
// absent-model — a nil *time.Time is the "not set" sentinel. Use FormatNullTime
// when absence is modeled as a zero value.Time instead.
func FormatNullTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return FormatTime(*t)
}

// BoolToInt maps a Go bool to the 0/1 INTEGER stored in SQLite/libSQL.
func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
