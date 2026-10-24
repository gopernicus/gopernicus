package pgxdb

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// postureSecret is the DSN password every refusal must keep out of its text.
	postureSecret = "posture-s3cret"

	// postureCA is a self-signed CA certificate whose key was discarded; pgx
	// only loads it into the root pool.
	postureCA = "testdata/ca.pem"
)

// clearLibpqTLSEnv extends clearLibpqEnv with the PG* variables that would
// otherwise change a test DSN's hosts or TLS settings.
func clearLibpqTLSEnv(t *testing.T) {
	t.Helper()
	clearLibpqEnv(t)
	for _, key := range []string{"PGHOST", "PGPORT", "PGSSLMODE", "PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY", "PGSSLNEGOTIATION", "PGSSLSNI"} {
		t.Setenv(key, "")
	}
}

func postureDSN(query string) string {
	return "postgres://u:" + postureSecret + "@db.example.com:5432/app?" + query
}

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// TestPosture_FieldsCarryNoEnvTag pins the mode and the acknowledgements as
// programmatic: ParseEnvTags, which hosts run over Config, leaves them zero even
// with same-named variables set.
func TestPosture_FieldsCarryNoEnvTag(t *testing.T) {
	typ := reflect.TypeFor[Config]()
	for _, name := range []string{"RuntimeMode", "TLSUnverifiedAcknowledged", "TLSDisabledAcknowledged", "AppendOnlyWritableAcknowledged"} {
		field, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("Config.%s is missing", name)
		}
		if tag := field.Tag.Get("env"); tag != "" {
			t.Fatalf("Config.%s carries env tag %q", name, tag)
		}
	}
	for _, key := range []string{
		"RuntimeMode", "RUNTIME_MODE", "DB_RUNTIME_MODE", "APP_RUNTIME_MODE",
		"TLSUnverifiedAcknowledged", "TLS_UNVERIFIED_ACKNOWLEDGED", "DB_TLS_UNVERIFIED_ACKNOWLEDGED",
		"TLSDisabledAcknowledged", "TLS_DISABLED_ACKNOWLEDGED", "DB_TLS_DISABLED_ACKNOWLEDGED",
		"AppendOnlyWritableAcknowledged", "APPEND_ONLY_WRITABLE_ACKNOWLEDGED", "DB_APPEND_ONLY_WRITABLE_ACKNOWLEDGED",
	} {
		value := "true"
		if strings.Contains(strings.ToUpper(key), "MODE") {
			value = "production"
		}
		t.Setenv(key, value)
	}
	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.RuntimeMode != "" || cfg.TLSUnverifiedAcknowledged || cfg.TLSDisabledAcknowledged || cfg.AppendOnlyWritableAcknowledged {
		t.Fatalf("ParseEnvTags populated a programmatic field: mode=%q unverified=%v disabled=%v append_only=%v",
			cfg.RuntimeMode, cfg.TLSUnverifiedAcknowledged, cfg.TLSDisabledAcknowledged, cfg.AppendOnlyWritableAcknowledged)
	}
}

// TestPosture_ZeroModeIsBlind: without a mode, plaintext, query logging and the
// acknowledgements change nothing and log nothing.
func TestPosture_ZeroModeIsBlind(t *testing.T) {
	clearLibpqTLSEnv(t)
	logger, buf := captureLogger()
	for _, cfg := range []Config{
		{Logger: logger, LogQueries: true},
		{Logger: logger, Tracer: NewLoggingQueryTracer(logger)},
		{Logger: logger, TLSUnverifiedAcknowledged: true, TLSDisabledAcknowledged: true},
	} {
		for _, dsn := range []string{postureDSN("sslmode=disable"), postureDSN("sslmode=prefer"), "host=/tmp user=u dbname=app"} {
			if _, err := cfg.poolConfig(dsn); err != nil {
				t.Fatalf("zero mode poolConfig(%q) = %v, want nil", dsn, err)
			}
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("zero mode logged posture lines:\n%s", buf)
	}
}

func TestPosture_InvalidModeRefused(t *testing.T) {
	clearLibpqTLSEnv(t)
	for _, mode := range []environment.Mode{"prod", "Production", "staging"} {
		_, err := Config{RuntimeMode: mode}.poolConfig(postureDSN("sslmode=verify-full"))
		if !errors.Is(err, environment.ErrModeInvalid) {
			t.Fatalf("mode %q: err = %v, want environment.ErrModeInvalid", mode, err)
		}
	}
}

// TestPosture_ProductionTLS is the classification matrix over pgx's parse.
func TestPosture_ProductionTLS(t *testing.T) {
	clearLibpqTLSEnv(t)
	cases := []struct {
		name      string
		dsn       string
		cfg       Config
		wantClass tlsClass // empty: admitted
		wantAck   string   // the acknowledgement a refusal names, or the waiver an admission logs
		wantLog   string
	}{
		{name: "disable", dsn: postureDSN("sslmode=disable"), wantClass: tlsPlaintext, wantAck: ackTLSDisabled},
		{name: "verify_full", dsn: postureDSN("sslmode=verify-full")},
		{name: "verify_ca_with_ca", dsn: postureDSN("sslmode=verify-ca&sslrootcert=" + postureCA)},
		{name: "require_with_rootcert", dsn: postureDSN("sslmode=require&sslrootcert=" + postureCA)},
		{name: "rootcert_system", dsn: postureDSN("sslrootcert=system")},
		{name: "verify_full_rootcert_system", dsn: postureDSN("sslmode=verify-full&sslrootcert=system")},
		// The empty sslrootcert overrides a developer's ~/.postgresql/root.crt,
		// which pgconn would otherwise load and turn require into verify-ca.
		{name: "bare_require", dsn: postureDSN("sslmode=require&sslrootcert="), wantClass: tlsUnverified, wantAck: ackTLSUnverified},
		{
			name:    "bare_require_unverified_acknowledged",
			dsn:     postureDSN("sslmode=require&sslrootcert="),
			cfg:     Config{TLSUnverifiedAcknowledged: true},
			wantAck: ackTLSUnverified,
			wantLog: "posture_waiver=" + ackTLSUnverified,
		},
		{
			name:    "bare_require_disabled_acknowledged",
			dsn:     postureDSN("sslmode=require&sslrootcert="),
			cfg:     Config{TLSDisabledAcknowledged: true},
			wantAck: ackTLSDisabled,
			wantLog: "posture_waiver=" + ackTLSDisabled,
		},
		{name: "prefer_plaintext_fallback", dsn: postureDSN("sslmode=prefer"), wantClass: tlsPlaintext, wantAck: ackTLSDisabled},
		{name: "default_sslmode_is_prefer", dsn: postureDSN(""), wantClass: tlsPlaintext, wantAck: ackTLSDisabled},
		{name: "allow", dsn: postureDSN("sslmode=allow"), wantClass: tlsPlaintext, wantAck: ackTLSDisabled},
		{
			name:      "prefer_unverified_acknowledgement_is_not_enough",
			dsn:       postureDSN("sslmode=prefer"),
			cfg:       Config{TLSUnverifiedAcknowledged: true},
			wantClass: tlsPlaintext,
			wantAck:   ackTLSDisabled,
		},
		{
			name:    "disable_acknowledged",
			dsn:     postureDSN("sslmode=disable"),
			cfg:     Config{TLSDisabledAcknowledged: true},
			wantAck: ackTLSDisabled,
			wantLog: "posture_waiver=" + ackTLSDisabled,
		},
		{name: "unix_socket", dsn: "host=/tmp user=u dbname=app", wantLog: "tls=local_socket"},
		{name: "unix_socket_url", dsn: "postgres://u@/app?host=/tmp", wantLog: "tls=local_socket"},
		{
			name:      "socket_then_plaintext_tcp",
			dsn:       "host=/tmp,db.example.com port=5432 user=u password=" + postureSecret + " dbname=app sslmode=disable",
			wantClass: tlsPlaintext,
			wantAck:   ackTLSDisabled,
		},
		{name: "two_verified_hosts", dsn: "postgres://u:" + postureSecret + "@db1.example.com:5432,db2.example.com:5432/app?sslmode=verify-full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf := captureLogger()
			cfg := tc.cfg
			cfg.RuntimeMode = environment.ModeProduction
			cfg.Logger = logger
			cfg.DSN = sdk.Secret(tc.dsn)
			_, err := cfg.poolConfig(tc.dsn)
			if tc.wantClass == "" {
				if err != nil {
					t.Fatalf("poolConfig = %v, want admitted", err)
				}
				if tc.wantLog == "" && buf.Len() != 0 {
					t.Fatalf("admitted verified config logged:\n%s", buf)
				}
				if tc.wantLog != "" && !strings.Contains(buf.String(), tc.wantLog) {
					t.Fatalf("log lacks %q:\n%s", tc.wantLog, buf)
				}
				return
			}
			if !errors.Is(err, ErrTLSRequired) {
				t.Fatalf("poolConfig = %v, want ErrTLSRequired", err)
			}
			msg := err.Error()
			for _, want := range []string{"is " + string(tc.wantClass), "sslmode=verify-full", "sslrootcert=system", "sslmode=verify-ca", "Config." + tc.wantAck} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal lacks %q: %s", want, msg)
				}
			}
			if strings.Contains(msg, postureSecret) {
				t.Errorf("refusal echoes the DSN password: %s", msg)
			}
			if strings.Contains(buf.String(), "posture_waiver") {
				t.Errorf("a refusal logged a waiver:\n%s", buf)
			}
		})
	}
}

// TestPosture_RefusalNamesRedactedTarget: a URL DSN is named masked, a keyword
// DSN only by its failing host.
func TestPosture_RefusalNamesRedactedTarget(t *testing.T) {
	clearLibpqTLSEnv(t)
	cfg := Config{RuntimeMode: environment.ModeProduction, DSN: sdk.Secret(postureDSN("sslmode=disable"))}
	_, err := cfg.poolConfig(cfg.DSN.Reveal())
	if want := cfg.Redacted(); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal %v does not name %q", err, want)
	}
	if !strings.Contains(err.Error(), "db.example.com:5432") {
		t.Fatalf("refusal %v does not name the target", err)
	}
}

// TestPosture_FallbackTargetsAreClassified: a verified primary does not admit a
// plaintext fallback.
func TestPosture_FallbackTargetsAreClassified(t *testing.T) {
	conn := &pgconn.Config{
		Host:      "db1.example.com",
		Port:      5432,
		TLSConfig: &tls.Config{ServerName: "db1.example.com"},
		Fallbacks: []*pgconn.FallbackConfig{{Host: "db2.example.com", Port: 5432}},
	}
	err := Config{RuntimeMode: environment.ModeProduction}.checkPosture(conn)
	if !errors.Is(err, ErrTLSRequired) || !strings.Contains(err.Error(), "db2.example.com:5432") {
		t.Fatalf("checkPosture = %v, want ErrTLSRequired naming the fallback", err)
	}
}

func TestPosture_ProductionRefusesQueryLogging(t *testing.T) {
	clearLibpqTLSEnv(t)
	other := &fakeQueryTracer{}
	argsTracer := NewLoggingQueryTracerWithConfig(LoggingQueryTracerConfig{LogArgs: true})
	for _, tc := range []struct {
		name   string
		cfg    Config
		source string
	}{
		{"log_queries", Config{LogQueries: true}, "Config.LogQueries (DB_LOG_QUERIES)"},
		{"log_query_args_alone", Config{LogQueryArgs: true}, "Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"},
		{"log_queries_with_args", Config{LogQueries: true, LogQueryArgs: true}, "Config.LogQueries (DB_LOG_QUERIES) and Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"},
		{"logging_tracer", Config{Tracer: NewLoggingQueryTracer(nil)}, "Config.Tracer"},
		{"logging_tracer_with_args", Config{Tracer: argsTracer}, "Config.Tracer"},
		{"wrapped_logging_tracer", Config{Tracer: NewMultiQueryTracer(other, NewMultiQueryTracer(NewLoggingQueryTracer(nil)))}, "Config.Tracer"},
		{"acknowledgements_do_not_admit", Config{LogQueries: true, TLSUnverifiedAcknowledged: true, TLSDisabledAcknowledged: true}, "Config.LogQueries (DB_LOG_QUERIES)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.RuntimeMode = environment.ModeProduction
			cfg.Logger = slog.New(slog.DiscardHandler)
			_, err := cfg.poolConfig(postureDSN("sslmode=verify-full"))
			if !errors.Is(err, ErrQueryLoggingRefused) {
				t.Fatalf("poolConfig = %v, want ErrQueryLoggingRefused", err)
			}
			if msg := err.Error(); !strings.Contains(msg, "production refuses query logging") || !strings.Contains(msg, tc.source) {
				t.Fatalf("refusal %q does not name %q", msg, tc.source)
			}
		})
	}

	cfg := Config{RuntimeMode: environment.ModeProduction, Tracer: NewMultiQueryTracer(other)}
	if _, err := cfg.poolConfig(postureDSN("sslmode=verify-full")); err != nil {
		t.Fatalf("a non-logging tracer was refused: %v", err)
	}

	cfg = Config{RuntimeMode: environment.ModeProduction, LogQueries: true}
	_, err := cfg.poolConfig(postureDSN("sslmode=disable"))
	if !errors.Is(err, ErrQueryLoggingRefused) || !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("poolConfig = %v, want both refusals joined", err)
	}
}

// TestPosture_DevelopmentWarns: development admits every condition production
// refuses and logs one WARN for each.
func TestPosture_DevelopmentWarns(t *testing.T) {
	clearLibpqTLSEnv(t)
	logger, buf := captureLogger()
	cfg := Config{RuntimeMode: environment.ModeDevelopment, Logger: logger, LogQueries: true}
	if _, err := cfg.poolConfig(postureDSN("sslmode=disable")); err != nil {
		t.Fatalf("development poolConfig = %v, want admitted", err)
	}
	out := buf.String()
	if got := strings.Count(out, "level=WARN"); got != 2 {
		t.Fatalf("development logged %d WARN lines, want 2:\n%s", got, out)
	}
	for _, want := range []string{"tls=plaintext", `msg="postgres: query logging is on; production refuses it" source="Config.LogQueries (DB_LOG_QUERIES)"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("development log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "posture_waiver") {
		t.Fatalf("development logged a waiver:\n%s", out)
	}

	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"with_args", Config{LogQueries: true, LogQueryArgs: true}, `msg="postgres: query logging is on and logs bind arguments verbatim; production refuses it" source="Config.LogQueries (DB_LOG_QUERIES),Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"`},
		{"args_alone_is_inert", Config{LogQueryArgs: true}, `msg="postgres: LogQueryArgs is inert without LogQueries; production refuses it" source="Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"`},
	} {
		buf.Reset()
		cfg := tc.cfg
		cfg.RuntimeMode = environment.ModeDevelopment
		cfg.Logger = logger
		if _, err := cfg.poolConfig(postureDSN("sslmode=verify-full")); err != nil {
			t.Fatalf("%s: development poolConfig = %v, want admitted", tc.name, err)
		}
		if out := buf.String(); strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: want one WARN %q, got:\n%s", tc.name, tc.want, out)
		}
	}

	buf.Reset()
	if _, err := (Config{RuntimeMode: environment.ModeDevelopment, Logger: logger}).poolConfig("host=/tmp user=u dbname=app"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "level=INFO") || !strings.Contains(buf.String(), "tls=local_socket") {
		t.Fatalf("unix socket did not log the local line:\n%s", buf)
	}
}

// TestPosture_RefusedBeforeConnecting: against an unreachable host, the zero
// mode reaches the ping and production stops at the TLS check first.
func TestPosture_RefusedBeforeConnecting(t *testing.T) {
	clearLibpqTLSEnv(t)
	const unreachable = "postgres://u:" + postureSecret + "@127.0.0.1:1/app?sslmode=disable"

	_, err := Open(context.Background(), Config{DSN: unreachable, ConnectTimeout: 2 * time.Second})
	if err == nil || errors.Is(err, ErrTLSRequired) || !strings.Contains(err.Error(), "pinging database") {
		t.Fatalf("zero mode Open = %v, want the ping failure", err)
	}

	for _, attempts := range []int{0, 3} {
		db, err := Open(context.Background(), Config{
			DSN:            unreachable,
			RuntimeMode:    environment.ModeProduction,
			ConnectTimeout: 2 * time.Second,
			Retry:          RetryPolicy{Attempts: attempts},
		})
		if db != nil || !errors.Is(err, ErrTLSRequired) || strings.Contains(err.Error(), "pinging") || strings.Contains(err.Error(), "verifying") {
			t.Fatalf("production Open (attempts=%d) = %v, %v; want ErrTLSRequired before any connection", attempts, db, err)
		}
	}
}
