package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestSensitiveRemovalRejectsProofFromEarlierCredentials(t *testing.T) {
	for _, operation := range []string{"password", "oauth"} {
		for _, mutation := range []string{"password", "recovery"} {
			t.Run(operation+"/"+mutation, func(t *testing.T) {
				h := newHarness(t, nil)
				ctx := context.Background()
				uid := "proof-owner"
				email := h.seedUnlinkUser(t, uid, "old-recovery@example.com", true, true, "google")
				if operation == "password" {
					if _, err := h.svc.StartRemovePassword(ctx, uid); err != nil {
						t.Fatal(err)
					}
				} else if _, err := h.svc.StartUnlinkOAuth(ctx, uid, "google"); err != nil {
					t.Fatal(err)
				}
				code := h.mailer.codeFor(t, email)
				if mutation == "password" {
					if err := h.pw.Set(ctx, uid, "hash:changedpassword123456"); err != nil {
						t.Fatal(err)
					}
				} else {
					u, err := h.users.Get(ctx, uid)
					if err != nil {
						t.Fatal(err)
					}
					_, err = h.idents.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
						UserID: uid, Kind: identifier.KindEmail, NormalizedValue: "new-recovery@example.com",
						LoginEnabled: true, RecoveryEnabled: true, MakePrimary: true,
					}, u.AuthRevision, time.Now())
					if err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if operation == "password" {
					_, err = h.svc.RemovePassword(ctx, uid, code)
				} else {
					err = h.svc.UnlinkOAuth(ctx, uid, "google", code)
				}
				if !errors.Is(err, ErrChallengeInvalid) {
					t.Fatalf("stale delivered proof: %v", err)
				}
				if _, err := h.pw.Get(ctx, uid); err != nil {
					t.Fatalf("password removed: %v", err)
				}
				if h.linkCount(t, uid) != 1 {
					t.Fatal("provider removed by stale proof")
				}
			})
		}
	}
}

func TestSensitiveRemovalDoesNotRebaseConsumedProof(t *testing.T) {
	for _, operation := range []string{"password", "oauth"} {
		t.Run(operation, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			uid := "consumed-owner"
			email := h.seedUnlinkUser(t, uid, "consumed@example.com", true, true, "google")
			if operation == "password" {
				if _, err := h.svc.StartRemovePassword(ctx, uid); err != nil {
					t.Fatal(err)
				}
			} else if _, err := h.svc.StartUnlinkOAuth(ctx, uid, "google"); err != nil {
				t.Fatal(err)
			}
			code := h.mailer.codeFor(t, email)
			h.creds.beforeApply = func() {
				if err := h.pw.Set(ctx, uid, "hash:changedafterproof123456"); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if operation == "password" {
				_, err = h.svc.RemovePassword(ctx, uid, code)
			} else {
				err = h.svc.UnlinkOAuth(ctx, uid, "google", code)
			}
			if !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("proof rebased after credential change: %v", err)
			}
			if got, err := h.pw.Get(ctx, uid); err != nil || got != "hash:changedafterproof123456" {
				t.Fatalf("current password changed: %q %v", got, err)
			}
			if h.linkCount(t, uid) != 1 {
				t.Fatal("provider removed after credential change")
			}
		})
	}
}

func TestIdentifierMutationDoesNotRebaseRecentProof(t *testing.T) {
	for _, operation := range []string{"remove", "uses"} {
		for _, proof := range []string{"primary", "grant"} {
			t.Run(operation+"/"+proof, func(t *testing.T) {
				h := newHarness(t, nil)
				ctx := context.Background()
				uid, email, sid := h.mustVerifiedLogin(t, "identifier-proof@example.com", "password123456789")
				target, err := h.idents.GetLogin(ctx, "email", email)
				if err != nil {
					t.Fatal(err)
				}
				if proof == "grant" {
					h.backdateLogin(sid, time.Hour)
					purpose := authgrant.PurposeRemoveIdentifier
					if operation == "uses" {
						purpose = authgrant.PurposeChangeIdentifierUses
					}
					u, err := h.users.Get(ctx, uid)
					if err != nil {
						t.Fatal(err)
					}
					now := time.Now()
					_, err = h.grants.Create(ctx, authgrant.Grant{UserID: uid, SessionID: sid, Purpose: purpose,
						ContextDigest: grantContextDigest(target.ID), AuthenticatedAt: now, Assurance: session.AssuranceAAL1,
						CreatedAt: now, ExpiresAt: now.Add(time.Minute)}, u.AuthRevision, now)
					if err != nil {
						t.Fatal(err)
					}
				}
				h.creds.beforeApply = func() {
					if err := h.pw.Set(ctx, uid, "hash:changedafterproof123456"); err != nil {
						t.Fatal(err)
					}
				}
				if operation == "remove" {
					err = h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{UserID: uid, SessionID: sid, IdentifierID: target.ID})
				} else {
					err = h.svc.SetIdentifierUses(ctx, IdentifierUsesInput{UserID: uid, SessionID: sid, IdentifierID: target.ID,
						Uses: identifier.Uses{Login: true, Recovery: false}})
				}
				if !errors.Is(err, sdk.ErrConflict) {
					t.Fatalf("mutation rebased after proof: %v", err)
				}
				current, err := h.idents.Get(ctx, target.ID)
				if err != nil || !current.Active() || !current.RecoveryEnabled {
					t.Fatalf("identifier changed: %+v %v", current, err)
				}
			})
		}
	}
}

func TestStepUpCodeRejectsChangedIdentifierRevision(t *testing.T) {
	for _, mutation := range []string{"replace_destination", "add_other_identifier"} {
		t.Run(mutation, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			uid, email, sid := h.mustVerifiedLogin(t, "step-revision@example.com", "password123456789")
			in := StepUpStart{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeSetPassword}
			if _, err := h.svc.BeginStepUp(ctx, in); err != nil {
				t.Fatal(err)
			}
			code := h.mailer.codeFor(t, email)
			u, err := h.users.Get(ctx, uid)
			if err != nil {
				t.Fatal(err)
			}
			change := identifier.ApplyVerifiedChangeInput{UserID: uid, Kind: identifier.KindEmail,
				NormalizedValue: "replacement@example.com", LoginEnabled: true, RecoveryEnabled: true, MakePrimary: true}
			if mutation == "add_other_identifier" {
				change.Kind, change.NormalizedValue = identifier.KindPhone, "+14155550123"
			}
			if _, err := h.idents.ApplyVerifiedChange(ctx, change, u.AuthRevision, time.Now()); err != nil {
				t.Fatal(err)
			}
			// Identifier replacement may retain sessions. Its revision still invalidates
			// the outstanding code, even if the chosen email row itself was unchanged.
			if _, err := h.sess.Get(ctx, sid); err != nil {
				t.Fatalf("fixture did not retain session: %v", err)
			}
			_, err = h.svc.CompleteStepUpWithIdentifierCode(ctx, StepUpCompletion{UserID: uid, SessionID: sid, Purpose: in.Purpose}, code)
			if !errors.Is(err, ErrChallengeInvalid) {
				t.Fatalf("old code earned a grant: %v", err)
			}
			if h.grants.unconsumed() != 0 {
				t.Fatal("stale proof persisted grant")
			}
		})
	}
}

func TestRecentAuthenticationRejectsInactiveOwnerWithRetainedSession(t *testing.T) {
	h := newHarness(t, nil)
	uid, _, sid := h.mustVerifiedLogin(t, "inactive-proof@example.com", "password123456789")
	// Model a host retaining a physical session row after disabling its owner.
	h.users.mu.Lock()
	u := h.users.byID[uid]
	u.Status = user.StatusDeactivated
	h.users.byID[uid] = u
	h.users.mu.Unlock()
	_, err := h.svc.RequireRecentAuthentication(context.Background(), sid, uid, authgrant.PurposeSetPassword, "", RecentAuthPolicy{})
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("inactive owner's recent login accepted: %v", err)
	}
}

type mutateAfterPendingConsumption struct {
	contactchange.Repository
	mutate func()
}

func (r *mutateAfterPendingConsumption) Consume(ctx context.Context, userID string, kind identifier.Kind, expectedID string) (contactchange.PendingChange, error) {
	pending, err := r.Repository.Consume(ctx, userID, kind, expectedID)
	if err == nil {
		r.mutate()
	}
	return pending, err
}

func TestIdentifierConfirmDoesNotRebaseConsumedProof(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, original, sid := h.mustVerifiedLogin(t, "confirm-proof@example.com", "password123456789")
	target := "new-confirm@example.com"
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{UserID: uid, SessionID: sid,
		Kind: identifier.KindEmail, Value: target, Uses: identifier.Uses{Login: true, Recovery: true}, MakePrimary: true}); err != nil {
		t.Fatal(err)
	}
	code := h.mailer.proofCodeFor(t, target)
	h.svc.contactChanges = &mutateAfterPendingConsumption{Repository: h.svc.contactChanges, mutate: func() {
		if err := h.pw.Set(ctx, uid, "hash:changedafterproof123456"); err != nil {
			t.Fatal(err)
		}
	}}
	err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{UserID: uid, SessionID: sid, Kind: identifier.KindEmail, Code: code})
	if !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("consumed proof rebased across password change: %v", err)
	}
	if _, err := h.idents.GetLogin(ctx, "email", target); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("new recovery address claimed after password change: %v", err)
	}
	if _, err := h.idents.GetLogin(ctx, "email", original); err != nil {
		t.Fatalf("original login address retired: %v", err)
	}
}

func TestIdentifierConfirmRejectsEarlierIssuanceRevision(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "confirm-issuance@example.com", "password123456789")
	target := "issued-before-change@example.com"
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{UserID: uid, SessionID: sid,
		Kind: identifier.KindEmail, Value: target, Uses: identifier.Uses{Login: true, Recovery: true}}); err != nil {
		t.Fatal(err)
	}
	code := h.mailer.proofCodeFor(t, target)
	u, err := h.users.Get(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	// This credential change advances the revision while retaining the session.
	if _, err := h.idents.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{UserID: uid,
		Kind: identifier.KindPhone, NormalizedValue: "+14155550123", LoginEnabled: true}, u.AuthRevision, time.Now()); err != nil {
		t.Fatal(err)
	}
	err = h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{UserID: uid, SessionID: sid, Kind: identifier.KindEmail, Code: code})
	if !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("old issuance accepted at current revision: %v", err)
	}
	if _, err := h.idents.GetLogin(ctx, "email", target); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("stale issuance claimed target: %v", err)
	}
}

func TestIdentifierConfirmRequiresLiveIssuingSession(t *testing.T) {
	for _, invalid := range []string{"empty", "missing", "expired", "other_owner", "other_session"} {
		t.Run(invalid, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			uid, _, sid := h.mustVerifiedLogin(t, "confirm-session@example.com", "password123456789")
			target := "session-bound@example.com"
			if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{UserID: uid, SessionID: sid,
				Kind: identifier.KindEmail, Value: target, Uses: identifier.Uses{Notification: true}}); err != nil {
				t.Fatal(err)
			}
			code := h.mailer.proofCodeFor(t, target)
			inputSession := "invalid-session"
			wantErr := ErrStepUpRequired
			switch invalid {
			case "empty":
				inputSession = ""
			case "expired", "other_owner", "other_session":
				sess := session.Session{ID: inputSession, UserID: uid, RefreshTokenHash: inputSession, ExpiresAt: time.Now().Add(time.Hour)}
				if invalid == "expired" {
					sess.ExpiresAt = time.Now().Add(-time.Hour)
				}
				if invalid == "other_owner" {
					sess.UserID = "another-owner"
				}
				if invalid == "other_session" {
					wantErr = ErrChallengeInvalid
				}
				if _, err := h.sess.Create(ctx, sess); err != nil {
					t.Fatal(err)
				}
			}
			err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{UserID: uid, SessionID: inputSession, Kind: identifier.KindEmail, Code: code})
			if !errors.Is(err, wantErr) {
				t.Fatalf("invalid confirming session: %v, want %v", err, wantErr)
			}
			if invalid != "other_session" {
				// Rejection before proof consumption leaves the legitimate confirmation usable.
				if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{UserID: uid, SessionID: sid, Kind: identifier.KindEmail, Code: code}); err != nil {
					t.Fatalf("invalid session consumed valid proof: %v", err)
				}
			}
		})
	}
}
