package authsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// resolverFixture builds a Service over linked user/identifier fakes and returns
// the fakes so a test can seed identifier rows directly (the identifier lifecycle
// service methods land in later phases).
func resolverFixture() (*Service, *fakeUsers, *fakeIdentifiers) {
	users := newFakeUsers()
	idents := newFakeIdentifiers(users)
	svc := NewService(Deps{Users: users, Identifiers: idents})
	return svc, users, idents
}

// TestResolveUserWithDisplayName: a user principal resolves to its DisplayName,
// carrying its active verified email as an identity.KindEmail Address (design §7).
func TestResolveUserWithDisplayName(t *testing.T) {
	svc, users, idents := resolverFixture()
	users.byID["u1"] = user.User{ID: "u1", DisplayName: "Alice"}
	idents.byID["i1"] = identifier.Identifier{
		ID: "i1", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "alice@example.com",
		VerifiedAt: time.Now(), LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true, IsPrimary: true,
	}

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
// name resolves to the primary email local part.
func TestResolveUserDisplayNameFallsBackToEmailLocalPart(t *testing.T) {
	svc, users, idents := resolverFixture()
	users.byID["u2"] = user.User{ID: "u2"}
	idents.byID["i2"] = identifier.Identifier{
		ID: "i2", UserID: "u2", Kind: identifier.KindEmail, NormalizedValue: "bob@example.com",
		VerifiedAt: time.Now(), LoginEnabled: true, IsPrimary: true,
	}

	info, err := svc.Resolve(context.Background(), identity.Principal{Type: identity.User, ID: "u2"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.DisplayName != "bob" {
		t.Errorf("DisplayName = %q, want bob (email local part)", info.DisplayName)
	}
}

// TestResolveProjectsAllVerifiedIdentifiersPrimaryFirst: every active verified
// identifier is projected, primary-first then oldest-first, across kinds — a
// notification-only verified phone included, a login email too (design §7).
func TestResolveProjectsAllVerifiedIdentifiersPrimaryFirst(t *testing.T) {
	svc, users, idents := resolverFixture()
	users.byID["u3"] = user.User{ID: "u3", DisplayName: "Carol"}
	base := time.Now()
	// Seeded out of order to prove the sort, not map iteration.
	idents.byID["b"] = identifier.Identifier{
		ID: "b", UserID: "u3", Kind: identifier.KindEmail, NormalizedValue: "second@example.com",
		VerifiedAt: base, LoginEnabled: true, CreatedAt: base.Add(2 * time.Hour),
	}
	idents.byID["p"] = identifier.Identifier{
		ID: "p", UserID: "u3", Kind: identifier.KindEmail, NormalizedValue: "primary@example.com",
		VerifiedAt: base, LoginEnabled: true, IsPrimary: true, CreatedAt: base.Add(1 * time.Hour),
	}
	idents.byID["ph"] = identifier.Identifier{
		ID: "ph", UserID: "u3", Kind: identifier.KindPhone, NormalizedValue: "+15551230000",
		VerifiedAt: base, NotificationEnabled: true, CreatedAt: base.Add(3 * time.Hour),
	}

	info, err := svc.Resolve(context.Background(), identity.Principal{Type: identity.User, ID: "u3"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []identity.Address{
		{Kind: identity.KindEmail, Value: "primary@example.com"}, // primary first
		{Kind: identity.KindEmail, Value: "second@example.com"},  // then oldest of the rest
		{Kind: identity.KindPhone, Value: "+15551230000"},        // notification-only phone still projected
	}
	if len(info.Addresses) != len(want) {
		t.Fatalf("Addresses = %+v, want %+v", info.Addresses, want)
	}
	for i := range want {
		if info.Addresses[i] != want[i] {
			t.Errorf("Addresses[%d] = %+v, want %+v", i, info.Addresses[i], want[i])
		}
	}
}

// TestResolveExcludesReplacedAndUnverified: replaced (retired) and unverified rows
// never enter the identity projection (design §7); only active verified remain.
func TestResolveExcludesReplacedAndUnverified(t *testing.T) {
	svc, users, idents := resolverFixture()
	users.byID["u4"] = user.User{ID: "u4", DisplayName: "Dave"}
	base := time.Now()
	idents.byID["live"] = identifier.Identifier{
		ID: "live", UserID: "u4", Kind: identifier.KindEmail, NormalizedValue: "live@example.com",
		VerifiedAt: base, LoginEnabled: true, IsPrimary: true, CreatedAt: base,
	}
	idents.byID["old"] = identifier.Identifier{
		ID: "old", UserID: "u4", Kind: identifier.KindEmail, NormalizedValue: "old@example.com",
		VerifiedAt: base, LoginEnabled: true, CreatedAt: base, ReplacedAt: base.Add(time.Minute),
	}
	idents.byID["pending"] = identifier.Identifier{
		ID: "pending", UserID: "u4", Kind: identifier.KindPhone, NormalizedValue: "+15550009999",
		NotificationEnabled: true, CreatedAt: base, // unverified
	}

	info, err := svc.Resolve(context.Background(), identity.Principal{Type: identity.User, ID: "u4"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(info.Addresses) != 1 || info.Addresses[0].Value != "live@example.com" {
		t.Errorf("Addresses = %+v, want only the active verified live@example.com", info.Addresses)
	}
}

// TestResolveUnverifiedRegistrationProjectsNoAddress: a freshly registered user
// (unverified primary identifier) projects no addresses — identity is verified-only.
func TestResolveUnverifiedRegistrationProjectsNoAddress(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "fresh@example.com", "password123456789")

	info, err := h.svc.Resolve(context.Background(), identity.Principal{Type: identity.User, ID: u.ID})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(info.Addresses) != 0 {
		t.Errorf("Addresses = %+v, want none (registration identifier unverified)", info.Addresses)
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
