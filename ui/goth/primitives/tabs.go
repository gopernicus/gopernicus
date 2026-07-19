package primitives

import "github.com/a-h/templ"

// TabsOrientation is the tab-list layout/traversal axis. The zero value is
// horizontal.
type TabsOrientation string

const (
	// TabsHorizontal is the zero value: a horizontal tab list traversed with
	// ArrowLeft/ArrowRight.
	TabsHorizontal TabsOrientation = ""
	// TabsVertical stacks tabs and traverses with ArrowUp/ArrowDown.
	TabsVertical TabsOrientation = "vertical"
)

// Valid reports whether o is a known TabsOrientation.
func (o TabsOrientation) Valid() bool {
	switch o {
	case TabsHorizontal, TabsVertical:
		return true
	default:
		return false
	}
}

func (o TabsOrientation) attr() string {
	if o == TabsVertical {
		return "vertical"
	}
	return "horizontal"
}

// TabsActivation selects automatic (activate on arrow-key focus) versus manual
// (activate only on Enter/Space/click) activation. The zero value is automatic.
type TabsActivation string

const (
	// TabsAutomatic is the zero value: moving focus with the arrow keys also
	// activates the focused tab.
	TabsAutomatic TabsActivation = ""
	// TabsManual moves focus without activating; Enter/Space or click activates.
	TabsManual TabsActivation = "manual"
)

// Valid reports whether a is a known TabsActivation.
func (a TabsActivation) Valid() bool {
	switch a {
	case TabsAutomatic, TabsManual:
		return true
	default:
		return false
	}
}

func (a TabsActivation) attr() string {
	if a == TabsManual {
		return "manual"
	}
	return "automatic"
}

// TabsProps configures the Tabs container (P34, family F4). The caller composes a
// TabsList of TabsTrigger buttons and one TabsContent panel per tab. The SERVER
// owns which tab is active: the active panel is rendered visible and the rest
// hidden, so the active tab's content is fully readable with no JavaScript (the
// baseline). The gothTabs controller enhances the list with roving focus and
// client-side tab switching. ARIA linkage is caller-passed via Base.ID: each
// TabsTrigger sets its own ID and Controls (the panel id); each TabsContent sets
// its own ID and Labelledby (the trigger id). data-slot hooks: tabs, tab-list,
// tab, tab-panel.
type TabsProps struct {
	Base
	Orientation TabsOrientation
	Activation  TabsActivation
}

// TabsListProps configures the TabsList (role=tablist).
type TabsListProps struct {
	Base
	Orientation TabsOrientation
}

// TabsTriggerProps configures one TabsTrigger (role=tab button).
type TabsTriggerProps struct {
	Base
	// Value identifies the tab and matches its TabsContent Value.
	Value string
	// Active marks the server-selected tab (aria-selected, data-state=active,
	// the single roving tab stop).
	Active bool
	// Controls is the id of the TabsContent panel this tab controls (aria-controls).
	Controls string
}

// TabsContentProps configures one TabsContent panel (role=tabpanel).
type TabsContentProps struct {
	Base
	// Value matches the controlling TabsTrigger Value.
	Value string
	// Active marks the server-selected panel; inactive panels render hidden.
	Active bool
	// Labelledby is the id of the controlling TabsTrigger (aria-labelledby).
	Labelledby string
}

func tabsClass(p TabsProps) string { return classNames("goth-tabs", p.Class) }

func tabsAttrs(p TabsProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":        "tabs",
		"data-orientation": p.Orientation.attr(),
		"data-activation":  p.Activation.attr(),
		"x-data":           "gothTabs",
	})
}

func tabsListClass(p TabsListProps) string { return classNames("goth-tabs-list", p.Class) }

func tabsListAttrs(p TabsListProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":        "tab-list",
		"role":             "tablist",
		"aria-orientation": p.Orientation.attr(),
		"x-on:keydown":     "onKeydown($event)",
	})
}

func tabsTriggerClass(p TabsTriggerProps) string { return classNames("goth-tabs-trigger", p.Class) }

func tabsTriggerAttrs(p TabsTriggerProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":     "tab",
		"role":          "tab",
		"type":          "button",
		"data-value":    p.Value,
		"data-state":    activeState(p.Active),
		"aria-selected": boolAttr(p.Active),
		"tabindex":      rovingTabindex(p.Active),
		"x-on:click":    "activate($event)",
	}
	if p.Controls != "" {
		owned["aria-controls"] = p.Controls
	}
	return ownedAttrs(p.Base, owned)
}

func tabsContentClass(p TabsContentProps) string { return classNames("goth-tabs-content", p.Class) }

func tabsContentAttrs(p TabsContentProps) templ.Attributes {
	owned := templ.Attributes{
		"data-slot":  "tab-panel",
		"role":       "tabpanel",
		"data-value": p.Value,
		"tabindex":   "0",
		"hidden":     !p.Active,
	}
	if p.Labelledby != "" {
		owned["aria-labelledby"] = p.Labelledby
	}
	return ownedAttrs(p.Base, owned)
}

// activeState maps a boolean active flag to the frozen data-state selection value.
func activeState(active bool) string {
	if active {
		return "active"
	}
	return "inactive"
}

// boolAttr renders a "true"/"false" string for aria-* attributes that take a
// literal boolean token (aria-selected, aria-expanded).
func boolAttr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// rovingTabindex gives the active roving item the single "0" tab stop and every
// other item "-1", so the group is one Tab stop and arrows move within it.
func rovingTabindex(active bool) string {
	if active {
		return "0"
	}
	return "-1"
}
