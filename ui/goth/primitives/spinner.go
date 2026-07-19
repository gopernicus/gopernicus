package primitives

import "github.com/a-h/templ"

// spinnerDefaultLabel is the accessible name used when SpinnerProps.Label is
// empty and the spinner is not decorative.
const spinnerDefaultLabel = "Loading"

// SpinnerProps configures Spinner (P23, family F1): a busy indicator. By default
// it is a named live status (role="status" with a visually-hidden Label, so it
// is never nameless). Set Decorative when an external live region already
// announces busy state — the spinner then renders aria-hidden with no role or
// label, avoiding a duplicate announcement. Its spin animation is CSS-only and
// collapses under prefers-reduced-motion. The zero value is a valid named
// spinner labelled "Loading".
type SpinnerProps struct {
	Base
	// Label is the accessible name (visually hidden). Empty uses "Loading".
	Label string
	// Decorative hides the spinner from assistive tech (for composition with an
	// external live region).
	Decorative bool
}

func (p SpinnerProps) label() string {
	if p.Label != "" {
		return p.Label
	}
	return spinnerDefaultLabel
}

func spinnerClass(p SpinnerProps) string { return classNames("goth-spinner", p.Class) }

func spinnerAttrs(p SpinnerProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "spinner"}
	if p.Decorative {
		owned["aria-hidden"] = "true"
	} else {
		owned["role"] = "status"
	}
	return ownedAttrs(p.Base, owned)
}
