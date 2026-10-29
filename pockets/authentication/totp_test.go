package authentication

import (
	"context"
	"errors"
	"strings"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// stubTOTP is a construction-only TOTP store: nothing here enrolls anyone.
type stubTOTP struct{ totp.Repository }

func (stubTOTP) RecoveryCodeKeyIDs(context.Context) ([]string, error) { return nil, nil }

// probingTOTP reports its schema through totp.SchemaProber.
type probingTOTP struct {
	stubTOTP
	err    error
	probed *int
}

func (p probingTOTP) ProbeSchema(context.Context) error {
	*p.probed++
	return p.err
}

func testTOTPEncrypter() cryptids.KeyedEncrypter {
	ring, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: "t1", Keys: map[string][]byte{"t1": []byte(strings.Repeat("t", 32))}})
	if err != nil {
		panic(err)
	}
	return ring
}

// TestTOTPWiring pins WithTOTP's validation: an encrypter needs the TOTP store
// and the rails its gates use, views need an encrypter and page views, and the
// issuer is required without a colon.
func TestTOTPWiring(t *testing.T) {
	protector := passwordlessProtector(t)
	if _, _, err := (mfaWiring{ack: true, totp: true, protector: protector}).build(environment.ModeDevelopment); err != nil {
		t.Fatalf("a wired TOTP: %v", err)
	}
	for name, tc := range map[string]struct {
		cfg   TOTPConfig
		store bool
		want  error
	}{
		"no TOTP store":            {TOTPConfig{Encrypter: testTOTPEncrypter(), Issuer: "Example"}, false, ErrTOTPReposRequired},
		"views without encrypter":  {TOTPConfig{Views: struct{ inbound.TOTPViews }{}}, true, sdk.ErrInvalidInput},
		"views without page views": {TOTPConfig{Encrypter: testTOTPEncrypter(), Issuer: "Example", Views: struct{ inbound.TOTPViews }{}}, true, sdk.ErrInvalidInput},
		"no issuer":                {TOTPConfig{Encrypter: testTOTPEncrypter()}, true, sdk.ErrInvalidInput},
		"a colon in the issuer":    {TOTPConfig{Encrypter: testTOTPEncrypter(), Issuer: "a:b"}, true, sdk.ErrInvalidInput},
	} {
		repos := testRepositories(Repositories{})
		if tc.store {
			repos.TOTP, repos.CredentialMutations, repos.AuthenticationGrants, repos.Challenges = stubTOTP{}, stubCredentialMutations{}, stubGrants{}, stubChallenges{}
		}
		_, err := New(repos, stubSigner{}, environment.ModeDevelopment, "off",
			WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: true}),
			WithIdentity(IdentityConfig{ChallengeProtector: protector}),
			WithDelivery(DeliveryConfig{Mailer: stubMailer{}}),
			WithTOTP(tc.cfg))
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// TestTOTPSchemaProbeOnlyWhenWired pins the owner ruling (0026 conditional): a
// store that probes its schema is probed at New only when WithTOTP is wired; a
// missing schema then refuses boot naming 0026, and an unwired host boots.
func TestTOTPSchemaProbeOnlyWhenWired(t *testing.T) {
	protector := passwordlessProtector(t)
	probed := 0
	missing := probingTOTP{err: errors.New("relation totp_credentials does not exist"), probed: &probed}
	_, _, err := mfaWiring{ack: true, totp: true, totpRepo: missing, protector: protector}.build(environment.ModeDevelopment)
	if !errors.Is(err, ErrTOTPSchemaMissing) || !strings.Contains(err.Error(), "0026") || probed != 1 {
		t.Fatalf("wired over a missing schema = %v (probed %d)", err, probed)
	}
	present := probingTOTP{probed: &probed}
	if _, _, err := (mfaWiring{ack: true, totp: true, totpRepo: present, protector: protector}).build(environment.ModeDevelopment); err != nil || probed != 2 {
		t.Fatalf("wired over a migrated schema = %v (probed %d)", err, probed)
	}
	repos := testRepositories(Repositories{TOTP: missing})
	if _, err := New(repos, stubSigner{}, environment.ModeDevelopment, "off",
		WithPassword(PasswordConfig{Hasher: stubHasher{}, PasswordFlowsDisabled: true}),
		WithDelivery(DeliveryConfig{Mailer: stubMailer{}})); err != nil || probed != 2 {
		t.Fatalf("an unwired host over a missing schema = %v (probed %d)", err, probed)
	}
}

// TestMFAWithTOTP pins the resolve widening (B-2, row 4), the Methods
// validation (row 2) and the boot line's sources and methods.
func TestMFAWithTOTP(t *testing.T) {
	protector := passwordlessProtector(t)
	rp := relyingParty(t, "example.com", []string{"https://example.com"})
	for want, w := range map[string]mfaWiring{
		"mfa_required=admins source=default gates=/auth/admin/users*,machine_lifecycle sources=totp methods=any":             {ack: true, totp: true, protector: protector, adminCheck: true},
		"mfa_required=none source=default gates=none sources=totp methods=any":                                               {ack: true, totp: true, protector: protector},
		"mfa_required=admins source=default gates=/auth/admin/users*,machine_lifecycle sources=passkey,totp methods=passkey": {ack: true, totp: true, passkeys: rp, protector: protector, adminCheck: true, methods: []MFAMethod{MFAMethodPasskey}},
		"mfa_required=none source=default gates=none sources=none methods=any":                                               {ack: true},
	} {
		_, logs, err := w.build(environment.ModeDevelopment)
		if err != nil || !strings.Contains(logs, want) {
			t.Errorf("%q: err = %v, logs:\n%s", want, err, logs)
		}
	}
	for name, tc := range map[string]struct {
		w    mfaWiring
		want error
	}{
		"an unknown method":          {mfaWiring{ack: true, totp: true, protector: protector, methods: []MFAMethod{"sms"}}, sdk.ErrInvalidInput},
		"totp without WithTOTP":      {mfaWiring{ack: true, passkeys: rp, protector: protector, methods: []MFAMethod{MFAMethodTOTP}}, ErrMFAUnsatisfiable},
		"passkey without passkeys":   {mfaWiring{ack: true, totp: true, protector: protector, methods: []MFAMethod{MFAMethodPasskey}}, ErrMFAUnsatisfiable},
		"idp without WithExternal":   {mfaWiring{ack: true, totp: true, protector: protector, methods: []MFAMethod{MFAMethodIdP}}, ErrMFAUnsatisfiable},
		"native all beside TOTP":     {mfaWiring{ack: true, totp: true, protector: protector, required: MFARequiredAll}, ErrMFAEnrollmentStartRequired},
		"admins satisfiable by TOTP": {mfaWiring{ack: true, totp: true, protector: protector, adminCheck: true, required: MFARequiredAdmins}, nil},
	} {
		_, _, err := tc.w.build(environment.ModeDevelopment)
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}
