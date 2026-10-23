package main

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authjobs"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
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

// Inquiry purge defaults (SOC 2 S3). cms keeps every contact-form inquiry — a third party's
// name, email and message — until the host disposes of it through the messaging.InquiryEraser
// it wired as cms.Repositories.InquiryMaintenance. Retention is host-chosen; these defaults
// purge hourly, keep inquiries for 90 days and remove at most 500 per pass.
const (
	defaultInquiryPurgeInterval  = time.Hour
	defaultInquiryPurgeRetention = 90 * 24 * time.Hour
	defaultInquiryPurgeBatch     = 500
)

// Authorization operation-expiry defaults: a pass runs hourly and removes at most 500
// operation records past the authorization pocket's retention (180 days by default).
const (
	defaultOperationExpiryInterval = time.Hour
	defaultOperationExpiryBatch    = 500
)

// defaultMaintenanceInterval is how often the retention runner ticks the
// authentication maintenance pass (MAINTENANCE_INTERVAL).
const defaultMaintenanceInterval = time.Hour

// deliveryPurgeConfig is the host-owned schedule/retention/batch for the terminal-purge loop.
type deliveryPurgeConfig struct {
	// Interval is how often a purge pass runs.
	Interval time.Duration
	// Retention is the minimum age of a terminal row before it is purged.
	Retention time.Duration
	// Batch caps how many rows one pass removes (bounded work per tick).
	Batch int
}

// inquiryPurgeConfig is the host-owned schedule/retention/batch for the inquiry purge loop.
type inquiryPurgeConfig struct {
	// Interval is how often a purge pass runs.
	Interval time.Duration
	// Retention is the minimum age (from created_at) of an inquiry before it is purged.
	Retention time.Duration
	// Batch caps how many inquiries one pass removes (bounded work per tick).
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

// inquiryPurgeConfigFromEnv reads the inquiry purge schedule/retention/batch from the
// environment, with the same fallback rules as the delivery purge:
//
//   - INQUIRY_PURGE_INTERVAL  (Go duration, e.g. "1h")    → how often a pass runs.
//   - INQUIRY_PURGE_RETENTION (Go duration, e.g. "2160h") → inquiry minimum age to purge.
//   - INQUIRY_PURGE_BATCH     (int, e.g. "500")           → max inquiries removed per pass.
func inquiryPurgeConfigFromEnv(log *slog.Logger) inquiryPurgeConfig {
	return inquiryPurgeConfig{
		Interval:  envPositiveDuration(log, "INQUIRY_PURGE_INTERVAL", defaultInquiryPurgeInterval),
		Retention: envPositiveDuration(log, "INQUIRY_PURGE_RETENTION", defaultInquiryPurgeRetention),
		Batch:     envPositiveInt(log, "INQUIRY_PURGE_BATCH", defaultInquiryPurgeBatch),
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
func newDeliveryPurge(purger authjobs.Purger, rt delivery.JobRuntime, cfg deliveryPurgeConfig, now func() time.Time) func(context.Context) (int, error) {
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

// newInquiryPurge binds one bounded inquiry purge pass over the eraser the host wired into
// cms.Repositories.InquiryMaintenance: it removes at most cfg.Batch inquiries created at or
// before now−cfg.Retention. now is injected so a test can pin the cutoff.
func newInquiryPurge(eraser messaging.InquiryEraser, cfg inquiryPurgeConfig, now func() time.Time) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		return eraser.PurgeBefore(ctx, now().Add(-cfg.Retention), cfg.Batch)
	}
}

// runInquiryPurgeLoop drives the inquiry purge on a ticker until ctx is canceled, logging one
// step=inquiries count=<n> line per pass (zero included, so an operator sees the step ran). A
// pass error is logged and the loop continues. The first pass runs one interval after start.
func runInquiryPurgeLoop(ctx context.Context, interval time.Duration, purge func(context.Context) (int, error), log *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := purge(ctx)
			if err != nil {
				log.ErrorContext(ctx, "retention step failed; continuing", "step", "inquiries", "error", err)
				continue
			}
			log.InfoContext(ctx, "retention step", "step", "inquiries", "count", n)
		}
	}
}

// startInquiryPurge runs runInquiryPurgeLoop on its own Background-derived context — like the
// delivery purge, it must outlive the parent ctx until HTTP has drained — and returns the stop
// the shutdown sequence calls: cancel, then wait up to 5s for the in-flight pass.
func startInquiryPurge(cfg inquiryPurgeConfig, purge func(context.Context) (int, error), log *slog.Logger) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runInquiryPurgeLoop(ctx, cfg.Interval, purge, log)
	}()
	log.InfoContext(ctx, "inquiry purge scheduler ENABLED", "interval", cfg.Interval, "retention", cfg.Retention, "batch", cfg.Batch)
	return func() {
		log.InfoContext(context.Background(), "stopping inquiry purge scheduler")
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			log.WarnContext(context.Background(), "inquiry purge scheduler did not stop within 5s")
		}
		log.InfoContext(context.Background(), "inquiry purge scheduler stopped")
	}
}

// runOperationExpiryLoop calls expire (the authorization pocket's
// Mutations.ExpireOperations) for at most batch operation records on every tick until ctx
// is canceled. A failed pass is logged and the loop continues; it returns promptly on
// cancellation. The first pass runs one interval after start.
func runOperationExpiryLoop(ctx context.Context, interval time.Duration, expire func(context.Context, int) (int, error), batch int, log *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := expire(ctx, batch)
			if err != nil {
				log.ErrorContext(ctx, "retention step failed; continuing", "step", "operations", "error", err)
				continue
			}
			log.InfoContext(ctx, "retention step", "step", "operations", "count", n)
		}
	}
}
