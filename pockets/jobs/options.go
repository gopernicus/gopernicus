package jobs

import (
	"log/slog"
	"time"

	"github.com/gopernicus/gopernicus/pockets/jobs/logic/schedules"
	"github.com/gopernicus/gopernicus/sdk"
)

// Option configures optional queue and schedule behavior before construction.
type Option func(*config)

type config struct {
	maxAttempts   int
	clock         func() time.Time
	cron          schedules.CronParser
	scheduleBatch int
	ids           sdk.IDGenerator
	logger        *slog.Logger
}

// WithLogger sets the logger the queue service's construction lines use. Nil
// selects slog.Default.
func WithLogger(logger *slog.Logger) Option { return func(c *config) { c.logger = logger } }

// WithMaxAttempts replaces the queue admission retry limit. Nonpositive values select three attempts.
func WithMaxAttempts(n int) Option { return func(c *config) { c.maxAttempts = n } }

// WithClock replaces the queue admission clock. Nil selects time.Now in UTC.
func WithClock(clock func() time.Time) Option { return func(c *config) { c.clock = clock } }

// WithCronParser replaces the optional recurrence parser; nil supports fixed intervals only.
func WithCronParser(parser schedules.CronParser) Option { return func(c *config) { c.cron = parser } }

// WithScheduleBatchSize replaces the limit for each due-schedule and pending-occurrence
// query. Nonpositive values select 20.
func WithScheduleBatchSize(n int) Option { return func(c *config) { c.scheduleBatch = n } }

// WithIDs replaces the record-id generator for job and schedule ids the
// services mint. The zero value selects the class default: NanoID for
// identities, UUIDv7 for records (jobs has only records); a configured
// generator wins everywhere. A caller-supplied Enqueue.ID or Ensure.ID is kept.
// EnsureSchedule mints a create-only candidate on every call, so a
// counter-style generator skips values on updates.
func WithIDs(ids sdk.IDGenerator) Option { return func(c *config) { c.ids = ids } }
