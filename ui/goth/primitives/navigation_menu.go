package primitives

import "github.com/a-h/templ"

// Navigation Menu (P44, family F4). Unlike the action menus (P38/P41/P43),
// navigation is link-first: every destination is a real server-rendered <a> that
// works with NO JavaScript, and a menu of sub-destinations is a native
// <details>/<summary> disclosure that also opens, closes, and is keyboard-operable
// with no JavaScript. This is the recorded F4-native precedent (README §7/catalog
// family note): a sufficient native baseline satisfies the F4 row with no
// controller bound — the same call Select (P46) and Popover (P45) made in
// GOTH-4.3. There is no gothMenu here: menu items are links, not roving
// menuitems, so the action-menu controller would be semantically wrong; the
// native disclosure is honest and complete. data-slot hooks: navigation-menu,
// navigation-menu-list, navigation-menu-item, navigation-menu-link,
// navigation-menu-sub, navigation-menu-trigger, navigation-menu-content.

// NavigationMenuProps configures the <nav> landmark.
type NavigationMenuProps struct {
	Base
	// Label is the navigation landmark's accessible name (aria-label). Recommended
	// so assistive technology distinguishes multiple navs on a page.
	Label string
}

// NavigationMenuListProps configures the <ul> of items.
type NavigationMenuListProps struct{ Base }

// NavigationMenuItemProps configures a single <li> item (a link or a disclosure).
type NavigationMenuItemProps struct{ Base }

// NavigationMenuLinkProps configures a real navigation link.
type NavigationMenuLinkProps struct {
	Base
	// URL is the destination. A NavigationMenuLink always renders an <a>; an empty
	// URL renders a non-navigating anchor (rarely wanted — set a URL).
	URL URL
	// Active marks the current page (aria-current="page").
	Active bool
}

// NavigationMenuSubProps configures a native <details> disclosure holding a
// trigger and a panel of links.
type NavigationMenuSubProps struct {
	Base
	// Open renders the disclosure server-open (readable with no JavaScript). Zero
	// value is collapsed; a <summary> click toggles it natively.
	Open bool
}

// NavigationMenuTriggerProps configures the <summary> that toggles the disclosure.
type NavigationMenuTriggerProps struct{ Base }

// NavigationMenuContentProps configures the revealed panel of links.
type NavigationMenuContentProps struct{ Base }

func navigationMenuAttrs(p NavigationMenuProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "navigation-menu"}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return ownedAttrs(p.Base, owned)
}

// navigationMenuLinkAttrs carries the slot + active-state hooks. The href itself
// is rendered natively in the templ (href={ URL.SafeURL() }) so a typed safe URL
// is emitted, never smuggled through the generic attribute spread.
func navigationMenuLinkAttrs(p NavigationMenuLinkProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "navigation-menu-link"}
	if p.Active {
		owned["aria-current"] = "page"
		owned["data-active"] = "true"
	}
	return ownedAttrs(p.Base, owned)
}

func navigationMenuSubAttrs(p NavigationMenuSubProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "navigation-menu-sub"}
	if p.Open {
		owned["open"] = true
	}
	return ownedAttrs(p.Base, owned)
}

func navigationMenuListAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": "navigation-menu-list"})
}

func navigationMenuItemAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": "navigation-menu-item"})
}

func navigationMenuTriggerAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": "navigation-menu-trigger"})
}

func navigationMenuContentAttrs(b Base) templ.Attributes {
	return ownedAttrs(b, templ.Attributes{"data-slot": "navigation-menu-content"})
}
