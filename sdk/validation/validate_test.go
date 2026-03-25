package validation

import (
	"regexp"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// =============================================================================
// String Validators
// =============================================================================

func TestRequired(t *testing.T) {
	if err := Required("name", "alice"); err != nil {
		t.Errorf("Required(valid) = %v, want nil", err)
	}
	if err := Required("name", ""); err == nil {
		t.Error("Required(empty) = nil, want error")
	}
	if err := Required("name", "   "); err == nil {
		t.Error("Required(whitespace) = nil, want error")
	}
}

func TestRequiredPtr(t *testing.T) {
	if err := RequiredPtr("name", ptr("alice")); err != nil {
		t.Errorf("RequiredPtr(valid) = %v, want nil", err)
	}
	if err := RequiredPtr("name", nil); err == nil {
		t.Error("RequiredPtr(nil) = nil, want error")
	}
	if err := RequiredPtr("name", ptr("")); err == nil {
		t.Error("RequiredPtr(empty) = nil, want error")
	}
}

func TestMinLength(t *testing.T) {
	if err := MinLength("pw", "abcde", 3); err != nil {
		t.Errorf("MinLength(5, min=3) = %v, want nil", err)
	}
	if err := MinLength("pw", "ab", 3); err == nil {
		t.Error("MinLength(2, min=3) = nil, want error")
	}
}

func TestMaxLength(t *testing.T) {
	if err := MaxLength("bio", "abc", 5); err != nil {
		t.Errorf("MaxLength(3, max=5) = %v, want nil", err)
	}
	if err := MaxLength("bio", "abcdef", 5); err == nil {
		t.Error("MaxLength(6, max=5) = nil, want error")
	}
}

func TestMinLengthPtr(t *testing.T) {
	if err := MinLengthPtr("pw", nil, 3); err != nil {
		t.Errorf("MinLengthPtr(nil) = %v, want nil", err)
	}
	if err := MinLengthPtr("pw", ptr("ab"), 3); err == nil {
		t.Error("MinLengthPtr(2, min=3) = nil, want error")
	}
}

func TestMaxLengthPtr(t *testing.T) {
	if err := MaxLengthPtr("bio", nil, 5); err != nil {
		t.Errorf("MaxLengthPtr(nil) = %v, want nil", err)
	}
	if err := MaxLengthPtr("bio", ptr("abcdef"), 5); err == nil {
		t.Error("MaxLengthPtr(6, max=5) = nil, want error")
	}
}

func TestOneOf(t *testing.T) {
	if err := OneOf("role", "admin", "admin", "user"); err != nil {
		t.Errorf("OneOf(valid) = %v, want nil", err)
	}
	if err := OneOf("role", "hacker", "admin", "user"); err == nil {
		t.Error("OneOf(invalid) = nil, want error")
	}
	if err := OneOf("role", "", "admin", "user"); err != nil {
		t.Errorf("OneOf(empty) = %v, want nil (empty passes)", err)
	}
}

func TestOneOfPtr(t *testing.T) {
	if err := OneOfPtr("role", nil, "admin", "user"); err != nil {
		t.Errorf("OneOfPtr(nil) = %v, want nil", err)
	}
	if err := OneOfPtr("role", ptr("hacker"), "admin", "user"); err == nil {
		t.Error("OneOfPtr(invalid) = nil, want error")
	}
}

func TestEmail(t *testing.T) {
	if err := Email("email", "user@example.com"); err != nil {
		t.Errorf("Email(valid) = %v, want nil", err)
	}
	if err := Email("email", "not-an-email"); err == nil {
		t.Error("Email(invalid) = nil, want error")
	}
	if err := Email("email", ""); err != nil {
		t.Errorf("Email(empty) = %v, want nil (empty passes)", err)
	}
}

func TestEmailPtr(t *testing.T) {
	if err := EmailPtr("email", nil); err != nil {
		t.Errorf("EmailPtr(nil) = %v, want nil", err)
	}
	if err := EmailPtr("email", ptr("bad")); err == nil {
		t.Error("EmailPtr(invalid) = nil, want error")
	}
}

func TestUUID(t *testing.T) {
	if err := UUID("id", "550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Errorf("UUID(valid) = %v, want nil", err)
	}
	if err := UUID("id", "not-a-uuid"); err == nil {
		t.Error("UUID(invalid) = nil, want error")
	}
	if err := UUID("id", ""); err != nil {
		t.Errorf("UUID(empty) = %v, want nil (empty passes)", err)
	}
}

func TestUUIDPtr(t *testing.T) {
	if err := UUIDPtr("id", nil); err != nil {
		t.Errorf("UUIDPtr(nil) = %v, want nil", err)
	}
	if err := UUIDPtr("id", ptr("bad")); err == nil {
		t.Error("UUIDPtr(invalid) = nil, want error")
	}
}

func TestURL(t *testing.T) {
	if err := URL("website", "https://example.com"); err != nil {
		t.Errorf("URL(valid) = %v, want nil", err)
	}
	if err := URL("website", "not-a-url"); err == nil {
		t.Error("URL(no scheme/host) = nil, want error")
	}
	if err := URL("website", ""); err != nil {
		t.Errorf("URL(empty) = %v, want nil (empty passes)", err)
	}
}

func TestURLPtr(t *testing.T) {
	if err := URLPtr("website", nil); err != nil {
		t.Errorf("URLPtr(nil) = %v, want nil", err)
	}
	if err := URLPtr("website", ptr("bad")); err == nil {
		t.Error("URLPtr(invalid) = nil, want error")
	}
}

func TestMatches(t *testing.T) {
	phone := regexp.MustCompile(`^\+?[0-9]{10,15}$`)

	if err := Matches("phone", "+1234567890", phone, "a valid phone number"); err != nil {
		t.Errorf("Matches(valid) = %v, want nil", err)
	}
	if err := Matches("phone", "abc", phone, "a valid phone number"); err == nil {
		t.Error("Matches(invalid) = nil, want error")
	}
	if err := Matches("phone", "", phone, "a valid phone number"); err != nil {
		t.Errorf("Matches(empty) = %v, want nil (empty passes)", err)
	}
}

func TestMatchesPtr(t *testing.T) {
	phone := regexp.MustCompile(`^\+?[0-9]{10,15}$`)

	if err := MatchesPtr("phone", nil, phone, "a valid phone number"); err != nil {
		t.Errorf("MatchesPtr(nil) = %v, want nil", err)
	}
	if err := MatchesPtr("phone", ptr("abc"), phone, "a valid phone number"); err == nil {
		t.Error("MatchesPtr(invalid) = nil, want error")
	}
}

// =============================================================================
// Numeric Validators
// =============================================================================

func TestMin(t *testing.T) {
	if err := Min("age", 18, 13); err != nil {
		t.Errorf("Min(18, min=13) = %v, want nil", err)
	}
	if err := Min("age", 10, 13); err == nil {
		t.Error("Min(10, min=13) = nil, want error")
	}
}

func TestMinPtr(t *testing.T) {
	if err := MinPtr("age", nil, 13); err != nil {
		t.Errorf("MinPtr(nil) = %v, want nil", err)
	}
	if err := MinPtr("age", ptr(10), 13); err == nil {
		t.Error("MinPtr(10, min=13) = nil, want error")
	}
}

func TestMax(t *testing.T) {
	if err := Max("qty", 5, 10); err != nil {
		t.Errorf("Max(5, max=10) = %v, want nil", err)
	}
	if err := Max("qty", 15, 10); err == nil {
		t.Error("Max(15, max=10) = nil, want error")
	}
}

func TestMaxPtr(t *testing.T) {
	if err := MaxPtr("qty", nil, 10); err != nil {
		t.Errorf("MaxPtr(nil) = %v, want nil", err)
	}
	if err := MaxPtr("qty", ptr(15), 10); err == nil {
		t.Error("MaxPtr(15, max=10) = nil, want error")
	}
}

func TestRange(t *testing.T) {
	if err := Range("score", 50, 0, 100); err != nil {
		t.Errorf("Range(50, 0-100) = %v, want nil", err)
	}
	if err := Range("score", -1, 0, 100); err == nil {
		t.Error("Range(-1, 0-100) = nil, want error")
	}
	if err := Range("score", 101, 0, 100); err == nil {
		t.Error("Range(101, 0-100) = nil, want error")
	}
}

func TestPositive(t *testing.T) {
	if err := Positive("count", 1); err != nil {
		t.Errorf("Positive(1) = %v, want nil", err)
	}
	if err := Positive("count", 0); err == nil {
		t.Error("Positive(0) = nil, want error")
	}
	if err := Positive("count", -1); err == nil {
		t.Error("Positive(-1) = nil, want error")
	}
}

func TestPositivePtr(t *testing.T) {
	if err := PositivePtr("count", nil); err != nil {
		t.Errorf("PositivePtr(nil) = %v, want nil", err)
	}
	if err := PositivePtr("count", ptr(0)); err == nil {
		t.Error("PositivePtr(0) = nil, want error")
	}
}

// =============================================================================
// Collection Validators
// =============================================================================

func TestNotEmpty(t *testing.T) {
	if err := NotEmpty("tags", []string{"go"}); err != nil {
		t.Errorf("NotEmpty(non-empty) = %v, want nil", err)
	}
	if err := NotEmpty[string]("tags", nil); err == nil {
		t.Error("NotEmpty(nil) = nil, want error")
	}
	if err := NotEmpty("tags", []string{}); err == nil {
		t.Error("NotEmpty(empty) = nil, want error")
	}
}

func TestMinItems(t *testing.T) {
	if err := MinItems("tags", []string{"a", "b"}, 2); err != nil {
		t.Errorf("MinItems(2, min=2) = %v, want nil", err)
	}
	if err := MinItems("tags", []string{"a"}, 2); err == nil {
		t.Error("MinItems(1, min=2) = nil, want error")
	}
}

func TestMaxItems(t *testing.T) {
	if err := MaxItems("tags", []string{"a", "b"}, 3); err != nil {
		t.Errorf("MaxItems(2, max=3) = %v, want nil", err)
	}
	if err := MaxItems("tags", []string{"a", "b", "c", "d"}, 3); err == nil {
		t.Error("MaxItems(4, max=3) = nil, want error")
	}
}

// =============================================================================
// Common Validators
// =============================================================================

func TestSlug(t *testing.T) {
	if err := Slug("slug", "hello-world"); err != nil {
		t.Errorf("Slug(valid) = %v, want nil", err)
	}
	if err := Slug("slug", "abc123"); err != nil {
		t.Errorf("Slug(no hyphens) = %v, want nil", err)
	}
	if err := Slug("slug", "Hello-World"); err == nil {
		t.Error("Slug(uppercase) = nil, want error")
	}
	if err := Slug("slug", "hello world"); err == nil {
		t.Error("Slug(spaces) = nil, want error")
	}
	if err := Slug("slug", "-leading"); err == nil {
		t.Error("Slug(leading hyphen) = nil, want error")
	}
	if err := Slug("slug", ""); err != nil {
		t.Errorf("Slug(empty) = %v, want nil (empty passes)", err)
	}
}

func TestPasswordStrength(t *testing.T) {
	if err := PasswordStrength("password", "Str0ng!Pass"); err != nil {
		t.Errorf("PasswordStrength(strong) = %v, want nil", err)
	}
	if err := PasswordStrength("password", ""); err != nil {
		t.Errorf("PasswordStrength(empty) = %v, want nil (empty passes)", err)
	}
	if err := PasswordStrength("password", "short"); err == nil {
		t.Error("PasswordStrength(weak) = nil, want error")
	}
	if err := PasswordStrength("password", "alllowercase1!"); err == nil {
		t.Error("PasswordStrength(no uppercase) = nil, want error")
	}
	if err := PasswordStrength("password", "ALLUPPERCASE1!"); err == nil {
		t.Error("PasswordStrength(no lowercase) = nil, want error")
	}
	if err := PasswordStrength("password", "NoDigits!!"); err == nil {
		t.Error("PasswordStrength(no digit) = nil, want error")
	}
	if err := PasswordStrength("password", "NoSpecial1a"); err == nil {
		t.Error("PasswordStrength(no special) = nil, want error")
	}
}

func TestPasswordsMatch(t *testing.T) {
	if err := PasswordsMatch("abc", "abc"); err != nil {
		t.Errorf("PasswordsMatch(same) = %v, want nil", err)
	}
	if err := PasswordsMatch("abc", "xyz"); err == nil {
		t.Error("PasswordsMatch(different) = nil, want error")
	}
}

// =============================================================================
// IfSet
// =============================================================================

func TestIfSet(t *testing.T) {
	// Nil pointer — skip validation.
	if err := IfSet[string](nil, func(v string) error {
		return Required("field", v)
	}); err != nil {
		t.Errorf("IfSet(nil) = %v, want nil", err)
	}

	// Non-nil valid value.
	if err := IfSet(ptr("hello"), func(v string) error {
		return MinLength("field", v, 3)
	}); err != nil {
		t.Errorf("IfSet(valid) = %v, want nil", err)
	}

	// Non-nil invalid value.
	if err := IfSet(ptr("hi"), func(v string) error {
		return MinLength("field", v, 3)
	}); err == nil {
		t.Error("IfSet(invalid) = nil, want error")
	}
}
