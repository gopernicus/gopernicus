package showcase

import (
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// registerOverlayMechanicsSpecimens registers the GOTH-4.1 shared-mechanics test
// fixtures. These are infrastructure specimens (no catalog Primitive id): they
// exercise the frozen overlay/menu mechanics — nested-aware dismiss, focus
// trap/restore, scroll lock, background inert, anchored placement + collision,
// roving focus, typeahead, and submenu hierarchy — through the existing
// gothDialog / gothMenu controllers BEFORE any Phase-4 primitive wrapper is built.
// They run under the Interactive profile so the browser harness drives the real
// Alpine CSP runtime. The markup is host-authored raw HTML (like interactiveBody);
// no ui/goth primitive exists for these overlays yet.
func registerOverlayMechanicsSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:      "mechanics-dialog",
		Title:   "Dialog mechanics (trap/restore, nested dismiss, scroll lock, inert)",
		Section: SectionMechanics,
		Profile: goth.Interactive,
		Body:    dialogMechanicsBody,
	})
	r.Register(Specimen{
		ID:      "mechanics-menu",
		Title:   "Menu mechanics (anchored placement, roving, typeahead, submenu)",
		Section: SectionMechanics,
		Profile: goth.Interactive,
		Body:    menuMechanicsBody,
	})
	// An RTL menu fixture proves the submenu opens toward the correct side and the
	// submenu open/close keys mirror under dir=rtl.
	r.Register(Specimen{
		ID:      "mechanics-menu-rtl",
		Title:   "Menu mechanics RTL",
		Section: SectionMechanics,
		Profile: goth.Interactive,
		Dir:     theme.DirectionRTL,
		Body:    menuMechanicsBody,
	})
}

// dialogMechanicsBody renders a modal dialog whose panel contains a nested modal
// dialog, so the browser harness can prove: focus trap + restore, nested-aware
// Escape/outside dismissal (inner closes first), background scroll lock, and
// background inert — all from the shared mechanics via gothDialog.
func dialogMechanicsBody() string {
	nested := `<div class="goth-dialog" data-slot="nested-dialog" data-state="closed" x-data="gothDialog">` +
		`<button type="button" class="goth-button" data-slot="trigger" id="nested-trigger" x-on:click="show($event)">Open nested dialog</button>` +
		`<div class="goth-overlay" data-slot="overlay">` +
		`<div class="goth-overlay-scrim" data-slot="scrim"></div>` +
		`<div class="goth-overlay-panel" data-slot="content" role="dialog" aria-modal="true" aria-labelledby="nested-title">` +
		`<h3 id="nested-title">Nested dialog</h3>` +
		`<p>Escape closes only this nested dialog first, then its parent.</p>` +
		`<input type="text" class="goth-input" data-slot="nested-input" id="nested-input" aria-label="Nested field" />` +
		`<button type="button" class="goth-button" data-slot="nested-close" id="nested-close" x-on:click="hide($event)">Close nested</button>` +
		`</div></div></div>`

	dialog := `<div class="goth-dialog" data-slot="dialog" data-state="closed" x-data="gothDialog">` +
		`<button type="button" class="goth-button" data-slot="trigger" id="dialog-trigger" x-on:click="show($event)">Open dialog</button>` +
		`<div class="goth-overlay" data-slot="overlay">` +
		`<div class="goth-overlay-scrim" data-slot="scrim"></div>` +
		`<div class="goth-overlay-panel" data-slot="content" role="dialog" aria-modal="true" aria-labelledby="dialog-title">` +
		`<h2 id="dialog-title">Outer dialog</h2>` +
		`<p>Focus is trapped in this panel. Escape or a scrim click dismisses it, and focus returns to the trigger.</p>` +
		`<input type="text" class="goth-input" data-slot="dialog-input" id="dialog-input" aria-label="Sample field" />` +
		nested +
		`<button type="button" class="goth-button" data-slot="dialog-close" id="dialog-close" x-on:click="hide($event)">Close</button>` +
		`</div></div></div>`

	return page("Dialog mechanics", `<section data-slot="dialog-mechanics">`+
		`<p data-slot="scroll-probe">Long page content below the dialog proves scroll lock.</p>`+
		dialog+
		`<div data-slot="tall-filler" aria-hidden="true"></div>`+
		`</section>`)
}

// menuMechanicsBody renders a dropdown menu with a submenu, so the browser
// harness can prove: anchored placement under the trigger with viewport collision
// flipping, roving focus (arrow/Home/End) over the items, typeahead, submenu
// hierarchy (ArrowRight opens / ArrowLeft closes, RTL-mirrored), and nested-aware
// dismissal — all from the shared mechanics via gothMenu.
func menuMechanicsBody() string {
	item := func(value, label string) string {
		return `<button type="button" class="goth-menu-item" role="menuitem" data-slot="item" data-value="` + value + `" x-on:click="select($event)">` + label + `</button>`
	}
	submenu := `<div class="goth-submenu-root" data-slot="submenu-root">` +
		`<button type="button" class="goth-menu-item" role="menuitem" data-slot="submenu-trigger" id="submenu-trigger" aria-haspopup="menu" aria-expanded="false">Share</button>` +
		`<div class="goth-floating" data-slot="submenu" role="menu" data-state="closed">` +
		`<button type="button" class="goth-menu-item" role="menuitem" data-slot="item" data-value="email" id="submenu-email">Email link</button>` +
		`<button type="button" class="goth-menu-item" role="menuitem" data-slot="item" data-value="copy">Copy link</button>` +
		`</div></div>`

	menu := `<div class="goth-menu" data-slot="menu" data-state="closed" x-data="gothMenu">` +
		`<button type="button" class="goth-button" data-slot="trigger" id="menu-trigger" aria-haspopup="menu" aria-expanded="false" x-on:click="toggle($event)">Open menu</button>` +
		`<div class="goth-floating" data-slot="content" role="menu" data-state="closed" x-on:keydown="onKeydown($event)">` +
		item("new", "New file") +
		item("open", "Open recent") +
		submenu +
		item("settings", "Settings") +
		item("delete", "Delete") +
		`</div></div>`

	return page("Menu mechanics", `<section data-slot="menu-mechanics">`+menu+`</section>`)
}
