package primitives

import "github.com/a-h/templ"

// Menubar (P43, family F4). A horizontal role=menubar of menu buttons, each
// opening a dropdown menu. Compound parts: Menubar wraps several MenubarMenu
// groups; each MenubarMenu is a gothMenu (README §8) with a MenubarTrigger
// (role=menuitem, aria-haspopup) and a MenubarContent panel. gothMenu detects the
// menubar ancestor and coordinates horizontal roving across the triggers
// (ArrowLeft/ArrowRight, RTL-aware), opens the adjacent menu when one is already
// open, opens on ArrowDown/Enter/Space, and unwinds with Escape. No-JS baseline
// is server-owned via MenubarMenuProps.Open (see menu.go); a menubar is an
// application menu with no CSS-only open path, so the readable baseline is the
// server-open state. data-slot hooks: menubar, menubar-menu, trigger, content,
// item, label, separator, submenu-root, submenu-trigger, submenu.

// MenubarProps configures the menubar container.
type MenubarProps struct {
	Base
	// Label is the accessible name for the menubar (aria-label). Optional but
	// recommended so assistive technology announces the bar's purpose.
	Label string
}

// MenubarMenuProps configures one menu within the bar (a gothMenu root).
type MenubarMenuProps struct {
	Base
	// Open renders this menu server-open (the readable no-JS baseline). Zero value
	// is closed.
	Open bool
}

// MenubarTriggerProps configures a top-level menu button (role=menuitem).
type MenubarTriggerProps struct{ Base }

// MenubarContentProps configures a menu's floating panel.
type MenubarContentProps struct {
	Base
	// Open must match MenubarMenuProps.Open for the server-owned no-JS baseline.
	Open bool
}

// MenubarItemProps configures a role=menuitem action.
type MenubarItemProps struct {
	Base
	Value    string
	Disabled bool
}

// MenubarCheckboxItemProps configures a role=menuitemcheckbox item.
type MenubarCheckboxItemProps struct {
	Base
	Value    string
	Checked  bool
	Disabled bool
}

// MenubarRadioItemProps configures a role=menuitemradio item.
type MenubarRadioItemProps struct {
	Base
	Value    string
	Checked  bool
	Disabled bool
}

// MenubarLabelProps configures a non-interactive group label.
type MenubarLabelProps struct{ Base }

// MenubarSeparatorProps configures a role=separator divider.
type MenubarSeparatorProps struct{ Base }

// MenubarSubProps configures a submenu wrapper.
type MenubarSubProps struct{ Base }

// MenubarSubTriggerProps configures a submenu trigger.
type MenubarSubTriggerProps struct{ Base }

// MenubarSubContentProps configures the nested submenu panel.
type MenubarSubContentProps struct{ Base }

func menubarAttrs(p MenubarProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "menubar",
		"role":      "menubar",
	}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

func menubarMenuAttrs(p MenubarMenuProps) templ.Attributes {
	return menuRootAttrs(p.Base, "menubar-menu", p.Open)
}

// menubarTriggerAttrs builds a menubar top-level button. Like a dropdown trigger
// (toggle on click, keyboard opening) but role=menuitem for the menubar pattern.
func menubarTriggerAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":     "trigger",
		"type":          "button",
		"role":          "menuitem",
		"aria-haspopup": "menu",
		"aria-expanded": "false",
		"x-on:click":    "toggle($event)",
		"x-on:keydown":  "onTriggerKeydown($event)",
	})
}

func menubarContentAttrs(p MenubarContentProps) templ.Attributes {
	return menuContentAttrs(p.Base, p.Open)
}
