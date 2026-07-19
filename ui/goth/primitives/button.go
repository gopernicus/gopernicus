package primitives

import (
	"errors"

	"github.com/a-h/templ"
)

// ErrButtonURLConflict is returned by ButtonProps.Validate when a URL (link form)
// is combined with a submit/reset type or a disabled state. An anchor is neither
// a submit control nor disable-able, so the combination is invalid.
var ErrButtonURLConflict = errors.New("goth/primitives: Button URL cannot combine with a submit/reset type or disabled state")

// ButtonVariant is the typed style enum for Button (P06). The zero value renders
// the documented default (solid primary) variant.
type ButtonVariant string

const (
	// ButtonDefault is the zero value and the documented default (solid primary).
	ButtonDefault     ButtonVariant = ""
	ButtonSecondary   ButtonVariant = "secondary"
	ButtonDestructive ButtonVariant = "destructive"
	ButtonOutline     ButtonVariant = "outline"
	ButtonGhost       ButtonVariant = "ghost"
	ButtonLink        ButtonVariant = "link"
)

// Valid reports whether v is a known ButtonVariant.
func (v ButtonVariant) Valid() bool {
	switch v {
	case ButtonDefault, ButtonSecondary, ButtonDestructive, ButtonOutline, ButtonGhost, ButtonLink:
		return true
	default:
		return false
	}
}

func (v ButtonVariant) orDefault() ButtonVariant {
	if v.Valid() {
		return v
	}
	return ButtonDefault
}

func (v ButtonVariant) attr() string {
	if v.orDefault() == ButtonDefault {
		return "default"
	}
	return string(v.orDefault())
}

// ButtonSize is the typed size enum for Button (P06). The zero value is the
// default size. ButtonIcon is the icon-only (square) size and REQUIRES a
// non-empty ButtonProps.Label for its accessible name.
type ButtonSize string

const (
	// ButtonSizeDefault is the zero value and the documented default.
	ButtonSizeDefault ButtonSize = ""
	ButtonSizeSmall   ButtonSize = "sm"
	ButtonSizeLarge   ButtonSize = "lg"
	// ButtonIcon is the icon-only square size; icon-only controls require Label.
	ButtonIcon ButtonSize = "icon"
)

// Valid reports whether s is a known ButtonSize.
func (s ButtonSize) Valid() bool {
	switch s {
	case ButtonSizeDefault, ButtonSizeSmall, ButtonSizeLarge, ButtonIcon:
		return true
	default:
		return false
	}
}

func (s ButtonSize) orDefault() ButtonSize {
	if s.Valid() {
		return s
	}
	return ButtonSizeDefault
}

func (s ButtonSize) attr() string {
	if s.orDefault() == ButtonSizeDefault {
		return "default"
	}
	return string(s.orDefault())
}

// ButtonType is the native button type attribute. The zero value is "button"
// (safe default: it never submits a form implicitly). It is meaningful only for
// the native <button> form; a link-form Button ignores it (and combining it with
// a URL is a validation error).
type ButtonType string

const (
	// ButtonTypeButton is the zero value and the safe default.
	ButtonTypeButton ButtonType = ""
	ButtonTypeSubmit ButtonType = "submit"
	ButtonTypeReset  ButtonType = "reset"
)

func (t ButtonType) attr() string {
	switch t {
	case ButtonTypeSubmit:
		return "submit"
	case ButtonTypeReset:
		return "reset"
	default:
		return "button"
	}
}

// ButtonProps configures Button (P06, family F2). One exported Button function
// renders either a native <button> (URL empty) or an anchor styled as a button
// (URL set). The principal label is templ children; LeadingIcon/TrailingIcon are
// optional decorative glyph slots. The zero value is a valid default button.
//
// Cross-field guard: a set URL together with a submit/reset Type OR Disabled is
// invalid (an anchor is neither a submit control nor disable-able). Validate
// reports it, and the render degrades to a native <button> that HONORS the
// Type/Disabled intent (the safer resolution — a disabled control stays disabled)
// and marks itself data-goth-invalid rather than silently emitting a
// spec-violating anchor.
type ButtonProps struct {
	Base
	Variant ButtonVariant
	Size    ButtonSize
	// Type is the native button type; ignored in the link form. Zero value is
	// "button".
	Type ButtonType
	// URL, when non-zero, renders the button as an <a> link styled as a button.
	URL URL
	// Disabled renders a disabled native button; invalid with a URL.
	Disabled bool
	// Loading renders a decorative spinner glyph, marks aria-busy, and disables the
	// native button so it cannot be re-submitted while pending.
	Loading bool
	// Label is the accessible name. It is REQUIRED for the icon-only size
	// (ButtonIcon) — an icon-only control cannot ship nameless — and is emitted as
	// aria-label whenever set. For a text button the visible children are the name
	// and Label may be left empty.
	Label string
	// LeadingIcon and TrailingIcon are optional decorative glyph slots (rendered
	// aria-hidden). For an icon-only button the icon is the LeadingIcon and Label
	// carries the accessible name.
	LeadingIcon  templ.Component
	TrailingIcon templ.Component
}

// Validate reports the invalid URL cross-field combinations. It returns
// ErrButtonURLConflict when a URL is combined with a submit/reset Type or a
// Disabled/Loading state. Callers may use it to fail fast; the render never
// panics and applies the documented safe degradation.
func (p ButtonProps) Validate() error {
	if p.URL.IsZero() {
		return nil
	}
	if p.Type == ButtonTypeSubmit || p.Type == ButtonTypeReset || p.Disabled || p.Loading {
		return ErrButtonURLConflict
	}
	return nil
}

// renderAsLink reports whether the button renders as an anchor: a non-zero URL
// with no conflicting cross-field state.
func (p ButtonProps) renderAsLink() bool {
	return !p.URL.IsZero() && p.Validate() == nil
}

func buttonClass(p ButtonProps) string { return classNames("goth-button", p.Class) }

func buttonAttrs(p ButtonProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":    "button",
		"data-variant": p.Variant.attr(),
		"data-size":    p.Size.attr(),
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	if p.renderAsLink() {
		return ownedAttrs(p.Base, owned)
	}
	// Native <button> form (URL empty, or the degraded invalid-URL case).
	owned["type"] = p.Type.attr()
	if p.Disabled || p.Loading {
		owned["disabled"] = true
	}
	if p.Loading {
		owned["aria-busy"] = "true"
	}
	if !p.URL.IsZero() {
		// The URL was dropped because of a cross-field conflict: mark it rather than
		// silently emitting a broken anchor.
		owned["data-goth-invalid"] = "button-url-conflict"
	}
	return ownedAttrs(p.Base, owned)
}
