package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
)

func TestPasswordResetRejectsReplacedRecoveryProof(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	u := h.mustRegister(t, "old-recovery@example.com", "password123456789")
	h.mustVerify(t, "old-recovery@example.com")
	if err := h.svc.ForgotPassword(ctx, "old-recovery@example.com"); err != nil {
		t.Fatal(err)
	}
	token := resetTokenFromMail(t, h.mailer.last())
	u, _ = h.users.Get(ctx, u.ID)
	old, _ := h.svc.identifiers.GetRecovery(ctx, "email", "old-recovery@example.com")
	if _, err := h.svc.identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID: u.ID, Kind: identifier.KindEmail, NormalizedValue: "new-recovery@example.com",
		LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true, MakePrimary: true,
		ReplacesIdentifierID: old.ID,
	}, u.AuthRevision, h.svc.now()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ResetPassword(ctx, token, "rejectedpassword1234"); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("obsolete recovery proof reset password: %v", err)
	}
	if hash, err := h.pw.Get(ctx, u.ID); err != nil || hash != "hash:password123456789" {
		t.Fatalf("rejected reset changed password: %q, %v", hash, err)
	}
	if err := h.svc.ForgotPassword(ctx, "new-recovery@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ResetPassword(ctx, resetTokenFromMail(t, h.mailer.last()), "newpassword12345678"); err != nil {
		t.Fatalf("fresh recovery proof rejected: %v", err)
	}
}
