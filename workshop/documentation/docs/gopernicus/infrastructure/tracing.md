---
sidebar_position: 11
title: Tracing
---

# Tracing

`infrastructure/tracing/` provides OpenTelemetry exporter implementations that return a [`telemetry.Provider`](../telemetry/overview.md). The provider is the entry point for all tracing in the application — span creation, attributes, context propagation, and shutdown are covered in the [Telemetry](../telemetry/overview.md) docs.

## Implementations

| Package | Transport | Use Case |
|---|---|---|
| `otlptrace/` | gRPC | Production — sends to any OTLP-compatible collector (Jaeger, Tempo, Grafana, Datadog, etc.) |
| `stdouttrace/` | Stdout | Development — prints spans to the terminal |

Both return a `*telemetry.Provider`. The rest of the application doesn't know or care which exporter is behind it.

## otlptrace

OTLP gRPC exporter with batched sending and configurable sampling.

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
| `Endpoint` | Collector address (`host:port`) |
| `Insecure` | Plaintext gRPC (no TLS) |
| `ServiceName` | Identifies this service in traces |
| `ServiceVersion` | Version tag |
| `Environment` | Deployment environment tag |
| `SampleRate` | Trace sampling ratio (`0.0`–`1.0`) |

The exporter attaches standard OTel resource attributes (`service.name`, `service.version`, `deployment.environment.name`) so traces are filterable in your collector UI.

## stdouttrace

Prints spans to stdout. Uses a synchronous exporter so spans appear immediately — no batching delay.

```go
import "github.com/gopernicus/gopernicus/infrastructure/tracing/stdouttrace"

// Quick start — pretty-printed with timestamps.
provider, err := stdouttrace.NewSimple("my-service")

// Full control.
provider, err := stdouttrace.New(stdouttrace.Options{
    ServiceName: "my-service",
    PrettyPrint: true,
    Timestamps:  true,
})
```

### Options

| Field | Description |
|---|---|
| `ServiceName` | Service name in span output |
| `PrettyPrint` | Indent JSON output |
| `Timestamps` | Include timestamps (disable for deterministic test output) |

## Wiring It Up

A typical setup in your application's boot sequence:

```go
// Choose exporter based on environment.
var provider *telemetry.Provider
if env.IsProduction() {
    provider, err = otlptrace.New(ctx, otlptrace.Options{
        Endpoint:    cfg.OTELEndpoint,
        Insecure:    cfg.OTELInsecure,
        ServiceName: "my-service",
        Environment: "production",
        SampleRate:  0.1,
    })
} else {
    provider, err = stdouttrace.NewSimple("my-service")
}

// Register globally so telemetry.StartSpan works everywhere.
provider.RegisterGlobal()

// On shutdown:
defer provider.Shutdown(ctx)
```

## Instrumented Infrastructure

Several infrastructure packages accept a `telemetry.Tracer` to automatically create spans:

| Package | Option | Docs |
|---|---|---|
| Cache | `cache.WithTracer(tracer)` | [Cache](cache.md) |
| Database | `pgxdb.WithTracer(tracer)` | [Database](database/overview.md) |
| HTTP (outbound) | `httpc.NewTracingTransport(tracer, base)` | [HTTP Client](httpc.md) |
| HTTP (inbound) | `httpmid.TelemetryMiddleware(tracer)` | [Bridge Middleware](../bridge/middleware.md) |

The HTTP `TelemetryMiddleware` creates server spans for every incoming request, extracts propagated trace context from headers, and records method, status code, and content length as span attributes.

## See Also

- [Telemetry](../telemetry/overview.md) — Provider, span helpers, context propagation
- [Logger](../sdk/logger.md) — `WithTracing()` injects trace and span IDs into log records
