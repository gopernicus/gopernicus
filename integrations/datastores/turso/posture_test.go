package turso

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// postureSecret is the URL credential every refusal must keep out of its text.
const postureSecret = "posture-s3cret"

func postureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// TestPosture_FieldsCarryNoEnvTag pins the mode and the acknowledgement as
// programmatic: ParseEnvTags leaves them zero with same-named variables set.
func TestPosture_FieldsCarryNoEnvTag(t *testing.T) {
	typ := reflect.TypeFor[Config]()
	for _, name := range []string{"RuntimeMode", "TLSDisabledAcknowledged"} {
		field, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("Config.%s is missing", name)
		}
		if tag := field.Tag.Get("env"); tag != "" {
			t.Fatalf("Config.%s carries env tag %q", name, tag)
		}
	}
	for _, key := range []string{"RuntimeMode", "RUNTIME_MODE", "DB_RUNTIME_MODE", "APP_RUNTIME_MODE"} {
		t.Setenv(key, "production")
	}
	for _, key := range []string{"TLSDisabledAcknowledged", "TLS_DISABLED_ACKNOWLEDGED", "DB_TLS_DISABLED_ACKNOWLEDGED"} {
		t.Setenv(key, "true")
	}
	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.RuntimeMode != "" || cfg.TLSDisabledAcknowledged {
		t.Fatalf("ParseEnvTags populated a programmatic field: mode=%q acknowledged=%v", cfg.RuntimeMode, cfg.TLSDisabledAcknowledged)
	}
}

// TestPosture_ProductionRefusesPlaintext: each refusal happens in Open before
// any I/O and names the redacted target, never its credential.
func TestPosture_ProductionRefusesPlaintext(t *testing.T) {
	for _, url := range []string{
		"http://127.0.0.1:8080?authToken=" + postureSecret,
		"ws://127.0.0.1:8080?authToken=" + postureSecret,
		"libsql://127.0.0.1:8080?tls=0&authToken=" + postureSecret,
		"libsql://127.0.0.1:8080?tls=0&tls=1",
		"example.test",
	} {
		t.Run(url, func(t *testing.T) {
			cfg := Config{URL: sdk.Secret(url), RuntimeMode: environment.ModeProduction, Logger: slog.New(slog.DiscardHandler)}
			db, err := Open(context.Background(), cfg)
			if db != nil || !errors.Is(err, ErrTLSRequired) {
				t.Fatalf("Open = %v, %v; want ErrTLSRequired", db, err)
			}
			msg := err.Error()
			if strings.Contains(msg, postureSecret) {
				t.Fatalf("refusal echoes the credential: %s", msg)
			}
			for _, want := range []string{cfg.Redacted(), "libsql://, https:// or wss://", "Config." + ackTLSDisabled} {
				if !strings.Contains(msg, want) {
					t.Fatalf("refusal lacks %q: %s", want, msg)
				}
			}
		})
	}
}

func TestPosture_ProductionAdmitsTLSAndLocalFiles(t *testing.T) {
	for _, url := range []string{
		"libsql://example.test",
		"libsql://example.test:8080?tls=1",
		"libsql://example.test?tls=1&tls=0",
		"https://example.test",
		"wss://example.test",
		"file:" + filepath.Join(t.TempDir(), "posture.db"),
	} {
		logger, buf := postureLogger()
		cfg := Config{URL: sdk.Secret(url), RuntimeMode: environment.ModeProduction, Logger: logger}
		dsn, err := cfg.dsn()
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.checkPosture(dsn); err != nil {
			t.Fatalf("checkPosture(%q) = %v, want admitted", url, err)
		}
		if buf.Len() != 0 {
			t.Fatalf("admitted %q logged:\n%s", url, buf)
		}
	}

	// Through Open: libsql and https ping lazily, a file opens locally.
	for _, url := range []string{"libsql://example.test", "https://example.test", "file:" + filepath.Join(t.TempDir(), "posture.db")} {
		db, err := Open(context.Background(), Config{URL: sdk.Secret(url), RuntimeMode: environment.ModeProduction})
		if err != nil {
			t.Fatalf("Open(%q) = %v, want admitted", url, err)
		}
		_ = db.Close()
	}
}

func TestPosture_AcknowledgedAdmitsWithWaiver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	logger, buf := postureLogger()
	db, err := Open(context.Background(), Config{
		URL:                     sdk.Secret(server.URL),
		RuntimeMode:             environment.ModeProduction,
		TLSDisabledAcknowledged: true,
		Logger:                  logger,
	})
	if err != nil {
		t.Fatalf("acknowledged Open = %v, want admitted", err)
	}
	_ = db.Close()
	out := buf.String()
	if strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, "posture_waiver="+ackTLSDisabled) {
		t.Fatalf("want one posture_waiver WARN, got:\n%s", out)
	}

	// Inert when nothing is waived.
	buf.Reset()
	cfg := Config{URL: "libsql://example.test", RuntimeMode: environment.ModeProduction, TLSDisabledAcknowledged: true, Logger: logger}
	if err := cfg.checkPosture(cfg.URL.Reveal()); err != nil || buf.Len() != 0 {
		t.Fatalf("acknowledgement over a TLS URL = %v, logged %q; want inert", err, buf)
	}
}

func TestPosture_DevelopmentWarnsAndZeroModeIsBlind(t *testing.T) {
	logger, buf := postureLogger()
	cfg := Config{URL: "http://127.0.0.1:8080?authToken=" + postureSecret, RuntimeMode: environment.ModeDevelopment, Logger: logger}
	if err := cfg.checkPosture(cfg.URL.Reveal()); err != nil {
		t.Fatalf("development checkPosture = %v, want admitted", err)
	}
	out := buf.String()
	if strings.Count(out, "level=WARN") != 1 || strings.Contains(out, "posture_waiver") || strings.Contains(out, postureSecret) {
		t.Fatalf("want one redacted WARN and no waiver, got:\n%s", out)
	}

	buf.Reset()
	cfg.RuntimeMode = ""
	cfg.TLSDisabledAcknowledged = true
	if err := cfg.checkPosture(cfg.URL.Reveal()); err != nil || buf.Len() != 0 {
		t.Fatalf("zero mode = %v, logged %q; want blind", err, buf)
	}
}

func TestPosture_InvalidModeRefused(t *testing.T) {
	_, err := Open(context.Background(), Config{URL: "libsql://example.test", RuntimeMode: "prod"})
	if !errors.Is(err, environment.ErrModeInvalid) {
		t.Fatalf("Open = %v, want environment.ErrModeInvalid", err)
	}
}

// TestPosture_RefusedBeforeConnecting: the zero mode dials an unreachable
// WebSocket and fails at the ping; production stops at the scheme first.
func TestPosture_RefusedBeforeConnecting(t *testing.T) {
	const unreachable = "ws://127.0.0.1:1"
	_, err := Open(context.Background(), Config{URL: unreachable})
	if err == nil || errors.Is(err, ErrTLSRequired) || !strings.Contains(err.Error(), "pinging libsql database") {
		t.Fatalf("zero mode Open = %v, want the ping failure", err)
	}
	for _, attempts := range []int{0, 3} {
		_, err := Open(context.Background(), Config{URL: unreachable, RuntimeMode: environment.ModeProduction, Retry: RetryPolicy{Attempts: attempts}})
		if !errors.Is(err, ErrTLSRequired) || strings.Contains(err.Error(), "pinging") || strings.Contains(err.Error(), "verifying") {
			t.Fatalf("production Open (attempts=%d) = %v, want ErrTLSRequired before any connection", attempts, err)
		}
	}
}

// TestPosture_ProductionRefusesQueryLogging: the refusal runs before the TLS
// early return — a file: URL has no transport to judge — and joins with a TLS
// refusal; no acknowledgement admits it, and it names field and variable.
func TestPosture_ProductionRefusesQueryLogging(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    Config
		source string
	}{
		{"log_queries", Config{LogQueries: true}, "Config.LogQueries (DB_LOG_QUERIES)"},
		{"log_query_args_alone", Config{LogQueryArgs: true}, "Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"},
		{"both", Config{LogQueries: true, LogQueryArgs: true}, "Config.LogQueries (DB_LOG_QUERIES) and Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"},
		{"acknowledgement_does_not_admit", Config{LogQueries: true, TLSDisabledAcknowledged: true}, "Config.LogQueries (DB_LOG_QUERIES)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "never-created")
			cfg := tc.cfg
			cfg.URL = sdk.Secret("file:" + filepath.Join(dir, "probe.db"))
			cfg.RuntimeMode = environment.ModeProduction
			cfg.Logger = slog.New(slog.DiscardHandler)
			db, err := Open(context.Background(), cfg)
			if db != nil || !errors.Is(err, ErrQueryLoggingRefused) || errors.Is(err, ErrTLSRequired) {
				t.Fatalf("Open = %v, %v; want ErrQueryLoggingRefused alone", db, err)
			}
			if msg := err.Error(); !strings.Contains(msg, "turso: production refuses query logging") || !strings.Contains(msg, tc.source) {
				t.Fatalf("refusal %q does not name %q", msg, tc.source)
			}
			if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
				t.Fatalf("a refused Open touched the filesystem: %v", statErr)
			}
		})
	}

	_, err := Open(context.Background(), Config{URL: "http://127.0.0.1:8080?authToken=" + postureSecret, LogQueries: true, RuntimeMode: environment.ModeProduction})
	if !errors.Is(err, ErrQueryLoggingRefused) || !errors.Is(err, ErrTLSRequired) || strings.Contains(err.Error(), postureSecret) {
		t.Fatalf("Open = %v, want both refusals joined without the credential", err)
	}
}

// TestPosture_DevelopmentQueryLogging: development admits query logging with
// one WARN; LogQueryArgs alone installs no tracer.
func TestPosture_DevelopmentQueryLogging(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cfg        Config
		want       string
		tracer     bool
		tracerArgs bool
	}{
		{"log_queries", Config{LogQueries: true}, `msg="turso: query logging is on; production refuses it" source="Config.LogQueries (DB_LOG_QUERIES)"`, true, false},
		{"with_args", Config{LogQueries: true, LogQueryArgs: true}, `msg="turso: query logging is on and logs bind arguments verbatim; production refuses it" source="Config.LogQueries (DB_LOG_QUERIES),Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"`, true, true},
		{"args_alone_is_inert", Config{LogQueryArgs: true}, `msg="turso: LogQueryArgs is inert without LogQueries; production refuses it" source="Config.LogQueryArgs (DB_LOG_QUERY_ARGS)"`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf := postureLogger()
			cfg := tc.cfg
			cfg.URL = sdk.Secret("file:" + filepath.Join(t.TempDir(), "probe.db"))
			cfg.RuntimeMode = environment.ModeDevelopment
			cfg.Logger = logger
			db, err := Open(context.Background(), cfg)
			if err != nil {
				t.Fatalf("development Open = %v, want admitted", err)
			}
			defer db.Close()
			if out := buf.String(); strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, tc.want) {
				t.Fatalf("want one WARN %q, got:\n%s", tc.want, out)
			}
			if (db.tracer != nil) != tc.tracer || (db.tracer != nil && db.tracer.logArgs != tc.tracerArgs) {
				t.Fatalf("tracer = %+v, want installed=%v args=%v", db.tracer, tc.tracer, tc.tracerArgs)
			}
		})
	}
}

// TestOpen_RefusesRedactedSecret: a URL or AuthToken composed from a rendered
// Secret fails in every mode, naming the field and Reveal(), before any I/O.
func TestOpen_RefusesRedactedSecret(t *testing.T) {
	token := sdk.Secret(postureSecret)
	for _, tc := range []struct {
		name   string
		cfg    Config
		fields []string
	}{
		{"url", Config{URL: sdk.Secret(fmt.Sprintf("libsql://example.test?authToken=%s", token))}, []string{"Config.URL"}},
		{"auth_token", Config{URL: "libsql://example.test", AuthToken: sdk.Secret(fmt.Sprint(token))}, []string{"Config.AuthToken"}},
		{"both_production", Config{URL: sdk.Secret("http://example.test?jwt=" + token.String()), AuthToken: sdk.Secret(token.String()), RuntimeMode: environment.ModeProduction}, []string{"Config.URL", "Config.AuthToken"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(context.Background(), tc.cfg)
			if db != nil || !errors.Is(err, sdk.ErrRedactedSecret) || errors.Is(err, ErrTLSRequired) {
				t.Fatalf("Open = %v, %v; want only sdk.ErrRedactedSecret", db, err)
			}
			for _, want := range append(tc.fields, "Reveal()") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal lacks %q: %s", want, err)
				}
			}
		})
	}
}

// TestConfig_SecretFieldsRedact: URL and AuthToken render as sdk.Redacted
// through fmt and slog, and ParseEnvTags still fills them.
func TestConfig_SecretFieldsRedact(t *testing.T) {
	t.Setenv("DB_URL", "libsql://example.test?authToken="+postureSecret)
	t.Setenv("DB_AUTH_TOKEN", postureSecret)
	t.Setenv("DB_LOG_QUERY_ARGS", "true")
	var cfg Config
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		t.Fatalf("ParseEnvTags: %v", err)
	}
	if cfg.AuthToken.Reveal() != postureSecret || !strings.Contains(cfg.URL.Reveal(), postureSecret) || !cfg.LogQueryArgs {
		t.Fatal("ParseEnvTags did not fill the secret fields or LogQueryArgs")
	}
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("config", slog.Any("cfg", cfg), slog.Any("url", cfg.URL))
	for _, out := range []string{fmt.Sprintf("%v %+v %#v %s", cfg, cfg, cfg, cfg.AuthToken), logged.String()} {
		if strings.Contains(out, postureSecret) {
			t.Fatalf("a secret field printed its value: %s", out)
		}
	}
}
