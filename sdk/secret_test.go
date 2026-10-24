package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const testSecretValue = "hunter2-not-a-real-secret"

type secretHolder struct {
	Name     string
	Password Secret
	Nested   struct{ Token Secret }
	Pointer  *Secret
}

func newSecretHolder() secretHolder {
	s := Secret(testSecretValue)
	h := secretHolder{Name: "probe", Password: s, Pointer: &s}
	h.Nested.Token = s
	return h
}

func assertNoSecret(t *testing.T, where, got string) {
	t.Helper()
	if strings.Contains(got, testSecretValue) {
		t.Errorf("%s leaks the secret: %s", where, got)
	}
}

func TestSecretFormatsRedactedForEveryVerb(t *testing.T) {
	s := Secret(testSecretValue)
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%10s", "%-20v", "%T"} {
		got := fmt.Sprintf(verb, s)
		if verb == "%T" {
			if got != "sdk.Secret" {
				t.Errorf("%%T = %q", got)
			}
			continue
		}
		if got != Redacted {
			t.Errorf("Sprintf(%q) = %q, want %q", verb, got, Redacted)
		}
	}
	for name, got := range map[string]string{
		"String":   s.String(),
		"GoString": s.GoString(),
		"Sprint":   fmt.Sprint(s),
		"Sprintln": fmt.Sprintln(s),
	} {
		if strings.TrimSpace(got) != Redacted {
			t.Errorf("%s = %q, want %q", name, got, Redacted)
		}
	}
}

func TestSecretInsideExportedFields(t *testing.T) {
	h := newSecretHolder()
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		got := fmt.Sprintf(verb, h)
		assertNoSecret(t, verb, got)
		if !strings.Contains(got, Redacted) {
			t.Errorf("%s of the holder lacks %q: %s", verb, Redacted, got)
		}
	}
	assertNoSecret(t, "%v of a slice", fmt.Sprintf("%v", []Secret{h.Password}))
	assertNoSecret(t, "%v of a map", fmt.Sprintf("%v", map[string]Secret{"k": h.Password}))
}

func TestSecretThroughSlog(t *testing.T) {
	h := newSecretHolder()
	for name, newHandler := range map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(newHandler(&buf))
			log.Info("probe", "password", h.Password, slog.Any("any", h.Password), "holder", h, "list", []Secret{h.Password})
			got := buf.String()
			assertNoSecret(t, name, got)
			if !strings.Contains(got, Redacted) {
				t.Errorf("%s output lacks %q: %s", name, Redacted, got)
			}
		})
	}
}

func TestSecretJSONAndText(t *testing.T) {
	h := newSecretHolder()
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret(t, "json.Marshal", string(raw))
	if !strings.Contains(string(raw), `"Password":"`+Redacted+`"`) {
		t.Errorf("json = %s", raw)
	}

	var decoded struct{ Password Secret }
	if err := json.Unmarshal([]byte(`{"Password":"from-json"}`), &decoded); err != nil || decoded.Password.Reveal() != "from-json" {
		t.Fatalf("unmarshal = %q, %v; want the string kind's own decode", decoded.Password.Reveal(), err)
	}
	var roundTrip secretHolder
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Password.Reveal() != Redacted {
		t.Errorf("round trip stored %q, want %q (documented)", roundTrip.Password.Reveal(), Redacted)
	}

	text, err := Secret(testSecretValue).MarshalText()
	if err != nil || string(text) != Redacted {
		t.Errorf("MarshalText = %q, %v", text, err)
	}
}

func TestSecretRevealAndAssignment(t *testing.T) {
	type config struct{ Password Secret }
	cfg := config{Password: "literal"}
	if cfg.Password.Reveal() != "literal" {
		t.Fatalf("Reveal = %q", cfg.Password.Reveal())
	}
	v := testSecretValue
	if Secret(v).Reveal() != testSecretValue {
		t.Fatal("conversion lost the value")
	}
}

func TestErrRedactedSecretMarksComposedValues(t *testing.T) {
	password := Secret(testSecretValue)
	composed := Secret(fmt.Sprintf("postgres://u:%s@h/db", password))
	if !strings.Contains(composed.Reveal(), Redacted) || strings.Contains(composed.Reveal(), testSecretValue) {
		t.Fatalf("composed = %q; want the marker, not the value", composed.Reveal())
	}
	wrapped := fmt.Errorf("Config.DSN: %w", ErrRedactedSecret)
	if !errors.Is(wrapped, ErrRedactedSecret) {
		t.Fatal("wrapped error does not match ErrRedactedSecret")
	}
	if IsExpected(wrapped) {
		t.Fatal("ErrRedactedSecret is configuration, not a domain sentinel")
	}
}
