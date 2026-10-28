// Tests for authmem run the shared auth storetest conformance suite against a
// fresh Store per call (the memory harness's clean-isolation contract). Per the
// auth-v1 phase-4 ratified edit R1, storetest.Run subsumes the bespoke
// uniqueness/honesty assertions a hand-written store would otherwise duplicate:
// it exercises email uniqueness (→ sdk.ErrAlreadyExists), the sentinel
// not-found contract, password upsert, and expired-at-read session/code/token
// behavior (→ sdk.ErrExpired). No behavior authmem implements is left
// uncovered by the suite, so there are no additional bespoke cases below.
package authmem

import (
	"os"
	"testing"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestMain swaps the store's own id generator for one whose mints sort in mint
// order. The store stamps rows it writes itself (the purge evidence rows) with
// time.Now, and consecutive calls can share a microsecond; the rail lists by
// (created_at, id), so random NanoID tiebreaks made the suite's oldest-first
// reading of those rows flaky.
func TestMain(m *testing.M) {
	ids = sdk.NewIDGenerator(sdk.UUIDv7())
	os.Exit(m.Run())
}

// TestConformance runs the auth storetest suite against authmem.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) auth.Repositories {
		return New().Repositories()
	})
}

// TestSessionActivityWired fails, rather than skipping, if authmem's sessions
// lose the idle-timeout capability the conformance suite's SessionActivity
// group would otherwise skip over.
func TestSessionActivityWired(t *testing.T) {
	if _, ok := New().Repositories().Sessions.(session.ActivityRepository); !ok {
		t.Fatal("authmem Sessions does not implement session.ActivityRepository")
	}
}

// TestPasskeyConformance runs the passkey suite, separate from Run, against
// authmem.
func TestPasskeyConformance(t *testing.T) {
	storetest.RunPasskeys(t, func(t *testing.T) auth.Repositories {
		return New().Repositories()
	})
}

// TestTOTPConformance runs the TOTP suite, separate from Run, against authmem.
func TestTOTPConformance(t *testing.T) {
	storetest.RunTOTP(t, func(t *testing.T) auth.Repositories {
		return New().Repositories()
	})
}

// TestDelegatedRotationFence runs the delegated-session rotation fence, separate
// from Run, against authmem.
func TestDelegatedRotationFence(t *testing.T) {
	storetest.RunDelegatedRotationFence(t, func(t *testing.T) auth.Repositories {
		return New().Repositories()
	})
}

// TestIDs runs the record-id persistence suite, separate from Run, against
// authmem.
func TestIDs(t *testing.T) {
	storetest.RunIDs(t, func(t *testing.T) auth.Repositories {
		return New().Repositories()
	})
}

// TestPasswordHashUpgrade runs the compare-and-swap re-hash suite, separate
// from Run, against authmem.
func TestPasswordHashUpgrade(t *testing.T) {
	storetest.RunPasswordHashUpgrade(t, func(t *testing.T) auth.Repositories {
		return New().Repositories()
	})
}
