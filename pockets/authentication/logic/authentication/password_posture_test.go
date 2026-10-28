package authentication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// countingHasher records the hashes VerifyPassword is called with.
type countingHasher struct {
	fakeHasher
	verified []string
}

func (h *countingHasher) VerifyPassword(hash, password string) error {
	h.verified = append(h.verified, hash)
	return h.fakeHasher.VerifyPassword(hash, password)
}

// TestLoginMissPathPaysOneVerify is SRE-10: an unknown identifier and a user
// with no password each spend one VerifyPassword against the construction-time
// dummy hash, as a wrong password spends one against the stored hash.
func TestLoginMissPathPaysOneVerify(t *testing.T) {
	h := newHarness(t, nil)
	counting := &countingHasher{}
	h.svc.hasher = counting
	h.svc.dummyHash, _ = counting.HashPassword(dummyPassword)
	if h.svc.dummyHash == "" {
		t.Fatal("no dummy hash")
	}
	ctx := context.Background()
	if _, _, err := h.svc.Login(ctx, "nobody@example.com", "password123456789"); err == nil {
		t.Fatal("unknown identifier logged in")
	}
	if len(counting.verified) != 1 || counting.verified[0] != h.svc.dummyHash {
		t.Fatalf("unknown identifier: verify calls = %v, want one against the dummy hash", counting.verified)
	}
	h.mustRegister(t, "known@example.com", "password123456789")
	counting.verified = nil
	if _, _, err := h.svc.Login(ctx, "known@example.com", "wrong-password-123456"); err == nil {
		t.Fatal("wrong password logged in")
	}
	if len(counting.verified) != 1 || counting.verified[0] == h.svc.dummyHash {
		t.Fatalf("wrong password: verify calls = %v, want one against the stored hash", counting.verified)
	}
}

// missUsers fails Get, a user row that vanished between the two reads.
type missUsers struct{ user.UserRepository }

func (missUsers) Get(context.Context, string) (user.User, error) { return user.User{}, sdk.ErrNotFound }

// movedIdentifiers answers the second GetLogin with another identifier, a
// concurrent reassignment of the address.
type movedIdentifiers struct {
	identifier.IdentifierRepository
	calls int
}

func (m *movedIdentifiers) GetLogin(ctx context.Context, kind, value string) (identifier.Identifier, error) {
	m.calls++
	ident, err := m.IdentifierRepository.GetLogin(ctx, kind, value)
	if m.calls > 1 {
		ident.ID += "-moved"
	}
	return ident, err
}

// missPasswords fails Get, a user with no password row.
type missPasswords struct{ user.PasswordRepository }

func (missPasswords) Get(context.Context, string) (string, error) { return "", sdk.ErrNotFound }

// TestEveryLoginMissPathPaysOneVerify is L2: the users.Get miss, the re-read
// mismatch and the passwords.Get miss each spend one VerifyPassword against the
// dummy hash.
func TestEveryLoginMissPathPaysOneVerify(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(h *harness)
	}{
		{"users.Get miss", func(h *harness) { h.svc.users = missUsers{h.svc.users} }},
		{"re-read mismatch", func(h *harness) { h.svc.identifiers = &movedIdentifiers{IdentifierRepository: h.svc.identifiers} }},
		{"passwords.Get miss", func(h *harness) { h.svc.passwords = missPasswords{h.svc.passwords} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.mustRegister(t, "miss@example.com", "password123456789")
			counting := &countingHasher{}
			h.svc.hasher = counting
			h.svc.dummyHash, _ = counting.HashPassword(dummyPassword)
			tc.mutate(h)
			if _, _, err := h.svc.Login(context.Background(), "miss@example.com", "password123456789"); !errors.Is(err, sdk.ErrUnauthorized) {
				t.Fatalf("Login = %v, want the generic 401", err)
			}
			if len(counting.verified) != 1 || counting.verified[0] != h.svc.dummyHash {
				t.Fatalf("verify calls = %v, want one against the dummy hash", counting.verified)
			}
		})
	}
}

// TestNewServiceMintsDummyHash proves the dummy hash is minted at
// construction for a password host and not for one without password flows.
func TestNewServiceMintsDummyHash(t *testing.T) {
	if svc := newService(constructorConfig{Hasher: &fakeHasher{}}); svc.dummyHash != "hash:"+dummyPassword {
		t.Fatalf("dummyHash = %q", svc.dummyHash)
	}
	if svc := newService(constructorConfig{Hasher: &fakeHasher{}, PasswordFlowsDisabled: true}); svc.dummyHash != "" {
		t.Fatalf("password flows disabled minted %q", svc.dummyHash)
	}
	var buf bytes.Buffer
	if svc := newService(constructorConfig{Hasher: &fakeHasher{hashErr: errors.New("down")}, Logger: slog.New(slog.NewTextHandler(&buf, nil))}); svc.dummyHash != "" {
		t.Fatalf("a failed hash left %q", svc.dummyHash)
	}
	if strings.Count(buf.String(), "warning=password_timing_dummy_unavailable") != 1 || strings.Contains(buf.String(), "down") {
		t.Fatalf("a failed dummy hash did not WARN once, or leaked the error: %q", buf.String())
	}
}

// TestBreachCheckUnavailableWarnsOnBothBranches is SRE-9: a checker error logs
// one password_breach_check_unavailable WARN naming the outcome and carrying
// neither the password nor the checker's error text.
func TestBreachCheckUnavailableWarnsOnBothBranches(t *testing.T) {
	const cause = "upstream said: prefix ABCDE leaked"
	for _, tc := range []struct {
		failOpen bool
		outcome  string
	}{{false, "refused"}, {true, "allowed"}} {
		h := serviceWithChecker(t, &stubChecker{err: fmt.Errorf("%s: %w", cause, sdk.ErrUnavailable)}, tc.failOpen)
		var buf bytes.Buffer
		h.svc.logger = slog.New(slog.NewTextHandler(&buf, nil))
		err := h.svc.validatePassword(context.Background(), baselinePassword)
		if tc.failOpen != (err == nil) {
			t.Fatalf("failOpen=%v: err = %v", tc.failOpen, err)
		}
		logs := buf.String()
		if strings.Count(logs, "warning=password_breach_check_unavailable outcome="+tc.outcome+" error_kind=unavailable") != 1 || strings.Count(logs, "level=WARN") != 1 {
			t.Errorf("failOpen=%v: logs = %q", tc.failOpen, logs)
		}
		if strings.Contains(logs, baselinePassword) || strings.Contains(logs, "ABCDE") || (err != nil && strings.Contains(err.Error(), "ABCDE")) {
			t.Errorf("failOpen=%v: the password or the checker's text leaked: %q %v", tc.failOpen, logs, err)
		}
	}
}

func TestBreachCheckErrorKind(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("hibp: transport failure: %w", sdk.ErrUnavailable): "unavailable",
		context.DeadlineExceeded: "deadline_exceeded",
		context.Canceled:         "canceled",
		errors.New("opaque"):     "unknown",
	} {
		if got := breachCheckErrorKind(err); got != want {
			t.Errorf("breachCheckErrorKind(%v) = %q, want %q", err, got, want)
		}
	}
}
