package oauth

import (
	"strings"
	"testing"
)

func TestGenerateCodeChallengeRFC7636Vector(t *testing.T) {
	// RFC 7636 Appendix B worked example: SHA256 of the verifier ASCII string,
	// base64url-encoded without padding.
	const (
		verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)

	if got := GenerateCodeChallenge(verifier); got != challenge {
		t.Fatalf("GenerateCodeChallenge(%q) = %q, want %q", verifier, got, challenge)
	}
}

func TestGenerateCodeChallengeIsDeterministic(t *testing.T) {
	const verifier = "a-stable-code-verifier-value"

	first := GenerateCodeChallenge(verifier)
	second := GenerateCodeChallenge(verifier)

	if first != second {
		t.Fatalf("challenge is not deterministic: %q != %q", first, second)
	}
}

func TestGenerateCodeChallengeIsURLSafeUnpadded(t *testing.T) {
	// A SHA256 digest is 32 bytes, which base64url encodes to 43 characters
	// with no padding. The challenge must be URL-safe: no '+', '/', or '='.
	got := GenerateCodeChallenge("another-verifier")

	if len(got) != 43 {
		t.Fatalf("challenge length = %d, want 43", len(got))
	}
	if strings.ContainsAny(got, "+/=") {
		t.Fatalf("challenge %q contains non-URL-safe or padding characters", got)
	}
}

func TestGenerateCodeChallengeDistinguishesVerifiers(t *testing.T) {
	if GenerateCodeChallenge("verifier-one") == GenerateCodeChallenge("verifier-two") {
		t.Fatal("distinct verifiers produced identical challenges")
	}
}
