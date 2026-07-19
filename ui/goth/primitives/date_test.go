package primitives

import (
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
)

// julyMonth is a fixed instant inside July 2026 (July 1 2026 is a Wednesday), so
// every calendar test is deterministic — the primitive holds no clock.
var julyMonth = time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestCalendarServerOwnedGrid proves the server renders the month grid, weekday
// order, caption, and month-navigation values — all with no JavaScript.
func TestCalendarServerOwnedGrid(t *testing.T) {
	out := render(t, Calendar(CalendarProps{Month: julyMonth}))
	mustContain(t, out,
		`class="goth-calendar"`, `data-slot="calendar"`, `role="group"`, `aria-label="Calendar"`,
		`data-slot="calendar-caption"`, "July 2026",
		`role="grid"`, `data-slot="calendar-grid"`, `aria-label="July 2026"`,
		`data-orientation="grid"`, `data-columns="7"`, `x-data="gothRovingFocus"`,
		`role="row"`, `role="columnheader"`, `role="gridcell"`,
	)

	// Default week start is Sunday: the first weekday header is "Su".
	firstHdr := out[strings.Index(out, `data-slot="calendar-weekday"`):]
	mustContain(t, firstHdr, `aria-label="Sunday"`, ">Su<")

	// Month navigation values are the adjacent months (2006-01), server-produced.
	mustContain(t, out,
		`data-slot="calendar-prev"`, `name="month"`, `value="2026-06"`, `aria-label="Previous month"`,
		`data-slot="calendar-next"`, `value="2026-08"`, `aria-label="Next month"`,
	)
}

// TestCalendarWeekStart proves the weekday header order follows WeekStart.
func TestCalendarWeekStart(t *testing.T) {
	out := render(t, Calendar(CalendarProps{Month: julyMonth, WeekStart: time.Monday}))
	first := out[strings.Index(out, `data-slot="calendar-weekday"`):]
	mustContain(t, first, `aria-label="Monday"`, ">Mo<")
}

// TestCalendarDayStates proves selected, today, out-of-range, and adjacent-month
// day states are server-computed, and only in-month enabled days are submit
// buttons carrying the ISO value.
func TestCalendarDayStates(t *testing.T) {
	out := render(t, Calendar(CalendarProps{
		Month:    julyMonth,
		Selected: day(2026, time.July, 15),
		Today:    day(2026, time.July, 17),
		Min:      day(2026, time.July, 6),
	}))

	// Selected day: a submit button with the ISO value and selection hooks.
	mustContain(t, out,
		`type="submit"`, `data-slot="calendar-day"`, `name="date"`, `value="2026-07-15"`,
		`aria-selected="true"`, `data-selected="true"`, `data-roving-item`,
		`data-col=`, `data-row=`,
	)
	// Today: aria-current + data-today.
	mustContain(t, out, `value="2026-07-17"`, `data-today="true"`, `aria-current="date"`)

	// Days before Min (July 1-5) are disabled: non-focusable spans, not buttons, so
	// they carry no submit value and cannot be selected with no JavaScript.
	mustContain(t, out, `data-disabled="true"`, `aria-disabled="true"`)
	mustNotContain(t, out, `value="2026-07-05"`, `value="2026-07-01"`)

	// An adjacent-month day (the leading/trailing week) is an outside span.
	mustContain(t, out, `data-outside="true"`)

	// Exactly one day button carries the roving tab stop (the selected day).
	if n := strings.Count(out, `tabindex="0"`); n != 1 {
		t.Errorf("calendar has %d tab stops, want exactly 1", n)
	}
}

// TestCalendarTabstopFallback proves the tab stop falls back to today, then the
// first enabled day, when nothing is selected.
func TestCalendarTabstopFallback(t *testing.T) {
	today := render(t, Calendar(CalendarProps{Month: julyMonth, Today: day(2026, time.July, 17)}))
	if n := strings.Count(today, `tabindex="0"`); n != 1 {
		t.Fatalf("want 1 tab stop, got %d", n)
	}
	// Nothing is selected, so today is the only interactive tab-stop candidate.
	mustContain(t, today, `value="2026-07-17"`, `data-today="true"`)

	first := render(t, Calendar(CalendarProps{Month: julyMonth}))
	if n := strings.Count(first, `tabindex="0"`); n != 1 {
		t.Errorf("want 1 tab stop for the first enabled day, got %d", n)
	}
	mustContain(t, first, `value="2026-07-01"`)
}

// TestCalendarDismissAndDayAttributes proves the Date Picker composition hooks:
// DismissTarget makes each day dismiss a native popover, and DayAttributes are
// merged onto day buttons while the owned name/value win.
func TestCalendarDismissAndDayAttributes(t *testing.T) {
	out := render(t, Calendar(CalendarProps{
		Month:         julyMonth,
		DismissTarget: "dp-pop",
		DayAttributes: templ.Attributes{
			"hx-get":    "/datepicker/pick",
			"hx-target": "#dp-fragment",
			"name":      "sneaky", // owned name wins
		},
	}))
	mustContain(t, out,
		`popovertarget="dp-pop"`, `popovertargetaction="hide"`,
		`hx-get="/datepicker/pick"`, `hx-target="#dp-fragment"`,
		`name="date"`,
	)
	mustNotContain(t, out, `name="sneaky"`)
}

// TestCalendarHideNav proves a display-only calendar omits the month navigation.
func TestCalendarHideNav(t *testing.T) {
	out := render(t, Calendar(CalendarProps{Month: julyMonth, HideNav: true}))
	mustContain(t, out, `data-slot="calendar-caption"`, "July 2026")
	mustNotContain(t, out, `data-slot="calendar-prev"`, `data-slot="calendar-next"`)
}

// TestDatePickerComposition proves the field/trigger/content parts carry the
// server parse/format/error contract and the native popover wiring.
func TestDatePickerComposition(t *testing.T) {
	root := renderKids(t, DatePicker(DatePickerProps{Base: Base{ID: "dp"}}), "x")
	mustContain(t, root, `class="goth-date-picker"`, `data-slot="date-picker"`, `id="dp"`)

	in := render(t, DatePickerInput(DatePickerInputProps{
		Base:        Base{ID: "dp-input"},
		Name:        "due",
		Value:       "2026-07-15",
		Format:      "YYYY-MM-DD",
		Invalid:     true,
		Describedby: "dp-error",
		Required:    true,
	}))
	mustContain(t, in,
		`data-slot="date-picker-input"`, `type="text"`, `name="due"`, `value="2026-07-15"`,
		`placeholder="YYYY-MM-DD"`, `aria-invalid="true"`, `aria-describedby="dp-error"`, `required`,
	)

	trig := renderKids(t, DatePickerTrigger(DatePickerTriggerProps{
		Base:   Base{ID: "dp-trigger"},
		Target: "dp-pop",
	}), "cal")
	mustContain(t, trig,
		`<button`, `data-slot="date-picker-trigger"`, `type="button"`,
		`popovertarget="dp-pop"`, `aria-label="Choose date"`,
	)

	content := renderKids(t, DatePickerContent(DatePickerContentProps{Base: Base{ID: "dp-pop"}}), "cal")
	mustContain(t, content, `data-slot="date-picker-content"`, `popover="auto"`, `id="dp-pop"`)
}

// TestDateNoInlineStyle proves invariant (a): no date primitive emits inline style.
func TestDateNoInlineStyle(t *testing.T) {
	outs := []string{
		render(t, Calendar(CalendarProps{Month: julyMonth, Selected: day(2026, time.July, 15), Today: day(2026, time.July, 17), Min: day(2026, time.July, 6)})),
		render(t, DatePickerInput(DatePickerInputProps{Name: "d", Value: "2026-07-15", Invalid: true})),
		renderKids(t, DatePickerTrigger(DatePickerTriggerProps{Target: "p"}), "x"),
		renderKids(t, DatePickerContent(DatePickerContentProps{Base: Base{ID: "p"}}), "x"),
	}
	for _, o := range outs {
		if strings.Contains(o, "style=") {
			t.Errorf("date primitive emitted an inline style=: %s", o)
		}
	}
}

// TestCalendarBaseMerge proves the shared Base contract on Calendar: a caller class
// in Attributes is dropped, Base.Class appends, Base.ID is honored, and the owned
// role wins the merge.
func TestCalendarBaseMerge(t *testing.T) {
	out := render(t, Calendar(CalendarProps{
		Month: julyMonth,
		Base: Base{
			ID:    "cal-1",
			Class: "mt-4",
			Attributes: templ.Attributes{
				"class":       "sneaky",
				"role":        "presentation",
				"data-testid": "keep",
			},
		},
	}))
	mustContain(t, out, `id="cal-1"`, `class="goth-calendar mt-4"`, `role="group"`, `data-testid="keep"`)
	mustNotContain(t, out, "sneaky", `role="presentation"`)
}
