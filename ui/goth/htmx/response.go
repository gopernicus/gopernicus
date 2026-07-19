package htmx

import "net/http"

// Request interprets the incoming request's HTMX headers as PRESENTATION HINTS
// ONLY. It never yields identity, CSRF, authorization, or business-state
// evidence. A non-HTMX request returns a zero Request (IsHTMX() == false), which
// the handler treats as "render the full document".
type Request struct {
	isHTMX      bool
	target      string
	triggerName string
}

// FromRequest reads the HTMX request headers (HX-Request / HX-Target /
// HX-Trigger-Name) from r. It reads headers only; it never inspects the body or
// derives a security decision.
func FromRequest(r *http.Request) Request {
	if r == nil {
		return Request{}
	}
	return Request{
		isHTMX:      r.Header.Get("HX-Request") == "true",
		target:      r.Header.Get("HX-Target"),
		triggerName: r.Header.Get("HX-Trigger-Name"),
	}
}

// IsHTMX reports whether the request declared itself an HTMX request. A false
// value means the handler should render the full document where the route
// supports HTML.
func (rq Request) IsHTMX() bool { return rq.isHTMX }

// Target returns the HX-Target hint (the id of the element being swapped), or "".
func (rq Request) Target() string { return rq.target }

// TriggerName returns the HX-Trigger-Name hint (the name of the triggering
// element), or "".
func (rq Request) TriggerName() string { return rq.triggerName }
