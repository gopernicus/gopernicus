package showcase

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/components/data"
	"github.com/gopernicus/gopernicus/ui/goth/components/feedback"
	"github.com/gopernicus/gopernicus/ui/goth/components/forms"
	"github.com/gopernicus/gopernicus/ui/goth/components/layouts"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// GOTH-7.1 component specimens. Each renders a REAL ui/goth component composing
// primitives (never hand-written markup), so the browser/axe/CSP harness exercises
// the actual emitted surface. Every ImplementedComponents entry has a specimen
// here (TestEveryImplementedComponentHasSpecimen enforces it), and each component
// is proven by a specimen consumer plus a documented adopter need (authentication
// GOTH-7.2 / CMS GOTH-7.3). Components add no primitive, no controller, and no
// route; they arrange primitives and select defaults only.

// registerComponentSpecimens registers one showcase page per GOTH-7.1 component.
func registerComponentSpecimens(r *Registry) {
	specs := []struct {
		id, title, key string
		profile        goth.Profile
		body           func() string
	}{
		{"document-shell", "DocumentShell (layouts)", "document-shell", goth.StylesOnly, documentShellSpecimen},
		{"app-shell", "AppShell (layouts)", "app-shell", goth.StylesOnly, appShellSpecimen},
		{"auth-shell", "AuthShell (layouts)", "auth-shell", goth.StylesOnly, authShellSpecimen},
		{"page-header", "PageHeader (layouts)", "page-header", goth.StylesOnly, pageHeaderSpecimen},
		{"action-bar", "ActionBar (layouts)", "action-bar", goth.StylesOnly, actionBarSpecimen},
		{"form-field", "FormField (forms)", "form-field", goth.StylesOnly, formFieldSpecimen},
		{"form-section", "FormSection (forms)", "form-section", goth.StylesOnly, formSectionSpecimen},
		{"error-summary", "ErrorSummary (forms)", "error-summary", goth.StylesOnly, errorSummarySpecimen},
		{"form-actions", "FormActions (forms)", "form-actions", goth.StylesOnly, formActionsSpecimen},
		{"empty-panel", "EmptyPanel (feedback)", "empty-panel", goth.StylesOnly, emptyPanelSpecimen},
		{"loading-panel", "LoadingPanel (feedback)", "loading-panel", goth.StylesOnly, loadingPanelSpecimen},
		{"error-panel", "ErrorPanel (feedback)", "error-panel", goth.StylesOnly, errorPanelSpecimen},
		{"table-toolbar", "TableToolbar (data)", "table-toolbar", goth.StylesOnly, tableToolbarSpecimen},
	}
	for _, s := range specs {
		r.Register(Specimen{
			ID:        "component-" + s.id,
			Title:     s.title,
			Section:   SectionComponent,
			Component: s.key,
			Profile:   s.profile,
			Body:      s.body,
		})
	}

	// ConfirmDialog composes the gothDialog-backed AlertDialog, so it needs the
	// Interactive profile (the controller opens/closes and traps focus). Its no-JS
	// baseline is the server-owned data-state, exercised in the render tests.
	r.Register(Specimen{
		ID:        "component-confirm-dialog",
		Title:     "ConfirmDialog (feedback)",
		Section:   SectionComponent,
		Component: "confirm-dialog",
		Profile:   goth.Interactive,
		Body:      confirmDialogSpecimen,
	})
}

// --- layouts ---------------------------------------------------------------

func documentShellSpecimen() string {
	header := templ.Raw(`<nav data-slot="site-nav"><strong>Acme</strong> · <a href="/">Home</a> · <a href="/about">About</a></nav>`)
	footer := templ.Raw(`<small>© Acme. A domain-neutral content page shell.</small>`)
	main := compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyH2}), "Welcome") +
		compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyP}),
			"DocumentShell centers a reading column between an optional header bar and footer. It composes only primitives and owns no route or domain type.")
	shell := compKids(layouts.DocumentShell(layouts.DocumentShellProps{Header: header, Footer: footer}), main)
	return page("DocumentShell", `<section data-slot="component-document-shell">`+shell+`</section>`)
}

func appShellSpecimen() string {
	sidebar := templ.Raw(`<nav data-slot="admin-nav" aria-label="Admin"><ul><li><a href="#dashboard" aria-current="page">Dashboard</a></li><li><a href="#people">People</a></li><li><a href="#settings">Settings</a></li></ul></nav>`)
	header := templ.Raw(`<div data-slot="admin-bar"><strong>Admin</strong></div>`)
	main := compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyH2}), "Dashboard") +
		compKids(primitives.Typography(primitives.TypographyProps{Variant: primitives.TypographyMuted}),
			"AppShell arranges a sidebar beside a header and the main region; one markup drives both breakpoints via CSS.")
	shell := compKids(layouts.AppShell(layouts.AppShellProps{Sidebar: sidebar, Header: header}), main)
	return page("AppShell", `<section data-slot="component-app-shell">`+shell+`</section>`)
}

func authShellSpecimen() string {
	brand := templ.Raw(`<span data-slot="wordmark"><strong>Acme</strong></span>`)
	footer := templ.Raw(`<span>New here? <a href="#register">Create an account</a></span>`)
	form := `<form method="post" action="#auth" data-slot="auth-form">` +
		compKids(forms.FormField(forms.FormFieldProps{Label: "Email", For: "auth-email"}),
			comp(primitives.Input(primitives.InputProps{Type: primitives.InputEmail, Name: "email",
				Base: primitives.Base{ID: "auth-email", Attributes: templ.Attributes{"autocomplete": "email"}}}))) +
		compKids(forms.FormField(forms.FormFieldProps{Label: "Password", For: "auth-password"}),
			comp(primitives.Input(primitives.InputProps{Type: primitives.InputPassword, Name: "password",
				Base: primitives.Base{ID: "auth-password", Attributes: templ.Attributes{"autocomplete": "current-password"}}}))) +
		compKids(forms.FormActions(forms.FormActionsProps{}),
			compKids(primitives.Button(primitives.ButtonProps{Type: primitives.ButtonTypeSubmit}), "Sign in")) +
		`</form>`
	shell := compKids(layouts.AuthShell(layouts.AuthShellProps{
		Brand:       brand,
		Title:       "Sign in",
		Description: "Use your Acme account",
		Footer:      footer,
	}), form)
	return page("AuthShell", `<section data-slot="component-auth-shell">`+shell+`</section>`)
}

func pageHeaderSpecimen() string {
	breadcrumb := comp(primitives.Breadcrumb(primitives.BreadcrumbProps{}))
	actions := compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonDefault}), "New person") + " " +
		compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Export")
	header := comp(layouts.PageHeader(layouts.PageHeaderProps{
		Title:       "People",
		Description: "Manage everyone in your workspace.",
		Breadcrumb:  templ.Raw(breadcrumb),
		Actions:     templ.Raw(actions),
	}))
	return page("PageHeader", `<section data-slot="component-page-header">`+header+`</section>`)
}

func actionBarSpecimen() string {
	start := compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Filter") + " " +
		compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Sort")
	end := compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonDestructive}), "Delete selected") + " " +
		compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonDefault}), "Save")
	bar := comp(layouts.ActionBar(layouts.ActionBarProps{
		Label: "Row actions",
		Start: templ.Raw(start),
		End:   templ.Raw(end),
	}))
	return page("ActionBar", `<section data-slot="component-action-bar">`+bar+`</section>`)
}

// --- forms -----------------------------------------------------------------

func formFieldSpecimen() string {
	// A wired field: the control's aria-describedby points at the derived
	// description/error ids the component owns.
	described := comp(primitives.Input(primitives.InputProps{
		Type: primitives.InputEmail, Name: "email",
		Base: primitives.Base{ID: "ff-email", Attributes: templ.Attributes{
			"aria-describedby": forms.DescriptionID("ff-email"),
		}},
	}))
	valid := compKids(forms.FormField(forms.FormFieldProps{
		Label: "Email", For: "ff-email", Description: "We never share it.", Required: true,
	}), described)

	invalidControl := comp(primitives.Input(primitives.InputProps{
		Type: primitives.InputText, Name: "handle", Value: "!!",
		Invalid: true,
		Base: primitives.Base{ID: "ff-handle", Attributes: templ.Attributes{
			"aria-describedby": forms.ErrorID("ff-handle"),
		}},
	}))
	invalid := compKids(forms.FormField(forms.FormFieldProps{
		Label: "Handle", For: "ff-handle", Error: "Only letters, numbers, and dashes are allowed.",
	}), invalidControl)

	body := `<form data-slot="form-field-demo">` + valid + invalid + `</form>`
	return page("FormField", `<section data-slot="component-form-field">`+body+`</section>`)
}

func formSectionSpecimen() string {
	field := func(label, id string) string {
		return compKids(forms.FormField(forms.FormFieldProps{Label: label, For: id}),
			comp(primitives.Input(primitives.InputProps{Name: id, Base: primitives.Base{ID: id}})))
	}
	section := compKids(forms.FormSection(forms.FormSectionProps{
		Title:       "Profile",
		Description: "This information is shown on your public profile.",
	}), field("Full name", "fs-name")+field("Headline", "fs-headline"))
	body := `<form data-slot="form-section-demo">` + section + `</form>`
	return page("FormSection", `<section data-slot="component-form-section">`+body+`</section>`)
}

func errorSummarySpecimen() string {
	summary := comp(forms.ErrorSummary(forms.ErrorSummaryProps{
		Errors: []forms.FieldMessage{
			{Message: "Email is required", FieldID: "es-email"},
			{Message: "Password must be at least 8 characters", FieldID: "es-password"},
			{Message: "The form could not be saved. Try again."},
		},
	}))
	// Real targets so the in-page anchors focus a control (accessible + testable).
	fields := `<form data-slot="error-summary-demo">` +
		compKids(forms.FormField(forms.FormFieldProps{Label: "Email", For: "es-email", Error: "Email is required"}),
			comp(primitives.Input(primitives.InputProps{Type: primitives.InputEmail, Name: "email", Invalid: true, Base: primitives.Base{ID: "es-email"}}))) +
		compKids(forms.FormField(forms.FormFieldProps{Label: "Password", For: "es-password", Error: "Password must be at least 8 characters"}),
			comp(primitives.Input(primitives.InputProps{Type: primitives.InputPassword, Name: "password", Invalid: true, Base: primitives.Base{ID: "es-password"}}))) +
		`</form>`
	return page("ErrorSummary", `<section data-slot="component-error-summary">`+summary+fields+`</section>`)
}

func formActionsSpecimen() string {
	end := compKids(forms.FormActions(forms.FormActionsProps{}),
		compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Cancel")+" "+
			compKids(primitives.Button(primitives.ButtonProps{Type: primitives.ButtonTypeSubmit}), "Save changes"))
	between := compKids(forms.FormActions(forms.FormActionsProps{Align: forms.FormActionsBetween}),
		compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonDestructive}), "Delete")+" "+
			compKids(primitives.Button(primitives.ButtonProps{Type: primitives.ButtonTypeSubmit}), "Save"))
	body := `<form data-slot="form-actions-demo"><p>End-aligned (default):</p>` + end +
		`<p>Space-between (destructive left, submit right):</p>` + between + `</form>`
	return page("FormActions", `<section data-slot="component-form-actions">`+body+`</section>`)
}

// --- feedback --------------------------------------------------------------

func emptyPanelSpecimen() string {
	media := templ.Raw(`<svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>`)
	action := templ.Raw(compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonDefault}), "Invite teammate"))
	panel := comp(feedback.EmptyPanel(feedback.EmptyPanelProps{
		Media:       media,
		Title:       "No people yet",
		Description: "Invite your first teammate to get started.",
		Action:      action,
	}))
	return page("EmptyPanel", `<section data-slot="component-empty-panel">`+panel+`</section>`)
}

func loadingPanelSpecimen() string {
	panel := comp(feedback.LoadingPanel(feedback.LoadingPanelProps{
		Label:       "Loading people",
		Description: "This should only take a moment.",
	}))
	return page("LoadingPanel", `<section data-slot="component-loading-panel">`+panel+`</section>`)
}

func errorPanelSpecimen() string {
	media := templ.Raw(`<svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`)
	action := templ.Raw(compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonOutline}), "Try again"))
	panel := comp(feedback.ErrorPanel(feedback.ErrorPanelProps{
		Media:       media,
		Title:       "Couldn't load people",
		Description: "The request failed. Check your connection and try again.",
		Action:      action,
	}))
	return page("ErrorPanel", `<section data-slot="component-error-panel">`+panel+`</section>`)
}

func confirmDialogSpecimen() string {
	// The confirm button submits a server-owned POST form to /components/confirm
	// (same-origin under form-action 'self'), so the destructive workflow is proven
	// end to end rather than faked with a fragment.
	dialog := comp(feedback.ConfirmDialog(feedback.ConfirmDialogProps{
		Base:         primitives.Base{ID: "confirm-delete"},
		TriggerLabel: "Delete project",
		Title:        "Delete project?",
		Description:  "This permanently removes the project and all of its data. This cannot be undone.",
		ConfirmLabel: "Delete project",
		Action:       mustURL("/components/confirm"),
	}))
	return page("ConfirmDialog", `<section data-slot="component-confirm-dialog"><p>ConfirmDialog composes the gothDialog-backed AlertDialog; the confirm button submits a server-owned form.</p>`+dialog+`</section>`)
}

// registerComponentFixtures wires the server-owned POST route the ConfirmDialog
// specimen submits to, proving the destructive-confirmation workflow end to end.
func (s *Server) registerComponentFixtures() {
	s.handler.Handle(http.MethodPost, "/components/confirm", func(w http.ResponseWriter, r *http.Request) {
		bundle := s.bundles[goth.Interactive]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		body := `<main data-slot="confirm-result"><h1>Project deleted</h1>` +
			`<p data-slot="confirm-outcome" role="status">The confirmation form was submitted to the server.</p>` +
			`<a href="/specimen/component-confirm-dialog">Back</a></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — confirmed"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}

// --- data ------------------------------------------------------------------

func tableToolbarSpecimen() string {
	filters := templ.Raw(comp(primitives.NativeSelect(primitives.NativeSelectProps{Name: "role", Base: primitives.Base{Attributes: templ.Attributes{"aria-label": "Role"}}})))
	actions := templ.Raw(compKids(primitives.Button(primitives.ButtonProps{Variant: primitives.ButtonDefault}), "New person"))
	toolbar := comp(data.TableToolbar(data.TableToolbarProps{
		Action:      mustURL("/specimen/component-table-toolbar"),
		SearchLabel: "Filter people",
		Filters:     filters,
		Actions:     actions,
	}))
	body := toolbar +
		`<p data-slot="table-toolbar-note">No JavaScript: the search is a form GET with a shareable URL. A host enhances it with explicit hx-* through SearchAttributes.</p>`
	return page("TableToolbar", `<section data-slot="component-table-toolbar">`+body+`</section>`)
}
