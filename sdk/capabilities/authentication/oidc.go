package authentication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// OIDC is the generic Provider for OIDC-shaped JWTs: a cryptids.JWTVerifier
// proves the token and a Profile maps its claims.
type OIDC struct {
	issuer     string
	verifier   cryptids.JWTVerifier
	claims     ClaimMapping
	trustPhone bool
	denySignIn []string
}

// NewOIDC returns a Provider for tokens whose iss is exactly issuer. The
// verifier must pin the same issuer and the expected audiences. It panics on
// an empty issuer, a nil verifier, or DenySignIn without a SignInMethod path,
// which would otherwise admit every sign-in method silently.
func NewOIDC(issuer string, verifier cryptids.JWTVerifier, profile Profile) *OIDC {
	if strings.TrimSpace(issuer) == "" {
		panic("authentication.NewOIDC: issuer is required")
	}
	if verifier == nil {
		panic("authentication.NewOIDC: verifier is required")
	}
	if len(profile.DenySignIn) > 0 && profile.Claims.SignInMethod == "" {
		panic("authentication.NewOIDC: DenySignIn requires Claims.SignInMethod")
	}
	return &OIDC{
		issuer:     issuer,
		verifier:   verifier,
		claims:     profile.Claims.withDefaults(),
		trustPhone: profile.TrustPhone,
		denySignIn: slices.Clone(profile.DenySignIn),
	}
}

// Issuer returns the configured issuer.
func (o *OIDC) Issuer() string { return o.issuer }

// Authenticate rejects oversize, malformed and foreign-issuer tokens before
// calling the verifier, then re-checks iss on the verified claims and maps them.
func (o *OIDC) Authenticate(ctx context.Context, cred Credential) (Identity, error) {
	if cred.Raw == "" {
		return Identity{}, fmt.Errorf("%w: empty", ErrInvalidCredential)
	}
	if len(cred.Raw) > MaxCredentialBytes {
		return Identity{}, fmt.Errorf("%w: exceeds %d bytes", ErrInvalidCredential, MaxCredentialBytes)
	}
	iss, err := peekIssuer(cred.Raw)
	if err != nil {
		return Identity{}, err
	}
	if iss != o.issuer {
		return Identity{}, ErrIssuerMismatch
	}

	claims, err := o.verifier.Verify(ctx, cred.Raw)
	if err != nil {
		return Identity{}, classifyVerifyError(err)
	}
	if verified, _ := claims["iss"].(string); verified != o.issuer {
		return Identity{}, ErrIssuerMismatch
	}
	return o.identity(claims)
}

func (o *OIDC) identity(claims map[string]any) (Identity, error) {
	m := o.claims
	id := Identity{
		Issuer:      o.issuer,
		Subject:     lookupString(claims, m.Subject),
		Email:       lookupString(claims, m.Email),
		Phone:       lookupString(claims, m.Phone),
		DisplayName: lookupString(claims, m.DisplayName),
		Tenant:      lookupString(claims, m.Tenant),
		Methods:     lookupStrings(claims, m.AMR),
	}
	if id.Subject == "" {
		return Identity{}, fmt.Errorf("%w: missing subject", ErrInvalidCredential)
	}
	exp, ok := lookupTime(claims, "exp")
	if !ok {
		return Identity{}, fmt.Errorf("%w: missing exp", ErrInvalidCredential)
	}
	id.ExpiresAt = exp
	if at, ok := lookupTime(claims, m.AuthTime); ok {
		id.AuthenticatedAt = at
		id.AuthTimeAsserted = true
	} else if at, ok := lookupTime(claims, "iat"); ok {
		id.AuthenticatedAt = at
	}

	id.EmailVerified = id.Email != "" && lookupBool(claims, m.EmailVerified)
	id.PhoneVerified = id.Phone != "" && (o.trustPhone || lookupBool(claims, m.PhoneVerified))
	id.MFA = (m.MFAFlag != "" && lookupFlag(claims, m.MFAFlag)) || m.amrMFA(id.Methods)

	if m.SignInMethod != "" && len(o.denySignIn) > 0 {
		if method := lookupString(claims, m.SignInMethod); slices.Contains(o.denySignIn, method) {
			return Identity{}, ErrSignInMethodDenied
		}
	}

	for _, path := range m.Extra {
		if len(id.Attributes) == MaxAttributes {
			break
		}
		if _, dup := id.Attributes[path]; dup {
			continue
		}
		v, _ := lookup(claims, path)
		if s, ok := v.(string); ok {
			if id.Attributes == nil {
				id.Attributes = make(map[string]string)
			}
			id.Attributes[path] = s
		}
	}
	return id, nil
}

// peekIssuer reads iss from an UNVERIFIED compact JWS. It is used only to
// reject; acceptance always follows the verifier.
func peekIssuer(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: malformed token", ErrInvalidCredential)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: malformed token", ErrInvalidCredential)
	}
	var body struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", fmt.Errorf("%w: malformed token", ErrInvalidCredential)
	}
	return body.Iss, nil
}

// classifyVerifyError keeps an outage from ever reading as an invalid token.
func classifyVerifyError(err error) error {
	var unavailable *UnavailableError
	switch {
	case errors.As(err, &unavailable):
		return err
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return &UnavailableError{Kind: UnavailableDeadline, Err: err}
	case errors.Is(err, sdk.ErrUnavailable):
		return &UnavailableError{Kind: UnavailableNetwork, Err: err}
	default:
		return fmt.Errorf("%w: %v", ErrInvalidCredential, err)
	}
}
