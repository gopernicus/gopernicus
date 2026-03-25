package telemetry

import "go.opentelemetry.io/otel/trace"

// Type aliases — use these instead of importing OTel directly.
type Span = trace.Span
type Tracer = trace.Tracer
