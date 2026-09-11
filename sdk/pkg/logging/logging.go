// Package logging configures standard *slog.Logger values with a log level,
// output format, and destination. Context-aware log calls include available
// request, trace, and span IDs from sdk context helpers.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options is the configuration for creating a logger.
// Fields use env tags compatible with sdk/pkg/environment.
type Options struct {
	Level  string `env:"LOG_LEVEL" default:"INFO"`
	Format string `env:"LOG_FORMAT" default:"json"`
	Output string `env:"LOG_OUTPUT" default:"STDERR"`
}

// New creates a logger that includes context IDs via ContextHandler.
// Empty or unrecognized options default to INFO, JSON, and stderr.
func New(opts Options) *slog.Logger {
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

	return slog.New(NewContextHandler(handler))
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
