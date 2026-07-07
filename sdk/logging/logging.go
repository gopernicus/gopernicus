// Package logging provides helpers for setting up a standard *slog.Logger from
// environment configuration.
//
// This package does NOT wrap slog — it returns a plain *slog.Logger that you
// use directly. The helpers handle common setup: parsing log levels, choosing
// output format, and optionally injecting request IDs from context.
//
// Example — basic setup:
//
//	log := logging.New(logging.Options{Level: "DEBUG", Format: "json"})
//	log.Info("server starting", "port", 8080)
//
// Example — with request-id injection:
//
//	log := logging.New(logging.Options{Level: "INFO", Format: "json"}, logging.WithTracing())
//	// Any log call with a context carrying a request ID will include it automatically.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options is the configuration for creating a logger.
// Fields use env tags compatible with sdk/environment.
type Options struct {
	Level  string `env:"LOG_LEVEL" default:"INFO"`
	Format string `env:"LOG_FORMAT" default:"json"`
	Output string `env:"LOG_OUTPUT" default:"STDERR"`
}

// Option applies optional configuration on top of the base Options.
type Option func(*config)

type config struct {
	tracing bool
}

// WithTracing wraps the handler with a TracingHandler that injects request_id
// from context into every log record.
func WithTracing() Option {
	return func(c *config) { c.tracing = true }
}

// New creates a standard *slog.Logger from the given Options.
func New(opts Options, loggerOpts ...Option) *slog.Logger {
	cfg := &config{}
	for _, opt := range loggerOpts {
		opt(cfg)
	}

	level := ParseLevel(opts.Level)
	output := parseOutput(opts.Output)

	handlerOpts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch strings.ToLower(opts.Format) {
	case "text":
		handler = slog.NewTextHandler(output, handlerOpts)
	default:
		handler = slog.NewJSONHandler(output, handlerOpts)
	}

	if cfg.tracing {
		handler = NewTracingHandler(handler)
	}

	return slog.New(handler)
}

// NewDefault creates a logger with sensible defaults (INFO level, JSON format, stderr output).
func NewDefault(opts ...Option) *slog.Logger {
	return New(Options{Level: "INFO", Format: "json", Output: "STDERR"}, opts...)
}

// NewNoop creates a logger that discards all output. Useful for tests.
func NewNoop() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ParseLevel converts a string log level to slog.Level.
// Accepts: DEBUG, INFO, WARN, WARNING, ERROR (case-insensitive).
// Defaults to INFO for unrecognized values.
func ParseLevel(s string) slog.Level {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// parseOutput converts an output string to an io.Writer.
// Accepts: STDOUT, STDERR (case-insensitive). Defaults to STDERR.
func parseOutput(s string) io.Writer {
	switch strings.ToUpper(s) {
	case "STDOUT":
		return os.Stdout
	default:
		return os.Stderr
	}
}
