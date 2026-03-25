# sdk/logger -- Logger Reference

Package `logger` provides helpers for setting up a standard `*slog.Logger` from environment configuration. It does not wrap slog -- it returns a plain `*slog.Logger` that you use directly.

**Import:** `github.com/gopernicus/gopernicus/sdk/logger`

## Creating a Logger

### logger.New

Creates a logger from explicit options.

```go
log := logger.New(logger.Options{
    Level:  "DEBUG",
    Format: "json",
    Output: "STDERR",
})
log.Info("server starting", "port", 8080)
```

### logger.NewDefault

Creates a logger with sensible defaults (INFO level, JSON format, stderr).

```go
log := logger.NewDefault()
log := logger.NewDefault(logger.WithTracing()) // with trace ID injection
```

### logger.NewNoop

Creates a logger that discards all output. Useful for tests.

```go
log := logger.NewNoop()
```

## Options

```go
type Options struct {
    Level  string `env:"LOG_LEVEL" default:"INFO"`
    Format string `env:"LOG_FORMAT" default:"json"`
    Output string `env:"LOG_OUTPUT" default:"STDERR"`
}
```

| Field | Values | Default |
|---|---|---|
| Level | DEBUG, INFO, WARN, WARNING, ERROR | INFO |
| Format | json, text | json |
| Output | STDOUT, STDERR | STDERR |

Options use `env` tags compatible with `sdk/environment.ParseEnvTags` for automatic population from environment variables.

## ParseLevel

Converts a string level to `slog.Level`. Unrecognized values default to INFO.

```go
level := logger.ParseLevel("DEBUG") // slog.LevelDebug
```

## Tracing Integration

### WithTracing Option

Wraps the slog handler with a `TracingHandler` that injects `trace_id`, `span_id`, and `request_id` from context into every log record.

```go
log := logger.New(logger.Options{
    Level:  "INFO",
    Format: "json",
}, logger.WithTracing())
```

When logging with a context that carries trace IDs, they appear automatically:

```json
{"level":"INFO","msg":"request handled","trace_id":"abc123","span_id":"def456","request_id":"req_789"}
```

### Context Helpers

Set trace/request IDs on context for downstream log injection:

```go
ctx = logger.WithTraceID(ctx, "abc123")
ctx = logger.WithSpanID(ctx, "def456")
ctx = logger.WithRequestID(ctx, "req_789")
```

These are typically called by middleware (tracing middleware, request ID middleware) so that all subsequent log calls within a request automatically include the IDs.

### TracingHandler

A `slog.Handler` wrapper that reads trace_id, span_id, and request_id from context and adds them as attributes to every log record. Only present values are added; missing values are skipped.

```go
handler := logger.NewTracingHandler(innerHandler)
```

## Usage Patterns

### Structured Logging

Since `logger.New` returns a standard `*slog.Logger`, use all standard slog methods:

```go
log.Info("user created", "user_id", userID, "email", email)
log.Warn("slow query", "duration_ms", duration.Milliseconds(), "query", sql)
log.Error("failed to send email", "error", err, "to", recipient)
log.Debug("cache hit", "key", cacheKey)
```

### Context-Aware Logging

Use `*Context` methods to pass the request context (trace IDs will be injected if `WithTracing()` is enabled):

```go
log.InfoContext(ctx, "processing order", "order_id", orderID)
log.ErrorContext(ctx, "payment failed", "error", err)
```

### Child Loggers

Create loggers with pre-set attributes for subsystems:

```go
dbLog := log.With("component", "database")
cacheLog := log.With("component", "cache")
```

## Related

- [infrastructure/tracing](../infrastructure/tracing.md) -- OpenTelemetry setup that provides trace_id/span_id
- [sdk/web](../sdk/web.md) -- `WithLogging` handler option accepts `*slog.Logger`
