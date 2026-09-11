package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// memIdentifierRepo is a minimal identifier.IdentifierRepository for the
// invitee-lookup test: it answers GetLogin/GetRecovery over the seeded active
// rows and treats every mutating operation as unused.
type memIdentifierRepo struct{ rows []identifier.Identifier }

func (r *memIdentifierRepo) add(id identifier.Identifier) { r.rows = append(r.rows, id) }

func (r *memIdentifierRepo) Get(_ context.Context, id string) (identifier.Identifier, error) {
	for _, row := range r.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (r *memIdentifierRepo) GetLogin(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	for _, row := range r.rows {
		if string(row.Kind) == kind && row.NormalizedValue == normalizedValue && row.LoginEnabled {
			return row, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (r *memIdentifierRepo) GetRecovery(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	for _, row := range r.rows {
		if string(row.Kind) == kind && row.NormalizedValue == normalizedValue && row.RecoveryEnabled {
			return row, nil
		}
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

func (r *memIdentifierRepo) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
	var out []identifier.Identifier
	for _, row := range r.rows {
		if row.UserID == userID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *memIdentifierRepo) ApplyVerifiedChange(context.Context, identifier.ApplyVerifiedChangeInput, int64, time.Time) (identifier.Identifier, error) {
	return identifier.Identifier{}, errors.New("unused")
}

// TestUserLookupRequiresVerifiedOwnership prevents automatic invitation grants
// from treating an unverified registration address as proof of ownership.
func TestUserLookupRequiresVerifiedOwnership(t *testing.T) {
	repo := &memIdentifierRepo{}
	norm := identifier.DefaultNormalizer{}
	now := time.Now()

	verified, err := identifier.New(sdk.IDGenerator{}, norm, "", identifier.KindEmail, "verified@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, now, now)
	if err != nil {
		t.Fatalf("new verified identifier: %v", err)
	}
	verified.UserID = "user-verified"
	repo.add(verified)

	// The unverified registration identifier Register creates atomically with the
	// user: login-enabled and active, but not yet proved.
	unverified, err := identifier.NewRegistrationEmail(sdk.IDGenerator{}, norm, "", "Unverified@Example.com", now)
	if err != nil {
		t.Fatalf("new registration identifier: %v", err)
	}
	unverified.UserID = "user-unverified"
	repo.add(unverified)
	if unverified.Verified() {
		t.Fatal("fixture bug: the registration identifier should be unverified")
	}

	lookup := userLookup(repo, norm)

	cases := []struct {
		name      string
		email     string
		wantID    string
		wantFound bool
	}{
		{"verified registration is a known user", "Verified@Example.com", "user-verified", true},
		{"unverified registration has no proven ownership", "unverified@example.com", "", false},
		{"unclaimed address is not a user", "nobody@example.com", "", false},
		{"unparseable address is not a user (never an error)", "not-an-email", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, found, err := lookup(context.Background(), tc.email)
			if err != nil {
				t.Fatalf("userLookup(%q): %v", tc.email, err)
			}
			if found != tc.wantFound || id != tc.wantID {
				t.Errorf("userLookup(%q) = (%q, %v), want (%q, %v)", tc.email, id, found, tc.wantID, tc.wantFound)
			}
		})
	}
}
