# integrations/scheduling/robfig-cron

A cron-scheduling connector wrapping exactly one third-party library —
`github.com/robfig/cron/v3`. Its `Parser` structurally satisfies the cron-parsing
port declared by the jobs feature (its `CronParser` and `CronSchedule` ports)
with zero import in either direction: the ports live with their consumer, this
integration knows only robfig cron.

It owns "how to parse and evaluate a cron expression," never any feature's
scheduling policy. A different parser (a hand-rolled or timezone-aware one) would
be a sibling connector, swapped at the composition root.

## Surface

| member | shape |
|---|---|
| `New() *Parser` | builds a parser configured with the standard five fields plus `@descriptor` aliases, evaluating in UTC |
| `Parser.Parse(expr) (CronSchedule, error)` | validates a five-field expression or `@descriptor`; non-nil error on an invalid expression |
| `CronSchedule` | the returned schedule shape: `Next(after time.Time) time.Time` |
| `CronSchedule.Next(after)` | next fire time strictly after `after`, evaluated in UTC; zero `time.Time` means it never fires again |

## Grammar and UTC contract

`Parse` accepts the standard five fields — minute, hour, day-of-month, month,
day-of-week — plus descriptor aliases (`@hourly`, `@daily`, `@weekly`,
`@monthly`, `@yearly`, `@every`). When both day-of-month and day-of-week are
restricted, a match on **either** fires (robfig OR-semantics).

Every schedule evaluates in **UTC**: `Next` normalizes its input to UTC before
delegating, because a robfig schedule built by the standard parser otherwise
evaluates in the input time's location. v1 has no timezone support, matching the
port contract so this adapter cannot silently localize.

## Wiring

`CronSchedule` is a type alias to the interface literal `interface { Next(after
time.Time) time.Time }`, identical in shape to the jobs feature's `CronSchedule`
port. Because that consumer-side port is a *defined* interface type (not an
alias), Go's method-identity rules mean an external `Parser` cannot be assigned
directly to the feature's `CronParser` field without a trivial composition-root
adapter (three lines: a struct forwarding `Parse`, whose returned schedule
satisfies the port's schedule leaf directly). The adapter is host code; neither
module imports the other.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — five-field
round-trips through `Next`, `@hourly`/`@daily` descriptors, invalid-expression
errors, the day-of-month/day-of-week OR-semantics spot check, UTC pinning against
a non-UTC input, the never-fires zero-time contract, and a compile-time
structural-satisfaction assertion against a locally-mirrored copy of the port
interfaces (no import of the jobs feature).
