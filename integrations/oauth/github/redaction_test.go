package github

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestProviderAndConfigNeverPrintTheClientSecret proves the secret stays out of
// every fmt verb and both slog handlers: Provider holds its Config in an
// unexported field, which fmt reads without calling any method, so Provider
// formats and logs itself.
func TestProviderAndConfigNeverPrintTheClientSecret(t *testing.T) {
	p, _ := newFakeGitHub(t, nil)
	cfg := Config{ClientID: testClientID, ClientSecret: testClientSecret}

	var out bytes.Buffer
	for _, v := range []any{p, cfg} {
		fmt.Fprintf(&out, "%v %+v %#v %s\n", v, v, v, v)
	}
	slog.New(slog.NewTextHandler(&out, nil)).Info("text", "provider", p, "config", cfg)
	slog.New(slog.NewJSONHandler(&out, nil)).Info("json", "provider", p, "config", cfg)

	if strings.Contains(out.String(), testClientSecret) {
		t.Fatalf("output carries the client secret:\n%s", out.String())
	}
	for _, want := range []string{sdk.Redacted, testClientID, `"provider":{"provider":"github","client_id":"` + testClientID + `"}`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
