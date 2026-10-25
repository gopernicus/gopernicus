// Package webauthntest is the conformance suite for webauthn.RelyingParty
// implementations and the software Authenticator it drives. Run(t, factory)
// pins the shared contract against bytes the Authenticator produces: the
// options of both ceremonies, registration and assertion with and without user
// verification under each Requirement, ceremony truth (a credential registered
// WITH verification and asserted WITHOUT it under Preferred reports
// UserVerified false), two credentials of one user, origin admission, every
// rejection class, extension outputs, counter regression, expiry and each
// Config.Validate refusal.
//
// The Authenticator is also the fixture for an implementation's consumers: a
// pocket test registers and asserts with it exactly as a browser would. Imports
// stdlib, the sdk root, sdk/pkg/web and sdk/capabilities/authentication/webauthn
// only.
//
// The suite cannot verify an implementation's verifier against itself, so the
// package's self-test runs Run over a test-only reference relying party built on
// stdlib crypto and an independent strict CBOR decoder, and the
// implementation of record runs the same suite over a WebAuthn library.
package webauthntest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const (
	suiteRPID         = "example.com"
	suiteRPName       = "webauthntest"
	suiteOrigin       = "https://example.com"
	suiteLoginOrigin  = "https://login.example.com"
	suiteTenantOrigin = "https://acme.tenants.example.com"
	suiteTenants      = "https://*.tenants.example.com"
	foreignRPID       = "evil.example.net"
	foreignOrigin     = "https://evil.example.net"

	// expiringTimeout is short enough to wait out and long enough for a
	// prompt ceremony to finish inside it.
	expiringTimeout = 250 * time.Millisecond
)

// Factory builds the relying party under test from cfg. It must run
// cfg.Validate first and return its error (matching sdk.ErrInvalidInput) for an
// invalid cfg.
type Factory func(cfg webauthn.Config) (webauthn.RelyingParty, error)

type suite struct {
	newRelyingParty Factory
}

type rejection[T any] struct {
	name string
	run  func(t *testing.T) (T, error)
}

// Run exercises the RelyingParty contract. Each subtest builds its own relying
// parties from newRelyingParty and its own authenticators.
func Run(t *testing.T, newRelyingParty Factory) {
	t.Helper()
	s := suite{newRelyingParty: newRelyingParty}

	t.Run("Config", s.testConfig)
	t.Run("ValidateRefusals", s.testValidateRefusals)
	t.Run("RegistrationOptions", s.testRegistrationOptions)
	t.Run("StartRefusals", s.testStartRefusals)
	t.Run("Registration", s.testRegistration)
	t.Run("RegistrationBackupFlags", s.testRegistrationBackupFlags)
	t.Run("RegistrationRejections", s.testRegistrationRejections)
	t.Run("AssertionOptions", s.testAssertionOptions)
	t.Run("Assertion", s.testAssertion)
	t.Run("VerifiedRegistrationUnverifiedAssertion", s.testVerifiedRegistrationUnverifiedAssertion)
	t.Run("AssertionCeremonyTruth", s.testAssertionCeremonyTruth)
	t.Run("ReadAssertion", s.testReadAssertion)
	t.Run("TwoCredentialsOneUser", s.testTwoCredentialsOneUser)
	t.Run("OriginAdmission", s.testOriginAdmission)
	t.Run("AssertionRejections", s.testAssertionRejections)
	t.Run("AssertionIgnoresExtensionOutputs", s.testAssertionIgnoresExtensionOutputs)
	t.Run("CounterRegression", s.testCounterRegression)
	t.Run("Expiry", s.testExpiry)
}

func (s suite) testConfig(t *testing.T) {
	cfg := suiteConfig(t)
	got := s.relyingParty(t, cfg).Config()
	if got.RPID != suiteRPID || got.RPName != suiteRPName {
		t.Errorf("Config() RPID, RPName = %q, %q; want %q, %q", got.RPID, got.RPName, suiteRPID, suiteRPName)
	}
	if got.Timeout != webauthn.MaxTimeout {
		t.Errorf("Config().Timeout = %v for a zero Timeout, want webauthn.MaxTimeout (%v)", got.Timeout, webauthn.MaxTimeout)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("Config().Validate() = %v, want nil", err)
	}
	for origin, want := range map[string]bool{suiteOrigin: true, suiteTenantOrigin: true, foreignOrigin: false} {
		if got.Origins.Allows(origin) != want {
			t.Errorf("Config().Origins.Allows(%q) = %v, want %v", origin, !want, want)
		}
	}
	cfg.Timeout = 2 * time.Minute
	if got := s.relyingParty(t, cfg).Config().Timeout; got != 2*time.Minute {
		t.Errorf("Config().Timeout = %v, want the configured 2m", got)
	}
}

func (s suite) testValidateRefusals(t *testing.T) {
	base := suiteConfig(t)
	with := func(edit func(*webauthn.Config)) webauthn.Config {
		cfg := base
		edit(&cfg)
		return cfg
	}
	tests := []struct {
		name    string
		cfg     webauthn.Config
		outside string
	}{
		{name: "empty RPID", cfg: with(func(c *webauthn.Config) { c.RPID = "" })},
		{name: "uppercase RPID", cfg: with(func(c *webauthn.Config) { c.RPID = "Example.com" })},
		{name: "RPID with a scheme", cfg: with(func(c *webauthn.Config) { c.RPID = "https://example.com" })},
		{name: "RPID with a port", cfg: with(func(c *webauthn.Config) { c.RPID = "example.com:443" })},
		{name: "RPID with a path", cfg: with(func(c *webauthn.Config) { c.RPID = "example.com/login" })},
		{name: "IP RPID", cfg: with(func(c *webauthn.Config) {
			c.RPID = "127.0.0.1"
			c.Origins = policy(t, []string{"https://127.0.0.1"})
		})},
		{name: "empty RPName", cfg: with(func(c *webauthn.Config) { c.RPName = "" })},
		{name: "zero Origins", cfg: with(func(c *webauthn.Config) { c.Origins = web.OriginPolicy{} })},
		{name: "exact origin outside the RPID", outside: "example.org", cfg: with(func(c *webauthn.Config) {
			c.Origins = policy(t, []string{suiteOrigin, "https://example.org"})
		})},
		{name: "namespace outside the RPID", outside: "other.test", cfg: with(func(c *webauthn.Config) {
			c.Origins = policy(t, nil, "https://*.other.test")
		})},
		{name: "negative Timeout", cfg: with(func(c *webauthn.Config) { c.Timeout = -time.Second })},
		{name: "Timeout above the maximum", cfg: with(func(c *webauthn.Config) { c.Timeout = 6 * time.Minute })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Config.Validate() = %v, want sdk.ErrInvalidInput", err)
			}
			rp, err := s.newRelyingParty(tt.cfg)
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("factory error = %v, want sdk.ErrInvalidInput (run Config.Validate first)", err)
			}
			if rp != nil && !reflect.ValueOf(rp).IsZero() {
				t.Errorf("factory returned a relying party beside its error")
			}
			if tt.outside == "" {
				return
			}
			if !errors.Is(err, webauthn.ErrOriginOutsideRPID) {
				t.Errorf("factory error = %v, want webauthn.ErrOriginOutsideRPID", err)
			}
			if !strings.Contains(err.Error(), tt.outside) {
				t.Errorf("factory error = %q, want it to name %q", err, tt.outside)
			}
		})
	}
}

func (s suite) testRegistrationOptions(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	exclude := [][]byte{randomBytes(32), randomBytes(16)}
	var challenges []string
	for _, uv := range []webauthn.Requirement{"", webauthn.Required, webauthn.Preferred, webauthn.Discouraged} {
		t.Run(requirementName(uv), func(t *testing.T) {
			before := time.Now()
			ceremony := beginRegistration(t, rp, webauthn.RegistrationStart{User: user, Exclude: exclude, UserVerification: uv})
			after := time.Now()
			checkCeremony(t, ceremony, before, after, webauthn.MaxTimeout)
			var opts creationOptionsJSON
			decodeOptions(t, ceremony.Options, &opts)
			if opts.RP.ID != suiteRPID || opts.RP.Name != suiteRPName {
				t.Errorf("options rp = {%q, %q}, want {%q, %q}", opts.RP.ID, opts.RP.Name, suiteRPID, suiteRPName)
			}
			if opts.User.ID != b64.EncodeToString(user.Handle) || opts.User.Name != user.Name || opts.User.DisplayName != user.DisplayName {
				t.Errorf("options user = %+v, want the base64url handle, Name and DisplayName", opts.User)
			}
			challenges = append(challenges, checkChallenge(t, opts.Challenge))
			var algs []int64
			for _, p := range opts.PubKeyCredParams {
				if p.Type != publicKeyType {
					t.Errorf("options pubKeyCredParams type %q, want %q", p.Type, publicKeyType)
				}
				algs = append(algs, p.Alg)
			}
			slices.Sort(algs)
			if want := []int64{-257, -8, -7}; !slices.Equal(algs, want) {
				t.Errorf("options pubKeyCredParams algs = %v, want exactly ES256, EdDSA, RS256 %v", algs, want)
			}
			checkTimeout(t, opts.Timeout, webauthn.MaxTimeout)
			var excluded [][]byte
			for _, d := range opts.ExcludeCredentials {
				if d.Type != publicKeyType {
					t.Errorf("options excludeCredentials type %q, want %q", d.Type, publicKeyType)
				}
				id, err := b64.DecodeString(d.ID)
				if err != nil {
					t.Errorf("options excludeCredentials id %q is not base64url", d.ID)
				}
				excluded = append(excluded, id)
			}
			if !slices.EqualFunc(excluded, exclude, bytes.Equal) {
				t.Errorf("options excludeCredentials = %x, want RegistrationStart.Exclude %x", excluded, exclude)
			}
			sel := opts.AuthenticatorSelection
			if sel.ResidentKey != "required" {
				t.Errorf("options residentKey = %q, want \"required\"", sel.ResidentKey)
			}
			if sel.RequireResidentKey != nil && !*sel.RequireResidentKey {
				t.Error("options requireResidentKey = false beside residentKey \"required\"")
			}
			if want := string(resolved(uv)); sel.UserVerification != want {
				t.Errorf("options userVerification = %q, want %q", sel.UserVerification, want)
			}
			if opts.Attestation != "" && opts.Attestation != "none" {
				t.Errorf("options attestation = %q, want \"none\"", opts.Attestation)
			}
		})
	}
	if len(challenges) == 4 && len(slices.Compact(slices.Sorted(slices.Values(challenges)))) != 4 {
		t.Errorf("challenges repeat across ceremonies: %q", challenges)
	}
	ceremony := beginRegistration(t, rp, webauthn.RegistrationStart{User: user})
	var opts creationOptionsJSON
	decodeOptions(t, ceremony.Options, &opts)
	if len(opts.ExcludeCredentials) != 0 {
		t.Errorf("options excludeCredentials = %+v with no Exclude, want none", opts.ExcludeCredentials)
	}
}

func (s suite) testStartRefusals(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	ctx := context.Background()
	for name, in := range map[string]webauthn.RegistrationStart{
		"empty handle":           {User: webauthn.User{Name: user.Name, DisplayName: user.DisplayName}},
		"oversize handle":        {User: webauthn.User{Handle: randomBytes(webauthn.MaxUserHandleBytes + 1), Name: user.Name}},
		"unknown requirement":    {User: user, UserVerification: "sometimes"},
		"misspelled requirement": {User: user, UserVerification: "require"},
	} {
		ceremony, err := rp.BeginRegistration(ctx, in)
		if !errors.Is(err, sdk.ErrInvalidInput) || !reflect.ValueOf(ceremony).IsZero() {
			t.Errorf("BeginRegistration(%s) = %v, %v; want a zero Ceremony and sdk.ErrInvalidInput", name, ceremony.Options, err)
		}
	}
	ceremony, err := rp.BeginAssertion(ctx, webauthn.AssertionStart{UserVerification: "sometimes"})
	if !errors.Is(err, sdk.ErrInvalidInput) || !reflect.ValueOf(ceremony).IsZero() {
		t.Errorf("BeginAssertion(unknown requirement) = %v, %v; want a zero Ceremony and sdk.ErrInvalidInput", ceremony.Options, err)
	}
	if _, err := rp.BeginRegistration(ctx, webauthn.RegistrationStart{User: webauthn.User{Handle: randomBytes(webauthn.MaxUserHandleBytes), Name: user.Name}}); err != nil {
		t.Errorf("BeginRegistration(a %d-byte handle) = %v, want nil", webauthn.MaxUserHandleBytes, err)
	}
}

func (s suite) testRegistration(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	tests := []struct {
		name      string
		uv        webauthn.Requirement
		verifying bool
		refused   bool
	}{
		{"default with verification", "", true, false},
		{"default without verification", "", false, false},
		{"preferred with verification", webauthn.Preferred, true, false},
		{"preferred without verification", webauthn.Preferred, false, false},
		{"required with verification", webauthn.Required, true, false},
		{"required without verification", webauthn.Required, false, true},
		{"discouraged with verification", webauthn.Discouraged, true, false},
		{"discouraged without verification", webauthn.Discouraged, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthenticator(tt.verifying)
			a.SetSignCount(7)
			ceremony := beginRegistration(t, rp, webauthn.RegistrationStart{User: newUser(t), UserVerification: tt.uv})
			response := mustRegister(t, a, ceremony.Options)
			cred, err := finishRegistration(rp, ceremony.State, response)
			if tt.refused {
				wantVerificationRequired(t, "FinishRegistration", err, cred)
				return
			}
			if err != nil {
				t.Fatalf("FinishRegistration = %v, want nil", err)
			}
			var raw registrationResponseJSON
			if err := json.Unmarshal(response, &raw); err != nil {
				t.Fatal(err)
			}
			if got := b64.EncodeToString(cred.ID); got != raw.RawID {
				t.Errorf("Credential.ID = %s, want the response rawId %s", got, raw.RawID)
			}
			if !bytes.Equal(cred.PublicKey, a.publicKeyOf(cred.ID)) {
				t.Errorf("Credential.PublicKey = %x, want the COSE_Key from the attested credential data %x", cred.PublicKey, a.publicKeyOf(cred.ID))
			}
			if !bytes.Equal(cred.AAGUID, testAAGUID) {
				t.Errorf("Credential.AAGUID = %x, want %x", cred.AAGUID, testAAGUID)
			}
			if cred.SignCount != 7 {
				t.Errorf("Credential.SignCount = %d, want this ceremony's 7", cred.SignCount)
			}
			if !slices.Equal(cred.Transports, []string{transportInternal}) {
				t.Errorf("Credential.Transports = %q, want %q", cred.Transports, []string{transportInternal})
			}
			if cred.UserVerified != tt.verifying || cred.BackupEligible || cred.BackupState {
				t.Errorf("Credential flags UV/BE/BS = %v/%v/%v, want %v/false/false from this ceremony", cred.UserVerified, cred.BackupEligible, cred.BackupState, tt.verifying)
			}
		})
	}
}

func (s suite) testRegistrationBackupFlags(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	for _, tt := range []struct {
		eligible, state bool
	}{{true, true}, {true, false}} {
		a := newAuthenticator(true)
		a.setBackup(tt.eligible, tt.state)
		cred := enroll(t, rp, a, newUser(t), "")
		if cred.BackupEligible != tt.eligible || cred.BackupState != tt.state {
			t.Errorf("Credential BE/BS = %v/%v, want %v/%v from this ceremony", cred.BackupEligible, cred.BackupState, tt.eligible, tt.state)
		}
	}
	a := newAuthenticator(true)
	a.setBackup(false, true)
	ceremony := beginRegistration(t, rp, webauthn.RegistrationStart{User: newUser(t)})
	cred, err := finishRegistration(rp, ceremony.State, mustRegister(t, a, ceremony.Options))
	wantRejected(t, "FinishRegistration(backup state without backup eligibility)", err, cred)
}

func (s suite) testRegistrationRejections(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	start := webauthn.RegistrationStart{User: newUser(t)}
	fresh := func(t *testing.T, a *Authenticator, f faults) (webauthn.Ceremony, []byte) {
		t.Helper()
		ceremony := beginRegistration(t, rp, start)
		response, err := a.register(ceremony.Options, f)
		if err != nil {
			t.Fatalf("Authenticator.Register: %v", err)
		}
		return ceremony, response
	}
	faithful := func(t *testing.T) (webauthn.Ceremony, []byte) {
		return fresh(t, newAuthenticator(true), faults{})
	}
	withResponse := func(response func(t *testing.T) []byte) func(t *testing.T) (webauthn.Credential, error) {
		return func(t *testing.T) (webauthn.Credential, error) {
			ceremony := beginRegistration(t, rp, start)
			return finishRegistration(rp, ceremony.State, response(t))
		}
	}
	withState := func(state func(t *testing.T) []byte) func(t *testing.T) (webauthn.Credential, error) {
		return func(t *testing.T) (webauthn.Credential, error) {
			_, response := faithful(t)
			return finishRegistration(rp, state(t), response)
		}
	}
	withFaults := func(a func() *Authenticator, f faults) func(t *testing.T) (webauthn.Credential, error) {
		return func(t *testing.T) (webauthn.Credential, error) {
			ceremony, response := fresh(t, a(), f)
			return finishRegistration(rp, ceremony.State, response)
		}
	}
	fromOrigin := func(origin string) func() *Authenticator {
		return func() *Authenticator {
			a := newAuthenticator(true)
			a.SetOrigin(origin)
			return a
		}
	}
	faithfulAuthenticator := func() *Authenticator { return newAuthenticator(true) }

	cases := []rejection[webauthn.Credential]{
		{"foreign state", withState(func(t *testing.T) []byte { c, _ := faithful(t); return c.State })},
		{"assertion state", withState(func(t *testing.T) []byte { return beginAssertion(t, rp, "").State })},
		{"assertion state whose challenge the response carries", func(t *testing.T) (webauthn.Credential, error) {
			assertion := beginAssertion(t, rp, "")
			options := withChallenge(t, beginRegistration(t, rp, start).Options, assertion.Options)
			return finishRegistration(rp, assertion.State, mustRegister(t, newAuthenticator(true), options))
		}},
		{"garbage state", withState(func(*testing.T) []byte { return []byte("not a ceremony state") })},
		{"empty state", withState(func(*testing.T) []byte { return nil })},
		{"origin outside the policy", withFaults(fromOrigin(foreignOrigin), faults{})},
		{"origin under the RPID the policy does not admit", withFaults(fromOrigin("https://other.example.com"), faults{})},
		{"rpIdHash of another RP ID", withFaults(faithfulAuthenticator, faults{rpID: foreignRPID})},
		{"clientDataJSON type webauthn.get", withFaults(faithfulAuthenticator, faults{clientDataType: getType})},
		{"user presence absent", withFaults(faithfulAuthenticator, faults{withoutPresence: true})},
		{"unsolicited extension outputs", withFaults(func() *Authenticator {
			a := newAuthenticator(true)
			a.SetExtensionOutputs(map[string]any{"credProtect": uint64(2)})
			return a
		}, faults{})},
		{"nil response", withResponse(func(*testing.T) []byte { return nil })},
		{"empty object", withResponse(func(*testing.T) []byte { return []byte(`{}`) })},
		{"not JSON", withResponse(func(*testing.T) []byte { return []byte("not json") })},
		{"truncated", withResponse(func(t *testing.T) []byte { _, r := faithful(t); return r[:len(r)/2] })},
		{"id differs from rawId", withResponse(func(t *testing.T) []byte {
			_, r := faithful(t)
			return tamper(t, r, func(v *registrationResponseJSON) { v.ID = b64.EncodeToString(randomBytes(32)) })
		})},
		{"an assertion response", withResponse(func(t *testing.T) []byte {
			a := newAuthenticator(true)
			enroll(t, rp, a, newUser(t), "")
			return mustAssert(t, a, beginAssertion(t, rp, "").Options)
		})},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cred, err := tt.run(t)
			wantRejected(t, "FinishRegistration", err, cred)
		})
	}
}

func (s suite) testAssertionOptions(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	var challenges []string
	for _, uv := range []webauthn.Requirement{"", webauthn.Required, webauthn.Preferred, webauthn.Discouraged} {
		t.Run(requirementName(uv), func(t *testing.T) {
			before := time.Now()
			ceremony := beginAssertion(t, rp, uv)
			after := time.Now()
			checkCeremony(t, ceremony, before, after, webauthn.MaxTimeout)
			var opts requestOptionsJSON
			decodeOptions(t, ceremony.Options, &opts)
			challenges = append(challenges, checkChallenge(t, opts.Challenge))
			if opts.RPID != suiteRPID {
				t.Errorf("options rpId = %q, want %q", opts.RPID, suiteRPID)
			}
			if len(opts.AllowCredentials) != 0 {
				t.Errorf("options allowCredentials = %+v, want none: every assertion is discoverable", opts.AllowCredentials)
			}
			if want := string(resolved(uv)); opts.UserVerification != want {
				t.Errorf("options userVerification = %q, want %q", opts.UserVerification, want)
			}
			checkTimeout(t, opts.Timeout, webauthn.MaxTimeout)
		})
	}
	if len(challenges) == 4 && len(slices.Compact(slices.Sorted(slices.Values(challenges)))) != 4 {
		t.Errorf("challenges repeat across ceremonies: %q", challenges)
	}
}

func (s suite) testAssertion(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(true)
	stored := enroll(t, rp, a, user, "")
	count := stored.SignCount
	tests := []struct {
		name      string
		uv        webauthn.Requirement
		verifying bool
		refused   bool
	}{
		{"default with verification", "", true, false},
		{"default without verification", "", false, false},
		{"preferred with verification", webauthn.Preferred, true, false},
		{"preferred without verification", webauthn.Preferred, false, false},
		{"required with verification", webauthn.Required, true, false},
		{"required without verification", webauthn.Required, false, true},
		{"discouraged with verification", webauthn.Discouraged, true, false},
		{"discouraged without verification", webauthn.Discouraged, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count += 10
			a.SetUserVerified(tt.verifying)
			a.SetSignCount(count)
			ceremony := beginAssertion(t, rp, tt.uv)
			got, err := finishAssertion(rp, ceremony.State, mustAssert(t, a, ceremony.Options), stored, user.Handle)
			if tt.refused {
				wantVerificationRequired(t, "FinishAssertion", err, got)
				return
			}
			if err != nil {
				t.Fatalf("FinishAssertion = %v, want nil", err)
			}
			want := webauthn.Assertion{SignCount: count, UserVerified: tt.verifying}
			if got != want {
				t.Errorf("FinishAssertion = %+v, want %+v", got, want)
			}
			stored.SignCount = got.SignCount
		})
	}
}

func (s suite) testVerifiedRegistrationUnverifiedAssertion(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(true)
	stored := enroll(t, rp, a, user, webauthn.Preferred)
	if !stored.UserVerified {
		t.Fatal("Credential.UserVerified = false for a verified registration")
	}
	a.SetUserVerified(false)

	ceremony := beginAssertion(t, rp, webauthn.Preferred)
	got, err := finishAssertion(rp, ceremony.State, mustAssert(t, a, ceremony.Options), stored, user.Handle)
	if err != nil {
		t.Fatalf("FinishAssertion(preferred, no verification) = %v, want nil", err)
	}
	if got.UserVerified {
		t.Error("Assertion.UserVerified = true: it must come from THIS ceremony's authenticator data, never the stored record")
	}

	ceremony = beginAssertion(t, rp, webauthn.Required)
	got, err = finishAssertion(rp, ceremony.State, mustAssert(t, a, ceremony.Options), stored, user.Handle)
	wantVerificationRequired(t, "FinishAssertion(required, a verified record, no verification now)", err, got)
}

func (s suite) testAssertionCeremonyTruth(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(false)
	a.setBackup(true, false)
	stored := enroll(t, rp, a, user, "")
	if !stored.BackupEligible || stored.BackupState || stored.UserVerified {
		t.Fatalf("Credential UV/BE/BS = %v/%v/%v, want false/true/false", stored.UserVerified, stored.BackupEligible, stored.BackupState)
	}
	assertWith := func(t *testing.T, record webauthn.Credential) (webauthn.Assertion, error) {
		t.Helper()
		ceremony := beginAssertion(t, rp, "")
		return finishAssertion(rp, ceremony.State, mustAssert(t, a, ceremony.Options), record, user.Handle)
	}

	t.Run("backup state, verification and count rise", func(t *testing.T) {
		a.setBackup(true, true)
		a.SetUserVerified(true)
		a.SetSignCount(10)
		record := stored
		record.SignCount = 3
		got, err := assertWith(t, record)
		want := webauthn.Assertion{SignCount: 10, UserVerified: true, BackupEligible: true, BackupState: true}
		if err != nil || got != want {
			t.Errorf("FinishAssertion = %+v, %v; want %+v from this ceremony", got, err, want)
		}
	})
	t.Run("backup state and verification fall", func(t *testing.T) {
		a.setBackup(true, false)
		a.SetUserVerified(false)
		a.SetSignCount(20)
		record := stored
		record.SignCount, record.BackupState, record.UserVerified = 10, true, true
		got, err := assertWith(t, record)
		want := webauthn.Assertion{SignCount: 20, BackupEligible: true}
		if err != nil || got != want {
			t.Errorf("FinishAssertion = %+v, %v; want %+v from this ceremony", got, err, want)
		}
	})
	t.Run("backup eligibility vanished", func(t *testing.T) {
		a.setBackup(false, false)
		got, err := assertWith(t, stored)
		wantRejected(t, "FinishAssertion", err, got)
	})
	t.Run("backup eligibility appeared", func(t *testing.T) {
		a.setBackup(true, false)
		record := stored
		record.BackupEligible = false
		got, err := assertWith(t, record)
		wantRejected(t, "FinishAssertion", err, got)
	})
	t.Run("backup state without eligibility", func(t *testing.T) {
		a.setBackup(false, true)
		record := stored
		record.BackupEligible = false
		got, err := assertWith(t, record)
		wantRejected(t, "FinishAssertion", err, got)
	})
}

func (s suite) testReadAssertion(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(true)
	stored := enroll(t, rp, a, user, "")
	response := mustAssert(t, a, beginAssertion(t, rp, "").Options)

	got, err := rp.ReadAssertion(response)
	if err != nil {
		t.Fatalf("ReadAssertion = %v, want nil", err)
	}
	if !bytes.Equal(got.CredentialID, stored.ID) || !bytes.Equal(got.UserHandle, user.Handle) {
		t.Errorf("ReadAssertion = %x/%x, want the credential id %x and user handle %x", got.CredentialID, got.UserHandle, stored.ID, user.Handle)
	}

	for name, malformed := range map[string][]byte{
		"nil":          nil,
		"empty object": []byte(`{}`),
		"not JSON":     []byte("not json"),
		"truncated":    response[:len(response)/2],
		"id differs from rawId": tamper(t, response, func(v *authenticationResponseJSON) {
			v.ID = b64.EncodeToString(randomBytes(32))
		}),
	} {
		got, err := rp.ReadAssertion(malformed)
		wantRejected(t, "ReadAssertion("+name+")", err, got)
	}
}

func (s suite) testTwoCredentialsOneUser(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	phone, key := newAuthenticator(true), newAuthenticator(true)
	first := enroll(t, rp, phone, user, "")

	ceremony := beginRegistration(t, rp, webauthn.RegistrationStart{User: user, Exclude: [][]byte{first.ID}})
	if _, err := phone.Register(ceremony.Options); !errors.Is(err, ErrCredentialExcluded) {
		t.Errorf("an authenticator holding an excluded credential registered again (%v): the options must carry RegistrationStart.Exclude", err)
	}
	second, err := finishRegistration(rp, ceremony.State, mustRegister(t, key, ceremony.Options))
	if err != nil {
		t.Fatalf("FinishRegistration(second credential) = %v, want nil", err)
	}
	if bytes.Equal(first.ID, second.ID) {
		t.Fatal("two authenticators produced one credential id")
	}

	for name, pair := range map[string]struct {
		a      *Authenticator
		record webauthn.Credential
	}{"first": {phone, first}, "second": {key, second}} {
		ceremony := beginAssertion(t, rp, webauthn.Required)
		got, err := finishAssertion(rp, ceremony.State, mustAssert(t, pair.a, ceremony.Options), pair.record, user.Handle)
		if err != nil || !got.UserVerified {
			t.Errorf("FinishAssertion(%s credential) = %+v, %v; want a verified assertion", name, got, err)
		}
	}
	ceremony = beginAssertion(t, rp, "")
	got, err := finishAssertion(rp, ceremony.State, mustAssert(t, phone, ceremony.Options), second, user.Handle)
	wantRejected(t, "FinishAssertion(the first credential's response, the second's record)", err, got)
}

func (s suite) testOriginAdmission(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(true)
	stored := enroll(t, rp, a, user, "")
	register := func(t *testing.T, origin string) (webauthn.Credential, error) {
		t.Helper()
		b := newAuthenticator(true)
		b.SetOrigin(origin)
		ceremony := beginRegistration(t, rp, webauthn.RegistrationStart{User: newUser(t)})
		return finishRegistration(rp, ceremony.State, mustRegister(t, b, ceremony.Options))
	}
	assert := func(t *testing.T, origin string) (webauthn.Assertion, error) {
		t.Helper()
		a.SetOrigin(origin)
		a.SetSignCount(stored.SignCount + 1)
		ceremony := beginAssertion(t, rp, "")
		return finishAssertion(rp, ceremony.State, mustAssert(t, a, ceremony.Options), stored, user.Handle)
	}
	for _, origin := range []string{suiteOrigin, suiteLoginOrigin, suiteTenantOrigin} {
		t.Run("admits "+origin, func(t *testing.T) {
			if _, err := register(t, origin); err != nil {
				t.Errorf("FinishRegistration = %v, want nil", err)
			}
			if _, err := assert(t, origin); err != nil {
				t.Errorf("FinishAssertion = %v, want nil", err)
			}
		})
	}
	for _, origin := range []string{
		foreignOrigin, "https://other.example.com", "http://example.com", "https://example.com:8443",
		"https://a.b.tenants.example.com", "https://tenants.example.com", "https://example.com.", "null", "",
	} {
		t.Run("refuses "+origin, func(t *testing.T) {
			cred, err := register(t, origin)
			wantRejected(t, "FinishRegistration", err, cred)
			got, err := assert(t, origin)
			wantRejected(t, "FinishAssertion", err, got)
		})
	}
}

func (s suite) testAssertionRejections(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user, other := newUser(t), newUser(t)
	a, b := newAuthenticator(true), newAuthenticator(true)
	stored := enroll(t, rp, a, user, "")
	otherStored := enroll(t, rp, b, other, "")

	fresh := func(t *testing.T, f faults) (webauthn.Ceremony, []byte) {
		t.Helper()
		ceremony := beginAssertion(t, rp, "")
		a.SetSignCount(stored.SignCount + 1)
		response, err := a.assert(ceremony.Options, f)
		if err != nil {
			t.Fatalf("Authenticator.Assert: %v", err)
		}
		return ceremony, response
	}
	finish := func(edit func(t *testing.T, state, response *[]byte, record *webauthn.Credential, handle *[]byte), f faults) func(t *testing.T) (webauthn.Assertion, error) {
		return func(t *testing.T) (webauthn.Assertion, error) {
			ceremony, response := fresh(t, f)
			state, record, handle := ceremony.State, stored, user.Handle
			if edit != nil {
				edit(t, &state, &response, &record, &handle)
			}
			return finishAssertion(rp, state, response, record, handle)
		}
	}
	editResponse := func(edit func(*authenticationResponseJSON)) func(t *testing.T, _, response *[]byte, _ *webauthn.Credential, _ *[]byte) {
		return func(t *testing.T, _, response *[]byte, _ *webauthn.Credential, _ *[]byte) {
			*response = tamper(t, *response, edit)
		}
	}
	replaceResponse := func(with func(t *testing.T, response []byte) []byte) func(t *testing.T, _, response *[]byte, _ *webauthn.Credential, _ *[]byte) {
		return func(t *testing.T, _, response *[]byte, _ *webauthn.Credential, _ *[]byte) {
			*response = with(t, *response)
		}
	}
	replaceState := func(with func(t *testing.T) []byte) func(t *testing.T, state, _ *[]byte, _ *webauthn.Credential, _ *[]byte) {
		return func(t *testing.T, state, _ *[]byte, _ *webauthn.Credential, _ *[]byte) {
			*state = with(t)
		}
	}

	cases := []rejection[webauthn.Assertion]{
		{"signature", finish(editResponse(func(v *authenticationResponseJSON) {
			sig, _ := b64.DecodeString(v.Response.Signature)
			sig[len(sig)-1] ^= 0x01
			v.Response.Signature = b64.EncodeToString(sig)
		}), faults{})},
		{"foreign state", finish(replaceState(func(t *testing.T) []byte { return beginAssertion(t, rp, "").State }), faults{})},
		{"registration state", finish(replaceState(func(t *testing.T) []byte {
			return beginRegistration(t, rp, webauthn.RegistrationStart{User: user}).State
		}), faults{})},
		{"registration state whose challenge the response carries", func(t *testing.T) (webauthn.Assertion, error) {
			registration := beginRegistration(t, rp, webauthn.RegistrationStart{User: user})
			options := withChallenge(t, beginAssertion(t, rp, "").Options, registration.Options)
			return finishAssertion(rp, registration.State, mustAssert(t, a, options), stored, user.Handle)
		}},
		{"garbage state", finish(replaceState(func(*testing.T) []byte { return []byte("not a ceremony state") }), faults{})},
		{"empty state", finish(replaceState(func(*testing.T) []byte { return nil }), faults{})},
		{"rpIdHash of another RP ID", finish(nil, faults{rpID: foreignRPID})},
		{"clientDataJSON type webauthn.create", finish(nil, faults{clientDataType: createType})},
		{"user presence absent", finish(nil, faults{withoutPresence: true})},
		{"owner handle differs", finish(func(_ *testing.T, _, _ *[]byte, _ *webauthn.Credential, handle *[]byte) {
			*handle = other.Handle
		}, faults{})},
		{"response user handle differs", finish(editResponse(func(v *authenticationResponseJSON) {
			v.Response.UserHandle = b64.EncodeToString(other.Handle)
		}), faults{})},
		{"response user handle missing", finish(editResponse(func(v *authenticationResponseJSON) {
			v.Response.UserHandle = ""
		}), faults{})},
		{"record names another credential", finish(func(_ *testing.T, _, _ *[]byte, record *webauthn.Credential, _ *[]byte) {
			*record = otherStored
		}, faults{})},
		{"record carries another public key", finish(func(_ *testing.T, _, _ *[]byte, record *webauthn.Credential, _ *[]byte) {
			record.PublicKey = otherStored.PublicKey
		}, faults{})},
		{"record carries this key under another id", finish(func(_ *testing.T, _, _ *[]byte, record *webauthn.Credential, _ *[]byte) {
			record.ID = otherStored.ID
		}, faults{})},
		{"id differs from rawId", finish(editResponse(func(v *authenticationResponseJSON) {
			v.ID = b64.EncodeToString(otherStored.ID)
		}), faults{})},
		{"nil response", finish(replaceResponse(func(*testing.T, []byte) []byte { return nil }), faults{})},
		{"empty object", finish(replaceResponse(func(*testing.T, []byte) []byte { return []byte(`{}`) }), faults{})},
		{"not JSON", finish(replaceResponse(func(*testing.T, []byte) []byte { return []byte("not json") }), faults{})},
		{"truncated", finish(replaceResponse(func(_ *testing.T, r []byte) []byte { return r[:len(r)/2] }), faults{})},
		{"a registration response", finish(replaceResponse(func(t *testing.T, _ []byte) []byte {
			return mustRegister(t, newAuthenticator(true), beginRegistration(t, rp, webauthn.RegistrationStart{User: newUser(t)}).Options)
		}), faults{})},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.run(t)
			wantRejected(t, "FinishAssertion", err, got)
		})
	}

	ceremony, response := fresh(t, faults{})
	if _, err := finishAssertion(rp, ceremony.State, response, stored, user.Handle); err != nil {
		t.Errorf("FinishAssertion(the faithful control) = %v, want nil", err)
	}
}

func (s suite) testAssertionIgnoresExtensionOutputs(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(true)
	stored := enroll(t, rp, a, user, "")
	a.SetExtensionOutputs(map[string]any{"credProtect": uint64(2), "webauthntest-unsolicited": map[string]any{"set": true}})
	ceremony := beginAssertion(t, rp, "")
	got, err := finishAssertion(rp, ceremony.State, mustAssert(t, a, ceremony.Options), stored, user.Handle)
	if err != nil || !got.UserVerified {
		t.Errorf("FinishAssertion(unsolicited extension outputs) = %+v, %v; want them ignored", got, err)
	}
}

func (s suite) testCounterRegression(t *testing.T) {
	rp := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(true)
	stored := enroll(t, rp, a, user, "")
	for _, tt := range []struct {
		stored, presented uint32
		want              bool
	}{
		{0, 0, false},
		{0, 5, false},
		{5, 6, false},
		{5, 5, true},
		{5, 4, true},
		{5, 0, true},
	} {
		record := stored
		record.SignCount = tt.stored
		a.SetSignCount(tt.presented)
		ceremony := beginAssertion(t, rp, "")
		got, err := finishAssertion(rp, ceremony.State, mustAssert(t, a, ceremony.Options), record, user.Handle)
		if err != nil {
			t.Errorf("FinishAssertion(stored %d, presented %d) = %v; a regression is reported, not an error", tt.stored, tt.presented, err)
			continue
		}
		if got.CounterRegressed != tt.want || got.SignCount != tt.presented {
			t.Errorf("FinishAssertion(stored %d, presented %d) = CounterRegressed %v SignCount %d; want %v, %d",
				tt.stored, tt.presented, got.CounterRegressed, got.SignCount, tt.want, tt.presented)
		}
	}
}

func (s suite) testExpiry(t *testing.T) {
	cfg := suiteConfig(t)
	cfg.Timeout = expiringTimeout
	short := s.relyingParty(t, cfg)
	long := s.relyingParty(t, suiteConfig(t))
	user := newUser(t)
	a := newAuthenticator(true)
	stored := enroll(t, long, a, user, "")

	ceremony := beginAssertion(t, short, "")
	var opts requestOptionsJSON
	decodeOptions(t, ceremony.Options, &opts)
	checkTimeout(t, opts.Timeout, expiringTimeout)
	if until := time.Until(ceremony.ExpiresAt); until > expiringTimeout+time.Second {
		t.Fatalf("Ceremony.ExpiresAt is %v away under a %v Timeout", until, expiringTimeout)
	}
	if _, err := finishAssertion(short, ceremony.State, mustAssert(t, a, ceremony.Options), stored, user.Handle); err != nil {
		t.Fatalf("FinishAssertion inside the Timeout = %v, want nil", err)
	}

	registration := beginRegistration(t, short, webauthn.RegistrationStart{User: newUser(t)})
	registrationResponse := mustRegister(t, newAuthenticator(true), registration.Options)
	assertion := beginAssertion(t, short, "")
	assertionResponse := mustAssert(t, a, assertion.Options)
	time.Sleep(time.Until(assertion.ExpiresAt) + 100*time.Millisecond)

	cred, err := finishRegistration(short, registration.State, registrationResponse)
	wantRejected(t, "FinishRegistration after ExpiresAt", err, cred)
	got, err := finishAssertion(short, assertion.State, assertionResponse, stored, user.Handle)
	wantRejected(t, "FinishAssertion after ExpiresAt", err, got)
}

func suiteConfig(t *testing.T) webauthn.Config {
	t.Helper()
	return webauthn.Config{
		RPID:    suiteRPID,
		RPName:  suiteRPName,
		Origins: policy(t, []string{suiteOrigin, suiteLoginOrigin}, suiteTenants),
	}
}

func policy(t *testing.T, exact []string, namespaces ...string) web.OriginPolicy {
	t.Helper()
	cfg := web.OriginPolicyConfig{Exact: exact}
	for _, pattern := range namespaces {
		ns, err := web.ParseOriginNamespace(pattern)
		if err != nil {
			t.Fatalf("ParseOriginNamespace(%q): %v", pattern, err)
		}
		cfg.Namespaces = append(cfg.Namespaces, ns)
	}
	p, err := web.NewOriginPolicy(cfg)
	if err != nil {
		t.Fatalf("NewOriginPolicy: %v", err)
	}
	return p
}

func (s suite) relyingParty(t *testing.T, cfg webauthn.Config) webauthn.RelyingParty {
	t.Helper()
	rp, err := s.newRelyingParty(cfg)
	if err != nil {
		t.Fatalf("factory(RPID %q) = %v, want a relying party", cfg.RPID, err)
	}
	if rp == nil {
		t.Fatal("factory returned a nil relying party")
	}
	return rp
}

func newAuthenticator(verifying bool) *Authenticator {
	return NewAuthenticator(AuthenticatorConfig{RPID: suiteRPID, Origin: suiteOrigin, UserVerifying: verifying})
}

func newUser(t *testing.T) webauthn.User {
	t.Helper()
	handle := randomBytes(32)
	tag := hex.EncodeToString(handle[:4])
	return webauthn.User{Handle: handle, Name: "user-" + tag + "@example.com", DisplayName: "User " + tag}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// enroll registers a credential for user on a and returns it.
func enroll(t *testing.T, rp webauthn.RelyingParty, a *Authenticator, user webauthn.User, uv webauthn.Requirement) webauthn.Credential {
	t.Helper()
	ceremony := beginRegistration(t, rp, webauthn.RegistrationStart{User: user, UserVerification: uv})
	cred, err := finishRegistration(rp, ceremony.State, mustRegister(t, a, ceremony.Options))
	if err != nil {
		t.Fatalf("FinishRegistration = %v, want nil", err)
	}
	return cred
}

func beginRegistration(t *testing.T, rp webauthn.RelyingParty, in webauthn.RegistrationStart) webauthn.Ceremony {
	t.Helper()
	ceremony, err := rp.BeginRegistration(context.Background(), in)
	if err != nil {
		t.Fatalf("BeginRegistration = %v, want nil", err)
	}
	return ceremony
}

func beginAssertion(t *testing.T, rp webauthn.RelyingParty, uv webauthn.Requirement) webauthn.Ceremony {
	t.Helper()
	ceremony, err := rp.BeginAssertion(context.Background(), webauthn.AssertionStart{UserVerification: uv})
	if err != nil {
		t.Fatalf("BeginAssertion = %v, want nil", err)
	}
	return ceremony
}

func finishRegistration(rp webauthn.RelyingParty, state, response []byte) (webauthn.Credential, error) {
	return rp.FinishRegistration(context.Background(), webauthn.RegistrationFinish{State: state, Response: response})
}

func finishAssertion(rp webauthn.RelyingParty, state, response []byte, record webauthn.Credential, handle []byte) (webauthn.Assertion, error) {
	return rp.FinishAssertion(context.Background(), webauthn.AssertionFinish{State: state, Response: response, Credential: record, UserHandle: handle})
}

func mustRegister(t *testing.T, a *Authenticator, options json.RawMessage) []byte {
	t.Helper()
	response, err := a.Register(options)
	if err != nil {
		t.Fatalf("Authenticator.Register: %v", err)
	}
	return response
}

func mustAssert(t *testing.T, a *Authenticator, options json.RawMessage) []byte {
	t.Helper()
	response, err := a.Assert(options)
	if err != nil {
		t.Fatalf("Authenticator.Assert: %v", err)
	}
	return response
}

// tamper decodes a response, edits it, and re-encodes it.
func tamper[T any](t *testing.T, response []byte, edit func(*T)) []byte {
	t.Helper()
	var v T
	if err := json.Unmarshal(response, &v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	edit(&v)
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	return out
}

// withChallenge returns options carrying the challenge of from, so a response
// to it answers another ceremony's challenge.
func withChallenge(t *testing.T, options, from json.RawMessage) json.RawMessage {
	t.Helper()
	var members, source map[string]json.RawMessage
	if err := json.Unmarshal(options, &members); err != nil {
		t.Fatalf("Ceremony.Options: %v", err)
	}
	if err := json.Unmarshal(from, &source); err != nil {
		t.Fatalf("Ceremony.Options: %v", err)
	}
	members["challenge"] = source["challenge"]
	out, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeOptions(t *testing.T, options json.RawMessage, v any) {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(options, &members); err != nil {
		t.Fatalf("Ceremony.Options is not a JSON object: %v", err)
	}
	if _, wrapped := members["publicKey"]; wrapped {
		t.Fatal("Ceremony.Options wraps a publicKey member; want the inner options JSON")
	}
	if err := json.Unmarshal(options, v); err != nil {
		t.Fatalf("Ceremony.Options: %v", err)
	}
}

func checkCeremony(t *testing.T, ceremony webauthn.Ceremony, before, after time.Time, timeout time.Duration) {
	t.Helper()
	if len(ceremony.State) == 0 {
		t.Error("Ceremony.State is empty")
	}
	if ceremony.ExpiresAt.Before(before.Add(timeout-time.Second)) || ceremony.ExpiresAt.After(after.Add(timeout+time.Second)) {
		t.Errorf("Ceremony.ExpiresAt = %v, want Begin's time plus %v", ceremony.ExpiresAt, timeout)
	}
}

func checkChallenge(t *testing.T, challenge string) string {
	t.Helper()
	raw, err := b64.DecodeString(challenge)
	if err != nil || len(raw) < 16 {
		t.Errorf("options challenge %q is not at least 16 bytes of unpadded base64url", challenge)
	}
	return challenge
}

func checkTimeout(t *testing.T, got *int64, want time.Duration) {
	t.Helper()
	if got == nil || *got != want.Milliseconds() {
		t.Errorf("options timeout = %v, want %d ms", got, want.Milliseconds())
	}
}

func resolved(uv webauthn.Requirement) webauthn.Requirement {
	if uv == "" {
		return webauthn.Preferred
	}
	return uv
}

func requirementName(uv webauthn.Requirement) string {
	if uv == "" {
		return "default"
	}
	return string(uv)
}

func wantRejected(t *testing.T, what string, err error, result any) {
	t.Helper()
	if !errors.Is(err, webauthn.ErrCeremonyRejected) || !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("%s error = %v, want webauthn.ErrCeremonyRejected", what, err)
	} else if errors.Is(err, webauthn.ErrUserVerificationRequired) {
		t.Errorf("%s error = %v, unexpectedly webauthn.ErrUserVerificationRequired", what, err)
	}
	if !reflect.ValueOf(result).IsZero() {
		t.Errorf("%s = %+v beside an error, want the zero value", what, result)
	}
}

func wantVerificationRequired(t *testing.T, what string, err error, result any) {
	t.Helper()
	if !errors.Is(err, webauthn.ErrUserVerificationRequired) || !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("%s error = %v, want webauthn.ErrUserVerificationRequired", what, err)
	}
	if !reflect.ValueOf(result).IsZero() {
		t.Errorf("%s = %+v beside an error, want the zero value", what, result)
	}
}
