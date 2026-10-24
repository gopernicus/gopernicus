package sdk

import (
	"fmt"
	"io"
	"log/slog"
)

// Redacted is the text a Secret renders as everywhere except Reveal.
const Redacted = "[redacted]"

// Secret is a credential-bearing string: a password, API key, token, private
// key or a DSN/URL that embeds one. It renders as Redacted through fmt (every
// verb, including %v, %+v, %#v, %s, %q and %x), slog, encoding/json and
// encoding.TextMarshaler; Reveal is the one read of the value.
//
// It is a string kind so an untyped constant or literal still assigns
// (Config{Password: "x"}) and environment.ParseEnvTags populates it like any
// string field. A typed string variable converts explicitly: sdk.Secret(v).
// Unmarshal is the string kind's own, so JSON, text and env sources populate
// it, but a JSON round trip stores Redacted, not the value.
//
// Limits: fmt calls no method through an unexported struct field, so %+v of a
// struct holding a Secret in an unexported field prints the value, and slog's
// TextHandler formats structs with %+v; encoding/json writes a string-kind map
// key verbatim, so never key a map by Secret. A type that keeps a secret in an
// unexported field implements fmt.Formatter and slog.LogValuer itself. A
// string composed from a Secret through fmt or + carries Redacted instead of
// the value; consumers that receive such a string refuse it with
// ErrRedactedSecret. See environment.Secret for decoding hex key material.
type Secret string

// Reveal returns the secret's value. Call it only where the value is used.
func (s Secret) Reveal() string { return string(s) }

// String returns Redacted.
func (s Secret) String() string { return Redacted }

// GoString returns Redacted.
func (s Secret) GoString() string { return Redacted }

// Format writes Redacted for every verb and flag.
func (s Secret) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, Redacted) }

// LogValue returns Redacted as a string value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalJSON encodes Redacted as a JSON string.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText returns Redacted.
func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }
