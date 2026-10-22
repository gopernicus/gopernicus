package turso

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
			cfg := Config{URL: url, RuntimeMode: environment.ModeProduction, Logger: slog.New(slog.DiscardHandler)}
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
		cfg := Config{URL: url, RuntimeMode: environment.ModeProduction, Logger: logger}
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
		db, err := Open(context.Background(), Config{URL: url, RuntimeMode: environment.ModeProduction})
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
		URL:                     server.URL,
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
	if err := cfg.checkPosture(cfg.URL); err != nil || buf.Len() != 0 {
		t.Fatalf("acknowledgement over a TLS URL = %v, logged %q; want inert", err, buf)
	}
}

func TestPosture_DevelopmentWarnsAndZeroModeIsBlind(t *testing.T) {
	logger, buf := postureLogger()
	cfg := Config{URL: "http://127.0.0.1:8080?authToken=" + postureSecret, RuntimeMode: environment.ModeDevelopment, Logger: logger}
	if err := cfg.checkPosture(cfg.URL); err != nil {
		t.Fatalf("development checkPosture = %v, want admitted", err)
	}
	out := buf.String()
	if strings.Count(out, "level=WARN") != 1 || strings.Contains(out, "posture_waiver") || strings.Contains(out, postureSecret) {
		t.Fatalf("want one redacted WARN and no waiver, got:\n%s", out)
	}

	buf.Reset()
	cfg.RuntimeMode = ""
	cfg.TLSDisabledAcknowledged = true
	if err := cfg.checkPosture(cfg.URL); err != nil || buf.Len() != 0 {
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
