package primitives

import "github.com/a-h/templ"

// AlertDialogProps configures the Alert Dialog container (P37, family F4). An
// alert dialog is a modal that interrupts for a labelled destructive decision, so
// it is ALWAYS modal (background scroll lock + inert) and — unlike Dialog — an
// outside/scrim press does NOT dismiss it (data-dismiss-outside="false"): the user
// must choose AlertDialogCancel or AlertDialogAction. Escape still cancels (APG
// alertdialog). No-JS baseline: the server-owned data-state governs visibility, so
// a server-open alert dialog is readable without JavaScript. ARIA: the panel is
// role="alertdialog" and both a name (AlertDialogTitle via Labelledby) and a
// description (AlertDialogDescription via Describedby) are expected. data-slot
// hooks: alert-dialog, trigger, overlay, scrim, content, header, footer, title,
// description, action, cancel.
type AlertDialogProps struct {
	Base
	// Open is the server-rendered initial state. Zero value is closed.
	Open bool
}

// AlertDialogTriggerProps configures the trigger button that opens the dialog.
type AlertDialogTriggerProps struct{ Base }

// AlertDialogContentProps configures the overlay+panel (role=alertdialog).
type AlertDialogContentProps struct {
	Base
	// Labelledby is the AlertDialogTitle id (aria-labelledby).
	Labelledby string
	// Describedby is the AlertDialogDescription id (aria-describedby).
	Describedby string
}

// AlertDialogHeaderProps configures the header layout region.
type AlertDialogHeaderProps struct{ Base }

// AlertDialogFooterProps configures the footer action region.
type AlertDialogFooterProps struct{ Base }

// AlertDialogTitleProps configures the title (h2); set Base.ID and reference it
// from AlertDialogContent.Labelledby.
type AlertDialogTitleProps struct{ Base }

// AlertDialogDescriptionProps configures the description (p); set Base.ID and
// reference it from AlertDialogContent.Describedby.
type AlertDialogDescriptionProps struct{ Base }

// AlertDialogActionProps configures the confirm/action button. It closes the
// dialog on click; to perform a server mutation, place it in a form or add hx-*
// through Base.Attributes (its default form-button type submits). Style it
// destructive with Base.Class.
type AlertDialogActionProps struct{ Base }

// AlertDialogCancelProps configures the cancel button; it dismisses the dialog
// with no side effect.
type AlertDialogCancelProps struct{ Base }

func alertDialogClass(p AlertDialogProps) string { return classNames("goth-alert-dialog", p.Class) }

func alertDialogAttrs(p AlertDialogProps) templ.Attributes {
	// Always modal; outside/scrim press never dismisses (dismissOutside=false).
	return overlayRootAttrs(p.Base, "alert-dialog", p.Open, true, false)
}

func alertDialogContentAttrs(p AlertDialogContentProps) templ.Attributes {
	return overlayPanelAttrs(p.Base, "alertdialog", p.Labelledby, p.Describedby)
}

func alertDialogActionAttrs(p AlertDialogActionProps) templ.Attributes {
	// No forced type: standalone it is a plain button whose x-on closes the dialog;
	// inside a form it submits (default button type) and then closes.
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "action",
		"x-on:click": "hide($event)",
	})
}

func alertDialogCancelAttrs(p AlertDialogCancelProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "cancel",
		"type":       "button",
		"x-on:click": "hide($event)",
	})
}
