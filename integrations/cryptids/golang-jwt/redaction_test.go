package golangjwt_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestSignerNeverPrintsTheSecret proves the HMAC secret stays out of every fmt
// verb and both slog handlers: Signer holds it in an unexported []byte, which
// fmt reads without calling any method, so Signer formats and logs itself.
func TestSignerNeverPrintsTheSecret(t *testing.T) {
	signer, err := golangjwt.New(testSecret)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	for _, v := range []any{signer, &golangjwt.Signer{}} {
		fmt.Fprintf(&out, "%v %+v %#v %s %x\n", v, v, v, v, v)
	}
	slog.New(slog.NewTextHandler(&out, nil)).Info("text", "signer", signer)
	slog.New(slog.NewJSONHandler(&out, nil)).Info("json", "signer", signer)

	secretBytes := fmt.Sprint([]byte(testSecret))
	for _, leak := range []string{testSecret, fmt.Sprintf("%x", testSecret), secretBytes[1 : len(secretBytes)-1]} {
		if strings.Contains(out.String(), leak) {
			t.Fatalf("output carries the secret as %q:\n%s", leak, out.String())
		}
	}
	for _, want := range []string{sdk.Redacted, "HS256", `"signer":{"method":"HS256"}`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
