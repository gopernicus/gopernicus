package primitives

import (
	"strconv"

	"github.com/a-h/templ"
)

// ProgressProps configures Progress (P20, family F1): a native <progress>
// element. Determinate progress emits value/max (the geometry is a native
// attribute, never an inline style — README §4 invariant a); an indeterminate
// bar omits value so the platform draws the running state. data-slot="progress"
// and data-state="determinate"/"indeterminate" are the stable emitted surface.
// The native busy animation is suppressed under prefers-reduced-motion by the
// kit stylesheet.
type ProgressProps struct {
	Base
	// Value is the current amount (0..Max). Ignored when Indeterminate is set.
	Value int
	// Max is the upper bound; zero defaults to 100.
	Max int
	// Indeterminate renders a running bar with no known value.
	Indeterminate bool
	// Label is the accessible name (aria-label). A determinate progress also has a
	// caller option to wire aria-labelledby via Base.Attributes instead.
	Label string
}

func (p ProgressProps) max() int {
	if p.Max <= 0 {
		return 100
	}
	return p.Max
}

// clampedValue returns Value bounded to [0, max].
func (p ProgressProps) clampedValue() int {
	m := p.max()
	switch {
	case p.Value < 0:
		return 0
	case p.Value > m:
		return m
	default:
		return p.Value
	}
}

func progressClass(p ProgressProps) string { return classNames("goth-progress", p.Class) }

func progressAttrs(p ProgressProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "progress",
		"max":       strconv.Itoa(p.max()),
	}
	if p.Indeterminate {
		owned["data-state"] = "indeterminate"
	} else {
		owned["data-state"] = "determinate"
		owned["value"] = strconv.Itoa(p.clampedValue())
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

// fallbackText is the human-readable content shown when <progress> is
// unsupported: a percentage for determinate, "Loading…" for indeterminate.
func (p ProgressProps) fallbackText() string {
	if p.Indeterminate {
		return "Loading…"
	}
	pct := p.clampedValue() * 100 / p.max()
	return strconv.Itoa(pct) + "%"
}
