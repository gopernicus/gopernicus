package bcrypt_test

import (
	"errors"
	"strings"
	"testing"

	xbcrypt "golang.org/x/crypto/bcrypt"

	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	"github.com/gopernicus/gopernicus/sdk"
)

// testCost keeps hashing fast; production cost is higher.
const testCost = 4

// passwordHasher mirrors auth.PasswordHasher in the auth pocket module. The
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
	if got != bcrypt.DefaultCost || bcrypt.DefaultCost != 12 {
		t.Fatalf("default cost = %d, want 12", got)
	}
	if h.Cost() != 12 {
		t.Fatalf("Cost() = %d, want 12", h.Cost())
	}
	var zero bcrypt.Hasher
	if zero.Cost() != 12 {
		t.Fatalf("zero-value Cost() = %d, want 12", zero.Cost())
	}
}

func TestCostReportsConfiguredCost(t *testing.T) {
	if got := bcrypt.New(bcrypt.WithCost(10)).Cost(); got != 10 {
		t.Fatalf("WithCost(10).Cost() = %d, want 10", got)
	}
	if got := bcrypt.New(bcrypt.WithCost(xbcrypt.MaxCost + 1)).Cost(); got != 12 {
		t.Fatalf("out-of-range Cost() = %d, want the default 12", got)
	}
}

func TestCostTenHashVerifiesUnderDefaultHasher(t *testing.T) {
	hash, err := xbcrypt.GenerateFromPassword([]byte("existing password"), 10)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	h := bcrypt.New()
	if err := h.VerifyPassword(string(hash), "existing password"); err != nil {
		t.Fatalf("cost-10 hash should verify at its stored cost: %v", err)
	}
	if err := h.VerifyPassword(string(hash), "other password"); err == nil {
		t.Fatal("cost-10 hash should reject a wrong password")
	}
}

func TestSeventyTwoByteBoundary(t *testing.T) {
	h := bcrypt.New(bcrypt.WithCost(testCost))
	for _, password := range []string{strings.Repeat("a", 72), strings.Repeat("😀", 18)} {
		hash, err := h.HashPassword(password)
		if err != nil {
			t.Fatalf("72-byte password should be accepted: %v", err)
		}
		if err := h.VerifyPassword(hash, password); err != nil {
			t.Fatalf("72-byte password should verify: %v", err)
		}
		for _, tooLong := range []string{password + "a", password + "😀"} {
			if _, err := h.HashPassword(tooLong); !errors.Is(err, bcrypt.ErrPasswordTooLong) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("HashPassword error = %v, want ErrPasswordTooLong and sdk.ErrInvalidInput", err)
			}
			if err := h.VerifyPassword(hash, tooLong); !errors.Is(err, bcrypt.ErrPasswordTooLong) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("VerifyPassword error = %v, want ErrPasswordTooLong and sdk.ErrInvalidInput", err)
			}
		}
	}
}
