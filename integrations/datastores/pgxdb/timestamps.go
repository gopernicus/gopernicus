package pgxdb

import "time"

// NullTime maps a possibly-zero timestamp to a nullable TIMESTAMPTZ arg: a zero
// time binds as NULL (never expires / not revoked / not set), any other value as
// its UTC instant. It is the value-typed absent-model — the caller's zero
// time.Time is the "not set" sentinel. Use NullTimePtr when absence is modeled as
// a nil pointer instead. Pair with FromNullTime on the read side.
func NullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// FromNullTime maps a scanned nullable TIMESTAMPTZ back to the domain's
// zero-value "not set" sentinel: a NULL column (nil pointer) reads back as the
// zero time. It is the read-side twin of NullTime.
func FromNullTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.UTC()
}

// NullTimePtr maps a possibly-nil timestamp to a nullable TIMESTAMPTZ arg: nil
// binds as NULL, any set value as its UTC instant. It is the pointer-typed
// absent-model — a nil *time.Time is the "not set" sentinel. Use NullTime when
// absence is modeled as a zero value instead. Pair with FromNullTimePtr on the
// read side.
func NullTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// FromNullTimePtr maps a scanned nullable TIMESTAMPTZ back to a pointer,
// preserving absence as nil: a NULL column (nil pointer) reads back as nil, any
// set value as its UTC instant. It is the read-side twin of NullTimePtr.
func FromNullTimePtr(p *time.Time) *time.Time {
	if p == nil {
		return nil
	}
	u := p.UTC()
	return &u
}
