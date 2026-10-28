package authentication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

// costedHasher encodes its work factor in the hash ("c10:<password>") and
// reports a lower one as needing a rehash, as bcrypt's NeedsRehash does.
type costedHasher struct {
	cost   int
	hashes int
}

func (h *costedHasher) HashPassword(password string) (string, error) {
	h.hashes++
	return fmt.Sprintf("c%d:%s", h.cost, password), nil
}

func (h *costedHasher) VerifyPassword(hash, password string) error {
	if _, pw, ok := strings.Cut(hash, ":"); ok && pw == password {
		return nil
	}
	return errors.New("mismatch")
}

func (h *costedHasher) NeedsRehash(hash string) bool {
	var cost int
	if _, err := fmt.Sscanf(hash, "c%d:", &cost); err != nil {
		return false
	}
	return cost < h.cost
}

// plainCostedHasher is costedHasher without NeedsRehash.
type plainCostedHasher struct{ inner *costedHasher }

func (h plainCostedHasher) HashPassword(p string) (string, error) { return h.inner.HashPassword(p) }
func (h plainCostedHasher) VerifyPassword(hash, p string) error {
	return h.inner.VerifyPassword(hash, p)
}

// upgradingPasswords adds the compare-and-swap UpgradeHash to the fake store.
type upgradingPasswords struct {
	*fakePasswords
	upgrades int
	err      error
}

func (u *upgradingPasswords) UpgradeHash(_ context.Context, userID, oldHash, newHash string) (bool, error) {
	u.upgrades++
	if u.err != nil {
		return false, u.err
	}
	u.fakePasswords.mu.Lock()
	defer u.fakePasswords.mu.Unlock()
	if u.fakePasswords.m[userID] != oldHash {
		return false, nil
	}
	u.fakePasswords.m[userID] = newHash
	return true, nil
}

var _ user.PasswordHashUpgrader = (*upgradingPasswords)(nil)

const rehashPassword = "password123456789"

// rehashHarness registers a user whose stored hash is cost 10, then raises the
// hasher to cost 12 over a store that offers UpgradeHash.
func rehashHarness(t *testing.T, storeErr error) (*harness, *costedHasher, *upgradingPasswords, *bytes.Buffer, string) {
	t.Helper()
	h := newHarness(t, nil)
	hasher := &costedHasher{cost: 10}
	h.svc.hasher = hasher
	userID, _, _ := h.mustVerifiedLogin(t, "rehash@example.com", rehashPassword)
	hasher.cost = 12
	store := &upgradingPasswords{fakePasswords: h.pw, err: storeErr}
	h.svc.passwords = store
	var buf bytes.Buffer
	h.svc.logger = slog.New(slog.NewTextHandler(&buf, nil))
	return h, hasher, store, &buf, userID
}

// TestLoginRehashesAnOldHashOnce proves a cost-10 hash is upgraded on the
// first successful login without touching the session fence, and the next
// login no longer rehashes.
func TestLoginRehashesAnOldHashOnce(t *testing.T) {
	h, hasher, store, logs, userID := rehashHarness(t, nil)
	ctx := context.Background()
	before, _ := h.users.Get(ctx, userID)
	hasher.hashes = 0
	if _, _, err := h.svc.Login(ctx, "rehash@example.com", rehashPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got, _ := h.pw.Get(ctx, userID); got != "c12:"+rehashPassword || store.upgrades != 1 || hasher.hashes != 1 {
		t.Fatalf("after the first login: hash %q, upgrades %d, hashes %d", got, store.upgrades, hasher.hashes)
	}
	if after, _ := h.users.Get(ctx, userID); after.AuthRevision != before.AuthRevision {
		t.Fatalf("the rehash bumped auth_revision %d -> %d", before.AuthRevision, after.AuthRevision)
	}
	if _, _, err := h.svc.Login(ctx, "rehash@example.com", rehashPassword); err != nil {
		t.Fatalf("second Login: %v", err)
	}
	if store.upgrades != 1 || hasher.hashes != 1 {
		t.Fatalf("the second login rehashed: upgrades %d, hashes %d", store.upgrades, hasher.hashes)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a successful rehash logged a WARN:\n%s", logs.String())
	}
}

// TestLoginRehashNeverOnFailure proves a wrong password and an unknown
// identifier never rehash.
func TestLoginRehashNeverOnFailure(t *testing.T) {
	h, _, store, _, _ := rehashHarness(t, nil)
	ctx := context.Background()
	h.svc.Login(ctx, "rehash@example.com", "wrong-password-123456")
	h.svc.Login(ctx, "nobody@example.com", rehashPassword)
	if store.upgrades != 0 {
		t.Fatalf("a failed login rehashed: %d", store.upgrades)
	}
}

// TestLoginRehashFailureNeverFailsLogin proves a failing store logs one
// password_rehash_failed WARN carrying neither the password nor a hash, and
// the login still succeeds.
func TestLoginRehashFailureNeverFailsLogin(t *testing.T) {
	h, _, store, logs, userID := rehashHarness(t, errors.New("store down"))
	ctx := context.Background()
	if _, _, err := h.svc.Login(ctx, "rehash@example.com", rehashPassword); err != nil {
		t.Fatalf("Login with a failing rehash store: %v", err)
	}
	if store.upgrades != 1 || strings.Count(logs.String(), "warning=password_rehash_failed") != 1 {
		t.Fatalf("upgrades %d, logs:\n%s", store.upgrades, logs.String())
	}
	if strings.Contains(logs.String(), rehashPassword) || strings.Contains(logs.String(), "c10:") || strings.Contains(logs.String(), "c12:") {
		t.Fatalf("the WARN carries the password or a hash:\n%s", logs.String())
	}
	if got, _ := h.pw.Get(ctx, userID); got != "c10:"+rehashPassword {
		t.Fatalf("hash changed despite the failure: %q", got)
	}
}

// TestRehashNeedsBothOptionalInterfaces proves a hasher without NeedsRehash,
// or a store without UpgradeHash, means no rehash and no WARN.
func TestRehashNeedsBothOptionalInterfaces(t *testing.T) {
	ctx := context.Background()
	h, hasher, store, logs, userID := rehashHarness(t, nil)
	h.svc.hasher = plainCostedHasher{hasher}
	if _, _, err := h.svc.Login(ctx, "rehash@example.com", rehashPassword); err != nil {
		t.Fatal(err)
	}
	if store.upgrades != 0 {
		t.Fatalf("a hasher without NeedsRehash rehashed: %d", store.upgrades)
	}
	h.svc.hasher = hasher
	h.svc.passwords = h.pw
	if _, _, err := h.svc.Login(ctx, "rehash@example.com", rehashPassword); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.pw.Get(ctx, userID); got != "c10:"+rehashPassword || logs.Len() != 0 {
		t.Fatalf("a store without UpgradeHash: hash %q, logs %q", got, logs.String())
	}
}

// TestStepUpRehashesAnOldHash proves the password step-up upgrades the hash
// as login does.
func TestStepUpRehashesAnOldHash(t *testing.T) {
	h := newHarness(t, nil)
	hasher := &costedHasher{cost: 10}
	h.svc.hasher = hasher
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "stepup-rehash@example.com", rehashPassword)
	hasher.cost = 12
	store := &upgradingPasswords{fakePasswords: h.pw}
	h.svc.passwords = store
	h.backdateLogin(sessionID, time.Hour)
	if _, err := h.svc.CompleteStepUpWithPassword(ctx, StepUpCompletion{SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeRemovePassword}, rehashPassword); err != nil {
		t.Fatalf("CompleteStepUpWithPassword: %v", err)
	}
	if got, _ := h.pw.Get(ctx, userID); got != "c12:"+rehashPassword || store.upgrades != 1 {
		t.Fatalf("after step-up: hash %q, upgrades %d", got, store.upgrades)
	}
}
