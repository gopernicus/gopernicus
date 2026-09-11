package jobs

import (
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
)

// Option configures optional queue and schedule behavior before construction.
type Option func(*config)

type config struct {
	maxAttempts   int
	clock         func() time.Time
	cron          schedules.CronParser
	scheduleBatch int
}

// WithMaxAttempts replaces the queue admission retry limit. Nonpositive values select three attempts.
func WithMaxAttempts(n int) Option { return func(c *config) { c.maxAttempts = n } }

// WithClock replaces the queue admission clock. Nil selects time.Now in UTC.
func WithClock(clock func() time.Time) Option { return func(c *config) { c.clock = clock } }

// WithCronParser replaces the optional recurrence parser; nil supports fixed intervals only.
func WithCronParser(parser schedules.CronParser) Option { return func(c *config) { c.cron = parser } }

// WithScheduleBatchSize replaces the limit for each due-schedule and pending-occurrence
// query. Nonpositive values select 20.
func WithScheduleBatchSize(n int) Option { return func(c *config) { c.scheduleBatch = n } }
