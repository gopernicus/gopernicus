package otel

import (
	"log"
	"log/slog"
	"sync"
	"time"

	otelapi "go.opentelemetry.io/otel"
)

// defaultErrorInterval is InstallErrorHandler's interval when every <= 0.
const defaultErrorInterval = time.Minute

// InstallErrorHandler routes OpenTelemetry's internal errors (failed exports,
// dropped records) to console at WARN, at most once per every (one minute
// when every <= 0), each line carrying how many errors were suppressed since
// the last one. It never writes through the OpenTelemetry log pipeline, so an
// exporter that keeps failing cannot feed its own errors back into itself.
// Pass the host's console logger, not one whose chain includes Logs.Handler.
//
// It replaces OpenTelemetry's process-wide error handler. restore reinstates
// the previous handler; afterwards this handler, still reachable through
// OpenTelemetry's default delegator, falls back to the standard library log
// (OpenTelemetry's own default) rather than calling anything that could loop.
func InstallErrorHandler(console *slog.Logger, every time.Duration) (restore func()) {
	if every <= 0 {
		every = defaultErrorInterval
	}
	if console == nil {
		console = slog.Default()
	}
	h := &rateLimitedErrors{console: console, every: every, now: time.Now}
	prev := otelapi.GetErrorHandler()
	otelapi.SetErrorHandler(h)
	return func() {
		h.disable()
		otelapi.SetErrorHandler(prev)
	}
}

// rateLimitedErrors is the OpenTelemetry error handler InstallErrorHandler
// installs.
type rateLimitedErrors struct {
	console *slog.Logger
	every   time.Duration
	now     func() time.Time

	mu         sync.Mutex
	last       time.Time
	suppressed int
	disabled   bool
}

func (h *rateLimitedErrors) Handle(err error) {
	if err == nil {
		return
	}
	h.mu.Lock()
	if h.disabled {
		h.mu.Unlock()
		log.Print(err)
		return
	}
	now := h.now()
	if !h.last.IsZero() && now.Sub(h.last) < h.every {
		h.suppressed++
		h.mu.Unlock()
		return
	}
	suppressed := h.suppressed
	h.last, h.suppressed = now, 0
	h.mu.Unlock()
	h.console.Warn("otel: telemetry export error", "error", err.Error(), "suppressed", suppressed)
}

func (h *rateLimitedErrors) disable() {
	h.mu.Lock()
	h.disabled = true
	h.mu.Unlock()
}
