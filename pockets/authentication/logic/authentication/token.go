package authentication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
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
)

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
// parameter. A successful issuance records a token_issued success event.
func (s *Service) IssueToken(ctx context.Context, emailAddr, password string) (TokenPair, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return TokenPair{}, err
	}
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		return TokenPair{}, invalidCredentials()
	}

	u, _, err := s.provePassword(ctx, normalized, password)
	if err != nil {
		return TokenPair{}, err
	}

	pair, err := s.mintSession(ctx, u.ID, u.AuthRevision, s.primaryAuthentication(session.MethodPassword))
	if err != nil {
		// A deactivated account is denied as ordinary bad credentials (CHAU-1.5).
		return TokenPair{}, genericIfNotActive(err, invalidCredentials())
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: u.ID,
		Type:   securityevent.TypeTokenIssued,
		Status: securityevent.StatusSuccess,
	})
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
	return parseAccessClaims(claims)
}

func parseAccessClaims(claims map[string]any) (string, Credential, bool) {
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
	if cred.Profile == session.ProfileFirstParty {
		for _, name := range []string{"iss", "aud", "client_id", "origin_client_id", "act"} {
			if _, present := claims[name]; present {
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
	userID, cred, ok := parseAccessClaims(claims)
	if !ok || cred.Profile != session.ProfileFirstParty {
		return "", Credential{}, false, ErrDelegatedCredential
	}
	return userID, cred, true, nil
}

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
	if rawProfile, present := claims[tokenClaimProfile]; present && rawProfile != string(session.ProfileFirstParty) {
		return true
	}
	for _, name := range []string{"iss", "aud", "client_id", "origin_client_id", "act"} {
		if _, present := claims[name]; present {
			return true
		}
	}
	return false
}
