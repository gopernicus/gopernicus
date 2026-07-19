package showcase

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// This file holds the showcase specimens for the GOTH-3.3 compact-selection
// primitives (P30 Input OTP, P35 Toggle, P36 Toggle Group). Input OTP and Toggle
// are entirely native (no controller), so they render under the StylesOnly profile
// and prove the no-JavaScript baseline. Toggle Group runs under the Interactive
// profile because its multiple mode binds the frozen gothRovingFocus controller for
// a single tab stop; its single mode is native radios. The compact-form specimen is
// a real <form> that submits with GET to an echo route so the harness proves
// ordinary (no-JS) submission of the native values — the OTP is reported by count
// only, never echoed (no secret echo).

// registerCompactSpecimens registers the three GOTH-3.3 primitive specimens plus
// the shared no-JS compact-form submission specimen.
func registerCompactSpecimens(r *Registry) {
	r.Register(Specimen{
		ID:        "primitive-input-otp",
		Title:     "Input OTP (P30)",
		Section:   SectionPrimitive,
		Primitive: "P30",
		Profile:   goth.StylesOnly,
		Body:      inputOTPSpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-toggle",
		Title:     "Toggle (P35)",
		Section:   SectionPrimitive,
		Primitive: "P35",
		Profile:   goth.StylesOnly,
		Body:      toggleSpecimen,
	})
	r.Register(Specimen{
		ID:        "primitive-toggle-group",
		Title:     "Toggle Group (P36)",
		Section:   SectionPrimitive,
		Primitive: "P36",
		Profile:   goth.Interactive,
		Body:      toggleGroupSpecimen,
	})
	// The no-JS compact form submits a toggle, a single toggle group, and an OTP in
	// one native <form>. It carries no Primitive id (it proves submission) but is
	// still crawled by the axe/CSP harness.
	r.Register(Specimen{
		ID:      "compact-form",
		Title:   "Compact form (no-JS submission)",
		Section: SectionPrimitive,
		Profile: goth.StylesOnly,
		Body:    compactFormSpecimen,
	})
}

// otpSlots renders six native single-character slots sharing one Name, with a
// separator after the third and one-time-code autocomplete on the first.
func otpSlots(prefix, name string, invalid bool) string {
	var b strings.Builder
	for i := 1; i <= 6; i++ {
		// Each slot carries its own accessible name; the group aria-labelledby names
		// the set, but each native input still needs a label of its own.
		slotBase := primitives.Base{
			ID:         prefix + "-" + strconv.Itoa(i),
			Attributes: templ.Attributes{"aria-label": "Digit " + strconv.Itoa(i) + " of 6"},
		}
		b.WriteString(comp(primitives.InputOTPSlot(primitives.InputOTPSlotProps{
			Base:         slotBase,
			Name:         name,
			Autocomplete: i == 1,
			Invalid:      invalid,
		})))
		if i == 3 {
			b.WriteString(comp(primitives.InputOTPSeparator(primitives.InputOTPSeparatorProps{})))
		}
	}
	return b.String()
}

func inputOTPSpecimen() string {
	valid := compKids(primitives.InputOTP(primitives.InputOTPProps{
		Base: primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "otp-label"}},
	}), otpSlots("otp", "code", false))

	// The invalid group renders no digits (no secret echo) and a generic error.
	invalid := compKids(primitives.InputOTP(primitives.InputOTPProps{
		Base: primitives.Base{Attributes: templ.Attributes{
			"aria-labelledby":  "otp-bad-label",
			"aria-describedby": "otp-bad-err",
		}},
	}), otpSlots("otp-bad", "badcode", true))
	err := compKids(primitives.FieldError(primitives.FieldErrorProps{Base: base("otp-bad-err")}), "That code was not recognized. Request a new one.")

	inner := `<h2 id="otp-label">Verification code</h2>` + valid +
		`<h2 id="otp-bad-label">Invalid code</h2>` + invalid + err
	return page("Input OTP", `<section data-slot="input-otps">`+inner+`</section>`)
}

func toggleSpecimen() string {
	var b strings.Builder
	b.WriteString(compKids(primitives.Toggle(primitives.ToggleProps{Base: base("tg-bold"), Name: "bold", Value: "on"}), "<strong>B</strong>"))
	b.WriteString(compKids(primitives.Toggle(primitives.ToggleProps{Base: base("tg-italic"), Name: "italic", Value: "on", Pressed: true}), "<em>I</em>"))
	b.WriteString(compKids(primitives.Toggle(primitives.ToggleProps{Base: base("tg-underline"), Name: "underline", Value: "on", Variant: primitives.ToggleOutline}), "U"))
	b.WriteString(compKids(primitives.Toggle(primitives.ToggleProps{Base: base("tg-lg"), Name: "big", Value: "on", Size: primitives.ToggleSizeLarge}), "Large"))
	b.WriteString(compKids(primitives.Toggle(primitives.ToggleProps{Base: base("tg-off"), Name: "off", Disabled: true}), "Off"))
	return page("Toggle", `<section data-slot="toggles">`+b.String()+`</section>`)
}

func toggleGroupSpecimen() string {
	alignItem := func(id, value, label string, pressed bool) string {
		return compKids(primitives.ToggleGroupItem(primitives.ToggleGroupItemProps{
			Base: base(id), Type: primitives.ToggleGroupSingle, Name: "align", Value: value, Pressed: pressed,
		}), label)
	}
	single := compKids(primitives.ToggleGroup(primitives.ToggleGroupProps{
		Base: primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "tg-align-label"}},
	}), alignItem("tga-left", "left", "Left", true)+
		alignItem("tga-center", "center", "Center", false)+
		alignItem("tga-right", "right", "Right", false))

	fmtItem := func(id, value, label string, pressed bool) string {
		return compKids(primitives.ToggleGroupItem(primitives.ToggleGroupItemProps{
			Base: base(id), Type: primitives.ToggleGroupMultiple, Name: "format", Value: value, Pressed: pressed, Variant: primitives.ToggleOutline,
		}), label)
	}
	multiple := compKids(primitives.ToggleGroup(primitives.ToggleGroupProps{
		Type:    primitives.ToggleGroupMultiple,
		Variant: primitives.ToggleOutline,
		Base:    primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "tg-format-label"}},
	}), fmtItem("tgf-bold", "bold", "B", true)+
		fmtItem("tgf-italic", "italic", "I", false)+
		fmtItem("tgf-underline", "underline", "U", false))

	inner := `<h2 id="tg-align-label">Alignment (single)</h2>` + single +
		`<h2 id="tg-format-label">Format (multiple)</h2>` + multiple
	return page("Toggle Group", `<section data-slot="toggle-groups">`+inner+`</section>`)
}

// compactFormSpecimen is a real native form: a Toggle, a single Toggle Group, and an
// Input OTP submit with GET (no JavaScript) to the echo route.
func compactFormSpecimen() string {
	bold := compKids(primitives.Toggle(primitives.ToggleProps{Base: base("cf-bold"), Name: "bold", Value: "on"}), "<strong>B</strong>")

	alignItem := func(id, value, label string, pressed bool) string {
		return compKids(primitives.ToggleGroupItem(primitives.ToggleGroupItemProps{
			Base: base(id), Type: primitives.ToggleGroupSingle, Name: "align", Value: value, Pressed: pressed,
		}), label)
	}
	align := compKids(primitives.ToggleGroup(primitives.ToggleGroupProps{
		Base: primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "cf-align-label"}},
	}), alignItem("cf-left", "left", "Left", true)+
		alignItem("cf-center", "center", "Center", false)+
		alignItem("cf-right", "right", "Right", false))

	otp := compKids(primitives.InputOTP(primitives.InputOTPProps{
		Base: primitives.Base{Attributes: templ.Attributes{"aria-labelledby": "cf-otp-label"}},
	}), otpSlots("cf-otp", "otp", false))

	submit := `<button class="goth-button" data-slot="button" data-variant="default" data-size="default" type="submit">Submit</button>`
	form := `<form method="get" action="/compact/echo" data-slot="compact-form">` +
		`<h2 id="cf-bold-label">Formatting</h2>` + bold +
		`<h2 id="cf-align-label">Alignment</h2>` + align +
		`<h2 id="cf-otp-label">One-time code</h2>` + otp +
		submit + `</form>`
	return page("Compact form", `<section data-slot="compact-form-section">`+form+`</section>`)
}

// registerCompactFixtures wires the echo route the no-JS compact-form specimen
// posts to. It reflects the toggle and toggle-group values and the OTP slot COUNT —
// never the entered digits (no secret echo) — so the harness can assert real no-JS
// submission.
func (s *Server) registerCompactFixtures() {
	s.handler.Handle(http.MethodGet, "/compact/echo", func(w http.ResponseWriter, r *http.Request) {
				bundle := s.bundles[goth.StylesOnly]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")

		q := r.URL.Query()
		otpCount := 0
		for _, v := range q["otp"] {
			if v != "" {
				otpCount++
			}
		}

		var b strings.Builder
		b.WriteString(`<main data-slot="compact-echo"><h1>Submitted</h1><dl>`)
		for _, f := range []struct{ name, val string }{{"bold", q.Get("bold")}, {"align", q.Get("align")}} {
			b.WriteString(`<dt>` + f.name + `</dt>`)
			b.WriteString(`<dd data-field="` + f.name + `">` + templ.EscapeString(f.val) + `</dd>`)
		}
		b.WriteString(`<dt>otp</dt><dd data-field="otp-count">` + strconv.Itoa(otpCount) + `</dd>`)
		b.WriteString(`</dl><a href="/specimen/compact-form">Back</a></main>`)

		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — submitted"}, rawComponent(b.String()))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}
