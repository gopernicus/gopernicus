package golangjwt_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
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

// TestRingSignerNeverPrintsAKey extends the redaction proof to RingSigner and
// to its errors, which never carry a key or token contents.
func TestRingSignerNeverPrintsAKey(t *testing.T) {
	r := newRing(t, "k2", map[string][]byte{"legacy": []byte(testSecret), "k2": ringKey2}, golangjwt.WithLegacyKeyID("legacy"))

	var out bytes.Buffer
	for _, v := range []any{r, &golangjwt.RingSigner{}} {
		fmt.Fprintf(&out, "%v %+v %#v %s %x\n", v, v, v, v, v)
	}
	slog.New(slog.NewTextHandler(&out, nil)).Info("text", "signer", r)
	slog.New(slog.NewJSONHandler(&out, nil)).Info("json", "signer", r)

	const payload = "claim-value-that-must-not-leak"
	foreign := signWithHeader(t, jwt.SigningMethodHS256, []byte(strings.Repeat("f", 32)), map[string]any{"kid": "k2"})
	tampered := strings.Replace(foreign, ".", ".x", 1)
	for _, token := range []string{
		foreign, tampered, "not-a-token-" + payload,
		signWithHeader(t, jwt.SigningMethodHS256, ringKey2, map[string]any{"kid": "K-" + payload}),
		signWithHeader(t, jwt.SigningMethodHS384, ringKey2, map[string]any{"kid": "k2", "x": payload}),
	} {
		if _, err := r.Verify(token); err == nil {
			t.Fatalf("token verified: %s", token)
		} else {
			fmt.Fprintln(&out, err)
		}
	}
	for _, ring := range []cryptids.KeyRing{
		{Active: "k1", Keys: map[string][]byte{"k1": []byte(testSecret[:31])}},
		{Active: "k9", Keys: map[string][]byte{"k1": ringKey2}},
	} {
		if _, err := golangjwt.NewRing(ring); err == nil {
			t.Fatal("NewRing accepted a bad ring")
		} else {
			fmt.Fprintln(&out, err)
		}
	}

	for _, key := range [][]byte{[]byte(testSecret), ringKey2} {
		asBytes := fmt.Sprint(key)
		for _, leak := range []string{string(key), fmt.Sprintf("%x", key), asBytes[1 : len(asBytes)-1]} {
			if strings.Contains(out.String(), leak) {
				t.Fatalf("output carries a key as %q:\n%s", leak, out.String())
			}
		}
	}
	if strings.Contains(out.String(), payload) || strings.Contains(out.String(), foreign) {
		t.Fatalf("output carries token contents:\n%s", out.String())
	}
	for _, want := range []string{sdk.Redacted, "Active:k2 Keys:2 Legacy:true",
		`"signer":{"method":"HS256","active_key_id":"k2","key_count":2,"legacy":true}`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
