package pgxdb

import (
	"context"
	"testing"

	jackpgx "github.com/jackc/pgx/v5"
)

// fakeQueryTracer records whether TraceQueryStart/TraceQueryEnd fired, so
// TestMultiQueryTracer_FanOut can assert every composed tracer runs.
type fakeQueryTracer struct {
	started bool
	ended   bool
}

func (f *fakeQueryTracer) TraceQueryStart(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryStartData) context.Context {
	f.started = true
	return ctx
}

func (f *fakeQueryTracer) TraceQueryEnd(ctx context.Context, conn *jackpgx.Conn, data jackpgx.TraceQueryEndData) {
	f.ended = true
}

// TestMultiQueryTracer_FanOut confirms MultiQueryTracer delegates both
// TraceQueryStart and TraceQueryEnd to every composed tracer, in order.
func TestMultiQueryTracer_FanOut(t *testing.T) {
	a := &fakeQueryTracer{}
	b := &fakeQueryTracer{}
	m := NewMultiQueryTracer(a, b)

	ctx := m.TraceQueryStart(context.Background(), nil, jackpgx.TraceQueryStartData{SQL: "SELECT 1"})
	m.TraceQueryEnd(ctx, nil, jackpgx.TraceQueryEndData{})

	for name, tr := range map[string]*fakeQueryTracer{"a": a, "b": b} {
		if !tr.started {
			t.Errorf("tracer %s: TraceQueryStart did not fire", name)
		}
		if !tr.ended {
			t.Errorf("tracer %s: TraceQueryEnd did not fire", name)
		}
	}
}
