package primitives

import "github.com/a-h/templ"

// Shared menu helpers for the GOTH-4.4 action-menu primitives: Dropdown Menu
// (P41), Context Menu (P38), and Menubar (P43). All three compose the frozen
// GOTH-4.1 menu mechanics (anchored placement, roving focus + typeahead, submenu
// hierarchy, nested-aware Escape/outside dismissal) through the single gothMenu
// controller — no primitive forks the mechanics and no new controller name is
// introduced (README §8). The div-based floating panel is the same
// .goth-floating layer GOTH-4.1 froze and browser-proved CSP-clean.
//
// No-JS baseline (server-owned, matching the GOTH-4.2 modal/panel precedent): the
// root's server-rendered data-state governs visibility — a menu rendered Open
// shows its content panel with no JavaScript, a closed menu shows only its
// trigger. A pointer/keyboard-invoked action menu has no CSS-only open path, so
// the honest readable baseline is the server-open state (Navigation Menu (P44),
// which is real links, ships a genuinely link-first no-JS baseline instead).
//
// Item roles per the parity rows: plain items are role="menuitem"; the check/
// radio variants carry role="menuitemcheckbox"/role="menuitemradio" with
// aria-checked. State transitions are server-owned (invariant 1): clicking an
// item dispatches goth:select and closes the menu; the app applies the change.

// menuRootAttrs builds the gothMenu root attributes shared by the action menus.
// The server-owned data-state is the no-JS baseline. slot names the primitive's
// stable data-slot (menu / context-menu / menubar-menu).
func menuRootAttrs(b Base, slot string, open bool) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":  slot,
		"data-state": openState(open),
		"x-data":     "gothMenu",
	})
}

// menuTriggerAttrs builds a button trigger that toggles the menu on click and
// opens it from the keyboard (ArrowDown/Enter/Space) and, inside a menubar,
// arrows across sibling triggers — all handled by gothMenu.
func menuTriggerAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":     "trigger",
		"type":          "button",
		"aria-haspopup": "menu",
		"aria-expanded": "false",
		"x-on:click":    "toggle($event)",
		"x-on:keydown":  "onTriggerKeydown($event)",
	})
}

// menuContentAttrs builds the floating menu panel (role=menu). The gothMenu
// controller anchors it under the trigger, roves focus over its items, and runs
// typeahead; keydown is bound here so the panel owns navigation.
func menuContentAttrs(b Base, open bool) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":    "content",
		"role":         "menu",
		"data-state":   openState(open),
		"x-on:keydown": "onKeydown($event)",
	})
}

// menuItemAttrs builds a role=menuitem action button. value is the data-value the
// controller reports on goth:select; disabled marks it non-actionable.
func menuItemAttrs(b Base, value string, disabled bool) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "item",
		"role":       "menuitem",
		"type":       "button",
		"x-on:click": "select($event)",
	}
	if value != "" {
		owned["data-value"] = value
	}
	if disabled {
		owned["disabled"] = true
		owned["aria-disabled"] = "true"
	}
	return ownedAttrs(b, owned)
}

// menuCheckedItemAttrs builds a role=menuitemcheckbox / role=menuitemradio button
// carrying aria-checked (server-owned state). role is "menuitemcheckbox" or
// "menuitemradio".
func menuCheckedItemAttrs(b Base, role, value string, checked, disabled bool) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":    "item",
		"role":         role,
		"type":         "button",
		"aria-checked": boolString(checked),
		"x-on:click":   "select($event)",
	}
	if value != "" {
		owned["data-value"] = value
	}
	if disabled {
		owned["disabled"] = true
		owned["aria-disabled"] = "true"
	}
	return ownedAttrs(b, owned)
}

// menuLabelAttrs builds a non-interactive group label.
func menuLabelAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": "label"})
}

// menuSeparatorAttrs builds a role=separator divider.
func menuSeparatorAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot": "separator",
		"role":      "separator",
	})
}

// menuGroupAttrs builds a role=group wrapper for related items.
func menuGroupAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot": "group",
		"role":      "group",
	})
}

// menuSubRootAttrs builds the submenu wrapper the controller wires as a submenu
// (its own open/close/roving/anchor lifecycle, RTL-aware keys).
func menuSubRootAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": "submenu-root"})
}

// menuSubTriggerAttrs builds the submenu trigger (a menuitem that opens the nested
// panel). It carries aria-haspopup="menu" and aria-expanded.
func menuSubTriggerAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":     "submenu-trigger",
		"type":          "button",
		"role":          "menuitem",
		"aria-haspopup": "menu",
		"aria-expanded": "false",
	})
}

// menuSubContentAttrs builds the nested submenu panel (role=menu).
func menuSubContentAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":  "submenu",
		"role":       "menu",
		"data-state": "closed",
	})
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
