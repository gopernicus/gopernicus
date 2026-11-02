package metrics

import (
	"context"
	"testing"
)

// TestNoop: every instrument is usable, records nothing and never panics; the
// gauge callback is never invoked and unregister is idempotent.
func TestNoop(t *testing.T) {
	var m Meter = Noop{}
	ctx := context.Background()
	m.Counter(Spec{Name: "jobs.completed"}).Add(ctx, 1, StringAttribute("job.kind", "mail"))
	m.Histogram(Spec{Name: "http.server.request.duration", Unit: "s"}).Record(ctx, 0.25)
	called := false
	unregister, err := m.Gauge(Spec{Name: "db.client.connection.count"}, func(context.Context) []Observation {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Noop Gauge: %v", err)
	}
	unregister()
	unregister()
	if called {
		t.Fatal("Noop called the gauge callback")
	}
}

func TestStringAttribute(t *testing.T) {
	if got := StringAttribute("k", "v"); got != (Attribute{Key: "k", Value: "v"}) {
		t.Fatalf("StringAttribute = %+v", got)
	}
}
