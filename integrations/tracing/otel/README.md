# integrations/tracing/otel

An OpenTelemetry connector that implements the `sdk/capabilities/tracing.Tracer` facility
port — it wraps exactly one external dependency, the OpenTelemetry Go family
(`go.opentelemetry.io/otel`, its trace SDK `go.opentelemetry.io/otel/sdk`, and
the bundled `stdout`, `OTLP/gRPC` and `OTLP/HTTP` exporters). `sdk/capabilities/tracing` stays stdlib-only
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

`Open(ctx, cfg)` returns a `*otel.Tracer`. `Config.Exporter` selects one of four
modes:

| `Config.Exporter`     | destination | ownership |
|---|---|---|
| `ExporterStdout` (default outside production) | `Config.Stdout` (default `os.Stdout`), synchronous | module owns the provider; `Shutdown` flushes it |
| `ExporterOTLPGRPC`    | OTLP/gRPC collector (Jaeger, Tempo, Grafana, an OpenTelemetry Collector), batched | module owns the provider; `Shutdown` flushes it |
| `ExporterOTLPHTTP`    | OTLP/HTTP endpoint (Grafana Cloud's OTLP gateway, a collector's `:4318`), batched, gzip | module owns the provider; `Shutdown` flushes it |
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
`Stdout`, `Provider` and `RuntimeMode` fields are programmatic-only.
`TRACING_EXPORTER` carries no env default: unset, it leaves `Exporter` empty and
`Open` fills stdout, so an unset exporter stays distinguishable from an explicit
`TRACING_EXPORTER=stdout`.

## OTLP/HTTP and Grafana Cloud

`ExporterOTLPHTTP` (`TRACING_EXPORTER=otlphttp`) sends to an OTLP/HTTP endpoint
with headers such as basic auth:

```sh
TRACING_EXPORTER=otlphttp
TRACING_OTLP_ENDPOINT=https://otlp-gateway-prod-us-east-0.grafana.net/otlp
TRACING_OTLP_HEADERS=Authorization=Basic%20<base64 of instance-id:token>
```

- **`Endpoint` is a base URL**, required in every mode. The exporter appends
  `/v1/traces`, so the URL from Grafana Cloud's OTLP setup page goes in as-is; one
  that already ends in `/v1/traces` is refused. The scheme decides the transport:
  `https` is TLS (system roots, TLS 1.2+), `http` is plain. `Insecure` with an
  `https` URL is refused as a contradiction. A URL with userinfo
  (`https://user:pass@…`), a query or a fragment is refused: credentials go in
  `Headers`.
- **`Headers` is an `sdk.Secret`** in the `OTEL_EXPORTER_OTLP_HEADERS` format:
  comma-separated `key=value`, percent-encoded (`%20` for the space in
  `Basic <token>`). Keys must be HTTP header names and unique; values may not hold
  control characters. It prints and logs as `[redacted]`, and no error carries a
  value.
- **The `OTEL_EXPORTER_OTLP_*` variables never apply.** The exporter sets every
  option the OTLP/HTTP library would otherwise read from them: endpoint, path,
  transport, headers (an empty set when `Headers` is unset), TLS, gzip
  compression and a 10-second request timeout. The standard `HTTPS_PROXY` /
  `NO_PROXY` variables still apply.

Malformed values are `sdk.ErrInvalidInput` at `Open`, in every mode.

## Error text stays out of spans by default

`RecordError` marks the span failed with `codes.Error` and the fixed status
description `error`. It adds no `exception` event, so `err.Error()` never reaches
the collector: error text can carry user input, ids or SQL fragments (a raw
Redis error from `goredis.TracingHook`, say). Set `Config.RecordErrorText`
(`TRACING_RECORD_ERROR_TEXT=true`) to export the `exception` event and use
`err.Error()` as the status description. Turn it on only when every error
reaching `RecordError` is safe to export.

## Production posture — `RuntimeMode`

`Config.RuntimeMode` is the host's `environment.Mode`, set in code by the
composition root (no env tag). The zero value is mode-blind: `Open` behaves
exactly as before. A non-zero mode must be `development` or `production`
(`environment.ErrModeRequired` / `ErrModeInvalid` otherwise). Production
refuses, before any default is filled:

| condition | sentinel | fix |
|---|---|---|
| empty `Exporter` | `ErrExporterRequired` | set `TRACING_EXPORTER` — `stdout` explicitly is admitted |
| `ExporterOTLPGRPC` with `Insecure` and an `Endpoint` that is not `127.0.0.1`, `::1` or `localhost` (with or without a port) | `ErrInsecureExporter` | unset `Insecure` to use TLS, or point at a loopback collector |
| `ExporterOTLPHTTP` with an `http://` `Endpoint` whose host is not `127.0.0.1`, `::1` or `localhost` | `ErrInsecureExporter` | use an `https` endpoint, or point at a loopback collector |

An empty `Endpoint` with `Insecure` counts as non-loopback: the OTLP exporter then
resolves its target from `OTEL_EXPORTER_OTLP_*` variables this module never sees,
so production asks for the loopback endpoint by name. Neither refusal has an
acknowledgement. Development admits both and logs one WARN per condition through
`slog.Default()`.

## Lifecycle

`Open` never blocks on a live collector — an OTLP/gRPC tracer opens offline and
connects lazily on first export. `Tracer.Shutdown(ctx)` flushes buffered spans
and stops any provider this module created; `Tracer.ForceFlush(ctx)` exports
without stopping. Both are no-ops in `ExporterProvider` mode and on a nil
`*Tracer`.

## Tests

Hermetic, no network. The provider mode is exercised against otel's
`tracetest.SpanRecorder`, asserting span name, attributes, and error status
through the `sdk/capabilities/tracing.Tracer` surface, and that error text is
exported only under `RecordErrorText`; the stdout mode redirects output to a
buffer and asserts the emitted JSON; the OTLP/gRPC mode is verified to construct
and shut down offline; the OTLP/HTTP mode exports to an in-process receiver over
plain HTTP and TLS (path, headers, gzip, decoded span), with every
`OTEL_EXPORTER_OTLP_*` variable poisoned, plus the validation and redaction
cases; the `RuntimeMode` posture cases run `Open` over
`ParseEnvTags` output and the loopback matrix. Run with `go test ./...`.

## HTTP spans and propagation

```go
router.Use(otel.Middleware(tracer, otel.HTTPConfig{
    TrustTraceContext: false,
}), web.Logger(log), web.Panics(log), otel.RouteNames()) // RouteNames last
```

### Route names under a global `Use`

A global `Use` runs before the mux matches, and the mux writes `r.Pattern` only onto
the request it receives: any layer that clones the request (`r.WithContext`, as most
middleware does) never sees it. `otel.RouteNames()` is the opt-in fix. Install it as
the **last** `Use`, directly on the mux. It records the matched pattern in a private
slot `otel.Middleware` put in the context, and the span is named when it finishes:

| request | span name | `http.route` |
|---|---|---|
| matched `GET /users/{id}` | `GET /users/{id}` | `/users/{id}` |
| no match, 404 or 405 | `GET unmatched` | none |
| `RouteNames` not installed | `GET` (method only) | none |

Names never use the request path, which can carry ids and tokens. A `RouteNames`
installed anywhere but last sees no pattern, and spans keep their method-only names; a
matched route is never called `unmatched`, except when a misplaced `RouteNames` sits
over a handler that itself answers 404 or 405. Without `otel.Middleware`,
`RouteNames` does nothing. `otel.Middleware` mounted per route instead sees
`r.Pattern` at start and needs no `RouteNames`.

This adapter starts
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
