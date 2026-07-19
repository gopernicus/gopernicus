package primitives

import "github.com/a-h/templ"

// Dropdown Menu (P41, family F4). Compound parts the caller composes into a
// trigger button and an anchored menu of actions. Backed by the frozen gothMenu
// controller (README §8): the trigger toggles the panel, roving focus + typeahead
// drive the items, submenus open with the RTL-aware keys, and Escape/outside
// press dismiss it. No-JS baseline is server-owned via DropdownMenuProps.Open (see
// menu.go). Item roles: DropdownMenuItem is role="menuitem";
// DropdownMenuCheckboxItem / DropdownMenuRadioItem carry the checkbox/radio menu
// roles with server-owned aria-checked. data-slot hooks: menu, trigger, content,
// item, label, separator, group, submenu-root, submenu-trigger, submenu.

// DropdownMenuProps configures the Dropdown Menu root.
type DropdownMenuProps struct {
	Base
	// Open renders the menu server-open (the readable no-JS baseline). Zero value
	// is closed (only the trigger shows without JavaScript).
	Open bool
}

// DropdownMenuTriggerProps configures the trigger button.
type DropdownMenuTriggerProps struct{ Base }

// DropdownMenuContentProps configures the floating menu panel.
type DropdownMenuContentProps struct {
	Base
	// Open must match DropdownMenuProps.Open so the panel's data-state agrees with
	// the root for the server-owned no-JS baseline.
	Open bool
}

// DropdownMenuItemProps configures a role=menuitem action.
type DropdownMenuItemProps struct {
	Base
	// Value is reported on goth:select. Optional.
	Value string
	// Disabled marks the item non-actionable.
	Disabled bool
}

// DropdownMenuCheckboxItemProps configures a role=menuitemcheckbox item with
// server-owned Checked state.
type DropdownMenuCheckboxItemProps struct {
	Base
	Value    string
	Checked  bool
	Disabled bool
}

// DropdownMenuRadioItemProps configures a role=menuitemradio item with
// server-owned Checked state.
type DropdownMenuRadioItemProps struct {
	Base
	Value    string
	Checked  bool
	Disabled bool
}

// DropdownMenuLabelProps configures a non-interactive group label.
type DropdownMenuLabelProps struct{ Base }

// DropdownMenuSeparatorProps configures a role=separator divider.
type DropdownMenuSeparatorProps struct{ Base }

// DropdownMenuGroupProps configures a role=group wrapper.
type DropdownMenuGroupProps struct{ Base }

// DropdownMenuSubProps configures a submenu wrapper.
type DropdownMenuSubProps struct{ Base }

// DropdownMenuSubTriggerProps configures a submenu trigger (a menuitem that opens
// the nested panel).
type DropdownMenuSubTriggerProps struct{ Base }

// DropdownMenuSubContentProps configures the nested submenu panel.
type DropdownMenuSubContentProps struct{ Base }

func dropdownMenuAttrs(p DropdownMenuProps) templ.Attributes {
	return menuRootAttrs(p.Base, "menu", p.Open)
}

func dropdownMenuContentAttrs(p DropdownMenuContentProps) templ.Attributes {
	return menuContentAttrs(p.Base, p.Open)
}
