package sdk

import (
	"strconv"
	"time"
)

// Violation codes are the transport-agnostic vocabulary domains and transport
// edges share when refusing a write. They are stable strings: a client may
// switch on them, so they never change spelling.
const (
	// CodeRequired marks a value the write cannot proceed without.
	CodeRequired = "required"
	// CodeInvalidType marks a value of the wrong JSON/Go type.
	CodeInvalidType = "invalid_type"
	// CodeInvalidFormat marks a value of the right type whose contents do not
	// parse (a date, an instant, an identifier shape).
	CodeInvalidFormat = "invalid_format"
	// CodeUnknownField marks a key the write surface does not declare.
	CodeUnknownField = "unknown_field"
	// CodeUnknownReference marks a reference to an entity that does not exist.
	CodeUnknownReference = "unknown_reference"
)

// Violation is one refused field: which field, a stable machine code, and a
// caller-facing sentence. It carries no json tags — the kernel stays
// transport-agnostic and the transport (sdk/foundation/web) owns the wire shape.
type Violation struct {
	Field   string
	Code    string
	Message string
}

// ValidationError collects every problem with a write rather than failing at the
// first one, so a caller fixes a form in one round trip. It unwraps to
// [ErrInvalidInput], so existing errors.Is checks and IsExpected keep working;
// transports that recognize the concrete type additionally render the per-field
// detail.
//
// Every method has a POINTER receiver, and Error is deliberately pointer-only: a
// value receiver would make errors.As(err, &ve) silently miss a
// ValidationError stored by value, turning a 400 into a nondeterministic 500.
// Always return *ValidationError.
//
// The collector's usage shape:
//
//	var ve sdk.ValidationError
//	if in.Name == "" {
//	    ve.Add("name", sdk.CodeRequired, "name is required")
//	}
//	if err := ve.Err(); err != nil {
//	    return err
//	}
//
// Message is CALLER-FACING TEXT ONLY. Never put a store, driver, or wrapped
// internal error string in it — transports put these sentences on the wire.
type ValidationError struct {
	Violations []Violation
}

// Error reports the first violation as "field: message", with " (and N more)"
// when further violations were collected. An empty collector reports
// "validation failed" — mirroring web.FieldErrors.Error.
func (e *ValidationError) Error() string {
	if len(e.Violations) == 0 {
		return "validation failed"
	}
	s := e.Violations[0].Field + ": " + e.Violations[0].Message
	if n := len(e.Violations) - 1; n > 0 {
		return s + " (and " + strconv.Itoa(n) + " more)"
	}
	return s
}

// Unwrap returns ErrInvalidInput so a validation refusal answers the existing
// errors.Is checks (and IsExpected) without any transport knowing the type.
func (e *ValidationError) Unwrap() error { return ErrInvalidInput }

// Add appends one violation to the collector.
func (e *ValidationError) Add(field, code, message string) {
	e.Violations = append(e.Violations, Violation{Field: field, Code: code, Message: message})
}

// Err returns nil when nothing was collected, else the collector itself. The
// explicit nil matters: returning e unconditionally hands the caller a non-nil
// error interface holding a typed nil-equivalent value, and `if err != nil`
// fires on a write that had no problem at all.
func (e *ValidationError) Err() error {
	if len(e.Violations) == 0 {
		return nil
	}
	return e
}

// Refuse builds a single-violation ValidationError — the one-liner for a domain
// rule that refuses on the spot.
//
//	return sdk.Refuse("starts_at", sdk.CodeInvalidFormat, "starts_at must precede ends_at")
//
// message is caller-facing text. Never wrap a store or driver error into it
// (sdk.Refuse(field, code, err.Error()) is the anti-pattern): a transport puts
// this sentence on the wire verbatim.
func Refuse(field, code, message string) *ValidationError {
	return &ValidationError{Violations: []Violation{{Field: field, Code: code, Message: message}}}
}

// UnknownReference builds a CodeUnknownReference violation naming the id the
// caller supplied for field.
//
// This is a DOMAIN PRE-CHECK artifact: only the domain knows that a request's
// "owner_id" names a user, so only the domain can produce a field-named message.
// Store connectors map a foreign-key fault to a bare ErrInvalidReference with no
// field name and cannot do better. The division of labor is therefore:
//
//   - the pre-check produces the good message for the common case;
//   - the FK constraint still guards the check-then-write race;
//   - the race's fallback is the generic 400 "invalid reference".
//
// Pass only an id the CALLER supplied in that field — never a server-resolved
// one — because the message echoes it back to the caller.
func UnknownReference(field, id string) *ValidationError {
	return Refuse(field, CodeUnknownReference, "unknown "+field+" "+strconv.Quote(id))
}

// StaleError reports a compare-and-set write that lost: the caller's expected
// version token did not match the stored one. It unwraps to [ErrConflict] and
// carries the CURRENT token so the caller can re-read, re-decide, and retry
// without a second round trip.
//
// The comparison contract, which the framework cannot enforce for you:
//
//   - Compare with time.Time.Equal, never == and never a string compare of
//     formatted tokens. A formatted compare is a CAS that never matches.
//   - Compare AT THE STORE'S PRECISION. Postgres timestamptz keeps
//     microseconds, so a domain comparing against pgx truncates both sides with
//     Truncate(time.Microsecond); a text-storing store (turso) round-trips
//     whatever it wrote. A CAS that never matches on one store is worse than no
//     CAS at all.
//
// The emitted token is RFC3339Nano in UTC so it round-trips what the store
// returned.
type StaleError struct {
	CurrentUpdatedAt time.Time
}

// Error reports the refusal with the current token. Pointer-only, for the same
// errors.As reason as ValidationError.Error.
func (e *StaleError) Error() string {
	return "stale write: the resource changed at " + e.CurrentUpdatedAt.UTC().Format(time.RFC3339Nano)
}

// Unwrap returns ErrConflict so a stale write answers the existing errors.Is
// checks (and IsExpected) without any transport knowing the type.
func (e *StaleError) Unwrap() error { return ErrConflict }
