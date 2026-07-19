package primitives

import "github.com/a-h/templ"

// DrawerSide is the viewport edge a Drawer panel is anchored to. The zero value is
// the bottom edge (the responsive mobile-drawer default).
type DrawerSide string

const (
	// DrawerBottom is the zero value: the panel is pinned to the bottom edge.
	DrawerBottom DrawerSide = ""
	// DrawerTop pins the panel to the top edge.
	DrawerTop DrawerSide = "top"
	// DrawerLeft pins the panel to the left edge.
	DrawerLeft DrawerSide = "left"
	// DrawerRight pins the panel to the right edge.
	DrawerRight DrawerSide = "right"
)

// Valid reports whether s is a known DrawerSide.
func (s DrawerSide) Valid() bool {
	switch s {
	case DrawerBottom, DrawerTop, DrawerLeft, DrawerRight:
		return true
	default:
		return false
	}
}

func (s DrawerSide) attr() string {
	switch s {
	case DrawerTop:
		return "top"
	case DrawerLeft:
		return "left"
	case DrawerRight:
		return "right"
	default:
		return "bottom"
	}
}

// DrawerProps configures the Drawer container (P40, family F4): a dialog-backed
// edge panel that defaults to a bottom sheet with a decorative drag handle. It is
// always modal and reuses the frozen gothDialog overlay mechanics; drag-to-dismiss
// is intentionally out of scope for this wave (the reduced-motion-honored slide is
// the only motion). No-JS baseline: the server-owned data-state governs
// visibility, so a server-open drawer is readable without JavaScript. ARIA linkage
// is caller-passed via Base.ID (Labelledby/Describedby). data-slot hooks: drawer,
// trigger, overlay, scrim, content, handle, header, footer, title, description,
// close.
type DrawerProps struct {
	Base
	// Open is the server-rendered initial state. Zero value is closed.
	Open bool
}

// DrawerTriggerProps configures the trigger button that opens the drawer.
type DrawerTriggerProps struct{ Base }

// DrawerContentProps configures the overlay+edge panel (role=dialog). A bottom or
// top drawer renders a decorative drag handle at the leading edge.
type DrawerContentProps struct {
	Base
	// Side selects the anchored edge. Zero value is the bottom edge.
	Side DrawerSide
	// Labelledby is the DrawerTitle id (aria-labelledby).
	Labelledby string
	// Describedby is the DrawerDescription id (aria-describedby).
	Describedby string
}

// DrawerHeaderProps configures the header layout region.
type DrawerHeaderProps struct{ Base }

// DrawerFooterProps configures the footer action region.
type DrawerFooterProps struct{ Base }

// DrawerTitleProps configures the title (h2); set Base.ID and reference it from
// DrawerContent.Labelledby.
type DrawerTitleProps struct{ Base }

// DrawerDescriptionProps configures the description (p); set Base.ID and reference
// it from DrawerContent.Describedby.
type DrawerDescriptionProps struct{ Base }

// DrawerCloseProps configures a close button that dismisses the drawer.
type DrawerCloseProps struct{ Base }

func drawerClass(p DrawerProps) string { return classNames("goth-drawer", p.Class) }

func drawerAttrs(p DrawerProps) templ.Attributes {
	return overlayRootAttrs(p.Base, "drawer", p.Open, true, true)
}

func drawerPanelClass(p DrawerContentProps) string { return classNames("goth-drawer-panel", p.Class) }

func drawerPanelAttrs(p DrawerContentProps) templ.Attributes {
	return overlayEdgePanelAttrs(p.Base, p.Side.attr(), p.Labelledby, p.Describedby)
}

// drawerHasHandle reports whether the decorative drag handle renders for this side
// (bottom/top drawers only, where a top/bottom grabber reads correctly).
func drawerHasHandle(p DrawerContentProps) bool {
	return p.Side == DrawerBottom || p.Side == DrawerTop
}
