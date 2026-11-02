package pgxdb

import (
	"context"
	"errors"

	"github.com/gopernicus/gopernicus/sdk/capabilities/metrics"
)

// PoolMetricsConfig configures RegisterPoolMetrics.
type PoolMetricsConfig struct {
	// PoolName labels every gauge db.client.connection.pool.name, for a host
	// with more than one pool. It must be a fixed, host-chosen name (never a
	// DSN or host). Empty omits the label.
	PoolName string
}

// RegisterPoolMetrics registers the pool's Stats as observable gauges on
// meter, read only when the meter collects:
//
//   - db.client.connection.count            {state=idle|used}
//   - db.client.connection.max
//   - db.client.connection.constructing
//   - db.client.connection.acquire.count    (cumulative)
//   - db.client.connection.acquire.canceled (cumulative)
//   - db.client.connection.acquire.empty    (cumulative)
//   - db.client.connection.acquire.duration (cumulative, seconds)
//   - db.client.connection.wait.duration    (cumulative empty-acquire wait, seconds)
//   - db.client.connection.create.count     (cumulative)
//   - db.client.connection.destroy.count    {reason=max_lifetime|max_idle} (cumulative)
//
// Every gauge carries db.system.name=postgresql and, when set,
// db.client.connection.pool.name. Cumulative values are reported as gauges
// (the port has no observable counter); a backend's rate() over them is the
// per-second rate. unregister removes them all and is safe to call more than
// once. A nil meter registers nothing.
func (d *DB) RegisterPoolMetrics(meter metrics.Meter, cfg PoolMetricsConfig) (unregister func(), err error) {
	if meter == nil || d == nil {
		return func() {}, nil
	}
	base := []metrics.Attribute{metrics.StringAttribute("db.system.name", "postgresql")}
	if cfg.PoolName != "" {
		base = append(base, metrics.StringAttribute("db.client.connection.pool.name", cfg.PoolName))
	}
	with := func(extra ...metrics.Attribute) []metrics.Attribute {
		return append(append([]metrics.Attribute(nil), base...), extra...)
	}
	one := func(v float64) []metrics.Observation { return []metrics.Observation{{Value: v, Attrs: with()}} }

	gauges := []struct {
		spec    metrics.Spec
		observe func(PoolStats) []metrics.Observation
	}{
		{metrics.Spec{Name: "db.client.connection.count", Unit: "{connection}", Description: "Connections in the pool by state."}, func(s PoolStats) []metrics.Observation {
			return []metrics.Observation{
				{Value: float64(s.IdleConns), Attrs: with(metrics.StringAttribute("db.client.connection.state", "idle"))},
				{Value: float64(s.AcquiredConns), Attrs: with(metrics.StringAttribute("db.client.connection.state", "used"))},
			}
		}},
		{metrics.Spec{Name: "db.client.connection.max", Unit: "{connection}", Description: "The pool's maximum connections."}, func(s PoolStats) []metrics.Observation { return one(float64(s.MaxConns)) }},
		{metrics.Spec{Name: "db.client.connection.constructing", Unit: "{connection}", Description: "Connections being established."}, func(s PoolStats) []metrics.Observation { return one(float64(s.ConstructingConns)) }},
		{metrics.Spec{Name: "db.client.connection.acquire.count", Unit: "{acquire}", Description: "Cumulative successful acquires."}, func(s PoolStats) []metrics.Observation { return one(float64(s.AcquireCount)) }},
		{metrics.Spec{Name: "db.client.connection.acquire.canceled", Unit: "{acquire}", Description: "Cumulative acquires canceled before a connection was available."}, func(s PoolStats) []metrics.Observation { return one(float64(s.CanceledAcquireCount)) }},
		{metrics.Spec{Name: "db.client.connection.acquire.empty", Unit: "{acquire}", Description: "Cumulative acquires that waited for a connection."}, func(s PoolStats) []metrics.Observation { return one(float64(s.EmptyAcquireCount)) }},
		{metrics.Spec{Name: "db.client.connection.acquire.duration", Unit: "s", Description: "Cumulative time spent acquiring connections."}, func(s PoolStats) []metrics.Observation { return one(s.AcquireDuration.Seconds()) }},
		{metrics.Spec{Name: "db.client.connection.wait.duration", Unit: "s", Description: "Cumulative time spent waiting in empty acquires."}, func(s PoolStats) []metrics.Observation { return one(s.EmptyAcquireWaitTime.Seconds()) }},
		{metrics.Spec{Name: "db.client.connection.create.count", Unit: "{connection}", Description: "Cumulative connections created."}, func(s PoolStats) []metrics.Observation { return one(float64(s.NewConnsCount)) }},
		{metrics.Spec{Name: "db.client.connection.destroy.count", Unit: "{connection}", Description: "Cumulative connections destroyed by reason."}, func(s PoolStats) []metrics.Observation {
			return []metrics.Observation{
				{Value: float64(s.MaxLifetimeDestroyCount), Attrs: with(metrics.StringAttribute("reason", "max_lifetime"))},
				{Value: float64(s.MaxIdleDestroyCount), Attrs: with(metrics.StringAttribute("reason", "max_idle"))},
			}
		}},
	}
	var unregisters []func()
	undo := func() {
		for _, u := range unregisters {
			u()
		}
	}
	for _, g := range gauges {
		observe := g.observe
		u, err := meter.Gauge(g.spec, func(context.Context) []metrics.Observation { return observe(d.Stats()) })
		if err != nil {
			undo()
			return func() {}, errors.Join(errors.New("pgxdb: registering pool metric "+g.spec.Name), err)
		}
		unregisters = append(unregisters, u)
	}
	return undo, nil
}
