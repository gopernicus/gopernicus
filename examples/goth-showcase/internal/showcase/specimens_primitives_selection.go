package showcase

import (
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// This file holds the showcase specimens for the GOTH-3.2 form-selection
// primitives (P28 Checkbox, P31 Radio Group, P32 Slider, P33 Switch). Every one
// is native (no Alpine controller), so they render under the StylesOnly profile
// and prove the no-JavaScript baseline: each specimen renders the real primitive
// component, and the selection-form specimen is a real <form> that submits with
// GET to an echo route so the harness can prove ordinary (no-JS) submission of the
// native values.

// registerSelectionSpecimens registers the four GOTH-3.2 primitive specimens plus
// the shared no-JS form-submission specimen.
func registerSelectionSpecimens(r *Registry) {
	specs := []struct {
		id, title, prim string
		body            func() string
	}{
		{"checkbox", "Checkbox (P28)", "P28", checkboxSpecimen},
		{"switch", "Switch (P33)", "P33", switchSpecimen},
		{"radio-group", "Radio Group (P31)", "P31", radioGroupSpecimen},
		{"slider", "Slider (P32)", "P32", sliderSpecimen},
	}
	for _, s := range specs {
		r.Register(Specimen{
			ID:        "primitive-" + s.id,
			Title:     s.title,
			Section:   SectionPrimitive,
			Primitive: s.prim,
			Profile:   goth.StylesOnly,
			Body:      s.body,
		})
	}
	// The no-JS form-submission specimen exercises all four controls in one native
	// <form>. It carries no Primitive id (it proves submission, not a single
	// entry) but is still crawled by the axe/CSP harness.
	r.Register(Specimen{
		ID:      "selection-form",
		Title:   "Selection form (no-JS submission)",
		Section: SectionPrimitive,
		Profile: goth.StylesOnly,
		Body:    selectionFormSpecimen,
	})
}

// labelled pairs a control with a Label wired through for/id, so every native
// control has an accessible name and a clickable label.
func labelled(id, text, control string) string {
	return `<div class="goth-field" data-orientation="horizontal">` +
		control +
		compKids(primitives.Label(primitives.LabelProps{For: id}), text) +
		`</div>`
}

func checkboxSpecimen() string {
	var b strings.Builder
	b.WriteString(labelled("cb-terms", "Accept terms",
		comp(primitives.Checkbox(primitives.CheckboxProps{Base: base("cb-terms"), Name: "terms", Value: "yes"}))))
	b.WriteString(labelled("cb-news", "Subscribe (checked)",
		comp(primitives.Checkbox(primitives.CheckboxProps{Base: base("cb-news"), Name: "news", Value: "on", Checked: true}))))
	b.WriteString(labelled("cb-all", "Select all (indeterminate)",
		comp(primitives.Checkbox(primitives.CheckboxProps{Base: base("cb-all"), Name: "all", Indeterminate: true}))))
	b.WriteString(labelled("cb-off", "Unavailable (disabled)",
		comp(primitives.Checkbox(primitives.CheckboxProps{Base: base("cb-off"), Name: "off", Disabled: true}))))
	b.WriteString(labelled("cb-bad", "Required (invalid)",
		comp(primitives.Checkbox(primitives.CheckboxProps{Base: describedBy("cb-bad", "cb-bad-err"), Name: "bad", Required: true, Invalid: true}))))
	b.WriteString(compKids(primitives.FieldError(primitives.FieldErrorProps{Base: base("cb-bad-err")}), "This box is required."))
	return page("Checkbox", `<section data-slot="checkboxes">`+b.String()+`</section>`)
}

func switchSpecimen() string {
	var b strings.Builder
	b.WriteString(labelled("sw-notify", "Email notifications",
		comp(primitives.Switch(primitives.SwitchProps{Base: base("sw-notify"), Name: "notify", Value: "on"}))))
	b.WriteString(labelled("sw-beta", "Beta features (on)",
		comp(primitives.Switch(primitives.SwitchProps{Base: base("sw-beta"), Name: "beta", Value: "on", Checked: true}))))
	b.WriteString(labelled("sw-lock", "Locked (disabled)",
		comp(primitives.Switch(primitives.SwitchProps{Base: base("sw-lock"), Name: "lock", Checked: true, Disabled: true}))))
	return page("Switch", `<section data-slot="switches">`+b.String()+`</section>`)
}

func radioGroupSpecimen() string {
	item := func(id, name, value, label string, checked, disabled bool) string {
		control := comp(primitives.RadioGroupItem(primitives.RadioGroupItemProps{
			Base: base(id), Name: name, Value: value, Checked: checked, Disabled: disabled,
		}))
		return `<div class="goth-field" data-orientation="horizontal">` +
			control + compKids(primitives.Label(primitives.LabelProps{For: id}), label) + `</div>`
	}

	vertical := compKids(primitives.RadioGroup(primitives.RadioGroupProps{
		Base: primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "rg-plan-label"}},
	}),
		item("rg-free", "plan", "free", "Free", true, false)+
			item("rg-pro", "plan", "pro", "Pro", false, false)+
			item("rg-ent", "plan", "enterprise", "Enterprise (unavailable)", false, true))

	horizontal := compKids(primitives.RadioGroup(primitives.RadioGroupProps{
		Orientation: primitives.RadioHorizontal,
		Base:        primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "rg-size-label"}},
	}),
		item("rg-s", "size", "s", "S", false, false)+
			item("rg-m", "size", "m", "M", true, false)+
			item("rg-l", "size", "l", "L", false, false))

	inner := `<h2 id="rg-plan-label">Plan</h2>` + vertical +
		`<h2 id="rg-size-label">Size</h2>` + horizontal
	return page("Radio Group", `<section data-slot="radio-groups">`+inner+`</section>`)
}

func sliderSpecimen() string {
	var b strings.Builder
	// A native <output> shows the server-rendered value. Live sync is a documented
	// deferral (needs JS); the value submits and updates natively regardless.
	b.WriteString(compKids(primitives.Label(primitives.LabelProps{For: "sl-vol"}), "Volume"))
	b.WriteString(comp(primitives.Slider(primitives.SliderProps{Base: base("sl-vol"), Name: "volume", Value: 60})))
	b.WriteString(`<output for="sl-vol" data-slot="slider-output">60</output>`)

	b.WriteString(compKids(primitives.Label(primitives.LabelProps{For: "sl-temp"}), "Temperature (10–30, step 5)"))
	b.WriteString(comp(primitives.Slider(primitives.SliderProps{Base: base("sl-temp"), Name: "temp", Min: 10, Max: 30, Step: 5, Value: 20})))

	b.WriteString(compKids(primitives.Label(primitives.LabelProps{For: "sl-off"}), "Locked (disabled)"))
	b.WriteString(comp(primitives.Slider(primitives.SliderProps{Base: base("sl-off"), Name: "off", Value: 40, Disabled: true})))
	return page("Slider", `<section data-slot="sliders">`+b.String()+`</section>`)
}

// selectionFormSpecimen is a real native form: a checkbox, a switch, a radio
// group, and a slider submit with GET (no JavaScript) to the echo route, which
// reflects the submitted native values back.
func selectionFormSpecimen() string {
	terms := labelled("f-terms", "I accept the terms",
		comp(primitives.Checkbox(primitives.CheckboxProps{Base: base("f-terms"), Name: "terms", Value: "yes"})))
	notify := labelled("f-notify", "Email me updates",
		comp(primitives.Switch(primitives.SwitchProps{Base: base("f-notify"), Name: "notify", Value: "on", Checked: true})))

	radioItem := func(id, value, label string, checked bool) string {
		return `<div class="goth-field" data-orientation="horizontal">` +
			comp(primitives.RadioGroupItem(primitives.RadioGroupItemProps{Base: base(id), Name: "plan", Value: value, Checked: checked})) +
			compKids(primitives.Label(primitives.LabelProps{For: id}), label) + `</div>`
	}
	plan := compKids(primitives.RadioGroup(primitives.RadioGroupProps{
		Base: primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "f-plan-label"}},
	}), radioItem("f-plan-free", "free", "Free", true)+radioItem("f-plan-pro", "pro", "Pro", false))

	slider := compKids(primitives.Label(primitives.LabelProps{For: "f-volume"}), "Volume") +
		comp(primitives.Slider(primitives.SliderProps{Base: base("f-volume"), Name: "volume", Value: 30}))

	submit := `<button class="goth-button" data-slot="button" data-variant="default" data-size="default" type="submit">Submit</button>`

	form := `<form method="get" action="/selection/echo" data-slot="selection-form">` +
		terms + notify +
		`<h2 id="f-plan-label">Plan</h2>` + plan +
		slider + submit + `</form>`
	return page("Selection form", `<section data-slot="selection-form-section">`+form+`</section>`)
}

// registerSelectionFixtures wires the echo route the no-JS form-submission
// specimen posts to. It reflects the submitted native values as HTML so the
// browser harness can assert real submission with no JavaScript.
func (s *Server) registerSelectionFixtures() {
	s.handler.Handle(http.MethodGet, "/selection/echo", func(w http.ResponseWriter, r *http.Request) {
				bundle := s.bundles[goth.StylesOnly]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")

		q := r.URL.Query()
		var b strings.Builder
		b.WriteString(`<main data-slot="selection-echo"><h1>Submitted</h1><dl>`)
		for _, name := range []string{"terms", "notify", "plan", "volume"} {
			val := q.Get(name)
			b.WriteString(`<dt>` + templ.EscapeString(name) + `</dt>`)
			b.WriteString(`<dd data-field="` + templ.EscapeString(name) + `">` + templ.EscapeString(val) + `</dd>`)
		}
		b.WriteString(`</dl><a href="/specimen/selection-form">Back</a></main>`)

		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — submitted"}, rawComponent(b.String()))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}
