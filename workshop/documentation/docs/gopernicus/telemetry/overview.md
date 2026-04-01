---
sidebar_position: 1
title: Overview
---

# Telemetry

`telemetry` is a thin wrapper around [OpenTelemetry](https://opentelemetry.io/) so the rest of the application can create spans, record errors, and propagate trace context without importing multiple OTel packages directly. If tracing is not configured, every helper degrades to a no-op — no nil checks needed at call sites.

```
import "github.com/gopernicus/gopernicus/telemetry"
```

## Provider

`Provider` wraps an OTel `TracerProvider`. Infrastructure exporters (see [Tracing](../infrastructure/tracing.md)) return a `*telemetry.Provider` — the application passes it to anything that needs a tracer.

```go
// Returned by an exporter — e.g. otlptrace.New() or stdouttrace.New()
provider, err := otlptrace.New(ctx, otlptrace.Options{...})

// Get the primary tracer (named after the service).
tracer := provider.Tracer()

// Get a tracer scoped to a submodule.
tracer := provider.NamedTracer("my-subsystem")
```

Call `RegisterGlobal()` to set the provider as the global OTel tracer provider. This enables `telemetry.StartSpan` (which uses the global tracer) and allows third-party libraries that call `otel.GetTracerProvider()` to participate in traces.

```go
provider.RegisterGlobal()
```

On shutdown, flush pending spans:

```go
provider.Shutdown(ctx)
```

All methods are nil-safe — calling them on a nil `*Provider` is a no-op.

## Type Aliases

The package re-exports the two OTel types you use most so downstream code doesn't need a direct OTel import:

```go
type Span   = trace.Span
type Tracer = trace.Tracer
```

Accept `telemetry.Tracer` in constructors and store `telemetry.Span` in local variables — the rest of the framework already does this.

## Span Helpers

All span helpers are nil-safe and skip work when the span is not recording.

### Creating Spans

```go
// Using the global tracer (requires RegisterGlobal).
ctx, span := telemetry.StartSpan(ctx, "operation.name")
defer span.End()

// Using a specific tracer.
ctx, span := telemetry.StartSpanWithTracer(ctx, tracer, "operation.name")
defer span.End()

// Server span — for incoming requests.
ctx, span := telemetry.StartServerSpan(ctx, tracer, "http.request")
defer span.End()

// Client span — for outgoing calls.
ctx, span := telemetry.StartClientSpan(ctx, tracer, "db.query")
defer span.End()
```

### Attributes and Events

```go
telemetry.AddAttribute(span, "order.id", orderID)
telemetry.AddAttributes(span, map[string]string{"a": "1", "b": "2"})
telemetry.AddIntAttribute(span, "result.count", 42)
telemetry.AddInt64Attribute(span, "bytes", 1024)
telemetry.AddBoolAttribute(span, "cache.hit", true)
telemetry.AddEvent(span, "retry")
```

### Errors

```go
telemetry.RecordError(span, err)       // records the error + sets span status
telemetry.SetSpanError(span, "timeout") // sets status without an error value
```

## Context Helpers

Extract IDs from the current trace context — useful for logging or passing to external systems:

```go
traceID := telemetry.TraceID(ctx)  // "" if no active trace
spanID  := telemetry.SpanID(ctx)   // "" if no active span
span    := telemetry.SpanFromContext(ctx)
```

### HTTP Propagation

Trace context needs to travel across service boundaries so distributed traces stay connected. There are two ways to handle this depending on whether you're using the `httpc` package.

**With `httpc`** — wrap the transport and propagation is automatic. Outbound requests get a client span (`HTTP OUT`) with method, URL, and status attributes, and trace headers are injected for you:

```go
client := httpc.NewClient(
    httpc.WithTransport(httpc.NewTracingTransport(tracer, http.DefaultTransport)),
)
```

See [HTTP Client](../infrastructure/httpc.md) for full details.

**Without `httpc`** — use `ExtractContext` and `InjectContext` directly to read and write trace headers on standard `http.Request`/`http.Header` values:

```go
// Inbound: extract incoming trace context (e.g. in custom middleware).
ctx = telemetry.ExtractContext(ctx, r.Header)

// Outbound: inject trace context before making a request.
telemetry.InjectContext(ctx, req.Header)
```

Both use the W3C `traceparent` header format via OTel's `propagation.TraceContext`.

## Integration Points

The telemetry package is consumed across several layers. Each is documented in its own section:

| Layer | Integration | Docs |
|---|---|---|
| Infrastructure | `cache.WithTracer(tracer)` — spans for cache ops | [Cache](../infrastructure/cache.md) |
| Infrastructure | `pgxdb.WithTracer(tracer)` — spans for DB queries | [Database](../infrastructure/database/overview.md) |
| Infrastructure | `httpc.NewTracingTransport(tracer, base)` — client spans for outbound HTTP | [HTTP Client](../infrastructure/httpc.md) |
| Infrastructure | Exporter setup (`otlptrace`, `stdouttrace`) | [Tracing](../infrastructure/tracing.md) |
| Bridge | `TelemetryMiddleware` — server spans for HTTP | [Middleware](../bridge/middleware.md) |
| SDK | `logger.WithTracing()` — injects trace/span IDs into logs | [Logger](../sdk/logger.md) |
