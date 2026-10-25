package authentication

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// causeExternalNoMFA is the single-factor cause of an external provider that
// admits tokens below AAL2.
const causeExternalNoMFA = "external_no_mfa"

// The gates that refuse a credential below AAL2. Each names itself in the
// mfa_required_denied row's gate detail.
const (
	// MFAGateRoute is the RequirePrincipal RequireMFA option.
	MFAGateRoute MFAGate = "route"
	// MFAGateAdmin is the bundled /auth/admin/users* gate MFAConfig admins
	// enables, run after the host's user-administration check admits the
	// caller.
	MFAGateAdmin MFAGate = "admin"
	// MFAGateProvider is ExternalProvider.RequireMFA, checked in Verify before
	// the subject resolves.
	MFAGateProvider MFAGate = "provider"
	// MFAGateMachine is the bundled machine-identity lifecycle gate MFAConfig
	// admins enables beside passkeys, run after AdministrationConfig's
	// MachineRoutesGate admits the caller.
	MFAGateMachine MFAGate = "machine"
)

// The MFAConfig.Methods values: the AAL2 sources a host admits.
const (
	// MFAMethodPasskey is a user-verified passkey sign-in or step-up.
	MFAMethodPasskey MFAMethod = "passkey"
	// MFAMethodIdP is an external provider that asserted multi-factor
	// authentication.
	MFAMethodIdP MFAMethod = "idp"
	// MFAMethodTOTP is a TOTP step-up; a recovery-code step-up counts under it.
	MFAMethodTOTP MFAMethod = "totp"
)

// The mfa_required_denied reasons.
const (
	// MFADenialAssurance is a credential below AAL2.
	MFADenialAssurance = "assurance"
	// MFADenialMethod is an AAL2 credential whose sources MFAConfig.Methods does
	// not admit.
	MFADenialMethod = "method"
)

var (
	// ErrMFARequired denies a credential below AAL2 where multi-factor
	// authentication is required. It matches sdk.ErrUnauthorized; the HTTP
	// adapter answers it with the RFC 9470 insufficient_user_authentication
	// challenge.
	ErrMFARequired = fmt.Errorf("authentication: multi-factor authentication is required: %w", sdk.ErrUnauthorized)
	// ErrSingleFactorUnacknowledged is returned in production when the host
	// admits single-factor sign-in without SingleFactorAcknowledged: through
	// NewExternalAuthenticator, a provider without RequireMFA; through the
	// pocket root's New, native password, passwordless or OAuth login, or an
	// external provider without RequireMFA while MFAConfig.Required is none.
	// The wrapping error names the acknowledgement and the causes.
	ErrSingleFactorUnacknowledged = errors.New("authentication: production refuses single-factor sign-in unless SingleFactorAcknowledged is set")
)

// MFAGate names the gate that refused a credential below AAL2.
type MFAGate string

// MFAMethod names one AAL2 source: passkey, idp or totp.
type MFAMethod string

// MFADenial is one refused multi-factor requirement: the gate that refused it,
// the principal and the credential it refused, and why (Reason: assurance or
// method; empty reads as assurance). RecordMFADenied records it as an
// mfa_required_denied row carrying bounded values only — the gate, the
// credential kind, the provider name, the presented and required assurance, the
// reason and the credential's AAL2 source — never a claim, an amr value or the
// token.
type MFADenial struct {
	Gate       MFAGate
	Principal  Principal
	Credential Credential
	Reason     string
}

// MFASources reports the AAL2 sources behind cred: idp for an external token;
// for an access token passkey when its methods carry a user-verified passkey
// and totp when they carry a TOTP or recovery code. An access token without a
// methods claim (minted before it existed) has none.
func MFASources(cred Credential) []MFAMethod {
	if cred.Kind == CredentialExternalToken {
		return []MFAMethod{MFAMethodIdP}
	}
	var out []MFAMethod
	add := func(m MFAMethod) {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	for _, kind := range cred.Methods {
		switch kind {
		case session.MethodPasskey:
			add(MFAMethodPasskey)
		case session.MethodTOTP, session.MethodRecoveryCode:
			add(MFAMethodTOTP)
		}
	}
	return out
}

// MFASourceAllowed is the one source predicate every MFA gate applies beside
// the AAL2 floor: an empty allowed admits any source; otherwise one of cred's
// AAL2 sources (MFASources) must be allowed, so an access token without a
// methods claim fails closed under a restriction until its next refresh.
func MFASourceAllowed(cred Credential, allowed []MFAMethod) bool {
	if len(allowed) == 0 {
		return true
	}
	return slices.ContainsFunc(MFASources(cred), func(m MFAMethod) bool { return slices.Contains(allowed, m) })
}

// RecordMFADenied records d as an mfa_required_denied row through the
// service's recorder. The HTTP adapter calls it for the RequireMFA option and
// the admin and machine gates.
func (s *Service) RecordMFADenied(ctx context.Context, d MFADenial) {
	provider := ""
	if s.external != nil {
		provider = s.external.providerName(d.Credential)
	}
	s.recordSecurityEvent(ctx, mfaDenialEvent(d, provider))
}

// RecordMFADenied records d as an mfa_required_denied row through the
// engine's recorder. The HTTP adapter calls it for the RequireMFA option; the
// engine records its own provider gate.
func (e *ExternalAuthenticator) RecordMFADenied(ctx context.Context, d MFADenial) {
	e.events.Record(ctx, mfaDenialEvent(d, e.providerName(d.Credential)))
}

// providerName returns the wired provider name of an external credential, or
// "" for any other credential.
func (e *ExternalAuthenticator) providerName(cred Credential) string {
	if cred.Kind != CredentialExternalToken {
		return ""
	}
	return e.providers[cred.Identity.Issuer].Name
}

// mfaDenialEvent builds the bounded, budgeted mfa_required_denied row: an AAL1
// holder can drive it, so the failure-row budget keys it per client IP.
func mfaDenialEvent(d MFADenial, provider string) securityEventInput {
	details := map[string]any{
		"gate":       boundedEnum(string(d.Gate), string(MFAGateRoute), string(MFAGateAdmin), string(MFAGateMachine), string(MFAGateProvider)),
		"credential": boundedEnum(string(d.Credential.Kind), string(CredentialAccessToken), string(CredentialAPIKey), string(CredentialExternalToken)),
		"assurance":  boundedEnum(string(d.Credential.Assurance), string(session.AssuranceAAL1), string(session.AssuranceAAL2), string(session.AssuranceAAL3)),
		"required":   string(session.AssuranceAAL2),
		"reason":     boundedEnum(cmp.Or(d.Reason, MFADenialAssurance), MFADenialAssurance, MFADenialMethod),
		"method":     "none",
	}
	if sources := MFASources(d.Credential); len(sources) > 0 {
		details["method"] = string(sources[0])
	}
	if provider != "" {
		details["provider"] = provider
	}
	userID := ""
	if d.Principal.Type == PrincipalUser {
		userID = d.Principal.ID
	}
	return securityEventInput{
		UserID:   userID,
		Type:     securityevent.TypeMFARequiredDenied,
		Status:   securityevent.StatusBlocked,
		Details:  details,
		Budgeted: true,
	}
}

// boundedEnum returns value when it is one of allowed, else "unknown".
func boundedEnum(value string, allowed ...string) string {
	for _, a := range allowed {
		if value == a {
			return value
		}
	}
	return "unknown"
}

// externalAssurance maps verified provider evidence to an assurance level:
// AAL2 when the provider asserted multi-factor authentication, AAL1 otherwise.
// Nothing here produces AAL3.
func externalAssurance(identity sdkauth.Identity) session.AssuranceLevel {
	if identity.MFA {
		return session.AssuranceAAL2
	}
	return session.AssuranceAAL1
}

// checkExternalPosture is NewExternalAuthenticator's production posture: a
// provider without RequireMFA admits single-factor sign-in (cause
// external_no_mfa). Production refuses that unless the acknowledgement is set;
// an honoured acknowledgement logs one posture_waiver WARN; development WARNs
// once, naming the acknowledgement. Every provider setting RequireMFA clears it.
func checkExternalPosture(cfg ExternalConfig, mode environment.Mode, log *slog.Logger) error {
	present := false
	for _, p := range cfg.Providers {
		present = present || !p.RequireMFA
	}
	if !present {
		return nil
	}
	causes := []string{causeExternalNoMFA}
	switch {
	case cfg.SingleFactorAcknowledged:
		log.Warn("authentication: production posture requirement waived", "posture_waiver", "SingleFactorAcknowledged", "runtime_mode", mode.String(), "causes", causes)
	case mode.IsProduction():
		return fmt.Errorf("%w: set RequireMFA on every provider or ExternalConfig.SingleFactorAcknowledged (causes %v)", ErrSingleFactorUnacknowledged, causes)
	default:
		log.Warn("authentication: an external provider without RequireMFA admits single-factor sign-in; production refuses this without ExternalConfig.SingleFactorAcknowledged", "causes", causes)
	}
	return nil
}
