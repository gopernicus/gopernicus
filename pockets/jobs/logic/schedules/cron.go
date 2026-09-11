package schedules

import "time"

// CronSchedule is a parsed cron expression that yields fire times. It is a type
// alias to the interface literal (not a defined type) so any conforming parser —
// including integrations/scheduling/robfig-cron, whose Parse returns the identical
// aliased shape — satisfies CronParser directly at the composition root, with no
// adapter and zero import in either direction.
type CronSchedule = interface {
	// Next returns the next fire time strictly after (per the adapter) the given
	// time. A zero result is rejected as a recurrence that cannot advance. Evaluation is UTC.
	Next(after time.Time) time.Time
}

// CronParser parses cron expressions into schedules. It is pocket-owned and
// consumer-declared; integrations/scheduling/robfig-cron satisfies it
// structurally with zero import in either direction.
type CronParser interface {
	// Parse validates a 5-field cron expression (plus @hourly-style descriptors)
	// and returns its schedule. Evaluation is UTC — v1 has no timezone support;
	// the contract states it so an adapter cannot silently localize.
	Parse(expr string) (CronSchedule, error)
}
