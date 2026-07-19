package primitives

import "github.com/a-h/templ"

// Sidebar (P54, family F4) is the application navigation shell. It is a COMPOSITION,
// not a new controller: the mobile off-canvas overlay reuses the frozen gothDialog
// overlay mechanics (scrim/panel, focus trap+restore, background scroll lock, inert,
// nested-aware Escape/outside dismissal) exactly as Sheet (P47) does, and nested
// collapsible groups reuse Collapsible (P29, native <details> + gothCollapse). No new
// §8 controller name is introduced.
//
// State ownership (README §8 invariant 1). The SERVER owns two orthogonal states:
//
//   - the mobile off-canvas open/closed state (SidebarProps.Open → the gothDialog
//     data-state, so a server-open sheet is readable with no JavaScript); and
//   - the desktop expanded/collapsed rail state (SidebarProps.Collapsed →
//     data-collapsed, driving the rail width and label visibility through CSS).
//
// Both states are expressible as server round-trips: SidebarRail is a real link the
// host points at a toggle URL, so collapse/expand works with no JavaScript (full
// document reload) and can be HTMX-enhanced through Base.Attributes with no new
// controller. Preference PERSISTENCE is deliberately NOT a client concern here: the
// primitive renders whatever Collapsed/Open the server supplies, so a host persists
// the preference in its own namespaced cookie/query param (opt-in, host-owned) and
// the kit adds no client persistence surface.
//
// Responsive behavior. One markup renders both breakpoints via CSS: below the
// sidebar breakpoint the panel is an off-canvas sheet (scrim + slide-in, gothDialog
// governs open/close, SidebarTrigger opens it); at/above it the panel is a static
// rail in flow (scrim/trigger suppressed, width driven by data-collapsed). gothDialog
// stays dormant on the desktop rail because the trigger is display:none there.
//
// Navigation semantics (P44 precedent). Menu buttons are real <a href> links with
// aria-current="page" for the active item; a plain <button> is rendered only when no
// URL is set. Nested sub-menus are ordinary link lists the caller may wrap in a
// Collapsible.
//
// data-slot hooks: sidebar, trigger, overlay, scrim, content, sidebar-header,
// sidebar-content, sidebar-footer, sidebar-group, sidebar-group-label, sidebar-menu,
// sidebar-menu-item, sidebar-menu-button, sidebar-menu-icon, sidebar-menu-label,
// sidebar-menu-sub, sidebar-menu-sub-item, sidebar-menu-sub-button, sidebar-separator,
// sidebar-rail, sidebar-close.

// SidebarSide is the viewport edge the rail is pinned to (desktop) and the sheet
// slides in from (mobile). The zero value is the left edge.
type SidebarSide string

const (
	// SidebarLeft is the zero value: the rail/sheet is on the left (inline-start).
	SidebarLeft SidebarSide = ""
	// SidebarRight pins the rail/sheet to the right (inline-end).
	SidebarRight SidebarSide = "right"
)

// Valid reports whether s is a known SidebarSide.
func (s SidebarSide) Valid() bool {
	switch s {
	case SidebarLeft, SidebarRight:
		return true
	default:
		return false
	}
}

func (s SidebarSide) attr() string {
	if s == SidebarRight {
		return "right"
	}
	return "left"
}

// SidebarProviderProps configures the app-shell layout row that holds the Sidebar
// and the SidebarInset side by side. On the desktop rail it is a flex row; the
// off-canvas mobile sheet floats over it.
type SidebarProviderProps struct{ Base }

// SidebarInsetProps configures the main content region beside the rail.
type SidebarInsetProps struct{ Base }

// SidebarProps configures the Sidebar root (the gothDialog-backed shell). The caller
// composes SidebarTrigger and SidebarPanel inside it (the Sheet composition shape).
type SidebarProps struct {
	Base
	// Open is the server-rendered mobile off-canvas state. Zero value is closed; the
	// desktop rail ignores it (CSS shows the rail regardless).
	Open bool
	// Collapsed is the server-rendered desktop rail state. Zero value is expanded;
	// true renders the icon-only rail (labels visually hidden, accessible names kept).
	Collapsed bool
}

// SidebarTriggerProps configures the mobile hamburger button that opens the sheet.
// It is display:none on the desktop rail.
type SidebarTriggerProps struct{ Base }

// SidebarPanelProps configures the overlay+scrim+nav panel. The <nav> is the
// navigation landmark and the gothDialog focus-trap region (data-slot="content").
type SidebarPanelProps struct {
	Base
	// Side selects the pinned edge. Zero value is the left edge.
	Side SidebarSide
	// Label is the navigation landmark's accessible name (aria-label). Recommended so
	// assistive technology distinguishes it from other navs on the page.
	Label string
}

// SidebarHeaderProps configures the fixed header region (brand / rail toggle).
type SidebarHeaderProps struct{ Base }

// SidebarContentProps configures the scrollable body region (holds the groups).
type SidebarContentProps struct{ Base }

// SidebarFooterProps configures the fixed footer region.
type SidebarFooterProps struct{ Base }

// SidebarGroupProps configures a labelled section of the menu.
type SidebarGroupProps struct{ Base }

// SidebarGroupLabelProps configures a group heading. It is hidden in the collapsed
// rail.
type SidebarGroupLabelProps struct{ Base }

// SidebarMenuProps configures the menu list (<ul>).
type SidebarMenuProps struct{ Base }

// SidebarMenuItemProps configures a single menu row (<li>).
type SidebarMenuItemProps struct{ Base }

// SidebarMenuButtonProps configures a navigation entry. A set URL renders an <a>
// (the no-JS baseline); an empty URL renders a <button> for a caller-driven action.
type SidebarMenuButtonProps struct {
	Base
	// URL is the destination. When set the entry is a real link; empty renders a
	// <button>.
	URL URL
	// Active marks the current page (aria-current="page" + data-active).
	Active bool
	// Icon is the leading control glyph, shown in both expanded and collapsed rails.
	Icon templ.Component
}

// SidebarMenuSubProps configures a nested link list (<ul>) under a menu item.
type SidebarMenuSubProps struct{ Base }

// SidebarMenuSubItemProps configures a nested row (<li>).
type SidebarMenuSubItemProps struct{ Base }

// SidebarMenuSubButtonProps configures a nested navigation link.
type SidebarMenuSubButtonProps struct {
	Base
	// URL is the destination. When set the entry is a real link; empty renders a
	// <button>.
	URL URL
	// Active marks the current page (aria-current="page" + data-active).
	Active bool
}

// SidebarSeparatorProps configures a decorative divider between groups.
type SidebarSeparatorProps struct{ Base }

// SidebarRailProps configures the desktop collapse/expand control. It is a real link
// (server round-trip): the host points URL at a toggle route that flips the
// server-owned collapsed state. With no JavaScript it navigates; enhanced, hx-* on
// Base.Attributes can swap instead. It carries aria-expanded for the current state.
type SidebarRailProps struct {
	Base
	// URL is the server toggle target that flips the collapsed state. Empty renders a
	// non-navigating control (rarely wanted — set a URL for the no-JS round-trip).
	URL URL
	// Expanded reflects the CURRENT rail state as aria-expanded (true = expanded).
	Expanded bool
	// Label is the control's accessible name (aria-label), e.g. "Toggle sidebar".
	Label string
}

// SidebarCloseProps configures a close button inside the mobile sheet panel.
type SidebarCloseProps struct{ Base }

func sidebarClass(p SidebarProps) string { return classNames("goth-sidebar", p.Class) }

func sidebarAttrs(p SidebarProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":      "sidebar",
		"data-state":     openState(p.Open),
		"data-collapsed": boolAttr(p.Collapsed),
		"x-data":         "gothDialog",
	}
	return ownedAttrs(p.Base, owned)
}

func sidebarPanelClass(p SidebarPanelProps) string { return classNames("goth-sidebar-panel", p.Class) }

func sidebarPanelAttrs(p SidebarPanelProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "content",
		"data-side": p.Side.attr(),
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func sidebarPartClass(base Base, stable string) string { return classNames(stable, base.Class) }

func sidebarPartAttrs(base Base, slot string) templ.Attributes {
	return ownedAttrs(base, templ.Attributes{"data-slot": slot})
}

// sidebarMenuButtonAttrs carries the slot + active-state hooks. The href itself is
// rendered natively in the templ so a typed safe URL is emitted, never smuggled
// through the generic attribute spread.
func sidebarMenuButtonAttrs(base Base, active, link bool, slot string) templ.Attributes {
	owned := templ.Attributes{"data-slot": slot}
	if !link {
		owned["type"] = "button"
	}
	if active {
		owned["aria-current"] = "page"
		owned["data-active"] = "true"
	}
	return ownedAttrs(base, owned)
}

func sidebarRailAttrs(p SidebarRailProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":     "sidebar-rail",
		"aria-expanded": boolAttr(p.Expanded),
	}
	if p.URL.IsZero() {
		owned["type"] = "button"
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}
