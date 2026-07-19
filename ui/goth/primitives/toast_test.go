package primitives

import (
	"strings"
	"testing"
)

// GOTH-6.5 Toast (P64). These tests prove the composable F4 parts: the Toaster region
// (the gothToast controller binding + role=region landmark with a required accessible
// name + position/max hooks), the Toast entry (priority/duration/dedup hooks +
// server-owned data-state="open"), the title/description parts, the action (link vs
// button), the close button (default name), attribute-merge ownership, and the
// no-inline-style invariant. The queue behaviors (announce, pause, overflow, dismiss,
// HTMX) are proven in the 3-engine toast e2e spec.

// TestNoToastPrimitiveEmitsInlineStyle proves invariant (a).
func TestNoToastPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Toaster(ToasterProps{}), "x"),
		renderKids(t, Toast(ToastProps{Priority: ToastAssertive, Duration: 5000}), "x"),
		renderKids(t, ToastTitle(ToastTitleProps{}), "t"),
		renderKids(t, ToastDescription(ToastDescriptionProps{}), "d"),
		renderKids(t, ToastAction(ToastActionProps{}), "Undo"),
		renderKids(t, ToastClose(ToastCloseProps{}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("toast primitive emitted an inline style=: %s", o)
		}
	}
}

// TestToasterRegion proves the region binds the controller and is a named, reachable,
// non-trapping landmark carrying the position/max hooks.
func TestToasterRegion(t *testing.T) {
	out := renderKids(t, Toaster(ToasterProps{
		Base: Base{ID: "toasts"}, Position: ToastTopEnd, Max: 3,
	}), "body")
	mustContain(t, out,
		`class="goth-toaster"`, `data-slot="toaster"`, `id="toasts"`,
		`role="region"`, `aria-label="Notifications"`, `tabindex="-1"`,
		`x-data="gothToast"`, `data-position="top-end"`, `data-max="3"`, `body`)
}

// TestToasterDefaults proves the zero-value region is bottom-end, named by default, and
// emits no data-max (unbounded).
func TestToasterDefaults(t *testing.T) {
	out := renderKids(t, Toaster(ToasterProps{}), "x")
	mustContain(t, out, `data-position="bottom-end"`, `aria-label="Notifications"`)
	mustNotContain(t, out, `data-max=`)

	named := renderKids(t, Toaster(ToasterProps{Label: "Alerts"}), "x")
	mustContain(t, named, `aria-label="Alerts"`)
}

// TestToastEntry proves the toast carries the priority/duration/dedup hooks and the
// server-owned open state; a persistent (Duration 0) toast emits no data-duration.
func TestToastEntry(t *testing.T) {
	out := renderKids(t, Toast(ToastProps{
		Priority: ToastAssertive, Duration: 6000, DedupKey: "save",
	}), "hello")
	mustContain(t, out,
		`class="goth-toast"`, `data-slot="toast"`, `data-priority="assertive"`,
		`data-state="open"`, `data-duration="6000"`, `data-dedup-key="save"`, `hello`)

	polite := renderKids(t, Toast(ToastProps{}), "x")
	mustContain(t, polite, `data-priority="polite"`, `data-state="open"`)
	mustNotContain(t, polite, `data-duration=`, `data-dedup-key=`)
}

// TestToastPartsAndAction proves the title/description parts and the action (a real link
// when URL is set, else a button) plus the close button's default accessible name.
func TestToastPartsAndAction(t *testing.T) {
	title := renderKids(t, ToastTitle(ToastTitleProps{}), "Saved")
	mustContain(t, title, `data-slot="toast-title"`, `Saved`)

	desc := renderKids(t, ToastDescription(ToastDescriptionProps{}), "Your changes are live.")
	mustContain(t, desc, `data-slot="toast-description"`, `Your changes are live.`)

	link := renderKids(t, ToastAction(ToastActionProps{URL: mustParseURL(t, "/undo")}), "Undo")
	mustContain(t, link, `<a`, `href="/undo"`, `data-slot="toast-action"`, `Undo`)
	mustNotContain(t, link, `type="button"`)

	btn := renderKids(t, ToastAction(ToastActionProps{}), "Retry")
	mustContain(t, btn, `<button`, `type="button"`, `data-slot="toast-action"`, `Retry`)

	closeBtn := renderKids(t, ToastClose(ToastCloseProps{}), "x")
	mustContain(t, closeBtn, `<button`, `type="button"`, `data-slot="toast-close"`, `aria-label="Dismiss"`)

	named := renderKids(t, ToastClose(ToastCloseProps{Label: "Close alert"}), "x")
	mustContain(t, named, `aria-label="Close alert"`)
}

// TestToastMergeHonorsOwnership proves a caller cannot overwrite a behavior-critical
// owned attribute (the controller binding, data-slot, data-state) or drop the
// compatibility class, while still adding data-* through the escape hatch.
func TestToastMergeHonorsOwnership(t *testing.T) {
	out := renderKids(t, Toaster(ToasterProps{Base: Base{
		Class: "custom-c",
		Attributes: map[string]any{
			"x-data":    "evil",
			"data-slot": "hijack",
			"class":     "dropped",
			"data-test": "kept",
		},
	}}), "x")
	mustContain(t, out, `x-data="gothToast"`, `data-slot="toaster"`,
		`goth-toaster custom-c`, `data-test="kept"`)
	mustNotContain(t, out, `evil`, `hijack`, `dropped`)

	toast := renderKids(t, Toast(ToastProps{Priority: ToastPolite, Base: Base{
		Attributes: map[string]any{"data-priority": "assertive", "data-state": "closed"},
	}}), "x")
	mustContain(t, toast, `data-priority="polite"`, `data-state="open"`)
	mustNotContain(t, toast, `data-state="closed"`)
}
