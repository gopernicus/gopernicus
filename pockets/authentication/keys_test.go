package authentication

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

var allKeys = []KeyName{KeyJWT, KeyChallengePepper, KeyIdentifier, KeyDeliveryEncrypter, KeyTokenEncrypter, KeyTOTPRing}

func hexKey(fill byte, n int) string { return strings.Repeat(fmt.Sprintf("%02x", fill), n) }

func fullKeysInput() KeysInput {
	return KeysInput{
		JWTSecret:            hexKey(0x01, 32),
		ChallengePepper:      hexKey(0x02, 48),
		IdentifierKey:        hexKey(0x03, 32),
		DeliveryEncrypterKey: hexKey(0x04, 32),
		TokenEncrypterKey:    hexKey(0x05, 32),
		TOTPKeys:             "t1:" + hexKey(0x06, 32) + ",t0:" + hexKey(0x07, 32),
		TOTPActiveKey:        "t1",
	}
}

// TestKeyNamesMatchKeysInputTags pins each KeyName to the variable its
// KeysInput field is parsed from, so an error naming a KeyName names the
// variable a host must set.
func TestKeyNamesMatchKeysInputTags(t *testing.T) {
	typ := reflect.TypeOf(KeysInput{})
	if typ.NumField() != len(allKeys)+1 {
		t.Fatalf("KeysInput has %d fields, want %d", typ.NumField(), len(allKeys)+1)
	}
	if got := typ.Field(len(allKeys)).Tag.Get("env"); got != keyTOTPActive {
		t.Errorf("KeysInput.TOTPActiveKey env tag = %q, want %q", got, keyTOTPActive)
	}
	for i, name := range allKeys {
		if got := typ.Field(i).Tag.Get("env"); got != string(name) {
			t.Errorf("KeysInput.%s env tag = %q, want %q", typ.Field(i).Name, got, name)
		}
	}
}

// TestKeysInputParsesFromEnvironment proves the host recipe: ParseEnvTags fills
// KeysInput from the five variables.
func TestKeysInputParsesFromEnvironment(t *testing.T) {
	want := fullKeysInput()
	t.Setenv(string(KeyJWT), want.JWTSecret)
	t.Setenv(string(KeyChallengePepper), want.ChallengePepper)
	t.Setenv(string(KeyIdentifier), want.IdentifierKey)
	t.Setenv(string(KeyDeliveryEncrypter), want.DeliveryEncrypterKey)
	t.Setenv(string(KeyTokenEncrypter), want.TokenEncrypterKey)
	t.Setenv(string(KeyTOTPRing), want.TOTPKeys)
	t.Setenv(keyTOTPActive, want.TOTPActiveKey)
	var in KeysInput
	if err := environment.ParseEnvTags("", &in); err != nil {
		t.Fatal(err)
	}
	if in != want {
		t.Fatal("ParseEnvTags did not fill every KeysInput field")
	}
}

func TestNewKeysDecodesEveryKey(t *testing.T) {
	for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
		keys, err := NewKeys(mode, fullKeysInput(), slog.New(slog.DiscardHandler), allKeys...)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		for name, got := range map[string][]byte{"jwt": keys.JWTSecret, "pepper": keys.ChallengePepper, "identifier": keys.IdentifierKey, "delivery": keys.DeliveryEncrypterKey, "token": keys.TokenEncrypterKey} {
			if len(got) < 32 {
				t.Errorf("%s: %s key decoded to %d bytes", mode, name, len(got))
			}
		}
		if len(keys.ChallengePepper) != 48 || keys.ChallengePepper[0] != 0x02 {
			t.Errorf("%s: pepper not decoded from hex: %x", mode, keys.ChallengePepper)
		}
	}
}

// TestNewKeysProductionJoinsEveryUnsetNeededKey proves a production host sees
// every missing variable in one error, and that an unneeded unset key and the
// optional token key are not reported.
func TestNewKeysProductionJoinsEveryUnsetNeededKey(t *testing.T) {
	_, err := NewKeys(environment.ModeProduction, KeysInput{}, slog.New(slog.DiscardHandler), KeyJWT, KeyChallengePepper, KeyDeliveryEncrypter, KeyTokenEncrypter)
	if !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("err = %v, want ErrKeyRequired", err)
	}
	for _, name := range []KeyName{KeyJWT, KeyChallengePepper, KeyDeliveryEncrypter} {
		if !strings.Contains(err.Error(), string(name)+" is unset") {
			t.Errorf("joined error does not name %s: %v", name, err)
		}
	}
	for _, name := range []KeyName{KeyIdentifier, KeyTokenEncrypter} {
		if strings.Contains(err.Error(), string(name)) {
			t.Errorf("error names %s, which is unneeded or optional: %v", name, err)
		}
	}
}

func TestNewKeysMalformedValues(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*KeysInput)
		variable KeyName
		want     error
	}{
		{"odd length", func(in *KeysInput) { in.JWTSecret = in.JWTSecret[:63] }, KeyJWT, environment.ErrSecretEncoding},
		{"non-hex byte", func(in *KeysInput) { in.ChallengePepper = "zz" + in.ChallengePepper[2:] }, KeyChallengePepper, environment.ErrSecretEncoding},
		{"short", func(in *KeysInput) { in.IdentifierKey = hexKey(0x03, 31) }, KeyIdentifier, environment.ErrSecretUnderMinimum},
		{"short encrypter", func(in *KeysInput) { in.DeliveryEncrypterKey = hexKey(0x04, 16) }, KeyDeliveryEncrypter, environment.ErrSecretUnderMinimum},
		{"long encrypter", func(in *KeysInput) { in.TokenEncrypterKey = hexKey(0x05, 33) }, KeyTokenEncrypter, nil},
		{"raw 32-character encrypter", func(in *KeysInput) { in.DeliveryEncrypterKey = "0123456789abcdef0123456789abcdef" }, KeyDeliveryEncrypter, environment.ErrSecretUnderMinimum},
	}
	for _, tc := range cases {
		for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
			t.Run(tc.name+"/"+string(mode), func(t *testing.T) {
				in := fullKeysInput()
				tc.mutate(&in)
				// A malformed value is an error even when the key is not needed.
				_, err := NewKeys(mode, in, slog.New(slog.DiscardHandler))
				if !errors.Is(err, ErrKeyRequired) {
					t.Fatalf("err = %v, want ErrKeyRequired", err)
				}
				if tc.want != nil && !errors.Is(err, tc.want) {
					t.Errorf("err = %v, want it to wrap %v", err, tc.want)
				}
				if !strings.Contains(err.Error(), string(tc.variable)) {
					t.Errorf("err does not name %s: %v", tc.variable, err)
				}
				if strings.Contains(err.Error(), "invalid byte") || strings.Contains(err.Error(), "odd length") || strings.Contains(err.Error(), "U+") {
					t.Errorf("err carries the hex decoder's text: %v", err)
				}
			})
		}
	}
}

// TestNewKeysErrorsNeverEchoValues proves no value, raw or decoded, reaches an
// error even when several keys fail together.
func TestNewKeysErrorsNeverEchoValues(t *testing.T) {
	in := KeysInput{JWTSecret: "secretvaluethatisnothex", IdentifierKey: hexKey(0xab, 8), DeliveryEncrypterKey: hexKey(0xcd, 40)}
	_, err := NewKeys(environment.ModeProduction, in, slog.New(slog.DiscardHandler), allKeys...)
	if err == nil {
		t.Fatal("malformed keys accepted")
	}
	for _, value := range []string{in.JWTSecret, "secretvalue", in.IdentifierKey, in.DeliveryEncrypterKey, "abab", "cdcd"} {
		if strings.Contains(err.Error(), value) {
			t.Errorf("error echoes %q: %v", value, err)
		}
	}
	for _, name := range []KeyName{KeyJWT, KeyChallengePepper, KeyIdentifier, KeyDeliveryEncrypter} {
		if !strings.Contains(err.Error(), string(name)) {
			t.Errorf("joined error does not name %s: %v", name, err)
		}
	}
}

// TestNewKeysDevelopmentMintsEphemeralKeys proves development fills each unset
// needed key with a random key and one WARN, leaves unneeded keys nil, and
// never mints the optional token key.
func TestNewKeysDevelopmentMintsEphemeralKeys(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	keys, err := NewKeys(environment.ModeDevelopment, KeysInput{}, log, KeyJWT, KeyChallengePepper, KeyDeliveryEncrypter, KeyTokenEncrypter)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys.JWTSecret) != 32 || len(keys.ChallengePepper) != 32 || len(keys.DeliveryEncrypterKey) != 32 {
		t.Errorf("ephemeral keys not minted: %v", keys)
	}
	if bytes.Equal(keys.JWTSecret, keys.ChallengePepper) {
		t.Error("two ephemeral keys share material")
	}
	if keys.IdentifierKey != nil {
		t.Error("an unneeded key was minted")
	}
	if keys.TokenEncrypterKey != nil {
		t.Error("the token encrypter key was minted; it must stay nil when unset")
	}
	logs := buf.String()
	if n := strings.Count(logs, "level=WARN msg=\"authentication: key unset; using an EPHEMERAL per-boot key"); n != 3 {
		t.Fatalf("ephemeral WARN count = %d, want 3:\n%s", n, logs)
	}
	for _, name := range []KeyName{KeyJWT, KeyChallengePepper, KeyDeliveryEncrypter} {
		if !strings.Contains(logs, "key="+string(name)) {
			t.Errorf("no WARN names %s:\n%s", name, logs)
		}
	}
	if strings.Count(logs, "level=INFO") != 1 || !strings.Contains(logs, "AUTH_TOKEN_ENCRYPTER_KEY is unset") {
		t.Errorf("want one INFO line for the unset token key:\n%s", logs)
	}
}

// TestNewKeysTokenEncrypterOptionalInProduction proves O-2: the token key unset
// is nil, never an error and never ephemeral, in production too.
func TestNewKeysTokenEncrypterOptionalInProduction(t *testing.T) {
	in := fullKeysInput()
	in.TokenEncrypterKey = ""
	keys, err := NewKeys(environment.ModeProduction, in, slog.New(slog.DiscardHandler), allKeys...)
	if err != nil {
		t.Fatalf("unset token key in production: %v", err)
	}
	if keys.TokenEncrypterKey != nil {
		t.Error("token key is not nil")
	}
}

func TestNewKeysRejectsUnknownNameAndInvalidMode(t *testing.T) {
	if _, err := NewKeys(environment.ModeDevelopment, KeysInput{}, nil, KeyName("AUTH_NOPE")); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("unknown key name: err = %v, want sdk.ErrInvalidInput", err)
	}
	if _, err := NewKeys("", KeysInput{}, nil); !errors.Is(err, ErrRuntimeModeRequired) {
		t.Errorf("empty mode: err = %v, want ErrRuntimeModeRequired", err)
	}
	if _, err := NewKeys("Production", KeysInput{}, nil); !errors.Is(err, ErrRuntimeModeInvalid) {
		t.Errorf("invalid mode: err = %v, want ErrRuntimeModeInvalid", err)
	}
}

// TestKeysPrintRedacted proves neither type leaks material through fmt or slog.
func TestKeysPrintRedacted(t *testing.T) {
	in := fullKeysInput()
	keys, err := NewKeys(environment.ModeProduction, in, slog.New(slog.DiscardHandler), allKeys...)
	if err != nil {
		t.Fatal(err)
	}
	keys.TokenEncrypterKey = nil
	var logged bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logged, nil))
	log.Info("keys", "keys", keys, "input", in)
	outputs := []string{
		fmt.Sprint(keys), fmt.Sprintf("%v %+v %#v %s", keys, keys, keys, keys),
		fmt.Sprintf("%v %+v %#v", &keys, struct{ K Keys }{keys}, in),
		fmt.Sprint(in), logged.String(),
	}
	for _, out := range outputs {
		for _, secret := range []string{in.JWTSecret, in.ChallengePepper, in.IdentifierKey, in.DeliveryEncrypterKey, string(keys.JWTSecret), "[1 1 1", "0x01"} {
			if strings.Contains(out, secret) {
				t.Errorf("output leaks key material %q: %s", secret, out)
			}
		}
	}
	if got := fmt.Sprint(keys); !strings.Contains(got, "JWTSecret:[redacted]") || !strings.Contains(got, "TokenEncrypterKey:[unset]") {
		t.Errorf("String() = %q, want presence markers", got)
	}
}

// TestNewKeysTOTPRing pins the sixth key: AUTH_TOTP_KEYS / AUTH_TOTP_ACTIVE_KEY
// parse into a ring that builds an AES-GCM ring encrypter; production refuses
// it unset only when it is needed; development mints a one-key ephemeral ring
// with the WARN; a malformed ring is ErrKeyRequired naming the variables and
// never echoing material.
func TestNewKeysTOTPRing(t *testing.T) {
	keys, err := NewKeys(environment.ModeProduction, fullKeysInput(), slog.New(slog.DiscardHandler), KeyTOTPRing)
	if err != nil {
		t.Fatal(err)
	}
	if keys.TOTPRing.Active != "t1" || len(keys.TOTPRing.Keys) != 2 || keys.TOTPRing.Keys["t0"][0] != 0x07 {
		t.Fatalf("ring = active %q, %d keys", keys.TOTPRing.Active, len(keys.TOTPRing.Keys))
	}
	if _, err := cryptids.NewAESGCMRing(keys.TOTPRing); err != nil {
		t.Fatalf("NewAESGCMRing: %v", err)
	}
	if _, err := NewKeys(environment.ModeProduction, KeysInput{}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("an unneeded unset ring in production: %v", err)
	}
	if _, err := NewKeys(environment.ModeProduction, KeysInput{}, slog.New(slog.DiscardHandler), KeyTOTPRing); !errors.Is(err, ErrKeyRequired) || !strings.Contains(err.Error(), "AUTH_TOTP_KEYS is unset") {
		t.Fatalf("a needed unset ring in production = %v", err)
	}
	var buf bytes.Buffer
	dev, err := NewKeys(environment.ModeDevelopment, KeysInput{}, slog.New(slog.NewTextHandler(&buf, nil)), KeyTOTPRing)
	if err != nil || dev.TOTPRing.Active != "ephemeral" || len(dev.TOTPRing.Keys["ephemeral"]) != 32 || !strings.Contains(buf.String(), "key=AUTH_TOTP_KEYS") {
		t.Fatalf("development ring = %v, %v, %q", dev.TOTPRing.Active, err, buf.String())
	}
	if none, _ := NewKeys(environment.ModeDevelopment, KeysInput{}, slog.New(slog.DiscardHandler)); len(none.TOTPRing.Keys) != 0 {
		t.Fatal("an unneeded ring was minted")
	}
	for _, bad := range []KeysInput{
		{TOTPKeys: "t1:" + hexKey(0x06, 16), TOTPActiveKey: "t1"},
		{TOTPKeys: "t1:" + hexKey(0x06, 32), TOTPActiveKey: "t2"},
		{TOTPKeys: "t1:zz", TOTPActiveKey: "t1"},
		{TOTPActiveKey: "t1"},
	} {
		_, err := NewKeys(environment.ModeDevelopment, bad, slog.New(slog.DiscardHandler))
		if !errors.Is(err, ErrKeyRequired) || !strings.Contains(err.Error(), "AUTH_TOTP_KEYS") || strings.Contains(err.Error(), hexKey(0x06, 16)) {
			t.Errorf("malformed ring %+v = %v", bad.TOTPActiveKey, err)
		}
	}
	if got := fmt.Sprint(keys); !strings.Contains(got, "TOTPRing:[redacted]") || strings.Contains(got, hexKey(0x06, 4)) {
		t.Errorf("String() = %q", got)
	}
}
