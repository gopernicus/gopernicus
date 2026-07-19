package primitives

import "github.com/a-h/templ"

// SheetSide is the viewport edge a Sheet panel is anchored to. The zero value is
// the right edge.
type SheetSide string

const (
	// SheetRight is the zero value: the panel is pinned to the right edge.
	SheetRight SheetSide = ""
	// SheetLeft pins the panel to the left edge.
	SheetLeft SheetSide = "left"
	// SheetTop pins the panel to the top edge.
	SheetTop SheetSide = "top"
	// SheetBottom pins the panel to the bottom edge.
	SheetBottom SheetSide = "bottom"
)

// Valid reports whether s is a known SheetSide.
func (s SheetSide) Valid() bool {
	switch s {
	case SheetRight, SheetLeft, SheetTop, SheetBottom:
		return true
	default:
		return false
	}
}

func (s SheetSide) attr() string {
	switch s {
	case SheetLeft:
		return "left"
	case SheetTop:
		return "top"
	case SheetBottom:
		return "bottom"
	default:
		return "right"
	}
}

// SheetProps configures the Sheet container (P47, family F4): a dialog-backed edge
// panel. It is always modal (background scroll lock + inert) and reuses the frozen
// gothDialog overlay mechanics; only the panel geometry (edge-anchored, slide-in)
// differs from Dialog. No-JS baseline: the server-owned data-state governs
// visibility, so a server-open sheet is readable without JavaScript. The slide-in
// animation collapses under prefers-reduced-motion. ARIA linkage is caller-passed
// via Base.ID (Labelledby/Describedby). data-slot hooks: sheet, trigger, overlay,
// scrim, content, header, footer, title, description, close.
type SheetProps struct {
	Base
	// Open is the server-rendered initial state. Zero value is closed.
	Open bool
}

// SheetTriggerProps configures the trigger button that opens the sheet.
type SheetTriggerProps struct{ Base }

// SheetContentProps configures the overlay+edge panel (role=dialog).
type SheetContentProps struct {
	Base
	// Side selects the anchored edge. Zero value is the right edge.
	Side SheetSide
	// Labelledby is the SheetTitle id (aria-labelledby).
	Labelledby string
	// Describedby is the SheetDescription id (aria-describedby).
	Describedby string
}

// SheetHeaderProps configures the header layout region.
type SheetHeaderProps struct{ Base }

// SheetFooterProps configures the footer action region.
type SheetFooterProps struct{ Base }

// SheetTitleProps configures the title (h2); set Base.ID and reference it from
// SheetContent.Labelledby.
type SheetTitleProps struct{ Base }

// SheetDescriptionProps configures the description (p); set Base.ID and reference
// it from SheetContent.Describedby.
type SheetDescriptionProps struct{ Base }

// SheetCloseProps configures a close button that dismisses the sheet.
type SheetCloseProps struct{ Base }

func sheetClass(p SheetProps) string { return classNames("goth-sheet", p.Class) }

func sheetAttrs(p SheetProps) templ.Attributes {
	return overlayRootAttrs(p.Base, "sheet", p.Open, true, true)
}

func sheetPanelClass(p SheetContentProps) string { return classNames("goth-sheet-panel", p.Class) }

func sheetPanelAttrs(p SheetContentProps) templ.Attributes {
	return overlayEdgePanelAttrs(p.Base, p.Side.attr(), p.Labelledby, p.Describedby)
}
