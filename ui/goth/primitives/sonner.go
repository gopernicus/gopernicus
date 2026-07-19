package primitives

// Sonner (P63, family F4) is the opinionated toast facade over the ONE shared
// gothToast live-region queue (P64 Toast runtime). Sonner renders a Toaster marked
// data-sonner for the opinionated stacked presentation, and SonnerToast renders a
// COMPLETE variant toast (colored status accent + title + optional description +
// optional action + a close button) from a single props struct — the "opinionated
// toast queue API over shared live-region runtime" of the row. It adds no controller
// and no runtime of its own: it is defaults + composition over Toast (see toast.go).
//
// Variant → live-region priority mapping is opinionated: Error and Warning announce
// assertively (they interrupt); Default/Success/Info announce politely. Sonner also
// defaults a duration (SonnerDefaultDuration) and a visible cap (SonnerDefaultMax) so
// a caller gets sensible auto-dismiss/overflow without wiring either — drop to the raw
// Toast parts for a persistent or unbounded queue.
//
// data-slot hooks reuse the Toast set (toaster/toast/toast-*); Sonner adds the
// goth-sonner / goth-sonner-toast classes and data-sonner / data-variant hooks.

// SonnerDefaultDuration is the opinionated auto-dismiss delay (ms) SonnerToast applies
// when the caller leaves Duration at 0.
const SonnerDefaultDuration = 4000

// SonnerDefaultMax is the opinionated visible-toast cap Sonner applies when the caller
// leaves Max at 0.
const SonnerDefaultMax = 3

// SonnerVariant selects the opinionated status styling and announcement priority. The
// zero value is the neutral default.
type SonnerVariant string

const (
	// SonnerDefault is the zero value: neutral styling, polite announcement.
	SonnerDefault SonnerVariant = ""
	SonnerSuccess SonnerVariant = "success"
	SonnerInfo    SonnerVariant = "info"
	SonnerWarning SonnerVariant = "warning"
	SonnerError   SonnerVariant = "error"
)

// Valid reports whether v is a known SonnerVariant.
func (v SonnerVariant) Valid() bool {
	switch v {
	case SonnerDefault, SonnerSuccess, SonnerInfo, SonnerWarning, SonnerError:
		return true
	default:
		return false
	}
}

func (v SonnerVariant) attr() string {
	if v == SonnerDefault {
		return "default"
	}
	return string(v)
}

// priority maps a variant to its live-region announcement priority: errors and
// warnings interrupt (assertive); everything else is polite.
func (v SonnerVariant) priority() ToastPriority {
	switch v {
	case SonnerError, SonnerWarning:
		return ToastAssertive
	default:
		return ToastPolite
	}
}

// SonnerProps configures the Sonner region — an opinionated Toaster. The zero value is
// a valid bottom-end region capped at SonnerDefaultMax named "Notifications".
type SonnerProps struct {
	Base
	// Label is the accessible name of the region. Defaults to "Notifications".
	Label string
	// Position places the stack. Zero = bottom-end.
	Position ToastPosition
	// Max caps the visible toasts; 0 applies SonnerDefaultMax (Sonner is opinionated —
	// use a raw Toaster for an unbounded queue).
	Max int
}

// SonnerToastProps configures a single opinionated toast rendered by SonnerToast.
type SonnerToastProps struct {
	Base
	// Variant selects status styling + announcement priority.
	Variant SonnerVariant
	// Title is the primary line (required for a meaningful announcement).
	Title string
	// Description is optional supporting detail.
	Description string
	// ActionLabel + ActionURL render an optional action control (link when ActionURL is
	// set, otherwise a button). ActionLabel empty renders no action.
	ActionLabel string
	ActionURL   URL
	// CloseLabel names the dismiss control (defaults to "Dismiss").
	CloseLabel string
	// Duration is the auto-dismiss delay (ms); 0 applies SonnerDefaultDuration.
	Duration int
	// DedupKey collapses an older toast with the same key.
	DedupKey string
}

// sonnerToaster maps SonnerProps onto ToasterProps, applying the opinionated defaults
// and the data-sonner / goth-sonner presentation markers.
func sonnerToaster(p SonnerProps) ToasterProps {
	max := p.Max
	if max == 0 {
		max = SonnerDefaultMax
	}
	attrs := templAttrsWith(p.Attributes, "data-sonner", "true")
	return ToasterProps{
		Base:     Base{ID: p.ID, Class: classNames("goth-sonner", p.Class), Attributes: attrs},
		Label:    p.Label,
		Position: p.Position,
		Max:      max,
	}
}

// sonnerToast maps SonnerToastProps onto the ToastProps for the composed Toast entry,
// applying the opinionated duration default and the variant priority + data-variant.
func sonnerToast(p SonnerToastProps) ToastProps {
	duration := p.Duration
	if duration == 0 {
		duration = SonnerDefaultDuration
	}
	attrs := templAttrsWith(p.Attributes, "data-variant", p.Variant.attr())
	return ToastProps{
		Base:     Base{ID: p.ID, Class: classNames("goth-sonner-toast", p.Class), Attributes: attrs},
		Priority: p.Variant.priority(),
		Duration: duration,
		DedupKey: p.DedupKey,
	}
}

func sonnerCloseProps(p SonnerToastProps) ToastCloseProps {
	return ToastCloseProps{Label: p.CloseLabel}
}

func sonnerActionProps(p SonnerToastProps) ToastActionProps {
	return ToastActionProps{URL: p.ActionURL}
}
