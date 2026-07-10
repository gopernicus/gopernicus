package turso

import (
	"fmt"
	"time"
)

// Time is a sql.Scanner over the connector's fixed-width TEXT timestamp. It reads
// a stored timestamp through ParseTime, so a db-tagged row-struct field of this
// type performs the same conversion the hand-scan ParseTime path did — inside
// rows.Scan, where ScanStruct binds its address. A NULL surfaces the driver error
// exactly as scanning a NULL into a plain string would (the byte-identical twin of
// the ParseTime path); use NullTime for a nullable column. The parsed, UTC-
// normalized value is read via .Time.
type Time struct {
	Time time.Time
}

// Scan implements sql.Scanner, parsing the stored TEXT timestamp via ParseTime.
func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case string:
		parsed, err := ParseTime(v)
		if err != nil {
			return err
		}
		t.Time = parsed
		return nil
	case []byte:
		parsed, err := ParseTime(string(v))
		if err != nil {
			return err
		}
		t.Time = parsed
		return nil
	case time.Time:
		t.Time = v.UTC()
		return nil
	case nil:
		return fmt.Errorf("turso: cannot scan NULL into turso.Time (use turso.NullTime for a nullable column)")
	default:
		return fmt.Errorf("turso: cannot scan %T into turso.Time", src)
	}
}

// NullTime is the nullable twin of Time, mirroring sql.NullTime's shape: it reads
// a stored TEXT timestamp through ParseNullTime's semantics — a NULL or empty
// column reads back as the zero time with Valid false (the domain's "not set"
// sentinel), any other value parses via ParseTime with Valid true. The value is
// read via .Time/.Valid, or via TimePtr for a *time.Time domain field.
type NullTime struct {
	Time  time.Time
	Valid bool
}

// Scan implements sql.Scanner with ParseNullTime's semantics: NULL or empty ⇒
// zero time, not valid; any other value parses via ParseTime.
func (nt *NullTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		nt.Time, nt.Valid = time.Time{}, false
		return nil
	case string:
		return nt.parse(v)
	case []byte:
		return nt.parse(string(v))
	case time.Time:
		nt.Time, nt.Valid = v.UTC(), true
		return nil
	default:
		return fmt.Errorf("turso: cannot scan %T into turso.NullTime", src)
	}
}

// parse maps an empty string to the not-set sentinel (matching ParseNullTime) and
// otherwise parses through ParseTime.
func (nt *NullTime) parse(s string) error {
	if s == "" {
		nt.Time, nt.Valid = time.Time{}, false
		return nil
	}
	parsed, err := ParseTime(s)
	if err != nil {
		return err
	}
	nt.Time, nt.Valid = parsed, true
	return nil
}

// TimePtr returns a *time.Time view: nil when not valid, otherwise a pointer to a
// copy of the parsed time. It is the read helper for a domain field modeled as a
// nil *time.Time (rather than a zero value.Time) absent-sentinel.
func (nt NullTime) TimePtr() *time.Time {
	if !nt.Valid {
		return nil
	}
	t := nt.Time
	return &t
}

// Bool is a sql.Scanner over the 0/1 INTEGER a bool is stored as in SQLite/libSQL
// (the read twin of BoolToInt). A db-tagged row-struct field of this type performs
// the `!= 0` conversion inside rows.Scan; read it with a plain bool(field)
// conversion. A NULL surfaces an error — use a pointer or sql.NullBool for a
// nullable column.
type Bool bool

// Scan implements sql.Scanner, mapping the stored 0/1 INTEGER (int64 from the
// driver) to a bool by the `!= 0` rule BoolToInt is the inverse of.
func (b *Bool) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		*b = v != 0
		return nil
	case int:
		*b = v != 0
		return nil
	case bool:
		*b = Bool(v)
		return nil
	case []byte:
		s := string(v)
		*b = s != "" && s != "0"
		return nil
	case string:
		*b = v != "" && v != "0"
		return nil
	case nil:
		return fmt.Errorf("turso: cannot scan NULL into turso.Bool")
	default:
		return fmt.Errorf("turso: cannot scan %T into turso.Bool", src)
	}
}
