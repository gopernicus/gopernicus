package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

const leaked = "LEAKED-VALUE"

type groupValuer struct{}

func (groupValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("inner", leaked), slog.Int("n", 1))
}

type secretValuer struct{}

func (secretValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("password", leaked), slog.String("visible", "ok"))
}

func newRedactLogger(buf *bytes.Buffer, keys []string) *slog.Logger {
	return slog.New(NewRedactHandler(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}), keys))
}

func TestRedactHandler_DefaultKeysAndNormalization(t *testing.T) {
	var buf bytes.Buffer
	log := newRedactLogger(&buf, append(DefaultRedactKeys, "x-custom"))
	args := []any{"visible", "ok"}
	for _, key := range DefaultRedactKeys {
		args = append(args, key, leaked)
	}
	args = append(args,
		"PASSWORD", leaked, "Set_Cookie", leaked, "x_custom", leaked, "X-CUSTOM", leaked,
		"refresh_token_hash", "kept", "tokens", "kept", "my_password", "kept")
	log.Info("probe", args...)

	entry := parseJSON(t, buf.Bytes())
	if strings.Contains(buf.String(), leaked) {
		t.Fatalf("a matching key leaked: %s", buf.String())
	}
	for _, key := range append(append([]string(nil), DefaultRedactKeys...), "PASSWORD", "Set_Cookie", "x_custom", "X-CUSTOM") {
		if entry[key] != sdk.Redacted {
			t.Errorf("%s = %v, want %q", key, entry[key], sdk.Redacted)
		}
	}
	for _, key := range []string{"visible", "refresh_token_hash", "tokens", "my_password"} {
		if entry[key] == sdk.Redacted {
			t.Errorf("%s was redacted; matching is exact", key)
		}
	}
}

func TestRedactHandler_MatchingKeyReplacesWhateverKind(t *testing.T) {
	var buf bytes.Buffer
	log := newRedactLogger(&buf, DefaultRedactKeys)
	log.Info("probe",
		slog.Any("token", groupValuer{}),
		slog.Group("password", slog.String("inner", leaked)),
		slog.Int("secret", -987654321),
		slog.Any("dsn", map[string]string{"k": leaked}),
	)
	entry := parseJSON(t, buf.Bytes())
	if strings.Contains(buf.String(), leaked) || strings.Contains(buf.String(), "-987654321") {
		t.Fatalf("a matching key kept its value: %s", buf.String())
	}
	for _, key := range []string{"token", "password", "secret", "dsn"} {
		if entry[key] != sdk.Redacted {
			t.Errorf("%s = %v, want one redacted string", key, entry[key])
		}
	}
}

func TestRedactHandler_RecursesIntoNonMatchingGroups(t *testing.T) {
	var buf bytes.Buffer
	log := newRedactLogger(&buf, DefaultRedactKeys)
	log.Info("probe",
		slog.Group("g", slog.String("token", leaked), slog.String("visible", "ok"),
			slog.Group("deeper", slog.String("api_key", leaked))),
		slog.Any("valuer", secretValuer{}),
		slog.Group("", slog.String("cookie", leaked), slog.String("inline", "ok")),
	)
	if strings.Contains(buf.String(), leaked) {
		t.Fatalf("a nested matching key leaked: %s", buf.String())
	}
	entry := parseJSON(t, buf.Bytes())
	g := entry["g"].(map[string]any)
	if g["token"] != sdk.Redacted || g["visible"] != "ok" || g["deeper"].(map[string]any)["api_key"] != sdk.Redacted {
		t.Errorf("group g = %v", g)
	}
	valuer := entry["valuer"].(map[string]any)
	if valuer["password"] != sdk.Redacted || valuer["visible"] != "ok" {
		t.Errorf("resolved LogValuer = %v", valuer)
	}
	if entry["cookie"] != sdk.Redacted || entry["inline"] != "ok" {
		t.Errorf("empty-key group not treated as inline: %v", entry)
	}
}

func TestRedactHandler_WithAttrsAndWithGroup(t *testing.T) {
	var buf bytes.Buffer
	bound := []slog.Attr{slog.String("password", leaked), slog.String("service", "svc")}
	log := slog.New(NewRedactHandler(slog.NewJSONHandler(&buf, nil), DefaultRedactKeys).WithAttrs(bound)).WithGroup("app")
	log.Info("probe", "token", leaked, "visible", "ok")
	if bound[0].Value.String() != leaked {
		t.Fatal("WithAttrs mutated the caller's slice")
	}
	if strings.Contains(buf.String(), leaked) {
		t.Fatalf("leaked: %s", buf.String())
	}
	entry := parseJSON(t, buf.Bytes())
	app := entry["app"].(map[string]any)
	if entry["password"] != sdk.Redacted || entry["service"] != "svc" || app["token"] != sdk.Redacted || app["visible"] != "ok" {
		t.Errorf("entry = %v", entry)
	}
}

func TestRedactHandler_EnabledAndKeysAreExact(t *testing.T) {
	h := NewRedactHandler(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}), nil)
	if h.Enabled(context.Background(), slog.LevelInfo) || !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("Enabled does not delegate")
	}
	var buf bytes.Buffer
	newRedactLogger(&buf, []string{"probe_only", " ", ""}).Info("probe", "password", "kept", "probe_only", leaked)
	entry := parseJSON(t, buf.Bytes())
	if entry["password"] != "kept" || entry["probe_only"] != sdk.Redacted {
		t.Fatalf("NewRedactHandler must redact exactly its keys: %v", entry)
	}
}

func TestNew_RedactsDefaultsAndRedactKeys(t *testing.T) {
	ctx := sdk.WithRequestID(context.Background(), "req-1")
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			_, stderr := captureOutput(t, func() {
				log := New(Options{Format: format, RedactKeys: []string{"probe_only"}})
				log.InfoContext(ctx, "probe", "password", leaked, "x-api-key", leaked,
					slog.Group("g", "token", leaked), "probe_only", leaked, "visible", "ok")
			})
			if strings.Contains(stderr, leaked) {
				t.Fatalf("New leaked a redacted key: %s", stderr)
			}
			for _, want := range []string{"visible", "ok", "req-1", sdk.Redacted} {
				if !strings.Contains(stderr, want) {
					t.Errorf("output lacks %q: %s", want, stderr)
				}
			}
		})
	}
	if len(DefaultRedactKeys) != 17 {
		t.Fatalf("DefaultRedactKeys changed length to %d; update the README policy paragraph", len(DefaultRedactKeys))
	}
}

// x_api_key is a default key; a header-derived spelling matches in its '_'
// form, and a longer key does not (the list is exact, never a prefix).
func TestRedactHandler_HeaderDerivedKeys(t *testing.T) {
	var buf bytes.Buffer
	newRedactLogger(&buf, DefaultRedactKeys).Info("probe",
		"x_api_key", leaked, "x-api-key", leaked, "X-API-Key", leaked, "x_api_key_id", "kept", "api_key_name", "kept")
	if strings.Contains(buf.String(), leaked) {
		t.Fatalf("a header-derived key leaked: %s", buf.String())
	}
	entry := parseJSON(t, buf.Bytes())
	for _, key := range []string{"x_api_key_id", "api_key_name"} {
		if entry[key] != "kept" {
			t.Errorf("%s = %v; the list is exact, not a prefix", key, entry[key])
		}
	}
}
