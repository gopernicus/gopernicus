// Package htmx builds explicit hx-* attributes (HTMX 2.0.10) and interprets HTMX
// request headers as presentation hints only. It emits ordinary hx-* attributes
// on the element they affect — it never inherits, never uses hx-boost, and never
// conceals server behavior. See ../README.md §9. The Attrs field set was FROZEN at
// GOTH-5.3 against its first consumers (Data Table, Combobox) and FINALIZED at
// GOTH-7.3 against the CMS (mutating) adopter: nothing is provisional anymore. The
// once-provisional candidates (Vals, Include, Headers/hx-headers, DisabledElt,
// additional trigger/swap modifiers, and typed-URL alignment) are RETIRED as
// no-demonstrated-need — the CMS entries-list HTMX (sort/filter/page) needs none of
// them, and every CMS mutation rides a <form>, so the CSRF posture is settled as
// hidden-input-only (no hx-headers consumer). A retired candidate is reopenable by
// the standard rule if a real consumer later needs it.
package htmx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
)

// ErrInvalidAttrs is returned by Attrs.Build for an invalid method/URL/selector
// combination.
var ErrInvalidAttrs = errors.New("goth/htmx: invalid attributes")

// Method is an HTMX request verb.
type Method string

const (
	MethodGet    Method = "get"
	MethodPost   Method = "post"
	MethodPut    Method = "put"
	MethodPatch  Method = "patch"
	MethodDelete Method = "delete"
)

// Valid reports whether m is a known verb. The zero value ("") is not valid but
// signals "no request verb"; Build only validates a non-empty Method.
func (m Method) Valid() bool {
	switch m {
	case MethodGet, MethodPost, MethodPut, MethodPatch, MethodDelete:
		return true
	default:
		return false
	}
}

// Swap is an hx-swap strategy.
type Swap string

const (
	SwapInnerHTML   Swap = "innerHTML" // zero value maps here (HTMX default)
	SwapOuterHTML   Swap = "outerHTML"
	SwapBeforeBegin Swap = "beforebegin"
	SwapAfterBegin  Swap = "afterbegin"
	SwapBeforeEnd   Swap = "beforeend"
	SwapAfterEnd    Swap = "afterend"
	SwapDelete      Swap = "delete"
	SwapNone        Swap = "none"
)

// Valid reports whether s is a known swap strategy.
func (s Swap) Valid() bool {
	switch s {
	case SwapInnerHTML, SwapOuterHTML, SwapBeforeBegin, SwapAfterBegin,
		SwapBeforeEnd, SwapAfterEnd, SwapDelete, SwapNone:
		return true
	default:
		return false
	}
}

// Trigger is a typed hx-trigger specification for ONE element. The zero value
// emits no hx-trigger (the element's natural default applies).
//
// FROZEN at GOTH-5.3. It replaces the earlier free-string Trigger field: the two
// first real consumers both need first-class debounce + "changed" support — the
// Combobox async input (input changed delay:150ms) and the Data Table live filter
// (keyup changed delay:300ms) — which a bare string could not express or validate
// safely. The CMS entries-list filter (GOTH-7.3) is a third consumer (Event:
// "change"). Additional trigger modifiers (from:, target:, once, queue:, multiple
// comma-separated triggers) had no consumer through GOTH-7.3 and are RETIRED as
// no-demonstrated-need (reopenable by the standard rule).
type Trigger struct {
	// Event is the DOM/HTMX event name, e.g. "input", "keyup", "submit", "load".
	Event string
	// Changed adds the "changed" modifier (fire only when the value changed).
	Changed bool
	// Delay adds "delay:<ms>" (debounce): the request waits, resetting on each
	// event, until Delay elapses. Zero omits the modifier.
	Delay time.Duration
	// Throttle adds "throttle:<ms>": at most one request per window. Zero omits it.
	Throttle time.Duration
}

// IsZero reports whether t emits no hx-trigger.
func (t Trigger) IsZero() bool { return t == Trigger{} }

// build returns the hx-trigger value, or an error for an invalid combination.
func (t Trigger) build() (string, error) {
	if t.IsZero() {
		return "", nil
	}
	if t.Event == "" {
		return "", fmt.Errorf("%w: trigger modifiers require an Event", ErrInvalidAttrs)
	}
	if hasControl(t.Event) {
		return "", fmt.Errorf("%w: control character in trigger event", ErrInvalidAttrs)
	}
	parts := []string{t.Event}
	if t.Changed {
		parts = append(parts, "changed")
	}
	if t.Delay > 0 {
		parts = append(parts, "delay:"+millis(t.Delay))
	}
	if t.Throttle > 0 {
		parts = append(parts, "throttle:"+millis(t.Throttle))
	}
	return strings.Join(parts, " "), nil
}

// SwapModifiers are optional hx-swap modifiers appended after the swap strategy.
// The zero value adds nothing.
//
// FROZEN at GOTH-5.3. The Data Table swaps its content region on every sort,
// filter, and page transition and must not jump the viewport or steal focus;
// these are the modifiers a server-owned fragment swap needs to preserve
// scroll/focus. The CMS entries-list swap (GOTH-7.3) is a second consumer
// (Show:"none" + FocusScroll:false). Other swap modifiers (swap:<ms> delay) had no
// consumer through GOTH-7.3 and are RETIRED as no-demonstrated-need.
type SwapModifiers struct {
	// Show sets "show:<value>" — "none" suppresses the scroll-into-view (preserving
	// scroll position), or "top"/"bottom"/"<selector>:top". Empty omits it.
	Show string
	// Scroll sets "scroll:<value>" — "top"/"bottom" or "<selector>:top". Empty omits it.
	Scroll string
	// FocusScroll sets "focus-scroll:true|false". Nil omits it; a pointer to false
	// keeps HTMX from scrolling a refocused element into view after the swap.
	FocusScroll *bool
	// Settle sets "settle:<ms>". Zero omits it.
	Settle time.Duration
}

func (m SwapModifiers) isZero() bool { return m == SwapModifiers{} }

// parts returns the ordered hx-swap modifier tokens, or an error for a control
// character in a selector.
func (m SwapModifiers) parts() ([]string, error) {
	var parts []string
	if m.Show != "" {
		if hasControl(m.Show) {
			return nil, fmt.Errorf("%w: control character in swap show", ErrInvalidAttrs)
		}
		parts = append(parts, "show:"+m.Show)
	}
	if m.Scroll != "" {
		if hasControl(m.Scroll) {
			return nil, fmt.Errorf("%w: control character in swap scroll", ErrInvalidAttrs)
		}
		parts = append(parts, "scroll:"+m.Scroll)
	}
	if m.FocusScroll != nil {
		if *m.FocusScroll {
			parts = append(parts, "focus-scroll:true")
		} else {
			parts = append(parts, "focus-scroll:false")
		}
	}
	if m.Settle > 0 {
		parts = append(parts, "settle:"+millis(m.Settle))
	}
	return parts, nil
}

// Attrs builds explicit hx-* attributes for ONE element. It emits ordinary hx-*
// attributes only — it never inherits, never uses hx-boost, and never conceals
// server behavior. The zero value emits nothing.
//
// FINALIZED field set. Frozen at GOTH-5.3 against the Data Table and Combobox and
// confirmed final at GOTH-7.3 against the CMS entries-list HTMX (sort/filter/page),
// which consumes only the existing fields. The once-residual candidates (Vals,
// Include, Headers/hx-headers, DisabledElt, and aligning URL with the typed
// primitives.URL) are RETIRED as no-demonstrated-need: the CMS list carries full
// state in shareable URLs and its filter form serializes its own fields (no
// hx-vals/hx-include), the busy affordance rides data-state/.htmx-request (no
// hx-disabled-elt), the caller already passes an already-validated URL string (no
// htmx->primitives coupling buys safety), and every CMS mutation rides a <form> so
// the CSRF posture is settled as hidden-input-only (no hx-headers consumer). This
// reader/builder derives no CSRF or authorization from any hx header regardless. A
// retired candidate is reopenable by the standard rule.
type Attrs struct {
	Method    Method        // GET/POST/PUT/PATCH/DELETE; zero value = none emitted
	URL       string        // request URL; required when Method is set, else error at Build
	Target    string        // hx-target selector; empty = default (this element)
	Swap      Swap          // hx-swap strategy; zero value = HTMX default innerHTML
	SwapMods  SwapModifiers // hx-swap modifiers (scroll/focus preservation); zero = none
	Select    string        // hx-select
	Indicator string        // hx-indicator selector
	Confirm   string        // hx-confirm text
	Trigger   Trigger       // hx-trigger; zero value = natural default for the element
	PushURL   bool          // hx-push-url="true" when set
}

// Build validates the combination and returns templ.Attributes for the caller to
// pass into a primitive's Base.Attributes (where MergeAttributes applies them in
// one spread). It errors when Method is set without a URL, when Method is not a
// known verb, when a non-empty Swap is unknown, when a Trigger sets modifiers
// without an Event, or when a selector/URL contains a control character. It NEVER
// emits a partially valid attribute set alongside an error.
func (a Attrs) Build() (templ.Attributes, error) {
	out := templ.Attributes{}

	if a.Method != "" {
		if !a.Method.Valid() {
			return nil, fmt.Errorf("%w: unknown method %q", ErrInvalidAttrs, a.Method)
		}
		if a.URL == "" {
			return nil, fmt.Errorf("%w: method %q requires a URL", ErrInvalidAttrs, a.Method)
		}
		if hasControl(a.URL) {
			return nil, fmt.Errorf("%w: control character in URL", ErrInvalidAttrs)
		}
		out["hx-"+string(a.Method)] = a.URL
	}

	for field, value := range map[string]string{
		"hx-target":    a.Target,
		"hx-select":    a.Select,
		"hx-indicator": a.Indicator,
		"hx-confirm":   a.Confirm,
	} {
		if value == "" {
			continue
		}
		if hasControl(value) {
			return nil, fmt.Errorf("%w: control character in %s", ErrInvalidAttrs, field)
		}
		out[field] = value
	}

	trigger, err := a.Trigger.build()
	if err != nil {
		return nil, err
	}
	if trigger != "" {
		out["hx-trigger"] = trigger
	}

	if a.Swap != "" || !a.SwapMods.isZero() {
		strategy := a.Swap
		if strategy == "" {
			strategy = SwapInnerHTML
		}
		if !strategy.Valid() {
			return nil, fmt.Errorf("%w: unknown swap %q", ErrInvalidAttrs, a.Swap)
		}
		mods, err := a.SwapMods.parts()
		if err != nil {
			return nil, err
		}
		value := string(strategy)
		if len(mods) > 0 {
			value += " " + strings.Join(mods, " ")
		}
		out["hx-swap"] = value
	}

	if a.PushURL {
		out["hx-push-url"] = "true"
	}

	return out, nil
}

// millis formats a duration as an HTMX-style millisecond value (e.g. "150ms").
func millis(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
