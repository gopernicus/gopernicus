package session

// AssuranceLevel is an honest authenticator-assurance classification in the NIST
// SP 800-63B sense. Auth v3 ships AAL1 methods only — password, email magic
// link/code, SMS OTP, and ordinary OAuth are single-factor as actually provided.
// AAL2/AAL3 are declared but unused, reserved for the auth-v4 MFA add-on
// (design §12.1), so no v3 seam assumes one method equals one assurance unit.
type AssuranceLevel string

const (
	// AssuranceUnknown is the zero value: no assurance recorded.
	AssuranceUnknown AssuranceLevel = ""
	// AssuranceAAL1 is single-factor assurance — every method v3 provides.
	AssuranceAAL1 AssuranceLevel = "aal1"
	// AssuranceAAL2 is reserved for auth-v4 multi-factor methods.
	AssuranceAAL2 AssuranceLevel = "aal2"
	// AssuranceAAL3 is reserved for auth-v4 hardware-backed methods.
	AssuranceAAL3 AssuranceLevel = "aal3"
)

// MethodKind enumerates the authentication methods v3 can present on a session
// or a recent-authentication grant. It is the discovery vocabulary policy
// (design §5.6) reasons over; auth-v4 adds passkey/TOTP/recovery-code kinds
// without changing these.
type MethodKind string

const (
	// MethodPassword is a knowledge-factor password.
	MethodPassword MethodKind = "password"
	// MethodEmailLink is a magic link delivered to a verified email identifier.
	MethodEmailLink MethodKind = "email_link"
	// MethodEmailCode is a one-time code delivered to a verified email identifier.
	MethodEmailCode MethodKind = "email_code"
	// MethodSMSCode is a one-time code delivered over SMS (a PSTN channel).
	MethodSMSCode MethodKind = "sms_code"
	// MethodOAuth is an ordinary OAuth/OIDC provider sign-in.
	MethodOAuth MethodKind = "oauth"
)

// AuthenticationMethod describes one method's honest security properties so
// CredentialPolicy (design §5.6) can reason over a method set rather than a
// scalar count. No v3 method is phishing- or replay-resistant; PSTN marks a
// channel that rides the public switched telephone network (SMS), which policy
// treats as restricted rather than strong.
type AuthenticationMethod struct {
	Kind              MethodKind
	Assurance         AssuranceLevel
	PhishingResistant bool
	ReplayResistant   bool
	PSTN              bool
}

// methodDescriptors is the frozen honest property table for the v3 methods.
// Every entry is AAL1; only SMS is PSTN; none is phishing- or replay-resistant.
var methodDescriptors = map[MethodKind]AuthenticationMethod{
	MethodPassword:  {Kind: MethodPassword, Assurance: AssuranceAAL1},
	MethodEmailLink: {Kind: MethodEmailLink, Assurance: AssuranceAAL1},
	MethodEmailCode: {Kind: MethodEmailCode, Assurance: AssuranceAAL1},
	MethodSMSCode:   {Kind: MethodSMSCode, Assurance: AssuranceAAL1, PSTN: true},
	MethodOAuth:     {Kind: MethodOAuth, Assurance: AssuranceAAL1},
}

// DescribeMethod returns the frozen descriptor for a method kind. The second
// result is false for an unknown kind — e.g. an auth-v4 method kind until it
// registers its own descriptor — so callers never silently treat an unknown
// method as AAL1.
func DescribeMethod(kind MethodKind) (AuthenticationMethod, bool) {
	d, ok := methodDescriptors[kind]
	return d, ok
}
