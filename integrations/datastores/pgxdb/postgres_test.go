package pgxdb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// testDSN is a syntactically valid DSN; poolConfig never connects.
	testDSN = "postgres://u:p@127.0.0.1:5432/db?sslmode=disable"

	// microsecFromUnixEpochToY2K converts a Unix-epoch microsecond count to
	// PostgreSQL's binary timestamptz encoding, which counts from 2000-01-01
	// (pgtype/timestamptz.go).
	microsecFromUnixEpochToY2K = 946684800 * 1_000_000
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

// clearLibpqEnv removes the ambient libpq configuration that would otherwise
// decide the session time zone for a "default behaviour" case: PGTZ and
// PGOPTIONS map straight onto the timezone/options runtime parameters, and a
// PGSERVICE entry can carry either. pgconn ignores an empty value, so setting
// them to "" is "unset" for the duration of the test.
func clearLibpqEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PGTZ", "")
	t.Setenv("PGOPTIONS", "")
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGSERVICEFILE", "")
}

// TestPoolConfig_AfterConnectRegistersCodecs pins the connector's ownership of
// AfterConnect: it is installed for any DSN, whatever the host's zone choice.
func TestPoolConfig_AfterConnectRegistersCodecs(t *testing.T) {
	clearLibpqEnv(t)

	for _, dsn := range []string{
		testDSN,
		"postgres://u:p@127.0.0.1:5432/db?sslmode=disable&timezone=Europe/Oslo",
		"host=127.0.0.1 port=5432 user=u dbname=db sslmode=disable",
	} {
		poolConfig, err := Config{}.poolConfig(dsn)
		if err != nil {
			t.Fatalf("poolConfig(%q): %v", dsn, err)
		}
		if poolConfig.AfterConnect == nil {
			t.Fatalf("poolConfig(%q).AfterConnect = nil, want the connector's codec registration", dsn)
		}
	}
}

// TestPoolConfig_DefaultsSessionTimeZoneUTC is D6b's default: no host choice
// anywhere means the startup packet carries timezone=UTC.
func TestPoolConfig_DefaultsSessionTimeZoneUTC(t *testing.T) {
	clearLibpqEnv(t)

	poolConfig, err := Config{}.poolConfig(testDSN)
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}
	if got := poolConfig.ConnConfig.RuntimeParams["timezone"]; got != "UTC" {
		t.Fatalf(`RuntimeParams["timezone"] = %q, want "UTC"`, got)
	}
	if got := poolConfig.ConnConfig.ConnString(); got != testDSN {
		t.Fatalf("ConnString() = %q, want the DSN unchanged (%q)", got, testDSN)
	}
}

// TestPoolConfig_HostTimeZoneWins covers every way a host names a session zone:
// the DSN, PGTZ, and an options value (DSN or PGOPTIONS, either casing). Each
// must be left exactly as the host wrote it.
func TestPoolConfig_HostTimeZoneWins(t *testing.T) {
	cases := []struct {
		name      string
		dsn       string
		env       map[string]string
		wantKey   string
		wantValue string
	}{
		{
			name:      "dsn_timezone",
			dsn:       testDSN + "&timezone=Europe/Oslo",
			wantKey:   "timezone",
			wantValue: "Europe/Oslo",
		},
		{
			name:      "pgtz",
			dsn:       testDSN,
			env:       map[string]string{"PGTZ": "Europe/Oslo"},
			wantKey:   "timezone",
			wantValue: "Europe/Oslo",
		},
		{
			name:      "pgoptions",
			dsn:       testDSN,
			env:       map[string]string{"PGOPTIONS": "-c TimeZone=Europe/Oslo"},
			wantKey:   "options",
			wantValue: "-c TimeZone=Europe/Oslo",
		},
		{
			name:      "dsn_options",
			dsn:       testDSN + "&options=-c%20timezone%3DEurope%2FOslo",
			wantKey:   "options",
			wantValue: "-c timezone=Europe/Oslo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearLibpqEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			poolConfig, err := Config{}.poolConfig(tc.dsn)
			if err != nil {
				t.Fatalf("poolConfig: %v", err)
			}
			params := poolConfig.ConnConfig.RuntimeParams
			if got := params[tc.wantKey]; got != tc.wantValue {
				t.Fatalf("RuntimeParams[%q] = %q, want %q", tc.wantKey, got, tc.wantValue)
			}
			if tc.wantKey == "options" {
				if got, ok := params["timezone"]; ok {
					t.Fatalf(`RuntimeParams["timezone"] = %q, want absent — the host named a zone in options`, got)
				}
			}
		})
	}
}

// TestHasSessionTimeZone pins the detection rules directly, including the
// case-folded key and the case-folded "timezone=" marker inside options.
func TestHasSessionTimeZone(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]string
		want   bool
	}{
		{"nil", nil, false},
		{"empty", map[string]string{}, false},
		{"unrelated", map[string]string{"application_name": "x"}, false},
		{"timezone", map[string]string{"timezone": "Europe/Oslo"}, true},
		{"timezone_mixed_case_key", map[string]string{"TimeZone": "Europe/Oslo"}, true},
		{"options_lower", map[string]string{"options": "-c timezone=Europe/Oslo"}, true},
		{"options_mixed", map[string]string{"options": "-c TimeZone=Europe/Oslo"}, true},
		{"options_other", map[string]string{"options": "-c statement_timeout=1000"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSessionTimeZone(tc.params); got != tc.want {
				t.Fatalf("hasSessionTimeZone(%v) = %v, want %v", tc.params, got, tc.want)
			}
		})
	}
}

// TestRegisterUTCTimestamptz is D6a proved on a bare pgtype.Map: the scalar
// codec carries ScanLocation UTC, and the array type is built over that same
// element (pgx's default array codec captured the DEFAULT element pointer at
// init — pgtype_default.go:169).
func TestRegisterUTCTimestamptz(t *testing.T) {
	m := pgtype.NewMap()
	registerUTCTimestamptz(m)

	tz, ok := m.TypeForOID(pgtype.TimestamptzOID)
	if !ok {
		t.Fatal("TypeForOID(TimestamptzOID): not registered")
	}
	codec, ok := tz.Codec.(*pgtype.TimestamptzCodec)
	if !ok {
		t.Fatalf("timestamptz codec = %T, want *pgtype.TimestamptzCodec", tz.Codec)
	}
	if codec.ScanLocation != time.UTC {
		t.Fatalf("ScanLocation = %v, want time.UTC", codec.ScanLocation)
	}

	arr, ok := m.TypeForOID(pgtype.TimestamptzArrayOID)
	if !ok {
		t.Fatal("TypeForOID(TimestamptzArrayOID): not registered")
	}
	arrCodec, ok := arr.Codec.(*pgtype.ArrayCodec)
	if !ok {
		t.Fatalf("_timestamptz codec = %T, want *pgtype.ArrayCodec", arr.Codec)
	}
	if arrCodec.ElementType != tz {
		t.Fatal("_timestamptz ArrayCodec.ElementType is not the newly registered timestamptz type")
	}
}

// TestRegisterUTCTimestamptz_Decodes drives the registration end to end through
// the type map — binary (the extended-protocol default, the one that produced
// time.Local values) and text (simple protocol) — without a server.
func TestRegisterUTCTimestamptz_Decodes(t *testing.T) {
	m := pgtype.NewMap()
	registerUTCTimestamptz(m)

	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(want.UnixMicro()-microsecFromUnixEpochToY2K))

	var binaryGot time.Time
	if err := m.Scan(pgtype.TimestamptzOID, pgtype.BinaryFormatCode, buf[:], &binaryGot); err != nil {
		t.Fatalf("scan binary timestamptz: %v", err)
	}
	if !binaryGot.Equal(want) {
		t.Fatalf("binary scan = %v, want the instant %v", binaryGot, want)
	}
	if binaryGot.Location() != time.UTC {
		t.Fatalf("binary scan location = %v, want time.UTC", binaryGot.Location())
	}

	var textGot time.Time
	if err := m.Scan(pgtype.TimestamptzOID, pgtype.TextFormatCode, []byte("2026-01-01 00:00:00+05"), &textGot); err != nil {
		t.Fatalf("scan text timestamptz: %v", err)
	}
	if wantText := time.Date(2025, 12, 31, 19, 0, 0, 0, time.UTC); !textGot.Equal(wantText) {
		t.Fatalf("text scan = %v, want the instant %v", textGot, wantText)
	}
	if textGot.Location() != time.UTC {
		t.Fatalf("text scan location = %v, want time.UTC", textGot.Location())
	}

	var arrGot []time.Time
	if err := m.Scan(pgtype.TimestamptzArrayOID, pgtype.TextFormatCode, []byte(`{"2026-01-01 00:00:00+05"}`), &arrGot); err != nil {
		t.Fatalf("scan text timestamptz[]: %v", err)
	}
	if len(arrGot) != 1 {
		t.Fatalf("timestamptz[] scan len = %d, want 1", len(arrGot))
	}
	if arrGot[0].Location() != time.UTC {
		t.Fatalf("timestamptz[] element location = %v, want time.UTC", arrGot[0].Location())
	}
}
