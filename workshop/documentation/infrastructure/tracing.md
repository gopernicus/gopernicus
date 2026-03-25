# infrastructure/tracing -- Tracing Reference

Package `tracing` provides OpenTelemetry setup for distributed tracing with OTLP and stdout exporters.

**Import paths:**
- `github.com/gopernicus/gopernicus/infrastructure/tracing/otlptrace`
- `github.com/gopernicus/gopernicus/infrastructure/tracing/stdouttrace`

## Overview

Tracing is built on OpenTelemetry and produces spans that can be exported to any OTEL-compatible collector (Jaeger, Tempo, Grafana, Datadog, etc.). The setup returns a `telemetry.Provider` that the rest of the application uses to create tracers and spans.

## OTLP Exporter

The primary exporter for production use. Sends traces via gRPC to an OTLP-compatible collector.

```go
import "github.com/gopernicus/gopernicus/infrastructure/tracing/otlptrace"

provider, err := otlptrace.New(ctx, otlptrace.Options{
    Endpoint:       "otel-collector:4317",
    Insecure:       true,
    ServiceName:    "my-service",
    ServiceVersion: "1.0.0",
    Environment:    "production",
    SampleRate:     1.0, // 1.0 = 100%, 0.1 = 10%
})
```

### Options

| Field | Description |
|---|---|
| `Endpoint` | OTLP collector address (host:port) |
| `Insecure` | Use plaintext gRPC (no TLS) |
| `ServiceName` | Identifies this service in traces |
| `ServiceVersion` | Service version tag |
| `Environment` | Deployment environment tag |
| `SampleRate` | Trace sampling rate (0.0 to 1.0) |

The exporter uses batched sending for efficiency and attaches standard OpenTelemetry resource attributes (`service.name`, `service.version`, `deployment.environment.name`).

## Stdout Exporter

Prints traces to stdout. Useful for local development and debugging.

```go
import "github.com/gopernicus/gopernicus/infrastructure/tracing/stdouttrace"
```

## telemetry.Provider

Both exporters return a `telemetry.Provider` that wraps the OpenTelemetry `TracerProvider`. The provider is used throughout the application to create named tracers.

```go
tracer := provider.Tracer("my-component")
```

## Span Context Propagation

OpenTelemetry spans propagate through context. The tracing middleware extracts incoming trace context from HTTP headers and creates server spans.

```go
ctx, span := telemetry.StartClientSpan(ctx, tracer, "operation.name")
defer span.End()

// Add attributes
telemetry.AddAttribute(span, "key", "value")
telemetry.AddBoolAttribute(span, "cache.hit", true)
telemetry.AddInt64Attribute(span, "result.count", 42)

// Record errors
telemetry.RecordError(span, err)
```

## How to Instrument

### HTTP Handlers

Tracing middleware automatically creates spans for incoming HTTP requests. The span context flows through `context.Context`.

### Database Queries

Use `pgxdb.WithTracer(tracer)` to add tracing to database queries. The `LoggingQueryTracer` can also be combined via `MultiQueryTracer`.

### Cache Operations

Use `cache.WithTracer(tracer)` when creating a cache. All Get/Set/Delete operations are instrumented.

### Custom Operations

```go
func (s *Service) ProcessOrder(ctx context.Context, orderID string) error {
    ctx, span := telemetry.StartClientSpan(ctx, s.tracer, "service.process_order")
    defer span.End()

    telemetry.AddAttribute(span, "order.id", orderID)

    // ... business logic (child spans inherit from this parent) ...
    return nil
}
```

## Integration with Logger

Use `logger.WithTracing()` to automatically inject `trace_id`, `span_id`, and `request_id` from context into log records. This links logs to traces for correlated observability.

```go
log := logger.New(logger.Options{Level: "INFO", Format: "json"}, logger.WithTracing())
```

## Graceful Shutdown

Flush pending traces before shutdown:

```go
provider.Shutdown(ctx)
```

## Related

- [sdk/logger](../sdk/logger.md) -- `WithTracing()` injects trace IDs into log records
- [infrastructure/database](../infrastructure/database.md) -- `WithTracer` option for query tracing
- [infrastructure/cache](../infrastructure/cache.md) -- `WithTracer` option for cache operation tracing
