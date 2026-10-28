package cryptids

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
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

func TestAESGCMRingLegacyRead(t *testing.T) {
	single, err := NewAESGCM(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	const plaintext = "provider-token-é"
	legacy, err := single.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string][]byte{"legacy": testKey(1), "k2": testKey(2)}

	ring, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: keys}, WithLegacyKeyID("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if ring.LegacyKeyID() != "legacy" {
		t.Fatalf("LegacyKeyID: %q", ring.LegacyKeyID())
	}
	if got, err := ring.Decrypt(legacy); err != nil || got != plaintext {
		t.Fatalf("legacy opens: %q %v", got, err)
	}
	if id, err := ring.KeyIDOf(legacy); err != nil || id != "legacy" {
		t.Fatalf("KeyIDOf legacy: %q %v", id, err)
	}
	sealed, err := ring.Encrypt(plaintext)
	if err != nil || !strings.HasPrefix(sealed, "v1.k2.") {
		t.Fatalf("non-legacy active writes prefixed: %q %v", sealed, err)
	}
	if got, err := ring.Decrypt(sealed); err != nil || got != plaintext {
		t.Fatalf("prefixed still opens: %q %v", got, err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if _, err := ring.Decrypt(base64.RawURLEncoding.EncodeToString(raw)); !errors.Is(err, ErrCiphertextAuthFailed) {
		t.Fatalf("tampered legacy: %v", err)
	}
	if _, err := ring.Decrypt(""); !errors.Is(err, ErrCiphertextMalformed) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := ring.KeyIDOf(""); !errors.Is(err, ErrCiphertextMalformed) {
		t.Fatalf("KeyIDOf empty: %v", err)
	}

	plain, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	if plain.LegacyKeyID() != "" {
		t.Fatalf("no option LegacyKeyID: %q", plain.LegacyKeyID())
	}
	if _, err := plain.Decrypt(legacy); !errors.Is(err, ErrCiphertextMalformed) {
		t.Fatalf("no option: want ErrCiphertextMalformed, got %v", err)
	}
	if _, err := plain.KeyIDOf(legacy); !errors.Is(err, ErrCiphertextMalformed) {
		t.Fatalf("no option KeyIDOf: %v", err)
	}
	if _, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: keys}, WithLegacyKeyID("")); err != nil {
		t.Fatalf("empty id is no option: %v", err)
	}
}

func TestAESGCMRingLegacyMissing(t *testing.T) {
	_, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: map[string][]byte{"k2": testKey(2)}}, WithLegacyKeyID("legacy"))
	if !errors.Is(err, ErrLegacyKeyMissing) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("want ErrLegacyKeyMissing, got %v", err)
	}
}

func TestAESGCMRingLegacyActiveWritesSingleKeyShape(t *testing.T) {
	single, err := NewAESGCM(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	ring, err := NewAESGCMRing(KeyRing{Active: "legacy", Keys: map[string][]byte{"legacy": testKey(1), "k2": testKey(2)}}, WithLegacyKeyID("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	const plaintext = "envelope"
	sealed, err := ring.Encrypt(plaintext)
	if err != nil || strings.Contains(sealed, ".") {
		t.Fatalf("legacy-active writes unprefixed: %q %v", sealed, err)
	}
	if got, err := single.Decrypt(sealed); err != nil || got != plaintext {
		t.Fatalf("plain AESGCM opens ring output: %q %v", got, err)
	}
	fromSingle, err := single.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ring.Decrypt(fromSingle); err != nil || got != plaintext {
		t.Fatalf("ring opens plain AESGCM output: %q %v", got, err)
	}
}

func TestParseKeyRingMinimum(t *testing.T) {
	k32, k64 := hex.EncodeToString(testKey(1)), hex.EncodeToString(bytes.Repeat([]byte{2}, 64))
	ring, err := ParseKeyRingMinimum("k1:"+k32+",k2:"+k64, "k2", 32)
	if err != nil || len(ring.Keys["k2"]) != 64 {
		t.Fatalf("minimum parse: %v", err)
	}
	if err := ring.ValidateMinimum(32); err != nil {
		t.Fatalf("ValidateMinimum: %v", err)
	}
	if err := ring.Validate(); !errors.Is(err, ErrKeyMalformed) {
		t.Fatalf("Validate keeps exact-32: %v", err)
	}
	if _, err := ParseKeyRing("k1:"+k32+",k2:"+k64, "k2"); !errors.Is(err, ErrKeyMalformed) {
		t.Fatalf("ParseKeyRing keeps exact-32: %v", err)
	}
	for name, in := range map[string]struct {
		keys string
		min  int
	}{
		"below min":          {"k1:" + k32, 33},
		"floor raised to 32": {"k1:" + k32[:62], 16},
		"not hex":            {"k1:zz" + k64[2:], 32},
		"empty":              {"", 32},
	} {
		_, err := ParseKeyRingMinimum(in.keys, "k1", in.min)
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
		if err != nil && (strings.Contains(err.Error(), k32[:40]) || strings.Contains(err.Error(), k64[:40])) {
			t.Errorf("%s: error carries key material: %v", name, err)
		}
	}
}

func TestAESGCMRingLegacyErrorsCarryNoBytes(t *testing.T) {
	single, err := NewAESGCM(testKey(3))
	if err != nil {
		t.Fatal(err)
	}
	const plaintext = "do-not-echo-this"
	foreign, err := single.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: map[string][]byte{"legacy": testKey(1), "k2": testKey(2)}}, WithLegacyKeyID("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]string{
		"wrong key":  foreign,
		"not base64": "!!!" + foreign,
		"short":      "AAAA",
	} {
		_, err := ring.Decrypt(in)
		if err == nil {
			t.Fatalf("%s: want error", name)
		}
		msg := err.Error()
		for _, leak := range []string{plaintext, foreign, hex.EncodeToString(testKey(1)), string(testKey(1))} {
			if strings.Contains(msg, leak) {
				t.Errorf("%s: error carries bytes: %v", name, err)
			}
		}
	}
	if _, err := ring.Decrypt(foreign); !errors.Is(err, ErrCiphertextAuthFailed) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := ring.Decrypt("!!!" + foreign); !errors.Is(err, ErrCiphertextMalformed) {
		t.Fatalf("not base64: %v", err)
	}
}

func TestAESGCMRingKeyIDs(t *testing.T) {
	ring, err := NewAESGCMRing(KeyRing{Active: "k2", Keys: map[string][]byte{"k2": testKey(2), "legacy": testKey(1), "a1": testKey(3)}}, WithLegacyKeyID("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	ids := ring.KeyIDs()
	if want := []string{"a1", "k2", "legacy"}; !slices.Equal(ids, want) {
		t.Fatalf("KeyIDs: %v, want %v", ids, want)
	}
	ids[0] = "mutated"
	if got := ring.KeyIDs(); got[0] != "a1" {
		t.Fatalf("KeyIDs leaks its slice: %v", got)
	}
	if _, err := ring.Decrypt("v1.mutated.AAAA"); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("mutation reached the ring: %v", err)
	}
}
