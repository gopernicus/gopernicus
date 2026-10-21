package showcase

import (
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// registerOverlaySpecimens registers the GOTH-4.2 modal/panel primitives (P37
// Alert Dialog, P39 Dialog, P40 Drawer, P47 Sheet). Each renders the REAL
// primitive component (not hand-written markup) so the browser/axe harness
// exercises the actual emitted surface. The interactive specimens run under the
// Interactive profile so the gothDialog controller enhances the closed baseline
// (focus trap/restore, nested dismiss, scroll lock, inert). The "-open" specimens
// run under StylesOnly and render the primitive server-open, proving the no-JS
// baseline: the content is in the DOM and displayed by CSS with no JavaScript.
func registerOverlaySpecimens(r *Registry) {
	interactive := []struct {
		id, title, prim string
		body            func() string
	}{
		{"dialog", "Dialog (P39)", "P39", dialogSpecimen},
		{"alert-dialog", "Alert Dialog (P37)", "P37", alertDialogSpecimen},
		{"sheet", "Sheet (P47)", "P47", sheetSpecimen},
		{"drawer", "Drawer (P40)", "P40", drawerSpecimen},
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
		{"dialog-open", "Dialog server-open (P39)", "P39", dialogOpenSpecimen},
		{"alert-dialog-open", "Alert Dialog server-open (P37)", "P37", alertDialogOpenSpecimen},
		{"sheet-open", "Sheet server-open (P47)", "P47", sheetOpenSpecimen},
		{"drawer-open", "Drawer server-open (P40)", "P40", drawerOpenSpecimen},
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

// input is a small labelled text input used inside overlay panels so the focus
// trap has a real focusable target.
func input(id, label string) string {
	return `<input type="text" class="goth-input" id="` + id + `" aria-label="` + label + `" />`
}

// dialogBody composes a Dialog. When open is true the root is server-rendered open
// (the no-JS baseline); nested adds a second Dialog inside the panel so the
// nested-overlay dismissal path can be driven.
func dialogBody(open, nested bool) string {
	var inner string
	if nested {
		inner = compKids(primitives.Dialog(primitives.DialogProps{}),
			compKids(primitives.DialogTrigger(primitives.DialogTriggerProps{Base: primitives.Base{ID: "nested-trigger"}}), "Open nested dialog")+
				compKids(primitives.DialogContent(primitives.DialogContentProps{Labelledby: "nested-title", Describedby: "nested-desc"}),
					compKids(primitives.DialogHeader(primitives.DialogHeaderProps{}),
						compKids(primitives.DialogTitle(primitives.DialogTitleProps{Base: primitives.Base{ID: "nested-title"}}), "Nested dialog")+
							compKids(primitives.DialogDescription(primitives.DialogDescriptionProps{Base: primitives.Base{ID: "nested-desc"}}), "Escape closes only this nested dialog first, then its parent."))+
						input("nested-input", "Nested field")+
						compKids(primitives.DialogFooter(primitives.DialogFooterProps{}),
							compKids(primitives.DialogClose(primitives.DialogCloseProps{Base: primitives.Base{ID: "nested-close"}}), "Close nested"))))
	}

	panel := compKids(primitives.DialogHeader(primitives.DialogHeaderProps{}),
		compKids(primitives.DialogTitle(primitives.DialogTitleProps{Base: primitives.Base{ID: "dialog-title"}}), "Edit profile")+
			compKids(primitives.DialogDescription(primitives.DialogDescriptionProps{Base: primitives.Base{ID: "dialog-desc"}}), "Update your display name. Focus is trapped until you close.")) +
		input("dialog-input", "Display name") +
		inner +
		compKids(primitives.DialogFooter(primitives.DialogFooterProps{}),
			compKids(primitives.DialogClose(primitives.DialogCloseProps{Base: primitives.Base{ID: "dialog-close"}}), "Cancel")+
				`<button type="submit" class="goth-button" data-slot="dialog-save">Save</button>`)

	dialog := compKids(primitives.Dialog(primitives.DialogProps{Open: open}),
		compKids(primitives.DialogTrigger(primitives.DialogTriggerProps{Base: primitives.Base{ID: "dialog-trigger"}}), "Open dialog")+
			compKids(primitives.DialogContent(primitives.DialogContentProps{Labelledby: "dialog-title", Describedby: "dialog-desc"}), panel))

	return `<section data-slot="dialog-specimen">` +
		`<p data-slot="scroll-probe">Long page content proves scroll lock while the modal is open.</p>` +
		dialog +
		`<div data-slot="tall-filler" aria-hidden="true"></div>` +
		`</section>`
}

func dialogSpecimen() string     { return page("Dialog", dialogBody(false, true)) }
func dialogOpenSpecimen() string { return page("Dialog (server-open)", dialogBody(true, false)) }

func alertDialogBody(open bool) string {
	panel := compKids(primitives.AlertDialogHeader(primitives.AlertDialogHeaderProps{}),
		compKids(primitives.AlertDialogTitle(primitives.AlertDialogTitleProps{Base: primitives.Base{ID: "alert-title"}}), "Delete this project?")+
			compKids(primitives.AlertDialogDescription(primitives.AlertDialogDescriptionProps{Base: primitives.Base{ID: "alert-desc"}}), "This permanently removes the project and all of its data. This action cannot be undone.")) +
		compKids(primitives.AlertDialogFooter(primitives.AlertDialogFooterProps{}),
			compKids(primitives.AlertDialogCancel(primitives.AlertDialogCancelProps{Base: primitives.Base{ID: "alert-cancel"}}), "Cancel")+
				compKids(primitives.AlertDialogAction(primitives.AlertDialogActionProps{Base: primitives.Base{ID: "alert-action", Class: "goth-button"}}), "Delete"))

	dialog := compKids(primitives.AlertDialog(primitives.AlertDialogProps{Open: open}),
		compKids(primitives.AlertDialogTrigger(primitives.AlertDialogTriggerProps{Base: primitives.Base{ID: "alert-trigger"}}), "Delete project")+
			compKids(primitives.AlertDialogContent(primitives.AlertDialogContentProps{Labelledby: "alert-title", Describedby: "alert-desc"}), panel))

	return `<section data-slot="alert-dialog-specimen">` + dialog + `</section>`
}

func alertDialogSpecimen() string     { return page("Alert Dialog", alertDialogBody(false)) }
func alertDialogOpenSpecimen() string { return page("Alert Dialog (server-open)", alertDialogBody(true)) }

func sheetBody(open bool, side primitives.SheetSide) string {
	panel := compKids(primitives.SheetHeader(primitives.SheetHeaderProps{}),
		compKids(primitives.SheetTitle(primitives.SheetTitleProps{Base: primitives.Base{ID: "sheet-title"}}), "Edit settings")+
			compKids(primitives.SheetDescription(primitives.SheetDescriptionProps{Base: primitives.Base{ID: "sheet-desc"}}), "Adjust your preferences. The panel slides from the edge.")) +
		input("sheet-input", "Workspace name") +
		compKids(primitives.SheetFooter(primitives.SheetFooterProps{}),
			compKids(primitives.SheetClose(primitives.SheetCloseProps{Base: primitives.Base{ID: "sheet-close"}}), "Close"))

	sheet := compKids(primitives.Sheet(primitives.SheetProps{Open: open}),
		compKids(primitives.SheetTrigger(primitives.SheetTriggerProps{Base: primitives.Base{ID: "sheet-trigger"}}), "Open sheet")+
			compKids(primitives.SheetContent(primitives.SheetContentProps{Side: side, Labelledby: "sheet-title", Describedby: "sheet-desc"}), panel))

	return `<section data-slot="sheet-specimen">` + sheet + `</section>`
}

func sheetSpecimen() string     { return page("Sheet", sheetBody(false, primitives.SheetRight)) }
func sheetOpenSpecimen() string { return page("Sheet (server-open)", sheetBody(true, primitives.SheetLeft)) }

func drawerBody(open bool) string {
	panel := compKids(primitives.DrawerHeader(primitives.DrawerHeaderProps{}),
		compKids(primitives.DrawerTitle(primitives.DrawerTitleProps{Base: primitives.Base{ID: "drawer-title"}}), "Move to")+
			compKids(primitives.DrawerDescription(primitives.DrawerDescriptionProps{Base: primitives.Base{ID: "drawer-desc"}}), "Choose a destination for the selected items.")) +
		input("drawer-input", "Destination") +
		compKids(primitives.DrawerFooter(primitives.DrawerFooterProps{}),
			compKids(primitives.DrawerClose(primitives.DrawerCloseProps{Base: primitives.Base{ID: "drawer-close"}}), "Close"))

	drawer := compKids(primitives.Drawer(primitives.DrawerProps{Open: open}),
		compKids(primitives.DrawerTrigger(primitives.DrawerTriggerProps{Base: primitives.Base{ID: "drawer-trigger"}}), "Open drawer")+
			compKids(primitives.DrawerContent(primitives.DrawerContentProps{Labelledby: "drawer-title", Describedby: "drawer-desc"}), panel))

	return `<section data-slot="drawer-specimen">` + drawer + `</section>`
}

func drawerSpecimen() string     { return page("Drawer", drawerBody(false)) }
func drawerOpenSpecimen() string { return page("Drawer (server-open)", drawerBody(true)) }
