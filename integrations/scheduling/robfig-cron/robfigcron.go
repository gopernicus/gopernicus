// Package robfigcron is a cron-scheduling connector wrapping exactly one
// third-party library, github.com/robfig/cron/v3. Its Parser structurally
// satisfies the cron-parsing port declared by the jobs feature (its CronParser
// and CronSchedule ports) with zero import in either direction: the ports live
// with their consumer, this integration knows only robfig cron.
//
// It owns "how to parse and evaluate a cron expression," never any feature's
// scheduling policy. The robfig parser is configured with the standard five
// fields (minute, hour, day-of-month, month, day-of-week) plus @descriptor
// aliases, and every schedule is evaluated in UTC — v1 has no timezone support,
// matching the port contract so this adapter cannot silently localize.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/scheduling/robfig-cron)
// depending only on github.com/robfig/cron/v3. A different parser (a hand-rolled
// or timezone-aware one) would be a sibling connector, swapped at the
// composition root.
package robfigcron

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// parseOptions matches the standard five-field cron grammar plus descriptor
// aliases (@hourly, @daily, @weekly, @monthly, @yearly, @every). This is the
// same flag set the prior art used, so whatever a host accepts when ensuring a
// schedule the fire engine can always evaluate.
const parseOptions = cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor

// CronSchedule is the shape Parse returns: a parsed expression that yields its
// next fire time. It is a type alias to an interface literal — not a defined
// type — so it is the identical type to the jobs feature's consumer-declared
// CronSchedule port, satisfying it structurally with zero import in either
// direction.
type CronSchedule = interface {
	// Next returns the next fire time strictly after the given time, evaluated in
	// UTC. A zero time.Time means the expression never fires again.
	Next(after time.Time) time.Time
}

// Parser parses cron expressions into UTC-evaluated schedules. Construct it with
// New; the zero value is not usable.
type Parser struct {
	inner cron.Parser
}

// New builds a Parser configured with the standard five-field flag set plus
// descriptor support. Evaluation is fixed to UTC to honor the port contract.
func New() *Parser {
	return &Parser{inner: cron.NewParser(parseOptions)}
}

// Parse validates a five-field cron expression (or an @descriptor alias) and
// returns its schedule. It returns a non-nil error for an invalid expression.
func (p *Parser) Parse(expr string) (CronSchedule, error) {
	inner, err := p.inner.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("robfigcron: parse %q: %w", expr, err)
	}
	return schedule{inner: inner}, nil
}

// schedule wraps a robfig schedule and pins evaluation to UTC.
type schedule struct {
	inner cron.Schedule
}

// Next normalizes after to UTC before delegating: a robfig schedule built by the
// standard parser evaluates in the input time's location, so a non-UTC input
// would otherwise localize the result. A zero time.Time means the expression
// never fires again.
func (s schedule) Next(after time.Time) time.Time {
	return s.inner.Next(after.UTC())
}
