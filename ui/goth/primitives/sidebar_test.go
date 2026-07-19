package primitives

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// GOTH-5.4 Sidebar (P54). These tests prove the composition contract: the
// gothDialog-backed root carries both server-owned states (mobile data-state +
// desktop data-collapsed), the panel is a navigation landmark and the gothDialog
// focus-trap region (data-slot="content"), menu buttons are real links with
// aria-current, the rail collapse toggle is a real round-trip link with
// aria-expanded, attribute-merge ownership holds, and no part emits an inline style.

// TestSidebarRootServerOwnedState proves the root binds gothDialog and reflects BOTH
// server-owned states (mobile open + desktop collapsed) as data hooks CSS reads.
func TestSidebarRootServerOwnedState(t *testing.T) {
	closed := renderKids(t, Sidebar(SidebarProps{}), "x")
	mustContain(t, closed,
		`class="goth-sidebar"`, `data-slot="sidebar"`, `x-data="gothDialog"`,
		`data-state="closed"`, `data-collapsed="false"`)

	open := renderKids(t, Sidebar(SidebarProps{Open: true, Collapsed: true}), "x")
	mustContain(t, open, `data-state="open"`, `data-collapsed="true"`)
}

// TestSidebarPanelIsNavLandmark proves the panel is the <nav> landmark and the
// gothDialog focus-trap region, carrying the caller-passed accessible name and side.
func TestSidebarPanelIsNavLandmark(t *testing.T) {
	left := renderKids(t, SidebarPanel(SidebarPanelProps{Label: "Main"}), "body")
	mustContain(t, left,
		`class="goth-sidebar-overlay"`, `data-slot="overlay"`,
		`class="goth-sidebar-scrim"`, `data-slot="scrim"`, `aria-hidden="true"`,
		`<nav`, `class="goth-sidebar-panel"`, `data-slot="content"`,
		`data-side="left"`, `aria-label="Main"`, "body")

	right := renderKids(t, SidebarPanel(SidebarPanelProps{Side: SidebarRight}), "x")
	mustContain(t, right, `data-side="right"`)
}

// TestSidebarMenuButtonNavigation proves the link-first navigation semantics: a set
// URL renders a real <a href> carrying aria-current for the active page; an empty URL
// renders a <button>; the icon is decorative and the label carries the name.
func TestSidebarMenuButtonNavigation(t *testing.T) {
	icon := templ.Raw(`<svg aria-hidden="true"></svg>`)
	active := renderKids(t, SidebarMenuButton(SidebarMenuButtonProps{
		URL: mustParseURL(t, "/dashboard"), Active: true, Icon: icon,
	}), "Dashboard")
	mustContain(t, active,
		`<a`, `href="/dashboard"`, `class="goth-sidebar-menu-button"`,
		`data-slot="sidebar-menu-button"`, `aria-current="page"`, `data-active="true"`,
		`class="goth-sidebar-menu-icon"`, `data-slot="sidebar-menu-icon"`,
		`class="goth-sidebar-menu-label"`, `data-slot="sidebar-menu-label"`, "Dashboard")

	inactive := renderKids(t, SidebarMenuButton(SidebarMenuButtonProps{
		URL: mustParseURL(t, "/settings"),
	}), "Settings")
	mustContain(t, inactive, `href="/settings"`, "Settings")
	mustNotContain(t, inactive, `aria-current`, `data-active`)

	button := renderKids(t, SidebarMenuButton(SidebarMenuButtonProps{}), "Action")
	mustContain(t, button, `<button`, `type="button"`, `data-slot="sidebar-menu-button"`, "Action")
	mustNotContain(t, button, `<a `, `href=`)
}

// TestSidebarMenuSubButtonNavigation proves nested links share the link-first rule.
func TestSidebarMenuSubButtonNavigation(t *testing.T) {
	link := renderKids(t, SidebarMenuSubButton(SidebarMenuSubButtonProps{
		URL: mustParseURL(t, "/team/billing"), Active: true,
	}), "Billing")
	mustContain(t, link,
		`<a`, `href="/team/billing"`, `data-slot="sidebar-menu-sub-button"`,
		`aria-current="page"`, `data-active="true"`, "Billing")

	button := renderKids(t, SidebarMenuSubButton(SidebarMenuSubButtonProps{}), "Run")
	mustContain(t, button, `<button`, `type="button"`, "Run")
}

// TestSidebarRailIsRoundTripToggle proves the desktop collapse control is a real
// link (server round-trip) carrying aria-expanded for the CURRENT state.
func TestSidebarRailIsRoundTripToggle(t *testing.T) {
	collapse := renderKids(t, SidebarRail(SidebarRailProps{
		URL: mustParseURL(t, "/app?sidebar=collapsed"), Expanded: true, Label: "Collapse sidebar",
	}), "Collapse")
	mustContain(t, collapse,
		`<a`, `href="/app?sidebar=collapsed"`, `class="goth-sidebar-rail"`,
		`data-slot="sidebar-rail"`, `aria-expanded="true"`, `aria-label="Collapse sidebar"`, "Collapse")

	expand := renderKids(t, SidebarRail(SidebarRailProps{
		URL: mustParseURL(t, "/app?sidebar=expanded"), Expanded: false,
	}), "Expand")
	mustContain(t, expand, `aria-expanded="false"`, `href="/app?sidebar=expanded"`)

	noURL := renderKids(t, SidebarRail(SidebarRailProps{Expanded: true}), "Toggle")
	mustContain(t, noURL, `<button`, `type="button"`, `aria-expanded="true"`)
}

// TestSidebarTriggerAndClose proves the mobile open/close controls bind the
// gothDialog show/hide handlers (reused from the overlay helpers, no new controller).
func TestSidebarTriggerAndClose(t *testing.T) {
	trigger := renderKids(t, SidebarTrigger(SidebarTriggerProps{}), "Menu")
	mustContain(t, trigger,
		`class="goth-sidebar-trigger"`, `type="button"`, `aria-haspopup="dialog"`,
		`x-on:click="show($event)"`, "Menu")

	close := renderKids(t, SidebarClose(SidebarCloseProps{}), "Close")
	mustContain(t, close,
		`class="goth-sidebar-close"`, `type="button"`, `x-on:click="hide($event)"`, "Close")
}

// TestSidebarStructuralParts proves the group/menu/separator parts carry their
// stable slots and semantic elements.
func TestSidebarStructuralParts(t *testing.T) {
	group := renderKids(t, SidebarGroup(SidebarGroupProps{}), "x")
	mustContain(t, group, `class="goth-sidebar-group"`, `data-slot="sidebar-group"`)

	label := renderKids(t, SidebarGroupLabel(SidebarGroupLabelProps{}), "Platform")
	mustContain(t, label, `data-slot="sidebar-group-label"`, "Platform")

	menu := renderKids(t, SidebarMenu(SidebarMenuProps{}), "x")
	mustContain(t, menu, `<ul`, `class="goth-sidebar-menu"`, `data-slot="sidebar-menu"`)

	item := renderKids(t, SidebarMenuItem(SidebarMenuItemProps{}), "x")
	mustContain(t, item, `<li`, `class="goth-sidebar-menu-item"`, `data-slot="sidebar-menu-item"`)

	sub := renderKids(t, SidebarMenuSub(SidebarMenuSubProps{}), "x")
	mustContain(t, sub, `<ul`, `class="goth-sidebar-menu-sub"`, `data-slot="sidebar-menu-sub"`)

	sep := render(t, SidebarSeparator(SidebarSeparatorProps{}))
	mustContain(t, sep, `class="goth-sidebar-separator"`, `role="none"`, `data-slot="sidebar-separator"`)
}

// TestSidebarSideEnum proves the zero-value default and Valid membership.
func TestSidebarSideEnum(t *testing.T) {
	if SidebarLeft.attr() != "left" {
		t.Error("zero-value SidebarSide should map to left")
	}
	if SidebarRight.attr() != "right" {
		t.Error("SidebarRight should map to right")
	}
	if !SidebarLeft.Valid() || !SidebarRight.Valid() {
		t.Error("known SidebarSide values should be Valid")
	}
	if SidebarSide("middle").Valid() {
		t.Error("unknown SidebarSide should not be Valid")
	}
	if SidebarSide("middle").attr() != "left" {
		t.Error("unknown SidebarSide should render the safe left default")
	}
}

// TestSidebarMergeHonorsOwnership proves a caller cannot overwrite a
// behavior-critical owned attribute or drop the compatibility class through the
// Base.Attributes escape hatch.
func TestSidebarMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, Sidebar(SidebarProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"x-data":    "hijack",
			"data-slot": "hijack",
			"class":     "dropped",
		},
	}}), "x")
	mustContain(t, out, `x-data="gothDialog"`, `data-slot="sidebar"`, `goth-sidebar custom-x`)
	mustNotContain(t, out, `hijack`, `dropped`)
}

// TestNoSidebarPrimitiveEmitsInlineStyle proves invariant (a): no Sidebar part emits
// an inline style= in any state.
func TestNoSidebarPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Sidebar(SidebarProps{Open: true, Collapsed: true}), "x"),
		renderKids(t, SidebarTrigger(SidebarTriggerProps{}), "x"),
		renderKids(t, SidebarPanel(SidebarPanelProps{Label: "Main", Side: SidebarRight}), "x"),
		renderKids(t, SidebarHeader(SidebarHeaderProps{}), "x"),
		renderKids(t, SidebarContent(SidebarContentProps{}), "x"),
		renderKids(t, SidebarFooter(SidebarFooterProps{}), "x"),
		renderKids(t, SidebarGroup(SidebarGroupProps{}), "x"),
		renderKids(t, SidebarGroupLabel(SidebarGroupLabelProps{}), "x"),
		renderKids(t, SidebarMenu(SidebarMenuProps{}), "x"),
		renderKids(t, SidebarMenuItem(SidebarMenuItemProps{}), "x"),
		renderKids(t, SidebarMenuButton(SidebarMenuButtonProps{URL: mustParseURL(t, "/x"), Active: true}), "x"),
		renderKids(t, SidebarMenuSub(SidebarMenuSubProps{}), "x"),
		renderKids(t, SidebarMenuSubItem(SidebarMenuSubItemProps{}), "x"),
		renderKids(t, SidebarMenuSubButton(SidebarMenuSubButtonProps{URL: mustParseURL(t, "/x")}), "x"),
		render(t, SidebarSeparator(SidebarSeparatorProps{})),
		renderKids(t, SidebarRail(SidebarRailProps{URL: mustParseURL(t, "/x"), Expanded: true}), "x"),
		renderKids(t, SidebarClose(SidebarCloseProps{}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("sidebar primitive emitted an inline style=: %s", o)
		}
	}
}
