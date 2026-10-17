package cryptids

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestParseKeyRing(t *testing.T) {
	k1, k2 := hex.EncodeToString(testKey(1)), hex.EncodeToString(testKey(2))
	ring, err := ParseKeyRing(" k1:"+k1+", k2 : "+k2+" ,", " k2 ")
	if err != nil || ring.Active != "k2" || len(ring.Keys) != 2 || !bytes.Equal(ring.Keys["k1"], testKey(1)) {
		t.Fatalf("parse: %+v %v", ring, err)
	}
	for name, in := range map[string][2]string{
		"empty":           {"", "k1"},
		"active missing":  {"k1:" + k1, "k2"},
		"no colon":        {"k1" + k1, "k1"},
		"bad id":          {"K1:" + k1, "K1"},
		"dot in id":       {"k.1:" + k1, "k.1"},
		"duplicate":       {"k1:" + k1 + ",k1:" + k2, "k1"},
		"not hex":         {"k1:zz", "k1"},
		"short key":       {"k1:" + k1[:62], "k1"},
		"material leaked": {"k1:" + k1[:62], "k1"},
	} {
		_, err := ParseKeyRing(in[0], in[1])
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), k1[:40]) {
			t.Errorf("%s: error carries key material: %v", name, err)
		}
	}
}

func TestAESGCMRingRotation(t *testing.T) {
	old, err := NewAESGCMRing(KeyRing{Active: "k1", Keys: map[string][]byte{"k1": testKey(1)}})
	if err != nil {
		t.Fatal(err)
	}
	const plaintext = "cf-token-é"
	sealedOld, err := old.Encrypt(plaintext)
	if err != nil || !strings.HasPrefix(sealedOld, "v1.k1.") {
		t.Fatalf("encrypt: %q %v", sealedOld, err)
	}

	// Step 2 of a rotation: k2 is active, k1 is still present.
	rotated, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: map[string][]byte{"k1": testKey(1), "k2": testKey(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Decrypt(sealedOld); err != nil || got != plaintext {
		t.Fatalf("retired-but-present key must decrypt: %q %v", got, err)
	}
	if id, err := rotated.KeyIDOf(sealedOld); err != nil || id != "k1" || rotated.ActiveKeyID() != "k2" {
		t.Fatalf("key ids: %q %v active=%q", id, err, rotated.ActiveKeyID())
	}
	sealedNew, err := rotated.Encrypt(plaintext)
	if err != nil || !strings.HasPrefix(sealedNew, "v1.k2.") || sealedNew == sealedOld {
		t.Fatalf("re-encrypt under active: %q %v", sealedNew, err)
	}
	if got, err := rotated.Decrypt(sealedNew); err != nil || got != plaintext {
		t.Fatalf("round trip: %q %v", got, err)
	}

	// Step 4: k1 dropped. Anything still under it is reported, not guessed.
	dropped, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: map[string][]byte{"k2": testKey(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dropped.Decrypt(sealedOld); !errors.Is(err, ErrUnknownKeyID) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("want ErrUnknownKeyID, got %v", err)
	}
	if _, err := dropped.KeyIDOf(sealedOld); err != nil {
		t.Fatalf("KeyIDOf needs no key: %v", err)
	}
}

func TestAESGCMRingRejectsBadCiphertext(t *testing.T) {
	ring, err := NewAESGCMRing(KeyRing{Active: "k1", Keys: map[string][]byte{"k1": testKey(1), "k2": testKey(2)}})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := ring.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.TrimPrefix(sealed, "v1.k1.")

	// Same payload relabelled to another ring key: the key disagrees.
	if _, err := ring.Decrypt("v1.k2." + payload); !errors.Is(err, ErrCiphertextAuthFailed) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("relabelled: %v", err)
	}
	// Flipped tag bit, in the decoded bytes so the text stays valid base64url.
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if _, err := ring.Decrypt("v1.k1." + base64.RawURLEncoding.EncodeToString(raw)); !errors.Is(err, ErrCiphertextAuthFailed) {
		t.Fatalf("tampered: %v", err)
	}
	for name, in := range map[string]string{
		"legacy aesgcm": payload,
		"v2":            "v2.k1." + payload,
		"two segments":  "v1.k1",
		"empty payload": "v1.k1.",
		"bad key id":    "v1.K1." + payload,
		"short payload": "v1.k1.AAAA",
		"not base64":    "v1.k1.!!!",
	} {
		_, err := ring.Decrypt(in)
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: error carries plaintext: %v", name, err)
		}
	}
	if _, err := ring.Decrypt("v2.k1." + payload); !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("scheme: %v", err)
	}
	if _, err := ring.Encrypt(""); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty plaintext: %v", err)
	}
}

func TestNewAESGCMRingValidates(t *testing.T) {
	for name, ring := range map[string]KeyRing{
		"empty":          {Active: "k1"},
		"active missing": {Active: "k2", Keys: map[string][]byte{"k1": testKey(1)}},
		"short key":      {Active: "k1", Keys: map[string][]byte{"k1": testKey(1)[:31]}},
		"bad id":         {Active: "k 1", Keys: map[string][]byte{"k 1": testKey(1)}},
	} {
		if _, err := NewAESGCMRing(ring); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
	}
	var _ Encrypter = (*AESGCMRing)(nil)
}
