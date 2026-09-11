# integrations/scheduling/robfig-cron

A cron-scheduling connector wrapping exactly one third-party library —
`github.com/robfig/cron/v3`. Its `Parser` structurally satisfies the cron-parsing
port declared by the jobs pocket (its `CronParser` and `CronSchedule` ports)
with zero import in either direction: the ports live with their consumer, this
integration knows only robfig cron.

It owns "how to parse and evaluate a cron expression," never any pocket's
scheduling policy. A different parser (a hand-rolled or timezone-aware one) would
be a sibling connector, swapped at the composition root.

## Surface

| member | shape |
|---|---|
| `New() *Parser` | builds a parser configured with the standard five fields plus `@descriptor` aliases, evaluating in UTC |
| `Parser.Parse(expr) (CronSchedule, error)` | validates a five-field expression or `@descriptor`; non-nil error on an invalid expression |
| `CronSchedule` | the returned schedule shape: `Next(after time.Time) time.Time` |
| `CronSchedule.Next(after)` | next fire time strictly after `after`, evaluated in UTC; zero means no match within the vendor's bounded search window |

## Grammar and UTC contract

`Parse` accepts the standard five fields — minute, hour, day-of-month, month,
day-of-week — plus descriptor aliases (`@hourly`, `@daily`, `@weekly`,
`@monthly`, `@yearly`, `@every`). When both day-of-month and day-of-week are
restricted, a match on **either** fires (robfig OR-semantics).

Every schedule evaluates in **UTC**: `Next` normalizes its input to UTC before
delegating, because a robfig schedule built by the standard parser otherwise
evaluates in the input time's location. `TZ=` and `CRON_TZ=` prefixes are rejected,
including `UTC`, to keep the accepted grammar consistent with the port.

`@every` requires a positive whole-second duration of at least one second.
Zero, negative and fractional-second intervals fail instead of being silently
rounded. Robfig aligns the next occurrence to a whole-second boundary, so
`@every 5s` from `12:00:00.500Z` returns `12:00:05Z`.

Calendar search stops after the input year plus five. For example, February 29
from 2097 returns zero even though another occurrence exists in 2104. The jobs
pocket rejects a recurrence that returns zero. Hosts needing a larger search
window can supply a different parser; this connector retains the vendor's bound.

## Wiring

`CronSchedule` is a type alias to the interface literal `interface { Next(after
time.Time) time.Time }`, identical in shape to the jobs pocket's `CronSchedule`
port. Both are aliases, so `robfigcron.New()` directly satisfies
`pockets/jobs/logic/schedules.CronParser`. The host passes it to the schedules
configuration's `Cron` field; no forwarding adapter is needed. Neither module
imports the other.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — five-field
round-trips through `Next`, `@hourly`/`@daily` descriptors, invalid-expression
errors, the day-of-month/day-of-week OR-semantics spot check, UTC pinning against
a non-UTC input, rejected timezone prefixes and invalid intervals, whole-second
`@every` recurrence, the never-fires zero-time contract, and a compile-time
structural-satisfaction assertion against a locally-mirrored copy of the port
interfaces (no import of the jobs pocket).
