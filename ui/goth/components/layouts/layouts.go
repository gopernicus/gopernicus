// Package layouts holds the opinionated, domain-neutral page/shell compositions
// built from ui/goth primitives (GOTH-7.1). A component arranges primitives and
// selects sensible defaults; it never imports a feature domain, adds a primitive,
// registers a route, or introduces a new Alpine controller. Every component reuses
// the frozen primitives.Base grammar (Class appended after the stable class,
// Attributes funnelled through the shared merge) and emits no server-rendered
// style attribute or inline <style> — layout comes from the stable goth-* classes
// in the kit stylesheet.
//
// Shells (DocumentShell, AppShell, AuthShell) provide the in-<body> page scaffold;
// the enclosing <html>/<head> is still the host's (goth.Document or bespoke) so a
// shell stays Bundle-free. PageHeader and ActionBar are the repeatable header/
// toolbar rows every admin and content page arranges.
package layouts

import (
	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/components/internal/kit"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// DocumentShellProps configures DocumentShell, the centered single-column page
// scaffold for public/content pages: an optional top Header bar, the principal
// content (templ children) inside a centered reading column, and an optional
// Footer. The zero value renders a bare centered column. data-slot hooks:
// document-shell, document-shell-header, document-shell-main,
// document-shell-footer.
type DocumentShellProps struct {
	primitives.Base
	// Header is an optional top bar (nav/brand); nil omits it.
	Header templ.Component
	// Footer is an optional bottom region; nil omits it.
	Footer templ.Component
}

// AppShellProps configures AppShell, the application chrome for admin pages: a
// left Sidebar region (caller passes a Sidebar primitive or nav), an optional top
// Header bar, and the principal content (templ children) in the main region. One
// markup renders both breakpoints via CSS (the sidebar stacks above the main on
// narrow viewports; a static rail beside it on wide ones). data-slot hooks:
// app-shell, app-shell-sidebar, app-shell-header, app-shell-main.
type AppShellProps struct {
	primitives.Base
	// Sidebar is the navigation region (e.g. a primitives.Sidebar). nil omits it.
	Sidebar templ.Component
	// Header is an optional top bar spanning the main column. nil omits it.
	Header templ.Component
}

// AuthShellProps configures AuthShell, the centered card layout every
// authentication page (sign-in/register/verify/reset) shares: an optional Brand
// slot, a Title and Description header, the principal form (templ children) inside
// a Card, and an optional Footer (secondary links). It composes primitives.Card
// centered in the viewport. The zero value renders a bare centered card.
// data-slot hooks: auth-shell, auth-shell-brand, plus the composed card slots.
type AuthShellProps struct {
	primitives.Base
	// Brand is an optional leading logo/wordmark slot above the title. nil omits it.
	Brand templ.Component
	// Title is the page heading (e.g. "Sign in"). Rendered as the card title.
	Title string
	// Description is the supporting line under the title. Empty omits it.
	Description string
	// Footer is an optional secondary-action region under the form (e.g. links).
	// nil omits it.
	Footer templ.Component
}

// PageHeaderProps configures PageHeader, the title/description/actions row every
// admin and content page opens with: an optional Breadcrumb slot above the title,
// the Title (rendered as an h1), an optional Description, and an optional Actions
// slot (a button/action cluster) aligned to the end. The zero value renders an
// empty header. data-slot hooks: page-header, page-header-breadcrumb,
// page-header-heading, page-header-title, page-header-description,
// page-header-actions.
type PageHeaderProps struct {
	primitives.Base
	// Breadcrumb is an optional trail rendered above the heading. nil omits it.
	Breadcrumb templ.Component
	// Title is the page heading (h1). Empty omits the heading text.
	Title string
	// Description is the supporting line under the title. Empty omits it.
	Description string
	// Actions is an optional end-aligned action cluster (e.g. a primary button).
	// nil omits it.
	Actions templ.Component
}

// ActionBarProps configures ActionBar, a horizontal toolbar that groups actions
// with a Start cluster (leading) and an End cluster (trailing) separated by
// flexible space. Both are optional templ.Component slots. It is a native
// role="toolbar" region; the caller supplies its accessible name via Label (or an
// aria-label through Base.Attributes). data-slot hooks: action-bar,
// action-bar-start, action-bar-end.
type ActionBarProps struct {
	primitives.Base
	// Label is the toolbar's accessible name (aria-label). A toolbar grouping
	// controls should be named; empty leaves naming to Base.Attributes.
	Label string
	// Start is the leading action cluster. nil omits it.
	Start templ.Component
	// End is the trailing action cluster. nil omits it.
	End templ.Component
}

func documentShellAttrs(p DocumentShellProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{"data-slot": "document-shell"})
}

func appShellAttrs(p AppShellProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{"data-slot": "app-shell"})
}

func authShellAttrs(p AuthShellProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{"data-slot": "auth-shell"})
}

func pageHeaderAttrs(p PageHeaderProps) templ.Attributes {
	return kit.RootAttrs(p.Base, templ.Attributes{"data-slot": "page-header"})
}

func actionBarAttrs(p ActionBarProps) templ.Attributes {
	owned := templ.Attributes{"data-slot": "action-bar", "role": "toolbar"}
	if p.Label != "" {
		owned["aria-label"] = p.Label
	}
	return kit.RootAttrs(p.Base, owned)
}
