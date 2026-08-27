package authsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// CurrentUserView is the GET /auth/me hydration read: the user aggregate plus the
// account's UNMASKED active email identity, shaped so /auth/me reports exactly
// what login reported.

// TestCurrentUserViewUnverifiedAccount proves the allowed-but-unverified case the
// verified-only accessor cannot serve: a just-registered account has no verified
// identifier, yet the view still reports its email with EmailVerified false —
// login's own body for the same account.
func TestCurrentUserViewUnverifiedAccount(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "New@Example.com", "password123456789")

	view, err := h.svc.CurrentUserView(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CurrentUserView: %v", err)
	}
	if view.User.ID != u.ID || view.User.DisplayName != "Test User" {
		t.Errorf("user = %+v, want the registered aggregate", view.User)
	}
	if view.Email != "new@example.com" {
		t.Errorf("email = %q, want the normalized primary email", view.Email)
	}
	if view.EmailVerified {
		t.Error("email_verified = true for an unverified account")
	}
	// The verified-only accessor genuinely cannot produce this — that is why the
	// view exists.
	if _, err := h.svc.ActiveVerifiedIdentifier(context.Background(), u.ID, string(identifier.KindEmail)); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("ActiveVerifiedIdentifier err = %v, want not-found for an unverified account", err)
	}
}

// TestCurrentUserViewVerifiedAccount proves the proof flag flips once the address
// is verified and the value stays unmasked.
func TestCurrentUserViewVerifiedAccount(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "verified@example.com", "password123456789")
	code := h.mailer.codeFor(t, "verified@example.com")
	if err := h.svc.Verify(context.Background(), "verified@example.com", code); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	view, err := h.svc.CurrentUserView(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CurrentUserView: %v", err)
	}
	if view.Email != "verified@example.com" || !view.EmailVerified {
		t.Errorf("view = {%q, %v}, want the unmasked verified email", view.Email, view.EmailVerified)
	}
}

// TestCurrentUserViewPrefersVerifiedThenPrimary pins the selection order against
// ActiveVerifiedIdentifier's, so /auth/me and login can never disagree: a verified
// identifier wins over an unverified primary, and among verified ones the primary
// wins.
func TestCurrentUserViewPrefersVerifiedThenPrimary(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name         string
		idents       []identifier.Identifier
		wantEmail    string
		wantVerified bool
	}{
		{
			name: "verified secondary beats unverified primary",
			idents: []identifier.Identifier{
				{ID: "a", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "unverified@example.com", IsPrimary: true, CreatedAt: now},
				{ID: "b", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "proven@example.com", VerifiedAt: now, CreatedAt: now.Add(time.Hour)},
			},
			wantEmail:    "proven@example.com",
			wantVerified: true,
		},
		{
			name: "verified primary beats verified older secondary",
			idents: []identifier.Identifier{
				{ID: "a", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "old@example.com", VerifiedAt: now, CreatedAt: now},
				{ID: "b", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "primary@example.com", VerifiedAt: now, IsPrimary: true, CreatedAt: now.Add(time.Hour)},
			},
			wantEmail:    "primary@example.com",
			wantVerified: true,
		},
		{
			name: "unverified: primary first, then oldest",
			idents: []identifier.Identifier{
				{ID: "a", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "second@example.com", CreatedAt: now},
				{ID: "b", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "first@example.com", IsPrimary: true, CreatedAt: now.Add(time.Hour)},
			},
			wantEmail:    "first@example.com",
			wantVerified: false,
		},
		{
			name: "a retired row is never projected",
			idents: []identifier.Identifier{
				{ID: "a", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "retired@example.com", VerifiedAt: now, IsPrimary: true, CreatedAt: now, ReplacedAt: now},
				{ID: "b", UserID: "u1", Kind: identifier.KindEmail, NormalizedValue: "current@example.com", VerifiedAt: now, CreatedAt: now},
			},
			wantEmail:    "current@example.com",
			wantVerified: true,
		},
		{
			name: "a phone identifier is not an email identity",
			idents: []identifier.Identifier{
				{ID: "a", UserID: "u1", Kind: identifier.KindPhone, NormalizedValue: "+15555550123", VerifiedAt: now, IsPrimary: true, CreatedAt: now},
			},
			wantEmail:    "",
			wantVerified: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.users.byID["u1"] = user.User{ID: "u1", DisplayName: "Seed"}
			for _, it := range tt.idents {
				h.idents.insert(it)
			}
			view, err := h.svc.CurrentUserView(context.Background(), "u1")
			if err != nil {
				t.Fatalf("CurrentUserView: %v", err)
			}
			if view.Email != tt.wantEmail || view.EmailVerified != tt.wantVerified {
				t.Errorf("view = {%q, %v}, want {%q, %v}", view.Email, view.EmailVerified, tt.wantEmail, tt.wantVerified)
			}
		})
	}
}

// TestCurrentUserViewUnknownUser proves the read fails closed rather than
// fabricating a profile for an id with no aggregate.
func TestCurrentUserViewUnknownUser(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.svc.CurrentUserView(context.Background(), "nobody"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("err = %v, want not-found", err)
	}
}

// TestIdentityUnavailableIsServerShaped pins the error KIND of an unwired user
// rail: a host wiring bug is a 500, never a resource-shaped 404 telling a
// signed-in caller their own account does not exist. Wrapping no sdk sentinel is
// exactly what web.ErrFromDomain maps to 500.
func TestIdentityUnavailableIsServerShaped(t *testing.T) {
	if errors.Is(ErrIdentityUnavailable, sdk.ErrNotFound) {
		t.Error("ErrIdentityUnavailable is not-found shaped; an unwired rail must not report 404")
	}
	if sdk.IsExpected(ErrIdentityUnavailable) {
		t.Errorf("ErrIdentityUnavailable wraps a domain sentinel (%v); it must fall through to 500", ErrIdentityUnavailable)
	}

	s := &Service{}
	if _, err := s.CurrentUserView(context.Background(), "u1"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("err = %v, want ErrIdentityUnavailable", err)
	}
}
