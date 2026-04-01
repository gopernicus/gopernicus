---
sidebar_position: 7
title: Logger
---

# SDK — Logger

`sdk/logger` handles setup for `log/slog` loggers. It wraps `log/slog`, `os`, and `context` from the standard library.

This package does **not** wrap `*slog.Logger` — it returns a plain one. The helpers handle the common setup work: parsing log level strings, choosing output format, and optionally injecting trace and request IDs from context into every log record.

Loggers are passed explicitly via dependency injection throughout Gopernicus. There are no global loggers.

## Creating a logger

```go
log := logger.New(logger.Options{
    Level:  "INFO",   // DEBUG, INFO, WARN, WARNING, ERROR
    Format: "json",   // "json" (default) or "text"
    Output: "STDERR", // "STDERR" (default) or "STDOUT"
})
```

`Options` uses the same env tags as `sdk/environment`, so it can be populated directly from env vars:

```go
var opts logger.Options
environment.ParseEnvTags("APP", &opts) // reads APP_LOG_LEVEL, APP_LOG_FORMAT, APP_LOG_OUTPUT
log := logger.New(opts)
```

Convenience constructors:

```go
log := logger.NewDefault()  // INFO, JSON, stderr
log := logger.NewNoop()     // discards all output — useful in tests
```

## Tracing integration

Pass `WithTracing()` to wrap the handler with `TracingHandler`, which injects `trace_id`, `span_id`, and `request_id` from context into every log record automatically:

```go
log := logger.New(logger.Options{
    Level:  "INFO",
    Format: "json",
}, logger.WithTracing())
```

Values are only added when present in the context. Missing or empty values are skipped.

### Storing IDs in context

Middleware or the telemetry layer attaches IDs to the request context:

```go
ctx = logger.WithTraceID(ctx, traceID)
ctx = logger.WithSpanID(ctx, spanID)
ctx = logger.WithRequestID(ctx, requestID)
```

Any log call that uses the context will then include them:

```go
log.InfoContext(ctx, "request complete", "status", 200)
// {"level":"INFO","msg":"request complete","status":200,"trace_id":"abc","span_id":"def","request_id":"xyz"}
```

## TracingHandler

`TracingHandler` implements `slog.Handler` and can be composed with any other handler directly if you need more control:

```go
inner := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
log := slog.New(logger.NewTracingHandler(inner))
```
