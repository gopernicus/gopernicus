package identifier

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// e164Pattern is strict naive E.164 (design §2.2): a required leading '+', a
// non-zero leading digit, then 1–14 more digits (15 total maximum). No country
// inference — a number without a '+' fails rather than being localized.
var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// Normalizer produces the single canonical form of an identifier value used for
// persistence, lookup, invitations, rate-limit keys, and audit details (design
// §2.2). Its method set matches the feature's authentication.IdentifierNormalizer
// so one injected value satisfies both the domain constructors and the public
// config seam. The bundled DefaultNormalizer is the strict default; a host may
// inject a custom policy (for example one performing full IDNA ToASCII, which the
// stdlib-only feature core cannot).
type Normalizer interface {
	Normalize(kind, value string) (string, error)
}

// DefaultNormalizer is the bundled strict identifier normalizer (design §2.2).
//
// Email: an addr-spec only (display-name and angle-addr forms are rejected),
// surrounding whitespace trimmed, the domain lowercased, and the local part
// folded to lowercase by default. It applies NO provider-specific rewriting —
// Gmail-style dot stripping and "+tag" removal are deliberately absent, because
// silently rewriting a provider's addresses is a policy a host opts into, not a
// framework default.
//
// Documented domain/IDNA policy: the domain is canonicalized by Unicode-aware
// case folding (strings.ToLower) only. Punycode/IDNA mapping (ToASCII) is NOT
// applied — Go's standard library has no IDNA implementation, and the feature
// core is stdlib-only (it may not import golang.org/x/net/idna). A host needing
// full IDNA canonicalization injects a custom Normalizer. Two Unicode domains
// that differ only by a mapping this policy does not perform are treated as
// distinct; the folding this policy does perform is idempotent.
//
// Phone: strict naive E.164 (see e164Pattern). Visual separators (spaces, NBSP,
// hyphens, parentheses, dots) are stripped, then the result must match
// ^\+[1-9][0-9]{1,14}$ with the leading '+' required and no country inference.
type DefaultNormalizer struct {
	// PreserveLocalPartCase disables email local-part case folding. The zero
	// value (false) folds the local part to lowercase — the v1-compatible
	// default recorded in the upgrade note (v1 lowercased the whole address). A
	// host that must preserve local-part case sets this true.
	PreserveLocalPartCase bool
}

// compile-time proof the bundled default satisfies the domain seam.
var _ Normalizer = DefaultNormalizer{}

// Normalize dispatches on the closed kind vocabulary; an unknown kind is
// ErrUnknownKind (wrapping sdk.ErrInvalidInput).
func (n DefaultNormalizer) Normalize(kind, value string) (string, error) {
	switch Kind(kind) {
	case KindEmail:
		return normalizeEmail(value, n.PreserveLocalPartCase)
	case KindPhone:
		return normalizePhone(value)
	default:
		return "", fmt.Errorf("identifier: normalize %q: %w", kind, ErrUnknownKind)
	}
}

// normalizeEmail validates an addr-spec-only email and canonicalizes it per the
// DefaultNormalizer policy. It rejects display-name and angle-addr forms.
func normalizeEmail(value string, preserveLocalCase bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("identifier: email is required: %w", sdk.ErrInvalidInput)
	}
	// Reject display-name and angle-addr forms before parsing: mail.ParseAddress
	// accepts both `Bob <bob@x>` (Name set) and `<bob@x>` (Name empty), so the
	// bracket guard closes the empty-name angle-addr the Name check alone misses.
	if strings.ContainsAny(value, "<>") {
		return "", fmt.Errorf("identifier: email must be a bare addr-spec, not a display-name form: %w", sdk.ErrInvalidInput)
	}
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return "", fmt.Errorf("identifier: invalid email address: %w", sdk.ErrInvalidInput)
	}
	if addr.Name != "" {
		return "", fmt.Errorf("identifier: email must be a bare addr-spec, not a display-name form: %w", sdk.ErrInvalidInput)
	}
	at := strings.LastIndex(addr.Address, "@")
	if at <= 0 || at == len(addr.Address)-1 {
		return "", fmt.Errorf("identifier: invalid email address: %w", sdk.ErrInvalidInput)
	}
	local := addr.Address[:at]
	domain := strings.ToLower(addr.Address[at+1:])
	if !preserveLocalCase {
		local = strings.ToLower(local)
	}
	return local + "@" + domain, nil
}

// normalizePhone strips visual separators and validates strict naive E.164.
func normalizePhone(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("identifier: phone is required: %w", sdk.ErrInvalidInput)
	}
	cleaned := stripPhoneSeparators(value)
	if !e164Pattern.MatchString(cleaned) {
		return "", fmt.Errorf("identifier: %q is not strict E.164 (need a leading + and 1–15 digits, no country inference): %w", value, sdk.ErrInvalidInput)
	}
	return cleaned, nil
}

// stripPhoneSeparators removes the visual separators permitted in human-entered
// phone numbers, leaving the digits and any leading '+'.
func stripPhoneSeparators(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', ' ', '-', '(', ')', '.':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
