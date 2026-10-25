package authentication

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn/webauthntest"
)

// registrationOptions is the part of the creation options these tests read.
type registrationOptions struct {
	ExcludeCredentials []struct {
		ID string `json:"id"`
	} `json:"excludeCredentials"`
	AuthenticatorSelection struct {
		UserVerification string `json:"userVerification"`
	} `json:"authenticatorSelection"`
}

func readRegistrationOptions(t *testing.T, ceremony PasskeyCeremony) registrationOptions {
	t.Helper()
	var opts registrationOptions
	if err := json.Unmarshal(ceremony.PublicKey, &opts); err != nil {
		t.Fatalf("creation options %s: %v", ceremony.PublicKey, err)
	}
	return opts
}

func (o registrationOptions) excludes(id []byte) bool {
	return slices.ContainsFunc(o.ExcludeCredentials, func(d struct {
		ID string `json:"id"`
	}) bool {
		raw, err := base64.RawURLEncoding.DecodeString(d.ID)
		return err == nil && bytes.Equal(raw, id)
	})
}

// finishWith registers a's response to ceremony for the caller's session.
func (h *passkeyHarness) finishWith(t *testing.T, a *webauthntest.Authenticator, ceremony PasskeyCeremony, sessionID, userID string) (passkey.Passkey, error) {
	t.Helper()
	response, err := a.Register(ceremony.PublicKey)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return h.svc.FinishPasskeyRegistration(context.Background(), PasskeyRegistrationInput{SessionID: sessionID, UserID: userID, Ceremony: ceremony.Ceremony, Response: response})
}

// TestPasskeyRegistrationUserVerificationRequest pins the per-ceremony
// requirement: absent or preferred keeps the host's, required raises it, a
// request never lowers a host's required, and any other value is refused.
func TestPasskeyRegistrationUserVerificationRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		host    webauthn.Requirement
		request []PasskeyRegistrationOptions
		want    string
	}{
		{"absent", webauthn.Preferred, nil, "preferred"},
		{"empty", webauthn.Preferred, []PasskeyRegistrationOptions{{}}, "preferred"},
		{"preferred", webauthn.Preferred, []PasskeyRegistrationOptions{{UserVerification: webauthn.Preferred}}, "preferred"},
		{"required", webauthn.Preferred, []PasskeyRegistrationOptions{{UserVerification: webauthn.Required}}, "required"},
		{"host required, absent", webauthn.Required, nil, "required"},
		{"host required, preferred does not lower it", webauthn.Required, []PasskeyRegistrationOptions{{UserVerification: webauthn.Preferred}}, "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newPasskeyHarness(t)
			h.svc.passkeyUV = tc.host
			u, _, sessionID := h.account(t, "uv@example.com")
			ceremony, err := h.svc.BeginPasskeyRegistration(context.Background(), sessionID, u.ID, tc.request...)
			if err != nil {
				t.Fatalf("BeginPasskeyRegistration: %v", err)
			}
			if got := readRegistrationOptions(t, ceremony).AuthenticatorSelection.UserVerification; got != tc.want {
				t.Errorf("userVerification = %q, want %q", got, tc.want)
			}
		})
	}
	h := newPasskeyHarness(t)
	u, _, sessionID := h.account(t, "unknown@example.com")
	for _, uv := range []webauthn.Requirement{webauthn.Discouraged, "REQUIRED", "always"} {
		if _, err := h.svc.BeginPasskeyRegistration(context.Background(), sessionID, u.ID, PasskeyRegistrationOptions{UserVerification: uv}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%q = %v, want ErrInvalidInput", uv, err)
		}
	}
}

// TestPasskeyRegistrationRequiredRefusesPresenceOnly pins that a UV-required
// ceremony refuses a response without user verification through the relying
// party's deferred check, as passkey_rejected with a uv_required row.
func TestPasskeyRegistrationRequiredRefusesPresenceOnly(t *testing.T) {
	h := newPasskeyHarness(t)
	u, _, sessionID := h.account(t, "presence@example.com")
	ceremony, err := h.svc.BeginPasskeyRegistration(context.Background(), sessionID, u.ID, PasskeyRegistrationOptions{UserVerification: webauthn.Required})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.finishWith(t, newPasskeyAuthenticator(false), ceremony, sessionID, u.ID); !errors.Is(err, ErrPasskeyRejected) {
		t.Fatalf("presence-only response = %v, want ErrPasskeyRejected", err)
	}
	rows := h.rows(securityevent.TypePasskeyFailed)
	if len(rows) != 1 || rows[0].Details["reason"] != securityevent.ReasonUVRequired || rows[0].Details["ceremony"] != "registration" || rows[0].UserID != u.ID {
		t.Fatalf("passkey_failed rows = %+v", rows)
	}
	if stored, _ := h.passkeys.ListByUser(context.Background(), u.ID); len(stored) != 0 {
		t.Fatalf("stored passkeys = %+v", stored)
	}
}

// TestPasskeyRegistrationRequiredUpgradesInPlace pins the exclude list: a
// UV-required ceremony leaves presence-only passkeys out, so the authenticator
// holding one can add a verified passkey; AAL2-capable passkeys stay excluded,
// and a ceremony that does not require UV excludes every passkey.
func TestPasskeyRegistrationRequiredUpgradesInPlace(t *testing.T) {
	ctx := context.Background()
	h := newPasskeyHarness(t)
	u, _, sessionID := h.account(t, "upgrade@example.com")
	a := newPasskeyAuthenticator(false)
	presence := h.enroll(t, a, sessionID, u.ID, "")
	if presence.AAL2Capable() {
		t.Fatalf("presence-only passkey = %+v", presence)
	}

	ceremony, err := h.svc.BeginPasskeyRegistration(ctx, sessionID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !readRegistrationOptions(t, ceremony).excludes(presence.CredentialID) {
		t.Fatal("a preferred ceremony does not exclude the presence-only passkey")
	}
	if _, err := a.Register(ceremony.PublicKey); !errors.Is(err, webauthntest.ErrCredentialExcluded) {
		t.Fatalf("Register under a preferred ceremony = %v, want ErrCredentialExcluded", err)
	}

	ceremony, err = h.svc.BeginPasskeyRegistration(ctx, sessionID, u.ID, PasskeyRegistrationOptions{UserVerification: webauthn.Required})
	if err != nil {
		t.Fatal(err)
	}
	if opts := readRegistrationOptions(t, ceremony); len(opts.ExcludeCredentials) != 0 || opts.AuthenticatorSelection.UserVerification != "required" {
		t.Fatalf("required ceremony options = %+v, want no exclusions", opts)
	}
	a.SetUserVerified(true)
	verified, err := h.finishWith(t, a, ceremony, sessionID, u.ID)
	if err != nil {
		t.Fatalf("FinishPasskeyRegistration: %v", err)
	}
	if !verified.AAL2Capable() || !bytes.Equal(verified.UserHandle, presence.UserHandle) {
		t.Fatalf("verified passkey = %+v, want AAL2-capable under the same handle", verified)
	}
	if stored, _ := h.passkeys.ListByUser(ctx, u.ID); len(stored) != 2 {
		t.Fatalf("stored passkeys = %d, want the presence-only one kept beside the verified one", len(stored))
	}

	stepped, err := h.stepUp(t, a, sessionID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	aal2 := h.sessionOf(t, stepped.Tokens)
	for _, tc := range []struct {
		opts     []PasskeyRegistrationOptions
		presence bool
	}{
		{nil, true},
		{[]PasskeyRegistrationOptions{{UserVerification: webauthn.Required}}, false},
	} {
		ceremony, err := h.svc.BeginPasskeyRegistration(ctx, aal2, u.ID, tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		opts := readRegistrationOptions(t, ceremony)
		if !opts.excludes(verified.CredentialID) || opts.excludes(presence.CredentialID) != tc.presence {
			t.Errorf("opts %+v: excludes verified %v, presence-only %v", tc.opts, opts.excludes(verified.CredentialID), opts.excludes(presence.CredentialID))
		}
	}
}
