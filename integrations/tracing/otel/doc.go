// Package otel implements the sdk/capabilities/tracing.Tracer facility port over the
// OpenTelemetry Go family (go.opentelemetry.io/otel and its trace SDK +
// exporters). It is the exporter-bearing counterpart to sdk/capabilities/tracing, which
// stays stdlib-only and ships only the Noop default: here the OpenTelemetry
// dependency is isolated so a host wires real span export by importing this
// module and passing the resulting *Tracer wherever an sdk/capabilities/tracing.Tracer is
// accepted.
//
// One module carries the whole OpenTelemetry family as a single coherent
// dependency (ruling R-KV1): the otel API, the trace SDK, and both bundled
// exporters (stdout and OTLP/gRPC) version together, so splitting them into
// separate modules would only triplicate the same require/go.sum surface.
//
// The exporter is chosen by Config.Exporter — stdout for local development,
// OTLP/gRPC for a collector (Jaeger/Tempo/Grafana/…), or an adapter over a
// caller-supplied TracerProvider when the host already builds its own. Open
// returns a *Tracer whose Shutdown flushes and stops any provider this module
// created; in provider mode the caller retains ownership of its provider's
// lifecycle and Shutdown is a no-op.
package otel
