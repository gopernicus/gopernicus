package protection

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// testPepper returns a deterministic >=32-byte key seeded by b so tests can vary
// the key material.
func testPepper(b byte) []byte {
	k := make([]byte, minKeyBytes)
	for i := range k {
		k[i] = b
	}
	return k
}

func newTestProtector(t *testing.T, active string, keys map[string][]byte) *HMACChallengeProtector {
	t.Helper()
	p, err := NewHMACChallengeProtector(HMACKeyRing{Active: active, Keys: keys})
	if err != nil {
		t.Fatalf("NewHMACChallengeProtector: %v", err)
	}
	return p
}

// TestChallengeProtectorDigestCodeDeterministic proves the same input digests to
// the same value every time — the property the atomic store's compare relies on.
func TestChallengeProtectorDigestCodeDeterministic(t *testing.T) {
	p := newTestProtector(t, "2026-01", map[string][]byte{"2026-01": testPepper(1)})
	a, err := p.DigestCode("2026-01", "user-1", "login_otp", "123456")
	if err != nil {
		t.Fatalf("DigestCode: %v", err)
	}
	b, err := p.DigestCode("2026-01", "user-1", "login_otp", "123456")
	if err != nil {
		t.Fatalf("DigestCode: %v", err)
	}
	if a != b {
		t.Errorf("digest not deterministic: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("digest length = %d, want 64 hex chars", len(a))
	}
}

// TestChallengeProtectorDomainUserPurposeSeparation proves that changing the
// user, purpose, or code — one field at a time — changes the digest, so digests
// are bound to their identity/purpose/code and cannot cross-replay.
func TestChallengeProtectorDomainUserPurposeSeparation(t *testing.T) {
	p := newTestProtector(t, "k", map[string][]byte{"k": testPepper(9)})
	base, _ := p.DigestCode("k", "user-1", "login_otp", "123456")

	cases := map[string][3]string{
		"different user":    {"user-2", "login_otp", "123456"},
		"different purpose": {"user-1", "change_email", "123456"},
		"different code":    {"user-1", "login_otp", "654321"},
	}
	for name, in := range cases {
		got, err := p.DigestCode("k", in[0], in[1], in[2])
		if err != nil {
			t.Fatalf("%s: DigestCode: %v", name, err)
		}
		if got == base {
			t.Errorf("%s: digest collided with base", name)
		}
	}

	// Length-prefix binding: (user "ab", purpose "c") must not collide with
	// (user "a", purpose "bc") despite sharing a concatenation.
	x, _ := p.DigestCode("k", "ab", "c", "code")
	y, _ := p.DigestCode("k", "a", "bc", "code")
	if x == y {
		t.Error("field-boundary ambiguity: 'ab'+'c' collided with 'a'+'bc'")
	}
}

// TestChallengeProtectorDifferentKeyDiverges proves that the same input under a
// different pepper produces a different digest — the pepper is load-bearing.
func TestChallengeProtectorDifferentKeyDiverges(t *testing.T) {
	p1 := newTestProtector(t, "k", map[string][]byte{"k": testPepper(1)})
	p2 := newTestProtector(t, "k", map[string][]byte{"k": testPepper(2)})
	a, _ := p1.DigestCode("k", "user-1", "login_otp", "123456")
	b, _ := p2.DigestCode("k", "user-1", "login_otp", "123456")
	if a == b {
		t.Error("digest did not diverge under a different pepper")
	}
}

// TestChallengeProtectorKeyRotation proves CandidateCodeDigests returns one
// candidate per accepted key ID and that the old-key candidate equals the digest
// computed directly under the old key — so an unexpired old-key challenge stays
// verifiable during rotation.
func TestChallengeProtectorKeyRotation(t *testing.T) {
	keys := map[string][]byte{
		"2026-01": testPepper(1),
		"2026-02": testPepper(2),
	}
	p := newTestProtector(t, "2026-02", keys)

	if p.ActiveKeyID() != "2026-02" {
		t.Errorf("ActiveKeyID = %q, want 2026-02", p.ActiveKeyID())
	}

	cands, err := p.CandidateCodeDigests("user-1", "login_otp", "123456")
	if err != nil {
		t.Fatalf("CandidateCodeDigests: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}

	byID := map[string]string{}
	for _, c := range cands {
		byID[c.KeyID] = c.Digest
	}
	for id := range keys {
		direct, err := p.DigestCode(id, "user-1", "login_otp", "123456")
		if err != nil {
			t.Fatalf("DigestCode(%s): %v", id, err)
		}
		if byID[id] != direct {
			t.Errorf("candidate for %s = %q, want %q", id, byID[id], direct)
		}
	}
	if byID["2026-01"] == byID["2026-02"] {
		t.Error("candidates under different keys should differ")
	}
}

// TestChallengeProtectorInvalidActiveKeyID proves construction fails when the
// active key ID names no key in the ring, and when Active is empty.
func TestChallengeProtectorInvalidActiveKeyID(t *testing.T) {
	_, err := NewHMACChallengeProtector(HMACKeyRing{
		Active: "missing",
		Keys:   map[string][]byte{"present": testPepper(1)},
	})
	if !errors.Is(err, ErrChallengeActiveKeyMissing) {
		t.Errorf("absent active key: err=%v, want ErrChallengeActiveKeyMissing", err)
	}

	_, err = NewHMACChallengeProtector(HMACKeyRing{
		Keys: map[string][]byte{"k": testPepper(1)},
	})
	if !errors.Is(err, ErrChallengeActiveKeyMissing) {
		t.Errorf("empty active: err=%v, want ErrChallengeActiveKeyMissing", err)
	}
}

// TestChallengeProtectorShortAndMissingKey proves construction rejects an empty
// ring and any key under 32 bytes.
func TestChallengeProtectorShortAndMissingKey(t *testing.T) {
	_, err := NewHMACChallengeProtector(HMACKeyRing{Active: "k", Keys: nil})
	if !errors.Is(err, ErrChallengeKeyRingEmpty) {
		t.Errorf("empty ring: err=%v, want ErrChallengeKeyRingEmpty", err)
	}

	short := make([]byte, minKeyBytes-1)
	_, err = NewHMACChallengeProtector(HMACKeyRing{
		Active: "k",
		Keys:   map[string][]byte{"k": short},
	})
	if !errors.Is(err, ErrChallengeKeyTooShort) {
		t.Errorf("short key: err=%v, want ErrChallengeKeyTooShort", err)
	}
}

// TestChallengeProtectorUnknownKeyID proves DigestCode rejects a key ID not in
// the ring.
func TestChallengeProtectorUnknownKeyID(t *testing.T) {
	p := newTestProtector(t, "k", map[string][]byte{"k": testPepper(1)})
	_, err := p.DigestCode("nope", "user-1", "login_otp", "123456")
	if !errors.Is(err, ErrChallengeUnknownKeyID) {
		t.Errorf("unknown key ID: err=%v, want ErrChallengeUnknownKeyID", err)
	}
}

// TestChallengeProtectorEmptyCode proves an empty code is rejected on both the
// single-key and candidate paths — an empty code is never a legitimate secret.
func TestChallengeProtectorEmptyCode(t *testing.T) {
	p := newTestProtector(t, "k", map[string][]byte{"k": testPepper(1)})
	if _, err := p.DigestCode("k", "user-1", "login_otp", ""); !errors.Is(err, ErrEmptyChallengeCode) {
		t.Errorf("DigestCode empty code: err=%v, want ErrEmptyChallengeCode", err)
	}
	if _, err := p.CandidateCodeDigests("user-1", "login_otp", ""); !errors.Is(err, ErrEmptyChallengeCode) {
		t.Errorf("CandidateCodeDigests empty code: err=%v, want ErrEmptyChallengeCode", err)
	}
}

// TestChallengeProtectorDigestToken proves token digesting is deterministic,
// 64-char hex, and that an empty token digests to the empty string (the
// empty-hash guard so it never matches a stored row).
func TestChallengeProtectorDigestToken(t *testing.T) {
	p := newTestProtector(t, "k", map[string][]byte{"k": testPepper(1)})
	a := p.DigestToken("a-256-bit-url-token")
	b := p.DigestToken("a-256-bit-url-token")
	if a != b {
		t.Errorf("token digest not deterministic: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("token digest length = %d, want 64", len(a))
	}
	if p.DigestToken("") != "" {
		t.Errorf("empty token digest = %q, want empty string", p.DigestToken(""))
	}
	// Token digesting is unkeyed SHA-256, so it does not depend on the pepper.
	p2 := newTestProtector(t, "k", map[string][]byte{"k": testPepper(2)})
	if p2.DigestToken("a-256-bit-url-token") != a {
		t.Error("token digest should be pepper-independent SHA-256")
	}
}

// TestChallengeProtectorNoPlaintextCode proves the returned candidate structures
// carry no plaintext code — only a key ID and an opaque hex digest.
func TestChallengeProtectorNoPlaintextCode(t *testing.T) {
	p := newTestProtector(t, "k", map[string][]byte{"k": testPepper(1)})
	const code = "424242"
	cands, err := p.CandidateCodeDigests("user-1", "login_otp", code)
	if err != nil {
		t.Fatalf("CandidateCodeDigests: %v", err)
	}
	for _, c := range cands {
		if strings.Contains(c.KeyID, code) || strings.Contains(c.Digest, code) {
			t.Errorf("candidate leaked the plaintext code: %+v", c)
		}
		if !isHex(c.Digest) {
			t.Errorf("candidate digest is not hex: %q", c.Digest)
		}
	}
}

// TestConstantTimeDigestEqual proves the comparison helper matches equal digests,
// rejects unequal ones, and treats an empty digest as never-matching.
func TestConstantTimeDigestEqual(t *testing.T) {
	if !ConstantTimeDigestEqual("abcd", "abcd") {
		t.Error("equal digests should compare equal")
	}
	if ConstantTimeDigestEqual("abcd", "abce") {
		t.Error("unequal digests should not compare equal")
	}
	if ConstantTimeDigestEqual("", "") || ConstantTimeDigestEqual("abcd", "") {
		t.Error("empty digest must never match")
	}
}

// TestIdentifierKeyer proves the keyer is deterministic, separates kind and
// value, diverges under a different key, rejects short keys, and never reuses the
// challenge pepper's output for the same key bytes (distinct domain separation).
func TestIdentifierKeyer(t *testing.T) {
	k, err := NewHMACIdentifierKeyer(testPepper(7))
	if err != nil {
		t.Fatalf("NewHMACIdentifierKeyer: %v", err)
	}

	a := k.IdentifierKey("email", "user@example.com")
	if a != k.IdentifierKey("email", "user@example.com") {
		t.Error("identifier key not deterministic")
	}
	if len(a) != 64 || !isHex(a) {
		t.Errorf("identifier key = %q, want 64-char hex", a)
	}
	if strings.Contains(a, "user@example.com") {
		t.Error("identifier key leaked the raw identifier")
	}

	if a == k.IdentifierKey("phone", "user@example.com") {
		t.Error("kind must separate keys")
	}
	if a == k.IdentifierKey("email", "other@example.com") {
		t.Error("value must separate keys")
	}

	k2, _ := NewHMACIdentifierKeyer(testPepper(8))
	if a == k2.IdentifierKey("email", "user@example.com") {
		t.Error("identifier key did not diverge under a different key")
	}

	if _, err := NewHMACIdentifierKeyer(make([]byte, minKeyBytes-1)); !errors.Is(err, ErrIdentifierKeyTooShort) {
		t.Errorf("short key: err=%v, want ErrIdentifierKeyTooShort", err)
	}
}

// TestIdentifierKeyAuditRecipe pins the HMAC input the AUDIT-054 lookup recipe
// rebuilds in the shell: three fields — the domain label, the kind and the
// normalized value — each an 8-byte big-endian BYTE length then the bytes, under
// HMAC-SHA-256 keyed with the hex-decoded AUTH_IDENTIFIER_KEY. Each want value is
// what the recipe printed for that key and value, so a change to the keyer's
// construction fails here before it silently falsifies the recipe.
func TestIdentifierKeyAuditRecipe(t *testing.T) {
	key, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	keyer, err := NewHMACIdentifierKeyer(key)
	if err != nil {
		t.Fatalf("NewHMACIdentifierKeyer: %v", err)
	}
	const prefix = "\x00\x00\x00\x00\x00\x00\x00\x21gopernicus.auth.identifier-key.v1" + "\x00\x00\x00\x00\x00\x00\x00\x05email"
	long := strings.Repeat("a", 288) + "@example.com"
	for _, tc := range []struct {
		value, input, recipe string
	}{
		{"alice@example.com", prefix + "\x00\x00\x00\x00\x00\x00\x00\x11alice@example.com",
			"35916db2967bd336a7f3e7b94ebee7e4cd70017d104cb28c7427e8d77a51f478"},
		{"jos\u00e9@example.com", prefix + "\x00\x00\x00\x00\x00\x00\x00\x11jos\u00e9@example.com",
			"46167489a92ee365470faef711ec8aee73d12b11043beb95da3f2d361aad5b71"},
		{long, prefix + "\x00\x00\x00\x00\x00\x00\x01\x2c" + long,
			"9c3d50a74975a7387f4034c14f23cc787342d6bb1bae63de90f312fda4eab059"},
	} {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(tc.input))
		documented := hex.EncodeToString(mac.Sum(nil))
		got := keyer.IdentifierKey("email", tc.value)
		if got != documented || got != tc.recipe {
			t.Errorf("IdentifierKey(email, %.20q…) = %s; documented input digests to %s, recipe printed %s", tc.value, got, documented, tc.recipe)
		}
	}
}

// TestIdentifierKeyerDomainSeparatedFromChallenge proves that even if a host
// misconfigured the identifier keyer with the SAME key bytes as the challenge
// pepper, the two never produce the same digest — the domain labels differ. This
// is defense-in-depth; the two keys are required to be distinct regardless.
func TestIdentifierKeyerDomainSeparatedFromChallenge(t *testing.T) {
	same := testPepper(3)
	keyer, _ := NewHMACIdentifierKeyer(same)
	protector := newTestProtector(t, "k", map[string][]byte{"k": same})

	idKey := keyer.IdentifierKey("login_otp", "user-1")
	codeDigest, _ := protector.DigestCode("k", "user-1", "login_otp", "user-1")
	if idKey == codeDigest {
		t.Error("identifier key collided with a challenge code digest under the same key bytes")
	}
}

// isHex reports whether s is a non-empty lowercase hex string.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
