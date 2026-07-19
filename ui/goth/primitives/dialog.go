package primitives

import "github.com/a-h/templ"

// DialogProps configures the Dialog container (P39, family F4). No-JS baseline:
// the root's server-rendered data-state governs visibility — a Dialog rendered
// Open shows its overlay/panel with no JavaScript (the content is in the DOM and
// displayed by CSS), and a closed Dialog shows only its trigger. The gothDialog
// controller enhances the baseline with focus trap/restore, nested-aware
// Escape/outside dismissal, background scroll lock, and background inert (the
// frozen GOTH-4.1 overlay mechanics). A non-modal Dialog (NonModal) skips scroll
// lock/inert and the controller asserts no aria-modal. ARIA linkage is
// caller-passed via Base.ID: DialogContent.Labelledby points at the DialogTitle
// id and DialogContent.Describedby at the DialogDescription id. data-slot hooks:
// dialog, trigger, overlay, scrim, content, header, footer, title, description,
// close.
type DialogProps struct {
	Base
	// Open is the server-rendered initial state. Zero value is closed. When true
	// the overlay renders visible (readable without JavaScript).
	Open bool
	// NonModal opts out of modal behavior: the controller applies no background
	// scroll lock or inert and asserts no aria-modal. Zero value is modal.
	NonModal bool
}

// DialogTriggerProps configures the DialogTrigger button that opens the dialog.
type DialogTriggerProps struct{ Base }

// DialogContentProps configures the DialogContent overlay+panel. The panel is the
// role=dialog region; Base.ID/Class/Attributes apply to it.
type DialogContentProps struct {
	Base
	// Labelledby is the DialogTitle id (aria-labelledby).
	Labelledby string
	// Describedby is the DialogDescription id (aria-describedby).
	Describedby string
}

// DialogHeaderProps configures the DialogHeader layout region.
type DialogHeaderProps struct{ Base }

// DialogFooterProps configures the DialogFooter action region.
type DialogFooterProps struct{ Base }

// DialogTitleProps configures the DialogTitle (h2); set Base.ID and reference it
// from DialogContent.Labelledby.
type DialogTitleProps struct{ Base }

// DialogDescriptionProps configures the DialogDescription (p); set Base.ID and
// reference it from DialogContent.Describedby.
type DialogDescriptionProps struct{ Base }

// DialogCloseProps configures a DialogClose button that dismisses the dialog.
type DialogCloseProps struct{ Base }

func dialogClass(p DialogProps) string { return classNames("goth-dialog", p.Class) }

func dialogAttrs(p DialogProps) templ.Attributes {
	return overlayRootAttrs(p.Base, "dialog", p.Open, !p.NonModal, true)
}

func dialogContentAttrs(p DialogContentProps) templ.Attributes {
	return overlayPanelAttrs(p.Base, "dialog", p.Labelledby, p.Describedby)
}
