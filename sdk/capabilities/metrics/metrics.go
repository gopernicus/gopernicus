// Package metrics is the facility port for application metrics, the sibling of
// sdk/capabilities/tracing. A Meter creates three instrument kinds: a Counter
// (monotonic int64), a Histogram (float64 distribution) and an observable
// Gauge (a callback the implementation runs on each collection). That is the
// whole stdlib boundary: sdk never imports OpenTelemetry. The exporter-bearing
// implementation is integrations/tracing/otel's *Metrics.
//
// Noop is the shipped default: instruments that record nothing, so call sites
// can create and use instruments unconditionally whether or not a real meter
// is wired.
//
// Cardinality is the caller's contract. Every distinct attribute set is a
// separate time series in the backend, so attribute values must come from
// small, bounded sets: a method, a route template, a status class, a job kind.
// Ids, paths, emails, tokens and error text never belong in an Attribute. The
// port cannot enforce this; each caller must be bounded by construction.
package metrics

import "context"

// Meter creates instruments. An integration (e.g. OpenTelemetry) implements it
// to add metrics without sdk importing a metrics library. Instruments are
// created once, typically at construction, and are safe for concurrent use.
type Meter interface {
	// Counter returns a monotonic int64 counter.
	Counter(spec Spec) Counter
	// Histogram returns a float64 distribution, e.g. a duration in seconds.
	Histogram(spec Spec) Histogram
	// Gauge registers an observable gauge whose observe callback runs on each
	// collection and returns the current values. unregister stops the
	// callback; it is safe to call more than once.
	Gauge(spec Spec, observe func(ctx context.Context) []Observation) (unregister func(), err error)
}

// Spec names and describes an instrument. Name follows OpenTelemetry naming
// (lowercase, dot-separated, e.g. "http.server.request.duration"); Unit is a
// UCUM unit such as "s", "By" or "{connection}".
type Spec struct {
	Name        string
	Description string
	Unit        string
}

// Counter is a monotonic int64 counter.
type Counter interface {
	// Add increments the counter by n (n >= 0) for the attribute set.
	Add(ctx context.Context, n int64, attrs ...Attribute)
}

// Histogram records a distribution of float64 values.
type Histogram interface {
	// Record adds v to the distribution for the attribute set.
	Record(ctx context.Context, v float64, attrs ...Attribute)
}

// Observation is one gauge value with its attribute set.
type Observation struct {
	Value float64
	Attrs []Attribute
}

// Attribute is a string key-value pair on a measurement. Its value must come
// from a bounded set (see the package doc). It is this package's own type:
// capabilities never import each other.
type Attribute struct {
	Key   string
	Value string
}

// StringAttribute is a convenience constructor for Attribute.
func StringAttribute(key, value string) Attribute {
	return Attribute{Key: key, Value: value}
}

// Noop is a Meter whose instruments record nothing. Gauge never calls observe
// and returns a no-op unregister.
type Noop struct{}

var (
	_ Meter     = Noop{}
	_ Counter   = noopCounter{}
	_ Histogram = noopHistogram{}
)

// Counter returns a counter that records nothing.
func (Noop) Counter(Spec) Counter { return noopCounter{} }

// Histogram returns a histogram that records nothing.
func (Noop) Histogram(Spec) Histogram { return noopHistogram{} }

// Gauge registers nothing and returns a no-op unregister.
func (Noop) Gauge(Spec, func(context.Context) []Observation) (func(), error) {
	return func() {}, nil
}

type noopCounter struct{}

func (noopCounter) Add(context.Context, int64, ...Attribute) {}

type noopHistogram struct{}

func (noopHistogram) Record(context.Context, float64, ...Attribute) {}
