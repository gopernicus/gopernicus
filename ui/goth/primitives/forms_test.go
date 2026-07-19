package primitives

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// TestNoFormPrimitiveEmitsInlineStyle proves invariant (a): no GOTH-2.3
// field/data primitive emits an inline style= attribute in any state — including
// Progress, whose geometry is a native value/max attribute, never a style.
func TestNoFormPrimitiveEmitsInlineStyle(t *testing.T) {
	outs := []string{
		renderKids(t, Label(LabelProps{For: "email", Required: true}), "Email"),
		render(t, Input(InputProps{Type: InputEmail, Name: "email", Invalid: true})),
		render(t, Input(InputProps{Type: InputFile, Name: "f"})),
		render(t, Textarea(TextareaProps{Name: "bio", Rows: 4, Value: "hi"})),
		render(t, Progress(ProgressProps{Value: 40})),
		render(t, Progress(ProgressProps{Indeterminate: true})),
		renderKids(t, NativeSelect(NativeSelectProps{Name: "c"}), "x"),
		renderKids(t, Field(FieldProps{Invalid: true}), "x"),
		renderKids(t, InputGroup(InputGroupProps{}), "x"),
		renderKids(t, Table(TableProps{}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("primitive emitted an inline style=: %s", o)
		}
	}
}

func TestLabel(t *testing.T) {
	out := renderKids(t, Label(LabelProps{For: "email", Required: true}), "Email")
	mustContain(t, out, `<label`, `class="goth-label"`, `data-slot="label"`, `for="email"`, ">Email", `class="goth-label-required"`, `aria-hidden="true"`)

	// A non-required label carries no indicator.
	plain := renderKids(t, Label(LabelProps{For: "name"}), "Name")
	mustNotContain(t, plain, `goth-label-required`)

	// Disabled marks data-disabled for styling.
	dis := renderKids(t, Label(LabelProps{For: "x", Disabled: true}), "X")
	mustContain(t, dis, `data-disabled="true"`)
}

func TestInputRender(t *testing.T) {
	out := render(t, Input(InputProps{Type: InputEmail, Name: "email", Placeholder: "you@example.com", Required: true}))
	mustContain(t, out, `<input`, `class="goth-input"`, `data-slot="input"`, `type="email"`, `name="email"`, `placeholder="you@example.com"`, `required`)

	// Zero type renders text; unknown type falls back to text without panicking.
	def := render(t, Input(InputProps{Type: InputType("weird")}))
	mustContain(t, def, `type="text"`)

	// Invalid state exposes both aria-invalid and data-invalid.
	inv := render(t, Input(InputProps{Invalid: true}))
	mustContain(t, inv, `aria-invalid="true"`, `data-invalid="true"`)

	// Disabled/readonly native attributes.
	dr := render(t, Input(InputProps{Disabled: true, Readonly: true}))
	mustContain(t, dr, `disabled`, `readonly`)
}

// TestInputPasswordNeverEchoesValue proves no secret-value repopulation: a
// password Input never emits its Value back into the markup, while an ordinary
// text input does.
func TestInputPasswordNeverEchoesValue(t *testing.T) {
	secret := render(t, Input(InputProps{Type: InputPassword, Name: "password", Value: "hunter2"}))
	mustContain(t, secret, `type="password"`)
	mustNotContain(t, secret, "hunter2", `value=`)

	text := render(t, Input(InputProps{Type: InputText, Name: "q", Value: "hello"}))
	mustContain(t, text, `value="hello"`)
}

func TestTextarea(t *testing.T) {
	out := render(t, Textarea(TextareaProps{Name: "bio", Rows: 5, Placeholder: "About you", Value: "hi & bye"}))
	mustContain(t, out, `<textarea`, `class="goth-textarea"`, `data-slot="textarea"`, `name="bio"`, `rows="5"`, `placeholder="About you"`, "hi &amp; bye")

	inv := render(t, Textarea(TextareaProps{Invalid: true, Disabled: true}))
	mustContain(t, inv, `aria-invalid="true"`, `data-invalid="true"`, `disabled`)

	// Zero rows omits the attribute (UA default).
	norows := render(t, Textarea(TextareaProps{}))
	mustNotContain(t, norows, `rows=`)
}

func TestProgress(t *testing.T) {
	// Determinate: native <progress> with value/max (geometry via attribute).
	det := render(t, Progress(ProgressProps{Value: 40, Label: "Upload"}))
	mustContain(t, det, `<progress`, `class="goth-progress"`, `data-slot="progress"`, `data-state="determinate"`, `value="40"`, `max="100"`, `aria-label="Upload"`, "40%")

	// Value clamps to [0, max] and Max defaults to 100.
	over := render(t, Progress(ProgressProps{Value: 250}))
	mustContain(t, over, `value="100"`)
	under := render(t, Progress(ProgressProps{Value: -5}))
	mustContain(t, under, `value="0"`)

	// Custom max scales the fallback percentage.
	scaled := render(t, Progress(ProgressProps{Value: 3, Max: 12}))
	mustContain(t, scaled, `max="12"`, `value="3"`, "25%")

	// Indeterminate omits value and marks the running state.
	ind := render(t, Progress(ProgressProps{Indeterminate: true}))
	mustContain(t, ind, `data-state="indeterminate"`, "Loading")
	mustNotContain(t, ind, `value=`)
}

func TestNativeSelect(t *testing.T) {
	sel := renderKids(t, NativeSelect(NativeSelectProps{Name: "country", Required: true, Invalid: true}), "x")
	mustContain(t, sel, `<select`, `class="goth-native-select"`, `data-slot="native-select"`, `name="country"`, `required`, `aria-invalid="true"`, `data-invalid="true"`)

	opt := renderKids(t, NativeSelectOption(NativeSelectOptionProps{Value: "us", Selected: true}), "United States")
	mustContain(t, opt, `<option`, `data-slot="native-select-option"`, `value="us"`, `selected`, ">United States<")

	disOpt := renderKids(t, NativeSelectOption(NativeSelectOptionProps{Value: "x", Disabled: true}), "Nope")
	mustContain(t, disOpt, `disabled`)

	grp := renderKids(t, NativeSelectGroup(NativeSelectGroupProps{Label: "North America"}), "opts")
	mustContain(t, grp, `<optgroup`, `data-slot="native-select-group"`, `label="North America"`)
}

// TestFieldAriaLinkage proves the frozen ARIA-linkage rule: the label's `for`
// targets the control id, and the control references the description/error ids
// through caller-passed aria-describedby via Base.ID — no ambient context key.
func TestFieldAriaLinkage(t *testing.T) {
	field := renderKids(t, Field(FieldProps{Invalid: true}), "x")
	mustContain(t, field, `class="goth-field"`, `data-slot="field"`, `data-orientation="vertical"`, `data-invalid="true"`)

	lbl := renderKids(t, FieldLabel(FieldLabelProps{For: "email"}), "Email")
	mustContain(t, lbl, `<label`, `data-slot="field-label"`, `for="email"`, ">Email")

	// The control carries the id the label points at and describedby wiring
	// through the Base.Attributes escape hatch (the frozen linkage channel).
	control := render(t, Input(InputProps{
		Base: Base{ID: "email", Attributes: templ.Attributes{"aria-describedby": "email-desc email-err"}},
		Type: InputEmail, Name: "email", Invalid: true,
	}))
	mustContain(t, control, `id="email"`, `aria-describedby="email-desc email-err"`, `aria-invalid="true"`)

	desc := renderKids(t, FieldDescription(FieldDescriptionProps{Base: Base{ID: "email-desc"}}), "We never share it.")
	mustContain(t, desc, `id="email-desc"`, `data-slot="field-description"`, "We never share it.")

	errMsg := renderKids(t, FieldError(FieldErrorProps{Base: Base{ID: "email-err"}}), "Enter a valid email.")
	mustContain(t, errMsg, `id="email-err"`, `data-slot="field-error"`, "Enter a valid email.")

	// Horizontal orientation and fieldset/legend grouping.
	horiz := renderKids(t, Field(FieldProps{Orientation: FieldHorizontal}), "x")
	mustContain(t, horiz, `data-orientation="horizontal"`)

	set := renderKids(t, FieldSet(FieldSetProps{}), "x")
	mustContain(t, set, `<fieldset`, `data-slot="field-set"`)
	legend := renderKids(t, FieldLegend(FieldLegendProps{}), "Contact")
	mustContain(t, legend, `<legend`, `data-slot="field-legend"`, ">Contact")
}

func TestInputGroup(t *testing.T) {
	grp := renderKids(t, InputGroup(InputGroupProps{Invalid: true}), "x")
	mustContain(t, grp, `class="goth-input-group"`, `data-slot="input-group"`, `role="group"`, `data-invalid="true"`)

	addonStart := renderKids(t, InputGroupAddon(InputGroupAddonProps{}), "x")
	mustContain(t, addonStart, `data-slot="input-group-addon"`, `data-align="start"`)
	addonEnd := renderKids(t, InputGroupAddon(InputGroupAddonProps{Align: InputGroupAlignEnd}), "x")
	mustContain(t, addonEnd, `data-align="end"`)

	text := renderKids(t, InputGroupText(InputGroupTextProps{}), "https://")
	mustContain(t, text, `data-slot="input-group-text"`, "https://")

	// A text button takes its name from children; type defaults to button.
	btn := renderKids(t, InputGroupButton(InputGroupButtonProps{}), "Copy")
	mustContain(t, btn, `<button`, `data-slot="input-group-button"`, `type="button"`, ">Copy<")

	// An icon-only button carries an aria-label; a submit type is honored.
	icon := renderKids(t, InputGroupButton(InputGroupButtonProps{Type: ButtonTypeSubmit, Label: "Search"}), "<svg></svg>")
	mustContain(t, icon, `type="submit"`, `aria-label="Search"`, "<svg>")
}

func TestInputGroupAlignValid(t *testing.T) {
	for _, a := range []InputGroupAlign{InputGroupAlignStart, InputGroupAlignEnd} {
		if !a.Valid() {
			t.Errorf("%q should be valid", a)
		}
	}
	if InputGroupAlign("center").Valid() {
		t.Error("center should be invalid")
	}
}

func TestInputTypeValid(t *testing.T) {
	for _, ty := range []InputType{InputText, InputEmail, InputPassword, InputSearch, InputTel, InputURL, InputNumber, InputFile, InputDate, InputTime} {
		if !ty.Valid() {
			t.Errorf("%q should be valid", ty)
		}
	}
	if InputType("color").Valid() {
		t.Error("color should be invalid (not in the frozen set)")
	}
}

func TestTable(t *testing.T) {
	tbl := renderKids(t, Table(TableProps{}), "rows")
	mustContain(t, tbl, `class="goth-table-wrapper"`, `data-slot="table-container"`, `<table`, `class="goth-table"`, `data-slot="table"`)

	cap := renderKids(t, TableCaption(TableCaptionProps{}), "Users")
	mustContain(t, cap, `<caption`, `data-slot="table-caption"`, ">Users")

	head := renderKids(t, TableHeader(TableHeaderProps{}), "x")
	mustContain(t, head, `<thead`, `data-slot="table-header"`)
	body := renderKids(t, TableBody(TableBodyProps{}), "x")
	mustContain(t, body, `<tbody`, `data-slot="table-body"`)
	foot := renderKids(t, TableFooter(TableFooterProps{}), "x")
	mustContain(t, foot, `<tfoot`, `data-slot="table-footer"`)
	row := renderKids(t, TableRow(TableRowProps{}), "x")
	mustContain(t, row, `<tr`, `data-slot="table-row"`)

	// Column header defaults to scope="col".
	th := renderKids(t, TableHead(TableHeadProps{}), "Name")
	mustContain(t, th, `<th`, `data-slot="table-head"`, `scope="col"`, ">Name")
	// Row header scope.
	rowTh := renderKids(t, TableHead(TableHeadProps{Scope: "row"}), "R")
	mustContain(t, rowTh, `scope="row"`)
	// Explicit "none" omits scope.
	noScope := renderKids(t, TableHead(TableHeadProps{Scope: "none"}), "R")
	mustNotContain(t, noScope, `scope=`)

	cell := renderKids(t, TableCell(TableCellProps{}), "Ada")
	mustContain(t, cell, `<td`, `data-slot="table-cell"`, ">Ada")
}

// TestFormBaseMergeAndClass proves the shared Base contract on a form primitive:
// owned attributes win, a caller class in Attributes is dropped, Base.Class
// appends after the stable class, and Base.ID is honored.
func TestFormBaseMergeAndClass(t *testing.T) {
	out := render(t, Input(InputProps{
		Base: Base{
			ID:    "email",
			Class: "w-full",
			Attributes: templ.Attributes{
				"class":       "sneaky",   // dropped
				"data-slot":   "override", // owned wins
				"data-testid": "keep",
			},
		},
		Name: "email",
	}))
	mustContain(t, out, `id="email"`, `class="goth-input w-full"`, `data-slot="input"`, `data-testid="keep"`)
	mustNotContain(t, out, "sneaky", `data-slot="override"`)
}
