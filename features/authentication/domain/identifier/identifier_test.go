package identifier

import (
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// dbIDs mirrors the greenfield DB-generated convention: identifier IDs are empty
// at construction and the store assigns them inline.
var dbIDs = cryptids.NewGenerator(cryptids.Database)

func TestKind_ValidAndParse(t *testing.T) {
	for _, k := range []Kind{KindEmail, KindPhone} {
		if !k.Valid() {
			t.Errorf("Kind(%q).Valid() = false, want true", k)
		}
		got, err := ParseKind(string(k))
		if err != nil {
			t.Errorf("ParseKind(%q) err = %v, want nil", k, err)
		}
		if got != k {
			t.Errorf("ParseKind(%q) = %q, want %q", k, got, k)
		}
	}

	// The vocabulary is closed: any other string is rejected at the boundary.
	for _, s := range []string{"", "username", "sms", "e-mail", "EMAIL", "webauthn"} {
		if Kind(s).Valid() {
			t.Errorf("Kind(%q).Valid() = true, want false (closed vocabulary)", s)
		}
		if _, err := ParseKind(s); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ParseKind(%q) err = %v, want errors.Is sdk.ErrInvalidInput", s, err)
		}
		if _, err := ParseKind(s); !errors.Is(err, ErrUnknownKind) {
			t.Errorf("ParseKind(%q) err = %v, want errors.Is ErrUnknownKind", s, err)
		}
	}
}

// Kind constants are pinned to the sdk identity address-kind names so a resolved
// address and a stored identifier speak one kind string.
func TestKind_MatchesIdentityVocabulary(t *testing.T) {
	if string(KindEmail) != "email" {
		t.Errorf("KindEmail = %q, want %q", KindEmail, "email")
	}
	if string(KindPhone) != "phone" {
		t.Errorf("KindPhone = %q, want %q", KindPhone, "phone")
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		errIs error // nil → expect success
	}{
		{name: "already canonical", in: "bob@example.com", want: "bob@example.com"},
		{name: "surrounding whitespace trimmed", in: "  bob@example.com \t", want: "bob@example.com"},
		{name: "domain lowercased", in: "bob@Example.COM", want: "bob@example.com"},
		{name: "local part folded by default", in: "Bob.Smith@Example.com", want: "bob.smith@example.com"},
		{name: "no gmail dot rewriting", in: "a.b.c@gmail.com", want: "a.b.c@gmail.com"},
		{name: "no plus-tag rewriting", in: "user+promo@gmail.com", want: "user+promo@gmail.com"},
		{name: "unicode local preserved", in: "josé@example.com", want: "josé@example.com"},
		{name: "unicode domain lowercased not punycoded", in: "user@München.de", want: "user@münchen.de"},
		{name: "display-name form rejected", in: "Bob <bob@example.com>", errIs: sdk.ErrInvalidInput},
		{name: "angle-addr form rejected", in: "<bob@example.com>", errIs: sdk.ErrInvalidInput},
		{name: "empty rejected", in: "   ", errIs: sdk.ErrInvalidInput},
		{name: "missing at rejected", in: "notanemail", errIs: sdk.ErrInvalidInput},
		{name: "missing local rejected", in: "@example.com", errIs: sdk.ErrInvalidInput},
	}

	norm := DefaultNormalizer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := norm.Normalize(string(KindEmail), tt.in)
			if tt.errIs != nil {
				if !errors.Is(err, tt.errIs) {
					t.Fatalf("Normalize(email, %q) err = %v, want errors.Is %v", tt.in, err, tt.errIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(email, %q) err = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Normalize(email, %q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// PreserveLocalPartCase keeps the local part's case while still lowercasing the
// domain; the default (compatibility) folds the local part.
func TestNormalizeEmail_LocalPartFoldingConfig(t *testing.T) {
	const in = "Bob.Smith@Example.com"

	folded, err := DefaultNormalizer{}.Normalize(string(KindEmail), in)
	if err != nil || folded != "bob.smith@example.com" {
		t.Fatalf("default fold = %q, %v; want %q", folded, err, "bob.smith@example.com")
	}

	preserved, err := DefaultNormalizer{PreserveLocalPartCase: true}.Normalize(string(KindEmail), in)
	if err != nil || preserved != "Bob.Smith@example.com" {
		t.Fatalf("preserved = %q, %v; want %q", preserved, err, "Bob.Smith@example.com")
	}
}

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		errIs error
	}{
		{name: "already canonical", in: "+14155550123", want: "+14155550123"},
		{name: "spaces stripped", in: "+1 415 555 0123", want: "+14155550123"},
		{name: "visual separators stripped", in: "+1 (415) 555-0123", want: "+14155550123"},
		{name: "surrounding whitespace trimmed", in: "  +14155550123  ", want: "+14155550123"},
		{name: "dots stripped", in: "+1.415.555.0123", want: "+14155550123"},
		{name: "max 15 digits ok", in: "+123456789012345", want: "+123456789012345"},
		{name: "missing plus rejected no inference", in: "14155550123", errIs: sdk.ErrInvalidInput},
		{name: "leading zero rejected", in: "+04155550123", errIs: sdk.ErrInvalidInput},
		{name: "letters rejected", in: "+1415555ABCD", errIs: sdk.ErrInvalidInput},
		{name: "too long rejected", in: "+1234567890123456", errIs: sdk.ErrInvalidInput},
		{name: "empty rejected", in: "   ", errIs: sdk.ErrInvalidInput},
		{name: "plus only rejected", in: "+", errIs: sdk.ErrInvalidInput},
	}

	norm := DefaultNormalizer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := norm.Normalize(string(KindPhone), tt.in)
			if tt.errIs != nil {
				if !errors.Is(err, tt.errIs) {
					t.Fatalf("Normalize(phone, %q) err = %v, want errors.Is %v", tt.in, err, tt.errIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(phone, %q) err = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Normalize(phone, %q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Normalization is idempotent: re-normalizing an already-normalized value yields
// the same value across both kinds (property-style over a table of valid inputs).
func TestNormalize_Idempotent(t *testing.T) {
	norm := DefaultNormalizer{}
	cases := []struct {
		kind Kind
		in   string
	}{
		{KindEmail, "  Bob.Smith@Example.COM "},
		{KindEmail, "user+promo@gmail.com"},
		{KindEmail, "josé@München.de"},
		{KindPhone, "+1 (415) 555-0123"},
		{KindPhone, "+442071838750"},
	}
	for _, c := range cases {
		once, err := norm.Normalize(string(c.kind), c.in)
		if err != nil {
			t.Fatalf("Normalize(%s, %q) err = %v", c.kind, c.in, err)
		}
		twice, err := norm.Normalize(string(c.kind), once)
		if err != nil {
			t.Fatalf("Normalize(%s, %q) [2nd] err = %v", c.kind, once, err)
		}
		if once != twice {
			t.Errorf("Normalize not idempotent for (%s, %q): once=%q twice=%q", c.kind, c.in, once, twice)
		}
	}
}

func TestNormalize_UnknownKind(t *testing.T) {
	_, err := DefaultNormalizer{}.Normalize("username", "x")
	if !errors.Is(err, ErrUnknownKind) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Normalize(unknown) err = %v, want ErrUnknownKind/sdk.ErrInvalidInput", err)
	}
}

func TestNew_NormalizesAndSetsFields(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	verified := now.Add(-time.Hour)
	id, err := New(dbIDs, DefaultNormalizer{}, "u-1", KindEmail, "  Alice@Example.com ",
		Uses{Login: true, Notification: true}, true, verified, now)
	if err != nil {
		t.Fatalf("New err = %v, want nil", err)
	}
	if id.ID != "" {
		t.Errorf("ID = %q, want empty (DB-generated)", id.ID)
	}
	if id.NormalizedValue != "alice@example.com" {
		t.Errorf("NormalizedValue = %q, want %q", id.NormalizedValue, "alice@example.com")
	}
	if id.UserID != "u-1" || id.Kind != KindEmail || !id.IsPrimary {
		t.Errorf("fields = %+v, want userID u-1, email, primary", id)
	}
	if !id.Verified() || !id.Active() {
		t.Errorf("Verified=%v Active=%v, want true/true", id.Verified(), id.Active())
	}
	if !id.LoginEnabled || id.RecoveryEnabled || !id.NotificationEnabled {
		t.Errorf("uses = %+v, want login+notification only", id.CurrentUses())
	}
	if !id.CreatedAt.Equal(now) || !id.VerifiedAt.Equal(verified) {
		t.Errorf("timestamps createdAt=%v verifiedAt=%v", id.CreatedAt, id.VerifiedAt)
	}
}

// New rejects login or recovery use on an unverified identifier
// (ErrVerificationRequired, wrapping sdk.ErrConflict).
func TestNew_RejectsUnverifiedAuthenticationUse(t *testing.T) {
	now := time.Now().UTC()
	for _, uses := range []Uses{{Login: true}, {Recovery: true}, {Login: true, Recovery: true}} {
		_, err := New(dbIDs, DefaultNormalizer{}, "u-1", KindPhone, "+14155550123", uses, false, time.Time{}, now)
		if !errors.Is(err, ErrVerificationRequired) || !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("New(unverified, %+v) err = %v, want ErrVerificationRequired/sdk.ErrConflict", uses, err)
		}
	}

	// Notification-only unverified is permitted.
	id, err := New(dbIDs, DefaultNormalizer{}, "u-1", KindPhone, "+14155550123",
		Uses{Notification: true}, false, time.Time{}, now)
	if err != nil {
		t.Fatalf("New(notification-only unverified) err = %v, want nil", err)
	}
	if id.Verified() {
		t.Error("notification-only identifier reports Verified() true")
	}
}

func TestNew_UnknownKind(t *testing.T) {
	_, err := New(dbIDs, DefaultNormalizer{}, "u-1", Kind("sms"), "+14155550123",
		Uses{Notification: true}, false, time.Time{}, time.Now())
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("New(unknown kind) err = %v, want ErrUnknownKind", err)
	}
}

// NewRegistrationEmail is the single verification-invariant exception: an
// unverified email carrying login and recovery while its challenge is pending.
func TestNewRegistrationEmail_UnverifiedButLoginRecoveryEnabled(t *testing.T) {
	now := time.Now().UTC()
	_, err := New(dbIDs, DefaultNormalizer{}, "", KindEmail, "new@example.com",
		Uses{Login: true, Recovery: true, Notification: true}, true, time.Time{}, now)
	if !errors.Is(err, ErrVerificationRequired) {
		t.Fatalf("New with unverified login/recovery err = %v, want ErrVerificationRequired (guards the exception)", err)
	}

	reg, err := NewRegistrationEmail(dbIDs, DefaultNormalizer{}, "", "  New@Example.com ", now)
	if err != nil {
		t.Fatalf("NewRegistrationEmail err = %v, want nil", err)
	}
	if reg.Verified() {
		t.Error("registration email reports Verified() true, want false (challenge pending)")
	}
	if !reg.LoginEnabled || !reg.RecoveryEnabled || !reg.NotificationEnabled || !reg.IsPrimary {
		t.Errorf("registration email uses/primary = %+v/%v, want all true", reg.CurrentUses(), reg.IsPrimary)
	}
	if reg.NormalizedValue != "new@example.com" {
		t.Errorf("NormalizedValue = %q, want normalized", reg.NormalizedValue)
	}
}

func TestSetUses_VerificationInvariant(t *testing.T) {
	now := time.Now().UTC()
	id, err := New(dbIDs, DefaultNormalizer{}, "u-1", KindPhone, "+14155550123",
		Uses{Notification: true}, false, time.Time{}, now)
	if err != nil {
		t.Fatalf("New err = %v", err)
	}

	// Cannot enable login while unverified.
	if err := id.SetUses(Uses{Login: true, Notification: true}, now); !errors.Is(err, ErrVerificationRequired) {
		t.Fatalf("SetUses(login) on unverified err = %v, want ErrVerificationRequired", err)
	}

	// After verification, login/recovery may be enabled.
	id.Verify(now)
	if !id.Verified() {
		t.Fatal("Verify did not set VerifiedAt")
	}
	if err := id.SetUses(Uses{Login: true, Recovery: true, Notification: true}, now); err != nil {
		t.Fatalf("SetUses after verify err = %v, want nil", err)
	}
	if !id.LoginEnabled || !id.RecoveryEnabled {
		t.Errorf("uses after SetUses = %+v, want login+recovery", id.CurrentUses())
	}
}

func TestRetire_MarksReplacedNotDeleted(t *testing.T) {
	now := time.Now().UTC()
	id, err := New(dbIDs, DefaultNormalizer{}, "u-1", KindEmail, "a@example.com",
		Uses{Notification: true}, false, now, now)
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	if !id.Active() {
		t.Fatal("fresh identifier not Active()")
	}
	retiredAt := now.Add(time.Minute)
	id.Retire(retiredAt)
	if id.Active() {
		t.Error("retired identifier still Active()")
	}
	if !id.ReplacedAt.Equal(retiredAt) {
		t.Errorf("ReplacedAt = %v, want %v", id.ReplacedAt, retiredAt)
	}
}
