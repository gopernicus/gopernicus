package forms

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func renderKids(t *testing.T, c templ.Component, kids string) string {
	t.Helper()
	var sb strings.Builder
	ctx := templ.WithChildren(context.Background(), templ.Raw(kids))
	if err := c.Render(ctx, &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("output unexpectedly contains %q\n---\n%s", u, out)
		}
	}
}

func TestNoFormComponentEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, FormField(FormFieldProps{Label: "Email", For: "email", Description: "we never share it", Error: "required", Required: true}), `<input id="email">`),
		renderKids(t, FormSection(FormSectionProps{Title: "Profile", Description: "public"}), `<div>fields</div>`),
		render(t, ErrorSummary(ErrorSummaryProps{Errors: []FieldMessage{{Message: "Email is required", FieldID: "email"}}})),
		renderKids(t, FormActions(FormActionsProps{Align: FormActionsBetween}), `<button>Save</button>`),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") || strings.Contains(o, "<style") {
			t.Errorf("form component emitted inline style: %s", o)
		}
	}
}

func TestFormFieldWiresLabelDescriptionError(t *testing.T) {
	out := renderKids(t, FormField(FormFieldProps{
		Label:       "Email",
		For:         "email",
		Description: "We never share it.",
		Error:       "Email is required.",
		Required:    true,
	}), `<input id="email" name="email" aria-describedby="email-description email-error">`)

	mustContain(t, out,
		`data-slot="field"`, `data-invalid="true"`, // Error flags the field invalid
		`data-slot="field-label"`, `for="email"`, ">Email",
		`data-slot="field-required"`, `aria-hidden="true"`,
		`<input id="email"`,
		`data-slot="field-description"`, `id="email-description"`, "We never share it.",
		`data-slot="field-error"`, `id="email-error"`, "Email is required.")

	// The derived ids match the exported helpers callers use to wire the control.
	if DescriptionID("email") != "email-description" {
		t.Errorf("DescriptionID = %q", DescriptionID("email"))
	}
	if ErrorID("email") != "email-error" {
		t.Errorf("ErrorID = %q", ErrorID("email"))
	}
	if DescriptionID("") != "" || ErrorID("") != "" {
		t.Error("empty control id must yield empty derived ids")
	}
}

func TestFormFieldWithoutErrorIsValid(t *testing.T) {
	out := renderKids(t, FormField(FormFieldProps{Label: "Name", For: "name"}), `<input id="name">`)
	mustNotContain(t, out, `data-invalid="true"`, `field-error`)
}

func TestFormSectionComposesFieldGroup(t *testing.T) {
	out := renderKids(t, FormSection(FormSectionProps{Title: "Profile", Description: "public info"}), `<div class="f">fields</div>`)
	mustContain(t, out,
		`class="goth-form-section"`, `data-slot="form-section"`,
		`<h2 data-slot="form-section-title">Profile</h2>`,
		`<p data-slot="form-section-description">public info</p>`,
		`data-slot="field-group"`, `<div class="f">fields</div>`)

	// A titleless/descriptionless section omits the header.
	bare := renderKids(t, FormSection(FormSectionProps{}), `x`)
	mustNotContain(t, bare, `form-section-header`)
}

func TestErrorSummaryLinksFields(t *testing.T) {
	out := render(t, ErrorSummary(ErrorSummaryProps{
		Errors: []FieldMessage{
			{Message: "Email is required", FieldID: "email"},
			{Message: "Password too short", FieldID: "password"},
			{Message: "The form could not be saved"}, // form-level, no target
		},
	}))
	mustContain(t, out,
		`goth-alert`, `goth-error-summary`, `data-variant="destructive"`, `role="alert"`,
		"Please fix the following", // default title
		`data-slot="error-summary-list"`,
		`<a href="#email">Email is required</a>`,
		`<a href="#password">Password too short</a>`,
		`<li data-slot="error-summary-item">The form could not be saved</li>`)
}

func TestErrorSummaryEmptyRendersNothing(t *testing.T) {
	out := render(t, ErrorSummary(ErrorSummaryProps{}))
	if strings.TrimSpace(out) != "" {
		t.Errorf("empty ErrorSummary rendered %q", out)
	}
}

func TestFormActionsAlign(t *testing.T) {
	end := renderKids(t, FormActions(FormActionsProps{}), `<button>Save</button>`)
	mustContain(t, end, `class="goth-form-actions"`, `data-slot="form-actions"`, `data-align="end"`, `<button>Save</button>`)

	between := renderKids(t, FormActions(FormActionsProps{Align: FormActionsBetween}), `x`)
	mustContain(t, between, `data-align="between"`)

	start := renderKids(t, FormActions(FormActionsProps{Align: FormActionsStart}), `x`)
	mustContain(t, start, `data-align="start"`)

	// Unknown align falls back to the documented default.
	bad := renderKids(t, FormActions(FormActionsProps{Align: FormActionsAlign("weird")}), `x`)
	mustContain(t, bad, `data-align="end"`)
	if (FormActionsAlign("weird")).Valid() {
		t.Error("unknown align should be invalid")
	}
}
