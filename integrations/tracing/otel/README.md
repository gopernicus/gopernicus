# integrations/tracing/otel

An OpenTelemetry connector that implements the `sdk/capabilities/tracing.Tracer` facility
port — it wraps exactly one external dependency, the OpenTelemetry Go family
(`go.opentelemetry.io/otel`, its trace SDK `go.opentelemetry.io/otel/sdk`, and
the bundled `stdout` and `OTLP/gRPC` exporters). `sdk/capabilities/tracing` stays stdlib-only
and ships just the `Noop` default; this module isolates the OpenTelemetry
dependency so a host wires real span export by importing it and passing the
resulting `*otel.Tracer` wherever an `sdk/capabilities/tracing.Tracer` is accepted.

It imports only `sdk` and the OpenTelemetry family — **no pocket, no other
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
`ServiceName` to `gopernicus`. Stdout samples root spans and honors the parent
sampling decision. OTLP literal `SampleRate: 0` samples no roots; environment
loading defaults the ratio to 1. All configurations reject nonfinite ratios and
values outside [0,1]. Caller-supplied providers retain their own sampling policy.

```go
tracer, err := otel.Open(ctx, otel.Config{
    Exporter:    otel.ExporterOTLPGRPC,
    ServiceName: "cms",
    Endpoint:    "localhost:4317",
    SampleRate:  1,
    Insecure:    true,
})
if err != nil {
    return err
}
defer tracer.Shutdown(context.Background())

// tracer is an sdk/capabilities/tracing.Tracer:
ctx, span := tracer.StartSpan(ctx, "content.publish")
defer span.Finish()
span.SetAttributes(tracing.StringAttribute("entry.id", id))
```

The scalar `Config` fields carry `env:` tags for `sdk/pkg/environment.ParseEnvTags`
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
through the `sdk/capabilities/tracing.Tracer` surface; the stdout mode redirects output to a
buffer and asserts the emitted JSON; the OTLP/gRPC mode is verified to construct
and shut down offline. Run with `go test ./...`.

## HTTP spans and propagation

```go
router.Use(otel.Middleware(tracer, otel.HTTPConfig{
    TrustTraceContext: false,
}), web.Logger(log), web.Panics(log))
```

Mount middleware inside routing so `r.Pattern` is available. This adapter starts
server spans and supplies current typed HTTP attributes at creation time, where
the sampler can read them. It reuses SDK completion and context/log identity
handling. Ordinary `tracer.StartSpan` still starts internal spans.

`TrustTraceContext` explicitly accepts W3C traceparent/tracestate. With
parent-based sampling, accepted callers can choose the sampling flag regardless
of the root ratio. The default ignores incoming trace headers and keeps any
existing context parent. No global provider/propagator or baggage extraction is
installed. Hosts choose which inbound callers they trust.

The bounded metadata subset includes method, route template, scheme, server and
peer address/port, and known response status. Status and ports are integers.
Unknown routes never fall back to raw request paths. Raw URLs, queries, bodies,
user-agent and arbitrary headers are omitted. This is a deliberate subset of
HTTP semantic conventions, not a claim of full instrumentation coverage.

Completion reports response errors and escaping panics using safe failure
categories, finishes once, and rethrows escaping panics unchanged. A partial
response retains its committed status; panic/abort/hijack before commitment has
no invented status. Logger keeps its original cause. `error.type` is the fixed
`http.request_failed` category. The generic SDK middleware retains its legacy
string attribute path for other tracers.

Dashboard migration: the adapter uses `http.request.method`, integer
`http.response.status_code`, `server.address`/`server.port`,
`network.peer.address`/`network.peer.port` and method-free `http.route`.
Replace queries using `http.method`, string `http.status_code`, `http.host` or
`net.peer.ip` when switching middleware.
See [AUDIT-015](../../../AUDIT.md#audit-015-bound-oauth-flows-and-truthful-tracing).
