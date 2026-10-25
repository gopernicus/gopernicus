package session

// AssuranceLevel is an honest authenticator-assurance classification in the NIST
// SP 800-63B sense. Password, email magic link/code, SMS OTP, ordinary OAuth and
// a passkey without user verification are single-factor (AAL1) as actually
// provided. AAL2 comes from a user-verified passkey (the only native source) or
// from provider evidence: an external token whose provider asserted multi-factor
// authentication (sdkauth.Identity.MFA). AAL3 is declared but unused, so no seam
// assumes one method equals one assurance unit.
type AssuranceLevel string

const (
	// AssuranceUnknown is the zero value: no assurance recorded.
	AssuranceUnknown AssuranceLevel = ""
	// AssuranceAAL1 is single-factor assurance — every native method, and an
	// external token whose provider asserted no second factor.
	AssuranceAAL1 AssuranceLevel = "aal1"
	// AssuranceAAL2 is multi-factor assurance: a user-verified passkey, or an
	// external token whose provider asserted multi-factor authentication.
	AssuranceAAL2 AssuranceLevel = "aal2"
	// AssuranceAAL3 is reserved for hardware-backed methods; nothing produces it.
	AssuranceAAL3 AssuranceLevel = "aal3"
)

// AtLeast reports whether a meets min under unknown < aal1 < aal2 < aal3. An
// unrecognised level ranks as unknown, so it never satisfies a floor above it.
func (a AssuranceLevel) AtLeast(min AssuranceLevel) bool {
	return a.rank() >= min.rank()
}

func (a AssuranceLevel) rank() int {
	switch a {
	case AssuranceAAL1:
		return 1
	case AssuranceAAL2:
		return 2
	case AssuranceAAL3:
		return 3
	default:
		return 0
	}
}

// MethodKind enumerates the authentication methods that can present on a
// session or a recent-authentication grant. It is the discovery vocabulary
// policy (design §5.6) reasons over; TOTP and recovery-code kinds join it
// without changing these.
type MethodKind string

const (
	// MethodPassword is a knowledge-factor password.
	MethodPassword MethodKind = "password"
	// MethodEmailLink is a magic link delivered to a verified email identifier.
	MethodEmailLink MethodKind = "email_link"
	// MethodSMSLink is a sign-in link delivered over SMS.
	MethodSMSLink MethodKind = "sms_link"
	// MethodEmailCode is a one-time code delivered to a verified email identifier.
	MethodEmailCode MethodKind = "email_code"
	// MethodSMSCode is a one-time code delivered over SMS (a PSTN channel).
	MethodSMSCode MethodKind = "sms_code"
	// MethodOAuth is an ordinary OAuth/OIDC provider sign-in.
	MethodOAuth MethodKind = "oauth"
	// MethodPasskey is a WebAuthn passkey assertion whose authenticator data
	// reported user verification (a PIN or biometric): possession plus a second
	// factor, AAL2.
	MethodPasskey MethodKind = "passkey"
	// MethodPasskeyUnverified is a passkey assertion with user presence only:
	// possession alone, AAL1.
	MethodPasskeyUnverified MethodKind = "passkey_unverified"
)

// AuthenticationMethod describes one method's honest security properties so
// CredentialPolicy (design §5.6) can reason over a method set rather than a
// scalar count. Only the passkey kinds are phishing- and replay-resistant; PSTN
// marks a channel that rides the public switched telephone network (SMS), which
// policy treats as restricted rather than strong.
type AuthenticationMethod struct {
	Kind              MethodKind
	Assurance         AssuranceLevel
	PhishingResistant bool
	ReplayResistant   bool
	PSTN              bool
}

// methodDescriptors is the frozen honest property table. Every entry but the
// user-verified passkey is AAL1; only SMS is PSTN; only the passkeys are
// phishing- and replay-resistant.
var methodDescriptors = map[MethodKind]AuthenticationMethod{
	MethodPassword:          {Kind: MethodPassword, Assurance: AssuranceAAL1},
	MethodEmailLink:         {Kind: MethodEmailLink, Assurance: AssuranceAAL1},
	MethodSMSLink:           {Kind: MethodSMSLink, Assurance: AssuranceAAL1, PSTN: true},
	MethodEmailCode:         {Kind: MethodEmailCode, Assurance: AssuranceAAL1},
	MethodSMSCode:           {Kind: MethodSMSCode, Assurance: AssuranceAAL1, PSTN: true},
	MethodOAuth:             {Kind: MethodOAuth, Assurance: AssuranceAAL1},
	MethodPasskey:           {Kind: MethodPasskey, Assurance: AssuranceAAL2, PhishingResistant: true, ReplayResistant: true},
	MethodPasskeyUnverified: {Kind: MethodPasskeyUnverified, Assurance: AssuranceAAL1, PhishingResistant: true, ReplayResistant: true},
}

// DescribeMethod returns the frozen descriptor for a method kind. The second
// result is false for an unknown kind — e.g. an auth-v4 method kind until it
// registers its own descriptor — so callers never silently treat an unknown
// method as AAL1.
func DescribeMethod(kind MethodKind) (AuthenticationMethod, bool) {
	d, ok := methodDescriptors[kind]
	return d, ok
}
