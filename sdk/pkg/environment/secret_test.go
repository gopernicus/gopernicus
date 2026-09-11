package environment

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

const (
	// 32 bytes hex-encoded.
	hex32Bytes = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	// 16 bytes hex-encoded.
	hex16Bytes = "0123456789abcdef0123456789abcdef"
)

func TestDecodeSecretEmptyIsNil(t *testing.T) {
	raw, err := DecodeSecret("", 32)
	if err != nil {
		t.Fatalf("DecodeSecret(\"\", 32) error = %v, want nil", err)
	}
	if raw != nil {
		t.Fatalf("DecodeSecret(\"\", 32) = %v, want nil", raw)
	}
}

func TestDecodeSecretDecodesAtMinimum(t *testing.T) {
	want, err := hex.DecodeString(hex32Bytes)
	if err != nil {
		t.Fatalf("hex.DecodeString error = %v", err)
	}

	raw, err := DecodeSecret(hex32Bytes, 32)
	if err != nil {
		t.Fatalf("DecodeSecret error = %v, want nil", err)
	}
	if len(raw) != 32 {
		t.Fatalf("len(raw) = %d, want 32", len(raw))
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("raw = %x, want %x", raw, want)
	}
}

func TestDecodeSecretUnderMinimum(t *testing.T) {
	raw, err := DecodeSecret(hex16Bytes, 32)
	if raw != nil {
		t.Errorf("raw = %x, want nil", raw)
	}
	if !errors.Is(err, ErrSecretUnderMinimum) {
		t.Fatalf("err = %v, want errors.Is ErrSecretUnderMinimum", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "32") || !strings.Contains(msg, "16") {
		t.Errorf("error %q does not name both 32 and 16", msg)
	}
}

func TestDecodeSecretMinBytesZeroDisablesCheck(t *testing.T) {
	raw, err := DecodeSecret("ff", 0)
	if err != nil {
		t.Fatalf("DecodeSecret(\"ff\", 0) error = %v, want nil", err)
	}
	if !bytes.Equal(raw, []byte{0xff}) {
		t.Errorf("raw = %x, want ff", raw)
	}
}

func TestDecodeSecretEncodingFailures(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"non-hex characters", "zz"},
		{"odd length", "abc"},
		{"leftover comment text", "# >=32 bytes; hex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := DecodeSecret(tt.value, 32)
			if raw != nil {
				t.Errorf("raw = %x, want nil", raw)
			}
			if !errors.Is(err, ErrSecretEncoding) {
				t.Fatalf("DecodeSecret(%q) err = %v, want errors.Is ErrSecretEncoding", tt.value, err)
			}
		})
	}
}

func TestSecretUnsetIsNil(t *testing.T) {
	const key = "TEST_SECRET_UNSET"
	t.Setenv(key, "placeholder") // registers cleanup; the unset below is restored
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv error = %v", err)
	}

	raw, err := Secret(key, 32)
	if err != nil {
		t.Fatalf("Secret error = %v, want nil", err)
	}
	if raw != nil {
		t.Fatalf("raw = %x, want nil", raw)
	}
}

func TestSecretEmptyIsNil(t *testing.T) {
	const key = "TEST_SECRET_EMPTY"
	t.Setenv(key, "")

	raw, err := Secret(key, 32)
	if err != nil {
		t.Fatalf("Secret error = %v, want nil", err)
	}
	if raw != nil {
		t.Fatalf("raw = %x, want nil", raw)
	}
}

func TestSecretDecodes(t *testing.T) {
	const key = "TEST_SECRET_GOOD"
	t.Setenv(key, hex32Bytes)

	want, err := hex.DecodeString(hex32Bytes)
	if err != nil {
		t.Fatalf("hex.DecodeString error = %v", err)
	}

	raw, err := Secret(key, 32)
	if err != nil {
		t.Fatalf("Secret error = %v, want nil", err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("raw = %x, want %x", raw, want)
	}
}

func TestSecretErrorsNameTheKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		sentinel error
	}{
		{"encoding", "TEST_SECRET_BAD_HEX", "zz", ErrSecretEncoding},
		{"under minimum", "TEST_SECRET_SHORT", hex16Bytes, ErrSecretUnderMinimum},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)

			raw, err := Secret(tt.key, 32)
			if raw != nil {
				t.Errorf("raw = %x, want nil", raw)
			}
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("err = %v, want errors.Is %v", err, tt.sentinel)
			}
			if !strings.HasPrefix(err.Error(), tt.key+": ") {
				t.Errorf("error %q does not begin with the key name", err.Error())
			}
		})
	}
}
