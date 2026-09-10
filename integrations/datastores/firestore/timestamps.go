package firestore

import (
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// TimePrecision is the resolution Firestore stores timestamps at. Writing a
// finer value is not an error — the server silently drops the extra digits, and
// the round trip then fails an equality assertion that never mentioned
// precision. TruncateTime makes the loss happen in Go, where it is visible.
const TimePrecision = time.Microsecond

// TruncateTime normalizes t for storage: UTC, truncated to Firestore's
// microsecond resolution, monotonic reading stripped. Every store writes
// timestamps through it, so a value read back compares equal to the value
// written — the same posture pgx takes with its microsecond columns.
//
// The zero time truncates to the zero time, so it is safe to call before the
// NullTime absence check.
func TruncateTime(t time.Time) time.Time {
	return t.UTC().Truncate(TimePrecision)
}

// NullTime renders a possibly-zero timestamp for storage: the zero time writes
// as nil, any other value as a truncated UTC timestamp. It is the VALUE-typed
// absent model, mirroring turso's FormatNullTime: the caller's zero time.Time
// is the "not set" sentinel. Use NullTimePtr when absence is a nil pointer
// instead.
//
// Unlike the SQL connectors there is no string formatting: Firestore stores a
// native timestamp, and native timestamps order and range-filter natively.
//
// # nil writes an explicit NULL, not an absent field — and that is deliberate
//
// A nil in the document data becomes a stored field holding the null VALUE. It
// does not omit the field, and the difference is not cosmetic in Firestore:
//
//   - OrderBy on a field EXCLUDES every document that does not have it. A
//     document missing its order field is invisible to every List path — page,
//     cursor, reverse probe, and (since the count follows the ordered query)
//     the total. A null value is present, so the document is still traversed,
//     sorting first in ascending order (null is the lowest type in Firestore's
//     value ordering).
//   - INEQUALITY filters (<, <=, >, >=) exclude null anyway. A range query over
//     an expiry field will not see "never expires" rows whether the field is
//     null or absent, so a store that means "unbounded" filters for it
//     explicitly rather than expecting a range to include it.
//
// So: write every optional timestamp through NullTime or NullTimePtr, and never
// omit the field instead. Omitting it removes the document from its own list.
func NullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return TruncateTime(t)
}

// NullTimePtr renders a possibly-nil timestamp for storage: nil writes as nil,
// any set value as a truncated UTC timestamp. It is the POINTER-typed absent
// model, mirroring turso's FormatNullTimePtr.
func NullTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return NullTime(*t)
}

// ParseTime reads a required timestamp field out of a decoded document value
// (DocumentSnapshot.Data or DataAt), normalized to UTC and microseconds. A
// missing/null field is an error here — use ParseNullTime for optional fields.
func ParseTime(value any) (time.Time, error) {
	t, ok := value.(time.Time)
	if !ok {
		return time.Time{}, fmt.Errorf("firestore: timestamp field is %T, want time.Time: %w", value, sdk.ErrInvalidInput)
	}
	return TruncateTime(t), nil
}

// ParseNullTime reads an optional timestamp field: an absent field (a map lookup
// that returned nothing) and a null one both read back as the zero time — the
// domain's "not set" sentinel. It is the read twin of NullTime.
func ParseNullTime(value any) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	return ParseTime(value)
}

// ParseNullTimePtr reads an optional timestamp field into the pointer-typed
// absent model: absent or null reads back as nil. It is the read twin of
// NullTimePtr.
func ParseNullTimePtr(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	t, err := ParseTime(value)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
