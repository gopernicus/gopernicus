package primitives

import (
	"strings"
	"testing"
)

// TestNoOverlayPrimitiveEmitsInlineStyle proves invariant (a): no GOTH-4.2
// modal/panel primitive emits an inline style= in any state — all geometry and
// animation are data-state/data-side + external CSS, never style (the anchor/
// scroll-lock/inert mechanics are CSP-safe class/CSSOM only).
func TestNoOverlayPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Dialog(DialogProps{Open: true}), "x"),
		renderKids(t, DialogTrigger(DialogTriggerProps{}), "Open"),
		renderKids(t, DialogContent(DialogContentProps{Labelledby: "t", Describedby: "d"}), "x"),
		renderKids(t, DialogHeader(DialogHeaderProps{}), "x"),
		renderKids(t, DialogFooter(DialogFooterProps{}), "x"),
		renderKids(t, DialogTitle(DialogTitleProps{Base: Base{ID: "t"}}), "Title"),
		renderKids(t, DialogDescription(DialogDescriptionProps{Base: Base{ID: "d"}}), "Desc"),
		renderKids(t, DialogClose(DialogCloseProps{}), "Close"),
		renderKids(t, AlertDialog(AlertDialogProps{Open: true}), "x"),
		renderKids(t, AlertDialogContent(AlertDialogContentProps{Labelledby: "t", Describedby: "d"}), "x"),
		renderKids(t, AlertDialogAction(AlertDialogActionProps{}), "Delete"),
		renderKids(t, AlertDialogCancel(AlertDialogCancelProps{}), "Cancel"),
		renderKids(t, Sheet(SheetProps{Open: true}), "x"),
		renderKids(t, SheetContent(SheetContentProps{Side: SheetLeft}), "x"),
		renderKids(t, Drawer(DrawerProps{Open: true}), "x"),
		renderKids(t, DrawerContent(DrawerContentProps{}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("overlay primitive emitted an inline style=: %s", o)
		}
	}
}

// TestDialogServerOwnedBaseline proves the no-JS baseline: the root's server
// data-state governs visibility and the controller binding is present, but nothing
// depends on JavaScript to render the content into the DOM.
func TestDialogServerOwnedBaseline(t *testing.T) {
	closed := renderKids(t, Dialog(DialogProps{}), "x")
	mustContain(t, closed, `class="goth-dialog"`, `data-slot="dialog"`, `data-state="closed"`, `x-data="gothDialog"`)
	mustNotContain(t, closed, `data-modal`, `data-dismiss-outside`)

	open := renderKids(t, Dialog(DialogProps{Open: true}), "x")
	mustContain(t, open, `data-state="open"`)

	nonmodal := renderKids(t, Dialog(DialogProps{NonModal: true}), "x")
	mustContain(t, nonmodal, `data-modal="false"`)
}

// TestDialogContentStructure proves DialogContent owns the frozen overlay/scrim/
// panel structure the shared mechanics require, and wires caller ARIA linkage. No
// static aria-modal is emitted — the controller asserts it only under enforced
// modality (honest no-JS baseline).
func TestDialogContentStructure(t *testing.T) {
	out := renderKids(t, DialogContent(DialogContentProps{Base: Base{ID: "dlg"}, Labelledby: "title-id", Describedby: "desc-id"}), "body")
	mustContain(t, out,
		`class="goth-overlay"`, `data-slot="overlay"`,
		`class="goth-overlay-scrim"`, `data-slot="scrim"`, `aria-hidden="true"`,
		`class="goth-overlay-panel"`, `data-slot="content"`, `role="dialog"`,
		`id="dlg"`, `aria-labelledby="title-id"`, `aria-describedby="desc-id"`, "body")
	mustNotContain(t, out, `aria-modal`)
}

// TestDialogTriggerAndClose proves the trigger opens and the close dismisses via
// the gothDialog method contract (show/hide), and the trigger advertises a dialog
// popup.
func TestDialogTriggerAndClose(t *testing.T) {
	trig := renderKids(t, DialogTrigger(DialogTriggerProps{}), "Open")
	mustContain(t, trig, `<button`, `data-slot="trigger"`, `type="button"`, `aria-haspopup="dialog"`, `x-on:click="show($event)"`, "Open")

	closeBtn := renderKids(t, DialogClose(DialogCloseProps{}), "Close")
	mustContain(t, closeBtn, `<button`, `data-slot="close"`, `type="button"`, `x-on:click="hide($event)"`, "Close")
}

// TestAlertDialogDecisionContract proves Alert Dialog is always modal, opts out of
// outside/scrim dismissal (Escape still cancels via the shared stack), uses
// role="alertdialog", and exposes the destructive action + cancel choice.
func TestAlertDialogDecisionContract(t *testing.T) {
	root := renderKids(t, AlertDialog(AlertDialogProps{}), "x")
	mustContain(t, root, `class="goth-alert-dialog"`, `data-slot="alert-dialog"`, `x-data="gothDialog"`, `data-dismiss-outside="false"`)
	mustNotContain(t, root, `data-modal="false"`) // always modal

	content := renderKids(t, AlertDialogContent(AlertDialogContentProps{Labelledby: "t", Describedby: "d"}), "x")
	mustContain(t, content, `role="alertdialog"`, `aria-labelledby="t"`, `aria-describedby="d"`)

	action := renderKids(t, AlertDialogAction(AlertDialogActionProps{}), "Delete")
	mustContain(t, action, `data-slot="action"`, `x-on:click="hide($event)"`, "Delete")
	// Action carries no forced type so it submits inside a form (default button type).
	mustNotContain(t, action, `type="button"`)

	cancel := renderKids(t, AlertDialogCancel(AlertDialogCancelProps{}), "Cancel")
	mustContain(t, cancel, `data-slot="cancel"`, `type="button"`, `x-on:click="hide($event)"`, "Cancel")
}

// TestSheetSideResolution proves SheetSide maps to the frozen data-side the CSS
// anchors from, with the right edge as the zero-value default and an unknown value
// falling back to the default without panicking.
func TestSheetSideResolution(t *testing.T) {
	cases := map[SheetSide]string{
		SheetRight:            "right",
		SheetLeft:             "left",
		SheetTop:              "top",
		SheetBottom:           "bottom",
		SheetSide("nonsense"): "right",
	}
	for side, want := range cases {
		out := renderKids(t, SheetContent(SheetContentProps{Side: side}), "x")
		mustContain(t, out, `class="goth-sheet-panel"`, `data-slot="content"`, `role="dialog"`, `data-side="`+want+`"`)
	}
	if !SheetRight.Valid() || !SheetBottom.Valid() || SheetSide("nonsense").Valid() {
		t.Error("SheetSide.Valid mismatch")
	}
}

// TestDrawerSideAndHandle proves DrawerSide defaults to the bottom edge and the
// decorative drag handle renders only for the bottom/top edges.
func TestDrawerSideAndHandle(t *testing.T) {
	bottom := renderKids(t, DrawerContent(DrawerContentProps{}), "x")
	mustContain(t, bottom, `class="goth-drawer-panel"`, `data-side="bottom"`, `class="goth-drawer-handle"`, `data-slot="handle"`, `aria-hidden="true"`)

	right := renderKids(t, DrawerContent(DrawerContentProps{Side: DrawerRight}), "x")
	mustContain(t, right, `data-side="right"`)
	mustNotContain(t, right, `goth-drawer-handle`)

	if !DrawerBottom.Valid() || DrawerSide("x").Valid() {
		t.Error("DrawerSide.Valid mismatch")
	}
}

// TestOverlayMergeHonorsOwnership proves a caller cannot overwrite a behavior-
// critical owned attribute (x-data controller binding, data-slot, role) or drop
// the compatibility class through the Base.Attributes escape hatch, while a
// benign caller data-* attribute still merges.
func TestOverlayMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, Dialog(DialogProps{Base: Base{
		Class: "custom-x",
		Attributes: map[string]any{
			"x-data":        "evil",
			"data-slot":     "hijack",
			"class":         "dropped",
			"data-testhook": "keep",
		},
	}}), "x")
	mustContain(t, out, `x-data="gothDialog"`, `data-slot="dialog"`, `goth-dialog custom-x`, `data-testhook="keep"`)
	mustNotContain(t, out, `evil`, `hijack`, `dropped`)
}
