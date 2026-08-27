package command

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// newEncrypter builds a real AES-GCM encrypter with a 32-byte key.
func newEncrypter(t *testing.T) cryptids.Encrypter {
	t.Helper()
	enc, err := cryptids.NewAESGCM([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	return enc
}

// sealRaw encrypts an arbitrary plaintext directly through enc, bypassing Seal's
// validation — the only way a test can forge a durable payload whose decoded
// envelope Open must reject (unknown version, malformed stage) or that is not a
// well-formed envelope at all.
func sealRaw(t *testing.T, enc cryptids.Encrypter, plaintext string) []byte {
	t.Helper()
	ct, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt raw: %v", err)
	}
	return []byte(ct)
}

// A rendered command round-trips exactly through Seal/Open.
func TestSealOpenRenderedRoundTrip(t *testing.T) {
	enc := newEncrypter(t)
	env, err := NewRendered("email", "registration_verification", "keyed-digest",
		"user@example.com", "Verify your email", "Your code is 123456",
		"<p>Your code is 123456</p>", "123456")
	if err != nil {
		t.Fatalf("NewRendered: %v", err)
	}
	payload, err := Seal(enc, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := Open(enc, payload)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != env {
		t.Fatalf("round-trip = %+v, want %+v", got, env)
	}
}

// An opaque start command round-trips exactly through Seal/Open.
func TestSealOpenOpaqueRoundTrip(t *testing.T) {
	enc := newEncrypter(t)
	env, err := NewOpaque("phone", "login_code", "keyed-digest", "+15550001111")
	if err != nil {
		t.Fatalf("NewOpaque: %v", err)
	}
	payload, err := Seal(enc, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := Open(enc, payload)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != env || !got.Opaque() {
		t.Fatalf("round-trip = %+v, want opaque %+v", got, env)
	}
}

// The sealed payload is opaque ciphertext: none of the destination, resolution
// input, body, or secret survives as plaintext in the durable bytes.
func TestSealPayloadIsOpaque(t *testing.T) {
	enc := newEncrypter(t)
	env, err := NewRendered("email", "magic_link", "keyed-digest",
		"victim@example.com", "Your sign-in link",
		"Your magic link: https://app.example/redeem?t=SECRETTOKEN",
		"<a>https://app.example/redeem?t=SECRETTOKEN</a>", "SECRETTOKEN")
	if err != nil {
		t.Fatalf("NewRendered: %v", err)
	}
	payload, err := Seal(enc, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	blob := string(payload)
	for _, secret := range []string{
		env.Destination, env.Body, env.HTML, env.Secret, "SECRETTOKEN", "victim",
	} {
		if strings.Contains(blob, secret) {
			t.Fatalf("payload leaks %q in plaintext", secret)
		}
	}
}

// Seal and Open reject a nil encrypter (the durable payload is always sealed).
func TestCodecNilEncrypter(t *testing.T) {
	env, _ := NewOpaque("phone", "login_code", "k", "+15550001111")
	if _, err := Seal(nil, env); !errors.Is(err, ErrEncrypterRequired) {
		t.Fatalf("Seal(nil) err=%v, want ErrEncrypterRequired", err)
	}
	if _, err := Open(nil, nil); !errors.Is(err, ErrEncrypterRequired) {
		t.Fatalf("Open(nil) err=%v, want ErrEncrypterRequired", err)
	}
}

// Seal validates before encrypting: an invalid command never becomes a durable
// payload.
func TestSealRejectsInvalid(t *testing.T) {
	enc := newEncrypter(t)
	// Missing purpose.
	bad := Envelope{Version: Version1, Kind: "phone", Key: "k", Stage: StageOpaque, ResolutionInput: "+1"}
	if _, err := Seal(enc, bad); !errors.Is(err, ErrMissingPurpose) {
		t.Fatalf("Seal(missing purpose) err=%v, want ErrMissingPurpose", err)
	}
}

// Open rejects an UNSEALED durable payload — plaintext bytes that are not valid
// ciphertext — without echoing the payload bytes (which carry the secret) into the
// error.
func TestOpenRejectsUnsealedPayloadNoLeak(t *testing.T) {
	enc := newEncrypter(t)
	env, err := NewRendered("phone", "login_code", "k", "+15550001111",
		"", "Your sign-in code is UNSEALED-LEAK", "", "UNSEALED-LEAK")
	if err != nil {
		t.Fatalf("NewRendered: %v", err)
	}
	// A caller that mistakenly persists the marshaled (unsealed) envelope: the raw
	// JSON carries the secret in the clear.
	unsealed, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = Open(enc, unsealed)
	if !errors.Is(err, ErrUnsealedPayload) {
		t.Fatalf("Open(unsealed) err=%v, want ErrUnsealedPayload", err)
	}
	if strings.Contains(err.Error(), "UNSEALED-LEAK") || strings.Contains(err.Error(), "+15550001111") {
		t.Fatalf("unsealed-payload error leaked payload bytes: %q", err.Error())
	}
}

// Open rejects decrypted-but-malformed bytes without wrapping the unmarshal error —
// which would echo the decrypted plaintext (the secret) into the error string.
func TestOpenRejectsMalformedPayloadNoLeak(t *testing.T) {
	enc := newEncrypter(t)
	// Well-formed ciphertext of an invalid-JSON plaintext that embeds a canary.
	payload := sealRaw(t, enc, `{"secret":"MALFORMED-LEAK","body":"code MALFORMED-LEAK`)
	_, err := Open(enc, payload)
	if !errors.Is(err, ErrMalformedPayload) {
		t.Fatalf("Open(malformed) err=%v, want ErrMalformedPayload", err)
	}
	if strings.Contains(err.Error(), "MALFORMED-LEAK") {
		t.Fatalf("malformed-payload error leaked decrypted plaintext: %q", err.Error())
	}
}

// Open rejects a sealed payload whose envelope declares an unknown version, without
// leaking the sealed content.
func TestOpenRejectsUnknownVersionNoLeak(t *testing.T) {
	enc := newEncrypter(t)
	future := Envelope{
		Version: Version1 + 1, Kind: "email", Purpose: "login_code", Key: "k",
		Stage: StageRendered, Destination: "user@example.com",
		Body: "code VERSION-LEAK", Secret: "VERSION-LEAK",
	}
	raw, _ := json.Marshal(future)
	payload := sealRaw(t, enc, string(raw))
	_, err := Open(enc, payload)
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("Open(future version) err=%v, want ErrUnknownVersion", err)
	}
	if strings.Contains(err.Error(), "VERSION-LEAK") {
		t.Fatalf("unknown-version error leaked payload content: %q", err.Error())
	}
}

// Open rejects a sealed payload with a malformed stage combination (an opaque stage
// carrying rendered content) without leaking the sealed secret.
func TestOpenRejectsMalformedStageNoLeak(t *testing.T) {
	enc := newEncrypter(t)
	mixed := Envelope{
		Version: Version1, Kind: "phone", Purpose: "login_code", Key: "k",
		Stage: StageOpaque, ResolutionInput: "+15550001111",
		Body: "code STAGE-LEAK", Secret: "STAGE-LEAK",
	}
	raw, _ := json.Marshal(mixed)
	payload := sealRaw(t, enc, string(raw))
	_, err := Open(enc, payload)
	if !errors.Is(err, ErrStageMismatch) {
		t.Fatalf("Open(mixed stage) err=%v, want ErrStageMismatch", err)
	}
	if strings.Contains(err.Error(), "STAGE-LEAK") {
		t.Fatalf("stage-mismatch error leaked payload content: %q", err.Error())
	}
}

// Every codec/validation error wraps sdk.ErrInvalidInput so a transport maps it
// uniformly.
func TestCodecErrorsAreInvalidInput(t *testing.T) {
	for _, err := range []error{
		ErrEncrypterRequired, ErrUnknownVersion, ErrMissingKind, ErrMissingPurpose,
		ErrMissingKey, ErrInvalidStage, ErrStageMismatch, ErrUnsealedPayload, ErrMalformedPayload,
	} {
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("%v does not wrap sdk.ErrInvalidInput", err)
		}
	}
}
