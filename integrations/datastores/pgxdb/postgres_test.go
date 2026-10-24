package pgxdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

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

// TestMapError covers the six SQLSTATE codes plus jackpgx.ErrNoRows and the
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
		{"invalid_text_representation", &pgconn.PgError{Code: "22P02"}, sdk.ErrInvalidInput},
		{"insufficient_privilege", &pgconn.PgError{Code: "42501"}, sdk.ErrForbidden},
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

// TestMapError_InvalidTextRepresentationKeepsMessage: 22P02 is the one mapped
// code whose server message survives, as the sentence before the sentinel, so a
// host's log still names the malformed value; the neighbouring codes stay bare.
func TestMapError_InvalidTextRepresentationKeepsMessage(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "not-a-uuid"`}

	got := MapError(fmt.Errorf("select plan: %w", pgErr))
	if !errors.Is(got, sdk.ErrInvalidInput) {
		t.Fatalf("MapError(22P02) = %v, want ErrInvalidInput", got)
	}
	if !strings.HasPrefix(got.Error(), pgErr.Message+": ") {
		t.Fatalf("MapError(22P02) = %q, want the server message as the sentence before the sentinel", got)
	}

	// The existing codes are unchanged: a bare sentinel, no message.
	if got := MapError(&pgconn.PgError{Code: "23502", Message: "null value in column"}); got != sdk.ErrInvalidInput {
		t.Fatalf("MapError(23502) = %v, want the bare sdk.ErrInvalidInput", got)
	}
}

// TestMapError_InsufficientPrivilege: 42501 — a missing grant or an
// append-only trigger's refusal — is sdk.ErrForbidden, and the driver error
// stays in the chain so a caller can still match its SQLSTATE.
func TestMapError_InsufficientPrivilege(t *testing.T) {
	for _, pgErr := range []*pgconn.PgError{
		{Code: "42501", Message: "permission denied for table security_events"},
		{Code: "42501", Message: "security_events is append-only"},
	} {
		got := MapError(fmt.Errorf("update: %w", pgErr))
		if !errors.Is(got, sdk.ErrForbidden) {
			t.Fatalf("MapError(42501) = %v, want ErrForbidden", got)
		}
		var inner *pgconn.PgError
		if !errors.As(got, &inner) || inner.Code != "42501" {
			t.Fatalf("MapError(42501) = %v, want the *pgconn.PgError kept in the chain", got)
		}
		if !strings.Contains(got.Error(), pgErr.Message) {
			t.Fatalf("MapError(42501) = %q, want the server message kept", got)
		}
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
	db, err := Open(context.Background(), Config{})
	if err == nil {
		t.Fatal("want error on empty DSN, got nil")
	}
	if db != nil {
		t.Fatal("want nil DB on error")
	}
}

// TestOpen_RefusesRedactedSecret: a DSN or Password composed from a rendered
// Secret fails in every mode, naming the field and Reveal(), before any dial.
func TestOpen_RefusesRedactedSecret(t *testing.T) {
	password := sdk.Secret("hunter2")
	composed := Config{DSN: sdk.Secret(fmt.Sprintf("postgres://u:%s@127.0.0.1:1/db?sslmode=disable", password)), ConnectTimeout: 2 * time.Second}
	for _, tc := range []struct {
		name   string
		cfg    Config
		fields []string
	}{
		{"dsn", composed, []string{"Config.DSN"}},
		{"password", Config{Host: "127.0.0.1", Port: "1", User: "u", Password: sdk.Secret(fmt.Sprint(password))}, []string{"Config.Password"}},
		{"both", Config{DSN: composed.DSN, Password: sdk.Secret("x" + password.String())}, []string{"Config.DSN", "Config.Password"}},
		{"production", Config{DSN: composed.DSN, RuntimeMode: environment.ModeProduction}, []string{"Config.DSN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(context.Background(), tc.cfg)
			if db != nil || !errors.Is(err, sdk.ErrRedactedSecret) || errors.Is(err, ErrTLSRequired) {
				t.Fatalf("Open = %v, %v; want only sdk.ErrRedactedSecret", db, err)
			}
			msg := err.Error()
			for _, want := range append(tc.fields, "Reveal()") {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal lacks %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, "pinging") || strings.Contains(msg, "127.0.0.1") {
				t.Errorf("refusal dialed or echoed the target: %s", msg)
			}
		})
	}
}

// TestConfig_SecretFieldsRedact: DSN and Password render as sdk.Redacted
// through fmt and slog, and ParseEnvTags still fills them.
func TestConfig_SecretFieldsRedact(t *testing.T) {
	t.Setenv("DB_URL", "postgres://u:hunter2@db.example.com/app")
	t.Setenv("DB_PASSWORD", "hunter2")
	t.Setenv("DB_LOG_QUERY_ARGS", "true")
	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.DSN.Reveal() != "postgres://u:hunter2@db.example.com/app" || cfg.Password.Reveal() != "hunter2" || !cfg.LogQueryArgs {
		t.Fatalf("ParseEnvTags did not fill the secret fields or LogQueryArgs: %v %v", cfg.DSN.Reveal() != "", cfg.LogQueryArgs)
	}
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("config", slog.Any("cfg", cfg), slog.Any("dsn", cfg.DSN))
	for _, out := range []string{fmt.Sprintf("%v %+v %#v %s", cfg, cfg, cfg, cfg.DSN), logged.String()} {
		if strings.Contains(out, "hunter2") {
			t.Fatalf("a secret field printed its value: %s", out)
		}
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
	if want := cfg.DSN.Reveal(); got != want {
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
	if lt, ok := tracer.(*LoggingQueryTracer); !ok || lt.logArgs {
		t.Fatalf("queryTracer() = %#v, want a *LoggingQueryTracer without args", tracer)
	}

	cfg.LogQueryArgs = true
	if lt, ok := cfg.queryTracer().(*LoggingQueryTracer); !ok || !lt.logArgs {
		t.Fatalf("LogQueryArgs: queryTracer() = %#v, want a *LoggingQueryTracer logging args", cfg.queryTracer())
	}

	cfg.LogQueries = false
	if tracer := cfg.queryTracer(); tracer != nil {
		t.Fatalf("LogQueryArgs alone: queryTracer() = %#v, want nil (inert)", tracer)
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

// TestPoolConfig_TimeoutsAppliedInMilliseconds: a set knob becomes its startup
// parameter in whole milliseconds (sub-millisecond remainders truncate), and the
// DSN string itself is untouched.
func TestPoolConfig_TimeoutsAppliedInMilliseconds(t *testing.T) {
	clearLibpqEnv(t)

	for _, tc := range []struct {
		name          string
		cfg           Config
		wantStatement string
		wantLock      string
	}{
		{"both", Config{StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second}, "30000", "5000"},
		{"one_millisecond", Config{StatementTimeout: time.Millisecond, LockTimeout: time.Millisecond}, "1", "1"},
		{"truncates_remainder", Config{StatementTimeout: 1500 * time.Microsecond, LockTimeout: 2*time.Second + 999*time.Microsecond}, "1", "2000"},
		{"statement_only", Config{StatementTimeout: time.Minute}, "60000", ""},
		{"lock_only", Config{LockTimeout: 250 * time.Millisecond}, "", "250"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			poolConfig, err := tc.cfg.poolConfig(testDSN)
			if err != nil {
				t.Fatalf("poolConfig: %v", err)
			}
			params := poolConfig.ConnConfig.RuntimeParams
			for param, want := range map[string]string{"statement_timeout": tc.wantStatement, "lock_timeout": tc.wantLock} {
				got, ok := params[param]
				if want == "" {
					if ok {
						t.Fatalf("RuntimeParams[%q] = %q, want absent for a zero knob", param, got)
					}
					continue
				}
				if got != want {
					t.Fatalf("RuntimeParams[%q] = %q, want %q", param, got, want)
				}
			}
			if got := poolConfig.ConnConfig.ConnString(); got != testDSN {
				t.Fatalf("ConnString() = %q, want the DSN unchanged (%q)", got, testDSN)
			}
		})
	}
}

// TestPoolConfig_TimeoutsZeroUnset: the zero Config sends neither parameter, so
// the server's (or role's) setting stands.
func TestPoolConfig_TimeoutsZeroUnset(t *testing.T) {
	clearLibpqEnv(t)

	poolConfig, err := Config{}.poolConfig(testDSN)
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}
	for _, param := range []string{"statement_timeout", "lock_timeout"} {
		if got, ok := poolConfig.ConnConfig.RuntimeParams[param]; ok {
			t.Fatalf("RuntimeParams[%q] = %q, want absent", param, got)
		}
	}
}

// TestPoolConfig_TimeoutsRejectInvalid: negative values, and positive values
// under 1ms — which would serialize as "0", the server's "disabled" — are
// ErrInvalidInput naming the field.
func TestPoolConfig_TimeoutsRejectInvalid(t *testing.T) {
	clearLibpqEnv(t)

	for _, tc := range []struct {
		name  string
		cfg   Config
		field string
	}{
		{"negative_statement", Config{StatementTimeout: -time.Second}, "StatementTimeout"},
		{"negative_lock", Config{LockTimeout: -time.Millisecond}, "LockTimeout"},
		{"nanosecond_statement", Config{StatementTimeout: time.Nanosecond}, "StatementTimeout"},
		{"sub_millisecond_statement", Config{StatementTimeout: 999 * time.Microsecond}, "StatementTimeout"},
		{"sub_millisecond_lock", Config{LockTimeout: 500 * time.Microsecond}, "LockTimeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.cfg.poolConfig(testDSN)
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("poolConfig error = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("poolConfig error = %q, want it to name %s", err, tc.field)
			}
		})
	}
}

// TestPoolConfig_TimeoutDoubleSettingRefused: a knob set beside a connection
// string or PGOPTIONS that names the same parameter — as a key or in any options
// switch form the server accepts — is ErrInvalidInput, never a silent winner.
// The refusal names the parameter and keeps the DSN's password out of its text.
func TestPoolConfig_TimeoutDoubleSettingRefused(t *testing.T) {
	const secretDSN = "postgres://u:timeout-s3cret@127.0.0.1:5432/db?sslmode=disable"

	for _, tc := range []struct {
		name  string
		dsn   string
		env   map[string]string
		cfg   Config
		param string
	}{
		{"dsn_key", secretDSN + "&statement_timeout=2000", nil, Config{StatementTimeout: time.Second}, "statement_timeout"},
		{"dsn_key_mixed_case", secretDSN + "&Lock_Timeout=2000", nil, Config{LockTimeout: time.Second}, "lock_timeout"},
		{"keyword_value_dsn", "host=127.0.0.1 user=u password=timeout-s3cret dbname=db sslmode=disable statement_timeout=2000", nil, Config{StatementTimeout: time.Second}, "statement_timeout"},
		{"dsn_options_c", secretDSN + "&options=-c%20statement_timeout%3D2000", nil, Config{StatementTimeout: time.Second}, "statement_timeout"},
		{"dsn_options_joined", secretDSN + "&options=-clock_timeout%3D2000", nil, Config{LockTimeout: time.Second}, "lock_timeout"},
		{"dsn_options_long", secretDSN + "&options=--lock-timeout%3D2000", nil, Config{LockTimeout: time.Second}, "lock_timeout"},
		{"dsn_options_among_others", secretDSN + "&options=-c%20search_path%3Dapp%20-c%20Statement-Timeout%3D2000", nil, Config{StatementTimeout: time.Second}, "statement_timeout"},
		{"pgoptions", secretDSN, map[string]string{"PGOPTIONS": "-c statement_timeout=2000"}, Config{StatementTimeout: time.Second}, "statement_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLibpqEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := tc.cfg.poolConfig(tc.dsn)
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("poolConfig error = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tc.param) {
				t.Fatalf("poolConfig error = %q, want it to name %s", err, tc.param)
			}
			if strings.Contains(err.Error(), "timeout-s3cret") {
				t.Fatalf("poolConfig error = %q leaks the DSN password", err)
			}
		})
	}
}

// TestPoolConfig_TimeoutDSNOnlyUntouched: with the knob zero, the host's own
// DSN/options setting reaches the server exactly as written; a knob for the
// OTHER parameter still applies beside it, and deadlock_timeout is not mistaken
// for lock_timeout.
func TestPoolConfig_TimeoutDSNOnlyUntouched(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dsn    string
		cfg    Config
		want   map[string]string
		absent []string
	}{
		{
			name:   "dsn_key",
			dsn:    testDSN + "&statement_timeout=1500",
			want:   map[string]string{"statement_timeout": "1500"},
			absent: []string{"lock_timeout"},
		},
		{
			name:   "dsn_options",
			dsn:    testDSN + "&options=-c%20lock_timeout%3D700",
			want:   map[string]string{"options": "-c lock_timeout=700"},
			absent: []string{"statement_timeout", "lock_timeout"},
		},
		{
			name: "dsn_names_one_knob_sets_the_other",
			dsn:  testDSN + "&statement_timeout=1500",
			cfg:  Config{LockTimeout: 5 * time.Second},
			want: map[string]string{"statement_timeout": "1500", "lock_timeout": "5000"},
		},
		{
			name: "deadlock_timeout_is_not_lock_timeout",
			dsn:  testDSN + "&options=-c%20deadlock_timeout%3D2s",
			cfg:  Config{LockTimeout: 5 * time.Second},
			want: map[string]string{"options": "-c deadlock_timeout=2s", "lock_timeout": "5000"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLibpqEnv(t)

			poolConfig, err := tc.cfg.poolConfig(tc.dsn)
			if err != nil {
				t.Fatalf("poolConfig: %v", err)
			}
			params := poolConfig.ConnConfig.RuntimeParams
			for param, want := range tc.want {
				if got := params[param]; got != want {
					t.Fatalf("RuntimeParams[%q] = %q, want %q", param, got, want)
				}
			}
			for _, param := range tc.absent {
				if got, ok := params[param]; ok {
					t.Fatalf("RuntimeParams[%q] = %q, want absent", param, got)
				}
			}
		})
	}
}

// TestOpen_TimeoutDoubleSettingRefusedBeforeConnecting: the double setting is
// refused by Open itself, before the pool exists — the listener never sees a
// connection attempt.
func TestOpen_TimeoutDoubleSettingRefusedBeforeConnecting(t *testing.T) {
	clearLibpqEnv(t)
	dsn, connected := stalledPostgres(t)

	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn + "&statement_timeout=2000"), StatementTimeout: time.Second})
	if db != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Open = %v, %v; want nil, ErrInvalidInput", db, err)
	}
	select {
	case <-connected:
		t.Fatal("Open attempted a connection for a refused configuration")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestOptionsNameParam pins the options parse: every switch form the server
// accepts, '-' folded to '_', names compared exactly (not as substrings).
func TestOptionsNameParam(t *testing.T) {
	for _, tc := range []struct {
		options string
		want    bool
	}{
		{"", false},
		{"-c statement_timeout=1000", true},
		{"-cstatement_timeout=1000", true},
		{"--statement_timeout=1000", true},
		{"-c statement-timeout=1000", true},
		{"-c STATEMENT_TIMEOUT=1000", true},
		{"-c search_path=app  -c statement_timeout=1000", true},
		{"-c statement_timeout", false},
		{"-c", false},
		{"-c idle_in_transaction_session_timeout=1000", false},
		{"-c xstatement_timeout=1000", false},
		{"statement_timeout=1000", false},
	} {
		if got := optionsNameParam(tc.options, "statement_timeout"); got != tc.want {
			t.Errorf("optionsNameParam(%q, statement_timeout) = %v, want %v", tc.options, got, tc.want)
		}
	}
	if optionsNameParam("-c deadlock_timeout=2s", "lock_timeout") {
		t.Error(`optionsNameParam("-c deadlock_timeout=2s", lock_timeout) = true, want false`)
	}
}

// TestConfig_TimeoutEnvTags: the knobs parse from DB_STATEMENT_TIMEOUT and
// DB_LOCK_TIMEOUT as Go durations.
func TestConfig_TimeoutEnvTags(t *testing.T) {
	t.Setenv("DB_STATEMENT_TIMEOUT", "30s")
	t.Setenv("DB_LOCK_TIMEOUT", "5s")

	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.StatementTimeout != 30*time.Second || cfg.LockTimeout != 5*time.Second {
		t.Fatalf("StatementTimeout=%v LockTimeout=%v, want 30s and 5s", cfg.StatementTimeout, cfg.LockTimeout)
	}
}
