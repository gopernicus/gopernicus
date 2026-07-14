package main

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// Delivery terminal-purge scheduler defaults (IX-10). Sane for a proof host: purge hourly,
// keeping terminal delivery rows for a day, removing at most 500 per pass. A production host
// tunes these via the env knobs below to match its delivery volume and retention policy.
const (
	// defaultDeliveryPurgeInterval is how often a purge pass runs.
	defaultDeliveryPurgeInterval = time.Hour
	// defaultDeliveryPurgeRetention is how long a TERMINAL delivery row is retained before it
	// is eligible for purge (measured from now backwards).
	defaultDeliveryPurgeRetention = 24 * time.Hour
	// defaultDeliveryPurgeBatch is the maximum number of terminal rows removed in ONE pass, so
	// a purge is bounded work; a backlog larger than the batch drains over successive passes.
	defaultDeliveryPurgeBatch = 500
)

// deliveryPurgeConfig is the host-owned schedule/retention/batch for the terminal-purge loop.
type deliveryPurgeConfig struct {
	// Interval is how often a purge pass runs.
	Interval time.Duration
	// Retention is the minimum age of a terminal row before it is purged.
	Retention time.Duration
	// Batch caps how many rows one pass removes (bounded work per tick).
	Batch int
}

// deliveryPurgeConfigFromEnv reads the purge schedule/retention/batch from the environment,
// falling back to the documented defaults. An unparseable or non-positive value is a loud WARN
// and selects the default (never a silent zero that would disable or busy-loop the scheduler):
//
//   - DELIVERY_PURGE_INTERVAL  (Go duration, e.g. "1h")   → how often a pass runs.
//   - DELIVERY_PURGE_RETENTION (Go duration, e.g. "24h")  → terminal-row minimum age to purge.
//   - DELIVERY_PURGE_BATCH     (int, e.g. "500")          → max rows removed per pass.
func deliveryPurgeConfigFromEnv(log *slog.Logger) deliveryPurgeConfig {
	return deliveryPurgeConfig{
		Interval:  envPositiveDuration(log, "DELIVERY_PURGE_INTERVAL", defaultDeliveryPurgeInterval),
		Retention: envPositiveDuration(log, "DELIVERY_PURGE_RETENTION", defaultDeliveryPurgeRetention),
		Batch:     envPositiveInt(log, "DELIVERY_PURGE_BATCH", defaultDeliveryPurgeBatch),
	}
}

// envPositiveDuration reads a Go duration env var, returning def on unset/invalid/non-positive.
func envPositiveDuration(log *slog.Logger, key string, def time.Duration) time.Duration {
	v := environment.GetEnvOrDefault(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Warn("invalid duration env var; using default", "key", key, "value", v, "default", def)
		return def
	}
	return d
}

// envPositiveInt reads an int env var, returning def on unset/invalid/non-positive.
func envPositiveInt(log *slog.Logger, key string, def int) int {
	v := environment.GetEnvOrDefault(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Warn("invalid int env var; using default", "key", key, "value", v, "default", def)
		return def
	}
	return n
}

// newDeliveryPurge binds one bounded terminal-purge pass over the jobs Service: it purges at
// most cfg.Batch terminal delivery rows older than the cfg.Retention window (measured from
// now) and emits the purged lifecycle observation via authjobs.PurgeTerminal. now is injected
// so a test can pin the retention cutoff.
func newDeliveryPurge(purger authjobs.Purger, rt auth.DeliveryJobRuntime, cfg deliveryPurgeConfig, now func() time.Time) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		before := now().Add(-cfg.Retention)
		return authjobs.PurgeTerminal(ctx, purger, rt, before, cfg.Batch)
	}
}

// runDeliveryPurgeLoop drives purge on a ticker until ctx is canceled (IX-10). A purge-pass
// error is logged and the loop CONTINUES — a failing purge never stops the scheduler or the
// host. It returns promptly on cancellation (host-owned shutdown). The first pass runs one
// interval after start, not immediately.
func runDeliveryPurgeLoop(ctx context.Context, interval time.Duration, purge func(context.Context) (int, error), log *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := purge(ctx)
			if err != nil {
				log.ErrorContext(ctx, "delivery terminal purge pass failed; continuing", "error", err)
				continue
			}
			if n > 0 {
				log.InfoContext(ctx, "delivery terminal purge pass removed terminal rows", "purged", n)
			}
		}
	}
}
