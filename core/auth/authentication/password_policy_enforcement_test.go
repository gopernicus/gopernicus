package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// The policy gate must run before any repository access — a zero
// Authenticator proves both the ordering and the enforcement.
func TestPasswordPolicyEnforcedInCoreMutators(t *testing.T) {
	ctx := context.Background()
	a := &Authenticator{}

	t.Run("ChangePassword rejects out-of-policy passwords", func(t *testing.T) {
		err := a.ChangePassword(ctx, "user-1", "sess-1", "current-password", "short", false)
		if !errors.Is(err, ErrWeakPassword) {
			t.Fatalf("want ErrWeakPassword, got %v", err)
		}
		if !errs.IsExpected(err) {
			t.Fatalf("weak-password error must be an expected domain error, got %v", err)
		}
	})

	t.Run("ResetPassword rejects out-of-policy passwords", func(t *testing.T) {
		err := a.ResetPassword(ctx, "some-token", "short")
		if !errors.Is(err, ErrWeakPassword) {
			t.Fatalf("want ErrWeakPassword, got %v", err)
		}
		if !errs.IsExpected(err) {
			t.Fatalf("weak-password error must be an expected domain error, got %v", err)
		}
	})
}
