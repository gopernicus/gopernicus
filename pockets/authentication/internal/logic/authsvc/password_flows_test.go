package authsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestPasswordFlowsDisabled_UseCasesRefuse proves the service-level half of
// the posture: every password entry point refuses with
// ErrPasswordFlowsDisabled (wrapping sdk.ErrNotFound) before touching any
// store, so a host reaching the Service directly gets the unmounted route's
// answer. The zero-value Deps keep the flows on.
func TestPasswordFlowsDisabled_UseCasesRefuse(t *testing.T) {
	s := NewService(Deps{PasswordFlowsDisabled: true})
	if s.PasswordFlowsEnabled() {
		t.Fatal("PasswordFlowsEnabled must be false when disabled")
	}
	ctx := context.Background()
	calls := map[string]func() error{
		"Register":       func() error { _, err := s.Register(ctx, "a@example.com", "pw", ""); return err },
		"Login":          func() error { _, _, err := s.Login(ctx, "a@example.com", "pw"); return err },
		"IssueToken":     func() error { _, err := s.IssueToken(ctx, "a@example.com", "pw"); return err },
		"ForgotPassword": func() error { return s.ForgotPassword(ctx, "a@example.com") },
		"ResetPassword":  func() error { return s.ResetPassword(ctx, "token", "pw") },
		"ChangePassword": func() error { _, err := s.ChangePassword(ctx, "u", "old", "new"); return err },
		"SetPassword":    func() error { _, err := s.SetPassword(ctx, "sess", "u", "pw"); return err },
		"RemovePassword": func() error { _, err := s.RemovePassword(ctx, "u", "code"); return err },
	}
	for name, call := range calls {
		err := call()
		if !errors.Is(err, ErrPasswordFlowsDisabled) || !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("%s: err=%v, want ErrPasswordFlowsDisabled (wrapping sdk.ErrNotFound)", name, err)
		}
	}
	if !NewService(Deps{}).PasswordFlowsEnabled() {
		t.Fatal("zero-value Deps must keep password flows enabled")
	}
}
