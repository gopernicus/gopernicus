package authentication

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

const denialPassword = "password123456789"

// assertNoSecretInDetails fails when a Details value of any recorded row equals
// or contains one of the attempted secrets: an address, code, token or password.
func assertNoSecretInDetails(t *testing.T, events []securityevent.SecurityEvent, secrets ...string) {
	t.Helper()
	for _, evt := range events {
		for key, value := range evt.Details {
			text := fmt.Sprint(value)
			for _, secret := range secrets {
				if secret != "" && strings.Contains(text, secret) {
					t.Errorf("%s/%s Details[%q] carries an attempted secret", evt.EventType, evt.EventStatus, key)
				}
			}
		}
	}
}

// requireDenial asserts the last recorded row of eventType carries status and
// reason, and that its UserID is userID — "" asserts no account is attributed.
func requireDenial(t *testing.T, spy *spySecurityEvents, eventType, status, reason, userID string) securityevent.SecurityEvent {
	t.Helper()
	var found *securityevent.SecurityEvent
	var seen []string
	for _, evt := range spy.recorded() {
		seen = append(seen, evt.EventType+"/"+evt.EventStatus)
		if evt.EventType == eventType {
			evt := evt
			found = &evt
		}
	}
	if found == nil {
		t.Fatalf("no %s row recorded; got %v", eventType, seen)
	}
	if found.EventStatus != status || found.Details["reason"] != reason {
		t.Fatalf("%s row = %s reason=%v, want %s reason=%s", eventType, found.EventStatus, found.Details["reason"], status, reason)
	}
	if found.UserID != userID {
		t.Fatalf("%s row UserID = %q, want %q", eventType, found.UserID, userID)
	}
	return *found
}

func setUserStatus(users *fakeUsers, userID string, status user.Status) {
	users.mu.Lock()
	defer users.mu.Unlock()
	u := users.byID[userID]
	u.Status = status
	users.byID[userID] = u
}

// wrongCodeFor returns a six-digit code that is not code.
func wrongCodeFor(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

// TestCredentialDenialsRecordAlike pins IssueToken's denial rows and their
// parity with Login: both record the same status, reason and attribution
// through recordCredentialOutcome, and token_issued never carries the address.
func TestCredentialDenialsRecordAlike(t *testing.T) {
	const address = "parity@example.com"
	cases := []struct {
		name            string
		requireVerified bool
		limiter         ratelimiter.Limiter
		attempt         string
		password        string
		deactivate      bool
		status, reason  string
		attributed      bool
	}{
		{name: "wrong password", attempt: address, password: "wrong-password-0451",
			status: securityevent.StatusFailure, reason: securityevent.ReasonInvalidCredentials, attributed: true},
		{name: "unknown address", attempt: "nobody-0451@example.com", password: denialPassword,
			status: securityevent.StatusFailure, reason: securityevent.ReasonInvalidCredentials},
		{name: "malformed address", attempt: "not an address 0451", password: denialPassword,
			status: securityevent.StatusFailure, reason: securityevent.ReasonInvalidCredentials},
		{name: "rate limited", limiter: denyLimiter{}, attempt: address, password: denialPassword,
			status: securityevent.StatusBlocked, reason: securityevent.ReasonRateLimited},
		{name: "email unverified", requireVerified: true, attempt: address, password: denialPassword,
			status: securityevent.StatusFailure, reason: securityevent.ReasonEmailUnverified, attributed: true},
		{name: "inactive", deactivate: true, attempt: address, password: denialPassword,
			status: securityevent.StatusBlocked, reason: securityevent.ReasonInactive, attributed: true},
	}
	for _, tc := range cases {
		for _, op := range []string{securityevent.TypeTokenIssued, securityevent.TypeLogin} {
			t.Run(tc.name+"/"+op, func(t *testing.T) {
				ctx := context.Background()
				h := newTokenHarness(t, newFakeSigner(), tc.requireVerified, tc.limiter)
				u := h.mustRegister(t, address, denialPassword)
				if tc.deactivate {
					setUserStatus(h.users, u.ID, user.StatusDeactivated)
				}
				var err error
				if op == securityevent.TypeTokenIssued {
					_, err = h.svc.IssueToken(ctx, tc.attempt, tc.password)
				} else {
					_, _, err = h.svc.Login(ctx, tc.attempt, tc.password)
				}
				if err == nil {
					t.Fatal("the attempt succeeded")
				}
				want := ""
				if tc.attributed {
					want = u.ID
				}
				requireDenial(t, h.events, op, tc.status, tc.reason, want)
				if op == securityevent.TypeTokenIssued {
					assertNoSecretInDetails(t, h.events.recorded(), tc.attempt, tc.password, denialPassword)
				}
			})
		}
	}
}

func TestChangePasswordRecordsDenials(t *testing.T) {
	ctx := context.Background()
	t.Run("wrong current password", func(t *testing.T) {
		h := newHarness(t, nil)
		u := h.mustRegister(t, "change-denied@example.com", denialPassword)
		if _, err := h.svc.ChangePassword(ctx, u.ID, "wrong-current-0451", "newpassword456789"); !errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("ChangePassword = %v, want unauthorized", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordChange, securityevent.StatusFailure, securityevent.ReasonInvalidCredentials, u.ID)
		assertNoSecretInDetails(t, h.events.recorded(), "wrong-current-0451", "newpassword456789")
	})
	t.Run("rejected new password", func(t *testing.T) {
		h := newHarness(t, nil)
		u := h.mustRegister(t, "change-policy@example.com", denialPassword)
		if _, err := h.svc.ChangePassword(ctx, u.ID, denialPassword, "short"); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ChangePassword = %v, want invalid input", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordChange, securityevent.StatusFailure, securityevent.ReasonPolicy, u.ID)
		assertNoSecretInDetails(t, h.events.recorded(), denialPassword, "short")
	})
}

func TestSetPasswordRecordsDenials(t *testing.T) {
	ctx := context.Background()
	t.Run("rejected password", func(t *testing.T) {
		h := newHarness(t, nil)
		const userID, sessionID = "u-set-policy", "sess-set-policy"
		h.seedPasswordlessUser(userID, "set-policy@example.com")
		h.grantFor(t, sessionID, userID, authgrant.PurposeSetPassword)
		if _, err := h.svc.SetPassword(ctx, sessionID, userID, "short"); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("SetPassword = %v, want invalid input", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordSet, securityevent.StatusFailure, securityevent.ReasonPolicy, userID)
		assertNoSecretInDetails(t, h.events.recorded(), "short")
	})
	t.Run("grant refused", func(t *testing.T) {
		h := newHarness(t, nil)
		const userID, sessionID = "u-set-grant", "sess-set-grant"
		h.seedPasswordlessUser(userID, "set-grant@example.com")
		if _, err := h.svc.SetPassword(ctx, sessionID, userID, denialPassword); !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("SetPassword = %v, want ErrStepUpRequired", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordSet, securityevent.StatusFailure, securityevent.ReasonInvalidToken, userID)
		assertNoSecretInDetails(t, h.events.recorded(), denialPassword)
	})
}

func TestResetPasswordRecordsDenialsWithoutAUser(t *testing.T) {
	ctx := context.Background()
	t.Run("invalid token", func(t *testing.T) {
		h := newHarness(t, nil)
		h.mustRegister(t, "reset-denied@example.com", denialPassword)
		const token = "unknown-reset-token-0451"
		if err := h.svc.ResetPassword(ctx, token, "newpassword456789"); !errors.Is(err, ErrPasswordResetInvalid) {
			t.Fatalf("ResetPassword = %v, want ErrPasswordResetInvalid", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordReset, securityevent.StatusFailure, securityevent.ReasonInvalidToken, "")
		assertNoSecretInDetails(t, h.events.recorded(), token, "newpassword456789")
	})
	t.Run("rejected password", func(t *testing.T) {
		h := newHarness(t, nil)
		const token = "another-reset-token-0451"
		if err := h.svc.ResetPassword(ctx, token, "short"); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ResetPassword = %v, want invalid input", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordReset, securityevent.StatusFailure, securityevent.ReasonPolicy, "")
		assertNoSecretInDetails(t, h.events.recorded(), token, "short")
	})
}

// denialConflictIdentifiers loses every verified-change revision race.
type denialConflictIdentifiers struct {
	identifier.IdentifierRepository
}

func (denialConflictIdentifiers) ApplyVerifiedChange(context.Context, identifier.ApplyVerifiedChangeInput, int64, time.Time) (identifier.Identifier, error) {
	return identifier.Identifier{}, sdk.ErrConflict
}

func TestVerifyRecordsDenials(t *testing.T) {
	ctx := context.Background()
	const address = "verify-denied@example.com"
	t.Run("wrong code", func(t *testing.T) {
		h := newHarness(t, nil)
		u := h.mustRegister(t, address, denialPassword)
		wrong := wrongCodeFor(h.mailer.codeFor(t, address))
		if err := h.svc.Verify(ctx, address, wrong); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("Verify = %v, want ErrChallengeInvalid", err)
		}
		requireDenial(t, h.events, securityevent.TypeEmailVerified, securityevent.StatusFailure, securityevent.ReasonInvalidCode, u.ID)
		assertNoSecretInDetails(t, h.events.recorded(), address, wrong)
	})
	t.Run("unknown address", func(t *testing.T) {
		h := newHarness(t, nil)
		if err := h.svc.Verify(ctx, "nobody-0451@example.com", "123456"); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("Verify = %v, want ErrChallengeInvalid", err)
		}
		requireDenial(t, h.events, securityevent.TypeEmailVerified, securityevent.StatusFailure, securityevent.ReasonInvalidCode, "")
		assertNoSecretInDetails(t, h.events.recorded(), "nobody-0451@example.com", "123456")
	})
	t.Run("inactive", func(t *testing.T) {
		h := newHarness(t, nil)
		u := h.mustRegister(t, address, denialPassword)
		code := h.mailer.codeFor(t, address)
		setUserStatus(h.users, u.ID, user.StatusDeactivated)
		if err := h.svc.Verify(ctx, address, code); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("Verify = %v, want ErrChallengeInvalid", err)
		}
		requireDenial(t, h.events, securityevent.TypeEmailVerified, securityevent.StatusBlocked, securityevent.ReasonInactive, u.ID)
		assertNoSecretInDetails(t, h.events.recorded(), address, code)
	})
	t.Run("revision conflict", func(t *testing.T) {
		h := newHarness(t, nil)
		u := h.mustRegister(t, address, denialPassword)
		code := h.mailer.codeFor(t, address)
		h.svc.identifiers = denialConflictIdentifiers{h.svc.identifiers}
		if err := h.svc.Verify(ctx, address, code); !errors.Is(err, ErrRegistrationVerificationConflict) {
			t.Fatalf("Verify = %v, want ErrRegistrationVerificationConflict", err)
		}
		requireDenial(t, h.events, securityevent.TypeEmailVerified, securityevent.StatusFailure, securityevent.ReasonRevisionConflict, u.ID)
		assertNoSecretInDetails(t, h.events.recorded(), address, code)
	})
	t.Run("lockout keeps challenge_lockout", func(t *testing.T) {
		h := newHarness(t, nil)
		u := h.mustRegister(t, address, denialPassword)
		wrong := wrongCodeFor(h.mailer.codeFor(t, address))
		var err error
		for range challenge.MaxAttempts {
			err = h.svc.Verify(ctx, address, wrong)
		}
		if !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("Verify after %d wrong codes = %v, want ErrTooManyAttempts", challenge.MaxAttempts, err)
		}
		requireDenial(t, h.events, securityevent.TypeEmailVerified, securityevent.StatusBlocked, securityevent.ReasonLockout, u.ID)
		requireEvent(t, h.events, securityevent.TypeChallengeLockout, securityevent.StatusBlocked)
		assertNoSecretInDetails(t, h.events.recorded(), address, wrong)
	})
}

func TestRemovePasswordRecordsDenials(t *testing.T) {
	ctx := context.Background()
	t.Run("wrong code", func(t *testing.T) {
		h := newHarness(t, nil)
		userID, address, _ := h.mustVerifiedLogin(t, "remove-denied@example.com", denialPassword)
		if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
			t.Fatal(err)
		}
		wrong := wrongCodeFor(h.mailer.codeFor(t, address))
		if _, err := h.svc.RemovePassword(ctx, userID, wrong); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("RemovePassword = %v, want ErrChallengeInvalid", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordRemoved, securityevent.StatusFailure, securityevent.ReasonInvalidCode, userID)
		assertNoSecretInDetails(t, h.events.recorded(), wrong)
	})
	t.Run("credential policy", func(t *testing.T) {
		h := newHarness(t, nil)
		const userID = "u-remove-policy"
		h.seedUnlinkUser(t, userID, "remove-policy@example.com", true, false)
		if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
			t.Fatal(err)
		}
		code := h.mailer.codeFor(t, "remove-policy@example.com")
		if _, err := h.svc.RemovePassword(ctx, userID, code); err == nil {
			t.Fatal("removing the last login method succeeded")
		}
		requireDenial(t, h.events, securityevent.TypePasswordRemoved, securityevent.StatusFailure, securityevent.ReasonPolicy, userID)
		assertNoSecretInDetails(t, h.events.recorded(), code)
	})
	t.Run("revision conflict", func(t *testing.T) {
		h := newHarness(t, nil)
		const userID = "u-remove-race"
		address := h.seedUnlinkUser(t, userID, "remove-race@example.com", true, true, "google")
		if _, err := h.svc.StartRemovePassword(ctx, userID); err != nil {
			t.Fatal(err)
		}
		code := h.mailer.codeFor(t, address)
		h.creds.beforeApply = func() {
			if err := h.pw.Set(ctx, userID, "hash:changedduringremoval123"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.svc.RemovePassword(ctx, userID, code); !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("RemovePassword = %v, want conflict", err)
		}
		requireDenial(t, h.events, securityevent.TypePasswordRemoved, securityevent.StatusFailure, securityevent.ReasonRevisionConflict, userID)
		assertNoSecretInDetails(t, h.events.recorded(), code)
	})
}

func TestIdentifierManagementRecordsDenials(t *testing.T) {
	ctx := context.Background()
	t.Run("confirm wrong code", func(t *testing.T) {
		h := newHarness(t, nil)
		userID, _, sessionID := h.mustVerifiedLogin(t, "ident-denied@example.com", denialPassword)
		const target = "ident-target-0451@example.com"
		if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{SessionID: sessionID, UserID: userID,
			Kind: identifier.KindEmail, Value: target, Uses: identifier.Uses{Notification: true}}); err != nil {
			t.Fatal(err)
		}
		wrong := wrongCodeFor(h.mailer.proofCodeFor(t, target))
		if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{SessionID: sessionID, UserID: userID,
			Kind: identifier.KindEmail, Code: wrong}); !errors.Is(err, ErrChallengeInvalid) {
			t.Fatalf("Confirm = %v, want ErrChallengeInvalid", err)
		}
		requireDenial(t, h.events, securityevent.TypeEmailChanged, securityevent.StatusFailure, securityevent.ReasonInvalidCode, userID)
		assertNoSecretInDetails(t, h.events.recorded(), target, wrong)
	})
	t.Run("confirm revision conflict", func(t *testing.T) {
		h := newHarness(t, nil)
		userID, _, sessionID := h.mustVerifiedLogin(t, "ident-race@example.com", denialPassword)
		const target = "ident-race-target@example.com"
		if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{SessionID: sessionID, UserID: userID,
			Kind: identifier.KindEmail, Value: target, Uses: identifier.Uses{Login: true, Recovery: true}}); err != nil {
			t.Fatal(err)
		}
		code := h.mailer.proofCodeFor(t, target)
		h.svc.contactChanges = &mutateAfterPendingConsumption{Repository: h.svc.contactChanges, mutate: func() {
			if err := h.pw.Set(ctx, userID, "hash:changedduringconfirm123"); err != nil {
				t.Fatal(err)
			}
		}}
		if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{SessionID: sessionID, UserID: userID,
			Kind: identifier.KindEmail, Code: code}); !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("Confirm = %v, want conflict", err)
		}
		requireDenial(t, h.events, securityevent.TypeEmailChanged, securityevent.StatusFailure, securityevent.ReasonRevisionConflict, userID)
		assertNoSecretInDetails(t, h.events.recorded(), target, code)
	})
	t.Run("remove credential policy", func(t *testing.T) {
		h := newHarness(t, nil)
		const userID = "u-ident-sole"
		now := time.Now().UTC()
		h.users.mu.Lock()
		h.users.byID[userID] = user.User{ID: userID}
		h.users.mu.Unlock()
		sole := h.idents.insert(identifier.Identifier{
			UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "ident-sole@example.com",
			VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
			IsPrimary: true, CreatedAt: now, UpdatedAt: now,
		})
		if _, err := h.svc.mintSession(ctx, userID, h.currentAuthRevision(userID), h.svc.primaryAuthentication(session.MethodPassword)); err != nil {
			t.Fatal(err)
		}
		if err := h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{SessionID: h.sessionIDFor(t, userID), UserID: userID,
			IdentifierID: sole.ID}); err == nil {
			t.Fatal("removing the last login method succeeded")
		}
		requireDenial(t, h.events, securityevent.TypeEmailRemoved, securityevent.StatusFailure, securityevent.ReasonPolicy, userID)
		assertNoSecretInDetails(t, h.events.recorded(), "ident-sole@example.com")
	})
	for _, operation := range []string{"remove", "uses"} {
		t.Run(operation+" revision conflict", func(t *testing.T) {
			h := newHarness(t, nil)
			userID, _, sessionID := h.mustVerifiedLogin(t, "ident-mutation@example.com", denialPassword)
			// A second verified recovery identifier keeps the policy satisfied, so the
			// mutation reaches the store and loses the revision race there.
			now := time.Now().UTC()
			target := h.idents.insert(identifier.Identifier{
				UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "ident-second@example.com",
				VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
				CreatedAt: now, UpdatedAt: now,
			})
			var err error
			h.creds.beforeApply = func() {
				if err := h.pw.Set(ctx, userID, "hash:changedduringmutation123"); err != nil {
					t.Fatal(err)
				}
			}
			eventType := securityevent.TypeEmailRemoved
			if operation == "remove" {
				err = h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{UserID: userID, SessionID: sessionID, IdentifierID: target.ID})
			} else {
				eventType = securityevent.TypeIdentifierUsesChanged
				err = h.svc.SetIdentifierUses(ctx, IdentifierUsesInput{UserID: userID, SessionID: sessionID, IdentifierID: target.ID,
					Uses: identifier.Uses{Login: true, Recovery: true}})
			}
			if !errors.Is(err, sdk.ErrConflict) {
				t.Fatalf("%s = %v, want conflict", operation, err)
			}
			requireDenial(t, h.events, eventType, securityevent.StatusFailure, securityevent.ReasonRevisionConflict, userID)
			assertNoSecretInDetails(t, h.events.recorded(), "ident-second@example.com")
		})
	}
	t.Run("uses verification required", func(t *testing.T) {
		h := newHarness(t, nil)
		userID, _, sessionID := h.mustVerifiedLogin(t, "ident-uses@example.com", denialPassword)
		now := time.Now().UTC()
		pending := h.idents.insert(identifier.Identifier{
			UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "ident-unverified@example.com",
			NotificationEnabled: true, CreatedAt: now, UpdatedAt: now,
		})
		if err := h.svc.SetIdentifierUses(ctx, IdentifierUsesInput{SessionID: sessionID, UserID: userID,
			IdentifierID: pending.ID, Uses: identifier.Uses{Login: true}}); !errors.Is(err, identifier.ErrVerificationRequired) {
			t.Fatalf("SetIdentifierUses = %v, want ErrVerificationRequired", err)
		}
		requireDenial(t, h.events, securityevent.TypeIdentifierUsesChanged, securityevent.StatusFailure, securityevent.ReasonPolicy, userID)
		assertNoSecretInDetails(t, h.events.recorded(), "ident-unverified@example.com")
	})
}

// requireOAuthProvider asserts evt names provider and carries no other Details
// than the provider and, on a denial, the reason.
func requireOAuthProvider(t *testing.T, evt securityevent.SecurityEvent, provider string) {
	t.Helper()
	if evt.Details["provider"] != provider {
		t.Fatalf("%s row provider = %v, want %q", evt.EventType, evt.Details["provider"], provider)
	}
	for key := range evt.Details {
		if key != "provider" && key != "reason" {
			t.Fatalf("%s row carries Details[%q]", evt.EventType, key)
		}
	}
}

func TestOAuthCallbackRecordsDenials(t *testing.T) {
	ctx := context.Background()
	const providerEmail = "oauth-denied-0451@example.com"
	newProvider := func(subject string) *fakeProvider {
		return &fakeProvider{name: "google", trust: true, providerUserID: subject, email: providerEmail, emailVerified: true}
	}
	t.Run("malformed callback", func(t *testing.T) {
		h := newOAuthHarness(t, newProvider("g-malformed"), nil)
		if _, err := h.svc.OAuthCallback(ctx, "google", OAuthCallbackRequest{Mode: OAuthBrowser, State: "state-0451", Code: "code-0451"}); !errors.Is(err, ErrInvalidOAuthState) {
			t.Fatalf("OAuthCallback = %v, want ErrInvalidOAuthState", err)
		}
		evt := requireDenial(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusFailure, securityevent.ReasonInvalidState, "")
		requireOAuthProvider(t, evt, "google")
		assertNoSecretInDetails(t, h.events.recorded(), "state-0451", "code-0451")
	})
	t.Run("replayed state", func(t *testing.T) {
		h := newOAuthHarness(t, newProvider("g-replay"), nil)
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("replay = %v, want not found", err)
		}
		requireDenial(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusFailure, securityevent.ReasonInvalidState, "")
		assertNoSecretInDetails(t, h.events.recorded(), state, h.flows[state].FlowSecret, providerEmail)
	})
	t.Run("exchange failed", func(t *testing.T) {
		p := newProvider("g-exchange")
		p.exchangeErr = errors.New("provider refused the code")
		h := newOAuthHarness(t, p, nil)
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); err == nil {
			t.Fatal("callback succeeded")
		}
		evt := requireDenial(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusFailure, securityevent.ReasonExchangeFailed, "")
		requireOAuthProvider(t, evt, "google")
		assertNoSecretInDetails(t, h.events.recorded(), state, "provider refused the code")
	})
	t.Run("identity invalid", func(t *testing.T) {
		h := newOAuthHarness(t, newProvider(""), nil)
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); err == nil {
			t.Fatal("callback succeeded")
		}
		requireDenial(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusFailure, securityevent.ReasonIdentityInvalid, "")
		assertNoSecretInDetails(t, h.events.recorded(), state, "access-tok")
	})
	t.Run("email unverified", func(t *testing.T) {
		p := newProvider("g-unverified")
		p.emailVerified = false
		h := newOAuthHarness(t, p, nil)
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); !errors.Is(err, ErrProviderEmailUnverified) {
			t.Fatalf("callback = %v, want ErrProviderEmailUnverified", err)
		}
		requireDenial(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusFailure, securityevent.ReasonEmailUnverified, "")
		assertNoSecretInDetails(t, h.events.recorded(), providerEmail, state)
	})
	t.Run("inactive linked account", func(t *testing.T) {
		h := newOAuthHarness(t, newProvider("g-inactive"), nil)
		owner := h.mustOAuthUser(t, providerEmail)
		acct, _ := oauthaccount.New(owner.ID, "google", "g-inactive", time.Now())
		if _, err := h.accounts.Create(ctx, acct); err != nil {
			t.Fatal(err)
		}
		setUserStatus(h.users, owner.ID, user.StatusDeactivated)
		state := h.startState(t, "")
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(state)); !errors.Is(err, sdk.ErrUnauthorized) {
			t.Fatalf("callback = %v, want unauthorized", err)
		}
		evt := requireDenial(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusBlocked, securityevent.ReasonInactive, owner.ID)
		requireOAuthProvider(t, evt, "google")
		assertNoSecretInDetails(t, h.events.recorded(), providerEmail, state)
	})
	t.Run("link start conflict", func(t *testing.T) {
		h := newOAuthHarness(t, newProvider("g-shared"), nil)
		first := h.mustOAuthUser(t, "link-first@example.com")
		second := h.mustOAuthUser(t, "link-second@example.com")
		if _, err := h.startLink(ctx, first.ID, "google", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(h.provider.lastState)); err != nil {
			t.Fatal(err)
		}
		if _, err := h.startLink(ctx, second.ID, "google", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.OAuthCallback(ctx, "google", h.callbackRequest(h.provider.lastState)); err == nil {
			t.Fatal("conflicting link succeeded")
		}
		evt := requireDenial(t, h.events, securityevent.TypeOAuthLinked, securityevent.StatusFailure, securityevent.ReasonLinkConflict, second.ID)
		requireOAuthProvider(t, evt, "google")
		assertNoSecretInDetails(t, h.events.recorded(), providerEmail)
	})
}

func TestOAuthPendingLinkSentIsRecorded(t *testing.T) {
	const address = "pending-owner@example.com"
	h := newOAuthHarness(t, &fakeProvider{name: "google", trust: true, providerUserID: "g-pending-sent", email: address, emailVerified: true}, nil)
	owner := h.mustOAuthUser(t, address)
	token := h.completePendingLink(t)
	evt := requireEvent(t, h.events, securityevent.TypeOAuthPendingLinkSent, securityevent.StatusSuccess)
	if evt.UserID != owner.ID {
		t.Fatalf("oauth_pending_link_sent UserID = %q, want the matched account %q", evt.UserID, owner.ID)
	}
	requireOAuthProvider(t, evt, "google")
	if _, ok := evt.Details["reason"]; ok {
		t.Fatal("a success row carries a reason")
	}
	assertNoSecretInDetails(t, h.events.recorded(), address, token)
}

func TestVerifyLinkRecordsDenials(t *testing.T) {
	ctx := context.Background()
	const address = "link-owner@example.com"
	build := func(t *testing.T, subject string) *oauthHarness {
		return newOAuthHarness(t, &fakeProvider{name: "google", trust: true, providerUserID: subject, email: address, emailVerified: true}, nil)
	}
	t.Run("invalid token", func(t *testing.T) {
		h := build(t, "g-link-token")
		const token = "unknown-link-token-0451"
		if _, err := h.svc.VerifyLink(ctx, token); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("VerifyLink = %v, want not found", err)
		}
		requireDenial(t, h.events, securityevent.TypeOAuthLinkVerified, securityevent.StatusFailure, securityevent.ReasonInvalidToken, "")
		assertNoSecretInDetails(t, h.events.recorded(), token)
	})
	t.Run("invalid state", func(t *testing.T) {
		h := build(t, "g-link-state")
		owner := h.mustOAuthUserVerified(t, address, false)
		token := h.completePendingLink(t)
		h.idents.retireAll(owner.ID, time.Now())
		if _, err := h.svc.VerifyLink(ctx, token); !errors.Is(err, ErrInvalidOAuthState) {
			t.Fatalf("VerifyLink = %v, want ErrInvalidOAuthState", err)
		}
		evt := requireDenial(t, h.events, securityevent.TypeOAuthLinkVerified, securityevent.StatusFailure, securityevent.ReasonInvalidState, owner.ID)
		requireOAuthProvider(t, evt, "google")
		assertNoSecretInDetails(t, h.events.recorded(), address, token)
	})
	t.Run("inactive", func(t *testing.T) {
		h := build(t, "g-link-inactive")
		owner := h.mustOAuthUser(t, address)
		token := h.completePendingLink(t)
		setUserStatus(h.users, owner.ID, user.StatusDeactivated)
		if _, err := h.svc.VerifyLink(ctx, token); err == nil {
			t.Fatal("VerifyLink succeeded for a deactivated account")
		}
		evt := requireDenial(t, h.events, securityevent.TypeOAuthLinkVerified, securityevent.StatusBlocked, securityevent.ReasonInactive, owner.ID)
		requireOAuthProvider(t, evt, "google")
		assertNoSecretInDetails(t, h.events.recorded(), address, token)
	})
}
