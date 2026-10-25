package user

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestStatusIsThreeValued pins the closed vocabulary: the three wire values
// round-trip, erased is valid and inactive, and ErrErased is a conflict, never
// an invalid-input refusal.
func TestStatusIsThreeValued(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		want   Status
		active bool
	}{
		{"active", StatusActive, true},
		{"deactivated", StatusDeactivated, false},
		{"erased", StatusErased, false},
	} {
		got, err := ParseStatus(tc.raw)
		if err != nil || got != tc.want || !got.Valid() || got.Active() != tc.active {
			t.Errorf("ParseStatus(%q) = %q, %v; active=%v", tc.raw, got, err, got.Active())
		}
	}
	for _, bad := range []string{"", "ERASED", "deleted", "suspended"} {
		if _, err := ParseStatus(bad); !errors.Is(err, ErrInvalidStatus) {
			t.Errorf("ParseStatus(%q): err = %v, want ErrInvalidStatus", bad, err)
		}
	}
	if !errors.Is(ErrErased, sdk.ErrConflict) || errors.Is(ErrErased, sdk.ErrInvalidInput) {
		t.Error("ErrErased must be a conflict (a refused transition), not invalid input")
	}
	if !errors.Is(ErrEraseBlockedByClaim, sdk.ErrConflict) {
		t.Error("ErrEraseBlockedByClaim must be a conflict")
	}
	if (User{Status: StatusErased}).Active() {
		t.Error("an erased user reads as active")
	}
}

// TestEraseCountsMerge pins the user_erased Details spelling every store
// writes, and that the caller's map is never written.
func TestEraseCountsMerge(t *testing.T) {
	in := map[string]any{"note": "x"}
	out := EraseCounts{Sessions: 1, Grants: 2, Passwords: 3, OAuthAccounts: 4, ExternalSubjects: 5, ContactChanges: 6, Codes: 7,
		Challenges: 8, IdentifiersBlanked: 9, InvitationsDeleted: 10, InvitationsBlanked: 11, InvitationsCancelled: 12,
		ServiceAccounts: 13, APIKeys: 14, Passkeys: 15, TOTP: 16, RecoveryCodes: 17}.Merge(in)
	want := map[string]int{"sessions": 1, "grants": 2, "passwords": 3, "oauth_accounts": 4, "external_subjects": 5, "contact_changes": 6,
		"codes": 7, "challenges": 8, "identifiers_blanked": 9, "invitations_deleted": 10, "invitations_blanked": 11,
		"invitations_cancelled": 12, "service_accounts": 13, "api_keys": 14, "passkeys": 15, "totp": 16, "recovery_codes": 17}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("Details[%q] = %v, want %d", k, out[k], v)
		}
	}
	if out["note"] != "x" || len(out) != len(want)+1 || len(in) != 1 {
		t.Errorf("merge = %v, input = %v", out, in)
	}
	if got := (EraseCounts{}).Merge(nil); len(got) != len(want) {
		t.Errorf("merge into nil = %v", got)
	}
}
