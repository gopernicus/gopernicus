package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// Toast (P64, family F4) is a composable notification: the caller composes a
// Toaster region (the ONE gothToast-backed live-region queue) holding Toast
// entries, each built from ToastTitle / ToastDescription / ToastAction / ToastClose
// parts. The kit ships ONE queue runtime shared by Toast (P64) and the opinionated
// Sonner facade (P63) — see sonner.go.
//
// No-JS baseline (README invariant 2). The Toaster is a server-rendered role=region
// landmark, accessibly named and reachable, holding readable server-rendered toasts.
// With no JavaScript the toasts are visible and readable; auto-dismiss timers,
// pause-on-hover, and the close button are enhancement only. The region is NOT itself
// an aria-live region and the toasts carry no role=status/alert, so there is exactly
// ONE announcement channel (the shared live-region mechanic, below) and announcements
// never double-fire.
//
// Enhancement (gothToast, README §8; the pre-provisioned GOTH-1.4 controller refined
// in place for this wave — no new controller name). Bound to the Toaster region, the
// single controller owns the whole queue: it announces each toast ONCE through the
// shared mechanics/live-region.js (polite by default, assertive by Priority — the
// "live-region priority" of the row); runs per-toast auto-dismiss timers that PAUSE on
// region hover/focus and while the page is hidden and RESUME afterward; dedupes by
// DedupKey; enforces the Max overflow cap (oldest out); and handles keyboard/pointer
// dismissal (ToastClose) and actions (ToastAction). Entrance/exit animation collapses
// under prefers-reduced-motion.
//
// data-slot hooks: toaster, toast, toast-title, toast-description, toast-action,
// toast-close. State: data-state (open/closed) + data-index (stack order) on a toast;
// data-priority (polite/assertive), data-duration (ms), data-dedup-key on a toast;
// data-position / data-max on the region, data-paused / data-expanded / data-count set
// by the controller.

// ToastPriority is the live-region announcement priority. The zero value is polite
// (queued behind current speech); assertive interrupts for errors/urgent notices.
type ToastPriority string

const (
	// ToastPolite is the zero value and the documented default.
	ToastPolite ToastPriority = ""
	// ToastAssertive interrupts current announcements (errors/urgent notices).
	ToastAssertive ToastPriority = "assertive"
)

// Valid reports whether p is a known ToastPriority.
func (p ToastPriority) Valid() bool {
	switch p {
	case ToastPolite, ToastAssertive:
		return true
	default:
		return false
	}
}

func (p ToastPriority) attr() string {
	if p == ToastAssertive {
		return "assertive"
	}
	return "polite"
}

// ToastPosition places the Toaster stack against a viewport corner. The zero value
// is bottom-end. Positions use logical edges so they follow the writing direction.
type ToastPosition string

const (
	// ToastBottomEnd is the zero value and the documented default.
	ToastBottomEnd   ToastPosition = ""
	ToastBottomStart ToastPosition = "bottom-start"
	ToastTopEnd      ToastPosition = "top-end"
	ToastTopStart    ToastPosition = "top-start"
)

// Valid reports whether p is a known ToastPosition.
func (p ToastPosition) Valid() bool {
	switch p {
	case ToastBottomEnd, ToastBottomStart, ToastTopEnd, ToastTopStart:
		return true
	default:
		return false
	}
}

func (p ToastPosition) attr() string {
	if p == ToastBottomEnd {
		return "bottom-end"
	}
	return string(p)
}

// ToasterProps configures the Toaster region — the gothToast-backed live-region
// queue. The zero value is a valid bottom-end, unlimited, "Notifications" region.
type ToasterProps struct {
	Base
	// Label is the accessible name of the notification region. Defaults to
	// "Notifications" (a region landmark REQUIRES a name).
	Label string
	// Position places the stack against a viewport corner. Zero = bottom-end.
	Position ToastPosition
	// Max caps the visible toasts; older toasts overflow out when a new one arrives.
	// 0 leaves the queue unbounded.
	Max int
}

// ToastProps configures one Toast entry inside a Toaster. The zero value is a valid
// polite, persistent (no auto-dismiss) toast.
type ToastProps struct {
	Base
	// Priority selects polite (default) or assertive live-region announcement.
	Priority ToastPriority
	// Duration is the auto-dismiss delay in milliseconds; 0 keeps the toast until it
	// is dismissed (persistent). Timers pause on hover/focus and while the page hides.
	Duration int
	// DedupKey collapses an older toast carrying the same key when a new one arrives.
	DedupKey string
}

// ToastTitleProps configures the ToastTitle (the toast's primary line).
type ToastTitleProps struct{ Base }

// ToastDescriptionProps configures the ToastDescription (supporting detail line).
type ToastDescriptionProps struct{ Base }

// ToastActionProps configures a ToastAction control. With URL set it is a real link
// (native navigation, the no-JS action); empty renders a button that dispatches
// goth:select and dismisses the toast.
type ToastActionProps struct {
	Base
	// URL is the action destination. Set renders a link; empty a button.
	URL URL
}

// ToastCloseProps configures the ToastClose dismiss button. Label is the accessible
// name and defaults to "Dismiss".
type ToastCloseProps struct {
	Base
	// Label is the accessible name (aria-label) of the dismiss control.
	Label string
}

func toasterClass(p ToasterProps) string { return classNames("goth-toaster", p.Class) }

func toasterAttrs(p ToasterProps) templ.Attributes {
	// role=region + a required accessible name make the queue a reachable, non-trapping
	// landmark; tabindex=-1 allows programmatic focus (F6-style) without joining the tab
	// order. x-data binds the shared gothToast queue controller (inert with no JS).
	owned := templ.Attributes{
		"data-slot":     "toaster",
		"role":          "region",
		"aria-label":    toasterLabel(p),
		"tabindex":      "-1",
		"x-data":        "gothToast",
		"data-position": p.Position.attr(),
	}
	if p.Max > 0 {
		owned["data-max"] = strconv.Itoa(p.Max)
	}
	return ownedAttrs(p.Base, owned)
}

func toasterLabel(p ToasterProps) string {
	if p.Label != "" {
		return p.Label
	}
	return "Notifications"
}

func toastClass(p ToastProps) string { return classNames("goth-toast", p.Class) }

func toastAttrs(p ToastProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":     "toast",
		"data-priority": p.Priority.attr(),
		"data-state":    "open",
	}
	if p.Duration > 0 {
		owned["data-duration"] = strconv.Itoa(p.Duration)
	}
	if p.DedupKey != "" {
		owned["data-dedup-key"] = p.DedupKey
	}
	return ownedAttrs(p.Base, owned)
}

func toastPartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func toastPartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}

func toastActionAttrs(p ToastActionProps, link bool) templ.Attributes {
	owned := templ.Attributes{"data-slot": "toast-action"}
	if !link {
		owned["type"] = "button"
	}
	return ownedAttrs(p.Base, owned)
}

func toastCloseAttrs(p ToastCloseProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "toast-close",
		"type":       "button",
		"aria-label": toastCloseLabel(p),
	})
}

func toastCloseLabel(p ToastCloseProps) string {
	if p.Label != "" {
		return p.Label
	}
	return "Dismiss"
}
