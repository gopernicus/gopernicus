# integrations/tracing/otel

An OpenTelemetry connector that implements the `sdk/tracing.Tracer` facility
port — it wraps exactly one external dependency, the OpenTelemetry Go family
(`go.opentelemetry.io/otel`, its trace SDK `go.opentelemetry.io/otel/sdk`, and
the bundled `stdout` and `OTLP/gRPC` exporters). `sdk/tracing` stays stdlib-only
and ships just the `Noop` default; this module isolates the OpenTelemetry
dependency so a host wires real span export by importing it and passing the
resulting `*otel.Tracer` wherever an `sdk/tracing.Tracer` is accepted.

It imports only `sdk` and the OpenTelemetry family — **no feature, no other
integration**.

## Why one module for the whole family (R-KV1)

The OpenTelemetry Go packages are one coherent dependency that version together:
the API, the trace SDK, and its exporters share a release train. The module unit
is the **library family**, not the port — splitting the API, SDK, and each
exporter into separate modules would only triplicate the same `require`,
`go.sum`, and version-bump surface for no boundary benefit (the same ruling that
lets `kvstores/goredis` back three ports from one client).

## Construction — exporter chosen by Config

`Open(ctx, cfg)` returns a `*otel.Tracer`. `Config.Exporter` selects one of three
modes:

| `Config.Exporter`     | destination | ownership |
|---|---|---|
| `ExporterStdout` (default) | `Config.Stdout` (default `os.Stdout`), synchronous | module owns the provider; `Shutdown` flushes it |
| `ExporterOTLPGRPC`    | OTLP/gRPC collector (Jaeger, Tempo, Grafana, an OpenTelemetry Collector), batched | module owns the provider; `Shutdown` flushes it |
| `ExporterProvider`    | a caller-supplied `trace.TracerProvider` (`Config.Provider`) | caller owns the provider; `Shutdown` is a no-op |

A zero `Config` is a usable dev tracer: empty `Exporter` defaults to stdout, empty
`ServiceName` to `gopernicus`, and a zero `SampleRate` to full sampling.

```go
tracer, err := otel.Open(ctx, otel.Config{
    Exporter:    otel.ExporterOTLPGRPC,
    ServiceName: "cms",
    Endpoint:    "localhost:4317",
    Insecure:    true,
})
if err != nil {
    return err
}
defer tracer.Shutdown(context.Background())

// tracer is an sdk/tracing.Tracer:
ctx, span := tracer.StartSpan(ctx, "content.publish")
defer span.Finish()
span.SetAttributes(tracing.StringAttribute("entry.id", id))
```

The scalar `Config` fields carry `env:` tags for `sdk/environment.ParseEnvTags`
(`TRACING_EXPORTER`, `TRACING_SERVICE_NAME`, `TRACING_OTLP_ENDPOINT`, …); the
`Stdout` and `Provider` fields are programmatic-only.

## Lifecycle

`Open` never blocks on a live collector — an OTLP/gRPC tracer opens offline and
connects lazily on first export. `Tracer.Shutdown(ctx)` flushes buffered spans
and stops any provider this module created; `Tracer.ForceFlush(ctx)` exports
without stopping. Both are no-ops in `ExporterProvider` mode and on a nil
`*Tracer`.

## Tests

Hermetic, no network. The provider mode is exercised against otel's
`tracetest.SpanRecorder`, asserting span name, attributes, and error status
through the `sdk/tracing.Tracer` surface; the stdout mode redirects output to a
buffer and asserts the emitted JSON; the OTLP/gRPC mode is verified to construct
and shut down offline. Run with `go test ./...`.
