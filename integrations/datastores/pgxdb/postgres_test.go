package pgxdb

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestMapError covers the four SQLSTATE codes plus jackpgx.ErrNoRows and the
// nil/passthrough cases — the connector's entire error taxonomy, hermetically.
func TestMapError(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"unique_violation", &pgconn.PgError{Code: "23505"}, sdk.ErrAlreadyExists},
		{"foreign_key_violation", &pgconn.PgError{Code: "23503"}, sdk.ErrInvalidReference},
		{"check_violation", &pgconn.PgError{Code: "23514"}, sdk.ErrInvalidInput},
		{"not_null_violation", &pgconn.PgError{Code: "23502"}, sdk.ErrInvalidInput},
		{"no_rows", jackpgx.ErrNoRows, sdk.ErrNotFound},
		{"wrapped_unique", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"}), sdk.ErrAlreadyExists},
		{"wrapped_no_rows", fmt.Errorf("scan: %w", jackpgx.ErrNoRows), sdk.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MapError(tc.in)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("MapError(%v) = %v, want nil", tc.in, got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("MapError(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestMapError_Passthrough confirms an unrecognized error is returned unchanged.
func TestMapError_Passthrough(t *testing.T) {
	orig := errors.New("connection refused")
	if got := MapError(orig); got != orig {
		t.Fatalf("MapError(unknown) = %v, want the original error unchanged", got)
	}
	// An unrecognized SQLSTATE code passes through too.
	other := &pgconn.PgError{Code: "40001"} // serialization_failure
	if got := MapError(other); !errors.Is(got, other) {
		t.Fatalf("MapError(unknown code) = %v, want the original error", got)
	}
	if sdk.IsExpected(MapError(other)) {
		t.Fatal("unknown SQLSTATE should not map to a domain sentinel")
	}
}

// TestOpen_EmptyDSN is the hermetic config-validation case: Open rejects an
// empty DSN before any connection attempt.
func TestOpen_EmptyDSN(t *testing.T) {
	db, err := Open(Config{})
	if err == nil {
		t.Fatal("want error on empty DSN, got nil")
	}
	if db != nil {
		t.Fatal("want nil DB on error")
	}
}

func TestConfigConnectionString_DSNWins(t *testing.T) {
	cfg := Config{
		DSN:      "postgres://from-dsn.example/db?sslmode=require",
		Host:     "localhost",
		Port:     "5432",
		Database: "ignored",
		SSLMode:  "disable",
	}
	got, err := cfg.connectionString()
	if err != nil {
		t.Fatalf("connectionString: %v", err)
	}
	if want := cfg.DSN; got != want {
		t.Fatalf("connectionString() = %q, want %q", got, want)
	}
}

func TestConfigConnectionString_SplitFields(t *testing.T) {
	cfg := Config{
		Host:     "db.internal",
		Port:     "5433",
		User:     "gps360",
		Password: "secret",
		Database: "gps360",
		SSLMode:  "disable",
	}
	got, err := cfg.connectionString()
	if err != nil {
		t.Fatalf("connectionString: %v", err)
	}
	want := "postgres://gps360:secret@db.internal:5433/gps360?sslmode=disable"
	if got != want {
		t.Fatalf("connectionString() = %q, want %q", got, want)
	}
}

func TestConfigConnectionString_SplitFieldsDefaultMissingValues(t *testing.T) {
	cfg := Config{User: "postgres"}
	got, err := cfg.connectionString()
	if err != nil {
		t.Fatalf("connectionString: %v", err)
	}
	want := "postgres://postgres@localhost:5432/postgres"
	if got != want {
		t.Fatalf("connectionString() = %q, want %q", got, want)
	}
}

func TestConfigRedacted(t *testing.T) {
	cfg := Config{
		Host:     "db.internal",
		Port:     "5433",
		User:     "gps360",
		Password: "secret",
		Database: "gps360",
		SSLMode:  "disable",
	}
	want := "postgres://gps360:REDACTED@db.internal:5433/gps360?sslmode=disable"
	if got := cfg.Redacted(); got != want {
		t.Fatalf("Redacted() = %q, want %q", got, want)
	}
}

func TestConfigQueryTracer_LogQueries(t *testing.T) {
	cfg := Config{
		LogQueries: true,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	tracer := cfg.queryTracer()
	if _, ok := tracer.(*LoggingQueryTracer); !ok {
		t.Fatalf("queryTracer() = %T, want *LoggingQueryTracer", tracer)
	}
}

func TestConfigQueryTracer_LogQueriesComposesWithTracer(t *testing.T) {
	custom := &fakeQueryTracer{}
	cfg := Config{
		LogQueries: true,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:     custom,
	}

	tracer := cfg.queryTracer()
	multi, ok := tracer.(*MultiQueryTracer)
	if !ok {
		t.Fatalf("queryTracer() = %T, want *MultiQueryTracer", tracer)
	}
	if len(multi.Tracers) != 2 {
		t.Fatalf("len(MultiQueryTracer.Tracers) = %d, want 2", len(multi.Tracers))
	}
	if multi.Tracers[0] != custom {
		t.Fatalf("first tracer = %T, want custom tracer", multi.Tracers[0])
	}
	if _, ok := multi.Tracers[1].(*LoggingQueryTracer); !ok {
		t.Fatalf("second tracer = %T, want *LoggingQueryTracer", multi.Tracers[1])
	}
}
