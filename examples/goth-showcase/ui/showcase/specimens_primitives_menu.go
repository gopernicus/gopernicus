package showcase

import (
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// This file holds the showcase specimens for the GOTH-4.4 menu primitives (P38
// Context Menu, P41 Dropdown Menu, P43 Menubar, P44 Navigation Menu). Each
// specimen renders the REAL primitive component so the browser/axe harness
// exercises the actual emitted surface. Dropdown/Context/Menubar run under the
// Interactive profile (the gothMenu controller enhances the closed baseline:
// anchored open, roving, typeahead, submenu, Escape unwind). Dropdown also ships a
// StylesOnly server-open specimen (the readable no-JS baseline). Navigation Menu
// is link-first + native <details>, so it ships under StylesOnly and works with
// no JavaScript at all.

func registerMenuSpecimens(r *Registry) {
	interactive := []struct {
		id, title, prim string
		body            func() string
	}{
		{"dropdown-menu", "Dropdown Menu (P41)", "P41", dropdownMenuSpecimen},
		{"context-menu", "Context Menu (P38)", "P38", contextMenuSpecimen},
		{"menubar", "Menubar (P43)", "P43", menubarSpecimen},
	}
	for _, s := range interactive {
		r.Register(Specimen{
			ID:        "primitive-" + s.id,
			Title:     s.title,
			Section:   SectionPrimitive,
			Primitive: s.prim,
			Profile:   goth.Interactive,
			Body:      s.body,
		})
	}

	static := []struct {
		id, title, prim string
		body            func() string
	}{
		{"dropdown-menu-open", "Dropdown Menu server-open (P41)", "P41", dropdownMenuOpenSpecimen},
		{"navigation-menu", "Navigation Menu (P44)", "P44", navigationMenuSpecimen},
	}
	for _, s := range static {
		r.Register(Specimen{
			ID:        "primitive-" + s.id,
			Title:     s.title,
			Section:   SectionPrimitive,
			Primitive: s.prim,
			Profile:   goth.StylesOnly,
			Body:      s.body,
		})
	}
}

// dropdownMenuBody composes a Dropdown Menu with every item role (menuitem,
// menuitemcheckbox, menuitemradio), a label, separators, and a submenu. When open
// is true the root/content are server-open (the readable no-JS baseline).
func dropdownMenuBody(open bool) string {
	sub := compKids(primitives.DropdownMenuSub(primitives.DropdownMenuSubProps{}),
		compKids(primitives.DropdownMenuSubTrigger(primitives.DropdownMenuSubTriggerProps{Base: primitives.Base{ID: "dm-share"}}), "Share")+
			compKids(primitives.DropdownMenuSubContent(primitives.DropdownMenuSubContentProps{}),
				compKids(primitives.DropdownMenuItem(primitives.DropdownMenuItemProps{Base: primitives.Base{ID: "dm-email"}, Value: "email"}), "Email link")+
					compKids(primitives.DropdownMenuItem(primitives.DropdownMenuItemProps{Value: "copy"}), "Copy link")))

	content := compKids(primitives.DropdownMenuLabel(primitives.DropdownMenuLabelProps{}), "Actions") +
		compKids(primitives.DropdownMenuItem(primitives.DropdownMenuItemProps{Base: primitives.Base{ID: "dm-new"}, Value: "new"}), "New file") +
		compKids(primitives.DropdownMenuItem(primitives.DropdownMenuItemProps{Value: "open"}), "Open recent") +
		compKids(primitives.DropdownMenuSeparator(primitives.DropdownMenuSeparatorProps{}), "") +
		compKids(primitives.DropdownMenuCheckboxItem(primitives.DropdownMenuCheckboxItemProps{Value: "grid", Checked: true}), "Show grid") +
		compKids(primitives.DropdownMenuRadioItem(primitives.DropdownMenuRadioItemProps{Value: "sm", Checked: true}), "Small") +
		compKids(primitives.DropdownMenuRadioItem(primitives.DropdownMenuRadioItemProps{Value: "lg"}), "Large") +
		compKids(primitives.DropdownMenuSeparator(primitives.DropdownMenuSeparatorProps{}), "") +
		sub +
		compKids(primitives.DropdownMenuItem(primitives.DropdownMenuItemProps{Value: "delete", Disabled: true}), "Delete")

	menu := compKids(primitives.DropdownMenu(primitives.DropdownMenuProps{Open: open}),
		compKids(primitives.DropdownMenuTrigger(primitives.DropdownMenuTriggerProps{Base: primitives.Base{ID: "dm-trigger", Class: "goth-button"}}), "Actions")+
			compKids(primitives.DropdownMenuContent(primitives.DropdownMenuContentProps{Open: open}), content))

	return `<section data-slot="dropdown-menu-specimen">` + menu + `</section>`
}

func dropdownMenuSpecimen() string     { return page("Dropdown Menu", dropdownMenuBody(false)) }
func dropdownMenuOpenSpecimen() string { return page("Dropdown Menu (server-open)", dropdownMenuBody(true)) }

// contextMenuBody composes a Context Menu over a right-clickable region.
func contextMenuBody() string {
	sub := compKids(primitives.ContextMenuSub(primitives.ContextMenuSubProps{}),
		compKids(primitives.ContextMenuSubTrigger(primitives.ContextMenuSubTriggerProps{Base: primitives.Base{ID: "cm-share"}}), "Share")+
			compKids(primitives.ContextMenuSubContent(primitives.ContextMenuSubContentProps{}),
				compKids(primitives.ContextMenuItem(primitives.ContextMenuItemProps{Base: primitives.Base{ID: "cm-email"}, Value: "email"}), "Email")))

	content := compKids(primitives.ContextMenuItem(primitives.ContextMenuItemProps{Base: primitives.Base{ID: "cm-copy"}, Value: "copy"}), "Copy") +
		compKids(primitives.ContextMenuItem(primitives.ContextMenuItemProps{Value: "paste"}), "Paste") +
		compKids(primitives.ContextMenuSeparator(primitives.ContextMenuSeparatorProps{}), "") +
		sub

	menu := compKids(primitives.ContextMenu(primitives.ContextMenuProps{}),
		compKids(primitives.ContextMenuTrigger(primitives.ContextMenuTriggerProps{Base: primitives.Base{ID: "context-region"}}), "Right-click (or press the Menu key) in this area")+
			compKids(primitives.ContextMenuContent(primitives.ContextMenuContentProps{}), content))

	return `<section data-slot="context-menu-specimen">` + menu + `</section>`
}

func contextMenuSpecimen() string { return page("Context Menu", contextMenuBody()) }

// menubarBody composes a three-menu application menubar.
func menubarBody() string {
	fileMenu := compKids(primitives.MenubarMenu(primitives.MenubarMenuProps{}),
		compKids(primitives.MenubarTrigger(primitives.MenubarTriggerProps{Base: primitives.Base{ID: "mb-file"}}), "File")+
			compKids(primitives.MenubarContent(primitives.MenubarContentProps{}),
				compKids(primitives.MenubarItem(primitives.MenubarItemProps{Base: primitives.Base{ID: "mb-file-new"}, Value: "new"}), "New")+
					compKids(primitives.MenubarItem(primitives.MenubarItemProps{Value: "open"}), "Open")+
					compKids(primitives.MenubarSeparator(primitives.MenubarSeparatorProps{}), "")+
					compKids(primitives.MenubarItem(primitives.MenubarItemProps{Value: "exit"}), "Exit")))

	editMenu := compKids(primitives.MenubarMenu(primitives.MenubarMenuProps{}),
		compKids(primitives.MenubarTrigger(primitives.MenubarTriggerProps{Base: primitives.Base{ID: "mb-edit"}}), "Edit")+
			compKids(primitives.MenubarContent(primitives.MenubarContentProps{}),
				compKids(primitives.MenubarItem(primitives.MenubarItemProps{Base: primitives.Base{ID: "mb-edit-undo"}, Value: "undo"}), "Undo")+
					compKids(primitives.MenubarItem(primitives.MenubarItemProps{Value: "redo"}), "Redo")))

	viewMenu := compKids(primitives.MenubarMenu(primitives.MenubarMenuProps{}),
		compKids(primitives.MenubarTrigger(primitives.MenubarTriggerProps{Base: primitives.Base{ID: "mb-view"}}), "View")+
			compKids(primitives.MenubarContent(primitives.MenubarContentProps{}),
				compKids(primitives.MenubarCheckboxItem(primitives.MenubarCheckboxItemProps{Value: "sidebar", Checked: true}), "Sidebar")+
					compKids(primitives.MenubarItem(primitives.MenubarItemProps{Value: "zoom"}), "Zoom in")))

	bar := compKids(primitives.Menubar(primitives.MenubarProps{Label: "Application"}), fileMenu+editMenu+viewMenu)
	return `<section data-slot="menubar-specimen">` + bar + `</section>`
}

func menubarSpecimen() string { return page("Menubar", menubarBody()) }

// navigationMenuBody composes a link-first navigation menu with a native <details>
// disclosure. Everything works with no JavaScript.
func navigationMenuBody() string {
	products := compKids(primitives.NavigationMenuSub(primitives.NavigationMenuSubProps{}),
		compKids(primitives.NavigationMenuTrigger(primitives.NavigationMenuTriggerProps{Base: primitives.Base{ID: "nav-products"}}), "Products")+
			compKids(primitives.NavigationMenuContent(primitives.NavigationMenuContentProps{}),
				compKids(primitives.NavigationMenuLink(primitives.NavigationMenuLinkProps{Base: primitives.Base{ID: "nav-analytics"}, URL: mustURL("/products/analytics")}), "Analytics")+
					compKids(primitives.NavigationMenuLink(primitives.NavigationMenuLinkProps{URL: mustURL("/products/billing")}), "Billing")))

	list := compKids(primitives.NavigationMenuItem(primitives.NavigationMenuItemProps{}),
		compKids(primitives.NavigationMenuLink(primitives.NavigationMenuLinkProps{Base: primitives.Base{ID: "nav-home"}, URL: mustURL("/"), Active: true}), "Home")) +
		compKids(primitives.NavigationMenuItem(primitives.NavigationMenuItemProps{}),
			compKids(primitives.NavigationMenuLink(primitives.NavigationMenuLinkProps{URL: mustURL("/docs")}), "Docs")) +
		compKids(primitives.NavigationMenuItem(primitives.NavigationMenuItemProps{}), products)

	nav := compKids(primitives.NavigationMenu(primitives.NavigationMenuProps{Label: "Primary"}),
		compKids(primitives.NavigationMenuList(primitives.NavigationMenuListProps{}), list))

	return `<section data-slot="navigation-menu-specimen">` + nav + `</section>`
}

func navigationMenuSpecimen() string { return page("Navigation Menu", navigationMenuBody()) }
