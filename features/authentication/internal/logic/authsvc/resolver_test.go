package authsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// TestResolveUserWithDisplayName: a user principal resolves to its DisplayName,
// carrying the email as an identity.KindEmail Address.
func TestResolveUserWithDisplayName(t *testing.T) {
	users := newFakeUsers()
	users.byID["u1"] = user.User{ID: "u1", Email: "alice@example.com", DisplayName: "Alice"}
	svc := NewService(Deps{Users: users})

	info, err := svc.Resolve(context.Background(), identity.Principal{Type: identity.User, ID: "u1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.DisplayName != "Alice" {
		t.Errorf("DisplayName = %q, want Alice", info.DisplayName)
	}
	if len(info.Addresses) != 1 || info.Addresses[0].Kind != identity.KindEmail || info.Addresses[0].Value != "alice@example.com" {
		t.Errorf("Addresses = %+v, want one email address", info.Addresses)
	}
	if info.Principal != (identity.Principal{Type: identity.User, ID: "u1"}) {
		t.Errorf("Principal = %+v", info.Principal)
	}
}

// TestResolveUserDisplayNameFallsBackToEmailLocalPart: a user with no display
// name resolves to the email local part.
func TestResolveUserDisplayNameFallsBackToEmailLocalPart(t *testing.T) {
	users := newFakeUsers()
	users.byID["u2"] = user.User{ID: "u2", Email: "bob@example.com"}
	svc := NewService(Deps{Users: users})

	info, err := svc.Resolve(context.Background(), identity.Principal{Type: identity.User, ID: "u2"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.DisplayName != "bob" {
		t.Errorf("DisplayName = %q, want bob (email local part)", info.DisplayName)
	}
}

// TestResolveServiceAccount: a service-account principal resolves to its Name,
// with no addresses.
func TestResolveServiceAccount(t *testing.T) {
	sas := newFakeServiceAccounts()
	sas.m["sa1"] = serviceaccount.ServiceAccount{ID: "sa1", Name: "ci-bot"}
	svc := NewService(Deps{ServiceAccounts: sas})

	info, err := svc.Resolve(context.Background(), identity.Principal{Type: identity.ServiceAccount, ID: "sa1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.DisplayName != "ci-bot" {
		t.Errorf("DisplayName = %q, want ci-bot", info.DisplayName)
	}
	if len(info.Addresses) != 0 {
		t.Errorf("Addresses = %+v, want none", info.Addresses)
	}
}

// TestResolveFailClosed proves every unresolvable case fails CLOSED with the
// sdk.ErrNotFound class and never panics: an unknown type, a missing user row,
// a missing service-account row, and — the machine subsystem off — a
// service-account principal against a nil ServiceAccounts repository.
func TestResolveFailClosed(t *testing.T) {
	users := newFakeUsers()
	sas := newFakeServiceAccounts()

	cases := []struct {
		name string
		svc  *Service
		p    identity.Principal
	}{
		{"unknown_type", NewService(Deps{Users: users, ServiceAccounts: sas}), identity.Principal{Type: "robot", ID: "x"}},
		{"missing_user", NewService(Deps{Users: users, ServiceAccounts: sas}), identity.Principal{Type: identity.User, ID: "ghost"}},
		{"missing_service_account", NewService(Deps{Users: users, ServiceAccounts: sas}), identity.Principal{Type: identity.ServiceAccount, ID: "ghost"}},
		{"machine_subsystem_off", NewService(Deps{Users: users}), identity.Principal{Type: identity.ServiceAccount, ID: "sa1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := tc.svc.Resolve(context.Background(), tc.p)
			if !errors.Is(err, sdk.ErrNotFound) {
				t.Errorf("Resolve(%s): err=%v, want ErrNotFound", tc.name, err)
			}
			if info.DisplayName != "" || len(info.Addresses) != 0 {
				t.Errorf("Resolve(%s) fabricated an Info: %+v", tc.name, info)
			}
		})
	}
}
