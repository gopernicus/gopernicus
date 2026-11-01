package authenticationhttp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestActAsUserAdmissionBootWarning pins the boot WARN for a bundled group
// override that admits act-as-user keys: exactly one line per admitting group,
// none for a set that refuses or never admits a key or on a host without
// machine identity, and one escalation line per admitting group beside
// MFAConfig.APIKeysExempt.
func TestActAsUserAdmissionBootWarning(t *testing.T) {
	const (
		admits    = "posture_warning=bundled_group_admits_act_as_user_keys"
		escalates = "posture_warning=bundled_group_act_as_user_mfa_exempt"
	)
	machine := newServiceWithFakes(authenticationFixture{
		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},
	})
	plain := newServiceWithFakes(authenticationFixture{})

	for _, tc := range []struct {
		name          string
		svc           *testService
		routeAuth     BundledRouteAuthentication
		exempt        bool
		wantAdmits    map[string]int
		wantEscalates map[string]int
	}{
		{"defaults", machine, BundledRouteAuthentication{}, true, nil, nil},
		{"UserAdministration Live", machine, BundledRouteAuthentication{UserAdministration: PrincipalStrategy(Live())}, false,
			map[string]int{"UserAdministration": 1}, nil},
		{"Invitations Live", machine, BundledRouteAuthentication{Invitations: PrincipalStrategy(Live())}, false,
			map[string]int{"Invitations": 1}, nil},
		{"both Live", machine, BundledRouteAuthentication{UserAdministration: PrincipalStrategy(Live()), Invitations: PrincipalStrategy(Live())}, false,
			map[string]int{"UserAdministration": 1, "Invitations": 1}, nil},
		{"both Live with APIKeysExempt", machine, BundledRouteAuthentication{UserAdministration: PrincipalStrategy(Live()), Invitations: PrincipalStrategy(Live())}, true,
			map[string]int{"UserAdministration": 1, "Invitations": 1}, map[string]int{"UserAdministration": 1, "Invitations": 1}},
		{"RefuseActAsUserKeys", machine, BundledRouteAuthentication{UserAdministration: PrincipalStrategy(Live(), RefuseActAsUserKeys()), Invitations: PrincipalStrategy(RefuseActAsUserKeys())}, true, nil, nil},
		{"FirstParty", machine, BundledRouteAuthentication{UserAdministration: PrincipalStrategy(FirstParty()), Invitations: PrincipalStrategy(FirstParty())}, true, nil, nil},
		{"Accept access token", machine, BundledRouteAuthentication{UserAdministration: PrincipalStrategy(Accept(CredentialAccessToken)), Invitations: PrincipalStrategy(Accept(CredentialAccessToken))}, true, nil, nil},
		{"another group admitting keys", machine, BundledRouteAuthentication{SessionHydration: PrincipalStrategy(Live()), CSRFBootstrap: PrincipalStrategy()}, true, nil, nil},
		{"machine identity unwired", plain, BundledRouteAuthentication{UserAdministration: PrincipalStrategy(Live()), Invitations: PrincipalStrategy(Live())}, true, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			if _, err := New(tc.svc.Service, environment.ModeDevelopment,
				WithAuthenticatorPolicy(AuthenticatorPolicy{Logger: logger}),
				WithRouteAuthentication(tc.routeAuth),
				WithMFAAPIKeysExempt(tc.exempt)); err != nil {
				t.Fatal(err)
			}
			for _, group := range []string{"UserAdministration", "Invitations"} {
				if got := countLogLines(buf.String(), admits, "group="+group); got != tc.wantAdmits[group] {
					t.Errorf("%s admission lines = %d, want %d:\n%s", group, got, tc.wantAdmits[group], buf.String())
				}
				if got := countLogLines(buf.String(), escalates, "group="+group); got != tc.wantEscalates[group] {
					t.Errorf("%s escalation lines = %d, want %d:\n%s", group, got, tc.wantEscalates[group], buf.String())
				}
			}
			if got := countLogLines(buf.String(), "posture_warning=bundled_group_"); got != sum(tc.wantAdmits)+sum(tc.wantEscalates) {
				t.Errorf("bundled-group posture lines = %d, want %d:\n%s", got, sum(tc.wantAdmits)+sum(tc.wantEscalates), buf.String())
			}
		})
	}
}

// countLogLines counts the lines of logs carrying every one of parts.
func countLogLines(logs string, parts ...string) int {
	n := 0
	for _, line := range strings.Split(logs, "\n") {
		matched := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				matched = false
				break
			}
		}
		if matched && line != "" {
			n++
		}
	}
	return n
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
