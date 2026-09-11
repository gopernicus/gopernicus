package robfigcron_test

import (
	"testing"
	"time"

	robfigcron "github.com/gopernicus/gopernicus/integrations/scheduling/robfig-cron"
)

// cronSchedule and cronParser mirror the cron ports declared by the jobs
// pocket (its CronSchedule and CronParser ports). The ports live with their
// consumer, so this integration cannot import them; instead the interface
// shapes are copied here and the assertions below prove Parser satisfies them
// structurally with zero import in either direction.
//
// cronSchedule is written as a type alias (not a defined type) so it is the
// identical type to the shape Parse returns, which is what lets *Parser satisfy
// cronParser — an interface whose Parse method itself returns an interface.
type cronSchedule = interface {
	Next(after time.Time) time.Time
}

type cronParser interface {
	Parse(expr string) (cronSchedule, error)
}

// Compile-time structural-satisfaction assertion against the mirrored port.
// This transitively pins Parse's return type to the cronSchedule shape, so the
// schedule leaf is asserted too.
var _ cronParser = (*robfigcron.Parser)(nil)

func TestFiveFieldRoundtrip(t *testing.T) {
	// Every day at 12:00; from 07:00 the next fire is 12:00 the same day.
	sched, err := robfigcron.New().Parse("0 12 * * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	base := time.Date(2026, 7, 8, 7, 0, 0, 0, time.UTC)
	got := sched.Next(base)
	want := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestDescriptorsRoundtrip(t *testing.T) {
	base := time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC)
	cases := []struct {
		expr string
		want time.Time
	}{
		{"@hourly", time.Date(2026, 7, 8, 13, 0, 0, 0, time.UTC)},
		{"@daily", time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		sched, err := robfigcron.New().Parse(c.expr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.expr, err)
		}
		if got := sched.Next(base); !got.Equal(c.want) {
			t.Fatalf("Next(%q) = %s, want %s", c.expr, got.Format(time.RFC3339), c.want.Format(time.RFC3339))
		}
	}
}

func TestInvalidExpressionErrors(t *testing.T) {
	for _, expr := range []string{
		"",           // empty
		"not a cron", // garbage
		"* * * *",    // four fields
		"60 * * * *", // minute out of range
		"* * * * 8",  // day-of-week out of range
		"@nonsense",  // unknown descriptor
	} {
		if _, err := robfigcron.New().Parse(expr); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", expr)
		}
	}
}

func TestRejectsTimezonePrefixes(t *testing.T) {
	for _, expr := range []string{
		"TZ=America/New_York 0 12 * * *",
		"CRON_TZ=America/New_York @daily",
		"TZ=UTC 0 12 * * *",
		"CRON_TZ=UTC @every 1h",
		"TZ=UTC",
		"CRON_TZ=",
		"TZ=UTC\t0 12 * * *",
		"  CRON_TZ=UTC",
	} {
		t.Run(expr, func(t *testing.T) {
			if schedule, err := robfigcron.New().Parse(expr); schedule != nil || err == nil {
				t.Fatalf("Parse(%q) = %v, %v; want nil schedule and error", expr, schedule, err)
			}
		})
	}
}

func TestEveryRequiresWholeSeconds(t *testing.T) {
	for _, expr := range []string{"@every 0s", "@every -1h", "@every 500ms", "@every 1500ms", "@every 1s1ns", "@every 999999999999999999999h"} {
		if schedule, err := robfigcron.New().Parse(expr); schedule != nil || err == nil {
			t.Errorf("Parse(%q) = %v, %v; want nil schedule and error", expr, schedule, err)
		}
	}
	base := time.Date(2026, 7, 8, 10, 0, 0, 500_000_000, time.FixedZone("plus5", 5*60*60))
	for _, tc := range []struct {
		expr  string
		delay time.Duration
	}{
		{"@every 1s", time.Second},
		{"@every 90s", 90 * time.Second},
		{"@every 1h30m", 90 * time.Minute},
	} {
		schedule, err := robfigcron.New().Parse(tc.expr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.expr, err)
		}
		// Robfig aligns recurring intervals to whole-second boundaries.
		want := base.UTC().Truncate(time.Second).Add(tc.delay)
		got := schedule.Next(base)
		if !got.Equal(want) || got.Location() != time.UTC || !got.After(base) {
			t.Errorf("Next(%q) = %s, want %s in UTC strictly after input", tc.expr, got, want)
		}
		if next := schedule.Next(got); !next.Equal(got.Add(tc.delay)) {
			t.Errorf("second Next(%q) = %s, want %s", tc.expr, next, got.Add(tc.delay))
		}
	}
}

// TestDomDowOrSemantics pins the robfig OR-semantics: when both day-of-month and
// day-of-week are restricted, a match on EITHER fires. "0 0 1 * 1" is midnight
// on the 1st OR any Monday; from Wednesday 2026-07-08 the next fire is the
// coming Monday (2026-07-13), not the next 1st — proving the day-of-week arm is
// live even though day-of-month is restricted (OR, not AND).
func TestDomDowOrSemantics(t *testing.T) {
	sched, err := robfigcron.New().Parse("0 0 1 * 1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	base := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) // Wednesday
	got := sched.Next(base)
	want := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC) // Monday
	if got.Weekday() != time.Monday {
		t.Fatalf("Next weekday = %s, want Monday (OR semantics)", got.Weekday())
	}
	if !got.Equal(want) {
		t.Fatalf("Next = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestUTCPinning proves evaluation is pinned to UTC regardless of the input
// time's location. A robfig schedule built by the standard parser would
// otherwise evaluate in the input's location: for "0 12 * * *" from 10:00 in a
// +05:00 zone (05:00 UTC), a localized adapter yields 12:00+05:00 (07:00 UTC),
// while the UTC-pinned answer is 12:00Z.
func TestUTCPinning(t *testing.T) {
	sched, err := robfigcron.New().Parse("0 12 * * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	plus5 := time.FixedZone("plus5", 5*60*60)
	base := time.Date(2026, 7, 8, 10, 0, 0, 0, plus5) // 05:00 UTC
	got := sched.Next(base)
	want := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %s (%s), want %s", got.Format(time.RFC3339), got.UTC().Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestNeverFires covers the port's zero-time contract: an expression that can
// never match (midnight on February 30th) returns a zero time.Time.
func TestNeverFires(t *testing.T) {
	sched, err := robfigcron.New().Parse("0 0 30 2 *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	base := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	if got := sched.Next(base); !got.IsZero() {
		t.Fatalf("Next = %s, want zero time (never fires)", got.Format(time.RFC3339))
	}
}
