package authentication

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func allowCan(context.Context, sdk.Principal, string, string, string) (bool, error) { return true, nil }

// TestNewServiceInvitationResourceRuleMatrix pins the D2 construction matrix for
// the invitation resource rule, including incomplete-before-absent precedence.
func TestNewServiceInvitationResourceRuleMatrix(t *testing.T) {
	base := constructorConfig{Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{}, RuntimeMode: environment.ModeDevelopment, DeliveryMode: delivery.ModeOff}
	perms := map[string]string{"project": "manage"}
	type wiring struct {
		granter bool
		perms   map[string]string
		can     bool
		check   bool
	}
	cases := []struct {
		name string
		w    wiring
		want error
	}{
		{"rule on", wiring{granter: true, perms: perms, can: true}, nil},
		{"rule on + InviteCheck", wiring{granter: true, perms: perms, can: true, check: true}, nil},
		{"rule off + InviteCheck", wiring{granter: true, check: true}, nil},
		{"granter without policy", wiring{granter: true}, ErrInviteCheckRequired},
		{"lone map", wiring{granter: true, perms: perms}, ErrInvitationResourceRuleIncomplete},
		{"lone Can", wiring{granter: true, can: true}, ErrInvitationResourceRuleIncomplete},
		{"lone map + InviteCheck", wiring{granter: true, perms: perms, check: true}, ErrInvitationResourceRuleIncomplete},
		{"empty key", wiring{granter: true, perms: map[string]string{"": "manage"}, can: true}, ErrInvitationResourceRuleIncomplete},
		{"empty permission", wiring{granter: true, perms: map[string]string{"project": ""}, can: true}, ErrInvitationResourceRuleIncomplete},
		{"lone map without granter", wiring{perms: perms}, ErrInvitationResourceRuleIncomplete},
		{"lone Can without granter", wiring{can: true}, ErrInvitationResourceRuleIncomplete},
		{"rule without granter", wiring{perms: perms, can: true}, ErrInvitationResourceRuleWithoutGranter},
		{"rule + InviteCheck without granter", wiring{perms: perms, can: true, check: true}, ErrInvitationResourceRuleWithoutGranter},
		{"InviteCheck without granter", wiring{check: true}, ErrInviteCheckWithoutGranter},
		{"invitations off", wiring{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			repos := Repositories{}
			if tc.w.granter {
				cfg.Granter = stubGranter{}
				repos.Invitations = stubInvitations{}
			}
			cfg.ResourcePermissions = tc.w.perms
			if tc.w.can {
				cfg.Can = allowCan
			}
			if tc.w.check {
				cfg.InviteCheck = allowInvite
			}
			c, err := newFixture(testRepositories(repos), cfg)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err=%v, want %v", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if (c.Invitations != nil) != tc.w.granter {
				t.Fatalf("invitations enabled=%v, want %v", c.Invitations != nil, tc.w.granter)
			}
		})
	}
}

// TestWithInvitationsClonesResourcePermissions proves a host mutating its map
// after configuring cannot widen the rule.
func TestWithInvitationsClonesResourcePermissions(t *testing.T) {
	perms := map[string]string{"project": "manage"}
	var cfg constructorConfig
	WithInvitations(InvitationsConfig{Granter: stubGranter{}, ResourcePermissions: perms, Can: allowCan})(&cfg)
	perms["folder"] = "manage"
	if _, ok := cfg.ResourcePermissions["folder"]; ok || cfg.ResourcePermissions["project"] != "manage" || cfg.Can == nil {
		t.Fatalf("WithInvitations did not copy the rule: %+v", cfg.ResourcePermissions)
	}
}
