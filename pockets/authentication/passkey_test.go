package authentication

import (
	"context"
	"errors"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/webauthnstub"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// The construction-only passkey stores: nothing here runs a ceremony.
type (
	stubPasskeys            struct{ passkey.Repository }
	stubCredentialMutations struct{ credential.MutationRepository }
	stubGrants              struct{ authgrant.Repository }
)

// relyingParty builds the stub relying party for rpID over the origin rules.
func relyingParty(t *testing.T, rpID string, exact []string, namespaces ...string) webauthn.RelyingParty {
	t.Helper()
	cfg := web.OriginPolicyConfig{Exact: exact}
	for _, pattern := range namespaces {
		ns, err := web.ParseOriginNamespace(pattern)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Namespaces = append(cfg.Namespaces, ns)
	}
	policy, err := web.NewOriginPolicy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rp, err := webauthnstub.New(webauthn.Config{RPID: rpID, RPName: "Example", Origins: policy})
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

func TestPasskeyConfigValidation(t *testing.T) {
	rp := relyingParty(t, "example.com", []string{"https://example.com"})
	protector := passwordlessProtector(t)
	ok := mfaWiring{ack: true, passkeys: rp, protector: protector}
	if _, _, err := ok.build(environment.ModeDevelopment); err != nil {
		t.Fatalf("a wired relying party: %v", err)
	}
	for name, tc := range map[string]struct {
		opts []Option
		want error
	}{
		"no passkey store":         {[]Option{WithPasskeys(PasskeyConfig{RelyingParty: rp})}, ErrPasskeyReposRequired},
		"views without a party":    {[]Option{WithPasskeys(PasskeyConfig{Views: struct{ inbound.PasskeyViews }{}})}, sdk.ErrInvalidInput},
		"views without page views": {[]Option{WithPasskeys(PasskeyConfig{RelyingParty: rp, Views: struct{ inbound.PasskeyViews }{}})}, sdk.ErrInvalidInput},
	} {
		repos := testRepositories(Repositories{})
		if name != "no passkey store" {
			repos.Passkeys, repos.CredentialMutations, repos.AuthenticationGrants, repos.Challenges = stubPasskeys{}, stubCredentialMutations{}, stubGrants{}, stubChallenges{}
		}
		opts := append([]Option{
			WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: true}),
			WithIdentity(IdentityConfig{ChallengeProtector: protector}),
			WithDelivery(DeliveryConfig{Mailer: stubMailer{}}),
		}, tc.opts...)
		if _, err := New(repos, stubSigner{}, environment.ModeDevelopment, "off", opts...); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	for name, cfg := range map[string]authlogic.PasskeyConfig{
		"unknown user verification": {RelyingParty: rp, UserVerification: "sometimes"},
		"cap above 25":              {RelyingParty: rp, MaxPerUser: 26},
		"negative cap":              {RelyingParty: rp, MaxPerUser: -1},
	} {
		w := ok
		_, _, err := w.buildWith(environment.ModeDevelopment, WithPasskeys(PasskeyConfig{RelyingParty: cfg.RelyingParty, UserVerification: cfg.UserVerification, MaxPerUser: cfg.MaxPerUser}))
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

// probingPasskeys reports its schema through passkey.SchemaProber.
type probingPasskeys struct {
	stubPasskeys
	err    error
	probed *int
}

func (p probingPasskeys) ProbeSchema(context.Context) error {
	*p.probed++
	return p.err
}

// TestPasskeySchemaProbeOnlyWhenWired pins the owner ruling (0025
// conditional): a store that probes its schema is probed at New only when
// WithPasskeys is wired; a missing schema then refuses boot naming 0025, and an
// unwired host boots.
func TestPasskeySchemaProbeOnlyWhenWired(t *testing.T) {
	rp := relyingParty(t, "example.com", []string{"https://example.com"})
	protector := passwordlessProtector(t)
	probed := 0
	missing := probingPasskeys{err: errors.New("relation passkey_credentials does not exist"), probed: &probed}
	newPocket := func(repo passkey.Repository, opts ...Option) error {
		repos := testRepositories(Repositories{})
		repos.Passkeys, repos.CredentialMutations, repos.AuthenticationGrants, repos.Challenges = repo, stubCredentialMutations{}, stubGrants{}, stubChallenges{}
		_, err := New(repos, stubSigner{}, environment.ModeDevelopment, "off", append([]Option{
			WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: true}),
			WithIdentity(IdentityConfig{ChallengeProtector: protector}),
			WithDelivery(DeliveryConfig{Mailer: stubMailer{}}),
		}, opts...)...)
		return err
	}
	err := newPocket(missing, WithPasskeys(PasskeyConfig{RelyingParty: rp}))
	if !errors.Is(err, ErrPasskeySchemaMissing) || !strings.Contains(err.Error(), "0025_passkey_credentials.sql") || probed != 1 {
		t.Fatalf("wired over a missing schema = %v (probed %d)", err, probed)
	}
	if err := newPocket(probingPasskeys{probed: &probed}, WithPasskeys(PasskeyConfig{RelyingParty: rp})); err != nil || probed != 2 {
		t.Fatalf("wired over a migrated schema = %v (probed %d)", err, probed)
	}
	if err := newPocket(missing); err != nil || probed != 2 {
		t.Fatalf("an unwired host over a missing schema = %v (probed %d)", err, probed)
	}
}

func TestPasskeyProductionRelyingParty(t *testing.T) {
	protector := passwordlessProtector(t)
	for name, tc := range map[string]struct {
		rp      webauthn.RelyingParty
		refused bool
	}{
		"a real domain":     {relyingParty(t, "example.com", []string{"https://example.com"}, "https://*.example.com"), false},
		"localhost":         {relyingParty(t, "localhost", []string{"http://localhost:8082"}), true},
		"a localhost label": {relyingParty(t, "app.localhost", []string{"http://app.localhost:8082"}), true},
		"an http namespace": {relyingParty(t, "example.com", []string{"https://example.com"}, "http://*.example.com"), true},
	} {
		_, _, err := mfaWiring{ack: true, passkeys: tc.rp, protector: protector}.build(environment.ModeProduction)
		if got := errors.Is(err, ErrPasskeyDevelopmentRelyingParty); got != tc.refused {
			t.Errorf("%s in production: err = %v, refused %v want %v", name, err, got, tc.refused)
		}
	}
	if _, _, err := (mfaWiring{ack: true, passkeys: relyingParty(t, "localhost", []string{"http://localhost:8082"}), protector: protector}).build(environment.ModeDevelopment); err != nil {
		t.Errorf("localhost in development: %v", err)
	}
}

// TestMFARequiredResolvesOnce pins B-2: the zero Required resolves once, before
// any consumer, to admins beside passkeys and a UserAdminCheck and to none
// otherwise; none is the explicit opt-out; and an IdP host at the zero value
// still trips external_no_mfa.
func TestMFARequiredResolvesOnce(t *testing.T) {
	rp := relyingParty(t, "example.com", []string{"https://example.com"})
	protector := passwordlessProtector(t)
	for want, w := range map[string]mfaWiring{
		"mfa_required=admins source=default gates=/auth/admin/users*,machine_lifecycle": {ack: true, passkeys: rp, protector: protector, adminCheck: true},
		"mfa_required=none source=default gates=none":                                   {ack: true, passkeys: rp, protector: protector},
		"mfa_required=none source=config gates=none":                                    {ack: true, passkeys: rp, protector: protector, adminCheck: true, required: MFARequiredNone},
		"mfa_required=admins source=config gates=/auth/admin/users*,machine_lifecycle":  {ack: true, passkeys: rp, protector: protector, adminCheck: true, required: MFARequiredAdmins},
	} {
		_, logs, err := w.build(environment.ModeDevelopment)
		if err != nil || !strings.Contains(logs, want) {
			t.Errorf("%q: err = %v, logs:\n%s", want, err, logs)
		}
	}

	idp := []authlogic.ExternalProvider{idp("idp", "https://idp.example.test", false)}
	_, _, err := mfaWiring{providers: idp}.build(environment.ModeProduction)
	if !errors.Is(err, ErrSingleFactorUnacknowledged) || !strings.Contains(err.Error(), "external_no_mfa") {
		t.Fatalf("an IdP host at the zero value = %v, want the external_no_mfa refusal", err)
	}

	_, _, err = mfaWiring{ack: true, passkeys: rp, protector: protector, required: MFARequiredAll}.build(environment.ModeDevelopment)
	if !errors.Is(err, ErrMFAUnsatisfiable) || !strings.Contains(err.Error(), "s4c2-native-all") {
		t.Fatalf("native all = %v, want ErrMFAUnsatisfiable naming s4c2-native-all", err)
	}
}

// buildWith is build with extra options appended.
func (w mfaWiring) buildWith(mode environment.Mode, extra ...Option) (*Components, string, error) {
	repos := testRepositories(Repositories{SecurityEvents: stubSecurityEvents{}})
	repos.Passkeys, repos.CredentialMutations, repos.AuthenticationGrants, repos.Challenges = stubPasskeys{}, stubCredentialMutations{}, stubGrants{}, stubChallenges{}
	opts := append([]Option{
		WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: true}),
		WithIdentity(IdentityConfig{IdentifierKeyer: prodKeyer{}, SingleFactorAcknowledged: true, ChallengeProtector: w.protector}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: durableLimiter{}}),
		WithDelivery(DeliveryConfig{Mailer: prodMailer{}}),
		WithBrowser(BrowserConfig{SessionCookie: inbound.CookieConfig{Secure: true}}),
	}, extra...)
	c, err := New(repos, stubSigner{}, mode, "off", opts...)
	return c, "", err
}
