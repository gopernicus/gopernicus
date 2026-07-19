package primitives

import (
	"strings"
	"testing"
)

// TestNoMenuPrimitiveEmitsInlineStyle proves invariant (a): no GOTH-4.4 menu
// primitive emits an inline style= in any state — placement is data-side/data-
// state + the CSSOM anchor offsets + external CSS, never a server-rendered style
// attribute.
func TestNoMenuPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, DropdownMenu(DropdownMenuProps{Open: true}), "x"),
		renderKids(t, DropdownMenuTrigger(DropdownMenuTriggerProps{}), "Open"),
		renderKids(t, DropdownMenuContent(DropdownMenuContentProps{Open: true}), "x"),
		renderKids(t, DropdownMenuItem(DropdownMenuItemProps{Value: "a"}), "A"),
		renderKids(t, DropdownMenuCheckboxItem(DropdownMenuCheckboxItemProps{Value: "a", Checked: true}), "A"),
		renderKids(t, DropdownMenuRadioItem(DropdownMenuRadioItemProps{Value: "a", Checked: true}), "A"),
		renderKids(t, DropdownMenuSubTrigger(DropdownMenuSubTriggerProps{}), "More"),
		renderKids(t, ContextMenu(ContextMenuProps{Open: true}), "x"),
		renderKids(t, ContextMenuTrigger(ContextMenuTriggerProps{}), "Right-click"),
		renderKids(t, ContextMenuContent(ContextMenuContentProps{}), "x"),
		renderKids(t, Menubar(MenubarProps{Label: "Main"}), "x"),
		renderKids(t, MenubarMenu(MenubarMenuProps{}), "x"),
		renderKids(t, MenubarTrigger(MenubarTriggerProps{}), "File"),
		renderKids(t, NavigationMenu(NavigationMenuProps{Label: "Main"}), "x"),
		renderKids(t, NavigationMenuLink(NavigationMenuLinkProps{URL: mustParseURL(t, "/docs"), Active: true}), "Docs"),
		renderKids(t, NavigationMenuSub(NavigationMenuSubProps{Open: true}), "x"),
		renderKids(t, NavigationMenuTrigger(NavigationMenuTriggerProps{}), "Products"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("menu primitive emitted an inline style=: %s", o)
		}
	}
}

// TestDropdownMenuStructure proves the gothMenu backing, the trigger/menu/item
// roles, the checkbox/radio menu-item roles with aria-checked, and the submenu
// wiring hooks.
func TestDropdownMenuStructure(t *testing.T) {
	root := renderKids(t, DropdownMenu(DropdownMenuProps{}), "x")
	mustContain(t, root, `class="goth-menu goth-dropdown-menu"`, `data-slot="menu"`, `data-state="closed"`, `x-data="gothMenu"`)

	trig := renderKids(t, DropdownMenuTrigger(DropdownMenuTriggerProps{Base: Base{ID: "dm-trigger"}}), "Actions")
	mustContain(t, trig, `<button`, `data-slot="trigger"`, `type="button"`, `aria-haspopup="menu"`, `aria-expanded="false"`,
		`x-on:click="toggle($event)"`, `x-on:keydown="onTriggerKeydown($event)"`, `id="dm-trigger"`, "Actions")

	content := renderKids(t, DropdownMenuContent(DropdownMenuContentProps{}), "x")
	mustContain(t, content, `class="goth-floating goth-menu-content"`, `data-slot="content"`, `role="menu"`,
		`data-state="closed"`, `x-on:keydown="onKeydown($event)"`)

	item := renderKids(t, DropdownMenuItem(DropdownMenuItemProps{Value: "rename", Disabled: true}), "Rename")
	mustContain(t, item, `data-slot="item"`, `role="menuitem"`, `type="button"`, `data-value="rename"`,
		`x-on:click="select($event)"`, `disabled`, `aria-disabled="true"`, "Rename")

	check := renderKids(t, DropdownMenuCheckboxItem(DropdownMenuCheckboxItemProps{Value: "grid", Checked: true}), "Grid")
	mustContain(t, check, `role="menuitemcheckbox"`, `aria-checked="true"`, `data-value="grid"`, `data-slot="item-indicator"`, "Grid")

	radio := renderKids(t, DropdownMenuRadioItem(DropdownMenuRadioItemProps{Value: "sm", Checked: false}), "Small")
	mustContain(t, radio, `role="menuitemradio"`, `aria-checked="false"`, `data-value="sm"`, "Small")

	label := renderKids(t, DropdownMenuLabel(DropdownMenuLabelProps{}), "View")
	mustContain(t, label, `class="goth-menu-label"`, `data-slot="label"`, "View")

	sep := renderKids(t, DropdownMenuSeparator(DropdownMenuSeparatorProps{}), "")
	mustContain(t, sep, `data-slot="separator"`, `role="separator"`)

	sub := renderKids(t, DropdownMenuSub(DropdownMenuSubProps{}), "x")
	mustContain(t, sub, `class="goth-submenu-root"`, `data-slot="submenu-root"`)

	subTrig := renderKids(t, DropdownMenuSubTrigger(DropdownMenuSubTriggerProps{Base: Base{ID: "sub-t"}}), "Share")
	mustContain(t, subTrig, `data-slot="submenu-trigger"`, `role="menuitem"`, `aria-haspopup="menu"`, `aria-expanded="false"`, "Share")

	subContent := renderKids(t, DropdownMenuSubContent(DropdownMenuSubContentProps{}), "x")
	mustContain(t, subContent, `class="goth-floating goth-menu-content"`, `data-slot="submenu"`, `role="menu"`, `data-state="closed"`)
}

// TestMenuServerOpenBaseline proves the no-JS baseline: an Open menu renders its
// root and content with data-state="open" so the panel is readable without
// JavaScript (matching the GOTH-4.2 modal/panel precedent).
func TestMenuServerOpenBaseline(t *testing.T) {
	root := renderKids(t, DropdownMenu(DropdownMenuProps{Open: true}), "x")
	mustContain(t, root, `data-slot="menu"`, `data-state="open"`)
	content := renderKids(t, DropdownMenuContent(DropdownMenuContentProps{Open: true}), "x")
	mustContain(t, content, `data-slot="content"`, `data-state="open"`)
}

// TestContextMenuTrigger proves the right-click region carries the contextmenu +
// keyboard openers and is focusable, and the root is gothMenu-backed.
func TestContextMenuTrigger(t *testing.T) {
	root := renderKids(t, ContextMenu(ContextMenuProps{}), "x")
	mustContain(t, root, `class="goth-menu goth-context-menu"`, `data-slot="context-menu"`, `x-data="gothMenu"`)

	region := renderKids(t, ContextMenuTrigger(ContextMenuTriggerProps{}), "Right-click here")
	mustContain(t, region, `data-slot="context-trigger"`, `tabindex="0"`,
		`x-on:contextmenu="openContext($event)"`, `x-on:keydown="onContextKeydown($event)"`, "Right-click here")

	item := renderKids(t, ContextMenuItem(ContextMenuItemProps{Value: "copy"}), "Copy")
	mustContain(t, item, `role="menuitem"`, `data-value="copy"`, "Copy")
}

// TestMenubarStructure proves the menubar landmark, each menu's gothMenu backing,
// and the top-level triggers' role=menuitem within role=menubar.
func TestMenubarStructure(t *testing.T) {
	bar := renderKids(t, Menubar(MenubarProps{Label: "Application"}), "x")
	mustContain(t, bar, `class="goth-menubar"`, `data-slot="menubar"`, `role="menubar"`, `aria-label="Application"`)

	menu := renderKids(t, MenubarMenu(MenubarMenuProps{}), "x")
	mustContain(t, menu, `class="goth-menu goth-menubar-menu"`, `data-slot="menubar-menu"`, `x-data="gothMenu"`)

	trig := renderKids(t, MenubarTrigger(MenubarTriggerProps{Base: Base{ID: "mb-file"}}), "File")
	mustContain(t, trig, `data-slot="trigger"`, `role="menuitem"`, `aria-haspopup="menu"`,
		`x-on:click="toggle($event)"`, `x-on:keydown="onTriggerKeydown($event)"`, "File")

	content := renderKids(t, MenubarContent(MenubarContentProps{}), "x")
	mustContain(t, content, `data-slot="content"`, `role="menu"`, `x-on:keydown="onKeydown($event)"`)
}

// TestNavigationMenuLinksAndDisclosure proves Navigation Menu is link-first and
// native (no controller): links are real anchors with href, the active link
// carries aria-current, and the disclosure is a native <details>/<summary>.
func TestNavigationMenuLinksAndDisclosure(t *testing.T) {
	nav := renderKids(t, NavigationMenu(NavigationMenuProps{Label: "Primary"}), "x")
	mustContain(t, nav, `<nav`, `class="goth-navigation-menu"`, `data-slot="navigation-menu"`, `aria-label="Primary"`)
	mustNotContain(t, nav, `x-data`) // link-first, no controller

	link := renderKids(t, NavigationMenuLink(NavigationMenuLinkProps{URL: mustParseURL(t, "/pricing"), Active: true}), "Pricing")
	mustContain(t, link, `<a`, `href="/pricing"`, `data-slot="navigation-menu-link"`, `aria-current="page"`, `data-active="true"`, "Pricing")

	plain := renderKids(t, NavigationMenuLink(NavigationMenuLinkProps{URL: mustParseURL(t, "/blog")}), "Blog")
	mustContain(t, plain, `href="/blog"`, "Blog")
	mustNotContain(t, plain, `aria-current`)

	sub := renderKids(t, NavigationMenuSub(NavigationMenuSubProps{Open: true}), "x")
	mustContain(t, sub, `<details`, `class="goth-navigation-menu-sub"`, `data-slot="navigation-menu-sub"`, `open`)

	trig := renderKids(t, NavigationMenuTrigger(NavigationMenuTriggerProps{}), "Products")
	mustContain(t, trig, `<summary`, `data-slot="navigation-menu-trigger"`, "Products")

	content := renderKids(t, NavigationMenuContent(NavigationMenuContentProps{}), "links")
	mustContain(t, content, `data-slot="navigation-menu-content"`, "links")
}

// TestMenuMergeHonorsOwnership proves a caller cannot overwrite a behavior-
// critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch, while a benign caller data-* attribute merges.
func TestMenuMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, DropdownMenu(DropdownMenuProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"x-data":        "evil",
			"data-slot":     "hijack",
			"class":         "dropped",
			"data-testhook": "keep",
		},
	}}), "x")
	mustContain(t, out, `x-data="gothMenu"`, `data-slot="menu"`, `goth-menu goth-dropdown-menu custom-x`, `data-testhook="keep"`)
	mustNotContain(t, out, `evil`, `hijack`, `dropped`)
}
