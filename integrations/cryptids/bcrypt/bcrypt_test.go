package bcrypt_test

import (
	"strings"
	"testing"

	xbcrypt "golang.org/x/crypto/bcrypt"

	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
)

// testCost keeps hashing fast; production cost is higher.
const testCost = 4

// passwordHasher mirrors auth.PasswordHasher in the auth feature module. The
// port lives with its consumer, so this integration cannot import it; instead
// the interface literal is copied here and the assertion below proves Hasher
// satisfies the port's method set structurally.
type passwordHasher interface {
	HashPassword(password string) (string, error)
	VerifyPassword(hash, password string) error
}

// Compile-time structural-satisfaction assertion against the mirrored port.
var _ passwordHasher = (*bcrypt.Hasher)(nil)

func TestRoundtrip(t *testing.T) {
	h := bcrypt.New(bcrypt.WithCost(testCost))
	hash, err := h.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("empty hash")
	}
	if err := h.VerifyPassword(hash, "correct horse battery staple"); err != nil {
		t.Fatalf("VerifyPassword should match: %v", err)
	}
}

func TestWrongPasswordFails(t *testing.T) {
	h := bcrypt.New(bcrypt.WithCost(testCost))
	hash, err := h.HashPassword("right")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := h.VerifyPassword(hash, "wrong"); err == nil {
		t.Fatal("VerifyPassword should reject a wrong password")
	}
}

func TestSamePasswordDifferentHashes(t *testing.T) {
	h := bcrypt.New(bcrypt.WithCost(testCost))
	a, err := h.HashPassword("same")
	if err != nil {
		t.Fatalf("HashPassword a: %v", err)
	}
	b, err := h.HashPassword("same")
	if err != nil {
		t.Fatalf("HashPassword b: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
	if err := h.VerifyPassword(a, "same"); err != nil {
		t.Fatalf("verify a: %v", err)
	}
	if err := h.VerifyPassword(b, "same"); err != nil {
		t.Fatalf("verify b: %v", err)
	}
}

func TestCostOptionRespected(t *testing.T) {
	const cost = 6
	h := bcrypt.New(bcrypt.WithCost(cost))
	hash, err := h.HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	got, err := xbcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("reading cost: %v", err)
	}
	if got != cost {
		t.Fatalf("cost = %d, want %d", got, cost)
	}
}

func TestDefaultCost(t *testing.T) {
	h := bcrypt.New()
	hash, err := h.HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	got, err := xbcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("reading cost: %v", err)
	}
	if got != xbcrypt.DefaultCost {
		t.Fatalf("default cost = %d, want %d", got, xbcrypt.DefaultCost)
	}
}

func TestSeventyTwoByteBoundary(t *testing.T) {
	h := bcrypt.New(bcrypt.WithCost(testCost))

	if _, err := h.HashPassword(strings.Repeat("a", 72)); err != nil {
		t.Fatalf("72-byte password should be accepted: %v", err)
	}
	if _, err := h.HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Fatal("73-byte password should be rejected")
	}
}
