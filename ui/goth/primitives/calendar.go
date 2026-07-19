package primitives

import (
	"strconv"
	"time"

	"github.com/a-h/templ"
)

// CalendarProps configures the Calendar (P49, family F4). The SERVER owns all date
// math and locale rendering: Go computes the month grid, the weekday header order,
// and the day states here, so the calendar is fully readable and operable with no
// JavaScript (the baseline). Prev/next month navigation and day selection are
// native submit buttons — the caller wraps the Calendar in a form, and the server
// re-renders for the submitted month/date. The gothRovingFocus controller (grid
// mode) enhances the grid with a single tab stop and APG arrow/Home/End keyboard
// navigation; it never computes calendar layout.
//
// The primitive holds NO clock and NO time-zone policy: Today is caller-supplied
// (zero = no "today" marker) and every date operates on the year/month/day of the
// provided times in their own location. data-slot hooks: calendar, calendar-header,
// calendar-prev, calendar-caption, calendar-next, calendar-grid, calendar-weekdays,
// calendar-weekday, calendar-week, calendar-cell, calendar-day.
type CalendarProps struct {
	Base
	// Month is any instant within the displayed month; only its year/month are
	// used. The zero value renders the zero-time month, so callers set it.
	Month time.Time
	// Selected is the selected date (zero = none). Only year/month/day matter.
	Selected time.Time
	// Today marks the current date (zero = none). The server supplies it in its
	// chosen time zone; the primitive never calls time.Now.
	Today time.Time
	// Min and Max bound the selectable range (zero = unbounded). A day outside the
	// range renders disabled and is not a submit control.
	Min time.Time
	Max time.Time
	// WeekStart is the first column's weekday (locale week start). The zero value
	// is time.Sunday.
	WeekStart time.Weekday
	// Name is the form field a day button submits (default "date"); the value is
	// the ISO date (2006-01-02).
	Name string
	// NavName is the form field the prev/next buttons submit (default "month"); the
	// value is the target month (2006-01).
	NavName string
	// Label is the calendar region's accessible name (default "Calendar").
	Label string
	// MonthLabel is the caption text (default Month.Format("January 2006")). The
	// server passes a localized label here.
	MonthLabel string
	// Weekdays and WeekdaysFull are the display-order short/full weekday labels
	// starting at WeekStart. An empty slot falls back to the English stdlib name,
	// so a partially-filled array is the caller's responsibility.
	Weekdays     [7]string
	WeekdaysFull [7]string
	// PrevLabel and NextLabel are the nav buttons' accessible names (defaults
	// "Previous month" / "Next month").
	PrevLabel string
	NextLabel string
	// HideNav omits the prev/next month navigation for a display-only calendar.
	HideNav bool
	// DismissTarget, when set, is a popover id each day button dismisses natively
	// (popovertarget + popovertargetaction="hide") — the Date Picker composition
	// closes its popover on selection with no JavaScript.
	DismissTarget string
	// DayAttributes is merged onto every interactive day button (e.g. hx-get /
	// hx-target for an optional HTMX-enhanced selection). The day button's owned
	// name/value/dismiss attributes always win the merge.
	DayAttributes templ.Attributes
}

// calendarDay is one computed grid cell.
type calendarDay struct {
	Date        time.Time
	Label       string // the day-of-month number as text
	ISO         string // 2006-01-02
	Col         int
	Row         int
	InMonth     bool
	Disabled    bool
	Selected    bool
	Today       bool
	Tabstop     bool
	Interactive bool // an in-month, enabled day rendered as a submit button
}

type calendarWeekday struct {
	Short string
	Full  string
}

func (p CalendarProps) dayName() string {
	if p.Name == "" {
		return "date"
	}
	return p.Name
}

func (p CalendarProps) navName() string {
	if p.NavName == "" {
		return "month"
	}
	return p.NavName
}

func (p CalendarProps) label() string {
	if p.Label == "" {
		return "Calendar"
	}
	return p.Label
}

func (p CalendarProps) monthLabel() string {
	if p.MonthLabel != "" {
		return p.MonthLabel
	}
	return p.Month.Format("January 2006")
}

func (p CalendarProps) prevLabel() string {
	if p.PrevLabel == "" {
		return "Previous month"
	}
	return p.PrevLabel
}

func (p CalendarProps) nextLabel() string {
	if p.NextLabel == "" {
		return "Next month"
	}
	return p.NextLabel
}

// firstOfMonth returns the first day of the displayed month in Month's location.
func (p CalendarProps) firstOfMonth() time.Time {
	y, m, _ := p.Month.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, p.Month.Location())
}

func (p CalendarProps) prevMonthValue() string {
	return p.firstOfMonth().AddDate(0, -1, 0).Format("2006-01")
}

func (p CalendarProps) nextMonthValue() string {
	return p.firstOfMonth().AddDate(0, 1, 0).Format("2006-01")
}

// weekdays returns the seven display-order weekday labels starting at WeekStart.
func (p CalendarProps) weekdays() []calendarWeekday {
	out := make([]calendarWeekday, 7)
	for i := 0; i < 7; i++ {
		wd := time.Weekday((int(p.WeekStart) + i) % 7)
		short := p.Weekdays[i]
		if short == "" {
			short = wd.String()[:2]
		}
		full := p.WeekdaysFull[i]
		if full == "" {
			full = wd.String()
		}
		out[i] = calendarWeekday{Short: short, Full: full}
	}
	return out
}

func (p CalendarProps) isDisabled(d time.Time) bool {
	if !p.Min.IsZero() && dayLess(d, p.Min) {
		return true
	}
	if !p.Max.IsZero() && dayLess(p.Max, d) {
		return true
	}
	return false
}

// calendarWeeks computes the month grid: a slice of weeks, each seven cells wide,
// including leading/trailing adjacent-month days so every row is rectangular. It
// marks exactly one interactive day as the roving tab stop (the selected day, else
// today, else the first enabled day).
func calendarWeeks(p CalendarProps) [][]calendarDay {
	first := p.firstOfMonth()
	month := first.Month()
	year := first.Year()
	offset := (int(first.Weekday()) - int(p.WeekStart) + 7) % 7
	start := first.AddDate(0, 0, -offset)
	daysIn := time.Date(year, month+1, 0, 0, 0, 0, 0, p.Month.Location()).Day()
	weeks := (offset + daysIn + 6) / 7

	grid := make([][]calendarDay, 0, weeks)
	cur := start
	for w := 0; w < weeks; w++ {
		row := make([]calendarDay, 0, 7)
		for c := 0; c < 7; c++ {
			inMonth := cur.Month() == month && cur.Year() == year
			disabled := p.isDisabled(cur)
			day := calendarDay{
				Date:        cur,
				Label:       strconv.Itoa(cur.Day()),
				ISO:         cur.Format("2006-01-02"),
				Col:         c,
				Row:         w,
				InMonth:     inMonth,
				Disabled:    disabled,
				Selected:    sameDay(cur, p.Selected),
				Today:       sameDay(cur, p.Today),
				Interactive: inMonth && !disabled,
			}
			row = append(row, day)
			cur = cur.AddDate(0, 0, 1)
		}
		grid = append(grid, row)
	}
	markTabstop(grid)
	return grid
}

// markTabstop sets Tabstop on the single day that carries the roving tab stop.
func markTabstop(grid [][]calendarDay) {
	var selected, today, firstEnabled *calendarDay
	for w := range grid {
		for c := range grid[w] {
			d := &grid[w][c]
			if !d.Interactive {
				continue
			}
			if firstEnabled == nil {
				firstEnabled = d
			}
			if d.Selected && selected == nil {
				selected = d
			}
			if d.Today && today == nil {
				today = d
			}
		}
	}
	switch {
	case selected != nil:
		selected.Tabstop = true
	case today != nil:
		today.Tabstop = true
	case firstEnabled != nil:
		firstEnabled.Tabstop = true
	}
}

// sameDay reports whether a and b fall on the same calendar day. A zero b (no
// selection/today) is never a match.
func sameDay(a, b time.Time) bool {
	if b.IsZero() {
		return false
	}
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// dayLess reports whether a's calendar day is strictly before b's.
func dayLess(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	if ay != by {
		return ay < by
	}
	if am != bm {
		return am < bm
	}
	return ad < bd
}

func calendarClass(p CalendarProps) string { return classNames("goth-calendar", p.Class) }

func calendarAttrs(p CalendarProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":  "calendar",
		"role":       "group",
		"aria-label": p.label(),
	})
}

func calendarGridAttrs(p CalendarProps) templ.Attributes {
	return templ.Attributes{
		"data-slot":        "calendar-grid",
		"role":             "grid",
		"aria-label":       p.monthLabel(),
		"data-orientation": "grid",
		"data-columns":     "7",
		"x-data":           "gothRovingFocus",
		"x-on:keydown":     "onKeydown($event)",
	}
}

func calendarPrevAttrs(p CalendarProps) templ.Attributes {
	return templ.Attributes{
		"type":       "submit",
		"data-slot":  "calendar-prev",
		"name":       p.navName(),
		"value":      p.prevMonthValue(),
		"aria-label": p.prevLabel(),
	}
}

func calendarNextAttrs(p CalendarProps) templ.Attributes {
	return templ.Attributes{
		"type":       "submit",
		"data-slot":  "calendar-next",
		"name":       p.navName(),
		"value":      p.nextMonthValue(),
		"aria-label": p.nextLabel(),
	}
}

func calendarDayAttrs(p CalendarProps, day calendarDay) templ.Attributes {
	owned := templ.Attributes{
		"type":             "submit",
		"data-slot":        "calendar-day",
		"data-roving-item": true,
		"name":             p.dayName(),
		"value":            day.ISO,
		"data-col":         strconv.Itoa(day.Col),
		"data-row":         strconv.Itoa(day.Row),
		"aria-label":       day.Date.Format("Monday, January 2, 2006"),
		"tabindex":         rovingTabindex(day.Tabstop),
	}
	if day.Selected {
		owned["data-selected"] = "true"
	}
	if day.Today {
		owned["data-today"] = "true"
		owned["aria-current"] = "date"
	}
	if p.DismissTarget != "" {
		owned["popovertarget"] = p.DismissTarget
		owned["popovertargetaction"] = "hide"
	}
	return MergeAttributes(p.DayAttributes, owned)
}

func calendarOutsideAttrs(day calendarDay) templ.Attributes {
	owned := templ.Attributes{
		"data-slot": "calendar-day",
	}
	if !day.InMonth {
		owned["data-outside"] = "true"
	}
	if day.Disabled {
		owned["data-disabled"] = "true"
	}
	if day.Today {
		owned["data-today"] = "true"
	}
	return owned
}
