package authentication

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// MaxCredentialBytes bounds a presented credential. Larger input is rejected
// before any verification work.
const MaxCredentialBytes = 16 << 10

// MaxAttributes bounds Identity.Attributes.
const MaxAttributes = 16

const (
	TransportHeader Transport = "header"
	TransportCookie Transport = "cookie"
)

const (
	// UnavailableNetwork: the provider could not be reached.
	UnavailableNetwork UnavailableKind = "network"
	// UnavailableUpstream: the provider answered with a server error.
	UnavailableUpstream UnavailableKind = "upstream"
	// UnavailableDeadline: the call ran out of time.
	UnavailableDeadline UnavailableKind = "deadline"
	// UnavailableRejectedCredential: the provider refused THIS process's own
	// credential (a rotated or revoked service key). It is an operator problem,
	// not a provider outage, and is named so on-call does not chase one.
	UnavailableRejectedCredential UnavailableKind = "rejected_credential"
)

var (
	// ErrInvalidCredential rejects a presented credential. It matches sdk.ErrUnauthorized.
	ErrInvalidCredential = fmt.Errorf("authentication: invalid credential: %w", sdk.ErrUnauthorized)

	// ErrIssuerMismatch rejects a credential from an issuer other than the
	// provider's. It matches ErrInvalidCredential.
	ErrIssuerMismatch = fmt.Errorf("authentication: issuer mismatch: %w", ErrInvalidCredential)

	// ErrSignInMethodDenied rejects a verified credential whose sign-in method
	// the provider's Profile refuses. It matches ErrInvalidCredential.
	ErrSignInMethodDenied = fmt.Errorf("authentication: sign-in method denied: %w", ErrInvalidCredential)

	// ErrProviderUnavailable reports that the provider could not answer; it says
	// nothing about the credential. It matches sdk.ErrUnavailable.
	ErrProviderUnavailable = fmt.Errorf("authentication: provider unavailable: %w", sdk.ErrUnavailable)
)

// Provider verifies one credential kind from one issuer.
type Provider interface {
	// Issuer is the exact iss this provider accepts; consumers route on it.
	Issuer() string

	// Authenticate returns a zero Identity with every error.
	Authenticate(ctx context.Context, cred Credential) (Identity, error)
}

// RevocationChecker is optional; a provider that cannot answer does not implement it.
type RevocationChecker interface {
	// CheckRevocation never reports an unknown subject as valid: it returns
	// Disabled facts or an error matching sdk.ErrNotFound.
	CheckRevocation(ctx context.Context, issuer, subject string) (RevocationFacts, error)
}

// Transport is how a credential reached the consumer.
type Transport string

// Credential is a presented proof. Raw is never logged; String and LogValue
// redact it.
type Credential struct {
	Transport Transport
	Raw       string
}

// String redacts Raw.
func (c Credential) String() string {
	return fmt.Sprintf("{Transport:%s Raw:[redacted]}", c.Transport)
}

// LogValue redacts Raw.
func (c Credential) LogValue() slog.Value {
	return slog.GroupValue(slog.String("transport", string(c.Transport)))
}

// Identity is a verified external identity: who the provider says presented the
// credential and how well. It carries NO sdk.Principal and NO local user id —
// mapping to the host's principal belongs to the identity owner. Email/Phone
// carry their own verified flags, which is why the capability does not reuse
// root IdentityAddress (a projection of an already trusted record).
type Identity struct {
	Issuer          string // the token's iss, verbatim
	Subject         string // the issuer's sub, verbatim
	Email           string
	EmailVerified   bool
	Phone           string
	PhoneVerified   bool
	DisplayName     string
	Tenant          string
	AuthenticatedAt time.Time // auth_time, else iat
	ExpiresAt       time.Time
	Methods         []string // amr verbatim
	MFA             bool
	Attributes      map[string]string // Profile Claims.Extra paths, string-valued, at most MaxAttributes
}

// Clone returns a copy that shares no slice or map with i. Consumers clone on
// every hand-off so a caller cannot mutate a stashed identity.
func (i Identity) Clone() Identity {
	i.Methods = slices.Clone(i.Methods)
	i.Attributes = maps.Clone(i.Attributes)
	return i
}

// RevocationFacts is what a provider knows about a subject's standing. The
// consumer compares ValidAfter to each token's AuthenticatedAt per request, so a
// cached answer never locks out a subject who just re-authenticated.
type RevocationFacts struct {
	Disabled   bool
	ValidAfter time.Time // zero: never revoked
	CheckedAt  time.Time
}

// UnavailableKind classifies a provider outage for logs.
type UnavailableKind string

// UnavailableError is a provider outage with its kind. It matches
// ErrProviderUnavailable and the wrapped cause.
type UnavailableError struct {
	Kind UnavailableKind
	Err  error
}

func (e *UnavailableError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%v (%s)", ErrProviderUnavailable, e.Kind)
	}
	return fmt.Sprintf("%v (%s): %v", ErrProviderUnavailable, e.Kind, e.Err)
}

func (e *UnavailableError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrProviderUnavailable}
	}
	return []error{ErrProviderUnavailable, e.Err}
}
