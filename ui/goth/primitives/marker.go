package primitives

import "github.com/a-h/templ"

// MarkerVariant is the typed style enum for Marker (P17). The zero value is the
// status variant (a small labelled status marker).
type MarkerVariant string

const (
	// MarkerStatus is the zero value: an inline status dot + label.
	MarkerStatus    MarkerVariant = ""
	MarkerNote      MarkerVariant = "note"      // an icon + note block
	MarkerRow       MarkerVariant = "row"       // a full-width icon + content row
	MarkerSeparator MarkerVariant = "separator" // a labelled divider
)

// Valid reports whether v is a known MarkerVariant.
func (v MarkerVariant) Valid() bool {
	switch v {
	case MarkerStatus, MarkerNote, MarkerRow, MarkerSeparator:
		return true
	default:
		return false
	}
}

func (v MarkerVariant) orDefault() MarkerVariant {
	if v.Valid() {
		return v
	}
	return MarkerStatus
}

func (v MarkerVariant) attr() string {
	if v.orDefault() == MarkerStatus {
		return "status"
	}
	return string(v.orDefault())
}

// MarkerTone is an optional status color for the status/note variants. The zero
// value inherits the default (muted) tone.
type MarkerTone string

const (
	MarkerToneDefault     MarkerTone = ""
	MarkerToneSuccess     MarkerTone = "success"
	MarkerToneWarning     MarkerTone = "warning"
	MarkerToneDestructive MarkerTone = "destructive"
)

// Valid reports whether t is a known MarkerTone.
func (t MarkerTone) Valid() bool {
	switch t {
	case MarkerToneDefault, MarkerToneSuccess, MarkerToneWarning, MarkerToneDestructive:
		return true
	default:
		return false
	}
}

func (t MarkerTone) orDefault() MarkerTone {
	if t.Valid() {
		return t
	}
	return MarkerToneDefault
}

func (t MarkerTone) attr() string {
	if t.orDefault() == MarkerToneDefault {
		return "default"
	}
	return string(t.orDefault())
}

// MarkerProps configures Marker (P17, family F2): a labelled marker in one of
// four variants (status/note/row/separator) with an optional leading icon and a
// principal content region passed as templ children. When URL is set the marker
// renders as an anchor (link form); otherwise the semantic element for its
// variant. The zero value is a valid default status marker.
type MarkerProps struct {
	Base
	Variant MarkerVariant
	Tone    MarkerTone
	// Icon is an optional leading glyph slot.
	Icon templ.Component
	// URL, when non-zero, renders the marker as an <a> link (link form).
	URL URL
}

func markerClass(p MarkerProps) string { return classNames("goth-marker", p.Class) }

func markerAttrs(p MarkerProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":    "marker",
		"data-variant": p.Variant.attr(),
		"data-tone":    p.Tone.attr(),
	}
	return ownedAttrs(p.Base, owned)
}
