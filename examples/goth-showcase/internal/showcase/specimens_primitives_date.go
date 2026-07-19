package showcase

import (
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/ui/goth"
	"github.com/gopernicus/gopernicus/ui/goth/htmx"
	"github.com/gopernicus/gopernicus/ui/goth/primitives"
)

// This file holds the GOTH-5.1 date primitives' showcase specimens (P49 Calendar,
// P53 Date Picker). The server owns every date computation and the parse/format
// contract: Go renders the month grid and weekday order, and the fixture routes
// parse/format the submitted value. Each specimen renders the REAL primitive so the
// browser/axe harness exercises the actual emitted surface.
//
// Deterministic clock: the showcase pins "today" to 2026-07-17 and the default
// month to July 2026 so the specimens and tests never depend on the wall clock.

const (
	dateFormat      = "2006-01-02"
	dateMonthFormat = "2006-01"
	dateHumanFormat = "Monday, January 2, 2006"
)

var (
	showcaseToday        = time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC)
	showcaseDefaultMonth = time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
)

// registerDateSpecimens registers the Calendar and Date Picker specimens.
func registerDateSpecimens(r *Registry) {
	// Calendar (P49) runs under Interactive so the browser exercises the real
	// gothRovingFocus grid enhancement over the server-rendered no-JS baseline.
	r.Register(Specimen{
		ID:        "primitive-calendar",
		Title:     "Calendar (P49)",
		Section:   SectionPrimitive,
		Primitive: "P49",
		Profile:   goth.Interactive,
		Body:      calendarSpecimen,
	})

	// Date Picker (P53) under Full: the calendar day buttons carry HTMX attributes
	// so selection swaps the picker fragment; the native popover dismiss closes the
	// panel with no JavaScript either way.
	r.Register(Specimen{
		ID:           "primitive-date-picker",
		Title:        "Date Picker (P53)",
		Section:      SectionPrimitive,
		Primitive:    "P53",
		Profile:      goth.Full,
		AllowConnect: true,
		Body:         datePickerSpecimen,
	})

	// A StylesOnly Date Picker proves the pure no-JS baseline: native popover +
	// native form GET submission, with Alpine and HTMX entirely absent.
	r.Register(Specimen{
		ID:        "primitive-date-picker-nojs",
		Title:     "Date Picker no-JS (P53)",
		Section:   SectionPrimitive,
		Primitive: "P53",
		Profile:   goth.StylesOnly,
		Body:      datePickerNoJSSpecimen,
	})
}

// calendarComponent renders a July-2026 calendar with a selected day, today, and a
// min/max window (so disabled days show at both ends), wrapped in a form so month
// navigation and day selection round-trip to the server with no JavaScript.
func calendarComponent(month, selected time.Time) string {
	cal := comp(primitives.Calendar(primitives.CalendarProps{
		Base:     primitives.Base{ID: "showcase-calendar"},
		Month:    month,
		Selected: selected,
		Today:    showcaseToday,
		Min:      time.Date(2026, time.July, 6, 0, 0, 0, 0, time.UTC),
		Max:      time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC),
		Label:    "Choose a date",
	}))
	return `<form method="get" action="/calendar/select" data-slot="calendar-form">` + cal + `</form>`
}

func calendarSpecimen() string {
	selected := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	body := `<section data-slot="calendar-specimen">` +
		`<p>The server renders the month grid; arrow keys rove the grid, and prev/next and day selection submit to the server.</p>` +
		calendarComponent(showcaseDefaultMonth, selected) +
		`</section>`
	return page("Calendar", body)
}

// datePickerInner returns the swappable #dp-fragment: the field, trigger, native
// popover, and Calendar. When htmxEnhanced, the calendar day buttons swap this
// fragment; otherwise selection is a plain server round-trip. The server owns the
// formatted value and the invalid state.
func datePickerInner(month, selected time.Time, value string, invalid bool, htmxEnhanced bool) string {
	var dayAttrs templ.Attributes
	if htmxEnhanced {
		dayAttrs = templ.Attributes{
			"hx-get":    "/datepicker/pick",
			"hx-target": "#dp-fragment",
			"hx-swap":   "outerHTML",
		}
	}
	cal := comp(primitives.Calendar(primitives.CalendarProps{
		Base:          primitives.Base{ID: "dp-calendar"},
		Month:         month,
		Selected:      selected,
		Today:         showcaseToday,
		Name:          "date",
		Label:         "Choose a date",
		DismissTarget: "dp-pop",
		DayAttributes: dayAttrs,
	}))

	input := comp(primitives.DatePickerInput(primitives.DatePickerInputProps{
		Base:        primitives.Base{ID: "dp-input"},
		Name:        "date-text",
		Value:       value,
		Format:      "YYYY-MM-DD",
		Invalid:     invalid,
		Describedby: describedbyFor(invalid),
	}))
	trigger := compKids(primitives.DatePickerTrigger(primitives.DatePickerTriggerProps{
		Base:   primitives.Base{ID: "dp-trigger"},
		Target: "dp-pop",
	}), calendarIcon())
	content := compKids(primitives.DatePickerContent(primitives.DatePickerContentProps{
		Base: primitives.Base{ID: "dp-pop"},
	}), cal)

	picker := compKids(primitives.DatePicker(primitives.DatePickerProps{Base: primitives.Base{ID: "dp"}}),
		input+trigger+content)

	var errHTML string
	if invalid {
		errHTML = `<p id="dp-error" role="alert" data-slot="date-picker-error">Enter a date as YYYY-MM-DD.</p>`
	}
	return `<div id="dp-fragment" data-slot="date-picker-fragment">` + picker + errHTML + `</div>`
}

func describedbyFor(invalid bool) string {
	if invalid {
		return "dp-error"
	}
	return ""
}

// calendarIcon is a small decorative calendar glyph for the trigger button.
func calendarIcon() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><rect x="3" y="4" width="18" height="18" rx="2"></rect><path d="M16 2v4M8 2v4M3 10h18"></path></svg>`
}

func datePickerFormBody(inner string) string {
	return `<form method="get" action="/datepicker/pick" data-slot="date-picker-form">` + inner + `</form>`
}

func datePickerSpecimen() string {
	selected := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	inner := datePickerInner(showcaseDefaultMonth, selected, "2026-07-15", false, true)
	body := `<section data-slot="date-picker-specimen">` +
		`<p>Type a date or open the calendar. Selecting a day swaps the field via HTMX and closes the popover; with no JavaScript the same submission reloads the page.</p>` +
		datePickerFormBody(inner) + `</section>`
	return page("Date Picker", body)
}

func datePickerNoJSSpecimen() string {
	selected := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	inner := datePickerInner(showcaseDefaultMonth, selected, "2026-07-15", false, false)
	body := `<section data-slot="date-picker-nojs-specimen">` +
		`<p>No JavaScript: the native popover opens the calendar, and a day submits the form (GET) to the server.</p>` +
		datePickerFormBody(inner) + `</section>`
	return page("Date Picker (no-JS)", body)
}

// parseMonth parses a "2006-01" value, falling back to the default July 2026 month.
func parseMonth(v string) time.Time {
	if t, err := time.Parse(dateMonthFormat, v); err == nil {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return showcaseDefaultMonth
}

// registerDateFixtures wires the server-owned no-JS/HTMX round-trip routes. The
// server parses and formats every submitted value — the primitives never do.
func (s *Server) registerDateFixtures() {
	// /calendar/select re-renders the calendar for the submitted month and echoes
	// the selected date. Prev/next and day selection both round-trip here, so the
	// no-JS calendar has shareable URLs and full-document navigation.
	s.handler.Handle(http.MethodGet, "/calendar/select", func(w http.ResponseWriter, r *http.Request) {
				bundle := s.bundles[goth.Interactive]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, false))
		w.Header().Set("X-Content-Type-Options", "nosniff")

		month := parseMonth(r.URL.Query().Get("month"))
		var selected time.Time
		var echo string
		if raw := r.URL.Query().Get("date"); raw != "" {
			if d, err := time.Parse(dateFormat, raw); err == nil {
				selected = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
				month = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
				echo = `<p data-slot="calendar-echo" data-field="date" role="status">Selected: ` +
					templ.EscapeString(selected.Format(dateHumanFormat)) + `</p>`
			}
		}
		body := `<main data-slot="calendar-select"><h1>Calendar</h1>` + echo +
			calendarComponent(month, selected) +
			`<p><a href="/specimen/primitive-calendar">Back</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — calendar"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})

	// /datepicker/pick parses the selection (a day button's date, else the typed
	// date-text) and re-renders the picker. An HTMX request gets the swappable
	// fragment; a direct request gets the full document (history re-fetch correct).
	s.handler.Handle(http.MethodGet, "/datepicker/pick", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		month := parseMonth(q.Get("month"))
		var selected time.Time
		var value string
		invalid := false

		raw := q.Get("date")
		manual := false
		if raw == "" {
			raw = strings.TrimSpace(q.Get("date-text"))
			manual = raw != ""
		}
		if raw != "" {
			if d, err := time.Parse(dateFormat, raw); err == nil {
				selected = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
				month = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
				value = selected.Format(dateFormat)
			} else if manual {
				value = raw
				invalid = true
			}
		}

		fragment := datePickerInner(month, selected, value, invalid, true)
		if htmx.FromRequest(r).IsHTMX() {
			writeFragment(w, http.StatusOK, fragment)
			return
		}
				bundle := s.bundles[goth.Full]
		w.Header().Set("Content-Security-Policy", buildCSP(bundle, true))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		body := `<main data-slot="date-picker-page"><h1>Date Picker</h1>` +
			datePickerFormBody(fragment) +
			`<p><a href="/specimen/primitive-date-picker">Back</a></p></main>`
		doc := bundle.Document(goth.DocumentOptions{Title: "goth showcase — date picker"}, rawComponent(body))
		web.Render(r.Context(), w, http.StatusOK, doc)
	})
}
