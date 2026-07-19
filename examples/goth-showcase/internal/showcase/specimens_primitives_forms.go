package showcase

import (
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// This file holds the showcase specimens for the GOTH-2.3 field/data primitives
// (P11 Field, P12 Input, P13 Input Group, P16 Label, P18 Native Select,
// P20 Progress, P24 Table, P25 Textarea). Each specimen renders the real
// primitive component so the browser/axe harness exercises the actual emitted
// surface. All eight are presentational (StylesOnly profile — native form
// controls, no JS); the field specimen is a real <form> that submits with GET so
// the harness can prove ordinary (no-JavaScript) form submission.

func base(id string) primitives.Base { return primitives.Base{ID: id} }

func describedBy(id, describedby string) primitives.Base {
	return primitives.Base{ID: id, Attributes: templ.Attributes{"aria-describedby": describedby}}
}

// fieldSpecimen renders a complete native form: each control is grouped with its
// label, description, and (for one field) an error, wired through for/id and
// aria-describedby. The form submits with GET to the same page — no JavaScript.
func fieldSpecimen() string {
	// Text field with description.
	name := compKids(primitives.Field(primitives.FieldProps{}),
		compKids(primitives.FieldLabel(primitives.FieldLabelProps{For: "f-name"}), "Name")+
			comp(primitives.Input(primitives.InputProps{Base: describedBy("f-name", "f-name-desc"), Name: "name", Placeholder: "Ada Lovelace", Required: true}))+
			compKids(primitives.FieldDescription(primitives.FieldDescriptionProps{Base: base("f-name-desc")}), "Your full name."))

	// Invalid email field with an error message referenced by aria-describedby.
	email := compKids(primitives.Field(primitives.FieldProps{Invalid: true}),
		compKids(primitives.FieldLabel(primitives.FieldLabelProps{For: "f-email"}), "Email")+
			comp(primitives.Input(primitives.InputProps{Base: describedBy("f-email", "f-email-err"), Type: primitives.InputEmail, Name: "email", Invalid: true, Value: "not-an-email"}))+
			compKids(primitives.FieldError(primitives.FieldErrorProps{Base: base("f-email-err")}), "Enter a valid email address."))

	// Native select field.
	country := compKids(primitives.Field(primitives.FieldProps{}),
		compKids(primitives.FieldLabel(primitives.FieldLabelProps{For: "f-country"}), "Country")+
			compKids(primitives.NativeSelect(primitives.NativeSelectProps{Base: base("f-country"), Name: "country"}),
				compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: ""}), "Choose…")+
					compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "us", Selected: true}), "United States")+
					compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "gb"}), "United Kingdom")))

	// Textarea field.
	bio := compKids(primitives.Field(primitives.FieldProps{}),
		compKids(primitives.FieldLabel(primitives.FieldLabelProps{For: "f-bio"}), "Bio")+
			comp(primitives.Textarea(primitives.TextareaProps{Base: base("f-bio"), Name: "bio", Rows: 3, Placeholder: "A short bio"})))

	// Grouped radios in a fieldset/legend.
	plan := compKids(primitives.FieldSet(primitives.FieldSetProps{}),
		compKids(primitives.FieldLegend(primitives.FieldLegendProps{}), "Plan")+
			`<label class="goth-label"><input type="radio" name="plan" value="free" checked> Free</label>`+
			`<label class="goth-label"><input type="radio" name="plan" value="pro"> Pro</label>`)

	submit := `<button class="goth-button" data-slot="button" data-variant="default" data-size="default" type="submit">Submit</button>`

	group := compKids(primitives.FieldGroup(primitives.FieldGroupProps{}), name+email+country+bio+plan+submit)
	form := `<form method="get" action="/specimen/primitive-field">` + group + `</form>`
	return page("Field", `<section data-slot="fields">`+form+`</section>`)
}

func inputSpecimen() string {
	var b strings.Builder
	rows := []struct {
		labelID, forID, label string
		props                 primitives.InputProps
	}{
		{"l-text", "i-text", "Text", primitives.InputProps{Base: base("i-text"), Name: "text", Placeholder: "Type here"}},
		{"l-email", "i-email", "Email", primitives.InputProps{Base: base("i-email"), Type: primitives.InputEmail, Name: "email", Placeholder: "you@example.com"}},
		{"l-password", "i-password", "Password", primitives.InputProps{Base: base("i-password"), Type: primitives.InputPassword, Name: "password"}},
		{"l-search", "i-search", "Search", primitives.InputProps{Base: base("i-search"), Type: primitives.InputSearch, Name: "q", Placeholder: "Search…"}},
		{"l-file", "i-file", "File", primitives.InputProps{Base: base("i-file"), Type: primitives.InputFile, Name: "file"}},
		{"l-invalid", "i-invalid", "Invalid", primitives.InputProps{Base: base("i-invalid"), Name: "invalid", Invalid: true, Value: "bad"}},
		{"l-disabled", "i-disabled", "Disabled", primitives.InputProps{Base: base("i-disabled"), Name: "disabled", Disabled: true, Value: "locked"}},
	}
	for _, r := range rows {
		b.WriteString(compKids(primitives.Field(primitives.FieldProps{}),
			compKids(primitives.Label(primitives.LabelProps{For: r.forID}), r.label)+
				comp(primitives.Input(r.props))))
	}
	return page("Input", `<section data-slot="inputs">`+b.String()+`</section>`)
}

func inputGroupSpecimen() string {
	// URL prefix + copy button.
	urlGroup := compKids(primitives.InputGroup(primitives.InputGroupProps{}),
		compKids(primitives.InputGroupAddon(primitives.InputGroupAddonProps{}),
			compKids(primitives.InputGroupText(primitives.InputGroupTextProps{}), "https://"))+
			comp(primitives.Input(primitives.InputProps{Base: base("ig-url"), Name: "site", Placeholder: "example.com"}))+
			compKids(primitives.InputGroupAddon(primitives.InputGroupAddonProps{Align: primitives.InputGroupAlignEnd}),
				compKids(primitives.InputGroupButton(primitives.InputGroupButtonProps{}), "Copy")))

	// Search input with a trailing icon-only submit button (icon-only requires a Label).
	searchIcon := `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="11" cy="11" r="8"></circle><path d="m21 21-4.3-4.3"></path></svg>`
	searchBtn := compKids(primitives.InputGroupButton(primitives.InputGroupButtonProps{Type: primitives.ButtonTypeSubmit, Label: "Search"}), searchIcon)
	searchGroup := compKids(primitives.InputGroup(primitives.InputGroupProps{}),
		comp(primitives.Input(primitives.InputProps{Base: base("ig-search"), Type: primitives.InputSearch, Name: "q", Placeholder: "Search docs"}))+
			compKids(primitives.InputGroupAddon(primitives.InputGroupAddonProps{Align: primitives.InputGroupAlignEnd}), searchBtn))

	l1 := compKids(primitives.Label(primitives.LabelProps{For: "ig-url"}), "Website")
	l2 := compKids(primitives.Label(primitives.LabelProps{For: "ig-search"}), "Search")
	return page("Input Group", `<section data-slot="input-groups"><p>`+l1+urlGroup+`</p><p>`+l2+searchGroup+`</p></section>`)
}

func labelSpecimen() string {
	plain := compKids(primitives.Label(primitives.LabelProps{For: "lbl-a"}), "Username") +
		comp(primitives.Input(primitives.InputProps{Base: base("lbl-a"), Name: "username"}))
	required := compKids(primitives.Label(primitives.LabelProps{For: "lbl-b", Required: true}), "Email") +
		comp(primitives.Input(primitives.InputProps{Base: base("lbl-b"), Type: primitives.InputEmail, Name: "email", Required: true}))
	disabled := compKids(primitives.Label(primitives.LabelProps{For: "lbl-c", Disabled: true}), "Disabled") +
		comp(primitives.Input(primitives.InputProps{Base: base("lbl-c"), Name: "disabled", Disabled: true}))
	return page("Label", `<section data-slot="labels"><p>`+plain+`</p><p>`+required+`</p><p>`+disabled+`</p></section>`)
}

func nativeSelectSpecimen() string {
	single := compKids(primitives.NativeSelect(primitives.NativeSelectProps{Base: base("ns-a"), Name: "fruit"}),
		compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "apple", Selected: true}), "Apple")+
			compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "banana"}), "Banana")+
			compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "cherry"}), "Cherry"))

	grouped := compKids(primitives.NativeSelect(primitives.NativeSelectProps{Base: base("ns-b"), Name: "city"}),
		compKids(primitives.NativeSelectGroup(primitives.NativeSelectGroupProps{Label: "North America"}),
			compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "nyc"}), "New York")+
				compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "tor"}), "Toronto"))+
			compKids(primitives.NativeSelectGroup(primitives.NativeSelectGroupProps{Label: "Europe"}),
				compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "ldn"}), "London")+
					compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "par"}), "Paris")))

	invalid := compKids(primitives.NativeSelect(primitives.NativeSelectProps{Base: base("ns-c"), Name: "role", Invalid: true, Required: true}),
		compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: ""}), "Choose a role…")+
			compKids(primitives.NativeSelectOption(primitives.NativeSelectOptionProps{Value: "admin"}), "Admin"))

	l1 := compKids(primitives.Label(primitives.LabelProps{For: "ns-a"}), "Fruit")
	l2 := compKids(primitives.Label(primitives.LabelProps{For: "ns-b"}), "City")
	l3 := compKids(primitives.Label(primitives.LabelProps{For: "ns-c"}), "Role")
	return page("Native Select", `<section data-slot="native-selects"><p>`+l1+single+`</p><p>`+l2+grouped+`</p><p>`+l3+invalid+`</p></section>`)
}

func progressSpecimen() string {
	var b strings.Builder
	b.WriteString(comp(primitives.Progress(primitives.ProgressProps{Value: 0, Label: "Zero percent"})))
	b.WriteString(comp(primitives.Progress(primitives.ProgressProps{Value: 40, Label: "Forty percent"})))
	b.WriteString(comp(primitives.Progress(primitives.ProgressProps{Value: 100, Label: "Complete"})))
	b.WriteString(comp(primitives.Progress(primitives.ProgressProps{Value: 3, Max: 12, Label: "Step 3 of 12"})))
	b.WriteString(comp(primitives.Progress(primitives.ProgressProps{Indeterminate: true, Label: "Loading"})))
	return page("Progress", `<section data-slot="progress-bars">`+b.String()+`</section>`)
}

func tableSpecimen() string {
	head := compKids(primitives.TableHeader(primitives.TableHeaderProps{}),
		compKids(primitives.TableRow(primitives.TableRowProps{}),
			compKids(primitives.TableHead(primitives.TableHeadProps{}), "Invoice")+
				compKids(primitives.TableHead(primitives.TableHeadProps{}), "Status")+
				compKids(primitives.TableHead(primitives.TableHeadProps{}), "Amount")))
	rowsData := []struct{ inv, status, amt string }{
		{"INV-001", "Paid", "$250.00"},
		{"INV-002", "Pending", "$150.00"},
		{"INV-003", "Unpaid", "$350.00"},
	}
	var body strings.Builder
	for _, r := range rowsData {
		body.WriteString(compKids(primitives.TableRow(primitives.TableRowProps{}),
			compKids(primitives.TableHead(primitives.TableHeadProps{Scope: "row"}), r.inv)+
				compKids(primitives.TableCell(primitives.TableCellProps{}), r.status)+
				compKids(primitives.TableCell(primitives.TableCellProps{}), r.amt)))
	}
	foot := compKids(primitives.TableFooter(primitives.TableFooterProps{}),
		compKids(primitives.TableRow(primitives.TableRowProps{}),
			compKids(primitives.TableHead(primitives.TableHeadProps{Scope: "row"}), "Total")+
				compKids(primitives.TableCell(primitives.TableCellProps{}), "")+
				compKids(primitives.TableCell(primitives.TableCellProps{}), "$750.00")))
	caption := compKids(primitives.TableCaption(primitives.TableCaptionProps{}), "Recent invoices")
	tbl := compKids(primitives.Table(primitives.TableProps{}),
		caption+head+compKids(primitives.TableBody(primitives.TableBodyProps{}), body.String())+foot)
	return page("Table", `<section data-slot="tables">`+tbl+`</section>`)
}

func textareaSpecimen() string {
	basic := compKids(primitives.Label(primitives.LabelProps{For: "ta-a"}), "Message") +
		comp(primitives.Textarea(primitives.TextareaProps{Base: base("ta-a"), Name: "message", Rows: 4, Placeholder: "Write a message…"}))
	filled := compKids(primitives.Label(primitives.LabelProps{For: "ta-b"}), "Notes") +
		comp(primitives.Textarea(primitives.TextareaProps{Base: base("ta-b"), Name: "notes", Rows: 3, Value: "Prefilled note content."}))
	invalid := compKids(primitives.Label(primitives.LabelProps{For: "ta-c"}), "Invalid") +
		comp(primitives.Textarea(primitives.TextareaProps{Base: base("ta-c"), Name: "bad", Rows: 3, Invalid: true, Value: "too short"}))
	return page("Textarea", `<section data-slot="textareas"><p>`+basic+`</p><p>`+filled+`</p><p>`+invalid+`</p></section>`)
}
