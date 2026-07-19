// Package primitives holds all 64 ui/goth catalog primitives in ONE package
// (Shadcn-style compound prefixes) plus the shared props/slot/attribute/ID/merge
// conventions every primitive embeds. GOTH-1.1 lands the shared foundation; the
// per-entry primitives land in Phases 2-6. See ../README.md §7 for the frozen
// grammar.
package primitives

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/a-h/templ"
)

// ErrInvalidURL is returned by ParseURL for a scheme or byte the safe-URL type
// rejects.
var ErrInvalidURL = errors.New("goth/primitives: invalid URL")

// Base is embedded by every primitive Props type.
type Base struct {
	// ID is the caller-provided stable element id. An interactive primitive that
	// needs an id REQUIRES a non-empty ID (documented per primitive) OR takes ids
	// from a request-scoped IDFactory; the kit uses NO global/duplicate counter. A
	// non-interactive primitive leaves ID empty.
	ID string

	// Class is appended AFTER the primitive's stable base class, so callers add
	// utilities without dropping the compatibility class. Class is the ONLY class
	// channel: a "class" key inside Attributes is rejected (never merged), so the
	// compatibility class can never be dropped through the escape hatch.
	Class string

	// Attributes is the escape hatch for ids, names, ARIA, data-*, and explicit
	// hx-* attributes. Behavior-critical attributes the primitive owns are applied
	// AFTER Attributes and cannot be silently overwritten; owned and caller
	// attributes funnel through ONE merged spread on the element (see
	// MergeAttributes). A "class" key here is rejected — use Base.Class.
	Attributes templ.Attributes
}

// MergeAttributes returns owned attributes layered over caller attributes so a
// caller can add ids/aria/data/hx-* via Base.Attributes while the primitive's
// behavior-critical keys (owned) always win. This is the single documented,
// tested merge order used by every primitive.
//
// Frozen emission rule: the merged result is applied as ONE spread on the element
// ({ attrs... }). Owned and caller attributes never appear as sibling static
// attributes on the same element, because templ 0.3.1020 emits duplicate
// attributes rather than overriding when a static attr and a spread collide. A
// "class" key in caller attributes is dropped here — Base.Class is the only class
// channel.
func MergeAttributes(caller, owned templ.Attributes) templ.Attributes {
	out := templ.Attributes{}
	for k, v := range caller {
		if k == "class" {
			continue
		}
		out[k] = v
	}
	for k, v := range owned {
		out[k] = v
	}
	return out
}

// URL is a validated, safely rendered URL for href/src/action props. Construct
// with ParseURL; the zero value is the empty URL and renders nothing. It never
// exposes a raw templ.SafeURL conversion through a generic string helper.
type URL struct {
	raw string
}

// ParseURL validates s (scheme allowlist http/https/mailto/tel + relative
// same-origin paths; rejects javascript:, data:, control chars) and returns a
// URL. An empty string yields the zero URL and no error; any other invalid input
// is an error, not a silently dropped attribute.
func ParseURL(s string) (URL, error) {
	if s == "" {
		return URL{}, nil
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return URL{}, fmt.Errorf("%w: control character", ErrInvalidURL)
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return URL{}, fmt.Errorf("%w: %s", ErrInvalidURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "":
		// Relative/same-origin path (may carry a host only if scheme is set; a
		// scheme-relative //host form parses with an empty scheme but a host, which
		// we reject to keep same-origin honest).
		if u.Host != "" {
			return URL{}, fmt.Errorf("%w: scheme-relative URL", ErrInvalidURL)
		}
	case "http", "https", "mailto", "tel":
		// Allowed absolute schemes.
	default:
		return URL{}, fmt.Errorf("%w: disallowed scheme %q", ErrInvalidURL, u.Scheme)
	}
	return URL{raw: s}, nil
}

// IsZero reports whether u is the empty URL, which renders nothing.
func (u URL) IsZero() bool { return u.raw == "" }

// String returns the raw validated URL string.
func (u URL) String() string { return u.raw }

// SafeURL returns the validated URL as a templ.SafeURL for href/src/action
// attributes. The zero URL returns an empty SafeURL.
func (u URL) SafeURL() templ.SafeURL { return templ.SafeURL(u.raw) }

// IDFactory yields stable, unique element ids within one request/render. A host
// or component supplies one (request-scoped); the kit ships a default
// constructor. There is NO package-level counter, so ids are deterministic per
// render and never collide across concurrent requests.
type IDFactory interface {
	// NextID returns a new unique id with the given human-readable prefix.
	NextID(prefix string) string
}

// NewIDFactory returns a fresh request-scoped IDFactory. It holds no global
// state; a new factory restarts numbering, so ids are deterministic per render.
func NewIDFactory() IDFactory { return &idFactory{} }

type idFactory struct {
	n int
}

func (f *idFactory) NextID(prefix string) string {
	f.n++
	if prefix == "" {
		prefix = "goth"
	}
	return prefix + "-" + strconv.Itoa(f.n)
}
