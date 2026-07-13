package delivery

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
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

// A sealed envelope round-trips exactly.
func TestEnvelopeSealOpenRoundTrip(t *testing.T) {
	enc := newEncrypter(t)
	env := Envelope{
		Destination:     "user@example.com",
		ResolutionInput: "user@example.com",
		Subject:         "Your code",
		Body:            "Your code is 123456",
		Secret:          "123456",
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

// The sealed payload is opaque ciphertext: none of the destination, message, or
// account-resolution input survives as plaintext in the job's only payload column.
// This is the "encrypted payload envelope only" invariant — a leaked outbox row
// exposes ciphertext, not PII or a rendered secret.
func TestEnvelopePayloadIsOpaque(t *testing.T) {
	enc := newEncrypter(t)
	env := Envelope{
		Destination:     "victim@example.com",
		ResolutionInput: "victim@example.com",
		Subject:         "Sign in",
		Body:            "Your magic link: https://app.example/redeem?t=SECRETTOKEN",
		Secret:          "SECRETTOKEN",
	}
	payload, err := Seal(enc, env)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Persist the payload exactly as a store would: the job carries only the
	// opaque blob, and the deliveryjob.Job type has NO plaintext destination /
	// message / identifier field to leak.
	job := deliveryjob.Job{
		Kind:           "email",
		Purpose:        "login_magic_link",
		IdempotencyKey: "keyed-digest",
		Payload:        payload,
		State:          deliveryjob.StatePending,
	}

	blob := string(job.Payload)
	for _, secret := range []string{
		env.Destination, env.ResolutionInput, env.Body, env.Secret, "SECRETTOKEN", "victim",
	} {
		if strings.Contains(blob, secret) {
			t.Fatalf("payload leaks %q in plaintext: %q", secret, blob)
		}
	}
}

// Seal/Open reject a nil encrypter (DeliveryEncrypter is required).
func TestEnvelopeNilEncrypter(t *testing.T) {
	if _, err := Seal(nil, Envelope{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Seal(nil) err=%v, want ErrInvalidInput", err)
	}
	if _, err := Open(nil, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Open(nil) err=%v, want ErrInvalidInput", err)
	}
}
