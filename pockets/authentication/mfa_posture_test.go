package authentication

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// mfaPostureProvider verifies every token as one AAL1 identity under issuer.
type mfaPostureProvider struct{ issuer string }

func (p mfaPostureProvider) Issuer() string { return p.issuer }
func (p mfaPostureProvider) Authenticate(context.Context, sdkauth.Credential) (sdkauth.Identity, error) {
	return sdkauth.Identity{Issuer: p.issuer, Subject: "s1", Methods: []string{"pwd"}}, nil
}

// mfaWiring is a production-valid wiring whose single-factor sources and MFA
// requirement each test chooses: external providers (none when nil), password
// flows, an OAuth provider, a UserAdminCheck and the acknowledgements.
type mfaWiring struct {
	required    MFARequirement
	providers   []authlogic.ExternalProvider
	password    bool
	oauth       bool
	adminCheck  bool
	ack         bool
	externalAck bool
	// passkeys wires a relying party and the stores it requires; protector is
	// the challenge rail's protector it needs.
	passkeys  webauthn.RelyingParty
	protector protection.ChallengeProtector
	// totp wires TOTP over totpRepo (stubTOTP when nil); methods is
	// MFAConfig.Methods.
	totp     bool
	totpRepo totp.Repository
	methods  []MFAMethod
}

func idp(name, issuer string, requireMFA bool) authlogic.ExternalProvider {
	return authlogic.ExternalProvider{Name: name, Provider: mfaPostureProvider{issuer: issuer}, RequireMFA: requireMFA}
}

func (w mfaWiring) build(mode environment.Mode) (*Components, string, error) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	repos := Repositories{SecurityEvents: stubSecurityEvents{}}
	opts := []Option{
		WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: !w.password}),
		WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}, SingleFactorAcknowledged: w.ack, ChallengeProtector: w.protector}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
		WithMFA(MFAConfig{Required: w.required, Methods: w.methods}),
		WithLogger(log),
	}
	if w.providers != nil {
		repos.ExternalSubjects = cookieTestSubjects{}
		opts = append(opts, WithExternal(authlogic.ExternalConfig{Providers: w.providers, SingleFactorAcknowledged: w.externalAck}))
	}
	if w.oauth {
		repos.OAuthAccounts, repos.OAuthStates = stubOAuthAccounts{}, stubOAuthStates{}
		opts = append(opts,
			WithOAuth(OAuthConfig{Providers: []oauth.Provider{stubProvider{}}, OAuthCallbackBase: "https://app.example.com"}),
			WithLinks(LinksConfig{OAuthLinkBaseURL: "https://app.example.com/auth/oauth/link"}))
	}
	if w.passkeys != nil {
		repos.Passkeys, repos.CredentialMutations, repos.AuthenticationGrants, repos.Challenges = stubPasskeys{}, stubCredentialMutations{}, stubGrants{}, stubChallenges{}
		opts = append(opts, WithPasskeys(PasskeyConfig{RelyingParty: w.passkeys}))
	}
	if w.totp {
		repos.TOTP = w.totpRepo
		if repos.TOTP == nil {
			repos.TOTP = stubTOTP{}
		}
		repos.CredentialMutations, repos.AuthenticationGrants = stubCredentialMutations{}, stubGrants{}
		if repos.Challenges == nil {
			repos.Challenges = stubChallenges{}
		}
		opts = append(opts, WithTOTP(TOTPConfig{Encrypter: testTOTPEncrypter(), Issuer: "Example"}))
	}
	if w.adminCheck {
		repos.UserAdmin = stubUserAdmin{}
		opts = append(opts, WithAdministration(AdministrationConfig{UserAdminCheck: func(context.Context, inbound.UserAdminCheckRequest) error { return nil }}))
	}
	c, err := New(testRepositories(repos), stubSigner{}, mode, delivery.ModeOff, opts...)
	return c, buf.String(), err
}

// postureToken is a JWT-shaped token naming iss; mfaPostureProvider accepts it.
func postureToken(iss string) string {
	body, _ := json.Marshal(map[string]any{"iss": iss, "sub": "s1"})
	return "e30." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

// TestMFARequirementValidation proves every unsatisfiable or unknown
// requirement fails construction in both modes, naming the field to change.
func TestMFARequirementValidation(t *testing.T) {
	one := []authlogic.ExternalProvider{idp("idp", "https://idp.example.test", false)}
	cases := []struct {
		name   string
		wiring mfaWiring
		want   error
		names  string
	}{
		{"unknown requirement", mfaWiring{required: "most", providers: one}, sdk.ErrInvalidInput, "MFAConfig.Required"},
		{"admins without External", mfaWiring{required: MFARequiredAdmins, adminCheck: true}, ErrMFAUnsatisfiable, "WithExternal"},
		{"all without External", mfaWiring{required: MFARequiredAll}, ErrMFAUnsatisfiable, "WithExternal"},
		{"admins without a check", mfaWiring{required: MFARequiredAdmins, providers: one}, ErrMFAUnsatisfiable, "AdministrationConfig.UserAdminCheck"},
		{"all beside password flows", mfaWiring{required: MFARequiredAll, providers: one, password: true}, ErrMFAUnsatisfiable, "PasswordConfig.PasswordFlowsDisabled"},
		{"all beside OAuth", mfaWiring{required: MFARequiredAll, providers: one, oauth: true}, ErrMFAUnsatisfiable, "OAuthConfig.Providers"},
		{"standalone acknowledgement on New", mfaWiring{providers: one, externalAck: true, ack: true}, sdk.ErrInvalidInput, "IdentityConfig.SingleFactorAcknowledged"},
	}
	for _, mode := range []environment.Mode{environment.ModeDevelopment, environment.ModeProduction} {
		for _, tc := range cases {
			_, _, err := tc.wiring.build(mode)
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.names) {
				t.Errorf("%s/%s: err = %v, want %v naming %s", mode, tc.name, err, tc.want, tc.names)
			}
		}
	}
}

// TestAllRefusesPasswordless proves R-3 for the passwordless source: no native
// second factor exists, so all beside a Passwordless kind cannot be met.
func TestAllRefusesPasswordless(t *testing.T) {
	_, err := New(testRepositories(passwordlessDevRepos()), stubSigner{}, environment.ModeDevelopment, delivery.ModeOff,
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithPasswordless(PasswordlessConfig{Passwordless: []string{"email"}}),
		WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{idp("idp", "https://idp.example.test", false)}}),
		WithMFA(MFAConfig{Required: MFARequiredAll}))
	if !errors.Is(err, ErrMFAUnsatisfiable) || !strings.Contains(err.Error(), "PasswordlessConfig.Passwordless") {
		t.Fatalf("all beside passwordless = %v, want ErrMFAUnsatisfiable naming PasswordlessConfig.Passwordless", err)
	}
}

// TestProductionSingleFactorCauses is the cause table: each login source that
// admits AAL1 sign-in trips the refusal naming it, and a declared posture
// clears the external cause.
func TestProductionSingleFactorCauses(t *testing.T) {
	one := []authlogic.ExternalProvider{idp("idp", "https://idp.example.test", false)}
	refused := []struct {
		name   string
		wiring mfaWiring
		causes string
	}{
		{"external, Required none", mfaWiring{providers: one}, "[external_no_mfa]"},
		{"one of two providers without RequireMFA", mfaWiring{providers: []authlogic.ExternalProvider{idp("a", "https://a.example.test", true), idp("b", "https://b.example.test", false)}}, "[external_no_mfa]"},
		{"native OAuth", mfaWiring{oauth: true}, "[native_oauth]"},
		{"password beside external", mfaWiring{password: true, providers: one}, "[native_password external_no_mfa]"},
		{"password beside admins", mfaWiring{password: true, providers: one, required: MFARequiredAdmins, adminCheck: true}, "[native_password]"},
	}
	for _, tc := range refused {
		_, _, err := tc.wiring.build(environment.ModeProduction)
		if !errors.Is(err, ErrSingleFactorUnacknowledged) || !strings.Contains(err.Error(), "causes "+tc.causes) || !strings.Contains(err.Error(), "IdentityConfig.SingleFactorAcknowledged") {
			t.Errorf("%s: err = %v, want ErrSingleFactorUnacknowledged with causes %s", tc.name, err, tc.causes)
		}
	}

	booted := []struct {
		name   string
		wiring mfaWiring
	}{
		{"admins with a check", mfaWiring{providers: one, required: MFARequiredAdmins, adminCheck: true}},
		{"all", mfaWiring{providers: one, required: MFARequiredAll}},
		{"RequireMFA on every provider", mfaWiring{providers: []authlogic.ExternalProvider{idp("idp", "https://idp.example.test", true)}}},
	}
	for _, tc := range booted {
		_, logs, err := tc.wiring.build(environment.ModeProduction)
		if err != nil || strings.Contains(logs, "SingleFactorAcknowledged") {
			t.Errorf("%s: err = %v, logs:\n%s", tc.name, err, logs)
		}
	}

	_, logs, err := mfaWiring{oauth: true, ack: true}.build(environment.ModeProduction)
	if err != nil || strings.Count(logs, "posture_waiver=SingleFactorAcknowledged") != 1 || !strings.Contains(logs, "causes=[native_oauth]") {
		t.Fatalf("acknowledged OAuth: err = %v, logs:\n%s", err, logs)
	}
	_, logs, err = mfaWiring{password: true, providers: one, ack: true}.build(environment.ModeProduction)
	if err != nil || !strings.Contains(logs, `posture_waiver=SingleFactorAcknowledged runtime_mode=production causes="[native_password external_no_mfa]"`) {
		t.Fatalf("acknowledged password beside external: err = %v, logs:\n%s", err, logs)
	}
}

// TestDevelopmentSingleFactorWarnsOnce proves the pre-deploy signal: one WARN
// naming the acknowledgement and the causes for every shape production would
// refuse, and none for a declared posture.
func TestDevelopmentSingleFactorWarnsOnce(t *testing.T) {
	one := []authlogic.ExternalProvider{idp("idp", "https://idp.example.test", false)}
	for name, tc := range map[string]struct {
		wiring mfaWiring
		causes string
	}{
		"external":   {mfaWiring{providers: one}, "causes=[external_no_mfa]"},
		"OAuth":      {mfaWiring{oauth: true}, "causes=[native_oauth]"},
		"password":   {mfaWiring{password: true}, "causes=[native_password]"},
		"declared":   {mfaWiring{providers: one, required: MFARequiredAdmins, adminCheck: true}, ""},
		"no sources": {mfaWiring{}, ""},
	} {
		_, logs, err := tc.wiring.build(environment.ModeDevelopment)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := 0
		if tc.causes != "" {
			want = 1
		}
		if n := strings.Count(logs, "IdentityConfig.SingleFactorAcknowledged"); n != want || (tc.causes != "" && !strings.Contains(logs, tc.causes)) || strings.Contains(logs, "posture_waiver") {
			t.Errorf("%s: WARN count %d, want %d with %s:\n%s", name, n, want, tc.causes, logs)
		}
	}
}

// TestMFARequiredAllClonesProviders proves all sets RequireMFA on copies: the
// host's slice is untouched and the engine denies an AAL1 token.
func TestMFARequiredAllClonesProviders(t *testing.T) {
	providers := []authlogic.ExternalProvider{idp("a", "https://a.example.test", false), idp("b", "https://b.example.test", false)}
	c, logs, err := mfaWiring{providers: providers, required: MFARequiredAll}.build(environment.ModeProduction)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range providers {
		if p.RequireMFA {
			t.Fatalf("New wrote RequireMFA into the host's provider %q", p.Name)
		}
	}
	for _, issuer := range []string{"https://a.example.test", "https://b.example.test"} {
		if _, _, err := c.External.Verify(context.Background(), postureToken(issuer), authlogic.TransportHeader); !errors.Is(err, ErrMFARequired) {
			t.Fatalf("%s AAL1 token under all = %v, want ErrMFARequired", issuer, err)
		}
	}
	if !strings.Contains(logs, "auth: mfa posture") || !strings.Contains(logs, "mfa_required=all source=config gates=external_tokens") {
		t.Fatalf("boot line missing:\n%s", logs)
	}
}

// TestMFAPostureBootLine pins the one boot line per requirement.
func TestMFAPostureBootLine(t *testing.T) {
	one := []authlogic.ExternalProvider{idp("idp", "https://idp.example.test", false)}
	for want, wiring := range map[string]mfaWiring{
		"mfa_required=none source=default gates=none":                {providers: one, ack: true},
		"mfa_required=none source=config gates=none":                 {providers: one, ack: true, required: MFARequiredNone},
		"mfa_required=admins source=config gates=/auth/admin/users*": {providers: one, required: MFARequiredAdmins, adminCheck: true},
	} {
		_, logs, err := wiring.build(environment.ModeDevelopment)
		if err != nil || strings.Count(logs, "auth: mfa posture") != 1 || !strings.Contains(logs, want) {
			t.Errorf("boot line %q: err = %v, logs:\n%s", want, err, logs)
		}
	}
}

// TestSingleFactorSentinelIsShared proves one errors.Is target serves both
// constructors.
func TestSingleFactorSentinelIsShared(t *testing.T) {
	if ErrSingleFactorUnacknowledged != authlogic.ErrSingleFactorUnacknowledged || ErrMFARequired != authlogic.ErrMFARequired {
		t.Fatal("the root re-exports drifted from the logic sentinels")
	}
	if !errors.Is(ErrMFARequired, sdk.ErrUnauthorized) {
		t.Fatal("ErrMFARequired does not match sdk.ErrUnauthorized")
	}
}
