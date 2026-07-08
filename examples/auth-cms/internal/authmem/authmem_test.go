// Tests for authmem run the shared auth storetest conformance suite against a
// fresh Store per call (the memory harness's clean-isolation contract). Per the
// auth-v1 phase-4 ratified edit R1, storetest.Run subsumes the bespoke
// uniqueness/honesty assertions a hand-written store would otherwise duplicate:
// it exercises email uniqueness (→ errs.ErrAlreadyExists), the sentinel
// not-found contract, password upsert, and expired-at-read session/code/token
// behavior (→ errs.ErrExpired). No behavior authmem implements is left
// uncovered by the suite, so there are no additional bespoke cases below.
package authmem

import (
	"testing"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/storetest"
)

// TestConformance runs the auth storetest suite against authmem.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) auth.Repositories {
		return New().Repositories()
	})
}
