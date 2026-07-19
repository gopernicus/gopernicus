package primitives

import (
	"strings"
	"testing"
)

// GOTH-6.5 Sonner (P63). These tests prove the opinionated facade over the shared
// gothToast queue: the Sonner region is a data-sonner Toaster with the opinionated Max
// default and the goth-sonner class; SonnerToast renders a complete variant toast
// (data-variant + priority mapping + duration default + title/description/action/close)
// over the shared Toast parts, so it rides the same runtime with no fork.

// TestSonnerPriorityMapping proves the opinionated variant→priority mapping.
func TestSonnerPriorityMapping(t *testing.T) {
	cases := map[SonnerVariant]ToastPriority{
		SonnerDefault: ToastPolite,
		SonnerSuccess: ToastPolite,
		SonnerInfo:    ToastPolite,
		SonnerWarning: ToastAssertive,
		SonnerError:   ToastAssertive,
	}
	for v, want := range cases {
		if got := v.priority(); got != want {
			t.Errorf("variant %q priority = %q, want %q", v, got, want)
		}
	}
}

// TestNoSonnerPrimitiveEmitsInlineStyle proves invariant (a).
func TestNoSonnerPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Sonner(SonnerProps{}), "x"),
		render(t, SonnerToast(SonnerToastProps{Variant: SonnerError, Title: "Failed", Description: "Try again", ActionLabel: "Retry"})),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("sonner primitive emitted an inline style=: %s", o)
		}
	}
}

// TestSonnerRegion proves the opinionated region is a data-sonner Toaster with the
// goth-sonner class and the SonnerDefaultMax when Max is left at zero.
func TestSonnerRegion(t *testing.T) {
	out := renderKids(t, Sonner(SonnerProps{Base: Base{ID: "sonner"}}), "x")
	mustContain(t, out,
		`data-slot="toaster"`, `x-data="gothToast"`, `role="region"`,
		`data-sonner="true"`, `goth-toaster goth-sonner`, `id="sonner"`,
		`data-max="3"`, `data-position="bottom-end"`)

	capped := renderKids(t, Sonner(SonnerProps{Max: 5}), "x")
	mustContain(t, capped, `data-max="5"`)
}

// TestSonnerToast proves a complete opinionated toast: the variant + mapped assertive
// priority + default duration on the composed Toast, the accent/title/description, an
// action, and a named close button.
func TestSonnerToast(t *testing.T) {
	out := render(t, SonnerToast(SonnerToastProps{
		Variant:     SonnerError,
		Title:       "Upload failed",
		Description: "The file was too large.",
		ActionLabel: "Retry",
		ActionURL:   mustParseURL(t, "/retry"),
		CloseLabel:  "Dismiss alert",
	}))
	mustContain(t, out,
		`data-slot="toast"`, `goth-toast goth-sonner-toast`, `data-variant="error"`,
		`data-priority="assertive"`, `data-duration="4000"`, `data-state="open"`,
		`data-slot="sonner-accent"`, `data-slot="toast-title"`, `Upload failed`,
		`data-slot="toast-description"`, `The file was too large.`,
		`data-slot="toast-action"`, `href="/retry"`, `Retry`,
		`data-slot="toast-close"`, `aria-label="Dismiss alert"`)
}

// TestSonnerToastPoliteMinimal proves a default-variant toast is polite, keeps the
// opinionated duration default, and omits the description/action when unset.
func TestSonnerToastPoliteMinimal(t *testing.T) {
	out := render(t, SonnerToast(SonnerToastProps{Title: "Saved"}))
	mustContain(t, out,
		`data-variant="default"`, `data-priority="polite"`, `data-duration="4000"`,
		`data-slot="toast-title"`, `Saved`, `data-slot="toast-close"`)
	mustNotContain(t, out, `data-slot="toast-description"`, `data-slot="toast-action"`)
}

// TestSonnerToastDurationOverride proves a caller-set duration overrides the default.
func TestSonnerToastDurationOverride(t *testing.T) {
	out := render(t, SonnerToast(SonnerToastProps{Title: "Sticky", Duration: 10000}))
	mustContain(t, out, `data-duration="10000"`)
}
