# jobs: Config.Heartbeat — the sdk v0.7.1 pool heartbeat reaches jobs-built pools

Status: BUILT + TAGGED 2026-09-01 — tag `pockets/jobs/v0.4.1` (originating
host gps-360-go `cmd/workers/io`; follows sdk v0.7.1 / #32 / #33).

## Context

sdk v0.7.1 gave `foundation/workers` its idle-observability seams: the Debug
"iteration: no work" line (automatic) and `WithHeartbeat(interval)` — an
opt-in INFO "pool alive" beat carrying the deltas since the last one. But a
jobs host never calls `workers.NewPool`: its pools are assembled inside
`jobs.NewRuntime`, and the pocket exposed no way to pass the option. The
first host that asked for the heartbeat (gps-360-go's io worker) could not
reach it. The Debug line needed nothing — it rides the pool itself.

## The change

One additive field, threaded through the existing config path:

- `jobs.Config.Heartbeat time.Duration` → `resolvedConfig.heartbeat` →
  `runtime.Deps.Heartbeat` → `workers.WithHeartbeat(d.Heartbeat)` on BOTH
  pools (queue and scheduler). Zero is the default and stays "no heartbeat" —
  no clamp, matching the sdk option's own contract, so every existing host
  behaves exactly as before.
- `pockets/jobs` go.mod: sdk `v0.5.0` → `v0.7.1` (the option's home).

## The proof

`TestHeartbeatReachesThePools` (runtime package): an IDLE queue pool — every
claim `ErrNoWork` — with `Heartbeat: 20ms` beats "pool alive" into a captured
logger; proven discriminating by removing the `WithHeartbeat` pass and
watching it fail. An all-zero beat from a workless pool is the liveness
signal the option exists for, so the idle case IS the test. Full jobs suite
green.

## Release record

- Tag `pockets/jobs/v0.4.1`, 2026-09-01. PATCH: one additive Config field,
  one pin move, no schema, no store retags, no behavior change at the zero
  value.
- First adopter: gps-360-go pins v0.4.1 and exposes it as the io worker's
  `JOBS_HEARTBEAT_INTERVAL`.
