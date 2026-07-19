package primitives

import "github.com/a-h/templ"

// Context Menu (P38, family F4). Compound parts the caller composes into a
// right-clickable target region (ContextMenuTrigger) and an anchored menu of
// actions positioned at the pointer. Backed by the frozen gothMenu controller
// (README §8): a contextmenu (right-click / long-press) opens the panel at the
// pointer, the ContextMenu key / Shift+F10 opens it from the keyboard at the
// region, roving focus + typeahead drive the items, submenus open with the
// RTL-aware keys, and Escape/outside press dismiss it (focus returns to the
// region). A pointer-invoked menu has no CSS-only open path, so the honest no-JS
// baseline is the server-open state (ContextMenuProps.Open); with no JavaScript a
// right-click shows the browser's native menu instead. data-slot hooks:
// context-menu, context-trigger, content, item, label, separator, submenu-root,
// submenu-trigger, submenu.

// ContextMenuProps configures the Context Menu root.
type ContextMenuProps struct {
	Base
	// Open renders the menu server-open (the readable no-JS baseline). Zero value
	// is closed.
	Open bool
}

// ContextMenuTriggerProps configures the right-clickable target region. It is
// focusable (tabindex 0) so the ContextMenu key / Shift+F10 can open the menu.
type ContextMenuTriggerProps struct{ Base }

// ContextMenuContentProps configures the floating menu panel.
type ContextMenuContentProps struct {
	Base
	// Open must match ContextMenuProps.Open for the server-owned no-JS baseline.
	Open bool
}

// ContextMenuItemProps configures a role=menuitem action.
type ContextMenuItemProps struct {
	Base
	Value    string
	Disabled bool
}

// ContextMenuCheckboxItemProps configures a role=menuitemcheckbox item.
type ContextMenuCheckboxItemProps struct {
	Base
	Value    string
	Checked  bool
	Disabled bool
}

// ContextMenuRadioItemProps configures a role=menuitemradio item.
type ContextMenuRadioItemProps struct {
	Base
	Value    string
	Checked  bool
	Disabled bool
}

// ContextMenuLabelProps configures a non-interactive group label.
type ContextMenuLabelProps struct{ Base }

// ContextMenuSeparatorProps configures a role=separator divider.
type ContextMenuSeparatorProps struct{ Base }

// ContextMenuSubProps configures a submenu wrapper.
type ContextMenuSubProps struct{ Base }

// ContextMenuSubTriggerProps configures a submenu trigger.
type ContextMenuSubTriggerProps struct{ Base }

// ContextMenuSubContentProps configures the nested submenu panel.
type ContextMenuSubContentProps struct{ Base }

func contextMenuAttrs(p ContextMenuProps) templ.Attributes {
	return menuRootAttrs(p.Base, "context-menu", p.Open)
}

// contextMenuTriggerAttrs builds the focusable right-click region. gothMenu binds
// openContext on contextmenu and onContextKeydown for the keyboard open keys.
func contextMenuTriggerAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{
		"data-slot":        "context-trigger",
		"tabindex":         "0",
		"x-on:contextmenu": "openContext($event)",
		"x-on:keydown":     "onContextKeydown($event)",
	})
}

func contextMenuContentAttrs(p ContextMenuContentProps) templ.Attributes {
	return menuContentAttrs(p.Base, p.Open)
}
