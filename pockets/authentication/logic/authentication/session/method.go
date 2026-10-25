package session

// AssuranceLevel is an honest authenticator-assurance classification in the NIST
// SP 800-63B sense. Password, email magic link/code, SMS OTP, ordinary OAuth and
// a passkey without user verification are single-factor (AAL1) as actually
// provided. AAL2 comes from a user-verified passkey, from a TOTP or recovery-code
// step-up of a live session (the second factor composed onto the primary), or
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
	// AssuranceAAL2 is multi-factor assurance: a user-verified passkey, a TOTP or
	// recovery-code step-up, or an external token whose provider asserted
	// multi-factor authentication.
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
// policy (design §5.6) reasons over.
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
	// MethodTOTP is an authenticator-app code (RFC 6238): possession of the
	// shared secret, a second factor only — never a sign-in method.
	MethodTOTP MethodKind = "totp"
	// MethodRecoveryCode is a single-use recovery code standing in for the
	// authenticator app.
	MethodRecoveryCode MethodKind = "recovery_code"
	// MethodExternal marks an external provider token's evidence on its
	// Credential. It has no descriptor and never appears on a session row or in
	// an access token.
	MethodExternal MethodKind = "external"
)

// AuthenticationMethod describes one method's honest security properties so
// CredentialPolicy (design §5.6) can reason over a method set rather than a
// scalar count. Only the passkey kinds are phishing-resistant; they and the
// one-time-code factors (TOTP, recovery codes) are replay-resistant; PSTN
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
// user-verified passkey is AAL1 on its own; only SMS is PSTN; only the passkeys
// are phishing-resistant; the passkeys, TOTP and recovery codes are
// replay-resistant.
var methodDescriptors = map[MethodKind]AuthenticationMethod{
	MethodPassword:          {Kind: MethodPassword, Assurance: AssuranceAAL1},
	MethodEmailLink:         {Kind: MethodEmailLink, Assurance: AssuranceAAL1},
	MethodSMSLink:           {Kind: MethodSMSLink, Assurance: AssuranceAAL1, PSTN: true},
	MethodEmailCode:         {Kind: MethodEmailCode, Assurance: AssuranceAAL1},
	MethodSMSCode:           {Kind: MethodSMSCode, Assurance: AssuranceAAL1, PSTN: true},
	MethodOAuth:             {Kind: MethodOAuth, Assurance: AssuranceAAL1},
	MethodPasskey:           {Kind: MethodPasskey, Assurance: AssuranceAAL2, PhishingResistant: true, ReplayResistant: true},
	MethodPasskeyUnverified: {Kind: MethodPasskeyUnverified, Assurance: AssuranceAAL1, PhishingResistant: true, ReplayResistant: true},
	MethodTOTP:              {Kind: MethodTOTP, Assurance: AssuranceAAL1, ReplayResistant: true},
	MethodRecoveryCode:      {Kind: MethodRecoveryCode, Assurance: AssuranceAAL1, ReplayResistant: true},
}

// DescribeMethod returns the frozen descriptor for a method kind. The second
// result is false for an unknown kind — e.g. an auth-v4 method kind until it
// registers its own descriptor — so callers never silently treat an unknown
// method as AAL1.
func DescribeMethod(kind MethodKind) (AuthenticationMethod, bool) {
	d, ok := methodDescriptors[kind]
	return d, ok
}
