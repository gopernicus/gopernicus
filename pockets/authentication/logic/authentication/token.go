package authentication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// tokenClaimUserID is the access-JWT claim carrying the authenticated user's
	// id (§1.1); every access-token resolution reads it.
	tokenClaimUserID = "user_id"
	// tokenClaimSessionID is the access-JWT claim carrying the backing session's
	// app-minted id (§1.1); the Live() tier and the logout fallback read it.
	tokenClaimSessionID = "session_id"
	tokenClaimProfile   = "session_profile"
	// tokenClaimAssurance is the access JWT's authenticator assurance (aal1 or
	// aal2), signed from the session row's assurance_level. A row that recorded
	// none signs no claim; a delegated token carries one only when its issuer
	// set OAuth2 DelegatedAssurance and the grant's approval recorded a level.
	tokenClaimAssurance = "aal"
	// tokenClaimMethods is the access JWT's method kinds (password, passkey,
	// totp, …), signed from the session row's authentication methods, so an
	// MFAConfig.Methods gate can tell which factor produced its AAL2. A row
	// that recorded none signs no claim; a delegated token carries the
	// approving session's, as for tokenClaimAssurance.
	tokenClaimMethods = "methods"
	// tokenClaimMFAEnrollBy is native all's enrollment deadline (unix
	// seconds), signed on a first-party token below AAL2 whose holder has no
	// AAL2 factor. It is read on the first-party profile only.
	tokenClaimMFAEnrollBy = "mfa_enroll_by"
	// tokenClaimMFAEnrolled is true on a first-party token below AAL2 under
	// native all whose holder holds an AAL2 factor, so the floor asks for a
	// step-up rather than a refresh. It is read on the first-party profile
	// only.
	tokenClaimMFAEnrolled = "mfa_enrolled"
)

// firstPartyDelegationMarkers are the claims only a delegated token carries.
// iss and aud are judged against SessionsConfig.Issuer/Audience instead.
var firstPartyDelegationMarkers = []string{"client_id", "origin_client_id", "act"}

// ErrDelegatedCredential refuses a delegated credential at a first-party
// lifecycle boundary without changing either session or browser cookies.
var ErrDelegatedCredential = fmt.Errorf("credential is not a first-party session: %w", sdk.ErrUnauthorized)

// TokenEnabled reports whether the bearer-JWT token endpoint is available. The
// signer is now required (D3), so it is always true on a constructed Service; the
// transport keeps gating POST /auth/token on it for symmetry with the other
// subsystems.
func (s *Service) TokenEnabled() bool {
	return s.tokenSigner != nil
}

// IssueToken authenticates login-shaped credentials and mints a session-backed
// TokenPair — the API-flow twin of Login (§1.1, breaking response-contract change
// from AV6's stateless-only token). It mirrors Login's discipline exactly: it
// rate-limits FIRST on the same (email, client-IP) key BEFORE any credential
// work, returns the same generic sdk.ErrUnauthorized for every credential
// mismatch, and honors RequireVerifiedEmail (ErrEmailNotVerified, 403) AFTER
// password verification so it never leaks a verified/unverified signal.
//
// Identity resolves through the login-enabled email identifier (GetLogin), not the
// legacy email column, and the verified gate reads the identifier's proof state.
// The rate-limit IP is read from the request's client-info carrier (WithClientInfo)
// — the single source of truth for IP (design §5.1 WI4); there is no clientIP
// parameter. Every exit records a token_issued event exactly as Login records
// its login event (recordCredentialOutcome), without the attempted address.
func (s *Service) IssueToken(ctx context.Context, emailAddr, password string) (TokenPair, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return TokenPair{}, err
	}
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		s.recordCredentialOutcome(ctx, securityevent.TypeTokenIssued, "", invalidCredentials(), nil)
		return TokenPair{}, invalidCredentials()
	}

	u, _, err := s.provePassword(ctx, normalized, password)
	if err != nil {
		s.recordCredentialOutcome(ctx, securityevent.TypeTokenIssued, u.ID, err, nil)
		return TokenPair{}, err
	}

	pair, err := s.mintSession(ctx, u.ID, u.AuthRevision, s.primaryAuthentication(session.MethodPassword))
	if err != nil {
		// A deactivated account is denied as ordinary bad credentials.
		if errors.Is(err, session.ErrUserNotActive) {
			s.recordCredentialOutcome(ctx, securityevent.TypeTokenIssued, u.ID, err, nil)
		}
		return TokenPair{}, genericIfNotActive(err, invalidCredentials())
	}
	s.recordCredentialOutcome(ctx, securityevent.TypeTokenIssued, u.ID, nil, nil)
	return pair, nil
}

func (s *Service) verifyAccessClaims(raw string) (string, Credential, bool) {
	if s.tokenSigner == nil {
		return "", Credential{}, false
	}
	claims, err := s.tokenSigner.Verify(raw)
	if err != nil {
		return "", Credential{}, false
	}
	return s.parseAccessClaims(claims)
}

// parseAccessClaims reads verified claims. A first-party token must carry
// exactly the configured iss and aud (SessionsConfig.Issuer/Audience) and no
// other delegation claim; with neither configured it must carry no iss or aud.
// Both profiles admit aal and methods under the same rules: an unknown value
// refuses the token, an absent one reads AssuranceUnknown and no methods. A
// first-party token's mfa_enroll_by must be numeric and its mfa_enrolled true
// or false, or the token is refused; a delegated token's are never read.
func (s *Service) parseAccessClaims(claims map[string]any) (string, Credential, bool) {
	userID, ok := claimString(claims[tokenClaimUserID])
	if !ok {
		return "", Credential{}, false
	}
	cred := Credential{Kind: CredentialAccessToken, Profile: session.ProfileFirstParty}
	if raw, present := claims[tokenClaimSessionID]; present {
		cred.SessionID, ok = claimString(raw)
		if !ok {
			return "", Credential{}, false
		}
	}
	if raw, present := claims[tokenClaimProfile]; present {
		profile, valid := claimString(raw)
		if !valid {
			return "", Credential{}, false
		}
		cred.Profile = session.Profile(profile)
	}
	if cred.Assurance, cred.Methods, ok = claimAssurance(claims); !ok {
		return "", Credential{}, false
	}
	if cred.Profile == session.ProfileFirstParty {
		for _, name := range firstPartyDelegationMarkers {
			if _, present := claims[name]; present {
				return "", Credential{}, false
			}
		}
		if !s.firstPartyIssuerMatches(claims) {
			return "", Credential{}, false
		}
		if raw, present := claims[tokenClaimMFAEnrollBy]; present {
			if cred.MFAEnrollBy, ok = claimExpiry(raw); !ok {
				return "", Credential{}, false
			}
		}
		if raw, present := claims[tokenClaimMFAEnrolled]; present {
			if cred.MFAEnrolled, ok = raw.(bool); !ok {
				return "", Credential{}, false
			}
		}
		return userID, cred, true
	}
	if cred.Profile != session.ProfileDelegated || cred.SessionID == "" {
		return "", Credential{}, false
	}
	if cred.accessExpiresAt, ok = claimExpiry(claims["exp"]); !ok {
		return "", Credential{}, false
	}
	if cred.Issuer, ok = claimString(claims["iss"]); !ok {
		return "", Credential{}, false
	}
	if cred.ClientID, ok = claimString(claims["client_id"]); !ok {
		return "", Credential{}, false
	}
	if cred.Audiences, ok = claimAudiences(claims["aud"]); !ok || len(cred.Audiences) != 1 {
		return "", Credential{}, false
	}
	origin, hasOrigin := claims["origin_client_id"]
	actor, hasActor := claims["act"]
	if hasOrigin != hasActor {
		return "", Credential{}, false
	}
	if hasOrigin {
		if cred.OriginClientID, ok = claimString(origin); !ok {
			return "", Credential{}, false
		}
		act, valid := actor.(map[string]any)
		if !valid || len(act) != 1 {
			return "", Credential{}, false
		}
		if cred.ActorID, ok = claimString(act["sub"]); !ok || cred.ActorID != cred.ClientID {
			return "", Credential{}, false
		}
	}
	return userID, cred, true
}

// firstPartyIssuerMatches reports whether a first-party token's iss and aud
// are exactly the configured ones: present and equal when configured, absent
// when not.
func (s *Service) firstPartyIssuerMatches(claims map[string]any) bool {
	rawIssuer, hasIssuer := claims["iss"]
	if s.tokenIssuer == "" {
		if hasIssuer {
			return false
		}
	} else if issuer, ok := claimString(rawIssuer); !hasIssuer || !ok || issuer != s.tokenIssuer {
		return false
	}
	rawAudience, hasAudience := claims["aud"]
	if s.tokenAudience == "" {
		return !hasAudience
	}
	audiences, ok := claimAudiences(rawAudience)
	return hasAudience && ok && audiences[0] == s.tokenAudience
}

// claimAssurance reads the optional aal and methods claims: aal is aal1 or
// aal2, methods as claimMethods. A present claim of any other shape refuses the
// token (false); absent claims read AssuranceUnknown and no methods.
func claimAssurance(claims map[string]any) (session.AssuranceLevel, []session.MethodKind, bool) {
	var (
		level session.AssuranceLevel
		kinds []session.MethodKind
	)
	if raw, present := claims[tokenClaimAssurance]; present {
		value, valid := claimString(raw)
		level = session.AssuranceLevel(value)
		switch level {
		case session.AssuranceAAL1, session.AssuranceAAL2:
		default:
			valid = false
		}
		if !valid {
			return "", nil, false
		}
	}
	if raw, present := claims[tokenClaimMethods]; present {
		var ok bool
		if kinds, ok = claimMethods(raw); !ok {
			return "", nil, false
		}
	}
	return level, kinds, true
}

// claimMethods reads the methods claim: a non-empty array of distinct known
// session method kinds. Anything else refuses the token.
func claimMethods(raw any) ([]session.MethodKind, bool) {
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return nil, false
	}
	kinds := make([]session.MethodKind, 0, len(values))
	for _, v := range values {
		name, ok := claimString(v)
		if !ok {
			return nil, false
		}
		kind := session.MethodKind(name)
		if _, known := session.DescribeMethod(kind); !known || slices.Contains(kinds, kind) {
			return nil, false
		}
		kinds = append(kinds, kind)
	}
	return kinds, true
}

func claimString(raw any) (string, bool) {
	value, ok := raw.(string)
	return value, ok && value != "" && strings.TrimSpace(value) == value
}

func claimAudiences(raw any) ([]string, bool) {
	switch value := raw.(type) {
	case string:
		audience, ok := claimString(value)
		return []string{audience}, ok
	case []string:
		if len(value) != 1 {
			return nil, false
		}
		return claimAudiences(value[0])
	case []any:
		if len(value) != 1 {
			return nil, false
		}
		audience, ok := claimString(value[0])
		return []string{audience}, ok
	default:
		return nil, false
	}
}

func claimExpiry(raw any) (time.Time, bool) {
	var seconds float64
	switch value := raw.(type) {
	case float64:
		seconds = value
	case json.Number:
		var err error
		seconds, err = value.Float64()
		if err != nil {
			return time.Time{}, false
		}
	case int64:
		seconds = float64(value)
	case int:
		seconds = float64(value)
	default:
		return time.Time{}, false
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < -62135596800 || seconds >= 253402300800 {
		return time.Time{}, false
	}
	whole, fraction := math.Modf(seconds)
	return time.Unix(int64(whole), int64(fraction*float64(time.Second))), true
}

// firstPartyLogoutToken checks the shape even when delegated admission is disabled,
// so pairing a delegated access token with a web refresh cannot log out the web.
func (s *Service) firstPartyLogoutToken(raw string) (string, Credential, bool, error) {
	claims, err := s.tokenSigner.Verify(raw)
	if err != nil {
		// Inspection here only refuses ambiguous logout. Unverified claims never
		// identify a user or session and can never authorize a mutation.
		if unverifiedDelegatedShape(raw) {
			return "", Credential{}, false, ErrDelegatedCredential
		}
		return "", Credential{}, false, nil
	}
	userID, cred, ok := s.parseAccessClaims(claims)
	if !ok || cred.Profile != session.ProfileFirstParty {
		// Delegation is decided by shape, not admission: a first-party token
		// refused only because SessionsConfig.Issuer/Audience changed since it
		// was minted still lets the refresh token identify the session.
		if delegatedShape(claims) {
			return "", Credential{}, false, ErrDelegatedCredential
		}
		return "", Credential{}, false, nil
	}
	return userID, cred, true, nil
}

// unverifiedDelegatedShape decodes an unverified payload and applies
// delegatedShape. An oversized or undecodable payload is not first-party
// shaped enough to trust, so oversized is delegated and malformed is not.
func unverifiedDelegatedShape(raw string) bool {
	const maxTokenBytes = 16 << 10
	if len(raw) > maxTokenBytes {
		return true
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	return delegatedShape(claims)
}

// delegatedShape is the one shape rule logout uses, verified or not: a
// session_profile other than first_party, or any delegation marker, is
// delegated. With session_profile absent, any iss or aud is delegated too;
// with first_party present, iss and aud are judged by admission (they may be
// SessionsConfig.Issuer/Audience, current or since changed), never here.
func delegatedShape(claims map[string]any) bool {
	rawProfile, hasProfile := claims[tokenClaimProfile]
	if hasProfile && rawProfile != string(session.ProfileFirstParty) {
		return true
	}
	for _, name := range firstPartyDelegationMarkers {
		if _, present := claims[name]; present {
			return true
		}
	}
	if hasProfile {
		return false
	}
	_, hasIssuer := claims["iss"]
	_, hasAudience := claims["aud"]
	return hasIssuer || hasAudience
}
