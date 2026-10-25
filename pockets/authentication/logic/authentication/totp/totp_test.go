package totp

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// rfcSecret is the SHA-1 test key of RFC 4226 Appendix D and RFC 6238 Appendix B.
var rfcSecret = []byte("12345678901234567890")

// TestCodeRFC4226 pins the HOTP values of RFC 4226 Appendix D.
func TestCodeRFC4226(t *testing.T) {
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for counter, code := range want {
		if got := Code(rfcSecret, int64(counter)); got != code {
			t.Errorf("Code(counter %d) = %s, want %s", counter, got, code)
		}
	}
}

// TestCodeRFC6238 pins the SHA-1 rows of RFC 6238 Appendix B, truncated to six
// digits (the eight-digit value modulo 10^6).
func TestCodeRFC6238(t *testing.T) {
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		at := time.Unix(tc.unix, 0)
		if got := Code(rfcSecret, Step(at)); got != tc.code {
			t.Errorf("Code at %d = %s, want %s", tc.unix, got, tc.code)
		}
		step, ok := Validate(rfcSecret, tc.code, at, 0)
		if !ok || step != Step(at) {
			t.Errorf("Validate at %d = %d, %v", tc.unix, step, ok)
		}
	}
}

func TestValidateWindow(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	now := Step(at)
	for _, offset := range []int64{-1, 0, 1} {
		step, ok := Validate(rfcSecret, Code(rfcSecret, now+offset), at, 1)
		if !ok || step != now+offset {
			t.Errorf("offset %d: Validate = %d, %v", offset, step, ok)
		}
	}
	for _, offset := range []int64{-2, 2} {
		if _, ok := Validate(rfcSecret, Code(rfcSecret, now+offset), at, 1); ok {
			t.Errorf("offset %d accepted outside the window", offset)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "12 34 5x"} {
		if _, ok := Validate(rfcSecret, bad, at, 1); ok {
			t.Errorf("Validate(%q) accepted", bad)
		}
	}
	spaced := Code(rfcSecret, now)
	if _, ok := Validate(rfcSecret, spaced[:3]+" "+spaced[3:], at, 1); !ok {
		t.Error("a code typed with a space was refused")
	}
}

func TestKeyURI(t *testing.T) {
	uri, err := KeyURI("Gopernicus CMS", "a@example.com", rfcSecret)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "otpauth" || u.Host != "totp" {
		t.Fatalf("KeyURI = %q, %v", uri, err)
	}
	if !strings.HasPrefix(uri, "otpauth://totp/Gopernicus%20CMS:a@example.com?") {
		t.Fatalf("label = %q", uri)
	}
	q := u.Query()
	if q.Get("secret") != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" || q.Get("issuer") != "Gopernicus CMS" || q.Get("algorithm") != "SHA1" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Fatalf("query = %v", q)
	}
	if !strings.Contains(uri, "issuer=Gopernicus%20CMS") || strings.Contains(uri, "+") {
		t.Fatalf("issuer query = %q, want %%20 for the space", uri)
	}
	odd, err := KeyURI("Gopernicus CMS", "a:b+c@example.com", rfcSecret)
	if err != nil || !strings.HasPrefix(odd, "otpauth://totp/Gopernicus%20CMS:a%3Ab+c@example.com?") || strings.Count(odd, ":") != 2 {
		t.Fatalf("label with a colon = %q, %v", odd, err)
	}
	for _, tc := range []struct{ issuer, account string }{{"", "a"}, {"a:b", "a"}, {"Issuer", ""}} {
		if _, err := KeyURI(tc.issuer, tc.account, rfcSecret); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("KeyURI(%q, %q) = %v", tc.issuer, tc.account, err)
		}
	}
}

func TestSecretEncoding(t *testing.T) {
	secret, err := NewSecret()
	if err != nil || len(secret) != SecretBytes {
		t.Fatalf("NewSecret = %d bytes, %v", len(secret), err)
	}
	encoded := EncodeSecret(secret)
	if strings.Contains(encoded, "=") || len(encoded) != 32 {
		t.Fatalf("EncodeSecret = %q", encoded)
	}
	grouped := FormatSecret(secret)
	if len(strings.Fields(grouped)) != 8 {
		t.Fatalf("FormatSecret = %q", grouped)
	}
	back, err := DecodeSecret(strings.ToLower(grouped))
	if err != nil || string(back) != string(secret) {
		t.Fatalf("DecodeSecret(FormatSecret) = %v", err)
	}
}

func TestEnvelopeBindsTheUser(t *testing.T) {
	ring, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: "k1", Keys: map[string][]byte{"k1": []byte(strings.Repeat("k", 32))}})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(ring, "user-a", rfcSecret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, EncodeSecret(rfcSecret)) || strings.Contains(sealed, "user-a") {
		t.Fatal("the sealed envelope carries plaintext")
	}
	got, err := Open(ring, "user-a", sealed)
	if err != nil || string(got) != string(rfcSecret) {
		t.Fatalf("Open(owner) = %v", err)
	}
	if _, err := Open(ring, "user-b", sealed); !errors.Is(err, ErrEnvelope) {
		t.Fatalf("Open(another user) = %v, want ErrEnvelope", err)
	}
	if _, err := Open(ring, "user-a", sealed+"x"); !errors.Is(err, ErrEnvelope) {
		t.Fatalf("Open(tampered) = %v, want ErrEnvelope", err)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatalf("NewRecoveryCodes = %d, %v", len(codes), err)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || strings.ToLower(c) != c {
			t.Fatalf("code %q is not xxxxx-xxxxx lowercase", c)
		}
		n, ok := NormalizeRecoveryCode(c)
		if !ok || len(n) != 10 || seen[n] {
			t.Fatalf("NormalizeRecoveryCode(%q) = %q, %v", c, n, ok)
		}
		seen[n] = true
		for _, typed := range []string{strings.ToUpper(c), strings.ReplaceAll(c, "-", " "), " " + n + " "} {
			if got, ok := NormalizeRecoveryCode(typed); !ok || got != n {
				t.Fatalf("NormalizeRecoveryCode(%q) = %q, %v", typed, got, ok)
			}
		}
	}
	for _, bad := range []string{"", "abcde-fghi", "abcde-fghiu", "abcde-fghijk"} {
		if _, ok := NormalizeRecoveryCode(bad); ok {
			t.Errorf("NormalizeRecoveryCode(%q) accepted", bad)
		}
	}
}
