package pgxdb

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// tracerLog returns a JSON logger behind logging.ContextHandler, the composition
// logging.New uses, so request_id arrives from the query's context.
func tracerLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(logging.NewContextHandler(slog.NewJSONHandler(&buf, nil))), &buf
}

// TestLoggingQueryTracer_ArityByDefault: the default lines carry sql,
// arg_count, arg_types and the context's request_id, and never an argument
// value or error text.
func TestLoggingQueryTracer_ArityByDefault(t *testing.T) {
	logger, buf := tracerLog()
	tracer := NewLoggingQueryTracer(logger)
	ctx := sdk.WithRequestID(context.Background(), "req-tracer")

	ctx = tracer.TraceQueryStart(ctx, nil, jackpgx.TraceQueryStartData{
		SQL:  "INSERT INTO users (email, hash) VALUES ($1, $2)",
		Args: []any{"probe@example.com", []byte("hash-bytes"), 7, nil},
	})
	tracer.TraceQueryEnd(ctx, nil, jackpgx.TraceQueryEndData{Err: &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "users_email_key",
		Message:        "duplicate key value violates unique constraint",
		Detail:         "Key (email)=(probe@example.com) already exists.",
	}})
	tracer.TraceQueryEnd(ctx, nil, jackpgx.TraceQueryEndData{Err: fmt.Errorf("failed to encode args[0]: %#v", "probe@example.com")})

	out := buf.String()
	for _, want := range []string{
		`"sql":"INSERT INTO users`,
		`"arg_count":4`,
		`"arg_types":["string","[]uint8","int","<nil>"]`,
		`"sqlstate":"23505"`,
		`"constraint":"users_email_key"`,
		`"error_type":"*errors.errorString"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
	if got := strings.Count(out, `"request_id":"req-tracer"`); got != 3 {
		t.Errorf("request_id on %d of 3 lines:\n%s", got, out)
	}
	for _, leak := range []string{"probe@example.com", "hash-bytes", `"args"`, `"error"`, "duplicate key"} {
		if strings.Contains(out, leak) {
			t.Errorf("default log carries %q:\n%s", leak, out)
		}
	}
}

// TestLoggingQueryTracer_LogArgs: LogArgs adds the verbatim args and error text.
func TestLoggingQueryTracer_LogArgs(t *testing.T) {
	logger, buf := tracerLog()
	tracer := NewLoggingQueryTracerWithConfig(LoggingQueryTracerConfig{Logger: logger, LogArgs: true})
	ctx := tracer.TraceQueryStart(context.Background(), nil, jackpgx.TraceQueryStartData{SQL: "SELECT $1", Args: []any{"probe@example.com"}})
	tracer.TraceQueryEnd(ctx, nil, jackpgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "probe"`}})

	out := buf.String()
	for _, want := range []string{`"args":["probe@example.com"]`, `"arg_count":1`, `"sqlstate":"22P02"`, `invalid input syntax for type uuid`} {
		if !strings.Contains(out, want) {
			t.Errorf("LogArgs log lacks %s:\n%s", want, out)
		}
	}
}
