package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
)

// totpCode is the code of the base32 secret steps time steps from now; the ±1
// window admits steps -1, 0 and 1.
func totpCode(t *testing.T, secret string, steps int64) string {
	t.Helper()
	raw, err := totp.DecodeSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	return totp.Code(raw, totp.Step(time.Now())+steps)
}

// enrollTOTP enrolls c's authenticator app over the JSON routes and returns the
// secret and the recovery codes.
func (c *linkClient) enrollTOTP() (string, []string) {
	c.t.Helper()
	resp, body := c.mutate("/auth/totp/enrollment", "{}")
	var e struct {
		Secret     string `json:"secret"`
		OTPAuthURI string `json:"otpauth_uri"`
		Issuer     string `json:"issuer"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &e) != nil || e.Issuer != "Gopernicus CMS" || !strings.HasPrefix(e.OTPAuthURI, "otpauth://totp/Gopernicus%20CMS:") {
		c.t.Fatalf("enrollment = %d %s", resp.StatusCode, body)
	}
	resp, body = c.mutate("/auth/totp/enrollment/confirm", `{"code":"`+totpCode(c.t, e.Secret, 0)+`"}`)
	var confirmed struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &confirmed) != nil || len(confirmed.RecoveryCodes) != 10 {
		c.t.Fatalf("confirm = %d %s", resp.StatusCode, body)
	}
	return e.Secret, confirmed.RecoveryCodes
}

// TestTOTPThroughTheHost walks the TOTP JSON routes over this host's real route
// table — the ring from NewKeys, authmem, the in-process mailer — with codes
// computed by the totp package: the admins default now naming totp as a
// source, the AAL1 bootstrap, the reused confirm step, the AAL2 step-up and
// the methods claim, the second-factor gate on passkeys, recovery codes, the
// admin gate and reset.
func TestTOTPThroughTheHost(t *testing.T) {
	var admins sync.Map
	host := newPasskeyHost(t, func(cfg *authenticationConfig) {
		cfg.UserAdminCheck = func(_ context.Context, req authenticationhttp.UserAdminCheckRequest) error {
			if _, ok := admins.Load(req.Principal.ID); ok {
				return nil
			}
			return sdk.ErrForbidden
		}
	})
	if !strings.Contains(host.logs.String(), "mfa_required=admins source=default") || !strings.Contains(host.logs.String(), "sources=passkey,totp methods=any") {
		t.Fatalf("boot line:\n%s", host.logs)
	}

	bob := host.signUp("bob@example.com")
	if status, _ := bob.assurance("/demo/mfa"); status != http.StatusUnauthorized {
		t.Fatalf("AAL1 /demo/mfa = %d", status)
	}
	secret, _ := bob.enrollTOTP()
	if resp, body := bob.mutate("/auth/step-up/totp", `{"code":"`+totpCode(t, secret, 0)+`"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the confirmed step = %d %s, want 401 (reused)", resp.StatusCode, body)
	}
	if status, _ := bob.enrollPasskey(host.authenticator(true)); status != http.StatusForbidden {
		t.Fatalf("a TOTP holder's passkey at AAL1 = %d, want 403", status)
	}
	resp, body := bob.mutate("/auth/step-up/totp", `{"code":"`+totpCode(t, secret, 1)+`"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"assurance":"aal2"`) {
		t.Fatalf("TOTP step-up = %d %s", resp.StatusCode, body)
	}
	if status, level := bob.assurance("/demo/mfa"); status != http.StatusOK || level != "aal2" {
		t.Fatalf("/demo/mfa after the step-up = %d %q", status, level)
	}
	_, methods := bob.do("GET", "/auth/methods", "", nil)
	if !strings.Contains(string(methods), `"assurance":"aal2"`) || !strings.Contains(string(methods), `"recovery_codes":{"remaining":10}`) || !strings.Contains(string(methods), `"totp":{"enrolled":true`) {
		t.Fatalf("methods = %s", methods)
	}

	carol := host.signUp("carol@example.com")
	_, carolCodes := carol.enrollTOTP()
	resp, body = carol.mutate("/auth/step-up/recovery-code", `{"code":"`+strings.ToUpper(carolCodes[0])+`"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"assurance":"aal2"`) {
		t.Fatalf("recovery-code step-up = %d %s", resp.StatusCode, body)
	}
	if resp, _ := carol.mutate("/auth/step-up/recovery-code", `{"code":"`+carolCodes[0]+`"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a spent recovery code = %d", resp.StatusCode)
	}

	admin := host.signUp("ops@example.com")
	admins.Store(admin.userIDFor(), true)
	if resp, _ := admin.do("GET", "/auth/admin/users", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("AAL1 admin = %d", resp.StatusCode)
	}
	adminSecret, _ := admin.enrollTOTP()
	if resp, body := admin.mutate("/auth/step-up/totp", `{"code":"`+totpCode(t, adminSecret, 1)+`"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin step-up = %d %s", resp.StatusCode, body)
	}
	if resp, body := admin.do("GET", "/auth/admin/users", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("AAL2 admin = %d %s", resp.StatusCode, body)
	}
	resp, body = admin.mutate("/auth/admin/users/"+bob.userIDFor()+"/totp/reset", "{}")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"removed":true`) {
		t.Fatalf("reset = %d %s", resp.StatusCode, body)
	}
	if resp, _ := bob.do("GET", "/auth/totp", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bob's session after the reset = %d, want revoked", resp.StatusCode)
	}
	again := host.newClient()
	if resp, body := again.do("POST", "/auth/login", `{"email":"bob@example.com","password":"`+linkPassword+`"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("login after reset = %d %s", resp.StatusCode, body)
	}
	again.enrollTOTP()
}
