package content

import (
	"strconv"
	"time"
)

// FieldKind is the type tag of a custom field value. It governs how a raw
// stored string is coerced on read and which input the generated editor
// renders. The set is closed in v1 (locked decision 7: flat fields + relations;
// repeaters deferred).
type FieldKind string

const (
	KindText     FieldKind = "text"
	KindRichText FieldKind = "richtext" // markdown
	KindNumber   FieldKind = "number"
	KindBool     FieldKind = "bool"
	KindDate     FieldKind = "date"
	KindImage    FieldKind = "image"    // media asset reference (asset ID)
	KindRelation FieldKind = "relation" // reference to another entry (entry ID)
)

// Valid reports whether k is a known field kind.
func (k FieldKind) Valid() bool {
	switch k {
	case KindText, KindRichText, KindNumber, KindBool, KindDate, KindImage, KindRelation:
		return true
	default:
		return false
	}
}

// Value is a single custom-field value, stored as a (kind, raw string) pair in
// entry_fields. The kind is persisted alongside the raw text so reads can coerce
// without consulting the schema. Raw encodings: number → strconv int/float,
// bool → "true"/"false", date → RFC3339, relation → entry ID, image → asset ID,
// text/richtext → the text itself.
type Value struct {
	Kind FieldKind
	Raw  string
}

// Fields is the dynamic custom-field bag carried by an Entry. The typed
// accessors are the "typed edge": templates read e.Fields.String("subtitle")
// rather than reaching into raw strings. Missing keys return the zero value.
type Fields map[string]Value

// String returns the raw text of key (text/richtext and the underlying string
// of any other kind), or "" when absent.
func (f Fields) String(key string) string {
	return f[key].Raw
}

// Int returns key parsed as an integer, or 0 when absent or unparseable.
func (f Fields) Int(key string) int {
	n, err := strconv.Atoi(f[key].Raw)
	if err != nil {
		return 0
	}
	return n
}

// Float returns key parsed as a float, or 0 when absent or unparseable.
func (f Fields) Float(key string) float64 {
	v, err := strconv.ParseFloat(f[key].Raw, 64)
	if err != nil {
		return 0
	}
	return v
}

// Bool returns key parsed as a boolean ("true" → true), or false otherwise.
func (f Fields) Bool(key string) bool {
	return f[key].Raw == "true"
}

// Time returns key parsed as an RFC3339 timestamp (UTC), or the zero time when
// absent or unparseable.
func (f Fields) Time(key string) time.Time {
	t, err := time.Parse(time.RFC3339, f[key].Raw)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// Relation returns the target entry ID stored under key, or "" when absent.
func (f Fields) Relation(key string) string {
	return f[key].Raw
}

// Image returns the media asset ID stored under key, or "" when absent.
func (f Fields) Image(key string) string {
	return f[key].Raw
}
