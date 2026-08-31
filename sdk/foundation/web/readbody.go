package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// DefaultBodyLimit is the maximum request body [ReadBody] accepts, in bytes. It
// is the package's single bound: a route needing a different one reads the body
// itself. An overrun surfaces as *http.MaxBytesError, which [ErrValidation]
// answers with a 413.
const DefaultBodyLimit = 1 << 20

// BodyKeyExpectedUpdatedAt is the wire key for the compare-and-set token a
// conditional write carries. It is snake_case like every other key this
// framework puts on the wire, and it is NOT implicit: a CAS route declares it in
// its [ReadBody] key list explicitly, like any other field.
const BodyKeyExpectedUpdatedAt = "expected_updated_at"

// Body is a decoded, key-checked JSON request object with typed getters.
//
// The getters NEVER short-circuit: each one records a violation and returns its
// zero value, so one pass over the body collects every problem the caller has,
// and [Body.Err] is the single terminal check.
//
//	body, err := web.ReadBody(w, r, "title", "summary", web.BodyKeyExpectedUpdatedAt)
//	if err != nil {
//	    web.RespondJSONError(w, web.ErrValidation(err))
//	    return
//	}
//
//	title := body.Str("title")
//	summary := body.OptStr("summary")
//	expected := body.ExpectedUpdatedAt()
//	if err := body.Err(); err != nil {
//	    web.RespondJSONError(w, web.ErrValidation(err))
//	    return
//	}
//
// For a PATCH, guard each getter with [Body.Has] and wrap the result in
// crud.Some — the sparse-write vocabulary lives in sdk/foundation/crud, which
// this package may not import (guard G21), so the getters return plain values
// and the handler composes the two.
type Body struct {
	values map[string]any
	ve     sdk.ValidationError
}

// ReadBody decodes the request body as exactly one JSON object whose keys are
// all declared in keys, bounded by [DefaultBodyLimit].
//
// A structural failure returns a nil Body and a non-nil error:
//
//   - an overrun wraps *http.MaxBytesError, so [ErrValidation] answers 413;
//   - an empty body, JSON null, a non-object, or trailing content wraps
//     sdk.ErrInvalidInput with its sentence preserved as the prefix;
//   - an undeclared key produces a *sdk.ValidationError with one
//     sdk.CodeUnknownField violation PER unknown key, all collected. This is the
//     rule that a body must not smuggle a path-owned id: an undeclared
//     organization_id is a named 400, never silently ignored.
//
// An object with no keys at all ({}) is VALID — whether an empty patch is
// acceptable is the domain's call, not the reader's.
func ReadBody(w http.ResponseWriter, r *http.Request, keys ...string) (*Body, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, DefaultBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("request body is empty: %w", sdk.ErrInvalidInput)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var values map[string]any
	if err := dec.Decode(&values); err != nil {
		return nil, fmt.Errorf("request body must be a JSON object: %w", sdk.ErrInvalidInput)
	}
	if values == nil {
		return nil, fmt.Errorf("request body must be a JSON object, not null: %w", sdk.ErrInvalidInput)
	}
	if dec.More() {
		return nil, fmt.Errorf("request body must contain exactly one JSON object: %w", sdk.ErrInvalidInput)
	}

	declared := make(map[string]bool, len(keys))
	for _, k := range keys {
		declared[k] = true
	}

	unknown := make([]string, 0)
	for k := range values {
		if !declared[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		ve := &sdk.ValidationError{}
		for _, k := range unknown {
			ve.Add(k, sdk.CodeUnknownField, "unknown field "+strconv.Quote(k))
		}
		return nil, ve
	}

	return &Body{values: values}, nil
}

// Has reports whether the caller sent the field at all. An explicit null counts
// as sent — that is the distinction a PATCH needs between "leave it alone" and
// "clear it".
func (b *Body) Has(field string) bool {
	_, ok := b.values[field]
	return ok
}

// Err returns nil when no getter recorded a problem, else the accumulated
// *sdk.ValidationError. It is the single terminal check for a read.
func (b *Body) Err() error { return b.ve.Err() }

// Str returns a required string field.
func (b *Body) Str(field string) string {
	raw, ok := b.values[field]
	if !ok {
		b.required(field)
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		b.invalidType(field, "a string")
		return ""
	}
	return s
}

// OptStr returns an optional string field: nil when the caller omitted it or
// sent null (the explicit clear), a pointer to the value when they sent one. A
// value of the wrong type is still a violation.
func (b *Body) OptStr(field string) *string {
	raw, ok := b.values[field]
	if !ok || raw == nil {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		b.invalidType(field, "a string")
		return nil
	}
	return &s
}

// Bool returns a required boolean field.
func (b *Body) Bool(field string) bool {
	raw, ok := b.values[field]
	if !ok {
		b.required(field)
		return false
	}
	v, ok := raw.(bool)
	if !ok {
		b.invalidType(field, "a boolean")
		return false
	}
	return v
}

// Strs returns a required array-of-strings field. A non-string element is a
// violation naming its index.
func (b *Body) Strs(field string) []string {
	raw, ok := b.values[field]
	if !ok {
		b.required(field)
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		b.invalidType(field, "an array of strings")
		return nil
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			b.ve.Add(field, sdk.CodeInvalidType, field+"["+strconv.Itoa(i)+"] must be a string")
			return nil
		}
		out = append(out, s)
	}
	return out
}

// Date returns a required calendar-date field, parsed strictly as YYYY-MM-DD
// and returned at midnight UTC.
//
// Format the result back with time.DateOnly and store it in a DATE column: a
// zone conversion changes the calendar day, which is why this is not an
// instant. (When a civil-date type lands in sdk, this getter may change or be
// deprecated.)
func (b *Body) Date(field string) time.Time {
	s, ok := b.parseable(field)
	if !ok {
		return time.Time{}
	}
	t, err := time.ParseInLocation(time.DateOnly, s, time.UTC)
	if err != nil {
		b.ve.Add(field, sdk.CodeInvalidFormat, field+" must be a date in YYYY-MM-DD format")
		return time.Time{}
	}
	return t
}

// Instant returns a required timestamp field, parsed as RFC 3339 (fractional
// seconds accepted).
func (b *Body) Instant(field string) time.Time {
	s, ok := b.parseable(field)
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		b.ve.Add(field, sdk.CodeInvalidFormat, field+" must be an RFC 3339 timestamp")
		return time.Time{}
	}
	return t
}

// ExpectedUpdatedAt returns the compare-and-set token on
// [BodyKeyExpectedUpdatedAt], with [Body.Instant]'s semantics. Compare it with
// time.Time.Equal at the store's precision — never a string compare of the
// formatted tokens (see sdk.StaleError).
func (b *Body) ExpectedUpdatedAt() time.Time {
	return b.Instant(BodyKeyExpectedUpdatedAt)
}

// parseable resolves a required string field for the parsing getters, recording
// the presence and type violations they share.
func (b *Body) parseable(field string) (string, bool) {
	raw, ok := b.values[field]
	if !ok {
		b.required(field)
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		b.invalidType(field, "a string")
		return "", false
	}
	return s, true
}

func (b *Body) required(field string) {
	b.ve.Add(field, sdk.CodeRequired, field+" is required")
}

func (b *Body) invalidType(field, want string) {
	b.ve.Add(field, sdk.CodeInvalidType, field+" must be "+want)
}
