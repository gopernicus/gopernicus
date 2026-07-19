package primitives

import "github.com/a-h/templ"

// MessageScroller (P60, family F4) is a controller-backed transcript scroller: the
// caller composes MessageScrollerViewport (the scrollable role=log region),
// MessageScrollerHistory (a "load earlier" trigger at the top), MessageScrollerContent
// (the turns list = the HTMX prepend/append target holding Message/Bubble rows), and
// MessageScrollerStatus (the unread / jump-to-latest affordance). MessageScrollerJump
// links jump to a named message.
//
// No-JS baseline (README invariant 2). The viewport is a native scroll region with a
// readable server-rendered role=log transcript; MessageScrollerHistory is a real link
// (full-document reload of earlier history); MessageScrollerJump links are native
// in-page anchors (#id); CSS overflow-anchor keeps an HTMX prepend from jumping. The
// unread status affordance is CSS-hidden while data-unread="0", so no-JS never shows a
// dead control.
//
// Enhancement (gothMessageScroller, README §8; ratified 2026-07-18 gate-b-review.md
// addendum). The controller composes the frozen GOTH-4.1 mechanics + live-region.js to
// add: live-edge following (stick to the bottom while the reader is at the live edge,
// stop when they scroll up), history-prepend-without-jump (anchor the viewport across
// an HTMX afterbegin swap), jump-to-message preserving focus/reader intent, and
// unread/scroll-state exposure (data-at-edge / data-unread on the root + documented
// goth:change events + a polite live-region summary when messages arrive off-edge).
//
// data-slot hooks: message-scroller, message-scroller-viewport,
// message-scroller-history, message-scroller-content, message-scroller-status,
// message-scroller-jump. State on the root: data-at-edge (true/false), data-unread (N).

// MessageScrollerProps configures the MessageScroller root. The zero value is valid;
// in the Interactive/Full profiles it binds the gothMessageScroller controller, and in
// StylesOnly the binding is inert (the no-JS baseline applies).
type MessageScrollerProps struct {
	Base
}

// MessageScrollerViewportProps configures the scrollable role=log region. Label is the
// REQUIRED accessible name for the transcript log (a scrollable region must be named),
// enforced by a contract test.
type MessageScrollerViewportProps struct {
	Base
	// Label is the accessible name (aria-label) of the transcript log. Required.
	Label string
}

// MessageScrollerHistoryProps configures the "load earlier" trigger. With URL set it is
// a real link (the no-JS full-document reload of earlier history); empty renders a
// button. Label is the REQUIRED accessible name. Callers add hx-get/hx-target/
// hx-swap="afterbegin" via Base.Attributes for the HTMX prepend; the primitive marks it
// data-goth-history so the controller anchors the viewport across that swap.
type MessageScrollerHistoryProps struct {
	Base
	// URL is the earlier-history destination. Set renders a link; empty a button.
	URL URL
	// Label is the accessible name (aria-label). Required.
	Label string
}

// MessageScrollerContentProps configures the turns list — the HTMX prepend/append swap
// target. The caller composes Message (P59) / Bubble (P56) rows inside, each with a
// stable Base.ID so MessageScrollerJump can target it.
type MessageScrollerContentProps struct{ Base }

// MessageScrollerStatusProps configures the unread / jump-to-latest affordance. It is a
// button the controller reveals (data-unread on the root) and whose text it updates
// with the unread count; clicking it returns to the live edge. Label is the REQUIRED
// accessible name (a per-default when empty).
type MessageScrollerStatusProps struct {
	Base
	// Label is the accessible name (aria-label), e.g. "Jump to latest messages".
	// Required.
	Label string
}

// MessageScrollerJumpProps configures a jump-to-message link. TargetID is the REQUIRED
// id of the message to scroll to (rendered as href="#TargetID", the no-JS anchor);
// Label is the REQUIRED accessible name. The controller enhances the native anchor by
// focusing the target so a keyboard/AT reader lands on the jumped message.
type MessageScrollerJumpProps struct {
	Base
	// TargetID is the id of the target message element. Required.
	TargetID string
	// Label is the accessible name. Required.
	Label string
}

func messageScrollerClass(p MessageScrollerProps) string {
	return classNames("goth-message-scroller", p.Class)
}

func messageScrollerAttrs(p MessageScrollerProps) templ.Attributes {
	// x-data binds the controller in the Interactive/Full runtime; inert (and the
	// no-JS baseline applies) when Alpine is absent. data-at-edge/data-unread are the
	// initial scroll state the controller then owns.
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":    "message-scroller",
		"x-data":       "gothMessageScroller",
		"data-at-edge": "true",
		"data-unread":  "0",
	})
}

func messageScrollerViewportClass(p MessageScrollerViewportProps) string {
	return classNames("goth-message-scroller-viewport", p.Class)
}

func messageScrollerViewportAttrs(p MessageScrollerViewportProps) templ.Attributes {
	// role=log + a required aria-label name the scrollable transcript region;
	// tabindex=0 makes it keyboard-scrollable. role=log carries the implicit polite
	// live semantics the controller toggles off-edge.
	owned := templ.Attributes{
		"data-slot": "message-scroller-viewport",
		"role":      "log",
		"tabindex":  "0",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func messageScrollerHistoryClass(p MessageScrollerHistoryProps) string {
	return classNames("goth-message-scroller-history", p.Class)
}

func messageScrollerHistoryAttrs(p MessageScrollerHistoryProps, link bool) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":         "message-scroller-history",
		"data-goth-history": "true",
	}
	if !link {
		owned["type"] = "button"
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func messageScrollerContentClass(p MessageScrollerContentProps) string {
	return classNames("goth-message-scroller-content", p.Class)
}

func messageScrollerContentAttrs(p MessageScrollerContentProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "message-scroller-content"})
}

func messageScrollerStatusClass(p MessageScrollerStatusProps) string {
	return classNames("goth-message-scroller-status", p.Class)
}

func messageScrollerStatusAttrs(p MessageScrollerStatusProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":        "message-scroller-status",
		"data-goth-status": "true",
		"type":             "button",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func messageScrollerStatusLabel(p MessageScrollerStatusProps) string {
	if p.Label != "" {
		return p.Label
	}
	return "Jump to latest messages"
}

func messageScrollerJumpClass(p MessageScrollerJumpProps) string {
	return classNames("goth-message-scroller-jump", p.Class)
}

func messageScrollerJumpHref(p MessageScrollerJumpProps) templ.SafeURL {
	return templ.SafeURL("#" + p.TargetID)
}

func messageScrollerJumpAttrs(p MessageScrollerJumpProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":      "message-scroller-jump",
		"data-goth-jump": "true",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}
