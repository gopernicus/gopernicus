package showcase

import (
	"sort"

	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// Section names for specimen grouping. Ordered by Sections().
const (
	SectionProfile   = "profile"
	SectionTheme     = "theme"
	SectionHTMX      = "htmx"
	SectionMechanics = "mechanics"
	SectionPrimitive = "primitive"
	SectionComponent = "component"
)

var sectionOrder = map[string]int{
	SectionProfile:   0,
	SectionTheme:     1,
	SectionHTMX:      2,
	SectionMechanics: 3,
	SectionPrimitive: 4,
	SectionComponent: 5,
}

// Specimen is one showcase page. The registry is the single source of truth: it
// drives both the HTTP routes and the completeness test, so a primitive declared
// implemented (Primitive set) without a registered specimen fails the Go suite.
type Specimen struct {
	// ID is the URL slug and the registry key (unique).
	ID string
	// Title is the human label.
	Title string
	// Section groups the specimen (profile/theme/htmx/primitive).
	Section string
	// Primitive is the catalog id (P01..P64) a primitive specimen proves; empty
	// for infrastructure specimens.
	Primitive string
	// Component is the GOTH-7.1 component key (e.g. "page-header") a component
	// specimen proves; empty for non-component specimens. It drives the component
	// completeness test the same way Primitive drives the primitive one.
	Component string
	// Profile selects the bundle asset set.
	Profile goth.Profile
	// Appearance / Dir drive the document element attributes.
	Appearance theme.Appearance
	Dir        theme.Direction
	// AllowConnect adds connect-src 'self' to the CSP for HTMX pages (host concern).
	AllowConnect bool
	// UseThemedBundle renders against the host-theme bundle, which links a real
	// host-authored theme stylesheet after the kit stylesheet (the WordPress model)
	// under a strict style-src 'self'.
	UseThemedBundle bool
	// Body returns the host-authored specimen markup.
	Body func() string
}

// Path is the specimen's route.
func (s Specimen) Path() string { return "/specimen/" + s.ID }

// Registry holds every registered specimen.
type Registry struct {
	specimens []Specimen
	byID      map[string]Specimen
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]Specimen{}}
}

// Register adds a specimen. It panics on a duplicate id — a programming error the
// host must fix before boot.
func (r *Registry) Register(s Specimen) {
	if _, dup := r.byID[s.ID]; dup {
		panic("showcase: duplicate specimen id " + s.ID)
	}
	r.byID[s.ID] = s
	r.specimens = append(r.specimens, s)
}

// All returns every specimen in a deterministic (section, id) order.
func (r *Registry) All() []Specimen {
	out := append([]Specimen{}, r.specimens...)
	sort.Slice(out, func(i, j int) bool {
		si, sj := sectionOrder[out[i].Section], sectionOrder[out[j].Section]
		if si != sj {
			return si < sj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Sections returns the distinct sections present, in canonical order.
func (r *Registry) Sections() []string {
	seen := map[string]bool{}
	for _, s := range r.specimens {
		seen[s.Section] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return sectionOrder[out[i]] < sectionOrder[out[j]] })
	return out
}

// BySection returns the specimens in a section, id-ordered.
func (r *Registry) BySection(section string) []Specimen {
	var out []Specimen
	for _, s := range r.specimens {
		if s.Section == section {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup returns a specimen by id.
func (r *Registry) Lookup(id string) (Specimen, bool) {
	s, ok := r.byID[id]
	return s, ok
}

// PrimitiveSpecimens returns the catalog ids that have a registered specimen.
func (r *Registry) PrimitiveSpecimens() map[string]bool {
	out := map[string]bool{}
	for _, s := range r.specimens {
		if s.Primitive != "" {
			out[s.Primitive] = true
		}
	}
	return out
}

// ComponentSpecimens returns the GOTH-7.1 component keys that have a registered
// specimen.
func (r *Registry) ComponentSpecimens() map[string]bool {
	out := map[string]bool{}
	for _, s := range r.specimens {
		if s.Component != "" {
			out[s.Component] = true
		}
	}
	return out
}

// ImplementedPrimitives is the set of catalog ids whose ui/goth implementation
// has landed. Each Phase 2–6 primitive task appends its id here AND registers a
// specimen in DefaultRegistry; TestEveryImplementedPrimitiveHasSpecimen then
// fails automatically if a specimen is missing, so a primitive can never ship
// without a showcase page. GOTH-2.1 adds the twelve content/status primitives;
// GOTH-2.2 adds the six action/navigation primitives; GOTH-3.1 adds the three
// disclosure primitives (P27 Accordion, P29 Collapsible, P34 Tabs).
var ImplementedPrimitives = []string{
	"P01", // Alert
	"P02", // Aspect Ratio
	"P03", // Avatar
	"P04", // Badge
	"P05", // Breadcrumb
	"P06", // Button
	"P07", // Button Group
	"P08", // Card
	"P09", // Direction
	"P10", // Empty
	"P11", // Field
	"P12", // Input
	"P13", // Input Group
	"P14", // Item
	"P15", // Kbd
	"P16", // Label
	"P17", // Marker
	"P18", // Native Select
	"P19", // Pagination
	"P20", // Progress
	"P21", // Separator
	"P22", // Skeleton
	"P23", // Spinner
	"P24", // Table
	"P25", // Textarea
	"P26", // Typography
	"P27", // Accordion
	"P29", // Collapsible
	"P34", // Tabs
	"P28", // Checkbox
	"P31", // Radio Group
	"P32", // Slider
	"P33", // Switch
	"P30", // Input OTP
	"P35", // Toggle
	"P36", // Toggle Group
	"P37", // Alert Dialog
	"P39", // Dialog
	"P40", // Drawer
	"P47", // Sheet
	"P42", // Hover Card
	"P45", // Popover
	"P46", // Select
	"P48", // Tooltip
	"P38", // Context Menu
	"P41", // Dropdown Menu
	"P43", // Menubar
	"P44", // Navigation Menu
	"P49", // Calendar
	"P53", // Date Picker
	"P50", // Combobox
	"P51", // Command
	"P52", // Data Table
	"P54", // Sidebar
	"P55", // Attachment
	"P56", // Bubble
	"P59", // Message
	"P60", // Message Scroller
	"P57", // Carousel
	"P62", // Scroll Area
	"P61", // Resizable
	"P58", // Chart
	"P64", // Toast
	"P63", // Sonner
}

// ImplementedComponents is the set of GOTH-7.1 component keys whose ui/goth
// composition has landed. Each has at least one registered showcase specimen (the
// specimen consumer) plus a documented adopter need (authentication GOTH-7.2 /
// CMS GOTH-7.3); TestEveryImplementedComponentHasSpecimen fails automatically if a
// component ships without a specimen.
var ImplementedComponents = []string{
	"document-shell",
	"app-shell",
	"auth-shell",
	"page-header",
	"action-bar",
	"form-field",
	"form-section",
	"error-summary",
	"form-actions",
	"empty-panel",
	"loading-panel",
	"error-panel",
	"confirm-dialog",
	"table-toolbar",
}

// DefaultRegistry returns the registry with every registered specimen.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	registerProfileSpecimens(r)
	registerThemeSpecimens(r)
	registerHTMXSpecimen(r)
	registerOverlayMechanicsSpecimens(r)
	registerPrimitiveSpecimens(r)
	registerDisclosureSpecimens(r)
	registerSelectionSpecimens(r)
	registerCompactSpecimens(r)
	registerOverlaySpecimens(r)
	registerAnchoredSpecimens(r)
	registerMenuSpecimens(r)
	registerDateSpecimens(r)
	registerPaletteSpecimens(r)
	registerDataSpecimens(r)
	registerSidebarSpecimens(r)
	registerAttachmentSpecimens(r)
	registerMessagingSpecimens(r)
	registerSpatialSpecimens(r)
	registerChartSpecimens(r)
	registerNotificationSpecimens(r)
	registerComponentSpecimens(r)
	return r
}
