package showcase

import (
	"net/http"
	"strconv"

	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// GOTH-6.5 notification specimens (Full profile). Both P64 Toast and P63 Sonner ride
// the ONE gothToast-backed live-region queue (the pre-provisioned GOTH-1.4 controller
// refined in place — no new §8 name).
//
//   - primitive-toast (P64): a server-rendered role=region Toaster holding readable
//     baseline toasts (no JS needed to read them). Enhanced, gothToast announces each
//     toast once through the shared live region (polite/assertive by priority), runs
//     auto-dismiss timers that pause on hover/focus/hidden-page, dedupes, caps overflow
//     (data-max), and handles keyboard/pointer dismissal + actions. HTMX triggers append
//     toast fragments to prove the live-triggered path.
//   - primitive-sonner (P63): the opinionated facade — a data-sonner Toaster with
//     single-call SonnerToast variants (success/info/warning/error) mapping variant →
//     live-region priority, an opinionated duration/overflow default, and a stacked
//     presentation. HTMX triggers append variant toasts.

// sonnerBaselineDuration keeps the server-rendered Sonner baseline toasts readable for
// the whole no-JS baseline (a practically-persistent duration); the HTMX-triggered
// variant toasts use the opinionated SonnerDefaultDuration to prove auto-dismiss.
const sonnerBaselineDuration = 600000

// notificationCounter yields deterministic ids for HTMX-appended toasts across engines.
var notificationCounter = struct{ n int }{}

func registerNotificationSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:           "primitive-toast",
		Title:        "Toast (P64)",
		Section:      SectionPrimitive,
		Primitive:    "P64",
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         toastSpecimen,
	})
	r.Register(Specimen{
		ID:           "primitive-sonner",
		Title:        "Sonner (P63)",
		Section:      SectionPrimitive,
		Primitive:    "P63",
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         sonnerSpecimen,
	})
}

// toast composes a Toast entry with a title, optional description, optional action, and
// a close button — the composable P64 shape.
func toast(p primitives.ToastProps, title, description, actionLabel string) string {
	inner := compKids(primitives.ToastClose(primitives.ToastCloseProps{}), `<span aria-hidden="true">&#215;</span>`)
	inner += compKids(primitives.ToastTitle(primitives.ToastTitleProps{}), title)
	if description != "" {
		inner += compKids(primitives.ToastDescription(primitives.ToastDescriptionProps{}), description)
	}
	if actionLabel != "" {
		inner += compKids(primitives.ToastAction(primitives.ToastActionProps{}), actionLabel)
	}
	return compKids(primitives.Toast(p), inner)
}

func toastSpecimen() string {
	// Two server-rendered baseline toasts, readable with no JS. The region caps at three
	// visible toasts so the HTMX "plain" trigger proves overflow.
	baseline := toast(primitives.ToastProps{Base: primitives.Base{ID: "toast-welcome"}},
		"Welcome back", "Your session is ready.", "") +
		toast(primitives.ToastProps{Base: primitives.Base{ID: "toast-alert"}, Priority: primitives.ToastAssertive},
			"Storage almost full", "Free up space to keep syncing.", "")

	region := compKids(primitives.Toaster(primitives.ToasterProps{
		Base: primitives.Base{ID: "toaster"}, Max: 3,
	}), baseline)

	// HTMX triggers append toast fragments (beforeend) to the region.
	trigger := func(id, path, label string) string {
		return `<button type="button" data-slot="` + id + `" hx-get="` + path +
			`" hx-target="#toaster" hx-swap="beforeend">` + label + `</button>`
	}
	controls := `<p data-slot="toast-controls">` +
		trigger("toast-timed", "/toast/timed", "Show timed toast") +
		trigger("toast-assertive", "/toast/assertive", "Show assertive toast") +
		trigger("toast-action", "/toast/action", "Show toast with action") +
		trigger("toast-plain", "/toast/plain", "Show plain toast") +
		`</p>`

	body := `<section data-slot="toast-specimen">` +
		`<p>The notification region below is a server-rendered <code>role=region</code> landmark. With no JavaScript its toasts are readable; enhanced, the shared gothToast queue announces each toast through the shared live region (polite/assertive by priority), auto-dismisses timed toasts (pausing on hover/focus/hidden page), caps overflow, and handles keyboard dismissal and actions.</p>` +
		controls + region +
		`</section>`
	return page("Toast", body)
}

func sonnerSpecimen() string {
	// Server-rendered baseline (readable with no JS); a long duration keeps them for the
	// baseline. The opinionated variants map to polite/assertive priority automatically.
	baseline := comp(primitives.SonnerToast(primitives.SonnerToastProps{
		Base: primitives.Base{ID: "sonner-saved"}, Variant: primitives.SonnerSuccess,
		Title: "Changes saved", Description: "Your document is up to date.",
		Duration: sonnerBaselineDuration,
	})) + comp(primitives.SonnerToast(primitives.SonnerToastProps{
		Base: primitives.Base{ID: "sonner-info"}, Variant: primitives.SonnerInfo,
		Title: "New version available", Description: "Reload to update.",
		Duration: sonnerBaselineDuration,
	}))

	region := compKids(primitives.Sonner(primitives.SonnerProps{
		Base: primitives.Base{ID: "sonner"},
	}), baseline)

	trigger := func(id, path, label string) string {
		return `<button type="button" data-slot="` + id + `" hx-get="` + path +
			`" hx-target="#sonner" hx-swap="beforeend">` + label + `</button>`
	}
	controls := `<p data-slot="sonner-controls">` +
		trigger("sonner-success", "/sonner/success", "Show success") +
		trigger("sonner-error", "/sonner/error", "Show error") +
		`</p>`

	body := `<section data-slot="sonner-specimen">` +
		`<p>Sonner is the opinionated facade over the same gothToast queue: one call renders a variant toast (status accent + mapped polite/assertive priority + an opinionated auto-dismiss and overflow cap). The baseline toasts are server-rendered and readable with no JS.</p>` +
		controls + region +
		`</section>`
	return page("Sonner", body)
}

// registerNotificationFixtures wires the HTMX toast/sonner fragment routes. Each returns
// a single toast fragment appended (beforeend) to the region; the routes are HTMX-only
// (the no-JS baseline is the server-rendered region above).
func (s *Server) registerNotificationFixtures() {
	next := func() string {
		notificationCounter.n++
		return strconv.Itoa(notificationCounter.n)
	}

	s.handler.Handle(http.MethodGet, "/toast/timed", func(w http.ResponseWriter, r *http.Request) {
		n := next()
		writeFragment(w, http.StatusOK, toast(primitives.ToastProps{
			Base: primitives.Base{ID: "toast-timed-" + n}, Duration: 3000,
		}, "Saved draft #"+n, "This toast dismisses itself.", ""))
	})
	s.handler.Handle(http.MethodGet, "/toast/assertive", func(w http.ResponseWriter, r *http.Request) {
		n := next()
		writeFragment(w, http.StatusOK, toast(primitives.ToastProps{
			Base: primitives.Base{ID: "toast-assertive-" + n}, Priority: primitives.ToastAssertive,
		}, "Action required #"+n, "Please review the flagged item.", ""))
	})
	s.handler.Handle(http.MethodGet, "/toast/action", func(w http.ResponseWriter, r *http.Request) {
		n := next()
		writeFragment(w, http.StatusOK, toast(primitives.ToastProps{
			Base: primitives.Base{ID: "toast-action-" + n},
		}, "Item deleted #"+n, "You can undo this change.", "Undo"))
	})
	s.handler.Handle(http.MethodGet, "/toast/plain", func(w http.ResponseWriter, r *http.Request) {
		n := next()
		writeFragment(w, http.StatusOK, toast(primitives.ToastProps{
			Base: primitives.Base{ID: "toast-plain-" + n},
		}, "Notice #"+n, "", ""))
	})

	s.handler.Handle(http.MethodGet, "/sonner/success", func(w http.ResponseWriter, r *http.Request) {
		n := next()
		writeFragment(w, http.StatusOK, comp(primitives.SonnerToast(primitives.SonnerToastProps{
			Base: primitives.Base{ID: "sonner-success-" + n}, Variant: primitives.SonnerSuccess,
			Title: "Uploaded #" + n, Description: "The file is now available.",
		})))
	})
	s.handler.Handle(http.MethodGet, "/sonner/error", func(w http.ResponseWriter, r *http.Request) {
		n := next()
		writeFragment(w, http.StatusOK, comp(primitives.SonnerToast(primitives.SonnerToastProps{
			Base: primitives.Base{ID: "sonner-error-" + n}, Variant: primitives.SonnerError,
			Title: "Upload failed #" + n, Description: "The connection was lost.",
			ActionLabel: "Retry",
		})))
	})
}
