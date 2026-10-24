package sendgrid_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/email/sendgrid"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestSenderAndConfigNeverPrintTheAPIKey proves the key stays out of every fmt
// verb and both slog handlers: Sender holds it in an unexported field, which
// fmt reads without calling any method, so Sender formats and logs itself.
func TestSenderAndConfigNeverPrintTheAPIKey(t *testing.T) {
	const key = "SG.hunter2-api-key"
	cfg := sendgrid.Config{APIKey: key, Host: "https://api.example.test"}
	sender, err := sendgrid.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	for _, v := range []any{sender, cfg} {
		fmt.Fprintf(&out, "%v %+v %#v %s\n", v, v, v, v)
	}
	slog.New(slog.NewTextHandler(&out, nil)).Info("text", "sender", sender, "config", cfg)
	slog.New(slog.NewJSONHandler(&out, nil)).Info("json", "sender", sender, "config", cfg)

	if strings.Contains(out.String(), key) || strings.Contains(out.String(), "hunter2") {
		t.Fatalf("output carries the API key:\n%s", out.String())
	}
	for _, want := range []string{sdk.Redacted, "https://api.example.test", `"sender":{"host":"https://api.example.test"}`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
